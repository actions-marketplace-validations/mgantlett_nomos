package hookcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/cli/hookcmd/shellguard"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/task"
)

// @verifies TC-TSK-23
// @verifies TC-TSK-24
func TestEvaluatePreToolHook_WorktreeOracleLockAndHardScopeFence(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("NOMOS_HOME", homeDir)

	repo := t.TempDir()
	wtDir := filepath.Join(repo, "worktrees", "demo-SUB-77")
	if err := os.MkdirAll(filepath.Join(wtDir, "src", "auth"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(wtDir, "src", "ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(wtDir, "tests", "oracle"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeHookFixture(t, filepath.Join(wtDir, "nomos.yaml"), "project: demo\ndefault_branch: main\ngovernance:\n  quality_gates:\n    immutable_test_paths:\n      - tests/oracle/**\n")
	writeHookFixture(t, filepath.Join(wtDir, "src", "auth", "login.ts"), "export const ok = true;\n")
	writeHookFixture(t, filepath.Join(wtDir, "src", "auth", "login.test.ts"), "test('login', () => {});\n")
	writeHookFixture(t, filepath.Join(wtDir, "src", "ui", "nav.ts"), "export const nav = true;\n")
	writeHookFixture(t, filepath.Join(wtDir, "tests", "oracle", "spec.test.ts"), "test('oracle', () => {});\n")

	for _, args := range [][]string{{"init"}, {"config", "user.email", "t@e.com"}, {"config", "user.name", "T"}, {"add", "."}, {"commit", "-m", "init"}} {
		if out, err := workspace.SafeGitCommand(wtDir, args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, string(out))
		}
	}

	store, err := task.NewDefaultSQLiteTaskStore()
	if err != nil {
		t.Fatalf("open task store: %v", err)
	}
	defer func() { _ = store.Close() }()
	if cErr := store.CreateTask(task.TaskRecord{
		Key: "SUB-77", Plane: "substrate", SchemaType: task.SchemaSubstrate,
		Title: "Auth fix with locked tests", Status: task.StatusEdit,
		ScopePaths: []string{"src/auth/login.ts"}, LockTests: true,
	}); cErr != nil {
		t.Fatalf("create task: %v", cErr)
	}

	// 1. Committed existing test file blocked by --lock-tests
	dec1 := EvaluatePreToolHook(&shellguard.HookPayload{ToolName: "Edit", Cwd: wtDir, TargetFile: filepath.Join(wtDir, "src", "auth", "login.test.ts")}, "")
	if dec1.Decision != hookDecisionDeny || !strings.Contains(dec1.Reason, "Held-Out Verification Oracle Violation") {
		t.Fatalf("expected --lock-tests deny on committed test, got %+v", dec1)
	}
	// 2. Out-of-scope file blocked by hard --scope fence
	dec2 := EvaluatePreToolHook(&shellguard.HookPayload{ToolName: "Edit", Cwd: wtDir, TargetFile: filepath.Join(wtDir, "src", "ui", "nav.ts")}, "")
	if dec2.Decision != hookDecisionDeny || !strings.Contains(dec2.Reason, "Hard Scope Fence Violation") {
		t.Fatalf("expected hard scope fence deny on out-of-scope file, got %+v", dec2)
	}
	// 3. immutable_test_paths blocked
	dec3 := EvaluatePreToolHook(&shellguard.HookPayload{ToolName: "Edit", Cwd: wtDir, TargetFile: filepath.Join(wtDir, "tests", "oracle", "spec.test.ts")}, "")
	if dec3.Decision != hookDecisionDeny || !strings.Contains(dec3.Reason, "Immutable Verification Oracle Violation") {
		t.Fatalf("expected immutable_test_paths deny, got %+v", dec3)
	}
	// 4. In-scope production file allowed
	dec4 := EvaluatePreToolHook(&shellguard.HookPayload{ToolName: "Edit", Cwd: wtDir, TargetFile: filepath.Join(wtDir, "src", "auth", "login.ts")}, "")
	if dec4.Decision != hookDecisionAllow {
		t.Fatalf("expected in-scope production file allow, got %+v", dec4)
	}
}

func writeHookFixture(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}
