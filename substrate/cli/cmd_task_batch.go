// Package cli implements Tier 1 agent adapters, software physics telemetry,
// release readiness, upgrades, and CLI command dispatch contracts.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mgantlett/nomos/substrate/cli/taskui"
	"github.com/mgantlett/nomos/substrate/cli/tui"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/types"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/task"
	"github.com/spf13/cobra"
)

var syncVerifiesTagRe = regexp.MustCompile(`@verifies\s+(TC-[A-Z0-9]+-[0-9]+)\b`)

func newTaskBatchCmd() *cobra.Command {
	var filePath, project string
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "batch",
		Short: "Atomically compile a multi-node JSON DAG subgraph with $ref wiring",
		Long: `Validate and commit an Agent-to-Agent TaskBatchManifest JSON subgraph in one
atomic SQLite transaction, resolving symbolic $ref keys and rejecting DAG cycles.`,
		Example: `  nomos task batch -F wave.json --json
  cat <<'EOF' | nomos task batch -F - --json
  {"tasks":[{"ref":"spec","plane":"intent","title":"Author spec","scope":"Define contract","acceptance_criteria":["NCTE-100 clean"],"verify_cmd":"nomos verify"}]}
  EOF`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := readBatchManifestBytes(cmd.InOrStdin(), filePath)
			if err != nil {
				return err
			}
			store, err := task.NewDefaultSQLiteTaskStore()
			if err != nil {
				return err
			}
			defer func() {
				if cErr := store.Close(); cErr != nil {
					// closed
				}
			}()
			effectiveProj := taskui.ResolveActiveTaskProject(project)
			res, err := task.ExecuteTaskBatch(store, raw, effectiveProj)
			if err != nil {
				return err
			}
			return renderTaskBatchOutput(cmd.OutOrStdout(), res, jsonOut)
		},
	}
	cmd.Flags().StringVarP(&filePath, "file", "F", "-", "Path to JSON TaskBatchManifest file (use '-' for stdin)")
	cmd.Flags().StringVar(&project, tui.FlagNameProject, "", "Repository project identifier (defaults to active repository)")
	cmd.Flags().BoolVar(&jsonOut, tui.FlagNameJSON, false, "Emit structured JSON TaskBatchResult")
	return cmd
}

func readBatchManifestBytes(stdin io.Reader, filePath string) ([]byte, error) {
	clean := strings.TrimSpace(filePath)
	if clean == "" || clean == "-" {
		if stdin == nil {
			stdin = os.Stdin
		}
		data, err := io.ReadAll(stdin)
		if err != nil {
			return nil, types.NewNomosError(types.ErrCodeValidationFailed, "failed reading batch JSON from stdin: %v", err)
		}
		return data, nil
	}
	data, err := os.ReadFile(clean)
	if err != nil {
		return nil, types.NewNomosError(types.ErrCodeValidationFailed, "failed reading batch JSON file %s: %v", clean, err)
	}
	return data, nil
}

func renderTaskBatchOutput(out io.Writer, res *task.TaskBatchResult, jsonOut bool) error {
	if jsonOut {
		return emitStructuredJSON(out, res)
	}
	fmt.Fprintln(out, tui.RenderBrandTitle(fmt.Sprintf("🕸️  Nomos Atomic DAG Subgraph Compiled (%d tasks)", len(res.Tasks))))
	fmt.Fprintln(out, tui.UiRule(tui.RuleNarrow))
	for _, rec := range res.Tasks {
		line := fmt.Sprintf("  %s (%s) - %s", tui.StyleTaskKey(rec.Key), tui.StylePlane(rec.Plane), tui.UiBoldValueStyle.Render(rec.Title))
		if len(rec.Dependencies) > 0 {
			line += fmt.Sprintf(" [depends_on: %s]", strings.Join(rec.Dependencies, ", "))
		}
		fmt.Fprintln(out, line)
	}
	fmt.Fprintln(out, tui.UiRule(tui.RuleNarrow))
	return nil
}

func emitStructuredJSON(out io.Writer, payload any) error {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return types.NewNomosError(types.ErrCodeCore, "failed marshaling JSON response: %v", err)
	}
	fmt.Fprintln(out, string(data))
	return nil
}

func renderTurnZeroExecutionBrief(out io.Writer, t *task.TaskRecord) {
	if t == nil {
		return
	}
	if len(t.ScopePaths) == 0 && len(t.Obligations) == 0 && strings.TrimSpace(t.VerifyCmd) == "" {
		return
	}
	fmt.Fprintf(out, "  %s\n", tui.UiHeaderStyle.Render("Turn-0 Agent Handoff Brief:"))
	if len(t.ScopePaths) > 0 {
		fmt.Fprintf(out, "    🎯 Paths       : %s\n", tui.UiValueStyle.Render(strings.Join(t.ScopePaths, ", ")))
	}
	if len(t.Obligations) > 0 {
		fmt.Fprintf(out, "    📜 Obligations : %s\n", tui.UiValueStyle.Render(strings.Join(t.Obligations, ", ")))
	}
	if strings.TrimSpace(t.VerifyCmd) != "" {
		fmt.Fprintf(out, "    🧪 Verify Cmd  : %s\n", tui.UiCommandStyle.Render(t.VerifyCmd))
	}
}

func verifyTaskBoundObligations(scanRoot string, t *task.TaskRecord) error {
	if t == nil || t.Plane != "substrate" || len(t.Obligations) == 0 {
		return nil
	}
	found := collectRepositoryVerifiesTags(scanRoot)
	var missing []string
	for _, tc := range t.Obligations {
		if !found[strings.ToUpper(strings.TrimSpace(tc))] {
			missing = append(missing, tc)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return types.NewNomosError(types.ErrCodeValidationFailed,
		"task %s missing @verifies test binding(s) for required obligation(s): %s",
		t.Key, strings.Join(missing, ", ")).
		WithRemedy(fmt.Sprintf("add '// @verifies %s' inside a test verifying the invariant before running 'nomos task sync %s'",
			missing[0], t.Key))
}

func collectRepositoryVerifiesTags(scanRoot string) map[string]bool {
	found := make(map[string]bool)
	walkErr := filepath.WalkDir(scanRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if shouldSkipVerifiesDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if isCandidateTestFile(d.Name()) {
			scanFileVerifiesTags(path, found)
		}
		return nil
	})
	if walkErr != nil {
		return found
	}
	return found
}

func shouldSkipVerifiesDir(name string) bool {
	return name == ".git" || name == "node_modules" || name == "worktrees" || name == "vendor"
}

func scanFileVerifiesTags(path string, found map[string]bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, m := range syncVerifiesTagRe.FindAllStringSubmatch(string(raw), -1) {
		found[m[1]] = true
	}
}

func isCandidateTestFile(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, "_test.go") ||
		strings.HasSuffix(lower, ".test.ts") ||
		strings.HasSuffix(lower, ".spec.ts") ||
		strings.HasSuffix(lower, ".test.js") ||
		strings.HasSuffix(lower, "_test.py") ||
		(strings.HasPrefix(lower, "test_") && strings.HasSuffix(lower, ".py"))
}

type pairedRecordBuildSpec struct {
	key     string
	plane   string
	twinKey string
	project string
	title   string
}

func buildPairedRecordFromSpec(spec pairedRecordBuildSpec, opts taskPairOpts, ir *tui.ResolvedCardIR, deps []string) task.TaskRecord {
	return task.TaskRecord{
		Key: spec.key, Plane: spec.plane, Project: spec.project, SchemaType: task.SchemaType(spec.plane),
		Title: spec.title, Description: ir.Body, ScopePaths: ir.ScopePaths,
		Obligations: ir.Obligations, AcceptanceCriteria: ir.AcceptanceCriteria,
		VerifyCmd: ir.VerifyCmd, Status: task.StatusTodo,
		Tier: task.Tier(opts.tier), Priority: opts.priority, TwinKey: spec.twinKey,
		Dependencies: deps, Burden: ir.Burden,
	}
}

func persistTwinnedPair(store task.TaskStore, intTask, subTask task.TaskRecord) error {
	if err := store.CreateTask(intTask); err != nil {
		return fmt.Errorf("failed creating intent task %s: %w", intTask.Key, err)
	}
	if err := store.CreateTask(subTask); err != nil {
		return fmt.Errorf("failed creating substrate task %s: %w", subTask.Key, err)
	}
	if err := store.BindTwin(intTask.Key, subTask.Key); err != nil {
		return fmt.Errorf("failed binding twins %s <-> %s: %w", intTask.Key, subTask.Key, err)
	}
	return nil
}
