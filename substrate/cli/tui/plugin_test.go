package tui_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/cli"
	"github.com/mgantlett/nomos/substrate/cli/tui"
	"github.com/spf13/cobra"
)

func TestPluginDiscovery_NeutralBrandingAndNoCoreShadowing(t *testing.T) {
	isolatedRepo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(isolatedRepo, ".git"), 0755); err != nil {
		t.Fatalf("MkdirAll .git: %v", err)
	}
	nomosYAML := "project: demo\ndefault_branch: main\ngovernance:\n  boundary:\n    enforce: true\n    prohibited_commands:\n      - \"blocked\"\n"
	if err := os.WriteFile(filepath.Join(isolatedRepo, "nomos.yaml"), []byte(nomosYAML), 0644); err != nil {
		t.Fatalf("WriteFile nomos.yaml: %v", err)
	}
	origWD, wdErr := os.Getwd()
	if wdErr != nil {
		t.Fatalf("Getwd: %v", wdErr)
	}
	if err := os.Chdir(isolatedRepo); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	pluginDir := t.TempDir()
	customPlugin := filepath.Join(pluginDir, "nomos-plugin-demo")
	shadowPlugin := filepath.Join(pluginDir, "nomos-plugin-verify")
	blockedPlugin := filepath.Join(pluginDir, "nomos-plugin-blocked")
	for _, p := range []string{customPlugin, shadowPlugin, blockedPlugin} {
		if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
			t.Fatalf("WriteFile %s: %v", p, err)
		}
	}
	t.Setenv("NOMOS_PLUGINS_ROOT", pluginDir)

	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("root --help failed: %v", err)
	}
	helpText := out.String()
	if !strings.Contains(helpText, "External Plugins:") || !strings.Contains(helpText, "External plugin: demo") {
		t.Fatalf("expected neutral External Plugins group in help, got:\n%s", helpText)
	}
	if strings.Contains(helpText, "Sovereign") || strings.Contains(helpText, "ADR-0031") {
		t.Fatalf("expected zero Sovereign/ADR-0031 branding on community plugins, got:\n%s", helpText)
	}
	if strings.Contains(helpText, "External plugin: verify") {
		t.Fatalf("Law 6 violation: plugin must never shadow core 'verify' command")
	}
	if strings.Contains(helpText, "External plugin: blocked") {
		t.Fatalf("expected prohibited_commands plugin 'blocked' to be omitted from help, got:\n%s", helpText)
	}

	filteredRoot := &cobra.Command{Use: "nomos"}
	tui.DiscoverPlugins(filteredRoot, []string{"blocked", "demo", "verify"})
	var filteredOut bytes.Buffer
	filteredRoot.SetOut(&filteredOut)
	filteredRoot.SetArgs([]string{"--help"})
	if err := filteredRoot.Execute(); err != nil {
		t.Fatalf("filteredRoot --help failed: %v", err)
	}
	if strings.Contains(filteredOut.String(), "External Plugins:") {
		t.Fatalf("expected all prohibited plugins omitted without empty External Plugins group, got:\n%s", filteredOut.String())
	}
}
