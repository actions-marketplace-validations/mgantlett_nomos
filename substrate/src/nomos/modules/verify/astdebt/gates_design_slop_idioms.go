// Package astdebt enforces Python anti-slop idiom checks and shared delimiter/tuple utilities (Law 2 & Law 6).
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
	name         string
	indent       int
	startLine    int
	bodyBaseLine int
	bodyLines    []string
}

type tupleScanState struct {
	depth    int
	quote    byte
	escaped  bool
	splitIdx int
}

// scanPythonIdiomSlop checks Go-style error tuples, empty Exception sprawl, and bool-return ceremony.
func scanPythonIdiomSlop(relPath string, lines []string, funcs []pyScopedFunc) []string {
	var out []string
	out = append(out, scanGoStyleErrorTupleReturns(relPath, funcs)...)
	out = append(out, scanEmptyExceptionSprawl(relPath, lines)...)
	for _, fn := range funcs {
		out = append(out, scanFuncRedundantBoolReturns(relPath, fn)...)
	}
	return out
}

// splitAndCollectDirectPythonFuncs parses Python functions with nested def bodies zeroed out.
func splitAndCollectDirectPythonFuncs(text string) ([]string, []pyScopedFunc) {
	lines := strings.Split(text, "\n")
	rawFuncs := parseAllPyFuncs(text)
	out := make([]pyScopedFunc, 0, len(rawFuncs))
	for _, fn := range rawFuncs {
		out = append(out, pyScopedFunc{
			name: fn.name, indent: fn.indent, startLine: fn.startLine,
			bodyBaseLine: fn.sigEndIdx + 1, bodyLines: sliceDirectFuncBody(fn.bodyLines, fn.indent),
		})
	}
	return lines, out
}

// sliceDirectFuncBody masks nested def suites so outer function scanners ignore inner helpers.
func sliceDirectFuncBody(body []string, funcIndent int) []string {
	out := make([]string, len(body))
	for i := 0; i < len(body); i++ {
		if m := pyAnyDefRe.FindStringSubmatch(strings.TrimSpace(body[i])); len(m) == 3 {
			if subIndent := countLeadingIndentCols(body[i]); subIndent > funcIndent {
				end := findPythonBlockEnd(body, i+1, subIndent)
				i = end - 1
				continue
			}
		}
		out[i] = body[i]
	}
	return out
}

// tallyComplementarySentinel updates balanced failure vs success return counters across tuples.
func tallyComplementarySentinel(failCond, okCond bool, failHits, okHits *int) {
	if failCond {
		*failHits++
	} else if okCond {
		*okHits++
	}
}

// scanGoStyleErrorTupleReturns rejects Go-style (val, err) / (ok, val) tuple returns in Python.
func scanGoStyleErrorTupleReturns(relPath string, funcs []pyScopedFunc) []string {
	var out []string
	for _, fn := range funcs {
		noneCount, noneBalanced := countPySentinelReturns(fn.bodyLines, "None", "")
		boolCount, boolBalanced := countPySentinelReturns(fn.bodyLines, "False", "True")
		total := 0
		if noneBalanced && noneCount >= minGoErrTupleReturns {
			total = noneCount
		} else if boolBalanced && boolCount >= minGoErrTupleReturns {
			total = boolCount
		}
		if total >= minGoErrTupleReturns {
			out = append(out, fmt.Sprintf(
				"%s:%d: function %s uses Go-style (value, err) tuple returns or (ok, value) status pairs (%d branches); raise a typed exception or return value/None directly",
				relPath, fn.startLine, fn.name, total,
			))
		}
	}
	return out
}

// countPySentinelReturns tallies balanced 2-tuple returns matching failTok vs non-failTok / okTok.
func countPySentinelReturns(body []string, failTok, okTok string) (int, bool) {
	failHits, okHits := 0, 0
	for _, raw := range body {
		trimmed := strings.TrimSpace(raw)
		if !strings.HasPrefix(trimmed, "return ") {
			continue
		}
		expr := unwrapOuterDelims(stripPythonComment(strings.TrimSpace(strings.TrimPrefix(trimmed, "return"))), '(', ')')
		lhs, rhs, ok := splitTopLevelTwoTuple(expr)
		if !ok {
			continue
		}
		if okTok == "" {
			tallyComplementarySentinel(lhs == failTok && rhs != failTok, rhs == failTok && lhs != failTok, &failHits, &okHits)
		} else {
			tallyComplementarySentinel(lhs == failTok || rhs == failTok, lhs == okTok || rhs == okTok, &failHits, &okHits)
		}
	}
	return failHits + okHits, failHits > 0 && okHits > 0
}

// unwrapOuterDelims strips one redundant matching outer delimiter pair wrapping the entire string.
func unwrapOuterDelims(s string, openCh, closeCh byte) string {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) < 2 || trimmed[0] != openCh || trimmed[len(trimmed)-1] != closeCh {
		return trimmed
	}
	depth := 0
	for i := 0; i < len(trimmed); i++ {
		if trimmed[i] == openCh {
			depth++
		} else if trimmed[i] == closeCh {
			depth--
			if depth == 0 && i < len(trimmed)-1 {
				return trimmed
			}
		}
	}
	return strings.TrimSpace(trimmed[1 : len(trimmed)-1])
}

// splitTopLevelTwoTuple splits an unnested A, B tuple expression outside strings and brackets.
func splitTopLevelTwoTuple(expr string) (string, string, bool) {
	st := &tupleScanState{splitIdx: -1}
	for i := 0; i < len(expr); i++ {
		if !st.advance(expr[i], i) {
			return "", "", false
		}
	}
	if st.splitIdx <= 0 || st.splitIdx >= len(expr)-1 {
		return "", "", false
	}
	return strings.TrimSpace(expr[:st.splitIdx]), strings.TrimSpace(expr[st.splitIdx+1:]), true
}

// advance steps one byte across quote state and bracket nesting inside tupleScanState.
func (s *tupleScanState) advance(ch byte, idx int) bool {
	if s.escaped {
		s.escaped = false
		return true
	}
	if s.quote != 0 {
		s.quote, s.escaped = stepSingleQuoteByte(ch, s.quote)
		return true
	}
	if ch == '\'' || ch == '"' {
		s.quote = ch
		return true
	}
	return s.stepDelimiter(ch, idx)
}

// stepDelimiter tracks nested brackets and locates at most one top-level tuple comma.
func (s *tupleScanState) stepDelimiter(ch byte, idx int) bool {
	if ch == '(' || ch == '[' || ch == '{' {
		s.depth++
	} else if (ch == ')' || ch == ']' || ch == '}') && s.depth > 0 {
		s.depth--
	} else if ch == ',' && s.depth == 0 {
		if s.splitIdx >= 0 {
			return false
		}
		s.splitIdx = idx
	}
	return true
}

// stripPythonComment strips trailing # comments outside single and double quoted string literals.
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

// scanEmptyExceptionSprawl flags modules declaring 3 or more empty Error/Exception subclasses.
func scanEmptyExceptionSprawl(relPath string, lines []string) []string {
	emptyCount, firstLine := 0, 0
	for i := 0; i < len(lines); i++ {
		m := pyExceptionClassRe.FindStringSubmatch(lines[i])
		if len(m) != 4 || !isExceptionBaseList(m[3]) {
			continue
		}
		end := findPythonBlockEnd(lines, i+1, countLeadingIndentCols(m[1]))
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

// isExceptionBaseList reports whether a Python class inherits from an Error or Exception base.
func isExceptionBaseList(bases string) bool {
	for _, raw := range strings.Split(bases, ",") {
		base := strings.TrimSpace(raw)
		if strings.HasSuffix(base, "Error") || strings.HasSuffix(base, "Exception") {
			return true
		}
	}
	return false
}

// extractPythonExecLines returns non-comment executable lines inside a Python suite.
func extractPythonExecLines(body []string) []string {
	execLines, hasFossil := extractPythonExecBodyInfo(body)
	if hasFossil && len(execLines) == 0 {
		return nil
	}
	return execLines
}

// isEmptyPythonClassBody reports whether a class suite only contains docstrings, pass, or ellipsis.
func isEmptyPythonClassBody(body []string) bool {
	for _, stmt := range extractPythonExecLines(body) {
		if cleaned := stripPythonComment(stmt); cleaned != "" && cleaned != "pass" && cleaned != "..." {
			return false
		}
	}
	return true
}

// scanFuncRedundantBoolReturns flags 'if cond: return True/False' followed directly by its opposite return.
func scanFuncRedundantBoolReturns(relPath string, fn pyScopedFunc) []string {
	var out []string
	body := fn.bodyLines
	for i := 0; i < len(body); i++ {
		m := pyIfBoolHeaderRe.FindStringSubmatch(strings.TrimSpace(body[i]))
		if len(m) != 3 {
			continue
		}
		ifIndent := countLeadingIndentCols(body[i])
		firstRet, bodyEnd := extractIfSingleBoolReturn(body, i, ifIndent, strings.TrimSpace(m[2]))
		if firstRet != "" && matchesOppositeFollowingReturn(body, bodyEnd, ifIndent, firstRet) {
			out = append(out, fmt.Sprintf(
				"%s:%d: redundant 'if ...: return %s' before opposite boolean return; return bool(<cond>) or not (<cond>) directly",
				relPath, fn.bodyBaseLine+i+1, firstRet,
			))
		}
	}
	return out
}

// extractIfSingleBoolReturn extracts 'return True|False' from an inline or 1-statement if suite.
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

// matchesOppositeFollowingReturn checks whether the next statement or else suite returns opposite/bool.
func matchesOppositeFollowingReturn(body []string, startIdx, ifIndent int, firstRet string) bool {
	want := "True"
	if firstRet == "True" {
		want = "False"
	}
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
			return matchesElseBoolReturn(body, j, ifIndent, strings.TrimSpace(strings.TrimPrefix(t, "else:")), want)
		}
		return parseReturnBoolLiteral(t) == want || isReturnBooleanLikeExpr(t)
	}
	return false
}

// matchesElseBoolReturn checks an else branch suite for a single opposite or boolean expression return.
func matchesElseBoolReturn(body []string, idx, ifIndent int, tail, want string) bool {
	if tail != "" && !strings.HasPrefix(tail, "#") {
		return parseReturnBoolLiteral(tail) == want || isReturnBooleanLikeExpr(tail)
	}
	end := findPythonBlockEnd(body, idx+1, ifIndent)
	execLines := extractPythonExecLines(body[idx+1 : end])
	return len(execLines) == 1 && (parseReturnBoolLiteral(execLines[0]) == want || isReturnBooleanLikeExpr(execLines[0]))
}

// isReturnBooleanLikeExpr identifies non-literal boolean return expressions following a guard return.
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

// parseReturnBoolLiteral parses 'return True' or 'return False' statements.
func parseReturnBoolLiteral(stmt string) string {
	switch stripPythonComment(stmt) {
	case "return True":
		return "True"
	case "return False":
		return "False"
	default:
		return ""
	}
}
