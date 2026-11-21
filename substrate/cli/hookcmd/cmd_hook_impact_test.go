package hookcmd

import (
	"github.com/mgantlett/nomos/substrate/cli/hookcmd/shellguard"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/handshake"
)

func TestHookSymlinkSecretAndFirstEditImpact(t *testing.T) {
	t.Setenv("NOMOS_HOME", t.TempDir())
	ws := t.TempDir()

	// 1. ln -s blocked by Law 6
	decSym := EvaluatePreToolHook(&shellguard.HookPayload{ToolName: "Bash", CommandLine: "ln -s ../solution_cache/a.json out.json", Cwd: ws}, ws)
	if decSym.Decision != hookDecisionDeny || !strings.Contains(decSym.Reason, "ZeroFilesystemAliasGate") {
		t.Fatalf("expected symlink command block, got: %+v", decSym)
	}

	// 2. printenv SECRET blocked by Law 7
	decEnv := EvaluatePreToolHook(&shellguard.HookPayload{ToolName: "Bash", CommandLine: "printenv WEBHOOK_SECRET_KEY > log.txt", Cwd: ws}, ws)
	if decEnv.Decision != hookDecisionDeny || !strings.Contains(decEnv.Reason, "Secret Airlock") {
		t.Fatalf("expected printenv secret dump block, got: %+v", decEnv)
	}

	// 3. Read outside hermeticDir blocked
	decRead := EvaluatePreToolHook(&shellguard.HookPayload{ToolName: "Read", TargetFile: "../verifier/vectors.json", Cwd: ws}, ws)
	if decRead.Decision != hookDecisionDeny {
		t.Fatalf("expected read escape outside hermeticDir to be denied, got: %+v", decRead)
	}

	// 4. First-edit 1-hop impact fires once per file and suppresses repeat spam
	wtDir := filepath.Join(ws, "worktrees", "demo-SUB-1")
	if err := os.MkdirAll(wtDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtDir, "mux.go"), []byte("package chi\ntype Router interface { Get() }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	treePath := filepath.Join(wtDir, "tree.go")
	if err := os.WriteFile(treePath, []byte("package chi\nfunc Register() int { return 1 }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	p := &shellguard.HookPayload{ToolName: "Write", TargetFile: treePath, Cwd: wtDir}
	adv1 := EvaluatePostToolHook(p, wtDir)
	if !strings.Contains(adv1, "Nomos 1-Hop Impact") || !strings.Contains(adv1, "mux.go") {
		t.Fatalf("expected first-edit 1-hop impact naming mux.go, got: %q", adv1)
	}
	adv2 := EvaluatePostToolHook(p, wtDir)
	if strings.Contains(adv2, "Nomos 1-Hop Impact") {
		t.Fatalf("expected second edit on same file not to repeat 1-hop impact, got: %q", adv2)
	}

	// 5. Compact handshake smoke check
	RenderCompactHandshakeBanner(&handshake.HandshakeEvent{
		Repository: "demo",
		Phase:      "EDIT",
		ActiveTask: "SUB-1",
		GateCount:  42,
		BrownfieldTopology: &handshake.BrownfieldTopologyBrief{
			Languages: []string{"go"},
		},
	})
}

func TestHotspotFirstReadHint_FiresOnceAboveCeilingAndSilentBelow(t *testing.T) {
	t.Setenv("NOMOS_HOME", t.TempDir())
	wtDir := filepath.Join(t.TempDir(), "worktrees", "demo-SUB-2")
	if err := os.MkdirAll(wtDir, 0755); err != nil {
		t.Fatal(err)
	}
	small := filepath.Join(wtDir, "small.go")
	if err := os.WriteFile(small, []byte("package chi\ntype Router interface { Get() }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if msg := evaluateReadSpiralAdvisory(&shellguard.HookPayload{ToolName: "Read", TargetFile: small, Cwd: wtDir}, "", "terse"); msg != "" {
		t.Fatalf("expected silent read on small file, got: %q", msg)
	}

	hot := filepath.Join(wtDir, "mux_hot.go")
	bigSrc := "package chi\n" + strings.Repeat("// line\n", 510) + "func Handle() {}\n"
	if err := os.WriteFile(hot, []byte(bigSrc), 0644); err != nil {
		t.Fatal(err)
	}
	msg1 := evaluateReadSpiralAdvisory(&shellguard.HookPayload{ToolName: "Read", TargetFile: hot, Cwd: wtDir}, "", "terse")
	if !strings.Contains(msg1, "Nomos Hotspot Read") || !strings.Contains(msg1, "small.go") || !strings.Contains(msg1, "outline: Handle:L512-512") {
		t.Fatalf("expected one-time hotspot read orientation + outline + peer hint, got: %q", msg1)
	}
	if msg2 := evaluateReadSpiralAdvisory(&shellguard.HookPayload{ToolName: "Read", TargetFile: hot, Cwd: wtDir}, "", "terse"); msg2 != "" {
		t.Fatalf("expected second read of hotspot file to be silent, got: %q", msg2)
	}
}

func TestFeatureBranchMode_EmitsFirstEditImpactAndHotspotRead(t *testing.T) {
	t.Setenv("NOMOS_HOME", t.TempDir())
	repoDir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := workspace.SafeGitCommand(repoDir, args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v (%s)", args, err, string(out))
		}
	}
	runGit("init", "-q", "-b", "main")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repoDir, "nomos.yaml"), []byte("project: demo\ndefault_branch: main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	peer := filepath.Join(repoDir, "router.go")
	if err := os.WriteFile(peer, []byte("package demo\ntype Router interface { Serve() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := filepath.Join(repoDir, "handler.go")
	if err := os.WriteFile(handler, []byte("package demo\nfunc Dispatch() int { return 2 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hot := filepath.Join(repoDir, "monolith.go")
	if err := os.WriteFile(hot, []byte("package demo\n"+strings.Repeat("// legacy\n", 510)+"func Handle() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", ".")
	runGit("commit", "-q", "-m", "init")
	runGit("checkout", "-q", "-b", "feat/seam-fix")

	readHint := evaluateReadSpiralAdvisory(&shellguard.HookPayload{ToolName: "Read", TargetFile: hot, Cwd: repoDir}, "", "terse")
	if !strings.Contains(readHint, "Nomos Hotspot Read") || !strings.Contains(readHint, "router.go") {
		t.Fatalf("expected hotspot read hint on feat/* branch, got: %q", readHint)
	}
	editHint := EvaluatePostToolHook(&shellguard.HookPayload{ToolName: "Write", TargetFile: handler, Cwd: repoDir}, repoDir)
	if !strings.Contains(editHint, "Nomos 1-Hop Impact") || !strings.Contains(editHint, "router.go") {
		t.Fatalf("expected 1-hop impact hint on feat/* branch, got: %q", editHint)
	}
}

func TestMonorepoFirstReadSeamHint_EmitsPairedTestAndPeersOnLargeWorkspace(t *testing.T) {
	t.Setenv("NOMOS_HOME", t.TempDir())
	wtDir := filepath.Join(t.TempDir(), "worktrees", "mono-SUB-9")
	if err := os.MkdirAll(filepath.Join(wtDir, "tools", "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(wtDir, "tests", "tools", "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	rules := filepath.Join(wtDir, "tools", "api", "rules.py")
	if err := os.WriteFile(rules, []byte("RULES = {'S021': 'summary'}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtDir, "tools", "api", "card.py"), []byte("from tools.api import rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtDir, "tests", "tools", "api", "test_rules.py"), []byte("def test_ok():\n    assert True\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr := &HookSessionTrace{WorktreeRoot: wtDir, ImpactedFiles: []string{monoScaleYesToken}}
	got := evaluateFirstHotspotReadHint(&shellguard.HookPayload{ToolName: "Read", TargetFile: rules, Cwd: wtDir}, tr, wtDir)
	if !strings.Contains(got, "Nomos Monorepo Seam") || !strings.Contains(got, "tests/tools/api/test_rules.py") || !strings.Contains(got, "tools/api/card.py") {
		t.Fatalf("expected monorepo seam hint with mirrored test + 1-hop dependent, got: %q", got)
	}
	if again := evaluateFirstHotspotReadHint(&shellguard.HookPayload{ToolName: "Read", TargetFile: rules, Cwd: wtDir}, tr, wtDir); again != "" {
		t.Fatalf("expected second read of same file to stay silent, got: %q", again)
	}
}
