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
	for _, want := range []string{
		"fetch-depth: 0",
		"nomos verify --sarif",
		"github/codeql-action/upload-sarif@v3",
		"NOMOS_NO_UPDATE_CHECK: \"1\"",
		`export NOMOS_INSTALL_DIR="${HOME}/.local/bin"`,
		"install.sh | bash",
		`echo "${NOMOS_INSTALL_DIR}" >> "${GITHUB_PATH}"`,
	} {
		if !strings.Contains(ghText, want) {
			t.Fatalf("github workflow missing %q:\n%s", want, ghText)
		}
	}
	if strings.Contains(ghText, "install.sh | sh") {
		t.Fatalf("github workflow must pipe install.sh to bash, not sh:\n%s", ghText)
	}

	glPath := filepath.Join(dir, ".gitlab-ci.yml")
	glBytes, err := os.ReadFile(glPath)
	if err != nil {
		t.Fatalf("read gitlab ci: %v", err)
	}
	glText := string(glBytes)
	for _, want := range []string{
		"nomos-verify:",
		"nomos verify",
		`export NOMOS_INSTALL_DIR="${HOME}/.local/bin"`,
		"install.sh | bash",
		`export PATH="${NOMOS_INSTALL_DIR}:${PATH}"`,
	} {
		if !strings.Contains(glText, want) {
			t.Fatalf("gitlab ci missing %q:\n%s", want, glText)
		}
	}
	if strings.Contains(glText, "install.sh | sh") {
		t.Fatalf("gitlab ci must pipe install.sh to bash, not sh:\n%s", glText)
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

// TestRootActionYAML_MarketplaceBrandingAndStepSummaryExitTrap verifies root action.yml metadata and failure summary capture.
func TestRootActionYAML_MarketplaceBrandingAndStepSummaryExitTrap(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "action.yml"))
	if err != nil {
		t.Fatalf("read root action.yml: %v", err)
	}
	text := string(raw)
	for _, want := range []string{
		"branding:",
		"icon: 'shield'",
		"sarif-path:",
		"id: fast-binary",
		"install.sh",
		"if: steps.fast-binary.outputs.installed != 'true'",
		"nomos init --local -d .",
		"nomos.baseline.json",
		"VERIFY_EXIT=0",
		"|| VERIFY_EXIT=$?",
		`cat "${INPUT_RECEIPT_PATH}" >> "$GITHUB_STEP_SUMMARY"`,
		`exit "${VERIFY_EXIT}"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("action.yml missing %q:\n%s", want, text)
		}
	}
}
