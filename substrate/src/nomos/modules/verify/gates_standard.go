// Package verify implements the deterministic Definition of Done (DoD) quality gate pipeline.
package verify

import (
	"fmt"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/astdebt"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/config"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/types"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
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

func isPolyglotTargetFile(path string) bool {
	if vscope.IsExcludedPolyglotDir(filepath.ToSlash(path)) {
		return false
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case astdebt.ExtGo:
		return !strings.HasSuffix(path, astdebt.SuffixGoTest)
	case astdebt.ExtTS, astdebt.ExtTSX, astdebt.ExtJS, astdebt.ExtJSX, astdebt.ExtMJS, astdebt.ExtCJS:
		return !vscope.IsJSTestOrDeclFile(path)
	case astdebt.ExtSvelte:
		return true
	case astdebt.ExtPy:
		return !strings.HasPrefix(filepath.Base(path), astdebt.PrefixPyTest) && !strings.HasSuffix(path, astdebt.SuffixPyTest)
	case astdebt.ExtRB, astdebt.ExtRake:
		return !vscope.IsRubyTestFile(path)
	case astdebt.ExtRS, astdebt.ExtC, astdebt.ExtH, astdebt.ExtCPP, astdebt.ExtCC, astdebt.ExtCXX, astdebt.ExtHPP, astdebt.ExtJava, astdebt.ExtKT, astdebt.ExtKTS, astdebt.ExtCS:
		return !vscope.IsNativeTestFile(path)
	default:
		return false
	}
}

// CognitiveDebtGate verifies workspace cognitive debt scores.
type CognitiveDebtGate struct{}

func (g *CognitiveDebtGate) Name() string { return "Cognitive Debt Score Invariant" }

func (g *CognitiveDebtGate) Evaluate(ctx *vscope.VerifyContext) (*vscope.GateResult, error) {
	root := ctx.WorktreePath
	if root == "" {
		root = ctx.RepoRoot
	}
	thresh := resolveThresholds(ctx)
	var metrics []astdebt.FileMetric
	sources := make(map[string][]byte)

	err := vscope.WalkTargetFiles(ctx, root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || !isPolyglotTargetFile(path) {
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

	violations := evaluateRatchetedCDSViolations(ctx, root, metrics, sources, thresh)
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

// evaluateCrossCorpusCloneViolations indexes sibling corpus files when diff-scoped edits add new structural clones.
func evaluateCrossCorpusCloneViolations(ctx *vscope.VerifyContext, root string, sources map[string][]byte, thresh astdebt.Thresholds) []string {
	if len(sources) == 0 || (ctx != nil && ctx.AllFiles) || vscope.ResolveEffectiveVerifyScope(ctx) == config.VerifyScopeRepo || isUpstreamSurgicalRatchet(ctx) {
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
		if b, ok := loadBaselineFileBytes(ctx, root, filepath.Join(root, rel)); ok {
			baseSources[norm] = b
		}
	}
	groups := astdebt.DetectFocusedCorpusClones(sources, baseSources, minLines, func(langs map[string]bool, visit func(relPath string, src []byte)) {
		excludes := vscope.ResolveScopeExcludes(ctx, absRoot, absRoot)
		if wErr := workspace.WalkWithExcludes(absRoot, excludes, func(path string, info os.FileInfo, walkErr error) error {
			return visitCorpusCloneCandidate(absRoot, path, info, walkErr, focusNorm, langs, minLines, visit)
		}); wErr != nil {
			return
		}
	})
	return astdebt.FormatCloneViolations(groups)
}

func visitCorpusCloneCandidate(absRoot, path string, info os.FileInfo, walkErr error, focusNorm, langs map[string]bool, minLines int, visit func(string, []byte)) error {
	if walkErr != nil || info == nil || info.IsDir() || !isPolyglotTargetFile(path) {
		return nil
	}
	rel, rErr := filepath.Rel(absRoot, path)
	if rErr != nil {
		return nil
	}
	normRel := filepath.ToSlash(filepath.Clean(rel))
	if focusNorm[normRel] || !langs[astdebt.CloneLangFamily(normRel)] {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil || countBytesLines(raw) < minLines {
		return nil
	}
	visit(normRel, raw)
	return nil
}

// MaxFileLOCGate verifies that no file exceeds the line length limit.
type MaxFileLOCGate struct{}

func (g *MaxFileLOCGate) Name() string { return "Max File LOC Invariant" }

func (g *MaxFileLOCGate) Evaluate(ctx *vscope.VerifyContext) (*vscope.GateResult, error) {
	root := ctx.WorktreePath
	if root == "" {
		root = ctx.RepoRoot
	}
	thresh := resolveThresholds(ctx)
	var metrics []astdebt.FileMetric

	err := vscope.WalkTargetFiles(ctx, root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || !isPolyglotTargetFile(path) {
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

	violations := astdebt.EnrichFileLOCViolations(root, filterRatchetedFileLOC(ctx, root, metrics, thresh), thresh)
	violations = append(violations, EvaluatePackageFanOutViolations(ctx, root, metrics, thresh)...)
	violations = append(violations, EvaluateUpstreamSurgicalViolations(ctx, root, metrics)...)
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
	return countBytesLines(raw)
}

// ComplexityGate verifies cyclomatic complexity and function LOC bounds.
type ComplexityGate struct{}

func (g *ComplexityGate) Name() string { return "Cognitive Complexity Invariant" }

func (g *ComplexityGate) Evaluate(ctx *vscope.VerifyContext) (*vscope.GateResult, error) {
	root := ctx.WorktreePath
	if root == "" {
		root = ctx.RepoRoot
	}
	thresh := resolveThresholds(ctx)
	var funcMetrics []astdebt.FunctionMetric

	err := vscope.WalkTargetFiles(ctx, root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || !isPolyglotTargetFile(path) {
			return nil
		}
		rel, rErr := filepath.Rel(root, path)
		if rErr != nil {
			rel = path
		}
		if !strings.HasSuffix(path, ".go") {
			collectPolyglotComplexity(path, rel, &funcMetrics)
			return nil
		}
		fset, node, pErr := vscope.MemoizedParseGoFile(ctx, path, 0)
		if pErr != nil || node == nil {
			return nil
		}
		collectFunctionMetrics(node, fset, rel, &funcMetrics)
		return nil
	})
	if err != nil {
		return nil, types.NewNomosError(types.ErrCodeCore, "complexity walk failed: %v", err)
	}

	ratchetedFuncs, checkArity := filterRatchetedComplexity(ctx, root, funcMetrics, thresh)
	violations := astdebt.EnrichComplexityViolations(root, ratchetedFuncs, thresh, checkArity)
	violations = append(violations, evaluateBrownfieldArityRatchet(ctx, root, funcMetrics, thresh)...)
	violations = append(violations, evaluateGoSwitchIdiomViolations(ctx, root)...)
	violations = append(violations, evaluateTestComplexityViolations(ctx, root)...)
	if len(violations) > 0 {
		return &vscope.GateResult{
			GateName: g.Name(), Passed: false,
			Message: fmt.Sprintf("detected %d complexity/arity violation(s):\n    - %s", len(violations), strings.Join(violations, "\n    - ")),
			Remedy:  "decompose complex functions along the prescribed AST seams and encapsulate >8 parameters into structs",
		}, nil
	}
	return &vscope.GateResult{GateName: g.Name(), Passed: true, Message: "all functions within complexity bounds"}, nil
}

func collectPolyglotComplexity(path, rel string, out *[]astdebt.FunctionMetric) {
	if content, err := os.ReadFile(path); err == nil {
		collectPolyglotComplexityFromBytes(path, rel, content, out)
	}
}

func collectFunctionMetrics(node *ast.File, fset *token.FileSet, relPath string, out *[]astdebt.FunctionMetric) {
	for _, decl := range node.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		start := fset.Position(fn.Pos()).Line
		end := fset.Position(fn.End()).Line
		comp := astdebt.CalculateCyclomaticComplexity(fn)
		*out = append(*out, astdebt.FunctionMetric{
			File:       relPath,
			Line:       start,
			Name:       fn.Name.Name,
			Complexity: comp,
			LOC:        end - start + 1,
		})
	}
}

// HermeticWorktreeGate verifies worktree boundaries.
type HermeticWorktreeGate struct{}

func (g *HermeticWorktreeGate) Name() string { return "Hermetic Worktree Invariant" }

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

func (g *PhaseDisciplineGate) Name() string { return "Phase Discipline Invariant" }

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

func (g *CanonicalSingularityGate) Name() string { return "Canonical Singularity Invariant" }

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
