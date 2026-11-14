// Package cli implements deterministic Receipt-Trace Parity validation and atomic 2PC rollback guards.
package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mgantlett/nomos/substrate/cli/hookcmd"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/types"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
)

var (
	exclusiveScopePattern   = regexp.MustCompile("(?i)\\bonly\\s+(?:modified|edited|changed|touched)\\s+`?([a-zA-Z0-9_./-]+\\.[a-zA-Z0-9]+)`?")
	explicitTestRunClaimPat = regexp.MustCompile("(?i)\\b(?:ran|executed)\\s+`?(?:go\\s+test|pytest|cargo\\s+test|npm\\s+test|pnpm\\s+test|yarn\\s+test|vitest|jest|bun\\s+test)\\b")
	changesBulletFilePat    = regexp.MustCompile("^[-*]\\s+`?([A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)+\\.[A-Za-z0-9]+)`?\\s*:")
)

func readGitHeadSHA(repoRoot string) string {
	if repoRoot == "" {
		return ""
	}
	out, err := workspace.SafeGitCommand(repoRoot, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	sha := strings.TrimSpace(string(out))
	if len(sha) != 40 && len(sha) != 64 {
		return ""
	}
	return sha
}

func rollbackTrunkMergeOnDBError(repoRoot, preMergeSHA string) {
	if repoRoot == "" || preMergeSHA == "" {
		return
	}
	unlockTrunk(repoRoot)
	if err := workspace.SafeGitCommand(repoRoot, "reset", "--hard", preMergeSHA).Run(); err != nil {
		lockTrunk(repoRoot)
		return
	}
	lockTrunk(repoRoot)
}

// validateReceiptTraceParity cross-checks walkthrough claims against git diff touchedFiles and
// any recorded worktree hook trace, degrading gracefully when no hook session log exists.
func validateReceiptTraceParity(wtPath, walkthroughPath string, touchedFiles []string) error {
	return validateReceiptTraceParityContent(wtPath, readWalkthroughContent(walkthroughPath), touchedFiles)
}

func validateReceiptTraceParityContent(wtPath, rawContent string, touchedFiles []string) error {
	content := strings.TrimSpace(rawContent)
	if content == "" {
		return nil
	}
	trace := hookcmd.LoadHookSessionTrace(wtPath)
	mutatedCount := len(touchedFiles)
	if mutatedCount == 0 && trace != nil {
		mutatedCount = len(trace.MutatedFiles)
	}
	if mutatedCount > 0 && walkthroughClaimsZeroMutations(content) {
		return types.NewNomosError(
			types.ErrCodeValidationFailed,
			"Receipt-Trace Parity Violation (Law 2): walkthrough claims zero file modifications, but %d file(s) were mutated in the task diff/trace",
			mutatedCount,
		).WithRemedy("update the walkthrough summary to accurately reflect the modified files before re-running 'nomos task sync'")
	}
	if offender, claimed := findExclusiveScopeContradiction(content, touchedFiles); offender != "" {
		return types.NewNomosError(
			types.ErrCodeValidationFailed,
			"Receipt-Trace Parity Violation (Law 2): walkthrough claims exclusive modification of '%s', but git diff also modified '%s'",
			claimed, offender,
		).WithRemedy("list all modified files or remove false single-file exclusivity claims in the walkthrough")
	}
	if phantom := findUnchangedClaimedFileInChangesSection(content, touchedFiles); phantom != "" {
		return types.NewNomosError(
			types.ErrCodeValidationFailed,
			"Receipt-Trace Parity Violation (Law 2): walkthrough ## Changes claims modification of '%s', which has zero diff in the worktree",
			phantom,
		).WithRemedy("remove un-modified files from walkthrough ## Changes or stage the missing edits before 'nomos task sync'")
	}
	if trace == nil || trace.ToolCallsCount == 0 || trace.ShellCommandsCount == 0 {
		return nil
	}
	if trace.TestCommandsCount == 0 && explicitTestRunClaimPat.MatchString(content) {
		return types.NewNomosError(
			types.ErrCodeValidationFailed,
			"Receipt-Trace Parity Violation (Law 2): walkthrough claims explicit test-runner execution, but the active hook session trace recorded 0 test commands across %d shell call(s)",
			trace.ShellCommandsCount,
		).WithRemedy("execute the test suite in the task worktree or remove unverified test-execution claims from the walkthrough")
	}
	return nil
}

func walkthroughClaimsZeroMutations(content string) bool {
	lower := strings.ToLower(content)
	return hookcmd.HasAnySubstring(lower,
		"no files were modified",
		"no files modified",
		"zero files modified",
		"zero files changed",
		"no code changes were made",
		"made no file modifications",
	)
}

func findExclusiveScopeContradiction(content string, touchedFiles []string) (string, string) {
	if len(touchedFiles) <= 1 {
		return "", ""
	}
	matches := exclusiveScopePattern.FindStringSubmatch(content)
	if len(matches) < 2 {
		return "", ""
	}
	claimed := filepath.ToSlash(strings.TrimSpace(matches[1]))
	claimedBase := filepath.Base(claimed)
	for _, f := range touchedFiles {
		norm := filepath.ToSlash(strings.TrimSpace(f))
		if norm == "" {
			continue
		}
		if norm != claimed && !strings.HasSuffix(norm, "/"+claimed) && filepath.Base(norm) != claimedBase {
			return norm, claimed
		}
	}
	return "", ""
}

func findUnchangedClaimedFileInChangesSection(content string, touchedFiles []string) string {
	inChanges := false
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if strings.HasPrefix(line, "## ") {
			inChanges = strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(line, "## ")), "Changes")
			continue
		}
		if !inChanges {
			continue
		}
		m := changesBulletFilePat.FindStringSubmatch(line)
		if len(m) == 2 && !matchesAnyTouchedFile(m[1], touchedFiles) {
			return m[1]
		}
	}
	return ""
}

func matchesAnyTouchedFile(claimed string, touchedFiles []string) bool {
	normClaim := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(claimed)), "./")
	claimBase := filepath.Base(normClaim)
	for _, tf := range touchedFiles {
		norm := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(tf)), "./")
		if norm == normClaim || strings.HasSuffix(norm, "/"+normClaim) || strings.HasSuffix(normClaim, "/"+norm) || filepath.Base(norm) == claimBase {
			return true
		}
	}
	return false
}

func rebaseOntoTrunkIfDiverged(repoRoot, wtPath, branch string) (bool, error) {
	fi, err := os.Stat(wtPath)
	if err != nil || !fi.IsDir() {
		return false, nil
	}
	trunk := detectDefaultBranch(repoRoot)
	ancCmd := workspace.SafeGitCommand(repoRoot, "merge-base", "--is-ancestor", trunk, branch)
	if ancCmd.Run() == nil {
		return false, nil
	}
	rebCmd := workspace.SafeGitCommand(wtPath, "rebase", trunk)
	rebCmd.Env = append(rebCmd.Env, "NOMOS_COMMIT_IN_PROGRESS=1")
	if out, rebErr := rebCmd.CombinedOutput(); rebErr != nil {
		if abErr := workspace.SafeGitCommand(wtPath, "rebase", "--abort").Run(); abErr != nil {
			// best-effort clean abort
		}
		return false, types.NewNomosError(types.ErrCodeBoundary,
			"concurrent trunk conflict rebasing %s onto %s (rebase aborted cleanly): %s",
			branch, trunk, strings.TrimSpace(string(out)))
	}
	return true, nil
}

func fastForwardTrunkMerge(repoRoot, branch string) error {
	healAccidentalBareConfig(repoRoot)
	unlockTrunk(repoRoot)
	mergeCmd := workspace.SafeGitCommand(repoRoot, "merge", "--ff-only", branch)
	if out, err := mergeCmd.CombinedOutput(); err != nil {
		lockTrunk(repoRoot)
		return types.NewNomosError(types.ErrCodeBoundary, "fast-forward merge failed: %s", string(out))
	}
	lockTrunk(repoRoot)
	return nil
}

func healAccidentalBareConfig(repoRoot string) {
	if repoRoot == "" {
		return
	}
	gitDir := filepath.Join(repoRoot, ".git")
	fi, err := os.Stat(gitDir)
	if err != nil || !fi.IsDir() {
		return
	}
	out, cfgErr := workspace.SafeGitCommand(repoRoot, "config", "--bool", "core.bare").Output()
	if cfgErr != nil || strings.TrimSpace(string(out)) != "true" {
		return
	}
	if setErr := workspace.SafeGitCommand(repoRoot, "config", "--bool", "core.bare", "false").Run(); setErr != nil {
		// best-effort bare flag healing
	}
}

func readWalkthroughContent(path string) string {
	if path == "" {
		return ""
	}
	absPath, aErr := filepath.Abs(filepath.Clean(path))
	if aErr != nil {
		return ""
	}
	absDir := filepath.Dir(absPath)
	if rel, rErr := filepath.Rel(absDir, absPath); rErr != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return ""
	}
	return string(data)
}

func unlockTrunk(root string) {
	if err := workspace.UnlockTrunk(root); err != nil {
		// ignore non-fatal trunk permission errors
	}
}

func lockTrunk(root string) {
	if err := workspace.LockTrunk(root); err != nil {
		// ignore non-fatal trunk permission errors
	}
}
