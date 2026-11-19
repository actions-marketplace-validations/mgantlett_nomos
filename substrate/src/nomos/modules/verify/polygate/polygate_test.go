package polygate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/astdebt"
)

func TestPolygateViolationEvaluatorsAndTDDHelpers(t *testing.T) {
	if len(EvaluateCoverageViolations(50.0, 65.0)) != 1 || len(EvaluateCoverageViolations(80.0, 65.0)) != 0 {
		t.Fatalf("unexpected EvaluateCoverageViolations result")
	}
	if len(EvaluateCDSViolations([]astdebt.FileMetric{{Name: "a.go", CDS: 50}}, 45)) != 1 {
		t.Fatalf("expected CDS violation")
	}
	if len(EvaluateDocstringViolations(50, 90, 1, 2, []string{"Foo"})) != 1 {
		t.Fatalf("expected docstring violation")
	}
	if len(EvaluateComplexityViolations([]astdebt.FunctionMetric{{File: "a.go", Line: 1, Name: "f", Complexity: 20, LOC: 70}}, 14, 55)) != 2 {
		t.Fatalf("expected complexity and LOC violations")
	}
	if len(EvaluateFileLOCViolations([]astdebt.FileMetric{{Name: "a.go", LOC: 450}}, 400)) != 1 {
		t.Fatalf("expected file LOC violation")
	}
	if len(EvaluateZeroFilesystemAliasViolations([]string{"sym"})) != 1 || len(EvaluateZeroFilesystemAliasViolations(nil)) != 0 {
		t.Fatalf("unexpected symlink violation output")
	}

	pct, ok := ParseSimpleCovCoverage([]byte(`{"RSpec":{"coverage":{"/app/lib/foo.rb":{"lines":[1,1,0,null]}}}}`))
	if !ok || pct < 66.0 || pct > 67.0 {
		t.Fatalf("expected ~66.6%% SimpleCov coverage, got pct=%v ok=%v", pct, ok)
	}

	if !IsPolyglotImplFileForTDD("worker.py") || IsPolyglotImplFileForTDD("test_worker.py") || IsPolyglotImplFileForTDD("types.d.ts") {
		t.Fatalf("unexpected IsPolyglotImplFileForTDD classification")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "calc.go"), []byte("package sample\nfunc Add(a, b int) int { return a + b }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "calc_test.go"), []byte("package sample\nimport \"testing\"\nfunc TestAdd(t *testing.T) { if Add(1, 2) != 3 { t.Fatal() } }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	hasGo, hasTest := InspectPackageFiles(dir)
	if !hasGo || !hasTest {
		t.Fatalf("expected coupled Go tests to pass InspectPackageFiles, got hasGo=%v hasTest=%v", hasGo, hasTest)
	}
}
