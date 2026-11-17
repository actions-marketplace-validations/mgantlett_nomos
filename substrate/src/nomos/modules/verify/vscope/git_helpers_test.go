package vscope

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
)

// @verifies TC-TS-13
func TestParseGitNameStatusRenames_AndResolveRenamedBaselinePath(t *testing.T) {
	raw := []byte("M\x00pkg/keep.go\x00R091\x00pkg/legacy.go\x00pkg/sub/legacy.go\x00C100\x00pkg/orig.go\x00pkg/copy.go\x00")
	got := parseGitNameStatusRenames(raw)
	if got["pkg/sub/legacy.go"] != "pkg/legacy.go" {
		t.Fatalf("expected R091 rename mapping, got %+v", got)
	}
	if _, copied := got["pkg/copy.go"]; copied {
		t.Fatalf("expected C100 copy entry not to be treated as rename, got %+v", got)
	}

	repo := t.TempDir()
	_ = workspace.SafeGitCommand(repo, "init", "-b", "main").Run()
	_ = workspace.SafeGitCommand(repo, "config", "user.email", "test@nomos.local").Run()
	_ = workspace.SafeGitCommand(repo, "config", "user.name", "Nomos Test").Run()

	srcDir := filepath.Join(repo, "pkg")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "package pkg\n\n// Compute returns deterministic sum.\nfunc Compute(a, b int) int {\n\treturn a + b\n}\n"
	if err := os.WriteFile(filepath.Join(srcDir, "calc.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = workspace.SafeGitCommand(repo, "add", "-A").Run()
	_ = workspace.SafeGitCommand(repo, "commit", "-m", "chore: baseline").Run()

	subDir := filepath.Join(repo, "pkg", "sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := workspace.SafeGitCommand(repo, "mv", "pkg/calc.go", "pkg/sub/calc.go").CombinedOutput(); err != nil {
		t.Fatalf("git mv failed: %v (%s)", err, string(out))
	}

	ctx := &VerifyContext{RepoRoot: repo, WorktreePath: repo}
	BeginVerifyContextCache(ctx)
	defer EndVerifyContextCache(ctx)

	oldRel := ResolveRenamedBaselinePath(ctx, repo, "HEAD", "pkg/sub/calc.go")
	if oldRel != "pkg/calc.go" {
		t.Fatalf("expected ResolveRenamedBaselinePath='pkg/calc.go', got %q", oldRel)
	}
}

// @verifies TC-TS-21
func TestIsExcludedPolyglotDir_AndWorkspaceVenvDefaults(t *testing.T) {
	for _, sample := range []string{
		"venv/lib/python3.12/site-packages/pip/auth.py",
		"/workspace/venv/lib/python3.12/site-packages/pip/auth.py",
		".venv/lib/site-packages/mod.py",
		"pkg/__pycache__/mod.py",
	} {
		if !IsExcludedPolyglotDir(sample) {
			t.Fatalf("expected IsExcludedPolyglotDir(%q) = true", sample)
		}
	}
	for _, dirName := range []string{"venv", ".venv", "site-packages", "__pycache__"} {
		if !workspace.IsDefaultIntermediateDir(dirName) {
			t.Fatalf("expected workspace.IsDefaultIntermediateDir(%q) = true", dirName)
		}
	}

	repo := t.TempDir()
	for _, cmdArgs := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@nomos.local"},
		{"config", "user.name", "Nomos Test"},
	} {
		if out, err := workspace.SafeGitCommand(repo, cmdArgs...).CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v (%s)", cmdArgs, err, string(out))
		}
	}
	venvPy := filepath.Join(repo, "venv", "lib", "python3.12", "site-packages", "pip", "auth.py")
	appPy := filepath.Join(repo, "etl_pipeline.py")
	for path, content := range map[string]string{
		venvPy: "def f():\n    try:\n        return 1\n    except Exception:\n        pass\n",
		appPy:  "def main():\n    return 0\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	found := make(map[string]bool)
	collectUncommittedPolyglotFiles(repo, found)
	if found[venvPy] {
		t.Fatalf("expected venv site-packages file to be excluded, got %+v", found)
	}
	if !found[appPy] {
		t.Fatalf("expected app file %q in modified set, got %+v", appPy, found)
	}
}

