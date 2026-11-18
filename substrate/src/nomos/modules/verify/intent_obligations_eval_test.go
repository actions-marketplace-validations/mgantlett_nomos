package verify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/intentlint"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

// @verifies TC-OBL-012
// @verifies TC-OBL-013
// @verifies TC-OBL-014
func TestEvaluateContractObligationChecks_BijectiveOrphanAndSurjection(t *testing.T) {
	tmp := seedMinimalContractRepo(t)
	ctx := &vscope.VerifyContext{RepoRoot: tmp, WorktreePath: tmp}
	gate := &IntentTraceabilityGate{}

	resExtra, err := gate.Evaluate(ctx)
	if err != nil || resExtra == nil || resExtra.Passed || !strings.Contains(resExtra.Message, "UndocumentedExtra") {
		t.Fatalf("expected extra AST field UndocumentedExtra to fail IntentTraceabilityGate, got: %+v err=%v", resExtra, err)
	}

	alignedGo := "package sample\n// Item models a contract (@intent item_spec.md #Item).\ntype Item struct {\n\tID string\n}\n"
	if wErr := os.WriteFile(filepath.Join(tmp, "substrate", "pkg", "sample", "item.go"), []byte(alignedGo), 0o644); wErr != nil {
		t.Fatalf("write aligned item.go: %v", wErr)
	}

	resOrphan, err2 := gate.Evaluate(ctx)
	if err2 != nil || resOrphan == nil || resOrphan.Passed || !strings.Contains(resOrphan.Message, "TC-ORPHAN-99") {
		t.Fatalf("expected orphaned @verifies TC-ORPHAN-99 to fail IntentTraceabilityGate, got: %+v err=%v", resOrphan, err2)
	}

	cleanTest := "package sample\nimport \"testing\"\n// @verifies TC-ITM-01\nfunc TestItem(t *testing.T) {}\n"
	if wErr := os.WriteFile(filepath.Join(tmp, "substrate", "pkg", "sample", "item_test.go"), []byte(cleanTest), 0o644); wErr != nil {
		t.Fatalf("write clean item_test.go: %v", wErr)
	}

	resPass, err3 := gate.Evaluate(ctx)
	if err3 != nil || resPass == nil || !resPass.Passed {
		t.Fatalf("expected clean bijective repo to pass IntentTraceabilityGate, got: %+v err=%v", resPass, err3)
	}
}

// @verifies TC-OBL-012
// @verifies TC-OBL-013
// @verifies TC-OBL-014
func TestSelfDogfood_NomosBijectiveAndSurjectiveParity(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Clean("../../../../.."))
	if err != nil {
		t.Fatalf("resolve repoRoot: %v", err)
	}
	if isActiveIntentWorktreeOrBranch(repoRoot) {
		t.Skip("skipping inside active INT-* worktree")
	}
	specRoots := []string{"intent/intent.md", "intent/architecture/specs"}
	if sigIssues := VerifySpecTypeSignatures(repoRoot, filepath.Join(repoRoot, "substrate"), specRoots); len(sigIssues) != 0 {
		t.Fatalf("bijective struct parity drift on nomos: %v", sigIssues)
	}
	if orphans := intentlint.VerifyNoOrphanedTestObligations(repoRoot, specRoots); len(orphans) != 0 {
		t.Fatalf("orphaned @verifies TC-* vectors on nomos: %v", orphans)
	}
	if surj := intentlint.VerifyNormativeRegistrySurjection(repoRoot, specRoots); len(surj) != 0 {
		t.Fatalf("normative registry surjection gaps on nomos: %v", surj)
	}
}

// @verifies TC-OBL-015
func TestCollectInFlightSubSpecDigestsWithTwin_IncludesBoundTwinSpecsWithoutSourceTags(t *testing.T) {
	repo := t.TempDir()
	specFile := filepath.Join(repo, "intent", "architecture", "specs", "portal_spec.md")
	svelteFile := filepath.Join(repo, "substrate", "membrane", "src", "GsiNavbar.svelte")
	if err := os.MkdirAll(filepath.Dir(specFile), 0o755); err != nil {
		t.Fatalf("mkdir spec: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(svelteFile), 0o755); err != nil {
		t.Fatalf("mkdir svelte: %v", err)
	}
	if err := os.WriteFile(specFile, []byte("# Portal Spec\n"), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	if err := os.WriteFile(svelteFile, []byte("<header>Auto Light Dark</header>\n"), 0o644); err != nil {
		t.Fatalf("write svelte: %v", err)
	}
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"add", "."},
		{"commit", "-m", "[INT-2123] docs(specs): specify portal theme mode"},
	} {
		if out, err := workspace.SafeGitCommand(repo, args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v (%s)", args, err, string(out))
		}
	}

	got := collectInFlightSubSpecDigestsWithTwin(nil, repo, "INT-2123", nil)
	if !got["intent/architecture/specs/portal_spec.md"] {
		t.Fatalf("expected bound twin spec portal_spec.md in in-flight digests without @intent tag, got: %+v", got)
	}
}

func seedMinimalContractRepo(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	specDir := filepath.Join(tmp, "intent", "architecture", "specs")
	pkgDir := filepath.Join(tmp, "substrate", "pkg", "sample")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatalf("mkdir specs: %v", err)
	}
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}
	spec := "# Item Spec\n```go\npackage sample\ntype Item struct {\n\tID string\n}\n```\n| ID | Desc |\n|---|---|\n| `TC-ITM-01` | Base |\n"
	if err := os.WriteFile(filepath.Join(specDir, "item_spec.md"), []byte(spec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	goSrc := "package sample\n// Item models a contract (@intent item_spec.md #Item).\ntype Item struct {\n\tID string\n\tUndocumentedExtra int\n}\n"
	if err := os.WriteFile(filepath.Join(pkgDir, "item.go"), []byte(goSrc), 0o644); err != nil {
		t.Fatalf("write item.go: %v", err)
	}
	testSrc := "package sample\nimport \"testing\"\n// @verifies TC-ITM-01\n// @verifies TC-ORPHAN-99\nfunc TestItem(t *testing.T) {}\n"
	if err := os.WriteFile(filepath.Join(pkgDir, "item_test.go"), []byte(testSrc), 0o644); err != nil {
		t.Fatalf("write item_test.go: %v", err)
	}
	yamlCfg := "project: sample\ndefault_branch: main\ngovernance:\n  boundary:\n    enforce: true\n  quality_gates:\n    strict_mode: true\n    contracts:\n      contracts:\n        enabled: true\n        strict_types: true\n        verify_obligations: true\n    thresholds:\n      max_cognitive_debt_score: 45\n      max_cyclomatic_complexity: 15\n      max_file_loc: 400\n      max_func_loc: 60\n      min_comment_density_pct: 10\n      min_test_coverage_pct: 55\n"
	if err := os.WriteFile(filepath.Join(tmp, "nomos.yaml"), []byte(yamlCfg), 0o644); err != nil {
		t.Fatalf("write nomos.yaml: %v", err)
	}
	return tmp
}
