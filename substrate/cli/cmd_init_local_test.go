package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/constants"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
)

func TestSyncAndClearLocalStealthExcludes_IdempotentAndFailClosed(t *testing.T) {
	nonGitDir := t.TempDir()
	if err := syncLocalStealthExcludes(nonGitDir, false); err == nil {
		t.Fatalf("expected stealth excludes to fail closed outside a Git repository")
	}

	repoDir := t.TempDir()
	if out, err := workspace.SafeGitCommand(repoDir, "init", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v\n%s", err, string(out))
	}
	excludePath := filepath.Join(repoDir, constants.DirGit, "info", "exclude")
	if err := os.WriteFile(excludePath, []byte("*.log\n"), 0644); err != nil {
		t.Fatalf("seed exclude: %v", err)
	}

	if err := syncLocalStealthExcludes(repoDir, false); err != nil {
		t.Fatalf("first syncLocalStealthExcludes failed: %v", err)
	}
	if err := syncLocalStealthExcludes(repoDir, false); err != nil {
		t.Fatalf("second syncLocalStealthExcludes failed: %v", err)
	}
	raw, rErr := os.ReadFile(excludePath)
	if rErr != nil {
		t.Fatalf("read exclude: %v", rErr)
	}
	content := string(raw)
	if strings.Count(content, localStealthStartMarker) != 1 || !strings.Contains(content, "*.log") {
		t.Fatalf("expected single idempotent stealth block preserving custom excludes, got:\n%s", content)
	}

	if err := clearLocalStealthExcludes(repoDir); err != nil {
		t.Fatalf("clearLocalStealthExcludes failed: %v", err)
	}
	clearedRaw, cErr := os.ReadFile(excludePath)
	if cErr != nil {
		t.Fatalf("read cleared exclude: %v", cErr)
	}
	cleared := string(clearedRaw)
	if strings.Contains(cleared, localStealthStartMarker) || !strings.Contains(cleared, "*.log") {
		t.Fatalf("expected stealth block stripped while retaining custom excludes, got:\n%s", cleared)
	}
}
