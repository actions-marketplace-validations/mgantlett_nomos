// Package astdebt extracts cross-file and intra-file polyglot AST clone candidates across 10 languages (Law 6).
package astdebt

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/treesitter"
)

var (
	polyglotStringLitRe = regexp.MustCompile(`(?:"[^"\\]*(?:\\.[^"\\]*)*"|'[^'\\]*(?:\\.[^'\\]*)*')`)
	polyglotNumberLitRe = regexp.MustCompile(`\b\d+(?:\.\d+)?\b`)
	polyglotSelfCallRe  = regexp.MustCompile(`\b(?:self|this)\.[A-Za-z_][A-Za-z0-9_]*\(`)
	polyglotSpaceRe     = regexp.MustCompile(`\s+`)
)

func extractPolyglotCloneCandidates(lang, relPath string, src []byte, minLines int) []cloneBlockCandidate {
	funcs, err := treesitter.ExtractFunctions(lang, relPath, src)
	if err != nil || len(funcs) == 0 {
		return nil
	}
	crossMin, siblingEnabled := resolvePolyglotCloneBounds(lang, minLines)
	lines := strings.Split(string(src), "\n")
	var out []cloneBlockCandidate
	for _, fn := range funcs {
		if cand, ok := buildPolyglotFuncCloneCandidate(lang, relPath, fn, lines, crossMin, siblingEnabled); ok {
			out = append(out, cand)
		}
	}
	return out
}

func resolvePolyglotCloneBounds(lang string, minLines int) (int, bool) {
	if lang != "svelte" && minLines == DefaultMinCloneLines {
		return 8, true
	}
	return minLines, false
}

func buildPolyglotFuncCloneCandidate(lang, relPath string, fn treesitter.FunctionMetric, lines []string, crossMin int, siblingEnabled bool) (cloneBlockCandidate, bool) {
	startIdx := fn.Line - 1
	endIdx := startIdx + fn.LOC
	if startIdx < 0 || endIdx > len(lines) {
		return cloneBlockCandidate{}, false
	}
	canonRel := filepath.ToSlash(relPath)
	if fn.LOC >= crossMin {
		if skel, count := normalizePolyglotLines(lines[startIdx:endIdx], false); count > 0 && len(skel) >= 36 {
			return cloneBlockCandidate{file: canonRel, startLine: fn.Line, endLine: fn.Line + fn.LOC - 1, loc: fn.LOC, hash: hashSkeleton(lang + ":" + skel)}, true
		}
	}
	if siblingEnabled && fn.LOC >= 5 {
		if skel, count := normalizePolyglotLines(lines[startIdx:endIdx], true); count >= 4 && len(skel) >= 26 {
			return cloneBlockCandidate{file: canonRel, startLine: fn.Line, endLine: fn.Line + fn.LOC - 1, loc: fn.LOC, hash: hashSkeleton("intra:" + lang + ":" + canonRel + ":" + skel)}, true
		}
	}
	return cloneBlockCandidate{}, false
}

func normalizePolyglotLines(lines []string, normSelf bool) (string, int) {
	if len(lines) <= 1 {
		return "", 0
	}
	var cleaned []string
	inDoc := ""
	for i := 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if skip, nextDoc := stepPolyglotCloneDocLine(trimmed, inDoc); skip {
			inDoc = nextDoc
			continue
		}
		cleaned = append(cleaned, normalizePolyglotStmt(trimmed, normSelf))
	}
	return strings.Join(cleaned, "|"), len(cleaned)
}

func stepPolyglotCloneDocLine(trimmed, inDoc string) (bool, string) {
	if inDoc != "" {
		if strings.Contains(trimmed, inDoc) {
			return true, ""
		}
		return true, inDoc
	}
	if trimmed == "" || trimmed == "{" || trimmed == "}" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") {
		return true, ""
	}
	if delim := matchTripleQuoteAt(trimmed, 0); delim != "" {
		if strings.Count(trimmed, delim) < 2 {
			return true, delim
		}
		return true, ""
	}
	return false, ""
}

func normalizePolyglotStmt(trimmed string, normSelf bool) string {
	norm := polyglotStringLitRe.ReplaceAllString(trimmed, "$STR")
	norm = polyglotNumberLitRe.ReplaceAllString(norm, "$NUM")
	if normSelf {
		norm = polyglotSelfCallRe.ReplaceAllString(norm, "$SELF.(")
	}
	return polyglotSpaceRe.ReplaceAllString(norm, " ")
}
