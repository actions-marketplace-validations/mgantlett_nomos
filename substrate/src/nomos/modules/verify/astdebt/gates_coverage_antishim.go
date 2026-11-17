// Package astdebt detects coverage-directive reward hacking and available-library fallback shims (Law 2 & Law 6).
package astdebt

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	hashCovExclusionRe   = regexp.MustCompile(`(?i)\b(?:pragma:\s*no\s*cover|coverage:\s*ignore)\b`)
	braceCovExclusionRe  = regexp.MustCompile(`(?i)\b(?:(?:istanbul|c8|v8)\s+ignore\b|pragma:\s*no\s*cover\b)`)
	rustCovExclusionRe   = regexp.MustCompile(`(?i)(?:coverage\s*\(\s*off\s*\)|not\s*\(\s*tarpaulin_include\s*\))`)
	pyImportStmtRe       = regexp.MustCompile(`^\s*import\s+(.+)$`)
	pyFromImportStmtRe   = regexp.MustCompile(`^\s*from\s+([A-Za-z_][A-Za-z0-9_]*)(?:\.[A-Za-z0-9_]+)*\s+import\s+`)
	pyImportExceptRe     = regexp.MustCompile(`^(\s*)except\b([^:]*):\s*(.*)$`)
	pyReqPkgNameRe       = regexp.MustCompile(`^\s*([A-Za-z0-9][A-Za-z0-9._-]*)`)
	pyprojectDepArrayRe  = regexp.MustCompile(`(?s)dependencies\s*=\s*\[(.*?)\]`)
	quotedPackageTokenRe = regexp.MustCompile(`["']([A-Za-z0-9][A-Za-z0-9._-]*)`)

	pythonPackageAliases = map[string]string{
		"pyyaml":          "yaml",
		"beautifulsoup4":  "bs4",
		"pillow":          "pil",
		"scikit_learn":    "sklearn",
		"python_dateutil": "dateutil",
		"pyjwt":           "jwt",
		"attrs":           "attr",
	}

	pythonStdlibModules = map[string]bool{
		"json": true, "csv": true, "re": true, "sqlite3": true, "argparse": true,
		"pathlib": true, "collections": true, "itertools": true, "functools": true,
		"datetime": true, "urllib": true, "http": true, "hashlib": true, "hmac": true,
		"zlib": true, "gzip": true, "tarfile": true, "zipfile": true, "xml": true,
		"html": true, "configparser": true, "shutil": true, "tempfile": true,
		"logging": true, "typing": true, "dataclasses": true, "enum": true,
		"decimal": true, "fractions": true, "statistics": true, "heapq": true,
		"bisect": true,
	}
)

// CheckCoverageExclusionDirectives scans comments and attributes only for coverage suppression directives (@intent tree_sitter_ast_gate_spec.md).
func CheckCoverageExclusionDirectives(relPath string, content []byte) []string {
	if len(content) == 0 {
		return nil
	}
	ext := strings.ToLower(filepath.Ext(relPath))
	switch ext {
	case ExtGo:
		return scanGoCoverageExclusions(relPath, content)
	case ExtPy, ExtRB, ExtRake:
		return scanHashCommentDirectives(relPath, string(content), hashCovExclusionRe)
	case ExtRS:
		return scanRustCoverageExclusions(relPath, string(content))
	case ExtJS, ExtJSX, ExtTS, ExtTSX, ExtMJS, ExtCJS, ExtJava, ExtKT, ExtKTS, ExtCS, ExtCPP, ExtCC, ExtCXX, ExtC, ExtH, ExtHPP:
		return scanSlashCommentDirectives(relPath, string(content), braceCovExclusionRe)
	default:
		return nil
	}
}

func scanGoCoverageExclusions(relPath string, content []byte) []string {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, relPath, content, parser.ParseComments)
	if err != nil || node == nil {
		return nil
	}
	var out []string
	for _, cg := range node.Comments {
		for _, c := range cg.List {
			if tok := hashCovExclusionRe.FindString(c.Text); tok != "" {
				line := fset.Position(c.Pos()).Line
				out = append(out, formatCoverageHackMsg(relPath, line, tok))
			}
		}
	}
	return out
}

func scanHashCommentDirectives(relPath, text string, pat *regexp.Regexp) []string {
	var out []string
	for idx, comment := range extractPythonHashComments(text) {
		if comment == "" {
			continue
		}
		if tok := pat.FindString(comment); tok != "" {
			out = append(out, formatCoverageHackMsg(relPath, idx+1, tok))
		}
	}
	return out
}

func scanRustCoverageExclusions(relPath, text string) []string {
	var out []string
	for idx, rawLine := range strings.Split(text, "\n") {
		codePart, comment := splitSlashLineCommentOutsideStrings(rawLine)
		if tok := rustCovExclusionRe.FindString(comment); tok != "" {
			out = append(out, formatCoverageHackMsg(relPath, idx+1, tok))
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(codePart), "#[") {
			if tok := rustCovExclusionRe.FindString(codePart); tok != "" {
				out = append(out, formatCoverageHackMsg(relPath, idx+1, tok))
			}
		}
	}
	return out
}

func scanSlashCommentDirectives(relPath, text string, pat *regexp.Regexp) []string {
	var out []string
	inBlockComment := false
	for idx, rawLine := range strings.Split(text, "\n") {
		commentText, nextBlock := extractSlashCommentsFromLine(rawLine, inBlockComment)
		inBlockComment = nextBlock
		if tok := pat.FindString(commentText); tok != "" {
			out = append(out, formatCoverageHackMsg(relPath, idx+1, tok))
		}
	}
	return out
}

func formatCoverageHackMsg(relPath string, line int, token string) string {
	return fmt.Sprintf("%s:%d: coverage_exclusion_reward_hack (%s); cover branch with tests instead of excluding from coverage (Law 2)", relPath, line, strings.TrimSpace(token))
}

// DiscoverPythonManifestPackages reads declared third-party packages from pyproject.toml and requirements.txt alongside Python stdlib modules.
func DiscoverPythonManifestPackages(root string) map[string]bool {
	pkgs := make(map[string]bool, len(pythonStdlibModules)+16)
	for mod := range pythonStdlibModules {
		pkgs[mod] = true
	}
	if root == "" {
		return pkgs
	}
	if raw, err := os.ReadFile(filepath.Join(root, "requirements.txt")); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			if idx := strings.IndexByte(line, '#'); idx >= 0 {
				line = line[:idx]
			}
			if m := pyReqPkgNameRe.FindStringSubmatch(line); len(m) == 2 {
				addNormalizedPythonPkg(pkgs, m[1])
			}
		}
	}
	if raw, err := os.ReadFile(filepath.Join(root, "pyproject.toml")); err == nil {
		for _, block := range pyprojectDepArrayRe.FindAllStringSubmatch(string(raw), -1) {
			for _, m := range quotedPackageTokenRe.FindAllStringSubmatch(block[1], -1) {
				addNormalizedPythonPkg(pkgs, m[1])
			}
		}
	}
	return pkgs
}

func addNormalizedPythonPkg(dst map[string]bool, rawName string) {
	norm := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(rawName), "-", "_"))
	if norm == "" {
		return
	}
	dst[norm] = true
	if alias, ok := pythonPackageAliases[norm]; ok {
		dst[alias] = true
	}
}

// CheckManifestImportFallbacks flags hand-rolled fallbacks under except ImportError for stdlib or declared manifest dependencies.
func CheckManifestImportFallbacks(root, relPath string, content []byte) []string {
	if root == "" || len(content) == 0 || !strings.EqualFold(filepath.Ext(relPath), ExtPy) || isPythonTestRelPath(relPath) {
		return nil
	}
	available := DiscoverPythonManifestPackages(root)
	lines := strings.Split(string(content), "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		if msg := inspectImportFallbackTryAt(lines, i, relPath, available); msg != "" {
			out = append(out, msg)
		}
	}
	return out
}

func inspectImportFallbackTryAt(lines []string, idx int, relPath string, available map[string]bool) string {
	if strings.TrimSpace(lines[idx]) != "try:" {
		return ""
	}
	tryIndent := countLeadingIndentCols(lines[idx])
	imported, nextIdx := collectTryImportModules(lines, idx+1, tryIndent)
	if len(imported) == 0 || nextIdx >= len(lines) {
		return ""
	}
	em := pyImportExceptRe.FindStringSubmatch(lines[nextIdx])
	if len(em) == 0 || countLeadingIndentCols(em[1]) != tryIndent {
		return ""
	}
	clause := strings.TrimSpace(em[2])
	if clause != "" && !strings.Contains(clause, "ImportError") && !strings.Contains(clause, "ModuleNotFoundError") {
		return ""
	}
	hasDefOrClass, execCount := inspectPythonBlockStats(lines, nextIdx+1, tryIndent, strings.TrimSpace(em[3]))
	if !hasDefOrClass && execCount <= 2 {
		return ""
	}
	for _, mod := range imported {
		if available[mod] {
			return fmt.Sprintf("%s:%d: hand-rolled fallback inside except ImportError for available module %q; import and use the installed library directly (Law 6)", relPath, nextIdx+1, mod)
		}
	}
	return ""
}

func collectTryImportModules(lines []string, startIdx, tryIndent int) ([]string, int) {
	var mods []string
	idx := startIdx
	for ; idx < len(lines); idx++ {
		trimmed := strings.TrimSpace(lines[idx])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if countLeadingIndentCols(lines[idx]) <= tryIndent {
			break
		}
		mods = appendImportedLineModules(mods, trimmed)
	}
	return mods, idx
}

func appendImportedLineModules(mods []string, trimmed string) []string {
	if m := pyFromImportStmtRe.FindStringSubmatch(trimmed); len(m) == 2 {
		return append(mods, m[1])
	}
	m := pyImportStmtRe.FindStringSubmatch(trimmed)
	if len(m) != 2 {
		return mods
	}
	for _, item := range strings.Split(m[1], ",") {
		fields := strings.Fields(strings.TrimSpace(item))
		if len(fields) > 0 {
			mods = append(mods, strings.Split(fields[0], ".")[0])
		}
	}
	return mods
}

func inspectPythonBlockStats(lines []string, startIdx, headerIndent int, tail string) (bool, int) {
	if tail != "" {
		return strings.HasPrefix(tail, "def ") || strings.HasPrefix(tail, "class "), 1
	}
	hasDef := false
	execCount := 0
	for j := startIdx; j < len(lines); j++ {
		trimmed := strings.TrimSpace(lines[j])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if countLeadingIndentCols(lines[j]) <= headerIndent {
			break
		}
		if strings.HasPrefix(trimmed, "def ") || strings.HasPrefix(trimmed, "async def ") || strings.HasPrefix(trimmed, "class ") {
			hasDef = true
		}
		execCount++
	}
	return hasDef, execCount
}

func isPythonTestRelPath(relPath string) bool {
	slash := filepath.ToSlash(relPath)
	base := filepath.Base(slash)
	if strings.HasPrefix(base, PrefixPyTest) || strings.HasSuffix(base, SuffixPyTest) {
		return true
	}
	for _, dirTok := range []string{DirTests, DirTest} {
		if strings.HasPrefix(slash, dirTok+"/") || strings.Contains(slash, "/"+dirTok+"/") {
			return true
		}
	}
	return false
}
