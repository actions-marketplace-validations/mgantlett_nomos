package vscope

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGitSubprocessHygieneGate_Name(t *testing.T) {
	gate := &GitSubprocessHygieneGate{}
	if gate.Name() != "Git Subprocess Hygiene Invariant (Law 1/3)" {
		t.Errorf("unexpected gate name: %s", gate.Name())
	}
}

func TestGitSubprocessHygieneGate_EvaluateClean(t *testing.T) {
	gate := &GitSubprocessHygieneGate{}
	tempDir := t.TempDir()

	cleanCode := `package main
import "fmt"
func main() { fmt.Println("ok") }
`
	if err := os.WriteFile(filepath.Join(tempDir, "main.go"), []byte(cleanCode), 0644); err != nil {
		t.Fatalf("failed writing test file: %v", err)
	}

	ctx := &VerifyContext{RepoRoot: tempDir, WorktreePath: tempDir}
	res, err := gate.Evaluate(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil || !res.Passed {
		t.Errorf("expected clean code to pass, got: %+v", res)
	}
}

func TestGitSubprocessHygieneGate_EvaluateViolation(t *testing.T) {
	gate := &GitSubprocessHygieneGate{}
	tempDir := t.TempDir()

	violatingCode := `package main
import "os/exec"
func run() {
	_ = exec.Command("git", "status")
}
`
	if err := os.WriteFile(filepath.Join(tempDir, "bad.go"), []byte(violatingCode), 0644); err != nil {
		t.Fatalf("failed writing test file: %v", err)
	}

	ctx := &VerifyContext{RepoRoot: tempDir, WorktreePath: tempDir}
	res, err := gate.Evaluate(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil || res.Passed {
		t.Errorf("expected violation to fail, got: %+v", res)
	}
}

func TestGitSubprocessHygieneGate_ExternalGoModuleExempt(t *testing.T) {
	gate := &GitSubprocessHygieneGate{}
	extDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(extDir, "go.mod"), []byte("module example.com/externalrepo\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	rawCall := `package main
import "os/exec"
func run() {
	_ = exec.Command("git", "rev-parse", "HEAD")
}
`
	if err := os.WriteFile(filepath.Join(extDir, "gitwrap.go"), []byte(rawCall), 0644); err != nil {
		t.Fatalf("write gitwrap.go: %v", err)
	}

	resExt, err := gate.Evaluate(&VerifyContext{RepoRoot: extDir, WorktreePath: extDir})
	if err != nil || resExt == nil || !resExt.Passed {
		t.Fatalf("expected external Go module without nomos substrate dep to pass, got res=%+v err=%v", resExt, err)
	}

	intDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(intDir, "go.mod"), []byte("module github.com/mgantlett/nomos/substrate\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatalf("write internal go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(intDir, "gitwrap.go"), []byte(rawCall), 0644); err != nil {
		t.Fatalf("write internal gitwrap.go: %v", err)
	}

	resInt, err := gate.Evaluate(&VerifyContext{RepoRoot: intDir, WorktreePath: intDir})
	if err != nil || resInt == nil || resInt.Passed {
		t.Fatalf("expected internal nomos/substrate module to enforce SafeGitCommand fail-closed, got res=%+v err=%v", resInt, err)
	}
}
