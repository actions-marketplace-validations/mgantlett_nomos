package ratchet

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/astdebt"
)

func TestRatchetCommonAndSwitchHelpers(t *testing.T) {
	if !IsPolyglotTargetFile("pkg/svc/handler.go") || IsPolyglotTargetFile("pkg/svc/handler_test.go") {
		t.Fatalf("unexpected IsPolyglotTargetFile classification")
	}
	if CountBytesLines([]byte("a\nb")) != 2 || CountBytesLines(nil) != 0 {
		t.Fatalf("unexpected CountBytesLines output")
	}
	if MaxInt(3, 9) != 9 {
		t.Fatalf("expected MaxInt(3,9)=9")
	}
	lines := NonEmptyLines(" alpha \n\n beta\n")
	if len(lines) != 2 || lines[0] != "alpha" || lines[1] != "beta" {
		t.Fatalf("unexpected NonEmptyLines: %v", lines)
	}
	keys := SortedSetKeys(map[string]bool{"b": true, "a": true, "c": false})
	if len(keys) != 2 || keys[0] != "a" || keys[1] != "b" {
		t.Fatalf("unexpected SortedSetKeys: %v", keys)
	}

	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "sample.go", "package p\nfunc f(x int) int {\n\tswitch x {\n\tdefault:\n\t\treturn 0\n\tcase 1:\n\t\treturn 1\n\t}\n}\n", 0)
	if err != nil {
		t.Fatal(err)
	}
	hits := CheckGoSwitchDefaultOrder(node, fset, "sample.go")
	if len(hits) != 1 || !strings.Contains(hits[0], "non-terminal default") {
		t.Fatalf("expected non-terminal default hit, got %v", hits)
	}

	var metrics []astdebt.FunctionMetric
	CollectFunctionMetrics(node, fset, "sample.go", &metrics)
	if len(metrics) != 1 || metrics[0].Name != "f" {
		t.Fatalf("expected CollectFunctionMetrics to capture f, got %+v", metrics)
	}

	if hit := CheckNetNewCopyrightEmail("new.go", []byte("// Copyright <other@ext.dev>\npackage p\n"), "dev@local.org"); !strings.Contains(hit, "other@ext.dev") {
		t.Fatalf("expected copyright email mismatch, got %q", hit)
	}
}
