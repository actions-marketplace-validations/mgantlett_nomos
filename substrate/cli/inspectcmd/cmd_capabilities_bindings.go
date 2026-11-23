// Package inspectcmd provides Markdown section extraction and reverse workspace binding scanners for ContractSlice.
// @intent software_physics_engine_spec.md #ContractSlice
package inspectcmd

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// findNearestHeading returns the closest Markdown heading preceding needle in markdown.
func findNearestHeading(markdown, needle string) string {
	idx := strings.Index(markdown, needle)
	if idx < 0 {
		return extractTopDocumentHeading(markdown)
	}
	lines := strings.Split(markdown[:idx], "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "#") {
			return strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
		}
	}
	return ""
}

// extractTopDocumentHeading returns the first top-level Markdown heading in markdown.
func extractTopDocumentHeading(markdown string) string {
	for _, line := range strings.Split(markdown, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
		}
	}
	return ""
}

// extractFirstCodeFence returns the first fenced code block in markdown when present.
func extractFirstCodeFence(markdown string) string {
	if m := codeFenceSpanRe.FindStringSubmatch(markdown); len(m) >= 3 {
		return strings.TrimSpace(m[2])
	}
	return ""
}

// extractMatchingTableRows collects up to maxObligationsSlice Markdown table rows containing token.
func extractMatchingTableRows(markdown, token string) []string {
	var rows []string
	for _, line := range strings.Split(markdown, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") || strings.Contains(trimmed, ":---") {
			continue
		}
		if strings.Contains(trimmed, token) && tcRowExtractRe.MatchString(trimmed) {
			rows = append(rows, trimmed)
			if len(rows) >= maxObligationsSlice {
				break
			}
		}
	}
	return rows
}

// extractTCIDsFromRows extracts unique TC-* identifiers referenced across table rows.
func extractTCIDsFromRows(rows []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, r := range rows {
		for _, m := range tcRowExtractRe.FindAllStringSubmatch(r, -1) {
			if len(m) >= 2 && !seen[m[1]] {
				seen[m[1]] = true
				out = append(out, m[1])
			}
		}
	}
	sort.Strings(out)
	return out
}

// extractLineContextBlock extracts up to 5 surrounding lines around needle in markdown.
func extractLineContextBlock(markdown, needle string) string {
	lines := strings.Split(markdown, "\n")
	for i, line := range lines {
		if !strings.Contains(line, needle) {
			continue
		}
		start := i - 1
		if start < 0 {
			start = 0
		}
		end := i + 4
		if end > len(lines) {
			end = len(lines)
		}
		return strings.TrimSpace(strings.Join(lines[start:end], "\n"))
	}
	return ""
}

// clampBlock bounds contract excerpts so tool responses remain compact.
func clampBlock(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) <= maxContractBlockLen {
		return trimmed
	}
	return strings.TrimSpace(trimmed[:maxContractBlockLen]) + "\n..."
}

// scanReverseWorkspaceBindings scans source trees for @intent / symbol implementations and @verifies tests.
func scanReverseWorkspaceBindings(repoRoot, specBase, symbol string, tcIDs []string) ([]string, []string) {
	tcNeedles := buildTCNormalizedMatchers(tcIDs)
	var sources, tests []string
	walkErr := filepath.WalkDir(repoRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil
		}
		if d.IsDir() {
			return skipReverseScanDir(path, repoRoot, d.Name())
		}
		relSlash := toRepoRelSlash(repoRoot, path)
		if isScannableCodeAsset(relSlash) {
			classifyReverseBindingFile(path, relSlash, specBase, symbol, tcNeedles, &sources, &tests)
		}
		return nil
	})
	if walkErr != nil {
		sort.Strings(sources)
		sort.Strings(tests)
		return sources, tests
	}
	sort.Strings(sources)
	sort.Strings(tests)
	return sources, tests
}

// skipReverseScanDir prunes non-source, hidden, vendor, and nested worktree directories.
func skipReverseScanDir(path, repoRoot, name string) error {
	if path == repoRoot {
		return nil
	}
	if strings.HasPrefix(name, ".") || name == "worktrees" || name == "vendor" || name == "node_modules" || name == "intent" {
		return filepath.SkipDir
	}
	return nil
}

// buildTCNormalizedMatchers builds hyphenated (TC-A-01) and test-func underscore (TC_A_01) needles.
func buildTCNormalizedMatchers(tcIDs []string) []string {
	var out []string
	for _, tc := range tcIDs {
		out = append(out, tc, strings.ReplaceAll(tc, "-", "_"))
	}
	return out
}

// isScannableCodeAsset reports whether relSlash is a Go or polyglot source/test file.
func isScannableCodeAsset(relSlash string) bool {
	for _, ext := range []string{".go", ".ts", ".tsx", ".js", ".py", ".rs"} {
		if strings.HasSuffix(relSlash, ext) {
			return true
		}
	}
	return false
}

// isTestAssetPath reports whether relSlash follows unit test naming conventions.
func isTestAssetPath(relSlash string) bool {
	base := filepath.Base(relSlash)
	return strings.HasSuffix(base, "_test.go") ||
		strings.HasSuffix(base, ".test.ts") ||
		strings.HasSuffix(base, ".spec.ts") ||
		strings.HasPrefix(base, "test_") ||
		strings.HasSuffix(base, "_test.py")
}

// classifyReverseBindingFile checks one source or test file against symbol, specBase, and TC matchers.
func classifyReverseBindingFile(absPath, relSlash, specBase, symbol string, tcNeedles []string, sources, tests *[]string) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return
	}
	text := string(data)
	if isTestAssetPath(relSlash) {
		if matchesTestFileNeedles(text, symbol, tcNeedles) {
			*tests = append(*tests, relSlash)
		}
		return
	}
	if matchesSourceFileNeedles(text, specBase, symbol) {
		*sources = append(*sources, relSlash)
	}
}

// matchesTestFileNeedles reports whether test file content verifies any target TC-* or symbol.
func matchesTestFileNeedles(text, symbol string, tcNeedles []string) bool {
	for _, needle := range tcNeedles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return len(tcNeedles) == 0 && symbol != "" && strings.Contains(text, symbol)
}

// matchesSourceFileNeedles reports whether non-test source content binds to symbol or @intent specBase.
func matchesSourceFileNeedles(text, specBase, symbol string) bool {
	if symbol != "" {
		return strings.Contains(text, "#"+symbol) ||
			strings.Contains(text, "type "+symbol+" ") ||
			strings.Contains(text, symbol+" =") ||
			(strings.HasPrefix(symbol, "ERR_") && strings.Contains(text, symbol))
	}
	return specBase != "" && strings.Contains(text, "@intent") && strings.Contains(text, specBase)
}

// toRepoRelSlash converts absPath to a forward-slash path relative to repoRoot.
func toRepoRelSlash(repoRoot, absPath string) string {
	if rel, err := filepath.Rel(repoRoot, absPath); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(absPath)
}
