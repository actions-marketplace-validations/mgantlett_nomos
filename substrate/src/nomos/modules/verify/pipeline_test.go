package verify

import (
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
	"testing"
)

type mockGate struct {
	name   string
	probe  string
	passed bool
	msg    string
}

// Name returns the mock gate display name.
func (m *mockGate) Name() string { return m.name }

// Evaluate returns the configured mock gate result.
func (m *mockGate) Evaluate(ctx *vscope.VerifyContext) (*vscope.GateResult, error) {
	return &vscope.GateResult{
		GateName: m.name,
		ProbeID:  m.probe,
		Passed:   m.passed,
		Message:  m.msg,
	}, nil
}

// TestPipeline_Execute verifies baseline pipeline aggregation across passing mock gates.
func TestPipeline_Execute(t *testing.T) {
	gates := []vscope.Gate{
		&mockGate{name: "Anti-Shim AST Invariant", probe: "anti_shim", passed: true, msg: "clean"},
		&mockGate{name: "Complexity Invariant", probe: "complexity", passed: true, msg: "clean"},
	}

	pipe := NewPipeline(gates)
	ctx := &vscope.VerifyContext{TaskID: "SUB-1195", Phase: "REVIEW"}

	rep, err := pipe.Execute(ctx)
	if err != nil {
		t.Fatalf("unexpected error executing pipeline: %v", err)
	}

	if !rep.Passed {
		t.Errorf("expected pipeline to pass")
	}
	if rep.GatesTotal != 2 {
		t.Errorf("expected 2 total gates, got %d", rep.GatesTotal)
	}
	if rep.GatesPassed != 2 {
		t.Errorf("expected 2 passed gates, got %d", rep.GatesPassed)
	}
	if len(rep.LawReports) != 7 {
		t.Errorf("expected 7 law reports, got %d", len(rep.LawReports))
	}
	if len(rep.CellReports) != 21 {
		t.Errorf("expected 21 cell reports, got %d", len(rep.CellReports))
	}
}

// TestPipeline_Execute_Failure verifies pipeline failure propagation when any gate fails.
func TestPipeline_Execute_Failure(t *testing.T) {
	gates := []vscope.Gate{
		&mockGate{name: "Anti-Shim AST Invariant", probe: "anti_shim", passed: true, msg: "clean"},
		&mockGate{name: "Failing Gate", probe: "complexity", passed: false, msg: "exceeded"},
	}

	pipe := NewPipeline(gates)
	ctx := &vscope.VerifyContext{TaskID: "SUB-1195", Phase: "REVIEW"}

	rep, err := pipe.Execute(ctx)
	if err != nil {
		t.Fatalf("unexpected error executing pipeline: %v", err)
	}

	if rep.Passed {
		t.Errorf("expected pipeline to fail when a gate fails")
	}
	if rep.GatesPassed != 1 {
		t.Errorf("expected 1 passed gate, got %d", rep.GatesPassed)
	}
}

// TestPipeline_EnrichesAll42RegisteredGatesToCells verifies 42/42 ProbeID, Law, and Ring cell symmetry when gates omit ProbeID on pass.
func TestPipeline_EnrichesAll42RegisteredGatesToCells(t *testing.T) {
	baseGates := DefaultBaselineGates()
	if len(baseGates) != 42 {
		t.Fatalf("expected 42 registered baseline gates, got %d", len(baseGates))
	}
	mocks := make([]vscope.Gate, 0, len(baseGates))
	for _, g := range baseGates {
		mocks = append(mocks, &mockGate{name: g.Name(), probe: "", passed: true, msg: "ok"})
	}
	rep, err := NewPipeline(mocks).Execute(&vscope.VerifyContext{IsBaseline: true})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	for _, gr := range rep.GateResults {
		if gr.ProbeID == "" || gr.Law < 1 || gr.Law > 7 || gr.Ring == "" {
			t.Fatalf("expected enriched ProbeID/Law/Ring on %q, got %+v", gr.GateName, gr)
		}
	}
	totalProbes, passedProbes := 0, 0
	for _, c := range rep.CellReports {
		if len(c.Probes) != c.ProbeCount || c.PassedProbes != c.ProbeCount {
			t.Fatalf("cell %s probe mismatch: len=%d passed=%d want=%d", c.ID, len(c.Probes), c.PassedProbes, c.ProbeCount)
		}
		totalProbes += len(c.Probes)
		passedProbes += c.PassedProbes
	}
	if totalProbes != 42 || passedProbes != 42 {
		t.Fatalf("expected 42/42 cell probes populated, got %d/%d", passedProbes, totalProbes)
	}
}
