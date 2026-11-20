// Package prproof resolves GitHub remote slugs and renders live X-Ray badge links on PR receipts.
// @intent nomos_intent_traceability_spec.md
package prproof

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
)

const publicXrayOrigin = "https://sophialabs.ca"

// FormatPRProofMarkdownForRepo renders FormatPRProofMarkdown and appends a live X-Ray badge
// and scorecard link when targetDir resolves to a non-private GitHub repository remote.
func FormatPRProofMarkdownForRepo(receipt *PRProofReceipt, targetDir string) string {
	base := FormatPRProofMarkdown(receipt)
	if base == "" {
		return ""
	}
	slug := ResolvePublicGitHubSlug(targetDir)
	if slug == "" {
		return base
	}
	badgeURL := fmt.Sprintf("%s/api/xray/badge/%s.svg", publicXrayOrigin, slug)
	scorecardURL := fmt.Sprintf("%s/xray/github.com/%s", publicXrayOrigin, slug)
	footer := fmt.Sprintf(
		"\n[![Nomos X-Ray](%s)](%s) · [Live AST X-Ray Scorecard (`%s`)](%s)\n",
		badgeURL, scorecardURL, slug, scorecardURL,
	)
	return base + footer
}

// ResolvePublicGitHubSlug inspects remote.origin.url (falling back to GITHUB_REPOSITORY)
// and returns an "owner/repo" slug unless CI metadata marks the repository private/internal.
func ResolvePublicGitHubSlug(targetDir string) string {
	if isCIPrivateGitHubRepo() {
		return ""
	}
	if strings.TrimSpace(targetDir) != "" {
		if out, err := workspace.RunSafeGit(targetDir, "config", "--get", "remote.origin.url"); err == nil {
			if slug := ParseGitHubRepoSlug(string(out)); slug != "" {
				return slug
			}
		}
	}
	return ParseGitHubRepoSlug(os.Getenv("GITHUB_REPOSITORY"))
}

// ParseGitHubRepoSlug extracts a clean "owner/repo" slug from HTTPS, SSH, or git@github.com URLs.
func ParseGitHubRepoSlug(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	lower := strings.ToLower(s)
	switch {
	case strings.Contains(lower, "github.com/"):
		idx := strings.Index(lower, "github.com/")
		s = s[idx+len("github.com/"):]
	case strings.Contains(lower, "github.com:"):
		idx := strings.Index(lower, "github.com:")
		s = s[idx+len("github.com:"):]
	case strings.Contains(s, "://") || strings.Contains(s, "@"):
		return ""
	}
	s = strings.Trim(s, "/")
	s = strings.TrimSuffix(s, ".git")
	parts := strings.Split(s, "/")
	if len(parts) < 2 {
		return ""
	}
	owner, repo := parts[0], parts[1]
	if !isSafeGitHubSegment(owner) || !isSafeGitHubSegment(repo) {
		return ""
	}
	return owner + "/" + repo
}

func isSafeGitHubSegment(seg string) bool {
	if seg == "" || seg == "." || seg == ".." || len(seg) > 100 {
		return false
	}
	for _, r := range seg {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_.", r) {
			return false
		}
	}
	return true
}

func isCIPrivateGitHubRepo() bool {
	vis := strings.ToLower(strings.TrimSpace(os.Getenv("GITHUB_REPOSITORY_VISIBILITY")))
	if vis == "private" || vis == "internal" {
		return true
	}
	evPath := strings.TrimSpace(os.Getenv("GITHUB_EVENT_PATH"))
	if evPath == "" {
		return false
	}
	raw, err := os.ReadFile(evPath)
	if err != nil {
		return false
	}
	var payload struct {
		Repository struct {
			Private    bool   `json:"private"`
			Visibility string `json:"visibility"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return false
	}
	evVis := strings.ToLower(strings.TrimSpace(payload.Repository.Visibility))
	return payload.Repository.Private || evVis == "private" || evVis == "internal"
}
