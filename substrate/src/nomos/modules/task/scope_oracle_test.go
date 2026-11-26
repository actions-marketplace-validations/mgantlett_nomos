package task

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
)

// @verifies TC-TSK-23
func TestEvaluateTaskWriteBoundaries_HeldOutOracleLock(t *testing.T) {
	repo := t.TempDir()
	runScopeGit(t, repo, "init")
	runScopeGit(t, repo, "config", "user.email", "test@example.com")
	runScopeGit(t, repo, "config", "user.name", "Test")

	writeScopeFile(t, repo, "pkg/calc/calc.go", "package calc\nfunc Add(a, b int) int { return a + b }\n")
	writeScopeFile(t, repo, "pkg/calc/calc_test.go", "package calc\nimport \"testing\"\nfunc TestAdd(t *testing.T) {}\n")
	writeScopeFile(t, repo, "tests/oracle/contract_test.go", "package oracle\n")
	runScopeGit(t, repo, "add", ".")
	runScopeGit(t, repo, "commit", "-m", "seed repo")

	lockedRec := &TaskRecord{Key: "SUB-23", Plane: "substrate", LockTests: true}
	if reason := EvaluateTaskWriteBoundaries(repo, "SUB-23", "pkg/calc/calc_test.go", lockedRec, nil); !strings.Contains(reason, "Held-Out Verification Oracle Violation") {
		t.Fatalf("expected --lock-tests to block editing committed test file, got %q", reason)
	}
	if reason := EvaluateTaskWriteBoundaries(repo, "SUB-23", "pkg/calc/calc_extra_test.go", lockedRec, nil); reason != "" {
		t.Fatalf("expected --lock-tests to permit net-new test file, got %q", reason)
	}
	if reason := EvaluateTaskWriteBoundaries(repo, "SUB-23", "pkg/calc/calc.go", lockedRec, nil); reason != "" {
		t.Fatalf("expected --lock-tests to permit production source file, got %q", reason)
	}

	unlockedRec := &TaskRecord{Key: "SUB-24", Plane: "substrate", LockTests: false}
	if reason := EvaluateTaskWriteBoundaries(repo, "SUB-24", "tests/oracle/contract_test.go", unlockedRec, []string{"tests/oracle/**"}); !strings.Contains(reason, "Immutable Verification Oracle Violation") {
		t.Fatalf("expected immutable_test_paths to block SUB edit, got %q", reason)
	}
	if reason := EvaluateTaskWriteBoundaries(repo, "INT-24", "tests/oracle/contract_test.go", nil, []string{"tests/oracle/**"}); reason != "" {
		t.Fatalf("expected immutable_test_paths not to trigger on INT task, got %q", reason)
	}
}

// @verifies TC-TSK-24
func TestEvaluateTaskWriteBoundaries_HardScopeWriteFence(t *testing.T) {
	rec := &TaskRecord{
		Key:        "SUB-24",
		Plane:      "substrate",
		ScopePaths: []string{"src/auth/login.ts", "pkg/billing/**"},
	}
	cases := []struct {
		name      string
		relPath   string
		wantBlock bool
	}{
		{name: "exact_scoped_file_allowed", relPath: "src/auth/login.ts", wantBlock: false},
		{name: "paired_ts_test_allowed", relPath: "src/auth/login.test.ts", wantBlock: false},
		{name: "paired_dunder_tests_allowed", relPath: "src/auth/__tests__/login.test.ts", wantBlock: false},
		{name: "glob_dir_file_allowed", relPath: "pkg/billing/invoice.go", wantBlock: false},
		{name: "scratch_tmp_allowed", relPath: ".tmp/walkthrough-SUB-24.md", wantBlock: false},
		{name: "unrelated_file_blocked", relPath: "src/ui/navbar.ts", wantBlock: true},
		{name: "unrelated_test_blocked", relPath: "src/ui/navbar.test.ts", wantBlock: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason := EvaluateTaskWriteBoundaries("", "SUB-24", tc.relPath, rec, nil)
			if tc.wantBlock && !strings.Contains(reason, "Hard Scope Fence Violation") {
				t.Fatalf("expected Hard Scope Fence Violation for %s, got %q", tc.relPath, reason)
			}
			if !tc.wantBlock && reason != "" {
				t.Fatalf("expected %s to be permitted, got %q", tc.relPath, reason)
			}
		})
	}
}

func writeScopeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func runScopeGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := workspace.SafeGitCommand(dir, args...).CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v (%s)", args, err, string(out))
	}
}
