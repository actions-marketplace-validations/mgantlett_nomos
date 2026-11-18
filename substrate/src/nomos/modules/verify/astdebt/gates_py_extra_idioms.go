// Package astdebt implements shared tuple parsers and Python anti-slop collection, type-guard, and control-flow idiom checks.
package astdebt

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	pyIsinstanceNoBoolRe = regexp.MustCompile(`\bisinstance\s*\([^)]+\)\s+and\s+not\s+isinstance\s*\([^,]+,\s*bool\s*\)`)
	pyTypeCmpBuiltinRe   = regexp.MustCompile(`\btype\s*\([^)]+\)\s*(?:==|!=|is\s+not|is)\s*(?:int|float|str|list|dict|tuple|set|bytes|[A-Z][A-Za-z0-9_]*)\b`)
	pyGetEmptyLitRe      = regexp.MustCompile(`\.get\s*\([^,\n]+,\s*(?:\{\}|\[\])\s*\)`)
	pySelfNoneTernaryRe  = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\s+if\s+([A-Za-z_][A-Za-z0-9_]*)\s+is\s+not\s+None\s+else\s+None\b`)
	pyForInLineRe        = regexp.MustCompile(`^\s*for\s+([A-Za-z_][A-Za-z0-9_]*)\s+in\s+(.+):\s*$`)
	pyDictCopyForRe      = regexp.MustCompile(`^\s*for\s+([A-Za-z_][A-Za-z0-9_]*)\s*,\s*([A-Za-z_][A-Za-z0-9_]*)\s+in\s+[A-Za-z_][A-Za-z0-9_\.]*\.items\(\)\s*:\s*$`)
	pyNotInDictIfRe      = regexp.MustCompile(`^(\s*)if\s+([A-Za-z_][A-Za-z0-9_]*)\s+not\s+in\s+([A-Za-z_][A-Za-z0-9_\.]*)\s*:\s*$`)
	pyEmptyListAssignRe  = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*\[\s*\]\s*$`)
)

func unwrapOuterParens(s string) string {
	return unwrapOuterDelims(s, '(', ')')
}

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

func scanPythonExtraIdioms(relPath string, lines []string, funcs []pyScopedFunc) []string {
	var out []string
	out = append(out, scanPyUnnecessaryElseAfterJump(relPath, funcs)...)
	out = append(out, scanPyTypeGuardClutter(relPath, lines)...)
	out = append(out, scanPyDefensiveIsinstanceReturnSprawl(relPath, funcs)...)
	out = append(out, scanPyCollectionAndNoneIdioms(relPath, lines)...)
	return out
}

func scanPyDefensiveIsinstanceReturnSprawl(relPath string, funcs []pyScopedFunc) []string {
	count := 0
	for _, fn := range funcs {
		if pyFuncHasDefensiveIsinstanceReturn(fn.bodyLines) {
			count++
		}
	}
	if count >= 4 {
		return []string{fmt.Sprintf("%s:1: module repeats 'if not isinstance(...): return ...' across %d functions; consolidate per-field validation into a declarative schema table or boundary validator (Law 6)", relPath, count)}
	}
	return nil
}

func pyFuncHasDefensiveIsinstanceReturn(body []string) bool {
	for i := 0; i < len(body); i++ {
		cleaned := stripInlineStringsAndHashComment(strings.TrimSpace(body[i]))
		if !strings.HasPrefix(cleaned, "if not isinstance(") && !strings.HasPrefix(cleaned, "if (not isinstance(") {
			continue
		}
		suite, hasColon := splitPyTopLevelColonTail(cleaned)
		if !hasColon {
			continue
		}
		if suite != "" {
			if strings.HasPrefix(strings.TrimSpace(suite), "return") {
				return true
			}
			continue
		}
		if nextPyExecLineReturns(body, i+1, countLeadingIndentCols(body[i])) {
			return true
		}
	}
	return false
}

func nextPyExecLineReturns(body []string, start, ifIndent int) bool {
	for j := start; j < len(body); j++ {
		t := strings.TrimSpace(stripInlineStringsAndHashComment(body[j]))
		if t == "" {
			continue
		}
		return countLeadingIndentCols(body[j]) > ifIndent && strings.HasPrefix(t, "return")
	}
	return false
}

func scanPyUnnecessaryElseAfterJump(relPath string, funcs []pyScopedFunc) []string {
	var out []string
	for _, fn := range funcs {
		out = append(out, scanSinglePyFuncElseAfterJump(relPath, fn)...)
	}
	return out
}

func scanSinglePyFuncElseAfterJump(relPath string, fn pyScopedFunc) []string {
	var out []string
	body := fn.bodyLines
	for i := 0; i < len(body); i++ {
		t := strings.TrimSpace(body[i])
		if !strings.HasPrefix(t, "if ") && !strings.HasPrefix(t, "elif ") {
			continue
		}
		col := countLeadingIndentCols(body[i])
		end := findPythonBlockEnd(body, i+1, col)
		if end >= len(body) || !pythonIfBlockEndsInJump(body[i:end]) {
			continue
		}
		if nextIdx := nextNonBlankSameIndentPyLine(body, end, col); nextIdx >= 0 && strings.HasPrefix(strings.TrimSpace(body[nextIdx]), "else:") {
			out = append(out, fmt.Sprintf("%s:%d: unnecessary 'else' after return/raise/break; flatten following block instead of nesting else", relPath, fn.startLine+nextIdx+1))
		}
	}
	return out
}

func nextNonBlankSameIndentPyLine(body []string, start, wantIndent int) int {
	for j := start; j < len(body); j++ {
		t := strings.TrimSpace(body[j])
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if countLeadingIndentCols(body[j]) != wantIndent {
			return -1
		}
		return j
	}
	return -1
}

func scanPyTypeGuardClutter(relPath string, lines []string) []string {
	var out []string
	noBoolHits := 0
	for i, raw := range lines {
		cleaned := stripInlineStringsAndHashComment(strings.TrimSpace(raw))
		if cleaned == "" {
			continue
		}
		if pyIsinstanceNoBoolRe.MatchString(cleaned) {
			noBoolHits++
		}
		if pyTypeCmpBuiltinRe.MatchString(cleaned) {
			out = append(out, fmt.Sprintf("%s:%d: avoid 'type(x) ==/is Cls'; use isinstance(x, ...) (with explicit bool check only if ints exclude bool)", relPath, i+1))
		}
	}
	if noBoolHits >= 2 {
		out = append(out, fmt.Sprintf("%s:1: repeats 'isinstance(...) and not isinstance(..., bool)' %d times; centralize scalar/bool check once or use type(x) in (int, float)", relPath, noBoolHits))
	}
	return out
}

func scanPyCollectionAndNoneIdioms(relPath string, lines []string) []string {
	var out []string
	getEmptyCount := 0
	for i, raw := range lines {
		cleaned := stripInlineStringsAndHashComment(strings.TrimSpace(raw))
		if cleaned == "" {
			continue
		}
		if sm := pySelfNoneTernaryRe.FindStringSubmatch(cleaned); len(sm) == 3 && sm[1] == sm[2] {
			out = append(out, fmt.Sprintf("%s:%d: redundant '<x> if <x> is not None else None' ternary simplifies directly to <x>", relPath, i+1))
		}
		if pyGetEmptyLitRe.MatchString(cleaned) {
			getEmptyCount++
		}
		if msg := matchPyLoopOrDictIdiomAt(relPath, lines, i, cleaned); msg != "" {
			out = append(out, msg)
		}
	}
	if getEmptyCount >= 2 {
		out = append(out, fmt.Sprintf("%s:1: repeats empty mutable default .get(k, [])/{} %d times; use k in d, .get(k) or (), or collections.defaultdict", relPath, getEmptyCount))
	}
	return out
}

func matchPyLoopOrDictIdiomAt(relPath string, lines []string, idx int, cleaned string) string {
	if msg := matchPyManualSetdefault(relPath, lines, idx); msg != "" {
		return msg
	}
	if msg := matchPyDictCopyLoop(relPath, lines, idx); msg != "" {
		return msg
	}
	return matchPyAppendLoop(relPath, lines, idx, cleaned)
}

func matchPyManualSetdefault(relPath string, lines []string, idx int) string {
	m := pyNotInDictIfRe.FindStringSubmatch(lines[idx])
	if len(m) != 4 || idx+2 >= len(lines) {
		return ""
	}
	initStmt := strings.TrimSpace(lines[idx+1])
	appendStmt := strings.TrimSpace(lines[idx+2])
	if strings.HasPrefix(initStmt, m[3]+"["+m[2]+"] = []") && strings.HasPrefix(appendStmt, m[3]+"["+m[2]+"].append(") {
		return fmt.Sprintf("%s:%d: manual 'if k not in d: d[k] = []; d[k].append(...)'; use d.setdefault(k, []).append(...) or collections.defaultdict(list)", relPath, idx+1)
	}
	return ""
}

func matchPyDictCopyLoop(relPath string, lines []string, idx int) string {
	m := pyDictCopyForRe.FindStringSubmatch(lines[idx])
	if len(m) != 3 || idx+1 >= len(lines) {
		return ""
	}
	body := strings.TrimSpace(lines[idx+1])
	if strings.Contains(body, "["+m[1]+"] = "+m[2]) && !hasNextSameOrDeeperBodyLine(lines, idx+2, countLeadingIndentCols(lines[idx])) {
		return fmt.Sprintf("%s:%d: verbose dict copy loop; use target.update(src)", relPath, idx+1)
	}
	return ""
}

func matchPyAppendLoop(relPath string, lines []string, idx int, cleaned string) string {
	m := pyEmptyListAssignRe.FindStringSubmatch(cleaned)
	if len(m) != 2 || idx+2 >= len(lines) {
		return ""
	}
	fm := pyForInLineRe.FindStringSubmatch(lines[idx+1])
	if len(fm) != 3 {
		return ""
	}
	body := strings.TrimSpace(lines[idx+2])
	if strings.HasPrefix(body, m[1]+".append(") && !hasNextSameOrDeeperBodyLine(lines, idx+3, countLeadingIndentCols(lines[idx+1])) {
		return fmt.Sprintf("%s:%d: verbose list append loop; replace with a list comprehension [%s]", relPath, idx+1, m[1])
	}
	return ""
}

func hasNextSameOrDeeperBodyLine(lines []string, idx, loopIndent int) bool {
	for j := idx; j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		return countLeadingIndentCols(lines[j]) > loopIndent
	}
	return false
}
