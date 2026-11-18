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
func TestR4_BrownfieldModuleCapImmunityAndPolyglotSlopRatchet(t *testing.T) {
	dir := initTestGitRepo(t)
	basePy := `def h1(x):
    return x + 1

def h2(x):
    return x * 2

def entry(a, b):
    part = h1(a) + h1(b)
    return part + h2(a) + h2(b)
`
	mustWriteVerifyFile(t, filepath.Join(dir, "legacy.py"), basePy)
	commitAllTestGit(t, dir, "feat: baseline legacy module")

	editedPy := basePy + `
def h3(x):
    return x - 1

def entry_more(a, b):
    extra = h3(a) + h3(b)
    return entry(a, b) + extra
`
	mustWriteVerifyFile(t, filepath.Join(dir, "legacy.py"), editedPy)
	ctx := &vscope.VerifyContext{RepoRoot: dir, WorktreePath: dir}

	cleanHits := evaluateNetNewBrownfieldClonesAndSlop(
		ctx,
		dir,
		map[string][]byte{"legacy.py": []byte(editedPy)},
		map[string][]byte{"legacy.py": []byte(basePy)},
		astdebt.DefaultThresholds(),
	)
	if len(cleanHits) != 0 {
		t.Fatalf("expected legacy brownfield module-cap growth to produce 0 violations, got %v", cleanHits)
	}

	newTS := `export function m1(x: number): number { return x + 1; }
export function m2(x: number): number { return x + 2; }
export function m3(x: number): number { return x + 3; }
export function m4(x: number): number { return x + 4; }
export function run(a: number, b: number): number {
  const first = m1(a) + m1(b) + m2(a) + m2(b);
  return first + m3(a) + m3(b) + m4(a) + m4(b);
}
`
	newHits := evaluateNetNewBrownfieldClonesAndSlop(
		ctx,
		dir,
		map[string][]byte{"new_mod.ts": []byte(newTS)},
		map[string][]byte{"legacy.py": []byte(basePy)},
		astdebt.DefaultThresholds(),
	)
	if len(newHits) == 0 || !strings.Contains(strings.Join(newHits, "\n"), "multi-caller one-line return micro-helpers") {
		t.Fatalf("expected brand-new TS file with 4 multi-caller 1-liners to fail ratchet, got %v", newHits)
	}
}

func buildChainedBoundaryProdPy() string {
	var b strings.Builder
	b.WriteString("def in_span(ts, start, end):\n")
	b.WriteString("    return start <= ts <= end\n\n")
	b.WriteString("def filter_span(items, start, end):\n")
	b.WriteString("    return [x for x in items if start <= x <= end]\n\n")
	b.WriteString("def pad(v):\n")
	b.WriteString("    acc = v\n")
	for i := 0; i < 82; i++ {
		b.WriteString("    acc += 0\n")
	}
	b.WriteString("    return acc\n")
	return b.String()
}

// @verifies TC-COV-006
func TestR4_CompoundChainedBoundaryMutants(t *testing.T) {
	t.Setenv(polyrun.EnvInToolchainGate, "")
	t.Setenv(polyrun.EnvSkipToolchainGate, "")
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	dir := initTestGitRepo(t)
	mustWriteVerifyFile(t, filepath.Join(dir, "nomos.yaml"), "project: r4-chained\ndefault_branch: main\ngovernance:\n  quality_gates:\n    strict_mode: true\n")
	prodPy := buildChainedBoundaryProdPy()
	mustWriteVerifyFile(t, filepath.Join(dir, "span.py"), prodPy)
	commitAllTestGit(t, dir, "feat: add chained window")

	missingUpperTests := `import os, sys, unittest
sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', '..'))
import span
class T(unittest.TestCase):
    def test_1(self): self.assertTrue(span.in_span(1, 1, 10))
    def test_2(self): self.assertTrue(span.in_span(5, 1, 10))
    def test_3(self): self.assertFalse(span.in_span(0, 1, 10))
    def test_4(self): self.assertFalse(span.in_span(11, 1, 10))
    def test_5(self): self.assertEqual(span.filter_span([0, 1, 5, 11], 1, 10), [1, 5])
    def test_6(self): self.assertEqual(span.filter_span([2], 1, 10), [2])
    def test_7(self): self.assertEqual(span.filter_span([-1, 15], 1, 10), [])
    def test_8(self): self.assertEqual(span.pad(3), 3)
`
	testPath := filepath.Join(dir, ".tmp", "tests", "test_span.py")
	mustWriteVerifyFile(t, testPath, missingUpperTests)
	ctx := &vscope.VerifyContext{RepoRoot: dir, WorktreePath: dir, Strict: true}

	mutRes, mErr := (&CoverageGate{}).Evaluate(ctx)
	if mErr != nil || mutRes.Passed || !strings.Contains(mutRes.Message, "boundary-mutant spot-check failed") || !strings.Contains(mutRes.Message, "(<= -> <)") {
		t.Fatalf("expected chained upper-bound flip survival failure, got err=%v res=%+v", mErr, mutRes)
	}

	completeUpperTests := `import os, sys, unittest
sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', '..'))
import span
class T(unittest.TestCase):
    def test_1(self): self.assertTrue(span.in_span(1, 1, 10))
    def test_2(self): self.assertTrue(span.in_span(10, 1, 10))
    def test_3(self): self.assertFalse(span.in_span(0, 1, 10))
    def test_4(self): self.assertFalse(span.in_span(11, 1, 10))
    def test_5(self): self.assertEqual(span.filter_span([0, 1, 10, 11], 1, 10), [1, 10])
    def test_6(self): self.assertEqual(span.filter_span([2], 1, 10), [2])
    def test_7(self): self.assertEqual(span.filter_span([-1, 15], 1, 10), [])
    def test_8(self): self.assertEqual(span.pad(3), 3)
`
	mustWriteVerifyFile(t, testPath, completeUpperTests)
	passRes, pErr := (&CoverageGate{}).Evaluate(ctx)
	if pErr != nil || !passRes.Passed {
		t.Fatalf("expected explicit upper-bound coverage to pass CoverageGate, got err=%v res=%+v", pErr, passRes)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "span.py")); statErr != nil {
		t.Fatalf("expected span.py on disk after probe: %v", statErr)
	}
}
