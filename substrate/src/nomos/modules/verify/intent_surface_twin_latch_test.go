// Package verify tests polyglot TS/Svelte surface deltas, pre-commit Twin preflight, and anti-evasion latches.
package verify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/config"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/storage"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/task"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

// TestTC_CMT_010_PolyglotWebSurfaceAndIntraDirSplit verifies TS/Svelte surface enforcement and intra-directory split cancellation.
// @verifies TC-CMT-010
func TestTC_CMT_010_PolyglotWebSurfaceAndIntraDirSplit(t *testing.T) {
	t.Setenv("NOMOS_HOME", t.TempDir())
	repo := initSurfaceTestRepo(t)
	tsA := filepath.Join(repo, "ui", "src", "lib", "triage.ts")
	writeSurfaceTestFile(t, tsA, "export function existingHelper(): boolean { return true; }\n")
	runSurfaceGit(t, repo, "add", "-A")
	runSurfaceGit(t, repo, "commit", "-m", "[INT-42] feat(ui): seed web baseline\n\nSeed baseline.\n\n___\n\nTask: INT-42")
	runSurfaceGit(t, repo, "checkout", "-b", "feature/SUB-910")

	// Intra-directory TS split cancels balanced exports cleanly.
	tsB := filepath.Join(repo, "ui", "src", "lib", "triageSplit.ts")
	writeSurfaceTestFile(t, tsA, "function internalOnly(): void {}\n")
	writeSurfaceTestFile(t, tsB, "export function existingHelper(): boolean { return true; }\n")
	if delta := InspectWorktreeNormativeSurface(repo); delta.HasChanges() {
		t.Fatalf("expected intra-directory TS split to cancel out, got %+v", delta)
	}

	// Net-new Svelte component and TS export require Twin: INT-<N>.
	svPath := filepath.Join(repo, "ui", "src", "lib", "GovernorBanner.svelte")
	writeSurfaceTestFile(t, svPath, "<div>Governor</div>\n")
	writeSurfaceTestFile(t, tsB, "export function existingHelper(): boolean { return true; }\nexport const NEW_SCOPE = 'core';\n")
	fixMsg := "[SUB-910] fix(ui): add banner and scope constant\n\nAdd UI banner.\n\n___\n\nTask: SUB-910"
	v := ValidateWorktreeIntentTwin(repo, fixMsg, config.IntentTwinModeFeat)
	if !strings.Contains(v, "svelte_component:ui/src/lib:GovernorBanner") || !strings.Contains(v, "export_ts:ui/src/lib.NEW_SCOPE") {
		t.Fatalf("expected untwinned TS/Svelte expansion rejected, got: %s", v)
	}
}

// TestTC_CMT_010_StickyAntiEvasionLatchAndVerifyPreflight verifies verify preflight warnings and post-rejection export-hiding blocks.
// @verifies TC-CMT-010
func TestTC_CMT_010_StickyAntiEvasionLatchAndVerifyPreflight(t *testing.T) {
	t.Setenv("NOMOS_HOME", t.TempDir())
	repo := initSurfaceTestRepo(t)
	writeSurfaceTestFile(t, filepath.Join(repo, "nomos.yaml"), "project: demo\ndefault_branch: main\ngovernance:\n  quality_gates:\n    intent_twin_mode: feat\n")
	goFile := filepath.Join(repo, "substrate", "mem", "classifiers.go")
	writeSurfaceTestFile(t, goFile, "package mem\n\nfunc baseCheck() bool { return true }\n")
	runSurfaceGit(t, repo, "add", "-A")
	runSurfaceGit(t, repo, "commit", "-m", "[INT-42] feat(mem): seed baseline\n\nSeed baseline.\n\n___\n\nTask: INT-42")
	runSurfaceGit(t, repo, "checkout", "-b", "feature/SUB-911")

	// 1. CommitSchemaGate fails closed during uncommitted nomos verify and arms sticky latch immediately.
	writeSurfaceTestFile(t, goFile, "package mem\n\nfunc IsBuiltInGate() bool { return true }\n")
	gateRes, gErr := (&CommitSchemaGate{}).Evaluate(&vscope.VerifyContext{RepoRoot: repo, WorktreePath: repo, TaskID: "SUB-911"})
	if gErr != nil || gateRes == nil || gateRes.Passed || !strings.Contains(gateRes.Message, "IsBuiltInGate") || !strings.Contains(gateRes.Remedy, "--twin-of SUB-911") {
		t.Fatalf("expected CommitSchemaGate to fail closed on uncommitted surface delta, got res=%+v err=%v", gateRes, gErr)
	}

	// 2. Lowercasing IsBuiltInGate -> isBuiltInGate right after nomos verify STILL fails closed via armed latch!
	writeSurfaceTestFile(t, goFile, "package mem\n\nfunc isBuiltInGate() bool { return true }\n")
	evadeRes, evErr := (&CommitSchemaGate{}).Evaluate(&vscope.VerifyContext{RepoRoot: repo, WorktreePath: repo, TaskID: "SUB-911"})
	if evErr != nil || evadeRes == nil || evadeRes.Passed || !strings.Contains(evadeRes.Message, "previously triggered normative surface expansion") {
		t.Fatalf("expected CommitSchemaGate to block post-verify symbol hiding, got %+v", evadeRes)
	}
	fixMsg := "[SUB-911] fix(mem): add gate classifier\n\nAdd classifier.\n\n___\n\nTask: SUB-911"
	vEvade := ValidateWorktreeIntentTwin(repo, fixMsg, config.IntentTwinModeFeat)
	if !strings.Contains(vEvade, "previously triggered normative surface expansion") {
		t.Fatalf("expected sticky anti-evasion latch to block symbol hiding on commit, got: %s", vEvade)
	}

	// 3. Binding a valid ledger twin passes CommitSchemaGate, and supplying Twin: INT-42 clears the latch.
	bindLedgerPairForTest(t, "INT-90", "SUB-911")
	afterBindRes, bErr := (&CommitSchemaGate{}).Evaluate(&vscope.VerifyContext{RepoRoot: repo, WorktreePath: repo, TaskID: "SUB-911"})
	if bErr != nil || afterBindRes == nil || !afterBindRes.Passed {
		t.Fatalf("expected CommitSchemaGate to pass after ledger BindTwin, got res=%+v err=%v", afterBindRes, bErr)
	}
	if vPass := ValidateWorktreeIntentTwin(repo, fixMsg+"\nTwin: INT-42", config.IntentTwinModeFeat); vPass != "" {
		t.Fatalf("expected valid Twin: INT-42 to pass and clear latch, got: %s", vPass)
	}
	if _, statErr := os.Stat(twinLatchFilePath(repo, "SUB-911")); !os.IsNotExist(statErr) {
		t.Fatalf("expected latch file cleared after valid Twin commit, statErr=%v", statErr)
	}
}

// TestTC_CMT_011_GoConstAndSQLMutationFailClosedAndSplitCancel verifies private const/SQL surface checks and split cancellation.
// @verifies TC-CMT-011
func TestTC_CMT_011_GoConstAndSQLMutationFailClosedAndSplitCancel(t *testing.T) {
	t.Setenv("NOMOS_HOME", t.TempDir())
	repo := initSurfaceTestRepo(t)
	writeSurfaceTestFile(t, filepath.Join(repo, "nomos.yaml"), "project: demo\ndefault_branch: main\ngovernance:\n  quality_gates:\n    intent_twin_mode: feat\n")
	fileA := filepath.Join(repo, "substrate", "mem", "hygiene_a.go")
	fileB := filepath.Join(repo, "substrate", "mem", "hygiene_b.go")
	writeSurfaceTestFile(t, fileA, "package mem\nconst testTTLHours = 72\nfunc runSQL() string { return \"UPDATE memories SET status = 'tombstoned'\" }\n")
	writeSurfaceTestFile(t, fileB, "package mem\n")
	runSurfaceGit(t, repo, "add", "-A")
	runSurfaceGit(t, repo, "commit", "-m", "[INT-42] feat(mem): seed const and sql\n\nSeed baseline.\n\n___\n\nTask: INT-42")
	runSurfaceGit(t, repo, "checkout", "-b", "feature/SUB-912")

	// Intra-package split of private const and SQL string cancels out cleanly.
	writeSurfaceTestFile(t, fileA, "package mem\n")
	writeSurfaceTestFile(t, fileB, "package mem\nconst testTTLHours = 72\nfunc runSQL() string { return \"UPDATE memories SET status = 'tombstoned'\" }\n")
	if res, err := (&CommitSchemaGate{}).Evaluate(&vscope.VerifyContext{RepoRoot: repo, WorktreePath: repo, TaskID: "SUB-912"}); err != nil || !res.Passed {
		t.Fatalf("expected intra-package const/SQL split to pass untwinned, got res=%+v err=%v", res, err)
	}

	// Changing unexported TTL const or adding new SQL UPDATE fails CommitSchemaGate closed on Turn 1!
	writeSurfaceTestFile(t, fileB, "package mem\nconst testTTLHours = 24\nfunc runSQL() string { return \"UPDATE memories SET status = 'superseded'\" }\n")
	resFail, fErr := (&CommitSchemaGate{}).Evaluate(&vscope.VerifyContext{RepoRoot: repo, WorktreePath: repo, TaskID: "SUB-912"})
	if fErr != nil || resFail == nil || resFail.Passed || !strings.Contains(resFail.Message, "gov_const:") || !strings.Contains(resFail.Message, "sql_dml:") {
		t.Fatalf("expected Turn-1 private const + SQL DML mutation to fail CommitSchemaGate closed, got res=%+v err=%v", resFail, fErr)
	}
}

// bindLedgerPairForTest creates and links an INT and SUB task pair in the temporary SQLite ledger.
func bindLedgerPairForTest(t *testing.T, intKey, subKey string) {
	t.Helper()
	db, err := storage.OpenDB(config.ResolveGlobalDBPath())
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() {
		if cErr := db.Close(); cErr != nil {
			t.Errorf("Close: %v", cErr)
		}
	}()
	store := task.NewSQLiteTaskStore(db)
	desc := "## Scope & Context\nUpdate intent/architecture/specs/nomos_hermetic_worktree_spec.md.\n\n## Acceptance Criteria\n- [ ] Update spec\n\n## Verification\n- nomos verify"
	for _, rec := range []task.TaskRecord{
		{Key: intKey, Title: "Author intent specification", Description: desc, Plane: "intent", SchemaType: task.SchemaIntent, Tier: task.Tier1, Status: task.StatusInProgress, Priority: 50, Burden: 1},
		{Key: subKey, Title: "Implement substrate gate", Description: desc, Plane: "substrate", SchemaType: task.SchemaSubstrate, Tier: task.Tier1, Status: task.StatusInProgress, Priority: 50, Burden: 1},
	} {
		if cErr := store.CreateTask(rec); cErr != nil {
			t.Fatalf("CreateTask %s: %v", rec.Key, cErr)
		}
	}
	if bErr := store.BindTwin(intKey, subKey); bErr != nil {
		t.Fatalf("BindTwin: %v", bErr)
	}
}
