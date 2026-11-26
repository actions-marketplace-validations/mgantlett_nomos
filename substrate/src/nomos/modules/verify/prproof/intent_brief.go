// Package prproof builds the 15-Second Architectural Intent Brief for human merge-gate decisions (Law 2 & Law 4).
// @intent nomos_intent_traceability_spec.md
package prproof

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
)

var (
	diffContractDeclRe = regexp.MustCompile(`^\+\s*(?:export\s+)?(?:type|interface|class|struct|record)\s+([A-Z][A-Za-z0-9_]*)\b`)
	diffZodDeclRe      = regexp.MustCompile(`^\+\s*(?:export\s+)?const\s+([A-Z][A-Za-z0-9_]*)\s*=\s*z\.(?:strictObject|looseObject|object)\b`)
	diffTCVectorRe     = regexp.MustCompile(`TC-[A-Z0-9]+-[0-9]+\b`)
)

var depOrBoundaryManifests = map[string]bool{
	"go.mod": true, "go.sum": true, "package.json": true, "cargo.toml": true,
	"pyproject.toml": true, "requirements.txt": true, "pom.xml": true,
	"nomos.yaml": true, "nomos.schema.json": true,
}

// BuildArchitecturalIntentBrief summarizes mutated contracts, dependency/boundary shifts,
// TC-* obligation deltas, and mechanical debt posture against baseRef.
// @intent nomos_intent_traceability_spec.md #ArchitecturalIntentBrief
func BuildArchitecturalIntentBrief(repoRoot, baseRef string, zeroMechanicalDebt bool) *ArchitecturalIntentBrief {
	effBase := resolveEffectiveBaseRef(repoRoot, baseRef)
	rawFilesBytes, err := workspace.RunSafeGit(repoRoot, "diff", "--name-only", effBase, "--")
	if err != nil {
		rawFilesBytes = nil
	}
	rawDiffBytes, dErr := workspace.RunSafeGit(repoRoot, "diff", "--unified=0", effBase, "--")
	if dErr != nil {
		rawDiffBytes = nil
	}
	contracts := make(map[string]bool)
	deps := make(map[string]bool)
	collectIntentBriefFileSignals(string(rawFilesBytes), contracts, deps)
	obligations := make(map[string]bool)
	collectIntentBriefDiffSignals(string(rawDiffBytes), contracts, obligations)

	return &ArchitecturalIntentBrief{
		ContractsMutated:   sortedKeysBounded(contracts, 12),
		DepsOrBoundaries:   sortedKeysBounded(deps, 12),
		ObligationsDelta:   sortedKeysBounded(obligations, 12),
		ZeroMechanicalDebt: zeroMechanicalDebt,
	}
}

func collectIntentBriefFileSignals(rawFiles string, contracts, deps map[string]bool) {
	for _, line := range strings.Split(strings.TrimSpace(rawFiles), "\n") {
		rel := filepath.ToSlash(strings.TrimSpace(line))
		if rel == "" {
			continue
		}
		base := strings.ToLower(filepath.Base(rel))
		if depOrBoundaryManifests[base] {
			deps[rel] = true
		}
		if strings.HasSuffix(rel, "_spec.md") || strings.HasSuffix(rel, ".schema.json") {
			contracts[rel] = true
		}
	}
}

func collectIntentBriefDiffSignals(rawDiff string, contracts, obligations map[string]bool) {
	for _, line := range strings.Split(rawDiff, "\n") {
		if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
			continue
		}
		if m := diffContractDeclRe.FindStringSubmatch(line); len(m) == 2 {
			contracts["#"+m[1]] = true
		} else if m := diffZodDeclRe.FindStringSubmatch(line); len(m) == 2 {
			contracts["#"+m[1]] = true
		}
		if strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") {
			prefix := string(line[0])
			for _, tc := range diffTCVectorRe.FindAllString(line, -1) {
				obligations[prefix+tc] = true
			}
		}
	}
}

func sortedKeysBounded(m map[string]bool, limit int) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	if limit > 0 && len(out) > limit {
		return out[:limit]
	}
	return out
}

// FormatArchitecturalIntentBrief renders a compact 15-second Markdown architectural decision card.
// @intent nomos_intent_traceability_spec.md #ArchitecturalIntentBrief
func FormatArchitecturalIntentBrief(brief *ArchitecturalIntentBrief) string {
	if brief == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("### 15-Second Architectural Intent Brief\n\n")
	b.WriteString(formatBriefBullet("Contracts & Surfaces", brief.ContractsMutated, "none"))
	b.WriteString(formatBriefBullet("Dependencies & Boundaries", brief.DepsOrBoundaries, "none"))
	b.WriteString(formatBriefBullet("Obligation Delta (`TC-*`)", brief.ObligationsDelta, "none"))
	seal := "FAIL (structural or gate regression detected)"
	if brief.ZeroMechanicalDebt {
		seal = "SEALED (zero mechanical debt added)"
	}
	fmt.Fprintf(&b, "- **Mechanical Debt Seal:** %s\n", seal)
	return b.String()
}

func formatBriefBullet(label string, items []string, emptyLabel string) string {
	if len(items) == 0 {
		return fmt.Sprintf("- **%s:** %s\n", label, emptyLabel)
	}
	formatted := make([]string, len(items))
	for i, it := range items {
		formatted[i] = "`" + it + "`"
	}
	return fmt.Sprintf("- **%s:** %s\n", label, strings.Join(formatted, ", "))
}
