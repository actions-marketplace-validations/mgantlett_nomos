package hookcmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/cli/hookcmd"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/constants"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/mem"
)

func setupBranchGuardTestRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("NOMOS_HOME", t.TempDir())
	t.Setenv("NOMOS_MNEME_DB_PATH", filepath.Join(t.TempDir(), constants.FileMnemeDB))

	repoDir := workspace.InitHermeticGitRepo(t)
	cfgYAML := "project: bg-demo\ndefault_branch: main\ngovernance:\n  boundary:\n    enforce: true\n    isolation_mode: branch\n  quality_gates:\n    strict_mode: false\n"
	if err := os.WriteFile(filepath.Join(repoDir, constants.FileNomosYAML), []byte(cfgYAML), 0644); err != nil {
		t.Fatalf("write nomos.yaml: %v", err)
	}
	return repoDir
}

// @verifies TC-BG-01
func TestBranchGuard_DeniesTrunkEditWithSwitchRemedy(t *testing.T) {
	repoDir := setupBranchGuardTestRepo(t)
	target := filepath.Join(repoDir, "app.go")

	out := executeHookCmd(t, []string{"pre-tool"}, map[string]any{
		"tool_name":  "Write",
		"cwd":        repoDir,
		"tool_input": map[string]any{"file_path": target},
	})
	dec := parseDecision(t, out)
	if dec.Decision != "deny" || !strings.Contains(dec.Reason, "git switch -c feat/<slug>") {
		t.Fatalf("TC-BG-01: expected trunk deny with switch remedy, got %+v", dec)
	}

	redirOut := executeHookCmd(t, []string{"pre-tool"}, map[string]any{
		"tool_name":  "Bash",
		"cwd":        repoDir,
		"tool_input": map[string]any{"command": "echo ok > app.go"},
	})
	redirDec := parseDecision(t, redirOut)
	if redirDec.Decision != "deny" {
		t.Fatalf("TC-BG-01: expected redirect on main to deny, got %+v", redirDec)
	}
}

// @verifies TC-BG-02
// @verifies TC-BG-03
func TestBranchGuard_AllowsFeatureEditAndBlocksConfigTamper(t *testing.T) {
	repoDir := setupBranchGuardTestRepo(t)
	if out, err := workspace.SafeGitCommand(repoDir, "checkout", "-b", "feat/auth").CombinedOutput(); err != nil {
		t.Fatalf("git checkout feat/auth: %v (%s)", err, string(out))
	}

	appFile := filepath.Join(repoDir, "auth.go")
	out := executeHookCmd(t, []string{"pre-tool"}, map[string]any{
		"tool_name":  "Write",
		"cwd":        repoDir,
		"tool_input": map[string]any{"file_path": appFile},
	})
	if dec := parseDecision(t, out); dec.Decision != "allow" {
		t.Fatalf("TC-BG-02: expected feature branch write allow, got %+v", dec)
	}

	for _, protected := range []string{constants.FileNomosYAML, constants.FileNomosBaselineJSON, ".git/info/exclude"} {
		tamperOut := executeHookCmd(t, []string{"pre-tool"}, map[string]any{
			"tool_name":  "Edit",
			"cwd":        repoDir,
			"tool_input": map[string]any{"file_path": filepath.Join(repoDir, protected)},
		})
		if dec := parseDecision(t, tamperOut); dec.Decision != "deny" {
			t.Fatalf("TC-BG-03: expected %s tamper deny, got %+v", protected, dec)
		}
	}
}

// @verifies TC-BG-04
func TestBranchGuard_StopGateBlocksUntilVerifyClearsTrace(t *testing.T) {
	repoDir := setupBranchGuardTestRepo(t)
	if out, err := workspace.SafeGitCommand(repoDir, "checkout", "-b", "feat/cache").CombinedOutput(); err != nil {
		t.Fatalf("git checkout feat/cache: %v (%s)", err, string(out))
	}

	executeHookCmd(t, []string{"post-tool"}, map[string]any{
		"tool_name":  "Write",
		"cwd":        repoDir,
		"tool_input": map[string]any{"file_path": filepath.Join(repoDir, "cache.go")},
	})

	stopRaw := executeHookCmd(t, []string{"stop"}, map[string]any{
		"hook_event_name": "Stop",
		"cwd":             repoDir,
	})
	if dec := parseDecision(t, stopRaw); dec.Decision != "block" {
		t.Fatalf("TC-BG-04: expected Stop hook block for unverified branch edit, got %+v (%s)", dec, stopRaw)
	}

	executeHookCmd(t, []string{"post-tool"}, map[string]any{
		"tool_name":  "Bash",
		"cwd":        repoDir,
		"tool_input": map[string]any{"command": "nomos verify"},
	})
	cleanStop := executeHookCmd(t, []string{"stop"}, map[string]any{
		"hook_event_name": "Stop",
		"cwd":             repoDir,
	})
	if cleanStop != "{}" {
		t.Fatalf("TC-BG-04: expected clean Stop allow {} after verify, got %s", cleanStop)
	}
}

// @verifies TC-BG-07
func TestBranchGuard_PostCommitDistillsTrapToMnemeAndClearsTrace(t *testing.T) {
	repoDir := setupBranchGuardTestRepo(t)
	if out, err := workspace.SafeGitCommand(repoDir, "checkout", "-b", "feat/login").CombinedOutput(); err != nil {
		t.Fatalf("git checkout feat/login: %v (%s)", err, string(out))
	}

	srcFile := filepath.Join(repoDir, "login.go")
	if err := os.WriteFile(srcFile, []byte("package main\n\nfunc login() {}\n"), 0644); err != nil {
		t.Fatalf("write login.go: %v", err)
	}
	if err := workspace.SafeGitCommand(repoDir, "add", "login.go").Run(); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := workspace.SafeGitCommand(repoDir, "commit", "-m", "Add login helper and fix stub check").Run(); err != nil {
		t.Fatalf("git commit: %v", err)
	}

	executeHookCmd(t, []string{"post-tool"}, map[string]any{
		"tool_name":  "Write",
		"cwd":        repoDir,
		"tool_input": map[string]any{"file_path": srcFile},
	})
	hookcmd.MarkWorktreeVerificationFailed(repoDir)

	traps := []mem.GateTrapEntry{{
		GateName: "Anti-Shim Gate (Law 2)",
		Message:  "login.go: stub marker detected",
		Scope:    "root",
	}}
	if err := mem.RecordGateTrap(repoDir, "BRANCH-"+hookcmd.SanitizeBranchSlug("feat/login"), []string{"login.go"}, traps); err != nil {
		t.Fatalf("RecordGateTrap: %v", err)
	}

	hookcmd.RunPostCommitDistillation(repoDir)

	pitfalls := mem.RecallRepoPitfallsReadOnly(repoDir, []string{"login.go"}, 5)
	if len(pitfalls) == 0 {
		t.Fatalf("TC-BG-07: expected post-commit to distill BRANCH-* pitfall into Mneme")
	}
	if tr := hookcmd.LoadHookSessionTrace(hookcmd.BranchTraceKey(repoDir, "feat/login")); tr != nil {
		t.Fatalf("TC-BG-07: expected post-commit to clear branch trace, got %+v", tr)
	}
}
