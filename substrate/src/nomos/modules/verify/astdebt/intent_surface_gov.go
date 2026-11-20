// Package astdebt extracts scoped Go symbol, wire-schema, CLI command, and rule-literal governance tokens.
package astdebt

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// requiredCobraFields lists the mandatory self-documenting contract fields for every static CLI command.
var requiredCobraFields = []string{"Use", "Short", "Long", "Example"}

// ScanCobraAliasesInDir executes deterministic astdebt verification logic (@intent tree_sitter_ast_gate_spec.md).
func ScanCobraAliasesInDir(cliDir string) ([]string, error) {
	fset := token.NewFileSet()
	var aliases []string
	err := filepath.Walk(cliDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		node, pErr := parser.ParseFile(fset, path, nil, 0)
		if pErr != nil {
			return nil
		}
		aliases = append(aliases, extractAliasesFromNode(node)...)
		return nil
	})
	return aliases, err
}

func extractAliasesFromNode(node *ast.File) []string {
	var aliases []string
	ast.Inspect(node, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		key, isIdent := kv.Key.(*ast.Ident)
		if isIdent && key.Name == "Aliases" {
			aliases = append(aliases, fmt.Sprintf("%v", kv.Value))
		}
		return true
	})
	return aliases
}

// ScanCobraSurfaceCompletenessInDir inspects CLI Go ASTs to ensure every static cobra.Command defines Use, Short, Long, and Example.
func ScanCobraSurfaceCompletenessInDir(cliDir string) ([]string, error) {
	fset := token.NewFileSet()
	var violations []string
	err := filepath.Walk(cliDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		node, pErr := parser.ParseFile(fset, path, nil, 0)
		if pErr != nil {
			return nil
		}
		rel, rErr := filepath.Rel(cliDir, path)
		if rErr != nil {
			rel = filepath.Base(path)
		}
		ast.Inspect(node, func(n ast.Node) bool {
			comp, ok := n.(*ast.CompositeLit)
			if !ok || !isCobraCommandType(comp.Type) {
				return true
			}
			for _, missing := range ValidateCobraCommandCompositeLit(comp) {
				pos := fset.Position(comp.Pos())
				violations = append(violations, fmt.Sprintf("%s:%d (%s)", rel, pos.Line, missing))
			}
			return true
		})
		return nil
	})
	return violations, err
}

func isCobraCommandType(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	return ok && pkgIdent.Name == "cobra" && sel.Sel.Name == "Command"
}

// ValidateCobraCommandCompositeLit returns missing or empty contract fields on a cobra.Command literal.
// Dynamic passthrough shims with DisableFlagParsing: true delegate help directly to external binaries.
func ValidateCobraCommandCompositeLit(comp *ast.CompositeLit) []string {
	if comp == nil {
		return nil
	}
	present := make(map[string]bool, len(comp.Elts))
	cmdUse := "<unnamed>"
	passthrough := false

	for _, elt := range comp.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		if key.Name == "DisableFlagParsing" && isTrueIdent(kv.Value) {
			passthrough = true
		}
		if !isEmptyStringLiteral(kv.Value) {
			present[key.Name] = true
			if key.Name == "Use" {
				if lit, ok := kv.Value.(*ast.BasicLit); ok {
					cmdUse = strings.Trim(lit.Value, "\"`")
				}
			}
		}
	}

	if passthrough {
		return nil
	}

	var missing []string
	for _, req := range requiredCobraFields {
		if !present[req] {
			missing = append(missing, fmt.Sprintf("command %q missing non-empty %s", cmdUse, req))
		}
	}
	return missing
}

func isTrueIdent(expr ast.Expr) bool {
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == "true"
}

func isEmptyStringLiteral(expr ast.Expr) bool {
	if expr == nil {
		return true
	}
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}
	return strings.TrimSpace(strings.Trim(lit.Value, "\"`")) == ""
}

var (
	intentDocTagRe         = regexp.MustCompile(`@intent:?\s+([^\s)]+(?:\s+#[A-Za-z0-9_.-]+)?)`)
	handshakeBannerFieldRe = regexp.MustCompile(`^  [A-Z][A-Za-z0-9 &_-]{2,22}\s*:\s*`)
	govRuleMarkers         = []string{"%s:%d", "(Law ", "[NOMOS ", "[Nomos "}
)

// ParseGoSurfaceFingerprintForPath parses Go AST CLI, YAML, and path-scoped governance tokens (@intent nomos_hermetic_worktree_spec.md #NormativeSurfaceDelta).
func ParseGoSurfaceFingerprintForPath(relPath string, src []byte) GoSurfaceFingerprint {
	fp := GoSurfaceFingerprint{
		CLITokens:  make(map[string]bool),
		YAMLFields: make(map[string]bool),
		GovTokens:  make(map[string]bool),
	}
	if len(src) == 0 {
		return fp
	}
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "surface.go", src, parser.ParseComments)
	if err != nil || node == nil {
		return fp
	}
	pkgScope := resolveSurfacePkgScope(relPath, node.Name.Name)
	govPath := isGovernanceRuleSourcePath(relPath)
	for _, decl := range node.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			inspectGenDeclSurface(d, pkgScope, &fp)
		case *ast.FuncDecl:
			inspectExportedFuncDecl(d, pkgScope, fp.GovTokens)
		}
	}
	ast.Inspect(node, func(n ast.Node) bool {
		inspectSurfaceBodyNode(n, pkgScope, govPath, &fp)
		return true
	})
	return fp
}

func inspectSurfaceBodyNode(n ast.Node, pkgScope string, govPath bool, fp *GoSurfaceFingerprint) {
	switch x := n.(type) {
	case *ast.CompositeLit:
		inspectCobraCommandLit(x, fp.CLITokens)
	case *ast.CallExpr:
		inspectSurfaceCallExpr(x, fp)
	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return
		}
		val, ok := extractStringLiteral(x)
		if !ok {
			return
		}
		if pkgScope != "" {
			if sqlTok := extractSQLMutationToken(pkgScope, val); sqlTok != "" {
				fp.GovTokens[sqlTok] = true
			}
		}
		if govPath {
			if tok := extractGovRuleToken(pkgScope, val); tok != "" {
				fp.GovTokens[tok] = true
			}
		}
	}
}

func resolveSurfacePkgScope(relPath, pkgName string) string {
	trimmed := strings.TrimSpace(relPath)
	if trimmed == "" {
		return pkgName
	}
	normDir := filepath.ToSlash(filepath.Dir(filepath.FromSlash(trimmed)))
	normDir = strings.TrimPrefix(normDir, "./")
	if normDir == "." || normDir == "" {
		return pkgName
	}
	return normDir + ":" + pkgName
}

func isGovernanceRuleSourcePath(relPath string) bool {
	norm := filepath.ToSlash(strings.TrimSpace(relPath))
	if norm == "" || strings.HasSuffix(norm, "_test.go") {
		return false
	}
	return strings.HasPrefix(norm, "substrate/src/nomos/modules/verify/") ||
		strings.HasPrefix(norm, "substrate/cli/hookcmd/")
}

func inspectExportedFuncDecl(fn *ast.FuncDecl, pkgScope string, govOut map[string]bool) {
	if fn == nil || fn.Name == nil || !fn.Name.IsExported() {
		return
	}
	declName := fn.Name.Name
	if recv := extractFuncRecvName(fn.Recv); recv != "" {
		declName = recv + "." + declName
	}
	govOut["export_fn:"+pkgScope+"."+declName] = true
	collectCommentGroupIntentRefs(fn.Doc, nil, pkgScope, declName, govOut)
}

func extractFuncRecvName(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	return unwrapIdentName(recv.List[0].Type)
}

func unwrapIdentName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return unwrapIdentName(t.X)
	case *ast.IndexExpr:
		return unwrapIdentName(t.X)
	case *ast.IndexListExpr:
		return unwrapIdentName(t.X)
	default:
		return ""
	}
}

func collectCommentGroupIntentRefs(primary, secondary *ast.CommentGroup, pkgScope, declName string, govOut map[string]bool) {
	for _, group := range []*ast.CommentGroup{primary, secondary} {
		if group == nil {
			continue
		}
		for _, comment := range group.List {
			for _, match := range intentDocTagRe.FindAllStringSubmatch(comment.Text, -1) {
				target := strings.TrimRight(strings.Join(strings.Fields(match[1]), " "), ".,)")
				if target != "" {
					govOut["intent_ref:"+pkgScope+"."+declName+"->"+target] = true
				}
			}
		}
	}
}

func extractGovRuleToken(pkgScope, literalVal string) string {
	if literalVal == "" {
		return ""
	}
	matched := handshakeBannerFieldRe.MatchString(literalVal)
	for _, marker := range govRuleMarkers {
		if matched {
			break
		}
		if strings.Contains(literalVal, marker) {
			matched = true
		}
	}
	if !matched {
		return ""
	}
	normalized := strings.Join(strings.Fields(literalVal), " ")
	if normalized == "" {
		return ""
	}
	const maxPrefix = 48
	if len(normalized) > maxPrefix {
		normalized = normalized[:maxPrefix]
	}
	return "gov_rule:" + pkgScope + ":" + normalized
}
