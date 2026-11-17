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
	maxDesignSlopCorpusFiles  = 512
	maxDesignSlopFileBytes    = 512 * 1024
	minTrivialSinglesPerFile  = 2
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

type wrapperClass int

const (
	wrapperNone wrapperClass = iota
	wrapperDeadFossil
	wrapperSingleMiddleman
	wrapperTrivialSingle
)

func scanPythonFileWrappers(relPath, text string, corpusCounts map[string]int) []string {
	var out []string
	var trivialSingles []string
	for _, fn := range parseTopLevelPyFuncs(text) {
		switch classifyTopWrapper(fn, corpusCounts) {
		case wrapperDeadFossil:
			out = append(out, fmt.Sprintf("%s:%d: dead pass-through wrapper %q has 0 callers (checkpoint fossil); delete unused shim and call target directly (Law 6)", relPath, fn.startLine, fn.name))
		case wrapperSingleMiddleman:
			out = append(out, fmt.Sprintf("%s:%d: single-caller pass-through middleman %q forwards to another call with 1 caller; inline target call directly (Law 6)", relPath, fn.startLine, fn.name))
		case wrapperTrivialSingle:
			trivialSingles = append(trivialSingles, fn.name)
		}
	}
	if len(trivialSingles) >= minTrivialSinglesPerFile {
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
	if extRefs == 1 && isPureCallForwardingReturn(execLines[0]) {
		return fmt.Sprintf("%s:%d: single-caller pass-through method %q forwards to another call with 1 caller; inline target call at callsite (Law 6)", relPath, i+1, name), end - 1
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

func classifyTopWrapper(fn pyFuncBlock, corpusCounts map[string]int) wrapperClass {
	if isSkippablePyWrapper(fn) {
		return wrapperNone
	}
	execLines, bodyFossil := extractPythonExecBodyInfo(fn.bodyLines)
	if len(execLines) != 1 || !strings.HasPrefix(execLines[0], "return ") {
		return wrapperNone
	}
	extRefs := corpusCounts[fn.name] - (countWordInLines(fn.name, fn.bodyLines) + 1)
	isFossilTarget := strings.HasPrefix(fn.name, "_") || fn.hasFossilNote || bodyFossil
	if isFossilTarget && extRefs <= 0 && strings.Contains(execLines[0], "(") {
		return wrapperDeadFossil
	}
	if extRefs == 1 && isPureCallForwardingReturn(execLines[0]) {
		return wrapperSingleMiddleman
	}
	if extRefs <= 1 {
		return wrapperTrivialSingle
	}
	return wrapperNone
}
