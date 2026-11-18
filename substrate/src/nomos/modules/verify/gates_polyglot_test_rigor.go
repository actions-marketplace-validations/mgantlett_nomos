// Package verify enforces zero-baseline .tmp/tests/ steering and polyglot test-breadth rigor (Law 2).
package verify

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/astdebt"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/impact"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

const (
	minProdLOCForBreadthGate = 100
	minPolyglotTestCases     = 8
	minPolyglotAssertions    = 8
)

var (
	rigorTestCaseRe = regexp.MustCompile(`(?:\bdef\s+test_[A-Za-z0-9_]*|\.subTest\s*\(|\b(?:it|test)\s*\(|^\s*it\s+['"]|#\[test\]|\bfunc\s+Test[A-Za-z0-9_]*|t\.Run\s*\()`)
	rigorAssertRe   = regexp.MustCompile(`(?:\bself\.assert[A-Za-z_]*\s*\(|\bassert\s+|\bpytest\.raises\s*\(|\bassertRaises\s*\(|\bexpect\s*\(|\bassert[._][A-Za-z_]*|\bassert(?:_eq|_ne)?!\s*\(|\btoThrow\b|\brejects\.|\bassert_raises\b)`)
	rigorSkipDirs   = map[string]bool{
		".git": true, ".venv": true, "venv": true, "node_modules": true, "__pycache__": true,
		"vendor": true, ".tmp": true, ".nomos": true, "worktrees": true, "tests": true, "test": true,
		"__tests__": true, "spec": true, "testdata": true, "fixtures": true, "examples": true,
		"samples": true, "files": true, "assets": true,
	}
	rigorImplExts = map[string]bool{
		astdebt.ExtPy: true, astdebt.ExtRB: true, astdebt.ExtRake: true, astdebt.ExtRS: true,
		astdebt.ExtTS: true, astdebt.ExtTSX: true, astdebt.ExtJS: true, astdebt.ExtJSX: true,
		astdebt.ExtMJS: true, astdebt.ExtCJS: true,
	}
)

func hasZeroBaselineTrackedTests(root string) bool {
	if strings.TrimSpace(root) == "" || !vscope.HasGitMetadata(root) {
		return true
	}
	out, err := workspace.RunSafeGit(root, "ls-files")
	if err != nil {
		return true
	}
	for _, line := range strings.Split(string(out), "\n") {
		rel := strings.TrimSpace(line)
		if rel != "" && isPolyglotTestFilePath(rel) {
			return false
		}
	}
	return true
}

func resolveTDDRemedy(root string) string {
	if hasZeroBaselineTrackedTests(root) {
		return "keep deliverables clean by authoring tests under .tmp/tests/ (.tmp/tests/test_<module>.py, .tmp/tests/<module>.test.ts, .tmp/tests/<module>_test.rb, *_test.go, #[cfg(test)])"
	}
	return "author table-driven unit tests (*_test.go, stdlib unittest.TestCase in .tmp/tests/test_*.py or tests/ [or pytest when installed], *.test.ts, *_test.rb, #[cfg(test)], *Test.java, *Tests.cs)"
}

func resolvePolyglotCoverageRemedy(root string, threshold float64) string {
	if hasZeroBaselineTrackedTests(root) {
		return fmt.Sprintf("author additional unit tests in .tmp/tests/ to raise statement coverage to >= %.1f%%", threshold)
	}
	return fmt.Sprintf("author additional unit tests to raise statement coverage to >= %.1f%%", threshold)
}

func evaluatePolyglotTestRigor(ctx *vscope.VerifyContext, root, source string) *vscope.GateResult {
	if source == "" || source == "Python(diff-scoped skip)" || source == "LCOV" || source == "SimpleCov" {
		return nil
	}
	isZeroBase := hasZeroBaselineTrackedTests(root)
	if !isZeroBase && (ctx == nil || !ctx.Strict) {
		return nil
	}
	implFiles, prodLOC := collectRigorImplFilesAndLOC(ctx, root)
	if prodLOC < minProdLOCForBreadthGate {
		return nil
	}
	testFiles := collectWorkspaceAndScratchTests(root)
	cases, asserts := countPolyglotTestsAndAssertions(testFiles)
	if cases < minPolyglotTestCases || asserts < minPolyglotAssertions {
		return &vscope.GateResult{
			GateName: "Statement Coverage Invariant (Law 2)", ProbeID: probeIDCoverage, Law: 2, Ring: RingDynamic,
			Passed:  false,
			Message: fmt.Sprintf("shallow polyglot test breadth for %d production LOC (%d test case(s), %d assertion(s)); minimum %d test cases and %d assertions required", prodLOC, cases, asserts, minPolyglotTestCases, minPolyglotAssertions),
			Remedy:  "add >= 8 distinct test cases in .tmp/tests/ covering exact boundary inclusivity (<= vs <), multi-item/multi-trigger sequences, wildcard globs, and invalid inputs",
		}
	}
	_ = implFiles
	return nil
}

func collectRigorImplFilesAndLOC(ctx *vscope.VerifyContext, root string) ([]string, int) {
	if scoped := collectModifiedRigorImplFiles(ctx, root); len(scoped) > 0 {
		return scoped, sumNonEmptyCodeLOC(scoped)
	}
	var files []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		return visitRigorImplEntry(root, path, d, err, &files)
	})
	sort.Strings(files)
	return files, sumNonEmptyCodeLOC(files)
}

func collectModifiedRigorImplFiles(ctx *vscope.VerifyContext, root string) []string {
	if !vscope.HasGitMetadata(root) {
		return nil
	}
	var out []string
	for _, rel := range vscope.GetModifiedFilesInScope(ctx, root) {
		slash := filepath.ToSlash(filepath.Clean(rel))
		if hasSkippedRigorSegment(slash) || isPolyglotTestFilePath(slash) {
			continue
		}
		if !rigorImplExts[strings.ToLower(filepath.Ext(slash))] {
			continue
		}
		abs := filepath.Join(root, filepath.FromSlash(slash))
		if info, err := os.Stat(abs); err == nil && !info.IsDir() {
			out = append(out, abs)
		}
	}
	sort.Strings(out)
	return out
}

func visitRigorImplEntry(root, path string, d fs.DirEntry, err error, files *[]string) error {
	if err != nil || d == nil {
		return nil
	}
	if d.IsDir() {
		if path != root && (rigorSkipDirs[strings.ToLower(d.Name())] || strings.HasPrefix(d.Name(), ".")) {
			return filepath.SkipDir
		}
		return nil
	}
	if len(*files) >= 64 || !rigorImplExts[strings.ToLower(filepath.Ext(d.Name()))] {
		return nil
	}
	if !isPolyglotTestFilePath(d.Name()) {
		*files = append(*files, path)
	}
	return nil
}

func hasSkippedRigorSegment(slash string) bool {
	for _, part := range strings.Split(slash, "/") {
		if part == "" || part == "." {
			continue
		}
		if strings.HasPrefix(part, ".") || rigorSkipDirs[strings.ToLower(part)] {
			return true
		}
	}
	return false
}

func sumNonEmptyCodeLOC(absFiles []string) int {
	total := 0
	for _, abs := range absFiles {
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		for _, raw := range strings.Split(string(data), "\n") {
			t := strings.TrimSpace(raw)
			if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "//") {
				continue
			}
			total++
		}
	}
	return total
}

func collectWorkspaceAndScratchTests(root string) []string {
	var tests []string
	for _, sub := range []string{filepath.Join(".tmp", "tests"), filepath.Join(".tmp", "test"), "tests", "test"} {
		walkTestFilesUnder(filepath.Join(root, sub), &tests)
	}
	if entries, err := os.ReadDir(root); err == nil {
		for _, e := range entries {
			if !e.IsDir() && isPolyglotTestFilePath(e.Name()) && len(tests) < 64 {
				tests = append(tests, filepath.Join(root, e.Name()))
			}
		}
	}
	return tests
}

func walkTestFilesUnder(dir string, dst *[]string) {
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil
		}
		if d.IsDir() {
			if path != dir && impact.IsSkippableSecurityDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if len(*dst) < 64 && isPolyglotTestFilePath(d.Name()) {
			*dst = append(*dst, path)
		}
		return nil
	})
}

func countPolyglotTestsAndAssertions(absFiles []string) (int, int) {
	cases, asserts := 0, 0
	for _, abs := range absFiles {
		raw, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			cases += len(rigorTestCaseRe.FindAllStringIndex(line, -1))
			asserts += len(rigorAssertRe.FindAllStringIndex(line, -1))
		}
	}
	return cases, asserts
}
