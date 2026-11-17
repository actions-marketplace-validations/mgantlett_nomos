// Package hookcmd implements plain-text brownfield topology and Turn-0 handshake formatting.
// @intent task_lifecycle_cli_spec.md
// @intent nomos_intent_traceability_spec.md
package hookcmd

import (
	"fmt"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/types"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/handshake"
)

// BrownfieldKV represents a single label-value line in brownfield handshake banners.
type BrownfieldKV struct {
	Label string
	Value string
}

// EffectiveBriefScope returns the configured brownfield verify_scope or "diff".
// @intent task_lifecycle_cli_spec.md
func EffectiveBriefScope(bf *handshake.BrownfieldTopologyBrief) string {
	if bf != nil && strings.TrimSpace(bf.VerifyScope) != "" {
		return bf.VerifyScope
	}
	return "diff"
}

// BuildBrownfieldTopologyPairs formats stack, topology, shared modules, boundaries, and hotspots.
// @intent task_lifecycle_cli_spec.md
func BuildBrownfieldTopologyPairs(bf *handshake.BrownfieldTopologyBrief) []BrownfieldKV {
	if bf == nil {
		return nil
	}
	var pairs []BrownfieldKV
	stack := strings.Join(bf.Languages, ", ")
	if len(bf.TestRunners) > 0 {
		stack += " | test: " + strings.Join(bf.TestRunners, "; ")
	}
	if stack != "" {
		pairs = append(pairs, BrownfieldKV{Label: "Stack & Runners", Value: stack})
	}
	if len(bf.SourceRoots) > 0 {
		topo := strings.Join(bf.SourceRoots, ", ")
		if len(bf.TestRoots) > 0 {
			topo += " (tests: " + strings.Join(bf.TestRoots, ", ") + ")"
		}
		pairs = append(pairs, BrownfieldKV{Label: "Source Topology", Value: topo})
	}
	if len(bf.SharedModules) > 0 {
		pairs = append(pairs, BrownfieldKV{Label: "Shared Modules", Value: strings.Join(bf.SharedModules, ", ")})
	}
	if len(bf.ActiveBoundaries) > 0 {
		pairs = append(pairs, BrownfieldKV{Label: "Layer Boundaries", Value: strings.Join(bf.ActiveBoundaries, "; ")})
	}
	if len(bf.Hotspots) > 0 {
		spots := make([]string, 0, len(bf.Hotspots))
		for _, h := range bf.Hotspots {
			spots = append(spots, fmt.Sprintf("%s (%d LOC)", h.Path, h.Lines))
		}
		pairs = append(pairs, BrownfieldKV{Label: "Core Modules", Value: strings.Join(spots, ", ")})
	}
	return pairs
}

func appendPlainBrownfieldTopology(b *strings.Builder, ev *handshake.HandshakeEvent) {
	if ev == nil || ev.StrictMode || ev.BrownfieldTopology == nil {
		return
	}
	bf := ev.BrownfieldTopology
	b.WriteString(fmt.Sprintf("  Governance Mode   : BROWNFIELD (scope=%s, tdd=%s, cov=%s)\n",
		EffectiveBriefScope(bf), bf.TDDMode, bf.CoverageMode))
	for _, item := range BuildBrownfieldTopologyPairs(bf) {
		b.WriteString(fmt.Sprintf("  %-17s : %s\n", item.Label, item.Value))
	}
}

func appendPlainBoundedList(b *strings.Builder, label, icon string, items []string, limit int) {
	if len(items) == 0 {
		return
	}
	b.WriteString(fmt.Sprintf("  %-17s : %d active\n", label, len(items)))
	for i, item := range items {
		if i >= limit {
			break
		}
		b.WriteString(fmt.Sprintf("    %s %s\n", icon, item))
	}
}

// appendPlainTurnZeroTelemetry injects deterministic AST impact, Mneme dead-ends/pitfalls,
// verified blueprints, and HATEOAS next actions into the Turn-0 SessionStart prefix cache.
func appendPlainTurnZeroTelemetry(b *strings.Builder, ev *handshake.HandshakeEvent) {
	appendPlainTurnZeroTelemetryWithMode(b, ev, false)
}

func appendPlainTurnZeroTelemetryWithMode(b *strings.Builder, ev *handshake.HandshakeEvent, containerSandbox bool) {
	if ev == nil {
		return
	}
	appendPlainBoundedList(b, "AST Impact", "↪", ev.ImpactSummary, 4)
	if len(ev.StaleObligations) > 0 {
		paths := make([]string, 0, len(ev.StaleObligations))
		for i, s := range ev.StaleObligations {
			if i >= 3 {
				break
			}
			paths = append(paths, s.SpecPath)
		}
		b.WriteString(fmt.Sprintf("  Stale Specs       : %s\n", strings.Join(paths, ", ")))
	}
	appendPlainBoundedList(b, "Learned Pitfalls", "⚡", ev.LearnedPitfalls, 3)
	appendPlainBoundedList(b, "Verified Patterns", "🧩", ev.VerifiedPatterns, 2)
	appendPlainNextActions(b, ev.NextActions, containerSandbox)
}

func appendPlainNextActions(b *strings.Builder, actions []types.ActionAffordance, containerSandbox bool) {
	var filtered []types.ActionAffordance
	for _, act := range actions {
		if containerSandbox && strings.HasPrefix(strings.TrimSpace(act.Command), "git commit") {
			continue
		}
		filtered = append(filtered, act)
	}
	if len(filtered) == 0 {
		return
	}
	b.WriteString("  Next Actions      :\n")
	for _, act := range filtered {
		b.WriteString(fmt.Sprintf("    → %s (%s)\n", act.Command, act.Summary))
	}
}
