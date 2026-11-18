// Package astdebt defines canonical polyglot extension constants and lightweight shared utilities (Law 4 & Law 5).
package astdebt

import "strings"

const (
	ExtGo     = ".go"
	ExtPy     = ".py"
	ExtTS     = ".ts"
	ExtTSX    = ".tsx"
	ExtJS     = ".js"
	ExtJSX    = ".jsx"
	ExtMJS    = ".mjs"
	ExtCJS    = ".cjs"
	ExtSvelte = ".svelte"
	ExtRB     = ".rb"
	ExtRake   = ".rake"
	ExtRS     = ".rs"
	ExtC      = ".c"
	ExtH      = ".h"
	ExtCPP    = ".cpp"
	ExtCC     = ".cc"
	ExtCXX    = ".cxx"
	ExtHPP    = ".hpp"
	ExtJava   = ".java"
	ExtKT     = ".kt"
	ExtKTS    = ".kts"
	ExtCS     = ".cs"

	DirTest      = "test"
	DirTests     = "tests"
	DirSpec      = "spec"
	DirCoverage  = "coverage"
	PrefixPyTest = "test_"
	SuffixGoTest = "_test.go"
	SuffixPyTest = "_test.py"
)

// MinInt returns the smaller of two integer values.
func MinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// IsJSFamilyExt reports whether ext belongs to JavaScript, TypeScript, or Svelte sources.
func IsJSFamilyExt(ext string) bool {
	switch ext {
	case ExtTS, ExtTSX, ExtJS, ExtJSX, ExtMJS, ExtCJS, ExtSvelte:
		return true
	default:
		return false
	}
}

// makeWordSet builds a fast string lookup set from whitespace-delimited tokens.
func makeWordSet(words string) map[string]bool {
	fields := strings.Fields(words)
	set := make(map[string]bool, len(fields))
	for _, w := range fields {
		set[w] = true
	}
	return set
}

// countLeadingIndentCols computes visual indentation column width using 4-space tab stops.
func countLeadingIndentCols(s string) int {
	cols := 0
	for _, ch := range s {
		if ch == ' ' {
			cols++
		} else if ch == '\t' {
			cols += 4
		} else {
			break
		}
	}
	return cols
}
