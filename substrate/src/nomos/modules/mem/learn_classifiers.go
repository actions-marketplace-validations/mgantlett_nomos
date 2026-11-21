// Package mem classifies compiled structural gates, polyglot test assertions, and bare pitfall headers.
package mem

import "strings"

var astCeilingGateTokens = []string{
	"cognitive complexity",
	"cognitive_complexity",
	"cognitive debt",
	"cognitive_debt",
	"max file loc",
	"max_file_loc",
	"function length",
	"function_length",
	"file length",
	"file_length",
	"statement coverage",
	"statement_coverage",
}

var statutoryConventionGateTokens = []string{
	"docstring density",
	"docstring_density",
	"unchecked error",
	"unchecked_error",
	"anti-shim",
	"anti_shim",
	"tdd test pair",
	"tdd_test_pair",
	"dead-code",
	"dead_code",
	"go toolchain",
	"go_toolchain",
	"polyglot toolchain",
	"polyglot_toolchain",
	"intent steel-man",
	"intent_steel-man",
	"intent substrate parity",
	"intent_substrate_parity",
	"intent traceability",
	"intent_traceability",
	"intent config immutability",
	"intent_config_immutability",
	"bilateral parity",
	"bilateral_parity",
	"plane separation",
	"plane_separation",
	"planeseparationgate",
	"hermetic",
	"zero filesystem alias",
	"zero_filesystem_alias",
	"magic string",
	"magic_string",
	"git subprocess",
	"git_subprocess",
	"broken wire",
	"broken_wire",
	"threshold integrity",
	"threshold_integrity",
	"contract schema",
	"contract_schema",
	"open-core boundary",
	"open-core_boundary",
	"open_core_boundary",
	"modern secret",
	"modern_secret",
	"secret hygiene",
	"secret_hygiene",
	"canonical singularity",
	"canonical_singularity",
}

func containsAnyToken(lower string, tokens []string) bool {
	for _, tok := range tokens {
		if strings.Contains(lower, tok) {
			return true
		}
	}
	return false
}

// isAstCeilingGateName returns true when gateName enforces compiled AST size, complexity, or coverage ceilings.
func isAstCeilingGateName(gateName string) bool {
	return containsAnyToken(strings.ToLower(strings.TrimSpace(gateName)), astCeilingGateTokens)
}

// isStatutoryConventionGateName returns true when gateName enforces compiled statutory conventions or toolchain invariants.
func isStatutoryConventionGateName(gateName string) bool {
	return containsAnyToken(strings.ToLower(strings.TrimSpace(gateName)), statutoryConventionGateTokens)
}

// isBuiltInStructuralGateName returns true when gateName is compiled into the Nomos 42-gate binary rather than nomos.yaml literals.
func isBuiltInStructuralGateName(gateName string) bool {
	return isAstCeilingGateName(gateName) || isStatutoryConventionGateName(gateName)
}

// isBuiltInStructuralGateTopic returns true when a pitfall:<gate>:<scope> topic belongs to a compiled Tier-C gate.
func isBuiltInStructuralGateTopic(topic string) bool {
	slug := extractGateSlugFromPitfallTopic(topic)
	if slug == "" {
		slug = strings.ToLower(strings.TrimSpace(topic))
	}
	return isBuiltInStructuralGateName(slug)
}

// isPolyglotEphemeralTestContent detects single-test assertion failures across Go, TAP, pytest, Python unittest, and Vitest.
func isPolyglotEphemeralTestContent(content string) bool {
	lower := strings.ToLower(content)
	if !strings.Contains(lower, "trap:") {
		return false
	}
	if hasDirectTestRunnerFailureToken(lower) {
		return true
	}
	if strings.Contains(lower, "python coverage test execution failed:") && strings.Contains(lower, "-> fail") {
		return true
	}
	return isToolchainTestFileAssertion(lower)
}

func hasDirectTestRunnerFailureToken(lower string) bool {
	directTokens := []string{
		"trap: --- fail:",
		"trap: go compilation/test failed: --- fail:",
		"not ok ",
		"trap: python unittest suite failed:",
		"trap: failed tests/",
		"trap: failed test_",
	}
	return containsAnyToken(lower, directTokens)
}

func isToolchainTestFileAssertion(lower string) bool {
	isToolchain := strings.HasPrefix(lower, "[go toolchain gate") || strings.HasPrefix(lower, "[polyglot toolchain")
	if !isToolchain {
		return false
	}
	return containsAnyToken(lower, []string{
		"trap: location: '",
		"assertionerror:",
		".test.ts",
		".spec.ts",
	})
}

// isLegacyBarePitfallContent detects pre-SUB-1960 bare gate headers or empty divider traps lacking file diagnostics.
func isLegacyBarePitfallContent(content string) bool {
	trap, fix := splitPitfallTrapAndFix(content)
	if trap == "" {
		return false
	}
	if strings.HasSuffix(trap, ":") || strings.HasSuffix(trap, "TAP version 13") {
		return true
	}
	if strings.Trim(trap, "-= \t") == "" {
		return true
	}
	return strings.HasSuffix(fix, ":**") || strings.HasSuffix(fix, "**:")
}

func splitPitfallTrapAndFix(content string) (string, string) {
	body := strings.TrimSpace(content)
	if idx := strings.Index(body, "]"); strings.HasPrefix(body, "[") && idx > 0 {
		body = strings.TrimSpace(body[idx+1:])
	}
	parts := strings.SplitN(body, "-> Verified Fix", 2)
	trap := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(parts[0]), "Trap:"))
	if len(parts) < 2 {
		return trap, ""
	}
	rhs := strings.TrimSpace(parts[1])
	if strings.HasPrefix(rhs, "(") {
		if end := strings.Index(rhs, ")"); end > 1 {
			rhs = strings.TrimSpace(rhs[end+1:])
		}
	}
	return trap, strings.TrimSpace(strings.TrimPrefix(rhs, ":"))
}
