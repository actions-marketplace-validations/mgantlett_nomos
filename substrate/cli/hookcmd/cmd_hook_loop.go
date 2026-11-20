// Package hookcmd implements Tier 1 agent adapters, software physics telemetry,
// release readiness, upgrades, and CLI command dispatch contracts.
package hookcmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/mgantlett/nomos/substrate/cli/hookcmd/shellguard"
	"os"
	"path/filepath"
	"strings"
)

const (
	loopBreakerReason       = "Loop Circuit-Breaker (Law 1): Identical tool call repeated 3 consecutive times without workspace state change. Alter parameters or inspect workspace state before retrying."
	maxDigestFileBytes      = 262144
	maxTrackedRunnerDigests = 16
)

type loopBreakerState struct {
	Fingerprint       string         `json:"fingerprint"`
	WorkspaceDigest   string         `json:"workspace_digest"`
	ConsecutiveCount  int            `json:"consecutive_count"`
	RunnerFamily      string         `json:"runner_family,omitempty"`
	NoEditRunnerCount int            `json:"no_edit_runner_count,omitempty"`
	TestedDigests     map[string]int `json:"tested_digests,omitempty"`
}

// EvaluateLoopCircuitBreaker enforces the identical-failure and no-edit runner circuit-breaker
// when mutating or shell tool calls repeat without workspace mtime progress.
func EvaluateLoopCircuitBreaker(p *shellguard.HookPayload, hermeticDir string) shellguard.HookDecision {
	return EvaluateLoopCircuitBreakerWithStrikes(p, hermeticDir, resolveHookAdvisoryProfile(0, 0, 0, "").LoopStrikes)
}

// EvaluateLoopCircuitBreakerWithStrikes enforces a bounded N-strike identical-failure
// and flag-permuted no-edit verification runner circuit-breaker.
func EvaluateLoopCircuitBreakerWithStrikes(p *shellguard.HookPayload, hermeticDir string, strikes int) shellguard.HookDecision {
	if p == nil || !isLoopTrackedInvocation(p) {
		return shellguard.HookDecision{Decision: "allow"}
	}
	wsDir := resolveLoopTrackingDir(p, hermeticDir)
	fp := computeInvocationFingerprint(p)
	wsDigest := computeWorkspaceMtimeDigest(wsDir)
	runner := detectVerificationRunnerFamily(p.CommandLine)
	statePath := resolveLoopStateFilePath(wsDir)

	prev := loadLoopBreakerState(statePath)
	next := advanceLoopBreakerState(prev, fp, wsDigest, runner)
	saveLoopBreakerState(statePath, next)

	threshold := maxLoopStrikesWithThreshold(p, strikes)
	if next.ConsecutiveCount >= threshold {
		return shellguard.HookDecision{
			Decision: "deny",
			Reason:   formatLoopBreakerReason(threshold),
		}
	}
	if runner != "" && (next.NoEditRunnerCount >= threshold || next.TestedDigests[wsDigest] >= threshold) {
		return shellguard.HookDecision{
			Decision: "deny",
			Reason:   formatNoEditRunnerBreakerReason(runner, threshold),
		}
	}
	return shellguard.HookDecision{Decision: "allow"}
}

func copyTestedDigests(prev map[string]int) map[string]int {
	out := make(map[string]int, len(prev)+1)
	if len(prev) >= maxTrackedRunnerDigests {
		return out
	}
	for k, v := range prev {
		out[k] = v
	}
	return out
}

func advanceLoopBreakerState(prev loopBreakerState, fp, wsDigest, runner string) loopBreakerState {
	next := loopBreakerState{
		Fingerprint:      fp,
		WorkspaceDigest:  wsDigest,
		ConsecutiveCount: 1,
		RunnerFamily:     runner,
		TestedDigests:    copyTestedDigests(prev.TestedDigests),
	}
	if runner != "" {
		next.NoEditRunnerCount = 1
		next.TestedDigests[wsDigest]++
	}
	if prev.WorkspaceDigest != wsDigest {
		return next
	}
	if prev.Fingerprint == fp {
		next.ConsecutiveCount = prev.ConsecutiveCount + 1
	}
	if runner != "" && prev.RunnerFamily == runner {
		next.NoEditRunnerCount = prev.NoEditRunnerCount + 1
	}
	return next
}

var verificationRunnerRules = []struct {
	family string
	match  func(exe string, args []string) bool
}{
	{family: "pytest", match: func(exe string, args []string) bool {
		return exe == "pytest" || exe == "py.test" || hasPythonModuleArg(exe, args, "pytest")
	}},
	{family: "unittest", match: func(exe string, args []string) bool {
		return hasPythonModuleArg(exe, args, "unittest")
	}},
	{family: "go-test", match: func(exe string, args []string) bool {
		return exe == "go" && len(args) > 0 && args[0] == "test"
	}},
	{family: "cargo-test", match: func(exe string, args []string) bool {
		return exe == "cargo" && len(args) > 0 && args[0] == "test"
	}},
	{family: "js-test", match: isJSVerificationSegment},
	{family: "ctest", match: func(exe string, args []string) bool {
		return exe == "ctest"
	}},
}

func detectVerificationRunnerFamily(cmdLine string) string {
	trimmed := strings.TrimSpace(cmdLine)
	if trimmed == "" {
		return ""
	}
	for _, seg := range shellguard.SplitUnquotedShellSegments(trimmed) {
		exe, _, rawArgs := shellguard.ExtractLeadingExeAndArgs(seg)
		if exe == "" {
			continue
		}
		args := lowerSlice(rawArgs)
		for _, rule := range verificationRunnerRules {
			if rule.match(exe, args) {
				return rule.family
			}
		}
		if isPythonTestScriptSegment(exe, args) {
			return "python-test"
		}
	}
	return ""
}

func isJSVerificationSegment(exe string, args []string) bool {
	if exe == "vitest" || exe == "jest" {
		return true
	}
	if exe == "npm" || exe == "pnpm" || exe == "yarn" || exe == "bun" {
		return len(args) > 0 && args[0] == "test"
	}
	return false
}

func hasPythonModuleArg(exe string, args []string, mod string) bool {
	if !shellguard.IsPythonExe(exe) {
		return false
	}
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-m" && args[i+1] == mod {
			return true
		}
	}
	return false
}

func isPythonTestScriptSegment(exe string, args []string) bool {
	if !shellguard.IsPythonExe(exe) {
		return false
	}
	for i, arg := range args {
		if arg == "-c" || arg == "-" {
			return false
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if i > 0 && args[i-1] == "-m" {
			return strings.Contains(arg, "test")
		}
		return strings.Contains(arg, "tests/") || strings.HasPrefix(filepath.Base(arg), "test_") || strings.HasSuffix(arg, "_test.py")
	}
	return false
}

func lowerSlice(vals []string) []string {
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = strings.ToLower(v)
	}
	return out
}

func formatLoopBreakerReason(threshold int) string {
	if threshold == defaultLoopStrikes {
		return loopBreakerReason
	}
	return fmt.Sprintf(
		"Loop Circuit-Breaker (Law 1): Identical tool call repeated %d consecutive times without workspace state change. Alter parameters or inspect workspace state before retrying.",
		threshold,
	)
}

func formatNoEditRunnerBreakerReason(runner string, threshold int) string {
	return fmt.Sprintf(
		"Loop Circuit-Breaker (Law 1): Verification runner '%s' executed %d consecutive times without any workspace file modification. Edit source or inspect failure root cause before re-running.",
		runner, threshold,
	)
}

func isLoopTrackedInvocation(p *shellguard.HookPayload) bool {
	if strings.TrimSpace(p.CommandLine) != "" {
		return true
	}
	if strings.TrimSpace(p.TargetFile) != "" {
		return true
	}
	return strings.TrimSpace(p.ArgsDigest) != ""
}

func maxLoopStrikesWithThreshold(p *shellguard.HookPayload, strikes int) int {
	cmd := strings.ToLower(strings.TrimSpace(p.CommandLine))
	if cmd != "" {
		for _, tok := range []string{"status", "poll", "wait", "ps ", "jobs"} {
			if strings.Contains(cmd, tok) {
				return 12
			}
		}
	}
	if strikes <= 0 {
		return defaultLoopStrikes
	}
	return clampAdvisoryInt(strikes, minLoopStrikes, maxLoopStrikesBound)
}

func computeInvocationFingerprint(p *shellguard.HookPayload) string {
	raw := strings.ToLower(strings.TrimSpace(p.ToolName)) + "|" +
		strings.TrimSpace(p.TargetFile) + "|" +
		strings.TrimSpace(p.CommandLine) + "|" +
		strings.TrimSpace(p.ArgsDigest)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:16])
}

func resolveLoopStateFilePath(wsDir string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(wsDir)))
	name := fmt.Sprintf("nomos-hook-loop-%s.json", hex.EncodeToString(sum[:8]))
	hookDir := resolveHookStateCacheDir()
	if hookDir == "" {
		return ""
	}
	return filepath.Join(hookDir, name)
}

func loadLoopBreakerState(path string) loopBreakerState {
	var st loopBreakerState
	if !readRegularHookJSON(path, &st) {
		return loopBreakerState{}
	}
	return st
}

func saveLoopBreakerState(path string, st loopBreakerState) {
	if path == "" {
		return
	}
	if fi, err := os.Lstat(path); err == nil && !fi.Mode().IsRegular() {
		return
	}
	data, err := json.Marshal(st)
	if err != nil {
		return
	}
	if wErr := os.WriteFile(path, data, 0600); wErr != nil {
		return
	}
}
