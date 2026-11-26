package verify

import (
	"fmt"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/astdebt"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/impact"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/polyrun"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/ratchet"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/types"
)

// UncheckedErrorsGate verifies that error return values are checked.
type UncheckedErrorsGate struct{}

// Name returns the canonical display name.
func (g *UncheckedErrorsGate) Name() string { return "Unchecked Error Hygiene Invariant" }

// Evaluate checks target source files for discarded error return values.
func (g *UncheckedErrorsGate) Evaluate(ctx *vscope.VerifyContext) (*vscope.GateResult, error) {
	root := ctx.WorktreePath
	if root == "" {
		root = ctx.RepoRoot
	}
	var violations []string

	err := vscope.WalkTargetFiles(ctx, root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info == nil || info.IsDir() || !ratchet.IsPolyglotTargetFile(path) {
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			violations = append(violations, evaluatePolyglotUncheckedFile(ctx, root, path)...)
			return nil
		}
		fset, node, pErr := vscope.MemoizedParseGoFile(ctx, path, 0)
		if pErr != nil || node == nil {
			return nil
		}
		rel, rErr := filepath.Rel(root, path)
		if rErr != nil {
			rel = path
		}
		var fileViolations []string
		checkUncheckedErrorsInFile(node, fset, rel, &fileViolations)
		violations = append(violations, ratchet.FilterNetNewLineViolations(ctx, root, path, fileViolations, func(baseBytes []byte) []string {
			baseFset := token.NewFileSet()
			baseNode, bErr := parser.ParseFile(baseFset, path, baseBytes, 0)
			if bErr != nil {
				return nil
			}
			var baseV []string
			checkUncheckedErrorsInFile(baseNode, baseFset, rel, &baseV)
			return baseV
		})...)
		return nil
	})
	if err != nil {
		return nil, types.NewNomosError(types.ErrCodeCore, "unchecked error walk failed: %v", err)
	}

	if len(violations) > 0 {
		return &vscope.GateResult{
			GateName: g.Name(),
			Passed:   false,
			Message:  fmt.Sprintf("detected %d unchecked error discard(s):\n    - %s", len(violations), strings.Join(violations, "\n    - ")),
			Remedy:   "handle errors explicitly instead of discarding with blank identifier `_` (Law 7)",
		}, nil
	}
	return &vscope.GateResult{GateName: g.Name(), Passed: true, Message: "zero unchecked error suppressions detected"}, nil
}

func isIgnoredCallExpr(call *ast.CallExpr) bool {
	if call == nil {
		return false
	}
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		switch fun.Sel.Name {
		case "Close", "Write", "Flush", "Scan", "Sscanf", "Encode", "Decode",
			"Print", "Printf", "Println", "Sprint", "Sprintf", "Fprint", "Fprintf", "Fprintln",
			"Rollback", "Add", "Done", "Wait", "Seek", "Read", "WriteString":
			return true
		}
		if xIdent, ok := fun.X.(*ast.Ident); ok {
			switch xIdent.Name {
			case "fmt", "json", "yaml", "os", "io", "bufio", "strings", "bytes", "math", "rand", "log", "filepath", "strconv", "time", "regexp", "template", "http":
				return true
			}
		}
	case *ast.Ident:
		switch fun.Name {
		case "print", "println", "close", "recover", "append", "copy":
			return true
		}
	}
	return false
}

func checkUncheckedErrorsInFile(file *ast.File, fset *token.FileSet, relPath string, violations *[]string) {
	ast.Inspect(file, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) == 0 {
			return true
		}
		for _, expr := range assign.Rhs {
			if call, isCall := expr.(*ast.CallExpr); isCall {
				if isIgnoredCallExpr(call) {
					continue
				}
				lastIdx := len(assign.Lhs) - 1
				if ident, isIdent := assign.Lhs[lastIdx].(*ast.Ident); isIdent && ident.Name == "_" {
					pos := fset.Position(assign.Pos())
					*violations = append(*violations, fmt.Sprintf("%s:%d: discarded call result via blank identifier", relPath, pos.Line))
					break
				}
			}
		}
		return true
	})
}

func evaluatePolyglotUncheckedFile(ctx *vscope.VerifyContext, root, path string) []string {
	ext := strings.ToLower(filepath.Ext(path))
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	rel, rErr := filepath.Rel(root, path)
	if rErr != nil {
		rel = path
	}
	rel = filepath.ToSlash(rel)
	cur := astdebt.CheckPolyglotUncheckedErrors(ext, rel, raw)
	return ratchet.FilterNetNewLineViolations(ctx, root, path, cur, func(baseBytes []byte) []string {
		return astdebt.CheckPolyglotUncheckedErrors(ext, rel, baseBytes)
	})
}

// GoToolchainGate asserts clean compilation under the standard Go toolchain.
type GoToolchainGate struct{}

// Name returns the canonical display name.
func (g *GoToolchainGate) Name() string { return "Go Toolchain Gate" }

// Evaluate executes Go and polyglot syntax and test toolchains.
func (g *GoToolchainGate) Evaluate(ctx *vscope.VerifyContext) (*vscope.GateResult, error) {
	if os.Getenv(polyrun.EnvInToolchainGate) == "1" {
		return &vscope.GateResult{GateName: g.Name(), Passed: true, Message: "re-entrant toolchain execution bypassed"}, nil
	}
	root := ctx.WorktreePath
	if root == "" {
		root = ctx.RepoRoot
	}
	targetDir := resolveGoTargetDir(root)
	var stacks []string
	var rec *polyrun.ToolchainSuiteReceipt
	if targetDir != "" {
		rec = polyrun.EvaluateSharedGoSuite(ctx, root, targetDir)
		if !rec.GoPassed {
			return &vscope.GateResult{
				GateName: g.Name(),
				Passed:   false,
				Message:  fmt.Sprintf("Go compilation/test failed: %s", polyrun.TruncateToolchainOutput(rec.GoOutput)),
				Remedy:   "resolve Go compiler and test errors (GOWORK=off go test ./...)",
			}, nil
		}
		stacks = append(stacks, formatGoToolchainStackLabel(rec))
	}

	polyStacks, polyFail := evaluateOrReusePolyglotToolchains(ctx, root, rec)
	if polyFail != nil {
		return polyFail, nil
	}
	stacks = append(stacks, polyStacks...)
	if corpusLabel, corpusFail := ratchet.EvaluateCorpusOracle(g.Name(), ctx, root); corpusFail != nil {
		return corpusFail, nil
	} else if corpusLabel != "" {
		stacks = append(stacks, corpusLabel)
	}
	if benchLabel, benchFail := ratchet.EvaluateBenchmarkRatchet(g.Name(), ctx, root); benchFail != nil {
		return benchFail, nil
	} else if benchLabel != "" {
		stacks = append(stacks, benchLabel)
	}
	if len(stacks) == 0 {
		return &vscope.GateResult{GateName: g.Name(), Passed: true, Message: "no Go or polyglot toolchain module detected in scope"}, nil
	}
	return &vscope.GateResult{GateName: g.Name(), Passed: true, Message: fmt.Sprintf("verified toolchains: %s", strings.Join(stacks, ", "))}, nil
}

func evaluateOrReusePolyglotToolchains(ctx *vscope.VerifyContext, root string, rec *polyrun.ToolchainSuiteReceipt) ([]string, *vscope.GateResult) {
	if rec != nil && rec.CacheHit && rec.PolyglotVerified && polyrun.CanUseToolchainReceiptCache(ctx) {
		return rec.PolyglotStacks, nil
	}
	polyStacks, polyFail := polyrun.EvaluatePolyglotToolchains(ctx, root)
	if polyFail != nil {
		return nil, polyFail
	}
	if rec != nil && rec.GoPassed && !rec.GoSkippedNonGo && rec.SnapshotSHA256 != "" {
		rec.PolyglotVerified = true
		rec.PolyglotStacks = polyStacks
		polyrun.TrySaveDiskReceipt(ctx, rec.SnapshotSHA256, rec)
	}
	return polyStacks, nil
}

func formatGoToolchainStackLabel(rec *polyrun.ToolchainSuiteReceipt) string {
	if rec != nil && rec.GoSkippedNonGo {
		return "Go(diff-scoped skip)"
	}
	if rec != nil && rec.CacheHit {
		return "Go(receipt cache)"
	}
	return "Go(go test -cover)"
}

func resolveGoTargetDir(root string) string {
	subDir := filepath.Join(root, "substrate")
	if fi, err := os.Stat(filepath.Join(subDir, "go.mod")); err == nil && !fi.IsDir() {
		return subDir
	}
	if fi, err := os.Stat(filepath.Join(root, "go.mod")); err == nil && !fi.IsDir() {
		return root
	}
	return ""
}

// DefaultXrayMagicStringThreshold matches MagicStringDuplicationGate's statutory ceiling (Cell 5.1, Law 5).
const DefaultXrayMagicStringThreshold = 10

// CountFileUncheckedErrors counts swallowed or discarded error results in a single non-test source file (@intent tree_sitter_ast_gate_spec.md #XrayReport).
func CountFileUncheckedErrors(relSlash string, data []byte) int {
	if len(data) == 0 {
		return 0
	}
	if strings.HasSuffix(relSlash, astdebt.ExtGo) {
		fset := token.NewFileSet()
		node, err := parser.ParseFile(fset, relSlash, data, 0)
		if err != nil || node == nil {
			return 0
		}
		var violations []string
		checkUncheckedErrorsInFile(node, fset, relSlash, &violations)
		return len(violations)
	}
	ext := strings.ToLower(filepath.Ext(relSlash))
	return len(astdebt.CheckPolyglotUncheckedErrors(ext, relSlash, data))
}

// SplitFileSecretAndPathLeaks returns separate counts of credential secret tokens and hardcoded host-path literals in a single source file (@intent tree_sitter_ast_gate_spec.md #XrayReport).
func SplitFileSecretAndPathLeaks(relSlash string, data []byte) (int, int) {
	if len(data) == 0 {
		return 0, 0
	}
	secretTokens := 0
	if !isIgnoredSecretFile(relSlash) && impact.MatchSecretTokenLabel(string(data)) != "" {
		secretTokens = 1
	}
	pathLeaks := 0
	if !strings.HasSuffix(relSlash, "gates_governance.go") {
		pathLeaks = countHostPathLiteralsInBytes(relSlash, data, defaultForbiddenLiterals())
	}
	return secretTokens, pathLeaks
}

// CountFileSecretAndPathLeaks counts hardcoded secret tokens and workstation path literals in a single source file (@intent tree_sitter_ast_gate_spec.md #XrayReport).
func CountFileSecretAndPathLeaks(relSlash string, data []byte) int {
	secretTokens, pathLeaks := SplitFileSecretAndPathLeaks(relSlash, data)
	return secretTokens + pathLeaks
}

func countHostPathLiteralsInBytes(relSlash string, data []byte, patterns []string) int {
	if len(patterns) == 0 {
		return 0
	}
	if strings.HasSuffix(relSlash, astdebt.ExtGo) {
		return countGoStringPathLiterals(relSlash, data, patterns)
	}
	return countPolyglotLinePathLiterals(string(data), patterns)
}

func countGoStringPathLiterals(relSlash string, data []byte, patterns []string) int {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, relSlash, data, 0)
	if err != nil || node == nil {
		return 0
	}
	hits := 0
	ast.Inspect(node, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		val := strings.Trim(lit.Value, "`\"")
		for _, p := range patterns {
			if strings.Contains(val, p) {
				hits++
			}
		}
		return true
	})
	return hits
}

func countPolyglotLinePathLiterals(text string, patterns []string) int {
	hits := 0
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") {
			continue
		}
		for _, p := range patterns {
			if strings.Contains(line, p) {
				hits++
			}
		}
	}
	return hits
}

// CountCorpusMagicStringGroups counts package-scoped repeated string literal clusters exceeding maxDup (@intent tree_sitter_ast_gate_spec.md #XrayReport).
func CountCorpusMagicStringGroups(sources map[string][]byte, maxDup int) int {
	if len(sources) == 0 {
		return 0
	}
	if maxDup <= 0 {
		maxDup = DefaultXrayMagicStringThreshold
	}
	dirFiles, dirs := groupCorpusFilesByDir(sources)
	workers := min(max(runtime.GOMAXPROCS(0), 1), max(len(dirs), 1))
	var total atomic.Int64
	var cursor atomic.Int64
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			fset := token.NewFileSet()
			for {
				i := int(cursor.Add(1) - 1)
				if i >= len(dirs) {
					return
				}
				total.Add(int64(countSingleDirMagicClusters(fset, dirFiles[dirs[i]], sources, maxDup)))
			}
		}()
	}
	wg.Wait()
	return int(total.Load())
}

func groupCorpusFilesByDir(sources map[string][]byte) (map[string][]string, []string) {
	byDir := make(map[string][]string)
	for rel := range sources {
		dirKey := filepath.ToSlash(filepath.Dir(rel))
		byDir[dirKey] = append(byDir[dirKey], rel)
	}
	dirs := make([]string, 0, len(byDir))
	for dirKey, list := range byDir {
		sort.Strings(list)
		byDir[dirKey] = list
		dirs = append(dirs, dirKey)
	}
	sort.Strings(dirs)
	return byDir, dirs
}

func countSingleDirMagicClusters(fset *token.FileSet, files []string, sources map[string][]byte, maxDup int) int {
	pkgMaps := make(map[string]map[string]int)
	for _, rel := range files {
		if strings.HasSuffix(rel, astdebt.ExtGo) {
			collectGoStringLiteralsFromSrc(fset, rel, sources[rel], pkgMaps)
		} else {
			collectPolyglotStringLiteralsFromBytes(rel, sources[rel], pkgMaps)
		}
	}
	groups := 0
	for _, pkgMap := range pkgMaps {
		for _, cnt := range pkgMap {
			if cnt > maxDup {
				groups++
			}
		}
	}
	return groups
}
