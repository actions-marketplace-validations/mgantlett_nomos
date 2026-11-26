// Package task implements held-out verification oracle guards and hard --scope write fences (Law 2, Law 3, Law 7).
// @intent task_lifecycle_cli_spec.md
package task

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
)

// EvaluateTaskWriteBoundaries enforces immutable_test_paths, --lock-tests held-out suite immutability,
// and hard --scope write fencing (with auto-permitted paired unit verification files) on a workspace-relative path.
// @intent task_lifecycle_cli_spec.md #TaskRecord
func EvaluateTaskWriteBoundaries(wtRoot, taskKey, relPath string, rec *TaskRecord, immutableTestPaths []string) string {
	cleanRel := normalizeScopeRelPath(relPath)
	if cleanRel == "" || strings.HasPrefix(cleanRel, ".tmp/") {
		return ""
	}
	upperKey := strings.ToUpper(strings.TrimSpace(taskKey))
	if strings.HasPrefix(upperKey, "SUB-") && matchesAnyScopeGlob(cleanRel, immutableTestPaths) {
		return fmt.Sprintf("Immutable Verification Oracle Violation (Law 2 & Law 7): '%s' matches governance.quality_gates.immutable_test_paths", cleanRel)
	}
	if rec != nil && rec.LockTests && IsTestSuiteRelPath(cleanRel) && IsCommittedInGitHead(wtRoot, cleanRel) {
		return fmt.Sprintf("Held-Out Verification Oracle Violation (Law 2 & Law 7): task %s has --lock-tests active; pre-existing committed verification suite '%s' is read-only", upperKey, cleanRel)
	}
	if rec != nil && len(rec.ScopePaths) > 0 && !MatchesAnyScopeOrPairedTest(cleanRel, rec.ScopePaths) {
		return fmt.Sprintf("Hard Scope Fence Violation (Law 3 & Law 7): task %s restricts edits to scope_paths [%s]; '%s' is outside declared scope", upperKey, strings.Join(rec.ScopePaths, ", "), cleanRel)
	}
	return ""
}

var testSuiteSuffixes = []string{
	"_test.go", "_test.py", "_spec.rb", "_test.rs",
	"test.java", "test.kt", "tests.cs", "_test.cpp",
}

// IsTestSuiteRelPath reports whether relPath is a recognized polyglot unit or integration verification file.
// @intent task_lifecycle_cli_spec.md #TaskRecord
func IsTestSuiteRelPath(relPath string) bool {
	lower := strings.ToLower(normalizeScopeRelPath(relPath))
	if lower == "" {
		return false
	}
	if strings.HasPrefix(lower, "tests/") || strings.Contains(lower, "/tests/") || strings.Contains(lower, "/__tests__/") {
		return true
	}
	base := path.Base(lower)
	if strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") || strings.HasPrefix(base, "test_") {
		return true
	}
	for _, suf := range testSuiteSuffixes {
		if strings.HasSuffix(base, suf) {
			return true
		}
	}
	return false
}

// IsCommittedInGitHead reports whether relPath already exists in git HEAD of wtRoot.
// @intent task_lifecycle_cli_spec.md #TaskRecord
func IsCommittedInGitHead(wtRoot, relPath string) bool {
	cleanRoot := strings.TrimSpace(wtRoot)
	cleanRel := normalizeScopeRelPath(relPath)
	if cleanRoot == "" || cleanRel == "" {
		return false
	}
	return workspace.SafeGitCommand(cleanRoot, "cat-file", "-e", "HEAD:"+cleanRel).Run() == nil
}

// MatchesAnyScopeOrPairedTest reports whether relPath matches scopePaths directly or is a paired
// unit verification file for a target matched by scopePaths.
// @intent task_lifecycle_cli_spec.md #TaskRecord
func MatchesAnyScopeOrPairedTest(relPath string, scopePaths []string) bool {
	cleanRel := normalizeScopeRelPath(relPath)
	if matchesAnyScopeGlob(cleanRel, scopePaths) {
		return true
	}
	if !IsTestSuiteRelPath(cleanRel) {
		return false
	}
	for _, prodCand := range derivePairedProductionPaths(cleanRel) {
		if matchesAnyScopeGlob(prodCand, scopePaths) {
			return true
		}
	}
	return false
}

func matchesAnyScopeGlob(relPath string, patterns []string) bool {
	for _, raw := range patterns {
		if matchScopeGlob(relPath, raw) {
			return true
		}
	}
	return false
}

func matchScopeGlob(relPath, rawPattern string) bool {
	pat := normalizeScopeRelPath(rawPattern)
	if pat == "" {
		return false
	}
	if strings.HasSuffix(pat, "/**") {
		prefix := strings.TrimSuffix(pat, "/**")
		return relPath == prefix || strings.HasPrefix(relPath, prefix+"/")
	}
	if strings.HasSuffix(rawPattern, "/") {
		return relPath == pat || strings.HasPrefix(relPath, pat+"/")
	}
	if strings.Contains(pat, "**") {
		return matchDoubleStarGlob(relPath, pat)
	}
	if matched, err := path.Match(pat, relPath); err == nil && matched {
		return true
	}
	return relPath == pat
}

func matchDoubleStarGlob(relPath, pat string) bool {
	parts := strings.Split(pat, "**")
	if len(parts) != 2 {
		return false
	}
	prefix := strings.TrimSuffix(parts[0], "/")
	suffix := strings.TrimPrefix(parts[1], "/")
	if prefix != "" && relPath != prefix && !strings.HasPrefix(relPath, prefix+"/") {
		return false
	}
	if suffix == "" {
		return true
	}
	if matched, err := path.Match(suffix, path.Base(relPath)); err == nil && matched {
		return true
	}
	return strings.HasSuffix(relPath, "/"+suffix) || relPath == suffix
}

func derivePairedProductionPaths(testRel string) []string {
	dir, base := path.Dir(testRel), path.Base(testRel)
	if dir == "." {
		dir = ""
	}
	parentDir := dir
	if path.Base(dir) == "__tests__" {
		parentDir = path.Dir(dir)
		if parentDir == "." {
			parentDir = ""
		}
	}
	var stems []string
	if strings.HasSuffix(base, "_test.go") {
		stems = append(stems, strings.TrimSuffix(base, "_test.go")+".go")
	}
	for _, marker := range []string{".test.", ".spec."} {
		if idx := strings.Index(base, marker); idx > 0 {
			stems = append(stems, base[:idx]+"."+base[idx+len(marker):])
		}
	}
	if strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py") {
		stems = append(stems, strings.TrimPrefix(base, "test_"))
	}
	if strings.HasSuffix(base, "_test.py") {
		stems = append(stems, strings.TrimSuffix(base, "_test.py")+".py")
	}
	var out []string
	for _, st := range stems {
		out = append(out, joinRelSlash(dir, st))
		if parentDir != dir {
			out = append(out, joinRelSlash(parentDir, st))
		}
	}
	return out
}

func joinRelSlash(dir, file string) string {
	if dir == "" {
		return file
	}
	return dir + "/" + file
}

func normalizeScopeRelPath(raw string) string {
	clean := filepath.ToSlash(strings.TrimSpace(raw))
	clean = strings.TrimPrefix(clean, "./")
	clean = strings.TrimPrefix(clean, "/")
	return strings.TrimSuffix(clean, "/")
}
