// Package astdebt scans bounded workspace corpora for Python dead pass-through and trivial wrapper slop.
package astdebt

import (
	"fmt"
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

var (
	pyTopDefRe               = regexp.MustCompile(`^(?:async\s+)?def\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	pyClassMethodDefRe       = regexp.MustCompile(`^([ \t]+)(?:async\s+)?def\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(\s*self\b`)
	pyFossilNoteRe           = regexp.MustCompile(`(?i)\b(?:compatibility|backwards?[- ]compatible|legacy|deprecated|checkpoint)\b`)
	excludedClassMethodNames = map[string]bool{
		"default": true, "format": true, "read": true, "write": true, "close": true,
		"get": true, "set": true, "keys": true, "items": true, "values": true,
		"copy": true, "reset": true, "run": true, "parse": true, "execute": true,
		"main": true, "to_dict": true, "from_dict": true, "peek": true, "clear": true,
	}
	designSlopCorpusExts  = map[string]bool{ExtPy: true, ExtJS: true, ExtJSX: true, ExtTS: true, ExtTSX: true}
	designSlopSkipDirName = map[string]bool{
		".git": true, ".venv": true, "venv": true, "node_modules": true,
		"__pycache__": true, "vendor": true, ".tmp": true, ".nomos": true,
	}
)

type pyFuncBlock struct {
	name          string
	startLine     int
	bodyLines     []string
	decorated     bool
	hasFossilNote bool
}

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

func collectWorkspaceSlopFiles(root string, maxFiles int, dst map[string][]byte) bool {
	if root == "" || maxFiles <= 0 {
		return false
	}
	collected := 0
	exceeded := false
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		return visitWorkspaceSlopEntry(root, path, d, err, maxFiles, &collected, &exceeded, dst)
	})
	if walkErr != nil && walkErr != fs.SkipAll {
		return true
	}
	return exceeded
}

func visitWorkspaceSlopEntry(root, path string, d fs.DirEntry, err error, maxFiles int, collected *int, exceeded *bool, dst map[string][]byte) error {
	if err != nil || d == nil {
		return nil
	}
	if d.IsDir() {
		if path != root && designSlopSkipDirName[d.Name()] {
			return filepath.SkipDir
		}
		return nil
	}
	if !designSlopCorpusExts[strings.ToLower(filepath.Ext(d.Name()))] {
		return nil
	}
	if *collected >= maxFiles {
		*exceeded = true
		return fs.SkipAll
	}
	if key, raw, ok := readBoundedCorpusFile(root, path, d); ok {
		dst[key] = raw
		*collected++
	}
	return nil
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
	return append(out, scanPythonDeadClassMethods(relPath, text, corpusCounts)...)
}

func scanPythonDeadClassMethods(relPath, text string, corpusCounts map[string]int) []string {
	lines := strings.Split(text, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		msg, nextIdx := checkClassMethodBlockAt(relPath, lines, i, corpusCounts)
		if msg != "" {
			out = append(out, msg)
		}
		i = nextIdx
	}
	return out
}

func checkClassMethodBlockAt(relPath string, lines []string, i int, corpusCounts map[string]int) (string, int) {
	m := pyClassMethodDefRe.FindStringSubmatchIndex(lines[i])
	if len(m) != 6 {
		return "", i
	}
	indent := countLeadingIndentCols(lines[i][m[2]:m[3]])
	name := lines[i][m[4]:m[5]]
	end := findPythonBlockEnd(lines, i+1, indent)
	if isSkippableClassMethod(name) {
		return "", end - 1
	}
	decorated, leadFossil := inspectPrecedingDefMeta(lines, i)
	if decorated {
		return "", end - 1
	}
	body := lines[i+1 : end]
	execLines, bodyFossil := extractPythonExecBodyInfo(body)
	hasSingleReturn := len(execLines) == 1 && strings.HasPrefix(execLines[0], "return ")
	if !hasSingleReturn || (!strings.Contains(execLines[0], "(") && !leadFossil && !bodyFossil) {
		return "", end - 1
	}
	extRefs := corpusCounts[name] - (countWordInLines(name, body) + 1)
	if extRefs <= 0 {
		return fmt.Sprintf("%s:%d: dead pass-through method %q has 0 callers (checkpoint fossil); inline call at callsite (Law 6)", relPath, i+1, name), end - 1
	}
	return "", end - 1
}

func isSkippableClassMethod(name string) bool {
	return (strings.HasPrefix(name, "__") && strings.HasSuffix(name, "__")) ||
		strings.HasPrefix(name, "visit_") ||
		excludedClassMethodNames[name]
}

func isSkippablePyWrapper(fn pyFuncBlock) bool {
	return fn.decorated || fn.name == "main" || (strings.HasPrefix(fn.name, "__") && strings.HasSuffix(fn.name, "__"))
}

func classifyTrivialTopWrapper(fn pyFuncBlock, corpusCounts map[string]int) (bool, bool) {
	if isSkippablePyWrapper(fn) {
		return false, false
	}
	execLines, bodyFossil := extractPythonExecBodyInfo(fn.bodyLines)
	if len(execLines) != 1 || !strings.HasPrefix(execLines[0], "return ") {
		return false, false
	}
	extRefs := corpusCounts[fn.name] - (countWordInLines(fn.name, fn.bodyLines) + 1)
	isFossilTarget := strings.HasPrefix(fn.name, "_") || fn.hasFossilNote || bodyFossil
	if isFossilTarget && extRefs <= 0 && strings.Contains(execLines[0], "(") {
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
