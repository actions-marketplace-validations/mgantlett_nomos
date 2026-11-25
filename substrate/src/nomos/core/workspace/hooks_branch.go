// Package workspace provides post-commit Branch Guard Mneme distillation hook installation.
package workspace

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/types"
)

// PostCommitHookScript invokes nomos hook post-commit on Branch Guard commits to distill Mneme lessons.
const PostCommitHookScript = `#!/usr/bin/env bash
{
    if [[ "${NOMOS_INIT_IN_PROGRESS:-0}" == "1" ]]; then
        exit 0
    fi

    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    if [ -z "$REPO_ROOT" ] || [ ! -f "$REPO_ROOT/nomos.yaml" ]; then
        exit 0
    fi
    if ! grep -qE '^[[:space:]]*isolation_mode:[[:space:]]*["'\'']?branch["'\'']?([[:space:]]*(#.*)?)?$' "$REPO_ROOT/nomos.yaml"; then
        exit 0
    fi

    NOMOS_BIN=""
    if command -v nomos >/dev/null 2>&1; then
        NOMOS_BIN="nomos"
    elif [ -n "${GOBIN:-}" ] && [ -x "${GOBIN}/nomos" ]; then
        NOMOS_BIN="${GOBIN}/nomos"
    elif [ -x "$HOME/go/bin/nomos" ]; then
        NOMOS_BIN="$HOME/go/bin/nomos"
    elif [ -x "$HOME/.local/bin/nomos" ]; then
        NOMOS_BIN="$HOME/.local/bin/nomos"
    elif [ -x "$HOME/bin/nomos" ]; then
        NOMOS_BIN="$HOME/bin/nomos"
    fi

    if [ -n "$NOMOS_BIN" ]; then
        "$NOMOS_BIN" hook post-commit --dir "$REPO_ROOT" 2>/dev/null || true
    fi
    exit 0
}
`

func writePostCommitHook(hooksDir string) error {
	if err := os.WriteFile(filepath.Join(hooksDir, "post-commit"), []byte(PostCommitHookScript), 0755); err != nil {
		return types.NewNomosError(types.ErrCodeBoundary, "failed writing post-commit hook: %v", err)
	}
	return nil
}

var canonicalExcludeEntries = []string{
	"/worktrees/",
	"/.tmp/",
	"*.tmp",
	"*.bak",
	"*.orig",
	"*.swp",
}

// EnsureGitExcludeBoundaries self-heals canonical scratch and worktree patterns in .git/info/exclude.
func EnsureGitExcludeBoundaries(repoRoot string) {
	if strings.TrimSpace(repoRoot) == "" {
		return
	}
	gitDir, err := resolveGitCommonDir(repoRoot)
	if err != nil {
		return
	}
	infoDir := filepath.Join(gitDir, "info")
	if mkErr := os.MkdirAll(infoDir, 0755); mkErr != nil {
		return
	}
	excludePath := filepath.Join(infoDir, "exclude")
	existingBytes, rErr := os.ReadFile(excludePath)
	var existing string
	if rErr == nil {
		existing = string(existingBytes)
	}
	var missing []string
	for _, pat := range canonicalExcludeEntries {
		if !excludeContainsPattern(existing, pat) {
			missing = append(missing, pat)
		}
	}
	if len(missing) == 0 {
		return
	}
	var sb strings.Builder
	sb.WriteString(existing)
	if len(existing) > 0 && !strings.HasSuffix(existing, "\n") {
		sb.WriteString("\n")
	}
	sb.WriteString("# Nomos Self-Healed Scratch & Worktree Boundaries\n")
	for _, m := range missing {
		sb.WriteString(m + "\n")
	}
	if wErr := os.WriteFile(excludePath, []byte(sb.String()), 0644); wErr != nil {
		return
	}
}

func excludeContainsPattern(existing, pattern string) bool {
	trimmedAnchor := strings.TrimPrefix(pattern, "/")
	for _, raw := range strings.Split(existing, "\n") {
		line := strings.TrimSpace(raw)
		if line == pattern || (trimmedAnchor != "" && line == trimmedAnchor) {
			return true
		}
	}
	return false
}
