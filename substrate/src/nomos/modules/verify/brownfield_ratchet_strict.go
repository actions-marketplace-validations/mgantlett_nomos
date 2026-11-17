// Package verify implements strict non-worktree scoping and net-new brownfield clone/design-slop evaluation.
package verify

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/astdebt"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

func filterStrictNonWorktreeComplexity(ctx *vscope.VerifyContext, root string, funcs []astdebt.FunctionMetric) ([]astdebt.FunctionMetric, bool) {
	if vscope.IsWorktreeContext(ctx, root) {
		return funcs, true
	}
	if ctx != nil && !ctx.IsBaseline && !ctx.AllFiles && vscope.HasGitMetadata(root) {
		if modified := vscope.GetModifiedFilesInScope(ctx, root); len(modified) > 0 {
			return subsetFuncsByModified(funcs, modified), true
		}
	}
	return funcs, false
}

func evaluateStrictNonWorktreeOrDefaultCDS(ctx *vscope.VerifyContext, root string, metrics []astdebt.FileMetric, sources map[string][]byte, thresh astdebt.Thresholds) []string {
	if vscope.IsWorktreeContext(ctx, root) {
		return astdebt.EnrichCDSViolations(root, metrics, sources, thresh, true)
	}
	if ctx != nil && !ctx.IsBaseline && !ctx.AllFiles && vscope.HasGitMetadata(root) {
		if modified := vscope.GetModifiedFilesInScope(ctx, root); len(modified) > 0 {
			subMetrics, subSources := subsetMetricsAndSources(metrics, sources, modified)
			return astdebt.EnrichCDSViolations(root, subMetrics, subSources, thresh, true)
		}
	}
	return astdebt.EnrichCDSViolations(root, metrics, sources, thresh, false)
}

func evaluateNetNewBrownfieldClonesAndSlop(ctx *vscope.VerifyContext, root string, changedSources, baseSources map[string][]byte, thresh astdebt.Thresholds) []string {
	if len(changedSources) == 0 || isUpstreamSurgicalRatchet(ctx) || (ctx != nil && (ctx.IsBaseline || ctx.AllFiles)) {
		return nil
	}
	violations := detectNetNewCloneViolations(changedSources, baseSources, thresh)
	rels := make([]string, 0, len(changedSources))
	for rel := range changedSources {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		if !strings.EqualFold(filepath.Ext(rel), astdebt.ExtPy) {
			continue
		}
		cur := astdebt.DetectPolyglotDesignSlop(root, map[string][]byte{rel: changedSources[rel]})
		filtered := filterNetNewLineViolations(ctx, root, filepath.Join(root, rel), cur, func(baseBytes []byte) []string {
			return astdebt.DetectPolyglotDesignSlop(root, map[string][]byte{rel: baseBytes})
		})
		violations = append(violations, filtered...)
	}
	return violations
}

func buildModifiedRelSet(modified []string) map[string]bool {
	set := make(map[string]bool, len(modified))
	for _, m := range modified {
		set[filepath.ToSlash(filepath.Clean(m))] = true
	}
	return set
}

func subsetMetricsAndSources(metrics []astdebt.FileMetric, sources map[string][]byte, modified []string) ([]astdebt.FileMetric, map[string][]byte) {
	modSet := buildModifiedRelSet(modified)
	var subMetrics []astdebt.FileMetric
	for _, m := range metrics {
		if modSet[filepath.ToSlash(filepath.Clean(m.Name))] {
			subMetrics = append(subMetrics, m)
		}
	}
	subSources := make(map[string][]byte, len(subMetrics))
	for k, v := range sources {
		if modSet[filepath.ToSlash(filepath.Clean(k))] {
			subSources[k] = v
		}
	}
	return subMetrics, subSources
}

func subsetFuncsByModified(funcs []astdebt.FunctionMetric, modified []string) []astdebt.FunctionMetric {
	modSet := buildModifiedRelSet(modified)
	var out []astdebt.FunctionMetric
	for _, fn := range funcs {
		if modSet[filepath.ToSlash(filepath.Clean(fn.File))] {
			out = append(out, fn)
		}
	}
	return out
}
