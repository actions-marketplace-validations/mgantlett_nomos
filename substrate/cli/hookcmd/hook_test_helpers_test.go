package hookcmd_test

import (
	"bytes"
	"encoding/json"
	"github.com/mgantlett/nomos/substrate/cli/hookcmd/shellguard"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/cli/hookcmd"
)

func executeHookCmd(t *testing.T, args []string, payload map[string]any) string {
	t.Helper()
	cmd := hookcmd.NewHookCmd("test", nil)
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

func parseDecision(t *testing.T, raw string) shellguard.HookDecision {
	t.Helper()
	var dec shellguard.HookDecision
	if err := json.Unmarshal([]byte(raw), &dec); err != nil {
		t.Fatalf("unmarshal decision %q: %v", raw, err)
	}
	return dec
}
