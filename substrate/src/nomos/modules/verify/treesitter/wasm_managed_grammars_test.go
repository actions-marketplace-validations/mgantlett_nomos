package treesitter_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/treesitter"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

// TestManagedWasmDrivers_ConstantReturnAntiShim verifies TC-POLY-005.
// @verifies TC-POLY-005
func TestManagedWasmDrivers_ConstantReturnAntiShim(t *testing.T) {
	ctx := context.Background()
	stubCases := []struct {
		name string
		lang string
		file string
		src  string
		want string
	}{
		{"java null return", "java", "Billing.java", "public class Billing {\n\tpublic String charge(int cents) {\n\t\treturn null;\n\t}\n}\n", "charge"},
		{"java zero return", "java", "Ledger.java", "public class Ledger {\n\tpublic int fee(int amount) {\n\t\treturn 0;\n\t}\n}\n", "fee"},
		{"java optional empty", "java", "Finder.java", "public class Finder {\n\tpublic Optional<String> lookup(String id) {\n\t\treturn Optional.empty();\n\t}\n}\n", "lookup"},
		{"csharp false return", "csharp", "Auth.cs", "public class Auth {\n\tpublic bool Verify(string token) {\n\t\treturn false;\n\t}\n}\n", "Verify"},
		{"csharp default return", "csharp", "Repo.cs", "public class Repo {\n\tpublic string Find(int id) {\n\t\treturn default;\n\t}\n}\n", "Find"},
		{"csharp empty string", "csharp", "Fmt.cs", "public class Fmt {\n\tpublic string Format(int v) {\n\t\treturn \"\";\n\t}\n}\n", "Format"},
	}

	for _, tc := range stubCases {
		t.Run(tc.name, func(t *testing.T) {
			rep := evalManagedByLang(t, ctx, tc.lang, tc.file, tc.src)
			if rep.Passed || len(rep.EmptyBlocks) != 1 || rep.EmptyBlocks[0] != tc.want {
				t.Fatalf("expected constant-return stub %s to fail closed, got %+v", tc.want, rep)
			}
			assertAntiShimGateRejectsManaged(t, tc.file, tc.src)
		})
	}
}

func evalManagedByLang(t *testing.T, ctx context.Context, lang, file, src string) *treesitter.GateEvaluationReport {
	t.Helper()
	var (
		rep *treesitter.GateEvaluationReport
		err error
	)
	if lang == "java" {
		rep, err = treesitter.EvaluateJavaWasm(ctx, file, []byte(src))
	} else {
		rep, err = treesitter.EvaluateCSharpWasm(ctx, file, []byte(src))
	}
	if err != nil {
		t.Fatalf("eval %s failed: %v", file, err)
	}
	return rep
}

func assertAntiShimGateRejectsManaged(t *testing.T, rel, src string) {
	t.Helper()
	root := t.TempDir()
	full := filepath.Join(root, rel)
	if err := os.WriteFile(full, []byte(src), 0644); err != nil {
		t.Fatalf("write fixture %s: %v", rel, err)
	}
	res, err := (&verify.AntiShimGate{}).Evaluate(&vscope.VerifyContext{RepoRoot: root, WorktreePath: root})
	if err != nil {
		t.Fatalf("AntiShimGate error: %v", err)
	}
	if res.Passed {
		t.Fatalf("expected AntiShimGate to fail on %s, got %+v", rel, res)
	}
}

// TestManagedWasmDrivers_LegitAndSyntaxErrors verifies valid Java/C# logic, grammar IDs, and syntax fail-closed.
// @verifies TC-POLY-005
func TestManagedWasmDrivers_LegitAndSyntaxErrors(t *testing.T) {
	ctx := context.Background()
	eng, err := treesitter.GetDefaultEngine()
	if err != nil {
		t.Fatalf("GetDefaultEngine failed: %v", err)
	}
	assertGrammarID(t, eng, "java", 1)
	assertGrammarID(t, eng, "csharp", 2)

	passCases := []struct {
		lang string
		file string
		src  string
	}{
		{"java", "Config.java", "public class Config {\n\t/** Default port */\n\tpublic int defaultPort() {\n\t\treturn 8080;\n\t}\n\t/** Compute total */\n\t@Override\n\tpublic int compute(int x) {\n\t\treturn x + 1;\n\t}\n}\n"},
		{"csharp", "Pricing.cs", "public class Pricing {\n\t/// Default rate.\n\tpublic int DefaultRate() {\n\t\treturn 5;\n\t}\n\t/// Apply tax.\n\t[HttpGet]\n\tpublic int Apply(int x) {\n\t\treturn x * 2;\n\t}\n}\n"},
	}
	for _, tc := range passCases {
		rep := evalManagedByLang(t, ctx, tc.lang, tc.file, tc.src)
		if !rep.Passed || rep.Documented != 2 || len(rep.EmptyBlocks) != 0 {
			t.Fatalf("expected passing report for %s, got %+v", tc.file, rep)
		}
	}

	assertManagedSyntaxRejected(t, ctx, "java", "Broken.java", "public class Broken { int[] a = new int[3; }")
	assertManagedSyntaxRejected(t, ctx, "csharp", "Broken.cs", "public class Broken { [Fact public void Run() {} }")
	assertHighComplexityRejected(t, ctx)
}

func assertGrammarID(t *testing.T, eng *treesitter.WazeroTreeSitterEngine, lang string, want int) {
	t.Helper()
	got, err := eng.ManagedGrammarID(lang)
	if err != nil || got != want {
		t.Fatalf("grammar_id(%s) = %d (err=%v), want %d", lang, got, err, want)
	}
}

func assertManagedSyntaxRejected(t *testing.T, ctx context.Context, lang, file, src string) {
	t.Helper()
	var err error
	if lang == "java" {
		_, err = treesitter.EvaluateJavaWasm(ctx, file, []byte(src))
	} else {
		_, err = treesitter.EvaluateCSharpWasm(ctx, file, []byte(src))
	}
	if err == nil {
		t.Fatalf("expected %s syntax error in %s to fail closed", lang, file)
	}
}

func assertHighComplexityRejected(t *testing.T, ctx context.Context) {
	t.Helper()
	var cs strings.Builder
	cs.WriteString("public class ComplexSvc {\n\t/// Complex check.\n\tpublic int Route(int x, int[] items) {\n")
	for i := 0; i < 16; i++ {
		cs.WriteString("\t\tif (x > 0) { x++; }\n")
	}
	cs.WriteString("\t\tforeach (var n in items) { x += n; }\n\t\treturn x;\n\t}\n}\n")
	rep, err := treesitter.EvaluateCSharpWasm(ctx, "ComplexSvc.cs", []byte(cs.String()))
	if err != nil || rep.Passed || len(rep.Functions) != 1 || rep.Functions[0].Complexity <= 15 {
		t.Fatalf("expected high complexity C# failure, got err=%v rep=%+v", err, rep)
	}
}
