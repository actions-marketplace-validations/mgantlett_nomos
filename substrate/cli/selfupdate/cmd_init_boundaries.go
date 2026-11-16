package selfupdate

import (
	"github.com/mgantlett/nomos/substrate/cli/tui"
	"os"
	"path/filepath"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/constants"
)

func canonicalBoundaryPatternsForMode(includeWorktrees bool) []string {
	patterns := make([]string, 0, 10)
	if includeWorktrees {
		patterns = append(patterns, "/"+constants.DirWorktrees+"/")
	}
	patterns = append(patterns,
		"/"+constants.DirTmp+"/",
		"/.vscode/",
		"/"+constants.DirNomosHome+"/",
		"/go.work",
		"/go.work.sum",
		"*.tmp",
		"*.bak",
		"*.orig",
		"*.swp",
	)
	return patterns
}

// ScaffoldNomosBoundaries executes deterministic selfupdate verification logic (@intent task_lifecycle_cli_spec.md).
func ScaffoldNomosBoundaries(dir string) error {
	return ScaffoldNomosBoundariesForMode(dir, true, false)
}

// ScaffoldNomosBoundariesForMode applies ignore boundaries parameterized by worktree creation and stealth mode (@intent task_lifecycle_cli_spec.md).
func ScaffoldNomosBoundariesForMode(dir string, includeWorktrees, localStealth bool) error {
	if localStealth {
		return nil
	}
	boundaries := canonicalBoundaryPatternsForMode(includeWorktrees)
	gitIgnorePath := filepath.Join(dir, ".gitignore")
	if err := appendMissingPatterns(gitIgnorePath, "# Nomos Operational Boundaries", boundaries); err != nil {
		return err
	}

	gitDir := filepath.Join(dir, constants.DirGit)
	if fi, err := os.Stat(gitDir); err == nil && fi.IsDir() {
		infoDir := filepath.Join(gitDir, "info")
		if mkErr := os.MkdirAll(infoDir, 0755); mkErr == nil {
			gitExcludePath := filepath.Join(infoDir, "exclude")
			if aErr := appendMissingPatterns(gitExcludePath, "# Nomos Operational Boundaries", boundaries); aErr != nil {
				return aErr
			}
		}
	}
	if !includeWorktrees {
		return nil
	}
	return tui.EnsureIDEBoundarySettings(dir)
}

func hasIgnorePattern(existingContent, pattern string) bool {
	trimmedAnchor := strings.TrimPrefix(pattern, "/")
	for _, rawLine := range strings.Split(existingContent, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == pattern || (trimmedAnchor != "" && line == trimmedAnchor) {
			return true
		}
	}
	return false
}

func appendMissingPatterns(filePath, header string, patterns []string) error {
	existingBytes, err := os.ReadFile(filePath)
	var existingContent string
	if err == nil {
		existingContent = string(existingBytes)
	}

	var toAdd []string
	for _, p := range patterns {
		if !hasIgnorePattern(existingContent, p) {
			toAdd = append(toAdd, p)
		}
	}
	if len(toAdd) == 0 {
		return nil
	}

	payload := formatIgnoreBlock(existingContent, header, toAdd)
	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer func() {
		if cErr := f.Close(); cErr != nil {
			// non-fatal close
		}
	}()

	_, err = f.WriteString(payload)
	return err
}

func formatIgnoreBlock(existingContent, header string, toAdd []string) string {
	var sb strings.Builder
	if len(existingContent) > 0 && !strings.HasSuffix(existingContent, "\n") {
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
	sb.WriteString(header)
	sb.WriteString("\n")
	for _, p := range toAdd {
		sb.WriteString(p)
		sb.WriteString("\n")
	}
	return sb.String()
}
