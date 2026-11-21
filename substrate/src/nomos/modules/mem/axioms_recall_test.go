package mem

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// @verifies TC-MEM-16
func TestRecallRepoAxioms_ReadOnlyAndPerTaskReinforcement(t *testing.T) {
	t.Setenv("NOMOS_HOME", t.TempDir())
	repo := filepath.Join(t.TempDir(), "nomos")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
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

	axSteel := &Memory{
		RepoID:  "nomos",
		Kind:    KindAxiom,
		Topic:   "axiom:intent-steel-man-parity-gate",
		Content: "[Autonomic Convention Axiom] Enforce NCTE-100 sentence bounds in intent/architecture/specs",
		TaskID:  "INT-2100",
		Status:  StatusActive,
	}
	axOtherRepo := &Memory{
		RepoID:  "other-repo",
		Kind:    KindAxiom,
		Topic:   "axiom:other",
		Content: "Other repository axiom must not leak",
		TaskID:  "SUB-1",
		Status:  StatusActive,
	}
	for _, rec := range []*Memory{axSteel, axOtherRepo} {
		if sErr := store.Save(rec); sErr != nil {
			t.Fatalf("Save(%s): %v", rec.Topic, sErr)
		}
	}

	ro := RecallRepoAxiomsReadOnly(repo, "intent specs NCTE", []string{"intent/architecture/specs/mneme_memory_spec.md"}, 2)
	if len(ro) != 1 || !strings.Contains(ro[0], "NCTE-100") {
		t.Fatalf("expected strictly 1 nomos axiom recalled, got %v", ro)
	}
	afterRO, gErr := store.Get(axSteel.ID)
	if gErr != nil || afterRO.AccessCount != 0 {
		t.Fatalf("expected ReadOnly recall to leave access_count=0, got %+v (err=%v)", afterRO, gErr)
	}

	_ = RecallRepoAxiomsForTask(repo, "SUB-3397", "intent specs", []string{"intent/architecture/specs/mneme_memory_spec.md"}, 2)
	_ = RecallRepoAxiomsForTask(repo, "SUB-3397", "intent specs", []string{"intent/architecture/specs/mneme_memory_spec.md"}, 2)
	afterTask, gErr2 := store.Get(axSteel.ID)
	if gErr2 != nil || afterTask.AccessCount != 1 {
		t.Fatalf("expected per-task deduplicated access_count=1, got %+v (err=%v)", afterTask, gErr2)
	}
}

// @verifies TC-MEM-16
func TestSweepStaleRepoMemories_RetiresAgedZeroRecallPatternOverflow(t *testing.T) {
	t.Setenv("NOMOS_HOME", t.TempDir())
	repo := filepath.Join(t.TempDir(), "nomos")
	cliDir := filepath.Join(repo, "substrate", "cli")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.MkdirAll(cliDir, 0755); err != nil {
		t.Fatalf("mkdir cli: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cliDir, "root.go"), []byte("package cli\n"), 0644); err != nil {
		t.Fatalf("write root.go: %v", err)
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

	now := time.Now().UTC()
	var ids []int
	for i := 1; i <= 5; i++ {
		rec := &Memory{
			RepoID:  "nomos",
			Kind:    KindPattern,
			Topic:   "pattern:substrate/cli",
			Content: fmt.Sprintf("[Pattern in substrate/cli (SUB-%d)] files=[root.go] -> Verified Summary: variant %d", i, i),
			TaskID:  fmt.Sprintf("SUB-%d", i),
			Status:  StatusActive,
		}
		if sErr := store.Save(rec); sErr != nil {
			t.Fatalf("Save(%d): %v", i, sErr)
		}
		ids = append(ids, rec.ID)
	}
	oldStamp := now.Add(-45 * 24 * time.Hour).Format(time.RFC3339)
	for _, id := range ids {
		if _, eErr := store.db.Exec(`UPDATE memories SET created_at = ? WHERE id = ?`, oldStamp, id); eErr != nil {
			t.Fatalf("backdate %d: %v", id, eErr)
		}
	}

	SweepRepoPitfallHygiene(store, repo, "nomos", now)
	oldest, oErr := store.Get(ids[0])
	newest, nErr := store.Get(ids[4])
	if oErr != nil || nErr != nil || oldest.Status != StatusSuperseded || newest.Status != StatusActive {
		t.Fatalf("expected oldest overflow pattern superseded and newest 3 active, got oldest=%+v newest=%+v", oldest, newest)
	}
}
