package mem

import (
	"path/filepath"
	"testing"
)

func TestSearch_ReadOnlyPreservesZeroAccessCount(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "mneme_search.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	rec := &Memory{
		RepoID:  "nomos",
		Kind:    KindArchitecture,
		Topic:   "Worktrees",
		Content: "Isolate every repository mutation inside a hermetic task worktree.",
	}
	if err := store.Save(rec); err != nil {
		t.Fatalf("Save: %v", err)
	}
	res, err := store.SearchWithOptions("hermetic task worktree", SearchOptions{
		Limit:    3,
		RepoID:   "nomos",
		ReadOnly: true,
	})
	if err != nil || len(res) != 1 {
		t.Fatalf("expected 1 match, got %d (err=%v)", len(res), err)
	}
	after, err := store.Get(rec.ID)
	if err != nil || after.AccessCount != 0 {
		t.Fatalf("expected ReadOnly search to keep access_count=0, got %+v (err=%v)", after, err)
	}
}
