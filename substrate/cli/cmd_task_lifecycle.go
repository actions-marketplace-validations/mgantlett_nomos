// Package cli implements Tier 1 agent adapters, software physics telemetry,
// release readiness, upgrades, and CLI command dispatch contracts.
package cli

import (
	"fmt"
	"github.com/mgantlett/nomos/substrate/cli/taskui"
	"github.com/mgantlett/nomos/substrate/cli/tui"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/capbac"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/storage"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/types"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/mem"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/task"
	"github.com/spf13/cobra"
)

type taskCreateOpts struct {
	plane     string
	tier      string
	customKey string
	twinOf    string
	priority  int
	card      tui.TaskCardFlags
	stdin     io.Reader
}

func newTaskCreateCmd() *cobra.Command {
	var opts taskCreateOpts

	cmd := &cobra.Command{
		Use:     "create <title>",
		Short:   "Allocate and persist a new Living Ledger task record",
		Long:    tui.HelpTaskCreateLong,
		Example: tui.HelpTaskCreateExample,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.stdin = cmd.InOrStdin()
			return executeTaskCreate(args[0], opts)
		},
	}
	cmd.Flags().StringVarP(&opts.plane, tui.FlagNamePlane, "p", "substrate", "Target plane (intent|substrate)")
	cmd.Flags().StringVarP(&opts.tier, tui.FlagNameTier, "t", "T1", "Cognitive tier (T1|T2)")
	cmd.Flags().IntVarP(&opts.priority, "priority", "r", 50, "Priority (0-100)")
	cmd.Flags().StringVarP(&opts.customKey, "key", "k", "", "Explicit task key (e.g. SUB-1212)")
	cmd.Flags().StringVar(&opts.twinOf, "twin-of", "", "Twin task key in opposite plane to link with")
	tui.BindTaskCardFlags(cmd, &opts.card, false)
	return cmd
}

func executeTaskCreate(title string, opts taskCreateOpts) error {
	opts.plane = tui.NormalizePlaneInput(opts.plane, opts.customKey)
	ir, err := tui.ResolveCardIR(opts.stdin, "create", title, opts.plane, opts.card)
	if err != nil {
		return err
	}
	store, err := task.NewDefaultSQLiteTaskStore()
	if err != nil {
		return err
	}
	key, err := resolveOrAllocateCreateKey(store, opts.plane, opts.customKey)
	if err != nil {
		return err
	}
	deps, err := tui.ResolveValidatedDependencies(store, opts.card.DependsOn, key)
	if err != nil {
		return err
	}
	twinOf := strings.ToUpper(strings.TrimSpace(opts.twinOf))
	if twinOf != "" {
		if err := task.ValidateTaskKey(twinOf); err != nil {
			return fmt.Errorf("invalid twin task key: %w", err)
		}
	}

	t := task.TaskRecord{
		Key: key, Plane: opts.plane, Project: taskui.ResolveActiveTaskProject(opts.card.Project),
		SchemaType: task.SchemaType(opts.plane), Title: title, Description: ir.Body,
		ScopePaths: ir.ScopePaths, Obligations: ir.Obligations,
		AcceptanceCriteria: ir.AcceptanceCriteria, VerifyCmd: ir.VerifyCmd,
		Status: task.StatusTodo, Tier: task.Tier(opts.tier), Priority: opts.priority,
		TwinKey: twinOf, Dependencies: deps, Burden: ir.Burden,
	}
	if err := store.CreateTask(t); err != nil {
		return err
	}
	if twinOf != "" {
		if err := store.BindTwin(key, twinOf); err != nil {
			return fmt.Errorf("failed linking twin %s: %w", twinOf, err)
		}
	}
	if opts.card.JSONOut {
		return emitStructuredJSON(os.Stdout, t)
	}
	renderCreatedTaskSummary(key, opts.plane, title, twinOf, deps)
	return nil
}

func renderCreatedTaskSummary(key, plane, title, twinOf string, deps []string) {
	if twinOf != "" {
		fmt.Printf("✨ %s %s (%s) - %s (twinned with %s)\n",
			tui.UiSuccessStyle.Render("Created task:"), tui.StyleTaskKey(key), tui.StylePlane(plane),
			tui.UiBoldValueStyle.Render(title), tui.StyleTaskKey(twinOf))
	} else {
		fmt.Printf("✨ %s %s (%s) - %s\n",
			tui.UiSuccessStyle.Render("Created task:"), tui.StyleTaskKey(key), tui.StylePlane(plane),
			tui.UiBoldValueStyle.Render(title))
	}
	if len(deps) > 0 {
		fmt.Printf("  %s %s %s\n", tui.UiWarnStyle.Render("Blocked By:"),
			strings.Join(deps, ", "), tui.UiMutedStyle.Render("(auto-promotes to wavefront once DONE)"))
	} else {
		fmt.Printf("  %s %s\n", tui.UiLabelStyle.Render("Next:"),
			tui.UiCommandStyle.Render(fmt.Sprintf("nomos task start %s --edit", key)))
	}
}

func resolveOrAllocateCreateKey(store task.TaskStore, plane, customKey string) (string, error) {
	if customKey == "" {
		return allocateNextKey(store, plane)
	}
	key := strings.ToUpper(strings.TrimSpace(customKey))
	if err := task.ValidateTaskKey(key); err != nil {
		return "", err
	}
	return key, nil
}

func newTaskStartCmd() *cobra.Command {
	var autoEdit, forceProject, jsonOut bool
	cmd := &cobra.Command{
		Use:     "start <key>",
		Short:   "Provision transient worktree and CapBAC lease for task",
		Long:    tui.HelpTaskStartLong,
		Example: tui.HelpTaskStartExample,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTaskStartWithJSON(args[0], autoEdit, forceProject, jsonOut)
		},
	}
	cmd.Flags().BoolVarP(&autoEdit, "edit", "e", false, "Immediately transition worktree into EDIT phase")
	cmd.Flags().BoolVar(&forceProject, "force-project", false, "Allow starting a task stamped with a different repository project")
	cmd.Flags().BoolVar(&jsonOut, tui.FlagNameJSON, false, "Emit structured JSON response")
	return cmd
}

func runTaskStart(rawKey string, autoEdit bool) error {
	return runTaskStartWithOpts(rawKey, autoEdit, false)
}

func runTaskStartWithOpts(rawKey string, autoEdit, forceProject bool) error {
	return runTaskStartWithJSON(rawKey, autoEdit, forceProject, false)
}

func runTaskStartWithJSON(rawKey string, autoEdit, forceProject, jsonOut bool) error {
	key := strings.ToUpper(strings.TrimSpace(rawKey))
	if err := task.ValidateTaskKey(key); err != nil {
		return err
	}
	store, err := task.NewDefaultSQLiteTaskStore()
	if err != nil {
		return err
	}
	t, err := store.GetTask(key)
	if err != nil {
		return err
	}
	mgr, err := workspace.NewManager("")
	if err != nil {
		return err
	}
	activeProj := taskui.ResolveRepoProjectName(mgr.RepoRoot)
	if !forceProject && !task.MatchesProject(t.Project, activeProj, true) {
		return types.NewNomosError(types.ErrCodeValidationFailed,
			"task %s belongs to project %q, cannot start inside repository %q", key, t.Project, activeProj).
			WithRemedy(fmt.Sprintf("switch to the %q repository, run 'nomos task dag' for %q tasks, or run 'nomos task project %s %s' to reassign",
				t.Project, activeProj, activeProj, key))
	}
	wtPath, err := mgr.CreateWorktree(key)
	if err != nil {
		return err
	}
	targetPhase, targetStatus := types.PhasePlan, task.StatusPlan
	if autoEdit {
		targetPhase, targetStatus = types.PhaseEdit, task.StatusEdit
	}
	if err := persistStartedLease(targetPhase, key, wtPath); err != nil {
		return err
	}
	allocateWorktreeMTPO(mgr.RepoRoot, key, wtPath)
	t.Status = targetStatus
	if err := store.UpdateTask(*t); err != nil {
		return err
	}
	if jsonOut {
		return emitStructuredJSON(os.Stdout, task.BuildInspectionRecord(*t, t.Dependencies, nil, wtPath, mgr.BranchName(key)))
	}
	tui.RenderTaskStartedBanner(key, wtPath, string(targetPhase))
	renderTurnZeroExecutionBrief(os.Stdout, t)
	renderLearnedPitfallsBriefing(mgr.RepoRoot, t.Title, t.ScopePaths)
	tui.RenderPhaseTransitionNextActions(os.Stdout, targetPhase, key, wtPath, mgr.RepoRoot)
	return nil
}

func persistStartedLease(phase types.Phase, key, wtPath string) error {
	tok, err := capbac.MintToken(phase, key, wtPath, 100, 24*time.Hour)
	if err != nil {
		return err
	}
	db, err := storage.OpenDB("")
	if err != nil {
		return nil
	}
	defer func() { _ = db.Close() }()
	return storage.SaveLease(db, tok.ID, key, string(phase), 100, 0)
}

func newTaskEditCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "edit <key>",
		Short:   "Transition task to EDIT phase and enable code mutations",
		Long:    tui.HelpTaskEditLong,
		Example: tui.HelpTaskEditExample,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := strings.ToUpper(strings.TrimSpace(args[0]))
			store, err := task.NewDefaultSQLiteTaskStore()
			if err != nil {
				return err
			}
			t, err := store.GetTask(key)
			if err != nil {
				return err
			}
			t.Status = task.StatusEdit
			if err := store.UpdateTask(*t); err != nil {
				return err
			}
			updateActiveLeasePhase(key, "EDIT")
			fmt.Printf("✏️  Task %s transitioned to %s phase\n", tui.StyleTaskKey(key), tui.StyleSemanticBadge("EDIT"))
			tui.RenderPhaseTransitionNextActions(os.Stdout, types.PhaseEdit, key, "", "")
			return nil
		},
	}
}

func newTaskReviewCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "review <key>",
		Short:   "Transition task to REVIEW phase and evaluate DoD quality gates",
		Long:    tui.HelpTaskReviewLong,
		Example: tui.HelpTaskReviewExample,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := strings.ToUpper(strings.TrimSpace(args[0]))
			store, err := task.NewDefaultSQLiteTaskStore()
			if err != nil {
				return err
			}
			t, err := store.GetTask(key)
			if err != nil {
				return err
			}
			t.Status = task.StatusReview
			if err := store.UpdateTask(*t); err != nil {
				return err
			}
			updateActiveLeasePhase(key, "REVIEW")
			_, gateErr := executeGateCheckForTask(key)
			if gateErr == nil {
				tui.RenderPhaseTransitionNextActions(os.Stdout, types.PhaseReview, key, "", "")
			}
			return gateErr
		},
	}
}

func updateActiveLeasePhase(taskKey, phase string) {
	db, err := storage.OpenDB("")
	if err != nil {
		return
	}
	defer func() { _ = db.Close() }()
	if leaseID, _, budget, used, lErr := storage.GetActiveLease(db, taskKey); lErr == nil {
		if sErr := storage.SaveLease(db, leaseID, taskKey, phase, budget, used); sErr != nil {
			fmt.Fprintf(os.Stderr, "⚠️  Warning: failed to update lease phase: %v\n", sErr)
		}
	}
}

func newTaskAbortCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "abort <key>",
		Short:   "Abort task execution, revoke capability lease, and remove worktree",
		Long:    tui.HelpTaskAbortLong,
		Example: tui.HelpTaskAbortExample,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := strings.ToUpper(strings.TrimSpace(args[0]))
			mgr, err := workspace.NewManager("")
			if err != nil {
				return err
			}
			if remErr := mgr.RemoveWorktree(key); remErr != nil {
				// non-fatal if worktree already removed
			}
			taskui.RevokeActiveLease(key)
			mem.RemoveTaskTrapBuffer(mgr.RepoRoot, key)
			store, err := task.NewDefaultSQLiteTaskStore()
			if err == nil {
				if t, getErr := store.GetTask(key); getErr == nil {
					t.Status = task.StatusAborted
					if updErr := store.UpdateTask(*t); updErr != nil {
						return updErr
					}
				}
			}
			fmt.Printf("🛑 Task %s %s\n", tui.StyleTaskKey(key), tui.UiWarnStyle.Render("aborted and pruned"))
			tui.RenderPhaseTransitionNextActions(os.Stdout, types.PhaseIdle, "", "", mgr.RepoRoot)
			return nil
		},
	}
}

func allocateNextKey(store task.TaskStore, plane string) (string, error) {
	prefix := "SUB"
	if strings.ToLower(plane) == "intent" {
		prefix = "INT"
	}
	tasks := store.ListTasks()
	maxNum := 0
	for _, t := range tasks {
		if strings.HasPrefix(t.Key, prefix+"-") {
			parts := strings.Split(t.Key, "-")
			if len(parts) >= 2 {
				if num, err := strconv.Atoi(parts[1]); err == nil && num > maxNum {
					maxNum = num
				}
			}
		}
	}
	return task.FormatTaskKey(prefix, maxNum+1)
}
