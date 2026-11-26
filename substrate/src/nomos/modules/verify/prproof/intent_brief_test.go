package prproof

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
)

// @verifies TC-PR-03
func TestBuildArchitecturalIntentBrief_TC_PR_03(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	} {
		if out, err := workspace.SafeGitCommand(repo, args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, string(out))
		}
	}
	writeBriefFixture(t, repo, "go.mod", "module demo\n\ngo 1.24\n")
	writeBriefFixture(t, repo, "pkg/api/types.go", "package api\ntype Base struct { ID string }\n")
	if out, err := workspace.SafeGitCommand(repo, "add", ".").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v (%s)", err, string(out))
	}
	if out, err := workspace.SafeGitCommand(repo, "commit", "-m", "base").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v (%s)", err, string(out))
	}
	if out, err := workspace.SafeGitCommand(repo, "checkout", "-b", "feature/intent-brief").CombinedOutput(); err != nil {
		t.Fatalf("git checkout: %v (%s)", err, string(out))
	}

	writeBriefFixture(t, repo, "go.mod", "module demo\n\ngo 1.24\n// dep bump\n")
	writeBriefFixture(t, repo, "pkg/api/types.go", "package api\ntype Base struct { ID string }\ntype BillingEvent struct { Amount int }\n")
	writeBriefFixture(t, repo, "pkg/api/types_test.go", "package api\n// @verifies TC-PR-03\n")
	if out, err := workspace.SafeGitCommand(repo, "add", ".").CombinedOutput(); err != nil {
		t.Fatalf("git add feature: %v (%s)", err, string(out))
	}
	if out, err := workspace.SafeGitCommand(repo, "commit", "-m", "feat: add billing event").CombinedOutput(); err != nil {
		t.Fatalf("git commit feature: %v (%s)", err, string(out))
	}

	brief := BuildArchitecturalIntentBrief(repo, "main", true)
	md := FormatArchitecturalIntentBrief(brief)
	for _, want := range []string{"#BillingEvent", "go.mod", "+TC-PR-03", "SEALED"} {
		if !strings.Contains(md, want) {
			t.Fatalf("expected %q in Architectural Intent Brief markdown:\n%s", want, md)
		}
	}
}

func writeBriefFixture(t *testing.T, repo, rel, body string) {
	t.Helper()
	full := filepath.Join(repo, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
