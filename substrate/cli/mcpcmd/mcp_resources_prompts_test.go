package mcpcmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestExecCapabilitiesTool_ResolvesContractSliceInDir(t *testing.T) {
	repoDir := t.TempDir()
	specDir := filepath.Join(repoDir, "intent", "architecture", "specs")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatalf("mkdir specs: %v", err)
	}
	specBody := "# Sample Spec\n\n## 3. Types\n\n```go\npackage sample\n\n// SampleContract defines a sample (#SampleContract).\ntype SampleContract struct {\n\tName string `json:\"name\"`\n}\n```\n"
	if err := os.WriteFile(filepath.Join(specDir, "sample_spec.md"), []byte(specBody), 0o644); err != nil {
		t.Fatalf("write sample_spec.md: %v", err)
	}

	raw, isErr := CallReadOnlyTool("nomos_capabilities", map[string]any{
		"dir": repoDir,
		"id":  "#SampleContract",
	})
	if isErr {
		t.Fatalf("nomos_capabilities contract lookup failed: %s", raw)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("unmarshal capabilities contract JSON: %v", err)
	}
	if parsed["type"] != "contract" || parsed["contract"] == nil {
		t.Fatalf("expected contract detail payload, got %+v", parsed)
	}
}
