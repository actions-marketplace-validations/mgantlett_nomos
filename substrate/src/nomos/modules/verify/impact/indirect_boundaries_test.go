package impact

import (
	"os"
	"path/filepath"
	"testing"
)

func seedIndirectBoundaryFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	apiDir := filepath.Join(root, "pkg", "api")
	scriptDir := filepath.Join(root, "scripts")
	if err := os.MkdirAll(apiDir, 0o755); err != nil {
		t.Fatalf("mkdir api: %v", err)
	}
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	goSrc := `package api

import "net/http"

type OrderPayload struct {
	ID string ` + "`json:\"id\"`" + `
}

func Register(mux *http.ServeMux, cmd interface{ Flags() interface{ BoolVar(*bool, string, bool, string) } }) {
	var strict bool
	cmd.Flags().BoolVar(&strict, "strict-check", false, "enable strict check")
	mux.HandleFunc("/api/v1/orders", nil)
	_ = "CREATE TABLE IF NOT EXISTS orders (id TEXT PRIMARY KEY)"
}
`
	if err := os.WriteFile(filepath.Join(apiDir, "handler.go"), []byte(goSrc), 0o644); err != nil {
		t.Fatalf("write handler.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scriptDir, "migrate.sql"), []byte("ALTER TABLE invoices ADD COLUMN total INT;\n"), 0o644); err != nil {
		t.Fatalf("write migrate.sql: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scriptDir, "deploy.sh"), []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
		t.Fatalf("write deploy.sh: %v", err)
	}
	return root
}

// @verifies TC-PHY-07
func TestComputeWorkspaceImpactReport_IndirectBoundaries(t *testing.T) {
	root := seedIndirectBoundaryFixture(t)
	modified := []string{
		"pkg/api/handler.go",
		"scripts/migrate.sql",
		"scripts/deploy.sh",
		"intent/notes.md",
	}
	rep := ComputeWorkspaceImpactReport(root, modified)
	if rep == nil || len(rep.IndirectBoundaries) != 6 {
		t.Fatalf("expected 6 indirect boundary signals across Go, SQL, and shell files, got %+v", rep)
	}

	seenCat := make(map[string]bool)
	for _, b := range rep.IndirectBoundaries {
		seenCat[b.Category] = true
	}
	for _, want := range []string{
		boundaryHTTPRoute,
		boundarySQLSchema,
		boundaryWireContract,
		boundaryCLIFlag,
		boundaryNonASTArtifact,
	} {
		if !seenCat[want] {
			t.Fatalf("missing indirect boundary category %q in %+v", want, rep.IndirectBoundaries)
		}
	}
}
