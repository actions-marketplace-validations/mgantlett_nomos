// Package astdebt implements 11-language Go and polyglot parameter-arity analysis with balanced generics.
package astdebt

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	pyFuncDeclStartRe  = regexp.MustCompile(`(?m)^\s*(?:async\s+)?def\s+([A-Za-z_]\w*)\s*\(`)
	rbFuncDeclStartRe  = regexp.MustCompile(`(?m)^\s*def\s+(?:self\.)?([A-Za-z_]\w*[?!]?)\s*\(`)
	rsFuncDeclStartRe  = regexp.MustCompile(`(?m)^\s*(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?(?:unsafe\s+)?(?:const\s+)?fn\s+([A-Za-z_]\w*)\s*(?:<[^>]*>)?\s*\(`)
	jsFuncDeclStartRe  = regexp.MustCompile(`(?m)(?:\bfunction\s+([A-Za-z_$][\w$]*)\s*(?:<[^>]*>)?\s*\(|\b(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*(?:async\s*)?(?:<[^>]*>)?\s*\()`)
	cLikeMethodStartRe     = regexp.MustCompile(`(?m)^\s*(?:(?:public|private|protected|internal|static|final|virtual|override|async|inline|constexpr|suspend|fun)\s+)*(?:[A-Za-z_][\w:<>,.?\[\]\s*&]*\s+)?([A-Za-z_]\w*)\s*\(`)
	nonFuncControlKeywords = makeWordSet("if for while switch catch return sizeof alignof decltype synchronized lock using when match")
)

// EvaluateParameterArityViolations enforces the maximum parameter count per function to prevent low-cohesion splits.
// @intent software_physics_engine_spec.md tree_sitter_ast_gate_spec.md #FunctionMetric
func EvaluateParameterArityViolations(relPath string, src []byte, maxParams int) []string {
	if maxParams <= 0 || len(src) == 0 {
		return nil
	}
	if strings.HasSuffix(relPath, ExtGo) {
		return scanGoParamViolations(relPath, src, maxParams)
	}
	return scanPolyglotParamViolations(relPath, src, maxParams)
}

// scanGoParamViolations flags Go functions whose declared parameter count exceeds maxParams.
func scanGoParamViolations(relPath string, src []byte, maxParams int) []string {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, relPath, src, 0)
	if err != nil {
		return nil
	}
	var violations []string
	for _, decl := range node.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if count := countGoFuncParams(fn); count > maxParams {
			span := formatFileSpan(relPath, fset.Position(fn.Pos()).Line, fset.Position(fn.End()).Line)
			violations = append(violations, fmt.Sprintf("%s (%s) parameter count %d exceeds max %d -> [1-TURN RECIPE: group >=%d parameters into a %sOptions struct (%s)]",
				span, fn.Name.Name, count, maxParams, (count-maxParams)+1, fn.Name.Name, span))
		}
	}
	return violations
}

// countSignatureParamsAtLine counts parameters for the signature beginning near lines[startIdx].
func countSignatureParamsAtLine(ext, fullText string, lines []string, startIdx int) int {
	offset := 0
	for i := 0; i < startIdx && i < len(lines); i++ {
		offset += len(lines[i]) + 1
	}
	if offset >= len(fullText) {
		return 0
	}
	openIdx := strings.IndexByte(fullText[offset:], '(')
	if openIdx < 0 || openIdx > 240 {
		return 0
	}
	paramRaw, _, ok := extractBalancedParenSpanWithEnd(fullText, offset+openIdx)
	if !ok {
		return 0
	}
	return countTopLevelParams(ext, paramRaw)
}

// scanPolyglotParamViolations flags non-Go functions exceeding maxParams declared arguments.
func scanPolyglotParamViolations(relPath string, src []byte, maxParams int) []string {
	ext := strings.ToLower(filepath.Ext(relPath))
	re := selectParamHeaderRegex(ext)
	if re == nil {
		return nil
	}
	text := string(src)
	var violations []string
	for _, m := range re.FindAllStringSubmatchIndex(text, -1) {
		name := extractFirstCapturedGroup(text, m)
		openParen := m[1] - 1
		if name == "" || nonFuncControlKeywords[name] || openParen < 0 || text[openParen] != '(' {
			continue
		}
		if IsJSFamilyExt(ext) && !isFollowedByFuncBodyOrArrow(text, openParen) {
			continue
		}
		paramRaw, closeIdx, ok := extractBalancedParenSpanWithEnd(text, openParen)
		if !ok {
			continue
		}
		if count := countTopLevelParams(ext, paramRaw); count > maxParams {
			startLine, endLine := strings.Count(text[:m[0]], "\n")+1, strings.Count(text[:closeIdx], "\n")+1
			if endLine <= startLine {
				endLine = startLine + 1
			}
			span := formatFileSpan(relPath, startLine, endLine)
			violations = append(violations, fmt.Sprintf("%s (%s) parameter count %d exceeds max %d -> [1-TURN RECIPE: group >=%d parameters into a %sOptions object/dataclass (%s)]",
				span, name, count, maxParams, (count-maxParams)+1, name, span))
		}
	}
	return violations
}

// selectParamHeaderRegex maps source extension to its function signature header regex.
func selectParamHeaderRegex(ext string) *regexp.Regexp {
	if IsJSFamilyExt(ext) {
		return jsFuncDeclStartRe
	}
	switch ext {
	case ExtPy:
		return pyFuncDeclStartRe
	case ExtRB, ExtRake:
		return rbFuncDeclStartRe
	case ExtRS:
		return rsFuncDeclStartRe
	case ExtC, ExtH, ExtCPP, ExtCC, ExtCXX, ExtHPP, ExtJava, ExtKT, ExtKTS, ExtCS:
		return cLikeMethodStartRe
	default:
		return nil
	}
}

// extractFirstCapturedGroup returns the first matched subexpression slice in match indices.
func extractFirstCapturedGroup(text string, m []int) string {
	for i := 2; i+1 < len(m); i += 2 {
		if m[i] >= 0 && m[i+1] >= 0 {
			return text[m[i]:m[i+1]]
		}
	}
	return ""
}

// isFollowedByFuncBodyOrArrow checks whether a JS/TS parameter list precedes '{' or '=>'.
func isFollowedByFuncBodyOrArrow(text string, openParen int) bool {
	_, closeIdx, ok := extractBalancedParenSpanWithEnd(text, openParen)
	if !ok || closeIdx+1 >= len(text) {
		return false
	}
	rest := strings.TrimSpace(text[closeIdx+1:])
	if strings.HasPrefix(rest, "{") || strings.HasPrefix(rest, "=>") {
		return true
	}
	if strings.HasPrefix(rest, ":") {
		if brace := strings.Index(rest, "{"); brace >= 0 && brace < 120 {
			return true
		}
		if arrow := strings.Index(rest, "=>"); arrow >= 0 && arrow < 120 {
			return true
		}
	}
	return false
}

// extractBalancedParenSpanWithEnd returns the balanced (...) substring, closing index, and validity.
func extractBalancedParenSpanWithEnd(text string, openParen int) (string, int, bool) {
	if openParen < 0 || openParen >= len(text) || text[openParen] != '(' {
		return "", -1, false
	}
	depth, quote, escaped := 0, byte(0), false
	for i := openParen; i < len(text); i++ {
		if escaped {
			escaped = false
			continue
		}
		ch := text[i]
		if quote != 0 {
			quote, escaped = stepSingleQuoteByte(ch, quote)
			continue
		}
		if ch == '"' || ch == '\'' || ch == '`' {
			quote = ch
		} else if ch == '(' {
			depth++
		} else if ch == ')' {
			depth--
			if depth == 0 {
				return text[openParen+1 : i], i, true
			}
		}
	}
	return "", -1, false
}

// countTopLevelParams splits a signature parameter list and excludes language implicit receivers.
func countTopLevelParams(ext, paramRaw string) int {
	trimmed := strings.TrimSpace(paramRaw)
	if trimmed == "" || ((ext == ExtC || ext == ExtH || ext == ExtCPP) && trimmed == "void") {
		return 0
	}
	count := 0
	for _, p := range splitTopLevelCommaParams(trimmed) {
		tok := cleanParamToken(p)
		if tok != "" && !isImplicitReceiverOrMarkerParam(ext, tok) {
			count++
		}
	}
	return count
}

// splitTopLevelCommaParams splits comma-separated parameters outside strings and nested delimiters.
func splitTopLevelCommaParams(s string) []string {
	var parts []string
	start, depth, quote, escaped := 0, 0, byte(0), false
	for i := 0; i < len(s); i++ {
		if escaped {
			escaped = false
			continue
		}
		ch := s[i]
		if quote != 0 {
			quote, escaped = stepSingleQuoteByte(ch, quote)
			continue
		}
		if ch == '"' || ch == '\'' || ch == '`' {
			quote = ch
			continue
		}
		prev := byte(0)
		if i > 0 {
			prev = s[i-1]
		}
		depth = updateNestedParamDepth(depth, ch, prev)
		if ch == ',' && depth == 0 {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}

// updateNestedParamDepth tracks (), <>, [], and {} nesting levels across parameter defaults/generics.
func updateNestedParamDepth(depth int, ch, prev byte) int {
	switch ch {
	case '(', '<', '[', '{':
		return depth + 1
	case ')', ']', '}':
		if depth > 0 {
			return depth - 1
		}
	case '>':
		if depth > 0 && prev != '=' && prev != '-' {
			return depth - 1
		}
	}
	return depth
}

// cleanParamToken strips single-line comments and joins multiline parameter declarations.
func cleanParamToken(raw string) string {
	var kept []string
	for _, l := range strings.Split(raw, "\n") {
		t := strings.TrimSpace(l)
		if idx := strings.Index(t, "//"); idx >= 0 {
			t = strings.TrimSpace(t[:idx])
		}
		if idx := strings.Index(t, "#"); idx >= 0 {
			t = strings.TrimSpace(t[:idx])
		}
		if t != "" {
			kept = append(kept, t)
		}
	}
	return strings.TrimSpace(strings.Join(kept, " "))
}

// isImplicitReceiverOrMarkerParam excludes Python self/cls/*/ markers and Rust self receivers.
func isImplicitReceiverOrMarkerParam(ext, tok string) bool {
	if ext == ExtPy {
		name := strings.TrimSpace(strings.SplitN(strings.SplitN(tok, ":", 2)[0], "=", 2)[0])
		return name == "self" || name == "cls" || name == "*" || name == "/"
	}
	if ext == ExtRS {
		return tok == "self" || tok == "mut self" || tok == "&self" || (strings.HasPrefix(tok, "&") && strings.HasSuffix(tok, " self"))
	}
	return false
}
