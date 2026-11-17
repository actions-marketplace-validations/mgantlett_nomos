// Package astdebt scores indent-based (Python/Ruby) per-function cognitive complexity including inline ternaries and comprehension filters.
package astdebt

import (
	"regexp"
	"strings"
)

var (
	cogPyWordIfRe   = regexp.MustCompile(`\bif\b`)
	cogPyWordElseRe = regexp.MustCompile(`\belse\b`)
	cogPyCompForRe  = regexp.MustCompile(`\bfor\b[^\n:]*\bin\b`)
)

type indentCogState struct {
	results    []cogFuncScore
	curName    string
	curStart   int
	curScore   int
	baseIndent int
	stack      []int
	inDocDelim string
}

func (s *indentCogState) flush() {
	if s.baseIndent < 0 {
		return
	}
	s.results = append(s.results, cogFuncScore{name: s.curName, startLine: s.curStart, score: s.curScore})
	s.curName = ""
	s.curStart = 0
	s.curScore = 0
	s.baseIndent = -1
	s.stack = s.stack[:0]
}

func scoreIndentCognitiveFuncs(text string) []cogFuncScore {
	st := &indentCogState{baseIndent: -1}
	for idx, rawLine := range strings.Split(text, "\n") {
		st.step(idx+1, rawLine)
	}
	st.flush()
	return st.results
}

func (s *indentCogState) step(lineNum int, rawLine string) {
	trimmed := strings.TrimSpace(rawLine)
	if s.consumeDocstringLine(trimmed) || trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return
	}
	indent := countLeadingIndentCols(rawLine)
	if indent <= s.baseIndent && s.baseIndent >= 0 {
		s.flush()
	}
	if m := cogPyFuncHeaderRe.FindStringSubmatch(trimmed); len(m) == 2 {
		s.handleFuncHeader(lineNum, indent, m[1])
		return
	}
	if s.baseIndent < 0 {
		return
	}
	s.stack = trimIndentStack(s.stack, indent)
	cleaned := stripInlineStringsAndHashComment(trimmed)
	if bm := cogIndentBranchRe.FindStringSubmatch(cleaned); len(bm) == 2 {
		s.applyIndentBranch(bm[1], indent, cleaned)
		return
	}
	s.curScore += countCogBooleanRuns(cogPyBoolTokenRe, cleaned)
	s.curScore += scoreInlinePyConstructs(cleaned, len(s.stack))
}

func (s *indentCogState) consumeDocstringLine(trimmed string) bool {
	if s.inDocDelim != "" {
		if strings.Contains(trimmed, s.inDocDelim) {
			s.inDocDelim = ""
		}
		return true
	}
	if delim := matchTripleQuoteAt(trimmed, 0); delim != "" {
		if strings.Count(trimmed, delim) < 2 {
			s.inDocDelim = delim
		}
		return true
	}
	return false
}

func (s *indentCogState) handleFuncHeader(lineNum, indent int, name string) {
	if s.baseIndent < 0 {
		s.curName = name
		s.curStart = lineNum
		s.curScore = 0
		s.baseIndent = indent
		return
	}
	s.curScore += 1 + len(s.stack)
	s.stack = append(s.stack, indent)
}

func (s *indentCogState) applyIndentBranch(kw string, indent int, cleaned string) {
	if kw == "elif" || kw == "else" {
		s.curScore++
	} else {
		s.curScore += 1 + len(s.stack)
	}
	s.stack = append(s.stack, indent)
	s.curScore += countCogBooleanRuns(cogPyBoolTokenRe, cleaned)
	inlineTail := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(cleaned), kw))
	s.curScore += scoreInlinePyConstructs(inlineTail, len(s.stack))
}

func scoreInlinePyConstructs(tail string, depth int) int {
	if tail == "" {
		return 0
	}
	ternaries := len(cogPyWordElseRe.FindAllStringIndex(tail, -1))
	compFilters := 0
	if cogPyCompForRe.MatchString(tail) {
		totalIfs := len(cogPyWordIfRe.FindAllStringIndex(tail, -1))
		if totalIfs > ternaries {
			compFilters = totalIfs - ternaries
		}
	}
	constructs := ternaries + compFilters
	if constructs == 0 {
		return 0
	}
	return constructs * (1 + depth)
}
