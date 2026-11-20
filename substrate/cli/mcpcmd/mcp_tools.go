// Package mcpcmd implements read-only MCP tool schemas and non-mutating handlers.
// @intent task_lifecycle_cli_spec.md
package mcpcmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mgantlett/nomos/substrate/cli/diag"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/config"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/storage"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/handshake"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/mem"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/task"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/impact"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/xray"
)

const (
	schemaTypeObject  = "object"
	schemaTypeString  = "string"
	schemaTypeInteger = "integer"
	propTypeKey       = "type"
	propDescKey       = "description"
	keyProperties     = "properties"
)

// ReadOnlyTools returns the canonical descriptor catalog of non-mutating Nomos MCP tools.
func ReadOnlyTools() []MCPToolDescriptor {
	ro := readOnlyToolAnnotations()
	dirProp := directoryPropertySchema()
	tools := make([]MCPToolDescriptor, 0, 7)
	tools = append(tools, buildSessionToolDescriptors(ro, dirProp)...)
	tools = append(tools, buildLedgerToolDescriptors(ro, dirProp)...)
	tools = append(tools, buildAnalysisToolDescriptors(ro, dirProp)...)
	tools = append(tools, capabilitiesToolDescriptor(ro))
	return tools
}

// CallReadOnlyTool dispatches strictly read-only MCP tool invocations and rejects mutating names.
func CallReadOnlyTool(name string, args map[string]any) (string, bool) {
	switch name {
	case "nomos_handshake":
		return execHandshakeTool(args)
	case "nomos_status":
		return marshalToolJSON(diag.CollectStatusReport())
	case "nomos_task_dag":
		return execTaskDAGTool(args)
	case "nomos_mem_recall":
		return execMemRecallTool(args)
	case "nomos_verify_impact":
		return execVerifyImpactTool(args)
	case "nomos_xray":
		return execXrayTool(args)
	case "nomos_capabilities":
		return execCapabilitiesTool(args)
	default:
		return fmt.Sprintf("ERR_MCP_READ_ONLY: tool %q is unknown or mutating; only read-only orientation tools are exposed", name), true
	}
}

func objectSchema(props map[string]any, required []string) map[string]any {
	schema := map[string]any{
		propTypeKey:   schemaTypeObject,
		keyProperties: props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func execHandshakeTool(args map[string]any) (string, bool) {
	targetDir := resolveTargetDir(stringArg(args, "dir"))
	event, err := handshake.SynthesizeDynamicHandshake(targetDir)
	if err != nil {
		return err.Error(), true
	}
	return marshalToolJSON(event)
}

func execTaskDAGTool(args map[string]any) (string, bool) {
	dbPath := config.ResolveGlobalDBPath()
	// Stat before opening SQLite so uninitialized hosts never receive implicit DB writes.
	if _, err := os.Stat(dbPath); err != nil {
		return "[]", false
	}
	db, err := storage.OpenDB(dbPath)
	if err != nil {
		return err.Error(), true
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			// Best-effort close on read-only query path.
		}
	}()

	store := task.NewSQLiteTaskStore(db)
	candidates, err := store.GetWavefront()
	if err != nil {
		return err.Error(), true
	}
	targetDir := resolveTargetDir(stringArg(args, "dir"))
	project := task.ResolveRepoProject(targetDir)
	filtered := make([]task.WavefrontCandidate, 0, len(candidates))
	for _, c := range candidates {
		if task.MatchesProject(c.Project, project, true) {
			filtered = append(filtered, c)
		}
	}
	limit := intArg(args, "limit", 0)
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return marshalToolJSON(filtered)
}

func execMemRecallTool(args map[string]any) (string, bool) {
	query := strings.TrimSpace(stringArg(args, "query"))
	if query == "" {
		return "invalid arguments: query is required", true
	}
	dbPath := config.ResolveMnemeDBPath()
	// Stat before opening SQLite so read-only recall never creates a fresh Mneme DB file.
	if _, err := os.Stat(dbPath); err != nil {
		return "[]", false
	}
	store, err := mem.NewStore(dbPath)
	if err != nil {
		return err.Error(), true
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			// Best-effort close on read-only recall path.
		}
	}()

	limit := intArg(args, "limit", 5)
	if limit <= 0 {
		limit = 5
	}
	results, err := store.Search(query, limit, "", "")
	if err != nil {
		return err.Error(), true
	}
	return marshalToolJSON(results)
}

func execVerifyImpactTool(args map[string]any) (string, bool) {
	targetDir := resolveTargetDir(stringArg(args, "dir"))
	impacted := impact.ComputeWorkspaceImpact(targetDir, nil)
	if impacted == nil {
		impacted = []string{}
	}
	return marshalToolJSON(map[string]any{
		"dir":               targetDir,
		"impacted_packages": impacted,
	})
}

func execXrayTool(args map[string]any) (string, bool) {
	targetDir := resolveTargetDir(stringArg(args, "dir"))
	opts := xray.DefaultScanOptions()
	if top := intArg(args, "top", 0); top > 0 {
		opts.TopHotspots = top
	}
	report, err := xray.ScanRepository(targetDir, opts)
	if err != nil {
		return err.Error(), true
	}
	return marshalToolJSON(report)
}

func resolveTargetDir(raw string) string {
	dir := strings.TrimSpace(raw)
	if dir == "" {
		if wd, err := os.Getwd(); err == nil {
			dir = wd
		} else {
			dir = "."
		}
	}
	if root, err := workspace.FindRepoRoot(dir); err == nil && strings.TrimSpace(root) != "" {
		return root
	}
	return dir
}

func stringArg(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	v, ok := args[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func intArg(args map[string]any, key string, fallback int) int {
	if args == nil {
		return fallback
	}
	v, ok := args[key]
	if !ok || v == nil {
		return fallback
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return fallback
	}
}

func marshalToolJSON(v any) (string, bool) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("marshal error: %v", err), true
	}
	return string(data), false
}
