package verify

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

func cloneTestBlockSource(pkgName, fnName, modeTag string) string {
	return fmt.Sprintf(`package %s

// %s validates tokenized shell words cleanly.
func %s(line string) []string {
	var out []string
	var cur []rune
	inSingle := false
	inDouble := false
	escaped := false
	for _, r := range line {
		if escaped {
			cur = append(cur, r)
			escaped = false
			continue
		}
		switch {
		case r == '\\' && !inSingle:
			escaped = true
		case r == '\'' && !inDouble:
			inSingle = !inSingle
		case r == '"' && !inSingle:
			inDouble = !inDouble
		case (r == ' ' || r == '\t') && !inSingle && !inDouble:
			if len(cur) > 0 {
				out = append(out, string(cur))
				cur = cur[:0]
			}
		default:
			cur = append(cur, r)
		}
	}
	if len(cur) > 0 {
		out = append(out, string(cur)+%q)
	}
	return out
}
`, pkgName, fnName, fnName, modeTag)
}

func writeTestRepoFile(t *testing.T, root, rel, content string) string {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	return abs
}

func setupStrictDiffRepoWithWorktree(t *testing.T, branch, wtRel string, initialFiles map[string]string) (string, string) {
	t.Helper()
	repoDir := t.TempDir()
	runTestGit(t, repoDir, "init", "-b", "main")
	cfgYAML := "project: demo\ndefault_branch: main\ngovernance:\n  boundary:\n    enforce: true\n  quality_gates:\n    strict_mode: true\n    verify_scope: diff\n    thresholds:\n      max_package_files: 40\n"
	writeTestRepoFile(t, repoDir, "nomos.yaml", cfgYAML)
	for rel, src := range initialFiles {
		writeTestRepoFile(t, repoDir, rel, src)
	}
	runTestGit(t, repoDir, "add", ".")
	runTestGit(t, repoDir, "commit", "-m", "baseline")

	wtDir := filepath.Join(repoDir, filepath.FromSlash(wtRel))
	if err := os.MkdirAll(filepath.Dir(wtDir), 0755); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repoDir, "worktree", "add", "-b", branch, wtDir)
	return repoDir, wtDir
}

func TestCognitiveDebtGate_BlocksCrossPackageCloneInDiffScope(t *testing.T) {
	initial := map[string]string{
		"pkg/a/lexer.go": cloneTestBlockSource("a", "ScanQuoted", "_a"),
	}
	repoDir, wtDir := setupStrictDiffRepoWithWorktree(t, "feature/SUB-99", "worktrees/demo-SUB-99", initial)
	writeTestRepoFile(t, wtDir, "pkg/b/hook.go", cloneTestBlockSource("b", "ParseHook", "_b"))

	ctx := &vscope.VerifyContext{RepoRoot: repoDir, WorktreePath: wtDir, TaskID: "SUB-99", Phase: "EDIT"}
	res, err := (&CognitiveDebtGate{}).Evaluate(ctx)
	if err != nil {
		t.Fatalf("CognitiveDebtGate error: %v", err)
	}
	if res.Passed || !strings.Contains(res.Message, "structural AST clone") ||
		!strings.Contains(res.Message, "pkg/a/lexer.go") || !strings.Contains(res.Message, "pkg/b/hook.go") {
		t.Fatalf("expected cross-package clone rejection naming both files, got %+v", res)
	}

	writeTestRepoFile(t, wtDir, "pkg/a/lexer.go", "package a\n\n// Delegate moves scanning out of lexer.\nfunc Delegate() string { return \"ok\" }\n")
	resAfter, errAfter := (&CognitiveDebtGate{}).Evaluate(&vscope.VerifyContext{RepoRoot: repoDir, WorktreePath: wtDir, TaskID: "SUB-99", Phase: "EDIT"})
	if errAfter != nil {
		t.Fatalf("CognitiveDebtGate after move error: %v", errAfter)
	}
	if !resAfter.Passed {
		t.Fatalf("expected intra-worktree move to pass once source clone removed, got %+v", resAfter)
	}
}

func TestCognitiveDebtGate_IgnoresPreExistingBaselineCloneOnUnrelatedEdit(t *testing.T) {
	initial := map[string]string{
		"pkg/a/lexer.go": cloneTestBlockSource("a", "ScanQuoted", "_a"),
		"pkg/b/hook.go":  cloneTestBlockSource("b", "ParseHook", "_b") + "\n// Touch returns marker.\nfunc Touch() string { return \"v1\" }\n",
	}
	repoDir, wtDir := setupStrictDiffRepoWithWorktree(t, "feature/SUB-100", "worktrees/demo-SUB-100", initial)
	writeTestRepoFile(t, wtDir, "pkg/b/hook.go", cloneTestBlockSource("b", "ParseHook", "_b")+"\n// Touch returns updated marker.\nfunc Touch() string {\n\tv := \"v2\"\n\treturn v\n}\n")

	res, err := (&CognitiveDebtGate{}).Evaluate(&vscope.VerifyContext{RepoRoot: repoDir, WorktreePath: wtDir, TaskID: "SUB-100", Phase: "EDIT"})
	if err != nil {
		t.Fatalf("CognitiveDebtGate error: %v", err)
	}
	if !res.Passed {
		t.Fatalf("expected unrelated edit in file with pre-existing clone to pass, got %+v", res)
	}
}

func buildPaddedGoFile(pkgName, fnPrefix string, totalLines int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\n// %s returns true.\nfunc %s() bool {\n\tx := true\n", pkgName, fnPrefix, fnPrefix)
	for i := 0; b.String() != "" && strings.Count(b.String(), "\n") < totalLines-3; i++ {
		fmt.Fprintf(&b, "\tx = x && true // line %d\n", i)
	}
	b.WriteString("\treturn x\n}\n")
	return b.String()
}

func TestMaxFileLOCGate_AndFastRadar_BlockNetNewMicroFileInHighFanoutPackage(t *testing.T) {
	initial := make(map[string]string, 27)
	for i := 1; i <= 26; i++ {
		initial[fmt.Sprintf("pkg/crowded/f%02d.go", i)] = buildPaddedGoFile("crowded", fmt.Sprintf("Fn%02d", i), 120)
	}
	initial["pkg/crowded/f27.go"] = buildPaddedGoFile("crowded", "Fn27", 40)

	repoDir, wtDir := setupStrictDiffRepoWithWorktree(t, "feature/SUB-101", "worktrees/demo-SUB-101", initial)
	baseRes, baseErr := (&MaxFileLOCGate{}).Evaluate(&vscope.VerifyContext{RepoRoot: repoDir, AllFiles: true, IsBaseline: true})
	if baseErr != nil || !baseRes.Passed {
		t.Fatalf("expected baseline AllFiles pass on main, err=%v res=%+v", baseErr, baseRes)
	}

	microSrc := buildPaddedGoFile("crowded", "ExtraHelper", 30)
	extraAbs := writeTestRepoFile(t, wtDir, "pkg/crowded/extra_helper.go", microSrc)

	wtRes, wtErr := (&MaxFileLOCGate{}).Evaluate(&vscope.VerifyContext{RepoRoot: repoDir, WorktreePath: wtDir, TaskID: "SUB-101", Phase: "EDIT"})
	if wtErr != nil {
		t.Fatalf("MaxFileLOCGate error: %v", wtErr)
	}
	if wtRes.Passed || !strings.Contains(wtRes.Message, "micro-file fragment") {
		t.Fatalf("expected MaxFileLOCGate to block net-new micro-file in high-fanout package, got %+v", wtRes)
	}

	radarRes, radarErr := EvaluateFastFileRadar(repoDir, extraAbs)
	if radarErr != nil {
		t.Fatalf("EvaluateFastFileRadar error: %v", radarErr)
	}
	foundRadar := false
	for _, br := range radarRes.Breaches {
		if strings.Contains(br, "micro-file fragment") {
			foundRadar = true
			break
		}
	}
	if !foundRadar {
		t.Fatalf("expected EvaluateFastFileRadar breach for micro-file fragment, got %+v", radarRes.Breaches)
	}

	writeTestRepoFile(t, wtDir, "pkg/crowded/extra_helper.go", "//go:build linux\n\n"+microSrc)
	tagRes, tagErr := (&MaxFileLOCGate{}).Evaluate(&vscope.VerifyContext{RepoRoot: repoDir, WorktreePath: wtDir, TaskID: "SUB-101", Phase: "EDIT"})
	if tagErr != nil {
		t.Fatalf("MaxFileLOCGate build-tag error: %v", tagErr)
	}
	if !tagRes.Passed {
		t.Fatalf("expected build-tagged micro-file to be exempt, got %+v", tagRes)
	}
}
