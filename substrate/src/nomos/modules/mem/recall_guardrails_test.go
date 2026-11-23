package mem

import (
	"path/filepath"
	"testing"
	"time"
)

func openGuardrailTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(filepath.Join(t.TempDir(), "mneme_guardrails.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() {
		if cErr := store.Close(); cErr != nil {
			t.Logf("close store: %v", cErr)
		}
	})
	return store
}

// @verifies TC-MEM-20
func TestSearchWithOptions_AttachesLineageAndTopicDeadEndGuardrails(t *testing.T) {
	store := openGuardrailTestStore(t)

	activeDec := &Memory{
		RepoID:  "nomos",
		Kind:    KindDecision,
		Topic:   "WorktreeCache",
		Content: "Hash unstaged worktree index via temporary GIT_INDEX_FILE before reusing receipts.",
	}
	if err := store.Save(activeDec); err != nil {
		t.Fatalf("save activeDec: %v", err)
	}
	oldEvolved := &Memory{
		RepoID:       "nomos",
		Kind:         KindDecision,
		Topic:        "WorktreeCache",
		Content:      "Cache verify receipts by git HEAD commit SHA alone without unstaged index hashing.",
		Status:       StatusSuperseded,
		SupersededBy: activeDec.ID,
	}
	dupClone := &Memory{
		RepoID:       "nomos",
		Kind:         KindDecision,
		Topic:        "WorktreeCache",
		Content:      "  hash unstaged worktree index via temporary git_index_file before reusing receipts. ",
		Status:       StatusSuperseded,
		SupersededBy: activeDec.ID,
	}
	topicDeadEnd := &Memory{
		RepoID:  "nomos",
		Kind:    KindDeadEnd,
		Topic:   "WorktreeCache",
		Content: "Mutating the primary .git/index during read-only verify corrupts staged user hunks.",
	}
	for _, rec := range []*Memory{oldEvolved, dupClone, topicDeadEnd} {
		if err := store.Save(rec); err != nil {
			t.Fatalf("save fixture %+v: %v", rec, err)
		}
	}

	results, err := store.SearchWithOptions("worktree index cache receipts", SearchOptions{
		Limit:    5,
		Kind:     KindDecision,
		RepoID:   "nomos",
		ReadOnly: true,
	})
	if err != nil || len(results) != 1 {
		t.Fatalf("expected 1 decision result, got %d (err=%v)", len(results), err)
	}
	assertGuardrailRelations(t, results[0].Guardrails, oldEvolved.ID, topicDeadEnd.ID)
}

func assertGuardrailRelations(t *testing.T, got []RecallGuardrail, wantLineageID, wantDeadEndID int) {
	t.Helper()
	if len(got) != 2 {
		t.Fatalf("expected 2 guardrails (skipping duplicate clone), got %+v", got)
	}
	if got[0].Relation != relationSupersededLineage || got[0].MemoryID != wantLineageID {
		t.Fatalf("expected superseded_lineage #%d first, got %+v", wantLineageID, got[0])
	}
	if got[1].Relation != relationTopicDeadEnd || got[1].MemoryID != wantDeadEndID {
		t.Fatalf("expected topic_dead_end #%d second, got %+v", wantDeadEndID, got[1])
	}
}

// @verifies TC-MEM-21
func TestMaintain_PreservesEvolvedLineageAndPurgesClonesAndTombstones(t *testing.T) {
	store := openGuardrailTestStore(t)
	oldStamp := time.Now().UTC().Add(-60 * 24 * time.Hour)

	activeAxiom := &Memory{
		RepoID:  "nomos",
		Kind:    KindAxiom,
		Topic:   "SQLite",
		Content: "Encapsulate all SQLite mutations behind pure-Go modernc.org/sqlite stores.",
	}
	if err := store.Save(activeAxiom); err != nil {
		t.Fatalf("save activeAxiom: %v", err)
	}
	evolvedAncestor := &Memory{
		RepoID:       "nomos",
		Kind:         KindAxiom,
		Topic:        "SQLite",
		Content:      "Shell out to sqlite3 CLI binary for ad-hoc ledger inspection.",
		CreatedAt:    oldStamp,
		Status:       StatusSuperseded,
		SupersededBy: activeAxiom.ID,
	}
	expiredClone := &Memory{
		RepoID:       "nomos",
		Kind:         KindAxiom,
		Topic:        "SQLite",
		Content:      "encapsulate all sqlite mutations behind pure-go modernc.org/sqlite stores.",
		CreatedAt:    oldStamp,
		Status:       StatusSuperseded,
		SupersededBy: activeAxiom.ID,
	}
	expiredTombstone := &Memory{
		RepoID:    "nomos",
		Kind:      KindReflection,
		Topic:     "Scratch",
		Content:   "Dismissed scratch note older than retention window.",
		CreatedAt: oldStamp,
		Status:    StatusTombstoned,
	}
	for _, rec := range []*Memory{evolvedAncestor, expiredClone, expiredTombstone} {
		if err := store.Save(rec); err != nil {
			t.Fatalf("save fixture: %v", err)
		}
	}

	rep, err := store.Maintain(MaintainOptions{InactiveRetentionDays: 30})
	if err != nil || rep.InactivePurged != 2 {
		t.Fatalf("expected 2 purged (clone + tombstone) and evolved ancestor kept, got %+v (err=%v)", rep, err)
	}
	if kept, gErr := store.Get(evolvedAncestor.ID); gErr != nil || kept == nil {
		t.Fatalf("expected evolved ancestor #%d preserved, err=%v", evolvedAncestor.ID, gErr)
	}
}
