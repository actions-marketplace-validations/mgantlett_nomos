// Package hookcmd renders plain-text brownfield topology, Turn-0 telemetry, and compact module hulls during SessionStart.
// @intent task_lifecycle_cli_spec.md
// @intent nomos_intent_traceability_spec.md
package hookcmd

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/types"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/handshake"
)

const (
	maxHullDeps    = 10
	maxHullModules = 6
	maxHullSymbols = 4
	maxHullFiles   = 16
)

var (
	hullPyDepBlockRe  = regexp.MustCompile(`(?s)dependencies\s*=\s*\[(.*?)\]`)
	hullQuotedTokenRe = regexp.MustCompile(`["']([A-Za-z0-9][A-Za-z0-9._-]*)`)
	hullReqLineRe     = regexp.MustCompile(`^\s*([A-Za-z0-9][A-Za-z0-9._-]*)`)
	hullCargoDepSecRe = regexp.MustCompile(`(?ms)^\[dependencies\]\s*\n(.*?)(?:^\[|\z)`)
	hullCargoKeyRe    = regexp.MustCompile(`^\s*([A-Za-z0-9_-]+)\s*=`)
	hullSymbolDefRe   = regexp.MustCompile(`^(?:def|class|export\s+(?:async\s+)?function|export\s+class|pub\s+(?:async\s+)?fn|fn)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	hullSkipDirs      = map[string]bool{
		".git": true, ".venv": true, "venv": true, "node_modules": true,
		"vendor": true, "worktrees": true, "substrate": true, "intent": true,
		"__pycache__": true, ".tmp": true,
		"testdata": true, "fixtures": true, "examples": true, "samples": true, "files": true, "assets": true,
	}
	hullSourceExts = map[string]bool{".py": true, ".ts": true, ".js": true, ".rs": true}
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

// appendPlainBrownfieldTopology appends compact brownfield topology KV pairs onto the SessionStart banner.
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

// appendPlainBoundedList formats a capped bullet list for Turn-0 handshake advisories.
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

// appendPlainTurnZeroTelemetry injects deterministic AST impact, Mneme pitfalls,
// verified blueprints, and HATEOAS next actions into the Turn-0 SessionStart prefix cache.
func appendPlainTurnZeroTelemetry(b *strings.Builder, ev *handshake.HandshakeEvent) {
	appendPlainTurnZeroTelemetryWithMode(b, ev, false)
}

// appendPlainTurnZeroTelemetryWithMode injects Turn-0 telemetry with container-safe next-action filtering.
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
	var filtered []types.ActionAffordance
	for _, act := range ev.NextActions {
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

// appendPlainWorkspaceHullWithMode prints declared dependencies, compact module hulls, and test guidance.
// @intent nomos_hermetic_worktree_spec.md
func appendPlainWorkspaceHullWithMode(b *strings.Builder, hookRoot string, containerSandbox bool) {
	if strings.TrimSpace(hookRoot) == "" {
		return
	}
	if deps := discoverManifestDeps(hookRoot); len(deps) > 0 {
		b.WriteString(fmt.Sprintf("  Manifest Deps     : %s\n", strings.Join(deps, ", ")))
	}
	mods := discoverWorkspaceModuleHull(hookRoot)
	if len(mods) > 0 {
		b.WriteString(fmt.Sprintf("  Workspace Modules : %s\n", strings.Join(mods, ", ")))
	}
	if (len(mods) > 0 || containerSandbox) && !hasWorkspaceAutomatedTests(hookRoot) {
		b.WriteString("  Regression Suite  : none detected - create stdlib unittest.TestCase tests in .tmp/tests/test_<module>.py covering boundary, multi-trigger/multi-item, and error cases before 'nomos verify' (keeps workspace git-clean and enforces >=80% coverage)\n")
	}
}

// discoverManifestDeps collects bounded dependency names from Python, Node, and Rust manifests.
func discoverManifestDeps(root string) []string {
	seen := make(map[string]bool)
	var out []string
	addDep := func(raw string) {
		name := strings.TrimSpace(raw)
		if name != "" && !seen[name] && len(out) < maxHullDeps {
			seen[name] = true
			out = append(out, name)
		}
	}
	collectPythonManifestDeps(root, addDep)
	collectNodeManifestDeps(root, addDep)
	collectCargoManifestDeps(root, addDep)
	return out
}

// collectPythonManifestDeps parses pyproject.toml and requirements.txt dependency names.
func collectPythonManifestDeps(root string, addDep func(string)) {
	if raw, err := os.ReadFile(filepath.Join(root, "pyproject.toml")); err == nil {
		for _, blk := range hullPyDepBlockRe.FindAllStringSubmatch(string(raw), -1) {
			for _, m := range hullQuotedTokenRe.FindAllStringSubmatch(blk[1], -1) {
				addDep(m[1])
			}
		}
	}
	if raw, err := os.ReadFile(filepath.Join(root, "requirements.txt")); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			if idx := strings.IndexByte(line, '#'); idx >= 0 {
				line = line[:idx]
			}
			if m := hullReqLineRe.FindStringSubmatch(line); len(m) == 2 {
				addDep(m[1])
			}
		}
	}
}

// collectNodeManifestDeps parses sorted dependency keys from package.json.
func collectNodeManifestDeps(root string, addDep func(string)) {
	raw, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return
	}
	var pkg struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if json.Unmarshal(raw, &pkg) != nil || len(pkg.Dependencies) == 0 {
		return
	}
	names := make([]string, 0, len(pkg.Dependencies))
	for k := range pkg.Dependencies {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, n := range names {
		addDep(n)
	}
}

// collectCargoManifestDeps parses dependency keys from Cargo.toml [dependencies].
func collectCargoManifestDeps(root string, addDep func(string)) {
	raw, err := os.ReadFile(filepath.Join(root, "Cargo.toml"))
	if err != nil {
		return
	}
	m := hullCargoDepSecRe.FindStringSubmatch(string(raw))
	if len(m) != 2 {
		return
	}
	for _, line := range strings.Split(m[1], "\n") {
		if km := hullCargoKeyRe.FindStringSubmatch(line); len(km) == 2 {
			addDep(km[1])
		}
	}
}

// discoverWorkspaceModuleHull summarizes top-level modules and their leading exported symbols.
func discoverWorkspaceModuleHull(root string) []string {
	cleanRoot := filepath.Clean(root)
	var files []string
	tooMany := false
	wErr := filepath.WalkDir(cleanRoot, func(path string, d fs.DirEntry, err error) error {
		return visitHullFileEntry(cleanRoot, path, d, err, &files, &tooMany)
	})
	if (wErr != nil && wErr != fs.SkipAll) || len(files) == 0 || tooMany {
		return nil
	}
	sort.Strings(files)
	var summaries []string
	for i := 0; i < len(files) && i < maxHullModules; i++ {
		summaries = append(summaries, formatModuleSymbols(cleanRoot, files[i]))
	}
	return summaries
}

// visitHullFileEntry filters skipped directories and test files while collecting bounded source paths.
func visitHullFileEntry(cleanRoot, path string, d fs.DirEntry, err error, files *[]string, tooMany *bool) error {
	if err != nil || d == nil {
		return nil
	}
	if d.IsDir() {
		if path != cleanRoot && hullSkipDirs[d.Name()] {
			return filepath.SkipDir
		}
		return nil
	}
	rel, rErr := filepath.Rel(cleanRoot, path)
	if rErr != nil {
		return nil
	}
	slash := filepath.ToSlash(rel)
	if !hullSourceExts[strings.ToLower(filepath.Ext(slash))] || isHullTestFile(slash) {
		return nil
	}
	*files = append(*files, slash)
	if len(*files) > maxHullFiles {
		*tooMany = true
		return fs.SkipAll
	}
	return nil
}

// isHullTestFile identifies unit test filenames so module hull summaries focus on source files.
func isHullTestFile(slash string) bool {
	base := filepath.Base(slash)
	return strings.HasPrefix(base, "test_") || strings.Contains(base, "_test.") || strings.Contains(base, ".test.") ||
		strings.HasPrefix(slash, "tests/") || strings.Contains(slash, "/tests/")
}

// formatModuleSymbols reads a small source file and extracts up to maxHullSymbols symbol names.
func formatModuleSymbols(root, slashRel string) string {
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(slashRel)))
	if err != nil || len(raw) > 256*1024 {
		return slashRel
	}
	var syms []string
	for _, line := range strings.Split(string(raw), "\n") {
		if len(syms) >= maxHullSymbols {
			break
		}
		if m := hullSymbolDefRe.FindStringSubmatch(line); len(m) == 2 && !strings.HasPrefix(m[1], "__") {
			syms = append(syms, m[1])
		}
	}
	if len(syms) == 0 {
		return slashRel
	}
	return fmt.Sprintf("%s (%s)", slashRel, strings.Join(syms, ", "))
}
