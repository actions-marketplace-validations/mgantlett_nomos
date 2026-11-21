package mem

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// @verifies TC-LRN-03
func TestOpenCoreHealedLocDeletedTrapFileAndBlueprintPriority(t *testing.T) {
	t.Setenv("NOMOS_HOME", t.TempDir())
	repo := filepath.Join(t.TempDir(), "nomos")
	cliDir := filepath.Join(repo, "substrate", "cli")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.MkdirAll(cliDir, 0755); err != nil {
		t.Fatalf("mkdir cli: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cliDir, "shrunk.go"), []byte(strings.Repeat("// ok\n", 250)), 0644); err != nil {
		t.Fatalf("write shrunk.go: %v", err)
	}

	store, err := NewStore("")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer func() {
		if cErr := store.Close(); cErr != nil {
			t.Errorf("Close: %v", cErr)
		}
	}()

	healedLoc := &Memory{
		RepoID: "nomos", Kind: KindPitfall, Topic: "pitfall:max_file_loc_invariant:substrate/cli",
		Content: "[Max File LOC Invariant in substrate/cli] Trap: substrate/cli/shrunk.go has 440 LOC (exceeds max 400) -> Verified Fix (SUB-1): split",
		TaskID:  "SUB-1", Status: StatusActive,
	}
	deletedTrap := &Memory{
		RepoID: "nomos", Kind: KindPitfall, Topic: "pitfall:forbidden_literals_invariant:substrate/cli",
		Content: "[Forbidden Literals Invariant in substrate/cli] Trap: substrate/cli/deleted_old.go:42 literal -> Verified Fix (SUB-2): removed",
		TaskID:  "SUB-2", Status: StatusActive,
	}
	astHabit := &Memory{
		RepoID: "nomos", Kind: KindPattern, Topic: "pattern:habit:max_file_loc_invariant",
		Content: "[Repo-Wide Habit (max file loc invariant)] Trap: old clone", TaskID: "SUB-3", Status: StatusActive,
	}
	blueprint := &Memory{
		RepoID: "nomos", Kind: KindPattern, Topic: "pattern:substrate/cli",
		Content: "[Pattern in substrate/cli (SUB-4)] files=[shrunk.go] -> Verified Summary: clean CLI helper",
		TaskID:  "SUB-4", Status: StatusActive,
	}
	for _, rec := range []*Memory{healedLoc, deletedTrap, astHabit, blueprint} {
		if sErr := store.Save(rec); sErr != nil {
			t.Fatalf("Save: %v", sErr)
		}
	}

	if recalled := RecallRepoPitfallsReadOnly(repo, nil, 5); len(recalled) != 0 {
		t.Fatalf("expected read-time suppression of healed LOC and absent Trap file pitfalls, got %v", recalled)
	}
	rep := SweepRepoPitfallHygiene(store, repo, "nomos", time.Now().UTC())
	if rep.DeadScopesTombstoned != 2 || rep.DeadPatternsTombstoned != 1 {
		t.Fatalf("expected 2 healed/deleted pitfalls and 1 AST habit tombstoned, got %+v", rep)
	}
	pats := RecallRepoPatternsReadOnly(repo, "", nil, 3)
	if len(pats) != 1 || !strings.Contains(pats[0], "files=[shrunk.go]") {
		t.Fatalf("expected positive structural blueprint in VerifiedPatterns, got %v", pats)
	}
}
