package mcpcmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCallReadOnlyTool_VerifyImpactIncludesIndirectBoundaries(t *testing.T) {
	repoDir := seedTestMCPRepo(t)
	scriptPath := filepath.Join(repoDir, "deploy.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\necho deploy\n"), 0o755); err != nil {
		t.Fatalf("write deploy.sh: %v", err)
	}

	raw, isErr := CallReadOnlyTool("nomos_verify_impact", map[string]any{"dir": repoDir})
	if isErr {
		t.Fatalf("nomos_verify_impact returned error: %s", raw)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("unmarshal impact JSON: %v", err)
	}
	if payload["indirect_boundaries"] == nil || payload["modified_files"] == nil {
		t.Fatalf("expected modified_files and indirect_boundaries keys in %+v", payload)
	}
}
