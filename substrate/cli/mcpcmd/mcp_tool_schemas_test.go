// Package mcpcmd verifies complete MCP tool TDQS schemas, titles, disambiguation, and structuredContent.
// @intent task_lifecycle_cli_spec.md
package mcpcmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/constants"
)

// @verifies TC-MCP-01
func TestReadOnlyTools_TDQSCompletenessAndDisambiguation(t *testing.T) {
	tools := ReadOnlyTools()
	if len(tools) != 7 {
		t.Fatalf("expected 7 read-only tools, got %d", len(tools))
	}
	for _, tool := range tools {
		if strings.TrimSpace(tool.Title) == "" {
			t.Fatalf("tool %q missing human-readable Title", tool.Name)
		}
		if !strings.Contains(tool.Description, "Use when") || !strings.Contains(tool.Description, "Do NOT use when") {
			t.Fatalf("tool %q description missing explicit sibling disambiguation: %q", tool.Name, tool.Description)
		}
		if tool.OutputSchema == nil || tool.OutputSchema["type"] != "object" {
			t.Fatalf("tool %q missing object OutputSchema: %+v", tool.Name, tool.OutputSchema)
		}
		if tool.Annotations["idempotentHint"] != true || tool.Annotations["readOnlyHint"] != true {
			t.Fatalf("tool %q missing complete read-only annotations: %+v", tool.Name, tool.Annotations)
		}
	}

	t.Setenv(constants.EnvNomosHome, t.TempDir())
	t.Setenv("NOMOS_MNEME_DB_PATH", filepath.Join(t.TempDir(), constants.FileMnemeDB))
	repoDir := seedTestMCPRepo(t)
	resps := runMCPSession(t, []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"nomos_verify_impact","arguments":{"dir":` + marshalJSONStr(t, repoDir) + `}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nomos_task_dag","arguments":{"dir":` + marshalJSONStr(t, repoDir) + `}}}`,
	})
	objRes := decodeToolResult(t, resps[0])
	if objRes.StructuredContent == nil || objRes.StructuredContent["dir"] == nil {
		t.Fatalf("expected structuredContent object for nomos_verify_impact, got %+v", objRes)
	}
	arrRes := decodeToolResult(t, resps[1])
	if arrRes.StructuredContent == nil || arrRes.StructuredContent["items"] == nil {
		t.Fatalf("expected wrapped structuredContent {items,count} for array tool nomos_task_dag, got %+v", arrRes)
	}
}
