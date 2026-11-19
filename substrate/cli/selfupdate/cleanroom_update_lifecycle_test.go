package selfupdate_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mgantlett/nomos/substrate/cli"
	"github.com/mgantlett/nomos/substrate/cli/selfupdate"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/constants"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/types"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
)

// TestCleanroomDownstreamVectors_VerifiedUpdateNoticeAndWorkspaceSync executes the 5-probe
// CDV-UPDATE adversarial cleanroom verification suite.
func TestCleanroomDownstreamVectors_VerifiedUpdateNoticeAndWorkspaceSync(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX cleanroom downstream vector suite")
	}
	t.Run("CDV_UP_01_PackageManagerProvenanceQuarantine", verifyCDVUpdateProvenanceQuarantine)
	t.Run("CDV_UP_02_TamperedSHA256ArchiveRejectedBeforeSwap", verifyCDVUpdateTamperedChecksumRejected)
	t.Run("CDV_UP_03_VerifiedAtomicSwapAnd24hHandshakeCache", verifyCDVUpdateAtomicSwapAndCache)
	t.Run("CDV_UP_04_DownstreamContractDriftAndIdempotentSync", verifyCDVUpdateWorkspaceContractSync)
	t.Run("CDV_UP_05_SelectiveAdapterAndStealthSyncFidelity", verifyCDVUpdateSelectiveStealthSync)
}

func verifyCDVUpdateProvenanceQuarantine(t *testing.T) {
	t.Setenv(constants.EnvNomosHome, t.TempDir())
	srv := startMockReleaseServer(t, "v99.7.0", "nomos_Linux_x86_64.tar.gz", []byte("x"), "00")
	t.Setenv(constants.EnvNomosUpdateAPIURL, srv.URL+"/api/latest")

	_, wantUsrBinCmd := selfupdate.DetectInstallMethod(filepath.Join("/usr", "bin", "nomos"))
	guards := map[string]string{
		filepath.Join("/opt", "homebrew", "bin", "nomos"):         "brew upgrade --cask mgantlett/tap/nomos",
		filepath.Join("/usr", "bin", "nomos"):                     wantUsrBinCmd,
		filepath.Join("/nix", "store", "hash-nomos", "bin/nomos"): "nix profile upgrade nomos",
	}
	for managedPath, wantCmd := range guards {
		t.Setenv(constants.EnvNomosBinaryPath, managedPath)
		root := cli.NewRootCmd()
		root.SetOut(new(bytes.Buffer))
		root.SetArgs([]string{"update"})
		err := root.Execute()
		var nErr *types.NomosError
		if !errors.As(err, &nErr) || nErr.Code != types.ErrCodeBoundary || !strings.Contains(nErr.Remedy, wantCmd) {
			t.Fatalf("CDV-UP-01: expected %s blocked with remedy %q, got %v", managedPath, wantCmd, err)
		}
	}
}

func verifyCDVUpdateTamperedChecksumRejected(t *testing.T) {
	t.Setenv(constants.EnvNomosHome, t.TempDir())
	assetName, err := selfupdate.FormatReleaseAssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatalf("FormatReleaseAssetName: %v", err)
	}
	archiveBytes, _ := buildTestTarGzArchive(t, "nomos", []byte("rogue-payload"))
	srv := startMockReleaseServer(t, "v99.7.1", assetName, archiveBytes, strings.Repeat("f", 64))

	binPath := filepath.Join(t.TempDir(), "nomos")
	sentinel := []byte("original-verified-substrate")
	if wErr := os.WriteFile(binPath, sentinel, 0755); wErr != nil {
		t.Fatalf("WriteFile: %v", wErr)
	}
	t.Setenv(constants.EnvNomosBinaryPath, binPath)
	t.Setenv(constants.EnvNomosUpdateAPIURL, srv.URL+"/api/latest")
	t.Setenv(constants.EnvNomosUpdateBaseURL, srv.URL+"/dl")

	root := cli.NewRootCmd()
	root.SetOut(new(bytes.Buffer))
	root.SetArgs([]string{"update"})
	if exErr := root.Execute(); exErr == nil {
		t.Fatalf("CDV-UP-02: expected tampered checksum archive to abort fail-closed")
	}
	disk, rErr := os.ReadFile(binPath)
	if rErr != nil || !bytes.Equal(disk, sentinel) {
		t.Fatalf("CDV-UP-02: binary was corrupted despite checksum failure")
	}
}

func verifyCDVUpdateAtomicSwapAndCache(t *testing.T) {
	nomosHome := t.TempDir()
	t.Setenv(constants.EnvNomosHome, nomosHome)
	t.Setenv(constants.EnvNomosNoUpdateCheck, "")
	t.Setenv("CI", "")

	assetName, err := selfupdate.FormatReleaseAssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatalf("FormatReleaseAssetName: %v", err)
	}
	upgraded := []byte("#!/bin/sh\necho nomos-v99.7.2\n")
	archiveBytes, sumHex := buildTestTarGzArchive(t, "nomos", upgraded)
	srv := startMockReleaseServer(t, "v99.7.2", assetName, archiveBytes, sumHex)

	binPath := filepath.Join(t.TempDir(), "nomos")
	if wErr := os.WriteFile(binPath, []byte("v4-old"), 0755); wErr != nil {
		t.Fatalf("WriteFile: %v", wErr)
	}
	t.Setenv(constants.EnvNomosBinaryPath, binPath)
	t.Setenv(constants.EnvNomosUpdateAPIURL, srv.URL+"/api/latest")
	t.Setenv(constants.EnvNomosUpdateBaseURL, srv.URL+"/dl")

	notice := selfupdate.RefreshAndFormatUpdateNotice("4.14.0", binPath, time.Now())
	if !strings.Contains(notice, "4.14.0 -> 99.7.2") {
		t.Fatalf("CDV-UP-03: expected 24h handshake notice before update, got %q", notice)
	}

	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"update", "--json"})
	if exErr := root.Execute(); exErr != nil {
		t.Fatalf("CDV-UP-03: standalone update failed: %v", exErr)
	}
	var rep selfupdate.UpdateReport
	if uErr := json.Unmarshal(out.Bytes(), &rep); uErr != nil || !rep.Updated || rep.ChecksumSHA256 != sumHex {
		t.Fatalf("CDV-UP-03: unexpected UpdateReport: err=%v rep=%+v", uErr, rep)
	}
}

func verifyCDVUpdateWorkspaceContractSync(t *testing.T) {
	t.Setenv(constants.EnvNomosHome, t.TempDir())
	repoDir := t.TempDir()

	initCmd := cli.NewRootCmd()
	initCmd.SetOut(new(bytes.Buffer))
	initCmd.SetArgs([]string{"init", "--dir", repoDir})
	if err := initCmd.Execute(); err != nil {
		t.Fatalf("CDV-UP-04: nomos init failed: %v", err)
	}

	agentsPath := filepath.Join(repoDir, "AGENTS.md")
	drifted := "<!-- NOMOS_OPEN_CORE_PROTOCOL_START -->\nlegacy v4.0 block\n<!-- NOMOS_OPEN_CORE_PROTOCOL_END -->\n\n## Downstream Rule\nPreserve me.\n"
	if wErr := os.WriteFile(agentsPath, []byte(drifted), 0644); wErr != nil {
		t.Fatalf("WriteFile AGENTS.md: %v", wErr)
	}
	if len(selfupdate.DetectWorkspaceContractDrift(repoDir)) == 0 {
		t.Fatalf("CDV-UP-04: expected AGENTS.md protocol drift detected")
	}

	srv := startMockReleaseServer(t, "v"+cli.RootCmdVersion, "nomos_Linux_x86_64.tar.gz", []byte("x"), "00")
	t.Setenv(constants.EnvNomosUpdateAPIURL, srv.URL+"/api/latest")
	t.Setenv(constants.EnvNomosBinaryPath, filepath.Join(t.TempDir(), "nomos"))

	syncCmd := cli.NewRootCmd()
	syncCmd.SetOut(new(bytes.Buffer))
	syncCmd.SetArgs([]string{"update", "--sync-workspace", "--dir", repoDir})
	if err := syncCmd.Execute(); err != nil {
		t.Fatalf("CDV-UP-04: --sync-workspace failed: %v", err)
	}
	if rem := selfupdate.DetectWorkspaceContractDrift(repoDir); len(rem) != 0 {
		t.Fatalf("CDV-UP-04: expected zero remaining drift after sync, got %v", rem)
	}
	finalAgents, rErr := os.ReadFile(agentsPath)
	if rErr != nil || !strings.Contains(string(finalAgents), "Downstream Rule") || strings.Contains(string(finalAgents), "legacy v4.0 block") {
		t.Fatalf("CDV-UP-04: expected protocol refreshed while preserving downstream rule")
	}
}

func verifyCDVUpdateSelectiveStealthSync(t *testing.T) {
	t.Setenv(constants.EnvNomosHome, t.TempDir())
	repoDir := initCleanroomGitRepo(t)

	initCmd := cli.NewRootCmd()
	initCmd.SetOut(new(bytes.Buffer))
	initCmd.SetArgs([]string{"init", "--local", "--agents", "claude", "--dir", repoDir})
	if err := initCmd.Execute(); err != nil {
		t.Fatalf("CDV-UP-05: stealth claude init failed: %v", err)
	}

	claudeSettings := filepath.Join(repoDir, ".claude", "settings.json")
	if wErr := os.WriteFile(claudeSettings, []byte(`{"env":{"CUSTOM":"1"}}`+"\n"), 0644); wErr != nil {
		t.Fatalf("WriteFile claude settings: %v", wErr)
	}
	if len(selfupdate.DetectWorkspaceContractDrift(repoDir)) == 0 {
		t.Fatalf("CDV-UP-05: expected .claude/settings.json drift detected")
	}

	srv := startMockReleaseServer(t, "v"+cli.RootCmdVersion, "nomos_Linux_x86_64.tar.gz", []byte("x"), "00")
	t.Setenv(constants.EnvNomosUpdateAPIURL, srv.URL+"/api/latest")
	t.Setenv(constants.EnvNomosBinaryPath, filepath.Join(t.TempDir(), "nomos"))

	syncCmd := cli.NewRootCmd()
	syncCmd.SetOut(new(bytes.Buffer))
	syncCmd.SetArgs([]string{"update", "--sync-workspace", "--dir", repoDir})
	if err := syncCmd.Execute(); err != nil {
		t.Fatalf("CDV-UP-05: --sync-workspace failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repoDir, ".cursor", "hooks.json")); err == nil {
		t.Fatalf("CDV-UP-05: unselected .cursor/hooks.json leaked during --sync-workspace")
	}
	statusOut, sErr := workspace.SafeGitCommand(repoDir, "status", "--porcelain").CombinedOutput()
	if sErr != nil || strings.TrimSpace(string(statusOut)) != "" {
		t.Fatalf("CDV-UP-05: --local stealth status dirtied: err=%v out=%q", sErr, string(statusOut))
	}
}

func initCleanroomGitRepo(t *testing.T) string {
	t.Helper()
	repoDir := t.TempDir()
	for _, args := range [][]string{{"init"}, {"config", "user.name", "CDV"}, {"config", "user.email", "cdv@nomos.local"}} {
		if out, err := workspace.SafeGitCommand(repoDir, args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, string(out))
		}
	}
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# Stealth\n"), 0644); err != nil {
		t.Fatalf("WriteFile README: %v", err)
	}
	for _, args := range [][]string{{"add", "README.md"}, {"commit", "-m", "initial"}} {
		if out, err := workspace.SafeGitCommand(repoDir, args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, string(out))
		}
	}
	return repoDir
}
