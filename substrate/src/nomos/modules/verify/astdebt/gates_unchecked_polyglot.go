// Package astdebt enforces polyglot unchecked-error, silent-catch, and exception-gaming hygiene (Law 7).
package astdebt

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	emptyCatchBlockRe         = regexp.MustCompile(`\bcatch\s*(?:\([^)]*\))?\s*\{\s*\}`)
	emptyPromiseCatchRe       = regexp.MustCompile(`\.catch\s*\(\s*(?:async\s*)?(?:\([^)]*\)|[A-Za-z0-9_$]+)?\s*(?:=>\s*\{\s*\}|function\s*\([^)]*\)\s*\{\s*\})\s*\)`)
	braceEmptyFallbackCatchRe = regexp.MustCompile(`\bcatch\s*(?:\([^)]*\))?\s*\{\s*return(?:\s+(?:null|undefined|false|\{\s*\}|\[\s*\]|""|''))?\s*;?\s*\}`)
	pyExceptClauseRe          = regexp.MustCompile(`^(\s*)except(?:\s+([^:]+?))?\s*:(.*)$`)
	pyExceptMultiOpenRe       = regexp.MustCompile(`^(\s*)except\s*\((.*)$`)
	pyAsAliasSuffixRe         = regexp.MustCompile(`\s+as\s+[A-Za-z_][A-Za-z0-9_]*\s*$`)
	rustLetBlankCallRe        = regexp.MustCompile(`^\s*let\s+_\s*=\s*([A-Za-z0-9_.:!]+)\s*\(`)
	rustEmptyErrArmRe         = regexp.MustCompile(`\bErr\s*\([^)]*\)\s*=>\s*(?:\{\s*\}|\(\s*\))`)
	rubyInlineRescueRe        = regexp.MustCompile(`\S+\s+rescue\s+nil\b`)
	rubyRescueHeaderRe        = regexp.MustCompile(`^(\s*)rescue(?:\s+[^#]+)?$`)

	pyLogicBugExceptions = makeWordSet("KeyError TypeError IndexError AttributeError NameError UnboundLocalError RecursionError ZeroDivisionError AssertionError NotImplementedError")
	pyBoundaryExceptions = makeWordSet("OSError IOError FileNotFoundError PermissionError URLError HTTPError HTTPException TimeoutError YAMLError JSONDecodeError TomlDecodeError DatabaseError OperationalError Error")
	pySilentFallbackSet  = map[string]bool{
		"return": true, "return None": true, "return {}": true, "return []": true,
		"return ()": true, "return False": true, `return ""`: true, "return ''": true,
		"return set()": true, "return dict()": true, "return list()": true,
	}
	braceSilentRules = []struct {
		re         *regexp.Regexp
		jsOnly     bool
		diagnostic string
	}{
		{emptyCatchBlockRe, false, "%s:%d: empty catch block silently swallows exception"},
		{emptyPromiseCatchRe, true, "%s:%d: empty promise .catch() handler silently swallows rejection"},
		{braceEmptyFallbackCatchRe, false, "%s:%d: catch block silently returns empty/null fallback without handling error"},
	}
)

type pyExceptHeader struct {
	startLine int
	endIdx    int
	indent    int
	clause    string
	tail      string
}

// CheckPolyglotUncheckedErrors executes deterministic astdebt verification logic (@intent tree_sitter_ast_gate_spec.md).
func CheckPolyglotUncheckedErrors(ext, relPath string, content []byte) []string {
	text := string(content)
	if IsJSFamilyExt(ext) {
		return scanBraceSilentCatches(relPath, text, true)
	}
	switch ext {
	case ExtPy:
		return scanPythonExcepts(relPath, text)
	case ExtJava, ExtKT, ExtKTS, ExtCS, ExtCPP, ExtCC, ExtCXX, ExtHPP:
		return scanBraceSilentCatches(relPath, text, false)
	case ExtRS:
		return scanRustErrorDiscards(relPath, text)
	case ExtRB, ExtRake:
		return scanRubySilentRescues(relPath, text)
	default:
		return nil
	}
}

// scanPythonExcepts checks silent pass/ellipsis handlers, bug-masking tuples, and silent fallbacks in one pass.
func scanPythonExcepts(relPath, text string) []string {
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
		if msg := evaluatePythonExceptHandler(relPath, lines, hdr); msg != "" {
			out = append(out, msg)
		}
	}
	return out
}

// evaluatePythonExceptHandler evaluates one parsed Python except header and its indented body.
func evaluatePythonExceptHandler(relPath string, lines []string, hdr pyExceptHeader) string {
	if isPythonPassExcept(lines, hdr) {
		return fmt.Sprintf("%s:%d: silent exception suppression in except block", relPath, hdr.startLine)
	}
	types := parsePythonExceptTypes(hdr.clause)
	if msg := checkBugMaskingExceptTuple(relPath, hdr.startLine, types); msg != "" {
		return msg
	}
	if isBroadOrMultiBoundaryCatch(hdr.clause, types) && isPythonSilentFallbackReturnBlock(lines, hdr) {
		return fmt.Sprintf("%s:%d: silent fallback return in broad or multi-exception handler swallows boundary failure", relPath, hdr.startLine)
	}
	return ""
}

// parsePythonExceptHeaderAt parses single-line or multi-line parenthesized Python except headers.
func parsePythonExceptHeaderAt(lines []string, idx int) (pyExceptHeader, bool) {
	if multi := pyExceptMultiOpenRe.FindStringSubmatch(lines[idx]); len(multi) == 3 {
		return collapseMultiLineExceptHeader(lines, idx, multi[1], multi[2])
	}
	single := pyExceptClauseRe.FindStringSubmatch(lines[idx])
	if len(single) == 0 {
		return pyExceptHeader{}, false
	}
	return pyExceptHeader{
		startLine: idx + 1, endIdx: idx, indent: countLeadingIndentCols(single[1]),
		clause: strings.TrimSpace(single[2]), tail: strings.TrimSpace(single[3]),
	}, true
}

// collapseMultiLineExceptHeader joins parenthesized exception tuples spanning up to 16 lines.
func collapseMultiLineExceptHeader(lines []string, idx int, rawIndent, firstSeg string) (pyExceptHeader, bool) {
	depth := 1
	var parts []string
	limit := MinInt(len(lines), idx+17)
	for j := idx; j < limit; j++ {
		seg := lines[j]
		if j == idx {
			seg = firstSeg
		}
		part, closeCol := scanParenTupleLine(seg, &depth)
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
			startLine: idx + 1, endIdx: j, indent: countLeadingIndentCols(rawIndent),
			clause: clause, tail: strings.TrimSpace(rest[colon+1:]),
		}, true
	}
	return pyExceptHeader{}, false
}

// scanParenTupleLine strips inline # comments while tracking parenthesis nesting depth.
func scanParenTupleLine(seg string, depth *int) (string, int) {
	for i := 0; i < len(seg); i++ {
		switch seg[i] {
		case '#':
			return seg[:i], -1
		case '(':
			*depth++
		case ')':
			*depth--
			if *depth == 0 {
				return seg[:i], i
			}
		}
	}
	return seg, -1
}

// findColonOutsideHash finds the header-terminating colon before any trailing comment.
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

// parsePythonExceptTypes splits normalized exception class expressions from an except clause.
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
	var types []string
	for _, p := range strings.Split(clean, ",") {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			types = append(types, trimmed)
		}
	}
	return types
}

// shortPythonExceptionName returns the unqualified class name for dotted exception expressions.
func shortPythonExceptionName(expr string) string {
	if dot := strings.LastIndexByte(expr, '.'); dot >= 0 && dot+1 < len(expr) {
		return strings.TrimSpace(expr[dot+1:])
	}
	return strings.TrimSpace(expr)
}

// checkBugMaskingExceptTuple flags catch-all tuples combining boundary errors with logic bugs.
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

// isBroadOrMultiBoundaryCatch reports whether an except clause is bare, generic Exception, or multi-boundary.
func isBroadOrMultiBoundaryCatch(clause string, types []string) bool {
	if clause == "" {
		return true
	}
	for _, rawType := range types {
		base := shortPythonExceptionName(rawType)
		if base == "Exception" || base == "BaseException" {
			return true
		}
		if len(types) > 1 && pyBoundaryExceptions[base] {
			return true
		}
	}
	return false
}

// collectUncommentedBlockStmts gathers statements inside a Python indented suite, aborting on comments.
func collectUncommentedBlockStmts(lines []string, startIdx, headerIndent, maxStmts int) ([]string, bool) {
	var stmts []string
	for j := startIdx; j < len(lines); j++ {
		trimmed := strings.TrimSpace(lines[j])
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			return nil, false
		}
		if countLeadingIndentCols(lines[j]) <= headerIndent {
			break
		}
		if strings.Contains(trimmed, "#") {
			return nil, false
		}
		stmts = append(stmts, trimmed)
		if maxStmts > 0 && len(stmts) > maxStmts {
			return nil, false
		}
	}
	return stmts, true
}

// isPythonPassExcept returns true when an except block contains strictly pass or ellipsis without comments.
func isPythonPassExcept(lines []string, hdr pyExceptHeader) bool {
	if hdr.tail != "" {
		return hdr.tail == "pass" || hdr.tail == "..."
	}
	stmts, ok := collectUncommentedBlockStmts(lines, hdr.endIdx+1, hdr.indent, 0)
	if !ok || len(stmts) == 0 {
		return false
	}
	for _, s := range stmts {
		if s != "pass" && s != "..." {
			return false
		}
	}
	return true
}

// isPythonSilentFallbackReturnBlock detects single-statement empty/null returns in broad except suites.
func isPythonSilentFallbackReturnBlock(lines []string, hdr pyExceptHeader) bool {
	if hdr.tail != "" {
		return pySilentFallbackSet[hdr.tail]
	}
	stmts, ok := collectUncommentedBlockStmts(lines, hdr.endIdx+1, hdr.indent, 1)
	return ok && len(stmts) == 1 && pySilentFallbackSet[stmts[0]]
}

// scanBraceSilentCatches scans brace-delimited languages for empty or fallback-only catch blocks.
func scanBraceSilentCatches(relPath, text string, checkPromises bool) []string {
	var out []string
	for _, rule := range braceSilentRules {
		if rule.jsOnly && !checkPromises {
			continue
		}
		for _, m := range rule.re.FindAllStringIndex(text, -1) {
			line := strings.Count(text[:m[0]], "\n") + 1
			out = append(out, fmt.Sprintf(rule.diagnostic, relPath, line))
		}
	}
	return out
}

// scanRustErrorDiscards detects discarded call results via let _ = or empty Err(_) match arms.
func scanRustErrorDiscards(relPath, text string) []string {
	if idx := strings.Index(text, "#[cfg(test)]"); idx >= 0 {
		text = text[:idx]
	}
	var out []string
	for idx, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "//") {
			continue
		}
		if m := rustLetBlankCallRe.FindStringSubmatch(line); len(m) > 1 && !isIgnoredRustBlankCall(m[1]) {
			out = append(out, fmt.Sprintf("%s:%d: discarded call result via let _ =", relPath, idx+1))
		} else if rustEmptyErrArmRe.MatchString(line) {
			out = append(out, fmt.Sprintf("%s:%d: silent Err(_) match arm discard", relPath, idx+1))
		}
	}
	return out
}

// isIgnoredRustBlankCall exempts intentional output and teardown sinks from let _ checks.
func isIgnoredRustBlankCall(callee string) bool {
	for _, sfx := range []string{"write!", "writeln!", "flush", "close", "print!", "println!", "eprint!", "eprintln!"} {
		if strings.HasSuffix(callee, sfx) {
			return true
		}
	}
	return false
}

// scanRubySilentRescues detects inline rescue nil and empty rescue blocks in Ruby sources.
func scanRubySilentRescues(relPath, text string) []string {
	lines := strings.Split(text, "\n")
	var out []string
	for i, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if strings.Contains(trimmed, "#") {
			continue
		}
		if rubyInlineRescueRe.MatchString(trimmed) {
			out = append(out, fmt.Sprintf("%s:%d: silent inline rescue nil suppression", relPath, i+1))
		} else if m := rubyRescueHeaderRe.FindStringSubmatch(raw); len(m) > 0 && isRubyEmptyRescueBlock(lines, i+1, countLeadingIndentCols(m[1])) {
			out = append(out, fmt.Sprintf("%s:%d: empty rescue block silently swallows exception", relPath, i+1))
		}
	}
	return out
}

// isRubyEmptyRescueBlock reports whether a Ruby rescue suite has no non-nil statements before closing.
func isRubyEmptyRescueBlock(lines []string, startIdx, rescueIndent int) bool {
	for j := startIdx; j < len(lines); j++ {
		trimmed := strings.TrimSpace(lines[j])
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "#") {
			return false
		}
		if countLeadingIndentCols(lines[j]) <= rescueIndent && (trimmed == "end" || strings.HasPrefix(trimmed, "ensure") || strings.HasPrefix(trimmed, "else") || strings.HasPrefix(trimmed, "rescue")) {
			return true
		}
		if trimmed != "nil" {
			return false
		}
	}
	return false
}

