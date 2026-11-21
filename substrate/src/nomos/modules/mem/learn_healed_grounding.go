// Package mem implements filesystem grounding for deleted Trap files and healed LOC/package-count ceilings.
package mem

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// trapFileAndCeilingPatterns compile Trap diagnostic matchers for source paths and LOC/file-count ceilings.
var (
	openCoreTrapSourceFileRe = regexp.MustCompile(`\b([A-Za-z0-9_-]+/[A-Za-z0-9_./-]+\.(?:go|ts|js|mjs|svelte|py|rs|md|json|yaml|yml))\b`)
	openCoreTrapMaxLocRe     = regexp.MustCompile(`\b([A-Za-z0-9_-]+/[A-Za-z0-9_./-]+)\s+has\s+\d+\s+LOC\s+\(exceeds\s+(?:brownfield\s+ceiling\s+\d+,\s+)?max\s+(\d+)\)`)
	openCoreTrapPkgCountRe   = regexp.MustCompile(`\b([A-Za-z0-9_-]+/[A-Za-z0-9_./-]+)\s+has\s+\d+\s+non-test\s+source\s+files\s+\(exceeds\s+(?:brownfield\s+ceiling\s+\d+,\s+)?max\s+(\d+)\)`)
)

// isAstCeilingGateSlug reports whether a gate slug enforces file-size or function-complexity ceilings rather than repo conventions.
func isAstCeilingGateSlug(slug string) bool {
	lower := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(slug)), "-", "_")
	for _, tok := range []string{"cognitive_complexity", "cognitive_debt", "max_file_loc", "function_length", "file_length"} {
		if strings.Contains(lower, tok) {
			return true
		}
	}
	return false
}

// isPitfallDeletedOrHealedOnDisk checks whether a KindPitfall cites absent Trap files or healed LOC/file-count ceilings.
func isPitfallDeletedOrHealedOnDisk(repoRoot string, m *Memory) bool {
	if m == nil || m.Kind != KindPitfall || strings.TrimSpace(repoRoot) == "" {
		return false
	}
	parts := strings.SplitN(m.Content, "-> Verified Fix", 2)
	trap := strings.TrimSpace(parts[0])
	if trap == "" {
		return false
	}
	if checked, stale := checkOpenCoreHealedLocTrap(repoRoot, trap); checked {
		return stale
	}
	if checked, stale := checkOpenCoreHealedPkgCountTrap(repoRoot, trap); checked {
		return stale
	}
	return allOpenCoreCitedTrapFilesAbsent(repoRoot, trap)
}

// checkOpenCoreHealedLocTrap returns (true, true) when a Max File LOC target is absent or shrunk to <=90% of max LOC.
func checkOpenCoreHealedLocTrap(repoRoot, trap string) (bool, bool) {
	m := openCoreTrapMaxLocRe.FindStringSubmatch(trap)
	if len(m) != 3 {
		return false, false
	}
	maxLOC, err := strconv.Atoi(m[2])
	if err != nil || maxLOC <= 0 {
		return false, false
	}
	raw, rErr := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(m[1])))
	if rErr != nil {
		return true, os.IsNotExist(rErr)
	}
	lines := bytes.Count(raw, []byte{'\n'})
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		lines++
	}
	return true, lines <= (maxLOC*9)/10
}

// checkOpenCoreHealedPkgCountTrap returns (true, true) when a package file-count target sits strictly below max files.
func checkOpenCoreHealedPkgCountTrap(repoRoot, trap string) (bool, bool) {
	m := openCoreTrapPkgCountRe.FindStringSubmatch(trap)
	if len(m) != 3 {
		return false, false
	}
	maxFiles, err := strconv.Atoi(m[2])
	if err != nil || maxFiles <= 0 {
		return false, false
	}
	entries, dErr := os.ReadDir(filepath.Join(repoRoot, filepath.FromSlash(m[1])))
	if dErr != nil {
		return true, os.IsNotExist(dErr)
	}
	nonTest := 0
	for _, e := range entries {
		if !e.IsDir() && !isOpenCoreTestFilename(e.Name()) {
			nonTest++
		}
	}
	return true, nonTest < maxFiles
}

// isOpenCoreTestFilename identifies unit-test filenames excluded from non-test package file counts.
func isOpenCoreTestFilename(name string) bool {
	lower := strings.ToLower(name)
	for _, suffix := range []string{"_test.go", ".test.ts", ".spec.ts", ".test.js", ".spec.js"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return strings.HasPrefix(lower, "test_") && strings.HasSuffix(lower, ".py")
}

// allOpenCoreCitedTrapFilesAbsent returns true when >=1 directory-anchored Trap file is cited under an existing top-level dir and none exist.
func allOpenCoreCitedTrapFilesAbsent(repoRoot, trap string) bool {
	matches := openCoreTrapSourceFileRe.FindAllStringSubmatch(trap, 8)
	checked := 0
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		rel := m[1]
		topSeg := strings.SplitN(rel, "/", 2)[0]
		if info, err := os.Stat(filepath.Join(repoRoot, topSeg)); err != nil || !info.IsDir() {
			continue
		}
		checked++
		if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(rel))); err == nil {
			return false
		}
	}
	return checked > 0
}
