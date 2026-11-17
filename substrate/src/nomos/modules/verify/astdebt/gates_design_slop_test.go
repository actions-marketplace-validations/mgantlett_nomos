package astdebt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// @verifies TC-TS-19
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
		{"MultiLineBlackFormattedTuple", ExtPy, "db.py", "def q():\n    try:\n        return run()\n    except (\n        sqlite3.Error,\n        KeyError,\n        TypeError,\n    ) as exc:\n        log_err(exc)\n", 1, "catch-all exception tuple"},
		{"MultiLineTupleInlineCommentMask", ExtPy, "db.py", "def q():\n    try:\n        return run()\n    except (\n        OSError,\n        KeyError,  # note: bug mask\n        TypeError,\n    ):\n        log_err()\n", 1, "catch-all exception tuple"},
		{"MultiLineBoundarySilentFallback", ExtPy, "cfg.py", "def load():\n    try:\n        return read_cfg()\n    except (\n        OSError,\n        yaml.YAMLError,\n    ) as exc:\n        return None\n", 1, "silent fallback return"},
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

// @verifies TC-TS-20
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

// @verifies TC-TS-20
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

// @verifies TC-TS-21
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

	t.Run("PublicCompatibilityFossilVsNormalPublicFunction", func(t *testing.T) {
		pubDir := t.TempDir()
		pubSrc := `def load_config(args):
    """Compatibility helper for callers loading a Part 1 config."""
    return parse_config(args)

def run_cli(args):
    """Execute primary CLI entrypoint."""
    return execute(args)
`
		pubHits := DetectPolyglotDesignSlop(pubDir, map[string][]byte{"rejector.py": []byte(pubSrc)})
		if len(pubHits) != 1 || !strings.Contains(pubHits[0], `dead pass-through wrapper "load_config"`) {
			t.Fatalf("expected only compatibility fossil load_config flagged, got %v", pubHits)
		}
	})

	t.Run("CorpusFileCapSkipsCrossFileWrappersWhenExceeded", func(t *testing.T) {
		capDir := t.TempDir()
		m1Src := "def _shared_helper(x):\n    return compute(x)\n"
		for name, body := range map[string]string{
			"m1.py": m1Src,
			"m2.py": "VAL_A = 1\n",
			"m3.py": "VAL_B = 2\n",
			"m4.py": "def run():\n    return _shared_helper(10)\n",
		} {
			if err := os.WriteFile(filepath.Join(capDir, name), []byte(body), 0644); err != nil {
				t.Fatal(err)
			}
		}
		if truncatedHits := detectPolyglotDesignSlopWithLimit(capDir, map[string][]byte{"m1.py": []byte(m1Src)}, 3); len(truncatedHits) != 0 {
			t.Fatalf("expected >cap repo to skip cross-file wrapper checks, got %v", truncatedHits)
		}
		if fullHits := DetectPolyglotDesignSlop(capDir, map[string][]byte{"m1.py": []byte(m1Src)}); len(fullHits) != 0 {
			t.Fatalf("expected 512-file limit to count m4.py caller and produce 0 hits, got %v", fullHits)
		}
	})

	t.Run("Returning3BranchChainAndDeadClassMethodAndComprehensionWrapper", func(t *testing.T) {
		slopPy := `class Parser:
    def ident(self, msg):
        return msg

    def alias(self):
        return self.ident("Expected alias")

def _comp_a(xs): return [x for x in xs if x]
def _comp_b(xs): return "a" if xs else "b"
def _comp_c(xs): return [y + 1 for y in xs]

def dispatch(kind, v):
    if kind == "a": return 1
    if kind == "b": return 2
    if kind == "c": return 3
    if isinstance(v, bool):
        return ("b", v)
    if isinstance(v, int):
        return ("i", v)
    if isinstance(v, str):
        return ("s", v)
    return _comp_a([1]) + _comp_b(1) + _comp_c([1])
`
		hits := DetectPolyglotDesignSlop(t.TempDir(), map[string][]byte{"parser.py": []byte(slopPy)})
		joined := strings.Join(hits, "\n")
		for _, want := range []string{
			`dead pass-through method "alias"`,
			"excessive trivial wrapper fragmentation",
			"consecutive 'if kind ==' checks",
			"consecutive 'if isinstance(v, ...)' checks",
		} {
			if !strings.Contains(joined, want) {
				t.Fatalf("expected %q in hits:\n%s", want, joined)
			}
		}
	})
}

// @verifies TC-TS-22
func TestUniversalCognitiveComplexityAndPythonIdioms(t *testing.T) {
	t.Run("CognitiveComplexity_NestedExceeds15_WhileFlatElifPasses", func(t *testing.T) {
		nestedPy := `def process_records(rows, cfg):
    out = []
    for row in rows:
        if row.get("active"):
            for item in row.get("items", []):
                if item.get("valid") and not item.get("skip"):
                    try:
                        if cfg.get("strict") or item.get("force"):
                            out.append(item["id"])
                    except ValueError:
                        out.append(None)
    return out

def dispatch_flat(op):
    if op == "a": return 1
    elif op == "b": return 2
    elif op == "c": return 3
    elif op == "d": return 4
    elif op == "e": return 5
    elif op == "f": return 6
    elif op == "g": return 7
    elif op == "h": return 8
    return 0
`
		hits := EvaluatePolyglotCognitiveViolations("engine.py", []byte(nestedPy), MaxFunctionCognitiveComplexity)
		if len(hits) != 1 || !strings.Contains(hits[0], "process_records cognitive complexity") {
			t.Fatalf("expected only process_records to exceed cognitive limit 15, got %v", hits)
		}
	})

	t.Run("GoStyleErrTuple_EmptyExceptionSprawl_SIM103_AndMultiCallerAllowed", func(t *testing.T) {
		py := `class ErrA(Exception): pass
class ErrB(Exception): pass
class ErrC(Exception): pass

def peek(buf):
    return buf[0]

def parse_cmd(line):
    if not line:
        return None, "empty"
    if peek(line) == "!":
        return None, "bang"
    if peek(line) == "#":
        return (line, None)
    return line.strip(), None

def is_ready(flag, count):
    if flag and count > 0:
        return True
    return False
`
		dir := t.TempDir()
		hits := DetectPolyglotDesignSlop(dir, map[string][]byte{"cmd.py": []byte(py)})
		joined := strings.Join(hits, "\n")
		for _, want := range []string{
			"Go-style (value, err) tuple returns",
			"declares 3 empty Exception subclasses",
			"redundant 'if ...: return True' before opposite boolean return",
		} {
			if !strings.Contains(joined, want) {
				t.Fatalf("expected %q in hits:\n%s", want, joined)
			}
		}
		if strings.Contains(joined, "peek") || strings.Contains(joined, "wrapper") {
			t.Fatalf("expected multi-caller 1-line helper peek() to remain allowed, got:\n%s", joined)
		}
	})

	t.Run("TwoExceptionsAndSingleNoneLookupPassCleanly", func(t *testing.T) {
		cleanPy := `class AppError(Exception):
    pass

class ValidationError(AppError):
    pass

def split_pair(val):
    if ":" not in val:
        return val, None
    return val.split(":", 1)[0], None
`
		hits := DetectPolyglotDesignSlop(t.TempDir(), map[string][]byte{"clean.py": []byte(cleanPy)})
		if len(hits) != 0 {
			t.Fatalf("expected 0 hits on 2 empty exceptions and single-side lookup tuple, got %v", hits)
		}
	})
}
