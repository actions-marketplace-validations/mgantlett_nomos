package hookcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
)

// TestEvaluateTrunkShellRedirectGuard_HeredocPrecision verifies non-shell heredoc '>' comparisons pass
// while direct trunk redirects and shell-interpreter heredocs remain blocked.
func TestEvaluateTrunkShellRedirectGuard_HeredocPrecision(t *testing.T) {
	repoRoot := filepath.Join(t.TempDir(), "trunk-repo")
	if err := os.MkdirAll(repoRoot, 0750); err != nil {
		t.Fatal(err)
	}
	if out, err := workspace.SafeGitCommand(repoRoot, "init", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v (%s)", err, string(out))
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "nomos.yaml"), []byte("project: trunk-repo\ndefault_branch: main\n"), 0600); err != nil {
		t.Fatal(err)
	}

	pyCompare := "python3 - <<'EOF'\ns = 'abc'\nif len(s) > 2200:\n    print(s)\nEOF"
	if dec := evaluateTrunkShellRedirectGuard(pyCompare, repoRoot, ""); dec.Decision != hookDecisionAllow {
		t.Fatalf("expected python heredoc '>' numeric comparison allowed on trunk, got %+v", dec)
	}

	for _, blockedCmd := range []string{
		"echo x > leaked.txt",
		"cat > leaked.txt <<'EOF'\nhello\nEOF",
		"bash <<'EOF'\necho x > leaked.txt\nEOF",
		"cat <<'EOF' | sh\necho x > leaked.txt\nEOF",
	} {
		dec := evaluateTrunkShellRedirectGuard(blockedCmd, repoRoot, "")
		if dec.Decision != hookDecisionDeny || !strings.Contains(dec.Reason, "Shell redirect targeting protected trunk path") {
			t.Fatalf("expected %q denied on trunk, got %+v", blockedCmd, dec)
		}
	}
}
