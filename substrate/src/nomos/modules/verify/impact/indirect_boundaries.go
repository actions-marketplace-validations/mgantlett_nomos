// Package impact detects runtime, wire, SQL, CLI flag, and non-AST coupling boundaries in modified files.
// @intent software_physics_engine_spec.md #WorkspaceImpactReport
package impact

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	boundaryHTTPRoute      = "http_route"
	boundarySQLSchema      = "sql_schema"
	boundaryWireContract   = "wire_contract"
	boundaryCLIFlag        = "cli_flag"
	boundaryNonASTArtifact = "non_ast_artifact"
)

var (
	sqlTableStmtRe = regexp.MustCompile(`(?i)\b(?:CREATE\s+TABLE(?:\s+IF\s+NOT\s+EXISTS)?|ALTER\s+TABLE|DROP\s+TABLE(?:\s+IF\s+EXISTS)?|INSERT\s+INTO|UPDATE)\s+([a-zA-Z_][a-zA-Z0-9_]*)`)
	httpRoutePfxRe = regexp.MustCompile(`^/(?:api|v1|v2|rpc|ws|webhooks?)(?:/|$)`)
)

// IndirectBoundarySignal describes a runtime or wire coupling boundary in modified files (#IndirectBoundarySignal).
type IndirectBoundarySignal struct {
	Category string   `json:"category"`
	File     string   `json:"file"`
	Symbols  []string `json:"symbols,omitempty"`
	Summary  string   `json:"summary"`
}

// WorkspaceImpactReport pairs 1-hop AST reverse-dependency packages with indirect boundary signals (#WorkspaceImpactReport).
type WorkspaceImpactReport struct {
	ModifiedFiles      []string                 `json:"modified_files"`
	ImpactedPackages   []string                 `json:"impacted_packages"`
	IndirectBoundaries []IndirectBoundarySignal `json:"indirect_boundaries,omitempty"`
}

// ComputeWorkspaceImpactReport synthesizes 1-hop static AST impact alongside indirect runtime/wire coupling signals.
// @intent software_physics_engine_spec.md #WorkspaceImpactReport
func ComputeWorkspaceImpactReport(worktreeDir string, modifiedFiles []string) *WorkspaceImpactReport {
	if len(modifiedFiles) == 0 {
		modifiedFiles = ListModifiedWorktreeFiles(worktreeDir, "")
	}
	if modifiedFiles == nil {
		modifiedFiles = []string{}
	}
	impacted := ComputeWorkspaceImpact(worktreeDir, modifiedFiles)
	if impacted == nil {
		impacted = []string{}
	}
	boundaries := DetectIndirectBoundaries(worktreeDir, modifiedFiles)
	return &WorkspaceImpactReport{
		ModifiedFiles:      modifiedFiles,
		ImpactedPackages:   impacted,
		IndirectBoundaries: boundaries,
	}
}

// DetectIndirectBoundaries inspects modified non-test files for HTTP routes, SQL tables, wire tags, CLI flags, and non-AST artifacts.
// @intent software_physics_engine_spec.md #IndirectBoundarySignal
func DetectIndirectBoundaries(worktreeDir string, modifiedFiles []string) []IndirectBoundarySignal {
	if strings.TrimSpace(worktreeDir) == "" || len(modifiedFiles) == 0 {
		return nil
	}
	fset := token.NewFileSet()
	var signals []IndirectBoundarySignal
	for _, rel := range modifiedFiles {
		cleanRel := filepath.ToSlash(strings.TrimSpace(rel))
		if cleanRel == "" || IsTestSourceFile(cleanRel) || isDocumentationFile(cleanRel) {
			continue
		}
		absPath := filepath.Join(worktreeDir, filepath.FromSlash(cleanRel))
		signals = append(signals, inspectFileIndirectBoundaries(fset, absPath, cleanRel)...)
	}
	sort.Slice(signals, func(i, j int) bool {
		if signals[i].File != signals[j].File {
			return signals[i].File < signals[j].File
		}
		return signals[i].Category < signals[j].Category
	})
	return signals
}

// inspectFileIndirectBoundaries classifies one modified non-test file into indirect coupling boundary signals.
func inspectFileIndirectBoundaries(fset *token.FileSet, absPath, cleanRel string) []IndirectBoundarySignal {
	lower := strings.ToLower(cleanRel)
	if strings.HasSuffix(lower, ".go") {
		return inspectGoIndirectBoundaries(fset, absPath, cleanRel)
	}
	if strings.HasSuffix(lower, ".sql") {
		return inspectSQLFileBoundaries(absPath, cleanRel)
	}
	if isNonASTOperationalArtifact(lower) {
		return []IndirectBoundarySignal{{
			Category: boundaryNonASTArtifact,
			File:     cleanRel,
			Summary:  "Non-AST script or configuration artifact outside static import graph",
		}}
	}
	return nil
}

// goBoundaryAccumulator collects deduplicated indirect boundary symbols discovered in a Go AST file.
type goBoundaryAccumulator struct {
	routes     map[string]bool
	sqlTables  map[string]bool
	wireTypes  map[string]bool
	cliFlags   map[string]bool
}

// inspectGoIndirectBoundaries parses a Go source file AST to extract route, SQL, wire-tag, and CLI flag boundaries.
func inspectGoIndirectBoundaries(fset *token.FileSet, absPath, cleanRel string) []IndirectBoundarySignal {
	node, err := parser.ParseFile(fset, absPath, nil, 0)
	if err != nil {
		return nil
	}
	acc := &goBoundaryAccumulator{
		routes:    make(map[string]bool),
		sqlTables: make(map[string]bool),
		wireTypes: make(map[string]bool),
		cliFlags:  make(map[string]bool),
	}
	collectGoWireStructs(node, acc)
	ast.Inspect(node, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			inspectGoCallBoundaries(x, acc)
		case *ast.BasicLit:
			inspectGoStringLitBoundaries(x, acc)
		}
		return true
	})
	return buildGoBoundarySignals(cleanRel, acc)
}

// collectGoWireStructs records exported Go structs carrying json or yaml struct field tags.
func collectGoWireStructs(node *ast.File, acc *goBoundaryAccumulator) {
	for _, decl := range node.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name == nil || !ts.Name.IsExported() {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if ok && structHasWireTags(st) {
				acc.wireTypes[ts.Name.Name] = true
			}
		}
	}
}

// structHasWireTags reports whether st declares at least one json or yaml struct field tag.
func structHasWireTags(st *ast.StructType) bool {
	if st == nil || st.Fields == nil {
		return false
	}
	for _, f := range st.Fields.List {
		if f.Tag == nil {
			continue
		}
		raw := f.Tag.Value
		if strings.Contains(raw, "json:\"") || strings.Contains(raw, "yaml:\"") {
			return true
		}
	}
	return false
}

// inspectGoCallBoundaries checks AST call expressions for HTTP route registrations and Cobra CLI flag definitions.
func inspectGoCallBoundaries(call *ast.CallExpr, acc *goBoundaryAccumulator) {
	if flagName := extractCobraFlagCallName(call); flagName != "" {
		acc.cliFlags["--"+flagName] = true
		return
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel == nil {
		return
	}
	name := sel.Sel.Name
	if (name == "HandleFunc" || name == "Handle") && len(call.Args) >= 1 {
		if lit, okStr := unquoteBasicLit(call.Args[0]); okStr && strings.HasPrefix(lit, "/") {
			acc.routes[lit] = true
		}
	}
}

// inspectGoStringLitBoundaries inspects Go string literals for API route prefixes and SQL table statements.
func inspectGoStringLitBoundaries(lit *ast.BasicLit, acc *goBoundaryAccumulator) {
	if lit == nil || lit.Kind != token.STRING {
		return
	}
	val, err := strconv.Unquote(lit.Value)
	if err != nil {
		return
	}
	trimmed := strings.TrimSpace(val)
	if httpRoutePfxRe.MatchString(trimmed) && !strings.ContainsAny(trimmed, " \t\n") {
		acc.routes[trimmed] = true
	}
	extractSQLTablesInto(val, acc.sqlTables)
}

// unquoteBasicLit unquotes an AST string literal expression when present.
func unquoteBasicLit(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

// inspectSQLFileBoundaries extracts SQL table names from a standalone .sql migration or schema file.
func inspectSQLFileBoundaries(absPath, cleanRel string) []IndirectBoundarySignal {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil
	}
	tables := make(map[string]bool)
	extractSQLTablesInto(string(data), tables)
	return []IndirectBoundarySignal{{
		Category: boundarySQLSchema,
		File:     cleanRel,
		Symbols:  sortedBoolKeys(tables),
		Summary:  "SQL schema or query file coupled across database callers",
	}}
}

// extractSQLTablesInto extracts SQL table identifiers from DDL and DML statements into dst.
func extractSQLTablesInto(sqlText string, dst map[string]bool) {
	for _, m := range sqlTableStmtRe.FindAllStringSubmatch(sqlText, -1) {
		if len(m) >= 2 {
			tbl := strings.ToLower(strings.TrimSpace(m[1]))
			if tbl != "set" && tbl != "select" && tbl != "values" {
				dst[tbl] = true
			}
		}
	}
}

// buildGoBoundarySignals converts accumulated Go boundary sets into sorted IndirectBoundarySignal entries.
func buildGoBoundarySignals(cleanRel string, acc *goBoundaryAccumulator) []IndirectBoundarySignal {
	var out []IndirectBoundarySignal
	if len(acc.routes) > 0 {
		out = append(out, IndirectBoundarySignal{
			Category: boundaryHTTPRoute,
			File:     cleanRel,
			Symbols:  sortedBoolKeys(acc.routes),
			Summary:  "HTTP route endpoints consumed outside static Go imports",
		})
	}
	if len(acc.sqlTables) > 0 {
		out = append(out, IndirectBoundarySignal{
			Category: boundarySQLSchema,
			File:     cleanRel,
			Symbols:  sortedBoolKeys(acc.sqlTables),
			Summary:  "SQLite/SQL table operations coupled across query callers",
		})
	}
	if len(acc.wireTypes) > 0 {
		out = append(out, IndirectBoundarySignal{
			Category: boundaryWireContract,
			File:     cleanRel,
			Symbols:  sortedBoolKeys(acc.wireTypes),
			Summary:  "Serialized JSON/YAML wire contract structs",
		})
	}
	if len(acc.cliFlags) > 0 {
		out = append(out, IndirectBoundarySignal{
			Category: boundaryCLIFlag,
			File:     cleanRel,
			Symbols:  sortedBoolKeys(acc.cliFlags),
			Summary:  "CLI flag surface consumed by scripts and hooks",
		})
	}
	return out
}

// isDocumentationFile reports whether cleanRel is prose documentation rather than executable/config source.
func isDocumentationFile(cleanRel string) bool {
	lower := strings.ToLower(cleanRel)
	return strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".txt") || strings.HasPrefix(lower, "intent/")
}

// isNonASTOperationalArtifact reports whether lower is a non-AST script, manifest, or configuration file.
func isNonASTOperationalArtifact(lower string) bool {
	base := filepath.Base(lower)
	if base == "dockerfile" || base == "makefile" {
		return true
	}
	for _, ext := range []string{".sh", ".bash", ".zsh", ".yaml", ".yml", ".json", ".toml", ".proto", ".graphql"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}
