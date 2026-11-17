package verify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/config"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/storage"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/task"
)

func initSurfaceTestRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runSurfaceGit(t, repo, "init", "-b", "main")
	runSurfaceGit(t, repo, "config", "user.name", "Nomos Test")
	runSurfaceGit(t, repo, "config", "user.email", "test@sophialabs.ca")

	cliFile := filepath.Join(repo, "substrate", "cli", "cmd_demo.go")
	if err := os.MkdirAll(filepath.Dir(cliFile), 0755); err != nil {
		t.Fatal(err)
	}
	baseSrc := `package cli

import "github.com/spf13/cobra"

type DemoConfig struct {
	Model string ` + "`yaml:\"model\"`" + `
}

func NewDemoCmd() *cobra.Command {
	var verbose bool
	cmd := &cobra.Command{Use: "demo"}
	cmd.Flags().BoolVar(&verbose, "verbose", false, "enable verbose output")
	return cmd
}
`
	if err := os.WriteFile(cliFile, []byte(baseSrc), 0644); err != nil {
		t.Fatal(err)
	}
	tplFile := filepath.Join(repo, "substrate", "cli", "templates", "hooks", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(tplFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tplFile, []byte(`{"version":1}`), 0644); err != nil {
		t.Fatal(err)
	}
	nomosTpl := filepath.Join(repo, "substrate", "cli", "templates", "nomos.yaml")
	if err := os.WriteFile(nomosTpl, []byte("# yaml-language-server: $schema=https://raw.githubusercontent.com/mgantlett/nomos/v4.14.0/nomos.schema.json\nproject: demo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runSurfaceGit(t, repo, "add", "-A")
	runSurfaceGit(t, repo, "commit", "-m", "[INT-42] docs(specs): seed demo baseline\n\nSeed baseline spec and command.\n\n___\n\nTask: INT-42")
	return repo
}

func runSurfaceGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	out, err := workspace.SafeGitCommand(dir, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}

// @verifies TC-CMT-006 TC-CMT-007 TC-CMT-008
func TestValidateWorktreeIntentTwin_SurfaceDeltaAndProvenance(t *testing.T) {
	t.Setenv("NOMOS_HOME", t.TempDir())
	repo := initSurfaceTestRepo(t)
	runSurfaceGit(t, repo, "checkout", "-b", "feature/SUB-900")

	cliFile := filepath.Join(repo, "substrate", "cli", "cmd_demo.go")

	// 1. TC-CMT-007: Internal helper refactor with zero CLI/YAML/template surface delta passes untwinned under feat mode.
	internalRefactorSrc := `package cli

import "github.com/spf13/cobra"

type DemoConfig struct {
	Model string ` + "`yaml:\"model\"`" + `
}

func normalizeLabel(s string) string { return s }

func NewDemoCmd() *cobra.Command {
	var verbose bool
	cmd := &cobra.Command{Use: "demo", Short: normalizeLabel("run demo")}
	cmd.Flags().BoolVar(&verbose, "verbose", false, "enable verbose output")
	return cmd
}
`
	if err := os.WriteFile(cliFile, []byte(internalRefactorSrc), 0644); err != nil {
		t.Fatal(err)
	}
	refactorUntwinned := "[SUB-900] refactor(cli): extract internal label normalizer\n\nExtract private helper without changing CLI flags or YAML config contracts.\n\n___\n\nTask: SUB-900"
	if v := ValidateWorktreeIntentTwin(repo, refactorUntwinned, config.IntentTwinModeFeat); v != "" {
		t.Fatalf("TC-CMT-007: expected internal refactor with empty NormativeSurfaceDelta to pass untwinned, got: %s", v)
	}

	// 2. TC-CMT-006: Refactor adding a new CLI flag and YAML field fails closed when untwinned.
	surfaceExpandSrc := `package cli

import "github.com/spf13/cobra"

type DemoConfig struct {
	Model          string ` + "`yaml:\"model\"`" + `
	ThinkingBudget int    ` + "`yaml:\"thinking_budget,omitempty\"`" + `
}

func NewDemoCmd() *cobra.Command {
	var verbose, antigravity bool
	cmd := &cobra.Command{Use: "demo"}
	cmd.Flags().BoolVar(&verbose, "verbose", false, "enable verbose output")
	cmd.Flags().BoolVar(&antigravity, "antigravity", false, "enable Antigravity 2.0 hook lifecycle")
	return cmd
}
`
	if err := os.WriteFile(cliFile, []byte(surfaceExpandSrc), 0644); err != nil {
		t.Fatal(err)
	}
	delta := InspectWorktreeNormativeSurface(repo)
	if !delta.HasChanges() || len(delta.CLISurface) == 0 || len(delta.ConfigSchema) == 0 {
		t.Fatalf("TC-CMT-006: expected CLI and YAML surface delta, got: %+v", delta)
	}
	vUntwinned := ValidateWorktreeIntentTwin(repo, refactorUntwinned, config.IntentTwinModeFeat)
	if !strings.Contains(vUntwinned, "modifies normative contract surface") || !strings.Contains(vUntwinned, "--antigravity") || !strings.Contains(vUntwinned, "DemoConfig.thinking_budget") {
		t.Fatalf("TC-CMT-006: expected normative surface violation naming --antigravity and DemoConfig.thinking_budget, got: %s", vUntwinned)
	}

	// 3. TC-CMT-006 pass when bound to real trunk commit [INT-42].
	refactorTwinnedGit := refactorUntwinned + "\nTwin: INT-42"
	if v := ValidateWorktreeIntentTwin(repo, refactorTwinnedGit, config.IntentTwinModeFeat); v != "" {
		t.Fatalf("TC-CMT-006: expected surface-expanding refactor twinned with trunk INT-42 to pass, got: %s", v)
	}

	// 4. TC-CMT-008: Unresolvable Twin: INT-999999 rejected when absent from Git history and Living Ledger.
	refactorBogusTwin := refactorUntwinned + "\nTwin: INT-999999"
	vBogus := ValidateWorktreeIntentTwin(repo, refactorBogusTwin, config.IntentTwinModeFeat)
	if !strings.Contains(vBogus, "unresolvable 'Twin: INT-999999'") {
		t.Fatalf("TC-CMT-008: expected unresolvable Twin error for INT-999999, got: %s", vBogus)
	}

	// 5. TC-CMT-008 pass when Twin: INT-55 is registered in the SQLite Living Ledger.
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
	if err := store.CreateTask(task.TaskRecord{
		Key:         "INT-55",
		Title:       "Author normative hook specification",
		Description: "## Scope & Context\nSpec update targeting intent/architecture/specs/nomos_hermetic_worktree_spec.md.\n\n## Acceptance Criteria\n- [ ] Update spec\n\n## Verification\n- nomos verify",
		Plane:       "intent",
		SchemaType:  task.SchemaIntent,
		Tier:        task.Tier1,
		Status:      task.StatusInProgress,
		Priority:    50,
		Burden:      1,
	}); err != nil {
		t.Fatalf("CreateTask INT-55: %v", err)
	}
	refactorLedgerTwin := refactorUntwinned + "\nTwin: INT-55"
	if v := ValidateWorktreeIntentTwin(repo, refactorLedgerTwin, config.IntentTwinModeFeat); v != "" {
		t.Fatalf("TC-CMT-008: expected Twin: INT-55 backed by Living Ledger to pass, got: %s", v)
	}
}

func TestInspectWorktreeNormativeSurface_TemplateSchemaVersionPin(t *testing.T) {
	repo := initSurfaceTestRepo(t)
	runSurfaceGit(t, repo, "checkout", "-b", "feature/SUB-102")
	nomosTpl := filepath.Join(repo, "substrate", "cli", "templates", "nomos.yaml")

	if err := os.WriteFile(nomosTpl, []byte("# yaml-language-server: $schema=https://raw.githubusercontent.com/mgantlett/nomos/v4.15.0/nomos.schema.json\nproject: demo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if delta := InspectWorktreeNormativeSurface(repo); delta.HasChanges() {
		t.Fatalf("expected statutory schema SemVer pin bump to produce zero normative delta, got: %+v", delta)
	}
	releaseCommit := "[SUB-102] chore(release): prepare v4.15.0 milestone release\n\nAlign statutory version pins.\n\n___\n\nTask: SUB-102"
	if v := ValidateWorktreeIntentTwin(repo, releaseCommit, config.IntentTwinModeFeat); v != "" {
		t.Fatalf("expected chore(release) schema SemVer pin bump to pass without Twin, got: %s", v)
	}

	if err := os.WriteFile(nomosTpl, []byte("# yaml-language-server: $schema=https://raw.githubusercontent.com/mgantlett/nomos/v4.15.0/nomos.schema.json\nproject: mutated\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if v := ValidateWorktreeIntentTwin(repo, releaseCommit, config.IntentTwinModeFeat); !strings.Contains(v, "template_assets[nomos.yaml]") {
		t.Fatalf("expected body mutation in templates/nomos.yaml to require Twin, got: %s", v)
	}
}

func writeSurfaceTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// @verifies TC-CMT-009
func TestValidateWorktreeIntentTwin_HardenedGovernanceSurface(t *testing.T) {
	t.Run("ExportedAstSensorAndUnexportedRuleAndJSONTagFailUntwinned", func(t *testing.T) {
		repo := initSurfaceTestRepo(t)
		astFile := filepath.Join(repo, "substrate", "src", "nomos", "modules", "verify", "astdebt", "gates_extra.go")
		hookFile := filepath.Join(repo, "substrate", "cli", "hookcmd", "hook_extra.go")
		writeSurfaceTestFile(t, astFile, "package astdebt\n\nfunc ExistingCheck() {}\n")
		writeSurfaceTestFile(t, hookFile, "package hookcmd\n\ntype HookSessionTrace struct {\n\tWorktreeRoot string `json:\"worktree_root\"`\n}\n")
		runSurfaceGit(t, repo, "add", "-A")
		runSurfaceGit(t, repo, "commit", "-m", "[INT-43] feat(verify): baseline gov surface\n\nSeed verify and hookcmd baseline.\n\n___\n\nTask: INT-43")
		runSurfaceGit(t, repo, "checkout", "-b", "feature/SUB-901")

		writeSurfaceTestFile(t, astFile, "package astdebt\n\nfunc ExistingCheck() {}\n\nfunc DetectDesignSlop() []string { return nil }\n")
		refactorMsg := "[SUB-901] refactor(verify): add exported ast sensor\n\nAdd AST sensor.\n\n___\n\nTask: SUB-901"
		vAst := ValidateWorktreeIntentTwin(repo, refactorMsg, config.IntentTwinModeFeat)
		if !strings.Contains(vAst, "export_fn:") || !strings.Contains(vAst, "DetectDesignSlop") {
			t.Fatalf("TC-CMT-009: expected untwinned refactor adding exported func to fail, got: %s", vAst)
		}
		if v := ValidateWorktreeIntentTwin(repo, refactorMsg+"\nTwin: INT-42", config.IntentTwinModeFeat); v != "" {
			t.Fatalf("TC-CMT-009: expected twinned commit to pass, got: %s", v)
		}

		writeSurfaceTestFile(t, astFile, "package astdebt\n\nfunc ExistingCheck() {}\n")
		writeSurfaceTestFile(t, hookFile, "package hookcmd\n\ntype HookSessionTrace struct {\n\tWorktreeRoot    string   `json:\"worktree_root\"`\n\tObservedDigests []string `json:\"observed_digests,omitempty\"`\n}\n\nfunc formatRule() string { return \"%s:%d: shell write blocked (Law 7)\" }\n")
		fixMsg := "[SUB-901] fix(hookcmd): expand trace schema and private rule\n\nExpand trace schema.\n\n___\n\nTask: SUB-901"
		vHook := ValidateWorktreeIntentTwin(repo, fixMsg, config.IntentTwinModeFeat)
		if !strings.Contains(vHook, "json_field:") || !strings.Contains(vHook, "gov_rule:") {
			t.Fatalf("TC-CMT-009: expected untwinned fix adding json_field and gov_rule to fail, got: %s", vHook)
		}
		if v := ValidateWorktreeIntentTwin(repo, fixMsg+"\nTwin: INT-42", config.IntentTwinModeFeat); v != "" {
			t.Fatalf("TC-CMT-009: expected twinned fix to pass, got: %s", v)
		}
	})

	t.Run("IntraPackageSplitAndVersionValueBumpExempt", func(t *testing.T) {
		repo := initSurfaceTestRepo(t)
		fileA := filepath.Join(repo, "substrate", "src", "nomos", "modules", "verify", "astdebt", "split_a.go")
		fileB := filepath.Join(repo, "substrate", "src", "nomos", "modules", "verify", "astdebt", "split_b.go")
		rootFile := filepath.Join(repo, "substrate", "cli", "root.go")
		writeSurfaceTestFile(t, fileA, "package astdebt\n\n// CheckOne validates input (@intent tree_sitter_ast_gate_spec.md).\nfunc CheckOne() bool { return true }\n")
		writeSurfaceTestFile(t, fileB, "package astdebt\n")
		writeSurfaceTestFile(t, rootFile, "package cli\n\nvar RootCmdVersion = \"4.23.0\"\n")
		runSurfaceGit(t, repo, "add", "-A")
		runSurfaceGit(t, repo, "commit", "-m", "[INT-44] feat(cli): baseline split and version\n\nSeed baseline.\n\n___\n\nTask: INT-44")
		runSurfaceGit(t, repo, "checkout", "-b", "feature/SUB-902")

		writeSurfaceTestFile(t, fileA, "package astdebt\n")
		writeSurfaceTestFile(t, fileB, "package astdebt\n\n// CheckOne validates input (@intent tree_sitter_ast_gate_spec.md).\nfunc CheckOne() bool { return true }\n")
		writeSurfaceTestFile(t, rootFile, "package cli\n\nvar RootCmdVersion = \"4.24.0\"\n")
		delta := InspectWorktreeNormativeSurface(repo)
		if delta.HasChanges() {
			t.Fatalf("TC-CMT-009: expected intra-package split and version bump to have empty delta, got: %+v", delta)
		}
		choreMsg := "[SUB-902] refactor(verify): split file intra-package and bump release version\n\nKeep symbols unchanged.\n\n___\n\nTask: SUB-902"
		if v := ValidateWorktreeIntentTwin(repo, choreMsg, config.IntentTwinModeFeat); v != "" {
			t.Fatalf("TC-CMT-009: expected intra-package split + version bump to pass untwinned, got: %s", v)
		}
	})
}
