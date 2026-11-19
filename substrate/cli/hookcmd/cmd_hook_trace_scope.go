// Package hookcmd provides worktree root resolution, bounded slice helpers, and test-command classifiers.
package hookcmd

import (
	"github.com/mgantlett/nomos/substrate/cli/hookcmd/shellguard"
	"io/fs"
	"path/filepath"
	"strings"
)

const maxAutomatedTestWalkFiles = 256

var behavioralTestSkipDirs = map[string]bool{
	".git": true, ".venv": true, "venv": true, "node_modules": true,
	"__pycache__": true, "vendor": true, ".nomos": true, "worktrees": true,
	".pytest_cache": true, ".mypy_cache": true, ".ruff_cache": true,
}

var executableSourceExts = map[string]bool{
	".py": true, ".js": true, ".ts": true, ".tsx": true, ".jsx": true,
	".mjs": true, ".cjs": true, ".rb": true, ".rake": true, ".rs": true,
	".java": true, ".kt": true, ".kts": true, ".cs": true,
}

func appendUniqueBounded(items []string, candidate string, limit int) []string {
	if candidate == "" || len(items) >= limit {
		return items
	}
	for _, existing := range items {
		if existing == candidate {
			return items
		}
	}
	return append(items, candidate)
}

func isRecognizedTestCommand(cmdLine string) bool {
	lower := strings.ToLower(cmdLine)
	for _, tok := range []string{
		"go test", "pytest", "py.test", "cargo test", "npm test",
		"pnpm test", "yarn test", "vitest", "jest", "bun test",
		"unittest", "ctest", "nomos verify", "nomos task review",
	} {
		if strings.Contains(lower, tok) {
			return true
		}
	}
	return false
}

// resolveWorktreeTraceRoot extracts the enclosing '/worktrees/<name>' root when operating in a task worktree.
func resolveWorktreeTraceRoot(p *shellguard.HookPayload, hermeticDir string) string {
	candidates := []string{hermeticDir, p.TargetFile, p.Cwd}
	candidates = append(candidates, p.WorkspacePaths...)
	baseCwd := resolveHookWorkspaceDir(p, hermeticDir)
	for _, raw := range candidates {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		norm := shellguard.NormalizeCanonicalPath(raw, baseCwd)
		if wt := extractEnclosingWorktreeRoot(norm); wt != "" {
			return wt
		}
	}
	return ""
}

func extractEnclosingWorktreeRoot(slashPath string) string {
	const marker = "/worktrees/"
	idx := strings.Index(slashPath, marker)
	if idx < 0 {
		return ""
	}
	rest := slashPath[idx+len(marker):]
	if rest == "" {
		return ""
	}
	seg := rest
	if slash := strings.IndexByte(rest, '/'); slash >= 0 {
		seg = rest[:slash]
	}
	if seg == "" {
		return ""
	}
	return filepath.ToSlash(slashPath[:idx+len(marker)+len(seg)])
}

func isExecutableSourceRelPath(rel string) bool {
	slash := strings.ToLower(filepath.ToSlash(strings.TrimSpace(rel)))
	if slash == "" || strings.HasPrefix(slash, ".tmp/") || isAutomatedTestRelPath(slash) {
		return false
	}
	return executableSourceExts[filepath.Ext(slash)]
}

func anyExecutableSourceRel(rels []string) bool {
	for _, r := range rels {
		if isExecutableSourceRelPath(r) {
			return true
		}
	}
	return false
}

func anyExecutableSourceAbs(wsRoot string, absPaths []string) bool {
	for _, abs := range absPaths {
		rel := abs
		if wsRoot != "" {
			if computed, err := filepath.Rel(wsRoot, abs); err == nil {
				rel = computed
			}
		}
		if isExecutableSourceRelPath(rel) {
			return true
		}
	}
	return false
}

func isAutomatedTestRelPath(lowerSlash string) bool {
	base := filepath.Base(lowerSlash)
	if strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py") {
		return true
	}
	for _, sfx := range []string{"_test.go", "_test.py", "_test.rb", "_test.rs", ".test.ts", ".test.tsx", ".test.js", ".test.jsx", ".spec.ts", ".spec.tsx", ".spec.js", "test.java", "tests.java", "test.cs", "tests.cs"} {
		if strings.HasSuffix(base, sfx) {
			return true
		}
	}
	for _, dirTok := range []string{"tests/", "test/", "__tests__/", "spec/", ".tmp/tests/", ".tmp/test/"} {
		if strings.HasPrefix(lowerSlash, dirTok) || strings.Contains(lowerSlash, "/"+dirTok) {
			return true
		}
	}
	return false
}

func hasWorkspaceAutomatedTests(wsRoot string) bool {
	if strings.TrimSpace(wsRoot) == "" {
		return false
	}
	cleanRoot := filepath.Clean(wsRoot)
	for _, sub := range []string{filepath.Join(".tmp", "tests"), filepath.Join(".tmp", "test"), "tests", "test", "__tests__", "spec"} {
		if dirContainsTestSource(filepath.Join(cleanRoot, sub)) {
			return true
		}
	}
	return walkRootForAutomatedTests(cleanRoot)
}

func dirContainsTestSource(dir string) bool {
	found := false
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d == nil || found {
			return nil
		}
		if d.IsDir() {
			if behavioralTestSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if executableSourceExts[strings.ToLower(filepath.Ext(d.Name()))] || strings.HasSuffix(strings.ToLower(d.Name()), "_test.go") {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found
}

func walkRootForAutomatedTests(cleanRoot string) bool {
	found := false
	seen := 0
	_ = filepath.WalkDir(cleanRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d == nil || found {
			return nil
		}
		if d.IsDir() {
			if path != cleanRoot && (behavioralTestSkipDirs[d.Name()] || d.Name() == ".tmp") {
				return filepath.SkipDir
			}
			return nil
		}
		seen++
		if seen > maxAutomatedTestWalkFiles {
			return fs.SkipAll
		}
		rel, rErr := filepath.Rel(cleanRoot, path)
		if rErr == nil && isAutomatedTestRelPath(strings.ToLower(filepath.ToSlash(rel))) {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found
}

func isBehavioralExecShellCommand(cmdLine string) bool {
	trimmed := strings.TrimSpace(cmdLine)
	if trimmed == "" || isNomosVerificationCommand(trimmed) {
		return false
	}
	lower := strings.ToLower(trimmed)
	for _, syntaxOnly := range []string{"py_compile", "node --check", "tsc --noemit", "cargo check", "ruff check", "ruff format", "black "} {
		if strings.Contains(lower, syntaxOnly) {
			return false
		}
	}
	if isRecognizedTestCommand(trimmed) {
		return true
	}
	for _, tok := range []string{"python ", "python3 ", "uv run", "node ", "tsx ", "ruby ", "cargo run", ".venv/bin/python"} {
		if strings.Contains(lower, tok) {
			return true
		}
	}
	return false
}

func hasBehavioralExecutionFailure(resp string) bool {
	if hasFailedVerificationResponse(resp) {
		return true
	}
	lower := strings.ToLower(resp)
	return hasAnySubstring(lower,
		"traceback (most recent call last)",
		"assertionerror",
		"syntaxerror",
		"exited with code ",
		"exit status ",
	)
}

func hasTrailingBehavioralSubcommand(cmdLine string) bool {
	last := ""
	for _, chunk := range strings.FieldsFunc(cmdLine, func(r rune) bool { return r == '\n' }) {
		for _, part := range strings.Split(chunk, "&&") {
			if t := strings.TrimSpace(part); t != "" {
				last = t
			}
		}
	}
	if last == "" || strings.Contains(last, ">") || strings.Contains(strings.ToLower(last), "tee ") {
		return false
	}
	return isBehavioralExecShellCommand(last)
}
