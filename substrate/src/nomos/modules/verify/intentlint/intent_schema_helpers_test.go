package intentlint_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/intentlint"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

func TestVerifyIntentASTAnchors_ShortCircuitsWhenNoTargetFilesHaveIntentTags(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "app.py")
	if err := os.WriteFile(src, []byte("def plain_helper():\n    return 42\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := &vscope.VerifyContext{RepoRoot: dir, WorktreePath: dir, AllFiles: true}
	if unresolved := intentlint.VerifyIntentASTAnchors(ctx, dir, dir); len(unresolved) != 0 {
		t.Fatalf("expected 0 unresolved anchors on untagged brownfield file, got %+v", unresolved)
	}

	tagged := filepath.Join(dir, "tagged.go")
	if err := os.WriteFile(tagged, []byte("package demo\n// Run (@intent #MissingType)\nfunc Run() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if unresolved := intentlint.VerifyIntentASTAnchors(ctx, dir, dir); len(unresolved) != 1 {
		t.Fatalf("expected 1 unresolved anchor when @intent #MissingType is present, got %+v", unresolved)
	}
}
