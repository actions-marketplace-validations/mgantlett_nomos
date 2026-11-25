package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mgantlett/nomos/substrate/cli/hookcmd"
)

func TestParseSelectedAgents_ValidationAndAllExpansion(t *testing.T) {
	all, err := parseSelectedAgents("all")
	if err != nil {
		t.Fatalf("parseSelectedAgents(all) failed: %v", err)
	}
	if !reflect.DeepEqual(all, canonicalInitAgents) {
		t.Fatalf("expected all canonical agents %v, got %v", canonicalInitAgents, all)
	}

	sub, sErr := parseSelectedAgents("Claude, cursor, claude")
	if sErr != nil {
		t.Fatalf("parseSelectedAgents(claude,cursor) failed: %v", sErr)
	}
	if !reflect.DeepEqual(sub, []string{agentClaude, agentCursor}) {
		t.Fatalf("expected deduplicated [claude cursor], got %v", sub)
	}

	if _, bErr := parseSelectedAgents("claude,bogus"); bErr == nil {
		t.Fatalf("expected unknown agent token to fail closed")
	}
	if _, eErr := parseSelectedAgents(" , "); eErr == nil {
		t.Fatalf("expected empty comma list to fail closed")
	}
}

// @verifies TC-INIT-09
func TestDetectInstalledAgents_RepoSignalsHomeAndZeroDumpFallback(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	emptyDir := t.TempDir()
	fallback, fErr := resolveInitAgents(emptyDir, "")
	if fErr != nil || len(fallback) != 0 {
		t.Fatalf("TC-INIT-09: expected empty agent slice when no repo/PATH/HOME signals exist, got %v (err=%v)", fallback, fErr)
	}

	// Verify $HOME/.cursor auto-detection works without cursor on PATH.
	homeWithCursor := t.TempDir()
	if err := os.MkdirAll(filepath.Join(homeWithCursor, hookcmd.CursorDirToken), 0755); err != nil {
		t.Fatalf("mkdir ~/.cursor: %v", err)
	}
	t.Setenv("HOME", homeWithCursor)
	homeDetected, hErr := resolveInitAgents(emptyDir, "")
	if hErr != nil || !reflect.DeepEqual(homeDetected, []string{agentCursor}) {
		t.Fatalf("TC-INIT-09: expected [cursor] from $HOME/.cursor, got %v (err=%v)", homeDetected, hErr)
	}

	t.Setenv("HOME", t.TempDir())
	claudeDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(claudeDir, claudeDirToken), 0755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	detected, dErr := resolveInitAgents(claudeDir, "")
	if dErr != nil {
		t.Fatalf("resolveInitAgents failed: %v", dErr)
	}
	if !reflect.DeepEqual(detected, []string{agentClaude}) {
		t.Fatalf("expected detected [claude] only, got %v", detected)
	}

	if err := scaffoldAmbientHooksForSelection(claudeDir, []string{agentClaude}); err != nil {
		t.Fatalf("scaffoldAmbientHooksForSelection failed: %v", err)
	}
	if !fileExists(filepath.Join(claudeDir, claudeDirToken, settingsFileName)) {
		t.Fatalf("expected claude settings manifest scaffolded")
	}
	if fileExists(filepath.Join(claudeDir, hookcmd.CursorDirToken, hookcmd.HooksFileName)) {
		t.Fatalf("did not expect cursor hooks scaffolded when only claude selected")
	}
	if !reflect.DeepEqual(detectExistingHookAgents(claudeDir), []string{agentClaude}) {
		t.Fatalf("expected detectExistingHookAgents=[claude], got %v", detectExistingHookAgents(claudeDir))
	}
}
