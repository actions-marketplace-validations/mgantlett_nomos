// Package astdebt implements Go and polyglot function seam extraction and file decomposition blueprints.
package astdebt

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
)

// MaxFunctionParams defines the maximum parameter arity before requiring a cohesive struct (#Thresholds).
const MaxFunctionParams = 8

var (
	polyglotBranchTokenRe = regexp.MustCompile(`\b(?:if|elif|elsif|for|while|switch|match|when|case|catch|except|rescue)\b`)
	rbBlockOpenTokenRe    = regexp.MustCompile(`(?:\b(?:def|if|unless|case|while|until|for|begin|class|module)\b|\bdo\b)`)
	rbBlockEndTokenRe     = regexp.MustCompile(`\bend\b`)
)

// PrescribeFunctionSeams analyzes a violating function and returns deterministic AST extraction cut-lines.
// @intent: #FunctionMetric
func PrescribeFunctionSeams(filePath string, src []byte, funcName string, funcLine int) []string {
	if len(src) == 0 {
		return nil
	}
	if strings.HasSuffix(filePath, ExtGo) {
		return prescribeGoFunctionSeams(filePath, src, funcName)
	}
	return prescribePolyglotFunctionSeams(filePath, src, funcLine)
}

// prescribeGoFunctionSeams inspects Go function parameters, nested statements, and logical branch counts.
func prescribeGoFunctionSeams(filePath string, src []byte, funcName string) []string {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, filePath, src, 0)
	if err != nil {
		return nil
	}
	fn := findGoFuncDecl(node, funcName)
	if fn == nil || fn.Body == nil {
		return nil
	}
	var seams []string
	if params := countGoFuncParams(fn); params > MaxFunctionParams {
		seams = append(seams, fmt.Sprintf("[SEAM (parameter arity %d > %d): encapsulate parameters into a cohesive domain struct]", params, MaxFunctionParams))
	}
	seams = append(seams, extractGoStatementSeams(fn, fset)...)
	if boolOps := countGoLogicalOps(fn.Body); boolOps >= 5 {
		seams = append(seams, fmt.Sprintf("[SEAM: %d compound boolean branches (&&/||) -> extract predicate helper or lookup map]", boolOps))
	}
	return seams
}

// findGoFuncDecl returns the matching top-level or receiver FuncDecl in node.
func findGoFuncDecl(node *ast.File, funcName string) *ast.FuncDecl {
	for _, decl := range node.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == funcName {
			return fn
		}
	}
	return nil
}

// countGoFuncParams returns total declared parameters in a Go function signature.
func countGoFuncParams(fn *ast.FuncDecl) int {
	if fn == nil || fn.Type == nil || fn.Type.Params == nil {
		return 0
	}
	count := 0
	for _, field := range fn.Type.Params.List {
		if len(field.Names) == 0 {
			count++
		} else {
			count += len(field.Names)
		}
	}
	return count
}

// extractGoStatementSeams identifies high-complexity or oversized top-level loops, switches, and branches.
func extractGoStatementSeams(fn *ast.FuncDecl, fset *token.FileSet) []string {
	var seams []string
	for _, stmt := range fn.Body.List {
		kind, comp := classifyGoBlockStmt(stmt)
		if kind == "" {
			continue
		}
		start, end := fset.Position(stmt.Pos()).Line, fset.Position(stmt.End()).Line
		span := end - start + 1
		if comp >= 3 || span >= 12 {
			inStr := ""
			if inputs := collectStmtInputs(stmt); len(inputs) > 0 {
				inStr = fmt.Sprintf(": inputs(%s)", strings.Join(inputs, ", "))
			}
			seams = append(seams, fmt.Sprintf("[SEAM lines %d-%d (%s, +%d complexity, %d LOC)%s -> extract pure helper]", start, end, kind, comp, span, inStr))
		}
	}
	return seams
}

// classifyGoBlockStmt categorizes a Go statement kind and counts internal decision branches.
func classifyGoBlockStmt(stmt ast.Stmt) (string, int) {
	kind := ""
	switch stmt.(type) {
	case *ast.ForStmt:
		kind = "for loop"
	case *ast.RangeStmt:
		kind = "range loop"
	case *ast.SwitchStmt, *ast.TypeSwitchStmt:
		kind = "switch dispatch"
	case *ast.IfStmt:
		kind = "conditional branch"
	default:
		return "", 0
	}
	comp := 0
	ast.Inspect(stmt, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.CaseClause, *ast.CommClause:
			comp++
		case *ast.BinaryExpr:
			if x.Op == token.LAND || x.Op == token.LOR {
				comp++
			}
		}
		return true
	})
	return kind, comp
}

// collectStmtInputs gathers up to 4 candidate local identifiers referenced inside a statement seam.
func collectStmtInputs(stmt ast.Stmt) []string {
	seen := make(map[string]bool)
	var list []string
	ast.Inspect(stmt, func(n ast.Node) bool {
		ident, ok := n.(*ast.Ident)
		if !ok || ident.Name == "_" || ident.Name == "nil" || ident.Name == "true" || ident.Name == "false" || ident.Name == "err" {
			return true
		}
		if len(ident.Name) > 1 && ident.Name[0] >= 'a' && ident.Name[0] <= 'z' && !seen[ident.Name] {
			seen[ident.Name] = true
			list = append(list, ident.Name)
		}
		return true
	})
	if len(list) > 4 {
		return list[:4]
	}
	return list
}

// countGoLogicalOps counts logical && and || operators inside a Go block body.
func countGoLogicalOps(body *ast.BlockStmt) int {
	ops := 0
	ast.Inspect(body, func(n ast.Node) bool {
		if bin, ok := n.(*ast.BinaryExpr); ok && (bin.Op == token.LAND || bin.Op == token.LOR) {
			ops++
		}
		return true
	})
	return ops
}

// prescribePolyglotFunctionSeams prescribes extraction seams for Python, Ruby, and brace functions.
func prescribePolyglotFunctionSeams(filePath string, src []byte, funcLine int) []string {
	lines := strings.Split(string(src), "\n")
	startIdx := funcLine - 1
	if startIdx < 0 || startIdx >= len(lines) {
		return nil
	}
	ext := strings.ToLower(filepath.Ext(filePath))
	bodyLines, endIdx := sliceLanguageFuncBody(ext, lines, startIdx)
	bodyText := strings.Join(bodyLines, "\n")
	var seams []string
	if params := countSignatureParamsAtLine(ext, string(src), lines, startIdx); params > MaxFunctionParams {
		seams = append(seams, fmt.Sprintf("[SEAM (parameter arity %d > %d): encapsulate parameters into a cohesive options/request type]", params, MaxFunctionParams))
	}
	seams = append(seams, extractPolyglotSubBlockSeams(bodyLines, funcLine)...)
	if boolOps := countPolyglotBooleanOps(ext, bodyText); boolOps >= 5 {
		if IsJSFamilyExt(ext) {
			seams = append(seams, fmt.Sprintf("[SEAM: %d inline fallback/ternary branches (|| / ?? / ?:) -> replace with typed default object / Object.assign]", boolOps))
		} else {
			seams = append(seams, fmt.Sprintf("[SEAM: %d inline boolean/fallback branches -> extract predicate helper or lookup table]", boolOps))
		}
	}
	if span := endIdx - startIdx + 1; span >= 20 {
		seams = append(seams, fmt.Sprintf("[SEAM lines %d-%d (%d LOC body) -> split around line %d into cohesive helper functions]", funcLine, endIdx+1, span, startIdx+span/2+1))
	}
	return seams
}

// sliceLanguageFuncBody returns lines spanning the function starting at startIdx and its final line index.
func sliceLanguageFuncBody(ext string, lines []string, startIdx int) ([]string, int) {
	if ext == ExtPy {
		sigEnd := findPyDefSignatureEnd(lines, startIdx, maxInt(strings.IndexByte(lines[startIdx], '('), 0))
		end := findPythonBlockEnd(lines, sigEnd+1, countLeadingIndentCols(lines[startIdx])) - 1
		if end < startIdx {
			end = startIdx
		}
		return lines[startIdx : end+1], end
	}
	if ext == ExtRB || ext == ExtRake {
		return sliceRubyFuncBody(lines, startIdx)
	}
	return sliceBraceFuncBody(lines, startIdx)
}

// sliceRubyFuncBody slices lines from def through matching end up to 160 lines.
func sliceRubyFuncBody(lines []string, startIdx int) ([]string, int) {
	depth, maxIdx := 0, MinInt(len(lines), startIdx+160)
	for i := startIdx; i < maxIdx; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if idx := strings.Index(trimmed, "#"); idx >= 0 {
			trimmed = trimmed[:idx]
		}
		depth += len(rbBlockOpenTokenRe.FindAllStringIndex(trimmed, -1)) - len(rbBlockEndTokenRe.FindAllStringIndex(trimmed, -1))
		if i >= startIdx && depth <= 0 {
			return lines[startIdx : i+1], i
		}
	}
	return lines[startIdx:maxIdx], maxIdx - 1
}

// sliceBraceFuncBody slices lines across balanced outer braces outside strings and // comments.
func sliceBraceFuncBody(lines []string, startIdx int) ([]string, int) {
	depth, started, maxIdx := 0, false, MinInt(len(lines), startIdx+160)
	for i := startIdx; i < maxIdx; i++ {
		for _, ch := range stripStringsAndSlashComment(lines[i]) {
			if ch == '{' {
				depth++
				started = true
			} else if ch == '}' {
				depth--
			}
		}
		if started && depth <= 0 {
			return lines[startIdx : i+1], i
		}
	}
	return lines[startIdx:maxIdx], maxIdx - 1
}

// stripStringsAndSlashComment strips quotes and // line comments for brace depth counting.
func stripStringsAndSlashComment(line string) string {
	code, comment := splitSlashLineCommentOutsideStrings(line)
	if comment != "" && strings.TrimSpace(code) == "" {
		return ""
	}
	var b strings.Builder
	quote, escaped := byte(0), false
	for i := 0; i < len(code); i++ {
		if escaped {
			escaped = false
			continue
		}
		ch := code[i]
		if quote != 0 {
			quote, escaped = stepSingleQuoteByte(ch, quote)
			continue
		}
		if ch == '"' || ch == '\'' || ch == '`' {
			quote = ch
			continue
		}
		b.WriteByte(ch)
	}
	return b.String()
}

// extractPolyglotSubBlockSeams identifies oversized branch or loop blocks inside a polyglot function.
func extractPolyglotSubBlockSeams(bodyLines []string, funcStartLine int) []string {
	var seams []string
	for i := 1; i < len(bodyLines); i++ {
		trimmed := strings.TrimSpace(bodyLines[i])
		m := polyglotBranchTokenRe.FindString(trimmed)
		if m == "" || !strings.HasPrefix(trimmed, m) {
			continue
		}
		branches, endRel := measureSubBlockExtent(bodyLines, i)
		span := endRel - i + 1
		if branches >= 3 || span >= 12 {
			seams = append(seams, fmt.Sprintf("[SEAM lines %d-%d (%s block, +%d complexity, %d LOC) -> extract pure helper]", funcStartLine+i, funcStartLine+endRel, m, branches, span))
			i = endRel
		}
	}
	return seams
}

// measureSubBlockExtent counts branches and line extent until indentation dedents or closes.
func measureSubBlockExtent(bodyLines []string, startRel int) (int, int) {
	branches := len(polyglotBranchTokenRe.FindAllStringIndex(strings.TrimSpace(bodyLines[startRel]), -1))
	endRel, baseIndent := startRel, countLeadingIndentCols(bodyLines[startRel])
	for j := startRel + 1; j < len(bodyLines); j++ {
		jt := strings.TrimSpace(bodyLines[j])
		if jt == "" {
			continue
		}
		curIndent := countLeadingIndentCols(bodyLines[j])
		if curIndent <= baseIndent && jt != "}" && jt != "end" {
			break
		}
		branches += len(polyglotBranchTokenRe.FindAllStringIndex(jt, -1))
		endRel = j
		if curIndent <= baseIndent && (jt == "}" || jt == "end") {
			break
		}
	}
	return branches, endRel
}

// countPolyglotBooleanOps tallies compound logical and nullish/ternary operators in bodyText.
func countPolyglotBooleanOps(ext, bodyText string) int {
	ops := strings.Count(bodyText, "&&") + strings.Count(bodyText, "||") + strings.Count(bodyText, "??") + strings.Count(bodyText, " ? ")
	if ext == ExtPy || ext == ExtRB || ext == ExtRake {
		ops += strings.Count(bodyText, " and ") + strings.Count(bodyText, " or ")
	}
	return ops
}

// PrescribeFileDecomposition generates a baseline delta and ranks the top 3 extraction candidates in a file.
// @intent: #FileMetric
func PrescribeFileDecomposition(root, relPath string, src []byte, currentLOC, currentCDS, maxComp, maxFuncLOC int) string {
	var parts []string
	if delta := computeGitBaselineDelta(root, relPath, currentLOC, currentCDS, maxComp, maxFuncLOC); delta != "" {
		parts = append(parts, delta)
	}
	if rep, err := AnalyzeFileDebt(relPath, src, maxComp, maxFuncLOC); err == nil && rep != nil && len(rep.Functions) > 0 {
		fns := make([]FunctionDebt, len(rep.Functions))
		copy(fns, rep.Functions)
		sort.Slice(fns, func(i, j int) bool { return (fns[i].Complexity*4 + fns[i].LOC) > (fns[j].Complexity*4 + fns[j].LOC) })
		for i := 0; i < MinInt(3, len(fns)); i++ {
			fn := fns[i]
			parts = append(parts, fmt.Sprintf("[EXTRACT %d: %s() lines %d-%d (%d LOC, complexity %d) -> move to sibling module]", i+1, fn.Name, fn.StartLine, fn.EndLine, fn.LOC, fn.Complexity))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "\n      " + strings.Join(parts, "\n      ")
}

// computeGitBaselineDelta compares current file metrics against trunk merge-base to enforce ratchets.
func computeGitBaselineDelta(root, relPath string, currentLOC, currentCDS, maxComp, maxFuncLOC int) string {
	if root == "" || relPath == "" {
		return ""
	}
	cleanRel, baseRef := filepath.ToSlash(relPath), resolveGitDefaultMergeBase(root)
	out, err := workspace.RunSafeGit(root, "show", baseRef+":"+cleanRel)
	if err != nil || len(out) == 0 {
		return ""
	}
	mainLOC, mainCDS := strings.Count(string(out), "\n")+1, 0
	if rep, dErr := AnalyzeFileDebt(cleanRel, out, maxComp, maxFuncLOC); dErr == nil && rep != nil {
		mainCDS = rep.DebtScore
	}
	shortRef := baseRef
	if len(shortRef) > 8 {
		shortRef = shortRef[:8]
	}
	msg := fmt.Sprintf("[BASELINE %s: %d LOC, %d CDS | ΔLOC %+d, ΔCDS %+d]", shortRef, mainLOC, mainCDS, currentLOC-mainLOC, currentCDS-mainCDS)
	if mainLOC > 400 || mainCDS > 45 {
		msg += fmt.Sprintf(" [RATCHET: restore ΔLOC <= 0 and ΔCDS <= 0 relative to baseline (%d LOC, %d CDS)]", mainLOC, mainCDS)
	}
	return msg
}

// resolveGitDefaultMergeBase finds the merge-base commit hash against trunk branch candidates.
func resolveGitDefaultMergeBase(root string) string {
	for _, ref := range []string{"main", "origin/main", "master", "origin/master"} {
		if out, err := workspace.RunSafeGit(root, "merge-base", "HEAD", ref); err == nil {
			if sha := strings.TrimSpace(string(out)); sha != "" {
				return sha
			}
		}
	}
	return "main"
}

// maxInt returns the larger of two integer values.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
