package intentlint

import (
	"os"
	"path/filepath"
	"strings"
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

// @verifies TC-OBL-015
func TestVerifySpecTypeSignaturesWithDelta_PolyglotSiblingDeferral(t *testing.T) {
	repo := initTestGitRepoWithBaseSpec(t)
	commitSpecFileMutation(t, repo, "intent/architecture/specs/portal_spec.md", "chore: base ts contract",
		"# Portal\n```ts\nexport interface BaseCfg {\n  id: string;\n}\n```\n")
	commitSpecFileMutation(t, repo, "intent/architecture/specs/portal_spec.md", "[INT-10] feat(intent): add own widget",
		"# Portal\n```ts\nexport interface BaseCfg {\n  id: string;\n  ownFlag: boolean;\n}\nexport interface OwnWidget {\n  slug: string;\n}\n```\n")
	commitSpecFileMutation(t, repo, "intent/architecture/specs/portal_spec.md", "[INT-20] feat(intent): add sibling event config",
		"# Portal\n```ts\nexport interface BaseCfg {\n  id: string;\n  ownFlag: boolean;\n  siblingFlag: boolean;\n}\nexport interface OwnWidget {\n  slug: string;\n}\nexport interface PortalEventConfig {\n  mode: string;\n}\n```\n")

	writeRepoFile(t, repo, "web/types.ts",
		"export interface BaseCfg {\n  id: string;\n  ownFlag: boolean;\n}\nexport interface OwnWidget {\n  slug: string;\n}\n")

	tasks := []task.TaskRecord{
		{Key: "SUB-10", Status: task.StatusEdit, TwinKey: "INT-10"},
		{Key: "SUB-20", Status: task.StatusEdit, TwinKey: "INT-20"},
	}
	sub10Delta := computePendingSiblingDeltasFromTasks(repo, repo, "SUB-10", tasks)
	if issues := VerifySpecTypeSignaturesWithDelta(repo, "", []string{"intent/architecture/specs/portal_spec.md"}, sub10Delta); len(issues) != 0 {
		t.Fatalf("expected sibling polyglot additions to defer cleanly for SUB-10, got: %v", issues)
	}

	sub20Delta := computePendingSiblingDeltasFromTasks(repo, repo, "SUB-20", tasks)
	sub20Issues := strings.Join(VerifySpecTypeSignaturesWithDelta(repo, "", []string{"intent/architecture/specs/portal_spec.md"}, sub20Delta), "; ")
	if !strings.Contains(sub20Issues, "missing compiled contract type PortalEventConfig") || !strings.Contains(sub20Issues, "type BaseCfg missing declared field siblingFlag") {
		t.Fatalf("expected SUB-20 to fail closed on its own unimplemented TS contract, got: %s", sub20Issues)
	}
}

// @verifies TC-OBL-015
func TestComputePendingSiblingDeltasFromTasks_BidirectionalMultiIntentTwins(t *testing.T) {
	repo := initTestGitRepoWithBaseSpec(t)
	commitSpecFileMutation(t, repo, "intent/architecture/specs/portal_spec.md", "[INT-20] feat(intent): add portal event config",
		"# Portal\n```ts\nexport interface PortalEventConfig {\n  active: boolean;\n}\n```\n| ID | Desc |\n|---|---|\n| `TC-EVT-01` | Event |\n")
	commitSpecFileMutation(t, repo, "intent/architecture/specs/portal_spec.md", "[INT-21] feat(intent): add solstice ornament",
		"# Portal\n```ts\nexport interface PortalEventConfig {\n  active: boolean;\n}\nexport interface SolsticeOrnamentSpec {\n  motif: string;\n}\n```\n| ID | Desc |\n|---|---|\n| `TC-EVT-01` | Event |\n| `TC-ORN-01` | Ornament |\n")

	tasks := []task.TaskRecord{
		{Key: "SUB-10", Status: task.StatusEdit},
		{Key: "SUB-20", Status: task.StatusEdit, TwinKey: "INT-20"},
		{Key: "INT-21", Status: task.StatusDone, TwinKey: "SUB-20"},
	}

	sibDelta := computePendingSiblingDeltasFromTasks(repo, repo, "SUB-10", tasks)
	if !sibDelta.HasAddedStruct("PortalEventConfig") || !sibDelta.HasAddedStruct("SolsticeOrnamentSpec") || !sibDelta.HasAddedTC("TC-EVT-01") || !sibDelta.HasAddedTC("TC-ORN-01") {
		t.Fatalf("expected both forward INT-20 and reverse-linked INT-21 additions to defer for SUB-10, got: %+v", sibDelta)
	}

	ownDelta := computePendingSiblingDeltasFromTasks(repo, repo, "SUB-20", tasks)
	if ownDelta.HasAddedStruct("PortalEventConfig") || ownDelta.HasAddedStruct("SolsticeOrnamentSpec") || ownDelta.HasAddedTC("TC-ORN-01") {
		t.Fatalf("expected SUB-20 NOT to self-exempt forward INT-20 or reverse-linked INT-21 obligations, got: %+v", ownDelta)
	}
}

// @verifies TC-OBL-015
func TestComputePendingSiblingDeltasFromTasks_TrunkWorktreeLivenessFailClosed(t *testing.T) {
	cases := []struct {
		name           string
		status         task.Status
		createWorktree bool
		wantDeferred   bool
	}{
		{name: "active_no_worktree_fails_closed", status: task.StatusEdit, createWorktree: false, wantDeferred: false},
		{name: "active_live_worktree_defers", status: task.StatusEdit, createWorktree: true, wantDeferred: true},
		{name: "done_with_stale_dir_fails_closed", status: task.StatusDone, createWorktree: true, wantDeferred: false},
		{name: "aborted_with_stale_dir_fails_closed", status: task.StatusAborted, createWorktree: true, wantDeferred: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := initTestGitRepoWithBaseSpec(t)
			commitSpecFileMutation(t, repo, "intent/architecture/specs/portal_spec.md", "[INT-20] feat(intent): add portal event",
				"# Portal\n```ts\nexport interface PortalEventConfig {\n  active: boolean;\n}\n```\n| ID | Desc |\n|---|---|\n| `TC-EVT-01` | Event |\n")
			wtDir := filepath.Join(repo, "worktrees", "repo-SUB-20")
			if tc.createWorktree {
				if err := os.MkdirAll(wtDir, 0o755); err != nil {
					t.Fatalf("mkdir worktree: %v", err)
				}
			}
			tasks := []task.TaskRecord{{Key: "SUB-20", Status: tc.status, TwinKey: "INT-20"}}
			delta := computePendingSiblingDeltasFromTasks(repo, repo, "", tasks)
			deferredSpecs := collectDeferredSiblingSpecsFromTasks(repo, repo, "", tasks)
			gotStruct := delta.HasAddedStruct("PortalEventConfig")
			gotTC := delta.HasAddedTC("TC-EVT-01")
			gotSpec := deferredSpecs["intent/architecture/specs/portal_spec.md"]
			if gotStruct != tc.wantDeferred || gotTC != tc.wantDeferred || gotSpec != tc.wantDeferred {
				t.Fatalf("trunk deferral mismatch: want=%v gotStruct=%v gotTC=%v gotSpec=%v", tc.wantDeferred, gotStruct, gotTC, gotSpec)
			}
			if tc.wantDeferred {
				if err := os.RemoveAll(wtDir); err != nil {
					t.Fatalf("remove worktree: %v", err)
				}
				afterRemove := computePendingSiblingDeltasFromTasks(repo, repo, "", tasks)
				if afterRemove.HasAddedStruct("PortalEventConfig") || afterRemove.HasAddedTC("TC-EVT-01") {
					t.Fatalf("expected trunk to fail closed immediately after worktree removal, got: %+v", afterRemove)
				}
			}
		})
	}
}

func writeRepoFile(t *testing.T, repo, relPath, body string) {
	t.Helper()
	full := filepath.Join(repo, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
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
