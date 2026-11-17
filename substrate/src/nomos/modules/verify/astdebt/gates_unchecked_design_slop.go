// Package astdebt enforces polyglot second-generation exception-gaming and silent-fallback hygiene (Law 7).
package astdebt

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	pyExceptClauseRe          = regexp.MustCompile(`^(\s*)except(?:\s+([^:]+?))?\s*:(.*)$`)
	pyExceptMultiOpenRe       = regexp.MustCompile(`^(\s*)except\s*\((.*)$`)
	pyAsAliasSuffixRe         = regexp.MustCompile(`\s+as\s+[A-Za-z_][A-Za-z0-9_]*\s*$`)
	braceEmptyFallbackCatchRe = regexp.MustCompile(`\bcatch\s*(?:\([^)]*\))?\s*\{\s*return(?:\s+(?:null|undefined|false|\{\s*\}|\[\s*\]|""|''))?\s*;?\s*\}`)

	pyLogicBugExceptions = map[string]bool{
		"KeyError":            true,
		"TypeError":           true,
		"IndexError":          true,
		"AttributeError":      true,
		"NameError":           true,
		"UnboundLocalError":   true,
		"RecursionError":      true,
		"ZeroDivisionError":   true,
		"AssertionError":      true,
		"NotImplementedError": true,
	}

	pyBoundaryExceptions = map[string]bool{
		"OSError":           true,
		"IOError":           true,
		"FileNotFoundError": true,
		"PermissionError":   true,
		"URLError":          true,
		"HTTPError":         true,
		"HTTPException":     true,
		"TimeoutError":      true,
		"YAMLError":         true,
		"JSONDecodeError":   true,
		"TomlDecodeError":   true,
		"DatabaseError":     true,
		"OperationalError":  true,
		"Error":             true,
	}

	pySilentFallbackReturns = map[string]bool{
		"return":        true,
		"return None":   true,
		"return {}":     true,
		"return []":     true,
		"return ()":     true,
		"return False":  true,
		"return \"\"":   true,
		"return ''":     true,
		"return set()":  true,
		"return dict()": true,
		"return list()": true,
	}
)

type pyExceptHeader struct {
	startLine int
	endIdx    int
	indent    int
	clause    string
	tail      string
}

func scanPythonExceptionGaming(relPath, text string) []string {
	lines := strings.Split(text, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		hdr, ok := parsePythonExceptHeaderAt(lines, i)
		if !ok {
			continue
		}
		i = hdr.endIdx
		if strings.Contains(hdr.tail, "#") {
			continue
		}
		types := parsePythonExceptTypes(hdr.clause)
		if msg := checkBugMaskingExceptTuple(relPath, hdr.startLine, types); msg != "" {
			out = append(out, msg)
			continue
		}
		if isBroadOrMultiBoundaryCatch(hdr.clause, types) && isPythonSilentFallbackReturnBlock(lines, hdr.endIdx+1, hdr.indent, hdr.tail) {
			out = append(out, fmt.Sprintf("%s:%d: silent fallback return in broad or multi-exception handler swallows boundary failure", relPath, hdr.startLine))
		}
	}
	return out
}

func parsePythonExceptHeaderAt(lines []string, idx int) (pyExceptHeader, bool) {
	if multi := pyExceptMultiOpenRe.FindStringSubmatch(lines[idx]); len(multi) == 3 {
		if hdr, ok := collapseMultiLineExceptHeader(lines, idx, multi[1], multi[2]); ok {
			return hdr, true
		}
		return pyExceptHeader{}, false
	}
	single := pyExceptClauseRe.FindStringSubmatch(lines[idx])
	if len(single) == 0 {
		return pyExceptHeader{}, false
	}
	return pyExceptHeader{
		startLine: idx + 1,
		endIdx:    idx,
		indent:    countLeadingIndentCols(single[1]),
		clause:    strings.TrimSpace(single[2]),
		tail:      strings.TrimSpace(single[3]),
	}, true
}

func collapseMultiLineExceptHeader(lines []string, idx int, rawIndent, firstSeg string) (pyExceptHeader, bool) {
	depth := 1
	var parts []string
	limit := minIntVal(len(lines), idx+17)
	for j := idx; j < limit; j++ {
		seg := lines[j]
		if j == idx {
			seg = firstSeg
		}
		part, closeCol, nextDepth := scanParenTupleLine(seg, depth)
		depth = nextDepth
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			parts = append(parts, trimmed)
		}
		if depth != 0 {
			continue
		}
		rest := seg[closeCol+1:]
		colon := findColonOutsideHash(rest)
		if colon < 0 {
			return pyExceptHeader{}, false
		}
		clause := "(" + strings.Join(parts, " ") + ")"
		if alias := strings.TrimSpace(rest[:colon]); alias != "" {
			clause += " " + alias
		}
		return pyExceptHeader{
			startLine: idx + 1,
			endIdx:    j,
			indent:    countLeadingIndentCols(rawIndent),
			clause:    clause,
			tail:      strings.TrimSpace(rest[colon+1:]),
		}, true
	}
	return pyExceptHeader{}, false
}

func scanParenTupleLine(seg string, depth int) (string, int, int) {
	for i := 0; i < len(seg); i++ {
		switch seg[i] {
		case '#':
			return seg[:i], -1, depth
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return seg[:i], i, 0
			}
		}
	}
	return seg, -1, depth
}

func findColonOutsideHash(rest string) int {
	for i := 0; i < len(rest); i++ {
		if rest[i] == '#' {
			return -1
		}
		if rest[i] == ':' {
			return i
		}
	}
	return -1
}

func minIntVal(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func parsePythonExceptTypes(clause string) []string {
	clean := strings.TrimSpace(pyAsAliasSuffixRe.ReplaceAllString(clause, ""))
	if clean == "" {
		return nil
	}
	if strings.HasPrefix(clean, "(") && strings.HasSuffix(clean, ")") {
		clean = clean[1 : len(clean)-1]
	} else if !strings.Contains(clean, ",") {
		return []string{clean}
	}
	parts := strings.Split(clean, ",")
	var types []string
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			types = append(types, trimmed)
		}
	}
	return types
}

func shortPythonExceptionName(expr string) string {
	if dot := strings.LastIndexByte(expr, '.'); dot >= 0 && dot+1 < len(expr) {
		return strings.TrimSpace(expr[dot+1:])
	}
	return strings.TrimSpace(expr)
}

func checkBugMaskingExceptTuple(relPath string, lineNum int, types []string) string {
	if len(types) < 3 {
		return ""
	}
	logicCount := 0
	for _, rawType := range types {
		if pyLogicBugExceptions[shortPythonExceptionName(rawType)] {
			logicCount++
		}
	}
	otherCount := len(types) - logicCount
	if (logicCount >= 1 && otherCount >= 1) || logicCount >= 4 {
		return fmt.Sprintf("%s:%d: catch-all exception tuple (%d types) masks programming-bug exceptions; catch narrow boundary errors only", relPath, lineNum, len(types))
	}
	return ""
}

func isBroadOrMultiBoundaryCatch(clause string, types []string) bool {
	if clause == "" {
		return true
	}
	if len(types) == 1 {
		base := shortPythonExceptionName(types[0])
		return base == "Exception" || base == "BaseException"
	}
	for _, rawType := range types {
		base := shortPythonExceptionName(rawType)
		if base == "Exception" || base == "BaseException" || pyBoundaryExceptions[base] {
			return true
		}
	}
	return false
}

func isPythonSilentFallbackReturnBlock(lines []string, startIdx, headerIndent int, tail string) bool {
	if tail != "" {
		return pySilentFallbackReturns[tail]
	}
	var execLines []string
	for j := startIdx; j < len(lines); j++ {
		trimmed := strings.TrimSpace(lines[j])
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			return false
		}
		if countLeadingIndentCols(lines[j]) <= headerIndent {
			break
		}
		if strings.Contains(trimmed, "#") {
			return false
		}
		execLines = append(execLines, trimmed)
		if len(execLines) > 1 {
			return false
		}
	}
	return len(execLines) == 1 && pySilentFallbackReturns[execLines[0]]
}

func scanBraceFallbackCatches(relPath, text string) []string {
	var out []string
	for _, m := range braceEmptyFallbackCatchRe.FindAllStringIndex(text, -1) {
		line := strings.Count(text[:m[0]], "\n") + 1
		out = append(out, fmt.Sprintf("%s:%d: catch block silently returns empty/null fallback without handling error", relPath, line))
	}
	return out
}
