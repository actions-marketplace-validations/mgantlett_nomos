package impact_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/impact"
)

func TestComputeWorkspaceImpact_SkipsGoWalkOnPythonAndUsesGitGrep(t *testing.T) {
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		if out, err := workspace.SafeGitCommand(dir, args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v (%s)", args, err, string(out))
		}
	}
	runGit("init", "-q")
	writeTestFile(t, filepath.Join(dir, "pkg", "billing_engine.py"), "def charge():\n    return 1\n")
	writeTestFile(t, filepath.Join(dir, "pkg", "test_billing_engine.py"), "def test_charge():\n    assert True\n")
	writeTestFile(t, filepath.Join(dir, "api", "checkout.py"), "from pkg import billing_engine\n")
	runGit("add", ".")

	got := impact.ComputeWorkspaceImpact(dir, []string{"pkg/billing_engine.py"})
	if len(got) != 1 {
		t.Fatalf("expected 1 impact summary, got %+v", got)
	}
	if !strings.Contains(got[0], "paired test: test_billing_engine.py") || !strings.Contains(got[0], "api/checkout.py") {
		t.Fatalf("unexpected impact summary: %s", got[0])
	}
}

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
