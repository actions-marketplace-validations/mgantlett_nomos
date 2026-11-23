// Package verify implements the deterministic Definition of Done (DoD) quality gate pipeline.
package verify

import (
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
	"runtime"
	"sync"
	"time"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/types"
)

// Sensor constants define the available telemetry drivers (#SensorType).
const (
	SensorGit          = "git"
	SensorAST          = "ast"
	SensorTestCoverage = "test_coverage"
	SensorSecret       = "secret"
	SensorLinks        = "links"
	SensorTraceability = "traceability"
	SensorCustom       = "custom"
)

// GateSpec defines the declarative schema for an individual sensor probe (#GateSpec).
type GateSpec struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Law     int    `json:"law"`
	Ring    string `json:"ring"`
	Domain  string `json:"domain"`
	Sensor  string `json:"sensor"`
	Phase   string `json:"phase"`
	Enabled bool   `json:"enabled"`
	Remedy  string `json:"remedy"`
	RuleKey string `json:"rule_key"`
}

// PipelineDeclaration models the compiled quality gate pipeline schema.
type PipelineDeclaration struct {
	Version string              `json:"version"`
	Cells   []InvariantCellSpec `json:"cells,omitempty"`
	Gates   []GateSpec          `json:"gates"`
}

// PipelineReport encapsulates the complete verification results across all gates, cells, and laws.
type PipelineReport struct {
	Passed         bool                 `json:"passed"`
	DurationMicros int64                `json:"duration_micros"`
	GatesTotal     int                  `json:"gates_total"`
	GatesPassed    int                  `json:"gates_passed"`
	GateResults    []*vscope.GateResult `json:"gate_results"`
	LawReports     []*LawReport         `json:"law_reports"`
	CellReports    []*CellReport        `json:"cell_reports"`
}

// Pipeline executes an ordered suite of Definition of Done quality gates.
type Pipeline struct {
	gates []vscope.Gate
}

// NewPipeline creates a new verification pipeline instance with the provided gates.
func NewPipeline(gates []vscope.Gate) *Pipeline {
	return &Pipeline{gates: gates}
}

// Execute runs all configured gates in a bounded worker pool, aggregates results, and builds matrix reports.
func (p *Pipeline) Execute(ctx *vscope.VerifyContext) (*PipelineReport, error) {
	if ctx == nil {
		return nil, types.NewNomosError(types.ErrCodeValidationFailed, "verify context cannot be nil")
	}
	vscope.BeginVerifyContextCache(ctx)
	defer vscope.EndVerifyContextCache(ctx)

	start := time.Now()
	results, gateErr := evaluateGatesBounded(p.gates, ctx)
	if gateErr != nil {
		return nil, gateErr
	}
	enrichCanonicalProbeMetadata(results)
	overallPassed := allGatesPassed(results)

	elapsed := time.Since(start).Microseconds()
	cellReports := buildCellReports(results)
	lawReports := buildLawReports(cellReports)

	return &PipelineReport{
		Passed:         overallPassed,
		DurationMicros: elapsed,
		GatesTotal:     len(results),
		GatesPassed:    countPassed(results),
		GateResults:    results,
		LawReports:     lawReports,
		CellReports:    cellReports,
	}, nil
}

func evaluateGatesBounded(gates []vscope.Gate, ctx *vscope.VerifyContext) ([]*vscope.GateResult, error) {
	n := len(gates)
	results := make([]*vscope.GateResult, n)
	errs := make([]error, n)
	if n == 0 {
		return results, nil
	}

	workers := runtime.GOMAXPROCS(0)
	if workers > 6 {
		workers = 6
	}
	if workers < 1 {
		workers = 1
	}
	if workers > n {
		workers = n
	}

	jobs := make(chan int, n)
	for i := 0; i < n; i++ {
		jobs <- i
	}
	close(jobs)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				g := gates[idx]
				res, err := vscope.TimedEvaluate(g, ctx)
				if err != nil {
					errs[idx] = types.NewNomosError(types.ErrCodeCore, "gate %s evaluation failed: %v", g.Name(), err)
					continue
				}
				if res == nil {
					res = &vscope.GateResult{GateName: g.Name(), Passed: false, Message: "nil gate result returned"}
				}
				results[idx] = res
			}
		}()
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return results, nil
}

func allGatesPassed(results []*vscope.GateResult) bool {
	for _, res := range results {
		if !res.Passed && !res.Skipped {
			return false
		}
	}
	return true
}

func countPassed(results []*vscope.GateResult) int {
	passed := 0
	for _, r := range results {
		if r.Passed || r.Skipped {
			passed++
		}
	}
	return passed
}

type probeCellCoord struct {
	law  int
	ring string
}

// buildProbeCellCoords indexes each canonical ProbeID to its statutory Law and Ring.
func buildProbeCellCoords() map[string]probeCellCoord {
	cells := DefaultBaselineCells()
	coords := make(map[string]probeCellCoord, len(CanonicalGateOrder))
	for _, c := range cells {
		for _, pid := range c.ProbeIDs {
			coords[pid] = probeCellCoord{law: c.Law, ring: c.Ring}
		}
	}
	return coords
}

// enrichCanonicalProbeMetadata backfills missing ProbeID, Law, and Ring fields from the canonical registry.
func enrichCanonicalProbeMetadata(results []*vscope.GateResult) {
	nameToID := GateNameToIDMap()
	coords := buildProbeCellCoords()
	for _, r := range results {
		if r == nil {
			continue
		}
		if r.ProbeID == "" {
			r.ProbeID = nameToID[r.GateName]
		}
		if c, ok := coords[r.ProbeID]; ok {
			if r.Law == 0 {
				r.Law = c.law
			}
			if r.Ring == "" {
				r.Ring = c.ring
			}
		}
	}
}

// buildCellReports maps gate results into the 21 canonical invariant cells.
func buildCellReports(results []*vscope.GateResult) []*CellReport {
	resMap := make(map[string]*vscope.GateResult, len(results))
	for _, r := range results {
		if r.ProbeID != "" {
			resMap[r.ProbeID] = r
		}
		resMap[r.GateName] = r
	}

	cells := DefaultBaselineCells()
	reports := make([]*CellReport, 0, len(cells))

	for _, cell := range cells {
		cr := aggregateCell(cell, resMap)
		reports = append(reports, cr)
	}
	return reports
}

func aggregateCell(cell InvariantCellSpec, resMap map[string]*vscope.GateResult) *CellReport {
	cr := &CellReport{
		ID:         cell.ID,
		Name:       cell.Name,
		Law:        cell.Law,
		Ring:       cell.Ring,
		Domain:     cell.Domain,
		Passed:     true,
		Remedy:     cell.Remedy,
		ProbeCount: len(cell.ProbeIDs),
		Probes:     make([]*vscope.GateResult, 0, len(cell.ProbeIDs)),
	}

	for _, pid := range cell.ProbeIDs {
		if r, ok := resMap[pid]; ok {
			cr.Probes = append(cr.Probes, r)
			cr.DurationMicros += r.DurationMicros
			if r.Passed || r.Skipped {
				cr.PassedProbes++
			} else {
				cr.Passed = false
			}
		}
	}
	return cr
}

// buildLawReports aggregates cell reports into 7 statutory law reports.
func buildLawReports(cells []*CellReport) []*LawReport {
	laws := make([]*LawReport, 7)
	for i, l := range CanonicalLaws {
		laws[i] = &LawReport{
			Law:    l.Law,
			Name:   l.Name,
			Domain: l.Domain,
			Passed: true,
			Cells:  make([]*CellReport, 0, 3),
		}
	}

	for _, c := range cells {
		idx := c.Law - 1
		if idx >= 0 && idx < 7 {
			laws[idx].TotalCells++
			laws[idx].DurationMicros += c.DurationMicros
			laws[idx].Cells = append(laws[idx].Cells, c)
			if c.Passed {
				laws[idx].PassedCells++
			} else {
				laws[idx].Passed = false
			}
		}
	}
	return laws
}

var (
	registryMu   sync.RWMutex
	gateRegistry = make(map[string]func() vscope.Gate)
)

// RegisterGate registers a gate constructor for a given canonical ID.
func RegisterGate(id string, ctor func() vscope.Gate) {
	registryMu.Lock()
	defer registryMu.Unlock()
	gateRegistry[id] = ctor
}

// GateByID retrieves a gate instance by its canonical sensor probe ID.
func GateByID(id string) vscope.Gate {
	registryMu.RLock()
	defer registryMu.RUnlock()
	if ctor, exists := gateRegistry[id]; exists {
		return ctor()
	}
	return nil
}

// RegisteredGateCount returns the number of active baseline quality gates.
func RegisteredGateCount() int {
	return len(DefaultBaselineGates())
}

// IDToGateNameMap returns a map from sensor probe ID to canonical display name.
func IDToGateNameMap() map[string]string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	m := make(map[string]string, len(gateRegistry))
	for id, ctor := range gateRegistry {
		m[id] = ctor().Name()
	}
	return m
}

// GateNameToIDMap returns a map from canonical display name to sensor probe ID.
func GateNameToIDMap() map[string]string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	m := make(map[string]string, len(gateRegistry))
	for id, ctor := range gateRegistry {
		m[ctor().Name()] = id
	}
	return m
}

// CanonicalGateOrder defines the statutory sequence of the 21 invariant cells' baseline gates.
var CanonicalGateOrder = []string{
	"phase_discipline", "hermetic_worktree", "staged_boundaries", "canonical_singularity",
	"commit_schema", "go_toolchain", "coverage", "tdd",
	"forbidden_literals", "anti_shim", "git_hygiene", "docstrings",
	"complexity", "broken_wire", "secret", "max_file_loc",
	"cognitive_debt", "ecosystem_drift", "boundary", "ip_leak",
	"magic_strings", "magic_string_duplication", "config_immutability", "threshold_integrity",
	"intent_traceability", "intent_parity", "review_receipt", "gate_integrity_proof", "unchecked_errors",
	"dead_code", "intent_steelman", "contract_parity", "ui_anti_mask", "infra_ingress",
	"capbac_lease", "release_receipt", "topology_parity", "zero_alias",
	"zero_filesystem_alias", "plane_separation", "uasset_dependency", "visual_regression",
}

// DefaultBaselineGates returns the canonical sequence of baseline verification gates.
func DefaultBaselineGates() []vscope.Gate {
	registryMu.RLock()
	defer registryMu.RUnlock()

	gates := make([]vscope.Gate, 0, len(CanonicalGateOrder))
	for _, id := range CanonicalGateOrder {
		if ctor, ok := gateRegistry[id]; ok {
			gates = append(gates, ctor())
		}
	}
	return gates
}
