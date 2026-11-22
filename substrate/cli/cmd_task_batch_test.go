package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/task"
)

// @verifies TC-TSK-19
func TestTaskBatchCLI_JSONSubgraphAndCycleError(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("NOMOS_HOME", tmpHome)

	cmd := NewTaskCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(`{
		"project": "nomos",
		"tasks": [
			{
				"ref": "spec",
				"plane": "intent",
				"title": "Author batch spec",
				"scope": "Document TaskBatchManifest in spec",
				"scope_paths": ["intent/architecture/specs/task_lifecycle_cli_spec.md"],
				"acceptance_criteria": ["NCTE-100 passes"],
				"verify_cmd": "nomos verify"
			},
			{
				"ref": "sub",
				"plane": "substrate",
				"title": "Implement batch command",
				"scope": "Add nomos task batch CLI subcommand",
				"scope_paths": ["substrate/cli/cmd_task_batch.go"],
				"obligations": ["TC-TSK-19"],
				"acceptance_criteria": ["All unit tests pass"],
				"verify_cmd": "nomos verify",
				"depends_on": ["$spec"],
				"twin_of": "$spec"
			}
		]
	}`))
	cmd.SetArgs([]string{"batch", "-F", "-", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("nomos task batch --json failed: %v\noutput: %s", err, out.String())
	}

	var res task.TaskBatchResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("failed unmarshaling TaskBatchResult JSON: %v\nraw: %s", err, out.String())
	}
	if len(res.CreatedKeys) != 2 || res.ResolvedRefs["$spec"] != "INT-1" {
		t.Fatalf("unexpected TaskBatchResult: %+v", res)
	}
}

// @verifies TC-TSK-21
func TestRenderTurnZeroExecutionBrief_EmitsPathsAndObligations(t *testing.T) {
	var buf bytes.Buffer
	rec := &task.TaskRecord{
		Key:         "SUB-99",
		Plane:       "substrate",
		Title:       "Handoff target task",
		ScopePaths:  []string{"substrate/cli/**", "substrate/src/nomos/modules/task/**"},
		Obligations: []string{"TC-TSK-21", "TC-TSK-22"},
		VerifyCmd:   "nomos verify",
	}
	renderTurnZeroExecutionBrief(&buf, rec)
	rendered := buf.String()
	for _, token := range []string{"Turn-0 Agent Handoff Brief", "substrate/cli/**", "TC-TSK-21", "nomos verify"} {
		if !strings.Contains(rendered, token) {
			t.Fatalf("expected Turn-0 brief to contain %q, got:\n%s", token, rendered)
		}
	}
}

// @verifies TC-TSK-22
func TestVerifyTaskBoundObligations_FailsClosedWhenUnbound(t *testing.T) {
	repoDir := t.TempDir()
	testFile := filepath.Join(repoDir, "sample_test.go")
	if err := os.WriteFile(testFile, []byte("package sample\n// @verifies TC-TSK-21\n"), 0644); err != nil {
		t.Fatalf("write sample_test.go: %v", err)
	}
	rec := &task.TaskRecord{
		Key:         "SUB-99",
		Plane:       "substrate",
		Obligations: []string{"TC-TSK-21", "TC-TSK-22"},
	}
	err := verifyTaskBoundObligations(repoDir, rec)
	if err == nil || !strings.Contains(err.Error(), "TC-TSK-22") {
		t.Fatalf("expected verifyTaskBoundObligations to reject missing TC-TSK-22, got: %v", err)
	}

	if err := os.WriteFile(testFile, []byte("package sample\n// @verifies TC-TSK-21\n// @verifies TC-TSK-22\n"), 0644); err != nil {
		t.Fatalf("update sample_test.go: %v", err)
	}
	if err := verifyTaskBoundObligations(repoDir, rec); err != nil {
		t.Fatalf("expected bound obligations to pass, got: %v", err)
	}
}
