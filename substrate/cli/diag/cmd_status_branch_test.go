package diag

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/constants"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/mem"
)

func seedBranchStatusRepo(t *testing.T, strict bool, stealth bool, withCI bool) string {
	t.Helper()
	t.Setenv("NOMOS_HOME", t.TempDir())
	t.Setenv("NOMOS_MNEME_DB_PATH", filepath.Join(t.TempDir(), constants.FileMnemeDB))

	repo := workspace.InitHermeticGitRepo(t)
	strictVal := "false"
	if strict {
		strictVal = "true"
	}
	cfg := "project: bg-status-demo\ndefault_branch: main\ngovernance:\n  boundary:\n    enforce: true\n    isolation_mode: \"branch\"\n  quality_gates:\n    strict_mode: " + strictVal + "\n"
	if err := os.WriteFile(filepath.Join(repo, constants.FileNomosYAML), []byte(cfg), 0644); err != nil {
		t.Fatalf("write nomos.yaml: %v", err)
	}
	if err := workspace.InstallHooks(repo); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}
	if stealth {
		infoDir := filepath.Join(repo, ".git", "info")
		if err := os.MkdirAll(infoDir, 0755); err != nil {
			t.Fatalf("mkdir info: %v", err)
		}
		if err := os.WriteFile(filepath.Join(infoDir, "exclude"), []byte(stealthMarkerToken+"\n/nomos.yaml\n"), 0644); err != nil {
			t.Fatalf("write exclude: %v", err)
		}
	}
	if withCI {
		wfDir := filepath.Join(repo, ".github", "workflows")
		if err := os.MkdirAll(wfDir, 0755); err != nil {
			t.Fatalf("mkdir workflows: %v", err)
		}
		if err := os.WriteFile(filepath.Join(wfDir, "nomos-verify.yml"), []byte("name: nomos\n"), 0644); err != nil {
			t.Fatalf("write workflow: %v", err)
		}
	}
	return repo
}

// @verifies TC-STATUS-BG-01
func TestBranchGuardStatus_ShieldScorecardAndFourRungLadder(t *testing.T) {
	// Rung 1: Stealth Trial
	stealthRepo := seedBranchStatusRepo(t, false, true, false)
	t.Chdir(stealthRepo)
	r1 := CollectStatusReport()
	if r1.GraduationRung != rungStealthTrial || !r1.StealthLocal {
		t.Fatalf("TC-STATUS-BG-01: expected rung 1 Stealth Trial, got rung=%d stealth=%v", r1.GraduationRung, r1.StealthLocal)
	}

	// Rung 2: Branch Guard with 3 verified commits -> nudges --ci
	bgRepo := seedBranchStatusRepo(t, false, false, false)
	if out, err := workspace.SafeGitCommand(bgRepo, "checkout", "-b", "feat/shield").CombinedOutput(); err != nil {
		t.Fatalf("checkout feat/shield: %v (%s)", err, string(out))
	}
	mem.RecordShieldEvent(bgRepo, mem.ShieldCategoryAntiCheat, "AntiShimGate", "feat/shield", "blocked skip")
	for i := 0; i < 3; i++ {
		mem.RecordShieldEvent(bgRepo, mem.ShieldCategoryVerifiedCommit, "post-commit", "feat/shield", "commit")
	}
	t.Chdir(bgRepo)

	cmd := NewStatusCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("nomos status failed: %v", err)
	}
	outStr := buf.String()
	for _, want := range []string{"Nomos Branch Guard Status", "Shield Score", "1 shortcut(s) blocked", "3 verified commit(s)", "[2] Branch Guard*", "nomos init --ci"} {
		if !strings.Contains(outStr, want) {
			t.Fatalf("TC-STATUS-BG-01: expected %q in Branch Guard status output, got:\n%s", want, outStr)
		}
	}
	if strings.Contains(outStr, "Living Ledger") {
		t.Fatalf("TC-STATUS-BG-01: did not expect Living Ledger worktree zeroes in Branch Guard status:\n%s", outStr)
	}

	// Verify JSON serialization contains Shield and Graduation metadata.
	jsonCmd := NewStatusCmd()
	var jBuf bytes.Buffer
	jsonCmd.SetOut(&jBuf)
	jsonCmd.SetErr(&jBuf)
	jsonCmd.SetArgs([]string{"--json"})
	if err := jsonCmd.Execute(); err != nil {
		t.Fatalf("nomos status --json failed: %v", err)
	}
	var parsed StatusReport
	if err := json.Unmarshal(jBuf.Bytes(), &parsed); err != nil {
		t.Fatalf("unmarshal status JSON: %v", err)
	}
	if parsed.IsolationMode != "branch" || parsed.GraduationRung != rungBranchGuard || parsed.Shield == nil || parsed.Shield.ShortcutsBlocked != 1 {
		t.Fatalf("TC-STATUS-BG-01: unexpected JSON status payload: %+v", parsed)
	}

	// Rung 3: CI PR Proofs (Grade A clean repo -> nudges --strict)
	ciRepo := seedBranchStatusRepo(t, false, false, true)
	t.Chdir(ciRepo)
	r3 := CollectStatusReport()
	if r3.GraduationRung != rungCIPRProofs || len(r3.NextActions) == 0 {
		t.Fatalf("TC-STATUS-BG-01: expected rung 3 CI PR Proofs, got rung=%d actions=%+v", r3.GraduationRung, r3.NextActions)
	}
	foundStrictNudge := false
	for _, a := range r3.NextActions {
		if a.Command == "nomos init --strict" {
			foundStrictNudge = true
		}
	}
	if !foundStrictNudge {
		t.Fatalf("TC-STATUS-BG-01: expected Grade A Rung 3 to nudge 'nomos init --strict', got %+v", r3.NextActions)
	}

	// Rung 4: Strict mode
	strictRepo := seedBranchStatusRepo(t, true, false, true)
	t.Chdir(strictRepo)
	r4 := CollectStatusReport()
	if r4.GraduationRung != rungStrictOrWorktree || !r4.StrictMode {
		t.Fatalf("TC-STATUS-BG-01: expected rung 4 Strict, got rung=%d strict=%v", r4.GraduationRung, r4.StrictMode)
	}
}
