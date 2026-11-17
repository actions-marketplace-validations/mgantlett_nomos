// Package hookcmd implements per-worktree hook execution trace persistence and
// deterministic Receipt-Trace Parity validation during atomic 2PC task sync.
package hookcmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/mgantlett/nomos/substrate/cli/tui"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/capbac"
)

// HookSessionTrace summarizes verified tool and shell activity inside an active task worktree.
type HookSessionTrace struct {
	WorktreeRoot         string   `json:"worktree_root"`
	ToolCallsCount       int      `json:"tool_calls_count"`
	ShellCommandsCount   int      `json:"shell_commands_count"`
	TestCommandsCount    int      `json:"test_commands_count"`
	DeniedInterceptions  int      `json:"denied_interceptions"`
	UnverifiedCalls      int      `json:"unverified_calls,omitempty"`
	ConsecutiveReadCalls int      `json:"consecutive_read_calls,omitempty"`
	DirtyUnverifiedEdits bool     `json:"dirty_unverified_edits,omitempty"`
	WarnedChurn75        bool     `json:"warned_churn_75,omitempty"`
	WarnedChurn150       bool     `json:"warned_churn_150,omitempty"`
	WarnedReadSpiral12   bool     `json:"warned_read_spiral_12,omitempty"`
	WarnedReadSpiral20   bool     `json:"warned_read_spiral_20,omitempty"`
	MutatedFiles         []string          `json:"mutated_files,omitempty"`
	RecalledPitfallDirs  []string          `json:"recalled_pitfall_dirs,omitempty"`
	ImpactedFiles        []string          `json:"impacted_files,omitempty"`
	ObservedDigests      map[string]string `json:"observed_digests,omitempty"`
	PendingShellRadar    []string          `json:"pending_shell_radar,omitempty"`
	StopBlockStrikes     int               `json:"stop_block_strikes,omitempty"`
}

var standaloneVerifyCmdRe = regexp.MustCompile(`(?i)^\s*(?:cd\s+\S+\s*&&\s*)?(?:\S+/)?nomos\s+(?:verify|task\s+review|task\s+sync)(?:\s|$)`)

// recordPreToolSessionTrace records allowed or denied PreToolUse activity scoped to an active worktree or branch.
func recordPreToolSessionTrace(p *HookPayload, hermeticDir string, denied bool) {
	if p == nil || p.IsPostInvoke {
		return
	}
	traceKey, wsRoot, branchLabel := resolveTraceScopeAndWorkspace(p, hermeticDir)
	if traceKey == "" && branchLabel == "" {
		return
	}
	path := resolveTraceStateFilePath(traceKey)
	if path == "" {
		return
	}
	tr := loadOrInitHookSessionTrace(path, wsRoot)
	if denied {
		tr.DeniedInterceptions++
		saveHookSessionTrace(path, tr)
		return
	}
	tr.ToolCallsCount++
	if isNomosVerificationCommand(p.CommandLine) {
		resetWorktreeChurnCounters(&tr)
	}
	recordTracePayloadDetails(&tr, p, wsRoot)
	saveHookSessionTrace(path, tr)
}

// evaluateWorktreeChurnAdvisory tracks unverified PostToolUse calls inside an active task worktree or feature branch
// and emits non-blocking advisories at 75 and 150 calls since the last 'nomos verify' run.
func evaluateWorktreeChurnAdvisory(p *HookPayload, hermeticDir, style string) string {
	if p == nil || p.IsPostInvoke {
		return ""
	}
	traceKey, wsRoot, branchLabel := resolveTraceScopeAndWorkspace(p, hermeticDir)
	if traceKey == "" && branchLabel == "" {
		return ""
	}
	path := resolveTraceStateFilePath(traceKey)
	if path == "" {
		return ""
	}
	tr := loadOrInitHookSessionTrace(path, wsRoot)
	if isNomosVerificationCommand(p.CommandLine) {
		if hasFailedVerificationResponse(p.ToolResponseText) {
			tr.DirtyUnverifiedEdits = true
		} else {
			resetWorktreeChurnCounters(&tr)
			refreshTraceObservedDigests(wsRoot, &tr)
		}
		saveHookSessionTrace(path, tr)
		return ""
	}
	tr.UnverifiedCalls++
	var msg string
	if tr.UnverifiedCalls >= 150 && !tr.WarnedChurn150 {
		tr.WarnedChurn75 = true
		tr.WarnedChurn150 = true
		msg = formatWorktreeChurnAdvisory(tr.UnverifiedCalls, style)
	} else if tr.UnverifiedCalls >= 75 && !tr.WarnedChurn75 {
		tr.WarnedChurn75 = true
		msg = formatWorktreeChurnAdvisory(tr.UnverifiedCalls, style)
	}
	saveHookSessionTrace(path, tr)
	return msg
}

func resolveWorktreeOrBranchTraceTarget(worktreeDir string) (traceKey, wsRoot, path string) {
	traceKey = extractEnclosingWorktreeRoot(normalizeCanonicalPath(worktreeDir, "/"))
	wsRoot = traceKey
	if traceKey == "" {
		repoRoot, cfg, branch, isBranch := ResolveBranchGuardContext(worktreeDir)
		if !isBranch || IsProtectedTrunkForConfig(branch, cfg) {
			return "", "", ""
		}
		traceKey = BranchTraceKey(repoRoot, branch)
		wsRoot = repoRoot
	}
	path = resolveTraceStateFilePath(traceKey)
	if path == "" {
		return "", "", ""
	}
	return traceKey, wsRoot, path
}

// MarkWorktreeVerificationPassed clears DirtyUnverifiedEdits and UnverifiedCalls when 'nomos verify' passes.
// @intent task_lifecycle_cli_spec.md
func MarkWorktreeVerificationPassed(worktreeDir string) {
	traceKey, wsRoot, path := resolveWorktreeOrBranchTraceTarget(worktreeDir)
	if path == "" || wsRoot == "" {
		return
	}
	tr := loadHookSessionTrace(traceKey)
	if tr == nil {
		return
	}
	resetWorktreeChurnCounters(tr)
	refreshTraceObservedDigests(wsRoot, tr)
	saveHookSessionTrace(path, *tr)
}

// MarkWorktreeVerificationFailed preserves DirtyUnverifiedEdits when 'nomos verify' fails in-process.
// @intent task_lifecycle_cli_spec.md
func MarkWorktreeVerificationFailed(worktreeDir string) {
	markWorktreeVerificationFailed(worktreeDir)
}

// markWorktreeVerificationFailed preserves DirtyUnverifiedEdits when 'nomos verify' fails in-process.
func markWorktreeVerificationFailed(worktreeDir string) {
	traceKey, wsRoot, path := resolveWorktreeOrBranchTraceTarget(worktreeDir)
	if path == "" || traceKey == "" {
		return
	}
	tr := loadOrInitHookSessionTrace(path, wsRoot)
	tr.DirtyUnverifiedEdits = true
	saveHookSessionTrace(path, tr)
}

func resetWorktreeChurnCounters(tr *HookSessionTrace) {
	tr.UnverifiedCalls = 0
	tr.DirtyUnverifiedEdits = false
	tr.WarnedChurn75 = false
	tr.WarnedChurn150 = false
	tr.StopBlockStrikes = 0
}

func isNomosVerificationCommand(cmdLine string) bool {
	trimmed := strings.TrimSpace(cmdLine)
	if trimmed == "" || !standaloneVerifyCmdRe.MatchString(trimmed) || strings.ContainsAny(trimmed, ";|") {
		return false
	}
	lower := strings.ToLower(trimmed)
	return !hasAnySubstring(lower, "--impact", "--fast-file", "--help", " -h")
}

func hasFailedVerificationResponse(resp string) bool {
	lower := strings.ToLower(strings.TrimSpace(resp))
	if lower == "" {
		return false
	}
	return hasAnySubstring(lower,
		"verification failed",
		"dod verification failed",
		"exited with code 1",
		"exit status 1",
	)
}

// recordPostToolSessionTrace records PostToolUse file mutations or shell runs when PreToolUse was not invoked.
func recordPostToolSessionTrace(p *HookPayload, hermeticDir string) {
	if p == nil || p.IsPostInvoke {
		return
	}
	traceKey, wsRoot, branchLabel := resolveTraceScopeAndWorkspace(p, hermeticDir)
	if traceKey == "" && branchLabel == "" {
		return
	}
	path := resolveTraceStateFilePath(traceKey)
	if path == "" {
		return
	}
	tr := loadOrInitHookSessionTrace(path, wsRoot)
	if tr.ToolCallsCount == 0 {
		tr.ToolCallsCount = 1
	}
	recordTracePayloadDetails(&tr, p, wsRoot)
	if p.CommandLine != "" && isPotentialMutatingShellCommand(p.CommandLine) {
		discoverShellMutatedFiles(wsRoot, &tr)
	}
	if isNomosVerificationCommand(p.CommandLine) && hasFailedVerificationResponse(p.ToolResponseText) {
		tr.DirtyUnverifiedEdits = true
	}
	saveHookSessionTrace(path, tr)
}

func resolveTraceScopeAndWorkspace(p *HookPayload, hermeticDir string) (string, string, string) {
	if wtRoot := resolveWorktreeTraceRoot(p, hermeticDir); wtRoot != "" {
		return wtRoot, wtRoot, ""
	}
	return resolveBranchGuardScopeFromPayload(p, hermeticDir)
}

func recordTracePayloadDetails(tr *HookSessionTrace, p *HookPayload, wtRoot string) {
	if cmd := strings.TrimSpace(p.CommandLine); cmd != "" {
		tr.ShellCommandsCount++
		if isRecognizedTestCommand(cmd) {
			tr.TestCommandsCount++
		}
	}
	if p.TargetFile == "" || p.CommandLine != "" || isReadOnlyTool(p.ToolName) {
		return
	}
	targets := p.TargetFiles
	if len(targets) == 0 {
		targets = []string{p.TargetFile}
	}
	for _, rawTarget := range targets {
		resolved := normalizeCanonicalPath(rawTarget, wtRoot)
		if !strings.HasPrefix(resolved, wtRoot+"/") {
			continue
		}
		rel := strings.TrimPrefix(resolved, wtRoot+"/")
		if rel == "" || strings.HasPrefix(rel, ".tmp/") {
			continue
		}
		tr.MutatedFiles = appendUniqueBounded(tr.MutatedFiles, rel, 64)
		tr.DirtyUnverifiedEdits = true
		if dig := computeFileContentDigest(resolved); dig != "" {
			if tr.ObservedDigests == nil {
				tr.ObservedDigests = make(map[string]string, 8)
			}
			tr.ObservedDigests[rel] = dig
		}
	}
}

func resolveTraceStateFilePath(wtRoot string) string {
	canon := normalizeCanonicalPath(wtRoot, "/")
	if canon == "" {
		return ""
	}
	hookDir := resolveHookStateCacheDir()
	if hookDir == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(canon))
	return filepath.Join(hookDir, fmt.Sprintf("nomos-hook-trace-%s.json", hex.EncodeToString(sum[:8])))
}

func loadOrInitHookSessionTrace(path, wtRoot string) HookSessionTrace {
	if existing := loadHookSessionTraceByFile(path); existing != nil {
		return *existing
	}
	return HookSessionTrace{WorktreeRoot: wtRoot}
}

// LoadHookSessionTrace loads persisted hook trace state for wtPath when present.
// @intent task_lifecycle_cli_spec.md
func LoadHookSessionTrace(wtPath string) *HookSessionTrace {
	return loadHookSessionTrace(wtPath)
}

func loadHookSessionTrace(wtPath string) *HookSessionTrace {
	if strings.TrimSpace(wtPath) == "" {
		return nil
	}
	return loadHookSessionTraceByFile(resolveTraceStateFilePath(wtPath))
}

func loadHookSessionTraceByFile(path string) *HookSessionTrace {
	if path == "" {
		return nil
	}
	if fi, err := os.Lstat(path); err != nil || !fi.Mode().IsRegular() {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var tr HookSessionTrace
	if uErr := json.Unmarshal(data, &tr); uErr != nil {
		return nil
	}
	return &tr
}

func saveHookSessionTrace(path string, tr HookSessionTrace) {
	if fi, err := os.Lstat(path); err == nil && !fi.Mode().IsRegular() {
		return
	}
	data, err := json.Marshal(tr)
	if err != nil {
		return
	}
	if wErr := capbac.WriteStateFileAtomically(path, data); wErr != nil {
		return
	}
}

// ClearHookSessionTrace removes transient worktree hook trace state after 2PC release or abort.
// @intent task_lifecycle_cli_spec.md
func ClearHookSessionTrace(wtPath string) {
	clearHookSessionTrace(wtPath)
}

// clearHookSessionTrace removes transient worktree hook trace state after 2PC release or abort.
func clearHookSessionTrace(wtPath string) {
	if path := resolveTraceStateFilePath(wtPath); path != "" {
		tui.RemoveBestEffort(path)
	}
}
