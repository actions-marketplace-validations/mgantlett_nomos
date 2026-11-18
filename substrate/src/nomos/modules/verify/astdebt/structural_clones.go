// Package astdebt implements Go and polyglot Type-2 structural clone detection and violation enrichment (Law 6).
package astdebt

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/treesitter"
)

// DefaultMinCloneLines defines the minimum function LOC required to evaluate Type-2 structural clones.
const DefaultMinCloneLines = 18

type cloneBlockCandidate struct {
	file      string
	startLine int
	endLine   int
	loc       int
	hash      string
}

var (
	polyglotStringLitRe = regexp.MustCompile(`(?:"[^"\\]*(?:\\.[^"\\]*)*"|'[^'\\]*(?:\\.[^'\\]*)*')`)
	polyglotNumberLitRe = regexp.MustCompile(`\b\d+(?:\.\d+)?\b`)
	polyglotSelfCallRe  = regexp.MustCompile(`\b(?:self|this)\.[A-Za-z_][A-Za-z0-9_]*\(`)
	polyglotSpaceRe     = regexp.MustCompile(`\s+`)
	polyglotCloneLangs  = makeWordSet("typescript svelte python ruby rust c cpp java kotlin csharp")
)

// DetectStructuralClones identifies Type-2 duplicated AST function structures across scanned source files.
// @intent: #CloneGroup
func DetectStructuralClones(files map[string][]byte, minLines int) []CloneGroup {
	if minLines <= 0 {
		minLines = DefaultMinCloneLines
	}
	buckets := make(map[string][]cloneBlockCandidate)
	for _, relPath := range sortedSourceKeys(files) {
		for _, c := range extractFileCloneCandidates(relPath, files[relPath], minLines) {
			buckets[c.hash] = append(buckets[c.hash], c)
		}
	}
	return buildCloneGroups(buckets)
}

// extractFileCloneCandidates dispatches Go AST or polyglot tree-sitter clone candidate extraction.
func extractFileCloneCandidates(relPath string, src []byte, minLines int) []cloneBlockCandidate {
	if strings.HasSuffix(relPath, ExtGo) {
		return extractGoCloneCandidates(relPath, src, minLines)
	}
	lang := treesitter.DetectLanguage(relPath)
	if polyglotCloneLangs[lang] {
		return extractPolyglotCloneCandidates(lang, relPath, src, minLines)
	}
	return nil
}

// extractGoCloneCandidates hashes AST skeletons for Go functions meeting minLines.
func extractGoCloneCandidates(relPath string, src []byte, minLines int) []cloneBlockCandidate {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, relPath, src, 0)
	if err != nil {
		return nil
	}
	var out []cloneBlockCandidate
	for _, decl := range node.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		start, end := fset.Position(fn.Pos()).Line, fset.Position(fn.End()).Line
		loc := end - start + 1
		if loc < minLines {
			continue
		}
		if skel := buildGoASTSkeleton(fn.Body); len(skel) >= 24 {
			out = append(out, cloneBlockCandidate{
				file: filepath.ToSlash(relPath), startLine: start, endLine: end,
				loc: loc, hash: hashSkeleton("go:" + skel),
			})
		}
	}
	return out
}

// buildGoASTSkeleton serializes normalized control and call structure inside a Go block.
func buildGoASTSkeleton(body *ast.BlockStmt) string {
	var tokens []string
	ast.Inspect(body, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		if tag := goNodeStructuralTag(n); tag != "" {
			tokens = append(tokens, tag)
		}
		return true
	})
	return strings.Join(tokens, ";")
}

// goNodeStructuralTag encodes a canonical structural token for an AST node.
func goNodeStructuralTag(n ast.Node) string {
	switch x := n.(type) {
	case *ast.IfStmt:
		return "IF"
	case *ast.ForStmt:
		return "FOR"
	case *ast.RangeStmt:
		return "RANGE"
	case *ast.SwitchStmt, *ast.TypeSwitchStmt:
		return "SWITCH"
	case *ast.CaseClause:
		return "CASE"
	case *ast.ReturnStmt:
		return fmt.Sprintf("RET:%d", len(x.Results))
	case *ast.AssignStmt:
		return fmt.Sprintf("ASN:%s", x.Tok.String())
	case *ast.BinaryExpr:
		return fmt.Sprintf("BIN:%s", x.Op.String())
	case *ast.CallExpr:
		if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
			return fmt.Sprintf("CALL:.%s/%d", sel.Sel.Name, len(x.Args))
		}
		if ident, ok := x.Fun.(*ast.Ident); ok {
			return fmt.Sprintf("CALL:%s/%d", ident.Name, len(x.Args))
		}
		return fmt.Sprintf("CALL:?/%d", len(x.Args))
	default:
		return ""
	}
}

// extractPolyglotCloneCandidates hashes normalized cross-file and sibling intra-file polyglot functions.
func extractPolyglotCloneCandidates(lang, relPath string, src []byte, minLines int) []cloneBlockCandidate {
	funcs, err := treesitter.ExtractFunctions(lang, relPath, src)
	if err != nil || len(funcs) == 0 {
		return nil
	}
	crossMin, siblingEnabled := minLines, false
	if lang != "svelte" && minLines == DefaultMinCloneLines {
		crossMin, siblingEnabled = 8, true
	}
	lines := strings.Split(string(src), "\n")
	var out []cloneBlockCandidate
	for _, fn := range funcs {
		if cand, ok := buildPolyglotFuncCloneCandidate(lang, relPath, fn, lines, crossMin, siblingEnabled); ok {
			out = append(out, cand)
		}
	}
	return out
}

// buildPolyglotFuncCloneCandidate builds a cross-file or intra-file hash candidate for fn.
func buildPolyglotFuncCloneCandidate(lang, relPath string, fn treesitter.FunctionMetric, lines []string, crossMin int, siblingEnabled bool) (cloneBlockCandidate, bool) {
	startIdx, endIdx := fn.Line-1, fn.Line-1+fn.LOC
	if startIdx < 0 || endIdx > len(lines) {
		return cloneBlockCandidate{}, false
	}
	canonRel := filepath.ToSlash(relPath)
	if fn.LOC >= crossMin {
		if skel, count := normalizePolyglotLines(lines[startIdx:endIdx], false); count > 0 && len(skel) >= 36 {
			return cloneBlockCandidate{file: canonRel, startLine: fn.Line, endLine: fn.Line + fn.LOC - 1, loc: fn.LOC, hash: hashSkeleton(lang + ":" + skel)}, true
		}
	}
	if siblingEnabled && fn.LOC >= 5 {
		if skel, count := normalizePolyglotLines(lines[startIdx:endIdx], true); count >= 4 && len(skel) >= 26 {
			return cloneBlockCandidate{file: canonRel, startLine: fn.Line, endLine: fn.Line + fn.LOC - 1, loc: fn.LOC, hash: hashSkeleton("intra:" + lang + ":" + canonRel + ":" + skel)}, true
		}
	}
	return cloneBlockCandidate{}, false
}

// normalizePolyglotLines alpha-normalizes literals and self/this calls across function body lines.
func normalizePolyglotLines(lines []string, normSelf bool) (string, int) {
	if len(lines) <= 1 {
		return "", 0
	}
	var cleaned []string
	inDoc := ""
	for i := 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || trimmed == "{" || trimmed == "}" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		if skip, nextDoc := stepPyDocDelim(trimmed, inDoc, nil); skip {
			inDoc = nextDoc
			continue
		}
		norm := polyglotStringLitRe.ReplaceAllString(trimmed, "$STR")
		norm = polyglotNumberLitRe.ReplaceAllString(norm, "$NUM")
		if normSelf {
			norm = polyglotSelfCallRe.ReplaceAllString(norm, "$SELF.(")
		}
		cleaned = append(cleaned, polyglotSpaceRe.ReplaceAllString(norm, " "))
	}
	return strings.Join(cleaned, "|"), len(cleaned)
}

// hashSkeleton computes a short deterministic SHA-256 prefix digest of a normalized skeleton.
func hashSkeleton(skel string) string {
	sum := sha256.Sum256([]byte(skel))
	return hex.EncodeToString(sum[:8])
}

// buildCloneGroups converts multi-occurrence skeleton hash buckets into sorted CloneGroups.
func buildCloneGroups(buckets map[string][]cloneBlockCandidate) []CloneGroup {
	var groups []CloneGroup
	for hash, list := range buckets {
		if len(list) < 2 {
			continue
		}
		occs := make([]CloneOccurrence, 0, len(list))
		maxLOC := 0
		for _, item := range list {
			if item.loc > maxLOC {
				maxLOC = item.loc
			}
			occs = append(occs, CloneOccurrence{File: item.file, StartLine: item.startLine, EndLine: item.endLine})
		}
		groups = append(groups, CloneGroup{Hash: hash, LineCount: maxLOC, Occurrences: occs})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Hash < groups[j].Hash })
	return groups
}

// FormatCloneViolations renders structural clone groups into actionable Law 6 violation strings.
// @intent: #CloneGroup
func FormatCloneViolations(clones []CloneGroup) []string {
	var violations []string
	for _, cg := range clones {
		if len(cg.Occurrences) < 2 {
			continue
		}
		locs := make([]string, 0, len(cg.Occurrences))
		for _, occ := range cg.Occurrences {
			locs = append(locs, fmt.Sprintf("%s:%d-%d", occ.File, occ.StartLine, occ.EndLine))
		}
		violations = append(violations, fmt.Sprintf(
			"structural AST clone [%s] (%d lines duplicated across %d locations: %s) -> [SEAM: consolidate duplicate logic into a single shared canonical function (Law 6)]",
			cg.Hash[:8], cg.LineCount, len(cg.Occurrences), strings.Join(locs, ", "),
		))
	}
	return violations
}

// EnrichCDSViolations appends file decomposition blueprints and structural clone diagnostics to CDS violations.
// @intent: #FileDebtReport
func EnrichCDSViolations(root string, metrics []FileMetric, sources map[string][]byte, thresh Thresholds, checkClones bool) []string {
	var violations []string
	for _, f := range metrics {
		if f.CDS > thresh.MaxCDS {
			msg := fmt.Sprintf("%s has cognitive debt %d (exceeds max %d)", f.Name, f.CDS, thresh.MaxCDS)
			msg += PrescribeFileDecomposition(root, f.Name, sources[f.Name], f.LOC, f.CDS, thresh.MaxComplexity, thresh.MaxFuncLOC)
			violations = append(violations, msg)
		}
	}
	if checkClones && len(sources) > 0 {
		minLines := thresh.MinCloneLines
		if minLines <= 0 {
			minLines = DefaultMinCloneLines
		}
		violations = append(violations, FormatCloneViolations(DetectStructuralClones(sources, minLines))...)
		violations = append(violations, DetectPolyglotDesignSlop(root, sources)...)
	}
	return violations
}

// EnrichFileLOCViolations appends baseline deltas and top extraction candidate functions to LOC violations.
// @intent: #FileMetric
func EnrichFileLOCViolations(root string, metrics []FileMetric, thresh Thresholds) []string {
	var violations []string
	for _, f := range metrics {
		if f.LOC > thresh.MaxFileLOC {
			msg := fmt.Sprintf("%s has %d LOC (exceeds max %d)", f.Name, f.LOC, thresh.MaxFileLOC)
			if src, err := os.ReadFile(filepath.Join(root, f.Name)); err == nil {
				msg += PrescribeFileDecomposition(root, f.Name, src, f.LOC, 0, thresh.MaxComplexity, thresh.MaxFuncLOC)
			}
			violations = append(violations, msg)
		}
	}
	return violations
}

// EnrichComplexityViolations appends AST seam cut-lines and parameter arity checks to complexity violations.
// @intent software_physics_engine_spec.md tree_sitter_ast_gate_spec.md #FunctionMetric
func EnrichComplexityViolations(root string, funcs []FunctionMetric, thresh Thresholds, checkParams bool) []string {
	var violations []string
	seenFiles := make(map[string][]byte)
	endLines := make(map[string]map[string]int)
	for _, fn := range funcs {
		src := loadCachedSourceBytes(root, fn.File, seenFiles)
		endLine := lookupFunctionEndLine(fn, src, thresh, endLines)
		span := formatFileSpan(fn.File, fn.Line, endLine)
		effMax := EffectiveMaxComplexity(fn.File, thresh.MaxComplexity)
		if fn.Complexity > effMax {
			violations = append(violations, formatComplexitySeamMsg(fn, src, span, fmt.Sprintf("cyclomatic complexity %d exceeds max %d", fn.Complexity, effMax), fmt.Sprintf("extract >=%d branch(es)", fn.Complexity-effMax)))
		}
		if fn.LOC > thresh.MaxFuncLOC {
			violations = append(violations, formatComplexitySeamMsg(fn, src, span, fmt.Sprintf("LOC %d exceeds max %d", fn.LOC, thresh.MaxFuncLOC), fmt.Sprintf("extract >=%d line(s)", fn.LOC-thresh.MaxFuncLOC)))
		}
	}
	if checkParams {
		maxParams := thresh.MaxFuncParams
		if maxParams <= 0 {
			maxParams = MaxFunctionParams
		}
		for rel, src := range seenFiles {
			violations = append(violations, EvaluateParameterArityViolations(rel, src, maxParams)...)
			violations = append(violations, EvaluatePolyglotNestingViolations(rel, src, MaxFunctionNesting)...)
			violations = append(violations, EvaluatePolyglotCognitiveViolations(rel, src, DefaultMaxCognitiveComplexity(rel))...)
		}
	}
	return violations
}

// formatComplexitySeamMsg appends seam blueprints and 1-turn extraction recipe onto a complexity/LOC violation.
func formatComplexitySeamMsg(fn FunctionMetric, src []byte, span, reason, recipeDelta string) string {
	seams := append(PrescribeFunctionSeams(fn.File, src, fn.Name, fn.Line), fmt.Sprintf("[1-TURN RECIPE: %s from %s (%s) into helper %sStep()]", recipeDelta, fn.Name, span, fn.Name))
	return fmt.Sprintf("%s (%s) %s -> %s", span, fn.Name, reason, strings.Join(seams, " "))
}

// lookupFunctionEndLine caches analyzed file end lines to render exact fn span ranges.
func lookupFunctionEndLine(fn FunctionMetric, src []byte, thresh Thresholds, cache map[string]map[string]int) int {
	byName, ok := cache[fn.File]
	if !ok {
		byName = make(map[string]int)
		if rep, err := AnalyzeFileDebt(fn.File, src, thresh.MaxComplexity, thresh.MaxFuncLOC); err == nil && rep != nil {
			for _, df := range rep.Functions {
				if df.EndLine > byName[df.Name] {
					byName[df.Name] = df.EndLine
				}
			}
		}
		cache[fn.File] = byName
	}
	if end := byName[fn.Name]; end >= fn.Line {
		return end
	}
	if fn.LOC > 0 {
		return fn.Line + fn.LOC - 1
	}
	return fn.Line
}
