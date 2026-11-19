package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/handshake"
)

func TestExecuteInit_BrownfieldGitRepoAndVerify(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "nomos-brownfield-git-*")
	if err != nil {
		t.Fatalf("failed creating temp dir: %v", err)
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	initGitBrownfieldRepo(t, tempDir)

	if err := executeInit(tempDir); err != nil {
		t.Fatalf("executeInit failed on brownfield git repo: %v", err)
	}

	if err := runVerification(tempDir, "", true, true, false); err != nil {
		t.Fatalf("expected brownfield repo to pass all 42 verification gates immediately after nomos init, got: %v", err)
	}
}

func initGitBrownfieldRepo(t *testing.T, dir string) {
	t.Helper()
	_ = workspace.SafeGitCommand(dir, "init", "-b", "main").Run()
	_ = workspace.SafeGitCommand(dir, "config", "user.email", "dev@nomos.local").Run()
	_ = workspace.SafeGitCommand(dir, "config", "user.name", "Cleanroom Agent").Run()

	srcDir := filepath.Join(dir, "substrate", "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("failed creating src dir: %v", err)
	}
	pyCode := "\"\"\"Brownfield Python service.\"\"\"\n\ndef compute_score(val: int) -> int:\n    \"\"\"Compute score.\"\"\"\n    if val > 0:\n        return val * 2\n    return 0\n"
	if err := os.WriteFile(filepath.Join(srcDir, "service.py"), []byte(pyCode), 0644); err != nil {
		t.Fatalf("failed writing service.py: %v", err)
	}
	_ = workspace.SafeGitCommand(dir, "add", ".").Run()
	_ = workspace.SafeGitCommand(dir, "commit", "-m", "chore: initial brownfield commit").Run()
}

// @verifies TC-INIT-01
func TestExecuteInit_DayZeroBoundaryInferenceAndMnemeSeed(t *testing.T) {
	t.Setenv("NOMOS_HOME", t.TempDir())
	repoDir := filepath.Join(t.TempDir(), "hex-brownfield")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatalf("mkdir repoDir: %v", err)
	}
	if err := workspace.SafeGitCommand(repoDir, "init", "-b", "main").Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	_ = workspace.SafeGitCommand(repoDir, "config", "user.email", "dev@nomos.local").Run()
	_ = workspace.SafeGitCommand(repoDir, "config", "user.name", "Hex Tester").Run()

	mustWriteLayeredFixture(t, repoDir, "src/domain/order.py", "\"\"\"Domain order.\"\"\"\n\nclass Order:\n    \"\"\"Order entity.\"\"\"\n    pass\n")
	mustWriteLayeredFixture(t, repoDir, "src/infra/store.py", "\"\"\"Infra store.\"\"\"\nfrom src.domain.order import Order\n\ndef load() -> Order:\n    \"\"\"Load order.\"\"\"\n    return Order()\n")
	_ = workspace.SafeGitCommand(repoDir, "add", ".").Run()
	_ = workspace.SafeGitCommand(repoDir, "commit", "-m", "chore: initial layered repo").Run()

	if err := executeInit(repoDir); err != nil {
		t.Fatalf("executeInit failed: %v", err)
	}
	if err := executeInit(repoDir); err != nil {
		t.Fatalf("second idempotent executeInit failed: %v", err)
	}

	rawCfg, err := os.ReadFile(filepath.Join(repoDir, "nomos.yaml"))
	if err != nil {
		t.Fatalf("read nomos.yaml: %v", err)
	}
	if !strings.Contains(string(rawCfg), "src-domain-purity") || !strings.Contains(string(rawCfg), "src/infra/**") {
		t.Fatalf("expected inferred src-domain-purity boundary in nomos.yaml, got:\n%s", string(rawCfg))
	}
	if err := runVerification(repoDir, "", true, false, false); err != nil {
		t.Fatalf("expected 0 Day-0 gate failures on inferred boundaries, got: %v", err)
	}

	ev, hErr := handshake.SynthesizeDynamicHandshake(repoDir)
	if hErr != nil {
		t.Fatalf("SynthesizeDynamicHandshake failed: %v", hErr)
	}
	if len(ev.VerifiedPatterns) != 1 || !strings.Contains(ev.VerifiedPatterns[0], "src-domain-purity") {
		t.Fatalf("expected Day-0 Mneme seed surfaced in VerifiedPatterns, got: %v", ev.VerifiedPatterns)
	}
}

func mustWriteLayeredFixture(t *testing.T, root, rel, body string) {
	t.Helper()
	dst := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(dst, []byte(body), 0644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}
