package containerguard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mgantlett/nomos/substrate/cli/hookcmd/shellguard"
)

const (
	// EnvContainerPolicyFile overrides the path to the tamper-protected container policy JSON file.
	EnvContainerPolicyFile     = "NOMOS_CONTAINER_POLICY_FILE"
	defaultHomePolicyPath      = "/root/.nomos-bin/policy.json"
	defaultContainerPolicyPath = "/etc/nomos/policy.json"
)

// SandboxPolicy declares external, tamper-protected capability limits enforced in --container-sandbox mode.
type SandboxPolicy struct {
	ReadOnly                         bool     `json:"read_only"`
	ProtectedPaths                   []string `json:"protected_paths"`
	AllowedCommandPrefixes           []string `json:"allowed_command_prefixes"`
	DeniedCommandPrefixes            []string `json:"denied_command_prefixes"`
	AllowedMCPTools                  []string `json:"allowed_mcp_tools"`
	DeniedMCPTools                   []string `json:"denied_mcp_tools"`
	AllowedMCPServers                []string `json:"allowed_mcp_servers"`
	DeniedMCPServers                 []string `json:"denied_mcp_servers"`
	EgressMode                       string   `json:"egress_mode"`
	AllowedEgressDomains             []string `json:"allowed_egress_domains"`
	BlockedEgressDomains             []string `json:"blocked_egress_domains"`
	IgnoreNpmScripts                 bool     `json:"ignore_npm_scripts"`
	UntrustedProject                 bool     `json:"untrusted_project"`
	DisableAutonomousSideEffectGuard bool     `json:"disable_autonomous_side_effect_guard"`
}

var (
	readOnlyShellWriteVerbRe = regexp.MustCompile(`(?i)\b(?:rm|mv|cp|tee|truncate|chmod|chown|chgrp|touch|mkdir|rmdir|ln|dd|install|patch|mktemp)\b`)
	readOnlyScriptWriteRe    = regexp.MustCompile(`(?i)(?:\bwrite_text\b|\bwrite_bytes\b|\bwriteFile|\bFile\.write\b|os\.WriteFile|open\([^)]*['"][wa])`)
	readOnlySQLMutateRe      = regexp.MustCompile(`(?i)\b(?:INSERT|UPDATE|DELETE|DROP|ALTER|CREATE|REPLACE|TRUNCATE|ATTACH)\b`)
	destructiveSQLTableRe    = regexp.MustCompile(`(?i)\b(?:DROP|TRUNCATE)\s+TABLE\b`)
	shellRedirectWriteRe     = regexp.MustCompile(`(?:>>|(?:\d*)>\s*[^\s&]|>\s*/dev/null)`)

	safeShellBuiltins = map[string]bool{
		"cd": true, "pwd": true, "true": true, "false": true, "test": true, "[": true,
	}
	readOnlyBlockedSubcmds = map[string]map[string]bool{
		"go":  {"run": true, "generate": true, "build": true, "install": true},
		"git": {"apply": true, "checkout": true, "reset": true, "clean": true, "commit": true, "stash": true},
	}
)

// LoadSandboxPolicy loads the active container SandboxPolicy from environment or default container paths.
func LoadSandboxPolicy() SandboxPolicy {
	candidates := []string{
		strings.TrimSpace(os.Getenv(EnvContainerPolicyFile)),
		defaultHomePolicyPath,
		defaultContainerPolicyPath,
	}
	for _, p := range candidates {
		if p == "" {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var pol SandboxPolicy
		if uErr := json.Unmarshal(data, &pol); uErr == nil {
			return pol
		}
	}
	return SandboxPolicy{}
}

// EvaluatePolicyGuards enforces read-only, protected-path, and command allow/deny policies.
func EvaluatePolicyGuards(p *shellguard.HookPayload, baseCwd string, pol SandboxPolicy) shellguard.HookDecision {
	if dec := evaluateContainerReadOnlyGuard(p, pol); dec.Decision == shellguard.DecisionDeny {
		return dec
	}
	if dec := evaluateContainerProtectedPathsGuard(p, baseCwd, pol); dec.Decision == shellguard.DecisionDeny {
		return dec
	}
	return evaluateContainerCommandListGuard(p, baseCwd, pol)
}

func evaluateContainerReadOnlyGuard(p *shellguard.HookPayload, pol SandboxPolicy) shellguard.HookDecision {
	if !pol.ReadOnly || p == nil {
		return shellguard.AllowDecision()
	}
	if p.CommandLine == "" {
		if !shellguard.IsReadOnlyTool(p.ToolName) {
			return shellguard.DenyDecision(fmt.Sprintf("❌ [NOMOS INTERCEPT] Read-Only Policy Violation (Law 7): File mutation tool '%s' is prohibited in read-only mode.", p.ToolName))
		}
		return shellguard.AllowDecision()
	}
	if isReadOnlyViolatingCommand(p.CommandLine) {
		return shellguard.DenyDecision("❌ [NOMOS INTERCEPT] Read-Only Policy Violation (Law 7): Mutating command or filesystem write is prohibited in read-only mode.")
	}
	return shellguard.AllowDecision()
}

func isReadOnlyViolatingCommand(cmdLine string) bool {
	if hasWriteRedirectOutsideDevNull(cmdLine) || readOnlyScriptWriteRe.MatchString(cmdLine) || readOnlySQLMutateRe.MatchString(cmdLine) {
		return true
	}
	for _, inv := range shellguard.ExtractAllContainerCommandInvocations(cmdLine) {
		if isMutatingContainerInvocation(inv) {
			return true
		}
	}
	return false
}

func isMutatingContainerInvocation(inv shellguard.ShellCommandInvocation) bool {
	if readOnlyShellWriteVerbRe.MatchString(inv.Exe) {
		return true
	}
	if subMap := readOnlyBlockedSubcmds[inv.Exe]; subMap != nil && len(inv.Args) > 0 && subMap[strings.ToLower(inv.Args[0])] {
		return true
	}
	if (inv.Exe == "sed" || inv.Exe == "perl") && hasArgPrefix(inv.Args, "-i") {
		return true
	}
	return inv.Exe == "ruby" && len(inv.Args) > 0 && strings.Contains(inv.Args[0], "bin/")
}

func hasArgPrefix(args []string, prefix string) bool {
	for _, a := range args {
		if strings.HasPrefix(strings.ToLower(a), prefix) {
			return true
		}
	}
	return false
}

func hasWriteRedirectOutsideDevNull(cmdLine string) bool {
	if strings.Contains(cmdLine, ">>") {
		return true
	}
	scrubbed := strings.ReplaceAll(cmdLine, ">/dev/null", "")
	scrubbed = strings.ReplaceAll(scrubbed, "> /dev/null", "")
	scrubbed = strings.ReplaceAll(scrubbed, "2>&1", "")
	scrubbed = strings.ReplaceAll(scrubbed, ">&2", "")
	return shellRedirectWriteRe.MatchString(scrubbed)
}

func evaluateContainerCommandListGuard(p *shellguard.HookPayload, baseCwd string, pol SandboxPolicy) shellguard.HookDecision {
	if p == nil || p.CommandLine == "" {
		return shellguard.AllowDecision()
	}
	if len(pol.AllowedCommandPrefixes) == 0 && len(pol.DeniedCommandPrefixes) == 0 {
		return shellguard.AllowDecision()
	}
	invs := shellguard.ExtractAllContainerCommandInvocations(p.CommandLine)
	if len(pol.AllowedCommandPrefixes) > 0 {
		if reason := checkAllowlistedInvocations(invs, baseCwd, pol.AllowedCommandPrefixes); reason != "" {
			return shellguard.DenyDecision(reason)
		}
	}
	if len(pol.DeniedCommandPrefixes) > 0 {
		if reason := checkDenylistedInvocations(p.CommandLine, invs, pol.DeniedCommandPrefixes); reason != "" {
			return shellguard.DenyDecision(reason)
		}
	}
	return shellguard.AllowDecision()
}

func checkAllowlistedInvocations(invs []shellguard.ShellCommandInvocation, baseCwd string, allowed []string) string {
	for _, inv := range invs {
		if safeShellBuiltins[inv.Exe] {
			continue
		}
		if shellguard.HasInterpreterSubprocessEscape(inv) && !rawMatchesAllowedSubprocess(inv.Raw, allowed) {
			return fmt.Sprintf("❌ [NOMOS INTERCEPT] Command Allowlist Violation (Law 7): Subprocess escape inside '%s' is not permitted.", inv.Exe)
		}
		fullCmd := normalizedInvocationText(inv)
		realExe := filepath.Base(ResolveRealPathDeep(inv.Exe, baseCwd))
		realCmd := strings.TrimSpace(realExe + " " + strings.Join(inv.Args, " "))
		if !matchesCommandPrefixList(fullCmd, allowed) && !matchesCommandPrefixList(realCmd, allowed) {
			return fmt.Sprintf("❌ [NOMOS INTERCEPT] Command Allowlist Violation (Law 7): Command '%s' is outside allowed_command_prefixes.", inv.Exe)
		}
	}
	return ""
}

func rawMatchesAllowedSubprocess(raw string, allowed []string) bool {
	lower := strings.ToLower(raw)
	for _, pfx := range allowed {
		if strings.Contains(lower, strings.ToLower(strings.TrimSpace(pfx))) {
			return true
		}
	}
	return false
}

func checkDenylistedInvocations(rawCmd string, invs []shellguard.ShellCommandInvocation, denied []string) string {
	if hasDenylistedDropRule(denied) && destructiveSQLTableRe.MatchString(rawCmd) {
		return "❌ [NOMOS INTERCEPT] Command Denylist Violation (Law 7): Destructive DROP/TRUNCATE TABLE SQL equivalent is prohibited by denylist policy."
	}
	for _, inv := range invs {
		if matchesCommandPrefixList(normalizedInvocationText(inv), denied) {
			return fmt.Sprintf("❌ [NOMOS INTERCEPT] Command Denylist Violation (Law 7): Denied command '%s' blocked across shell unwrapping.", inv.Exe)
		}
		if shellguard.HasInterpreterSubprocessEscape(inv) {
			if hit := findContainedPrefix(inv.Raw, denied); hit != "" {
				return fmt.Sprintf("❌ [NOMOS INTERCEPT] Command Denylist Violation (Law 7): Subprocess invocation of denied command '%s' is prohibited.", hit)
			}
		}
	}
	return ""
}

func hasDenylistedDropRule(denied []string) bool {
	for _, d := range denied {
		if strings.Contains(strings.ToLower(d), "drop") {
			return true
		}
	}
	return false
}

func findContainedPrefix(raw string, list []string) string {
	lower := strings.ToLower(raw)
	for _, item := range list {
		trimmed := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(item), "*")))
		if trimmed != "" && strings.Contains(lower, trimmed) {
			return item
		}
	}
	return ""
}

func normalizedInvocationText(inv shellguard.ShellCommandInvocation) string {
	if len(inv.Args) == 0 {
		return inv.Exe
	}
	return inv.Exe + " " + strings.Join(inv.Args, " ")
}

func matchesCommandPrefixList(cmdStr string, prefixes []string) bool {
	normCmd := strings.ToLower(strings.Join(strings.Fields(cmdStr), " "))
	for _, rawPfx := range prefixes {
		pfx := strings.ToLower(strings.Join(strings.Fields(strings.TrimSuffix(strings.TrimSpace(rawPfx), "*")), " "))
		if pfx != "" && (normCmd == pfx || strings.HasPrefix(normCmd, pfx+" ")) {
			return true
		}
	}
	return false
}
