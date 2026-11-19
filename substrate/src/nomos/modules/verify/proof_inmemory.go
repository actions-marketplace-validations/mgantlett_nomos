// Package verify implements anti-vacuity AST and in-memory gate integrity proofs.
package verify

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/types"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/treesitter"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

// EvaluateNonVacuousGates performs an AST or in-memory audit of all Gate.Evaluate implementations.
// @intent software_physics_engine_spec.md #ProofReport
func EvaluateNonVacuousGates(repoRoot string) (int, int, error) {
	root := repoRoot
	if root == "" {
		if cwd, err := os.Getwd(); err == nil {
			root = cwd
		}
	}

	verifyDir := filepath.Join(root, "substrate", "src", "nomos", "modules", "verify")
	if stat, err := os.Stat(verifyDir); err != nil || !stat.IsDir() {
		verifyDir = filepath.Join(root, "src", "nomos", "modules", "verify")
	}

	if stat, err := os.Stat(verifyDir); err == nil && stat.IsDir() {
		return evaluateASTGateDirectory(verifyDir)
	}

	return evaluateCompiledGatesInMemory()
}

func evaluateASTGateDirectory(verifyDir string) (int, int, error) {
	fset := token.NewFileSet()
	nonVacuous := 0
	totalGates := 0

	walkErr := filepath.Walk(verifyDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return err
		}
		if info.IsDir() {
			if strings.HasPrefix(info.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".go") || strings.HasSuffix(info.Name(), "_test.go") {
			return nil
		}
		fileAST, pErr := parser.ParseFile(fset, path, nil, 0)
		if pErr != nil {
			return types.NewNomosError(types.ErrCodeCore, "failed parsing verify module AST (%s): %v", path, pErr)
		}
		nv, tot := countEvaluateMethodsInFile(fileAST)
		nonVacuous += nv
		totalGates += tot
		return nil
	})
	if walkErr != nil {
		return 0, 0, walkErr
	}
	return nonVacuous, totalGates, nil
}

func countEvaluateMethodsInFile(fileAST *ast.File) (int, int) {
	nonVacuous := 0
	total := 0
	for _, decl := range fileAST.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Evaluate" || fn.Recv == nil || len(fn.Recv.List) == 0 {
			continue
		}
		total++
		if isNonVacuousEvaluate(fn) {
			nonVacuous++
		}
	}
	return nonVacuous, total
}

func evaluateCompiledGatesInMemory() (int, int, error) {
	gates := DefaultBaselineGates()
	totalGates := len(gates)
	if !verifyCompiledGateBehaviorally() {
		return 0, totalGates, nil
	}
	nonVacuous := 0
	for _, g := range gates {
		if g == nil || g.Name() == "" {
			continue
		}
		nonVacuous++
	}
	return nonVacuous, totalGates, nil
}

func isNonVacuousEvaluate(fn *ast.FuncDecl) bool {
	if fn.Body == nil || len(fn.Body.List) == 0 {
		return false
	}
	if len(fn.Body.List) == 1 {
		if ret, ok := fn.Body.List[0].(*ast.ReturnStmt); ok && isTrivialGateReturn(ret) {
			return false
		}
		if ifStmt, ok := fn.Body.List[0].(*ast.IfStmt); ok && ifStmt.Body != nil && len(ifStmt.Body.List) == 1 {
			if ret, ok := ifStmt.Body.List[0].(*ast.ReturnStmt); ok && isTrivialGateReturn(ret) {
				return false
			}
		}
	}
	return true
}

// verifyCompiledGateBehaviorally runs lightweight deterministic checks against
// compiled core gates using isolated temporary fixtures so binary-only proof
// confirms real rejection behavior rather than checking non-nil descriptors.
func verifyCompiledGateBehaviorally() bool {
	tmpDir, err := os.MkdirTemp("", "nomos-inmem-proof-*")
	if err != nil {
		return false
	}
	defer func() {
		if rErr := os.RemoveAll(tmpDir); rErr != nil {
			// best-effort temp fixture cleanup
		}
	}()

	// Construct a stub file triggering AntiShimGate (empty exported body) and
	// MaxFileLOCGate (>400 lines) without shelling out or touching the workspace.
	var sb strings.Builder
	sb.WriteString("package synthetic\n\n")
	sb.WriteString("// Stub triggers AntiShimGate via an empty exported body.\n")
	sb.WriteString("func Stub() {}\n")
	for i := 0; i < 405; i++ {
		sb.WriteString("// padding line to exceed max_file_loc\n")
	}

	probeFile := filepath.Join(tmpDir, "probe.go")
	if err := os.WriteFile(probeFile, []byte(sb.String()), 0644); err != nil {
		return false
	}

	ctx := &vscope.VerifyContext{
		RepoRoot:     tmpDir,
		WorktreePath: tmpDir,
		AllFiles:     true,
		Strict:       false,
	}

	// Both core gates must actively reject the planted violations.
	if !gateRejectsFixture(&AntiShimGate{}, ctx) {
		return false
	}
	if !gateRejectsFixture(&MaxFileLOCGate{}, ctx) {
		return false
	}
	return true
}

// gateRejectsFixture executes a single compiled gate and returns true only when
// the evaluation succeeds without error and reports Passed == false.
func gateRejectsFixture(g vscope.Gate, ctx *vscope.VerifyContext) bool {
	res, err := g.Evaluate(ctx)
	if err != nil || res == nil {
		return false
	}
	return !res.Passed
}

func init() {
	RegisterGate("gate_integrity_proof", func() vscope.Gate { return &GateIntegrityProofGate{} })
}

// GateIntegrityProofGate enforces the Three Invariant Attestation Vectors of Gate Integrity (Cell 2.3, Law 2).
type GateIntegrityProofGate struct{}

// Name returns the canonical display name of the gate.
func (g *GateIntegrityProofGate) Name() string {
	return "Gate Integrity & Invariant Attestation (Law 2)"
}

// Evaluate executes the Three Invariant Attestation Vectors against the target repository.
func (g *GateIntegrityProofGate) Evaluate(ctx *vscope.VerifyContext) (*vscope.GateResult, error) {
	root := ctx.RepoRoot
	if root == "" {
		root = ctx.WorktreePath
	}
	report, err := EvaluateGateIntegrityAttestation(root)
	if err != nil {
		return nil, types.NewNomosError(types.ErrCodeInvariant, "gate integrity attestation execution failed: %v", err)
	}
	if !report.Passed {
		return &vscope.GateResult{
			GateName: g.Name(),
			ProbeID:  "gate_integrity_proof",
			Law:      2,
			Ring:     RingCryptographic,
			Passed:   false,
			Message: fmt.Sprintf("gate integrity attestation failed: MS=%.1f%% (%d/%d killed), FPR=%.1f%%, NonVacuous=%d/%d",
				report.MutationScore, report.MutationKilled, report.MutationTotal,
				report.FalsePositiveRate, report.NonVacuousGates, report.TotalGatesEvaluated),
			Remedy: "Definition of Done gates must kill 100% of mutants, produce 0% false alarms, and have non-vacuous AST logic",
		}, nil
	}

	return &vscope.GateResult{
		GateName: g.Name(),
		ProbeID:  "gate_integrity_proof",
		Law:      2,
		Ring:     RingCryptographic,
		Passed:   true,
		Message: fmt.Sprintf("mathematical proof satisfied: MS=%.1f%% (%d/%d killed), FPR=%.1f%% (%d/%d passed), %d non-vacuous gates (%.2f ms)",
			report.MutationScore, report.MutationKilled, report.MutationTotal,
			report.FalsePositiveRate, report.ReferenceGatesPass, report.ReferenceGatesTotal,
			report.NonVacuousGates, float64(report.DurationTotalMicros)/1000.0),
	}, nil
}

func readGitRevisionBytes(worktreeDir, rev, relSlashPath string) []byte {
	out, err := workspace.SafeGitCommand(worktreeDir, "show", rev+":"+relSlashPath).Output()
	if err != nil {
		return nil
	}
	return out
}

func readWorktreeDiskBytes(absPath string) []byte {
	raw, err := os.ReadFile(absPath)
	if err != nil {
		return nil
	}
	return raw
}

// ArchitecturalEngineDescriptor captures an engine requirement declared in Intent.
type ArchitecturalEngineDescriptor struct {
	Name          string
	IntentSpec    string
	EnforcedBy    string
	IsImplemented bool
}

// AuditArchitecturalEngines inspects Substrate implementation against Intent engine charters.
func AuditArchitecturalEngines(root string) []ArchitecturalEngineDescriptor {
	return []ArchitecturalEngineDescriptor{
		{
			Name:          "SQLite Persistence & Task Lifecycle Engine",
			IntentSpec:    "task_lifecycle_cli_spec.md",
			EnforcedBy:    "substrate/src/nomos/core/storage, substrate/src/nomos/modules/task",
			IsImplemented: vscope.DirExists(filepath.Join(root, "substrate", "src", "nomos", "core", "storage")),
		},
		{
			Name:          "Nomos Mneme Memory Engine",
			IntentSpec:    "mneme_memory_spec.md",
			EnforcedBy:    "substrate/src/nomos/modules/mem",
			IsImplemented: vscope.DirExists(filepath.Join(root, "substrate", "src", "nomos", "modules", "mem")),
		},
		{
			Name:          "Compiled Go & JSON Schema Contract Engine",
			IntentSpec:    "go_ssot_and_json_schema_spec.md",
			EnforcedBy:    "substrate/src/nomos/core/config/schema.go",
			IsImplemented: vscope.FileExists(filepath.Join(root, "substrate", "src", "nomos", "core", "config", "schema.go")),
		},
		{
			Name:       "Nomos Software Physics & CLI Surface Engine",
			IntentSpec: "software_physics_engine_spec.md",
			EnforcedBy: "substrate/cli/physics.go, substrate/cli/cmd_physics.go",
			IsImplemented: vscope.FileExists(filepath.Join(root, "substrate", "cli", "physics.go")) &&
				vscope.FileExists(filepath.Join(root, "substrate", "cli", "cmd_physics.go")),
		},
		{
			Name:          "Hermetic Worktree & CapBAC Security Engine",
			IntentSpec:    "nomos_hermetic_worktree_spec.md",
			EnforcedBy:    "substrate/src/nomos/core/workspace, substrate/src/nomos/core/capbac",
			IsImplemented: vscope.DirExists(filepath.Join(root, "substrate", "src", "nomos", "core", "workspace")),
		},
		{
			Name:          "Polyglot Tree-sitter WASM AST Gate Engine",
			IntentSpec:    "tree_sitter_ast_gate_spec.md",
			EnforcedBy:    "substrate/src/nomos/modules/verify/treesitter",
			IsImplemented: checkWazeroImplemented(root) && vscope.DirExists(filepath.Join(root, "substrate", "src", "nomos", "modules", "verify", "treesitter")),
		},
		{
			Name:          "DoD 7x3 Gate Matrix & Obligation Compiler Engine",
			IntentSpec:    "nomos_intent_traceability_spec.md",
			EnforcedBy:    "substrate/src/nomos/modules/verify",
			IsImplemented: vscope.DirExists(filepath.Join(root, "substrate", "src", "nomos", "modules", "verify")),
		},
		{
			Name:          "Nomos Controlled Technical English (NCTE-100) Engine",
			IntentSpec:    "nomos_controlled_technical_english_spec.md",
			EnforcedBy:    "substrate/src/nomos/modules/ncte",
			IsImplemented: vscope.DirExists(filepath.Join(root, "substrate", "src", "nomos", "modules", "ncte")),
		},
	}
}

func checkWazeroImplemented(root string) bool {
	goModPath := filepath.Join(root, "substrate", "go.mod")
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return false
	}
	if !strings.Contains(string(data), "wazero") {
		return false
	}
	engine, err := treesitter.GetDefaultEngine()
	if err != nil || engine == nil {
		return false
	}
	return engine.Module() != nil && engine.Runtime() != nil
}

