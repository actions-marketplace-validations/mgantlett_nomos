package verify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/astdebt"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

// @verifies TC-TS-24
func TestR5_VenvSymlinksAllowedWhileRepoSymlinkBlocked(t *testing.T) {
	dir := initTestGitRepo(t)
	if out, err := workspace.SafeGitCommand(dir, "checkout", "-b", "feat/r5-sym").CombinedOutput(); err != nil {
		t.Fatalf("checkout: %v (%s)", err, string(out))
	}
	mustWriteVerifyFile(t, filepath.Join(dir, "app.py"), "def run():\n    return 1\n")
	if err := os.MkdirAll(filepath.Join(dir, ".venv", "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "app.py"), filepath.Join(dir, ".venv", "bin", "python")); err != nil {
		t.Fatal(err)
	}

	ctx := &vscope.VerifyContext{RepoRoot: dir, WorktreePath: dir}
	cleanRes, err := (&ZeroFilesystemAliasGate{}).Evaluate(ctx)
	if err != nil || !cleanRes.Passed {
		t.Fatalf("expected untracked .venv symlink to be ignored by ZeroFilesystemAliasGate, got err=%v res=%+v", err, cleanRes)
	}

	if err := os.MkdirAll(filepath.Join(dir, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "app.py"), filepath.Join(dir, "src", "bad_link.py")); err != nil {
		t.Fatal(err)
	}
	badRes, bErr := (&ZeroFilesystemAliasGate{}).Evaluate(ctx)
	if bErr != nil || badRes.Passed || !strings.Contains(badRes.Message, "bad_link.py") {
		t.Fatalf("expected src/bad_link.py symlink to fail ZeroFilesystemAliasGate, got err=%v res=%+v", bErr, badRes)
	}
}

// @verifies TC-TS-14
// @verifies TC-TS-24
func TestR5_QuotedDDLMigrationCWE89Precision(t *testing.T) {
	quotedDDL := `def q(ident):
    return '"' + ident.replace('"', '""') + '"'

def rebuild(conn, src, dst, cols):
    col_sql = ", ".join(q(c) for c in cols)
    conn.execute(f"INSERT INTO {q(dst)} ({col_sql}) SELECT {col_sql} FROM {q(src)}")
    conn.execute(f"DROP TABLE {q(src)}")
`
	if hits := DetectPolyglotCWESinks("rebuild.py", []byte(quotedDDL)); len(hits) != 0 {
		t.Fatalf("expected identifier-quoted DDL migration to pass CWE-89, got %v", hits)
	}

	badValuesSQL := `def q(ident):
    return '"' + ident.replace('"', '""') + '"'

def insert_user(conn, tbl, val):
    conn.execute(f"INSERT INTO {q(tbl)} VALUES ('{val}')")
`
	if hits := DetectPolyglotCWESinks("insert.py", []byte(badValuesSQL)); len(hits) == 0 || !strings.Contains(hits[0], "CWE-89") {
		t.Fatalf("expected VALUES interpolation to trigger CWE-89 even with q() present, got %v", hits)
	}

	rawTableSQL := "def wipe(conn, tbl):\n    conn.execute(f\"DELETE FROM {tbl}\")\n"
	if hits := DetectPolyglotCWESinks("wipe.py", []byte(rawTableSQL)); len(hits) == 0 || !strings.Contains(hits[0], "CWE-89") {
		t.Fatalf("expected unquoted dynamic DELETE FROM {tbl} to still trigger CWE-89, got %v", hits)
	}
}

// @verifies TC-TS-23
// @verifies TC-TS-24
func TestR5_DefensiveIsinstanceAndTypeofReturnSprawl(t *testing.T) {
	py := `def chk_a(v):
    if not isinstance(v, str):
        return ["err_a"]
    return []

def chk_b(v):
    if not isinstance(v, list): return ["err_b"]
    return []

def chk_c(v):
    if not isinstance(v, dict):
        return ["err_c"]
    return []

def chk_d(v):
    if not isinstance(v, int): return ["err_d"]
    return []
`
	hits := astdebt.DetectPolyglotDesignSlop(t.TempDir(), map[string][]byte{"chk.py": []byte(py)})
	if !strings.Contains(strings.Join(hits, "\n"), "repeats 'if not isinstance(...): return ...' across 4 functions") {
		t.Fatalf("expected defensive isinstance-return sprawl hit, got %v", hits)
	}

	ts := `export function a(v: unknown) { if (typeof v !== "string") { return ["a"]; } return []; }
export function b(v: unknown) { if (typeof v !== "number") { return ["b"]; } return []; }
export function c(v: unknown) { if (typeof v !== "boolean") return ["c"]; return []; }
export function d(v: unknown) { if (typeof v !== "object") { return ["d"]; } return []; }
`
	tsHits := astdebt.DetectPolyglotDesignSlop(t.TempDir(), map[string][]byte{"chk.ts": []byte(ts)})
	if !strings.Contains(strings.Join(tsHits, "\n"), "repeats 'if (typeof ... !== ...): return ...' across 4 checks") {
		t.Fatalf("expected TS typeof-return sprawl hit, got %v", tsHits)
	}

	dir := initTestGitRepo(t)
	mustWriteVerifyFile(t, filepath.Join(dir, "chk.py"), py)
	commitAllTestGit(t, dir, "feat: baseline validator")
	editedPy := py + "\ndef chk_e(v):\n    if not isinstance(v, bool): return [\"err_e\"]\n    return []\n"
	mustWriteVerifyFile(t, filepath.Join(dir, "chk.py"), editedPy)
	ctx := &vscope.VerifyContext{RepoRoot: dir, WorktreePath: dir}
	bfHits := evaluateNetNewBrownfieldClonesAndSlop(
		ctx,
		dir,
		map[string][]byte{"chk.py": []byte(editedPy)},
		map[string][]byte{"chk.py": []byte(py)},
		astdebt.DefaultThresholds(),
	)
	if len(bfHits) != 0 {
		t.Fatalf("expected pre-existing brownfield validator sprawl to remain exempt, got %v", bfHits)
	}
}
