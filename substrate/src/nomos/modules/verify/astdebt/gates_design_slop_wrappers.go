// Package astdebt enforces polyglot dead pass-through and one-line wrapper fragmentation checks (Law 6).
package astdebt

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/treesitter"
)

const (
	minTrivialSinglesPerFile = 2
	maxMultiOneLinersPy      = 2
	maxMultiOneLinersRuby    = 2
	maxMultiOneLinersBrace   = 3
)

type wrapperClass int

const (
	wrapperNone wrapperClass = iota
	wrapperDeadTopFossil
	wrapperDeadMethod
	wrapperSingleMiddleman
	wrapperTrivialSingle
	wrapperMultiOneLiner
)

type singleUseHit struct {
	name string
	line int
}

var (
	polyglotMemberRe     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:(?:\.|::)[A-Za-z_][A-Za-z0-9_]*)*\(`)
	polyglotSkipWrapperN = makeWordSet("main constructor new initialize default format read write close keys items values copy clone eq fmt run render init dispose destroy toJSON toString valueOf")
)

// isPolyglotDesignSlopExt reports whether ext participates in polyglot anti-slop wrapper scanning.
func isPolyglotDesignSlopExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ExtPy, ExtRB, ExtRake, ExtTS, ExtTSX, ExtJS, ExtJSX, ExtMJS, ExtCJS, ExtRS, ExtJava, ExtKT, ExtKTS, ExtCS:
		return true
	default:
		return false
	}
}

// isDesignSlopTestRelPath exempts language test suites and .d.ts declarations from production wrapper checks.
func isDesignSlopTestRelPath(relPath string) bool {
	if isPythonTestRelPath(relPath) {
		return true
	}
	lower := strings.ToLower(filepath.ToSlash(relPath))
	base := filepath.Base(lower)
	if strings.HasSuffix(lower, ".d.ts") || strings.Contains(lower, "/__tests__/") || strings.Contains(lower, "/spec/") || strings.HasPrefix(lower, "spec/") {
		return true
	}
	return strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") || strings.HasSuffix(base, "_test.rb") || strings.HasSuffix(base, "_spec.rb")
}

// scanPythonFileWrappers flags dead fossils, 1-caller middlemen, and 1-line helper fragmentation in Python.
func scanPythonFileWrappers(relPath, text string, corpusCounts map[string]int) []string {
	var out, multiNames []string
	var singles []singleUseHit
	for _, fn := range parseAllPyFuncs(text) {
		cls := classifyPyOneLiner(fn, corpusCounts)
		if msg := formatPyImmediateWrapperHit(relPath, fn, cls); msg != "" {
			out = append(out, msg)
		} else if cls == wrapperTrivialSingle {
			singles = append(singles, singleUseHit{name: fn.name, line: fn.startLine})
		} else if cls == wrapperMultiOneLiner {
			multiNames = append(multiNames, fn.name)
		}
	}
	return append(out, formatPyWrapperGroupHits(relPath, singles, multiNames)...)
}

// formatPyImmediateWrapperHit formats 0-caller fossil and 1-caller forwarding middleman diagnostics.
func formatPyImmediateWrapperHit(relPath string, fn pyFuncBlock, cls wrapperClass) string {
	switch cls {
	case wrapperDeadTopFossil:
		return fmt.Sprintf("%s:%d: dead pass-through wrapper %q has 0 callers (checkpoint fossil); delete unused shim and call target directly (Law 6)", relPath, fn.startLine, fn.name)
	case wrapperDeadMethod:
		return fmt.Sprintf("%s:%d: dead pass-through method %q has 0 callers (checkpoint fossil); inline expression at callsite (Law 6)", relPath, fn.startLine, fn.name)
	case wrapperSingleMiddleman:
		return fmt.Sprintf("%s:%d: single-caller pass-through middleman %q forwards to another call with 1 caller; inline target call directly (Law 6)", relPath, fn.startLine, fn.name)
	default:
		return ""
	}
}

// formatPyWrapperGroupHits reports grouped trivial 1-caller and multi-caller Python one-liner fragmentation.
func formatPyWrapperGroupHits(relPath string, singles []singleUseHit, multiNames []string) []string {
	var out []string
	if len(singles) == 1 {
		out = append(out, fmt.Sprintf("%s:%d: single-caller one-line return helper %q has only 1 caller; inline expression directly at callsite (Law 6)", relPath, singles[0].line, singles[0].name))
	} else if len(singles) >= minTrivialSinglesPerFile {
		names := make([]string, len(singles))
		for i, s := range singles {
			names[i] = s.name
		}
		out = append(out, fmt.Sprintf("%s: excessive trivial wrapper fragmentation (%d single-use 1-line return helpers: %s); inline 1-line single-caller wrappers instead of fragmenting module (Law 6)", relPath, len(singles), strings.Join(names, ", ")))
	}
	if len(multiNames) > maxMultiOneLinersPy {
		out = append(out, fmt.Sprintf("%s: module defines %d multi-caller one-line return micro-helpers (%s; max %d); inline trivial predicates/wrappers or consolidate into a dispatch table (Law 6)", relPath, len(multiNames), strings.Join(multiNames, ", "), maxMultiOneLinersPy))
	}
	return out
}

// classifyPyOneLiner classifies a Python function into dead fossil, middleman, or micro-helper buckets.
func classifyPyOneLiner(fn pyFuncBlock, corpusCounts map[string]int) wrapperClass {
	if isSkippablePyFunc(fn) {
		return wrapperNone
	}
	execLines, bodyFossil := extractPythonExecBodyInfo(fn.bodyLines)
	if len(execLines) != 1 || !strings.HasPrefix(execLines[0], "return ") {
		return wrapperNone
	}
	retLine := execLines[0]
	extRefs := corpusCounts[fn.name] - (countWordInLines(fn.name, fn.bodyLines) + 1)
	if extRefs <= 0 {
		return classifyDeadPyWrapper(fn, retLine, bodyFossil)
	}
	if extRefs == 1 {
		if isPureCallForwardingReturn(retLine) {
			return wrapperSingleMiddleman
		}
		return wrapperTrivialSingle
	}
	return wrapperMultiOneLiner
}

// classifyDeadPyWrapper checks whether an uncalled Python 1-liner is a method or compatibility fossil.
func classifyDeadPyWrapper(fn pyFuncBlock, retLine string, bodyFossil bool) wrapperClass {
	hasFossil := fn.hasFossilNote || bodyFossil
	hasCallLike := strings.Contains(retLine, "(") || strings.Contains(retLine, "[")
	if fn.indent > 0 && (hasCallLike || hasFossil) {
		return wrapperDeadMethod
	}
	if fn.indent == 0 && (strings.HasPrefix(fn.name, "_") || hasFossil) && (hasCallLike || hasFossil) {
		return wrapperDeadTopFossil
	}
	return wrapperNone
}

// scanPolyglotFileWrappers enforces zero-caller and multi-caller wrapper ceilings for brace/Ruby modules.
func scanPolyglotFileWrappers(relPath string, src []byte, corpusCounts map[string]int) []string {
	lang := treesitter.DetectLanguage(relPath)
	funcs, err := treesitter.ExtractFunctions(lang, relPath, src)
	if err != nil || len(funcs) == 0 {
		return nil
	}
	lines := strings.Split(string(src), "\n")
	var out, multiNames []string
	for _, fn := range funcs {
		retLine, bodyLines, ok := extractPolyglotSingleReturn(lang, fn, lines)
		if !ok || polyglotSkipWrapperN[fn.Name] {
			continue
		}
		extRefs := corpusCounts[fn.Name] - countWordInLines(fn.Name, bodyLines)
		if msg, isMulti := classifyPolyglotWrapper(relPath, fn.Name, fn.Line, retLine, extRefs); msg != "" {
			out = append(out, msg)
		} else if isMulti {
			multiNames = append(multiNames, fn.Name)
		}
	}
	limit := resolveMultiWrapperLimit(lang)
	if len(multiNames) > limit {
		out = append(out, fmt.Sprintf("%s: module defines %d multi-caller one-line return micro-helpers (%s; max %d); inline trivial helpers or consolidate into a table (Law 6)", relPath, len(multiNames), strings.Join(multiNames, ", "), limit))
	}
	return out
}

// resolveMultiWrapperLimit selects per-language cap on multi-caller one-line helper functions.
func resolveMultiWrapperLimit(lang string) int {
	if lang == "ruby" {
		return maxMultiOneLinersRuby
	}
	return maxMultiOneLinersBrace
}

// classifyPolyglotWrapper classifies dead private wrappers, forwarding shims, and multi-caller helpers.
func classifyPolyglotWrapper(relPath, name string, line int, retLine string, extRefs int) (string, bool) {
	if extRefs <= 0 {
		if strings.HasPrefix(name, "_") && !strings.HasPrefix(name, "__") {
			return fmt.Sprintf("%s:%d: dead private wrapper %q has 0 callers; remove unused shim (Law 6)", relPath, line, name), false
		}
		return "", false
	}
	if extRefs == 1 && isBracePassThroughCall(retLine) {
		return fmt.Sprintf("%s:%d: single-caller pass-through middleman %q forwards directly with 1 caller; inline target call (Law 6)", relPath, line, name), false
	}
	return "", extRefs >= 2
}

// isBracePassThroughCall checks whether a single-return body directly forwards to another call.
func isBracePassThroughCall(retStmt string) bool {
	trimmed := strings.TrimSpace(strings.TrimSuffix(retStmt, ";"))
	trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "return "))
	return polyglotMemberRe.MatchString(trimmed) && !strings.ContainsAny(trimmed, "+*/%<>|&^?:")
}

// extractPolyglotSingleReturn extracts a single-expression body from short Ruby or brace functions.
func extractPolyglotSingleReturn(lang string, fn treesitter.FunctionMetric, lines []string) (string, []string, bool) {
	startIdx := fn.Line - 1
	endIdx := startIdx + fn.LOC - 1
	if startIdx < 0 || endIdx >= len(lines) || fn.LOC > 5 || hasPrecedingAttributeOrGetter(lines, startIdx) {
		return "", nil, false
	}
	span := lines[startIdx : endIdx+1]
	if lang == "ruby" {
		return extractRubySingleReturn(span)
	}
	return extractBraceSingleReturn(span)
}

// hasPrecedingAttributeOrGetter exempts decorated getters, setters, and attributes from wrapper flags.
func hasPrecedingAttributeOrGetter(lines []string, startIdx int) bool {
	header := strings.TrimSpace(lines[startIdx])
	if strings.HasPrefix(header, "get ") || strings.HasPrefix(header, "set ") {
		return true
	}
	for j := startIdx - 1; j >= 0 && startIdx-j <= 2; j-- {
		t := strings.TrimSpace(lines[j])
		if t == "" {
			continue
		}
		return strings.HasPrefix(t, "@") || strings.HasPrefix(t, "#[")
	}
	return false
}

// extractRubySingleReturn isolates one executable line from a short Ruby def..end block.
func extractRubySingleReturn(span []string) (string, []string, bool) {
	var body []string
	for i := 1; i < len(span); i++ {
		t := strings.TrimSpace(span[i])
		if t != "" && t != "end" && !strings.HasPrefix(t, "#") {
			body = append(body, t)
		}
	}
	if len(body) != 1 {
		return "", nil, false
	}
	return body[0], span, true
}

// extractBraceSingleReturn isolates one statement from an arrow expression or single-statement block.
func extractBraceSingleReturn(span []string) (string, []string, bool) {
	joined := strings.Join(span, "\n")
	if strings.Contains(joined, "=>") && !strings.Contains(joined, "{") {
		expr := strings.TrimSpace(joined[strings.Index(joined, "=>")+2:])
		if expr == "" || strings.Contains(expr, "<") {
			return "", nil, false
		}
		return expr, span, true
	}
	openIdx := strings.IndexByte(joined, '{')
	closeIdx := strings.LastIndexByte(joined, '}')
	if openIdx < 0 || closeIdx <= openIdx {
		return "", nil, false
	}
	return singleStmtFromBraceInner(joined[openIdx+1:closeIdx], span)
}

// singleStmtFromBraceInner returns the sole uncommented statement in a brace body when non-trivial.
func singleStmtFromBraceInner(inner string, span []string) (string, []string, bool) {
	var stmts []string
	for _, raw := range strings.Split(inner, "\n") {
		code, comment := splitSlashLineCommentOutsideStrings(raw)
		if t := strings.TrimSpace(code); t != "" || (t != "" && comment == "") {
			stmts = append(stmts, t)
		}
	}
	if len(stmts) != 1 || strings.Contains(stmts[0], "<") {
		return "", nil, false
	}
	if s := stmts[0]; strings.HasPrefix(s, "return ") || !strings.HasSuffix(s, ";") {
		return s, span, true
	}
	return "", nil, false
}
