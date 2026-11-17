// Package astdebt enforces universal Python anti-slop idiom checks: Go-style (val, err) tuple returns,
// empty Exception hierarchy sprawl, and redundant if/return boolean ceremony (Law 2 & Law 6).
package astdebt

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	minGoErrTupleReturns = 3
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
		noneFirst, noneSecond := countNoneTupleReturns(fn.bodyLines)
		total := noneFirst + noneSecond
		if total >= minGoErrTupleReturns && noneFirst > 0 && noneSecond > 0 {
			out = append(out, fmt.Sprintf(
				"%s:%d: function %s uses Go-style (value, err) tuple returns (%d branches); raise a typed exception or return a structured result instead",
				relPath, fn.startLine, fn.name, total,
			))
		}
	}
	return out
}

func countNoneTupleReturns(body []string) (int, int) {
	noneFirst, noneSecond := 0, 0
	for _, raw := range body {
		trimmed := strings.TrimSpace(raw)
		if !strings.HasPrefix(trimmed, "return ") {
			continue
		}
		expr := strings.TrimSpace(strings.TrimPrefix(trimmed, "return"))
		expr = unwrapOuterParens(stripPythonComment(expr))
		lhs, rhs, ok := splitTopLevelTwoTuple(expr)
		if !ok {
			continue
		}
		if lhs == "None" && rhs != "None" {
			noneFirst++
		} else if rhs == "None" && lhs != "None" {
			noneSecond++
		}
	}
	return noneFirst, noneSecond
}

func unwrapOuterParens(s string) string {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) < 2 || trimmed[0] != '(' || trimmed[len(trimmed)-1] != ')' {
		return trimmed
	}
	depth := 0
	for i := 0; i < len(trimmed); i++ {
		switch trimmed[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 && i < len(trimmed)-1 {
				return trimmed
			}
		}
	}
	return strings.TrimSpace(trimmed[1 : len(trimmed)-1])
}

type tupleScanState struct {
	depth    int
	quote    byte
	escaped  bool
	splitIdx int
}

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
			elseRet, elseEnd := extractIfSingleBoolReturn(body, j, ifIndent, strings.TrimSpace(strings.TrimPrefix(t, "else:")))
			return elseEnd > j && elseRet == want
		}
		return parseReturnBoolLiteral(t) == want
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
