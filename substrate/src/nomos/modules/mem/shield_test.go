package mem

import (
	"os"
	"path/filepath"
	"testing"
)

// @verifies TC-SHIELD-01
// @verifies TC-SHIELD-02
// @verifies TC-SHIELD-03
func TestShieldEvents_RecordSummaryAndBranchWatermark(t *testing.T) {
	homeDir := t.TempDir()
	dbPath := filepath.Join(homeDir, "mneme.db")
	t.Setenv("NOMOS_HOME", homeDir)
	t.Setenv("NOMOS_MNEME_DB_PATH", dbPath)

	repoA := filepath.Join(t.TempDir(), "alpha-app")
	repoB := filepath.Join(t.TempDir(), "beta-app")
	if err := os.MkdirAll(repoA, 0755); err != nil {
		t.Fatalf("mkdir repoA: %v", err)
	}
	if err := os.MkdirAll(repoB, 0755); err != nil {
		t.Fatalf("mkdir repoB: %v", err)
	}

	// Record mixed categories in repoA and an isolated event in repoB.
	RecordShieldEvent(repoA, ShieldCategoryAntiCheat, "AntiShimGate", "feat/login", "skipped test blocked")
	RecordShieldEvent(repoA, ShieldCategorySecurityAirlock, "CredentialAirlock", "feat/login", "denied ~/.ssh read")
	RecordShieldEvent(repoA, ShieldCategoryTrunkGuard, "BranchGuard", "main", "blocked main edit")
	RecordShieldEvent(repoB, ShieldCategoryAntiCheat, "AntiShimGate", "feat/other", "beta catch")

	if got := QueryBranchShieldCatches(repoA, "feat/login"); got != 2 {
		t.Fatalf("TC-SHIELD-03: expected 2 branch catches before commit, got %d", got)
	}

	// Commit advances branch watermark and increments verified commits.
	RecordShieldEvent(repoA, ShieldCategoryVerifiedCommit, "post-commit", "feat/login", "feat: add login")
	if got := QueryBranchShieldCatches(repoA, "feat/login"); got != 0 {
		t.Fatalf("TC-SHIELD-03: expected 0 branch catches after verified_commit, got %d", got)
	}

	// Seed a distilled pitfall in repoA and confirm summary aggregates accurately.
	store, err := NewStore("")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if sErr := store.Save(&Memory{
		RepoID:  "alpha-app",
		Kind:    KindPitfall,
		Topic:   "pitfall:anti_shim:auth",
		Content: "Do not skip auth tests",
		TaskID:  "BRANCH-FEAT-LOGIN",
		Status:  StatusActive,
	}); sErr != nil {
		t.Fatalf("store.Save: %v", sErr)
	}
	if cErr := store.Close(); cErr != nil {
		t.Fatalf("store.Close: %v", cErr)
	}

	sumA, err := QueryShieldSummary(repoA)
	if err != nil {
		t.Fatalf("QueryShieldSummary(repoA): %v", err)
	}
	if sumA.RepoID != "alpha-app" || sumA.ShortcutsBlocked != 3 || sumA.BlockedLast7Days != 3 {
		t.Fatalf("TC-SHIELD-02: unexpected repoA summary counts: %+v", sumA)
	}
	if sumA.VerifiedCommits != 1 || sumA.DistilledLessons != 1 {
		t.Fatalf("TC-SHIELD-02: expected 1 verified commit and 1 distilled lesson, got %+v", sumA)
	}
	if sumA.CategoryCounts[ShieldCategoryAntiCheat] != 1 || sumA.CategoryCounts[ShieldCategorySecurityAirlock] != 1 || sumA.CategoryCounts[ShieldCategoryTrunkGuard] != 1 {
		t.Fatalf("TC-SHIELD-01: unexpected category breakdown: %+v", sumA.CategoryCounts)
	}

	sumB, err := QueryShieldSummary(repoB)
	if err != nil {
		t.Fatalf("QueryShieldSummary(repoB): %v", err)
	}
	if sumB.ShortcutsBlocked != 1 || sumB.VerifiedCommits != 0 {
		t.Fatalf("TC-SHIELD-01: repoB isolation failed: %+v", sumB)
	}
}
