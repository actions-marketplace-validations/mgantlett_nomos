// Package hookcmd implements one-time JIT directory-scoped Mneme pitfall recall and scratch test placement hygiene.
package hookcmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/mgantlett/nomos/substrate/cli/hookcmd/shellguard"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/config"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/mem"
)

var worktreeTaskKeyTokenRe = regexp.MustCompile(`(?i)(?:^|-)((?:SUB|INT)-\d+)(?:$|-)`)

type scopeMutationPair struct {
	relDir  string
	relFile string
}

// evaluateJITDirectoryPitfallAdvisory recalls matching directory-scoped pitfalls from Nomos Mneme
// at most once per directory in a worktree session trace (@intent mneme_memory_spec.md).
func evaluateJITDirectoryPitfallAdvisory(p *shellguard.HookPayload, hermeticDir string) string {
	wtRoot := resolveWorktreeTraceRoot(p, hermeticDir)
	if wtRoot == "" {
		return ""
	}
	tracePath := resolveTraceStateFilePath(wtRoot)
	if tracePath == "" {
		return ""
	}
	canonWt := shellguard.NormalizeCanonicalPath(wtRoot, "/")
	baseCwd := resolveHookWorkspaceDir(p, hermeticDir)
	tr := loadOrInitHookSessionTrace(tracePath, wtRoot)
	pairs, dirty := collectUnseenMutationScopes(p, &tr, canonWt, baseCwd)
	if dirty {
		saveHookSessionTrace(tracePath, tr)
	}
	if len(pairs) == 0 {
		return ""
	}
	if _, err := os.Stat(config.ResolveMnemeDBPath()); err != nil {
		return ""
	}
	repoRoot := resolveWorktreePrimaryRepoRoot(wtRoot)
	taskKey := extractWorktreeTaskKeyFromRoot(wtRoot)
	var lines []string
	for _, pair := range pairs {
		for _, item := range recallScopedDirectoryPitfalls(repoRoot, taskKey, pair.relFile, pair.relDir) {
			lines = append(lines, fmt.Sprintf("⚡ [Nomos JIT Pitfall · %s] %s", pair.relDir, item))
		}
	}
	return strings.Join(lines, "\n")
}

// collectUnseenMutationScopes records newly mutated worktree directories in HookSessionTrace
// so subsequent edits in the same package never repeat Mneme queries or spam advisories.
func collectUnseenMutationScopes(p *shellguard.HookPayload, tr *HookSessionTrace, canonWt, baseCwd string) ([]scopeMutationPair, bool) {
	targets := p.TargetFiles
	if len(targets) == 0 && p.TargetFile != "" {
		targets = []string{p.TargetFile}
	}
	var unseen []scopeMutationPair
	dirty := false
	for _, rawTarget := range targets {
		resolved := shellguard.NormalizeCanonicalPath(rawTarget, baseCwd)
		if !strings.HasPrefix(resolved, canonWt+"/") {
			continue
		}
		relFile := strings.TrimPrefix(resolved, canonWt+"/")
		if isIgnoredPitfallScopeFile(relFile) {
			continue
		}
		relDir := mem.DeriveDirectoryScope("", []string{relFile})
		if relDir == "" || containsTraceItem(tr.RecalledPitfallDirs, relDir) {
			continue
		}
		tr.RecalledPitfallDirs = appendUniqueBounded(tr.RecalledPitfallDirs, relDir, 64)
		dirty = true
		unseen = append(unseen, scopeMutationPair{relDir: relDir, relFile: relFile})
	}
	return unseen, dirty
}

// isIgnoredPitfallScopeFile skips hidden dotfiles and scratch .tmp directories from Mneme scoping.
func isIgnoredPitfallScopeFile(relFile string) bool {
	return relFile == "" || strings.HasPrefix(relFile, ".") || strings.Contains(relFile, "/.tmp/")
}

// containsTraceItem checks whether target already exists in items.
func containsTraceItem(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

// recallScopedDirectoryPitfalls invokes RecallRepoPitfallsForTask when an active directory-scoped
// pitfall matches relDir and filters results strictly to that directory scope.
func recallScopedDirectoryPitfalls(repoRoot, taskKey, relFile, relDir string) []string {
	matchedSet := collectMatchingScopedPitfallSet(repoRoot, relDir)
	if len(matchedSet) == 0 {
		return nil
	}
	var scoped []string
	for _, item := range mem.RecallRepoPitfallsForTask(repoRoot, taskKey, []string{relFile}, 2) {
		if matchedSet[item] {
			scoped = append(scoped, item)
		}
	}
	return scoped
}

// collectMatchingScopedPitfallSet returns active pitfall/dead-end entries scoped to relDir.
func collectMatchingScopedPitfallSet(repoRoot, relDir string) map[string]bool {
	store, err := mem.NewStore("")
	if err != nil {
		return nil
	}
	defer func() {
		if cErr := store.Close(); cErr != nil {
			return
		}
	}()
	all, err := store.ListAll()
	if err != nil {
		return nil
	}
	repoID := strings.ToLower(filepath.Base(filepath.Clean(repoRoot)))
	topicSuffix := ":" + strings.ToLower(strings.TrimSpace(relDir))
	inBracketTag := " in " + relDir + "]"
	matched := make(map[string]bool)
	for _, m := range all {
		if m == nil || m.Status != mem.StatusActive || (m.Kind != mem.KindPitfall && m.Kind != mem.KindDeadEnd) || strings.ToLower(strings.TrimSpace(m.RepoID)) != repoID {
			continue
		}
		lowerTopic := strings.ToLower(strings.TrimSpace(m.Topic))
		if strings.HasSuffix(lowerTopic, topicSuffix) || strings.Contains(m.Content, inBracketTag) {
			matched[m.Content] = true
		}
	}
	return matched
}

// resolveWorktreePrimaryRepoRoot resolves the parent trunk repo path owning wtRoot.
func resolveWorktreePrimaryRepoRoot(wtRoot string) string {
	if pr, err := workspace.FindRepoRoot(wtRoot); err == nil && pr != "" {
		return filepath.Clean(pr)
	}
	slashWt := filepath.ToSlash(wtRoot)
	if idx := strings.Index(slashWt, "/worktrees/"); idx > 0 {
		return filepath.FromSlash(slashWt[:idx])
	}
	return filepath.Clean(wtRoot)
}

// extractWorktreeTaskKeyFromRoot parses the trailing SUB-<N> or INT-<N> key from a worktree dir name.
func extractWorktreeTaskKeyFromRoot(wtRoot string) string {
	base := filepath.Base(filepath.Clean(wtRoot))
	matches := worktreeTaskKeyTokenRe.FindAllStringSubmatch(base, -1)
	if len(matches) == 0 || len(matches[len(matches)-1]) < 2 {
		return ""
	}
	return strings.ToUpper(strings.TrimSpace(matches[len(matches)-1][1]))
}

// evaluateTestPlacementHygieneAdvisory emits a one-time non-blocking tip when a net-new
// test file is created outside .tmp/ inside an initialized workspace (nomos.yaml) with zero baseline committed tests.
func evaluateTestPlacementHygieneAdvisory(resolved, baseCwd string) string {
	wsRoot := resolveHygieneRoot(resolved, baseCwd)
	if wsRoot == "" || !isWorkspaceTestPath(resolved, wsRoot) {
		return ""
	}
	if !hasExplicitNomosConfig(resolved, baseCwd) && !hasExplicitNomosConfig(resolved, wsRoot) {
		return ""
	}
	rel, err := filepath.Rel(wsRoot, resolved)
	if err != nil {
		return ""
	}
	slashRel := filepath.ToSlash(filepath.Clean(rel))
	if slashRel == "." || strings.HasPrefix(slashRel, "..") || strings.HasPrefix(strings.ToLower(slashRel), ".tmp/") {
		return ""
	}
	if !workspaceHasZeroTrackedTests(wsRoot) || !markTestHygieneAdvisedOnce(wsRoot) {
		return ""
	}
	return fmt.Sprintf(
		"💡 [Nomos Deliverable Hygiene] Created '%s' outside '.tmp/tests/' in a workspace with zero baseline tests; keep deliverable files clean by placing scratch verification tests under '.tmp/tests/test_<module>.py' (or '.tmp/tests/') unless tests were explicitly requested.",
		slashRel,
	)
}

// resolveHygieneRoot discovers the enclosing git root for resolved or baseCwd.
func resolveHygieneRoot(resolved, baseCwd string) string {
	if pr, err := workspace.FindRepoRoot(resolved); err == nil && pr != "" {
		return pr
	}
	if pr, err := workspace.FindRepoRoot(baseCwd); err == nil && pr != "" {
		return pr
	}
	return baseCwd
}

// workspaceHasZeroTrackedTests reports whether wsRoot tracks any pre-existing test files in Git.
func workspaceHasZeroTrackedTests(wsRoot string) bool {
	if strings.TrimSpace(wsRoot) == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(wsRoot, ".git")); err != nil {
		return true
	}
	out, gErr := workspace.RunSafeGit(wsRoot, "ls-files")
	if gErr != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if rel := strings.TrimSpace(line); rel != "" && isWorkspaceTestPath(filepath.Join(wsRoot, filepath.FromSlash(rel)), wsRoot) {
			return false
		}
	}
	return true
}

// markTestHygieneAdvisedOnce persists a workspace-keyed one-shot marker in os.TempDir.
func markTestHygieneAdvisedOnce(wsRoot string) bool {
	sum := sha256.Sum256([]byte(filepath.Clean(wsRoot)))
	marker := filepath.Join(os.TempDir(), "nomos-test-hygiene-"+hex.EncodeToString(sum[:8])+".seen")
	if _, err := os.Stat(marker); err == nil {
		return false
	}
	if wErr := os.WriteFile(marker, []byte("1"), 0600); wErr != nil {
		return true
	}
	return true
}
