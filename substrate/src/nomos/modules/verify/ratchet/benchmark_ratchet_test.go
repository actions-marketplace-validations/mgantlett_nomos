package ratchet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/config"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

// @verifies TC-BENCH-01
func TestBenchmarkRatchet_TC_BENCH_01(t *testing.T) {
	goOut := "BenchmarkParse-16\t100000\t120.5 ns/op\t64 B/op\t2 allocs/op\n"
	goMetrics := ParseBenchmarkMetrics(goOut)
	if goMetrics["BenchmarkParse.ns_op"] != 120.5 || goMetrics["BenchmarkParse.allocs_op"] != 2 {
		t.Fatalf("unexpected Go bench parse: %+v", goMetrics)
	}

	jsonMetrics := ParseBenchmarkMetrics(`{"p99_ms": 14.2, "allocs": 10}`)
	if jsonMetrics["p99_ms"] != 14.2 || jsonMetrics["allocs"] != 10 {
		t.Fatalf("unexpected JSON bench parse: %+v", jsonMetrics)
	}

	if reg := CompareBenchmarkAgainstBaseline(map[string]float64{"p99_ms": 10.0}, map[string]float64{"p99_ms": 11.4}, 15.0); len(reg) != 0 {
		t.Fatalf("expected +14%% within 15%% tolerance to pass, got %v", reg)
	}
	reg := CompareBenchmarkAgainstBaseline(map[string]float64{"p99_ms": 10.0}, map[string]float64{"p99_ms": 13.0}, 15.0)
	if len(reg) != 1 || !strings.Contains(reg[0], "+30.0%") {
		t.Fatalf("expected +30%% regression failure, got %v", reg)
	}

	root := t.TempDir()
	br := &config.BenchmarkRatchetConfig{
		Command:          `printf '{"p99_ms": 10.0}'`,
		BaselineFile:     "bench.baseline.json",
		MaxRegressionPct: 15.0,
	}
	basePath, seeded, err := UpdateBenchmarkBaseline(root, br)
	if err != nil || seeded["p99_ms"] != 10.0 {
		t.Fatalf("UpdateBenchmarkBaseline failed: err=%v metrics=%+v", err, seeded)
	}
	if _, statErr := os.Stat(filepath.Join(root, "bench.baseline.json")); statErr != nil {
		t.Fatalf("expected baseline file written at %s: %v", basePath, statErr)
	}

	writeYAML := "project: bench\ndefault_branch: main\ngovernance:\n  quality_gates:\n    benchmark_ratchet:\n      command: \"printf '{\\\"p99_ms\\\": 15.0}'\"\n      baseline_file: bench.baseline.json\n      max_regression_pct: 15\n"
	if err := os.WriteFile(filepath.Join(root, "nomos.yaml"), []byte(writeYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	_, failRes := EvaluateBenchmarkRatchet("Go Toolchain Gate", &vscope.VerifyContext{RepoRoot: root, WorktreePath: root}, root)
	if failRes == nil || failRes.Passed || !strings.Contains(failRes.Message, "+50.0%") {
		t.Fatalf("expected EvaluateBenchmarkRatchet regression block, got %+v", failRes)
	}
}
