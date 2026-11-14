package intentlint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/config"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/task"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

// TestIntentSteelmanStrictFrontmatterAndNCTE verifies strict Intent frontmatter and NCTE-100 rules.
// @intent intent.md
// @intent go_ssot_and_json_schema_spec.md
// @intent mneme_memory_spec.md
// @intent nomos_controlled_technical_english_spec.md
// @intent nomos_hermetic_worktree_spec.md
// @intent nomos_intent_traceability_spec.md
// @intent software_physics_engine_spec.md
// @intent task_lifecycle_cli_spec.md
// @intent tree_sitter_ast_gate_spec.md
// @verifies TC-NCTE-09 TC-NCTE-10
func TestIntentSteelmanStrictFrontmatterAndNCTE(t *testing.T) {
	cases := []struct {
		name      string
		relPath   string
		content   string
		wantError string
	}{
		{
			name:    "valid_flat_canonical_spec_passes",
			relPath: "architecture/specs/sample_spec.md",
			content: canonicalStrictSpecFixture,
		},
		{
			name:    "valid_root_intent_manifest_passes",
			relPath: "intent.md",
			content: canonicalStrictSpecFixture,
		},
		{
			name:      "rejects_handbook_document_in_open_core",
			relPath:   "architecture/handbook/01-guide.md",
			content:   canonicalStrictSpecFixture,
			wantError: "handbook",
		},
		{
			name:      "rejects_disallowed_frontmatter_trigger_and_tags",
			relPath:   "architecture/specs/extra_keys_spec.md",
			content:   strings.Replace(canonicalStrictSpecFixture, "plane: intent\n", "plane: intent\ntrigger: always_on\ntags:\n  - legacy\n", 1),
			wantError: "disallowed YAML frontmatter key(s)",
		},
		{
			name:      "rejects_non_spec_doc_type_in_strict_mode",
			relPath:   "architecture/specs/wrong_type_spec.md",
			content:   strings.Replace(canonicalStrictSpecFixture, "type: spec", "type: research", 1),
			wantError: "requires 'type: spec'",
		},
		{
			name:      "rejects_deprecated_lineage_and_last_updated_keys",
			relPath:   "architecture/specs/deprecated_lineage_spec.md",
			content:   strings.Replace(canonicalStrictSpecFixture, "plane: intent\n", "last_updated: \"2026-11-12\"\nlineage:\n  - INT-932\nplane: intent\n", 1),
			wantError: "disallowed YAML frontmatter key(s) [last_updated, lineage]",
		},
		{
			name:      "rejects_unicode_em_dash",
			relPath:   "architecture/specs/dash_spec.md",
			content:   canonicalStrictSpecFixture + "\nThis line contains an em-dash \u2014 in prose.\n",
			wantError: "Unicode em-dash or en-dash",
		},
		{
			name:      "rejects_missing_ssot_repository",
			relPath:   "architecture/specs/no_repo_spec.md",
			content:   strings.Replace(canonicalStrictSpecFixture, "ssot_repository: nomos\n", "", 1),
			wantError: "ssot_repository",
		},
		{
			name:      "rejects_deprecated_version_key",
			relPath:   "architecture/specs/deprecated_ver_spec.md",
			content:   strings.Replace(canonicalStrictSpecFixture, "type: spec\n", "type: spec\nversion: \"4.8.0\"\n", 1),
			wantError: "disallowed YAML frontmatter key(s) [version]",
		},
		{
			name:      "rejects_ncte_ambiguous_modal",
			relPath:   "architecture/specs/modal_spec.md",
			content:   canonicalStrictSpecFixture + "\nOperators should configure thresholds carefully.\n",
			wantError: "ERR_NCTE_AMBIGUOUS_MODAL",
		},
	}

	opts := SteelmanOptions{RequireFrontmatter: true, StrictStandard: true}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			full := filepath.Join(root, "intent", filepath.FromSlash(tc.relPath))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.WriteFile(full, []byte(tc.content), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			_, issues := ValidateIntentDocumentsWithOptions(filepath.Join(root, "intent"), opts)
			if tc.wantError == "" && len(issues) > 0 {
				t.Fatalf("expected clean pass, got issues: %v", issues)
			}
			if tc.wantError != "" && !strings.Contains(strings.Join(issues, "; "), tc.wantError) {
				t.Fatalf("expected error containing %q, got: %v", tc.wantError, issues)
			}
		})
	}
}

func TestRepositoryIntentCorpusPassesStrictSteelman(t *testing.T) {
	repoIntentDir := filepath.Join("..", "..", "..", "..", "..", "..", "intent")
	if _, err := os.Stat(repoIntentDir); os.IsNotExist(err) {
		t.Skip("repository intent directory not present at relative path")
	}
	opts := SteelmanOptions{RequireFrontmatter: true, StrictStandard: true}
	count, issues := ValidateIntentDocumentsWithOptions(repoIntentDir, opts)
	if count == 0 || len(issues) > 0 {
		t.Fatalf("expected clean Intent docs (count=%d), got:\n%s", count, strings.Join(issues, "\n"))
	}
}

// @verifies TC-OBL-001
// @verifies TC-OBL-002
func TestVerifyExecutableObligationsAndPolyglotAnchors(t *testing.T) {
	tmp := t.TempDir()
	specDir := filepath.Join(tmp, "intent", "architecture", "specs")
	srcDir := filepath.Join(tmp, "src", "billing")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatalf("mkdir specs: %v", err)
	}
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	specContent := "---\ntitle: Auth Spec\nplane: intent\nstatus: active\n---\n\n```typescript\nexport interface InvoicePayload {\n  id: string;\n  currency: string;\n}\n```\n\n```python\nclass LedgerRow:\n    row_id: str\n```\n\n| Vector ID | Scenario |\n| :--- | :--- |\n| `TC-AUTH-01` | Valid token |\n| `TC-AUTH-99` | Expired token |\n"
	if err := os.WriteFile(filepath.Join(specDir, "auth_spec.md"), []byte(specContent), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "auth_test.go"), []byte("package auth\n// @verifies TC-AUTH-01\nfunc TestValidToken() {}\n"), 0o644); err != nil {
		t.Fatalf("write go test: %v", err)
	}
	if issues := VerifyExecutableObligations(tmp, nil); len(issues) != 1 || !strings.Contains(issues[0], "TC-AUTH-99") {
		t.Fatalf("expected TC-AUTH-99 unbound, got: %v", issues)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "test_auth.py"), []byte("# @verifies TC-AUTH-99\ndef test_expired():\n    assert True\n"), 0o644); err != nil {
		t.Fatalf("write py test: %v", err)
	}
	if clean := VerifyExecutableObligations(tmp, nil); len(clean) != 0 {
		t.Fatalf("expected 0 unbound obligations, got: %v", clean)
	}

	tsSrc := "// @intent auth_spec.md #InvoicePayload #MissingAnchor\nexport interface InvoicePayload {\n  id: string;\n}\nconst k = process.env.BILLING_TOKEN;\n"
	tsPath := filepath.Join(srcDir, "invoice.ts")
	if err := os.WriteFile(tsPath, []byte(tsSrc), 0o644); err != nil {
		t.Fatalf("write ts: %v", err)
	}
	envVars := make(map[string]bool)
	ExtractPolyglotEnvVarsFromFile(tsPath, envVars)
	if !envVars["BILLING_TOKEN"] {
		t.Fatalf("expected BILLING_TOKEN extracted, got %v", envVars)
	}
	sigIssues := VerifySpecTypeSignatures(tmp, "", nil)
	if len(sigIssues) == 0 {
		t.Fatalf("expected polyglot contract drift on currency and LedgerRow")
	}
	unresolved := VerifyIntentASTAnchors(&vscope.VerifyContext{RepoRoot: tmp, WorktreePath: tmp}, tmp, tmp)
	if len(unresolved) == 0 || !strings.Contains(strings.Join(unresolved, ";"), "#MissingAnchor") {
		t.Fatalf("expected #MissingAnchor reported, got %v", unresolved)
	}
	cfg := &config.EcosystemConfig{Project: "scoped"}
	cfg.Governance.QualityGates.Contracts = &config.ContractsBoundaryConfig{
		Contracts: config.ContractsConfig{Enabled: true, SpecRoots: []string{"intent/architecture/specs/auth_spec.md"}},
	}
	docCount, scIssues, skipped := EvaluateConfiguredIntentSteelman(tmp, cfg)
	if skipped || docCount != 1 || len(scIssues) != 0 {
		t.Fatalf("expected scoped steelman pass, got count=%d skipped=%v issues=%v", docCount, skipped, scIssues)
	}
}

func TestSiblingTwinSpecIsolationHelpers(t *testing.T) {
	if got := ExtractWorktreeSubTaskKey("/repo/trunk"); got != "" {
		t.Fatalf("expected empty key for trunk root, got %q", got)
	}
	if got := ExtractWorktreeSubTaskKey("/repo/worktrees/nomos-INT-1951"); got != "" {
		t.Fatalf("expected empty key for INT worktree, got %q", got)
	}
	if got := ExtractWorktreeSubTaskKey("/repo/worktrees/nomos-SUB-2188"); got != "SUB-2188" {
		t.Fatalf("expected SUB-2188 for isolated SUB worktree, got %q", got)
	}

	tmp := t.TempDir()
	specDir := filepath.Join(tmp, "intent", "architecture", "specs")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatalf("mkdir specs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(specDir, "alpha_spec.md"), []byte("# Alpha\n"), 0o644); err != nil {
		t.Fatalf("write alpha: %v", err)
	}
	if err := os.WriteFile(filepath.Join(specDir, "beta_spec.md"), []byte("# Beta\n"), 0o644); err != nil {
		t.Fatalf("write beta: %v", err)
	}

	roots := []string{"intent/architecture/specs"}
	if trunkRoots := ResolveActiveWorktreeSpecRoots(tmp, roots); len(trunkRoots) != 1 || trunkRoots[0] != roots[0] {
		t.Fatalf("expected trunk root to keep original specRoots, got %v", trunkRoots)
	}
	if intRoots := ResolveActiveWorktreeSpecRoots("/repo/worktrees/nomos-INT-1967", roots); intRoots == nil || len(intRoots) != 0 {
		t.Fatalf("expected isolated INT worktree to skip Substrate AST contract parity roots, got %#v", intRoots)
	}
	kept, skipped := FilterDeferredSpecRoots(tmp, roots, map[string]bool{"intent/architecture/specs/beta_spec.md": true})
	if !skipped || len(kept) != 1 || filepath.Base(kept[0]) != "alpha_spec.md" {
		t.Fatalf("expected only alpha_spec.md after deferring beta_spec.md, got kept=%v skipped=%v", kept, skipped)
	}
	unchanged, skippedNone := FilterDeferredSpecRoots(tmp, roots, map[string]bool{"intent/architecture/specs/other.md": true})
	if skippedNone || len(unchanged) != 1 {
		t.Fatalf("expected unchanged roots when deferred path does not match, got %v skipped=%v", unchanged, skippedNone)
	}
	allDeferred, skippedAll := FilterDeferredSpecRoots(tmp, roots, map[string]bool{
		"intent/architecture/specs/alpha_spec.md": true,
		"intent/architecture/specs/beta_spec.md":  true,
	})
	if !skippedAll || allDeferred == nil || len(allDeferred) != 0 {
		t.Fatalf("expected non-nil empty slice when all specs deferred, got %#v skipped=%v", allDeferred, skippedAll)
	}
	if fallback := resolveObligationSpecFiles(tmp, allDeferred); len(fallback) != 0 {
		t.Fatalf("expected empty explicit specPaths slice not to fallback to default roots, got %v", fallback)
	}
	if !isSiblingSubPendingForWorktree(tmp, tmp, task.TaskRecord{Key: "SUB-9", Status: task.StatusTodo}) {
		t.Fatal("expected TODO sibling SUB-* task whose INT-* twin merged to remain deferred")
	}
	if isSiblingSubPendingForWorktree(tmp, tmp, task.TaskRecord{Key: "SUB-9", Status: task.StatusAborted}) {
		t.Fatal("expected ABORTED sibling SUB-* task not to be deferred")
	}
}
