// Package wiredrift provides per-language AST and lexical extractors for boundary wire drift sensors.
package wiredrift

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

func collectDeclaredWireKeys(rel string, data []byte, wireKeys, quotedSnake map[string]bool) {
	ext := strings.ToLower(filepath.Ext(rel))
	text := string(data)
	for _, m := range quotedSnakeLiteralRe.FindAllStringSubmatch(text, -1) {
		quotedSnake[m[1]] = true
	}
	if ext == ".go" {
		collectGoJSONTags(data, wireKeys)
		return
	}
	if ext == ".ts" || ext == ".tsx" || ext == ".svelte" || ext == ".py" {
		for _, m := range snakeFieldDeclRe.FindAllStringSubmatch(text, -1) {
			wireKeys[m[1]] = true
		}
	}
}

func collectGoJSONTags(data []byte, wireKeys map[string]bool) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", data, 0)
	if err != nil {
		return
	}
	ast.Inspect(file, func(n ast.Node) bool {
		st, ok := n.(*ast.StructType)
		if !ok || st.Fields == nil {
			return true
		}
		for _, field := range st.Fields.List {
			if key := parseFieldJSONKey(field); key != "" {
				wireKeys[key] = true
			}
		}
		return true
	})
}

func parseFieldJSONKey(field *ast.Field) string {
	if field == nil || field.Tag == nil {
		return ""
	}
	unquoted, err := strconv.Unquote(field.Tag.Value)
	if err != nil {
		return ""
	}
	tag := reflect.StructTag(unquoted).Get("json")
	name := strings.TrimSpace(strings.Split(tag, ",")[0])
	if name == "" || name == "-" {
		return ""
	}
	return name
}

func scanFileUntypedDTOEscapes(rel string, data []byte) ([]BoundaryDriftFinding, map[string]bool) {
	ext := strings.ToLower(filepath.Ext(rel))
	fields := make(map[string]bool)
	switch ext {
	case ".go":
		return scanGoUntypedDTOEscapes(rel, data, fields), fields
	case ".ts", ".tsx", ".svelte":
		return scanTSUntypedDTOEscapes(rel, string(data), fields), fields
	case ".py":
		return scanPyUntypedDTOEscapes(rel, string(data), fields), fields
	default:
		return nil, fields
	}
}

func scanGoUntypedDTOEscapes(rel string, data []byte, fields map[string]bool) []BoundaryDriftFinding {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, data, 0)
	if err != nil {
		return nil
	}
	var out []BoundaryDriftFinding
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name == nil || !ts.Name.IsExported() || !hasContractTypeSuffix(ts.Name.Name) {
				continue
			}
			out = append(out, inspectGoStructUntypedFields(fset, rel, ts.Name.Name, ts.Type, fields)...)
		}
	}
	return out
}

func inspectGoStructUntypedFields(fset *token.FileSet, rel, typeName string, expr ast.Expr, fields map[string]bool) []BoundaryDriftFinding {
	st, ok := expr.(*ast.StructType)
	if !ok || st.Fields == nil {
		return nil
	}
	var out []BoundaryDriftFinding
	for _, f := range st.Fields.List {
		jsonKey := parseFieldJSONKey(f)
		if jsonKey == "" || !isGoUntypedStringMap(f.Type) || isExemptOpenSchemaField(jsonKey) {
			continue
		}
		fieldIdent := jsonKey
		if len(f.Names) > 0 && f.Names[0] != nil {
			if isExemptOpenSchemaField(f.Names[0].Name) {
				continue
			}
			fieldIdent = f.Names[0].Name
		}
		fields[jsonKey] = true
		pos := fset.Position(f.Pos())
		out = append(out, BoundaryDriftFinding{
			File:   rel,
			Line:   pos.Line,
			Kind:   KindUntypedBoundaryRead,
			Symbol: typeName + "." + fieldIdent,
			Detail: fmt.Sprintf("contract %s.%s exposes untyped map[string]any wire field %q", typeName, fieldIdent, jsonKey),
		})
	}
	return out
}

func isGoUntypedStringMap(expr ast.Expr) bool {
	if arr, ok := expr.(*ast.ArrayType); ok {
		return isGoUntypedStringMap(arr.Elt)
	}
	mp, ok := expr.(*ast.MapType)
	if !ok {
		return false
	}
	keyIdent, ok := mp.Key.(*ast.Ident)
	if !ok || keyIdent.Name != "string" {
		return false
	}
	if valIdent, ok := mp.Value.(*ast.Ident); ok && valIdent.Name == "any" {
		return true
	}
	iface, ok := mp.Value.(*ast.InterfaceType)
	return ok && (iface.Methods == nil || len(iface.Methods.List) == 0)
}

func scanTSUntypedDTOEscapes(rel, text string, fields map[string]bool) []BoundaryDriftFinding {
	var out []BoundaryDriftFinding
	matches := tsExportContractBlockRe.FindAllStringSubmatchIndex(text, -1)
	for _, idx := range matches {
		typeName, body, bodyStart := extractTSBlockMatch(text, idx)
		if !hasContractTypeSuffix(typeName) {
			continue
		}
		baseLine := 1 + strings.Count(text[:bodyStart], "\n")
		for offset, line := range strings.Split(body, "\n") {
			sub := tsUntypedRecordFieldRe.FindStringSubmatch(line)
			if len(sub) < 2 || isExemptOpenSchemaField(sub[1]) {
				continue
			}
			fields[sub[1]] = true
			out = append(out, BoundaryDriftFinding{
				File:   rel,
				Line:   baseLine + offset,
				Kind:   KindUntypedBoundaryRead,
				Symbol: typeName + "." + sub[1],
				Detail: fmt.Sprintf("contract %s.%s uses untyped Record<string, any|unknown> escape hatch", typeName, sub[1]),
			})
		}
	}
	return out
}

func extractTSBlockMatch(text string, idx []int) (string, string, int) {
	if idx[2] >= 0 && idx[4] >= 0 {
		return text[idx[2]:idx[3]], text[idx[4]:idx[5]], idx[4]
	}
	return text[idx[6]:idx[7]], text[idx[8]:idx[9]], idx[8]
}

func scanPyUntypedDTOEscapes(rel, text string, fields map[string]bool) []BoundaryDriftFinding {
	var out []BoundaryDriftFinding
	activeClass := ""
	for i, line := range strings.Split(text, "\n") {
		if m := pyClassHeaderRe.FindStringSubmatch(line); len(m) == 2 {
			if hasContractTypeSuffix(m[1]) {
				activeClass = m[1]
			} else {
				activeClass = ""
			}
			continue
		}
		if activeClass == "" {
			continue
		}
		if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			activeClass = ""
			continue
		}
		if sub := pyUntypedDictFieldRe.FindStringSubmatch(line); len(sub) == 2 && !isExemptOpenSchemaField(sub[1]) {
			fields[sub[1]] = true
			out = append(out, BoundaryDriftFinding{
				File:   rel,
				Line:   i + 1,
				Kind:   KindUntypedBoundaryRead,
				Symbol: activeClass + "." + sub[1],
				Detail: fmt.Sprintf("contract %s.%s uses untyped dict[str, Any] escape hatch", activeClass, sub[1]),
			})
		}
	}
	return out
}

func isPhantomWireConsumerExt(ext string) bool {
	switch ext {
	case ".ts", ".tsx", ".js", ".svelte", ".py":
		return true
	default:
		return false
	}
}

func scanFilePhantomWireKeys(rel string, data []byte, wireKeys, quotedSnake, untypedDTOFields map[string]bool) []BoundaryDriftFinding {
	if !isPhantomWireConsumerExt(strings.ToLower(filepath.Ext(rel))) {
		return nil
	}
	text := string(data)
	vars := collectUntypedConsumerVars(text, untypedDTOFields)
	if len(vars) == 0 {
		return nil
	}
	var out []BoundaryDriftFinding
	seen := make(map[string]bool)
	for _, varName := range vars {
		out = append(out, scanVarPhantomWireKeys(rel, text, varName, wireKeys, quotedSnake, seen)...)
	}
	return out
}

func scanVarPhantomWireKeys(rel, text, varName string, wireKeys, quotedSnake, seen map[string]bool) []BoundaryDriftFinding {
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(varName) + `\.([a-z][a-z0-9]*_[a-z0-9_]+)\b`)
	var out []BoundaryDriftFinding
	for _, idx := range re.FindAllStringSubmatchIndex(text, -1) {
		key := text[idx[2]:idx[3]]
		if wireKeys[key] || quotedSnake[key] {
			continue
		}
		closest := findClosestStemWireKey(key, wireKeys)
		if closest == "" {
			continue
		}
		line := 1 + strings.Count(text[:idx[0]], "\n")
		dedup := fmt.Sprintf("%d:%s.%s", line, varName, key)
		if seen[dedup] {
			continue
		}
		seen[dedup] = true
		out = append(out, BoundaryDriftFinding{
			File:       rel,
			Line:       line,
			Kind:       KindPhantomWireKey,
			Symbol:     varName + "." + key,
			ClosestTag: closest,
			Detail:     fmt.Sprintf("untyped read %s.%s matches 0 declared wire keys", varName, key),
		})
	}
	return out
}

func collectUntypedConsumerVars(text string, untypedDTOFields map[string]bool) []string {
	set := make(map[string]bool)
	for _, m := range untypedParamDeclRe.FindAllStringSubmatch(text, -1) {
		set[m[1]] = true
	}
	for _, m := range untypedCastAssignRe.FindAllStringSubmatch(text, -1) {
		set[m[1]] = true
	}
	for field := range untypedDTOFields {
		assignRe := regexp.MustCompile(`\b(?:const|let|var)\s+([A-Za-z_]\w*)\s*=\s*[A-Za-z_]\w*\.` + regexp.QuoteMeta(field) + `\b`)
		for _, m := range assignRe.FindAllStringSubmatch(text, -1) {
			set[m[1]] = true
		}
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func findClosestStemWireKey(candidate string, wireKeys map[string]bool) string {
	if len(candidate) >= 6 {
		prefix := candidate + "_"
		var extMatches []string
		for wk := range wireKeys {
			if strings.HasPrefix(wk, prefix) {
				extMatches = append(extMatches, wk)
			}
		}
		if len(extMatches) > 0 {
			sort.Strings(extMatches)
			return extMatches[0]
		}
	}
	parts := strings.Split(candidate, "_")
	for i := 1; i < len(parts); i++ {
		stem := strings.Join(parts[i:], "_")
		if len(stem) >= 6 && wireKeys[stem] {
			return stem
		}
	}
	return ""
}
