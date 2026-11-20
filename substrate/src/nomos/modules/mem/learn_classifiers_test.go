package mem

import (
	"path/filepath"
	"testing"
	"time"
)

func TestClassifierBuiltInAndPolicyGates(t *testing.T) {
	builtIns := []string{
		"PlaneSeparationGate",
		"Open-Core Boundary Invariant (Charter §4)",
		"Magic String Duplication Gate",
		"Git Subprocess Hygiene Invariant (Law 1/3)",
		"Broken Wire & Link Invariant (Law 4)",
		"Threshold Integrity Gate (Law 2 & 7)",
		"Contract Schema Integrity Invariant",
		"Intent Traceability Validation Gate",
		"Cognitive Complexity Invariant",
	}
	for _, g := range builtIns {
		if !isBuiltInStructuralGateName(g) {
			t.Fatalf("expected %q to be classified as built-in gate", g)
		}
	}
	if !isAstCeilingGateName("Max File LOC Invariant") {
		t.Fatal("expected Max File LOC Invariant to be AST ceiling gate")
	}
	if isBuiltInStructuralGateName("Forbidden Literals Invariant") {
		t.Fatal("expected Forbidden Literals Invariant to remain Tier-B policy gate")
	}
	if !isBuiltInStructuralGateTopic("pitfall:open-core_boundary_invariant_(charter_§4):packaging/npm") {
		t.Fatal("expected open-core boundary pitfall topic to be built-in")
	}
}

func TestClassifierPolyglotEphemeralTestAndBareHeaders(t *testing.T) {
	ephemerals := []string{
		"[Go Toolchain Gate in cli] Trap: --- FAIL: TestHook (0.01s) -> Verified Fix (SUB-1): fixed",
		"[Go Toolchain Gate in packaging/pypi] Trap: Python unittest suite failed: ---------------------------------------------------------------------- -> Verified Fix (SUB-2): fixed",
		"[Go Toolchain Gate in tests] Trap: FAILED tests/test_cli.py::test_snapshot -> Verified Fix (SUB-3): fixed",
		"[Statement Coverage Invariant in root] Trap: Python coverage test execution failed: [seq] seq-01 -> FAIL -> Verified Fix (SUB-4): fixed",
		"[Go Toolchain Gate in src/lib/__tests__] Trap: location: '/tmp/wt/src/lib/__tests__/funnel.test.ts:12:4' -> Verified Fix (SUB-5): fixed",
	}
	for _, c := range ephemerals {
		if !isPolyglotEphemeralTestContent(c) {
			t.Fatalf("expected ephemeral test classification for %q", c)
		}
	}
	bares := []string{
		"[Cognitive Debt Score Invariant in src/lib] Trap: detected 1 CDS/clone violation(s): -> Verified Fix (SUB-6): split",
		"[Go Toolchain Gate in pypi] Trap: ---------------------------------------------------------------------- -> Verified Fix (SUB-7): fixed",
	}
	for _, b := range bares {
		if !isLegacyBarePitfallContent(b) {
			t.Fatalf("expected legacy bare header classification for %q", b)
		}
	}
	valid := "[Cognitive Complexity Invariant in substrate/cli] Trap: substrate/cli/root.go:42 cyclomatic 18 > 15 -> Verified Fix (SUB-8): extracted helper"
	if isPolyglotEphemeralTestContent(valid) || isLegacyBarePitfallContent(valid) {
		t.Fatalf("expected valid structural pitfall to stay durable: %q", valid)
	}
}

func TestAutonomicSweepRetiresBareHeadersAndCooledOnceGuardrails(t *testing.T) {
	t.Setenv("NOMOS_HOME", t.TempDir())
	repo := filepath.Join(t.TempDir(), "nomos")
	store, err := NewStore("")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	now := time.Now().UTC()
	bare := &Memory{
		RepoID: "nomos", Kind: KindPitfall, Topic: "pitfall:cognitive_debt_score_invariant:substrate/cli",
		Content: "[Cognitive Debt Score Invariant in substrate/cli] Trap: detected 1 CDS/clone violation(s): -> Verified Fix (SUB-1): split",
		TaskID: "SUB-1", Status: StatusActive, CreatedAt: now.Add(-2 * time.Hour),
	}
	cooled := &Memory{
		RepoID: "nomos", Kind: KindPitfall, Topic: "pitfall:planeseparationgate:substrate/verify",
		Content: "[PlaneSeparationGate in substrate/verify] Trap: .pyc written -> Verified Fix (SUB-2): -B flag",
		TaskID: "SUB-2", Status: StatusActive, AccessCount: 12, CreatedAt: now.Add(-20 * 24 * time.Hour),
	}
	pyTest := &Memory{
		RepoID: "nomos", Kind: KindPitfall, Topic: "pitfall:go_toolchain_gate:packaging/pypi",
		Content: "[Go Toolchain Gate in packaging/pypi] Trap: Python unittest suite failed: AssertionError -> Verified Fix (SUB-3): fixed",
		TaskID: "SUB-3", Status: StatusActive, CreatedAt: now.Add(-30 * time.Hour),
	}
	for _, rec := range []*Memory{bare, cooled, pyTest} {
		if sErr := store.Save(rec); sErr != nil {
			t.Fatalf("Save: %v", sErr)
		}
	}

	rep := SweepRepoPitfallHygiene(store, repo, "nomos", now)
	if rep.ExpiredTestsTombstoned != 2 {
		t.Fatalf("expected bare + 30h pytest trap tombstoned (2), got %d", rep.ExpiredTestsTombstoned)
	}
	cooledAfter, gErr := store.Get(cooled.ID)
	if gErr != nil || cooledAfter.Status != StatusSuperseded {
		t.Fatalf("expected cooled 1x guardrail retired to superseded, got %+v (err=%v)", cooledAfter, gErr)
	}
}
