// Package verify implements anti-vacuity AST and in-memory gate integrity proofs.
package verify

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/types"
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

