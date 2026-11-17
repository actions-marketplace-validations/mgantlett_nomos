// Package astdebt provides parsing, word counting, and call-forwarding helpers for Python wrapper slop detection.
package astdebt

import (
	"regexp"
	"strings"
)

var (
	pyCalleePrefixRe       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*$`)
	pyForwardDisallowedKwRe = regexp.MustCompile(`\b(?:and|or|not|if|else|for|in|lambda|is)\b`)
)

func accumulateWordCounts(text string, counts map[string]int) {
	start := -1
	for i := 0; i <= len(text); i++ {
		var ch byte
		if i < len(text) {
			ch = text[i]
		}
		if isWordIdentByte(ch, start >= 0) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			counts[text[start:i]]++
			start = -1
		}
	}
}

func isWordIdentByte(ch byte, continuing bool) bool {
	if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_' {
		return true
	}
	return continuing && ch >= '0' && ch <= '9'
}

func isPureCallForwardingReturn(retLine string) bool {
	if !strings.HasPrefix(retLine, "return ") {
		return false
	}
	rawExpr := strings.TrimSpace(strings.TrimPrefix(retLine, "return"))
	expr := strings.TrimSpace(stripInlineStringsAndHashComment(rawExpr))
	openIdx := strings.IndexByte(expr, '(')
	if openIdx <= 0 {
		return false
	}
	callee := strings.TrimSpace(expr[:openIdx])
	if !pyCalleePrefixRe.MatchString(callee) {
		return false
	}
	_, closeIdx, ok := extractBalancedParenSpanWithEnd(expr, openIdx)
	if !ok || closeIdx != len(expr)-1 {
		return false
	}
	args := expr[openIdx+1 : closeIdx]
	if strings.ContainsAny(args, "+*/%<>|&^") {
		return false
	}
	return !pyForwardDisallowedKwRe.MatchString(args)
}

func parseTopLevelPyFuncs(text string) []pyFuncBlock {
	lines := strings.Split(text, "\n")
	var blocks []pyFuncBlock
	for i := 0; i < len(lines); i++ {
		m := pyTopDefRe.FindStringSubmatchIndex(lines[i])
		if len(m) != 4 {
			continue
		}
		name := lines[i][m[2]:m[3]]
		openParen := m[1] - 1
		decorated, leadFossil := inspectPrecedingDefMeta(lines, i)
		if inlineTail := extractPyDefInlineTail(lines[i], openParen); inlineTail != "" {
			blocks = append(blocks, pyFuncBlock{
				name:          name,
				startLine:     i + 1,
				bodyLines:     []string{inlineTail},
				decorated:     decorated,
				hasFossilNote: leadFossil,
			})
			continue
		}
		end := findPythonBlockEnd(lines, i+1, 0)
		blocks = append(blocks, pyFuncBlock{
			name:          name,
			startLine:     i + 1,
			bodyLines:     lines[i+1 : end],
			decorated:     decorated,
			hasFossilNote: leadFossil,
		})
		i = end - 1
	}
	return blocks
}

func inspectPrecedingDefMeta(lines []string, idx int) (bool, bool) {
	decorated := false
	seenDecorator := false
	leadFossil := false
	for j := idx - 1; j >= 0; j-- {
		t := strings.TrimSpace(lines[j])
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "#") {
			if pyFossilNoteRe.MatchString(t) {
				leadFossil = true
			}
			continue
		}
		if strings.HasPrefix(t, "@") {
			if !seenDecorator {
				decorated = true
			}
			seenDecorator = true
			continue
		}
		break
	}
	return decorated, leadFossil
}

func extractPyDefInlineTail(line string, openParen int) string {
	_, closeIdx, ok := extractBalancedParenSpanWithEnd(line, openParen)
	if !ok {
		return ""
	}
	colon := strings.IndexByte(line[closeIdx:], ':')
	if colon < 0 {
		return ""
	}
	tail := strings.TrimSpace(line[closeIdx+colon+1:])
	if tail == "" || strings.HasPrefix(tail, "#") {
		return ""
	}
	return tail
}

func findPythonBlockEnd(lines []string, startIdx, headerIndent int) int {
	j := startIdx
	for ; j < len(lines); j++ {
		trimmed := strings.TrimSpace(lines[j])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if countLeadingIndentCols(lines[j]) <= headerIndent {
			break
		}
	}
	return j
}

func extractPythonExecBodyInfo(bodyLines []string) ([]string, bool) {
	var exec []string
	inDocDelim := ""
	hasFossil := false
	for _, raw := range bodyLines {
		t := strings.TrimSpace(raw)
		if t == "" {
			continue
		}
		if inDocDelim != "" {
			if pyFossilNoteRe.MatchString(t) {
				hasFossil = true
			}
			if strings.Contains(t, inDocDelim) {
				inDocDelim = ""
			}
			continue
		}
		if strings.HasPrefix(t, "#") {
			if pyFossilNoteRe.MatchString(t) {
				hasFossil = true
			}
			continue
		}
		if delim := matchTripleQuoteAt(t, 0); delim != "" {
			if pyFossilNoteRe.MatchString(t) {
				hasFossil = true
			}
			if strings.Count(t, delim) < 2 {
				inDocDelim = delim
			}
			continue
		}
		exec = append(exec, t)
	}
	return exec, hasFossil
}

func countWordInLines(target string, lines []string) int {
	local := make(map[string]int, 16)
	for _, l := range lines {
		accumulateWordCounts(l, local)
	}
	return local[target]
}
