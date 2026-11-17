// Package astdebt enforces polyglot second-generation exception-gaming and silent-fallback hygiene (Law 7).
package astdebt

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	pyExceptClauseRe          = regexp.MustCompile(`^(\s*)except(?:\s+([^:]+?))?\s*:(.*)$`)
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

func scanPythonExceptionGaming(relPath, text string) []string {
	lines := strings.Split(text, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		m := pyExceptClauseRe.FindStringSubmatch(lines[i])
		if len(m) == 0 {
			continue
		}
		tail := strings.TrimSpace(m[3])
		if strings.Contains(tail, "#") {
			continue
		}
		indent := countLeadingIndentCols(m[1])
		clause := strings.TrimSpace(m[2])
		types := parsePythonExceptTypes(clause)
		if msg := checkBugMaskingExceptTuple(relPath, i+1, types); msg != "" {
			out = append(out, msg)
			continue
		}
		if isBroadOrMultiBoundaryCatch(clause, types) && isPythonSilentFallbackReturnBlock(lines, i+1, indent, tail) {
			out = append(out, fmt.Sprintf("%s:%d: silent fallback return in broad or multi-exception handler swallows boundary failure", relPath, i+1))
		}
	}
	return out
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
