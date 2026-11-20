// Package hookcmd provides worktree-aware workspace digest resolution for the loop circuit-breaker.
package hookcmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mgantlett/nomos/substrate/cli/hookcmd/shellguard"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
)

// resolveLoopTrackingDir resolves the effective working tree or isolated worktree root
// targeted by a shell command or file edit so subdirectory Cwd settings never blind digests.
func resolveLoopTrackingDir(p *shellguard.HookPayload, hermeticDir string) string {
	baseDir := resolveHookWorkspaceDir(p, hermeticDir)
	if p == nil {
		return normalizeLoopWorkingTreeDir(baseDir)
	}
	if cdTarget := extractLeadingCdTarget(p.CommandLine, baseDir); cdTarget != "" {
		return normalizeLoopWorkingTreeDir(cdTarget)
	}
	if strings.TrimSpace(p.TargetFile) != "" {
		resolved := shellguard.NormalizeCanonicalPath(p.TargetFile, baseDir)
		if _, wtRoot, _, ok := extractWorktreeContextFromFile(resolved); ok {
			return filepath.Clean(wtRoot)
		}
	}
	return normalizeLoopWorkingTreeDir(baseDir)
}

// normalizeLoopWorkingTreeDir climbs from a nested subdirectory to its enclosing Git working tree root when present.
func normalizeLoopWorkingTreeDir(dir string) string {
	clean := filepath.Clean(dir)
	if wtRoot, err := workspace.FindWorkingTreeRoot(clean); err == nil && wtRoot != "" {
		return filepath.Clean(wtRoot)
	}
	return clean
}

// extractLeadingCdTarget extracts an existing target directory from a leading 'cd <dir>' shell segment.
func extractLeadingCdTarget(cmdLine, baseDir string) string {
	trimmed := strings.TrimSpace(cmdLine)
	if trimmed == "" {
		return ""
	}
	segs := shellguard.SplitUnquotedShellSegments(trimmed)
	if len(segs) == 0 {
		return ""
	}
	exe, _, rawArgs := shellguard.ExtractLeadingExeAndArgs(segs[0])
	if exe != "cd" || len(rawArgs) == 0 {
		return ""
	}
	candidate := strings.TrimSpace(rawArgs[0])
	if candidate == "" || candidate == "-" {
		return ""
	}
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(baseDir, candidate)
	}
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return filepath.Clean(candidate)
	}
	return ""
}

func hashWorkspaceFileStamp(path string, info os.FileInfo) string {
	if info.Size() <= maxDigestFileBytes && info.Mode().IsRegular() {
		if data, err := os.ReadFile(path); err == nil {
			sum := sha256.Sum256(data)
			return fmt.Sprintf("%d:%x", info.Size(), sum[:8])
		}
	}
	return fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
}

// computeWorkspaceMtimeDigest hashes either Git working-tree status (including sibling worktrees) or bounded file stamps.
func computeWorkspaceMtimeDigest(rootDir string) string {
	cleanRoot := normalizeLoopWorkingTreeDir(rootDir)
	if gitDigest, ok := computeGitWorkspaceDigest(cleanRoot); ok {
		return gitDigest
	}
	var records []string
	maxFiles := 512
	walkErr := filepath.WalkDir(cleanRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skippedStopAuditDirs[d.Name()] || d.Name() == "worktrees" || d.Name() == ".tmp" {
				return filepath.SkipDir
			}
			return nil
		}
		if len(records) >= maxFiles {
			return fs.SkipAll
		}
		if info, iErr := d.Info(); iErr == nil {
			rel, rErr := filepath.Rel(cleanRoot, path)
			if rErr != nil {
				rel = path
			}
			records = append(records, fmt.Sprintf("%s:%s", filepath.ToSlash(rel), hashWorkspaceFileStamp(path, info)))
		}
		return nil
	})
	if walkErr != nil {
		return "walk-err"
	}
	sort.Strings(records)
	sum := sha256.Sum256([]byte(strings.Join(records, "\n")))
	return hex.EncodeToString(sum[:16])
}

func computeGitWorkspaceDigest(cleanRoot string) (string, bool) {
	records, ok := collectSingleGitTreeRecords(cleanRoot, "")
	if !ok {
		return "", false
	}
	records = appendChildWorktreeRecords(cleanRoot, records)
	sort.Strings(records)
	sum := sha256.Sum256([]byte(strings.Join(records, "\n")))
	return hex.EncodeToString(sum[:16]), true
}

func appendChildWorktreeRecords(cleanRoot string, records []string) []string {
	wtParent := filepath.Join(cleanRoot, "worktrees")
	entries, err := os.ReadDir(wtParent)
	if err != nil {
		return records
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		wtDir := filepath.Join(wtParent, entry.Name())
		if wtRecs, ok := collectSingleGitTreeRecords(wtDir, "wt:"+entry.Name()+":"); ok {
			records = append(records, wtRecs...)
		}
	}
	return records
}

func collectSingleGitTreeRecords(treeDir, prefix string) ([]string, bool) {
	if _, err := os.Stat(filepath.Join(treeDir, ".git")); err != nil {
		return nil, false
	}
	out, err := workspace.SafeGitCommand(treeDir, "status", "--porcelain", "-uall").Output()
	if err != nil {
		return nil, false
	}
	var records []string
	if headOut, hErr := workspace.SafeGitCommand(treeDir, "rev-parse", "HEAD").Output(); hErr == nil {
		records = append(records, prefix+"HEAD:"+strings.TrimSpace(string(headOut)))
	}
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) < 4 {
			continue
		}
		rel := strings.TrimSpace(line[3:])
		if idx := strings.Index(rel, " -> "); idx >= 0 {
			rel = strings.TrimSpace(rel[idx+4:])
		}
		full := filepath.Join(treeDir, filepath.FromSlash(rel))
		if info, sErr := os.Stat(full); sErr == nil {
			records = append(records, fmt.Sprintf("%s%s:%s", prefix, line, hashWorkspaceFileStamp(full, info)))
		} else {
			records = append(records, prefix+line+":deleted")
		}
	}
	return records, true
}
