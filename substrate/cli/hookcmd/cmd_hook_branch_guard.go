// Package hookcmd implements Branch Guard PreToolUse, Stop, and post-commit hook logic.
package hookcmd

import (
	"fmt"
	"github.com/mgantlett/nomos/substrate/cli/hookcmd/shellguard"
	"os"
	"path/filepath"
	"strings"

	"github.com/mgantlett/nomos/substrate/cli/tui"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/config"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/constants"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/mem"
	"github.com/spf13/cobra"
)

// ResolveBranchGuardContext resolves repo root, config, active branch, and whether isolation_mode is branch.
// @intent nomos_hermetic_worktree_spec.md
func ResolveBranchGuardContext(dirOrPath string) (string, *config.EcosystemConfig, string, bool) {
	start := strings.TrimSpace(dirOrPath)
	if start == "" {
		return "", nil, "", false
	}
	if info, err := os.Stat(start); err == nil && !info.IsDir() {
		start = filepath.Dir(start)
	} else if os.IsNotExist(err) {
		start = filepath.Dir(start)
	}
	repoRoot := findRepoRootFast(start)
	if repoRoot == "" {
		return "", nil, "", false
	}
	if _, cfgErr := os.Stat(filepath.Join(repoRoot, constants.FileNomosYAML)); cfgErr != nil {
		return filepath.ToSlash(filepath.Clean(repoRoot)), nil, "", false
	}
	cfg, err := config.LoadConfig(repoRoot)
	if err != nil || cfg == nil || cfg.Governance.Boundary.EffectiveIsolationMode() != config.IsolationModeBranch {
		return filepath.ToSlash(filepath.Clean(repoRoot)), cfg, "", false
	}
	branch := resolveCurrentGitBranchFast(repoRoot)
	return filepath.ToSlash(filepath.Clean(repoRoot)), cfg, branch, true
}

func findRepoRootFast(start string) string {
	cur := filepath.Clean(start)
	if cur == "" || cur == "." {
		cwd, err := os.Getwd()
		if err != nil {
			return ""
		}
		cur = cwd
	}
	for {
		if _, err := os.Stat(filepath.Join(cur, constants.DirGit)); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

func resolveGitDirFast(repoRoot string) string {
	dotGit := filepath.Join(repoRoot, constants.DirGit)
	info, err := os.Stat(dotGit)
	if err != nil {
		return ""
	}
	if info.IsDir() {
		return dotGit
	}
	raw, rErr := os.ReadFile(dotGit)
	if rErr != nil {
		return ""
	}
	line := strings.TrimSpace(string(raw))
	const prefix = "gitdir:"
	if !strings.HasPrefix(line, prefix) {
		return ""
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if gitDir == "" {
		return ""
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(repoRoot, gitDir)
	}
	return filepath.Clean(gitDir)
}

func resolveCurrentGitBranchFast(repoRoot string) string {
	gitDir := resolveGitDirFast(repoRoot)
	if gitDir != "" {
		if headRaw, err := os.ReadFile(filepath.Join(gitDir, "HEAD")); err == nil {
			line := strings.TrimSpace(string(headRaw))
			const refPrefix = "ref: "
			const headsPrefix = "refs/heads/"
			if strings.HasPrefix(line, refPrefix) {
				ref := strings.TrimSpace(strings.TrimPrefix(line, refPrefix))
				if strings.HasPrefix(ref, headsPrefix) {
					return strings.TrimPrefix(ref, headsPrefix)
				}
				return "HEAD"
			}
			if line != "" {
				return "HEAD"
			}
		}
	}
	return resolveCurrentGitBranch(repoRoot)
}

func resolveCurrentGitBranch(repoRoot string) string {
	out, err := workspace.SafeGitCommand(repoRoot, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// IsProtectedTrunkForConfig reports whether branch is protected in Branch Guard mode.
// @intent nomos_hermetic_worktree_spec.md
func IsProtectedTrunkForConfig(branch string, cfg *config.EcosystemConfig) bool {
	trimmed := strings.TrimSpace(branch)
	if trimmed == "" || trimmed == "HEAD" || workspace.IsProtectedBranch(trimmed) {
		return true
	}
	if cfg != nil && strings.TrimSpace(cfg.DefaultBranch) != "" {
		return strings.EqualFold(trimmed, strings.TrimSpace(cfg.DefaultBranch))
	}
	return false
}

// SanitizeBranchSlug converts a git branch name into a deterministic uppercase trace/trap slug.
// @intent task_lifecycle_cli_spec.md
func SanitizeBranchSlug(branch string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToUpper(strings.TrimSpace(branch)) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// BranchTraceKey returns the canonical synthetic trace key for a feature branch in Branch Guard mode.
// @intent nomos_hermetic_worktree_spec.md
func BranchTraceKey(repoRoot, branch string) string {
	canon := filepath.ToSlash(filepath.Clean(repoRoot))
	return canon + "#branch:" + SanitizeBranchSlug(branch)
}

// ClearBranchSessionTrace deletes transient hook session trace state for repoRoot and branch.
// @intent task_lifecycle_cli_spec.md
func ClearBranchSessionTrace(repoRoot, branch string) {
	if strings.TrimSpace(repoRoot) == "" || strings.TrimSpace(branch) == "" {
		return
	}
	clearHookSessionTrace(BranchTraceKey(repoRoot, branch))
}

func isProtectedGovernanceTarget(resolved, repoRoot string) bool {
	canonRoot := filepath.ToSlash(filepath.Clean(repoRoot))
	canonTarget := filepath.ToSlash(filepath.Clean(resolved))
	if !strings.HasPrefix(canonTarget, canonRoot+"/") {
		return false
	}
	rel := strings.TrimPrefix(canonTarget, canonRoot+"/")
	switch rel {
	case constants.FileNomosYAML, constants.FileNomosBaselineJSON, ".git/info/exclude":
		return true
	}
	return strings.HasPrefix(rel, ".git/hooks/")
}

func evaluateBranchGuardMutation(rawTarget, resolved, repoRoot, branch string, cfg *config.EcosystemConfig) shellguard.HookDecision {
	if isProtectedGovernanceTarget(resolved, repoRoot) {
		return shellguard.HookDecision{
			Decision: hookDecisionDeny,
			Reason: fmt.Sprintf(
				"Governance Tamper Guard (Law 7): Direct agent mutation of '%s' (resolved: '%s') is prohibited in Branch Guard mode.",
				rawTarget, resolved,
			),
		}
	}
	if !strings.HasPrefix(resolved, repoRoot+"/") {
		return shellguard.HookDecision{Decision: hookDecisionAllow}
	}
	if IsProtectedTrunkForConfig(branch, cfg) {
		label := branch
		if label == "" {
			label = "main"
		}
		return shellguard.HookDecision{
			Decision: hookDecisionDeny,
			Reason: fmt.Sprintf(
				"Branch Guard Violation (Law 3): Direct mutation of '%s' on protected branch '%s' is prohibited. Create or switch to a feature branch via 'git switch -c feat/<slug>' (or 'fix/<slug>'), then edit files normally.",
				rawTarget, label,
			),
		}
	}
	return shellguard.HookDecision{Decision: hookDecisionAllow}
}

func resolveBranchGuardScopeFromPayload(p *shellguard.HookPayload, hermeticDir string) (string, string, string) {
	if p == nil || hermeticDir != "" {
		return "", "", ""
	}
	baseCwd := resolveHookWorkspaceDir(p, "")
	candidates := []string{p.TargetFile, p.Cwd, baseCwd}
	candidates = append(candidates, p.WorkspacePaths...)
	seen := make(map[string]bool, len(candidates))
	for _, raw := range candidates {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		norm := shellguard.NormalizeCanonicalPath(raw, baseCwd)
		if seen[norm] {
			continue
		}
		seen[norm] = true
		repoRoot, cfg, branch, isBranch := ResolveBranchGuardContext(norm)
		if !isBranch || IsProtectedTrunkForConfig(branch, cfg) {
			continue
		}
		return BranchTraceKey(repoRoot, branch), repoRoot, branch
	}
	return "", "", ""
}

func newHookPostCommitCmd() *cobra.Command {
	var targetDir string
	cmd := &cobra.Command{
		Use:     "post-commit",
		Short:   "Distill verified Branch Guard trap-to-fix lessons into Mneme after git commit",
		Long:    tui.HelpHookPostCommitLong,
		Example: tui.HelpHookPostCommitExample,
		RunE: func(_ *cobra.Command, _ []string) error {
			dir := strings.TrimSpace(targetDir)
			if dir == "" {
				cwd, err := os.Getwd()
				if err != nil {
					return nil
				}
				dir = cwd
			}
			RunPostCommitDistillation(dir)
			return nil
		},
	}
	cmd.Flags().StringVarP(&targetDir, "dir", "d", "", "Target repository directory (defaults to current working directory)")
	return cmd
}

// RunPostCommitDistillation distills resolved BRANCH-* traps and commit patterns in Branch Guard mode.
// @intent task_lifecycle_cli_spec.md
func RunPostCommitDistillation(targetDir string) {
	repoRoot, cfg, branch, isBranch := ResolveBranchGuardContext(targetDir)
	if !isBranch || IsProtectedTrunkForConfig(branch, cfg) {
		return
	}
	commitMsgOut, mErr := workspace.SafeGitCommand(repoRoot, "log", "-1", "--format=%B").Output()
	if mErr != nil {
		return
	}
	commitMsg := strings.TrimSpace(string(commitMsgOut))
	var shortSHA string
	if shortOut, sErr := workspace.SafeGitCommand(repoRoot, "log", "-1", "--format=%h").Output(); sErr == nil {
		shortSHA = strings.ToUpper(strings.TrimSpace(string(shortOut)))
	}
	var touched []string
	if filesOut, fErr := workspace.SafeGitCommand(repoRoot, "diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD").Output(); fErr == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(filesOut)), "\n") {
			if clean := strings.TrimSpace(line); clean != "" {
				touched = append(touched, clean)
			}
		}
	}
	branchTaskKey := "BRANCH-" + SanitizeBranchSlug(branch)
	if _, err := mem.DistillTaskLearnings(repoRoot, branchTaskKey, commitMsg, touched); err != nil {
		// non-blocking post-commit Mneme capture
	}
	if shortSHA != "" {
		if _, err := mem.DistillVerifiedPattern(repoRoot, "COMMIT-"+shortSHA, commitMsg, touched); err != nil {
			// non-blocking post-commit Mneme capture
		}
	}
	ClearBranchSessionTrace(repoRoot, branch)
}
