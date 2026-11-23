package verify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/sensorsummary"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

func TestPipelineSensorSummaryAndOrphanFixtureIntegration(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	} {
		if out, err := workspace.SafeGitCommand(dir, args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, string(out))
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "query.sql"), []byte("SELECT 1;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "init"}} {
		if out, err := workspace.SafeGitCommand(dir, args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, string(out))
		}
	}
	if out, err := workspace.SafeGitCommand(dir, "checkout", "-b", "feature/SUB-77").CombinedOutput(); err != nil {
		t.Fatalf("checkout: %v (%s)", err, string(out))
	}
	exDir := filepath.Join(dir, "examples")
	if err := os.MkdirAll(exDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exDir, "dead_demo.py"), []byte("x = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx := &vscope.VerifyContext{RepoRoot: dir, WorktreePath: dir, TaskID: "SUB-77"}
	pipe := NewPipeline([]vscope.Gate{&AntiShimGate{}})
	rep, err := pipe.Execute(ctx)
	if err != nil {
		t.Fatalf("Pipeline.Execute failed: %v", err)
	}
	if rep.Passed || len(rep.GateResults) != 1 || !strings.Contains(rep.GateResults[0].Message, "orphaned_fixture") {
		t.Fatalf("expected AntiShimGate orphaned_fixture failure, got %+v", rep.GateResults[0])
	}
	if rep.GateResults[0].ExecutionScope != sensorsummary.ScopeASTVerified || rep.SensorSummary[sensorsummary.ScopeASTVerified] != 1 {
		t.Fatalf("expected ExecutionScope=%s and summary, got scope=%q summary=%+v", sensorsummary.ScopeASTVerified, rep.GateResults[0].ExecutionScope, rep.SensorSummary)
	}
	if len(rep.UnindexedExtensions) != 1 || rep.UnindexedExtensions[0] != ".sql" {
		t.Fatalf("expected [.sql] unindexed extension, got %v", rep.UnindexedExtensions)
	}
}
