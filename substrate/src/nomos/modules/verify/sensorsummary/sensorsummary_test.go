package sensorsummary

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

// @verifies TC-DOD-07
func TestSensorSummaryAndExecutionScope_TC_DOD_07(t *testing.T) {
	cases := []struct {
		name   string
		res    *vscope.GateResult
		sensor string
		want   string
	}{
		{"nil_result", nil, "ast", ""},
		{"preset_scope", &vscope.GateResult{ExecutionScope: ScopeASTVerified, Passed: true}, "git", ScopeASTVerified},
		{"skipped_gate", &vscope.GateResult{Skipped: true, Passed: true}, "ast", ScopeConfigDisabled},
		{"disabled_msg", &vscope.GateResult{Passed: true, Message: "contract boundary verification disabled in configuration (skip)"}, "ast", ScopeConfigDisabled},
		{"no_scope_msg", &vscope.GateResult{Passed: true, Message: "no contract schema scope detected; skipping check"}, "ast", ScopeNoFilesInScope},
		{"zero_files", &vscope.GateResult{Passed: true, Message: "0 files scanned"}, "ast", ScopeNoFilesInScope},
		{"ast_sensor", &vscope.GateResult{Passed: true, Message: "zero violations"}, "ast", ScopeASTVerified},
		{"runtime_sensor", &vscope.GateResult{Passed: true, Message: "coverage 84%"}, "test_coverage", ScopeRuntimeVerified},
		{"secret_sensor", &vscope.GateResult{Passed: true, Message: "clean"}, "secret", ScopeBoundedHeuristic},
		{"structural_sensor", &vscope.GateResult{Passed: true, Message: "valid"}, "git", ScopeStructuralVerified},
	}
	var results []*vscope.GateResult
	for _, tc := range cases {
		got := ClassifyGateExecutionScope(tc.res, tc.sensor)
		if got != tc.want {
			t.Fatalf("%s: got %q, want %q", tc.name, got, tc.want)
		}
		if tc.res != nil {
			tc.res.ExecutionScope = got
			results = append(results, tc.res)
		}
	}

	if SensorForProbeID("anti_shim", "") != "ast" || SensorForProbeID("unknown_probe", "dynamic") != "test_coverage" || SensorForProbeID("unknown_probe", "syntactic") != "structural" {
		t.Fatalf("unexpected SensorForProbeID mapping")
	}

	summary := SummarizeSensors(results)
	if summary[ScopeASTVerified] != 2 || summary[ScopeConfigDisabled] != 2 || summary[ScopeNoFilesInScope] != 2 {
		t.Fatalf("unexpected SummarizeSensors: %+v", summary)
	}
	if SummarizeSensors(nil) != nil {
		t.Fatalf("expected nil summary for empty slice")
	}

	verifyUnindexedExtensionsAndFormat(t, summary)
}

func verifyUnindexedExtensionsAndFormat(t *testing.T, summary map[string]int) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "schema.sql"), []byte("SELECT 1;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "deploy.sh"), []byte("#!/bin/sh\necho ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "node_modules", "pkg"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "node_modules", "pkg", "ignored.lua"), []byte("-- ignored"), 0o600); err != nil {
		t.Fatal(err)
	}

	unindexed := ScanUnindexedExtensions(dir)
	if len(unindexed) != 2 || unindexed[0] != ".sh" || unindexed[1] != ".sql" {
		t.Fatalf("expected [.sh .sql], got %v", unindexed)
	}
	if ScanUnindexedExtensions("") != nil {
		t.Fatalf("expected nil for empty root")
	}

	line := FormatSensorSummaryLine(summary, unindexed)
	if !strings.Contains(line, "2 ast") || !strings.Contains(line, "heuristic-only exts: .sh, .sql") {
		t.Fatalf("unexpected summary line: %q", line)
	}
	if FormatSensorSummaryLine(nil, nil) != "" {
		t.Fatalf("expected empty line for nil summary")
	}
}
