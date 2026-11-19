// Package hookcmd implements Tier 1 agent adapters, software physics telemetry,
// release readiness, upgrades, and CLI command dispatch contracts.
package hookcmd

import (
	"fmt"
	"github.com/mgantlett/nomos/substrate/cli/hookcmd/shellguard"
	"github.com/mgantlett/nomos/substrate/cli/tui"
	"io"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const (
	hookDecisionBlock                = "block"
	hookDecisionContinue             = "continue"
	agyNormalStopReason              = "NO_TOOL_CALL"
	maxStopAuditFiles                = 128
	maxContainerStopBlocks           = 4
	maxConfiguredContainerStopBlocks = 6
	containerAppWorkdir              = "/app"
)

var skippedStopAuditDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"__pycache__":  true,
	".venv":        true,
	"venv":         true,
	"vendor":       true,
	"target":       true,
	"dist":         true,
	"build":        true,
}

func newHookStopCmd(hermeticDir *string, containerSandbox *bool) *cobra.Command {
	return &cobra.Command{
		Use:     "stop",
		Short:   "Evaluate Law 2 anti-phantom deliverable gate on Stop or AfterAgent turn exit",
		Long:    tui.HelpHookStopLong,
		Example: tui.HelpHookStopExample,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := io.ReadAll(cmd.InOrStdin())
			if err != nil {
				return writeEmptyJSON(cmd.OutOrStdout())
			}
			payload := parseHookPayload(raw)
			dec := EvaluateStopHook(payload, *hermeticDir, *containerSandbox)
			if dec.Decision != hookDecisionBlock {
				return writeEmptyJSON(cmd.OutOrStdout())
			}
			if payload != nil && payload.IsAntigravityEvent {
				return writeHookDecision(cmd.OutOrStdout(), shellguard.HookDecision{
					Decision: hookDecisionContinue,
					Reason:   dec.Reason,
				})
			}
			if payload != nil && payload.IsCursorEvent {
				return writeCursorStopResponse(cmd.OutOrStdout(), dec.Reason)
			}
			return writeHookDecision(cmd.OutOrStdout(), dec)
		},
	}
}

// EvaluateStopHook blocks premature agent turn exits when an active worktree holds
// unverified file modifications or when container-sandbox files fail syntax/snooping checks
// (@intent nomos_hermetic_worktree_spec.md).
func EvaluateStopHook(p *shellguard.HookPayload, hermeticDir string, containerSandbox bool) shellguard.HookDecision {
	if p == nil || (!containerSandbox && p.StopHookActive) {
		return shellguard.HookDecision{Decision: hookDecisionAllow}
	}
	if p.TerminationReason != "" && p.TerminationReason != agyNormalStopReason {
		return shellguard.HookDecision{Decision: hookDecisionAllow}
	}
	if containerSandbox && hermeticDir == "" && !p.StopHookActive {
		if dec := evaluateContainerStopHook(p); dec.Decision == hookDecisionBlock {
			return dec
		}
	}
	return evaluateTracedStopHook(p, hermeticDir, containerSandbox)
}

func evaluateTracedStopHook(p *shellguard.HookPayload, hermeticDir string, containerSandbox bool) shellguard.HookDecision {
	traceKey, wsRoot, branchLabel := resolveTraceScopeAndWorkspace(p, hermeticDir)
	if traceKey == "" {
		return shellguard.HookDecision{Decision: hookDecisionAllow}
	}
	tr := loadOrSyncStopTrace(traceKey, wsRoot)
	if tr == nil || len(tr.MutatedFiles) == 0 {
		return shellguard.HookDecision{Decision: hookDecisionAllow}
	}
	needsVerify := tr.DirtyUnverifiedEdits
	needsBehavior := tr.DirtyBehavioralEdits && !hasWorkspaceAutomatedTests(wsRoot)
	if !needsVerify && !needsBehavior {
		return shellguard.HookDecision{Decision: hookDecisionAllow}
	}
	maxBlocks := resolveContainerStopLimit(wsRoot, containerSandbox)
	if p.StopHookActive && tr.StopBlockStrikes >= maxBlocks {
		return shellguard.HookDecision{Decision: hookDecisionAllow}
	}
	tr.StopBlockStrikes++
	if path := resolveTraceStateFilePath(traceKey); path != "" {
		saveHookSessionTrace(path, *tr)
	}
	if !needsVerify && needsBehavior {
		return buildBehavioralStopDecision(wsRoot, len(tr.MutatedFiles), tr.StopBlockStrikes, maxBlocks, containerSandbox)
	}
	radarNote := collectStopInlineRadarSummary(wsRoot, tr.MutatedFiles)
	return buildUnverifiedStopDecision(wsRoot, branchLabel, len(tr.MutatedFiles), tr.StopBlockStrikes, maxBlocks, containerSandbox, radarNote)
}

func resolveContainerStopLimit(wsRoot string, containerSandbox bool) int {
	if containerSandbox && wsRoot != "" && hasExplicitNomosConfig(wsRoot, wsRoot) {
		return maxConfiguredContainerStopBlocks
	}
	return maxContainerStopBlocks
}

func buildBehavioralStopDecision(wsRoot string, fileCount, strike, maxBlocks int, containerSandbox bool) shellguard.HookDecision {
	strikePrefix := "❌ [Nomos Anti-Phantom Gate]"
	if containerSandbox {
		strikePrefix = fmt.Sprintf("❌ [Nomos Anti-Phantom Gate (attempt %d/%d)]", strike, maxBlocks)
	}
	return shellguard.HookDecision{
		Decision: hookDecisionBlock,
		Reason: fmt.Sprintf(
			"%s Workspace '%s' modified %d source file(s) with zero automated test suite and no passing post-edit program/test execution. Create stdlib unittest.TestCase tests in '.tmp/tests/test_<module>.py' covering boundary, multi-item, and error cases and run 'nomos verify', or execute a behavioral smoke test after your final edit before ending your turn (Law 2).",
			strikePrefix, filepath.Base(wsRoot), fileCount,
		),
	}
}

func buildUnverifiedStopDecision(wsRoot, branchLabel string, fileCount, strike, maxBlocks int, containerSandbox bool, radarNote string) shellguard.HookDecision {
	strikePrefix := "❌ [Nomos Anti-Phantom Gate]"
	if containerSandbox {
		strikePrefix = fmt.Sprintf("❌ [Nomos Anti-Phantom Gate (attempt %d/%d)]", strike, maxBlocks)
	}
	reason := fmt.Sprintf(
		"%s Active worktree '%s' has %d modified file(s) without a clean post-edit verification pass. Run 'nomos verify' inside the worktree before ending your turn (Law 2).",
		strikePrefix, filepath.Base(wsRoot), fileCount,
	)
	if branchLabel != "" {
		reason = fmt.Sprintf(
			"%s Active branch '%s' has %d modified file(s) without a clean post-edit verification pass. Run 'nomos verify' before ending your turn (Law 2).",
			strikePrefix, branchLabel, fileCount,
		)
	}
	if containerSandbox && maxBlocks > maxContainerStopBlocks {
		reason += " Headless autonomous session: no human operator is present to grant exceptions or relax nomos.yaml; you are pre-authorized and required to split/refactor complex modules into focused files (<400 LOC, CC/Cog <= 10), fix silent except blocks, and run 'nomos verify' until 42/42 gates pass."
	}
	if radarNote != "" {
		reason += "\n" + radarNote
	}
	return shellguard.HookDecision{Decision: hookDecisionBlock, Reason: reason}
}

func evaluateContainerStopHook(p *shellguard.HookPayload) shellguard.HookDecision {
	baseCwd := firstNonEmpty(p.Cwd, resolveHookWorkspaceDir(p, ""))
	if baseCwd == "" || baseCwd == "." {
		baseCwd = containerAppWorkdir
	}
	cleanRoot := filepath.Clean(baseCwd)
	if isProtectedContainerVerifierPath(filepath.ToSlash(cleanRoot)) {
		return shellguard.HookDecision{Decision: hookDecisionAllow}
	}
	scanned := 0
	var fault string
	walkErr := filepath.WalkDir(cleanRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skippedStopAuditDirs[d.Name()] || isProtectedContainerVerifierPath(filepath.ToSlash(path)) {
				return filepath.SkipDir
			}
			return nil
		}
		if !isStopAuditableFile(path) {
			return nil
		}
		scanned++
		if msg := inspectContainerStopFile(path); msg != "" {
			fault = msg
			return fs.SkipAll
		}
		if scanned >= maxStopAuditFiles {
			return fs.SkipAll
		}
		return nil
	})
	if walkErr != nil && fault == "" {
		return shellguard.HookDecision{Decision: hookDecisionAllow}
	}
	if fault != "" {
		return shellguard.HookDecision{
			Decision: hookDecisionBlock,
			Reason:   fmt.Sprintf("❌ [Nomos Anti-Phantom Gate] Cannot complete turn with broken or non-compliant workspace artifact: %s", fault),
		}
	}
	return shellguard.HookDecision{Decision: hookDecisionAllow}
}

func isStopAuditableFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".py", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".rs", ".json", ".jsonl", ".sh":
		return true
	default:
		return false
	}
}

func inspectContainerStopFile(resolved string) string {
	if snoop := validateContainerFileVerifierClean(resolved); snoop != "" {
		return snoop
	}
	if goMsg := validateGoSyntaxInMemory(resolved); goMsg != "" {
		return goMsg
	}
	return validateStructuredFileInMemory(resolved)
}
