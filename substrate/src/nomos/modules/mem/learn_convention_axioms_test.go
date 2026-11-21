package mem

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// @verifies TC-LRN-04
func TestConsolidateConventionAxioms_ScrubsTitlesPathsRetiresHabitsAndDeadEnds(t *testing.T) {
	ws := t.TempDir()
	repo := filepath.Join(ws, "nomos")
	for _, dir := range []string{filepath.Join(repo, ".git"), filepath.Join(repo, "substrate", "cli"), filepath.Join(repo, "packaging", "npm")} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	liveCLI := filepath.Join(repo, "substrate", "cli", "cmd_init.go")
	if err := os.WriteFile(liveCLI, []byte("package cli\n"), 0644); err != nil {
		t.Fatalf("write liveCLI: %v", err)
	}
	t.Setenv("NOMOS_MNEME_DB", filepath.Join(t.TempDir(), "mneme.db"))

	store, err := NewStore("")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer func() {
		if cErr := store.Close(); cErr != nil {
			t.Errorf("Close: %v", cErr)
		}
	}()

	truncAx, stackAx, habitClone, deadDeleted, deadCooled := seedConventionAndDeadEndFixtures(t, store)
	sup := ConsolidateConventionAxioms(store)
	if sup < 2 {
		t.Fatalf("expected >=2 records superseded (duplicate axiom + habit clone), got %d", sup)
	}
	assertConventionAxiomScrubAndHabitRetirement(t, store, truncAx.ID, stackAx.ID, habitClone.ID)

	rep := SweepRepoPitfallHygiene(store, repo, "nomos", time.Now().UTC().Add(10*24*time.Hour))
	if rep.DeadScopesTombstoned != 1 {
		t.Fatalf("expected 1 deleted-file dead_end tombstoned, got %d", rep.DeadScopesTombstoned)
	}
	assertDeadEndStatuses(t, store, deadDeleted.ID, deadCooled.ID)
	assertSameScopeDeadEndWinningFix(t, repo)
}

func seedConventionAndDeadEndFixtures(t *testing.T, store *Store) (*Memory, *Memory, *Memory, *Memory, *Memory) {
	t.Helper()
	truncAx := &Memory{
		RepoID: "nomos", Kind: KindAxiom, Topic: "axiom:broken-wire-&-link-invariant-(la",
		Content: "Older truncated broken-wire axiom", TaskID: "INT-1", Status: StatusActive,
	}
	newerBroken := &Memory{
		RepoID: "nomos", Kind: KindAxiom, Topic: "axiom:broken-wire-&-link-invariant-(law-4)",
		Content: "[Autonomic Convention Axiom · broken wire & link invariant (la (3x across 3 scopes)] Trap: broken ref -> Verified Fix (SUB-2): fixed",
		TaskID:  "SUB-2", Status: StatusActive,
	}
	stackAx := &Memory{
		RepoID: "nomos", Kind: KindAxiom, Topic: "axiom:go-toolchain-gate",
		Content: "[Autonomic Convention Axiom · go toolchain gate (9x across 6 scopes)] Trap: /opt/ws/nomos/worktrees/nomos-SUB-3223/substrate/cli/gates.go:16 +0x7c -> Verified Fix (SUB-3223): fixed",
		TaskID:  "SUB-3223", Status: StatusActive,
	}
	habitClone := &Memory{
		RepoID: "nomos", Kind: KindPattern, Topic: "pattern:habit:go_toolchain_gate",
		Content: "[Repo-Wide Habit (go toolchain gate across 3 dirs)] Trap: fail", TaskID: "SUB-9", Status: StatusActive,
	}
	deadDeleted := &Memory{
		RepoID: "nomos", Kind: KindDeadEnd, Topic: "dead_end:substrate/cli",
		Content: "[Dead-End in substrate/cli] Rolled back un-merged edit to substrate/cli/gen2_deleted.go during SUB-10 -> Winning Fix (cmd_init.go): render",
		TaskID:  "SUB-10", Status: StatusActive,
	}
	deadCooled := &Memory{
		RepoID: "nomos", Kind: KindDeadEnd, Topic: "dead_end:substrate/cli",
		Content: "[Dead-End in substrate/cli] Rolled back un-merged edit to substrate/cli/cmd_init.go during SUB-11 -> Winning Fix (cmd_init.go): render",
		TaskID:  "SUB-11", AccessCount: 12, Status: StatusActive,
	}
	for _, rec := range []*Memory{truncAx, newerBroken, stackAx, habitClone, deadDeleted, deadCooled} {
		if err := store.Save(rec); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	if _, err := store.db.Exec(`UPDATE memories SET access_count = 12 WHERE id = ?`, deadCooled.ID); err != nil {
		t.Fatalf("set access_count: %v", err)
	}
	return truncAx, stackAx, habitClone, deadDeleted, deadCooled
}

func assertConventionAxiomScrubAndHabitRetirement(t *testing.T, store *Store, truncID, stackID, habitID int) {
	t.Helper()
	if NormalizeConventionGateSlug("unchecked_error_hygiene_invarian") != "unchecked-error-hygiene-invariant" {
		t.Fatalf("unexpected slug normalization: %s", NormalizeConventionGateSlug("unchecked_error_hygiene_invarian"))
	}
	truncAfter := mustGetMemoryForTest(t, store, truncID)
	if truncAfter.Status != StatusSuperseded {
		t.Fatalf("expected truncated duplicate axiom superseded, got %+v", truncAfter)
	}
	stackAfter := mustGetMemoryForTest(t, store, stackID)
	if strings.Contains(stackAfter.Content, "worktrees/") || strings.Contains(stackAfter.Content, "+0x7c") || !strings.Contains(stackAfter.Content, "Go Toolchain Gate") {
		t.Fatalf("expected stack trace and worktree path scrubbed on axiom, got: %s", stackAfter.Content)
	}
	habitAfter := mustGetMemoryForTest(t, store, habitID)
	if habitAfter.Status != StatusSuperseded || habitAfter.SupersededBy != stackID {
		t.Fatalf("expected habit clone superseded by canonical axiom #%d, got %+v", stackID, habitAfter)
	}
}

func assertDeadEndStatuses(t *testing.T, store *Store, deletedID, cooledID int) {
	t.Helper()
	delGot := mustGetMemoryForTest(t, store, deletedID)
	if delGot.Status != StatusTombstoned {
		t.Fatalf("expected deleted-file dead_end tombstoned, got %+v", delGot)
	}
	coolGot := mustGetMemoryForTest(t, store, cooledID)
	if coolGot.Status != StatusSuperseded {
		t.Fatalf("expected >7d cooled dead_end superseded, got %+v", coolGot)
	}
}

func mustGetMemoryForTest(t *testing.T, store *Store, id int) *Memory {
	t.Helper()
	rec, err := store.Get(id)
	if err != nil {
		t.Fatalf("Get(%d): %v", id, err)
	}
	return rec
}

func assertSameScopeDeadEndWinningFix(t *testing.T, repo string) {
	t.Helper()
	peer := filepath.Join(repo, "substrate", "cli", "existing_peer.go")
	if err := os.WriteFile(peer, []byte("package cli\n"), 0644); err != nil {
		t.Fatalf("write peer: %v", err)
	}
	n, err := RecordRolledBackDeadEnds(
		repo,
		"SUB-99",
		[]string{"substrate/cli/existing_peer.go"},
		[]string{"packaging/npm/package.json", "substrate/cli/cmd_init.go"},
		"Refactored CLI init command",
	)
	if err != nil || n != 1 {
		t.Fatalf("RecordRolledBackDeadEnds: n=%d err=%v", n, err)
	}
	scoped := RecallRepoPitfallsReadOnly(repo, []string{"substrate/cli"}, 5)
	found := false
	for _, line := range scoped {
		if strings.Contains(line, "Winning Fix (cmd_init.go)") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected same-directory Winning Fix (cmd_init.go), got %v", scoped)
	}
}
