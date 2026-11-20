package intentlint

import "testing"

func TestValidateMarkdownEnumerationsAcceptsValidPatterns(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			name: "matching_three_bullets",
			body: "The substrate enforces three physical guarantees:\n\n- Boundary locks\n- Contract parity\n- Spec provenance\n",
		},
		{
			name: "two_column_comparison_table_with_many_rows",
			body: "Two coding-agent tiers exist across Distinct Envelopes:\n\n| Dimension | Tier 1 | Tier 2 |\n| :--- | :--- | :--- |\n| Role | Interactive | Headless |\n| Proof | 42 probes | TDD |\n| Scope | Worktree | DAG node |\n| Retry | 3 strikes | 3 attempts |\n",
		},
		{
			name: "inline_parenthetical_enumeration_before_colon",
			body: "Nomos eliminates the Three Silent Failures of coding (Reward Hacking, Hollow Stubs, and Workspace Drift):\n\n- Probe 1\n- Probe 2\n- Probe 3\n- Probe 4\n- Probe 5\n",
		},
		{
			name: "hyphenated_duration_compound_adjective",
			body: "Three-minute executive memos for engineering leaders:\n\n- EB-01\n- EB-02\n- EB-03\n- EB-04\n- EB-05\n",
		},
		{
			name: "grouped_category_bullets_with_comma_subitems",
			body: "Each round executes across four distinct model architectures:\n\n- **Cloud Frontier**: `sonnet-5.5`, `haiku-5.5`, and `gemini-3.8-flash`\n- **Local Silicon**: `Qwen3.8-27B`\n",
		},
		{
			name: "fenced_code_block_ignored",
			body: "```markdown\nThe system enforces three guarantees:\n- One\n- Two\n```\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scan := scanSpecBodyOutsideFences(tc.body)
			if got := validateMarkdownEnumerations(scan); got != "" {
				t.Fatalf("expected valid enumeration, got %q", got)
			}
		})
	}
}
