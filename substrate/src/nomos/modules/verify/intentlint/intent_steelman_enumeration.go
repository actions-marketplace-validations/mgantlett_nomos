// Package intentlint validates cardinal enumeration parity across Markdown lists and tables (Law 2 & Law 4).
package intentlint

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	inlineCodeSpanPattern = regexp.MustCompile("`[^`]*`")
	markdownLinkPattern   = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	leadingListMarkerRe   = regexp.MustCompile(`^\s*(?:[-*+]|\d+\.)\s+`)
	listItemLinePattern   = regexp.MustCompile(`^(\s*)([-*+]|\d+\.)\s+`)
	tableSepRowPattern    = regexp.MustCompile(`^\|[\s:\-|]+\|$`)
	clauseSplitPattern    = regexp.MustCompile(`(?:[.!?]| - )\s+`)

	enumerationLeadPattern = regexp.MustCompile(
		`(?i)\b(two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|[2-9]|1[0-2])` +
			`(?:-(?:pass|rung|layer|step|stage|phase|tier|part|point))?\s+` +
			`((?:[a-z][a-z\-]*\s+){0,3}([a-z]{3,}s))` +
			`(?:\s+(?:in|across|for|under|on|as|within|of|from|with|to)\b[^:()]*)?:\s*$`,
	)

	cardinalWordValues = map[string]int{
		"two": 2, "three": 3, "four": 4, "five": 5, "six": 6,
		"seven": 7, "eight": 8, "nine": 9, "ten": 10, "eleven": 11, "twelve": 12,
	}

	excludedMeasurementNouns = map[string]bool{
		"seconds": true, "minutes": true, "hours": true, "days": true, "weeks": true,
		"months": true, "years": true, "bytes": true, "lines": true, "tokens": true,
		"turns": true, "runs": true, "trials": true, "episodes": true, "languages": true,
		"checkpoints": true, "milliseconds": true, "parameters": true, "characters": true,
		"commits": true, "files": true, "repos": true, "repositories": true, "containers": true,
		"traces": true,
	}
)

type enumerationClaim struct {
	expected int
	phrase   string
}

// validateMarkdownEnumerations verifies that '<N> <plural-noun>:' lead-ins match following list or table counts.
func validateMarkdownEnumerations(scan specBodyScan) string {
	for i, raw := range scan.lines {
		if scan.fencedLines[i] {
			continue
		}
		claim, ok := extractEnumerationClaim(raw)
		if !ok {
			continue
		}
		nextIdx := nextNonEmptyUnfencedLine(scan, i+1)
		if nextIdx < 0 {
			continue
		}
		if errMsg := verifyBlockAgainstClaim(scan, i+1, nextIdx, claim); errMsg != "" {
			return errMsg
		}
	}
	return ""
}

// extractEnumerationClaim parses a final-clause '<N> <plural-noun>:' promise from an unfenced prose line.
func extractEnumerationClaim(raw string) (enumerationClaim, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || !strings.HasSuffix(trimmed, ":") || strings.Contains(raw, "nomos:count-ok") {
		return enumerationClaim{}, false
	}
	if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "|") || strings.HasPrefix(trimmed, ">") {
		return enumerationClaim{}, false
	}
	cleaned := leadingListMarkerRe.ReplaceAllString(raw, "")
	cleaned = inlineCodeSpanPattern.ReplaceAllString(cleaned, "code")
	cleaned = markdownLinkPattern.ReplaceAllString(cleaned, "$1")
	cleaned = strings.ReplaceAll(strings.ReplaceAll(cleaned, "**", ""), "*", "")
	parts := clauseSplitPattern.Split(strings.TrimSpace(cleaned), -1)
	clause := parts[len(parts)-1]

	m := enumerationLeadPattern.FindStringSubmatch(clause)
	if len(m) != 4 || excludedMeasurementNouns[strings.ToLower(m[3])] {
		return enumerationClaim{}, false
	}
	tok := strings.ToLower(m[1])
	val, ok := cardinalWordValues[tok]
	if !ok {
		parsed, err := strconv.Atoi(tok)
		if err != nil {
			return enumerationClaim{}, false
		}
		val = parsed
	}
	return enumerationClaim{expected: val, phrase: m[1] + " " + m[2]}, val >= 2
}

// nextNonEmptyUnfencedLine returns the next non-blank, non-fenced line index or -1.
func nextNonEmptyUnfencedLine(scan specBodyScan, start int) int {
	for j := start; j < len(scan.lines); j++ {
		if scan.fencedLines[j] {
			return -1
		}
		if strings.TrimSpace(scan.lines[j]) != "" {
			return j
		}
	}
	return -1
}

// verifyBlockAgainstClaim checks either a Markdown list or a Markdown table immediately below a claim line.
func verifyBlockAgainstClaim(scan specBodyScan, claimLineNum, blockIdx int, claim enumerationClaim) string {
	first := scan.lines[blockIdx]
	if bm := listItemLinePattern.FindStringSubmatch(first); len(bm) == 3 {
		return verifyListAgainstClaim(scan, claimLineNum, blockIdx, len(bm[1]), bm[2], claim)
	}
	if strings.HasPrefix(strings.TrimSpace(first), "|") {
		return verifyTableAgainstClaim(scan, claimLineNum, blockIdx, claim)
	}
	return ""
}

// verifyListAgainstClaim checks that a contiguous numbered or bullet list matches claim.expected.
func verifyListAgainstClaim(scan specBodyScan, claimLineNum, blockIdx, baseIndent int, marker string, claim enumerationClaim) string {
	isNumbered := marker[0] >= '0' && marker[0] <= '9'
	count, grouped := countContiguousListItems(scan, blockIdx, baseIndent, isNumbered)
	if count < 2 || count == claim.expected {
		return ""
	}
	if !isNumbered && count < claim.expected && grouped {
		return ""
	}
	return fmt.Sprintf("enumeration count mismatch on body line %d: prose states %q (%d), but following list has %d items (Law 2)", claimLineNum, claim.phrase, claim.expected, count)
}

// verifyTableAgainstClaim checks that a Markdown table's row or comparison-column count matches claim.expected.
func verifyTableAgainstClaim(scan specBodyScan, claimLineNum, blockIdx int, claim enumerationClaim) string {
	rows, cols := countMarkdownTableDimensions(scan, blockIdx)
	compCols := max(1, cols-1)
	if rows >= 2 && claim.expected != rows && claim.expected != compCols {
		return fmt.Sprintf("enumeration count mismatch on body line %d: prose states %q (%d), but following table has %d rows and %d comparison columns (Law 2)", claimLineNum, claim.phrase, claim.expected, rows, compCols)
	}
	return ""
}

// countContiguousListItems counts top-level sibling list items and detects grouped sub-lists.
func countContiguousListItems(scan specBodyScan, startIdx, baseIndent int, isNumbered bool) (int, bool) {
	count, grouped := 0, false
	for k := startIdx; k < len(scan.lines); k++ {
		if scan.fencedLines[k] || strings.TrimSpace(scan.lines[k]) == "" {
			continue
		}
		lk := scan.lines[k]
		if ok, sub := inspectListSiblingOrChild(lk, baseIndent, isNumbered, &count); ok {
			grouped = grouped || sub
			continue
		}
		break
	}
	return count, grouped
}

// inspectListSiblingOrChild classifies a non-empty list line as a same-level item, indented continuation, or terminator.
func inspectListSiblingOrChild(line string, baseIndent int, isNumbered bool, count *int) (bool, bool) {
	if bmk := listItemLinePattern.FindStringSubmatch(line); len(bmk) == 3 && len(bmk[1]) == baseIndent {
		if (bmk[2][0] >= '0' && bmk[2][0] <= '9') != isNumbered {
			return false, false
		}
		*count++
		return true, strings.Contains(line, ",") || strings.Contains(line, " and ")
	}
	if len(line)-len(strings.TrimLeft(line, " \t")) > baseIndent {
		return true, listItemLinePattern.MatchString(line)
	}
	return false, false
}

// countMarkdownTableDimensions returns (dataRows, totalColumns) for a contiguous Markdown table.
func countMarkdownTableDimensions(scan specBodyScan, startIdx int) (int, int) {
	header := strings.Trim(strings.TrimSpace(scan.lines[startIdx]), "|")
	cols := len(strings.Split(header, "|"))
	rows := 0
	for k := startIdx + 1; k < len(scan.lines); k++ {
		if scan.fencedLines[k] {
			break
		}
		trimmed := strings.TrimSpace(scan.lines[k])
		if !strings.HasPrefix(trimmed, "|") {
			break
		}
		if !tableSepRowPattern.MatchString(trimmed) {
			rows++
		}
	}
	return rows, cols
}
