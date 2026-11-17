// Package hookcmd provides worktree root resolution, bounded slice helpers, and test-command classifiers.
package hookcmd

import (
	"path/filepath"
	"strings"
)

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
func resolveWorktreeTraceRoot(p *HookPayload, hermeticDir string) string {
	candidates := []string{hermeticDir, p.TargetFile, p.Cwd}
	candidates = append(candidates, p.WorkspacePaths...)
	baseCwd := resolveHookWorkspaceDir(p, hermeticDir)
	for _, raw := range candidates {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		norm := normalizeCanonicalPath(raw, baseCwd)
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
