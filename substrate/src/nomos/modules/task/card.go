// Package task defines domain models, schema constraints, and tracking mechanisms
// enforced by compiled Go SSoT contracts.
package task

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/types"
)

const (
	minScopeRunes        = 10
	minACItemRunes       = 3
	minVerificationRunes = 5
	minExplicitBurden    = 1
	maxExplicitBurden    = 10
)

var (
	htmlCommentRe = regexp.MustCompile(`<!--[\s\S]*?-->`)
	checkboxRe    = regexp.MustCompile(`^[-*]\s*\[([ xX]?)\s*\]\s*(.*)$`)
	tcVectorIDRe  = regexp.MustCompile(`^TC-[A-Z0-9]+-[0-9]+$`)
)

// TaskCardInput models the fail-closed Agent-to-Agent backlog card contract (#TaskCardInput).
type TaskCardInput struct {
	Scope              string   `json:"scope"`
	ScopePaths         []string `json:"scope_paths,omitempty"`
	Obligations        []string `json:"obligations,omitempty"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	Verification       string   `json:"verification"`
	Burden             int      `json:"burden,omitempty"`
}

// NormalizeScopePaths deduplicates and normalizes CSV or repeatable path globs.
func NormalizeScopePaths(raw []string) []string {
	seen := make(map[string]bool, len(raw))
	var out []string
	for _, item := range raw {
		for _, part := range strings.Split(item, ",") {
			clean := strings.TrimSpace(strings.ReplaceAll(part, "\\", "/"))
			clean = strings.TrimPrefix(clean, "./")
			if clean == "" || seen[clean] {
				continue
			}
			seen[clean] = true
			out = append(out, clean)
		}
	}
	return out
}

// NormalizeObligations validates and deduplicates TC-* obligation identifiers.
func NormalizeObligations(raw []string) ([]string, error) {
	seen := make(map[string]bool, len(raw))
	var out []string
	for _, item := range raw {
		for _, part := range strings.Split(item, ",") {
			clean := strings.ToUpper(strings.TrimSpace(part))
			if clean == "" || seen[clean] {
				continue
			}
			if !tcVectorIDRe.MatchString(clean) {
				return nil, types.NewNomosError(types.ErrCodeValidationFailed,
					"invalid obligation %q: must match TC-<DOMAIN>-<NUM> (e.g. TC-TSK-18)", part)
			}
			seen[clean] = true
			out = append(out, clean)
		}
	}
	return out, nil
}

// TaskCardRemedy returns an actionable 3-line HATEOAS hint for backlog card failures.
// @intent INT-864 #TaskCardInput
func TaskCardRemedy(subcommand, title, plane string) string {
	t := strings.TrimSpace(title)
	if t == "" {
		t = "<title>"
	}
	p := strings.TrimSpace(plane)
	if p == "" {
		p = "substrate"
	}
	planeFlag := ""
	if subcommand == "create" {
		planeFlag = fmt.Sprintf(" --plane %s", p)
	}
	return fmt.Sprintf(
		"durable backlog tasks require the 3-section card contract (use 'nomos task chore %q' for 1-turn fixes):\n"+
			"  nomos task %s %q%s --scope \"<target files & problem>\" --ac \"<deliverable>\" --verify \"<test command>\"\n"+
			"  or pass 3-section Markdown via stdin: nomos task %s %q%s -F - <<'EOF' ... EOF",
		t, subcommand, t, planeFlag, subcommand, t, planeFlag,
	)
}

// RenderTaskCardBody synthesizes canonical 3-section Markdown from structured inputs.
// @intent INT-864 #TaskCardInput
func RenderTaskCardBody(in TaskCardInput) (string, error) {
	scope := strings.TrimSpace(in.Scope)
	if utf8.RuneCountInString(scope) < minScopeRunes {
		return "", types.NewNomosError(types.ErrCodeValidationFailed,
			"--scope must be at least %d characters", minScopeRunes)
	}
	items, err := normalizeACItems(in.AcceptanceCriteria)
	if err != nil {
		return "", err
	}
	verifyText := strings.TrimSpace(in.Verification)
	if utf8.RuneCountInString(verifyText) < minVerificationRunes {
		return "", types.NewNomosError(types.ErrCodeValidationFailed,
			"--verify must be at least %d characters", minVerificationRunes)
	}

	paths := NormalizeScopePaths(in.ScopePaths)
	obligations, oErr := NormalizeObligations(in.Obligations)
	if oErr != nil {
		return "", oErr
	}

	var sb strings.Builder
	sb.WriteString("## Scope & Context\n")
	sb.WriteString(scope)
	writeScopeMetadata(&sb, paths, obligations)
	sb.WriteString("\n\n## Acceptance Criteria\n")
	for _, it := range items {
		sb.WriteString("- [ ] ")
		sb.WriteString(it)
		sb.WriteString("\n")
	}
	sb.WriteString("\n## Verification\n")
	if !strings.HasPrefix(verifyText, "-") && !strings.HasPrefix(verifyText, "*") && !strings.HasPrefix(verifyText, "```") {
		sb.WriteString("- ")
	}
	sb.WriteString(verifyText)
	sb.WriteString("\n")

	rendered := sb.String()
	if err := ValidateTaskCardBody(rendered); err != nil {
		return "", err
	}
	return rendered, nil
}

func writeScopeMetadata(sb *strings.Builder, paths, obligations []string) {
	if len(paths) > 0 {
		sb.WriteString("\n- **Paths**: `")
		sb.WriteString(strings.Join(paths, "`, `"))
		sb.WriteString("`")
	}
	if len(obligations) > 0 {
		sb.WriteString("\n- **Obligations**: `")
		sb.WriteString(strings.Join(obligations, "`, `"))
		sb.WriteString("`")
	}
}

func normalizeACItems(raw []string) ([]string, error) {
	var clean []string
	for _, item := range raw {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		if m := checkboxRe.FindStringSubmatch(trimmed); m != nil {
			trimmed = strings.TrimSpace(m[2])
		} else {
			trimmed = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(trimmed, "- "), "* "))
		}
		if utf8.RuneCountInString(trimmed) < minACItemRunes {
			return nil, types.NewNomosError(types.ErrCodeValidationFailed,
				"each --ac item must be at least %d characters", minACItemRunes)
		}
		clean = append(clean, trimmed)
	}
	if len(clean) == 0 {
		return nil, types.NewNomosError(types.ErrCodeValidationFailed,
			"at least one --ac acceptance criterion is required")
	}
	return clean, nil
}

// ValidateTaskCardBody verifies that a Markdown task description satisfies the 3-section card contract.
// @intent INT-864 #TaskCardInput
func ValidateTaskCardBody(body string) error {
	cleaned := strings.TrimSpace(htmlCommentRe.ReplaceAllString(body, ""))
	if cleaned == "" {
		return types.NewNomosError(types.ErrCodeValidationFailed,
			"task description cannot be empty for durable backlog tasks")
	}
	if hasUnexpandedEscapedNewlines(cleaned) {
		return types.NewNomosError(types.ErrCodeValidationFailed,
			"task description contains literal '\\n' escape sequences; pass --scope/--ac/--verify or '-F -'")
	}

	sections := parseCardSections(cleaned)
	if err := checkScopeSection(sections["scope"]); err != nil {
		return err
	}
	if err := checkACSection(sections["ac"]); err != nil {
		return err
	}
	return checkVerifySection(sections["verify"])
}

func hasUnexpandedEscapedNewlines(text string) bool {
	inFence := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if !inFence && strings.Contains(line, `\n##`) {
			return true
		}
	}
	return strings.Count(text, "\n") == 0 && strings.Contains(text, `\n`)
}

func parseCardSections(cleaned string) map[string][]string {
	out := map[string][]string{}
	current := ""
	inFence := false
	for _, line := range strings.Split(cleaned, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
		}
		if !inFence && (strings.HasPrefix(trimmed, "## ") || strings.HasPrefix(trimmed, "### ")) {
			current = classifyCardHeader(trimmed)
			continue
		}
		if current != "" {
			out[current] = append(out[current], line)
		}
	}
	return out
}

func classifyCardHeader(headerLine string) string {
	norm := strings.ToLower(strings.TrimLeft(headerLine, "# "))
	switch {
	case strings.Contains(norm, "acceptance criteria"):
		return "ac"
	case strings.Contains(norm, "verification"):
		return "verify"
	case strings.Contains(norm, "scope"):
		return "scope"
	default:
		return "other"
	}
}

func checkScopeSection(lines []string) error {
	content := strings.TrimSpace(strings.Join(lines, "\n"))
	if utf8.RuneCountInString(content) < minScopeRunes {
		return types.NewNomosError(types.ErrCodeValidationFailed,
			"missing or under-specified '## Scope & Context' section (minimum %d characters)", minScopeRunes)
	}
	return nil
}

func checkACSection(lines []string) error {
	if len(lines) == 0 {
		return types.NewNomosError(types.ErrCodeValidationFailed,
			"missing '## Acceptance Criteria' section with '- [ ] <item>' checklist bullets")
	}
	validItems := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		m := checkboxRe.FindStringSubmatch(trimmed)
		if m == nil {
			continue
		}
		itemText := strings.TrimSpace(m[2])
		if utf8.RuneCountInString(itemText) < minACItemRunes {
			return types.NewNomosError(types.ErrCodeValidationFailed,
				"empty or trivial checkbox item in '## Acceptance Criteria'")
		}
		validItems++
	}
	if validItems == 0 {
		return types.NewNomosError(types.ErrCodeValidationFailed,
			"'## Acceptance Criteria' must contain at least one '- [ ] <deliverable>' item")
	}
	return nil
}

func checkVerifySection(lines []string) error {
	content := strings.TrimSpace(strings.Join(lines, "\n"))
	if utf8.RuneCountInString(content) < minVerificationRunes {
		return types.NewNomosError(types.ErrCodeValidationFailed,
			"missing or under-specified '## Verification' section (minimum %d characters)", minVerificationRunes)
	}
	return nil
}

// DeriveTaskBurden validates an explicit burden override (1-10) or deterministically derives 2-8 from card density.
// @intent INT-864 #TaskCardInput
func DeriveTaskBurden(explicit int, body string) (int, error) {
	if explicit != 0 {
		if explicit < minExplicitBurden || explicit > maxExplicitBurden {
			return 0, types.NewNomosError(types.ErrCodeValidationFailed,
				"--burden must be between %d and %d, got %d", minExplicitBurden, maxExplicitBurden, explicit)
		}
		return explicit, nil
	}
	checkCount := 0
	for _, line := range strings.Split(body, "\n") {
		if checkboxRe.MatchString(strings.TrimSpace(line)) {
			checkCount++
		}
	}
	score := 2
	if checkCount > 1 {
		score += checkCount - 1
	}
	runes := utf8.RuneCountInString(body)
	if runes > 500 {
		score++
	}
	if runes > 1200 {
		score++
	}
	if score > 8 {
		score = 8
	}
	return score, nil
}

// ExtractCardIRFromBody extracts normalized acceptance criteria and verification command from a validated Markdown card.
func ExtractCardIRFromBody(body string) ([]string, string) {
	cleaned := strings.TrimSpace(htmlCommentRe.ReplaceAllString(body, ""))
	sections := parseCardSections(cleaned)
	var ac []string
	for _, line := range sections["ac"] {
		if m := checkboxRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			if item := strings.TrimSpace(m[2]); item != "" {
				ac = append(ac, item)
			}
		}
	}
	verify := strings.TrimSpace(strings.Join(sections["verify"], "\n"))
	verify = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(verify, "- "), "* "))
	return ac, verify
}

