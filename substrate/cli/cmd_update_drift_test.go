package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/cli/hookcmd"
	"github.com/mgantlett/nomos/substrate/cli/selfupdate"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/constants"
)

// @verifies TC-UPD-007
func TestDetectAdapterAndStealthDrift_SelectiveAndStealthFidelity(t *testing.T) {
	t.Setenv(constants.EnvNomosHome, t.TempDir())
	repoDir := t.TempDir()
	runGitOrFail(t, repoDir, "init")
	runGitOrFail(t, repoDir, "config", "user.name", "Tester")
	runGitOrFail(t, repoDir, "config", "user.email", "tester@nomos.local")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# Test\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGitOrFail(t, repoDir, "add", "README.md")
	runGitOrFail(t, repoDir, "commit", "-m", "initial")

	if _, err := runRootCLIForTest(t, "", "init", "--local", "--agents", "claude,opencode", "--dir", repoDir); err != nil {
		t.Fatalf("nomos init --local --agents claude,opencode: %v", err)
	}
	if drift := selfupdate.DetectWorkspaceContractDrift(repoDir); len(drift) != 0 {
		t.Fatalf("expected zero drift right after selective stealth init, got %v", drift)
	}

	assertSelectiveAndStealthRefresh(t, repoDir)
}

func assertSelectiveAndStealthRefresh(t *testing.T, repoDir string) {
	t.Helper()
	claudeManifest := filepath.Join(repoDir, claudeDirToken, settingsFileName)
	if err := os.WriteFile(claudeManifest, []byte(`{"customKey":true}`+"\n"), 0644); err != nil {
		t.Fatalf("tamper claude settings: %v", err)
	}
	ocPlugin := filepath.Join(repoDir, openCodeDirToken, "plugins", "nomos.ts")
	staleOC := "// NOMOS_OPEN_CORE_PLUGIN_START\nexport const stale = true;\n// NOMOS_OPEN_CORE_PLUGIN_END\n"
	if err := os.WriteFile(ocPlugin, []byte(staleOC), 0644); err != nil {
		t.Fatalf("tamper opencode plugin: %v", err)
	}
	excludeFile := filepath.Join(repoDir, constants.DirGit, "info", "exclude")
	staleExclude := localStealthStartMarker + "\n/nomos.yaml\n" + localStealthEndMarker + "\n"
	if err := os.WriteFile(excludeFile, []byte(staleExclude), 0644); err != nil {
		t.Fatalf("tamper git exclude: %v", err)
	}

	drift := selfupdate.DetectWorkspaceContractDrift(repoDir)
	for _, want := range []string{
		filepath.ToSlash(filepath.Join(claudeDirToken, settingsFileName)),
		filepath.ToSlash(filepath.Join(openCodeDirToken, "plugins", "nomos.ts")),
		filepath.ToSlash(filepath.Join(constants.DirGit, "info", "exclude")),
	} {
		if !slices.Contains(drift, want) {
			t.Fatalf("expected drift to contain %q, got %v", want, drift)
		}
	}

	if _, err := selfupdate.SyncWorkspaceContracts(repoDir); err != nil {
		t.Fatalf("SyncWorkspaceContracts: %v", err)
	}
	if after := selfupdate.DetectWorkspaceContractDrift(repoDir); len(after) != 0 {
		t.Fatalf("expected zero drift after SyncWorkspaceContracts, got %v", after)
	}
	if fileExists(filepath.Join(repoDir, hookcmd.CursorDirToken, hookcmd.HooksFileName)) || fileExists(filepath.Join(repoDir, geminiDirToken, settingsFileName)) {
		t.Fatalf("SyncWorkspaceContracts unexpectedly created unselected cursor/gemini manifests")
	}
	if porcelain := strings.TrimSpace(runGitOrFail(t, repoDir, "status", "--porcelain")); porcelain != "" {
		t.Fatalf("expected empty git status --porcelain in --local stealth repo after sync, got %q", porcelain)
	}
}
