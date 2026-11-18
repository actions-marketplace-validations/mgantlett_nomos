// Package astdebt implements non-Python polyglot control-flow, Go-tuple hallucination, and collection/nullish slop checks (Law 2 & Law 6).
package astdebt

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	braceTypeGuardThrowRe = regexp.MustCompile(`\b(?:typeof\s+[A-Za-z_][A-Za-z0-9_]*|instanceof\s+[A-Z]|is_a\?\s*\(|kind_of\?\s*\()`)
	braceErrorThrowRe     = regexp.MustCompile(`\b(?:throw\s+new|throw\s+|raise\s+|bail!\s*\(|panic!\s*\(|fail\s*\(|abort\s*\()`)
	braceIfEqJumpHeaderRe = regexp.MustCompile(`^\s*if\s*\(\s*([A-Za-z_][A-Za-z0-9_\.]*)\s*(?:===|==)\s*[^)]+\)\s*\{\s*(?:return|throw)\b.*\}\s*$`)
	braceReturnArrayRe    = regexp.MustCompile(`\breturn\s+\[\s*([^,\]]+)\s*,\s*([^\]]+)\s*\]\s*;?`)
	tsVerboseNullishRe    = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_\.]*)\s*!==\s*(?:null|undefined)\s*&&\s*([A-Za-z_][A-Za-z0-9_\.]*)\s*!==\s*(?:undefined|null)\s*\?\s*([A-Za-z_][A-Za-z0-9_\.]*)\s*:`)
	braceIfFalseRetRe     = regexp.MustCompile(`^\s*if\s*\(.+\)\s*\{\s*return\s+(false|true)\s*;?\s*\}\s*$`)
	braceSimpleBoolRetRe  = regexp.MustCompile(`^\s*return\s+(.+?)\s*;?\s*$`)

	polyglotTupleCheckExts = map[string]bool{
		ExtTS: true, ExtTSX: true, ExtJS: true, ExtJSX: true,
		ExtMJS: true, ExtCJS: true, ExtRB: true, ExtRake: true,
	}
	polyglotNullishCheckExts = map[string]bool{
		ExtTS: true, ExtTSX: true, ExtJS: true, ExtJSX: true,
	}
)

// scanPolyglotIdiomAndControlFlowSlop evaluates non-Python, non-Go polyglot sources for control-flow and idiom slop.
func scanPolyglotIdiomAndControlFlowSlop(_, relPath, text string) []string {
	ext := strings.ToLower(filepath.Ext(relPath))
	if ext == ExtGo || ext == ExtSvelte || ext == ExtPy {
		return nil
	}
	lines := strings.Split(text, "\n")
	var out []string
	out = append(out, scanPolyglotTypeGuardSprawl(relPath, ext, lines)...)
	out = append(out, scanPolyglotEqChainAndBoolReturn(relPath, lines)...)
	out = append(out, scanPolyglotArrayTupleReturns(relPath, ext, lines)...)
	out = append(out, scanTSNullishVerbosity(relPath, ext, lines)...)
	return out
}

// scanPolyglotTypeGuardSprawl flags scattered runtime type-guard throws across polyglot non-Go modules.
func scanPolyglotTypeGuardSprawl(relPath, ext string, lines []string) []string {
	if ext == ExtGo {
		return nil
	}
	guards := 0
	for _, raw := range lines {
		code, cmt := splitSlashLineCommentOutsideStrings(raw)
		if strings.TrimSpace(code) == "" && cmt != "" {
			continue
		}
		if braceTypeGuardThrowRe.MatchString(code) && braceErrorThrowRe.MatchString(code) {
			guards++
		}
	}
	if guards >= 5 {
		return []string{fmt.Sprintf("%s:1: module scatters %d inline runtime type-guard throws; centralize schema validation at the boundary", relPath, guards)}
	}
	return nil
}

// scanPolyglotEqChainAndBoolReturn detects consecutive single-variable jump ifs and redundant bool-return pairs.
func scanPolyglotEqChainAndBoolReturn(relPath string, lines []string) []string {
	var out []string
	lastVar, runLen, runStart := "", 0, 0
	for i := 0; i < len(lines); i++ {
		code, cmt := splitSlashLineCommentOutsideStrings(lines[i])
		trimmed := strings.TrimSpace(code)
		if trimmed == "" || (trimmed == "" && cmt != "") {
			continue
		}
		lastVar, runLen, runStart = stepBraceEqChain(relPath, i+1, trimmed, lastVar, runLen, runStart, &out)
		if i+1 < len(lines) {
			if msg := checkBraceRedundantBoolPair(relPath, i, trimmed, lines[i+1]); msg != "" {
				out = append(out, msg)
			}
		}
	}
	if runLen >= 2 {
		out = append(out, fmt.Sprintf("%s:%d: %d consecutive 'if (%s === ...)' early-return checks; consolidate into a lookup table or switch", relPath, runStart, runLen, lastVar))
	}
	return out
}

// stepBraceEqChain tracks contiguous same-variable early-return checks in brace code.
func stepBraceEqChain(relPath string, lineNum int, trimmed, lastVar string, runLen, runStart int, out *[]string) (string, int, int) {
	if m := braceIfEqJumpHeaderRe.FindStringSubmatch(trimmed); len(m) == 2 {
		if m[1] == lastVar {
			return lastVar, runLen + 1, runStart
		}
		flushBraceEqChain(relPath, lastVar, runLen, runStart, out)
		return m[1], 1, lineNum
	}
	flushBraceEqChain(relPath, lastVar, runLen, runStart, out)
	return "", 0, 0
}

// flushBraceEqChain emits a violation when at least two consecutive jump checks terminate.
func flushBraceEqChain(relPath, lastVar string, runLen, runStart int, out *[]string) {
	if runLen >= 2 {
		*out = append(*out, fmt.Sprintf("%s:%d: %d consecutive 'if (%s === ...)' early-return checks; consolidate into a lookup table or switch", relPath, runStart, runLen, lastVar))
	}
}

// checkBraceRedundantBoolPair flags if-return-bool immediately followed by a boolean expression return.
func checkBraceRedundantBoolPair(relPath string, idx int, firstTrimmed, nextRaw string) string {
	m := braceIfFalseRetRe.FindStringSubmatch(firstTrimmed)
	if len(m) != 2 {
		return ""
	}
	nextCode, nextCmt := splitSlashLineCommentOutsideStrings(nextRaw)
	if strings.TrimSpace(nextCode) == "" && nextCmt != "" {
		return ""
	}
	bm := braceSimpleBoolRetRe.FindStringSubmatch(strings.TrimSpace(nextCode))
	if len(bm) != 2 {
		return ""
	}
	expr := strings.TrimSpace(bm[1])
	if isBraceBoolExprCandidate(expr, m[1]) {
		return fmt.Sprintf("%s:%d: redundant 'if (...) { return %s; }' before boolean return; combine into a single boolean return expression", relPath, idx+1, m[1])
	}
	return ""
}

// isBraceBoolExprCandidate checks whether an expression is an opposite boolean literal or boolean predicate.
func isBraceBoolExprCandidate(expr, firstBool string) bool {
	if (firstBool == "true" && expr == "false") || (firstBool == "false" && expr == "true") {
		return true
	}
	for _, op := range []string{"===", "!==", "&&", "||"} {
		if strings.Contains(expr, op) {
			return true
		}
	}
	return false
}

// scanPolyglotArrayTupleReturns rejects Go-style [val, err] and [ok, val] array returns outside Go.
func scanPolyglotArrayTupleReturns(relPath, ext string, lines []string) []string {
	if !polyglotTupleCheckExts[ext] {
		return nil
	}
	failCnt, okCnt, firstLine := 0, 0, 0
	for i, raw := range lines {
		df, dok := classifyArrayTupleReturnLine(raw)
		if (df > 0 || dok > 0) && firstLine == 0 {
			firstLine = i + 1
		}
		failCnt += df
		okCnt += dok
	}
	if failCnt > 0 && okCnt > 0 && failCnt+okCnt >= 2 {
		return []string{fmt.Sprintf("%s:%d: uses Go-style [value, err]/[ok, value] array tuple returns; throw/raise an Error or return a typed object instead (Law 6)", relPath, firstLine)}
	}
	return nil
}

func isNullOrNilToken(tok string) bool {
	return tok == "null" || tok == "nil"
}

// classifyArrayTupleReturnLine classifies a two-element bracketed array return into failure vs success sentinel counts.
func classifyArrayTupleReturnLine(raw string) (int, int) {
	code, cmt := splitSlashLineCommentOutsideStrings(raw)
	if strings.TrimSpace(code) == "" && cmt != "" {
		return 0, 0
	}
	sanitized := polyglotStringLitRe.ReplaceAllString(code, "$STR")
	m := braceReturnArrayRe.FindStringSubmatch(sanitized)
	if len(m) != 3 {
		return 0, 0
	}
	lhs, rhs := strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
	if lhs == "false" || rhs == "false" {
		return 1, 0
	}
	if lhs == "true" || rhs == "true" {
		return 0, 1
	}
	if isNullOrNilToken(lhs) && !isNullOrNilToken(rhs) {
		return 1, 0
	}
	if isNullOrNilToken(rhs) && !isNullOrNilToken(lhs) {
		return 0, 1
	}
	return 0, 0
}

// scanTSNullishVerbosity flags explicit null and undefined guard ternaries replaceable by nullish coalescing.
func scanTSNullishVerbosity(relPath, ext string, lines []string) []string {
	if !polyglotNullishCheckExts[ext] {
		return nil
	}
	var out []string
	for i, raw := range lines {
		code, cmt := splitSlashLineCommentOutsideStrings(raw)
		if strings.TrimSpace(code) == "" && cmt != "" {
			continue
		}
		if nm := tsVerboseNullishRe.FindStringSubmatch(code); len(nm) == 4 && nm[1] == nm[2] && nm[1] == nm[3] {
			out = append(out, fmt.Sprintf("%s:%d: verbose null+undefined ternary check; replace with nullish coalescing '??'", relPath, i+1))
		}
	}
	return out
}
