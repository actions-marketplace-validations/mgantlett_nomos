package verify

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/astdebt"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/polyrun"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

// @verifies TC-TS-23
func TestR4_TwoTierWrappersAndPolyglotWrappers(t *testing.T) {
	py := `def _day_matches(d, days):
    return days is None or d in days

def caller(d, days):
    return _day_matches(d, days)

def is_num(v):
    return isinstance(v, (int, float))

def truthy(v):
    return v is not None and bool(v)

def is_str(v):
    return isinstance(v, str)

def use_multi(a, b):
    return is_num(a) and is_num(b) and truthy(a) and truthy(b) and is_str(a) and is_str(b)

class Parser:
    def peek(self, n=0):
        return self.t[n] if n < len(self.t) else None
    def is_star(self, k):
        return k == "id" and self.peek(1) == "*"
`
	hits := astdebt.DetectPolyglotDesignSlop(t.TempDir(), map[string][]byte{"mod.py": []byte(py)})
	joined := strings.Join(hits, "\n")
	for _, want := range []string{
		`excessive trivial wrapper fragmentation (2 single-use 1-line return helpers: _day_matches, peek)`,
		`module defines 3 multi-caller one-line return micro-helpers`,
		`dead pass-through method "is_star"`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in Python wrapper hits:\n%s", want, joined)
		}
	}

	singleOnlyPy := "def _day_matches(d, days):\n    return days is None or d in days\n\ndef f(d, days):\n    return _day_matches(d, days)\n"
	singleHits := astdebt.DetectPolyglotDesignSlop(t.TempDir(), map[string][]byte{"single.py": []byte(singleOnlyPy)})
	if len(singleHits) != 1 || !strings.Contains(singleHits[0], `single-caller one-line return helper "_day_matches"`) {
		t.Fatalf("expected 1 single-caller one-line helper violation, got %v", singleHits)
	}

	ts := `function _deadPriv(x: number): number { return compute(x); }
function mid(x: number): number { return target(x); }
export function useMid(x: number): number { return mid(x); }
`
	tsHits := astdebt.DetectPolyglotDesignSlop(t.TempDir(), map[string][]byte{"mod.ts": []byte(ts)})
	tsJoined := strings.Join(tsHits, "\n")
	if !strings.Contains(tsJoined, `dead private wrapper "_deadPriv"`) || !strings.Contains(tsJoined, `single-caller pass-through middleman "mid"`) {
		t.Fatalf("expected TS dead private & middleman wrapper hits, got:\n%s", tsJoined)
	}
}

// @verifies TC-TS-23
func TestR4_IndentCogParityAndSiblingClones(t *testing.T) {
	glossPy := `def chk_gloss(q, outer):
    for cands, _ in gloss_decls(q):
        for c in cands:
            check(c[0] != "CANON", "no canon")
            if c[0] == "UPTREE":
                chk_uptree(c, outer)
            else:
                check(len(c) > 1 and c[0] in q.bound, "unknown alias")
`
	cogHits := astdebt.EvaluatePolyglotCognitiveViolations("check.py", []byte(glossPy), astdebt.MaxFunctionCognitiveComplexity)
	if len(cogHits) != 1 || !strings.Contains(cogHits[0], "cognitive complexity 11 exceeds max 10") {
		t.Fatalf("expected chk_gloss cognitive complexity 11 > 10, got %v", cogHits)
	}

	parserPy := `class P:
    def or_(self):
        n = self.and_()
        while self.peek() == ("kw", "or"):
            self.i += 1; n = ("or", n, self.and_())
        return n
    def and_(self):
        n = self.atom()
        while self.peek() == ("kw", "and"):
            self.i += 1; n = ("and", n, self.atom())
        return n
`
	clones := astdebt.DetectStructuralClones(map[string][]byte{"parser.py": []byte(parserPy)}, astdebt.DefaultMinCloneLines)
	if len(clones) != 1 {
		t.Fatalf("expected 1 intra-file sibling clone group for or_/and_, got %+v", clones)
	}
}

// @verifies TC-TS-23
func TestR4_PythonExtraIdiomsControlFlowAndTuples(t *testing.T) {
	py := `def validate_many(a, b, c):
    if not isinstance(a, dict): fail("a")
    if not isinstance(b, str): fail("b")
    if not isinstance(c, list): fail("c")
    return True

def dispatch(k, x):
    if k == "num":
        return float(x)
    if k in ("str", "id"):
        return str(x)
    if x is not None:
        return x
    else:
        return "default"

def cmp_bool(v, op, lit):
    if not isinstance(v, bool):
        return False
    return {"=": v == lit}.get(op, False)

def lookup(rec, path):
    if not isinstance(rec, dict):
        return False, None
    return True, rec

def collect(cfg, pairs, idx, k):
    a = cfg.get("a", {})
    b = cfg.get("b", [])
    if type(k) is not int:
        pass
    out = []
    for item in pairs:
        out.append(item)
    return a, b, out
`
	hits := astdebt.DetectPolyglotDesignSlop(t.TempDir(), map[string][]byte{"slop.py": []byte(py)})
	joined := strings.Join(hits, "\n")
	for _, want := range []string{
		"inline 'isinstance(...): raise' guards",
		"2 consecutive 'if k ==' checks",
		"unnecessary 'else' after return/raise/break",
		"before opposite boolean return",
		"Go-style (value, err) tuple returns",
		"avoid 'type(x) ==/is Cls'",
		"repeats empty mutable default .get(k, [])/{}",
		"verbose list append loop",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in slop.py hits:\n%s", want, joined)
		}
	}
}

// @verifies TC-TS-23
func TestR4_PolyglotBraceIdiomsExemptGo(t *testing.T) {
	ts := `export function check(a: string | null, op: string, f: boolean): [boolean, string | null] {
  const x = a !== null && a !== undefined ? a : "d";
  if (op === "a") { return [false, null]; }
  if (op === "b") { return [true, x]; }
  return [true, x];
}
`
	hits := astdebt.DetectPolyglotDesignSlop(t.TempDir(), map[string][]byte{"mod.ts": []byte(ts)})
	joined := strings.Join(hits, "\n")
	for _, want := range []string{"Go-style [value, err]/[ok, value]", "nullish coalescing '??'", "consecutive 'if (op === ...)'"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in TS hits:\n%s", want, joined)
		}
	}
	goSrc := "package demo\nfunc parse(s string) (string, error) { if s == \"\" { return \"\", nil }; return s, nil }\n"
	if goHits := astdebt.DetectPolyglotDesignSlop(t.TempDir(), map[string][]byte{"mod.go": []byte(goSrc)}); len(goHits) != 0 {
		t.Fatalf("expected Go files to remain exempt from polyglot idiom slop, got %v", goHits)
	}
}

func buildBoundaryFixtureProdPy() string {
	var b strings.Builder
	b.WriteString("def in_window(ts, start, end):\n")
	b.WriteString("    if start <= ts and ts <= end:\n")
	b.WriteString("        return True\n")
	b.WriteString("    return False\n\n")
	b.WriteString("def classify(val):\n")
	b.WriteString("    total = val\n")
	for i := 0; i < 98; i++ {
		b.WriteString("    total += 0\n")
	}
	b.WriteString("    return total >= 10\n")
	return b.String()
}

// @verifies TC-COV-006
func TestR4_CoverageBreadthAndIsolatedBoundaryMutants(t *testing.T) {
	t.Setenv(polyrun.EnvInToolchainGate, "")
	t.Setenv(polyrun.EnvSkipToolchainGate, "")
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	dir := initTestGitRepo(t)
	mustWriteVerifyFile(t, filepath.Join(dir, "nomos.yaml"), "project: r4-rigor\ndefault_branch: main\ngovernance:\n  quality_gates:\n    strict_mode: true\n")
	prodPy := buildBoundaryFixtureProdPy()
	mustWriteVerifyFile(t, filepath.Join(dir, "win.py"), prodPy)
	commitAllTestGit(t, dir, "feat: add win")

	shallowTest := "import os, sys, unittest\nsys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', '..'))\nimport win\nclass T(unittest.TestCase):\n    def test_basic(self):\n        self.assertTrue(win.in_window(5, 1, 10))\n        self.assertEqual(win.classify(15), True)\n"
	testPath := filepath.Join(dir, ".tmp", "tests", "test_win.py")
	mustWriteVerifyFile(t, testPath, shallowTest)
	ctx := &vscope.VerifyContext{RepoRoot: dir, WorktreePath: dir, Strict: true}

	shallowRes, err := (&CoverageGate{}).Evaluate(ctx)
	if err != nil || shallowRes.Passed || !strings.Contains(shallowRes.Message, "shallow polyglot test breadth") {
		t.Fatalf("expected shallow test breadth rejection, got err=%v res=%+v", err, shallowRes)
	}

	looseEightTests := `import os, sys, unittest
sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', '..'))
import win
class T(unittest.TestCase):
    def test_1(self): self.assertTrue(win.in_window(5, 1, 10))
    def test_2(self): self.assertFalse(win.in_window(0, 1, 10))
    def test_3(self): self.assertFalse(win.in_window(12, 1, 10))
    def test_4(self): self.assertTrue(win.in_window(6, 1, 10))
    def test_5(self): self.assertTrue(win.classify(20))
    def test_6(self): self.assertFalse(win.classify(5))
    def test_7(self): self.assertTrue(win.classify(30))
    def test_8(self): self.assertFalse(win.classify(1))
`
	mustWriteVerifyFile(t, testPath, looseEightTests)
	mutRes, mErr := (&CoverageGate{}).Evaluate(ctx)
	if mErr != nil || mutRes.Passed || !strings.Contains(mutRes.Message, "boundary-mutant spot-check failed") {
		t.Fatalf("expected boundary-mutant survival failure on untested <=/>=, got err=%v res=%+v", mErr, mutRes)
	}

	strictEightTests := `import os, sys, unittest
sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', '..'))
import win
class T(unittest.TestCase):
    def test_1(self): self.assertTrue(win.in_window(5, 1, 10))
    def test_2(self): self.assertTrue(win.in_window(1, 1, 10))
    def test_3(self): self.assertTrue(win.in_window(10, 1, 10))
    def test_4(self): self.assertFalse(win.in_window(11, 1, 10))
    def test_5(self): self.assertFalse(win.in_window(0, 1, 10))
    def test_6(self): self.assertTrue(win.classify(10))
    def test_7(self): self.assertFalse(win.classify(9))
    def test_8(self): self.assertTrue(win.classify(25))
`
	mustWriteVerifyFile(t, testPath, strictEightTests)
	passRes, pErr := (&CoverageGate{}).Evaluate(ctx)
	if pErr != nil || !passRes.Passed {
		t.Fatalf("expected thorough boundary test suite to pass CoverageGate, got err=%v res=%+v", pErr, passRes)
	}
	afterDisk, _ := os.ReadFile(filepath.Join(dir, "win.py"))
	if string(afterDisk) != prodPy {
		t.Fatalf("expected original win.py on disk to remain unmodified after boundary mutant probes")
	}
}
