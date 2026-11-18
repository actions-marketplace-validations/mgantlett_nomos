package hookcmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
)

// @verifies TC-BG-08
// @verifies TC-BG-11
func TestContainerStopAndHeredocSQLPrecision(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	ws := t.TempDir()

	safeHeredoc := "cat > rebuild.py <<'E'\ndef drop_col(conn):\n    conn.execute(\"DROP TABLE old_tbl\")\nE"
	outSafe := executeHookCmd(t, []string{"pre-tool", "--container-sandbox"}, map[string]any{
		"session_id":      "r5-heredoc-safe",
		"hook_event_name": "PreToolUse",
		"cwd":             ws,
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": safeHeredoc},
	})
	if strings.Contains(outSafe, `"deny"`) {
		t.Fatalf("expected static cat heredoc source write with DROP TABLE string to be allowed, got %s", outSafe)
	}

	outDirectSQL := executeHookCmd(t, []string{"pre-tool", "--container-sandbox"}, map[string]any{
		"session_id":      "r5-sql-direct",
		"hook_event_name": "PreToolUse",
		"cwd":             ws,
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": `sqlite3 app.db "DROP TABLE staging;"`},
	})
	if !strings.Contains(outDirectSQL, `"deny"`) {
		t.Fatalf("expected direct sqlite3 DROP TABLE execution to be denied, got %s", outDirectSQL)
	}

	pipedHeredoc := "cat <<'E' | sqlite3 app.db\nDROP TABLE staging;\nE"
	outPipedSQL := executeHookCmd(t, []string{"pre-tool", "--container-sandbox"}, map[string]any{
		"session_id":      "r5-sql-piped",
		"hook_event_name": "PreToolUse",
		"cwd":             ws,
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": pipedHeredoc},
	})
	if !strings.Contains(outPipedSQL, `"deny"`) {
		t.Fatalf("expected piped heredoc DROP TABLE into sqlite3 to be denied, got %s", outPipedSQL)
	}

	uninitRepo := workspace.InitHermeticGitRepo(t)
	if gOut, gErr := workspace.SafeGitCommand(uninitRepo, "checkout", "-b", "feat/uninit").CombinedOutput(); gErr != nil {
		t.Fatalf("checkout: %v (%s)", gErr, string(gOut))
	}
	if err := os.WriteFile(filepath.Join(uninitRepo, "app.py"), []byte("def f():\n    return 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	stopUninit := executeHookCmd(t, []string{"stop", "--container-sandbox"}, map[string]any{
		"hook_event_name": "Stop",
		"cwd":             uninitRepo,
	})
	if stopUninit != "{}" {
		t.Fatalf("expected uninitialized container without nomos.yaml to stay silent {}, got %s", stopUninit)
	}

	if err := os.WriteFile(filepath.Join(uninitRepo, "broken.py"), []byte("def broken(:\n    pass\n"), 0644); err != nil {
		t.Fatal(err)
	}
	stopSyntax := executeHookCmd(t, []string{"stop", "--container-sandbox"}, map[string]any{
		"hook_event_name": "Stop",
		"cwd":             uninitRepo,
	})
	if dec := parseDecision(t, stopSyntax); dec.Decision != "block" || strings.Contains(dec.Reason, "Headless autonomous session") {
		t.Fatalf("expected uninitialized syntax Stop block without nomos.yaml headless mandate, got %+v (%s)", dec, stopSyntax)
	}
}
