// Package ratchet implements optional runtime benchmark regression enforcement (Law 2 & Law 7).
// @intent nomos_intent_traceability_spec.md
package ratchet

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/config"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

const (
	// DefaultBenchmarkBaselineFile is the default workspace-relative baseline metric file.
	DefaultBenchmarkBaselineFile = "nomos.bench.baseline.json"
	// DefaultBenchmarkMaxRegressionPct is the default allowed regression percentage.
	DefaultBenchmarkMaxRegressionPct = 15.0
)

var (
	goBenchLineRe = regexp.MustCompile(`^(Benchmark[^\s]+)\s+\d+\s+(.+)$`)
	goBenchPairRe = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)\s+(ns/op|B/op|allocs/op)`)
)

// EvaluateBenchmarkRatchet runs governance.quality_gates.benchmark_ratchet when configured.
// @intent nomos_intent_traceability_spec.md #BenchmarkRatchetConfig
func EvaluateBenchmarkRatchet(gateName string, ctx *vscope.VerifyContext, root string) (string, *vscope.GateResult) {
	cfg := vscope.ResolveVerifyConfig(ctx)
	if cfg == nil || cfg.Governance.QualityGates.BenchmarkRatchet == nil {
		return "", nil
	}
	br := cfg.Governance.QualityGates.BenchmarkRatchet
	if strings.TrimSpace(br.Command) == "" {
		return "", nil
	}
	basePath := resolveBenchmarkBaselinePath(root, br)
	baseline, err := loadBenchmarkBaseline(basePath)
	if err != nil {
		return "", &vscope.GateResult{
			GateName: gateName, Passed: false,
			Message: fmt.Sprintf("benchmark_ratchet baseline unavailable (%s): %v", filepath.Base(basePath), err),
			Remedy:  "seed or refresh the benchmark baseline via 'nomos verify --update-bench-baseline'",
		}
	}
	current, runErr := RunBenchmarkCommand(root, br.Command)
	if runErr != nil {
		return "", &vscope.GateResult{
			GateName: gateName, Passed: false,
			Message: fmt.Sprintf("benchmark_ratchet command failed: %v", runErr),
			Remedy:  "fix the benchmark command in governance.quality_gates.benchmark_ratchet.command",
		}
	}
	maxPct := resolveBenchmarkMaxRegressionPct(br)
	if regressions := CompareBenchmarkAgainstBaseline(baseline, current, maxPct); len(regressions) > 0 {
		return "", &vscope.GateResult{
			GateName: gateName, Passed: false,
			Message: fmt.Sprintf("benchmark_ratchet regression(s) exceeded +%.1f%% budget: %s", maxPct, strings.Join(regressions, "; ")),
			Remedy:  "optimize the hot-path regression or intentionally refresh baseline via 'nomos verify --update-bench-baseline'",
		}
	}
	return fmt.Sprintf("Bench(%d metrics ok)", len(current)), nil
}

// UpdateBenchmarkBaseline executes the configured benchmark command and writes pinned baseline metrics to disk.
// @intent nomos_intent_traceability_spec.md #BenchmarkRatchetConfig
func UpdateBenchmarkBaseline(root string, br *config.BenchmarkRatchetConfig) (string, map[string]float64, error) {
	if br == nil || strings.TrimSpace(br.Command) == "" {
		return "", nil, fmt.Errorf("governance.quality_gates.benchmark_ratchet.command is not configured in nomos.yaml")
	}
	metrics, err := RunBenchmarkCommand(root, br.Command)
	if err != nil {
		return "", nil, err
	}
	if len(metrics) == 0 {
		return "", nil, fmt.Errorf("benchmark command emitted zero parseable Go -bench or JSON metrics")
	}
	target := resolveBenchmarkBaselinePath(root, br)
	raw, mErr := json.MarshalIndent(metrics, "", "  ")
	if mErr != nil {
		return "", nil, mErr
	}
	if wErr := os.WriteFile(target, append(raw, '\n'), 0o644); wErr != nil {
		return "", nil, wErr
	}
	return target, metrics, nil
}

// RunBenchmarkCommand executes cmdLine in root and extracts Go -bench or JSON runtime metrics.
// @intent nomos_intent_traceability_spec.md #BenchmarkRatchetConfig
func RunBenchmarkCommand(root, cmdLine string) (map[string]float64, error) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/C", cmdLine)
	} else {
		cmd = exec.Command("sh", "-c", cmdLine)
	}
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK=off", "NOMOS_IN_TOOLCHAIN_GATE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return ParseBenchmarkMetrics(string(out)), nil
}

// ParseBenchmarkMetrics extracts metric maps from JSON object stdout or Go 'go test -bench' lines.
// @intent nomos_intent_traceability_spec.md #BenchmarkRatchetConfig
func ParseBenchmarkMetrics(stdout string) map[string]float64 {
	metrics := make(map[string]float64)
	trimmed := strings.TrimSpace(stdout)
	if strings.HasPrefix(trimmed, "{") {
		var parsed map[string]float64
		if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil && len(parsed) > 0 {
			return parsed
		}
	}
	for _, line := range strings.Split(stdout, "\n") {
		parseGoBenchLine(strings.TrimSpace(line), metrics)
	}
	return metrics
}

func parseGoBenchLine(line string, metrics map[string]float64) {
	m := goBenchLineRe.FindStringSubmatch(line)
	if len(m) != 3 {
		return
	}
	name := stripGoBenchCPUSuffix(m[1])
	for _, pair := range goBenchPairRe.FindAllStringSubmatch(m[2], -1) {
		if len(pair) != 3 {
			continue
		}
		if val, err := strconv.ParseFloat(pair[1], 64); err == nil && !math.IsNaN(val) && !math.IsInf(val, 0) {
			unit := strings.ReplaceAll(pair[2], "/", "_")
			metrics[name+"."+unit] = val
		}
	}
}

func stripGoBenchCPUSuffix(name string) string {
	idx := strings.LastIndexByte(name, '-')
	if idx <= 0 || idx+1 >= len(name) {
		return name
	}
	if _, err := strconv.Atoi(name[idx+1:]); err == nil {
		return name[:idx]
	}
	return name
}

// CompareBenchmarkAgainstBaseline returns regressions exceeding maxRegressionPct over baseline metrics.
// @intent nomos_intent_traceability_spec.md #BenchmarkRatchetConfig
func CompareBenchmarkAgainstBaseline(baseline, current map[string]float64, maxRegressionPct float64) []string {
	keys := make([]string, 0, len(baseline))
	for k := range baseline {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var regressions []string
	for _, k := range keys {
		baseVal := baseline[k]
		curVal, ok := current[k]
		if !ok || baseVal <= 0 {
			continue
		}
		limit := baseVal * (1.0 + maxRegressionPct/100.0)
		if curVal > limit {
			pct := ((curVal - baseVal) / baseVal) * 100.0
			regressions = append(regressions, fmt.Sprintf("%s: %.2f > %.2f (+%.1f%%)", k, curVal, limit, pct))
		}
	}
	return regressions
}

func resolveBenchmarkBaselinePath(root string, br *config.BenchmarkRatchetConfig) string {
	rel := strings.TrimSpace(br.BaselineFile)
	if rel == "" {
		rel = DefaultBenchmarkBaselineFile
	}
	if filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(root, filepath.FromSlash(rel))
}

func resolveBenchmarkMaxRegressionPct(br *config.BenchmarkRatchetConfig) float64 {
	if br != nil && br.MaxRegressionPct > 0 {
		return br.MaxRegressionPct
	}
	return DefaultBenchmarkMaxRegressionPct
}

func loadBenchmarkBaseline(path string) (map[string]float64, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out map[string]float64
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
