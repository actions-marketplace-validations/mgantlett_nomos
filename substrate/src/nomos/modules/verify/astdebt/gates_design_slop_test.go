package astdebt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckPolyglotUncheckedErrors_SecondGenGaming(t *testing.T) {
	cases := []struct {
		name      string
		ext       string
		rel       string
		src       string
		wantCount int
		wantSub   string
	}{
		{"BugMaskingTuple", ExtPy, "db.py", "def q():\n    try:\n        return run()\n    except (OSError, KeyError, TypeError):\n        log_err()\n", 1, "catch-all exception tuple"},
		{"BroadExceptionReturnDict", ExtPy, "cfg.py", "def load():\n    try:\n        return read_cfg()\n    except Exception:\n        return {}\n", 1, "silent fallback return"},
		{"MultiBoundaryReturnNone", ExtPy, "cfg.py", "def load():\n    try:\n        return read_cfg()\n    except (OSError, yaml.YAMLError):\n        return None\n", 1, "silent fallback return"},
		{"CommentedBoundaryReturnAllowed", ExtPy, "cfg.py", "def load():\n    try:\n        return read_cfg()\n    except Exception:\n        # optional overlay absent\n        return {}\n", 0, ""},
		{"NarrowValueErrorReturnNoneAllowed", ExtPy, "parse.py", "def to_int(s):\n    try:\n        return int(s)\n    except ValueError:\n        return None\n", 0, ""},
		{"TSCatchReturnNull", ExtTS, "api.ts", "export function load() { try { return read(); } catch (e) { return null; } }\n", 1, "empty/null fallback"},
		{"TSCatchReturnHandledAllowed", ExtTS, "api.ts", "export function load() { try { return read(); } catch (e) { log(e); return null; } }\n", 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CheckPolyglotUncheckedErrors(tc.ext, tc.rel, []byte(tc.src))
			if len(got) != tc.wantCount {
				t.Fatalf("got %d violations %v, want %d", len(got), got, tc.wantCount)
			}
			if tc.wantSub != "" && !strings.Contains(got[0], tc.wantSub) {
				t.Fatalf("expected %q in %v", tc.wantSub, got)
			}
		})
	}
}

func TestCheckCoverageExclusionDirectives_CommentsVsStrings(t *testing.T) {
	cases := []struct {
		name      string
		rel       string
		src       string
		wantCount int
	}{
		{"PythonPragmaComment", "mod.py", "def f(x):\n    if x:\n        return 1  # pragma: no cover\n    return 0\n", 1},
		{"PythonStringLiteralIgnored", "mod.py", "def f():\n    return \"# pragma: no cover\"\n", 0},
		{"PyDocstringIgnored", "mod.py", "def f():\n    \"\"\"\n    # pragma: no cover\n    \"\"\"\n    return 1\n", 0},
		{"TSIstanbulIgnore", "mod.ts", "export function f() {\n  /* istanbul ignore next */\n  return 1;\n}\n", 1},
		{"TSV8StringIgnored", "mod.ts", "export const s = \"/* v8 ignore next */\";\n", 0},
		{"GoPragmaComment", "mod.go", "package p\nfunc f() int {\n\t// coverage: ignore\n\treturn 1\n}\n", 1},
		{"RustCovOff", "lib.rs", "#[coverage(off)]\npub fn f() -> i32 { 1 }\n", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CheckCoverageExclusionDirectives(tc.rel, []byte(tc.src))
			if len(got) != tc.wantCount {
				t.Fatalf("got %d hits %v, want %d", len(got), got, tc.wantCount)
			}
		})
	}
}

func TestCheckManifestImportFallbacks_ManifestAndStdlib(t *testing.T) {
	dir := t.TempDir()
	pyproject := "[project]\nname = \"demo\"\ndependencies = [\"pyyaml>=6.0\", \"click>=8.1\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(pyproject), 0644); err != nil {
		t.Fatal(err)
	}
	badSrc := "try:\n    import yaml\nexcept ImportError:\n    def parse_yaml(s):\n        return {}\n"
	if hits := CheckManifestImportFallbacks(dir, "cfg.py", []byte(badSrc)); len(hits) != 1 {
		t.Fatalf("expected ImportError fallback violation for declared pyyaml, got %v", hits)
	}
	badStdlib := "try:\n    import json\nexcept ModuleNotFoundError:\n    a = 1\n    b = 2\n    def load(s): return {}\n"
	if hits := CheckManifestImportFallbacks(dir, "cfg.py", []byte(badStdlib)); len(hits) != 1 {
		t.Fatalf("expected ImportError fallback violation for stdlib json, got %v", hits)
	}
	aliasOK := "try:\n    import yaml as serializer\nexcept ImportError:\n    serializer = None\n"
	if hits := CheckManifestImportFallbacks(dir, "cfg.py", []byte(aliasOK)); len(hits) != 0 {
		t.Fatalf("expected 1-line alias assignment to pass, got %v", hits)
	}
}

func TestDetectPolyglotDesignSlop_AndNesting(t *testing.T) {
	dir := t.TempDir()
	testFile := "from app import _unused_pass\n"
	if err := os.WriteFile(filepath.Join(dir, "other_note.txt"), []byte(testFile), 0644); err != nil {
		t.Fatal(err)
	}
	src := `def _fossil(x):
    return compute(x)

def _step_a(v): return v + 1
def _step_b(v): return v + 2
def _step_c(v): return v + 3

def validate(a, b, c, d):
    if not isinstance(a, int): raise ValueError("a")
    if not isinstance(b, int): raise ValueError("b")
    if not isinstance(c, int): raise ValueError("c")
    if not isinstance(d, int): raise ValueError("d")
    out = ""
    if op == "add": out = "a"
    if op == "sub": out = "b"
    if op == "mul": out = "c"
    return _step_a(a) + _step_b(b) + _step_c(c)

@property
def prop_alias(self):
    return self._inner()
`
	hits := DetectPolyglotDesignSlop(dir, map[string][]byte{"app.py": []byte(src)})
	joined := strings.Join(hits, "\n")
	for _, want := range []string{
		"dead pass-through wrapper \"_fossil\"",
		"trivial wrapper fragmentation",
		"inline 'if not isinstance(...): raise' guards",
		"consecutive 'if op ==' checks",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in hits:\n%s", want, joined)
		}
	}

	deepPy := "def deep(xs):\n  for x in xs:\n    if x:\n      for y in x:\n        if y:\n          for z in y:\n            if z:\n              return z\n"
	if nHits := EvaluatePolyglotNestingViolations("deep.py", []byte(deepPy), 5); len(nHits) != 1 {
		t.Fatalf("expected 1 nesting depth violation, got %v", nHits)
	}
}
