// Package polygate executes fast isolated relational boundary-mutant spot-checks on zero-baseline polyglot suites (Law 2).
package polygate

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/polyrun"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

const (
	ringDynamic                    = "dynamic"
	maxBoundaryMutantProbes        = 6
	maxBoundaryBaselineElapsed     = 1500 * time.Millisecond
	boundaryMutantExecTimeout      = 1500 * time.Millisecond
	minBoundaryMutantProdLOC       = 80
	maxBoundaryMutantProdLOC       = 2500
	maxBoundaryMutantImplFiles     = 16
	minBoundarySurvivingToFailGate = 2
)

const pyFastBoundaryRunner = `import io, os, sys, unittest
root = os.path.realpath(sys.argv[1])
targets = sys.argv[2:]
sys.path.insert(0, root)
try:
    import pytest
    sys.exit(0 if pytest.main(["-q", "--tb=no", "-p", "no:cacheprovider"] + targets) == 0 else 2)
except ImportError:
    pass
loader = unittest.defaultTestLoader
suite = unittest.TestSuite()
for t in targets:
    full = t if os.path.isabs(t) else os.path.join(root, t)
    if os.path.isfile(full):
        d, f = os.path.split(full)
        suite.addTests(loader.discover(d, pattern=f))
    elif os.path.isdir(full):
        suite.addTests(loader.discover(full, pattern="test*.py"))
if not targets:
    suite.addTests(loader.discover(root, pattern="test*.py"))
res = unittest.TextTestRunner(stream=io.StringIO()).run(suite)
sys.exit(0 if res.wasSuccessful() and res.testsRun > 0 else 2)
`

type boundaryMutantSite struct {
	relPath   string
	lineNum   int
	offset    int
	origOp    string
	mutOp     string
	origBytes []byte
}

func evaluatePolyglotBoundaryMutants(ctx *vscope.VerifyContext, root, source string, baselineElapsed time.Duration) *vscope.GateResult {
	if !shouldRunBoundaryMutantSpotCheck(ctx, root, source, baselineElapsed) {
		return nil
	}
	implFiles, prodLOC := collectRigorImplFilesAndLOC(ctx, root)
	if prodLOC < minBoundaryMutantProdLOC || prodLOC > maxBoundaryMutantProdLOC || len(implFiles) == 0 || len(implFiles) > maxBoundaryMutantImplFiles {
		return nil
	}
	sites := selectRelationalBoundarySites(root, implFiles, maxBoundaryMutantProbes)
	if len(sites) == 0 {
		return nil
	}
	survivors := probeBoundaryMutantsIsolated(ctx, root, source, sites)
	if len(survivors) >= minBoundarySurvivingToFailGate {
		return &vscope.GateResult{
			GateName: "Statement Coverage Invariant (Law 2)", ProbeID: ProbeIDCoverage, Law: 2, Ring: ringDynamic,
			Passed:  false,
			Message: fmt.Sprintf("boundary-mutant spot-check failed (%d/%d relational boundary flips survived: %s)", len(survivors), len(sites), strings.Join(survivors, ", ")),
			Remedy:  "verify exact <= vs < inclusivity against the specification and add explicit boundary test cases in .tmp/tests/",
		}
	}
	return nil
}

func shouldRunBoundaryMutantSpotCheck(ctx *vscope.VerifyContext, root, source string, elapsed time.Duration) bool {
	if elapsed <= 0 || elapsed >= maxBoundaryBaselineElapsed {
		return false
	}
	if !strings.HasPrefix(source, "Python") && !strings.HasPrefix(source, "JavaScript") && !strings.HasPrefix(source, "Ruby") {
		return false
	}
	if !hasZeroBaselineTrackedTests(root) {
		return false
	}
	return ctx != nil && !ctx.IsBaseline
}

func selectRelationalBoundarySites(root string, absFiles []string, limit int) []boundaryMutantSite {
	var strong, weak []boundaryMutantSite
	for _, abs := range absFiles {
		rel, rErr := filepath.Rel(root, abs)
		if rErr != nil {
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil || len(data) == 0 {
			continue
		}
		s1, s2 := collectFileBoundarySites(filepath.ToSlash(rel), data)
		strong = append(strong, s1...)
		weak = append(weak, s2...)
	}
	out := append(strong, weak...)
	if len(out) > limit {
		return out[:limit]
	}
	return out
}

func collectFileBoundarySites(relPath string, data []byte) ([]boundaryMutantSite, []boundaryMutantSite) {
	var strong, weak []boundaryMutantSite
	text := string(data)
	lineStart := 0
	for lineNum, raw := range strings.Split(text, "\n") {
		nextStart := lineStart + len(raw) + 1
		if isCandidateBoundaryLine(raw) {
			s1, s2 := findAllBoundaryOpsInLine(relPath, lineNum+1, lineStart, raw, data)
			strong = append(strong, s1...)
			weak = append(weak, s2...)
		}
		lineStart = nextStart
	}
	return strong, weak
}

func isCandidateBoundaryLine(raw string) bool {
	t := strings.TrimSpace(raw)
	if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "//") || strings.HasPrefix(t, "/*") || strings.HasPrefix(t, "*") {
		return false
	}
	if strings.HasPrefix(t, "if ") || strings.HasPrefix(t, "if(") ||
		strings.HasPrefix(t, "elif ") || strings.HasPrefix(t, "return ") || strings.HasPrefix(t, "while ") {
		return true
	}
	return strings.Contains(t, " if ") || strings.Contains(t, " = ")
}

func findAllBoundaryOpsInLine(relPath string, lineNum, lineStart int, raw string, fullData []byte) ([]boundaryMutantSite, []boundaryMutantSite) {
	type opPair struct {
		orig   string
		mut    string
		strong bool
	}
	pairs := []opPair{
		{" <= ", " < ", true},
		{" >= ", " > ", true},
		{" < ", " <= ", false},
		{" > ", " >= ", false},
	}
	quoteSafe := stripQuotedSpansKeepLen(raw)
	var strong, weak []boundaryMutantSite
	pos := 0
	for pos < len(quoteSafe) {
		bestIdx, bestPair := -1, opPair{}
		for _, p := range pairs {
			if idx := strings.Index(quoteSafe[pos:], p.orig); idx >= 0 && (bestIdx < 0 || idx < bestIdx) {
				bestIdx, bestPair = idx, p
			}
		}
		if bestIdx < 0 {
			break
		}
		site := boundaryMutantSite{
			relPath: relPath, lineNum: lineNum, offset: lineStart + pos + bestIdx,
			origOp: bestPair.orig, mutOp: bestPair.mut, origBytes: fullData,
		}
		if bestPair.strong {
			strong = append(strong, site)
		} else {
			weak = append(weak, site)
		}
		pos += bestIdx + len(bestPair.orig)
	}
	return strong, weak
}

func isInlineCommentStart(b []byte, i int) bool {
	if b[i] == '/' && i+1 < len(b) && b[i+1] == '/' {
		return true
	}
	return b[i] == '#' && (i == 0 || b[i-1] == ' ' || b[i-1] == '\t')
}

func stripQuotedSpansKeepLen(s string) string {
	b := []byte(s)
	quote := byte(0)
	escaped := false
	for i := 0; i < len(b); i++ {
		if escaped {
			b[i] = ' '
			escaped = false
			continue
		}
		if quote != 0 {
			if b[i] == '\\' {
				escaped = true
			} else if b[i] == quote {
				quote = 0
			}
			b[i] = ' '
			continue
		}
		if isInlineCommentStart(b, i) {
			for j := i; j < len(b); j++ {
				b[j] = ' '
			}
			break
		}
		if b[i] == '\'' || b[i] == '"' {
			quote = b[i]
			b[i] = ' '
		}
	}
	return string(b)
}

func probeBoundaryMutantsIsolated(ctx *vscope.VerifyContext, root, source string, sites []boundaryMutantSite) []string {
	tmpRoot, err := os.MkdirTemp("", "nomos-bmut-*")
	if err != nil {
		return nil
	}
	defer func() {
		if rmErr := os.RemoveAll(tmpRoot); rmErr != nil {
			return
		}
	}()
	if copyErr := copyWorkspaceForMutants(root, tmpRoot); copyErr != nil {
		return nil
	}
	var survivors []string
	for _, site := range sites {
		if runSingleMutantCheck(ctx, root, tmpRoot, source, site) {
			survivors = append(survivors, fmt.Sprintf("%s:%d (%s -> %s)", site.relPath, site.lineNum, strings.TrimSpace(site.origOp), strings.TrimSpace(site.mutOp)))
		}
	}
	return survivors
}

func runSingleMutantCheck(ctx *vscope.VerifyContext, origRoot, tmpRoot, source string, site boundaryMutantSite) bool {
	if site.offset < 0 || site.offset+len(site.origOp) > len(site.origBytes) {
		return false
	}
	target := filepath.Join(tmpRoot, filepath.FromSlash(site.relPath))
	mutated := make([]byte, 0, len(site.origBytes))
	mutated = append(mutated, site.origBytes[:site.offset]...)
	mutated = append(mutated, site.mutOp...)
	mutated = append(mutated, site.origBytes[site.offset+len(site.origOp):]...)
	if err := os.WriteFile(target, mutated, 0644); err != nil {
		return false
	}
	defer func() {
		if wErr := os.WriteFile(target, site.origBytes, 0644); wErr != nil {
			return
		}
	}()
	cmdCtx, cancel := context.WithTimeout(context.Background(), boundaryMutantExecTimeout)
	defer cancel()
	cmd := buildFastMutantTestCmd(cmdCtx, ctx, origRoot, tmpRoot, source)
	if cmd == nil {
		return false
	}
	return cmd.Run() == nil
}

func buildFastMutantTestCmd(cmdCtx context.Context, ctx *vscope.VerifyContext, origRoot, tmpRoot, source string) *exec.Cmd {
	switch {
	case strings.HasPrefix(source, "Python"):
		pyBin, err := polyrun.ResolvePythonBinary(origRoot, "python3")
		if err != nil {
			return nil
		}
		targets, shouldRun := polyrun.ResolveScopedPytestTargets(ctx, tmpRoot)
		if !shouldRun {
			return nil
		}
		args := append([]string{"-B", "-c", pyFastBoundaryRunner, tmpRoot}, targets...)
		cmd := exec.CommandContext(cmdCtx, pyBin, args...)
		cmd.Dir = tmpRoot
		cmd.Env = polyrun.SanitizedToolchainEnv(polyrun.EnvPyNoBytecodeKV, polyrun.EnvToolchainGateKV, "PYTHONPATH="+tmpRoot)
		return cmd
	case strings.HasPrefix(source, "JavaScript"):
		testFiles := polyrun.CollectJSTestFiles(tmpRoot)
		bin, args, ok := polyrun.ResolveJSTestCommand(tmpRoot, testFiles)
		if !ok {
			return nil
		}
		cmd := exec.CommandContext(cmdCtx, bin, args...)
		cmd.Dir = tmpRoot
		cmd.Env = polyrun.SanitizedToolchainEnv("CI=true", polyrun.EnvToolchainGateKV)
		return cmd
	case strings.HasPrefix(source, "Ruby"):
		rubyBin, err := exec.LookPath("ruby")
		if err != nil {
			return nil
		}
		testFiles := collectRubyUnitTestFiles(tmpRoot)
		if len(testFiles) == 0 {
			return nil
		}
		cmd := exec.CommandContext(cmdCtx, rubyBin, append([]string{"-I" + tmpRoot, "-e", "ARGV.each { |f| load File.expand_path(f) }"}, testFiles...)...)
		cmd.Dir = tmpRoot
		cmd.Env = polyrun.SanitizedToolchainEnv(polyrun.EnvToolchainGateKV)
		return cmd
	default:
		return nil
	}
}

func copyWorkspaceForMutants(srcRoot, dstRoot string) error {
	return filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, err error) error {
		return copyMutantEntry(srcRoot, dstRoot, path, d, err)
	})
}

var skipMutantCopyDirs = map[string]bool{
	".git": true, ".venv": true, "venv": true, "node_modules": true,
	"__pycache__": true, "worktrees": true, ".nomos": true,
}

func copyMutantEntry(srcRoot, dstRoot, path string, d fs.DirEntry, err error) error {
	if err != nil || d == nil {
		return nil
	}
	name := d.Name()
	if d.IsDir() {
		if path != srcRoot && skipMutantCopyDirs[name] {
			return filepath.SkipDir
		}
		return nil
	}
	if !isMutantCopyAllowedFile(name) {
		return nil
	}
	rel, rErr := filepath.Rel(srcRoot, path)
	if rErr != nil {
		return nil
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		return nil
	}
	dest := filepath.Join(dstRoot, rel)
	if mkErr := os.MkdirAll(filepath.Dir(dest), 0755); mkErr != nil {
		return mkErr
	}
	return os.WriteFile(dest, data, 0644)
}

func isMutantCopyAllowedFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	if rigorImplExts[ext] || isPolyglotTestFilePath(name) {
		return true
	}
	return ext == ".json" || ext == ".yaml" || ext == ".yml" || ext == ".toml" || ext == ".txt"
}
