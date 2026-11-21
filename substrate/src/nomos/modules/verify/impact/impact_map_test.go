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

	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/mono\n\ngo 1.23\n")
	writeTestFile(t, filepath.Join(dir, "pkg", "rbac", "check.go"), "package rbac\n\nfunc Check() bool { return true }\n")
	writeTestFile(t, filepath.Join(dir, "pkg", "api", "handler.go"), "package api\n\nimport \"example.com/mono/pkg/rbac\"\n\nfunc Handle() bool { return rbac.Check() }\n")
	runGit("add", ".")
	goImp := impact.ComputeWorkspaceImpact(dir, []string{"pkg/rbac/check.go"})
	if len(goImp) != 1 || !strings.Contains(goImp[0], "importers(1): api") {
		t.Fatalf("expected targeted Go reverse import match, got %+v", goImp)
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
