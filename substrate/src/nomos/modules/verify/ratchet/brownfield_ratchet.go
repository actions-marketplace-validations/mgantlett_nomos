// Package ratchet implements baseline AST delta ratcheting for brownfield repositories.
package ratchet

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/astdebt"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

func resolveGitTreeRootForPath(ctx *vscope.VerifyContext, fallbackRoot, absFile string) string {
	candidates := []string{}
	if ctx != nil && ctx.WorktreePath != "" {
		candidates = append(candidates, ctx.WorktreePath)
	}
	if fallbackRoot != "" {
		candidates = append(candidates, fallbackRoot)
	}
	if ctx != nil && ctx.RepoRoot != "" {
		candidates = append(candidates, ctx.RepoRoot)
	}
	for _, c := range candidates {
		absC, err := filepath.Abs(c)
		if err != nil {
			continue
		}
		if vscope.HasGitMetadata(absC) && (absFile == absC || strings.HasPrefix(absFile, absC+string(filepath.Separator))) {
			return absC
		}
	}
	if wtRoot, err := workspace.FindWorkingTreeRoot(filepath.Dir(absFile)); err == nil && vscope.HasGitMetadata(wtRoot) {
		return wtRoot
	}
	return ""
}

func resolveBaselineCommitRef(ctx *vscope.VerifyContext, gitRoot string) string {
	refs := []string{"main", "origin/main", "master", "origin/master"}
	if cfg := vscope.ResolveVerifyConfig(ctx); cfg != nil && strings.TrimSpace(cfg.DefaultBranch) != "" {
		refs = append([]string{strings.TrimSpace(cfg.DefaultBranch)}, refs...)
	}
	for _, ref := range refs {
		if out, err := workspace.RunSafeGit(gitRoot, "merge-base", "HEAD", ref); err == nil {
			if sha := strings.TrimSpace(string(out)); sha != "" {
				return sha
			}
		}
	}
	if !vscope.IsWorktreeContext(ctx, gitRoot) {
		if out, err := workspace.RunSafeGit(gitRoot, "rev-parse", "--verify", "HEAD"); err == nil {
			return strings.TrimSpace(string(out))
		}
	}
	return ""
}

// LoadBaselineFileBytes reads targetPath from the closest Git baseline commit, resolving renames when moved.
func LoadBaselineFileBytes(ctx *vscope.VerifyContext, fallbackRoot, targetPath string) ([]byte, bool) {
	absFile, err := filepath.Abs(targetPath)
	gitRoot := resolveGitTreeRootForPath(ctx, fallbackRoot, absFile)
	if err != nil || gitRoot == "" {
		return nil, false
	}
	rel, rErr := filepath.Rel(gitRoot, absFile)
	baseRef := resolveBaselineCommitRef(ctx, gitRoot)
	if rErr != nil || strings.HasPrefix(rel, "..") || baseRef == "" {
		return nil, false
	}
	relSlash := filepath.ToSlash(rel)
	if out, sErr := workspace.RunSafeGit(gitRoot, "show", baseRef+":"+relSlash); sErr == nil {
		return out, true
	}
	oldRel := vscope.ResolveRenamedBaselinePath(ctx, gitRoot, baseRef, relSlash)
	if oldRel == "" || oldRel == relSlash {
		return nil, false
	}
	renamedOut, rShowErr := workspace.RunSafeGit(gitRoot, "show", baseRef+":"+oldRel)
	return renamedOut, rShowErr == nil
}

// FilterRatchetedFileLOC filters file LOC metrics against brownfield baseline ceilings.
func FilterRatchetedFileLOC(ctx *vscope.VerifyContext, root string, metrics []astdebt.FileMetric, thresh astdebt.Thresholds) []astdebt.FileMetric {
	if !vscope.IsBrownfieldRatchetActive(ctx) {
		return metrics
	}
	filtered := make([]astdebt.FileMetric, 0, len(metrics))
	for _, m := range metrics {
		if m.LOC <= thresh.MaxFileLOC {
			continue
		}
		baseBytes, ok := LoadBaselineFileBytes(ctx, root, filepath.Join(root, m.Name))
		if !ok {
			filtered = append(filtered, m)
			continue
		}
		if IsUpstreamSurgicalRatchet(ctx) {
			continue
		}
		baseLOC := CountBytesLines(baseBytes)
		allowed := thresh.MaxFileLOC
		if baseLOC > thresh.MaxFileLOC {
			allowed = baseLOC + thresh.MaxFuncLOC
		}
		if m.LOC > allowed {
			filtered = append(filtered, m)
		}
	}
	return filtered
}

// ParseBaselineFunctions parses baseline function metrics for Go or polyglot source bytes.
func ParseBaselineFunctions(absPath, rel string, content []byte) map[string]astdebt.FunctionMetric {
	var list []astdebt.FunctionMetric
	if strings.HasSuffix(absPath, ".go") {
		fset := token.NewFileSet()
		if node, err := parser.ParseFile(fset, absPath, content, 0); err == nil {
			CollectFunctionMetrics(node, fset, rel, &list)
		}
	} else {
		CollectPolyglotComplexityFromBytes(absPath, rel, content, &list)
	}
	byName := make(map[string]astdebt.FunctionMetric, len(list))
	for _, fn := range list {
		prev, exists := byName[fn.Name]
		if exists {
			fn.Complexity = MaxInt(prev.Complexity, fn.Complexity)
			fn.LOC = MaxInt(prev.LOC, fn.LOC)
		}
		byName[fn.Name] = fn
	}
	return byName
}

// FilterRatchetedComplexity filters function complexity and LOC metrics against brownfield baselines.
func FilterRatchetedComplexity(ctx *vscope.VerifyContext, root string, funcs []astdebt.FunctionMetric, thresh astdebt.Thresholds) ([]astdebt.FunctionMetric, bool) {
	if !vscope.IsBrownfieldRatchetActive(ctx) {
		return filterStrictNonWorktreeComplexity(ctx, root, funcs)
	}
	baseCache := make(map[string]map[string]astdebt.FunctionMetric)
	var out []astdebt.FunctionMetric
	for _, fn := range funcs {
		effMax := astdebt.EffectiveMaxComplexity(fn.File, thresh.MaxComplexity)
		if fn.Complexity <= effMax && fn.LOC <= thresh.MaxFuncLOC {
			continue
		}
		if adj, ok := evaluateSingleRatchetedFunc(ctx, root, fn, effMax, thresh.MaxFuncLOC, baseCache); ok {
			out = append(out, adj)
		}
	}
	return out, false
}

func evaluateSingleRatchetedFunc(ctx *vscope.VerifyContext, root string, fn astdebt.FunctionMetric, effMax, maxLOC int, baseCache map[string]map[string]astdebt.FunctionMetric) (astdebt.FunctionMetric, bool) {
	baseMap, loaded := baseCache[fn.File]
	if !loaded {
		absPath := filepath.Join(root, fn.File)
		if baseBytes, ok := LoadBaselineFileBytes(ctx, root, absPath); ok {
			baseMap = ParseBaselineFunctions(absPath, fn.File, baseBytes)
		}
		baseCache[fn.File] = baseMap
	}
	if baseMap == nil {
		return fn, true
	}
	baseFn := baseMap[fn.Name]
	adj := fn
	if fn.Complexity <= MaxInt(effMax, baseFn.Complexity) {
		adj.Complexity = effMax
	}
	if fn.LOC <= MaxInt(maxLOC, baseFn.LOC) {
		adj.LOC = maxLOC
	}
	return adj, adj.Complexity > effMax || adj.LOC > maxLOC
}

// EvaluatePackageFanOutViolations enforces MaxPackageFiles per directory with brownfield ratcheting (@intent tree_sitter_ast_gate_spec.md #Thresholds).
func EvaluatePackageFanOutViolations(ctx *vscope.VerifyContext, root string, metrics []astdebt.FileMetric, thresh astdebt.Thresholds) []string {
	maxFiles := thresh.MaxPackageFiles
	if maxFiles <= 0 {
		maxFiles = astdebt.DefaultThresholds().MaxPackageFiles
	}
	dirs := collectUniqueMetricDirs(metrics)
	var violations []string
	for _, relDir := range dirs {
		if v := evaluateSingleDirFanOut(ctx, root, relDir, maxFiles); v != "" {
			violations = append(violations, v)
		}
	}
	for _, m := range metrics {
		if v := EvaluateNetNewMicroFileViolation(ctx, root, filepath.Join(root, m.Name), maxFiles, thresh.MaxFileLOC, m.LOC); v != "" {
			violations = append(violations, v)
		}
	}
	return violations
}

func collectUniqueMetricDirs(metrics []astdebt.FileMetric) []string {
	seen := make(map[string]bool, len(metrics))
	var dirs []string
	for _, m := range metrics {
		relDir := filepath.ToSlash(filepath.Dir(m.Name))
		if !seen[relDir] {
			seen[relDir] = true
			dirs = append(dirs, relDir)
		}
	}
	sort.Strings(dirs)
	return dirs
}

func evaluateSingleDirFanOut(ctx *vscope.VerifyContext, root, relDir string, maxFiles int) string {
	absDir := filepath.Join(root, filepath.FromSlash(relDir))
	curCount := countDirNonTestSourceFiles(absDir)
	if curCount <= maxFiles {
		return ""
	}
	ratchet := vscope.IsBrownfieldRatchetActive(ctx) || (ctx != nil && !ctx.Strict && !ctx.AllFiles && vscope.IsWorktreeContext(ctx, root))
	if ratchet {
		if baseCount, ok := countBaselineDirSourceFiles(ctx, root, absDir); ok {
			ceil := MaxInt(maxFiles, baseCount)
			if curCount <= ceil {
				return ""
			}
			return fmt.Sprintf("%s has %d non-test source files (exceeds brownfield ceiling %d, max %d) -> [1-TURN RECIPE: extract >=%d cohesive file(s) from %s into a sub-package]",
				relDir, curCount, ceil, maxFiles, curCount-ceil, relDir)
		}
	}
	return fmt.Sprintf("%s has %d non-test source files (exceeds max %d) -> [1-TURN RECIPE: extract >=%d cohesive file(s) from %s into focused sub-packages]",
		relDir, curCount, maxFiles, curCount-maxFiles, relDir)
}

func countDirNonTestSourceFiles(absDir string) int {
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() && IsPolyglotTargetFile(filepath.Join(absDir, e.Name())) {
			count++
		}
	}
	return count
}

func countBaselineDirSourceFiles(ctx *vscope.VerifyContext, fallbackRoot, absDir string) (int, bool) {
	cleanAbs, err := filepath.Abs(absDir)
	if err != nil {
		return 0, false
	}
	gitRoot := resolveGitTreeRootForPath(ctx, fallbackRoot, cleanAbs)
	baseRef := resolveBaselineCommitRef(ctx, gitRoot)
	if gitRoot == "" || baseRef == "" {
		return 0, false
	}
	rel, rErr := filepath.Rel(gitRoot, cleanAbs)
	if rErr != nil || strings.HasPrefix(rel, "..") {
		return 0, false
	}
	treeSpec := baseRef
	if rel != "." {
		treeSpec = baseRef + ":" + filepath.ToSlash(rel)
	}
	out, lErr := workspace.RunSafeGit(gitRoot, "ls-tree", "--name-only", treeSpec)
	if lErr != nil {
		return 0, false
	}
	count := 0
	for _, name := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if name != "" && IsPolyglotTargetFile(filepath.Join(cleanAbs, name)) {
			count++
		}
	}
	return count, true
}

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

func buildModifiedRelSet(modified []string) map[string]bool {
	set := make(map[string]bool, len(modified))
	for _, m := range modified {
		set[filepath.ToSlash(filepath.Clean(m))] = true
	}
	return set
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

func hasLeadingGoBuildTag(raw []byte) bool {
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "/*") {
			continue
		}
		if strings.HasPrefix(trimmed, "//go:build ") || strings.HasPrefix(trimmed, "// +build ") {
			return true
		}
		if !strings.HasPrefix(trimmed, "//") {
			return false
		}
	}
	return false
}

func findSmallestReceptiveSibling(absPath string, maxFileLOC, fileLOC int) (string, int) {
	entries, err := os.ReadDir(filepath.Dir(absPath))
	if err != nil || maxFileLOC <= 15 {
		return "", 0
	}
	dir := filepath.Dir(absPath)
	targetBase, targetExt := filepath.Base(absPath), filepath.Ext(absPath)
	bestName, bestLOC := "", 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || name == targetBase || !strings.EqualFold(filepath.Ext(name), targetExt) {
			continue
		}
		if sibLOC := readReceptiveSiblingLOC(filepath.Join(dir, name), strings.EqualFold(targetExt, astdebt.ExtGo)); sibLOC > 0 && sibLOC+fileLOC <= maxFileLOC-15 && (bestName == "" || sibLOC < bestLOC) {
			bestName, bestLOC = name, sibLOC
		}
	}
	return bestName, bestLOC
}

func readReceptiveSiblingLOC(sibAbs string, isGo bool) int {
	if !IsPolyglotTargetFile(sibAbs) {
		return 0
	}
	sibRaw, err := os.ReadFile(sibAbs)
	if err != nil || (isGo && hasLeadingGoBuildTag(sibRaw)) {
		return 0
	}
	return CountBytesLines(sibRaw)
}
