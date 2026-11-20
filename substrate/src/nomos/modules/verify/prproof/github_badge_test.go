package prproof_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/prproof"
)

// @verifies TC-PR-02
func TestParseGitHubRepoSlug_AndBadgeFooter(t *testing.T) {
	cases := map[string]string{
		"https://github.com/mgantlett/nomos.git":    "mgantlett/nomos",
		"https://github.com/mgantlett/nomos/":       "mgantlett/nomos",
		"git@github.com:go-chi/chi.git":             "go-chi/chi",
		"ssh://git@github.com/spf13/cobra.git":      "spf13/cobra",
		"mgantlett/nomos":                           "mgantlett/nomos",
		"https://gitlab.com/acme/private-service":   "",
		"https://github.com/../escape":              "",
		"":                                          "",
	}
	for raw, want := range cases {
		if got := prproof.ParseGitHubRepoSlug(raw); got != want {
			t.Fatalf("ParseGitHubRepoSlug(%q) = %q, want %q", raw, got, want)
		}
	}

	repo := initTestGitRepo(t)
	t.Setenv("GITHUB_REPOSITORY", "")
	t.Setenv("GITHUB_REPOSITORY_VISIBILITY", "")
	t.Setenv("GITHUB_EVENT_PATH", "")

	// Without a GitHub remote, no public badge footer is appended.
	receipt := &prproof.PRProofReceipt{
		ProjectName: "demo-pr", BaseRef: "main", HeadCommit: "abc1234",
		GatesPassed: 42, GatesTotal: 42, PlaneSummary: "substrate-only", Passed: true,
	}
	plain := prproof.FormatPRProofMarkdownForRepo(receipt, repo)
	if strings.Contains(plain, "sophialabs.ca/api/xray/badge/") {
		t.Fatalf("unexpected public badge without GitHub remote:\n%s", plain)
	}

	// With a public GitHub remote, RenderVerifyPRAndMutation writes the live X-Ray badge link.
	mustGit(t, repo, "remote", "add", "origin", "https://github.com/mgantlett/nomos.git")
	var outBuf bytes.Buffer
	outPath := filepath.Join(t.TempDir(), "receipt.md")
	posture := prproof.GatePostureSummary{GatesPassed: 42, GatesTotal: 42, PipelinePass: true}
	if _, err := prproof.RenderVerifyPRAndMutation(&outBuf, repo, posture, prproof.PRProofOptions{
		BaseRef: "main", OutputPath: outPath,
	}, false); err != nil {
		t.Fatalf("RenderVerifyPRAndMutation failed: %v", err)
	}
	if !strings.Contains(outBuf.String(), "https://sophialabs.ca/api/xray/badge/mgantlett/nomos.svg") ||
		!strings.Contains(outBuf.String(), "https://sophialabs.ca/xray/github.com/mgantlett/nomos") {
		t.Fatalf("expected live X-Ray badge and scorecard link in PR receipt:\n%s", outBuf.String())
	}

	// Private CI visibility suppresses public X-Ray badge links automatically.
	evFile := filepath.Join(t.TempDir(), "event.json")
	if err := os.WriteFile(evFile, []byte(`{"repository":{"private":true,"visibility":"private"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_EVENT_PATH", evFile)
	privMd := prproof.FormatPRProofMarkdownForRepo(receipt, repo)
	if strings.Contains(privMd, "sophialabs.ca/api/xray/badge/") {
		t.Fatalf("expected private repository event to suppress public X-Ray badge:\n%s", privMd)
	}
}
