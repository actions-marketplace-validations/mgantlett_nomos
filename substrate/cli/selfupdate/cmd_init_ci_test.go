package selfupdate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScaffoldCIWorkflows_GitHubGitLabAndIdempotency(t *testing.T) {
	// @verifies TC-CI-01
	dir := t.TempDir()
	written, err := ScaffoldCIWorkflows(dir, "main")
	if err != nil {
		t.Fatalf("ScaffoldCIWorkflows: %v", err)
	}
	if len(written) != 2 {
		t.Fatalf("expected 2 CI workflow files, got %v", written)
	}

	ghBytes, err := os.ReadFile(filepath.Join(dir, ".github", "workflows", "nomos-verify.yml"))
	if err != nil {
		t.Fatalf("read github workflow: %v", err)
	}
	ghText := string(ghBytes)
	for _, want := range []string{"fetch-depth: 0", "nomos verify --sarif", "github/codeql-action/upload-sarif@v3", "NOMOS_NO_UPDATE_CHECK: \"1\""} {
		if !strings.Contains(ghText, want) {
			t.Fatalf("github workflow missing %q:\n%s", want, ghText)
		}
	}

	glPath := filepath.Join(dir, ".gitlab-ci.yml")
	glBytes, err := os.ReadFile(glPath)
	if err != nil {
		t.Fatalf("read gitlab ci: %v", err)
	}
	if !strings.Contains(string(glBytes), "nomos-verify:") || !strings.Contains(string(glBytes), "nomos verify") {
		t.Fatalf("unexpected gitlab ci content:\n%s", string(glBytes))
	}

	// Re-running ScaffoldCIWorkflows must not duplicate nomos-verify job in existing .gitlab-ci.yml.
	if _, err := ScaffoldCIWorkflows(dir, "main"); err != nil {
		t.Fatalf("second ScaffoldCIWorkflows failed: %v", err)
	}
	glSecond, err := os.ReadFile(glPath)
	if err != nil {
		t.Fatalf("re-read gitlab ci: %v", err)
	}
	if strings.Count(string(glSecond), "nomos-verify:") != 1 {
		t.Fatalf("expected single nomos-verify job after idempotent rerun, got:\n%s", string(glSecond))
	}
	badge := FormatReadmeBadgeSnippet("demo-app")
	if !strings.Contains(badge, "actions/workflows/nomos-verify.yml/badge.svg") {
		t.Fatalf("unexpected badge snippet %q", badge)
	}
}
