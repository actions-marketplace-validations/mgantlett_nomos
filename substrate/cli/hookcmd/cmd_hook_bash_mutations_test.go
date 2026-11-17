package hookcmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/cli/hookcmd"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/types"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/handshake"
)

// @verifies TC-BG-08
func TestBranchGuard_BashShellMutationPostToolAndStopRearm(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	repoDir := setupBranchGuardTestRepo(t)
	if out, err := workspace.SafeGitCommand(repoDir, "checkout", "-b", "feat/scb").CombinedOutput(); err != nil {
		t.Fatalf("git checkout feat/scb: %v (%s)", err, string(out))
	}

	workerPath := filepath.Join(repoDir, "worker.py")
	badPy := "def run():\n    try:\n        return work()\n    except Exception:\n        return {}  # pragma: no cover\n"
	if err := os.WriteFile(workerPath, []byte(badPy), 0644); err != nil {
		t.Fatal(err)
	}

	postOut := executeHookCmd(t, []string{"post-tool", "--container-sandbox"}, map[string]any{
		"session_id":      "scb-bash-1",
		"hook_event_name": "PostToolUse",
		"cwd":             repoDir,
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "cat > worker.py << 'EOF'\n" + badPy + "EOF"},
	})
	if !strings.Contains(postOut, "Nomos Radar Breach") || !strings.Contains(postOut, "coverage_exclusion_reward_hack") {
		t.Fatalf("expected PostToolUse AST radar breach on Bash write, got %s", postOut)
	}

	stopBlocked := executeHookCmd(t, []string{"stop", "--container-sandbox"}, map[string]any{
		"hook_event_name": "Stop",
		"cwd":             repoDir,
	})
	if dec := parseDecision(t, stopBlocked); dec.Decision != "block" {
		t.Fatalf("expected container Stop block for unverified Bash write, got %+v (%s)", dec, stopBlocked)
	}

	cleanPy := "def run():\n    return 42\n"
	if err := os.WriteFile(workerPath, []byte(cleanPy), 0644); err != nil {
		t.Fatal(err)
	}
	executeHookCmd(t, []string{"post-tool", "--container-sandbox"}, map[string]any{
		"session_id":      "scb-bash-1",
		"hook_event_name": "PostToolUse",
		"cwd":             repoDir,
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "nomos verify"},
	})
	executeHookCmd(t, []string{"post-tool", "--container-sandbox"}, map[string]any{
		"session_id":      "scb-bash-1",
		"hook_event_name": "PostToolUse",
		"cwd":             repoDir,
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "python3 worker.py"},
	})
	stopAllowed := executeHookCmd(t, []string{"stop", "--container-sandbox"}, map[string]any{
		"hook_event_name": "Stop",
		"cwd":             repoDir,
	})
	if stopAllowed != "{}" {
		t.Fatalf("expected clean Stop '{}' after verify, got %s", stopAllowed)
	}

	if err := os.WriteFile(workerPath, []byte("def run():\n    return 99\n"), 0644); err != nil {
		t.Fatal(err)
	}
	reArmedStop := executeHookCmd(t, []string{"stop", "--container-sandbox"}, map[string]any{
		"hook_event_name": "Stop",
		"cwd":             repoDir,
	})
	if dec := parseDecision(t, reArmedStop); dec.Decision != "block" {
		t.Fatalf("expected Stop re-armed after post-verify Bash edit, got %+v (%s)", dec, reArmedStop)
	}
}

// @verifies TC-BG-08
func TestPlainHandshakeBannerWithRoot_ManifestAndWorkspaceHull(t *testing.T) {
	t.Setenv("NOMOS_HOME", t.TempDir())
	t.Setenv("NOMOS_NO_UPDATE_CHECK", "1")
	repoDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoDir, "pyproject.toml"), []byte("[project]\ndependencies = [\"pyyaml>=6.0\", \"click\"]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "etl.py"), []byte("def tokenize(s):\n    return []\n\ndef parse_expr(toks):\n    return toks\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ev := &handshake.HandshakeEvent{Repository: "scb-etl", Phase: types.PhaseEdit, ActiveTask: "BRANCH-FEAT", GateCount: 42, StrictMode: true}
	banner := hookcmd.FormatPlainHandshakeBannerWithRoot(ev, repoDir, false, "4.24.0")
	for _, want := range []string{"Manifest Deps", "pyyaml", "click", "Workspace Modules", "etl.py (tokenize, parse_expr)"} {
		if !strings.Contains(banner, want) {
			t.Fatalf("expected %q in handshake banner:\n%s", want, banner)
		}
	}
}

// @verifies TC-BG-08
func TestContainerStop_PreToolFailingBashWriteMultiStrikeAndPostVerifyRead(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	repoDir := setupBranchGuardTestRepo(t)
	if out, err := workspace.SafeGitCommand(repoDir, "checkout", "-b", "feat/scb").CombinedOutput(); err != nil {
		t.Fatalf("git checkout feat/scb: %v (%s)", err, string(out))
	}

	executeHookCmd(t, []string{"pre-tool", "--container-sandbox"}, map[string]any{
		"session_id":      "scb-fail-write",
		"hook_event_name": "PreToolUse",
		"cwd":             repoDir,
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "cat > etl.py && python etl.py"},
	})
	badPy := "def route(op):\n    if op == 'a': return 1\n    if op == 'b': return 2\n    if op == 'c': return 3\n    return 0\n"
	if err := os.WriteFile(filepath.Join(repoDir, "etl.py"), []byte(badPy), 0644); err != nil {
		t.Fatal(err)
	}

	stop1 := executeHookCmd(t, []string{"stop", "--container-sandbox"}, map[string]any{
		"hook_event_name": "Stop",
		"cwd":             repoDir,
	})
	if dec1 := parseDecision(t, stop1); dec1.Decision != "block" || !strings.Contains(dec1.Reason, "attempt 1/4") || !strings.Contains(dec1.Reason, "Nomos Radar Breach") {
		t.Fatalf("expected strike 1 block with inline radar breach, got %+v (%s)", dec1, stop1)
	}

	for attempt := 2; attempt <= 4; attempt++ {
		stopRetry := executeHookCmd(t, []string{"stop", "--container-sandbox"}, map[string]any{
			"hook_event_name":  "Stop",
			"cwd":              repoDir,
			"stop_hook_active": true,
		})
		if dec := parseDecision(t, stopRetry); dec.Decision != "block" {
			t.Fatalf("expected strike %d/4 to stay blocked under container sandbox, got %+v", attempt, dec)
		}
	}
	stopCap := executeHookCmd(t, []string{"stop", "--container-sandbox"}, map[string]any{
		"hook_event_name":  "Stop",
		"cwd":              repoDir,
		"stop_hook_active": true,
	})
	if stopCap != "{}" {
		t.Fatalf("expected container stop cap after 4 strikes to allow {}, got %s", stopCap)
	}

	if err := os.WriteFile(filepath.Join(repoDir, "etl.py"), []byte("def route(op):\n    return 0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	hookcmd.MarkWorktreeVerificationPassed(repoDir)
	executeHookCmd(t, []string{"post-tool", "--container-sandbox"}, map[string]any{
		"session_id":      "scb-fail-write",
		"hook_event_name": "PostToolUse",
		"cwd":             repoDir,
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "python3 etl.py"},
	})
	executeHookCmd(t, []string{"post-tool", "--container-sandbox"}, map[string]any{
		"session_id":      "scb-fail-write",
		"hook_event_name": "PostToolUse",
		"cwd":             repoDir,
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "git status --short"},
	})
	cleanStop := executeHookCmd(t, []string{"stop", "--container-sandbox"}, map[string]any{
		"hook_event_name": "Stop",
		"cwd":             repoDir,
	})
	if cleanStop != "{}" {
		t.Fatalf("expected read-only Bash after nomos verify pass to allow Stop {}, got %s", cleanStop)
	}
}

// @verifies TC-BG-09
func TestBehavioralStopGate_AndTestlessHandshakeGuidance(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("NOMOS_NO_UPDATE_CHECK", "1")
	repoDir := setupBranchGuardTestRepo(t)
	if out, err := workspace.SafeGitCommand(repoDir, "checkout", "-b", "feat/scb").CombinedOutput(); err != nil {
		t.Fatalf("git checkout feat/scb: %v (%s)", err, string(out))
	}

	etlPath := filepath.Join(repoDir, "etl.py")
	if err := os.WriteFile(etlPath, []byte("def run():\n    return 7\n"), 0644); err != nil {
		t.Fatal(err)
	}

	ev := &handshake.HandshakeEvent{Repository: "scb-etl", Phase: types.PhaseEdit, ActiveTask: "BRANCH-FEAT", GateCount: 42, StrictMode: false}
	testlessBanner := hookcmd.FormatPlainHandshakeBannerWithRoot(ev, repoDir, false, "4.24.0")
	if !strings.Contains(testlessBanner, "Regression Suite  : none detected") || !strings.Contains(testlessBanner, ".tmp/tests/test_<module>.py") {
		t.Fatalf("expected testless Regression Suite hull guidance, got:\n%s", testlessBanner)
	}

	executeHookCmd(t, []string{"post-tool", "--container-sandbox"}, map[string]any{
		"session_id":      "bg09",
		"hook_event_name": "PostToolUse",
		"cwd":             repoDir,
		"tool_name":       "Write",
		"tool_input":      map[string]any{"file_path": etlPath},
	})
	executeHookCmd(t, []string{"post-tool", "--container-sandbox"}, map[string]any{
		"session_id":      "bg09",
		"hook_event_name": "PostToolUse",
		"cwd":             repoDir,
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "nomos verify"},
		"tool_response":   map[string]any{"text": "All Laws Enforced"},
	})

	blockedStop := executeHookCmd(t, []string{"stop", "--container-sandbox"}, map[string]any{
		"hook_event_name": "Stop",
		"cwd":             repoDir,
	})
	if dec := parseDecision(t, blockedStop); dec.Decision != "block" || !strings.Contains(dec.Reason, "zero automated test suite") {
		t.Fatalf("expected DirtyBehavioralEdits Stop block after testless nomos verify, got %+v (%s)", dec, blockedStop)
	}

	executeHookCmd(t, []string{"post-tool", "--container-sandbox"}, map[string]any{
		"session_id":      "bg09",
		"hook_event_name": "PostToolUse",
		"cwd":             repoDir,
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "python3 etl.py"},
		"tool_response":   map[string]any{"text": "ok"},
	})
	passedAfterExec := executeHookCmd(t, []string{"stop", "--container-sandbox"}, map[string]any{
		"hook_event_name": "Stop",
		"cwd":             repoDir,
	})
	if passedAfterExec != "{}" {
		t.Fatalf("expected Stop '{}' after clean post-edit python3 execution, got %s", passedAfterExec)
	}

	scratchTests := filepath.Join(repoDir, ".tmp", "tests")
	if err := os.MkdirAll(scratchTests, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scratchTests, "test_etl.py"), []byte("import unittest\nfrom etl import run\nclass T(unittest.TestCase):\n    def test_run(self): self.assertEqual(run(), 7)\n"), 0644); err != nil {
		t.Fatal(err)
	}
	testedBanner := hookcmd.FormatPlainHandshakeBannerWithRoot(ev, repoDir, false, "4.24.0")
	if strings.Contains(testedBanner, "Regression Suite  : none detected") {
		t.Fatalf("expected Regression Suite guidance suppressed once .tmp/tests/test_etl.py exists, got:\n%s", testedBanner)
	}
}
