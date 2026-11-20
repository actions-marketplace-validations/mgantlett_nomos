// Package mcpcmd tests the read-only MCP JSON-RPC 2.0 stdio server contract.
// @intent task_lifecycle_cli_spec.md
package mcpcmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
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

type charDeviceFileInfo struct {
	os.FileInfo
}

func (i charDeviceFileInfo) Mode() os.FileMode {
	return i.FileInfo.Mode() | os.ModeCharDevice
}

type charDeviceReader struct {
	io.Reader
	info os.FileInfo
	err  error
}

func (r charDeviceReader) Stat() (os.FileInfo, error) {
	if r.err != nil {
		return nil, r.err
	}
	return charDeviceFileInfo{FileInfo: r.info}, nil
}

// @verifies TC-MCP-01
func TestMCPInteractiveTTYHintWritesOnlyToStderr(t *testing.T) {
	tmpFile, err := os.CreateTemp(t.TempDir(), "mcp-stdin-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	if _, err := tmpFile.WriteString(`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n"); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	if _, err := tmpFile.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	defer func() {
		if closeErr := tmpFile.Close(); closeErr != nil {
			t.Errorf("Close temp file: %v", closeErr)
		}
	}()

	info, err := tmpFile.Stat()
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}

	pingFrame := `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n"

	// 1. Interactive TTY stdin emits 1-line hint on stderr while keeping stdout pure JSON-RPC.
	cmdTTY := NewMCPCmd("4.22.0")
	var outTTY, errTTY bytes.Buffer
	cmdTTY.SetIn(charDeviceReader{Reader: strings.NewReader(pingFrame), info: info})
	cmdTTY.SetOut(&outTTY)
	cmdTTY.SetErr(&errTTY)
	if err := cmdTTY.Execute(); err != nil {
		t.Fatalf("Execute on TTY stdin failed: %v", err)
	}
	if !strings.Contains(errTTY.String(), "waiting for MCP client frames on stdin") {
		t.Fatalf("expected interactive stderr hint, got %q", errTTY.String())
	}
	var resp MCPResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(outTTY.String())), &resp); err != nil {
		t.Fatalf("expected valid JSON-RPC stdout response, got %q (%v)", outTTY.String(), err)
	}
	if string(resp.ID) != "1" {
		t.Fatalf("expected JSON-RPC response id 1, got %s", string(resp.ID))
	}

	// 2. Non-TTY stdin (strings.Reader and regular file) emits zero stderr noise.
	for name, r := range map[string]io.Reader{
		"string_reader": strings.NewReader(pingFrame),
		"regular_file":  tmpFile,
		"stat_error":    charDeviceReader{Reader: strings.NewReader(pingFrame), err: errors.New("stat failed")},
	} {
		t.Run(name, func(t *testing.T) {
			cmd := NewMCPCmd("4.22.0")
			var outBuf, errBuf bytes.Buffer
			cmd.SetIn(r)
			cmd.SetOut(&outBuf)
			cmd.SetErr(&errBuf)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute (%s) failed: %v", name, err)
			}
			if errBuf.Len() != 0 {
				t.Fatalf("expected empty stderr for non-TTY %s, got %q", name, errBuf.String())
			}
			if !strings.Contains(outBuf.String(), `"id":1`) {
				t.Fatalf("expected JSON-RPC response on stdout for %s, got %q", name, outBuf.String())
			}
		})
	}
}

// @verifies TC-MCP-02
func TestMCPVersionNegotiationResourcesPromptsAndCapabilities(t *testing.T) {
	t.Setenv(constants.EnvNomosHome, t.TempDir())
	frames := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2026-07-28"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"2026-12-01"}}`,
		`{"jsonrpc":"2.0","id":3,"method":"resources/list"}`,
		`{"jsonrpc":"2.0","id":4,"method":"resources/templates/list"}`,
		`{"jsonrpc":"2.0","id":5,"method":"resources/read","params":{"uri":"nomos://status"}}`,
		`{"jsonrpc":"2.0","id":6,"method":"resources/read","params":{"uri":"nomos://directives"}}`,
		`{"jsonrpc":"2.0","id":7,"method":"prompts/list"}`,
		`{"jsonrpc":"2.0","id":8,"method":"prompts/get","params":{"name":"xray-audit","arguments":{"dir":"."}}}`,
		`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"nomos_capabilities","arguments":{"type":"gate"}}}`,
		`{"jsonrpc":"2.0","id":10,"method":"resources/read","params":{"uri":"nomos://unknown"}}`,
		`{"jsonrpc":"2.0","id":11,"method":"server/discover"}`,
		`{"jsonrpc":"2.0","id":12,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"1999-01-01"}}}`,
	}
	resps := runMCPSession(t, frames)
	if len(resps) != len(frames) {
		t.Fatalf("expected %d responses, got %d", len(frames), len(resps))
	}
	assertModernMCPResponses(t, resps)
	assertStatelessDiscoverAndMeta(t, resps[10], resps[11])
}

func assertModernMCPResponses(t *testing.T, resps []MCPResponse) {
	t.Helper()
	r0, err := json.Marshal(resps[0].Result)
	if err != nil || !strings.Contains(string(r0), `"2026-07-28"`) || !strings.Contains(string(r0), `"resources"`) || !strings.Contains(string(r0), `"prompts"`) {
		t.Fatalf("expected negotiated 2026-07-28 with resources & prompts capabilities, got %s (err=%v)", string(r0), err)
	}
	r1, err := json.Marshal(resps[1].Result)
	if err != nil || !strings.Contains(string(r1), `"2026-07-28"`) {
		t.Fatalf("expected 2026-* client to negotiate 2026-07-28, got %s (err=%v)", string(r1), err)
	}
	r2, err := json.Marshal(resps[2].Result)
	if err != nil || !strings.Contains(string(r2), "nomos://capabilities") {
		t.Fatalf("expected resources/list to include nomos://capabilities, got %s", string(r2))
	}
	if resps[4].Error != nil || resps[5].Error != nil || resps[7].Error != nil {
		t.Fatalf("expected resources/read and prompts/get to succeed, got %+v / %+v / %+v", resps[4].Error, resps[5].Error, resps[7].Error)
	}
	capTool := decodeToolResult(t, resps[8])
	if capTool.IsError || !strings.Contains(capTool.Content[0].Text, "gates_count") {
		t.Fatalf("expected nomos_capabilities tool JSON output, got %+v", capTool)
	}
	if resps[9].Error == nil || resps[9].Error.Code != rpcErrInvalidParams {
		t.Fatalf("expected invalid params (-32602) on unknown resource URI, got %+v", resps[9].Error)
	}
}

func assertStatelessDiscoverAndMeta(t *testing.T, discResp, badMetaResp MCPResponse) {
	t.Helper()
	discRaw, err := json.Marshal(discResp.Result)
	if err != nil || !strings.Contains(string(discRaw), `"supportedVersions"`) || !strings.Contains(string(discRaw), statelessMCPProtocolVersion) {
		t.Fatalf("expected server/discover supportedVersions with 2026-07-28, got %s (err=%v)", string(discRaw), err)
	}
	if badMetaResp.Error == nil || badMetaResp.Error.Code != rpcErrUnsupportedVersion {
		t.Fatalf("expected -32022 unsupported protocol version error, got %+v", badMetaResp.Error)
	}
}
