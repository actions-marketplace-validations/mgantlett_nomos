package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/cli/hookcmd"
)

func runInternalHook(t *testing.T, args []string, payload map[string]any) string {
	t.Helper()
	root := NewRootCmd()
	outBuf := new(bytes.Buffer)
	root.SetOut(outBuf)
	root.SetErr(new(bytes.Buffer))
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	root.SetIn(bytes.NewReader(raw))
	root.SetArgs(append([]string{"hook"}, args...))
	if err := root.Execute(); err != nil {
		t.Fatalf("hook %v failed: %v", args, err)
	}
	return strings.TrimSpace(outBuf.String())
}

func TestReceiptTraceParity_GracefulManualSyncAndContradictionGuards(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	repoDir := t.TempDir()
	wtDir := filepath.Join(repoDir, "worktrees", "demo-SUB-77")
	if err := os.MkdirAll(wtDir, 0755); err != nil {
		t.Fatalf("mkdir wtDir: %v", err)
	}
	walkPath := filepath.Join(repoDir, "walkthrough-SUB-77.md")
	writeWalk := func(body string) {
		t.Helper()
		if err := os.WriteFile(walkPath, []byte(body), 0644); err != nil {
			t.Fatalf("write walkthrough: %v", err)
		}
	}

	writeWalk("# Walkthrough SUB-77\n- Ran go test ./cli/... and verified output.\n")
	if err := validateReceiptTraceParity(wtDir, walkPath, []string{"substrate/cli/a.go"}); err != nil {
		t.Fatalf("TC-TSK-17: expected graceful pass when no hook trace exists, got %v", err)
	}

	writeWalk("# Walkthrough SUB-77\n- No files were modified during this audit.\n")
	if err := validateReceiptTraceParity(wtDir, walkPath, []string{"substrate/cli/a.go"}); err == nil {
		t.Fatalf("TC-TSK-17: expected fail-closed error on false zero-mutation claim")
	}

	writeWalk("# Walkthrough SUB-77\n- Only modified `substrate/cli/a.go` to tighten validation.\n")
	if err := validateReceiptTraceParity(wtDir, walkPath, []string{"substrate/cli/a.go", "substrate/cli/b.go"}); err == nil {
		t.Fatalf("TC-TSK-17: expected fail-closed error on false exclusive-scope claim")
	}

	writeWalk("# Walkthrough SUB-77\n## Changes\n- `substrate/cli/a.go`: updated runner.\n- `substrate/cli/phantom_unmodified.go`: refactored phantom module.\n")
	if err := validateReceiptTraceParity(wtDir, walkPath, []string{"substrate/cli/a.go", "substrate/cli/b.go"}); err == nil {
		t.Fatalf("TC-TSK-17: expected fail-closed error when ## Changes claims un-modified file")
	}

	targetA := filepath.Join(wtDir, "substrate", "cli", "a.go")
	_ = runInternalHook(t, []string{"pre-tool"}, map[string]any{
		"session_id":      "sess-trace-77",
		"hook_event_name": "PreToolUse",
		"cwd":             wtDir,
		"tool_name":       "Write",
		"tool_input":      map[string]any{"file_path": targetA},
	})
	_ = runInternalHook(t, []string{"pre-tool"}, map[string]any{
		"session_id":      "sess-trace-77",
		"hook_event_name": "PreToolUse",
		"cwd":             wtDir,
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "git status --short"},
	})

	writeWalk("# Walkthrough SUB-77\n- Updated a.go and b.go.\n- Ran pytest across unit suite.\n")
	if err := validateReceiptTraceParity(wtDir, walkPath, []string{"substrate/cli/a.go", "substrate/cli/b.go"}); err == nil {
		t.Fatalf("TC-TSK-17: expected fail-closed error on fabricated pytest claim with 0 recorded test commands")
	}

	_ = runInternalHook(t, []string{"pre-tool"}, map[string]any{
		"session_id":      "sess-trace-77",
		"hook_event_name": "PreToolUse",
		"cwd":             wtDir,
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "pytest -q"},
	})
	if err := validateReceiptTraceParity(wtDir, walkPath, []string{"substrate/cli/a.go", "substrate/cli/b.go"}); err != nil {
		t.Fatalf("TC-TSK-17: expected parity check to pass once pytest was recorded in hook trace, got %v", err)
	}

	hookcmd.ClearHookSessionTrace(wtDir)
	if tr := hookcmd.LoadHookSessionTrace(wtDir); tr != nil {
		t.Fatalf("expected ClearHookSessionTrace to remove worktree trace file")
	}
}

func TestReceiptTraceParity_ZeroDiffChangesSectionFailsClosed(t *testing.T) {
	wtDir := filepath.Join(t.TempDir(), "worktrees", "repo-SUB-502")
	if err := os.MkdirAll(wtDir, 0755); err != nil {
		t.Fatal(err)
	}
	walkPath := filepath.Join(wtDir, "walkthrough.md")
	body := "# Walkthrough\n\n## Changes\n- `substrate/cli/phantom.go`: Claimed edit with zero worktree diff.\n"
	if err := os.WriteFile(walkPath, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	err := validateReceiptTraceParity(wtDir, walkPath, nil)
	if err == nil || !strings.Contains(err.Error(), "phantom.go") {
		t.Fatalf("expected 0-diff task claiming ## Changes bullet to fail closed, got %v", err)
	}
}
