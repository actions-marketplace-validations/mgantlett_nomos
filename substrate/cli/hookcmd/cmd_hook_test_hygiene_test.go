package hookcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
)

// @verifies TC-BG-10
func TestZeroBaselineContainerScratchTestHygieneAdvisory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.py"), []byte("def run():\n    return 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	testsDir := filepath.Join(dir, "tests")
	if err := os.MkdirAll(testsDir, 0755); err != nil {
		t.Fatal(err)
	}
	testFile := filepath.Join(testsDir, "test_app.py")
	if err := os.WriteFile(testFile, []byte("def test_run():\n    assert True\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if uninitMsg := EvaluateContainerPostToolHook(&HookPayload{ToolName: "Write", Cwd: dir, TargetFile: testFile}); uninitMsg != "" {
		t.Fatalf("expected byte-for-byte silence without nomos.yaml, got %q", uninitMsg)
	}
	if err := os.WriteFile(filepath.Join(dir, "nomos.yaml"), []byte("version: \"1\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	msg := EvaluateContainerPostToolHook(&HookPayload{ToolName: "Write", Cwd: dir, TargetFile: testFile})
	if !strings.Contains(msg, "Nomos Deliverable Hygiene") || !strings.Contains(msg, ".tmp/tests/") {
		t.Fatalf("expected non-blocking deliverable hygiene advisory steering to .tmp/tests/, got %q", msg)
	}

	secondFile := filepath.Join(testsDir, "test_more.py")
	if err := os.WriteFile(secondFile, []byte("def test_more():\n    assert True\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if secondMsg := EvaluateContainerPostToolHook(&HookPayload{ToolName: "Write", Cwd: dir, TargetFile: secondFile}); secondMsg != "" {
		t.Fatalf("expected one-time hygiene advisory deduplication, got %q", secondMsg)
	}

	brownDir := t.TempDir()
	if err := workspace.SafeGitCommand(brownDir, "init", "-b", "main").Run(); err != nil {
		t.Fatal(err)
	}
	_ = workspace.SafeGitCommand(brownDir, "config", "user.email", "test@nomos.dev").Run()
	_ = workspace.SafeGitCommand(brownDir, "config", "user.name", "Nomos").Run()
	if err := os.MkdirAll(filepath.Join(brownDir, "tests"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(brownDir, "nomos.yaml"), []byte("version: \"1\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	baseTest := filepath.Join(brownDir, "tests", "test_base.py")
	if err := os.WriteFile(baseTest, []byte("def test_base(): assert True\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_ = workspace.SafeGitCommand(brownDir, "add", ".").Run()
	_ = workspace.SafeGitCommand(brownDir, "commit", "-m", "baseline tests").Run()

	netNew := filepath.Join(brownDir, "tests", "test_extra.py")
	if err := os.WriteFile(netNew, []byte("def test_extra(): assert True\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if bfMsg := EvaluateContainerPostToolHook(&HookPayload{ToolName: "Write", Cwd: brownDir, TargetFile: netNew}); bfMsg != "" {
		t.Fatalf("expected silence when repo already has committed baseline tests, got %q", bfMsg)
	}
}
