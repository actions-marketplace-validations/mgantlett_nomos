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
