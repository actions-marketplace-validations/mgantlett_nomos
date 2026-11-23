// Package wiredrift implements deterministic polyglot AST sensors for untyped DTO boundary escapes and phantom wire keys.
// @intent tree_sitter_ast_gate_spec.md #BoundaryDriftFinding
package wiredrift

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	// KindUntypedBoundaryRead identifies an untyped map field exposed on a typed DTO contract.
	KindUntypedBoundaryRead = "untyped_boundary_read"
	// KindPhantomWireKey identifies a consumer snake_case read matching zero compiled wire keys.
	KindPhantomWireKey = "phantom_wire_key"
)

// BoundaryDriftFinding records a deterministic DTO escape or phantom wire-key violation (#BoundaryDriftFinding).
type BoundaryDriftFinding struct {
	File       string
	Line       int
	Kind       string
	Symbol     string
	ClosestTag string
	Detail     string
}

var (
	contractTypeSuffixes = []string{"Result", "Response", "Payload", "Snapshot", "Report", "DTO"}

	exemptOpenSchemaFields = map[string]bool{
		"inputschema": true, "outputschema": true, "structuredcontent": true,
		"schema": true, "schemas": true, "metadata": true, "meta": true,
		"frontmatter": true, "headers": true, "params": true, "args": true,
		"arguments": true, "query": true, "context": true, "extra": true,
		"extras": true, "attributes": true, "properties": true, "annotations": true,
		"labels": true, "env": true, "config": true, "options": true,
		"settings": true, "provenance": true, "raw": true, "details": true,
		"metrics": true, "fields": true, "values": true, "vars": true, "variables": true,
	}

	tsExportContractBlockRe = regexp.MustCompile(`(?ms)export\s+(?:interface\s+(\w+)[^{]*\{([^}]*)\}|type\s+(\w+)\s*=\s*\{([^}]*)\})`)
	tsUntypedRecordFieldRe  = regexp.MustCompile(`^\s*([A-Za-z_]\w*)\s*\??\s*:\s*(?:Array<\s*)?Record<\s*string\s*,\s*(?:any|unknown)\s*>`)
	pyClassHeaderRe         = regexp.MustCompile(`^class\s+(\w+)\s*(?:\([^)]*\))?\s*:`)
	pyUntypedDictFieldRe    = regexp.MustCompile(`^\s+([A-Za-z_]\w*)\s*:\s*(?:Optional\[\s*)?(?:dict|Dict)\[\s*str\s*,\s*(?:Any|object)\s*\]`)
	snakeFieldDeclRe        = regexp.MustCompile(`\b([a-z][a-z0-9]*_[a-z0-9_]+)\s*\??\s*:`)
	quotedSnakeLiteralRe    = regexp.MustCompile(`["']([a-z][a-z0-9]*_[a-z0-9_]+)["']`)
	untypedParamDeclRe      = regexp.MustCompile(`\b([A-Za-z_]\w*)\s*:\s*(?:Record<\s*string\s*,\s*(?:any|unknown)\s*>|(?:dict|Dict)\[\s*str\s*,\s*(?:Any|object)\s*\])`)
	untypedCastAssignRe     = regexp.MustCompile(`\b(?:const|let|var)\s+([A-Za-z_]\w*)\s*=\s*[^;\n]+\bas\s+(?:unknown\s+as\s+)?Record<\s*string\s*,\s*(?:any|unknown)\s*>`)
)

// ScanCorpusBoundaryWireDrift evaluates non-test Go, TypeScript/Svelte, and Python sources for DTO escapes and phantom wire keys (@intent tree_sitter_ast_gate_spec.md #BoundaryDriftFinding).
func ScanCorpusBoundaryWireDrift(sources map[string][]byte) []BoundaryDriftFinding {
	if len(sources) == 0 {
		return nil
	}
	paths := sortedCorpusPaths(sources)
	wireKeys := make(map[string]bool)
	quotedSnake := make(map[string]bool)
	untypedDTOFields := make(map[string]bool)
	var findings []BoundaryDriftFinding

	for _, rel := range paths {
		data := sources[rel]
		if isGeneratedBoundarySource(rel, data) {
			continue
		}
		collectDeclaredWireKeys(rel, data, wireKeys, quotedSnake)
		escapes, fields := scanFileUntypedDTOEscapes(rel, data)
		findings = append(findings, escapes...)
		for f := range fields {
			untypedDTOFields[f] = true
		}
	}

	if len(wireKeys) == 0 {
		return findings
	}
	for _, rel := range paths {
		data := sources[rel]
		if isGeneratedBoundarySource(rel, data) {
			continue
		}
		findings = append(findings, scanFilePhantomWireKeys(rel, data, wireKeys, quotedSnake, untypedDTOFields)...)
	}
	return findings
}

func sortedCorpusPaths(sources map[string][]byte) []string {
	paths := make([]string, 0, len(sources))
	for rel := range sources {
		paths = append(paths, filepath.ToSlash(rel))
	}
	sort.Strings(paths)
	return paths
}

func isGeneratedBoundarySource(rel string, data []byte) bool {
	lower := strings.ToLower(filepath.Base(rel))
	if strings.HasSuffix(lower, ".d.ts") || lower == "generated.ts" || lower == "generated_schemas.ts" {
		return true
	}
	head := string(data)
	if len(head) > 320 {
		head = head[:320]
	}
	return strings.Contains(head, "DO NOT EDIT") || strings.Contains(head, "Auto-generated") || strings.Contains(head, "Code generated")
}

func hasContractTypeSuffix(typeName string) bool {
	for _, suffix := range contractTypeSuffixes {
		if strings.HasSuffix(typeName, suffix) && len(typeName) > len(suffix) {
			return true
		}
	}
	return false
}

func isExemptOpenSchemaField(fieldName string) bool {
	norm := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(fieldName), "_", ""))
	return exemptOpenSchemaFields[norm]
}

// FormatBoundaryDriftFinding renders a human-actionable Law 4 violation message.
func FormatBoundaryDriftFinding(f BoundaryDriftFinding) string {
	if f.Kind == KindPhantomWireKey {
		return fmt.Sprintf("%s:%d [%s] %s (closest wire tag: %s)", f.File, f.Line, f.Kind, f.Detail, f.ClosestTag)
	}
	return fmt.Sprintf("%s:%d [%s] %s", f.File, f.Line, f.Kind, f.Detail)
}
