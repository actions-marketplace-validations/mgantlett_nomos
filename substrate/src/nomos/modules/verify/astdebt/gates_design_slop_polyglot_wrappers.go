// Package astdebt enforces polyglot two-tier one-line wrapper and pass-through slop detection (Law 6).
package astdebt

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/treesitter"
)

const (
	maxMultiOneLinersBrace = 3
	maxMultiOneLinersRuby  = 2
)

var (
	polyglotMemberRe     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:(?:\.|::)[A-Za-z_][A-Za-z0-9_]*)*\(`)
	polyglotSkipWrapperN = map[string]bool{
		"main": true, "constructor": true, "new": true, "initialize": true,
		"default": true, "format": true, "read": true, "write": true, "close": true,
		"keys": true, "items": true, "values": true, "copy": true, "clone": true,
		"eq": true, "fmt": true, "run": true, "render": true, "init": true,
		"dispose": true, "destroy": true, "toJSON": true, "toString": true, "valueOf": true,
	}
)

func isPolyglotDesignSlopExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ExtPy, ExtRB, ExtRake, ExtTS, ExtTSX, ExtJS, ExtJSX, ExtMJS, ExtCJS, ExtRS, ExtJava, ExtKT, ExtKTS, ExtCS:
		return true
	default:
		return false
	}
}

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

func resolveMultiWrapperLimit(lang string) int {
	if lang == "ruby" {
		return maxMultiOneLinersRuby
	}
	return maxMultiOneLinersBrace
}

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
	if extRefs >= 2 {
		return "", true
	}
	return "", false
}

func isBracePassThroughCall(retStmt string) bool {
	trimmed := strings.TrimSpace(strings.TrimSuffix(retStmt, ";"))
	trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "return "))
	if !polyglotMemberRe.MatchString(trimmed) {
		return false
	}
	return !strings.ContainsAny(trimmed, "+*/%<>|&^?:")
}

func extractPolyglotSingleReturn(lang string, fn treesitter.FunctionMetric, lines []string) (string, []string, bool) {
	startIdx := fn.Line - 1
	endIdx := startIdx + fn.LOC - 1
	if startIdx < 0 || endIdx >= len(lines) || fn.LOC > 5 {
		return "", nil, false
	}
	if hasPrecedingAttributeOrGetter(lines, startIdx) {
		return "", nil, false
	}
	span := lines[startIdx : endIdx+1]
	if lang == "ruby" {
		return extractRubySingleReturn(span)
	}
	return extractBraceSingleReturn(span)
}

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
		if strings.HasPrefix(t, "@") || strings.HasPrefix(t, "#[") {
			return true
		}
		break
	}
	return false
}

func extractRubySingleReturn(span []string) (string, []string, bool) {
	var body []string
	for i := 1; i < len(span); i++ {
		t := strings.TrimSpace(span[i])
		if t == "" || t == "end" || strings.HasPrefix(t, "#") {
			continue
		}
		body = append(body, t)
	}
	if len(body) != 1 {
		return "", nil, false
	}
	return body[0], span, true
}

func extractBraceSingleReturn(span []string) (string, []string, bool) {
	joined := strings.Join(span, "\n")
	if strings.Contains(joined, "=>") && !strings.Contains(joined, "{") {
		idx := strings.Index(joined, "=>")
		expr := strings.TrimSpace(joined[idx+2:])
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

func singleStmtFromBraceInner(inner string, span []string) (string, []string, bool) {
	var stmts []string
	for _, raw := range strings.Split(inner, "\n") {
		code, comment := splitSlashLineCommentOutsideStrings(raw)
		t := strings.TrimSpace(code)
		if t == "" || (t == "" && comment != "") {
			continue
		}
		stmts = append(stmts, t)
	}
	if len(stmts) != 1 || strings.Contains(stmts[0], "<") {
		return "", nil, false
	}
	s := stmts[0]
	if !strings.HasPrefix(s, "return ") && !strings.HasSuffix(s, ";") {
		return s, span, true
	}
	if strings.HasPrefix(s, "return ") {
		return s, span, true
	}
	return "", nil, false
}
