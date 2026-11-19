// Package hookcmd implements Tier 1 agent adapters, software physics telemetry,
// release readiness, upgrades, and CLI command dispatch contracts.
package hookcmd

import (
	"fmt"
	"github.com/mgantlett/nomos/substrate/cli/hookcmd/containerguard"
	"github.com/mgantlett/nomos/substrate/cli/hookcmd/shellguard"
	"path/filepath"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/capbac"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/config"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/task"
)

// EvaluatePreToolHook enforces Law 3 boundary isolation, Law 7 command safety,
// and Law 1 identical-failure loop circuit-breaking on PreToolUse events.
func EvaluatePreToolHook(p *shellguard.HookPayload, hermeticDir string) shellguard.HookDecision {
	return EvaluatePreToolHookWithProfile(p, hermeticDir, resolveHookAdvisoryProfile(0, 0, 0, ""))
}

// EvaluatePreToolHookWithProfile evaluates hard PreToolUse safety invariants alongside
// the bounded loop circuit-breaker threshold from profile.
func EvaluatePreToolHookWithProfile(p *shellguard.HookPayload, hermeticDir string, profile HookAdvisoryProfile) shellguard.HookDecision {
	if p == nil {
		return shellguard.HookDecision{Decision: hookDecisionAllow}
	}
	if p.CommandLine != "" {
		cmdCwd := firstNonEmpty(p.Cwd, resolveHookWorkspaceDir(p, hermeticDir))
		if dec := evaluateCommandGuard(p.CommandLine, cmdCwd, hermeticDir); dec.Decision == hookDecisionDeny {
			return dec
		}
	}
	wsRoot := resolveCanonicalHookWorkspace(p, hermeticDir)
	if dec := containerguard.EvaluateOutboundEgressGuard(p, wsRoot); dec.Decision == hookDecisionDeny {
		return dec
	}
	if dec := containerguard.EvaluateCredentialReadGuard(p, wsRoot); dec.Decision == hookDecisionDeny {
		return dec
	}
	if dec := evaluateMCPPreToolGuard(p, hermeticDir); dec.Decision == hookDecisionDeny {
		return dec
	}
	if dec := evaluateTargetFilePreToolGuard(p, hermeticDir); dec.Decision == hookDecisionDeny {
		return dec
	}
	if dec := evaluateCapabilityFlowGuard(p, hermeticDir); dec.Decision == hookDecisionDeny {
		return dec
	}
	return EvaluateLoopCircuitBreakerWithStrikes(p, hermeticDir, profile.LoopStrikes)
}

func evaluateTargetFilePreToolGuard(p *shellguard.HookPayload, hermeticDir string) shellguard.HookDecision {
	targets := p.TargetFiles
	if len(targets) == 0 && p.TargetFile != "" {
		targets = []string{p.TargetFile}
	}
	if len(targets) == 0 {
		return shellguard.HookDecision{Decision: hookDecisionAllow}
	}
	for _, target := range targets {
		cp := *p
		cp.TargetFile = target
		if cp.CommandLine == "" && hasUnexpandedShellPathToken(target) {
			return shellguard.HookDecision{
				Decision: hookDecisionDeny,
				Reason:   fmt.Sprintf("Hermetic Path Violation (Law 3): Unexpanded shell variable or tilde in target path '%s'. Native file tools do not expand shell syntax; pass a concrete relative or resolved path.", target),
			}
		}
		var dec shellguard.HookDecision
		if !shellguard.IsReadOnlyTool(cp.ToolName) {
			dec = evaluateFileMutationGuard(&cp, hermeticDir)
		} else {
			dec = evaluateReadToolBoundaryAndBinary(&cp, hermeticDir)
		}
		if dec.Decision == hookDecisionDeny {
			return dec
		}
	}
	return shellguard.HookDecision{Decision: hookDecisionAllow}
}

func evaluateMCPPreToolGuard(p *shellguard.HookPayload, hermeticDir string) shellguard.HookDecision {
	mcpInv, ok := capbac.ParseMCPInvocation(p.ToolName, p.TargetFile, p.ArgsDigest)
	if !ok {
		return shellguard.HookDecision{Decision: hookDecisionAllow}
	}
	wsRoot := resolveCanonicalHookWorkspace(p, hermeticDir)
	policy := loadWorkspaceMCPBoundaryPolicy(wsRoot)
	dec := capbac.EvaluateMCPBoundary(wsRoot, mcpInv, policy)
	if !dec.Allowed {
		return shellguard.HookDecision{
			Decision: hookDecisionDeny,
			Reason:   dec.Reason,
		}
	}
	return shellguard.HookDecision{Decision: hookDecisionAllow}
}

func loadWorkspaceMCPBoundaryPolicy(wsRoot string) capbac.MCPBoundaryPolicy {
	cfg, err := config.LoadConfig(wsRoot)
	if err != nil || cfg == nil {
		return capbac.MCPBoundaryPolicy{}
	}
	return capbac.MCPBoundaryPolicy{
		AllowedServers: cfg.Governance.Boundary.AllowedMCPServers,
		BlockedServers: cfg.Governance.Boundary.BlockedMCPServers,
	}
}

func evaluateCapabilityFlowGuard(p *shellguard.HookPayload, hermeticDir string) shellguard.HookDecision {
	wsRoot := resolveCanonicalHookWorkspace(p, hermeticDir)
	flowDec := capbac.EvaluateWorkspaceToolFlow(wsRoot, capbac.ToolFlowInvocation{
		ToolName:    p.ToolName,
		TargetFile:  p.TargetFile,
		CommandLine: p.CommandLine,
		ArgsDigest:  p.ArgsDigest,
	})
	if !flowDec.Allowed {
		return shellguard.HookDecision{
			Decision: hookDecisionDeny,
			Reason:   flowDec.Reason,
		}
	}
	return shellguard.HookDecision{Decision: hookDecisionAllow}
}

func resolveCanonicalHookWorkspace(p *shellguard.HookPayload, hermeticDir string) string {
	if hermeticDir != "" {
		return filepath.Clean(hermeticDir)
	}
	base := resolveHookWorkspaceDir(p, "")
	if pr := findRepoRootFast(base); pr != "" {
		return filepath.Clean(pr)
	}
	return filepath.Clean(base)
}

func hasUnexpandedShellPathToken(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "~") {
		return true
	}
	return strings.Contains(trimmed, "$")
}

func evaluateFileMutationGuard(p *shellguard.HookPayload, hermeticDir string) shellguard.HookDecision {
	if hasUnexpandedShellPathToken(p.TargetFile) {
		return shellguard.HookDecision{
			Decision: hookDecisionDeny,
			Reason:   fmt.Sprintf("Hermetic Path Violation (Law 3): Unexpanded shell variable or tilde in target path '%s'. Pass a canonical resolved path.", p.TargetFile),
		}
	}
	baseCwd := resolveHookWorkspaceDir(p, hermeticDir)
	resolved := shellguard.NormalizeCanonicalPath(p.TargetFile, baseCwd)
	if isSystemRootPath(resolved) {
		return shellguard.HookDecision{
			Decision: hookDecisionDeny,
			Reason:   fmt.Sprintf("Hermetic Boundary Violation (Law 3): System path mutation '%s' is strictly prohibited.", resolved),
		}
	}
	if hermeticDir != "" {
		return evaluateHermeticDirBoundary(p.TargetFile, resolved, hermeticDir)
	}
	return evaluateWorktreeBoundary(p.TargetFile, resolved, baseCwd, p.ToolName)
}

func evaluateHermeticDirBoundary(rawTarget, resolved, hermeticDir string) shellguard.HookDecision {
	canonRoot := shellguard.NormalizeCanonicalPath(hermeticDir, "/")
	if resolved == canonRoot || strings.HasPrefix(resolved, canonRoot+"/") {
		return shellguard.HookDecision{Decision: hookDecisionAllow}
	}
	return shellguard.HookDecision{
		Decision: hookDecisionDeny,
		Reason:   fmt.Sprintf("Hermetic Container Violation (Law 3): Target '%s' (resolved: '%s') escapes hermetic root '%s'.", rawTarget, resolved, canonRoot),
	}
}

func evaluateWorktreeBoundary(rawTarget, resolved, baseCwd, toolName string) shellguard.HookDecision {
	if isPermittedWorktreePath(resolved, baseCwd) {
		return shellguard.HookDecision{Decision: hookDecisionAllow}
	}
	if repoRoot, cfg, branch, isBranch := ResolveBranchGuardContext(resolved); isBranch {
		return evaluateBranchGuardMutation(rawTarget, resolved, repoRoot, branch, cfg)
	}
	if repoRoot, cfg, branch, isBranch := ResolveBranchGuardContext(baseCwd); isBranch && strings.HasPrefix(resolved, repoRoot+"/") {
		return evaluateBranchGuardMutation(rawTarget, resolved, repoRoot, branch, cfg)
	}
	if toolName == "" {
		toolName = "file_mutation"
	}
	choreHint := `nomos task chore "<title>"`
	if strings.Contains(resolved, "/intent/") || strings.HasSuffix(resolved, "/nomos.yaml") {
		choreHint = `nomos task chore "<title>" --plane intent`
	}
	return shellguard.HookDecision{
		Decision: hookDecisionDeny,
		Reason: fmt.Sprintf(
			"Hermetic Worktree Violation (Law 3): Direct trunk mutation of '%s' (resolved: '%s') via '%s' is prohibited. Provision an isolated EDIT worktree in 1 turn via '%s' (or 'nomos task start <KEY> --edit'), then edit strictly inside '/worktrees/'.",
			rawTarget, resolved, toolName, choreHint,
		),
	}
}

func isPermittedWorktreePath(resolved, baseCwd string) bool {
	if strings.Contains(resolved, "/worktrees/") || strings.Contains(resolved, "/.tmp/") {
		return true
	}
	return isExternalAgentBrainPath(resolved, baseCwd)
}

func isExternalAgentBrainPath(resolved, baseCwd string) bool {
	if !strings.Contains(resolved, "/brain/") {
		return false
	}
	if isInsideWorkspaceRoot(resolved, baseCwd) {
		return false
	}
	return hasAgentBrainParentSegment(resolved)
}

func isInsideWorkspaceRoot(resolved, baseCwd string) bool {
	if baseCwd == "" {
		return false
	}
	canonBase := shellguard.NormalizeCanonicalPath(baseCwd, "/")
	if pr, err := workspace.FindRepoRoot(canonBase); err == nil && pr != "" {
		canonBase = filepath.ToSlash(filepath.Clean(pr))
	}
	return resolved == canonBase || strings.HasPrefix(resolved, canonBase+"/")
}

func hasAgentBrainParentSegment(resolved string) bool {
	segs := strings.Split(resolved, "/")
	for i := 1; i < len(segs)-1; i++ {
		if segs[i] != "brain" {
			continue
		}
		parent := segs[i-1]
		if parent == "antigravity" || parent == "antigravity-ide" || parent == "antigravity-cli" {
			return true
		}
		if len(parent) > 1 && strings.HasPrefix(parent, ".") {
			return true
		}
	}
	return false
}

func isSystemRootPath(resolved string) bool {
	for _, sys := range systemRootPrefixes {
		if resolved == sys || strings.HasPrefix(resolved, sys+"/") {
			return true
		}
	}
	return resolved == "/"
}

// ExtractTaskKeyFromDirName extracts a canonical INT-N or SUB-N task key suffix from a directory basename.
// @intent task_lifecycle_cli_spec.md
func ExtractTaskKeyFromDirName(base string) string {
	return extractTaskKeyFromDirName(base)
}

func extractTaskKeyFromDirName(base string) string {
	parts := strings.Split(base, "-")
	if len(parts) < 2 {
		return ""
	}
	candKey := strings.ToUpper(parts[len(parts)-2] + "-" + parts[len(parts)-1])
	if err := task.ValidateTaskKey(candKey); err == nil {
		return candKey
	}
	candKeySingle := strings.ToUpper(parts[len(parts)-1])
	if err := task.ValidateTaskKey(candKeySingle); err == nil {
		return candKeySingle
	}
	return ""
}
