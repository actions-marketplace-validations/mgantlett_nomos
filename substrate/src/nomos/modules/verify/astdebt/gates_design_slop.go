// Package astdebt enforces dead pass-through wrapper, fragmentation, type-guard ladder, and nesting checks (Law 2 & Law 6).
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
)

// DetectPolyglotDesignSlop detects dead private or compatibility-annotated pass-through wrappers, 1-line wrapper fragmentation,
// inline type-guard ladders, consecutive same-variable if-chains, and anti-slop idioms across polyglot source files (@intent tree_sitter_ast_gate_spec.md).
func DetectPolyglotDesignSlop(root string, sources map[string][]byte) []string {
	return detectPolyglotDesignSlopWithLimit(root, sources, maxDesignSlopCorpusFiles)
}

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
		out = append(out, scanPythonFuncLadders(relPath, text)...)
		out = append(out, scanPythonIdiomSlop(relPath, text)...)
		return out
	}
	if !corpusTruncated {
		out = append(out, scanPolyglotFileWrappers(relPath, src, corpusCounts)...)
	}
	out = append(out, scanPolyglotIdiomAndControlFlowSlop(root, relPath, text)...)
	return out
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

func isPolyglotNestingExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ExtPy, ExtTS, ExtTSX, ExtJS, ExtJSX, ExtMJS, ExtCJS, ExtRS, ExtRB, ExtRake, ExtJava, ExtKT, ExtKTS, ExtCS, ExtCPP, ExtCC, ExtCXX, ExtC:
		return true
	default:
		return false
	}
}

func sortedSourceKeys(sources map[string][]byte) []string {
	paths := make([]string, 0, len(sources))
	for p := range sources {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

func scanPythonFuncLadders(relPath, text string) []string {
	lines := strings.Split(text, "\n")
	var out []string
	moduleIsinstanceGuards := 0
	for _, fn := range collectDirectPythonFuncs(lines) {
		isGuards, raiseGuards := countInlineIsinstanceAndRaiseGuards(fn.bodyLines)
		moduleIsinstanceGuards += isGuards
		if isGuards >= minInlineIsinstanceGuardsFunc {
			out = append(out, fmt.Sprintf("%s:%d: function %s contains %d inline 'isinstance(...): raise' guards; validate input schema once at boundary instead of repeating inline type-guard ladders", relPath, fn.startLine, fn.name, isGuards))
		} else if raiseGuards >= minInlineRaiseGuardsFunc {
			out = append(out, fmt.Sprintf("%s:%d: function %s contains %d inline 'if ...: raise' guards; consolidate validation into a shared helper or lookup table", relPath, fn.startLine, fn.name, raiseGuards))
		}
		for _, chain := range findConsecutiveBranchChains(fn.bodyLines, fn.startLine+1) {
			out = append(out, fmt.Sprintf("%s:%d: %d %s at same indent; consolidate into a lookup dict or match/case", relPath, chain.startLine, chain.length, chain.label))
		}
	}
	if moduleIsinstanceGuards >= minInlineIsinstanceGuardsModule {
		out = append(out, fmt.Sprintf("%s:1: module scatters %d inline 'isinstance(...): raise' guards across functions; centralize boundary validation into 1-2 shared validator helpers or a schema table", relPath, moduleIsinstanceGuards))
	}
	return out
}

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

func pyIfHeaderImmediatelyRaises(body []string, idx int, cleaned string) bool {
	suite, hasColon := splitPyTopLevelColonTail(cleaned)
	if !hasColon {
		return false
	}
	if suite != "" {
		return isPythonRaiseOrErrorHelper(suite)
	}
	return nextExecLineStartsWithRaise(body, idx+1, countLeadingIndentCols(body[idx]))
}

func isPythonRaiseOrErrorHelper(stmt string) bool {
	t := strings.TrimSpace(stmt)
	return strings.HasPrefix(t, "raise ") || t == "raise" || strings.HasPrefix(t, "raise(") || pyErrorHelperCallRe.MatchString(t)
}

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

func nextExecLineStartsWithRaise(lines []string, idx, ifIndent int) bool {
	for j := idx; j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if countLeadingIndentCols(lines[j]) <= ifIndent {
			return false
		}
		return isPythonRaiseOrErrorHelper(t)
	}
	return false
}

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

func formatFileSpan(relFile string, startLine, endLine int) string {
	if endLine > startLine {
		return fmt.Sprintf("%s:%d-%d", relFile, startLine, endLine)
	}
	return fmt.Sprintf("%s:%d", relFile, startLine)
}
