package sarif

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/task"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/sensorsummary"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

// @verifies TC-DBNOTE-005
func TestSensorSummaryReceiptAndSarifProperties_TC_DBNOTE_005(t *testing.T) {
	rep := &verify.PipelineReport{
		Passed:              false,
		GatesTotal:          2,
		GatesPassed:         1,
		SensorSummary:       map[string]int{sensorsummary.ScopeASTVerified: 1, sensorsummary.ScopeNoFilesInScope: 1},
		UnindexedExtensions: []string{".sql"},
		GateResults: []*vscope.GateResult{
			{
				GateName:       "Anti-Shim AST Invariant",
				ProbeID:        "anti_shim",
				ExecutionScope: sensorsummary.ScopeASTVerified,
				Passed:         false,
				Message:        "pkg/foo.go:12 empty stub",
				Remedy:         "implement body",
			},
			{
				GateName:       "Contract Schema Integrity Invariant",
				ProbeID:        "contract_parity",
				ExecutionScope: sensorsummary.ScopeNoFilesInScope,
				Passed:         true,
				Message:        "no contract schema scope detected; skipping",
			},
		},
	}

	log := FromVerifyReport(rep, "4.29.0")
	raw, err := MarshalIndent(log)
	if err != nil {
		t.Fatalf("MarshalIndent failed: %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, `"executionScope": "ast_verified"`) || !strings.Contains(body, `".sql"`) {
		t.Fatalf("expected SARIF executionScope and unindexedExtensions in output: %s", body)
	}

	note := task.ReleaseNotePayload{
		TaskKey:             "SUB-3527",
		CommitSHA:           strings.Repeat("a", 40),
		Timestamp:           "2026-11-23T17:40:00Z",
		Phase:               "SYNC",
		DoDGatesPassed:      42,
		SensorSummary:       map[string]int{sensorsummary.ScopeASTVerified: 15, sensorsummary.ScopeRuntimeVerified: 4},
		UnindexedExtensions: []string{".sh"},
		CapBACTokenID:       "cap-SUB-3527",
		WalkthroughContent:  "Verified sensor summary receipt fields.",
		SHA256Receipt:       strings.Repeat("b", 64),
	}
	if valErr := task.ValidateReleaseNote(note); valErr != nil {
		t.Fatalf("ValidateReleaseNote failed: %v", valErr)
	}
	encoded, mErr := json.Marshal(note)
	if mErr != nil || !strings.Contains(string(encoded), `"sensor_summary"`) || !strings.Contains(string(encoded), `"unindexed_extensions"`) {
		t.Fatalf("unexpected ReleaseNotePayload JSON: %s (err=%v)", string(encoded), mErr)
	}
}
