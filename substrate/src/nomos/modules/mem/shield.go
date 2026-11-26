// Package mem implements repo-scoped Shield telemetry persistence in Nomos Mneme.
// @intent mneme_memory_spec.md #ShieldSummary
package mem

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/config"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/constants"
)

const (
	// ShieldCategoryAntiCheat marks blocked skipped tests, stubs, or tampered assertions.
	ShieldCategoryAntiCheat = "anti_cheat"
	// ShieldCategorySecurityAirlock marks blocked credential reads, SSRF, or hook bypasses.
	ShieldCategorySecurityAirlock = "security_airlock"
	// ShieldCategoryTrunkGuard marks blocked direct mutations on a protected trunk branch.
	ShieldCategoryTrunkGuard = "trunk_guard"
	// ShieldCategoryStructuralRatchet marks blocked structural/debt/boundary gate failures.
	ShieldCategoryStructuralRatchet = "structural_ratchet"
	// ShieldCategoryStopGuard marks blocked premature agent turns with unverified edits.
	ShieldCategoryStopGuard = "stop_guard"
	// ShieldCategoryVerifiedCommit marks a completed verified commit checkpoint.
	ShieldCategoryVerifiedCommit = "verified_commit"
)

// ShieldSummary aggregates repo-scoped Branch Guard and verification catches for checkpoint visibility (#ShieldSummary).
type ShieldSummary struct {
	RepoID           string         `json:"repo_id"`
	ShortcutsBlocked int            `json:"shortcuts_blocked"`
	BlockedLast7Days int            `json:"blocked_last_7_days"`
	CategoryCounts   map[string]int `json:"category_counts,omitempty"`
	VerifiedCommits  int            `json:"verified_commits"`
	DistilledLessons int            `json:"distilled_lessons"`
	LastEventAt      string         `json:"last_event_at,omitempty"`
}

func resolveShieldRepoID(repoRoot string) string {
	clean := strings.TrimSpace(repoRoot)
	if clean == "" {
		return ""
	}
	cfgPath := filepath.Join(clean, constants.FileNomosYAML)
	if _, statErr := os.Stat(cfgPath); statErr == nil {
		if cfg, err := config.LoadConfig(cfgPath); err == nil && cfg != nil && strings.TrimSpace(cfg.Project) != "" {
			return strings.ToLower(strings.TrimSpace(cfg.Project))
		}
	}
	base := strings.ToLower(filepath.Base(filepath.Clean(clean)))
	if base == "" || base == "." || base == string(filepath.Separator) {
		return ""
	}
	return base
}

// RecordShieldEvent persists a repo-scoped hook block, verification failure, or verified commit counter into mneme.db (#ShieldSummary).
func RecordShieldEvent(repoRoot, category, gateName, branch, detail string) {
	repoID := resolveShieldRepoID(repoRoot)
	cleanCat := strings.TrimSpace(category)
	if repoID == "" || cleanCat == "" {
		return
	}
	store, err := NewStore("")
	if err != nil {
		return
	}
	defer func() {
		if cErr := store.Close(); cErr != nil {
			// best-effort close
		}
	}()
	if rErr := store.RecordShieldEvent(repoID, cleanCat, gateName, branch, detail); rErr != nil {
		// best-effort telemetry capture
	}
}

// RecordShieldEvent inserts a single shield telemetry row into shield_events (#ShieldSummary).
func (s *Store) RecordShieldEvent(repoID, category, gateName, branch, detail string) error {
	cleanRepo := strings.ToLower(strings.TrimSpace(repoID))
	cleanCat := strings.TrimSpace(category)
	if cleanRepo == "" || cleanCat == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(
		`INSERT INTO shield_events (repo_id, category, gate_name, branch, detail, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		cleanRepo, cleanCat, strings.TrimSpace(gateName), strings.TrimSpace(branch), truncateShieldDetail(detail), now,
	)
	return err
}

func truncateShieldDetail(detail string) string {
	trimmed := strings.TrimSpace(detail)
	if len(trimmed) > 240 {
		return trimmed[:240]
	}
	return trimmed
}

// QueryShieldSummary returns repo-scoped shield catch counters and active Mneme lesson totals (#ShieldSummary).
func QueryShieldSummary(repoRoot string) (*ShieldSummary, error) {
	repoID := resolveShieldRepoID(repoRoot)
	if repoID == "" {
		return &ShieldSummary{}, nil
	}
	store, err := NewStore("")
	if err != nil {
		return nil, err
	}
	defer func() {
		if cErr := store.Close(); cErr != nil {
			// best-effort close
		}
	}()
	return store.QueryShieldSummary(repoID)
}

// QueryShieldSummary aggregates lifetime and 7-day shield counters for repoID (#ShieldSummary).
func (s *Store) QueryShieldSummary(repoID string) (*ShieldSummary, error) {
	cleanRepo := strings.ToLower(strings.TrimSpace(repoID))
	sum := &ShieldSummary{RepoID: cleanRepo}
	if cleanRepo == "" {
		return sum, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff7d := time.Now().UTC().AddDate(0, 0, -7).Format(time.RFC3339Nano)
	if err := s.aggregateShieldEventRows(cleanRepo, cutoff7d, sum); err != nil {
		return nil, err
	}
	if count, err := s.countActiveDistilledLessonsLocked(cleanRepo); err == nil {
		sum.DistilledLessons = count
	}
	return sum, nil
}

func (s *Store) aggregateShieldEventRows(repoID, cutoff7d string, sum *ShieldSummary) error {
	rows, err := s.db.Query(
		`SELECT category, created_at FROM shield_events WHERE repo_id = ? ORDER BY id ASC`,
		repoID,
	)
	if err != nil {
		return err
	}
	defer func() {
		if cErr := rows.Close(); cErr != nil {
			// best-effort close
		}
	}()

	cats := make(map[string]int)
	for rows.Next() {
		var cat, createdAt string
		if sErr := rows.Scan(&cat, &createdAt); sErr != nil {
			return sErr
		}
		sum.LastEventAt = createdAt
		if cat == ShieldCategoryVerifiedCommit {
			sum.VerifiedCommits++
			continue
		}
		sum.ShortcutsBlocked++
		cats[cat]++
		if createdAt >= cutoff7d {
			sum.BlockedLast7Days++
		}
	}
	if len(cats) > 0 {
		sum.CategoryCounts = cats
	}
	return rows.Err()
}

func (s *Store) countActiveDistilledLessonsLocked(repoID string) (int, error) {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM memories WHERE repo_id = ? AND status = ? AND kind IN (?, ?)`,
		repoID, string(StatusActive), string(KindPitfall), string(KindPattern),
	).Scan(&count)
	return count, err
}

// QueryBranchShieldCatches returns shortcuts caught on branch since the last verified_commit event (#ShieldSummary).
func QueryBranchShieldCatches(repoRoot, branch string) int {
	repoID := resolveShieldRepoID(repoRoot)
	cleanBranch := strings.TrimSpace(branch)
	if repoID == "" || cleanBranch == "" {
		return 0
	}
	store, err := NewStore("")
	if err != nil {
		return 0
	}
	defer func() {
		if cErr := store.Close(); cErr != nil {
			// best-effort close
		}
	}()
	count, qErr := store.QueryBranchShieldCatches(repoID, cleanBranch)
	if qErr != nil {
		return 0
	}
	return count
}

// QueryBranchShieldCatches counts non-commit shield events on branch recorded after its latest verified_commit (#ShieldSummary).
func (s *Store) QueryBranchShieldCatches(repoID, branch string) (int, error) {
	cleanRepo := strings.ToLower(strings.TrimSpace(repoID))
	cleanBranch := strings.TrimSpace(branch)
	if cleanRepo == "" || cleanBranch == "" {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var lastCommitID int64
	err := s.db.QueryRow(
		`SELECT COALESCE(MAX(id), 0) FROM shield_events WHERE repo_id = ? AND branch = ? AND category = ?`,
		cleanRepo, cleanBranch, ShieldCategoryVerifiedCommit,
	).Scan(&lastCommitID)
	if err != nil && err != sql.ErrNoRows {
		return 0, err
	}

	var catches int
	err = s.db.QueryRow(
		`SELECT COUNT(*) FROM shield_events WHERE repo_id = ? AND branch = ? AND category != ? AND id > ?`,
		cleanRepo, cleanBranch, ShieldCategoryVerifiedCommit, lastCommitID,
	).Scan(&catches)
	return catches, err
}
