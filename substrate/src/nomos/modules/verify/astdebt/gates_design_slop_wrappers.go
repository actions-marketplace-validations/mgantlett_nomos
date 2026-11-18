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
	minTrivialSinglesPerFile = 2
	maxMultiOneLinersPy      = 2
)

var (
	pyFossilNoteRe           = regexp.MustCompile(`(?i)\b(?:compatibility|backwards?[- ]compatible|legacy|deprecated|checkpoint)\b`)
	excludedClassMethodNames = map[string]bool{
		"default": true, "format": true, "read": true, "write": true, "close": true,
		"keys": true, "items": true, "values": true, "copy": true,
		"main": true, "to_dict": true, "from_dict": true,
	}
	designSlopCorpusExts = map[string]bool{
		ExtPy: true, ExtRB: true, ExtRake: true,
		ExtTS: true, ExtTSX: true, ExtJS: true, ExtJSX: true, ExtMJS: true, ExtCJS: true,
		ExtRS: true, ExtJava: true, ExtKT: true, ExtKTS: true, ExtCS: true, ExtGo: true,
	}
	designSlopSkipDirName = map[string]bool{
		".git": true, ".venv": true, "venv": true, "node_modules": true,
		"__pycache__": true, "vendor": true, ".tmp": true, ".nomos": true,
	}
)

type pyFuncBlock struct {
	name          string
	indent        int
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
	wrapperDeadTopFossil
	wrapperDeadMethod
	wrapperSingleMiddleman
	wrapperTrivialSingle
	wrapperMultiOneLiner
)

type singleUseHit struct {
	name string
	line int
}

func scanPythonFileWrappers(relPath, text string, corpusCounts map[string]int) []string {
	var out []string
	var singles []singleUseHit
	var multiNames []string
	for _, fn := range parseAllPyFuncs(text) {
		cls, note := classifyPyOneLiner(fn, corpusCounts)
		if msg := formatImmediateWrapperHit(relPath, fn, cls, note); msg != "" {
			out = append(out, msg)
		} else if cls == wrapperTrivialSingle {
			singles = append(singles, singleUseHit{name: fn.name, line: fn.startLine})
		} else if cls == wrapperMultiOneLiner {
			multiNames = append(multiNames, fn.name)
		}
	}
	out = append(out, formatPyWrapperGroupHits(relPath, singles, multiNames)...)
	return out
}

func formatImmediateWrapperHit(relPath string, fn pyFuncBlock, cls wrapperClass, _ string) string {
	switch cls {
	case wrapperDeadTopFossil:
		return fmt.Sprintf("%s:%d: dead pass-through wrapper %q has 0 callers (checkpoint fossil); delete unused shim and call target directly (Law 6)", relPath, fn.startLine, fn.name)
	case wrapperDeadMethod:
		return fmt.Sprintf("%s:%d: dead pass-through method %q has 0 callers (checkpoint fossil); inline expression at callsite (Law 6)", relPath, fn.startLine, fn.name)
	case wrapperSingleMiddleman:
		return fmt.Sprintf("%s:%d: single-caller pass-through middleman %q forwards to another call with 1 caller; inline target call directly (Law 6)", relPath, fn.startLine, fn.name)
	default:
		return ""
	}
}

func formatPyWrapperGroupHits(relPath string, singles []singleUseHit, multiNames []string) []string {
	var out []string
	if len(singles) == 1 {
		out = append(out, fmt.Sprintf("%s:%d: single-caller one-line return helper %q has only 1 caller; inline expression directly at callsite (Law 6)", relPath, singles[0].line, singles[0].name))
	} else if len(singles) >= minTrivialSinglesPerFile {
		names := make([]string, len(singles))
		for i, s := range singles {
			names[i] = s.name
		}
		out = append(out, fmt.Sprintf("%s: excessive trivial wrapper fragmentation (%d single-use 1-line return helpers: %s); inline 1-line single-caller wrappers instead of fragmenting module (Law 6)", relPath, len(singles), strings.Join(names, ", ")))
	}
	if len(multiNames) > maxMultiOneLinersPy {
		out = append(out, fmt.Sprintf("%s: module defines %d multi-caller one-line return micro-helpers (%s; max %d); inline trivial predicates/wrappers or consolidate into a dispatch table (Law 6)", relPath, len(multiNames), strings.Join(multiNames, ", "), maxMultiOneLinersPy))
	}
	return out
}

func isSkippablePyFunc(fn pyFuncBlock) bool {
	if fn.decorated || fn.name == "main" || strings.HasPrefix(fn.name, "visit_") {
		return true
	}
	if strings.HasPrefix(fn.name, "__") && strings.HasSuffix(fn.name, "__") {
		return true
	}
	return fn.indent > 0 && excludedClassMethodNames[fn.name]
}

func classifyPyOneLiner(fn pyFuncBlock, corpusCounts map[string]int) (wrapperClass, string) {
	if isSkippablePyFunc(fn) {
		return wrapperNone, ""
	}
	execLines, bodyFossil := extractPythonExecBodyInfo(fn.bodyLines)
	if len(execLines) != 1 || !strings.HasPrefix(execLines[0], "return ") {
		return wrapperNone, ""
	}
	retLine := execLines[0]
	extRefs := corpusCounts[fn.name] - (countWordInLines(fn.name, fn.bodyLines) + 1)
	if extRefs <= 0 {
		return classifyDeadPyWrapper(fn, retLine, bodyFossil), ""
	}
	if extRefs == 1 {
		if isPureCallForwardingReturn(retLine) {
			return wrapperSingleMiddleman, ""
		}
		return wrapperTrivialSingle, ""
	}
	return wrapperMultiOneLiner, ""
}

func classifyDeadPyWrapper(fn pyFuncBlock, retLine string, bodyFossil bool) wrapperClass {
	hasFossil := fn.hasFossilNote || bodyFossil
	hasCallLike := strings.Contains(retLine, "(") || strings.Contains(retLine, "[")
	if fn.indent > 0 {
		if hasCallLike || hasFossil {
			return wrapperDeadMethod
		}
		return wrapperNone
	}
	isFossilTarget := strings.HasPrefix(fn.name, "_") || hasFossil
	if isFossilTarget && (hasCallLike || hasFossil) {
		return wrapperDeadTopFossil
	}
	return wrapperNone
}
