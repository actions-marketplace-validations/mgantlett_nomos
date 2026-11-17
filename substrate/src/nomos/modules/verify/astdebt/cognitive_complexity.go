// Package astdebt enforces per-function polyglot Cognitive Complexity bounds (Law 2 & Law 6).
package astdebt

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// MaxFunctionCognitiveComplexity defines the statutory upper bound on per-function cognitive complexity (@intent tree_sitter_ast_gate_spec.md).
const MaxFunctionCognitiveComplexity = 15

var (
	cogPyFuncHeaderRe    = regexp.MustCompile(`^(?:async\s+)?def\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	cogBraceFuncHeaderRe = regexp.MustCompile(`^(?:(?:pub(?:\([^)]*\))?\s+|export\s+|public\s+|private\s+|protected\s+|internal\s+|static\s+|async\s+|virtual\s+|override\s+|final\s+)*)(?:(?:function|fn)\s+([A-Za-z_][A-Za-z0-9_]*)|([A-Za-z_][A-Za-z0-9_]*)\s*(?:<[^>]*>)?)\s*\(`)
	cogIndentBranchRe    = regexp.MustCompile(`^(if|elif|else|for|while|except|rescue|match)\b`)
	cogBraceBranchRe     = regexp.MustCompile(`^(else\s+if|if|else|for|foreach|while|catch|switch|match)\b`)
	cogPyBoolTokenRe     = regexp.MustCompile(`\b(and|or)\b`)
	cogBraceBoolTokenRe  = regexp.MustCompile(`(&&|\|\|)`)
)

// EvaluatePolyglotCognitiveViolations flags non-test polyglot functions whose nesting-weighted cognitive complexity exceeds maxCog (@intent tree_sitter_ast_gate_spec.md).
func EvaluatePolyglotCognitiveViolations(relPath string, src []byte, maxCog int) []string {
	if len(src) == 0 || !isCognitiveCheckableFile(relPath) {
		return nil
	}
	if maxCog <= 0 {
		maxCog = MaxFunctionCognitiveComplexity
	}
	var out []string
	for _, fn := range analyzePolyglotCognitiveScores(relPath, string(src)) {
		if fn.score > maxCog {
			out = append(out, fmt.Sprintf(
				"%s:%d: function %s cognitive complexity %d exceeds max %d; flatten nested control flow or extract a focused helper",
				relPath, fn.startLine, fn.name, fn.score, maxCog,
			))
		}
	}
	return out
}

// MaxFileCognitiveComplexity returns the highest per-function cognitive complexity in a polyglot source file (@intent tree_sitter_ast_gate_spec.md).
func MaxFileCognitiveComplexity(relPath string, src []byte) int {
	if len(src) == 0 || !isCognitiveCheckableFile(relPath) {
		return 0
	}
	best := 0
	for _, fn := range analyzePolyglotCognitiveScores(relPath, string(src)) {
		if fn.score > best {
			best = fn.score
		}
	}
	return best
}

func isCognitiveCheckableFile(relPath string) bool {
	ext := strings.ToLower(filepath.Ext(relPath))
	if ext == ExtGo || !isPolyglotNestingExt(ext) {
		return false
	}
	lower := strings.ToLower(filepath.ToSlash(relPath))
	base := filepath.Base(lower)
	if isPythonTestRelPath(lower) {
		return false
	}
	return !strings.HasSuffix(lower, "_test"+ext) &&
		!strings.Contains(base, ".test.") &&
		!strings.Contains(base, ".spec.")
}

type cogFuncScore struct {
	name      string
	startLine int
	score     int
}

func analyzePolyglotCognitiveScores(relPath, text string) []cogFuncScore {
	ext := strings.ToLower(filepath.Ext(relPath))
	if ext == ExtPy || ext == ExtRB || ext == ExtRake {
		return scoreIndentCognitiveFuncs(text)
	}
	return scoreBraceCognitiveFuncs(text)
}

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
		s.stack = append(s.stack, indent)
	} else {
		s.curScore += 1 + len(s.stack)
		s.stack = append(s.stack, indent)
	}
	s.curScore += countCogBooleanRuns(cogPyBoolTokenRe, cleaned)
}

type braceCogState struct {
	results       []cogFuncScore
	curName       string
	curStart      int
	curScore      int
	funcDepth     int
	braceDepth    int
	controlBraces []int
	inBlockCmt    bool
}

func scoreBraceCognitiveFuncs(text string) []cogFuncScore {
	st := &braceCogState{funcDepth: -1}
	for idx, rawLine := range strings.Split(text, "\n") {
		st.step(idx+1, rawLine)
	}
	st.flush()
	return st.results
}

func (s *braceCogState) flush() {
	if s.funcDepth < 0 {
		return
	}
	s.results = append(s.results, cogFuncScore{name: s.curName, startLine: s.curStart, score: s.curScore})
	s.curName = ""
	s.curStart = 0
	s.curScore = 0
	s.funcDepth = -1
	s.controlBraces = s.controlBraces[:0]
}

func (s *braceCogState) step(lineNum int, rawLine string) {
	cleaned := stripBraceCommentsAndStrings(rawLine, &s.inBlockCmt)
	trimmed := strings.TrimSpace(cleaned)
	opens := strings.Count(trimmed, "{")
	closes := strings.Count(trimmed, "}")
	if s.funcDepth < 0 && opens > closes {
		if name := extractBraceFuncName(trimmed); name != "" {
			s.curName = name
			s.curStart = lineNum
			s.curScore = 0
			s.funcDepth = s.braceDepth + 1
		}
	} else if s.funcDepth >= 0 && trimmed != "" {
		s.scoreLineInsideFunc(trimmed, opens > closes)
	}
	s.braceDepth += opens - closes
	if s.braceDepth < 0 {
		s.braceDepth = 0
	}
	if s.funcDepth >= 0 && s.braceDepth < s.funcDepth {
		s.flush()
		return
	}
	s.controlBraces = trimControlBraces(s.controlBraces, s.braceDepth)
}

func (s *braceCogState) scoreLineInsideFunc(trimmed string, opensBlock bool) {
	if bm := cogBraceBranchRe.FindStringSubmatch(trimmed); len(bm) == 2 {
		kw := bm[1]
		if strings.HasPrefix(kw, "else") {
			s.curScore++
		} else {
			s.curScore += 1 + len(s.controlBraces)
		}
		if opensBlock {
			s.controlBraces = append(s.controlBraces, s.braceDepth+1)
		}
	}
	s.curScore += countCogBooleanRuns(cogBraceBoolTokenRe, trimmed)
}

func extractBraceFuncName(trimmed string) string {
	m := cogBraceFuncHeaderRe.FindStringSubmatch(trimmed)
	if len(m) != 3 {
		return ""
	}
	name := firstNonEmptyCog(m[1], m[2])
	if name == "" || nonFuncControlKeywords[name] {
		return ""
	}
	return name
}

func firstNonEmptyCog(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func countCogBooleanRuns(re *regexp.Regexp, line string) int {
	matches := re.FindAllString(line, -1)
	if len(matches) == 0 {
		return 0
	}
	runs := 0
	prev := ""
	for _, op := range matches {
		if op != prev {
			runs++
			prev = op
		}
	}
	return runs
}

func stripInlineStringsAndHashComment(line string) string {
	var b strings.Builder
	quote, escaped := byte(0), false
	for i := 0; i < len(line); i++ {
		if escaped {
			escaped = false
			continue
		}
		ch := line[i]
		if quote != 0 {
			quote, escaped = stepSingleQuoteByte(ch, quote)
			continue
		}
		if ch == '#' {
			break
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			ch = ' '
		}
		b.WriteByte(ch)
	}
	return b.String()
}

type braceLineStripper struct {
	out     strings.Builder
	inBlock bool
	quote   byte
	escaped bool
}

func stripBraceCommentsAndStrings(line string, inBlockCmt *bool) string {
	st := &braceLineStripper{inBlock: *inBlockCmt}
	for i := 0; i < len(line); i++ {
		advance, done := st.step(line, i)
		if done {
			break
		}
		i += advance
	}
	*inBlockCmt = st.inBlock
	return st.out.String()
}

func (s *braceLineStripper) step(line string, i int) (int, bool) {
	ch := line[i]
	if s.inBlock {
		if ch == '*' && i+1 < len(line) && line[i+1] == '/' {
			s.inBlock = false
			return 1, false
		}
		return 0, false
	}
	if s.escaped {
		s.escaped = false
		return 0, false
	}
	if s.quote != 0 {
		s.quote, s.escaped = stepSingleQuoteByte(ch, s.quote)
		return 0, false
	}
	return s.stepCodeByte(line, i, ch)
}

func (s *braceLineStripper) stepCodeByte(line string, i int, ch byte) (int, bool) {
	if ch == '/' && i+1 < len(line) {
		if line[i+1] == '/' {
			return 0, true
		}
		if line[i+1] == '*' {
			s.inBlock = true
			return 1, false
		}
	}
	if ch == '\'' || ch == '"' || ch == '`' {
		s.quote = ch
		s.out.WriteByte(' ')
		return 0, false
	}
	s.out.WriteByte(ch)
	return 0, false
}
