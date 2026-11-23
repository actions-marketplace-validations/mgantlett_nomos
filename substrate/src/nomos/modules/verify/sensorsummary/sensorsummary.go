// Package sensorsummary classifies gate sensor execution scopes, unindexed source extensions,
// and diff-ratcheted orphaned testdata/example fixtures across verification reports.
// @intent nomos_intent_traceability_spec.md
package sensorsummary

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

// Canonical gate sensor execution scope constants emitted on GateResult.ExecutionScope.
const (
	ScopeASTVerified        = "ast_verified"
	ScopeRuntimeVerified    = "runtime_verified"
	ScopeStructuralVerified = "structural_verified"
	ScopeBoundedHeuristic   = "bounded_heuristic"
	ScopeNoFilesInScope     = "no_files_in_scope"
	ScopeConfigDisabled     = "config_disabled"
)

var unindexedCodeExts = map[string]bool{
	".sh": true, ".bash": true, ".zsh": true, ".ps1": true,
	".sql": true, ".php": true, ".swift": true, ".scala": true,
	".ex": true, ".exs": true, ".erl": true, ".lua": true,
	".r": true, ".zig": true, ".dart": true, ".sol": true,
	".tf": true, ".hcl": true, ".gleam": true, ".clj": true,
	".hs": true, ".ml": true, ".pl": true, ".groovy": true,
}

var excludedScanDirs = map[string]bool{
	".git": true, "worktrees": true, "node_modules": true, "vendor": true,
	"dist": true, "build": true, ".svelte-kit": true, ".venv": true,
	"venv": true, "__pycache__": true, "testdata": true, "fixtures": true,
	"target": true, ".next": true, ".nuxt": true, ".tmp": true,
}

var canonicalProbeSensors = map[string]string{
	"anti_shim": "ast", "docstrings": "ast", "complexity": "ast", "max_file_loc": "ast",
	"cognitive_debt": "ast", "magic_strings": "ast", "magic_string_duplication": "ast",
	"unchecked_errors": "ast", "dead_code": "ast", "contract_parity": "ast",
	"ui_anti_mask": "ast", "zero_alias": "ast", "intent_traceability": "ast",
	"intent_steelman": "ast", "gate_integrity_proof": "ast",
	"go_toolchain": "test_coverage", "coverage": "test_coverage",
	"tdd": "test_coverage", "visual_regression": "test_coverage",
	"secret": "secret", "broken_wire": "links", "forbidden_literals": "secret",
	"ip_leak": "secret", "infra_ingress": "secret",
}

// SensorForProbeID maps a canonical ProbeID (or fallback Ring) to its verification sensor class.
// @intent nomos_intent_traceability_spec.md #GateResult
func SensorForProbeID(probeID, ring string) string {
	if s, ok := canonicalProbeSensors[strings.TrimSpace(probeID)]; ok {
		return s
	}
	if strings.Contains(strings.ToLower(strings.TrimSpace(ring)), "dynamic") {
		return "test_coverage"
	}
	return "structural"
}

// ClassifyGateExecutionScope derives the execution scope for a GateResult and its sensor kind.
// @intent nomos_intent_traceability_spec.md #GateResult
func ClassifyGateExecutionScope(res *vscope.GateResult, sensor string) string {
	if res == nil {
		return ""
	}
	if strings.TrimSpace(res.ExecutionScope) != "" {
		return res.ExecutionScope
	}
	lowMsg := strings.ToLower(strings.TrimSpace(res.Message))
	if res.Skipped || isDisabledGateMessage(lowMsg) {
		return ScopeConfigDisabled
	}
	if res.Passed && isNoScopeGateMessage(lowMsg) {
		return ScopeNoFilesInScope
	}
	return sensorToExecutionScope(sensor)
}

func isDisabledGateMessage(lowMsg string) bool {
	return strings.Contains(lowMsg, "disabled in configuration") ||
		(strings.Contains(lowMsg, "disabled") && strings.Contains(lowMsg, "skip"))
}

func isNoScopeGateMessage(lowMsg string) bool {
	if strings.Contains(lowMsg, "skipping ") || strings.Contains(lowMsg, "0 files scanned") {
		return true
	}
	if strings.Contains(lowMsg, "no ") && (strings.Contains(lowMsg, "scope") || strings.Contains(lowMsg, "files in scope") || strings.Contains(lowMsg, "target files")) {
		return true
	}
	return false
}

func sensorToExecutionScope(sensor string) string {
	switch strings.ToLower(strings.TrimSpace(sensor)) {
	case "ast":
		return ScopeASTVerified
	case "test_coverage":
		return ScopeRuntimeVerified
	case "secret", "links":
		return ScopeBoundedHeuristic
	default:
		return ScopeStructuralVerified
	}
}

// SummarizeSensors aggregates non-empty GateResult.ExecutionScope counts across a pipeline run.
// @intent nomos_intent_traceability_spec.md #PipelineReport
func SummarizeSensors(results []*vscope.GateResult) map[string]int {
	summary := make(map[string]int)
	for _, r := range results {
		if r == nil || r.ExecutionScope == "" {
			continue
		}
		summary[r.ExecutionScope]++
	}
	if len(summary) == 0 {
		return nil
	}
	return summary
}

// ScanUnindexedExtensions discovers script/query extensions in root outside the 11 AST-indexed languages.
// @intent nomos_intent_traceability_spec.md #PipelineReport
func ScanUnindexedExtensions(root string) []string {
	cleanRoot := strings.TrimSpace(root)
	if cleanRoot == "" {
		return nil
	}
	found := make(map[string]bool)
	visited := 0
	walkErr := filepath.WalkDir(cleanRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil
		}
		if d.IsDir() {
			if shouldSkipScanDir(path, cleanRoot, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		visited++
		if visited > 12000 {
			return filepath.SkipAll
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if unindexedCodeExts[ext] {
			found[ext] = true
		}
		return nil
	})
	if walkErr != nil {
		return sortedKeys(found)
	}
	return sortedKeys(found)
}

func shouldSkipScanDir(path, root, name string) bool {
	if path == root {
		return false
	}
	if strings.HasPrefix(name, ".") && name != ".github" {
		return true
	}
	return excludedScanDirs[strings.ToLower(name)]
}

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// FormatSensorSummaryLine renders a compact human summary of gate sensor modes and unindexed extensions.
// @intent nomos_intent_traceability_spec.md #PipelineReport
func FormatSensorSummaryLine(summary map[string]int, unindexed []string) string {
	if len(summary) == 0 {
		return ""
	}
	order := []struct {
		key   string
		label string
	}{
		{ScopeASTVerified, "ast"},
		{ScopeRuntimeVerified, "runtime"},
		{ScopeStructuralVerified, "structural"},
		{ScopeBoundedHeuristic, "heuristic"},
		{ScopeNoFilesInScope, "out-of-scope"},
		{ScopeConfigDisabled, "disabled"},
	}
	var parts []string
	for _, item := range order {
		if n := summary[item.key]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, item.label))
		}
	}
	line := strings.Join(parts, " · ")
	if len(unindexed) > 0 {
		line += fmt.Sprintf(" | heuristic-only exts: %s", strings.Join(unindexed, ", "))
	}
	return line
}
