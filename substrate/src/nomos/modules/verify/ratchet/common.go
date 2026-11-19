// Package ratchet implements baseline AST delta ratcheting, upstream surgical ratchets, and shared source metric collectors.
package ratchet

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/astdebt"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/treesitter"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

const maxSatelliteMicroFileLOC = 90

var canonicalStructFileNames = map[string]bool{
	"doc.go": true, "main.go": true, "constants.go": true, "types.go": true,
	"errors.go": true, "mod.rs": true, "lib.rs": true, "__init__.py": true,
	"index.ts": true, "index.js": true,
}

// IsPolyglotTargetFile reports whether path is a non-test supported source file across all 11 languages.
func IsPolyglotTargetFile(path string) bool {
	if vscope.IsExcludedPolyglotDir(filepath.ToSlash(path)) {
		return false
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case astdebt.ExtGo:
		return !strings.HasSuffix(path, astdebt.SuffixGoTest)
	case astdebt.ExtTS, astdebt.ExtTSX, astdebt.ExtJS, astdebt.ExtJSX, astdebt.ExtMJS, astdebt.ExtCJS:
		return !vscope.IsJSTestOrDeclFile(path)
	case astdebt.ExtSvelte:
		return true
	case astdebt.ExtPy:
		return !strings.HasPrefix(filepath.Base(path), astdebt.PrefixPyTest) && !strings.HasSuffix(path, astdebt.SuffixPyTest)
	case astdebt.ExtRB, astdebt.ExtRake:
		return !vscope.IsRubyTestFile(path)
	case astdebt.ExtRS, astdebt.ExtC, astdebt.ExtH, astdebt.ExtCPP, astdebt.ExtCC, astdebt.ExtCXX, astdebt.ExtHPP, astdebt.ExtJava, astdebt.ExtKT, astdebt.ExtKTS, astdebt.ExtCS:
		return !vscope.IsNativeTestFile(path)
	default:
		return false
	}
}

// ResolveThresholds merges statutory DefaultThresholds with active nomos.yaml overrides.
func ResolveThresholds(ctx *vscope.VerifyContext) astdebt.Thresholds {
	out := astdebt.DefaultThresholds()
	out.MaxMagicStringDuplication = 10
	cfg := vscope.ResolveVerifyConfig(ctx)
	if cfg == nil {
		return out
	}
	ct := cfg.Governance.QualityGates.Thresholds
	if ct.MaxFileLOC > 0 {
		out.MaxFileLOC = ct.MaxFileLOC
	}
	if ct.MaxFuncLOC > 0 {
		out.MaxFuncLOC = ct.MaxFuncLOC
	}
	if ct.MaxCyclomaticComplexity > 0 {
		out.MaxComplexity = ct.MaxCyclomaticComplexity
	}
	if ct.MaxCognitiveDebtScore > 0 {
		out.MaxCDS = ct.MaxCognitiveDebtScore
	}
	if ct.MinCommentDensityPct >= 0 {
		out.MinDocstringDensity = ct.MinCommentDensityPct
	}
	if ct.MinTestCoveragePct >= 0 {
		out.MinCoverage = float64(ct.MinTestCoveragePct)
	}
	if ct.MinCloneLines > 0 {
		out.MinCloneLines = ct.MinCloneLines
	}
	if ct.MaxFuncParams > 0 {
		out.MaxFuncParams = ct.MaxFuncParams
	}
	if ct.MaxMagicStringDuplication > 0 {
		out.MaxMagicStringDuplication = ct.MaxMagicStringDuplication
	}
	if ct.MaxPackageFiles > 0 {
		out.MaxPackageFiles = ct.MaxPackageFiles
	}
	return out
}

// CollectFunctionMetrics extracts Go function LOC and cyclomatic complexity from a parsed AST file.
func CollectFunctionMetrics(node *ast.File, fset *token.FileSet, relPath string, out *[]astdebt.FunctionMetric) {
	for _, decl := range node.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		start := fset.Position(fn.Pos()).Line
		end := fset.Position(fn.End()).Line
		comp := astdebt.CalculateCyclomaticComplexity(fn)
		*out = append(*out, astdebt.FunctionMetric{
			File:       relPath,
			Line:       start,
			Name:       fn.Name.Name,
			Complexity: comp,
			LOC:        end - start + 1,
		})
	}
}

// CollectPolyglotComplexity reads path and extracts Tree-sitter function metrics.
func CollectPolyglotComplexity(path, rel string, out *[]astdebt.FunctionMetric) {
	if content, err := os.ReadFile(path); err == nil {
		CollectPolyglotComplexityFromBytes(path, rel, content, out)
	}
}

// CollectPolyglotComplexityFromBytes extracts Tree-sitter function metrics from in-memory source bytes.
func CollectPolyglotComplexityFromBytes(path, rel string, content []byte, out *[]astdebt.FunctionMetric) {
	engine, err := treesitter.GetDefaultEngine()
	if err != nil {
		return
	}
	rep, err := engine.ParseAndEvaluate(context.Background(), path, content)
	if err != nil || rep == nil {
		return
	}
	for _, fn := range rep.Functions {
		*out = append(*out, astdebt.FunctionMetric{
			File:       rel,
			Line:       fn.Line,
			Name:       fn.Name,
			Complexity: fn.Complexity,
			LOC:        fn.LOC,
		})
	}
}

// RelativeOrFullPath returns slash-normalized path relative to base, falling back to target on error.
func RelativeOrFullPath(base, target string) string {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return target
	}
	return filepath.ToSlash(rel)
}

// FindSubstrateSrc locates the canonical substrate source root inside root.
func FindSubstrateSrc(root string) string {
	for _, c := range []string{
		filepath.Join(root, "substrate"),
		filepath.Join(root, "substrate", "src", "nomos"),
		filepath.Join(root, "src", "nomos"),
	} {
		if fi, err := os.Stat(c); err == nil && fi.IsDir() {
			return c
		}
	}
	return root
}

// NonEmptyLines splits raw on newlines and returns trimmed non-empty lines.
func NonEmptyLines(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// SortedSetKeys returns sorted string keys from a boolean map set.
func SortedSetKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k, v := range m {
		if v {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// MaxInt returns the larger of two integers.
func MaxInt(a, b int) int {
	if b > a {
		return b
	}
	return a
}

// CountBytesLines counts lines in data including any unterminated trailing line.
func CountBytesLines(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	lines := bytes.Count(data, []byte{'\n'})
	if data[len(data)-1] != '\n' {
		lines++
	}
	return lines
}

// IndexBaselineFunctionDebts indexes baseline function debt entries by function name.
func IndexBaselineFunctionDebts(funcs []astdebt.FunctionDebt) map[string]astdebt.FunctionDebt {
	byName := make(map[string]astdebt.FunctionDebt, len(funcs))
	for _, bfn := range funcs {
		prev, exists := byName[bfn.Name]
		if !exists {
			byName[bfn.Name] = bfn
			continue
		}
		prev.LOC = MaxInt(prev.LOC, bfn.LOC)
		prev.Complexity = MaxInt(prev.Complexity, bfn.Complexity)
		byName[bfn.Name] = prev
	}
	return byName
}

// EvaluateNetNewMicroFileViolation flags net-new micro-files in crowded packages when a sibling can absorb them.
func EvaluateNetNewMicroFileViolation(ctx *vscope.VerifyContext, root, absPath string, maxFiles, maxFileLOC, fileLOC int) string {
	if maxFiles <= 0 {
		maxFiles = astdebt.DefaultThresholds().MaxPackageFiles
	}
	if maxFiles < 10 || fileLOC <= 0 || fileLOC >= maxSatelliteMicroFileLOC || !isNetNewMicroFileCandidate(ctx, root, absPath) {
		return ""
	}
	curCount := countDirNonTestSourceFiles(filepath.Dir(absPath))
	if curCount < (maxFiles*65+99)/100 {
		return ""
	}
	bestName, bestLOC := findSmallestReceptiveSibling(absPath, maxFileLOC, fileLOC)
	if bestName == "" {
		return ""
	}
	relSlash := RelativeOrFullPath(root, absPath)
	return fmt.Sprintf("%s (%d LOC) is a net-new micro-file fragment in high-fanout package %s (%d/%d files) -> [1-TURN RECIPE: fold %s into cohesive sibling %s (%d LOC) or extract a sub-package (Law 6)]",
		relSlash, fileLOC, filepath.ToSlash(filepath.Dir(relSlash)), curCount, maxFiles, filepath.Base(absPath), bestName, bestLOC)
}

func isNetNewMicroFileCandidate(ctx *vscope.VerifyContext, root, absPath string) bool {
	if ctx != nil && (ctx.AllFiles || ctx.IsBaseline || IsUpstreamSurgicalRatchet(ctx)) {
		return false
	}
	ext := filepath.Ext(absPath)
	if strings.EqualFold(ext, astdebt.ExtSvelte) || canonicalStructFileNames[strings.ToLower(filepath.Base(absPath))] {
		return false
	}
	if strings.EqualFold(ext, astdebt.ExtGo) {
		if raw, err := os.ReadFile(absPath); err == nil && hasLeadingGoBuildTag(raw) {
			return false
		}
	}
	gitRoot := resolveGitTreeRootForPath(ctx, root, absPath)
	if gitRoot == "" || resolveBaselineCommitRef(ctx, gitRoot) == "" {
		return false
	}
	_, existed := LoadBaselineFileBytes(ctx, root, absPath)
	return !existed
}
