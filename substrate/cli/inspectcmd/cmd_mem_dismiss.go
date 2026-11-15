// Package inspectcmd implements Tier 1 agent adapters, software physics telemetry,
// release readiness, upgrades, and CLI command dispatch contracts.
package inspectcmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mgantlett/nomos/substrate/cli/tui"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/config"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/types"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/mem"
	"github.com/spf13/cobra"
)

type dismissOpts struct {
	stale      bool
	project    string
	jsonOutput bool
}

func newMemDismissCmd() *cobra.Command {
	var opts dismissOpts

	cmd := &cobra.Command{
		Use:     "dismiss [id...]",
		Short:   "Dismiss graduated or obsolete Nomos Mneme memory records",
		Long:    tui.HelpMemDismissLong,
		Example: tui.HelpMemDismissExample,
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMemDismissWithOpts(cmd.OutOrStdout(), args, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.stale, "stale", false, "Sweep and tombstone orphaned filesystem-scoped pitfalls and patterns")
	cmd.Flags().StringVarP(&opts.project, tui.FlagNameProject, "p", "", "Repository path or identifier override when paired with --stale")
	cmd.Flags().BoolVar(&opts.jsonOutput, tui.FlagNameJSON, false, "Emit dismissed memory records as structured JSON")
	return cmd
}

func runMemDismissWithOpts(out io.Writer, rawArgs []string, opts dismissOpts) error {
	if opts.stale {
		if len(rawArgs) > 0 {
			return types.NewNomosError(types.ErrCodeValidationFailed, "cannot combine explicit memory IDs with --stale")
		}
		return runMemDismissStale(out, opts.project, opts.jsonOutput)
	}
	return runMemDismiss(out, rawArgs, opts.jsonOutput)
}

func runMemDismissStale(out io.Writer, project string, jsonOutput bool) error {
	repoRoot, repoID, err := resolveDismissStaleTarget(project)
	if err != nil {
		return err
	}
	store, err := mem.NewStore(config.ResolveMnemeDBPath())
	if err != nil {
		return err
	}
	defer func() {
		if cErr := store.Close(); cErr != nil {
			// closed
		}
	}()

	dismissed := mem.DismissStaleRepoMemories(store, repoRoot, repoID, time.Now().UTC())
	if _, mErr := store.Maintain(mem.MaintainOptions{}); mErr != nil {
		// best-effort compaction
	}
	if dismissed == nil {
		dismissed = []*mem.Memory{}
	}
	if jsonOutput {
		return writeDismissedJSON(out, dismissed)
	}
	if len(dismissed) == 0 {
		fmt.Fprintf(out, "✅ %s in %s\n",
			tui.UiSuccessStyle.Render("Zero stale filesystem-scoped memories found"),
			tui.UiSubstrateStyle.Render(repoID))
		return nil
	}
	renderDismissedReceipts(out, dismissed)
	return nil
}

func resolveDismissStaleTarget(project string) (string, string, error) {
	trimmed := strings.TrimSpace(project)
	if trimmed == "" {
		if repoRoot, err := workspace.FindRepoRoot(""); err == nil {
			clean := filepath.Clean(repoRoot)
			return clean, filepath.Base(clean), nil
		}
		cwd, err := os.Getwd()
		if err != nil {
			return "", "", types.NewNomosError(types.ErrCodeValidationFailed, "unable to resolve repository root: %v", err)
		}
		clean := filepath.Clean(cwd)
		return clean, filepath.Base(clean), nil
	}
	if info, err := os.Stat(trimmed); err == nil && info.IsDir() {
		abs, aErr := filepath.Abs(trimmed)
		if aErr == nil {
			return abs, filepath.Base(abs), nil
		}
	}
	if curRoot, err := workspace.FindRepoRoot(""); err == nil {
		cleanCur := filepath.Clean(curRoot)
		if strings.EqualFold(filepath.Base(cleanCur), trimmed) {
			return cleanCur, filepath.Base(cleanCur), nil
		}
		sibling := filepath.Join(filepath.Dir(cleanCur), trimmed)
		if info, sErr := os.Stat(sibling); sErr == nil && info.IsDir() {
			return sibling, filepath.Base(sibling), nil
		}
	}
	return "", "", types.NewNomosError(types.ErrCodeValidationFailed, "repository checkout not found for %q", trimmed).
		WithRemedy("pass an active repository checkout directory or run inside the target repository")
}

func runMemDismiss(out io.Writer, rawArgs []string, jsonOutput bool) error {
	ids, err := parseMemoryDismissIDs(rawArgs)
	if err != nil {
		return err
	}

	store, err := mem.NewStore(config.ResolveMnemeDBPath())
	if err != nil {
		return err
	}
	defer func() {
		if cErr := store.Close(); cErr != nil {
			// closed
		}
	}()

	dismissed := make([]*mem.Memory, 0, len(ids))
	for _, id := range ids {
		rec, dErr := store.Dismiss(id)
		if dErr != nil {
			return dErr
		}
		dismissed = append(dismissed, rec)
	}

	if jsonOutput {
		return writeDismissedJSON(out, dismissed)
	}
	renderDismissedReceipts(out, dismissed)
	return nil
}

func writeDismissedJSON(out io.Writer, dismissed []*mem.Memory) error {
	data, mErr := json.MarshalIndent(dismissed, "", "  ")
	if mErr != nil {
		return types.NewNomosError(types.ErrCodeCore, "failed marshaling dismissed memories JSON: %v", mErr)
	}
	fmt.Fprintln(out, string(data))
	return nil
}

func renderDismissedReceipts(out io.Writer, dismissed []*mem.Memory) {
	for _, rec := range dismissed {
		fmt.Fprintf(out, "🗃️  %s #%d [%s/%s] (%s)\n",
			tui.UiSuccessStyle.Render("Dismissed memory"),
			rec.ID,
			tui.UiSubstrateStyle.Render(rec.RepoID),
			tui.UiValueStyle.Render(rec.Topic),
			tui.UiMutedStyle.Render(string(rec.Status)))
	}
}

func parseMemoryDismissIDs(rawArgs []string) ([]int, error) {
	seen := make(map[int]bool, len(rawArgs))
	var ids []int
	for _, raw := range rawArgs {
		for _, tok := range strings.Split(raw, ",") {
			clean := strings.TrimPrefix(strings.TrimSpace(tok), "#")
			if clean == "" {
				continue
			}
			id, err := strconv.Atoi(clean)
			if err != nil || id <= 0 {
				return nil, types.NewNomosError(types.ErrCodeValidationFailed, "invalid memory ID %q: must be a positive integer", tok).
					WithRemedy("run 'nomos mem list' to inspect valid numeric memory IDs")
			}
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return nil, types.NewNomosError(types.ErrCodeValidationFailed, "at least one numeric memory ID or --stale is required")
	}
	return ids, nil
}

func resolveMemListStatusFilter(raw string) (mem.MemoryStatus, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(mem.StatusActive):
		return mem.StatusActive, nil
	case string(mem.StatusSuperseded):
		return mem.StatusSuperseded, nil
	case string(mem.StatusTombstoned):
		return mem.StatusTombstoned, nil
	case "all":
		return "", nil
	default:
		return "", types.NewNomosError(types.ErrCodeValidationFailed,
			"invalid memory status %q: expected active, superseded, tombstoned, or all", raw)
	}
}
