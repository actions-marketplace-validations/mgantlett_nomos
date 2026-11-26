// Package sensorsummary implements diff-ratcheted suppression directive and type-escape detection (Law 2 & Law 7).
// @intent nomos_intent_traceability_spec.md
package sensorsummary

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/impact"
)

var (
	goNolintRe        = regexp.MustCompile(`//\s*nolint(?:\b|:)`)
	jsSuppressRe      = regexp.MustCompile(`(?://|/\*)\s*(?:eslint-disable(?:-next-line|-line)?|@ts-ignore|@ts-expect-error)\b`)
	tsAnyAnnotationRe = regexp.MustCompile(`:\s*any\b`)
	tsAsAnyRe         = regexp.MustCompile(`\bas\s+any\b`)
	pySuppressRe      = regexp.MustCompile(`#\s*(?:noqa\b|type:\s*ignore\b|pylint:\s*disable)`)
	rustAllowAttrRe   = regexp.MustCompile(`^\s*#!?\[allow\(`)
)

// ScanSuppressionEscapes scans non-test source bytes for linter suppression pragmas and TS type-escape hatches.
// Intended to run under diff-ratchet baseline subtraction so legacy occurrences remain grandfathered.
// @intent nomos_intent_traceability_spec.md #AntiShimDTO
func ScanSuppressionEscapes(relPath string, src []byte) []string {
	if len(src) == 0 || isExemptSuppressionFile(relPath) {
		return nil
	}
	ext := strings.ToLower(filepath.Ext(relPath))
	if !isSuppressionTargetExt(ext) {
		return nil
	}
	var violations []string
	for idx, line := range strings.Split(string(src), "\n") {
		if label := detectLineSuppressionEscape(ext, line); label != "" {
			violations = append(violations, fmt.Sprintf("%s:%d: suppression_escape (%s)", filepath.ToSlash(relPath), idx+1, label))
		}
	}
	return violations
}

func isExemptSuppressionFile(relPath string) bool {
	base := filepath.Base(relPath)
	if impact.IsVerificationSuiteFile(base) {
		return true
	}
	slash := strings.ToLower(filepath.ToSlash(relPath))
	return strings.Contains(slash, "/testdata/") || strings.Contains(slash, "/__tests__/") || strings.Contains(slash, "/fixtures/")
}

func isSuppressionTargetExt(ext string) bool {
	switch ext {
	case ".go", ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs", ".svelte", ".py", ".rs":
		return true
	default:
		return false
	}
}

func detectLineSuppressionEscape(ext, line string) string {
	switch ext {
	case ".go":
		return detectGoSuppression(line)
	case ".ts", ".tsx", ".mts", ".cts", ".svelte":
		return detectTSOrJSSuppression(line, true)
	case ".js", ".jsx", ".mjs", ".cjs":
		return detectTSOrJSSuppression(line, false)
	case ".py":
		return detectPythonSuppression(line)
	case ".rs":
		if rustAllowAttrRe.MatchString(line) {
			return "rust #[allow(...)]"
		}
	}
	return ""
}

func detectGoSuppression(line string) string {
	_, comment := splitSlashCodeAndComment(line)
	if comment != "" && goNolintRe.MatchString(comment) {
		return "go //nolint"
	}
	return ""
}

func detectTSOrJSSuppression(line string, checkAny bool) string {
	codeOnly, comment := splitSlashCodeAndComment(line)
	if comment != "" && jsSuppressRe.MatchString(comment) {
		return "js/ts lint or type suppression pragma"
	}
	if checkAny && (tsAsAnyRe.MatchString(codeOnly) || tsAnyAnnotationRe.MatchString(codeOnly)) {
		return "typescript any type-escape"
	}
	return ""
}

func detectPythonSuppression(line string) string {
	comment := extractHashCommentOutsideStrings(line)
	if comment != "" && pySuppressRe.MatchString(comment) {
		return "python lint or type suppression pragma"
	}
	return ""
}

func splitSlashCodeAndComment(line string) (string, string) {
	var b strings.Builder
	var quote byte
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if quote != 0 {
			if ch == '\\' && quote != '`' {
				i++
			} else if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '"' || ch == '\'' || ch == '`' {
			quote = ch
			b.WriteByte(' ')
			continue
		}
		if ch == '/' && i+1 < len(line) && (line[i+1] == '/' || line[i+1] == '*') {
			return b.String(), line[i:]
		}
		b.WriteByte(ch)
	}
	return b.String(), ""
}

func extractHashCommentOutsideStrings(line string) string {
	var quote byte
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if quote != 0 {
			if ch == '\\' {
				i++
			} else if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '"' || ch == '\'' {
			quote = ch
			continue
		}
		if ch == '#' {
			return line[i:]
		}
	}
	return ""
}
