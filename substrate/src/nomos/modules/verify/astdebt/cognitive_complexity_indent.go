// Package astdebt scores indent-based (Python/Ruby) per-function cognitive complexity and defines shared polyglot extension constants.
package astdebt

import (
	"regexp"
	"strings"
)

const (
	ExtGo     = ".go"
	ExtPy     = ".py"
	ExtTS     = ".ts"
	ExtTSX    = ".tsx"
	ExtJS     = ".js"
	ExtJSX    = ".jsx"
	ExtMJS    = ".mjs"
	ExtCJS    = ".cjs"
	ExtSvelte = ".svelte"
	ExtRB     = ".rb"
	ExtRake   = ".rake"
	ExtRS     = ".rs"
	ExtC      = ".c"
	ExtH      = ".h"
	ExtCPP    = ".cpp"
	ExtCC     = ".cc"
	ExtCXX    = ".cxx"
	ExtHPP    = ".hpp"
	ExtJava   = ".java"
	ExtKT     = ".kt"
	ExtKTS    = ".kts"
	ExtCS     = ".cs"

	DirTest      = "test"
	DirTests     = "tests"
	DirSpec      = "spec"
	DirCoverage  = "coverage"
	PrefixPyTest = "test_"
	SuffixGoTest = "_test.go"
	SuffixPyTest = "_test.py"
)

// MinInt returns the smaller of two integer values.
func MinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// IsJSFamilyExt reports whether ext belongs to JavaScript, TypeScript, or Svelte sources.
func IsJSFamilyExt(ext string) bool {
	switch ext {
	case ExtTS, ExtTSX, ExtJS, ExtJSX, ExtMJS, ExtCJS, ExtSvelte:
		return true
	default:
		return false
	}
}

// makeWordSet builds a fast string lookup set from whitespace-delimited tokens.
func makeWordSet(words string) map[string]bool {
	fields := strings.Fields(words)
	set := make(map[string]bool, len(fields))
	for _, w := range fields {
		set[w] = true
	}
	return set
}

// countLeadingIndentCols computes visual indentation column width using 4-space tab stops.
func countLeadingIndentCols(s string) int {
	cols := 0
	for _, ch := range s {
		if ch == ' ' {
			cols++
		} else if ch == '\t' {
			cols += 4
		} else {
			break
		}
	}
	return cols
}

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
	if trimmed == "" {
		return false
	}
	skip, nextDelim := stepPyDocDelim(trimmed, s.inDocDelim, nil)
	s.inDocDelim = nextDelim
	return skip
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
	if kw == "elif" || kw == "elsif" || kw == "else" {
		if len(s.stack) > 0 {
			s.curScore += 1 + len(s.stack) + 1
		} else {
			s.curScore++
		}
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
