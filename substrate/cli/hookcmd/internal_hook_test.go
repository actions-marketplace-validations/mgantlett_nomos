package hookcmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runInternalHook(t *testing.T, args []string, payload map[string]any) string {
	t.Helper()
	cmd := NewHookCmd("test", nil)
	var outBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&bytes.Buffer{})
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	cmd.SetIn(bytes.NewReader(raw))
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("hookcmd %v failed: %v", args, err)
	}
	return strings.TrimSpace(outBuf.String())
}

func TestHookPostTool_VerifiedHostHealthSnapshotsAndDeduplication(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	sandboxDir := t.TempDir()

	cleanOut := runInternalHook(t, []string{"post-tool", "--hermetic-dir", sandboxDir}, map[string]any{
		"session_id":      "sess-health-1",
		"hook_event_name": "PostToolUse",
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "echo ok"},
		"tool_response":   map[string]any{"stdout": "ok\n", "stderr": ""},
	})
	if cleanOut != "{}" {
		t.Fatalf("expected silent '{}' on clean Bash response, got %s", cleanOut)
	}

	cases := []struct {
		name      string
		eventName string
		toolName  string
		cmdLine   string
		stderr    string
		wantSub   string
	}{
		{
			name:      "dns_offline",
			eventName: "PostToolUse",
			toolName:  "Bash",
			cmdLine:   "pip install numpy",
			stderr:    "WARNING: Retrying: Temporary failure in name resolution",
			wantSub:   "Network egress or DNS resolution is unavailable",
		},
		{
			name:      "readonly_erofs",
			eventName: "PostToolUse",
			toolName:  "Bash",
			cmdLine:   "touch /app/out",
			stderr:    "touch: cannot touch '/app/out': Read-only file system",
			wantSub:   "Target mount is read-only",
		},
		{
			name:      "missing_bin",
			eventName: "AfterTool",
			toolName:  "run_shell_command",
			cmdLine:   "sudo rg pattern src/",
			stderr:    "sh: 1: rg: command not found",
			wantSub:   "Executable 'rg' is not installed",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := map[string]any{
				"session_id":      "sess-health-dedup",
				"hook_event_name": tc.eventName,
				"tool_name":       tc.toolName,
				"tool_input":      map[string]any{"command": tc.cmdLine},
				"tool_response":   map[string]any{"stderr": tc.stderr},
			}
			first := runInternalHook(t, []string{"post-tool", "--hermetic-dir", sandboxDir}, payload)
			if !strings.Contains(first, tc.wantSub) {
				t.Fatalf("expected %q in first fault response, got %s", tc.wantSub, first)
			}
			repeat := runInternalHook(t, []string{"post-tool", "--hermetic-dir", sandboxDir}, payload)
			if repeat != "{}" {
				t.Fatalf("expected deduplicated silent '{}' on repeat fault, got %s", repeat)
			}
		})
	}
}

// @verifies TC-LRN-13
func TestHookPostTool_SlowStepTimeoutGuardAndWorktreeChurn(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	sandboxDir := t.TempDir()
	now := time.Now().Unix()

	p := &HookPayload{SessionID: "sess-slow-1", CommandLine: "pytest -q"}
	statePath := resolveBudgetStateFilePath(p, sandboxDir)
	saveBudgetState(statePath, sessionBudgetState{
		StartedUnix:     now - 60,
		LastPreToolUnix: now - 50,
	})

	skipOut := runInternalHook(t, []string{"post-tool", "--hermetic-dir", sandboxDir, "--budget-sec", "180"}, map[string]any{
		"session_id":      "sess-slow-1",
		"hook_event_name": "PostToolUse",
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "timeout 60s pytest -q"},
		"tool_response":   map[string]any{"stdout": "ok\n"},
	})
	if skipOut != "{}" {
		t.Fatalf("expected explicit timeout command to skip slow-step advisory, got %s", skipOut)
	}

	saveBudgetState(statePath, sessionBudgetState{
		StartedUnix:     now - 60,
		LastPreToolUnix: now - 50,
	})
	first := runInternalHook(t, []string{"post-tool", "--hermetic-dir", sandboxDir, "--budget-sec", "180"}, map[string]any{
		"session_id":      "sess-slow-1",
		"hook_event_name": "PostToolUse",
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "pytest -q"},
		"tool_response":   map[string]any{"stdout": "ok\n"},
	})
	if !strings.Contains(first, "[Nomos Slow-Step Timeout Radar]") {
		t.Fatalf("expected slow-step timeout advisory, got %s", first)
	}

	wtDir := filepath.Join(t.TempDir(), "worktrees", "demo-SUB-88")
	if err := os.MkdirAll(wtDir, 0755); err != nil {
		t.Fatalf("mkdir wtDir: %v", err)
	}
	payload := map[string]any{
		"session_id":      "sess-churn-88",
		"hook_event_name": "PostToolUse",
		"cwd":             wtDir,
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "git status --short"},
		"tool_response":   map[string]any{"stdout": ""},
	}
	for i := 1; i <= 74; i++ {
		_ = runInternalHook(t, []string{"post-tool"}, payload)
	}
	warn75 := runInternalHook(t, []string{"post-tool"}, payload)
	if !strings.Contains(warn75, "[Nomos Worktree Churn Radar]") {
		t.Fatalf("expected 75-call churn advisory, got %s", warn75)
	}
	_ = runInternalHook(t, []string{"post-tool"}, map[string]any{
		"session_id":      "sess-churn-88",
		"hook_event_name": "PostToolUse",
		"cwd":             wtDir,
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "nomos verify"},
		"tool_response":   map[string]any{"stdout": "42/42 passed"},
	})
	if tr := loadHookSessionTrace(wtDir); tr == nil || tr.UnverifiedCalls != 0 || tr.WarnedChurn75 {
		t.Fatalf("expected nomos verify to reset churn state, got %+v", tr)
	}
}
