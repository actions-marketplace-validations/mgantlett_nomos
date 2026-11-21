package impact_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/impact"
)

func TestComputeWorkspaceImpact_GitStemAndGoParallelFastPaths(t *testing.T) {
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		if out, err := workspace.SafeGitCommand(dir, args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v (%s)", args, err, string(out))
		}
	}
	runGit("init", "-q")
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/demo\n\ngo 1.24\n")
	writeTestFile(t, filepath.Join(dir, "pkg", "core", "engine.go"), "package core\n\nfunc Run() {}\n")
	writeTestFile(t, filepath.Join(dir, "cmd", "api", "main.go"), "package main\n\nimport \"example.com/demo/pkg/core\"\n\nfunc main() { core.Run() }\n")
	writeTestFile(t, filepath.Join(dir, "src", "orphan_helper.ts"), "export const x = 1;\n")
	runGit("add", ".")

	gotTS := impact.ComputeWorkspaceImpact(dir, []string{"src/orphan_helper.ts"})
	if len(gotTS) != 1 || !strings.Contains(gotTS[0], "no paired unit test") {
		t.Fatalf("expected clean zero-dependent TS impact summary, got %+v", gotTS)
	}

	gotGo := impact.ComputeWorkspaceImpact(dir, []string{"pkg/core/engine.go"})
	if len(gotGo) != 1 || !strings.Contains(gotGo[0], "importers(1): api") {
		t.Fatalf("expected parallel Go reverse importer summary, got %+v", gotGo)
	}
}
