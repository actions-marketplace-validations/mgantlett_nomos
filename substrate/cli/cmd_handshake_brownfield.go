// Package cli implements lean brownfield session handshake formatting.
package cli

import (
	"fmt"
	"os"

	"github.com/mgantlett/nomos/substrate/cli/hookcmd"
	"github.com/mgantlett/nomos/substrate/cli/tui"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/handshake"
)

func renderBrownfieldHandshakeBanner(event *handshake.HandshakeEvent, bf *handshake.BrownfieldTopologyBrief, hookRoot string) {
	fmt.Println(tui.RenderBrandTitle("🤝 Nomos Brownfield Session Handshake"))
	fmt.Println(tui.UiRule(tui.RuleNarrow))
	fmt.Println(tui.KvLine("Active Repository", 17, tui.UiBoldValueStyle.Render(event.Repository)))
	if event.ActiveTask != "" {
		phaseVal := fmt.Sprintf("%s %s",
			tui.StyleSemanticBadge(string(event.Phase)),
			tui.UiMutedStyle.Render("(Task: "+tui.StyleTaskKey(event.ActiveTask)+")"))
		fmt.Println(tui.KvLine("Workspace Phase", 17, phaseVal))
	} else {
		phaseVal := fmt.Sprintf("%s %s",
			tui.StyleSemanticBadge(string(event.Phase)),
			tui.UiMutedStyle.Render("(Trunk baseline)"))
		fmt.Println(tui.KvLine("Workspace Phase", 17, phaseVal))
	}
	govVal := fmt.Sprintf("BROWNFIELD (scope=%s, tdd=%s, cov=%s, %d gates)",
		hookcmd.EffectiveBriefScope(bf), bf.TDDMode, bf.CoverageMode, event.GateCount)
	fmt.Println(tui.KvLine("Governance Mode", 17, tui.UiBoldValueStyle.Render(govVal)))
	for _, item := range hookcmd.BuildBrownfieldTopologyPairs(bf) {
		fmt.Println(tui.KvLine(item.Label, 17, tui.UiValueStyle.Render(item.Value)))
	}
	renderHandshakeUpdateAndDriftAdvisories(hookRoot)
	renderHandshakeMnemeTelemetry(event)
	tui.RenderNextActions(os.Stdout, event.NextActions)
	fmt.Println(tui.UiRule(tui.RuleNarrow))
}
