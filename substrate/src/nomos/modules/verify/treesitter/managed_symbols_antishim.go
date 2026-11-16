package treesitter

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	javaPublicTypeRe   = regexp.MustCompile(`(?m)^\s*(?:(?:public|abstract|final|static|sealed|non-sealed)\s+)*public\s+(?:(?:abstract|final|static|sealed|non-sealed)\s+)*(?:class|interface|enum|record|@interface)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	csharpPublicTypeRe = regexp.MustCompile(`(?m)^\s*(?:(?:public|abstract|sealed|static|partial|readonly|ref)\s+)*public\s+(?:(?:abstract|sealed|static|partial|readonly|ref)\s+)*(?:class|interface|enum|record(?:\s+(?:class|struct))?|struct)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	csharpExprMethodRe = regexp.MustCompile(`(?m)^\s*public\s+(?:(?:static|virtual|override|async|sealed|new)\s+)*(?:[A-Za-z_][A-Za-z0-9_.<>,\[\]?]*\s+)+([A-Za-z_][A-Za-z0-9_]*)\s*(?:<[^>]+>)?\s*\([^)]*\)\s*=>`)
	tsExportSymbolRe   = regexp.MustCompile(`(?m)^\s*export\s+(?:default\s+)?(?:async\s+)?(?:function\*?|class|interface|type|const|let|enum)\s+([A-Za-z_$][A-Za-z0-9_$]*)`)
	rustExportSymbolRe = regexp.MustCompile(`(?m)^\s*pub(?:\([^)]+\))?\s+(?:async\s+)?(?:unsafe\s+)?(?:fn|struct|enum|trait|type)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	pyExportSymbolRe   = regexp.MustCompile(`(?m)^(?:async\s+)?(?:def|class)\s+([A-Za-z][A-Za-z0-9_]*)`)
	rbExportSymbolRe   = regexp.MustCompile(`(?m)^\s*(?:def|class|module)\s+([A-Za-z_][A-Za-z0-9_!?=]*)`)
	identWordRe        = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
)

// ExtractExportedSymbols extracts sorted public/exported type and function symbols for a source language (@intent tree_sitter_ast_gate_spec.md #WazeroTreeSitterEngine).
func ExtractExportedSymbols(lang string, content []byte) []string {
	switch lang {
	case langJava, langCSharp:
		return extractManagedExportedSymbols(lang, content)
	case "go":
		return extractGoExportedSymbolNames(content)
	default:
		return extractRegexExportedSymbols(lang, content)
	}
}

func extractManagedExportedSymbols(lang string, content []byte) []string {
	cleaned := stripCLikeLiterals(string(content), true)
	seen := make(map[string]bool)
	typeRe, methodRe := javaPublicTypeRe, javaMethodRegex
	if lang == langCSharp {
		typeRe, methodRe = csharpPublicTypeRe, csharpMethodRe
	}
	addRegexSubmatches(seen, typeRe, cleaned)
	lines := strings.Split(cleaned, "\n")
	for _, sp := range collectBraceFuncSpans(lines, methodRe) {
		hdr := strings.Join(lines[sp.startIdx:sp.headerEnd+1], " ")
		if hasWordToken(hdr, "public") && !tsControlKeywords[sp.name] {
			seen[sp.name] = true
		}
	}
	if lang == langCSharp {
		addRegexSubmatches(seen, csharpExprMethodRe, cleaned)
	}
	return sortedSymbolKeys(seen)
}

func addRegexSubmatches(seen map[string]bool, re *regexp.Regexp, src string) {
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		if len(m) > 1 && m[1] != "" && !tsControlKeywords[m[1]] {
			seen[m[1]] = true
		}
	}
}

func extractGoExportedSymbolNames(content []byte) []string {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "scan.go", content, 0)
	if err != nil || node == nil {
		return []string{}
	}
	seen := make(map[string]bool)
	for _, decl := range node.Decls {
		collectGoDeclExportedNames(decl, seen)
	}
	return sortedSymbolKeys(seen)
}

func collectGoDeclExportedNames(decl ast.Decl, seen map[string]bool) {
	if fn, ok := decl.(*ast.FuncDecl); ok {
		if fn.Name != nil && fn.Name.IsExported() {
			seen[fn.Name.Name] = true
		}
		return
	}
	gd, ok := decl.(*ast.GenDecl)
	if !ok {
		return
	}
	for _, sp := range gd.Specs {
		if ts, okTS := sp.(*ast.TypeSpec); okTS && ts.Name != nil && ts.Name.IsExported() {
			seen[ts.Name.Name] = true
		}
	}
}

func extractRegexExportedSymbols(lang string, content []byte) []string {
	var re *regexp.Regexp
	cleaned := string(content)
	switch lang {
	case "typescript", "svelte":
		cleaned = stripCLikeLiterals(cleaned, true)
		re = tsExportSymbolRe
	case "rust":
		cleaned = stripCLikeLiterals(cleaned, false)
		re = rustExportSymbolRe
	case "python":
		cleaned = stripPythonLiterals(cleaned)
		re = pyExportSymbolRe
	case "ruby":
		cleaned = stripRubyLiterals(cleaned)
		re = rbExportSymbolRe
	default:
		return []string{}
	}
	seen := make(map[string]bool)
	addRegexSubmatches(seen, re, cleaned)
	return sortedSymbolKeys(seen)
}

func sortedSymbolKeys(seen map[string]bool) []string {
	if len(seen) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func isManagedConstantReturnStub(lang, filePath, header string, cleanBody, origBody []string) bool {
	if isManagedTestFilePath(filePath) {
		return false
	}
	params := extractManagedParamNames(header)
	if len(params) == 0 {
		return false
	}
	cleanInner, ok := extractBraceInner(cleanBody)
	if !ok || countManagedSemicolons(lang, cleanInner) != 1 || hasManagedControlFlow(cleanInner) {
		return false
	}
	expr, isRet := extractSingleReturnExpr(cleanInner)
	if !isRet || referencesAnyParam(expr, params) {
		return false
	}
	if expr == "" {
		origInner, origOK := extractBraceInner(origBody)
		return origOK && strings.Contains(origInner, `""`)
	}
	return isConstantStubReturnExpr(expr)
}

func isManagedTestFilePath(filePath string) bool {
	lower := strings.ToLower(filepath.Base(filePath))
	for _, sfx := range []string{"test.java", "tests.java", "test.cs", "tests.cs"} {
		if strings.HasSuffix(lower, sfx) {
			return true
		}
	}
	return false
}

func extractManagedParamNames(header string) []string {
	openIdx := strings.IndexByte(header, '(')
	closeIdx := findMatchingParen(header, openIdx)
	if openIdx < 0 || closeIdx <= openIdx+1 {
		return nil
	}
	raw := strings.TrimSpace(header[openIdx+1 : closeIdx])
	if raw == "" {
		return nil
	}
	var names []string
	for _, part := range splitTopLevelCommas(raw) {
		if eqIdx := strings.IndexByte(part, '='); eqIdx >= 0 {
			part = part[:eqIdx]
		}
		toks := identWordRe.FindAllString(part, -1)
		if len(toks) > 0 {
			names = append(names, toks[len(toks)-1])
		}
	}
	return names
}

func findMatchingParen(s string, openIdx int) int {
	if openIdx < 0 || openIdx >= len(s) {
		return -1
	}
	depth := 0
	for i := openIdx; i < len(s); i++ {
		if s[i] == '(' {
			depth++
		} else if s[i] == ')' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func splitTopLevelCommas(raw string) []string {
	var parts []string
	var depths [3]int
	start := 0
	for i := 0; i < len(raw); i++ {
		if raw[i] == ',' && depths == [3]int{0, 0, 0} {
			parts = append(parts, raw[start:i])
			start = i + 1
			continue
		}
		stepTopLevelDepth(raw[i], &depths)
	}
	return append(parts, raw[start:])
}

func stepTopLevelDepth(ch byte, depths *[3]int) {
	switch ch {
	case '<':
		depths[0]++
	case '>':
		if depths[0] > 0 {
			depths[0]--
		}
	case '(':
		depths[1]++
	case ')':
		if depths[1] > 0 {
			depths[1]--
		}
	case '[':
		depths[2]++
	case ']':
		if depths[2] > 0 {
			depths[2]--
		}
	}
}

func extractBraceInner(lines []string) (string, bool) {
	joined := strings.Join(lines, " ")
	first := strings.IndexByte(joined, '{')
	last := strings.LastIndexByte(joined, '}')
	if first < 0 || last <= first {
		return "", false
	}
	return strings.TrimSpace(joined[first+1 : last]), true
}

func countManagedSemicolons(lang, cleanInner string) int {
	if eng := peekDefaultEngine(); eng != nil {
		if cnt, handled := eng.callManagedByteScanner(lang, "count_semicolons", cleanInner); handled {
			return int(cnt)
		}
	}
	return strings.Count(cleanInner, ";")
}

func hasManagedControlFlow(cleanInner string) bool {
	for _, kw := range []string{"if", "for", "foreach", "while", "switch", "try", "catch", "throw"} {
		if hasWordToken(cleanInner, kw) {
			return true
		}
	}
	return false
}

func extractSingleReturnExpr(cleanInner string) (string, bool) {
	trimmed := strings.TrimSpace(cleanInner)
	if !strings.HasSuffix(trimmed, ";") || !strings.HasPrefix(trimmed, "return") {
		return "", false
	}
	after := trimmed[len("return"):]
	if len(after) == 0 || (after[0] != ' ' && after[0] != '\t' && after[0] != '(' && after[0] != ';') {
		return "", false
	}
	expr := strings.TrimSpace(strings.TrimSuffix(after, ";"))
	if strings.HasPrefix(expr, "(") && strings.HasSuffix(expr, ")") {
		expr = strings.TrimSpace(expr[1 : len(expr)-1])
	}
	return expr, true
}

func referencesAnyParam(expr string, params []string) bool {
	for _, p := range params {
		if hasWordToken(expr, p) {
			return true
		}
	}
	return false
}

func hasWordToken(src, word string) bool {
	for _, tok := range identWordRe.FindAllString(src, -1) {
		if tok == word {
			return true
		}
	}
	return false
}

func isConstantStubReturnExpr(expr string) bool {
	compact := strings.ReplaceAll(strings.TrimSpace(expr), " ", "")
	switch compact {
	case "null", "true", "false", "0", "0L", "0l", "0.0", "0.0f", "0f", "0d", "-1",
		"default", "Optional.empty()", "Collections.emptyList()", "Collections.emptyMap()",
		"Collections.emptySet()", "List.of()", "Map.of()", "Set.of()":
		return true
	}
	return (strings.HasPrefix(compact, "default(") && strings.HasSuffix(compact, ")")) ||
		(strings.HasPrefix(compact, "Array.Empty<") && strings.HasSuffix(compact, ">()")) ||
		strings.HasPrefix(compact, "Task.FromResult(null") ||
		strings.HasPrefix(compact, "Task.FromResult(false") ||
		strings.HasPrefix(compact, "Task.FromResult(true") ||
		strings.HasPrefix(compact, "Task.FromResult(0)")
}
