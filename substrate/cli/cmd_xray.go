// Package cli implements Tier 1 agent adapters, software physics telemetry,
// release readiness, upgrades, and CLI command dispatch contracts.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mgantlett/nomos/substrate/cli/tui"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/types"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/sarif"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/xray"
	"github.com/spf13/cobra"
)

// NewXrayCmd constructs the zero-init read-only 'nomos xray' command (@intent task_lifecycle_cli_spec.md).
func NewXrayCmd() *cobra.Command {
	var jsonOutput, markdownOutput, sarifOutput bool
	var topHotspots int

	cmd := &cobra.Command{
		Use:     "xray [dir]",
		Short:   "Run zero-init read-only brownfield AI-hangover & structural debt diagnostics",
		Long:    tui.HelpXrayLong,
		Example: tui.HelpXrayExample,
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateXrayOutputFlags(jsonOutput, markdownOutput, sarifOutput); err != nil {
				return err
			}
			targetDir, err := resolveXrayTargetDir(args)
			if err != nil {
				return err
			}
			return executeXray(cmd.OutOrStdout(), targetDir, topHotspots, jsonOutput, markdownOutput, sarifOutput)
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, tui.FlagNameJSON, false, "Emit structured XrayReport as JSON")
	cmd.Flags().BoolVar(&markdownOutput, "markdown", false, "Emit shareable GitHub Markdown diagnostic card")
	cmd.Flags().BoolVar(&sarifOutput, "sarif", false, "Emit OASIS SARIF 2.1.0 JSON report for GitHub Code Scanning")
	cmd.Flags().IntVar(&topHotspots, "top", xray.DefaultTopHotspots, "Maximum number of ranked structural hotspots to display")
	return cmd
}

func validateXrayOutputFlags(jsonOut, mdOut, sarifOut bool) error {
	count := 0
	for _, set := range []bool{jsonOut, mdOut, sarifOut} {
		if set {
			count++
		}
	}
	if count > 1 {
		return types.NewNomosError(types.ErrCodeValidationFailed, "cannot combine --json, --markdown, and --sarif on nomos xray").
			WithRemedy("specify at most one output flag (--json, --markdown, or --sarif)")
	}
	return nil
}

func resolveXrayTargetDir(args []string) (string, error) {
	target := "."
	if len(args) > 0 && args[0] != "" {
		target = args[0]
	} else if cwd, err := os.Getwd(); err == nil {
		target = cwd
	}
	absDir, err := filepath.Abs(target)
	if err != nil {
		return "", types.NewNomosError(types.ErrCodeBoundary, "failed resolving target directory %s: %v", target, err)
	}
	return absDir, nil
}

func executeXray(out io.Writer, targetDir string, topHotspots int, jsonOutput, markdownOutput, sarifOutput bool) error {
	rep, err := xray.ScanRepository(targetDir, xray.XrayScanOptions{TopHotspots: topHotspots})
	if err != nil {
		return types.NewNomosError(types.ErrCodeValidationFailed, "xray scan failed: %v", err).
			WithRemedy("pass an existing repository or source directory to 'nomos xray [dir]'")
	}
	if jsonOutput {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	if sarifOutput {
		raw, sErr := sarif.MarshalIndent(sarif.FromXrayReport(rep, RootCmdVersion))
		if sErr != nil {
			return sErr
		}
		_, wErr := fmt.Fprintln(out, string(raw))
		return wErr
	}
	if markdownOutput {
		_, wErr := fmt.Fprint(out, xray.FormatMarkdown(rep))
		return wErr
	}
	_, wErr := fmt.Fprint(out, tui.RenderXrayTerminal(rep))
	return wErr
}
