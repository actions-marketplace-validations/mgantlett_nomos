package hookcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/cli/hookcmd/shellguard"
)

// TestEvaluateLoopCircuitBreaker_IdenticalAndEditReset verifies 3-strike loop denial and file-modification reset.
func TestEvaluateLoopCircuitBreaker_IdenticalAndEditReset(t *testing.T) {
	t.Setenv("NOMOS_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	wsDir := t.TempDir()
	srcFile := filepath.Join(wsDir, "handler.go")
	if err := os.WriteFile(srcFile, []byte("package main\nvar v = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}

	payload := &shellguard.HookPayload{ToolName: "Bash", CommandLine: "go test ./...", Cwd: wsDir}
	if dec := EvaluateLoopCircuitBreakerWithStrikes(payload, "", 3); dec.Decision != "allow" {
		t.Fatalf("strike 1 expected allow, got %+v", dec)
	}
	if dec := EvaluateLoopCircuitBreakerWithStrikes(payload, "", 3); dec.Decision != "allow" {
		t.Fatalf("strike 2 expected allow, got %+v", dec)
	}
	dec3 := EvaluateLoopCircuitBreakerWithStrikes(payload, "", 3)
	if dec3.Decision != "deny" || !strings.Contains(dec3.Reason, "Loop Circuit-Breaker") {
		t.Fatalf("strike 3 expected deny, got %+v", dec3)
	}

	if err := os.WriteFile(srcFile, []byte("package main\nvar v = 2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if decAfterEdit := EvaluateLoopCircuitBreakerWithStrikes(payload, "", 3); decAfterEdit.Decision != "allow" {
		t.Fatalf("expected edit to reset loop breaker, got %+v", decAfterEdit)
	}
}
