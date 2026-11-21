package hookcmd

import (
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/types"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/handshake"
)

// TestFormatPlainHandshakeBanner_ActiveWorktrees verifies plain SessionStart banners surface isolated sibling worktrees.
// @verifies TC-JIT-05
func TestFormatPlainHandshakeBanner_ActiveWorktrees(t *testing.T) {
	ev := &handshake.HandshakeEvent{
		Seat:            "seat-local",
		Repository:      "demo",
		Phase:           types.PhaseIdle,
		ActiveWorktrees: []string{"INT-102", "SUB-205"},
		GateCount:       42,
	}
	rendered := FormatPlainHandshakeBanner(ev)
	if !strings.Contains(rendered, "Active Worktrees  : [INT-102, SUB-205] (isolated - safe to run parallel tasks)") {
		t.Fatalf("expected Active Worktrees concurrency guidance in plain banner, got:\n%s", rendered)
	}
}
