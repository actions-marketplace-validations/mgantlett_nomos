package inspectcmd_test

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/mgantlett/nomos/substrate/cli/inspectcmd"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/mem"
)

func TestMemSaveAndRecallJSON(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "mneme_cli.db")
	t.Setenv("NOMOS_MNEME_DB_PATH", dbPath)

	store, err := mem.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	active := &mem.Memory{RepoID: "nomos", Kind: mem.KindDecision, Topic: "Boundary", Content: "Validate worktree paths before writes."}
	if err := store.Save(active); err != nil {
		t.Fatalf("Save active: %v", err)
	}
	deadEnd := &mem.Memory{RepoID: "nomos", Kind: mem.KindDeadEnd, Topic: "Boundary", Content: "Relative symlink checks miss parent escapes."}
	if err := store.Save(deadEnd); err != nil {
		t.Fatalf("Save deadEnd: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	cmd := inspectcmd.NewMemCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"recall", "worktree paths", "-p", "nomos"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("mem recall failed: %v", err)
	}
}
