// Package verify implements the deterministic Definition of Done (DoD) quality gate pipeline.
package verify

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/config"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/types"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/astdebt"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/polygate"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/ratchet"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

var regexTaskKey = regexp.MustCompile(`^(INT|SUB)-[0-9]+(-[a-z0-9-]+)?$`)

func init() {
	RegisterGate("anti_shim", func() vscope.Gate { return &AntiShimGate{} })
	RegisterGate("cognitive_debt", func() vscope.Gate { return &CognitiveDebtGate{} })
	RegisterGate("max_file_loc", func() vscope.Gate { return &MaxFileLOCGate{} })
	RegisterGate("complexity", func() vscope.Gate { return &ComplexityGate{} })
	RegisterGate("hermetic_worktree", func() vscope.Gate { return &HermeticWorktreeGate{} })
	RegisterGate("phase_discipline", func() vscope.Gate { return &PhaseDisciplineGate{} })
	RegisterGate("canonical_singularity", func() vscope.Gate { return &CanonicalSingularityGate{} })
	RegisterGate("unchecked_errors", func() vscope.Gate { return &UncheckedErrorsGate{} })
	RegisterGate("go_toolchain", func() vscope.Gate { return &GoToolchainGate{} })
	RegisterGate("staged_boundaries", func() vscope.Gate { return &vscope.StagedBoundariesGate{} })
	RegisterGate("git_hygiene", func() vscope.Gate { return &vscope.GitSubprocessHygieneGate{} })
	RegisterGate("commit_schema", func() vscope.Gate { return &CommitSchemaGate{} })
}

// CognitiveDebtGate verifies workspace cognitive debt scores.
type CognitiveDebtGate struct{}

// Name returns the canonical display name.
func (g *CognitiveDebtGate) Name() string { return "Cognitive Debt Score Invariant" }

// Evaluate scans modified or repository files for cognitive debt and clone bounds.
func (g *CognitiveDebtGate) Evaluate(ctx *vscope.VerifyContext) (*vscope.GateResult, error) {
	root := ctx.WorktreePath
	if root == "" {
		root = ctx.RepoRoot
	}
	thresh := ratchet.ResolveThresholds(ctx)
	var metrics []astdebt.FileMetric
	sources := make(map[string][]byte)

	err := vscope.WalkTargetFiles(ctx, root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || !ratchet.IsPolyglotTargetFile(path) {
			return nil
		}
		content, rErr := os.ReadFile(path)
		if rErr != nil {
			return nil
		}
		rep, dErr := astdebt.AnalyzeFileDebt(path, content, thresh.MaxComplexity, thresh.MaxFuncLOC)
		if dErr == nil && rep != nil {
			rel, rErr := filepath.Rel(root, path)
			if rErr != nil {
				rel = path
			}
			metrics = append(metrics, astdebt.FileMetric{Name: rel, LOC: rep.TotalLOC, CDS: rep.DebtScore})
			sources[rel] = content
		}
		return nil
	})
	if err != nil {
		return nil, types.NewNomosError(types.ErrCodeCore, "cognitive debt walk failed: %v", err)
	}

	violations := ratchet.EvaluateRatchetedCDSViolations(ctx, root, metrics, sources, thresh)
	violations = append(violations, evaluateCrossCorpusCloneViolations(ctx, root, sources, thresh)...)
	if len(violations) > 0 {
		return &vscope.GateResult{
			GateName: g.Name(), Passed: false,
			Message: fmt.Sprintf("detected %d CDS/clone violation(s):\n    - %s", len(violations), strings.Join(violations, "\n    - ")),
			Remedy:  "apply the prescribed AST extraction seams and consolidate structural clones",
		}, nil
	}
	return &vscope.GateResult{GateName: g.Name(), Passed: true, Message: "all files within cognitive debt bounds"}, nil
}

func evaluateCrossCorpusCloneViolations(ctx *vscope.VerifyContext, root string, sources map[string][]byte, thresh astdebt.Thresholds) []string {
	if len(sources) == 0 || (ctx != nil && ctx.AllFiles) || vscope.ResolveEffectiveVerifyScope(ctx) == config.VerifyScopeRepo || ratchet.IsUpstreamSurgicalRatchet(ctx) {
		return nil
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		absRoot = root
	}
	minLines := thresh.MinCloneLines
	if minLines <= 0 {
		minLines = astdebt.DefaultMinCloneLines
	}
	focusNorm := make(map[string]bool, len(sources))
	baseSources := make(map[string][]byte, len(sources))
	for rel := range sources {
		norm := filepath.ToSlash(filepath.Clean(rel))
		focusNorm[norm] = true
		if b, ok := ratchet.LoadBaselineFileBytes(ctx, root, filepath.Join(root, rel)); ok {
			baseSources[norm] = b
		}
	}
	groups := astdebt.DetectFocusedCorpusClones(sources, baseSources, minLines, func(langs map[string]bool, visit func(relPath string, src []byte)) {
		excludes := vscope.ResolveScopeExcludes(ctx, absRoot, absRoot)
		spec := corpusCloneWalkSpec{absRoot: absRoot, focusNorm: focusNorm, langs: langs, minLines: minLines, visit: visit}
		if wErr := workspace.WalkWithExcludes(absRoot, excludes, spec.visitPath); wErr != nil {
			return
		}
	})
	return astdebt.FormatCloneViolations(groups)
}

type corpusCloneWalkSpec struct {
	absRoot   string
	focusNorm map[string]bool
	langs     map[string]bool
	minLines  int
	visit     func(string, []byte)
}

func (s corpusCloneWalkSpec) visitPath(path string, info os.FileInfo, walkErr error) error {
	if walkErr != nil || info == nil || info.IsDir() || !ratchet.IsPolyglotTargetFile(path) {
		return nil
	}
	rel, rErr := filepath.Rel(s.absRoot, path)
	if rErr != nil {
		return nil
	}
	normRel := filepath.ToSlash(filepath.Clean(rel))
	if s.focusNorm[normRel] || !s.langs[astdebt.CloneLangFamily(normRel)] {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil || ratchet.CountBytesLines(raw) < s.minLines {
		return nil
	}
	s.visit(normRel, raw)
	return nil
}

// MaxFileLOCGate verifies that no file exceeds the line length limit.
type MaxFileLOCGate struct{}

// Name returns the canonical display name.
func (g *MaxFileLOCGate) Name() string { return "Max File LOC Invariant" }

// Evaluate verifies target file LOC and package fan-out bounds.
func (g *MaxFileLOCGate) Evaluate(ctx *vscope.VerifyContext) (*vscope.GateResult, error) {
	root := ctx.WorktreePath
	if root == "" {
		root = ctx.RepoRoot
	}
	thresh := ratchet.ResolveThresholds(ctx)
	var metrics []astdebt.FileMetric

	err := vscope.WalkTargetFiles(ctx, root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || !ratchet.IsPolyglotTargetFile(path) {
			return nil
		}
		lines := countFileLines(path)
		rel, rErr := filepath.Rel(root, path)
		if rErr != nil {
			rel = path
		}
		metrics = append(metrics, astdebt.FileMetric{Name: rel, LOC: lines})
		return nil
	})
	if err != nil {
		return nil, types.NewNomosError(types.ErrCodeCore, "file loc walk failed: %v", err)
	}

	violations := astdebt.EnrichFileLOCViolations(root, ratchet.FilterRatchetedFileLOC(ctx, root, metrics, thresh), thresh)
	violations = append(violations, ratchet.EvaluatePackageFanOutViolations(ctx, root, metrics, thresh)...)
	violations = append(violations, ratchet.EvaluateUpstreamSurgicalViolations(ctx, root, metrics)...)
	if len(violations) > 0 {
		return &vscope.GateResult{
			GateName: g.Name(), Passed: false,
			Message: strings.Join(violations, "; "),
			Remedy:  "extract the top candidate functions listed above into modular sibling units",
		}, nil
	}
	return &vscope.GateResult{GateName: g.Name(), Passed: true, Message: "all files within LOC bounds"}, nil
}

func countFileLines(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return ratchet.CountBytesLines(raw)
}

// ComplexityGate verifies cyclomatic complexity and function LOC bounds.
type ComplexityGate struct{}

// Name returns the canonical display name.
func (g *ComplexityGate) Name() string { return "Cognitive Complexity Invariant" }

// Evaluate verifies function cyclomatic complexity, LOC, and arity bounds.
func (g *ComplexityGate) Evaluate(ctx *vscope.VerifyContext) (*vscope.GateResult, error) {
	root := ctx.WorktreePath
	if root == "" {
		root = ctx.RepoRoot
	}
	thresh := ratchet.ResolveThresholds(ctx)
	var funcMetrics []astdebt.FunctionMetric

	err := vscope.WalkTargetFiles(ctx, root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || !ratchet.IsPolyglotTargetFile(path) {
			return nil
		}
		rel, rErr := filepath.Rel(root, path)
		if rErr != nil {
			rel = path
		}
		if !strings.HasSuffix(path, ".go") {
			ratchet.CollectPolyglotComplexity(path, rel, &funcMetrics)
			return nil
		}
		fset, node, pErr := vscope.MemoizedParseGoFile(ctx, path, 0)
		if pErr != nil || node == nil {
			return nil
		}
		ratchet.CollectFunctionMetrics(node, fset, rel, &funcMetrics)
		return nil
	})
	if err != nil {
		return nil, types.NewNomosError(types.ErrCodeCore, "complexity walk failed: %v", err)
	}

	ratchetedFuncs, checkArity := ratchet.FilterRatchetedComplexity(ctx, root, funcMetrics, thresh)
	violations := astdebt.EnrichComplexityViolations(root, ratchetedFuncs, thresh, checkArity)
	violations = append(violations, ratchet.EvaluateBrownfieldArityRatchet(ctx, root, funcMetrics, thresh)...)
	violations = append(violations, ratchet.EvaluateGoSwitchIdiomViolations(ctx, root)...)
	violations = append(violations, polygate.EvaluateTestComplexityViolations(ctx, root)...)
	if len(violations) > 0 {
		return &vscope.GateResult{
			GateName: g.Name(), Passed: false,
			Message: fmt.Sprintf("detected %d complexity/arity violation(s):\n    - %s", len(violations), strings.Join(violations, "\n    - ")),
			Remedy:  "decompose complex functions along the prescribed AST seams and encapsulate >8 parameters into structs",
		}, nil
	}
	return &vscope.GateResult{GateName: g.Name(), Passed: true, Message: "all functions within complexity bounds"}, nil
}

// HermeticWorktreeGate verifies worktree boundaries.
type HermeticWorktreeGate struct{}

// Name returns the canonical display name.
func (g *HermeticWorktreeGate) Name() string { return "Hermetic Worktree Invariant" }

// Evaluate verifies isolated worktree task boundary invariants.
func (g *HermeticWorktreeGate) Evaluate(ctx *vscope.VerifyContext) (*vscope.GateResult, error) {
	if ctx.WorktreePath == "" {
		return &vscope.GateResult{GateName: g.Name(), Passed: true, Message: "no worktree path specified"}, nil
	}
	repo := ctx.RepoRoot
	if repo == "" {
		repo = "."
	}
	branch := ctx.Phase
	if branch == "" {
		branch = "main"
	}
	err := workspace.ValidateWorktreeTaskBoundary(repo, branch, ctx.WorktreePath, ctx.TaskID)
	if err != nil {
		return &vscope.GateResult{
			GateName: g.Name(), Passed: false,
			Message: err.Error(), Remedy: "execute mutations strictly within transient worktrees",
		}, nil
	}
	return &vscope.GateResult{GateName: g.Name(), Passed: true, Message: "worktree boundaries intact"}, nil
}

// PhaseDisciplineGate verifies lifecycle phase state.
type PhaseDisciplineGate struct{}

// Name returns the canonical display name.
func (g *PhaseDisciplineGate) Name() string { return "Phase Discipline Invariant" }

// Evaluate verifies active lifecycle phase state validity.
func (g *PhaseDisciplineGate) Evaluate(ctx *vscope.VerifyContext) (*vscope.GateResult, error) {
	if ctx == nil || ctx.Phase == "" {
		return &vscope.GateResult{GateName: g.Name(), Passed: true, Message: "no active phase enforced"}, nil
	}
	if !types.IsValidPhase(types.Phase(ctx.Phase)) {
		return &vscope.GateResult{
			GateName: g.Name(), Passed: false,
			Message: fmt.Sprintf("invalid workspace phase %q: must be IDLE, PLAN, EDIT, REVIEW, SYNC, or LEARN", ctx.Phase),
			Remedy:  "restore valid phase state via 'nomos handshake' or 'nomos task abort'",
		}, nil
	}
	return &vscope.GateResult{GateName: g.Name(), Passed: true, Message: fmt.Sprintf("phase %s valid", ctx.Phase)}, nil
}

// CanonicalSingularityGate verifies task key formatting.
type CanonicalSingularityGate struct{}

// Name returns the canonical display name.
func (g *CanonicalSingularityGate) Name() string { return "Canonical Singularity Invariant" }

// Evaluate verifies task key prefixes match canonical INT-<N> or SUB-<N> format.
func (g *CanonicalSingularityGate) Evaluate(ctx *vscope.VerifyContext) (*vscope.GateResult, error) {
	if ctx.TaskID == "" {
		return &vscope.GateResult{GateName: g.Name(), Passed: true, Message: "no task id specified"}, nil
	}
	if !regexTaskKey.MatchString(ctx.TaskID) {
		return &vscope.GateResult{
			GateName: g.Name(), Passed: false,
			Message: fmt.Sprintf("invalid task key %q: must adhere to INT-<N> or SUB-<N>", ctx.TaskID),
			Remedy:  "use canonical task key prefixes INT- or SUB-",
		}, nil
	}
	return &vscope.GateResult{GateName: g.Name(), Passed: true, Message: "canonical task key valid"}, nil
}
