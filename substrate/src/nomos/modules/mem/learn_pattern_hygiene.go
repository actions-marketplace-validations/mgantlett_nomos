// Package mem implements filesystem-grounded pattern scope and file-blueprint pruning for Nomos Mneme.
package mem

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	patternTopicPrefix           = "pattern:"
	habitTopicPrefix             = "pattern:habit:"
	deadEndTopicPrefix           = "dead_end:"
	maxZeroRecallPatternsPerDir  = 3
	staleZeroRecallPatternMaxAge = 30 * 24 * time.Hour
)

var blueprintBracketListRe = regexp.MustCompile(`\b(?:files|tests)=\[([^\]]+)\]`)

// SweepStaleRepoMemories tombstones dead-scope pitfalls, expired unit-test traps, and dead-directory
// or all-deleted-file structural patterns, returning all tombstoned records alongside the hygiene report (@intent: #PitfallHygieneReport).
func SweepStaleRepoMemories(store *Store, repoRoot, repoID string, now time.Time) ([]*Memory, PitfallHygieneReport) {
	var rep PitfallHygieneReport
	if store == nil || strings.TrimSpace(repoID) == "" {
		return nil, rep
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	all, err := store.ListAll()
	if err != nil {
		return nil, rep
	}
	checkDiskPaths := isAuthoritativeRepoRoot(repoRoot)
	normRepo := strings.ToLower(strings.TrimSpace(repoID))
	surviving, dismissed := sweepStalePitfallsWithReceipts(store, all, repoRoot, normRepo, checkDiskPaths, now, &rep)
	if checkDiskPaths {
		deadPatterns := sweepStalePatternsWithReceipts(store, all, repoRoot, normRepo, &rep)
		dismissed = append(dismissed, deadPatterns...)
	}
	retireOverflowZeroRecallPatterns(store, all, normRepo, now)
	rep.CrossScopeHabitsUpdated = consolidateCrossScopeGateHabits(store, surviving, repoID, normRepo)
	return dismissed, rep
}

// retireOverflowZeroRecallPatterns supersedes >30d zero-recall directory patterns beyond the newest 3 per scope.
func retireOverflowZeroRecallPatterns(store *Store, all []*Memory, normRepo string, now time.Time) {
	buckets := groupZeroRecallPatternsByScope(all, normRepo)
	cutoff := now.Add(-staleZeroRecallPatternMaxAge)
	for _, list := range buckets {
		if len(list) <= maxZeroRecallPatternsPerDir {
			continue
		}
		sort.Slice(list, func(i, j int) bool { return list[i].ID > list[j].ID })
		for _, old := range list[maxZeroRecallPatternsPerDir:] {
			if !old.CreatedAt.IsZero() && old.CreatedAt.Before(cutoff) {
				store.retireCooledPitfall(old.ID)
			}
		}
	}
}

// groupZeroRecallPatternsByScope buckets active unaccessed non-habit directory patterns by normalized scope.
func groupZeroRecallPatternsByScope(all []*Memory, normRepo string) map[string][]*Memory {
	buckets := make(map[string][]*Memory)
	for _, m := range all {
		if m == nil || m.Kind != KindPattern || m.Status != StatusActive || m.AccessCount > 0 {
			continue
		}
		if strings.ToLower(strings.TrimSpace(m.RepoID)) != normRepo {
			continue
		}
		if scope := strings.ToLower(extractScopeFromPatternTopic(m.Topic)); scope != "" {
			buckets[scope] = append(buckets[scope], m)
		}
	}
	return buckets
}

// DismissStaleRepoMemories executes SweepStaleRepoMemories and returns the itemized tombstoned records (@intent: #Memory).
func DismissStaleRepoMemories(store *Store, repoRoot, repoID string, now time.Time) []*Memory {
	dismissed, rep := SweepStaleRepoMemories(store, repoRoot, repoID, now)
	if rep.DeadPatternsTombstoned == 0 && rep.DeadScopesTombstoned == 0 && rep.ExpiredTestsTombstoned == 0 {
		return dismissed
	}
	return dismissed
}

func sweepStalePatternsWithReceipts(
	store *Store,
	all []*Memory,
	repoRoot, normRepo string,
	rep *PitfallHygieneReport,
) []*Memory {
	var dismissed []*Memory
	for _, m := range all {
		if m == nil || m.Kind != KindPattern || m.Status != StatusActive {
			continue
		}
		if strings.ToLower(strings.TrimSpace(m.RepoID)) != normRepo {
			continue
		}
		if !isStaleRepoPattern(repoRoot, m) && !isAstCeilingHabitPattern(m) {
			continue
		}
		if rec, err := store.Dismiss(m.ID); err == nil && rec != nil {
			rep.DeadPatternsTombstoned++
			dismissed = append(dismissed, rec)
		}
	}
	return dismissed
}

// isStaleRepoPattern returns true when a non-habit pattern:<scope> targets a deleted directory
// or when every file listed in its files=[...]/tests=[...] blueprint has been deleted from disk.
func isStaleRepoPattern(repoRoot string, m *Memory) bool {
	if m == nil || m.Kind != KindPattern {
		return false
	}
	scope := extractScopeFromPatternTopic(m.Topic)
	if scope == "" {
		return false
	}
	if isMissingRepoScopeDir(repoRoot, scope) {
		return true
	}
	listed := extractBlueprintFileList(m.Content)
	if len(listed) == 0 {
		return false
	}
	return !anyBlueprintFileExists(repoRoot, scope, listed)
}

// isStaleRepoPatternForRoots checks whether a pattern scope or file blueprint is missing across trunk and any active task worktree.
func isStaleRepoPatternForRoots(roots []string, m *Memory) bool {
	if len(roots) == 0 {
		return false
	}
	for _, root := range roots {
		if !isStaleRepoPattern(root, m) {
			return false
		}
	}
	return true
}

// isAstCeilingHabitPattern identifies legacy pattern:habit:* rows minted for AST file/complexity ceilings.
func isAstCeilingHabitPattern(m *Memory) bool {
	if m == nil || m.Kind != KindPattern {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(m.Topic))
	if !strings.HasPrefix(lower, habitTopicPrefix) {
		return false
	}
	return isAstCeilingGateSlug(strings.TrimPrefix(lower, habitTopicPrefix))
}

// isStaleRepoPitfallScope returns true when a pitfall or dead_end targets a missing directory, absent Trap file, or healed ceiling.
func isStaleRepoPitfallScope(repoRoot string, m *Memory) bool {
	if m == nil {
		return false
	}
	scope := extractScopeFromPitfallOrDeadEndTopic(m.Topic)
	return isMissingRepoScopeDir(repoRoot, scope) || isPitfallDeletedOrHealedOnDisk(repoRoot, m)
}

// isStaleRepoPitfallScopeForRoots checks whether a pitfall or dead_end scope is missing across trunk and any active task worktree.
func isStaleRepoPitfallScopeForRoots(roots []string, m *Memory) bool {
	if len(roots) == 0 {
		return false
	}
	for _, root := range roots {
		if !isStaleRepoPitfallScope(root, m) {
			return false
		}
	}
	return true
}

// resolveLiveRecallRoots returns authoritative repository and active worktree directories to check during read-time recall.
func resolveLiveRecallRoots(repoRoot, taskKey string) []string {
	if !isAuthoritativeRepoRoot(repoRoot) {
		return nil
	}
	cleanRoot := filepath.Clean(repoRoot)
	roots := []string{cleanRoot}
	cleanTask := strings.TrimSpace(taskKey)
	if cleanTask != "" {
		wt := filepath.Join(cleanRoot, "worktrees", filepath.Base(cleanRoot)+"-"+cleanTask)
		if info, err := os.Stat(wt); err == nil && info.IsDir() {
			roots = append(roots, wt)
		}
	}
	return roots
}

func extractScopeFromPitfallOrDeadEndTopic(topic string) string {
	if scope := extractScopeFromPitfallTopic(topic); scope != "" {
		return scope
	}
	trimmed := strings.TrimSpace(topic)
	if strings.HasPrefix(strings.ToLower(trimmed), deadEndTopicPrefix) {
		return strings.TrimSpace(trimmed[len(deadEndTopicPrefix):])
	}
	return ""
}

func extractScopeFromPatternTopic(topic string) string {
	trimmed := strings.TrimSpace(topic)
	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(lower, patternTopicPrefix) || strings.HasPrefix(lower, habitTopicPrefix) {
		return ""
	}
	return strings.TrimSpace(trimmed[len(patternTopicPrefix):])
}

func extractBlueprintFileList(content string) []string {
	matches := blueprintBracketListRe.FindAllStringSubmatch(content, 4)
	if len(matches) == 0 {
		return nil
	}
	var files []string
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		for _, tok := range strings.Split(match[1], ",") {
			if clean := strings.TrimSpace(tok); clean != "" {
				files = append(files, clean)
			}
		}
	}
	return files
}

func anyBlueprintFileExists(repoRoot, scope string, files []string) bool {
	baseDir := repoRoot
	if scope != "root" && scope != "." && scope != "" {
		baseDir = filepath.Join(repoRoot, filepath.FromSlash(scope))
	}
	for _, name := range files {
		info, err := os.Stat(filepath.Join(baseDir, filepath.FromSlash(name)))
		if err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}
