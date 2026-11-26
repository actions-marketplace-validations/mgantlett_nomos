package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/mgantlett/nomos/substrate/cli/hookcmd/shellguard"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/cli/hookcmd"
	"github.com/mgantlett/nomos/substrate/cli/selfupdate"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/config"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/constants"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/mem"
)

func seedLegacyBranchGuardRepo(t *testing.T) (string, string) {
	t.Helper()
	repoDir := t.TempDir()
	runGitOrFail(t, repoDir, "init", "-b", "main")
	runGitOrFail(t, repoDir, "config", "user.email", "dev@example.com")
	runGitOrFail(t, repoDir, "config", "user.name", "Adoption Tester")

	if err := os.WriteFile(filepath.Join(repoDir, "go.mod"), []byte("module example.com/onboarding\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	var monolith strings.Builder
	monolith.WriteString("package main\n\nimport \"fmt\"\n\n")
	for i := 0; i < 420; i++ {
		monolith.WriteString(fmt.Sprintf("// filler line %d\n", i))
	}
	monolith.WriteString("func main() {\n\tfmt.Println(\"hello\")\n}\n")
	if err := os.WriteFile(filepath.Join(repoDir, "app.go"), []byte(monolith.String()), 0644); err != nil {
		t.Fatalf("write app.go: %v", err)
	}
	testSrc := "package main\n\nimport \"testing\"\n\nfunc TestSmoke(t *testing.T) { main() }\n"
	if err := os.WriteFile(filepath.Join(repoDir, "app_test.go"), []byte(testSrc), 0644); err != nil {
		t.Fatalf("write app_test.go: %v", err)
	}
	runGitOrFail(t, repoDir, "add", ".")
	runGitOrFail(t, repoDir, "commit", "-m", "chore: initial indebted repo")
	headSHA := strings.TrimSpace(runGitOrFail(t, repoDir, "rev-parse", "HEAD"))
	return repoDir, headSHA
}

func runRootCLIForTest(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if stdin != "" {
		cmd.SetIn(strings.NewReader(stdin))
	}
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func assertHookJSONDecision(t *testing.T, raw, wantDecision string) {
	t.Helper()
	var dec shellguard.HookDecision
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &dec); err != nil {
		t.Fatalf("unmarshal hook decision (%q): %v", raw, err)
	}
	if dec.Decision != wantDecision {
		t.Fatalf("expected hook decision %q, got %+v (raw=%s)", wantDecision, dec, raw)
	}
}

// @verifies TC-INIT-04
// @verifies TC-INIT-05
// @verifies TC-INIT-06
func TestCleanroomBranchGuardOnboarding_StealthAutoBaselineAndCycle(t *testing.T) {
	t.Setenv(constants.EnvNomosHome, t.TempDir())
	t.Setenv("NOMOS_MNEME_DB_PATH", filepath.Join(t.TempDir(), constants.FileMnemeDB))
	t.Setenv("NOMOS_IN_TOOLCHAIN_GATE", "")
	t.Setenv("PATH", t.TempDir()+string(os.PathListSeparator)+os.Getenv("PATH"))

	repoDir, initialHead := seedLegacyBranchGuardRepo(t)
	verifyStealthLocalInit(t, repoDir, initialHead)
	verifyStealthPromotionToBranchGuard(t, repoDir, initialHead)
	verifyBranchGuardLifecycleAndMneme(t, repoDir)
}

func verifyStealthLocalInit(t *testing.T, repoDir, initialHead string) {
	t.Helper()
	out, err := runRootCLIForTest(t, "", "init", "--local", "--agents", "claude", "--dir", repoDir)
	if err != nil {
		t.Fatalf("nomos init --local failed: %v\n%s", err, out)
	}
	if status := strings.TrimSpace(runGitOrFail(t, repoDir, "status", "--porcelain")); status != "" {
		t.Fatalf("TC-INIT-05: expected completely empty git status after --local init, got:\n%s", status)
	}
	if headNow := strings.TrimSpace(runGitOrFail(t, repoDir, "rev-parse", "HEAD")); headNow != initialHead {
		t.Fatalf("TC-INIT-04: expected zero silent trunk commit, HEAD changed %s -> %s", initialHead, headNow)
	}
	cfg, cErr := config.LoadConfig(repoDir)
	if cErr != nil {
		t.Fatalf("LoadConfig failed: %v", cErr)
	}
	if cfg.Governance.Boundary.EffectiveIsolationMode() != config.IsolationModeBranch || cfg.Governance.QualityGates.StrictMode {
		t.Fatalf("TC-INIT-04: expected branch mode + strict_mode=false, got mode=%s strict=%v", cfg.Governance.Boundary.EffectiveIsolationMode(), cfg.Governance.QualityGates.StrictMode)
	}
	assertStealthLocalScaffoldFiles(t, repoDir)
}

func assertStealthLocalScaffoldFiles(t *testing.T, repoDir string) {
	t.Helper()
	if !fileExists(filepath.Join(repoDir, constants.FileNomosBaselineJSON)) {
		t.Fatalf("TC-INIT-04: expected auto-written nomos.baseline.json for indebted repo")
	}
	if pathExists(filepath.Join(repoDir, constants.DirWorktrees)) || pathExists(filepath.Join(repoDir, ".gitignore")) {
		t.Fatalf("TC-INIT-05: expected no worktrees/ dir or .gitignore in --local mode")
	}
	agentsRaw, aErr := os.ReadFile(filepath.Join(repoDir, "AGENTS.md"))
	agentsStr := string(agentsRaw)
	if aErr != nil || !strings.Contains(agentsStr, "Branch Guard") || strings.Contains(agentsStr, "THE ONE-PAGE INVARIANT CARD") {
		t.Fatalf("TC-INIT-06: expected compact AGENTS_BRANCH protocol in AGENTS.md, got:\n%s", agentsStr)
	}
	if !strings.Contains(agentsStr, "never ask the human operator to run") || !strings.Contains(agentsStr, "Never run `nomos init --worktrees` unprompted") {
		t.Fatalf("TC-INIT-06: expected invisible-stewardship and no-unprompted-worktrees rules in AGENTS_BRANCH.md, got:\n%s", agentsStr)
	}
	if !fileExists(filepath.Join(repoDir, claudeDirToken, settingsFileName)) || !fileExists(filepath.Join(repoDir, "CLAUDE.md")) {
		t.Fatalf("TC-INIT-06: expected claude hooks + CLAUDE.md")
	}
	if fileExists(filepath.Join(repoDir, hookcmd.CursorDirToken, hookcmd.HooksFileName)) || fileExists(filepath.Join(repoDir, geminiDirToken, settingsFileName)) {
		t.Fatalf("TC-INIT-06: unexpected non-selected agent manifests scaffolded")
	}
	if !fileExists(filepath.Join(repoDir, constants.DirGit, "hooks", "post-commit")) {
		t.Fatalf("expected executable post-commit git hook")
	}
}

func verifyStealthPromotionToBranchGuard(t *testing.T, repoDir, initialHead string) {
	t.Helper()
	if pOut, pErr := runRootCLIForTest(t, "", "init", "--agents", "claude", "--dir", repoDir); pErr != nil {
		t.Fatalf("promotion nomos init failed: %v\n%s", pErr, pOut)
	}
	if headAfterPromo := strings.TrimSpace(runGitOrFail(t, repoDir, "rev-parse", "HEAD")); headAfterPromo != initialHead {
		t.Fatalf("TC-INIT-04: expected promotion init NOT to commit on main silently")
	}
	excludeRaw, exErr := os.ReadFile(filepath.Join(repoDir, constants.DirGit, "info", "exclude"))
	if exErr != nil || strings.Contains(string(excludeRaw), localStealthStartMarker) {
		t.Fatalf("TC-INIT-05: expected stealth exclude block stripped after non-local init")
	}
	if giRaw, gErr := os.ReadFile(filepath.Join(repoDir, ".gitignore")); gErr != nil || strings.Contains(string(giRaw), "/worktrees/") {
		t.Fatalf("TC-INIT-04: expected .gitignore without /worktrees/ in branch mode, err=%v", gErr)
	}
}

func verifyBranchGuardLifecycleAndMneme(t *testing.T, repoDir string) {
	t.Helper()
	mainWritePayload := fmt.Sprintf(`{"tool_name":"Write","cwd":%q,"tool_input":{"file_path":%q}}`, repoDir, filepath.Join(repoDir, "login.go"))
	mainHookOut, mhErr := runRootCLIForTest(t, mainWritePayload, "hook", "pre-tool")
	if mhErr != nil {
		t.Fatalf("hook pre-tool on main failed: %v", mhErr)
	}
	assertHookJSONDecision(t, mainHookOut, "deny")

	runGitOrFail(t, repoDir, "switch", "-c", "feat/login")
	featWritePayload := fmt.Sprintf(`{"tool_name":"Write","cwd":%q,"tool_input":{"file_path":%q}}`, repoDir, filepath.Join(repoDir, "login.go"))
	featHookOut, fhErr := runRootCLIForTest(t, featWritePayload, "hook", "pre-tool")
	if fhErr != nil {
		t.Fatalf("hook pre-tool on feat/login failed: %v", fhErr)
	}
	assertHookJSONDecision(t, featHookOut, "allow")

	writeLoginFixtureAndAssertStopGate(t, repoDir)
	assertPostCommitDistillAndZeroDrift(t, repoDir)
}

func writeLoginFixtureAndAssertStopGate(t *testing.T, repoDir string) {
	t.Helper()
	loginCode := "package main\n\n// LoginHelper validates user token presence.\nfunc LoginHelper(tok string) bool { return tok != \"\" }\n"
	if wErr := os.WriteFile(filepath.Join(repoDir, "login.go"), []byte(loginCode), 0644); wErr != nil {
		t.Fatalf("write login.go: %v", wErr)
	}
	loginTest := "package main\n\nimport \"testing\"\n\nfunc TestLoginHelper(t *testing.T) {\n\tif !LoginHelper(\"abc\") {\n\t\tt.Fatal(\"expected valid\")\n\t}\n}\n"
	if wErr := os.WriteFile(filepath.Join(repoDir, "login_test.go"), []byte(loginTest), 0644); wErr != nil {
		t.Fatalf("write login_test.go: %v", wErr)
	}
	stopPayload := fmt.Sprintf(`{"hook_event_name":"Stop","cwd":%q}`, repoDir)
	stopBlocked, sbErr := runRootCLIForTest(t, stopPayload, "hook", "stop")
	if sbErr != nil {
		t.Fatalf("hook stop unverified failed: %v", sbErr)
	}
	assertHookJSONDecision(t, stopBlocked, "block")

	if vErr := runVerification(repoDir, "", false, false, false); vErr != nil {
		t.Fatalf("runVerification on feat/login failed: %v", vErr)
	}
	stopAllowed, saErr := runRootCLIForTest(t, stopPayload, "hook", "stop")
	if saErr != nil {
		t.Fatalf("hook stop after verify failed: %v", saErr)
	}
	if strings.TrimSpace(stopAllowed) != "{}" {
		t.Fatalf("expected clean Stop allow '{}' after verify, got %q", stopAllowed)
	}
}

func assertPostCommitDistillAndZeroDrift(t *testing.T, repoDir string) {
	t.Helper()
	runGitOrFail(t, repoDir, "add", "login.go", "login_test.go")
	runGitOrFail(t, repoDir, "commit", "-m", "feat: add login helper")
	if pcOut, pcErr := runRootCLIForTest(t, "", "hook", "post-commit", "--dir", repoDir); pcErr != nil {
		t.Fatalf("hook post-commit failed: %v\n%s", pcErr, pcOut)
	}
	store, sErr := mem.NewStore("")
	if sErr != nil {
		t.Fatalf("mem.NewStore failed: %v", sErr)
	}
	records, qErr := store.Query(mem.MemoryFilter{Kind: mem.KindPattern})
	if cErr := store.Close(); cErr != nil {
		t.Fatalf("store.Close failed: %v", cErr)
	}
	if qErr != nil {
		t.Fatalf("store.Query failed: %v", qErr)
	}
	foundPattern := false
	for _, rec := range records {
		if strings.Contains(rec.Content, "COMMIT-") && strings.Contains(rec.Content, "feat: add login helper") {
			foundPattern = true
			break
		}
	}
	if !foundPattern {
		t.Fatalf("expected post-commit Mneme pattern record for feat: add login helper, got %d record(s)", len(records))
	}
	if drift := selfupdate.DetectWorkspaceContractDrift(repoDir); len(drift) != 0 {
		t.Fatalf("expected zero workspace drift on clean Branch Guard repo, got %v", drift)
	}
}

// @verifies TC-INIT-07
func TestCleanroomBranchGuardOnboarding_StrictAndWorktreesOverrides(t *testing.T) {
	t.Setenv(constants.EnvNomosHome, t.TempDir())
	t.Setenv("NOMOS_IN_TOOLCHAIN_GATE", "")

	strictDir, strictHead := seedLegacyBranchGuardRepo(t)
	if out, err := runRootCLIForTest(t, "", "init", "--strict", "--agents", "claude", "--dir", strictDir); err != nil {
		t.Fatalf("nomos init --strict failed: %v\n%s", err, out)
	}
	if headNow := strings.TrimSpace(runGitOrFail(t, strictDir, "rev-parse", "HEAD")); headNow != strictHead {
		t.Fatalf("TC-INIT-07: expected --strict without --worktrees not to commit on main")
	}
	strictCfg, err := config.LoadConfig(strictDir)
	if err != nil {
		t.Fatalf("LoadConfig strictDir: %v", err)
	}
	if !strictCfg.Governance.QualityGates.StrictMode || fileExists(filepath.Join(strictDir, constants.FileNomosBaselineJSON)) {
		t.Fatalf("TC-INIT-07: expected --strict to enforce strict_mode: true and skip nomos.baseline.json")
	}

	wtDir, wtHeadBefore := seedLegacyBranchGuardRepo(t)
	if out, wErr := runRootCLIForTest(t, "", "init", "--worktrees", "--agents", "all", "--dir", wtDir); wErr != nil {
		t.Fatalf("nomos init --worktrees failed: %v\n%s", wErr, out)
	}
	wtCfg, wCfgErr := config.LoadConfig(wtDir)
	if wCfgErr != nil {
		t.Fatalf("LoadConfig wtDir: %v", wCfgErr)
	}
	if wtCfg.Governance.Boundary.EffectiveIsolationMode() != config.IsolationModeWorktree {
		t.Fatalf("TC-INIT-07: expected isolation_mode=worktree, got %q", wtCfg.Governance.Boundary.EffectiveIsolationMode())
	}
	wtAgents, wtReadErr := os.ReadFile(filepath.Join(wtDir, "AGENTS.md"))
	if wtReadErr != nil || !strings.Contains(string(wtAgents), "THE ONE-PAGE INVARIANT CARD") {
		t.Fatalf("TC-INIT-07: expected full AGENTS.md protocol card in --worktrees mode (err=%v)", wtReadErr)
	}
	if headAfter := strings.TrimSpace(runGitOrFail(t, wtDir, "rev-parse", "HEAD")); headAfter == wtHeadBefore {
		t.Fatalf("TC-INIT-07: expected --worktrees init to create baseline governance commit")
	}
}

// @verifies TC-HELP-01
func TestRootCmd_TwoTierHelpAndHelpAgentSurface(t *testing.T) {
	defaultOut, dErr := runRootCLIForTest(t, "", "--help")
	if dErr != nil {
		t.Fatalf("nomos --help failed: %v", dErr)
	}
	if !strings.Contains(defaultOut, "Cognitive Memory Commands:") || strings.Contains(defaultOut, "Agent Boot & Cognitive Memory Commands:") {
		t.Errorf("TC-HELP-01: expected default help to render 'Cognitive Memory Commands:' without Agent Boot prefix, got:\n%s", defaultOut)
	}
	for _, visible := range []string{"init", "status", "task", "verify", "xray", "mem", "issue", "update", "version", "--help-agent"} {
		if !strings.Contains(defaultOut, visible) {
			t.Errorf("TC-HELP-01: expected default help to include %q, got:\n%s", visible, defaultOut)
		}
	}
	for _, hidden := range []string{"handshake", "hook", "directive", "capabilities", "physics", "session", "mcp"} {
		if strings.Contains(defaultOut, "\n  "+hidden+" ") {
			t.Errorf("TC-HELP-01: expected internal RPC %q hidden in command list, got:\n%s", hidden, defaultOut)
		}
	}

	for _, args := range [][]string{{"--help-agent"}, {"help", "--help-agent"}} {
		agentOut, aErr := runRootCLIForTest(t, "", args...)
		if aErr != nil {
			t.Fatalf("nomos %v failed: %v", args, aErr)
		}
		if !strings.Contains(agentOut, "Agent Boot & Cognitive Memory Commands:") {
			t.Errorf("TC-HELP-01: expected %v to render 'Agent Boot & Cognitive Memory Commands:', got:\n%s", args, agentOut)
		}
		for _, revealed := range []string{"handshake", "hook", "directive", "capabilities", "physics", "session", "mcp"} {
			if !strings.Contains(agentOut, "\n  "+revealed+" ") {
				t.Errorf("TC-HELP-01: expected %v to list %q in command list, got:\n%s", args, revealed, agentOut)
			}
		}
	}
}

// @verifies TC-INIT-08
// @verifies TC-INIT-10
// @verifies TC-STATUS-BG-02
func TestCleanroomBranchGuardOnboarding_RatchetScratchStrictPromotionAndReceipts(t *testing.T) {
	t.Setenv(constants.EnvNomosHome, t.TempDir())
	t.Setenv("NOMOS_MNEME_DB_PATH", filepath.Join(t.TempDir(), constants.FileMnemeDB))
	t.Setenv("NOMOS_IN_TOOLCHAIN_GATE", "")
	t.Setenv("HOME", t.TempDir())

	// 1. Clean scratch repo starts in Ratchet mode with compact nomos.yaml and zero dumped agent folders.
	scratchDir := t.TempDir()
	runGitOrFail(t, scratchDir, "init", "-b", "main")
	runGitOrFail(t, scratchDir, "config", "user.email", "dev@example.com")
	runGitOrFail(t, scratchDir, "config", "user.name", "Scratch Tester")
	if err := os.WriteFile(filepath.Join(scratchDir, "README.md"), []byte("# Scratch\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGitOrFail(t, scratchDir, "add", ".")
	runGitOrFail(t, scratchDir, "commit", "-m", "chore: initial scratch")

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", t.TempDir())
	initOut, err := runRootCLIForTest(t, "", "init", "--dir", scratchDir)
	t.Setenv("PATH", origPath)
	if err != nil {
		t.Fatalf("TC-INIT-08: nomos init on scratch repo failed: %v\n%s", err, initOut)
	}
	rawCfg, rErr := os.ReadFile(filepath.Join(scratchDir, constants.FileNomosYAML))
	if rErr != nil {
		t.Fatalf("read nomos.yaml: %v", rErr)
	}
	lines := strings.Split(strings.TrimSpace(string(rawCfg)), "\n")
	if len(lines) > 40 || !strings.Contains(string(rawCfg), "strict_mode: false") || !strings.Contains(string(rawCfg), "$schema=") {
		t.Fatalf("TC-INIT-08: expected compact (<=40 lines) strict_mode: false nomos.yaml with $schema, got %d lines:\n%s", len(lines), string(rawCfg))
	}
	for _, hiddenDir := range []string{".claude", ".cursor", ".opencode", ".gemini", ".agents"} {
		if pathExists(filepath.Join(scratchDir, hiddenDir)) {
			t.Fatalf("TC-INIT-09: did not expect %s dumped when zero agents detected", hiddenDir)
		}
	}

	// 2. Post-commit Shield Receipt emits on first commit, stays quiet on routine clean second commit, and emits on catch commit.
	runGitOrFail(t, scratchDir, "switch", "-c", "feat/receipts")
	if wErr := os.WriteFile(filepath.Join(scratchDir, "note1.txt"), []byte("one\n"), 0644); wErr != nil {
		t.Fatalf("write note1: %v", wErr)
	}
	runGitOrFail(t, scratchDir, "add", "note1.txt")
	runGitOrFail(t, scratchDir, "-c", "core.hooksPath=/dev/null", "commit", "-m", "docs: first commit")
	pc1, pc1Err := runRootCLIForTest(t, "", "hook", "post-commit", "--dir", scratchDir)
	if pc1Err != nil || !strings.Contains(pc1, "first verified commit") {
		t.Fatalf("TC-STATUS-BG-02: expected first verified commit receipt, got err=%v out=%q", pc1Err, pc1)
	}

	if wErr := os.WriteFile(filepath.Join(scratchDir, "note2.txt"), []byte("two\n"), 0644); wErr != nil {
		t.Fatalf("write note2: %v", wErr)
	}
	runGitOrFail(t, scratchDir, "add", "note2.txt")
	runGitOrFail(t, scratchDir, "-c", "core.hooksPath=/dev/null", "commit", "-m", "docs: clean second commit")
	pc2, pc2Err := runRootCLIForTest(t, "", "hook", "post-commit", "--dir", scratchDir)
	if pc2Err != nil || strings.TrimSpace(pc2) != "" {
		t.Fatalf("TC-STATUS-BG-02: expected quiet clean second commit, got err=%v out=%q", pc2Err, pc2)
	}

	mem.RecordShieldEvent(scratchDir, mem.ShieldCategoryAntiCheat, "AntiShimGate", "feat/receipts", "blocked skip")
	if wErr := os.WriteFile(filepath.Join(scratchDir, "note3.txt"), []byte("three\n"), 0644); wErr != nil {
		t.Fatalf("write note3: %v", wErr)
	}
	runGitOrFail(t, scratchDir, "add", "note3.txt")
	runGitOrFail(t, scratchDir, "-c", "core.hooksPath=/dev/null", "commit", "-m", "fix: commit after catch")
	pc3, pc3Err := runRootCLIForTest(t, "", "hook", "post-commit", "--dir", scratchDir)
	if pc3Err != nil || !strings.Contains(pc3, "1 shortcut(s) caught before commit") {
		t.Fatalf("TC-STATUS-BG-02: expected catch receipt on third commit, got err=%v out=%q", pc3Err, pc3)
	}

	// 3. Idempotent --strict promotion upgrades existing nomos.yaml while preserving baseline manifest.
	if bErr := os.WriteFile(filepath.Join(scratchDir, constants.FileNomosBaselineJSON), []byte(`{"hangover_score":0,"grade":"A"}`), 0644); bErr != nil {
		t.Fatalf("write baseline: %v", bErr)
	}
	strictOut, sErr := runRootCLIForTest(t, "", "init", "--strict", "--dir", scratchDir)
	if sErr != nil {
		t.Fatalf("TC-INIT-10: nomos init --strict promotion failed: %v\n%s", sErr, strictOut)
	}
	promotedCfg, pErr := config.LoadConfig(scratchDir)
	if pErr != nil || !promotedCfg.Governance.QualityGates.StrictMode {
		t.Fatalf("TC-INIT-10: expected promoted strict_mode: true, got err=%v cfg=%+v", pErr, promotedCfg)
	}
	if !fileExists(filepath.Join(scratchDir, constants.FileNomosBaselineJSON)) {
		t.Fatalf("TC-INIT-10: expected historical nomos.baseline.json preserved on --strict promotion")
	}
}
