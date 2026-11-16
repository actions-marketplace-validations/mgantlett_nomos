// Package cli implements Tier 1 agent adapters, software physics telemetry,
// release readiness, upgrades, and CLI command dispatch contracts.
package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mgantlett/nomos/substrate/cli/hookcmd"
	"github.com/mgantlett/nomos/substrate/cli/selfupdate"
	"github.com/mgantlett/nomos/substrate/cli/tui"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/constants"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/types"
)

const (
	agentClaude   = "claude"
	agentCursor   = "cursor"
	agentOpenCode = "opencode"
	agentGemini   = "gemini"
	agentAgy      = "agy"
)

var canonicalInitAgents = []string{
	agentClaude,
	agentCursor,
	agentOpenCode,
	agentGemini,
	agentAgy,
}

func resolveInitAgents(absDir, rawFlag string) ([]string, error) {
	if strings.TrimSpace(rawFlag) != "" {
		return parseSelectedAgents(rawFlag)
	}
	return detectInstalledAgents(absDir), nil
}

func parseSelectedAgents(raw string) ([]string, error) {
	seen := make(map[string]bool)
	var selected []string
	for _, part := range strings.Split(raw, ",") {
		tok := strings.ToLower(strings.TrimSpace(part))
		if tok == "" {
			continue
		}
		if tok == "all" {
			return append([]string(nil), canonicalInitAgents...), nil
		}
		if !slices.Contains(canonicalInitAgents, tok) {
			return nil, types.NewNomosError(types.ErrCodeValidationFailed, "unknown agent target %q", tok).
				WithRemedy("valid --agents targets: claude, cursor, opencode, gemini, agy, all")
		}
		if !seen[tok] {
			seen[tok] = true
			selected = append(selected, tok)
		}
	}
	if len(selected) == 0 {
		return nil, types.NewNomosError(types.ErrCodeValidationFailed, "empty --agents filter").
			WithRemedy("pass comma-separated values from: claude, cursor, opencode, gemini, agy, all")
	}
	return selected, nil
}

func detectInstalledAgents(absDir string) []string {
	var detected []string
	for _, agent := range canonicalInitAgents {
		if hasAgentDirSignal(absDir, agent) || hasAgentBinaryOnPath(agent) {
			detected = append(detected, agent)
		}
	}
	if len(detected) == 0 {
		return append([]string(nil), canonicalInitAgents...)
	}
	return detected
}

func detectExistingHookAgents(absDir string) []string {
	var existing []string
	if fileExists(filepath.Join(absDir, claudeDirToken, settingsFileName)) {
		existing = append(existing, agentClaude)
	}
	if fileExists(filepath.Join(absDir, hookcmd.CursorDirToken, hookcmd.HooksFileName)) {
		existing = append(existing, agentCursor)
	}
	if fileExists(filepath.Join(absDir, openCodeDirToken, "plugins", "nomos.ts")) {
		existing = append(existing, agentOpenCode)
	}
	if fileExists(filepath.Join(absDir, geminiDirToken, settingsFileName)) {
		existing = append(existing, agentGemini)
	}
	if fileExists(filepath.Join(absDir, hookcmd.AgentsDirToken, hookcmd.HooksFileName)) {
		existing = append(existing, agentAgy)
	}
	return existing
}

func hasAgentDirSignal(absDir, agent string) bool {
	switch agent {
	case agentClaude:
		return pathExists(filepath.Join(absDir, claudeDirToken)) || fileExists(filepath.Join(absDir, "CLAUDE.md"))
	case agentCursor:
		return pathExists(filepath.Join(absDir, hookcmd.CursorDirToken))
	case agentOpenCode:
		return pathExists(filepath.Join(absDir, openCodeDirToken)) || fileExists(filepath.Join(absDir, "opencode.json"))
	case agentGemini:
		return pathExists(filepath.Join(absDir, geminiDirToken))
	case agentAgy:
		return pathExists(filepath.Join(absDir, hookcmd.AgentsDirToken))
	default:
		return false
	}
}

func hasAgentBinaryOnPath(agent string) bool {
	_, err := exec.LookPath(agent)
	return err == nil
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func containsAgent(agents []string, target string) bool {
	return slices.Contains(agents, target)
}

func scaffoldAmbientHooks(dir string) error {
	existing := detectExistingHookAgents(dir)
	if len(existing) == 0 {
		existing = canonicalInitAgents
	}
	return scaffoldAmbientHooksForSelection(dir, existing)
}

func scaffoldAmbientHooksForSelection(dir string, agents []string) error {
	for _, agent := range agents {
		if err := scaffoldSingleAgentHooks(dir, agent); err != nil {
			return err
		}
	}
	return nil
}

func scaffoldSingleAgentHooks(dir, agent string) error {
	switch agent {
	case agentAgy:
		return writeAmbientHookBundle(filepath.Join(dir, hookcmd.AgentsDirToken))
	case agentClaude:
		return writeMergedJSONManifest(filepath.Join(dir, claudeDirToken, settingsFileName), tui.DefaultClaudeSettingsHooksJSON())
	case agentGemini:
		return writeMergedJSONManifest(filepath.Join(dir, geminiDirToken, settingsFileName), DefaultGeminiSettingsHooksJSON())
	case agentCursor:
		return writeMergedJSONManifest(filepath.Join(dir, hookcmd.CursorDirToken, hookcmd.HooksFileName), hookcmd.DefaultCursorHooksJSON())
	case agentOpenCode:
		return selfupdate.ScaffoldOpenCodePlugin(dir)
	default:
		return nil
	}
}

func pruneLegacyWorkflows(dir string) {
	wfDir := filepath.Join(dir, hookcmd.AgentsDirToken, "workflows")
	legacy := []string{"nomos-handshake.md", "nomos-idle.md", "nomos-review.md"}
	for _, name := range legacy {
		if rErr := os.Remove(filepath.Join(wfDir, name)); rErr != nil && !os.IsNotExist(rErr) {
			// best-effort prune
		}
	}
	if entries, err := os.ReadDir(wfDir); err == nil && len(entries) == 0 {
		if rErr := os.Remove(wfDir); rErr != nil && !os.IsNotExist(rErr) {
			// best-effort directory prune
		}
	}
}

func writeAmbientHookBundle(rootDir string) error {
	scriptsDir := filepath.Join(rootDir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		return err
	}
	manifestBytes, rErr := initTemplates.ReadFile("templates/hooks/hooks.json")
	if rErr != nil {
		return rErr
	}
	manifestPath := filepath.Join(rootDir, "hooks.json")
	if rel, relErr := filepath.Rel(rootDir, manifestPath); relErr != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("unconfined hook manifest path: %s", manifestPath)
	}
	if mErr := scaffoldOrMergeHookManifestFile(manifestPath, manifestBytes); mErr != nil {
		return mErr
	}
	scripts := []string{"session-start.sh", "worktree-guard.sh", "nomos-build-guard.sh", "post-edit-radar.sh", "stop-guard.sh"}
	for _, name := range scripts {
		if wErr := writeHookBundleScript(scriptsDir, name); wErr != nil {
			return wErr
		}
	}
	return nil
}

func writeHookBundleScript(scriptsDir, name string) error {
	targetPath := filepath.Join(scriptsDir, name)
	tmplBytes, err := initTemplates.ReadFile("templates/hooks/scripts/" + name)
	if err != nil {
		return err
	}
	if err := os.WriteFile(targetPath, tmplBytes, 0755); err != nil {
		return err
	}
	return os.Chmod(targetPath, 0755)
}

func scaffoldOrMergeHookManifestFile(targetPath string, templateBytes []byte) error {
	existingBytes, err := os.ReadFile(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return os.WriteFile(targetPath, templateBytes, 0644)
		}
		return err
	}
	mergedBytes, mErr := tui.MergeHookManifest(existingBytes, templateBytes)
	if mErr != nil {
		return mErr
	}
	return os.WriteFile(targetPath, mergedBytes, 0644)
}

func summaryWriter(opts InitOptions) io.Writer {
	if opts.Out != nil {
		return opts.Out
	}
	return os.Stdout
}

func printInitializationSummary(info initSummaryData) {
	out := summaryWriter(info.opts)
	check := tui.UiSuccessStyle.Render("✅")
	modeLabel := resolveInitModeLabel(info.opts)

	writeSummaryLine(out, "%s %s\n", tui.RenderBrandTitle("🚀 Initialized Nomos workspace in"), tui.UiBoldValueStyle.Render(info.absDir))
	writeSummaryLine(out, "%s\n", tui.UiRule(tui.RuleNarrow))
	writeSummaryLine(out, "  %s Governance mode: %s\n", check, tui.UiBoldValueStyle.Render(modeLabel))
	writeSummaryLine(out, "  %s Configuration & protocol: %s\n", check, tui.UiCommandStyle.Render("nomos.yaml, AGENTS.md"))
	writeSummaryLine(out, "  %s Agent hooks: %s\n", check, tui.UiMutedStyle.Render(strings.Join(info.selectedAgents, ", ")))
	if info.wroteBaseline && info.preInitXray != nil {
		writeSummaryLine(out, "  %s Baseline locked %s\n", check, tui.UiCommandStyle.Render(fmt.Sprintf("(%s · score %d/100 · grade %s)", constants.FileNomosBaselineJSON, info.preInitXray.HangoverScore, info.preInitXray.Grade)))
	}
	if info.createdBaselineCommit {
		writeSummaryLine(out, "  %s Created baseline governance commit on %s\n", check, tui.UiBoldValueStyle.Render(info.defaultBranch))
	}
	printGitAndNextGuidance(out, check, info)
}

func resolveInitModeLabel(opts InitOptions) string {
	if opts.Worktrees && opts.Local {
		return "Hermetic Worktrees (Local Stealth)"
	}
	if opts.Worktrees {
		return "Hermetic Worktrees"
	}
	if opts.Local {
		return "Branch Guard (Local Stealth)"
	}
	return "Branch Guard"
}

func printGitAndNextGuidance(out io.Writer, check string, info initSummaryData) {
	if info.isGitRepo {
		writeSummaryLine(out, "  %s Installed git defense hooks %s\n", check, tui.UiMutedStyle.Render("(pre-commit, commit-msg, post-commit, pre-push)"))
	} else {
		writeSummaryLine(out, "  %s\n", tui.UiWarnStyle.Render("⚠️ Git defense hooks not installed (run 'git init' to enable)"))
	}
	writeSummaryLine(out, "  %s Living Ledger & Mneme storage connected\n", check)
	writeSummaryLine(out, "%s\n", tui.UiRule(tui.RuleNarrow))
	if !info.opts.Worktrees {
		writeSummaryLine(out, "  %s\n", tui.UiSuccessStyle.Render("✨ Ready on normal branches. Switch to a feature branch: git switch -c feat/<topic>"))
		return
	}
	if info.createdBaselineCommit {
		writeSummaryLine(out, "  %s\n", tui.UiSuccessStyle.Render("✨ Workspace ready for Tier 1 agent indirect control."))
		return
	}
	writeSummaryLine(out, "  %s\n", tui.UiSuccessStyle.Render("✨ Workspace ready. Provision an isolated task via nomos task chore \"<title>\"."))
}

func writeSummaryLine(out io.Writer, format string, args ...any) {
	if _, err := fmt.Fprintf(out, format, args...); err != nil {
		// non-fatal output write failure
	}
}
