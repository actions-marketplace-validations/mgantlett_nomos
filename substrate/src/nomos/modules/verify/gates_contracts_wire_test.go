package verify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/config"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

// TestContractParityGate_RejectsUntypedBoundaryAndPhantomWireKey verifies TC-XRAY-03 in Law 4.
// @verifies TC-XRAY-03
func TestContractParityGate_RejectsUntypedBoundaryAndPhantomWireKey(t *testing.T) {
	tempDir := t.TempDir()
	cfgContent := `
governance:
  quality_gates:
    contracts:
      contracts:
        enabled: true
        schema_files: ["nomos.schema.json"]
        backend_roots: ["substrate/src/nomos"]
`
	if err := os.WriteFile(filepath.Join(tempDir, "nomos.yaml"), []byte(cfgContent), 0644); err != nil {
		t.Fatalf("write nomos.yaml: %v", err)
	}
	schemaBytes, sErr := config.GenerateConfigJSONSchema()
	if sErr != nil {
		t.Fatalf("generate schema: %v", sErr)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "nomos.schema.json"), schemaBytes, 0644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	goDir := filepath.Join(tempDir, "substrate", "src", "nomos")
	if err := os.MkdirAll(goDir, 0755); err != nil {
		t.Fatalf("mkdir goDir: %v", err)
	}
	goBody := "package nomos\ntype ScanReport struct {\n\tHotspots []string `json:\"hotspots\"`\n}\n"
	if err := os.WriteFile(filepath.Join(goDir, "report.go"), []byte(goBody), 0644); err != nil {
		t.Fatalf("write report.go: %v", err)
	}
	webDir := filepath.Join(tempDir, "web")
	if err := os.MkdirAll(webDir, 0755); err != nil {
		t.Fatalf("mkdir webDir: %v", err)
	}
	tsBody := "export interface PublicScanResult {\n\treport: Record<string, unknown>;\n}\nexport function render(res: PublicScanResult): unknown {\n\tconst r = res.report;\n\treturn r.top_hotspots;\n}\n"
	if err := os.WriteFile(filepath.Join(webDir, "view.ts"), []byte(tsBody), 0644); err != nil {
		t.Fatalf("write view.ts: %v", err)
	}

	gate := &ContractParityGate{}
	ctx := &vscope.VerifyContext{WorktreePath: tempDir, RepoRoot: tempDir}
	res, err := gate.Evaluate(ctx)
	if err != nil {
		t.Fatalf("unexpected gate error: %v", err)
	}
	if res.Passed {
		t.Fatalf("expected ContractParityGate failure on untyped DTO and phantom wire key, got pass: %+v", res)
	}
	if !strings.Contains(res.Message, "untyped_boundary_read") || !strings.Contains(res.Message, "closest wire tag: hotspots") {
		t.Fatalf("expected untyped_boundary_read and closest wire tag hotspots in message, got: %s", res.Message)
	}
}
