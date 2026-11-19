// Package astdebt provides shared bounded corpus walking, word indexing, and Python function block parsing.
package astdebt

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	maxDesignSlopCorpusFiles = 512
	maxDesignSlopFileBytes   = 512 * 1024
)

type pyFuncBlock struct {
	name          string
	indent        int
	startLine     int
	sigEndIdx     int
	bodyLines     []string
	decorated     bool
	hasFossilNote bool
}

var (
	pyFossilNoteRe           = regexp.MustCompile(`(?i)\b(?:compatibility|backwards?[- ]compatible|legacy|deprecated|checkpoint)\b`)
	pyCalleePrefixRe         = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*$`)
	pyForwardDisallowedKwRe  = regexp.MustCompile(`\b(?:and|or|not|if|else|for|in|lambda|is)\b`)
	excludedClassMethodNames = makeWordSet("default format read write close keys items values copy main to_dict from_dict")
	designSlopCorpusExts     = map[string]bool{
		ExtPy: true, ExtRB: true, ExtRake: true, ExtGo: true,
		ExtTS: true, ExtTSX: true, ExtJS: true, ExtJSX: true, ExtMJS: true, ExtCJS: true,
		ExtRS: true, ExtJava: true, ExtKT: true, ExtKTS: true, ExtCS: true,
	}
	designSlopSkipDirName = makeWordSet(".git .venv venv node_modules __pycache__ vendor .tmp .nomos")
	pyExemptDecorators    = []string{
		"@property", "@abstractmethod", "@abc.abstractmethod",
		"@overload", "@typing.overload", "@override",
		"@cached_property", "@functools.cached_property",
		"@app.", "@router.", "@bp.", "@click.", "@pytest.", "@fixture",
	}
)

// buildDesignSlopCorpusCountsWithLimit indexes word frequencies across repo files up to maxFiles.
func buildDesignSlopCorpusCountsWithLimit(root string, sources map[string][]byte, maxFiles int) (map[string]int, bool) {
	merged := make(map[string][]byte, len(sources)+8)
	exceeded := collectWorkspaceSlopFiles(root, maxFiles, merged)
	for k, v := range sources {
		merged[filepath.ToSlash(filepath.Clean(k))] = v
	}
	if maxFiles > 0 && len(merged) > maxFiles {
		exceeded = true
	}
	counts := make(map[string]int, 256)
	for _, raw := range merged {
		accumulateWordCounts(string(raw), counts)
	}
	return counts, exceeded
}

type slopWalkState struct {
	root      string
	maxFiles  int
	collected int
	exceeded  bool
	dst       map[string][]byte
}

// collectWorkspaceSlopFiles walks root collecting bounded source files into dst.
func collectWorkspaceSlopFiles(root string, maxFiles int, dst map[string][]byte) bool {
	if root == "" || maxFiles <= 0 {
		return false
	}
	st := &slopWalkState{root: root, maxFiles: maxFiles, dst: dst}
	walkErr := filepath.WalkDir(root, st.visit)
	return (walkErr != nil && walkErr != fs.SkipAll) || st.exceeded
}

// visit filters skipped directories, extensions, and oversized corpus files.
func (s *slopWalkState) visit(path string, d fs.DirEntry, err error) error {
	if err != nil || d == nil {
		return nil
	}
	if d.IsDir() {
		if path != s.root && designSlopSkipDirName[d.Name()] {
			return filepath.SkipDir
		}
		return nil
	}
	if !designSlopCorpusExts[strings.ToLower(filepath.Ext(d.Name()))] {
		return nil
	}
	if s.collected >= s.maxFiles {
		s.exceeded = true
		return fs.SkipAll
	}
	if key, raw, ok := readBoundedCorpusFile(s.root, path, d); ok {
		s.dst[key] = raw
		s.collected++
	}
	return nil
}

// readBoundedCorpusFile reads one corpus file under the per-file size limit.
func readBoundedCorpusFile(root, path string, d fs.DirEntry) (string, []byte, bool) {
	info, iErr := d.Info()
	if iErr != nil || info.Size() > maxDesignSlopFileBytes {
		return "", nil, false
	}
	raw, rErr := os.ReadFile(path)
	if rErr != nil {
		return "", nil, false
	}
	if rel, err := filepath.Rel(root, path); err == nil {
		return filepath.ToSlash(rel), raw, true
	}
	return filepath.ToSlash(path), raw, true
}

// isWordIdentByte reports whether ch can start or continue a normalized identifier token.
func isWordIdentByte(ch byte, continuing bool) bool {
	if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_' {
		return true
	}
	return continuing && ch >= '0' && ch <= '9'
}

// accumulateWordCounts tallies identifier tokens present in text into counts.
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

// countWordInLines counts identifier references for target across a slice of lines.
func countWordInLines(target string, lines []string) int {
	local := make(map[string]int, 16)
	for _, l := range lines {
		accumulateWordCounts(l, local)
	}
	return local[target]
}

// isPureCallForwardingReturn detects 1-line return statements that only forward to another call.
func isPureCallForwardingReturn(retLine string) bool {
	if !strings.HasPrefix(retLine, "return ") {
		return false
	}
	expr := strings.TrimSpace(stripInlineStringsAndHashComment(strings.TrimSpace(strings.TrimPrefix(retLine, "return"))))
	openIdx := strings.IndexByte(expr, '(')
	if openIdx <= 0 {
		return false
	}
	callee := strings.TrimSpace(expr[:openIdx])
	if !pyCalleePrefixRe.MatchString(callee) && !strings.Contains(callee, "[") {
		return false
	}
	_, closeIdx, ok := extractBalancedParenSpanWithEnd(expr, openIdx)
	if !ok || closeIdx != len(expr)-1 {
		return false
	}
	args := expr[openIdx+1 : closeIdx]
	return !strings.ContainsAny(args, "+*/%<>|&^") && !pyForwardDisallowedKwRe.MatchString(args)
}

// isSkippablePyFunc exempts decorated endpoints/properties, dunders, visitors, and interface methods.
func isSkippablePyFunc(fn pyFuncBlock) bool {
	if fn.decorated || fn.name == "main" || strings.HasPrefix(fn.name, "visit_") {
		return true
	}
	if strings.HasPrefix(fn.name, "__") && strings.HasSuffix(fn.name, "__") {
		return true
	}
	return fn.indent > 0 && excludedClassMethodNames[fn.name]
}

// parseAllPyFuncs extracts Python def blocks along with signature span and leading metadata.
func parseAllPyFuncs(text string) []pyFuncBlock {
	lines := strings.Split(text, "\n")
	var blocks []pyFuncBlock
	for i := 0; i < len(lines); i++ {
		m := pyAnyDefRe.FindStringSubmatchIndex(lines[i])
		if len(m) != 6 {
			continue
		}
		indent := countLeadingIndentCols(lines[i][m[2]:m[3]])
		name := lines[i][m[4]:m[5]]
		openParen := m[1] - 1
		sigEnd := findPyDefSignatureEnd(lines, i, openParen)
		decorated, leadFossil := inspectPrecedingDefMeta(lines, i)
		if tail := extractPyDefInlineTail(lines[sigEnd], openParen); sigEnd == i && tail != "" {
			blocks = append(blocks, pyFuncBlock{
				name: name, indent: indent, startLine: i + 1, sigEndIdx: i,
				bodyLines: []string{tail}, decorated: decorated, hasFossilNote: leadFossil,
			})
			continue
		}
		end := findPythonBlockEnd(lines, sigEnd+1, indent)
		blocks = append(blocks, pyFuncBlock{
			name: name, indent: indent, startLine: i + 1, sigEndIdx: sigEnd,
			bodyLines: lines[sigEnd+1 : end], decorated: decorated, hasFossilNote: leadFossil,
		})
	}
	return blocks
}

// findPyDefSignatureEnd locates the closing signature line containing the trailing colon.
func findPyDefSignatureEnd(lines []string, startIdx, openParen int) int {
	depth := 0
	for k := startIdx; k < len(lines); k++ {
		colStart := 0
		if k == startIdx {
			colStart = openParen
		}
		for c := colStart; c < len(lines[k]); c++ {
			if lines[k][c] == '(' {
				depth++
			} else if lines[k][c] == ')' && depth > 0 {
				depth--
			}
		}
		if depth <= 0 && strings.Contains(lines[k], ":") {
			return k
		}
	}
	return startIdx
}

// inspectPrecedingDefMeta scans comments and decorators immediately above a Python def.
func inspectPrecedingDefMeta(lines []string, idx int) (bool, bool) {
	decorated, leadFossil := false, false
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
			for _, prefix := range pyExemptDecorators {
				if strings.HasPrefix(strings.ToLower(t), prefix) {
					decorated = true
					break
				}
			}
			continue
		}
		break
	}
	return decorated, leadFossil
}

// extractPyDefInlineTail extracts single-line def suite text after the signature colon.
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

// findPythonBlockEnd returns the first line index dedented back to or past headerIndent.
func findPythonBlockEnd(lines []string, startIdx, headerIndent int) int {
	for j := startIdx; j < len(lines); j++ {
		trimmed := strings.TrimSpace(lines[j])
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") && countLeadingIndentCols(lines[j]) <= headerIndent {
			return j
		}
	}
	return len(lines)
}

// stepPyDocDelim tracks triple-quote docstring state and blank/comment skipping on trimmed lines.
func stepPyDocDelim(trimmed, inDoc string, onText func(string)) (bool, string) {
	if inDoc != "" {
		if onText != nil {
			onText(trimmed)
		}
		if strings.Contains(trimmed, inDoc) {
			return true, ""
		}
		return true, inDoc
	}
	if strings.HasPrefix(trimmed, "#") {
		if onText != nil {
			onText(trimmed)
		}
		return true, ""
	}
	if delim := matchTripleQuoteAt(trimmed, 0); delim != "" {
		if onText != nil {
			onText(trimmed)
		}
		if strings.Count(trimmed, delim) < 2 {
			return true, delim
		}
		return true, ""
	}
	return false, ""
}

// extractPythonExecBodyInfo strips docstrings and comments while checking for fossil markers.
func extractPythonExecBodyInfo(bodyLines []string) ([]string, bool) {
	var exec []string
	inDoc, hasFossil := "", false
	markFossil := func(t string) {
		if pyFossilNoteRe.MatchString(t) {
			hasFossil = true
		}
	}
	for _, raw := range bodyLines {
		t := strings.TrimSpace(raw)
		if t == "" {
			continue
		}
		if skip, nextDoc := stepPyDocDelim(t, inDoc, markFossil); skip {
			inDoc = nextDoc
			continue
		}
		exec = append(exec, t)
	}
	return exec, hasFossil
}
