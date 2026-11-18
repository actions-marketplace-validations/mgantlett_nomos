// Package verify implements 11-language TDD paired-test verification across strict, ratchet, diff, and off modes.
package verify

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/config"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/astdebt"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/impact"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

var tddImplIgnoredNames = map[string]bool{
	"__init__.py": true, "conftest.py": true, "setup.py": true,
	"mod.rs": true, "lib.rs": true, "main.rs": true,
	"index.ts": true, "index.js": true, "types.ts": true, "constants.ts": true,
	"app.d.ts": true, "ambient.d.ts": true, "env.d.ts": true,
}

func resolveEffectiveTDDMode(ctx *vscope.VerifyContext) string {
	cfg := vscope.ResolveVerifyConfig(ctx)
	if cfg != nil && strings.TrimSpace(cfg.Governance.QualityGates.TDDMode) != "" {
		mode := cfg.Governance.QualityGates.EffectiveTDDMode()
		if mode == config.TDDModeOff && ctx != nil && ctx.Strict {
			return config.TDDModeStrict
		}
		return mode
	}
	if ctx != nil && ctx.Strict {
		return config.TDDModeStrict
	}
	if cfg != nil {
		return cfg.Governance.QualityGates.EffectiveTDDMode()
	}
	return config.TDDModeStrict
}

func resolveEffectiveCoverageMode(ctx *vscope.VerifyContext) string {
	cfg := vscope.ResolveVerifyConfig(ctx)
	if cfg != nil && (ctx == nil || !ctx.Strict) {
		return cfg.Governance.QualityGates.EffectiveCoverageMode()
	}
	return config.CoverageModeLive
}

func hasGoPairedTestsForModified(root, absDir string, modified []string) bool {
	for _, rel := range modified {
		if !strings.HasSuffix(rel, astdebt.ExtGo) || strings.HasSuffix(rel, astdebt.SuffixGoTest) {
			continue
		}
		if filepath.Clean(filepath.Dir(filepath.Join(root, rel))) != filepath.Clean(absDir) {
			continue
		}
		if !hasPairedPolyglotTest(root, rel) {
			return false
		}
	}
	return true
}

func isPolyglotImplFileForTDD(name string) bool {
	lower := strings.ToLower(name)
	if isPolyglotTestFilePath(lower) || strings.HasSuffix(lower, ".d.ts") || tddImplIgnoredNames[lower] || vscope.IsNonLogicToolConfigFile(lower) {
		return false
	}
	return isPolyglotTDDTrackedExt(filepath.Ext(lower))
}

func inspectPolyglotDirForStrictTDD(ctx *vscope.VerifyContext, root, dir string) (bool, bool) {
	if !hasPolyglotTestManifest(root) && !hasRepositoryPolyglotTests(root) {
		return false, false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, false
	}
	implFiles, hasLocalTest := classifyPolyglotDirEntries(entries)
	implFiles = vscope.FilterNonExcludedImplFiles(ctx, root, dir, implFiles)
	if len(implFiles) == 0 {
		return false, false
	}
	if hasLocalTest || (filepath.Clean(dir) == filepath.Clean(root) && HasScratchPolyglotTests(root)) || hasAnyPairedPolyglotImpl(root, dir, implFiles) {
		return true, true
	}
	return true, false
}

// HasScratchPolyglotTests reports whether untracked .tmp/tests or .tmp/test directories hold at least one unit test file.
// @intent nomos_intent_traceability_spec.md
func HasScratchPolyglotTests(root string) bool {
	if strings.TrimSpace(root) == "" {
		return false
	}
	for _, sub := range []string{astdebt.DirTests, astdebt.DirTest} {
		dirPath := filepath.Join(root, ".tmp", sub)
		if dirContainsPolyglotTestFile(dirPath) {
			return true
		}
	}
	return false
}

func dirContainsPolyglotTestFile(dirPath string) bool {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			if dirContainsPolyglotTestFile(filepath.Join(dirPath, e.Name())) {
				return true
			}
			continue
		}
		if isPolyglotTestFilePath(e.Name()) {
			return true
		}
	}
	return false
}

func classifyPolyglotDirEntries(entries []os.DirEntry) ([]string, bool) {
	var implFiles []string
	hasLocalTest := false
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			if name == "__tests__" || name == astdebt.DirTests || name == astdebt.DirTest {
				hasLocalTest = true
			}
			continue
		}
		if strings.HasSuffix(name, astdebt.ExtGo) {
			continue
		}
		if isPolyglotTestFilePath(name) {
			hasLocalTest = true
		} else if isPolyglotImplFileForTDD(name) {
			implFiles = append(implFiles, name)
		}
	}
	return implFiles, hasLocalTest
}

func hasAnyPairedPolyglotImpl(root, dir string, implFiles []string) bool {
	for _, name := range implFiles {
		absFile := filepath.Join(dir, name)
		if strings.HasSuffix(name, astdebt.ExtRS) && impact.HasInlineRustTestModule(absFile) {
			return true
		}
		if rel, rErr := filepath.Rel(root, absFile); rErr == nil && hasPairedPolyglotTest(root, filepath.ToSlash(rel)) {
			return true
		}
	}
	return false
}

func hasPairedPolyglotTest(root, rel string) bool {
	status := impact.ResolvePairedTestStatus(root, rel)
	return strings.HasPrefix(status, impact.PairedTestPrefix)
}

func evaluateBrownfieldPolyglotTDD(ctx *vscope.VerifyContext, root string, modified, polyFiles []string) []string {
	if len(polyFiles) == 0 {
		return nil
	}
	mode := resolveEffectiveTDDMode(ctx)
	hasModTest := hasModifiedTestFile(modified) || (mode != config.TDDModeDiff && HasScratchPolyglotTests(root))
	hasRepoSuite := hasRepositoryPolyglotTests(root)
	hasManifest := hasPolyglotTestManifest(root)

	seen := make(map[string]struct{})
	var missing []string
	for _, rel := range polyFiles {
		if isPolyglotFileSatisfyingBrownfieldTDD(ctx, root, rel, mode, hasModTest, hasRepoSuite, hasManifest) {
			continue
		}
		dirRel := filepath.ToSlash(filepath.Dir(rel))
		if _, ok := seen[dirRel]; !ok {
			seen[dirRel] = struct{}{}
			missing = append(missing, dirRel)
		}
	}
	return missing
}

func isPolyglotFileSatisfyingBrownfieldTDD(ctx *vscope.VerifyContext, root, rel, mode string, hasModTest, hasRepoSuite, hasManifest bool) bool {
	if !isPolyglotImplFileForTDD(filepath.Base(rel)) || vscope.IsTDDExcludedRel(ctx, root, rel) {
		return true
	}
	absPath := filepath.Join(root, rel)
	if strings.HasSuffix(rel, astdebt.ExtRS) && impact.HasInlineRustTestModule(absPath) {
		return true
	}
	if hasPairedPolyglotTest(root, rel) || hasModTest {
		return true
	}
	if mode == config.TDDModeDiff {
		return false
	}
	if mode == config.TDDModeStrict {
		return !hasManifest && !hasRepoSuite
	}
	_, existedOnBaseline := loadBaselineFileBytes(ctx, root, absPath)
	if existedOnBaseline && (hasRepoSuite || !hasManifest) {
		return true
	}
	return !existedOnBaseline && !hasManifest && !hasRepoSuite
}

const maxTestComplexity = 25

func evaluateTestComplexityViolations(ctx *vscope.VerifyContext, root string) []string {
	var testFuncs []astdebt.FunctionMetric
	fset := token.NewFileSet()

	walkErr := vscope.WalkTargetFiles(ctx, root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() || vscope.IsExcludedPolyglotDir(filepath.ToSlash(path)) {
			return nil
		}
		rel, rErr := filepath.Rel(root, path)
		if rErr != nil {
			rel = path
		}
		if !isPolyglotTestFilePath(rel) {
			return nil
		}
		if strings.HasSuffix(path, astdebt.ExtGo) {
			if node, pErr := parser.ParseFile(fset, path, nil, 0); pErr == nil {
				collectFunctionMetrics(node, fset, rel, &testFuncs)
			}
			return nil
		}
		collectPolyglotComplexity(path, rel, &testFuncs)
		return nil
	})
	if walkErr != nil {
		return nil
	}

	breaches := filterRatchetedTestComplexity(ctx, root, testFuncs)
	out := make([]string, 0, len(breaches))
	for _, fn := range breaches {
		out = append(out, fmt.Sprintf("%s:%d test function %s cyclomatic complexity %d exceeds max %d (decompose into modular table-driven subtests)",
			filepath.ToSlash(fn.File), fn.Line, fn.Name, fn.Complexity, maxTestComplexity))
	}
	return out
}

func filterRatchetedTestComplexity(ctx *vscope.VerifyContext, root string, funcs []astdebt.FunctionMetric) []astdebt.FunctionMetric {
	ratchet := vscope.IsBrownfieldRatchetActive(ctx)
	baseCache := make(map[string]map[string]astdebt.FunctionMetric)
	var out []astdebt.FunctionMetric
	for _, fn := range funcs {
		if fn.Complexity <= maxTestComplexity {
			continue
		}
		if !ratchet {
			out = append(out, fn)
			continue
		}
		baseMap, loaded := baseCache[fn.File]
		if !loaded {
			absPath := filepath.Join(root, fn.File)
			if baseBytes, ok := loadBaselineFileBytes(ctx, root, absPath); ok {
				baseMap = parseBaselineFunctions(absPath, fn.File, baseBytes)
			}
			baseCache[fn.File] = baseMap
		}
		if baseMap != nil && fn.Complexity <= maxInt(maxTestComplexity, baseMap[fn.Name].Complexity) {
			continue
		}
		out = append(out, fn)
	}
	return out
}

// hasCoupledGoPackageTests verifies that *_test.go files in dir reference at least one
// local package symbol when production .go files declare testable functions or types.
func hasCoupledGoPackageTests(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	fset := token.NewFileSet()
	declared := make(map[string]bool)
	var testFiles []*ast.File

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), astdebt.ExtGo) {
			continue
		}
		parsed, pErr := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		if pErr != nil {
			// Defer syntax error reporting to GoToolchainGate rather than double-flagging TDD.
			return true
		}
		if strings.HasSuffix(e.Name(), astdebt.SuffixGoTest) {
			testFiles = append(testFiles, parsed)
			continue
		}
		collectDeclaredGoSymbols(parsed, declared)
	}
	if len(testFiles) == 0 {
		return false
	}
	if len(declared) == 0 {
		return true
	}
	return anyGoTestReferencesDeclared(testFiles, declared)
}

func collectDeclaredGoSymbols(node *ast.File, declared map[string]bool) {
	for _, d := range node.Decls {
		switch decl := d.(type) {
		case *ast.FuncDecl:
			if decl.Name != nil && decl.Name.Name != "main" && decl.Name.Name != "init" && decl.Name.Name != "_" {
				declared[decl.Name.Name] = true
			}
		case *ast.GenDecl:
			collectGenDeclSymbols(decl, declared)
		}
	}
}

func collectGenDeclSymbols(decl *ast.GenDecl, declared map[string]bool) {
	for _, spec := range decl.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			if s.Name != nil && s.Name.Name != "_" {
				declared[s.Name.Name] = true
			}
		case *ast.ValueSpec:
			for _, n := range s.Names {
				if n != nil && n.Name != "_" {
					declared[n.Name] = true
				}
			}
		}
	}
}

func anyGoTestReferencesDeclared(testFiles []*ast.File, declared map[string]bool) bool {
	matched := false
	for _, tf := range testFiles {
		for _, decl := range tf.Decls {
			ast.Inspect(decl, func(n ast.Node) bool {
				if matched {
					return false
				}
				if id, ok := n.(*ast.Ident); ok && declared[id.Name] {
					matched = true
					return false
				}
				return true
			})
			if matched {
				return true
			}
		}
	}
	return false
}
