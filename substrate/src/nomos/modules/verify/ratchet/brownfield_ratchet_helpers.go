// Package ratchet implements CDS, clone, shim, docstring, and error-hygiene ratchets for brownfield mode.
package ratchet

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/astdebt"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/treesitter"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

var (
	lineNumberStripRe    = regexp.MustCompile(`:[0-9]+(?:-[0-9]+)?`)
	embeddedCountStripRe = regexp.MustCompile(`\b[0-9]+\b`)
)

// EvaluateRatchetedCDSViolations evaluates cognitive debt, clone, and design-slop ratchets across target metrics.
func EvaluateRatchetedCDSViolations(ctx *vscope.VerifyContext, root string, metrics []astdebt.FileMetric, sources map[string][]byte, thresh astdebt.Thresholds) []string {
	if !vscope.IsBrownfieldRatchetActive(ctx) {
		return evaluateStrictNonWorktreeOrDefaultCDS(ctx, root, metrics, sources, thresh)
	}
	var newFileMetrics []astdebt.FileMetric
	changedSources := make(map[string][]byte)
	baseSources := make(map[string][]byte)
	var violations []string

	for _, m := range metrics {
		absPath := filepath.Join(root, m.Name)
		baseBytes, ok := LoadBaselineFileBytes(ctx, root, absPath)
		if !ok {
			newFileMetrics = append(newFileMetrics, m)
			changedSources[m.Name] = sources[m.Name]
			continue
		}
		if !bytes.Equal(sources[m.Name], baseBytes) {
			changedSources[m.Name] = sources[m.Name]
			baseSources[m.Name] = baseBytes
		}
		if IsUpstreamSurgicalRatchet(ctx) {
			continue
		}
		if recipe := evaluateSingleRatchetedCDS(m, absPath, sources[m.Name], baseBytes, thresh); recipe != "" {
			violations = append(violations, recipe)
		}
	}

	violations = append(violations, astdebt.EnrichCDSViolations(root, newFileMetrics, sources, thresh, false)...)
	violations = append(violations, EvaluateNetNewBrownfieldClonesAndSlop(ctx, root, changedSources, baseSources, thresh)...)
	return violations
}

func evaluateSingleRatchetedCDS(m astdebt.FileMetric, absPath string, curSrc, baseBytes []byte, thresh astdebt.Thresholds) string {
	baseRep, err := astdebt.AnalyzeFileDebt(absPath, baseBytes, thresh.MaxComplexity, thresh.MaxFuncLOC)
	if err != nil || baseRep == nil {
		if m.CDS > thresh.MaxCDS {
			return fmt.Sprintf("%s has cognitive debt %d (exceeds max %d)", m.Name, m.CDS, thresh.MaxCDS)
		}
		return ""
	}
	ceil := MaxInt(thresh.MaxCDS, baseRep.DebtScore)
	if m.CDS <= ceil {
		return ""
	}
	return PrescribeRatchetedCDSRecipe(m.Name, curSrc, baseBytes, thresh)
}

// PrescribeRatchetedCDSRecipe pinpoints the top offending function delta causing a brownfield dCDS breach (@intent intent.md software_physics_engine_spec.md tree_sitter_ast_gate_spec.md #FileDebtReport).
func PrescribeRatchetedCDSRecipe(relPath string, curSrc, baseSrc []byte, thresh astdebt.Thresholds) string {
	curRep, cErr := astdebt.AnalyzeFileDebt(relPath, curSrc, thresh.MaxComplexity, thresh.MaxFuncLOC)
	baseRep, bErr := astdebt.AnalyzeFileDebt(relPath, baseSrc, thresh.MaxComplexity, thresh.MaxFuncLOC)
	if cErr != nil || curRep == nil || bErr != nil || baseRep == nil {
		return fmt.Sprintf("%s exceeded brownfield cognitive debt ceiling", relPath)
	}
	ceil := MaxInt(thresh.MaxCDS, baseRep.DebtScore)
	dCDS := curRep.DebtScore - ceil
	if dCDS < 1 {
		dCDS = 1
	}
	header := fmt.Sprintf("%s cognitive debt %d exceeds brownfield ceiling %d (dCDS %+d, baseline %d)", relPath, curRep.DebtScore, ceil, curRep.DebtScore-baseRep.DebtScore, baseRep.DebtScore)
	topFn, baseFn, fnDelta := selectTopOffendingFunctionDelta(curRep.Functions, baseRep.Functions)
	if topFn.Name == "" {
		return header
	}
	needBranches := (dCDS + 9) / 10
	needLines := (dCDS + 1) / 2
	return fmt.Sprintf("%s -> [1-TURN RECIPE: %s (%s:%d-%d) added %+d CDS (LOC %d->%d, V(G) %d->%d); extract >=%d line(s) or >=%d branch(es) into helper %sStep() to restore dCDS <= 0]",
		header, topFn.Name, relPath, topFn.StartLine, topFn.EndLine, fnDelta, baseFn.LOC, topFn.LOC, baseFn.Complexity, topFn.Complexity, needLines, needBranches, topFn.Name)
}

func selectTopOffendingFunctionDelta(curFns, baseFns []astdebt.FunctionDebt) (astdebt.FunctionDebt, astdebt.FunctionDebt, int) {
	baseMap := IndexBaselineFunctionDebts(baseFns)
	var bestCur, bestBase astdebt.FunctionDebt
	bestDelta := 0
	for _, fn := range curFns {
		bfn := baseMap[fn.Name]
		baseScore := astdebt.CalculateFunctionDebt(bfn.LOC, bfn.Complexity, astdebt.DefaultThresholds().MaxFuncLOC, astdebt.DefaultThresholds().MaxComplexity)
		if bfn.DebtScore > 0 {
			baseScore = bfn.DebtScore
		}
		delta := fn.DebtScore - baseScore
		if delta > bestDelta || (bestCur.Name == "" && fn.DebtScore > 0) {
			bestCur = fn
			bestBase = bfn
			bestDelta = delta
		}
	}
	return bestCur, bestBase, bestDelta
}

func detectNetNewCloneViolations(changedSources, baseSources map[string][]byte, thresh astdebt.Thresholds) []string {
	minLines := thresh.MinCloneLines
	if minLines <= 0 {
		minLines = astdebt.DefaultMinCloneLines
	}
	baseHashes := make(map[string]bool)
	for _, cg := range astdebt.DetectStructuralClones(baseSources, minLines) {
		baseHashes[cg.Hash] = true
	}
	var netNew []astdebt.CloneGroup
	for _, cg := range astdebt.DetectStructuralClones(changedSources, minLines) {
		if !baseHashes[cg.Hash] {
			netNew = append(netNew, cg)
		}
	}
	return astdebt.FormatCloneViolations(netNew)
}

// FilterNetNewLineViolations strips pre-existing baseline line violations using normalized multiset subtraction.
func FilterNetNewLineViolations(ctx *vscope.VerifyContext, root, absPath string, curViolations []string, scanBase func(baseBytes []byte) []string) []string {
	if len(curViolations) == 0 || !vscope.IsBrownfieldRatchetActive(ctx) {
		return curViolations
	}
	baseBytes, ok := LoadBaselineFileBytes(ctx, root, absPath)
	if !ok {
		return curViolations
	}
	baseCounts := make(map[string]int)
	for _, v := range scanBase(baseBytes) {
		baseCounts[normalizeViolationKey(v)]++
	}
	var netNew []string
	for _, v := range curViolations {
		if isBrownfieldModuleCapViolation(v) {
			continue
		}
		key := normalizeViolationKey(v)
		if baseCounts[key] > 0 {
			baseCounts[key]--
			continue
		}
		netNew = append(netNew, v)
	}
	return netNew
}

func isBrownfieldModuleCapViolation(v string) bool {
	return strings.Contains(v, "multi-caller one-line return micro-helpers") ||
		strings.Contains(v, "repeats empty mutable default") ||
		strings.Contains(v, "repeats 'if not isinstance(...): return") ||
		strings.Contains(v, "repeats 'if (typeof ")
}

func normalizeViolationKey(v string) string {
	stripped := lineNumberStripRe.ReplaceAllString(v, "")
	return embeddedCountStripRe.ReplaceAllString(stripped, "N")
}

// ScanTempBaselineViolations materializes baseBytes in a temporary file for path-based baseline scanners.
func ScanTempBaselineViolations(absPath string, baseBytes []byte, scanPath func(tempPath string) []string) []string {
	tmpFile, err := os.CreateTemp("", "nomos-base-*"+filepath.Ext(absPath))
	if err != nil {
		return nil
	}
	tmpPath := tmpFile.Name()
	defer func() {
		if rErr := os.Remove(tmpPath); rErr != nil && !os.IsNotExist(rErr) {
			return
		}
	}()
	if _, wErr := tmpFile.Write(baseBytes); wErr != nil {
		_ = tmpFile.Close()
		return nil
	}
	_ = tmpFile.Close()
	return scanPath(tmpPath)
}

// CountRatchetedDocstringsForFile evaluates exported docstring coverage excluding pre-existing undocumented baseline symbols.
func CountRatchetedDocstringsForFile(ctx *vscope.VerifyContext, srcDir, path, rel string) (int, int, []string, bool) {
	if !vscope.IsBrownfieldRatchetActive(ctx) {
		return 0, 0, nil, false
	}
	baseBytes, ok := LoadBaselineFileBytes(ctx, srcDir, path)
	if !ok {
		return 0, 0, nil, false
	}
	baseExportedDoc := extractExportedDocMap(path, baseBytes)
	curBytes, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, nil, true
	}
	curExportedDoc := extractExportedDocMap(path, curBytes)
	var total, documented int
	var missing []string
	for name, curDoc := range curExportedDoc {
		baseDoc, existed := baseExportedDoc[name]
		if existed && !baseDoc {
			continue
		}
		total++
		if curDoc {
			documented++
		} else {
			missing = append(missing, fmt.Sprintf("%s (%s)", rel, name))
		}
	}
	return total, documented, missing, true
}

func extractExportedDocMap(path string, content []byte) map[string]bool {
	res := make(map[string]bool)
	if strings.HasSuffix(path, ".go") {
		fset := token.NewFileSet()
		node, err := parser.ParseFile(fset, path, content, parser.ParseComments)
		if err != nil {
			return res
		}
		for _, decl := range node.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.IsExported() {
				res[fn.Name.Name] = fn.Doc != nil && len(fn.Doc.List) > 0
			}
		}
		return res
	}
	engine, err := treesitter.GetDefaultEngine()
	if err != nil {
		return res
	}
	rep, pErr := engine.ParseAndEvaluate(context.Background(), path, content)
	if pErr != nil || rep == nil {
		return res
	}
	for _, fn := range rep.Functions {
		res[fn.Name] = fn.Documented
	}
	return res
}

// CollectBaselineStringLiterals populates baseCounts with string literals from baseline source bytes.
func CollectBaselineStringLiterals(
	ctx *vscope.VerifyContext,
	targetDir, path string,
	fset *token.FileSet,
	baseCounts map[string]map[string]int,
	collectPolyglot func(path string, data []byte, counts map[string]map[string]int),
	collectGo func(fset *token.FileSet, path string, src any, counts map[string]map[string]int),
) {
	baseBytes, ok := LoadBaselineFileBytes(ctx, targetDir, path)
	if !ok {
		return
	}
	if !strings.HasSuffix(path, ".go") {
		collectPolyglot(path, baseBytes, baseCounts)
		return
	}
	collectGo(fset, path, baseBytes, baseCounts)
}

// FilterRatchetedForbiddenLiterals filters forbidden literal findings against baseline occurrences.
func FilterRatchetedForbiddenLiterals(
	ctx *vscope.VerifyContext,
	root, path string,
	patterns, cur []string,
	scanFile func(root, path string, patterns []string) []string,
) []string {
	rel, rErr := filepath.Rel(root, path)
	if rErr != nil {
		rel = path
	}
	return FilterNetNewLineViolations(ctx, root, path, cur, func(baseBytes []byte) []string {
		return ScanTempBaselineViolations(path, baseBytes, func(tmpPath string) []string {
			raw := scanFile(filepath.Dir(tmpPath), tmpPath, patterns)
			tmpBase := filepath.Base(tmpPath)
			for i, item := range raw {
				raw[i] = strings.Replace(item, tmpBase, rel, 1)
			}
			return raw
		})
	})
}

func evaluateStrictNonWorktreeOrDefaultCDS(ctx *vscope.VerifyContext, root string, metrics []astdebt.FileMetric, sources map[string][]byte, thresh astdebt.Thresholds) []string {
	if vscope.IsWorktreeContext(ctx, root) {
		return astdebt.EnrichCDSViolations(root, metrics, sources, thresh, true)
	}
	if ctx != nil && !ctx.IsBaseline && !ctx.AllFiles && vscope.HasGitMetadata(root) {
		if modified := vscope.GetModifiedFilesInScope(ctx, root); len(modified) > 0 {
			subMetrics, subSources := subsetMetricsAndSources(metrics, sources, modified)
			return astdebt.EnrichCDSViolations(root, subMetrics, subSources, thresh, true)
		}
	}
	return astdebt.EnrichCDSViolations(root, metrics, sources, thresh, false)
}

// EvaluateNetNewBrownfieldClonesAndSlop detects net-new structural clones and polyglot design slop on modified files.
func EvaluateNetNewBrownfieldClonesAndSlop(ctx *vscope.VerifyContext, root string, changedSources, baseSources map[string][]byte, thresh astdebt.Thresholds) []string {
	if len(changedSources) == 0 || IsUpstreamSurgicalRatchet(ctx) || (ctx != nil && (ctx.IsBaseline || ctx.AllFiles)) {
		return nil
	}
	violations := detectNetNewCloneViolations(changedSources, baseSources, thresh)
	rels := make([]string, 0, len(changedSources))
	for rel := range changedSources {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		if !isBrownfieldDesignSlopTarget(rel) {
			continue
		}
		cur := astdebt.DetectPolyglotDesignSlop(root, map[string][]byte{rel: changedSources[rel]})
		filtered := FilterNetNewLineViolations(ctx, root, filepath.Join(root, rel), cur, func(baseBytes []byte) []string {
			return astdebt.DetectPolyglotDesignSlop(root, map[string][]byte{rel: baseBytes})
		})
		violations = append(violations, filtered...)
	}
	return violations
}

func isBrownfieldDesignSlopTarget(rel string) bool {
	ext := filepath.Ext(rel)
	if strings.EqualFold(ext, astdebt.ExtGo) || strings.EqualFold(ext, astdebt.ExtSvelte) {
		return false
	}
	return IsPolyglotTargetFile(rel)
}

func subsetMetricsAndSources(metrics []astdebt.FileMetric, sources map[string][]byte, modified []string) ([]astdebt.FileMetric, map[string][]byte) {
	modSet := buildModifiedRelSet(modified)
	var subMetrics []astdebt.FileMetric
	for _, m := range metrics {
		if modSet[filepath.ToSlash(filepath.Clean(m.Name))] {
			subMetrics = append(subMetrics, m)
		}
	}
	subSources := make(map[string][]byte, len(subMetrics))
	for k, v := range sources {
		if modSet[filepath.ToSlash(filepath.Clean(k))] {
			subSources[k] = v
		}
	}
	return subMetrics, subSources
}
