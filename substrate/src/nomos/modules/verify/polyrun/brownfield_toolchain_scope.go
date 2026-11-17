// Package polyrun implements brownfield toolchain scoping and baseline coverage ratcheting.
package polyrun

import (
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/astdebt"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/impact"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// ResolveEffectiveCoverageThreshold executes deterministic polyrun verification logic (@intent nomos_intent_traceability_spec.md).
func ResolveEffectiveCoverageThreshold(ctx *vscope.VerifyContext, root, targetDir string, configured, curAvg float64) float64 {
	if curAvg >= configured || !IsBrownfieldGitScope(ctx, root) {
		return configured
	}
	if !vscope.IsWorktreeContext(ctx, root) {
		if len(vscope.GetModifiedFilesInScope(ctx, root)) == 0 {
			return curAvg
		}
		return configured
	}
	baseTargetDir := resolveBaselineGoTargetDir(ctx, root, targetDir)
	if baseTargetDir == "" {
		return configured
	}
	pkgs := ResolveGoTestPackages(ctx, root, targetDir)
	baseRec := executeGoPackagesCoverage(ctx.RepoRoot, baseTargetDir, pkgs)
	if baseRec != nil && baseRec.GoPassed && baseRec.AvgCoverage < configured {
		return baseRec.AvgCoverage
	}
	return configured
}

// IsBrownfieldGitScope executes deterministic polyrun verification logic (@intent nomos_intent_traceability_spec.md).
func IsBrownfieldGitScope(ctx *vscope.VerifyContext, root string) bool {
	if !vscope.IsBrownfieldRatchetActive(ctx) {
		return false
	}
	if vscope.HasGitMetadata(root) {
		return true
	}
	return ctx != nil && vscope.HasGitMetadata(ctx.RepoRoot)
}

func resolveBaselineGoTargetDir(ctx *vscope.VerifyContext, root, targetDir string) string {
	if ctx == nil || ctx.RepoRoot == "" || ctx.RepoRoot == root {
		return ""
	}
	relTarget, err := filepath.Rel(root, targetDir)
	if err != nil || strings.HasPrefix(relTarget, "..") {
		return ""
	}
	baseTargetDir := filepath.Join(ctx.RepoRoot, relTarget)
	fi, sErr := os.Stat(baseTargetDir)
	if sErr != nil || !fi.IsDir() {
		return ""
	}
	return baseTargetDir
}

func executeGoPackagesCoverage(repoRoot, targetDir string, pkgs []string) *ToolchainSuiteReceipt {
	if len(pkgs) == 0 {
		pkgs = []string{"./..."}
	}
	baseCtx := &vscope.VerifyContext{RepoRoot: repoRoot, IsBaseline: true}
	return executeGoTestWithPackages(baseCtx, repoRoot, targetDir, "", pkgs)
}

func resolveModifiedGoPackages(ctx *vscope.VerifyContext, root, targetDir string) []string {
	if !vscope.IsBrownfieldRatchetActive(ctx) || !vscope.IsWorktreeContext(ctx, root) {
		return nil
	}
	modified := vscope.GetModifiedFilesInScope(ctx, root)
	if len(modified) == 0 {
		return nil
	}
	pkgSet := make(map[string]struct{})
	for _, rel := range modified {
		if !strings.HasSuffix(rel, ".go") {
			continue
		}
		absPkg := filepath.Dir(filepath.Join(root, rel))
		if fi, err := os.Stat(absPkg); err != nil || !fi.IsDir() {
			continue
		}
		relPkg, rErr := filepath.Rel(targetDir, absPkg)
		if rErr != nil || strings.HasPrefix(relPkg, "..") {
			continue
		}
		slash := filepath.ToSlash(relPkg)
		if slash == "." || slash == "" {
			pkgSet["."] = struct{}{}
		} else {
			pkgSet["./"+slash] = struct{}{}
		}
	}
	if len(pkgSet) == 0 {
		return nil
	}
	ExpandModifiedGoCallerPackages(root, targetDir, modified, pkgSet)
	out := make([]string, 0, len(pkgSet))
	for p := range pkgSet {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// ExpandModifiedGoCallerPackages executes deterministic polyrun verification logic (@intent nomos_intent_traceability_spec.md).
func ExpandModifiedGoCallerPackages(root, targetDir string, modified []string, pkgSet map[string]struct{}) {
	goReverse := impact.BuildGoReverseImportIndex(root)
	if len(goReverse) == 0 {
		return
	}
	seenPkgDirs := make(map[string]bool)
	for _, rel := range modified {
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			continue
		}
		pkgDir := filepath.ToSlash(filepath.Dir(rel))
		if seenPkgDirs[pkgDir] {
			continue
		}
		seenPkgDirs[pkgDir] = true
		addReverseCallerTargetPkgs(root, targetDir, pkgDir, goReverse, pkgSet)
	}
}

func addReverseCallerTargetPkgs(root, targetDir, pkgDir string, goReverse map[string][]string, pkgSet map[string]struct{}) {
	for _, callerDir := range impact.LookupGoReverseImporterDirs(root, pkgDir, goReverse) {
		absCaller := filepath.Join(root, filepath.FromSlash(callerDir))
		relPkg, err := filepath.Rel(targetDir, absCaller)
		if err != nil || strings.HasPrefix(relPkg, "..") {
			continue
		}
		slash := filepath.ToSlash(relPkg)
		if slash == "." || slash == "" {
			pkgSet["."] = struct{}{}
		} else {
			pkgSet["./"+slash] = struct{}{}
		}
	}
}

// ResolvePythonBinary executes deterministic polyrun verification logic (@intent nomos_intent_traceability_spec.md).
func ResolvePythonBinary(root, name string) (string, error) {
	for _, venvDir := range []string{".venv", "venv"} {
		candidate := filepath.Join(root, venvDir, "bin", name)
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
			return candidate, nil
		}
	}
	return exec.LookPath(name)
}

// DiscoverPythonScratchTestDirs returns relative untracked .tmp/tests or .tmp/test directories holding Python tests (@intent nomos_intent_traceability_spec.md).
func DiscoverPythonScratchTestDirs(root string) []string {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	var out []string
	for _, rel := range []string{filepath.ToSlash(filepath.Join(".tmp", astdebt.DirTests)), filepath.ToSlash(filepath.Join(".tmp", astdebt.DirTest))} {
		if dirHasPyTestFile(filepath.Join(root, filepath.FromSlash(rel))) {
			out = append(out, rel)
		}
	}
	return out
}

func dirHasPyTestFile(dir string) bool {
	found := false
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() || found {
			return nil
		}
		if isPythonTestFilename(d.Name()) {
			found = true
		}
		return nil
	})
	return found
}

func isPythonTestFilename(name string) bool {
	return (strings.HasPrefix(name, astdebt.PrefixPyTest) && strings.HasSuffix(name, astdebt.ExtPy)) ||
		strings.HasSuffix(name, astdebt.SuffixPyTest)
}

func collectExplicitPytestRoots(root string, scratch []string) []string {
	var out []string
	for _, topDir := range []string{astdebt.DirTests, astdebt.DirTest} {
		if fi, err := os.Stat(filepath.Join(root, topDir)); err == nil && fi.IsDir() && dirHasPyTestFile(filepath.Join(root, topDir)) {
			out = append(out, topDir)
		}
	}
	if entries, err := os.ReadDir(root); err == nil {
		for _, e := range entries {
			if !e.IsDir() && isPythonTestFilename(e.Name()) {
				out = append(out, e.Name())
			}
		}
	}
	return append(out, scratch...)
}

// ResolveScopedPytestTargets executes deterministic polyrun verification logic (@intent nomos_intent_traceability_spec.md).
func ResolveScopedPytestTargets(ctx *vscope.VerifyContext, root string) ([]string, bool) {
	scratch := DiscoverPythonScratchTestDirs(root)
	if !vscope.IsBrownfieldRatchetActive(ctx) || !vscope.IsWorktreeContext(ctx, root) {
		if len(scratch) > 0 {
			return collectExplicitPytestRoots(root, scratch), true
		}
		return nil, true
	}
	modified := vscope.GetModifiedFilesInScope(ctx, root)
	if len(modified) == 0 {
		return nil, false
	}
	targets := collectBrownfieldPyTargets(root, modified)
	if len(targets) == 0 && len(scratch) > 0 {
		return scratch, true
	}
	if len(targets) == 0 {
		return nil, false
	}
	return appendUniquePyTargets(targets, scratch), true
}

func appendUniquePyTargets(targets, extra []string) []string {
	if len(extra) == 0 {
		return targets
	}
	seen := make(map[string]bool, len(targets)+len(extra))
	out := make([]string, 0, len(targets)+len(extra))
	for _, t := range targets {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	for _, e := range extra {
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	return out
}

func collectBrownfieldPyTargets(root string, modified []string) []string {
	targets := make(map[string]struct{})
	for _, rel := range modified {
		slash := filepath.ToSlash(rel)
		base := filepath.Base(slash)
		if isPythonTestFilename(base) {
			if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
				targets[slash] = struct{}{}
			}
			continue
		}
		if strings.HasSuffix(base, astdebt.ExtPy) && base != "__init__.py" {
			addPairedPyCandidates(root, rel, strings.TrimSuffix(base, astdebt.ExtPy), targets)
		}
	}
	list := make([]string, 0, len(targets))
	for t := range targets {
		list = append(list, t)
	}
	sort.Strings(list)
	return list
}

func addPairedPyCandidates(root, rel, stem string, targets map[string]struct{}) {
	for _, cand := range []string{
		filepath.Join(astdebt.DirTests, astdebt.PrefixPyTest+stem+astdebt.ExtPy),
		filepath.Join(astdebt.DirTests, stem+astdebt.SuffixPyTest),
		filepath.Join(astdebt.DirTest, astdebt.PrefixPyTest+stem+astdebt.ExtPy),
		filepath.Join(".tmp", astdebt.DirTests, astdebt.PrefixPyTest+stem+astdebt.ExtPy),
		filepath.Join(".tmp", astdebt.DirTests, stem+astdebt.SuffixPyTest),
		filepath.Join(".tmp", astdebt.DirTest, astdebt.PrefixPyTest+stem+astdebt.ExtPy),
		filepath.Join(".tmp", astdebt.DirTest, stem+astdebt.SuffixPyTest),
		filepath.Join(filepath.Dir(rel), astdebt.PrefixPyTest+stem+astdebt.ExtPy),
	} {
		if _, err := os.Stat(filepath.Join(root, cand)); err == nil {
			targets[filepath.ToSlash(cand)] = struct{}{}
		}
	}
}

// ResolveNomosInternalCLIDir executes deterministic polyrun verification logic (@intent nomos_intent_traceability_spec.md).
func ResolveNomosInternalCLIDir(ctx *vscope.VerifyContext, root string) string {
	subCLI := filepath.Join(root, "substrate", "cli")
	if _, err := os.Stat(subCLI); err == nil {
		return subCLI
	}
	if vscope.IsBrownfieldRatchetActive(ctx) {
		return ""
	}
	topCLI := filepath.Join(root, "cli")
	if _, err := os.Stat(topCLI); err == nil {
		return topCLI
	}
	return ""
}
