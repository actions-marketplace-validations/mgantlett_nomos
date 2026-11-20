package hookcmd

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mgantlett/nomos/substrate/cli/hookcmd/shellguard"
)

// TestLoopBreakerWorktreeEditsFromSubdirCwd verifies subdirectory Cwd invocations track target worktree edits accurately.
// @verifies TC-LOOP-01
func TestLoopBreakerWorktreeEditsFromSubdirCwd(t *testing.T) {
	t.Setenv("NOMOS_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	repoRoot := initHookGitRepoWithWorktree(t, "SUB-777")
	subdirCwd := filepath.Join(repoRoot, "strategy")
	if err := os.MkdirAll(subdirCwd, 0750); err != nil {
		t.Fatal(err)
	}
	wtPkgDir := filepath.Join(repoRoot, "worktrees", "demo-SUB-777", "pkg", "svc")
	wtFile := filepath.Join(wtPkgDir, "api.go")

	for i := 1; i <= 5; i++ {
		content := fmt.Sprintf("package svc\n\nfunc privateSeed() int { return %d }\n", i)
		if err := os.WriteFile(wtFile, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		cmd := fmt.Sprintf("cd %s && go test ./...", wtPkgDir)
		dec := EvaluateLoopCircuitBreakerWithStrikes(&shellguard.HookPayload{
			ToolName:    "Bash",
			CommandLine: cmd,
			Cwd:         subdirCwd,
		}, "", 3)
		if dec.Decision != "allow" {
			t.Fatalf("iteration %d: expected go test allowed after worktree edit when Cwd is subdirectory, got %+v", i, dec)
		}
	}

	// Also verify parent repo digest changes when child worktree files change without leading cd.
	d1 := computeWorkspaceMtimeDigest(subdirCwd)
	if err := os.WriteFile(wtFile, []byte("package svc\n\nfunc privateSeed() int { return 99 }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	d2 := computeWorkspaceMtimeDigest(subdirCwd)
	if d1 == d2 {
		t.Fatalf("expected subdirectory workspace digest to reflect child worktree file edit (%s == %s)", d1, d2)
	}
}
