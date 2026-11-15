package mem

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// @verifies TC-LRN-03
func TestDualGatePatternAndPitfallFilesystemGrounding(t *testing.T) {
	t.Setenv("NOMOS_HOME", t.TempDir())

	repo := filepath.Join(t.TempDir(), "nomos")
	liveDir := filepath.Join(repo, "substrate", "cli")
	for _, dir := range []string{filepath.Join(repo, ".git"), liveDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	liveFile := filepath.Join(liveDir, "root.go")
	deletedFile := filepath.Join(liveDir, "conduit_old.go")
	for _, f := range []string{liveFile, deletedFile} {
		if err := os.WriteFile(f, []byte("package cli\n"), 0644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
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

	save := func(kind MemoryKind, topic, content string) int {
		rec := &Memory{
			RepoID:  "nomos",
			Kind:    kind,
			Topic:   topic,
			Content: content,
			TaskID:  "SUB-3099",
			Status:  StatusActive,
		}
		if sErr := store.Save(rec); sErr != nil {
			t.Fatalf("Save(%s): %v", topic, sErr)
		}
		return rec.ID
	}

	deadDirPatID := save(KindPattern, "pattern:intent/contracts/cue", "[Pattern in intent/contracts/cue (INT-891)] files=[ncte.cue] -> Verified Summary: legacy CUE")
	deletedFilePatID := save(KindPattern, "pattern:substrate/cli", "[Pattern in substrate/cli (SUB-50)] files=[conduit_old.go] -> Verified Summary: old conduit CLI")
	livePatID := save(KindPattern, "pattern:substrate/cli", "[Pattern in substrate/cli (SUB-51)] files=[root.go] -> Verified Summary: live CLI root")
	deadPitfallID := save(KindPitfall, "pitfall:max_file_loc:substrate/conduit", "[Max File LOC in substrate/conduit] Trap: 412 LOC -> Verified Fix: split")

	// Remove conduit_old.go to simulate file deletion inside a surviving directory.
	if rErr := os.Remove(deletedFile); rErr != nil {
		t.Fatalf("remove %s: %v", deletedFile, rErr)
	}

	// Gate 1 (Read-time os.Stat suppression): before SweepStaleRepoMemories runs, deleted scopes/files must not surface.
	recalledPatterns := RecallRepoPatternsReadOnly(repo, "cue conduit cli", nil, 10)
	for _, line := range recalledPatterns {
		if strings.Contains(line, "ncte.cue") || strings.Contains(line, "conduit_old.go") {
			t.Fatalf("expected read-time os.Stat suppression for dead pattern, got: %s", line)
		}
	}
	if len(recalledPatterns) != 1 || !strings.Contains(recalledPatterns[0], "root.go") {
		t.Fatalf("expected live root.go pattern to remain recallable, got: %v", recalledPatterns)
	}

	recalledPitfalls := RecallRepoPitfallsReadOnly(repo, nil, 10)
	for _, line := range recalledPitfalls {
		if strings.Contains(line, "substrate/conduit") {
			t.Fatalf("expected read-time os.Stat suppression for dead pitfall scope, got: %s", line)
		}
	}

	// Gate 2 (Write/sweep tombstoning): SweepStaleRepoMemories must tombstone all 3 stale entries and return receipts.
	dismissed, rep := SweepStaleRepoMemories(store, repo, "nomos", time.Now().UTC())
	if rep.DeadPatternsTombstoned != 2 {
		t.Fatalf("expected 2 dead patterns tombstoned, got %d", rep.DeadPatternsTombstoned)
	}
	if rep.DeadScopesTombstoned != 1 {
		t.Fatalf("expected 1 dead pitfall scope tombstoned, got %d", rep.DeadScopesTombstoned)
	}
	if len(dismissed) != 3 {
		t.Fatalf("expected 3 dismissed memory receipts, got %d", len(dismissed))
	}

	for _, id := range []int{deadDirPatID, deletedFilePatID, deadPitfallID} {
		rec, gErr := store.Get(id)
		if gErr != nil || rec.Status != StatusTombstoned {
			t.Fatalf("expected #%d tombstoned, got %+v (err=%v)", id, rec, gErr)
		}
	}
	liveRec, lErr := store.Get(livePatID)
	if lErr != nil || liveRec.Status != StatusActive {
		t.Fatalf("expected live pattern #%d to stay active, got %+v (err=%v)", livePatID, liveRec, lErr)
	}
}
