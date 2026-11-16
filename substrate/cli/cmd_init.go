// Package cli implements Tier 1 agent adapters, software physics telemetry,
// release readiness, upgrades, and CLI command dispatch contracts.
package cli

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mgantlett/nomos/substrate/cli/hookcmd"
	"github.com/mgantlett/nomos/substrate/cli/selfupdate"
	"github.com/mgantlett/nomos/substrate/cli/tui"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/config"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/constants"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/storage"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/types"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/handshake"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/mem"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/xray"
	"github.com/spf13/cobra"
	"io"
)

//go:embed templates/* templates/hooks/* templates/hooks/scripts/*
var initTemplates embed.FS

// InitOptions parameterizes workspace initialization across Branch Guard, Worktrees, CI, and stealth trial modes (@intent task_lifecycle_cli_spec.md).
type InitOptions struct {
	Worktrees  bool
	Local      bool
	Strict     bool
	Baseline   bool
	CI         bool
	Agents     string
	LegacyInit bool
	Out        io.Writer
}

type initSummaryData struct {
	absDir                string
	defaultBranch         string
	opts                  InitOptions
	selectedAgents        []string
	isGitRepo             bool
	createdBaselineCommit bool
	preInitXray           *xray.XrayReport
	wroteBaseline         bool
	ciFiles               []string
}

// NewInitCmd creates the workspace initialization command.
func NewInitCmd() *cobra.Command {
	var targetDir string
	var opts InitOptions

	cmd := &cobra.Command{
		Use:     "init",
		Short:   "Initialize Nomos governance with Branch Guard, auto-baseline, or isolated worktrees",
		Long:    tui.HelpInitLong,
		Example: tui.HelpInitExample,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := targetDir
			if dir == "" {
				cwd, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("failed resolving current working directory: %w", err)
				}
				dir = cwd
			}
			opts.Out = cmd.OutOrStdout()
			return executeInitWithOptions(dir, opts)
		},
	}

	cmd.Flags().StringVarP(&targetDir, "dir", "d", "", "Target directory to initialize (defaults to current directory)")
	cmd.Flags().BoolVar(&opts.Baseline, "baseline", false, "Snapshot Day-0 brownfield structural debt bounds into nomos.baseline.json")
	cmd.Flags().BoolVar(&opts.Worktrees, "worktrees", false, "Enable isolated git worktrees and formal INT/SUB lifecycle instead of Branch Guard")
	cmd.Flags().BoolVar(&opts.Local, "local", false, "Register all Nomos files in .git/info/exclude for a zero-footprint local stealth trial")
	cmd.Flags().BoolVar(&opts.Strict, "strict", false, "Enforce strict_mode: true without automatic brownfield baseline relaxation")
	cmd.Flags().BoolVar(&opts.CI, "ci", false, "Generate offline GitHub Actions and GitLab CI workflows running nomos verify")
	cmd.Flags().StringVar(&opts.Agents, "agents", "", "Comma-separated agent targets (claude, cursor, opencode, gemini, agy, or all)")
	return cmd
}

func executeInit(dir string) error {
	return executeInitWithOptions(dir, InitOptions{
		Worktrees:  true,
		Agents:     "all",
		LegacyInit: true,
	})
}

func executeInitWithOptions(dir string, opts InitOptions) error {
	absDir, err := validateInitPreconditions(dir, opts)
	if err != nil {
		return err
	}
	selectedAgents, aErr := resolveInitAgents(absDir, opts.Agents)
	if aErr != nil {
		return aErr
	}
	preInitXray, shouldBaseline, scanErr := resolveInitBaseline(absDir, opts)
	if scanErr != nil {
		return scanErr
	}
	if err := scaffoldGovernanceForOptions(absDir, opts, selectedAgents, shouldBaseline, preInitXray); err != nil {
		return err
	}
	ciFiles, ciErr := scaffoldCIWorkflowsIfRequested(absDir, opts)
	if ciErr != nil {
		return ciErr
	}

	isGitRepo, createdCommit := initializeGitGovernanceWithOptions(absDir, opts)
	if dbErr := verifyDatabaseConnectivity(); dbErr != nil {
		return dbErr
	}
	seedDayZeroBrownfieldMemory(absDir)

	printInitializationSummary(initSummaryData{
		absDir:                absDir,
		defaultBranch:         detectDefaultBranch(absDir),
		opts:                  opts,
		selectedAgents:        selectedAgents,
		isGitRepo:             isGitRepo,
		createdBaselineCommit: createdCommit,
		preInitXray:           preInitXray,
		wroteBaseline:         shouldBaseline,
		ciFiles:               ciFiles,
	})
	return nil
}

func scaffoldCIWorkflowsIfRequested(absDir string, opts InitOptions) ([]string, error) {
	if !opts.CI {
		return nil, nil
	}
	return selfupdate.ScaffoldCIWorkflows(absDir, detectDefaultBranch(absDir))
}

func validateInitPreconditions(dir string, opts InitOptions) (string, error) {
	if opts.Baseline && opts.Strict {
		return "", types.NewNomosError(types.ErrCodeValidationFailed, "cannot combine --baseline and --strict on nomos init").
			WithRemedy("omit --baseline to enforce strict_mode: true, or omit --strict to lock a brownfield baseline")
	}
	if opts.CI && opts.Local {
		return "", types.NewNomosError(types.ErrCodeValidationFailed, "cannot combine --ci and --local on nomos init").
			WithRemedy("omit --local to commit CI workflow files, or omit --ci for a stealth local trial")
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", types.NewNomosError(types.ErrCodeBoundary, "failed resolving absolute path for %s: %v", dir, err)
	}
	if isProtectedToolchainTarget(absDir) {
		return "", types.NewNomosError(types.ErrCodeBoundary, "refusing to execute nomos init against release-governed repository %s inside toolchain gate", absDir)
	}
	if opts.Local {
		if _, gErr := resolveGitExcludeFile(absDir); gErr != nil {
			return "", gErr
		}
	}
	return absDir, nil
}

func resolveInitBaseline(absDir string, opts InitOptions) (*xray.XrayReport, bool, error) {
	if opts.Strict {
		return nil, false, nil
	}
	if !opts.Baseline && opts.LegacyInit {
		return nil, false, nil
	}
	rep, err := xray.ScanRepository(absDir, xray.DefaultScanOptions())
	if err != nil {
		if opts.Baseline {
			return nil, false, types.NewNomosError(types.ErrCodeSubstrate, "failed scanning Day-0 baseline: %v", err)
		}
		return nil, false, nil
	}
	shouldBaseline := opts.Baseline || (rep != nil && rep.HangoverScore > 0)
	return rep, shouldBaseline, nil
}

func scaffoldGovernanceForOptions(absDir string, opts InitOptions, selectedAgents []string, shouldBaseline bool, preInitXray *xray.XrayReport) error {
	if err := applyInitBoundaries(absDir, opts); err != nil {
		return err
	}
	forceBrownfield := shouldBaseline && preInitXray != nil && preInitXray.HangoverScore > 0
	if err := scaffoldNomosConfigForOptions(absDir, opts, forceBrownfield); err != nil {
		return types.NewNomosError(types.ErrCodeSubstrate, "failed scaffolding nomos.yaml: %v", err)
	}
	if err := scaffoldProtocolAndHooksForOptions(absDir, opts, selectedAgents); err != nil {
		return err
	}
	if shouldBaseline && preInitXray != nil {
		if _, wErr := xray.WriteBaselineManifest(absDir, preInitXray); wErr != nil {
			return types.NewNomosError(types.ErrCodeSubstrate, "failed writing %s: %v", constants.FileNomosBaselineJSON, wErr)
		}
	}
	return nil
}

func scaffoldProtocolAndHooksForOptions(absDir string, opts InitOptions, selectedAgents []string) error {
	if err := selfupdate.ScaffoldAgentsConfigForMode(absDir, !opts.Worktrees); err != nil {
		return types.NewNomosError(types.ErrCodeSubstrate, "failed scaffolding AGENTS.md: %v", err)
	}
	if containsAgent(selectedAgents, agentClaude) || fileExists(filepath.Join(absDir, "CLAUDE.md")) {
		if err := selfupdate.ScaffoldClaudeConfig(absDir); err != nil {
			return types.NewNomosError(types.ErrCodeSubstrate, "failed scaffolding CLAUDE.md: %v", err)
		}
	}
	if containsAgent(selectedAgents, agentAgy) || pathExists(filepath.Join(absDir, hookcmd.AgentsDirToken, "workflows")) {
		pruneLegacyWorkflows(absDir)
	}
	if err := scaffoldAmbientHooksForSelection(absDir, selectedAgents); err != nil {
		return types.NewNomosError(types.ErrCodeSubstrate, "failed scaffolding ambient lifecycle hooks: %v", err)
	}
	return nil
}

func applyInitBoundaries(absDir string, opts InitOptions) error {
	if opts.Local {
		return syncLocalStealthExcludes(absDir, opts.Worktrees)
	}
	if err := clearLocalStealthExcludes(absDir); err != nil {
		return err
	}
	if err := selfupdate.ScaffoldNomosBoundariesForMode(absDir, opts.Worktrees, false); err != nil {
		return types.NewNomosError(types.ErrCodeSubstrate, "failed scaffolding workspace boundaries: %v", err)
	}
	return nil
}

func scaffoldNomosConfigForOptions(dir string, opts InitOptions, forceBrownfield bool) error {
	configPath := filepath.Join(dir, constants.FileNomosYAML)
	if existingRaw, err := os.ReadFile(configPath); err == nil {
		if opts.Worktrees {
			updated := selfupdate.ApplyIsolationMode(string(existingRaw), config.IsolationModeWorktree)
			if updated != string(existingRaw) {
				return os.WriteFile(configPath, []byte(updated), 0644)
			}
		}
		return nil
	}
	tmplBytes, err := initTemplates.ReadFile("templates/nomos.yaml")
	if err != nil {
		return err
	}
	isolationMode := config.IsolationModeBranch
	if opts.Worktrees {
		isolationMode = config.IsolationModeWorktree
	}
	content := selfupdate.RenderScaffoldedNomosYAMLConfigured(dir, selfupdate.NomosYAMLRenderOptions{
		RawTemplate:     string(tmplBytes),
		ProjectName:     filepath.Base(dir),
		Branch:          detectDefaultBranch(dir),
		IsolationMode:   isolationMode,
		ForceBrownfield: forceBrownfield,
		ForceStrict:     opts.Strict,
	})
	return os.WriteFile(configPath, []byte(content), 0644)
}

func initializeGitGovernanceWithOptions(absDir string, opts InitOptions) (bool, bool) {
	gitDir := filepath.Join(absDir, constants.DirGit)
	fi, err := os.Stat(gitDir)
	if err != nil || !fi.IsDir() {
		writeSummaryLine(summaryWriter(opts), "%s %s is not a git repository. Run %s to enable Git defense hooks.\n",
			tui.UiWarnStyle.Render("⚠️"),
			tui.UiBoldValueStyle.Render(absDir),
			tui.UiCommandStyle.Render("'git init'"))
		return false, false
	}

	var createdCommit bool
	if opts.Worktrees && !opts.Local {
		var stageErr error
		createdCommit, stageErr = stageAndCommitGovernanceOnly(absDir)
		if stageErr != nil {
			writeSummaryLine(summaryWriter(opts), "%s failed staging governance files: %v\n", tui.UiWarnStyle.Render("⚠️"), stageErr)
		}
	}

	if hookErr := workspace.InstallHooks(absDir); hookErr != nil {
		writeSummaryLine(summaryWriter(opts), "%s failed installing git defense hooks: %v\n", tui.UiWarnStyle.Render("⚠️"), hookErr)
	}
	return true, createdCommit
}

func verifyDatabaseConnectivity() error {
	db, err := storage.OpenDB("")
	if err != nil {
		return types.NewNomosError(types.ErrCodeCore, "failed connecting to Living Ledger database: %v", err)
	}
	if cErr := db.Close(); cErr != nil {
		// safe to continue
	}
	return nil
}

// seedDayZeroBrownfieldMemory persists a Day-0 Mneme architecture baseline for brownfield repositories (@intent task_lifecycle_cli_spec.md).
func seedDayZeroBrownfieldMemory(absDir string) {
	cfg, err := config.LoadConfig(absDir)
	if err != nil || cfg == nil || cfg.Governance.QualityGates.StrictMode {
		return
	}
	bf := handshake.SynthesizeBrownfieldTopology(absDir, cfg)
	summary := handshake.FormatBrownfieldMemorySummary(filepath.Base(absDir), bf)
	if summary == "" {
		return
	}
	if sErr := mem.SeedRepositoryArchitectureBaseline(absDir, summary); sErr != nil {
		// best-effort cold-start memory warm-up
	}
}

func detectDefaultBranch(dir string) string {
	cmd := workspace.SafeGitCommand(dir, "branch", "--show-current")
	if out, err := cmd.Output(); err == nil {
		b := strings.TrimSpace(string(out))
		if b != "" && b != "HEAD" {
			return b
		}
	}
	symCmd := workspace.SafeGitCommand(dir, "symbolic-ref", "--short", "HEAD")
	if out, err := symCmd.Output(); err == nil {
		b := strings.TrimSpace(string(out))
		if b != "" {
			return b
		}
	}
	cfgCmd := workspace.SafeGitCommand(dir, "config", "--get", "init.defaultBranch")
	if out, err := cfgCmd.Output(); err == nil {
		b := strings.TrimSpace(string(out))
		if b != "" {
			return b
		}
	}
	return constants.ValMain
}
