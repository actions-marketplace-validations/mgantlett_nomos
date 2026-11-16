// Package selfupdate implements deterministic CI workflow scaffolding alongside workspace updates.
// @intent task_lifecycle_cli_spec.md
package selfupdate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/types"
)

const (
	githubWorkflowRelPath = ".github/workflows/nomos-verify.yml"
	gitlabCIConfigRelPath = ".gitlab-ci.yml"
)

// ScaffoldCIWorkflows generates deterministic zero-phone-home GitHub Actions and GitLab CI workflows (@intent task_lifecycle_cli_spec.md).
func ScaffoldCIWorkflows(absDir, defaultBranch string) ([]string, error) {
	branch := strings.TrimSpace(defaultBranch)
	if branch == "" {
		branch = "main"
	}
	ghPath := filepath.Join(absDir, githubWorkflowRelPath)
	if err := os.MkdirAll(filepath.Dir(ghPath), 0755); err != nil {
		return nil, types.NewNomosError(types.ErrCodeSubstrate, "failed creating CI workflow directory: %v", err)
	}
	if err := os.WriteFile(ghPath, []byte(renderGitHubWorkflowYAML(branch)), 0644); err != nil {
		return nil, types.NewNomosError(types.ErrCodeSubstrate, "failed writing %s: %v", githubWorkflowRelPath, err)
	}

	glPath := filepath.Join(absDir, gitlabCIConfigRelPath)
	if err := writeOrAppendGitLabCI(glPath); err != nil {
		return nil, types.NewNomosError(types.ErrCodeSubstrate, "failed writing %s: %v", gitlabCIConfigRelPath, err)
	}
	return []string{githubWorkflowRelPath, gitlabCIConfigRelPath}, nil
}

// FormatReadmeBadgeSnippet returns a Markdown badge snippet targeting the generated GitHub Actions workflow.
func FormatReadmeBadgeSnippet(project string) string {
	clean := strings.TrimSpace(project)
	if clean == "" {
		clean = "repo"
	}
	return fmt.Sprintf("[![Nomos Verify](https://github.com/<owner>/%s/actions/workflows/nomos-verify.yml/badge.svg)](https://github.com/<owner>/%s/actions/workflows/nomos-verify.yml)", clean, clean)
}

func renderGitHubWorkflowYAML(defaultBranch string) string {
	return fmt.Sprintf(`name: Nomos Verify

on:
  push:
    branches: [%s]
  pull_request:
    branches: [%s]

permissions:
  contents: read
  security-events: write

jobs:
  nomos-verify:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - name: Install Nomos Open-Core Static Binary
        env:
          NOMOS_NO_UPDATE_CHECK: "1"
        run: |
          export NOMOS_INSTALL_DIR="${HOME}/.local/bin"
          curl -fsSL https://raw.githubusercontent.com/mgantlett/nomos/main/install.sh | bash
          echo "${NOMOS_INSTALL_DIR}" >> "${GITHUB_PATH}"
      - name: Run Nomos SARIF Probe
        run: nomos verify --sarif > nomos.sarif || true
      - name: Upload SARIF to GitHub Code Scanning
        if: always()
        uses: github/codeql-action/upload-sarif@v3
        with:
          sarif_file: nomos.sarif
      - name: Enforce Nomos Definition of Done
        run: nomos verify
`, defaultBranch, defaultBranch)
}

func writeOrAppendGitLabCI(glPath string) error {
	jobBlock := renderGitLabVerifyJob()
	existing, err := os.ReadFile(glPath)
	if err != nil {
		if os.IsNotExist(err) {
			content := "stages:\n  - verify\n\n" + jobBlock
			return os.WriteFile(glPath, []byte(content), 0644)
		}
		return err
	}
	text := string(existing)
	if strings.Contains(text, "nomos-verify:") {
		return nil
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return os.WriteFile(glPath, []byte(text+"\n"+jobBlock), 0644)
}

func renderGitLabVerifyJob() string {
	return `nomos-verify:
  stage: verify
  variables:
    NOMOS_NO_UPDATE_CHECK: "1"
    GIT_DEPTH: "0"
  script:
    - export NOMOS_INSTALL_DIR="${HOME}/.local/bin"
    - curl -fsSL https://raw.githubusercontent.com/mgantlett/nomos/main/install.sh | bash
    - export PATH="${NOMOS_INSTALL_DIR}:${PATH}"
    - nomos verify
`
}
