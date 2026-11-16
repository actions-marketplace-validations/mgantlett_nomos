// Package cli implements Tier 1 agent adapters, software physics telemetry,
// release readiness, upgrades, and CLI command dispatch contracts.
package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mgantlett/nomos/substrate/cli/hookcmd"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/constants"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/types"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
)

const (
	localStealthStartMarker = "# Nomos Local Stealth Trial Start"
	localStealthEndMarker   = "# Nomos Local Stealth Trial End"
)

func localStealthPatterns(includeWorktrees bool) []string {
	patterns := []string{
		"/" + constants.FileNomosYAML,
		"/" + constants.FileNomosBaselineJSON,
		"/AGENTS.md",
		"/CLAUDE.md",
		"/" + constants.DirNomosHome + "/",
		"/" + constants.DirTmp + "/",
		"/" + hookcmd.AgentsDirToken + "/",
		"/" + claudeDirToken + "/",
		"/" + hookcmd.CursorDirToken + "/",
		"/" + geminiDirToken + "/",
		"/" + openCodeDirToken + "/",
		"/go.work",
		"/go.work.sum",
		"*.tmp",
		"*.bak",
		"*.orig",
		"*.swp",
	}
	if includeWorktrees {
		patterns = append(patterns, "/"+constants.DirWorktrees+"/")
	}
	return patterns
}

func resolveGitExcludeFile(absDir string) (string, error) {
	gitDir := filepath.Join(absDir, constants.DirGit)
	if out, err := workspace.SafeGitCommand(absDir, "rev-parse", "--git-common-dir").Output(); err == nil {
		raw := strings.TrimSpace(string(out))
		if raw != "" {
			if filepath.IsAbs(raw) {
				gitDir = raw
			} else {
				gitDir = filepath.Join(absDir, raw)
			}
		}
	}
	if _, err := os.Stat(gitDir); err != nil {
		return "", types.NewNomosError(types.ErrCodeBoundary, "--local stealth mode requires an initialized Git repository").
			WithRemedy("run 'git init' first or omit --local")
	}
	infoDir := filepath.Join(gitDir, "info")
	if err := os.MkdirAll(infoDir, 0755); err != nil {
		return "", types.NewNomosError(types.ErrCodeSubstrate, "failed creating git info directory: %v", err)
	}
	return filepath.Join(infoDir, "exclude"), nil
}

func stripStealthExcludeBlock(existing string) string {
	lines := strings.Split(existing, "\n")
	out := make([]string, 0, len(lines))
	inside := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == localStealthStartMarker {
			inside = true
			continue
		}
		if trimmed == localStealthEndMarker {
			inside = false
			continue
		}
		if !inside {
			out = append(out, line)
		}
	}
	cleaned := strings.TrimRight(strings.Join(out, "\n"), "\n")
	if cleaned != "" {
		cleaned += "\n"
	}
	return cleaned
}

func syncLocalStealthExcludes(absDir string, includeWorktrees bool) error {
	excludePath, err := resolveGitExcludeFile(absDir)
	if err != nil {
		return err
	}
	raw, rErr := os.ReadFile(excludePath)
	if rErr != nil && !os.IsNotExist(rErr) {
		return types.NewNomosError(types.ErrCodeSubstrate, "failed reading %s: %v", excludePath, rErr)
	}
	cleaned := stripStealthExcludeBlock(string(raw))
	var sb strings.Builder
	sb.WriteString(cleaned)
	if cleaned != "" && !strings.HasSuffix(cleaned, "\n\n") {
		sb.WriteString("\n")
	}
	sb.WriteString(localStealthStartMarker)
	sb.WriteString("\n")
	for _, pat := range localStealthPatterns(includeWorktrees) {
		sb.WriteString(pat)
		sb.WriteString("\n")
	}
	sb.WriteString(localStealthEndMarker)
	sb.WriteString("\n")
	if wErr := os.WriteFile(excludePath, []byte(sb.String()), 0644); wErr != nil {
		return types.NewNomosError(types.ErrCodeSubstrate, "failed writing %s: %v", excludePath, wErr)
	}
	return nil
}

func clearLocalStealthExcludes(absDir string) error {
	gitDir := filepath.Join(absDir, constants.DirGit)
	if _, err := os.Stat(gitDir); err != nil {
		return nil
	}
	excludePath, err := resolveGitExcludeFile(absDir)
	if err != nil {
		return nil
	}
	raw, rErr := os.ReadFile(excludePath)
	if rErr != nil || !strings.Contains(string(raw), localStealthStartMarker) {
		return nil
	}
	cleaned := stripStealthExcludeBlock(string(raw))
	if wErr := os.WriteFile(excludePath, []byte(cleaned), 0644); wErr != nil {
		return types.NewNomosError(types.ErrCodeSubstrate, "failed cleaning %s: %v", excludePath, wErr)
	}
	return nil
}
