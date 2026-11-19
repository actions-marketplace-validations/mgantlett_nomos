package verify

import (
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/astdebt"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/ratchet"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// @verifies TC-PHY-05 TC-PHY-06 TC-TS-11
func TestPrescribeRatchetedCDSAndComplexityRecipes_Polyglot(t *testing.T) {
	thresh := astdebt.DefaultThresholds()
	cdsCases := []struct {
		file string
		base string
		cur  string
		want []string
	}{
		{
			file: "mux.go",
			base: buildLegacyGoSource(19, ""),
			cur:  buildLegacyGoSource(21, ""),
			want: []string{"mux.go cognitive debt", "exceeds brownfield ceiling", "dCDS +", "1-TURN RECIPE: LegacyDispatch (mux.go:3-", "LegacyDispatchStep()"},
		},
		{
			file: "router.py",
			base: buildPolyglotBranchFixture("py", 18),
			cur:  buildPolyglotBranchFixture("py", 20),
			want: []string{"router.py cognitive debt", "exceeds brownfield ceiling", "dCDS +", "1-TURN RECIPE: dispatch_route (router.py:1-", "dispatch_routeStep()"},
		},
		{
			file: "router.ts",
			base: buildPolyglotBranchFixture("ts", 18),
			cur:  buildPolyglotBranchFixture("ts", 20),
			want: []string{"router.ts cognitive debt", "exceeds brownfield ceiling", "dCDS +", "1-TURN RECIPE: dispatchRoute (router.ts:1-", "dispatchRouteStep()"},
		},
	}
	for _, tc := range cdsCases {
		got := ratchet.PrescribeRatchetedCDSRecipe(tc.file, []byte(tc.cur), []byte(tc.base), thresh)
		assertContainsAllSubstrings(t, got, tc.want...)
	}

	tempDir := t.TempDir()
	goPath := filepath.Join(tempDir, "heavy.go")
	if err := os.WriteFile(goPath, []byte(buildLegacyGoSource(18, "")), 0644); err != nil {
		t.Fatal(err)
	}
	enriched := astdebt.EnrichComplexityViolations(tempDir, []astdebt.FunctionMetric{{File: "heavy.go", Line: 3, Name: "LegacyDispatch", Complexity: 19, LOC: 65}}, thresh, false)
	if len(enriched) != 2 {
		t.Fatalf("expected V(G) and LOC violations, got %v", enriched)
	}
	assertContainsAllSubstrings(t, enriched[0], "heavy.go:3-", "1-TURN RECIPE: extract >=4 branch(es) from LegacyDispatch", "LegacyDispatchStep()")
	assertContainsAllSubstrings(t, enriched[1], "heavy.go:3-", "1-TURN RECIPE: extract >=5 line(s) from LegacyDispatch", "LegacyDispatchStep()")
}

func buildPolyglotBranchFixture(lang string, branches int) string {
	var sb strings.Builder
	if lang == "py" {
		sb.WriteString("def dispatch_route(code: int) -> int:\n")
		for i := 1; i <= branches; i++ {
			sb.WriteString("    if code == 1:\n        return 2\n")
		}
		sb.WriteString("    return 0\n")
		return sb.String()
	}
	sb.WriteString("export function dispatchRoute(code: number): number {\n")
	for i := 1; i <= branches; i++ {
		sb.WriteString("  if (code === 1) {\n    return 2;\n  }\n")
	}
	sb.WriteString("  return 0;\n}\n")
	return sb.String()
}

func TestCognitiveDebtAndComplexityGates_EndToEnd(t *testing.T) {
	tempDir := t.TempDir()
	ctx := &vscope.VerifyContext{
		WorktreePath: tempDir,
		RepoRoot:     tempDir,
		TaskID:       "SUB-999",
	}

	cloneBody := `
func cloneWorker(items []int, mult int) int {
	res := 0
	for i, v := range items {
		if v > 100 {
			res += v * mult
		} else if v < 0 {
			res -= i
		}
		switch v {
		case 10:
			res += 1
		case 20:
			res += 2
		default:
			res += 3
		}
	}
	return res
}
`
	if err := os.WriteFile(filepath.Join(tempDir, "one.go"), []byte("package p\n"+cloneBody), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "two.go"), []byte("package p\n"+strings.ReplaceAll(cloneBody, "cloneWorker", "duplicateWorker")), 0644); err != nil {
		t.Fatal(err)
	}

	cdsGate := &CognitiveDebtGate{}
	cdsRes, err := cdsGate.Evaluate(ctx)
	if err != nil {
		t.Fatalf("unexpected error evaluating CognitiveDebtGate: %v", err)
	}
	if cdsRes.Passed || !strings.Contains(cdsRes.Message, "structural AST clone") {
		t.Fatalf("expected CognitiveDebtGate to fail closed on structural AST clone, got: %+v", cdsRes)
	}
}

func TestIntentTraceabilityGate_GoTypeAndSpecAnchorResolution(t *testing.T) {
	tempDir := t.TempDir()
	specDir := filepath.Join(tempDir, "intent", "architecture", "specs", "core")
	srcDir := filepath.Join(tempDir, "substrate", "src")
	if err := os.MkdirAll(specDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(specDir, "sample_spec.md"), []byte("---\ntitle: Sample\nplane: intent\nstatus: active\n---\n"), 0644); err != nil {
		t.Fatal(err)
	}

	validGo := "package p\n// DefinedContract models the compiled Go contract.\ntype DefinedContract struct{ ID string }\n// ValidFunc is anchored.\n// @intent sample_spec.md #DefinedContract\nfunc ValidFunc() int { return 1 }\n"
	if err := os.WriteFile(filepath.Join(srcDir, "valid.go"), []byte(validGo), 0644); err != nil {
		t.Fatal(err)
	}

	gate := &IntentTraceabilityGate{}
	ctx := &vscope.VerifyContext{RepoRoot: tempDir, WorktreePath: tempDir, IsBaseline: true}
	resOk, err := gate.Evaluate(ctx)
	if err != nil || !resOk.Passed {
		t.Fatalf("expected valid @intent Go type and spec anchor to pass, got: %+v, err=%v", resOk, err)
	}

	invalidGo := "package p\n// GhostFunc references a missing Go type.\n// @intent: #GhostSchema\nfunc GhostFunc() int { return 2 }\n"
	if err := os.WriteFile(filepath.Join(srcDir, "ghost.go"), []byte(invalidGo), 0644); err != nil {
		t.Fatal(err)
	}

	resFail, err := gate.Evaluate(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resFail.Passed || !strings.Contains(resFail.Message, "#GhostSchema") {
		t.Fatalf("expected IntentTraceabilityGate to fail closed on #GhostSchema, got: %+v", resFail)
	}
}
