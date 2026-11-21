// Package astdebt provides Go AST contract-surface extraction for Twin parity checks.
package astdebt

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"hash/fnv"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

// GoSurfaceFingerprint captures CLI command/flag, YAML struct tag, and governance token sets (#GoSurfaceFingerprint).
type GoSurfaceFingerprint struct {
	CLITokens  map[string]bool
	YAMLFields map[string]bool
	GovTokens  map[string]bool
}

var cobraFlagVarMethods = map[string]int{
	"StringVar":      1,
	"StringVarP":     1,
	"BoolVar":        1,
	"BoolVarP":       1,
	"IntVar":         1,
	"IntVarP":        1,
	"Int64Var":       1,
	"Int64VarP":      1,
	"UintVar":        1,
	"UintVarP":       1,
	"Float64Var":     1,
	"Float64VarP":    1,
	"DurationVar":    1,
	"DurationVarP":   1,
	"StringSliceVar": 1,
	"StringArrayVar": 1,
	"CountVar":       1,
	"CountVarP":      1,
}

var cobraDirectFlagMethods = map[string]bool{
	"String":      true,
	"StringP":     true,
	"Bool":        true,
	"BoolP":       true,
	"Int":         true,
	"IntP":        true,
	"Duration":    true,
	"StringSlice": true,
}

// ParseGoSurfaceFingerprint executes deterministic astdebt verification logic (@intent tree_sitter_ast_gate_spec.md).
func ParseGoSurfaceFingerprint(src []byte) GoSurfaceFingerprint {
	return ParseGoSurfaceFingerprintForPath("", src)
}

func inspectGenDeclSurface(gd *ast.GenDecl, pkgScope string, fp *GoSurfaceFingerprint) {
	for _, spec := range gd.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			if st, ok := s.Type.(*ast.StructType); ok {
				collectStructTaggedFields("", s.Name.Name, st, "yaml", "", fp.YAMLFields)
				if pkgScope != "" && s.Name.IsExported() {
					collectStructTaggedFields(pkgScope, s.Name.Name, st, "json", "json_field:", fp.GovTokens)
				}
			}
			if pkgScope != "" && s.Name != nil && s.Name.IsExported() {
				fp.GovTokens["export_type:"+pkgScope+"."+s.Name.Name] = true
				collectCommentGroupIntentRefs(gd.Doc, s.Doc, pkgScope, s.Name.Name, fp.GovTokens)
			}
		case *ast.ValueSpec:
			if gd.Tok == token.CONST && pkgScope != "" {
				collectConstThresholdSpec(pkgScope, s, fp.GovTokens)
			} else {
				collectGovernanceValueSpec(s, fp.GovTokens)
			}
		}
	}
}

func collectStructTaggedFields(pkgScope, typeName string, st *ast.StructType, tagKey, prefix string, out map[string]bool) {
	if st == nil || st.Fields == nil {
		return
	}
	scopedType := typeName
	if pkgScope != "" {
		scopedType = pkgScope + "." + typeName
	}
	for _, f := range st.Fields.List {
		if f.Tag == nil {
			continue
		}
		rawTag, err := strconv.Unquote(f.Tag.Value)
		if err != nil {
			continue
		}
		tagVal := reflect.StructTag(rawTag).Get(tagKey)
		if tagVal == "" {
			continue
		}
		fieldName := strings.TrimSpace(strings.Split(tagVal, ",")[0])
		if fieldName == "" || fieldName == "-" {
			continue
		}
		out[prefix+scopedType+"."+fieldName] = true
	}
}

// collectConstThresholdSpec records exported Go consts and unexported numeric/duration/boolean/iota thresholds while skipping private prose strings.
func collectConstThresholdSpec(pkgScope string, vs *ast.ValueSpec, out map[string]bool) {
	if vs == nil {
		return
	}
	for i, nameIdent := range vs.Names {
		if nameIdent == nil || nameIdent.Name == "_" {
			continue
		}
		valSig := "iota"
		if i < len(vs.Values) {
			if !nameIdent.IsExported() && isPlainStringConstExpr(vs.Values[i]) {
				continue
			}
			valSig = formatConstExprCompact(vs.Values[i])
		}
		out["gov_const:"+pkgScope+"."+nameIdent.Name+"="+valSig] = true
	}
}

// isPlainStringConstExpr reports whether expr is a bare Go string literal rather than a numeric, duration, or boolean threshold.
func isPlainStringConstExpr(expr ast.Expr) bool {
	lit, ok := expr.(*ast.BasicLit)
	return ok && lit.Kind == token.STRING
}

// formatConstExprCompact renders a deterministic single-line Go AST constant value signature.
func formatConstExprCompact(expr ast.Expr) string {
	var buf bytes.Buffer
	if err := format.Node(&buf, token.NewFileSet(), expr); err != nil {
		return "?"
	}
	compact := strings.Join(strings.Fields(buf.String()), "")
	if len(compact) > 48 {
		compact = compact[:48]
	}
	return compact
}

var sqlMutationPrefixRe = regexp.MustCompile(`(?i)^\s*(CREATE\s+TABLE(?:\s+IF\s+NOT\s+EXISTS)?|ALTER\s+TABLE|DROP\s+TABLE(?:\s+IF\s+EXISTS)?|INSERT(?:\s+OR\s+[A-Z]+)?\s+INTO|UPDATE|DELETE\s+FROM)\s+` + "`?" + `([A-Za-z_][A-Za-z0-9_]*)`)

// extractSQLMutationToken fingerprints SQLite DDL/DML statements so unexported persistence mutations require INT twins.
func extractSQLMutationToken(pkgScope, literalVal string) string {
	m := sqlMutationPrefixRe.FindStringSubmatch(literalVal)
	if len(m) < 3 {
		return ""
	}
	verb := strings.Join(strings.Fields(strings.ToUpper(m[1])), "_")
	table := strings.ToLower(m[2])
	h := fnv.New32a()
	if _, err := h.Write([]byte(strings.Join(strings.Fields(strings.ToLower(literalVal)), " "))); err != nil {
		return ""
	}
	return fmt.Sprintf("sql_dml:%s:%s:%s:%08x", pkgScope, verb, table, h.Sum32())
}

// collectGovernanceValueSpec extracts compiled commit schema regex literals from package var blocks.
func collectGovernanceValueSpec(vs *ast.ValueSpec, out map[string]bool) {
	for i, nameIdent := range vs.Names {
		if nameIdent.Name != "goldenSubjectSchema" || i >= len(vs.Values) {
			continue
		}
		call, ok := vs.Values[i].(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			continue
		}
		if pat, ok := extractStringLiteral(call.Args[0]); ok && pat != "" {
			out["commit_schema:"+pat] = true
		}
	}
}

func inspectCobraCommandLit(lit *ast.CompositeLit, cliOut map[string]bool) {
	if !isCobraCommandType(lit.Type) {
		return
	}
	for _, elt := range lit.Elts {
		if cmdName := extractUseKeyValue(elt); cmdName != "" {
			cliOut["cmd:"+cmdName] = true
		}
	}
}

func extractUseKeyValue(expr ast.Expr) string {
	kv, ok := expr.(*ast.KeyValueExpr)
	if !ok {
		return ""
	}
	keyIdent, ok := kv.Key.(*ast.Ident)
	if !ok || keyIdent.Name != "Use" {
		return ""
	}
	useVal, ok := extractStringLiteral(kv.Value)
	if !ok {
		return ""
	}
	fields := strings.Fields(useVal)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func inspectSurfaceCallExpr(call *ast.CallExpr, fp *GoSurfaceFingerprint) {
	if probeID := extractRegisterGateCall(call); probeID != "" {
		fp.GovTokens["gate:"+probeID] = true
		return
	}
	if flagName := extractCobraFlagCall(call); flagName != "" {
		fp.CLITokens["--"+flagName] = true
	}
}

func extractRegisterGateCall(call *ast.CallExpr) string {
	ident, ok := call.Fun.(*ast.Ident)
	if !ok || ident.Name != "RegisterGate" || len(call.Args) == 0 {
		return ""
	}
	if probeID, ok := extractStringLiteral(call.Args[0]); ok {
		return probeID
	}
	return ""
}

func extractCobraFlagCall(call *ast.CallExpr) string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel == nil {
		return ""
	}
	if argIdx, isVar := cobraFlagVarMethods[sel.Sel.Name]; isVar && len(call.Args) > argIdx {
		if name, ok := extractStringLiteral(call.Args[argIdx]); ok {
			return name
		}
		return ""
	}
	if cobraDirectFlagMethods[sel.Sel.Name] && isFlagSetChain(sel.X) && len(call.Args) > 0 {
		if name, ok := extractStringLiteral(call.Args[0]); ok {
			return name
		}
	}
	return ""
}

func isFlagSetChain(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel == nil {
		return false
	}
	switch sel.Sel.Name {
	case "Flags", "PersistentFlags", "LocalFlags":
		return true
	default:
		return false
	}
}

func extractStringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	val, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return val, true
}

// MergeSymmetricDiff executes deterministic astdebt verification logic (@intent tree_sitter_ast_gate_spec.md).
func MergeSymmetricDiff(dst, base, curr map[string]bool) {
	for k := range curr {
		if !base[k] {
			dst["+"+k] = true
		}
	}
	for k := range base {
		if !curr[k] {
			dst["-"+k] = true
		}
	}
}

var (
	webBlockCommentRe = regexp.MustCompile(`(?s)/\*.*?\*/`)
	webLineCommentRe  = regexp.MustCompile(`(?m)//.*$`)
	webExportValRe    = regexp.MustCompile(`(?m)^\s*export\s+(?:(?:async\s+)?function|const|let|var|class|enum)\s+([A-Za-z_$][A-Za-z0-9_$]*)`)
	webExportTypeRe   = regexp.MustCompile(`(?m)^\s*export\s+(?:interface|type)\s+([A-Za-z_$][A-Za-z0-9_$]*)`)
)

// IsNonTestWebSourcePath reports whether relPath is a non-test TypeScript, JavaScript, or Svelte source file (@intent nomos_hermetic_worktree_spec.md #NormativeSurfaceDelta).
func IsNonTestWebSourcePath(relPath string) bool {
	norm := strings.ToLower(filepath.ToSlash(strings.TrimSpace(relPath)))
	if norm == "" || isExcludedWebSurfacePath(norm) {
		return false
	}
	for _, ext := range []string{".ts", ".js", ".mjs", ".svelte"} {
		if strings.HasSuffix(norm, ext) {
			return true
		}
	}
	return false
}

// isExcludedWebSurfacePath filters build outputs, type stubs, and test suites from web surface fingerprinting.
func isExcludedWebSurfacePath(lowerSlash string) bool {
	for _, seg := range []string{"node_modules/", ".svelte-kit/", "/__tests__/", "dist/", "build/"} {
		if strings.Contains(lowerSlash, seg) {
			return true
		}
	}
	for _, suf := range []string{".d.ts", ".test.ts", ".spec.ts", ".test.js", ".spec.js"} {
		if strings.HasSuffix(lowerSlash, suf) {
			return true
		}
	}
	return false
}

// ParseWebSurfaceFingerprintForPath extracts directory-scoped TS/JS exports and Svelte component tokens (@intent nomos_hermetic_worktree_spec.md #NormativeSurfaceDelta).
func ParseWebSurfaceFingerprintForPath(relPath string, src []byte) map[string]bool {
	out := make(map[string]bool)
	if len(src) == 0 || !IsNonTestWebSourcePath(relPath) {
		return out
	}
	norm := filepath.ToSlash(strings.TrimSpace(relPath))
	dirScope := filepath.ToSlash(filepath.Dir(filepath.FromSlash(norm)))
	dirScope = strings.TrimPrefix(dirScope, "./")
	if dirScope == "" || dirScope == "." {
		dirScope = "root"
	}
	if strings.HasSuffix(strings.ToLower(norm), ".svelte") {
		compName := strings.TrimSuffix(filepath.Base(norm), filepath.Ext(norm))
		if compName != "" {
			out["svelte_component:"+dirScope+":"+compName] = true
		}
	}
	clean := stripWebComments(string(src))
	for _, m := range webExportValRe.FindAllStringSubmatch(clean, -1) {
		if len(m) > 1 {
			out["export_ts:"+dirScope+"."+m[1]] = true
		}
	}
	for _, m := range webExportTypeRe.FindAllStringSubmatch(clean, -1) {
		if len(m) > 1 {
			out["export_type_ts:"+dirScope+"."+m[1]] = true
		}
	}
	return out
}

// stripWebComments removes JS/TS block and line comments prior to lexical export matching.
func stripWebComments(src string) string {
	noBlock := webBlockCommentRe.ReplaceAllString(src, "")
	return webLineCommentRe.ReplaceAllString(noBlock, "")
}
