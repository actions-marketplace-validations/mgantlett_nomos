package verify

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/astdebt"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/polyrun"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/treesitter"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

func initTestRepoWithMainFile(t *testing.T, rel, content string) string {
	t.Helper()
	dir := initTestGitRepo(t)
	if rel != "" {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		if out, err := workspace.RunSafeGit(dir, "add", rel); err != nil {
			t.Fatalf("git add %s: %v (%s)", rel, err, string(out))
		}
		if out, err := workspace.RunSafeGit(dir, "commit", "-m", "baseline"); err != nil {
			t.Fatalf("git commit: %v (%s)", err, string(out))
		}
	}
	return dir
}

// @verifies TC-TS-22
func TestR3Tuning_PyTernaryCogAndPolyglotCC10(t *testing.T) {
	aggPy := `class Agg:
    def add(self, fn, v):
        if fn == "count":
            self.n += 1 if self.star else v is not None
        elif fn in ("sum", "avg"):
            if is_num(v):
                self.n += 1
                self.sum = v if self.sum is None else self.sum + v
        elif fn in ("min", "max"):
            pick = min if fn == "min" else max
            if is_num(v):
                self.nums = v if self.nums is None else pick(self.nums, v)
            elif isinstance(v, str):
                self.strs = v if self.strs is None else pick(self.strs, v)
`
	cogHits := astdebt.EvaluatePolyglotCognitiveViolations("logql_agg.py", []byte(aggPy), astdebt.MaxFunctionCognitiveComplexity)
	if len(cogHits) == 0 {
		t.Fatalf("expected Agg.add inline ternaries to exceed cognitive complexity 10")
	}

	var b strings.Builder
	b.WriteString("def branchy(x):\n")
	for i := 0; i < 11; i++ {
		b.WriteString(fmt.Sprintf("    if x == %d:\n        return %d\n", i, i))
	}
	b.WriteString("    return 99\n")

	engine, err := treesitter.NewTreeSitterEngine(context.Background())
	if err != nil {
		t.Fatalf("engine init: %v", err)
	}
	defer engine.Close(context.Background())
	rep, rErr := engine.ParseAndEvaluate(context.Background(), "branchy.py", []byte(b.String()))
	if rErr != nil || rep.Passed {
		t.Fatalf("expected Python CC=12 > 10 to fail GateEvaluationReport, got err=%v passed=%v", rErr, rep != nil && rep.Passed)
	}
	if astdebt.EffectiveMaxComplexity("handler.go", 15) != 15 || astdebt.EffectiveMaxComplexity("branchy.py", 15) != 10 {
		t.Fatalf("unexpected EffectiveMaxComplexity defaults")
	}
	if astdebt.EffectiveMaxComplexity("branchy.py", 12) != 12 {
		t.Fatalf("expected explicit override 12 to be preserved")
	}
}

// @verifies TC-TS-21
func TestR3Tuning_NonWorktreeBrownfieldAndStrictVerifyParity(t *testing.T) {
	legacyPy := "def legacy_complex(a, b, c, d, e, f, g, h, i):\n    if a:\n        if b:\n            if c:\n                return d\n    return 0\n"
	dir := initTestRepoWithMainFile(t, "legacy.py", legacyPy)
	brownYAML := "project: bf-py\ndefault_branch: main\ngovernance:\n  quality_gates:\n    strict_mode: false\n"
	if err := os.WriteFile(filepath.Join(dir, "nomos.yaml"), []byte(brownYAML), 0644); err != nil {
		t.Fatal(err)
	}
	badApp := "def bad_validate(x, y, z):\n    if not isinstance(x, str): raise ValueError('x')\n    if not isinstance(y, int): raise ValueError('y')\n    if not isinstance(z, dict): raise ValueError('z')\n    return True\n"
	if err := os.WriteFile(filepath.Join(dir, "app.py"), []byte(badApp), 0644); err != nil {
		t.Fatal(err)
	}
	ctx := &vscope.VerifyContext{RepoRoot: dir, WorktreePath: dir, Phase: "EDIT"}
	cdsRes, err := (&CognitiveDebtGate{}).Evaluate(ctx)
	if err != nil || cdsRes.Passed || !strings.Contains(cdsRes.Message, "app.py") || strings.Contains(cdsRes.Message, "legacy.py") {
		t.Fatalf("expected non-worktree brownfield CognitiveDebtGate to fail on app.py and skip untouched legacy.py, got err=%v res=%+v", err, cdsRes)
	}

	nestedApp := "def deep(xs):\n  for a in xs:\n    if a:\n      for b in a:\n        if b:\n          for c in b:\n            if c:\n              return c\n  return None\n"
	if err := os.WriteFile(filepath.Join(dir, "app.py"), []byte(nestedApp), 0644); err != nil {
		t.Fatal(err)
	}
	compRes, cErr := (&ComplexityGate{}).Evaluate(ctx)
	if cErr != nil || compRes.Passed || !strings.Contains(compRes.Message, "app.py") || strings.Contains(compRes.Message, "legacy.py") {
		t.Fatalf("expected non-worktree brownfield ComplexityGate to flag app.py cognitive/nesting and skip legacy.py, got err=%v res=%+v", cErr, compRes)
	}
}

// @verifies TC-COV-005
func TestR3Tuning_TmpTestsRealpathCoverageAndPairing(t *testing.T) {
	dir := initTestRepoWithMainFile(t, "nomos.yaml", "project: py-cov\ndefault_branch: main\ngovernance:\n  quality_gates:\n    strict_mode: false\n    min_test_coverage_pct: 80\n")
	if err := os.WriteFile(filepath.Join(dir, "calc.py"), []byte("def add(a, b):\n    return a + b\n\ndef mul(a, b):\n    return a * b\n"), 0644); err != nil {
		t.Fatal(err)
	}
	tmpTests := filepath.Join(dir, ".tmp", "tests")
	if err := os.MkdirAll(tmpTests, 0755); err != nil {
		t.Fatal(err)
	}
	testSrc := "import os, sys, unittest\nsys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', '..'))\nimport calc\nclass T(unittest.TestCase):\n    def test_ops(self):\n        self.assertEqual(calc.add(2, 3), 5)\n        self.assertEqual(calc.mul(2, 3), 6)\n"
	if err := os.WriteFile(filepath.Join(tmpTests, "test_calc.py"), []byte(testSrc), 0644); err != nil {
		t.Fatal(err)
	}
	ctx := &vscope.VerifyContext{RepoRoot: dir, WorktreePath: dir, Phase: "EDIT"}
	targets, ok := polyrun.ResolveScopedPytestTargets(ctx, dir)
	if !ok || len(targets) == 0 {
		t.Fatalf("expected .tmp/tests paired target discovery, got ok=%v targets=%v", ok, targets)
	}
	covRes, err := (&CoverageGate{}).Evaluate(ctx)
	if err != nil || !covRes.Passed {
		t.Fatalf("expected realpath-normalized CoverageGate >= 80%% from .tmp/tests relative sys.path, got err=%v res=%+v", err, covRes)
	}
}

// @verifies TC-TS-21
func TestR3Tuning_IsinstanceSprawlAndMiddlemanWrappers(t *testing.T) {
	sprawlPy := `def a(x):
    if isinstance(x, bool) or not isinstance(x, int): raise ValueError('a')
    return x

def b(x):
    if not isinstance(x, str): raise ValueError('b')
    return x

def c(x):
    if not isinstance(x, list): raise ValueError('c')
    return x

def d(x):
    if not isinstance(x, dict): raise ValueError('d')
    return x

def e(x):
    if not isinstance(x, float): raise ValueError('e')
    return x

def mid_alias(v):
    return target_fn(v)

def shared_fmt(s):
    return "prefix:" + str(s)

def caller():
    return mid_alias(shared_fmt(1) + shared_fmt(2))
`
	hits := astdebt.DetectPolyglotDesignSlop(t.TempDir(), map[string][]byte{"mod.py": []byte(sprawlPy)})
	joined := strings.Join(hits, "\n")
	if !strings.Contains(joined, "scatters 5 inline 'isinstance(...): raise' guards") {
		t.Fatalf("expected module-level isinstance sprawl hit, got:\n%s", joined)
	}
	if !strings.Contains(joined, `single-caller pass-through middleman "mid_alias"`) {
		t.Fatalf("expected single-caller middleman hit, got:\n%s", joined)
	}
	if strings.Contains(joined, "shared_fmt") {
		t.Fatalf("expected multi-caller helper shared_fmt to remain allowed, got:\n%s", joined)
	}
}
