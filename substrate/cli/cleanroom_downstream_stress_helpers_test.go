package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mgantlett/nomos/substrate/cli/commitmsg"
	"github.com/mgantlett/nomos/substrate/cli/tui"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/types"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/task"
)

func verifyTornWorktreeCrashSelfHeal(t *testing.T, repoDir, wt1 string) {
	t.Helper()
	_ = workspace.SafeGitCommand(wt1, "reset", "--hard", "main").Run()
	cleanLedger := "\"\"\"Ledger module.\"\"\"\n\ndef verify_balance(balance: int) -> bool:\n    \"\"\"Verify non-negative balance.\"\"\"\n    return balance >= 0\n"
	writeWorktreeFile(t, wt1, filepath.Join("substrate", "src", "ledger.py"), cleanLedger)
	_ = workspace.SafeGitCommand(wt1, "add", "-A").Run()
	opts := &commitmsg.CommitCmdOptions{Dir: wt1, Summary: "Add ledger balance verification", AutoStage: true}
	if err := runTaskCommitCmd([]string{"SUB-1"}, opts); err != nil {
		t.Fatalf("valid task commit failed: %v", err)
	}

	if err := os.RemoveAll(wt1); err != nil {
		t.Fatalf("removeAll wt1: %v", err)
	}

	startCmd := newTaskStartCmd()
	if err := startCmd.RunE(startCmd, []string{"SUB-1"}); err != nil {
		t.Fatalf("expected nomos task start SUB-1 to self-heal torn worktree lock, got: %v", err)
	}
	editCmd := newTaskEditCmd()
	if err := editCmd.RunE(editCmd, []string{"SUB-1"}); err != nil {
		t.Fatalf("task edit SUB-1 failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wt1, "substrate", "src", "ledger.py")); err != nil {
		t.Fatalf("expected committed ledger.py to be restored when torn worktree re-attached: %v", err)
	}
	syncCleanroomTask(t, repoDir, "SUB-1", "Recovered and synced ledger service")
}

func verifyCrossPlaneNoVerifyBlocked(t *testing.T, repoDir string) {
	t.Helper()
	wtInt := startCleanroomTaskInEdit(t, repoDir, "INT", "Architecture spec update")
	writeWorktreeFile(t, wtInt, filepath.Join("substrate", "src", "illegal.py"), "\"\"\"Illegal cross-plane file.\"\"\"\n\ndef x() -> int:\n    \"\"\"Return 1.\"\"\"\n    return 1\n")
	_ = workspace.SafeGitCommand(wtInt, "add", "-A").Run()
	_ = workspace.SafeGitCommand(wtInt, "commit", "--no-verify", "-m", "rogue cross-plane commit").Run()

	walk := writeWalkthroughTemp(t, repoDir, "INT-1", "Attempt cross-plane sync")
	syncCmd := newTaskSyncCmd()
	syncCmd.SetArgs([]string{"INT-1", "-F", walk})
	err := syncCmd.Execute()
	if err == nil {
		t.Fatalf("expected INT-1 modifying substrate/ to fail PlaneSeparationGate at sync (Law 7)")
	}
	if nErr, ok := err.(*types.NomosError); !ok || nErr.Code != types.ErrCodeCrossPlaneMutation {
		t.Fatalf("expected ErrCodeCrossPlaneMutation, got: %T (%v)", err, err)
	}
}

func seedCleanroomDualPlaneRepo(t *testing.T, repoDir string) {
	t.Helper()
	useCompiledNomosInPath(t)
	if err := os.MkdirAll(filepath.Join(repoDir, "substrate", "src"), 0755); err != nil {
		t.Fatalf("mkdir substrate/src: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repoDir, "intent", "specs"), 0755); err != nil {
		t.Fatalf("mkdir intent/specs: %v", err)
	}
	_ = workspace.SafeGitCommand(repoDir, "init", "-b", "main").Run()
	_ = workspace.SafeGitCommand(repoDir, "config", "user.email", "dev@acme.io").Run()
	_ = workspace.SafeGitCommand(repoDir, "config", "user.name", "Acme Dev").Run()

	writeRawFile(t, filepath.Join(repoDir, "substrate", "src", "seed.py"), "\"\"\"Seed module.\"\"\"\n\ndef seed_ok() -> bool:\n    \"\"\"Seed status.\"\"\"\n    return True\n")
	writeRawFile(t, filepath.Join(repoDir, "intent", "specs", "overview.md"), "# Architecture Overview\n")
	_ = workspace.SafeGitCommand(repoDir, "add", ".").Run()
	_ = workspace.SafeGitCommand(repoDir, "commit", "-m", "Initial cleanroom baseline").Run()
}

func startCleanroomTaskInEdit(t *testing.T, repoDir, plane, title string) string {
	t.Helper()
	card := tui.TaskCardFlags{
		Scope:  "Cleanroom stress target for " + title + " in " + plane + " plane.",
		AC:     []string{"Complete cleanroom mutation and pass DoD gates"},
		Verify: "nomos verify",
	}
	if err := executeTaskCreate(title, taskCreateOpts{plane: plane, tier: "T1", priority: 50, card: card}); err != nil {
		t.Fatalf("executeTaskCreate(%s) failed: %v", title, err)
	}
	store, err := task.NewDefaultSQLiteTaskStore()
	if err != nil {
		t.Fatalf("open task store: %v", err)
	}
	tasks := store.ListTasks()
	_ = store.Close()
	var key string
	for _, rec := range tasks {
		if rec.Title == title {
			key = rec.Key
			break
		}
	}
	if key == "" {
		t.Fatalf("failed discovering created task key for %q", title)
	}
	startCmd := newTaskStartCmd()
	if err := startCmd.RunE(startCmd, []string{key}); err != nil {
		t.Fatalf("task start %s failed: %v", key, err)
	}
	editCmd := newTaskEditCmd()
	if err := editCmd.RunE(editCmd, []string{key}); err != nil {
		t.Fatalf("task edit %s failed: %v", key, err)
	}
	mgr, err := workspace.NewManager(repoDir)
	if err != nil {
		t.Fatalf("workspace.NewManager: %v", err)
	}
	return mgr.WorktreePath(key)
}

func syncCleanroomTask(t *testing.T, repoDir, key, summary string) {
	t.Helper()
	walk := writeWalkthroughTemp(t, repoDir, key, summary)
	syncCmd := newTaskSyncCmd()
	syncCmd.SetArgs([]string{key, "-F", walk})
	if err := syncCmd.Execute(); err != nil {
		t.Fatalf("nomos task sync %s failed: %v", key, err)
	}
}

func writeWalkthroughTemp(t *testing.T, repoDir, key, summary string) string {
	t.Helper()
	p := filepath.Join(repoDir, ".tmp", "walkthrough-"+key+".md")
	writeRawFile(t, p, "# Walkthrough "+key+"\n\n"+summary+"\n")
	return p
}

func writeWorktreeFile(t *testing.T, wtPath, relPath, content string) {
	t.Helper()
	writeRawFile(t, filepath.Join(wtPath, relPath), content)
}

func writeRawFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
