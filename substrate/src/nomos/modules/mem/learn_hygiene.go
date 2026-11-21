// Package mem implements automated pitfall scope hygiene, ephemeral test-trace expiry,
// and cross-directory habit pattern synthesis for Nomos Learn.
package mem

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	ephemeralTestAssertionTTL     = 24 * time.Hour
	crossScopeConsolidationMinDir = 3
	cooledOncePitfallMinAccess    = 8
)

// PitfallHygieneReport summarizes automated pitfall and pattern scope pruning and cross-scope promotion (#PitfallHygieneReport).
type PitfallHygieneReport struct {
	DeadScopesTombstoned    int `json:"dead_scopes_tombstoned"`
	DeadPatternsTombstoned  int `json:"dead_patterns_tombstoned"`
	ExpiredTestsTombstoned  int `json:"expired_tests_tombstoned"`
	CrossScopeHabitsUpdated int `json:"cross_scope_habits_updated"`
}

// SweepRepoPitfallHygiene tombstones pitfalls and patterns pointing at deleted repo directories or files,
// expires stale one-off unit test failure traces, and synthesizes repo-wide habit
// patterns when a single gate trips across >= 3 directories (@intent: #Memory).
func SweepRepoPitfallHygiene(store *Store, repoRoot, repoID string, now time.Time) PitfallHygieneReport {
	_, rep := SweepStaleRepoMemories(store, repoRoot, repoID, now)
	return rep
}

// isAuthoritativeRepoRoot returns true when repoRoot is a real repository checkout on disk.
func isAuthoritativeRepoRoot(repoRoot string) bool {
	clean := strings.TrimSpace(repoRoot)
	if clean == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(clean, ".git")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(clean, "nomos.yaml")); err == nil {
		return true
	}
	return false
}

// isEphemeralTestAssertionContent delegates to isPolyglotEphemeralTestContent for polyglot single-test assertion traps.
func isEphemeralTestAssertionContent(content string) bool {
	return isPolyglotEphemeralTestContent(content)
}

// sweepStalePitfallsWithReceipts tombstones dead-directory pitfalls, bare legacy traps, expired test assertions, and cooled 1x guardrails.
func sweepStalePitfallsWithReceipts(
	store *Store,
	all []*Memory,
	repoRoot, normRepo string,
	checkDiskPaths bool,
	now time.Time,
	rep *PitfallHygieneReport,
) ([]*Memory, []*Memory) {
	topicDepths := countPitfallTopicChain(all, normRepo)
	var surviving []*Memory
	var dismissed []*Memory
	for _, m := range all {
		if !isActiveRepoPitfall(m, normRepo) {
			continue
		}
		if rec := tryDismissStaleOrNoisePitfall(store, m, repoRoot, checkDiskPaths, now, rep); rec != nil {
			dismissed = append(dismissed, rec)
			continue
		}
		if isCooledOnceGuardrail(m, topicDepths[strings.ToLower(m.Topic)], now) && store.retireCooledPitfall(m.ID) {
			continue
		}
		surviving = append(surviving, m)
	}
	return surviving, dismissed
}

func tryDismissStaleOrNoisePitfall(
	store *Store,
	m *Memory,
	repoRoot string,
	checkDiskPaths bool,
	now time.Time,
	rep *PitfallHygieneReport,
) *Memory {
	if checkDiskPaths && isStaleRepoPitfallScope(repoRoot, m) {
		if rec, err := store.Dismiss(m.ID); err == nil && rec != nil {
			rep.DeadScopesTombstoned++
			return rec
		}
	}
	if isLegacyBarePitfallContent(m.Content) || isExpiredEphemeralTestPitfall(m, now) {
		if rec, err := store.Dismiss(m.ID); err == nil && rec != nil {
			rep.ExpiredTestsTombstoned++
			return rec
		}
	}
	return nil
}

func isCooledOnceGuardrail(m *Memory, topicDepth int, now time.Time) bool {
	if m == nil || m.Kind != KindPitfall || topicDepth > 1 || m.AccessCount < cooledOncePitfallMinAccess {
		return false
	}
	if m.CreatedAt.IsZero() {
		return false
	}
	return now.Sub(m.CreatedAt) > defaultUnscopedPitfallCoolWindow
}

func (s *Store) retireCooledPitfall(id int) bool {
	if s == nil || id <= 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`UPDATE memories SET status = 'superseded' WHERE id = ? AND status = 'active'`, id)
	if err != nil {
		return false
	}
	n, rErr := res.RowsAffected()
	return rErr == nil && n > 0
}

// isMissingRepoScopeDir checks whether a relative directory scope has been removed from repoRoot.
func isMissingRepoScopeDir(repoRoot, scope string) bool {
	clean := strings.TrimSpace(scope)
	if clean == "" || clean == workspaceScopeLabel || clean == "root" || clean == "." {
		return false
	}
	info, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(clean)))
	if err != nil {
		return os.IsNotExist(err)
	}
	return !info.IsDir()
}

// isExpiredEphemeralTestPitfall returns true when a unit-test assertion failure has aged past its 72h retry window.
func isExpiredEphemeralTestPitfall(m *Memory, now time.Time) bool {
	if !isEphemeralTestAssertionContent(m.Content) || m.CreatedAt.IsZero() {
		return false
	}
	return now.Sub(m.CreatedAt) > ephemeralTestAssertionTTL
}

// extractScopeFromPitfallTopic parses the trailing <scope> segment from pitfall:<gate>:<scope>.
func extractScopeFromPitfallTopic(topic string) string {
	trimmed := strings.TrimSpace(topic)
	if !strings.HasPrefix(strings.ToLower(trimmed), "pitfall:") {
		return ""
	}
	rest := trimmed[len("pitfall:"):]
	idx := strings.IndexByte(rest, ':')
	if idx < 0 || idx+1 >= len(rest) {
		return ""
	}
	return strings.TrimSpace(rest[idx+1:])
}

// extractGateSlugFromPitfallTopic parses the <gate> slug from pitfall:<gate>:<scope>.
func extractGateSlugFromPitfallTopic(topic string) string {
	trimmed := strings.ToLower(strings.TrimSpace(topic))
	if !strings.HasPrefix(trimmed, "pitfall:") {
		return ""
	}
	rest := trimmed[len("pitfall:"):]
	idx := strings.IndexByte(rest, ':')
	if idx <= 0 {
		return rest
	}
	return rest[:idx]
}

// consolidateCrossScopeGateHabits synthesizes one repo-wide KindPattern when the same gate trips across >= 3 directories.
func consolidateCrossScopeGateHabits(store *Store, activePitfalls []*Memory, repoID, normRepo string) int {
	byGate := groupPitfallsByGateSlug(activePitfalls)
	updated := 0
	for gateSlug, scopesMap := range byGate {
		if len(scopesMap) < crossScopeConsolidationMinDir {
			continue
		}
		if upsertHabitPattern(store, repoID, normRepo, gateSlug, scopesMap) {
			updated++
		}
	}
	return updated
}

// groupPitfallsByGateSlug buckets non-ephemeral active pitfalls by gate slug and distinct directory scope.
func groupPitfallsByGateSlug(activePitfalls []*Memory) map[string]map[string]*Memory {
	byGate := make(map[string]map[string]*Memory)
	for _, m := range activePitfalls {
		if isEphemeralTestAssertionContent(m.Content) {
			continue
		}
		gateSlug := extractGateSlugFromPitfallTopic(m.Topic)
		scope := extractScopeFromPitfallTopic(m.Topic)
		if gateSlug == "" || isAstCeilingGateSlug(gateSlug) || scope == "" || scope == workspaceScopeLabel {
			continue
		}
		if byGate[gateSlug] == nil {
			byGate[gateSlug] = make(map[string]*Memory)
		}
		byGate[gateSlug][scope] = m
	}
	return byGate
}

// upsertHabitPattern saves and deduplicates a consolidated repo-wide habit blueprint under pattern:habit:<gate>.
func upsertHabitPattern(store *Store, repoID, normRepo, gateSlug string, scopesMap map[string]*Memory) bool {
	scopes := make([]string, 0, len(scopesMap))
	var newest *Memory
	for sc, rec := range scopesMap {
		scopes = append(scopes, sc)
		if newest == nil || rec.ID > newest.ID {
			newest = rec
		}
	}
	sort.Strings(scopes)
	topic := "pattern:habit:" + gateSlug
	exemplar := extractVerifiedFixTail(newest.Content)
	content := fmt.Sprintf("[Repo-Wide Habit (%s across %d dirs: %s)] %s",
		strings.ReplaceAll(gateSlug, "_", " "),
		len(scopes),
		strings.Join(scopes, ", "),
		exemplar,
	)
	rec := &Memory{
		RepoID:  repoID,
		Kind:    KindPattern,
		Topic:   topic,
		Content: content,
		TaskID:  newest.TaskID,
		Status:  StatusActive,
	}
	if err := store.Save(rec); err != nil {
		return false
	}
	store.supersedeOlderTopicPatterns(normRepo, topic, rec.ID)
	return true
}

// extractVerifiedFixTail extracts the concise Trap -> Verified Fix clause from a distilled pitfall body.
func extractVerifiedFixTail(content string) string {
	idx := strings.Index(content, "] ")
	if idx >= 0 && idx+2 < len(content) {
		return strings.TrimSpace(content[idx+2:])
	}
	return strings.TrimSpace(content)
}
