// Package intentlint implements sibling worktree obligation isolation across concurrent
// INT-*/SUB-* task pairs so independent in-flight tasks never deadlock each other.
package intentlint

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/constants"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/task"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/impact"
)

// ResolveActiveWorktreeSpecRoots scopes specification paths when running inside an
// isolated worktree, skipping Substrate AST parity inside INT-* worktrees and deferring
// specs mutated exclusively by another in-flight sibling SUB-* task's INT-* twin
// (@intent nomos_intent_traceability_spec.md #ContractsConfig).
func ResolveActiveWorktreeSpecRoots(scanRoot string, specRoots []string) []string {
	if extractWorktreePrefixedKey(scanRoot, "INT-") != "" {
		return []string{}
	}
	subKey := ExtractWorktreeSubTaskKey(scanRoot)
	if subKey == "" {
		return specRoots
	}
	return ResolveTaskContractSpecRoots(scanRoot, subKey, specRoots)
}

// ResolveTaskContractSpecRoots returns the contract specification files applicable to taskKey,
// excluding specs owned exclusively by another pending sibling SUB-* task's INT-* twin
// (@intent nomos_intent_traceability_spec.md #ContractsConfig).
func ResolveTaskContractSpecRoots(scanRoot, taskKey string, specRoots []string) []string {
	cleanKey := strings.TrimSpace(taskKey)
	if !strings.HasPrefix(cleanKey, "SUB-") {
		return specRoots
	}
	deferred := CollectActiveSiblingTwinSpecs(scanRoot, cleanKey)
	filtered, hasDeferred := FilterDeferredSpecRoots(scanRoot, specRoots, deferred)
	if !hasDeferred {
		return specRoots
	}
	return filtered
}

// ExtractWorktreeSubTaskKey returns the isolated SUB-* task key when scanRoot lies under worktrees/.
// @intent nomos_intent_traceability_spec.md #ContractsConfig
func ExtractWorktreeSubTaskKey(scanRoot string) string {
	return extractWorktreePrefixedKey(scanRoot, "SUB-")
}

func extractWorktreePrefixedKey(scanRoot, prefix string) string {
	slash := filepath.ToSlash(filepath.Clean(scanRoot))
	if !strings.Contains(slash, "/worktrees/") {
		return ""
	}
	baseUpper := strings.ToUpper(filepath.Base(slash))
	idx := strings.LastIndex(baseUpper, prefix)
	if idx < 0 {
		return ""
	}
	cand := baseUpper[idx:]
	if err := task.ValidateTaskKey(cand); err != nil {
		return ""
	}
	return cand
}

// CollectActiveSiblingTwinSpecs returns relative Markdown specification paths touched by
// other pending sibling SUB-* twins that are disjoint from activeSubKey's own twins.
// When activeSubKey is empty (trunk verification), deferral is strictly bounded to non-terminal
// sibling SUB-* tasks whose physical worktree directory exists on disk.
// @intent nomos_intent_traceability_spec.md #ContractsConfig
func CollectActiveSiblingTwinSpecs(scanRoot, activeSubKey string) map[string]bool {
	cleanKey := strings.TrimSpace(activeSubKey)
	if cleanKey != "" && !strings.HasPrefix(cleanKey, "SUB-") {
		return nil
	}
	trunkRoot := resolvePrimaryRepoRoot(scanRoot)
	tasks := loadRepoScopedTasks(trunkRoot)
	if len(tasks) == 0 {
		return nil
	}
	return collectDeferredSiblingSpecsFromTasks(trunkRoot, scanRoot, cleanKey, tasks)
}

func collectDeferredSiblingSpecsFromTasks(trunkRoot, scanRoot, activeSubKey string, tasks []task.TaskRecord) map[string]bool {
	ownSpecs := resolveOwnTwinSpecs(trunkRoot, activeSubKey, tasks)
	deferred := make(map[string]bool)
	for _, rec := range tasks {
		if !isEligibleSiblingRecord(trunkRoot, scanRoot, activeSubKey, rec) {
			continue
		}
		twins := collectTwinIntentKeysForSub(rec.Key, tasks)
		addSiblingTwinSpecFiles(trunkRoot, twins, ownSpecs, deferred)
	}
	return deferred
}

func addSiblingTwinSpecFiles(trunkRoot string, twins []string, ownSpecs, deferred map[string]bool) {
	for _, twin := range twins {
		for sp := range listTwinCommitSpecFiles(trunkRoot, twin) {
			if !ownSpecs[sp] {
				deferred[sp] = true
			}
		}
	}
}

// CollectActiveSiblingTwinDeltas extracts exact commit-delta TC-* vectors, structs, and struct fields
// introduced by pending sibling INT-* twins even when they share a canonical domain spec with activeSubKey.
// When activeSubKey is empty (trunk verification), deferral is strictly bounded to non-terminal
// sibling SUB-* tasks whose physical worktree directory exists on disk
// (@intent nomos_intent_traceability_spec.md #ContractsConfig).
func CollectActiveSiblingTwinDeltas(scanRoot, activeSubKey string) *impact.SiblingDelta {
	cleanKey := strings.TrimSpace(activeSubKey)
	if cleanKey != "" && !strings.HasPrefix(cleanKey, "SUB-") {
		return nil
	}
	trunkRoot := resolvePrimaryRepoRoot(scanRoot)
	tasks := loadRepoScopedTasks(trunkRoot)
	if len(tasks) == 0 {
		return nil
	}
	return computePendingSiblingDeltasFromTasks(trunkRoot, scanRoot, cleanKey, tasks)
}

func computePendingSiblingDeltasFromTasks(trunkRoot, scanRoot, activeSubKey string, tasks []task.TaskRecord) *impact.SiblingDelta {
	ownTwinList := collectTwinIntentKeysForSub(activeSubKey, tasks)
	ownTwins := stringSliceToSet(ownTwinList)
	delta := impact.NewSiblingDelta()
	for _, rec := range tasks {
		if !isEligibleSiblingRecord(trunkRoot, scanRoot, activeSubKey, rec) {
			continue
		}
		accumulateSiblingTwinDeltas(trunkRoot, collectTwinIntentKeysForSub(rec.Key, tasks), ownTwins, delta)
	}
	if len(ownTwinList) > 0 {
		ownDelta := impact.NewSiblingDelta()
		accumulateSiblingTwinDeltas(trunkRoot, ownTwinList, nil, ownDelta)
		subtractOwnDelta(delta, ownDelta)
	}
	return delta
}

func accumulateSiblingTwinDeltas(trunkRoot string, twins []string, exclude map[string]bool, into *impact.SiblingDelta) {
	for _, twin := range twins {
		if !exclude[twin] {
			accumulateTwinCommitDelta(trunkRoot, twin, into)
		}
	}
}

func isEligibleSiblingRecord(trunkRoot, scanRoot, activeSubKey string, rec task.TaskRecord) bool {
	if rec.Key == activeSubKey || !strings.HasPrefix(rec.Key, "SUB-") {
		return false
	}
	if activeSubKey == "" {
		return isSiblingSubLiveOnDisk(trunkRoot, rec)
	}
	return isSiblingSubPendingForWorktree(trunkRoot, scanRoot, rec)
}

func isSiblingSubLiveOnDisk(trunkRoot string, rec task.TaskRecord) bool {
	if rec.Status == task.StatusDone || rec.Status == task.StatusAborted {
		return false
	}
	return hasPhysicalTaskWorktree(trunkRoot, rec.Key)
}

func hasPhysicalTaskWorktree(trunkRoot, subKey string) bool {
	wtRoot := filepath.Join(trunkRoot, "worktrees")
	entries, err := os.ReadDir(wtRoot)
	if err != nil {
		return false
	}
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		if extractWorktreePrefixedKey(filepath.Join(wtRoot, ent.Name()), "SUB-") == subKey {
			return true
		}
	}
	return false
}

func collectTwinIntentKeysForSub(subKey string, tasks []task.TaskRecord) []string {
	cleanSub := strings.TrimSpace(subKey)
	if !strings.HasPrefix(cleanSub, "SUB-") {
		return nil
	}
	seen := make(map[string]bool)
	var twins []string
	for _, rec := range tasks {
		recTwin := strings.TrimSpace(rec.TwinKey)
		if rec.Key == cleanSub && strings.HasPrefix(recTwin, "INT-") && !seen[recTwin] {
			seen[recTwin] = true
			twins = append(twins, recTwin)
		}
		if strings.HasPrefix(rec.Key, "INT-") && recTwin == cleanSub && !seen[rec.Key] {
			seen[rec.Key] = true
			twins = append(twins, rec.Key)
		}
	}
	return twins
}

func stringSliceToSet(items []string) map[string]bool {
	if len(items) == 0 {
		return nil
	}
	set := make(map[string]bool, len(items))
	for _, item := range items {
		set[item] = true
	}
	return set
}

func accumulateTwinCommitDelta(repoRoot, twinKey string, into *impact.SiblingDelta) {
	sha := findTaskCommitSHA(repoRoot, twinKey)
	if sha == "" {
		return
	}
	for specRel := range listTwinCommitSpecFiles(repoRoot, twinKey) {
		beforeMD := readGitBlobString(repoRoot, sha+"~1:"+specRel)
		afterMD := readGitBlobString(repoRoot, sha+":"+specRel)
		if afterMD != "" {
			impact.ComputeMarkdownSpecDelta(beforeMD, afterMD, into)
			recordPolyglotSpecDelta(beforeMD, afterMD, into)
		}
	}
}

func subtractOwnDelta(sibling, own *impact.SiblingDelta) {
	subtractStringSet(sibling.AddedTCs, own.AddedTCs)
	subtractStringSet(sibling.RemovedTCs, own.RemovedTCs)
	subtractStringSet(sibling.AddedStructs, own.AddedStructs)
	subtractFieldSet(sibling.AddedFields, own.AddedFields)
	subtractFieldSet(sibling.RemovedFields, own.RemovedFields)
}

func subtractStringSet(dst, own map[string]bool) {
	for k := range own {
		delete(dst, k)
	}
}

func subtractFieldSet(dst, own map[string]map[string]bool) {
	for st, fields := range own {
		for f := range fields {
			delete(dst[st], f)
		}
	}
}

func findTaskCommitSHA(repoRoot, taskKey string) string {
	out, err := workspace.SafeGitCommand(repoRoot, "log", "--fixed-strings", "--grep=["+taskKey+"]", "-n", "1", "--format=%H").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func readGitBlobString(repoRoot, specRev string) string {
	out, err := workspace.SafeGitCommand(repoRoot, "show", specRev).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func resolvePrimaryRepoRoot(scanRoot string) string {
	if pr, err := workspace.FindRepoRoot(scanRoot); err == nil && pr != "" {
		return pr
	}
	return scanRoot
}

func loadRepoScopedTasks(trunkRoot string) []task.TaskRecord {
	store, err := task.NewDefaultSQLiteTaskStore()
	if err != nil || store == nil {
		return nil
	}
	defer func() {
		if cErr := store.Close(); cErr != nil {
			// best-effort read-only store close
		}
	}()
	project := task.ResolveRepoProject(trunkRoot)
	return task.FilterTasksByProject(store.ListTasks(), project, true)
}

func resolveOwnTwinSpecs(trunkRoot, activeSubKey string, tasks []task.TaskRecord) map[string]bool {
	ownTwins := collectTwinIntentKeysForSub(activeSubKey, tasks)
	if len(ownTwins) == 0 {
		return nil
	}
	specs := make(map[string]bool)
	for _, twin := range ownTwins {
		for sp := range listTwinCommitSpecFiles(trunkRoot, twin) {
			specs[sp] = true
		}
	}
	return specs
}

func isSiblingSubPendingForWorktree(trunkRoot, scanRoot string, rec task.TaskRecord) bool {
	if rec.Status != task.StatusDone && rec.Status != task.StatusAborted {
		return true
	}
	if rec.Status == task.StatusDone && strings.Contains(filepath.ToSlash(scanRoot), "/worktrees/") {
		return !worktreeContainsTaskCommit(trunkRoot, scanRoot, rec.Key)
	}
	return false
}

func worktreeContainsTaskCommit(trunkRoot, scanRoot, subKey string) bool {
	sha := findTaskCommitSHA(trunkRoot, subKey)
	if sha == "" {
		return true
	}
	return workspace.SafeGitCommand(scanRoot, "merge-base", "--is-ancestor", sha, constants.ValHead).Run() == nil
}

func listTwinCommitSpecFiles(repoRoot, twinKey string) map[string]bool {
	out, err := workspace.SafeGitCommand(repoRoot, "log", "--fixed-strings", "--grep=["+twinKey+"]", "-n", "1", "--name-only", "--format=").Output()
	if err != nil {
		return nil
	}
	specs := make(map[string]bool)
	for _, line := range strings.Split(string(out), "\n") {
		clean := filepath.ToSlash(strings.TrimSpace(line))
		if clean != "" && strings.HasSuffix(clean, ".md") {
			specs[clean] = true
		}
	}
	return specs
}

// FilterDeferredSpecRoots removes specRoots entries owned exclusively by pending sibling twins.
// @intent nomos_intent_traceability_spec.md #ContractsConfig
func FilterDeferredSpecRoots(scanRoot string, specRoots []string, deferred map[string]bool) ([]string, bool) {
	if len(deferred) == 0 {
		return specRoots, false
	}
	allFiles := resolveObligationSpecFiles(scanRoot, specRoots)
	kept := make([]string, 0, len(allFiles))
	skippedAny := false
	for _, f := range allFiles {
		if deferred[toRelSlash(scanRoot, f)] {
			skippedAny = true
			continue
		}
		kept = append(kept, f)
	}
	if !skippedAny {
		return specRoots, false
	}
	return kept, true
}
