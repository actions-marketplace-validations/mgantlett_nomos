// Package astdebt enforces polyglot dead pass-through wrapper, fragmentation, type-guard ladder, and nesting checks (Law 2 & Law 6).
package astdebt

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	minInlineIsinstanceGuardsFunc   = 3
	minInlineIsinstanceGuardsModule = 5
	minInlineRaiseGuardsFunc        = 4
)

var (
	pyAnyDefRe           = regexp.MustCompile(`^(\s*)(?:async\s+)?def\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	pyIfEqVarRe          = regexp.MustCompile(`^(\s*)if\s+([A-Za-z_][A-Za-z0-9_\.]*)\s*==\s*`)
	pyIfInVarRe          = regexp.MustCompile(`^(\s*)if\s+([A-Za-z_][A-Za-z0-9_\.]*)\s+in\s+`)
	pyIfIsinstanceVarRe  = regexp.MustCompile(`^(\s*)if\s+isinstance\s*\(\s*([A-Za-z_][A-Za-z0-9_\.]*)\s*,`)
	pyElseOrElifPrefixRe = regexp.MustCompile(`^(?:elif|else)\b`)
	pyErrorHelperCallRe  = regexp.MustCompile(`^(?:[A-Za-z_][A-Za-z0-9_]*\.)?(?:fail|abort|reject|panic|bail|error_out|err|raise_error|validation_error)\s*\(`)
	pyIsinstanceNoBoolRe = regexp.MustCompile(`\bisinstance\s*\([^)]+\)\s+and\s+not\s+isinstance\s*\([^,]+,\s*bool\s*\)`)
	pyTypeCmpBuiltinRe   = regexp.MustCompile(`\btype\s*\([^)]+\)\s*(?:==|!=|is\s+not|is)\s*(?:int|float|str|list|dict|tuple|set|bytes|[A-Z][A-Za-z0-9_]*)\b`)
	pyGetEmptyLitRe      = regexp.MustCompile(`\.get\s*\([^,\n]+,\s*(?:\{\}|\[\])\s*\)`)
	pySelfNoneTernaryRe  = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\s+if\s+([A-Za-z_][A-Za-z0-9_]*)\s+is\s+not\s+None\s+else\s+None\b`)
	pyForInLineRe        = regexp.MustCompile(`^\s*for\s+([A-Za-z_][A-Za-z0-9_]*)\s+in\s+(.+):\s*$`)
	pyDictCopyForRe      = regexp.MustCompile(`^\s*for\s+([A-Za-z_][A-Za-z0-9_]*)\s*,\s*([A-Za-z_][A-Za-z0-9_]*)\s+in\s+[A-Za-z_][A-Za-z0-9_\.]*\.items\(\)\s*:\s*$`)
	pyNotInDictIfRe      = regexp.MustCompile(`^(\s*)if\s+([A-Za-z_][A-Za-z0-9_]*)\s+not\s+in\s+([A-Za-z_][A-Za-z0-9_\.]*)\s*:\s*$`)
	pyEmptyListAssignRe  = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*\[\s*\]\s*$`)
)

// DetectPolyglotDesignSlop detects dead pass-through wrappers, 1-line wrapper fragmentation,
// inline type-guard ladders, consecutive same-variable if-chains, and anti-slop idioms across polyglot source files (@intent tree_sitter_ast_gate_spec.md).
func DetectPolyglotDesignSlop(root string, sources map[string][]byte) []string {
	return detectPolyglotDesignSlopWithLimit(root, sources, maxDesignSlopCorpusFiles)
}

// detectPolyglotDesignSlopWithLimit runs polyglot design-slop detection with an explicit corpus cap.
func detectPolyglotDesignSlopWithLimit(root string, sources map[string][]byte, maxCorpusFiles int) []string {
	if len(sources) == 0 {
		return nil
	}
	corpusCounts, corpusTruncated := buildDesignSlopCorpusCountsWithLimit(root, sources, maxCorpusFiles)
	var out []string
	for _, relPath := range sortedSourceKeys(sources) {
		out = append(out, evaluateSingleFileDesignSlop(root, relPath, sources[relPath], corpusCounts, corpusTruncated)...)
	}
	return out
}

// evaluateSingleFileDesignSlop dispatches design-slop checks for one non-Go, non-test source file.
func evaluateSingleFileDesignSlop(root, relPath string, src []byte, corpusCounts map[string]int, corpusTruncated bool) []string {
	ext := strings.ToLower(filepath.Ext(relPath))
	if ext == ExtGo || ext == ExtSvelte || !isPolyglotDesignSlopExt(ext) || isDesignSlopTestRelPath(relPath) {
		return nil
	}
	text := string(src)
	var out []string
	if ext == ExtPy {
		if !corpusTruncated {
			out = append(out, scanPythonFileWrappers(relPath, text, corpusCounts)...)
		}
		lines, funcs := splitAndCollectDirectPythonFuncs(text)
		out = append(out, scanPythonFuncLadders(relPath, funcs)...)
		out = append(out, scanPythonIdiomSlop(relPath, lines, funcs)...)
		out = append(out, scanPyExtraIdioms(relPath, lines, funcs)...)
		return out
	}
	if !corpusTruncated {
		out = append(out, scanPolyglotFileWrappers(relPath, src, corpusCounts)...)
	}
	return append(out, scanPolyglotIdiomAndControlFlowSlop(root, relPath, text)...)
}

// EvaluatePolyglotNestingViolations flags non-Go functions exceeding maxNesting control-flow depth (@intent tree_sitter_ast_gate_spec.md).
func EvaluatePolyglotNestingViolations(relPath string, src []byte, maxNesting int) []string {
	if len(src) == 0 || strings.HasSuffix(relPath, ExtGo) || !isPolyglotNestingExt(filepath.Ext(relPath)) {
		return nil
	}
	if maxNesting <= 0 {
		maxNesting = MaxFunctionNesting
	}
	_, obsNesting, _, deepNest := SummarizeFileArityAndNesting(relPath, src, MaxFunctionParams, maxNesting)
	if deepNest <= 0 {
		return nil
	}
	return []string{fmt.Sprintf(
		"%s: control-flow nesting depth %d exceeds max %d (%d deeply nested function(s)); flatten nested conditionals with early-return guard clauses",
		relPath, obsNesting, maxNesting, deepNest,
	)}
}

// isPolyglotNestingExt reports whether ext supports polyglot control-flow nesting checks.
func isPolyglotNestingExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ExtPy, ExtTS, ExtTSX, ExtJS, ExtJSX, ExtMJS, ExtCJS, ExtRS, ExtRB, ExtRake, ExtJava, ExtKT, ExtKTS, ExtCS, ExtCPP, ExtCC, ExtCXX, ExtC:
		return true
	default:
		return false
	}
}

// sortedSourceKeys returns source map keys in deterministic lexicographical order.
func sortedSourceKeys(sources map[string][]byte) []string {
	paths := make([]string, 0, len(sources))
	for p := range sources {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// scanPythonFuncLadders detects inline isinstance/raise guard ladders and consecutive branch chains.
func scanPythonFuncLadders(relPath string, funcs []pyScopedFunc) []string {
	var out []string
	moduleIsinstanceGuards := 0
	for _, fn := range funcs {
		isGuards, raiseGuards := countInlineIsinstanceAndRaiseGuards(fn.bodyLines)
		moduleIsinstanceGuards += isGuards
		if isGuards >= minInlineIsinstanceGuardsFunc {
			out = append(out, fmt.Sprintf("%s:%d: function %s contains %d inline 'isinstance(...): raise' guards; validate input schema once at boundary instead of repeating inline type-guard ladders", relPath, fn.startLine, fn.name, isGuards))
		} else if raiseGuards >= minInlineRaiseGuardsFunc {
			out = append(out, fmt.Sprintf("%s:%d: function %s contains %d inline 'if ...: raise' guards; consolidate validation into a shared helper or lookup table", relPath, fn.startLine, fn.name, raiseGuards))
		}
		for _, chain := range findConsecutiveBranchChains(fn.bodyLines, fn.bodyBaseLine+1) {
			out = append(out, fmt.Sprintf("%s:%d: %d %s at same indent; consolidate into a lookup dict or match/case", relPath, chain.startLine, chain.length, chain.label))
		}
	}
	if moduleIsinstanceGuards >= minInlineIsinstanceGuardsModule {
		out = append(out, fmt.Sprintf("%s:1: module scatters %d inline 'isinstance(...): raise' guards across functions; centralize boundary validation into 1-2 shared validator helpers or a schema table", relPath, moduleIsinstanceGuards))
	}
	return out
}

// countInlineIsinstanceAndRaiseGuards counts immediate raise/error guards in a Python body.
func countInlineIsinstanceAndRaiseGuards(body []string) (int, int) {
	isGuards, raiseGuards := 0, 0
	for i := 0; i < len(body); i++ {
		trimmed := strings.TrimSpace(body[i])
		if !strings.HasPrefix(trimmed, "if ") && !strings.HasPrefix(trimmed, "if(") {
			continue
		}
		cleaned := stripInlineStringsAndHashComment(trimmed)
		if !pyIfHeaderImmediatelyRaises(body, i, cleaned) {
			continue
		}
		raiseGuards++
		if strings.Contains(cleaned, "isinstance(") || strings.Contains(cleaned, "type(") {
			isGuards++
		}
	}
	return isGuards, raiseGuards
}

// pyIfHeaderImmediatelyRaises checks whether an if header raises/bails inline or on its first suite line.
func pyIfHeaderImmediatelyRaises(body []string, idx int, cleaned string) bool {
	suite, hasColon := splitPyTopLevelColonTail(cleaned)
	if !hasColon {
		return false
	}
	if suite != "" {
		return isPythonRaiseOrErrorHelper(suite)
	}
	return nextPyExecLineMatches(body, idx+1, countLeadingIndentCols(body[idx]), isPythonRaiseOrErrorHelper)
}

// isPythonRaiseOrErrorHelper matches Python raise statements or standard abort helper calls.
func isPythonRaiseOrErrorHelper(stmt string) bool {
	t := strings.TrimSpace(stmt)
	return strings.HasPrefix(t, "raise ") || t == "raise" || strings.HasPrefix(t, "raise(") || pyErrorHelperCallRe.MatchString(t)
}

// splitPyTopLevelColonTail locates the top-level suite colon outside nested brackets.
func splitPyTopLevelColonTail(line string) (string, bool) {
	depth := 0
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth > 0 {
				depth--
			}
		case ':':
			if depth == 0 {
				return strings.TrimSpace(line[i+1:]), true
			}
		}
	}
	return "", false
}

// nextPyExecLineMatches returns true when the first indented statement after an if header satisfies pred.
func nextPyExecLineMatches(body []string, start, ifIndent int, pred func(string) bool) bool {
	for j := start; j < len(body); j++ {
		t := strings.TrimSpace(stripInlineStringsAndHashComment(body[j]))
		if t == "" {
			continue
		}
		return countLeadingIndentCols(body[j]) > ifIndent && pred(t)
	}
	return false
}

// scanPyExtraIdioms runs else-after-jump, defensive isinstance return sprawl, and Python loop idioms.
func scanPyExtraIdioms(relPath string, lines []string, funcs []pyScopedFunc) []string {
	var out []string
	defensiveReturns := 0
	for _, fn := range funcs {
		out = append(out, scanSinglePyFuncElseAfterJump(relPath, fn)...)
		if pyFuncHasDefensiveIsinstanceReturn(fn.bodyLines) {
			defensiveReturns++
		}
	}
	if defensiveReturns >= 4 {
		out = append(out, fmt.Sprintf("%s:1: module repeats 'if not isinstance(...): return ...' across %d functions; consolidate per-field validation into a declarative schema table or boundary validator (Law 6)", relPath, defensiveReturns))
	}
	out = append(out, scanPyTypeAndCollectionIdioms(relPath, lines)...)
	return out
}

// pyFuncHasDefensiveIsinstanceReturn detects 'if not isinstance(...): return' validation sprawl.
func pyFuncHasDefensiveIsinstanceReturn(body []string) bool {
	isReturn := func(s string) bool { return strings.HasPrefix(strings.TrimSpace(s), "return") }
	for i := 0; i < len(body); i++ {
		cleaned := stripInlineStringsAndHashComment(strings.TrimSpace(body[i]))
		if !strings.HasPrefix(cleaned, "if not isinstance(") && !strings.HasPrefix(cleaned, "if (not isinstance(") {
			continue
		}
		suite, hasColon := splitPyTopLevelColonTail(cleaned)
		if !hasColon {
			continue
		}
		if (suite != "" && isReturn(suite)) || (suite == "" && nextPyExecLineMatches(body, i+1, countLeadingIndentCols(body[i]), isReturn)) {
			return true
		}
	}
	return false
}

// scanSinglePyFuncElseAfterJump flags redundant else clauses immediately following a jump suite.
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
			out = append(out, fmt.Sprintf("%s:%d: unnecessary 'else' after return/raise/break; flatten following block instead of nesting else", relPath, fn.bodyBaseLine+nextIdx+1))
		}
	}
	return out
}

// nextNonBlankSameIndentPyLine returns the next non-comment line at wantIndent before dedent.
func nextNonBlankSameIndentPyLine(body []string, start, wantIndent int) int {
	for j := start; j < len(body); j++ {
		t := strings.TrimSpace(body[j])
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if countLeadingIndentCols(body[j]) == wantIndent {
			return j
		}
		return -1
	}
	return -1
}

// scanPyTypeAndCollectionIdioms checks redundant type checks, empty .get defaults, and verbose loops.
func scanPyTypeAndCollectionIdioms(relPath string, lines []string) []string {
	var out []string
	noBoolHits, getEmptyCount := 0, 0
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
	if noBoolHits >= 2 {
		out = append(out, fmt.Sprintf("%s:1: repeats 'isinstance(...) and not isinstance(..., bool)' %d times; centralize scalar/bool check once or use type(x) in (int, float)", relPath, noBoolHits))
	}
	if getEmptyCount >= 2 {
		out = append(out, fmt.Sprintf("%s:1: repeats empty mutable default .get(k, [])/{} %d times; use k in d, .get(k) or (), or collections.defaultdict", relPath, getEmptyCount))
	}
	return out
}

// matchPyLoopOrDictIdiomAt detects 3-line setdefault, dict.update, and list-append loops.
func matchPyLoopOrDictIdiomAt(relPath string, lines []string, idx int, cleaned string) string {
	if idx+1 >= len(lines) {
		return ""
	}
	if dm := pyDictCopyForRe.FindStringSubmatch(lines[idx]); len(dm) == 3 {
		if body := strings.TrimSpace(lines[idx+1]); strings.Contains(body, "["+dm[1]+"] = "+dm[2]) && !hasDeeperPyBodyLine(lines, idx+2, countLeadingIndentCols(lines[idx])) {
			return fmt.Sprintf("%s:%d: verbose dict copy loop; use target.update(src)", relPath, idx+1)
		}
	}
	if idx+2 >= len(lines) {
		return ""
	}
	if nm := pyNotInDictIfRe.FindStringSubmatch(lines[idx]); len(nm) == 4 && strings.HasPrefix(strings.TrimSpace(lines[idx+1]), nm[3]+"["+nm[2]+"] = []") && strings.HasPrefix(strings.TrimSpace(lines[idx+2]), nm[3]+"["+nm[2]+"].append(") {
		return fmt.Sprintf("%s:%d: manual 'if k not in d: d[k] = []; d[k].append(...)'; use d.setdefault(k, []).append(...) or collections.defaultdict(list)", relPath, idx+1)
	}
	if am := pyEmptyListAssignRe.FindStringSubmatch(cleaned); len(am) == 2 && pyForInLineRe.MatchString(lines[idx+1]) && strings.HasPrefix(strings.TrimSpace(lines[idx+2]), am[1]+".append(") && !hasDeeperPyBodyLine(lines, idx+3, countLeadingIndentCols(lines[idx+1])) {
		return fmt.Sprintf("%s:%d: verbose list append loop; replace with a list comprehension [%s]", relPath, idx+1, am[1])
	}
	return ""
}

// hasDeeperPyBodyLine reports whether the next non-blank line remains indented inside loopIndent.
func hasDeeperPyBodyLine(lines []string, idx, loopIndent int) bool {
	for j := idx; j < len(lines); j++ {
		if t := strings.TrimSpace(lines[j]); t != "" && !strings.HasPrefix(t, "#") {
			return countLeadingIndentCols(lines[j]) > loopIndent
		}
	}
	return false
}

// loadCachedSourceBytes loads relative source bytes from cache or disk without failing on missing files.
func loadCachedSourceBytes(root, relFile string, cache map[string][]byte) []byte {
	if src, ok := cache[relFile]; ok {
		return src
	}
	src, err := os.ReadFile(filepath.Join(root, relFile))
	if err != nil {
		src = nil
	}
	cache[relFile] = src
	return src
}

// formatFileSpan formats relFile:startLine-endLine or relFile:startLine cleanly.
func formatFileSpan(relFile string, startLine, endLine int) string {
	if endLine > startLine {
		return fmt.Sprintf("%s:%d-%d", relFile, startLine, endLine)
	}
	return fmt.Sprintf("%s:%d", relFile, startLine)
}
