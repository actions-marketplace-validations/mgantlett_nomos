// Package astdebt enforces universal Python anti-slop idiom checks: Go-style (val, err) tuple returns,
// empty Exception hierarchy sprawl, and redundant if/return boolean ceremony (Law 2 & Law 6).
package astdebt

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	minGoErrTupleReturns = 2
	minEmptyExceptions   = 3
)

var (
	pyExceptionClassRe = regexp.MustCompile(`^(\s*)class\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(([^)]+)\)\s*:`)
	pyIfBoolHeaderRe   = regexp.MustCompile(`^if\s+(.+?)\s*:\s*(.*)$`)
)

type pyScopedFunc struct {
	name      string
	indent    int
	startLine int
	bodyLines []string
}

func scanPythonIdiomSlop(relPath, text string) []string {
	lines := strings.Split(text, "\n")
	funcs := collectDirectPythonFuncs(lines)
	var out []string
	out = append(out, scanGoStyleErrorTupleReturns(relPath, funcs)...)
	out = append(out, scanEmptyExceptionSprawl(relPath, lines)...)
	out = append(out, scanRedundantBoolReturns(relPath, funcs)...)
	out = append(out, scanPythonExtraIdioms(relPath, lines, funcs)...)
	return out
}

func collectDirectPythonFuncs(lines []string) []pyScopedFunc {
	var out []pyScopedFunc
	for i := 0; i < len(lines); i++ {
		m := pyAnyDefRe.FindStringSubmatch(lines[i])
		if len(m) != 3 {
			continue
		}
		indent := countLeadingIndentCols(m[1])
		end := findPythonBlockEnd(lines, i+1, indent)
		direct := sliceDirectFuncBody(lines[i+1:end], indent)
		out = append(out, pyScopedFunc{
			name:      m[2],
			indent:    indent,
			startLine: i + 1,
			bodyLines: direct,
		})
	}
	return out
}

func sliceDirectFuncBody(body []string, funcIndent int) []string {
	out := make([]string, len(body))
	for i := 0; i < len(body); i++ {
		trimmed := strings.TrimSpace(body[i])
		if m := pyAnyDefRe.FindStringSubmatch(trimmed); len(m) == 3 {
			subIndent := countLeadingIndentCols(body[i])
			if subIndent > funcIndent {
				end := findPythonBlockEnd(body, i+1, subIndent)
				for k := i; k < end; k++ {
					out[k] = ""
				}
				i = end - 1
				continue
			}
		}
		out[i] = body[i]
	}
	return out
}

func scanGoStyleErrorTupleReturns(relPath string, funcs []pyScopedFunc) []string {
	var out []string
	for _, fn := range funcs {
		noneCount, noneBalanced := countComplementarySentinelReturns(fn.bodyLines, "None", "")
		boolCount, boolBalanced := countComplementarySentinelReturns(fn.bodyLines, "False", "True")
		total := pickSentinelTupleCount(noneCount, noneBalanced, boolCount, boolBalanced)
		if total >= minGoErrTupleReturns {
			out = append(out, fmt.Sprintf(
				"%s:%d: function %s uses Go-style (value, err) tuple returns or (ok, value) status pairs (%d branches); raise a typed exception or return value/None directly",
				relPath, fn.startLine, fn.name, total,
			))
		}
	}
	return out
}

func pickSentinelTupleCount(noneCount int, noneBalanced bool, boolCount int, boolBalanced bool) int {
	if noneBalanced && noneCount >= minGoErrTupleReturns {
		return noneCount
	}
	if boolBalanced && boolCount >= minGoErrTupleReturns {
		return boolCount
	}
	return 0
}

func countComplementarySentinelReturns(body []string, failTok, okTok string) (int, bool) {
	lhsHits, rhsHits := 0, 0
	for _, raw := range body {
		trimmed := strings.TrimSpace(raw)
		if !strings.HasPrefix(trimmed, "return ") {
			continue
		}
		expr := unwrapOuterParens(stripPythonComment(strings.TrimSpace(strings.TrimPrefix(trimmed, "return"))))
		lhs, rhs, ok := splitTopLevelTwoTuple(expr)
		if ok {
			dFail, dOk := tallySentinelTuple(lhs, rhs, failTok, okTok)
			lhsHits += dFail
			rhsHits += dOk
		}
	}
	return lhsHits + rhsHits, lhsHits > 0 && rhsHits > 0
}

func tallySentinelTuple(lhs, rhs, failTok, okTok string) (int, int) {
	if okTok == "" {
		if lhs == failTok && rhs != failTok {
			return 1, 0
		}
		if rhs == failTok && lhs != failTok {
			return 0, 1
		}
		return 0, 0
	}
	if lhs == failTok || rhs == failTok {
		return 1, 0
	}
	if lhs == okTok || rhs == okTok {
		return 0, 1
	}
	return 0, 0
}

func stripPythonComment(expr string) string {
	quote, escaped := byte(0), false
	for i := 0; i < len(expr); i++ {
		if escaped {
			escaped = false
			continue
		}
		ch := expr[i]
		if quote != 0 {
			quote, escaped = stepSingleQuoteByte(ch, quote)
			continue
		}
		if ch == '#' {
			return strings.TrimSpace(expr[:i])
		}
		if ch == '\'' || ch == '"' {
			quote = ch
		}
	}
	return strings.TrimSpace(expr)
}

func scanEmptyExceptionSprawl(relPath string, lines []string) []string {
	emptyCount := 0
	firstLine := 0
	for i := 0; i < len(lines); i++ {
		m := pyExceptionClassRe.FindStringSubmatch(lines[i])
		if len(m) != 4 || !isExceptionBaseList(m[3]) {
			continue
		}
		indent := countLeadingIndentCols(m[1])
		end := findPythonBlockEnd(lines, i+1, indent)
		if isEmptyPythonClassBody(lines[i+1 : end]) {
			emptyCount++
			if firstLine == 0 {
				firstLine = i + 1
			}
		}
		i = end - 1
	}
	if emptyCount < minEmptyExceptions {
		return nil
	}
	return []string{fmt.Sprintf(
		"%s:%d: module declares %d empty Exception subclasses; consolidate into 1-2 domain exceptions with an error code/kind attribute instead of empty class hierarchies",
		relPath, firstLine, emptyCount,
	)}
}

func isExceptionBaseList(bases string) bool {
	for _, raw := range strings.Split(bases, ",") {
		base := strings.TrimSpace(raw)
		if strings.HasSuffix(base, "Error") || strings.HasSuffix(base, "Exception") {
			return true
		}
	}
	return false
}

func extractPythonExecLines(body []string) []string {
	execLines, hasFossil := extractPythonExecBodyInfo(body)
	if hasFossil && len(execLines) == 0 {
		return nil
	}
	return execLines
}

func isEmptyPythonClassBody(body []string) bool {
	execLines := extractPythonExecLines(body)
	for _, stmt := range execLines {
		cleaned := stripPythonComment(stmt)
		if cleaned != "" && cleaned != "pass" && cleaned != "..." {
			return false
		}
	}
	return true
}

func scanRedundantBoolReturns(relPath string, funcs []pyScopedFunc) []string {
	var out []string
	for _, fn := range funcs {
		out = append(out, scanFuncRedundantBoolReturns(relPath, fn)...)
	}
	return out
}

func scanFuncRedundantBoolReturns(relPath string, fn pyScopedFunc) []string {
	var out []string
	body := fn.bodyLines
	for i := 0; i < len(body); i++ {
		ifIndent := countLeadingIndentCols(body[i])
		trimmed := strings.TrimSpace(body[i])
		m := pyIfBoolHeaderRe.FindStringSubmatch(trimmed)
		if len(m) != 3 {
			continue
		}
		firstRet, bodyEnd := extractIfSingleBoolReturn(body, i, ifIndent, strings.TrimSpace(m[2]))
		if firstRet == "" {
			continue
		}
		if matchesOppositeFollowingReturn(body, bodyEnd, ifIndent, firstRet) {
			out = append(out, fmt.Sprintf(
				"%s:%d: redundant 'if ...: return %s' before opposite boolean return; return bool(<cond>) or not (<cond>) directly",
				relPath, fn.startLine+i+1, firstRet,
			))
		}
	}
	return out
}

func extractIfSingleBoolReturn(body []string, idx, ifIndent int, inlineTail string) (string, int) {
	if inlineTail != "" && !strings.HasPrefix(inlineTail, "#") {
		return parseReturnBoolLiteral(inlineTail), idx + 1
	}
	end := findPythonBlockEnd(body, idx+1, ifIndent)
	execLines := extractPythonExecLines(body[idx+1 : end])
	if len(execLines) != 1 {
		return "", end
	}
	return parseReturnBoolLiteral(execLines[0]), end
}

func matchesOppositeFollowingReturn(body []string, startIdx, ifIndent int, firstRet string) bool {
	want := oppositeBoolLiteral(firstRet)
	for j := startIdx; j < len(body); j++ {
		t := strings.TrimSpace(body[j])
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		col := countLeadingIndentCols(body[j])
		if col < ifIndent {
			return false
		}
		if col > ifIndent {
			continue
		}
		if strings.HasPrefix(t, "elif ") {
			return false
		}
		if strings.HasPrefix(t, "else:") {
			tail := strings.TrimSpace(strings.TrimPrefix(t, "else:"))
			return matchesBoolReturnOrExpr(body, j, ifIndent, tail, want)
		}
		return parseReturnBoolLiteral(t) == want || isReturnBooleanLikeExpr(t)
	}
	return false
}

func matchesBoolReturnOrExpr(body []string, idx, ifIndent int, tail, want string) bool {
	if tail != "" && !strings.HasPrefix(tail, "#") {
		return parseReturnBoolLiteral(tail) == want || isReturnBooleanLikeExpr(tail)
	}
	end := findPythonBlockEnd(body, idx+1, ifIndent)
	execLines := extractPythonExecLines(body[idx+1 : end])
	if len(execLines) != 1 {
		return false
	}
	return parseReturnBoolLiteral(execLines[0]) == want || isReturnBooleanLikeExpr(execLines[0])
}

func isReturnBooleanLikeExpr(stmt string) bool {
	cleaned := stripPythonComment(stmt)
	if !strings.HasPrefix(cleaned, "return ") {
		return false
	}
	expr := strings.TrimSpace(strings.TrimPrefix(cleaned, "return"))
	if expr == "True" || expr == "False" {
		return false
	}
	stripped := stripInlineStringsAndHashComment(expr)
	for _, prefix := range []string{"bool(", "isinstance(", "issubclass(", "any(", "all(", "hasattr(", "callable(", "not "} {
		if strings.HasPrefix(stripped, prefix) {
			return true
		}
	}
	if strings.HasSuffix(stripped, ", False)") || strings.HasSuffix(stripped, ", True)") {
		return true
	}
	for _, tok := range []string{"==", "!=", "<=", ">=", " and ", " or ", " is ", " is not ", " in ", " not in "} {
		if strings.Contains(stripped, tok) {
			return true
		}
	}
	return false
}

func parseReturnBoolLiteral(stmt string) string {
	cleaned := stripPythonComment(stmt)
	if cleaned == "return True" {
		return "True"
	}
	if cleaned == "return False" {
		return "False"
	}
	return ""
}

func oppositeBoolLiteral(val string) string {
	if val == "True" {
		return "False"
	}
	return "True"
}
