// Package astdebt extracts scoped Go symbol, wire-schema, and rule-literal governance tokens.
package astdebt

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"
)

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
		if !govPath || x.Kind != token.STRING {
			return
		}
		if val, ok := extractStringLiteral(x); ok {
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
