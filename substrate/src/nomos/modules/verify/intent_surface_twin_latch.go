// Package verify implements sticky worktree anti-evasion latches and pre-commit Twin preflight diagnostics.
package verify

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/config"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/storage"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/task"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

type twinRequiredLatch struct {
	TaskKey        string `json:"task_key"`
	SurfaceSummary string `json:"surface_summary"`
}

// twinLatchFilePath resolves the hermetic .tmp latch path inside an active task worktree.
func twinLatchFilePath(worktreeDir, taskKey string) string {
	cleanDir := strings.TrimSpace(worktreeDir)
	cleanKey := strings.ToUpper(strings.TrimSpace(taskKey))
	if cleanDir == "" || cleanKey == "" {
		return ""
	}
	return filepath.Join(cleanDir, ".tmp", "twin_required_"+cleanKey+".json")
}

// recordTwinRequiredLatch persists a sticky normative-surface trigger so symbol hiding cannot evade Twin enforcement.
func recordTwinRequiredLatch(worktreeDir, taskKey, summary string) {
	path := twinLatchFilePath(worktreeDir, taskKey)
	if path == "" || strings.TrimSpace(summary) == "" {
		return
	}
	info, err := os.Stat(worktreeDir)
	if err != nil || !info.IsDir() {
		return
	}
	if mkErr := os.MkdirAll(filepath.Dir(path), 0755); mkErr != nil {
		return
	}
	data, mErr := json.Marshal(twinRequiredLatch{
		TaskKey:        strings.ToUpper(strings.TrimSpace(taskKey)),
		SurfaceSummary: strings.TrimSpace(summary),
	})
	if mErr != nil {
		return
	}
	if wErr := os.WriteFile(path, data, 0644); wErr != nil {
		return
	}
}

// readTwinRequiredLatch loads any prior untwinned normative-surface rejection summary recorded for taskKey.
func readTwinRequiredLatch(worktreeDir, taskKey string) string {
	path := twinLatchFilePath(worktreeDir, taskKey)
	if path == "" {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var payload twinRequiredLatch
	if uErr := json.Unmarshal(raw, &payload); uErr != nil {
		return ""
	}
	return strings.TrimSpace(payload.SurfaceSummary)
}

// clearTwinRequiredLatch removes the anti-evasion latch once valid Twin provenance has been verified.
func clearTwinRequiredLatch(worktreeDir, taskKey string) {
	path := twinLatchFilePath(worktreeDir, taskKey)
	if path == "" {
		return
	}
	if rErr := os.Remove(path); rErr != nil && !os.IsNotExist(rErr) {
		return
	}
}

// EvaluateWorktreeTwinPreflight inspects uncommitted SUB-* worktrees for untwinned normative surface changes (#NormativeSurfaceDelta).
// @intent nomos_hermetic_worktree_spec.md #NormativeSurfaceDelta
func EvaluateWorktreeTwinPreflight(worktreeDir, taskKey string) string {
	cleanDir := strings.TrimSpace(worktreeDir)
	if cleanDir == "" {
		return ""
	}
	cleanKey := resolveSubTaskKeyForPreflight(cleanDir, taskKey)
	if !strings.HasPrefix(cleanKey, "SUB-") {
		return ""
	}
	mode := loadEffectiveWorktreeTwinMode(cleanDir)
	rank, ok := config.IntentTwinModeRank(mode)
	if !ok || rank == 0 || hasBoundIntentTwinForSubTask(cleanDir, cleanKey) {
		return ""
	}
	delta := InspectWorktreeNormativeSurface(cleanDir)
	if delta.HasChanges() {
		summary := delta.Summary()
		recordTwinRequiredLatch(cleanDir, cleanKey, summary)
		return fmt.Sprintf(
			"uncommitted SUB worktree [%s] modifies normative contract surface (%s) without a bound 'Twin: INT-<N>' under governance.quality_gates.intent_twin_mode=%s (Law 4 & Law 7)",
			cleanKey, summary, mode,
		)
	}
	if latched := readTwinRequiredLatch(cleanDir, cleanKey); latched != "" {
		return fmt.Sprintf(
			"uncommitted SUB worktree [%s] previously triggered normative surface expansion (%s); hiding or unexporting symbols to evade 'Twin: INT-<N>' is prohibited (Law 1 & Law 4)",
			cleanKey, latched,
		)
	}
	if mode == config.IntentTwinModeStrict {
		return fmt.Sprintf("uncommitted SUB worktree [%s] requires a bound 'Twin: INT-<N>' under governance.quality_gates.intent_twin_mode=strict (Law 7)", cleanKey)
	}
	return ""
}

// resolveSubTaskKeyForPreflight derives the active SUB-* task key from explicit input or feature/SUB-<N> branch naming.
func resolveSubTaskKeyForPreflight(worktreeDir, taskKey string) string {
	if clean := strings.ToUpper(strings.TrimSpace(taskKey)); clean != "" {
		return clean
	}
	out, err := workspace.SafeGitCommand(worktreeDir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	branch := strings.TrimSpace(string(out))
	if idx := strings.LastIndex(branch, "/"); idx >= 0 {
		branch = branch[idx+1:]
	}
	upper := strings.ToUpper(branch)
	if canonicalTaskTrailerValRe.MatchString(upper) {
		return upper
	}
	return ""
}

// loadEffectiveWorktreeTwinMode loads governance.quality_gates.intent_twin_mode for worktreeDir or its parent repo root.
func loadEffectiveWorktreeTwinMode(worktreeDir string) string {
	root, err := workspace.FindRepoRoot(worktreeDir)
	if err != nil {
		root = ""
	}
	return resolveIntentTwinMode(&vscope.VerifyContext{RepoRoot: root, WorktreePath: worktreeDir})
}

// hasBoundIntentTwinForSubTask checks whether taskKey already has a bound INT-* twin in the Living Ledger or HEAD commit.
func hasBoundIntentTwinForSubTask(worktreeDir, taskKey string) bool {
	if twin := lookupLedgerTwinKey(taskKey); strings.HasPrefix(twin, "INT-") {
		return hasGitCommitForTaskKey(worktreeDir, twin) || hasLedgerTaskRecord(twin)
	}
	out, err := workspace.SafeGitCommand(worktreeDir, "log", "-1", "--format=%B").Output()
	if err != nil {
		return false
	}
	subj, _, twins := extractSubCommitTwinContext(string(out))
	return subj == taskKey && len(twins) > 0
}

// lookupLedgerTwinKey reads TaskRecord.TwinKey for taskKey from the global SQLite Living Ledger.
func lookupLedgerTwinKey(taskKey string) string {
	dbPath := config.ResolveGlobalDBPath()
	if strings.TrimSpace(dbPath) == "" {
		return ""
	}
	if _, err := os.Stat(dbPath); err != nil {
		return ""
	}
	db, err := storage.OpenDB(dbPath)
	if err != nil {
		return ""
	}
	defer func() {
		if cErr := db.Close(); cErr != nil {
			// connection closed
		}
	}()
	rec, gErr := task.NewSQLiteTaskStore(db).GetTask(strings.TrimSpace(taskKey))
	if gErr != nil || rec == nil {
		return ""
	}
	return strings.TrimSpace(rec.TwinKey)
}
