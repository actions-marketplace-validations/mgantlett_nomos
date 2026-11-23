package sensorsummary

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

func initFixtureTestGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmds := [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	}
	for _, args := range cmds {
		cmd := workspace.SafeGitCommand(dir, args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v (%s)", args, err, string(out))
		}
	}
	return dir
}

func writeRepoFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// @verifies TC-AGT-11
func TestScanOrphanedFixtures_TC_AGT_11(t *testing.T) {
	repo := initFixtureTestGitRepo(t)
	writeRepoFile(t, repo, "examples/legacy_unused.txt", "legacy brownfield example\n")
	writeRepoFile(t, repo, "examples/README.md", "# Examples\n")
	writeRepoFile(t, repo, "pkg/svc/svc.go", "package svc\n")
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "init"}} {
		cmd := workspace.SafeGitCommand(repo, args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, string(out))
		}
	}

	baseCtx := &vscope.VerifyContext{RepoRoot: repo, IsBaseline: true}
	if got := ScanOrphanedFixtures(baseCtx, repo); len(got) != 0 {
		t.Fatalf("expected 0 violations in non-strict baseline, got %v", got)
	}

	strictCtx := &vscope.VerifyContext{RepoRoot: repo, Strict: true}
	strictGot := ScanOrphanedFixtures(strictCtx, repo)
	if len(strictGot) != 1 || !strings.Contains(strictGot[0], "examples/legacy_unused.txt") {
		t.Fatalf("expected strict violation on legacy_unused.txt, got %v", strictGot)
	}

	verifyWorktreeDiffOrphanFixtures(t, repo)
}

func verifyWorktreeDiffOrphanFixtures(t *testing.T, repo string) {
	t.Helper()
	if out, err := workspace.SafeGitCommand(repo, "checkout", "-b", "feature/SUB-99").CombinedOutput(); err != nil {
		t.Fatalf("checkout branch: %v (%s)", err, string(out))
	}
	writeRepoFile(t, repo, "examples/orphan_demo.py", "print('orphan')\n")
	writeRepoFile(t, repo, "pkg/svc/testdata/referenced.json", "{\"ok\":true}\n")
	writeRepoFile(t, repo, "pkg/walked/testdata/case1.golden", "expected\n")
	writeRepoFile(t, repo, "pkg/embedded/fixtures/schema.json", "{}\n")
	writeRepoFile(t, repo, "pkg/svc/svc_test.go", "package svc\n// loads referenced.json\n")
	writeRepoFile(t, repo, "pkg/walked/walked_test.go", "package walked\nimport \"os\"\nfunc TestAll() { _, _ = os.ReadDir(\"testdata\") }\n")
	writeRepoFile(t, repo, "pkg/embedded/embed.go", "package embedded\n//go:embed fixtures/schema.json\nvar raw string\n")

	wtCtx := &vscope.VerifyContext{RepoRoot: repo, WorktreePath: repo, TaskID: "SUB-99"}
	got := ScanOrphanedFixtures(wtCtx, repo)
	if len(got) != 1 || !strings.Contains(got[0], "examples/orphan_demo.py") {
		t.Fatalf("expected exactly 1 violation for examples/orphan_demo.py, got %v", got)
	}
}
