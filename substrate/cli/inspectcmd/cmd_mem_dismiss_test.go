package inspectcmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/config"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/mem"
)

// @verifies TC-MEM-19
func TestMemDismissCmd_StaleSweepAndValidation(t *testing.T) {
	t.Setenv("NOMOS_HOME", t.TempDir())

	repo := filepath.Join(t.TempDir(), "nomos")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "substrate", "cli"), 0755); err != nil {
		t.Fatalf("mkdir cli: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "substrate", "cli", "root.go"), []byte("package cli\n"), 0644); err != nil {
		t.Fatalf("write root.go: %v", err)
	}

	store, err := mem.NewStore(config.ResolveMnemeDBPath())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	staleRec := &mem.Memory{
		RepoID:  "nomos",
		Kind:    mem.KindPattern,
		Topic:   "pattern:substrate/conduit",
		Content: "[Pattern in substrate/conduit (SUB-1)] files=[conduit.go] -> Verified Summary: retired",
		TaskID:  "SUB-1",
		Status:  mem.StatusActive,
	}
	if err := store.Save(staleRec); err != nil {
		t.Fatalf("Save staleRec: %v", err)
	}
	if cErr := store.Close(); cErr != nil {
		t.Fatalf("Close: %v", cErr)
	}

	t.Run("rejects empty invocation without IDs or --stale", func(t *testing.T) {
		var buf bytes.Buffer
		cmd := newMemDismissCmd()
		cmd.SetOut(&buf)
		cmd.SetArgs([]string{})
		if err := cmd.Execute(); err == nil {
			t.Fatalf("expected validation error when neither ID nor --stale is passed")
		}
	})

	t.Run("rejects combining explicit IDs with --stale", func(t *testing.T) {
		var buf bytes.Buffer
		cmd := newMemDismissCmd()
		cmd.SetOut(&buf)
		cmd.SetArgs([]string{"--stale", "1"})
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "cannot combine") {
			t.Fatalf("expected mutual-exclusion error, got: %v", err)
		}
	})

	t.Run("sweeps stale pattern with --stale --json and reports clean on repeat", func(t *testing.T) {
		var buf bytes.Buffer
		cmd := newMemDismissCmd()
		cmd.SetOut(&buf)
		cmd.SetArgs([]string{"--stale", "-p", repo, "--json"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute --stale --json: %v", err)
		}
		var dismissed []*mem.Memory
		if uErr := json.Unmarshal(buf.Bytes(), &dismissed); uErr != nil {
			t.Fatalf("unmarshal JSON (%q): %v", buf.String(), uErr)
		}
		if len(dismissed) != 1 || dismissed[0].ID != staleRec.ID || dismissed[0].Status != mem.StatusTombstoned {
			t.Fatalf("unexpected dismissed payload: %+v", dismissed)
		}

		var secondBuf bytes.Buffer
		cmd2 := newMemDismissCmd()
		cmd2.SetOut(&secondBuf)
		cmd2.SetArgs([]string{"--stale", "-p", repo})
		if err := cmd2.Execute(); err != nil {
			t.Fatalf("Execute second --stale: %v", err)
		}
		if !strings.Contains(secondBuf.String(), "Zero stale") {
			t.Fatalf("expected clean zero-stale message on repeat, got: %s", secondBuf.String())
		}
	})
}
