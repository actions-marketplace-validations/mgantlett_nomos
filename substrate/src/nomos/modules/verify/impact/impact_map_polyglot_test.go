package impact_test

import (
	"path/filepath"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/impact"
)

func TestResolvePairedTestStatus_GitIndexedNestedTest(t *testing.T) {
	dir := t.TempDir()
	if out, err := workspace.SafeGitCommand(dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v (%s)", err, string(out))
	}
	writeTestFile(t, filepath.Join(dir, "Services", "OrderProcessor.cs"), "namespace Demo { class OrderProcessor {} }\n")
	writeTestFile(t, filepath.Join(dir, "Tests", "Unit", "OrderProcessorTests.cs"), "class OrderProcessorTests {}\n")
	if out, err := workspace.SafeGitCommand(dir, "add", ".").CombinedOutput(); err != nil {
		t.Fatalf("git add failed: %v (%s)", err, string(out))
	}

	got := impact.ResolvePairedTestStatus(dir, "Services/OrderProcessor.cs")
	if got != "paired test: OrderProcessorTests.cs" {
		t.Fatalf("expected nested C# paired test via git index, got %q", got)
	}
}
