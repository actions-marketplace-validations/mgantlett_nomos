// Package astdebt enforces dead pass-through wrapper, fragmentation, type-guard ladder, and nesting checks (Law 2 & Law 6).
package astdebt

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	maxDesignSlopCorpusFiles = 64
	maxDesignSlopFileBytes   = 512 * 1024
)

var (
	pyTopDefRe            = regexp.MustCompile(`^(?:async\s+)?def\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	pyAnyDefRe            = regexp.MustCompile(`^(\s*)(?:async\s+)?def\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	pyIsinstanceIfRe      = regexp.MustCompile(`^if\s+not\s+isinstance\s*\(`)
	pyIfEqVarRe           = regexp.MustCompile(`^(\s*)if\s+([A-Za-z_][A-Za-z0-9_\.]*)\s*==\s*`)
	pyElseOrElifPrefixRe  = regexp.MustCompile(`^(?:elif|else)\b`)
	pyBranchKeywordRe     = regexp.MustCompile(`\b(?:if|else|for|while)\b`)
	designSlopCorpusExts  = map[string]bool{ExtPy: true, ExtJS: true, ExtJSX: true, ExtTS: true, ExtTSX: true}
	designSlopSkipDirName = map[string]bool{
		".git": true, ".venv": true, "venv": true, "node_modules": true,
		"__pycache__": true, "vendor": true, ".tmp": true, ".nomos": true,
	}
)

type pyFuncBlock struct {
	name      string
	startLine int
	bodyLines []string
	decorated bool
}

// DetectPolyglotDesignSlop detects dead private pass-through wrappers, 1-line wrapper fragmentation,
// inline isinstance guard ladders, and consecutive same-variable if-chains in non-test Python files (@intent tree_sitter_ast_gate_spec.md).
func DetectPolyglotDesignSlop(root string, sources map[string][]byte) []string {
	if len(sources) == 0 {
		return nil
	}
	corpusCounts := buildDesignSlopCorpusCounts(root, sources)
	var out []string
	for _, relPath := range sortedSourceKeys(sources) {
		if !strings.EqualFold(filepath.Ext(relPath), ExtPy) || isPythonTestRelPath(relPath) {
			continue
		}
		text := string(sources[relPath])
		out = append(out, scanPythonFileWrappers(relPath, text, corpusCounts)...)
		out = append(out, scanPythonFuncLadders(relPath, text)...)
	}
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

func buildDesignSlopCorpusCounts(root string, sources map[string][]byte) map[string]int {
	merged := make(map[string][]byte, len(sources)+8)
	collectWorkspaceSlopFiles(root, merged)
	for k, v := range sources {
		merged[filepath.ToSlash(k)] = v
	}
	counts := make(map[string]int, 256)
	for _, raw := range merged {
		accumulateWordCounts(string(raw), counts)
	}
	return counts
}

func collectWorkspaceSlopFiles(root string, dst map[string][]byte) {
	if root == "" {
		return
	}
	collected := 0
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil
		}
		if d.IsDir() {
			if path != root && designSlopSkipDirName[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if collected >= maxDesignSlopCorpusFiles || !designSlopCorpusExts[strings.ToLower(filepath.Ext(d.Name()))] {
			return nil
		}
		if key, raw, ok := readBoundedCorpusFile(root, path, d); ok {
			dst[key] = raw
			collected++
		}
		return nil
	})
}

func readBoundedCorpusFile(root, path string, d fs.DirEntry) (string, []byte, bool) {
	info, iErr := d.Info()
	if iErr != nil || info.Size() > maxDesignSlopFileBytes {
		return "", nil, false
	}
	raw, rErr := os.ReadFile(path)
	if rErr != nil {
		return "", nil, false
	}
	rel, relErr := filepath.Rel(root, path)
	if relErr != nil {
		return filepath.ToSlash(path), raw, true
	}
	return filepath.ToSlash(rel), raw, true
}

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

func scanPythonFileWrappers(relPath, text string, corpusCounts map[string]int) []string {
	var out []string
	var trivialSingles []string
	for _, fn := range parseTopLevelPyFuncs(text) {
		isDead, isTrivialSingle := classifyTrivialTopWrapper(fn, corpusCounts)
		if isDead {
			out = append(out, fmt.Sprintf("%s:%d: dead pass-through wrapper %q has 0 callers (checkpoint fossil); delete unused shim and call target directly (Law 6)", relPath, fn.startLine, fn.name))
			continue
		}
		if isTrivialSingle {
			trivialSingles = append(trivialSingles, fn.name)
		}
	}
	if len(trivialSingles) >= 3 {
		out = append(out, fmt.Sprintf("%s: excessive trivial wrapper fragmentation (%d single-use 1-line return helpers: %s); inline 1-line single-caller wrappers instead of fragmenting module (Law 6)", relPath, len(trivialSingles), strings.Join(trivialSingles, ", ")))
	}
	return out
}

func classifyTrivialTopWrapper(fn pyFuncBlock, corpusCounts map[string]int) (bool, bool) {
	if fn.decorated || fn.name == "main" || (strings.HasPrefix(fn.name, "__") && strings.HasSuffix(fn.name, "__")) {
		return false, false
	}
	execLines := extractPythonExecBodyLines(fn.bodyLines)
	if len(execLines) != 1 || !strings.HasPrefix(execLines[0], "return ") || pyBranchKeywordRe.MatchString(execLines[0]) {
		return false, false
	}
	extRefs := corpusCounts[fn.name] - (countWordInLines(fn.name, fn.bodyLines) + 1)
	if strings.HasPrefix(fn.name, "_") && extRefs <= 0 && strings.Contains(execLines[0], "(") {
		return true, false
	}
	return false, extRefs <= 1
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
		if inlineTail := extractPyDefInlineTail(lines[i], openParen); inlineTail != "" {
			blocks = append(blocks, pyFuncBlock{
				name:      name,
				startLine: i + 1,
				bodyLines: []string{inlineTail},
				decorated: hasPrecedingDecorator(lines, i),
			})
			continue
		}
		end := findPythonBlockEnd(lines, i+1, 0)
		blocks = append(blocks, pyFuncBlock{
			name:      name,
			startLine: i + 1,
			bodyLines: lines[i+1 : end],
			decorated: hasPrecedingDecorator(lines, i),
		})
		i = end - 1
	}
	return blocks
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

func hasPrecedingDecorator(lines []string, idx int) bool {
	for j := idx - 1; j >= 0; j-- {
		t := strings.TrimSpace(lines[j])
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		return strings.HasPrefix(t, "@")
	}
	return false
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

func extractPythonExecBodyLines(bodyLines []string) []string {
	var exec []string
	inDocDelim := ""
	for _, raw := range bodyLines {
		t := strings.TrimSpace(raw)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if inDocDelim != "" {
			if strings.Contains(t, inDocDelim) {
				inDocDelim = ""
			}
			continue
		}
		if delim := matchTripleQuoteAt(t, 0); delim != "" {
			if strings.Count(t, delim) < 2 {
				inDocDelim = delim
			}
			continue
		}
		exec = append(exec, t)
	}
	return exec
}

func countWordInLines(target string, lines []string) int {
	local := make(map[string]int, 16)
	for _, l := range lines {
		accumulateWordCounts(l, local)
	}
	return local[target]
}

func scanPythonFuncLadders(relPath, text string) []string {
	lines := strings.Split(text, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		m := pyAnyDefRe.FindStringSubmatch(lines[i])
		if len(m) != 3 {
			continue
		}
		indent := countLeadingIndentCols(m[1])
		end := findPythonBlockEnd(lines, i+1, indent)
		body := lines[i+1 : end]
		if guards := countInlineIsinstanceRaiseGuards(body); guards >= 4 {
			out = append(out, fmt.Sprintf("%s:%d: function %s contains %d inline 'if not isinstance(...): raise' guards; validate input schema once at boundary instead of repeating inline type-guard ladders", relPath, i+1, m[2], guards))
		}
		if chainLine, runLen, varName := findConsecutiveIfEqChain(body, i+2); runLen > 0 {
			out = append(out, fmt.Sprintf("%s:%d: %d consecutive 'if %s ==' checks at same indent without elif or dispatch table; use elif, match, or a lookup dict", relPath, chainLine, runLen, varName))
		}
	}
	return out
}

func countInlineIsinstanceRaiseGuards(body []string) int {
	guards := 0
	for i := 0; i < len(body); i++ {
		trimmed := strings.TrimSpace(body[i])
		if !pyIsinstanceIfRe.MatchString(trimmed) {
			continue
		}
		if strings.Contains(trimmed, ": raise ") || nextExecLineStartsWithRaise(body, i+1, countLeadingIndentCols(body[i])) {
			guards++
		}
	}
	return guards
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
		return strings.HasPrefix(t, "raise ") || t == "raise"
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
