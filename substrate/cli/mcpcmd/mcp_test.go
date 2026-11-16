// Package mcpcmd tests the read-only MCP JSON-RPC 2.0 stdio server contract.
// @intent task_lifecycle_cli_spec.md
package mcpcmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/constants"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/mem"
)

func seedTestMCPRepo(t *testing.T) string {
	t.Helper()
	repoDir := t.TempDir()
	src := "package sample\n\n// Add adds two integers.\nfunc Add(a, b int) int { return a + b }\n"
	if err := os.WriteFile(filepath.Join(repoDir, "sample.go"), []byte(src), 0644); err != nil {
		t.Fatalf("write sample.go: %v", err)
	}
	return repoDir
}

func runMCPSession(t *testing.T, frames []string) []MCPResponse {
	t.Helper()
	var inBuf, outBuf bytes.Buffer
	for _, f := range frames {
		inBuf.WriteString(f + "\n")
	}
	if err := ServeStdio(&inBuf, &outBuf, "4.21.1"); err != nil {
		t.Fatalf("ServeStdio failed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(outBuf.String()), "\n")
	resps := make([]MCPResponse, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var r MCPResponse
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("unmarshal response line %q: %v", line, err)
		}
		resps = append(resps, r)
	}
	return resps
}

func decodeToolResult(t *testing.T, resp MCPResponse) MCPToolResult {
	t.Helper()
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var res MCPToolResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("unmarshal MCPToolResult: %v", err)
	}
	return res
}

// @verifies TC-MCP-01
func TestMCPStdioLifecycleAndReadOnlyTools(t *testing.T) {
	t.Setenv(constants.EnvNomosHome, t.TempDir())
	mnemePath := filepath.Join(t.TempDir(), constants.FileMnemeDB)
	t.Setenv("NOMOS_MNEME_DB_PATH", mnemePath)
	repoDir := seedTestMCPRepo(t)

	// Seed one Mneme entry so nomos_mem_recall returns structured records.
	store, err := mem.NewStore(mnemePath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.Save(&mem.Memory{RepoID: "sample", Kind: mem.KindPattern, Content: "Always keep MCP tools strictly read-only"}); err != nil {
		t.Fatalf("Save memory: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close store: %v", err)
	}

	frames := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"nomos_xray","arguments":{"dir":` + marshalJSONStr(t, repoDir) + `,"top":3}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"nomos_handshake","arguments":{"dir":` + marshalJSONStr(t, repoDir) + `}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"nomos_status","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"nomos_task_dag","arguments":{"dir":` + marshalJSONStr(t, repoDir) + `,"limit":2}}}`,
		`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"nomos_mem_recall","arguments":{"query":"read-only","limit":3}}}`,
		`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"nomos_verify_impact","arguments":{"dir":` + marshalJSONStr(t, repoDir) + `}}}`,
	}

	// Notification produces zero frames, so 10 input lines yield 9 responses.
	resps := runMCPSession(t, frames)
	if len(resps) != 9 {
		t.Fatalf("expected 9 JSON-RPC responses (notification omitted), got %d", len(resps))
	}
	assertInitializeAndToolsList(t, resps[0], resps[2])
	assertToolCallOutputs(t, resps[3:], repoDir)
}

func assertInitializeAndToolsList(t *testing.T, initResp, listResp MCPResponse) {
	t.Helper()
	initRaw, _ := json.Marshal(initResp.Result)
	if !strings.Contains(string(initRaw), mcpProtocolVersion) {
		t.Fatalf("expected initialize protocolVersion %s, got %s", mcpProtocolVersion, string(initRaw))
	}
	listRaw, _ := json.Marshal(listResp.Result)
	for _, toolName := range []string{"nomos_handshake", "nomos_status", "nomos_task_dag", "nomos_mem_recall", "nomos_verify_impact", "nomos_xray"} {
		if !strings.Contains(string(listRaw), toolName) {
			t.Fatalf("tools/list missing expected read-only tool %q", toolName)
		}
	}
}

func assertToolCallOutputs(t *testing.T, toolResps []MCPResponse, repoDir string) {
	t.Helper()
	for i, resp := range toolResps {
		if resp.Error != nil {
			t.Fatalf("tool call #%d returned RPC error: %+v", i, resp.Error)
		}
		res := decodeToolResult(t, resp)
		if res.IsError || len(res.Content) == 0 || strings.TrimSpace(res.Content[0].Text) == "" {
			t.Fatalf("tool call #%d failed unexpectedly: %+v", i, res)
		}
	}
	entries, err := os.ReadDir(repoDir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "sample.go" {
		t.Fatalf("TC-MCP-01 zero-write violation: unexpected repo entries after MCP calls: %v (err=%v)", len(entries), err)
	}
}

// @verifies TC-MCP-01
func TestMCPRejectsMutatingToolsUnknownMethodsAndMalformedJSON(t *testing.T) {
	t.Setenv(constants.EnvNomosHome, t.TempDir())
	t.Setenv("NOMOS_MNEME_DB_PATH", filepath.Join(t.TempDir(), "nonexistent.db"))

	frames := []string{
		`{not-valid-json`,
		`{"jsonrpc":"2.0","id":10,"method":"resources/write"}`,
		`{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"nomos_task_sync","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"nomos_mem_recall","arguments":{"query":"missing"}}}`,
		`{"jsonrpc":"2.0","id":13,"method":"tools/call","params":{"name":"nomos_mem_recall","arguments":{"query":""}}}`,
	}
	resps := runMCPSession(t, frames)
	if len(resps) != 5 {
		t.Fatalf("expected 5 responses, got %d", len(resps))
	}
	if resps[0].Error == nil || resps[0].Error.Code != rpcErrParse {
		t.Fatalf("expected parse error %d, got %+v", rpcErrParse, resps[0].Error)
	}
	if resps[1].Error == nil || resps[1].Error.Code != rpcErrMethodFound {
		t.Fatalf("expected method not found %d, got %+v", rpcErrMethodFound, resps[1].Error)
	}
	mutRes := decodeToolResult(t, resps[2])
	if !mutRes.IsError || !strings.Contains(mutRes.Content[0].Text, "ERR_MCP_READ_ONLY") {
		t.Fatalf("expected mutating tool rejection, got %+v", mutRes)
	}
	missingDBRes := decodeToolResult(t, resps[3])
	if missingDBRes.IsError || strings.TrimSpace(missingDBRes.Content[0].Text) != "[]" {
		t.Fatalf("expected missing Mneme DB to return [], got %+v", missingDBRes)
	}
	emptyQueryRes := decodeToolResult(t, resps[4])
	if !emptyQueryRes.IsError {
		t.Fatalf("expected empty query error, got %+v", emptyQueryRes)
	}

	cmd := NewMCPCmd("4.21.1")
	var out bytes.Buffer
	cmd.SetIn(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n"))
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil || !strings.Contains(out.String(), `"id":1`) {
		t.Fatalf("NewMCPCmd Execute failed: err=%v out=%s", err, out.String())
	}
}

func marshalJSONStr(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("json.Marshal(%q): %v", s, err)
	}
	return string(b)
}
