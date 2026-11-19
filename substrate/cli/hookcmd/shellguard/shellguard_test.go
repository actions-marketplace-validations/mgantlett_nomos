package shellguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
)

func TestShellScanner_InvocationsHeredocsAndHiddenScripts(t *testing.T) {
	invs := ExtractAllContainerCommandInvocations(`FOO=1 env bash -c "python3 -c 'import subprocess'" && $(curl -s http://localhost:8080)`)
	if len(invs) < 3 {
		t.Fatalf("expected nested invocations extracted, got %+v", invs)
	}
	foundEscape := false
	for _, inv := range invs {
		if HasInterpreterSubprocessEscape(inv) {
			foundEscape = true
		}
	}
	if !foundEscape {
		t.Fatalf("expected python subprocess escape to be detected")
	}

	if !HasDisallowedHiddenDotScript(".rogue/run.py", false) {
		t.Fatalf("expected .rogue/run.py to be flagged")
	}
	if HasDisallowedHiddenDotScript(".tmp/tests/test_app.py", false) {
		t.Fatalf("expected trusted .tmp/tests/test_app.py to be allowed")
	}
	if !HasDisallowedHiddenDotScript(".tmp/tests/test_app.py", true) {
		t.Fatalf("expected .tmp/tests/test_app.py to be blocked in untrusted mode")
	}

	safeSrc := "cat > app.py <<'EOF'\nDROP TABLE users;\nEOF"
	if strings.Contains(StripSafeFileWriteHeredocBodies(safeSrc), "DROP TABLE") {
		t.Fatalf("expected safe source heredoc body stripped")
	}
	pipedSrc := "cat <<'EOF' | sqlite3 app.db\nDROP TABLE users;\nEOF"
	if !strings.Contains(StripSafeFileWriteHeredocBodies(pipedSrc), "DROP TABLE") {
		t.Fatalf("expected piped heredoc body retained")
	}
}

func TestRuntimeGuard_BinaryMediaAndInteractiveCommands(t *testing.T) {
	dec := EvaluateBinaryReadGuard(&HookPayload{ToolName: "Read", TargetFile: "/tmp/model.safetensors"}, true)
	if dec.Decision != DecisionDeny {
		t.Fatalf("expected binary read deny, got %+v", dec)
	}
	gemDec := EvaluateBinaryReadGuard(&HookPayload{ToolName: "read_file", TargetFile: "/tmp/chart.png", IsGeminiEvent: true}, true)
	if gemDec.Decision != DecisionDeny {
		t.Fatalf("expected gemini media read deny, got %+v", gemDec)
	}
	if allow := EvaluateBinaryReadGuard(&HookPayload{ToolName: "Read", TargetFile: "/tmp/main.go"}, true); allow.Decision != DecisionAllow {
		t.Fatalf("expected source file read allow, got %+v", allow)
	}

	if hang := EvaluateInteractiveCommandGuard("sudo vim /etc/hosts"); hang.Decision != DecisionDeny {
		t.Fatalf("expected interactive vim deny, got %+v", hang)
	}
	if tailFollow := EvaluateInteractiveCommandGuard("tail -f app.log"); tailFollow.Decision != DecisionDeny {
		t.Fatalf("expected unbounded tail -f deny, got %+v", tailFollow)
	}
	if tailBounded := EvaluateInteractiveCommandGuard("tail -f app.log | head -n 5"); tailBounded.Decision != DecisionAllow {
		t.Fatalf("expected head-bounded tail -f allow, got %+v", tailBounded)
	}
}

func TestInstallGuard_BlocksWorktreeAndAllowsCleanTrunk(t *testing.T) {
	t.Setenv("NOMOS_HOME", t.TempDir())
	repoDir := filepath.Join(t.TempDir(), "nomos")
	cmdDir := filepath.Join(repoDir, "substrate", "cmd", "nomos")
	if err := os.MkdirAll(cmdDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "substrate", "go.mod"), []byte("module github.com/mgantlett/nomos/substrate\n\ngo 1.24\n"), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cmdDir, "main.go"), []byte("package main\nfunc main() {}\n"), 0644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	if err := workspace.SafeGitCommand(repoDir, "init", "-b", "main").Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if err := workspace.SafeGitCommand(repoDir, "config", "user.email", "t@example.com").Run(); err != nil {
		t.Fatalf("git email: %v", err)
	}
	if err := workspace.SafeGitCommand(repoDir, "config", "user.name", "Test").Run(); err != nil {
		t.Fatalf("git name: %v", err)
	}
	if err := workspace.SafeGitCommand(repoDir, "add", "-A").Run(); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := workspace.SafeGitCommand(repoDir, "commit", "-m", "init").Run(); err != nil {
		t.Fatalf("git commit: %v", err)
	}

	subDir := filepath.Join(repoDir, "substrate")
	if IsForbiddenNomosGoInstall("go install ./cmd/nomos", subDir, "") {
		t.Fatalf("expected clean IDLE main trunk go install ./cmd/nomos to be permitted")
	}
	if !IsForbiddenNomosGoInstall("go install ./cmd/nomos", subDir, "/worktrees/nomos-SUB-1") {
		t.Fatalf("expected hermetic worktree go install to be blocked")
	}
	if !IsForbiddenNomosGoInstall("go install github.com/mgantlett/nomos/substrate/cmd/nomos@latest", subDir, "") {
		t.Fatalf("expected remote @latest nomos go install to be blocked")
	}
}
