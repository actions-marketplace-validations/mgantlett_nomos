package intentlint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/task"
)

// @verifies TC-OBL-015
func TestComputePendingSiblingDeltasFromTasks_SharedSpecIsolation(t *testing.T) {
	repo := initTestGitRepoWithBaseSpec(t)
	commitSharedSpecMutation(t, repo, "[INT-10] feat(intent): add alpha obligation",
		"# Shared\n```go\npackage p\ntype Box struct {\n\tID string\n\tAlpha int\n}\n```\n| ID | Desc |\n|---|---|\n| `TC-SHR-01` | Base |\n| `TC-ALP-01` | Alpha |\n")
	commitSharedSpecMutation(t, repo, "[INT-20] feat(intent): add beta obligation",
		"# Shared\n```go\npackage p\ntype Box struct {\n\tID string\n\tAlpha int\n\tBeta bool\n}\n```\n| ID | Desc |\n|---|---|\n| `TC-SHR-01` | Base |\n| `TC-ALP-01` | Alpha |\n| `TC-BET-01` | Beta |\n")

	tasks := []task.TaskRecord{
		{Key: "SUB-10", Status: task.StatusEdit, TwinKey: "INT-10"},
		{Key: "SUB-20", Status: task.StatusEdit, TwinKey: "INT-20"},
	}
	delta := computePendingSiblingDeltasFromTasks(repo, repo, "SUB-10", tasks)
	if !delta.HasAddedTC("TC-BET-01") || !delta.HasAddedField("Box", "Beta") {
		t.Fatalf("expected sibling INT-20 additions (TC-BET-01, Box.Beta) to be deferred, got: %+v", delta)
	}
	if delta.HasAddedTC("TC-ALP-01") || delta.HasAddedField("Box", "Alpha") {
		t.Fatalf("expected own twin INT-10 additions (TC-ALP-01, Box.Alpha) NOT to be deferred, got: %+v", delta)
	}
}

// @verifies TC-OBL-015
func TestComputePendingSiblingDeltasFromTasks_SubtractsOwnRemovalsAndDefersSiblingRemovals(t *testing.T) {
	repo := initTestGitRepoWithBaseSpec(t)
	commitSharedSpecMutation(t, repo, "[INT-1] chore: seed fields",
		"# Shared\n```go\npackage p\ntype Box struct {\n\tID string\n\tOwnOld int\n\tSiblingOld string\n}\n```\n| ID | Desc |\n|---|---|\n| `TC-SHR-01` | Base |\n| `TC-OWN-01` | Own old |\n| `TC-SIB-01` | Sibling old |\n")
	commitSharedSpecMutation(t, repo, "[INT-10] fix(intent): retire own field and vector",
		"# Shared\n```go\npackage p\ntype Box struct {\n\tID string\n\tSiblingOld string\n}\n```\n| ID | Desc |\n|---|---|\n| `TC-SHR-01` | Base |\n| `TC-SIB-01` | Sibling old |\n")
	commitSharedSpecMutation(t, repo, "[INT-20] fix(intent): retire sibling field and vector",
		"# Shared\n```go\npackage p\ntype Box struct {\n\tID string\n}\n```\n| ID | Desc |\n|---|---|\n| `TC-SHR-01` | Base |\n")

	tasks := []task.TaskRecord{
		{Key: "SUB-10", Status: task.StatusEdit, TwinKey: "INT-10"},
		{Key: "SUB-20", Status: task.StatusEdit, TwinKey: "INT-20"},
	}
	delta := computePendingSiblingDeltasFromTasks(repo, repo, "SUB-10", tasks)
	if !delta.HasRemovedTC("TC-SIB-01") || !delta.HasRemovedField("Box", "SiblingOld") {
		t.Fatalf("expected sibling INT-20 removals (TC-SIB-01, Box.SiblingOld) to be deferred, got: %+v", delta)
	}
	if delta.HasRemovedTC("TC-OWN-01") || delta.HasRemovedField("Box", "OwnOld") {
		t.Fatalf("expected own twin INT-10 removals (TC-OWN-01, Box.OwnOld) NOT to be deferred, got: %+v", delta)
	}
}

// @verifies TC-OBL-015
func TestCollectDeferredSiblingSpecsFromTasks_ExcludesDisjointSiblingSpecRoots(t *testing.T) {
	repo := initTestGitRepoWithBaseSpec(t)
	commitSpecFileMutation(t, repo, "intent/architecture/specs/portal_spec.md", "[INT-10] feat(intent): portal mode",
		"# Portal\n```go\npackage p\ntype PortalCfg struct {\n\tMode string\n}\n```\n")
	commitSpecFileMutation(t, repo, "intent/architecture/specs/cockpit_spec.md", "[INT-20] feat(intent): retire checkpoint",
		"# Cockpit\n```go\npackage c\ntype TelemetryResp struct {\n\tActive bool\n}\n```\n")

	tasks := []task.TaskRecord{
		{Key: "SUB-10", Status: task.StatusEdit, TwinKey: "INT-10"},
		{Key: "SUB-20", Status: task.StatusEdit, TwinKey: "INT-20"},
	}
	deferred := collectDeferredSiblingSpecsFromTasks(repo, repo, "SUB-10", tasks)
	if !deferred["intent/architecture/specs/cockpit_spec.md"] {
		t.Fatalf("expected disjoint sibling cockpit_spec.md to be deferred, got: %+v", deferred)
	}
	if deferred["intent/architecture/specs/portal_spec.md"] {
		t.Fatalf("did not expect own portal_spec.md to be deferred, got: %+v", deferred)
	}

	scoped, skipped := FilterDeferredSpecRoots(repo, []string{"intent/architecture/specs"}, deferred)
	if !skipped {
		t.Fatal("expected FilterDeferredSpecRoots to report skipped=true")
	}
	for _, f := range scoped {
		if filepath.Base(f) == "cockpit_spec.md" {
			t.Fatalf("expected cockpit_spec.md to be excluded from scoped roots, got: %v", scoped)
		}
	}
}

func initTestGitRepoWithBaseSpec(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGitOrFail(t, repo, "init")
	runGitOrFail(t, repo, "config", "user.email", "test@example.com")
	runGitOrFail(t, repo, "config", "user.name", "Test")
	commitSharedSpecMutation(t, repo, "chore: base spec",
		"# Shared\n```go\npackage p\ntype Box struct {\n\tID string\n}\n```\n| ID | Desc |\n|---|---|\n| `TC-SHR-01` | Base |\n")
	return repo
}

func commitSharedSpecMutation(t *testing.T, repo, msg, body string) {
	t.Helper()
	commitSpecFileMutation(t, repo, "intent/architecture/specs/shared_spec.md", msg, body)
}

func commitSpecFileMutation(t *testing.T, repo, relPath, msg, body string) {
	t.Helper()
	specPath := filepath.Join(repo, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(specPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(specPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	runGitOrFail(t, repo, "add", ".")
	runGitOrFail(t, repo, "commit", "-m", msg)
}

func runGitOrFail(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := workspace.SafeGitCommand(dir, args...).CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v (%s)", args, err, string(out))
	}
}
