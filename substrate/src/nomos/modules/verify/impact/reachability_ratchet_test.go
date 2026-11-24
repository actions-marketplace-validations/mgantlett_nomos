package impact_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/impact"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

func TestEvaluateWorktreeUnreachedFiles_DetectsNetNewOrphan(t *testing.T) {
	dir := t.TempDir()
	mustWrite := func(rel, body string) {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mustWrite("src/routes/+page.svelte", "<script lang=\"ts\">\nimport { run } from '$lib/active';\nrun();\n</script>\n")
	mustWrite("src/lib/active.ts", "export function run(): number { return 1; }\n")

	if _, err := workspace.RunSafeGit(dir, "init", "-b", "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.RunSafeGit(dir, "config", "user.email", "test@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.RunSafeGit(dir, "config", "user.name", "Tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.RunSafeGit(dir, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.RunSafeGit(dir, "commit", "-m", "init"); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.RunSafeGit(dir, "checkout", "-b", "feature/SUB-1"); err != nil {
		t.Fatal(err)
	}

	mustWrite("src/lib/unreached_dust.ts", "export function dead(): number { return 0; }\n")
	ctx := &vscope.VerifyContext{RepoRoot: dir, WorktreePath: dir}
	violations := impact.EvaluateWorktreeUnreachedFiles(ctx, dir)
	if len(violations) != 1 {
		t.Fatalf("expected 1 unreached file violation, got %v", violations)
	}

	mustWrite("src/lib/active.ts", "import { dead } from './unreached_dust';\nexport function run(): number { return dead(); }\n")
	clean := impact.EvaluateWorktreeUnreachedFiles(ctx, dir)
	if len(clean) != 0 {
		t.Fatalf("expected 0 violations once imported, got %v", clean)
	}
}
