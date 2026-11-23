package task

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/storage"
)

func openTestSQLiteTaskStore(t *testing.T) *SQLiteTaskStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "tasks_ir_test.db")
	db, err := storage.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("failed opening test sqlite db: %v", err)
	}
	store := NewSQLiteTaskStore(db)
	t.Cleanup(func() {
		if cErr := store.Close(); cErr != nil {
			t.Errorf("close store: %v", cErr)
		}
	})
	return store
}

// @verifies TC-TSK-18
func TestTaskRecordStructuredIRRoundTrip(t *testing.T) {
	store := openTestSQLiteTaskStore(t)
	body, err := RenderTaskCardBody(TaskCardInput{
		Scope:              "Upgrade task schema for Agent-to-Agent handoff",
		ScopePaths:         []string{"./substrate/cli/**", "substrate/src/nomos/modules/task/"},
		Obligations:        []string{"tc-tsk-18", "TC-TSK-19"},
		AcceptanceCriteria: []string{"Persist scope_paths and obligations"},
		Verification:       "nomos verify",
	})
	if err != nil {
		t.Fatalf("RenderTaskCardBody error: %v", err)
	}
	if !strings.Contains(body, "TC-TSK-18") || !strings.Contains(body, "substrate/cli/**") {
		t.Fatalf("expected derived markdown to include paths and obligations, got:\n%s", body)
	}
	ac, verifyCmd := ExtractCardIRFromBody(body)
	rec := TaskRecord{
		Key:                "SUB-101",
		Plane:              "substrate",
		Project:            "nomos",
		SchemaType:         SchemaSubstrate,
		Title:              "Structured IR test task",
		Description:        body,
		ScopePaths:         NormalizeScopePaths([]string{"./substrate/cli/**"}),
		Obligations:        []string{"TC-TSK-18"},
		AcceptanceCriteria: ac,
		VerifyCmd:          verifyCmd,
		Status:             StatusTodo,
		Tier:               Tier1,
		Priority:           50,
		Burden:             3,
	}
	if err := store.CreateTask(rec); err != nil {
		t.Fatalf("CreateTask error: %v", err)
	}
	loaded, err := store.GetTask("SUB-101")
	if err != nil {
		t.Fatalf("GetTask error: %v", err)
	}
	if len(loaded.ScopePaths) != 1 || loaded.ScopePaths[0] != "substrate/cli/**" {
		t.Fatalf("unexpected ScopePaths: %+v", loaded.ScopePaths)
	}
	if len(loaded.Obligations) != 1 || loaded.Obligations[0] != "TC-TSK-18" {
		t.Fatalf("unexpected Obligations: %+v", loaded.Obligations)
	}
	if loaded.VerifyCmd != "nomos verify" || len(loaded.AcceptanceCriteria) != 1 {
		t.Fatalf("unexpected VerifyCmd=%q AC=%+v", loaded.VerifyCmd, loaded.AcceptanceCriteria)
	}
}

// @verifies TC-TSK-19
func TestExecuteTaskBatchSymbolicRefsAndCycleRejection(t *testing.T) {
	store := openTestSQLiteTaskStore(t)
	manifest := `{
		"project": "nomos",
		"tasks": [
			{
				"ref": "spec",
				"plane": "intent",
				"title": "Define batch IR contract",
				"scope": "Update task_lifecycle_cli_spec.md with TaskBatchManifest",
				"scope_paths": ["intent/architecture/specs/task_lifecycle_cli_spec.md"],
				"acceptance_criteria": ["NCTE-100 clean"],
				"verify_cmd": "nomos verify"
			},
			{
				"ref": "impl",
				"plane": "substrate",
				"title": "Implement batch SQLite compiler",
				"scope": "Implement ExecuteTaskBatch in substrate/src/nomos/modules/task",
				"scope_paths": ["substrate/src/nomos/modules/task/**"],
				"obligations": ["TC-TSK-19"],
				"acceptance_criteria": ["Commits batch in 1 SQLite tx"],
				"verify_cmd": "go test ./...",
				"depends_on": ["$spec"],
				"twin_of": "$spec"
			}
		]
	}`
	res, err := ExecuteTaskBatch(store, []byte(manifest), "nomos")
	if err != nil {
		t.Fatalf("ExecuteTaskBatch failed: %v", err)
	}
	if len(res.CreatedKeys) != 2 || res.ResolvedRefs["$spec"] != "INT-1" || res.ResolvedRefs["$impl"] != "SUB-1" {
		t.Fatalf("unexpected batch result: %+v", res)
	}
	sub, err := store.GetTask("SUB-1")
	if err != nil || sub.TwinKey != "INT-1" || len(sub.Dependencies) != 1 || sub.Dependencies[0] != "INT-1" {
		t.Fatalf("expected SUB-1 twinned and dependent on INT-1, got: %+v (err=%v)", sub, err)
	}

	cyclic := `{"tasks":[
		{"ref":"a","plane":"substrate","title":"Cycle node A","scope":"First cycle test node","acceptance_criteria":["item"],"verify_cmd":"nomos verify","depends_on":["$b"]},
		{"ref":"b","plane":"substrate","title":"Cycle node B","scope":"Second cycle test node","acceptance_criteria":["item"],"verify_cmd":"nomos verify","depends_on":["$a"]}
	]}`
	if _, cErr := ExecuteTaskBatch(store, []byte(cyclic), "nomos"); cErr == nil {
		t.Fatal("expected cyclic batch subgraph to fail closed")
	}
	if len(store.ListTasks()) != 2 {
		t.Fatalf("expected atomic rollback on cyclic graph, got %d tasks", len(store.ListTasks()))
	}
}

// @verifies TC-TSK-20
func TestWavefrontComputesPreFlightMTPOOverlap(t *testing.T) {
	store := openTestSQLiteTaskStore(t)
	active := TaskRecord{
		Key: "SUB-10", Plane: "substrate", Project: "nomos", SchemaType: SchemaSubstrate,
		Title: "Active edit task", Description: "desc", ScopePaths: []string{"substrate/cli/cmd_task.go"},
		Status: StatusEdit, Tier: Tier1, Priority: 50,
	}
	cand := TaskRecord{
		Key: "SUB-11", Plane: "substrate", Project: "nomos", SchemaType: SchemaSubstrate,
		Title: "Colliding ready task", Description: "desc", ScopePaths: []string{"substrate/cli/**"},
		Status: StatusTodo, Tier: Tier1, Priority: 50,
	}
	disjoint := TaskRecord{
		Key: "SUB-12", Plane: "substrate", Project: "nomos", SchemaType: SchemaSubstrate,
		Title: "Disjoint ready task", Description: "desc", ScopePaths: []string{"substrate/src/nomos/modules/mem/**"},
		Status: StatusTodo, Tier: Tier1, Priority: 50,
	}
	for _, rec := range []TaskRecord{active, cand, disjoint} {
		if err := store.CreateTask(rec); err != nil {
			t.Fatalf("CreateTask(%s): %v", rec.Key, err)
		}
	}
	wf, err := store.GetWavefront()
	if err != nil {
		t.Fatalf("GetWavefront: %v", err)
	}
	foundOverlap := false
	for _, c := range wf {
		if c.Key == "SUB-11" && len(c.OverlapsActive) == 1 && c.OverlapsActive[0] == "SUB-10" {
			foundOverlap = true
		}
		if c.Key == "SUB-12" && len(c.OverlapsActive) != 0 {
			t.Fatalf("expected SUB-12 to have zero active overlaps, got %+v", c.OverlapsActive)
		}
	}
	if !foundOverlap {
		t.Fatalf("expected SUB-11 wavefront candidate to report overlap with SUB-10, got %+v", wf)
	}
}
