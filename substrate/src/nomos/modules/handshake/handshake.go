// Package handshake implements JIT session orientation telemetry, CapBAC tokens,
// and NCTE-100 prose validation enforced by compiled Go SSoT contracts.
package handshake

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/config"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/types"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/task"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/impact"
)

// DirectivePhase represents valid workspace phases for JIT directives (#DirectivePhase).
type DirectivePhase types.Phase

// HandshakeEvent represents JIT session orientation telemetry (#HandshakeEvent).
type HandshakeEvent struct {
	Seat             string            `json:"seat"`
	Repository       string            `json:"repository"`
	Plane            string            `json:"plane,omitempty"`
	Phase            types.Phase       `json:"phase"`
	ActiveTask       string            `json:"active_task,omitempty"`
	Assignee         string            `json:"assignee,omitempty"`
	TokenID          string            `json:"token_id,omitempty"`
	StepBudget       int               `json:"step_budget,omitempty"`
	TokensUsed       int               `json:"tokens_used,omitempty"`
	DatabasesHealthy bool              `json:"databases_healthy"`
	GateCount        int               `json:"gate_count"`
	ReadyWavefront   []string          `json:"ready_wavefront"`
	Directives           []string                 `json:"directives,omitempty"`
	LearnedPitfalls      []string                 `json:"learned_pitfalls,omitempty"`
	VerifiedPatterns     []string                 `json:"verified_patterns,omitempty"`
	GraduationCandidates []string                 `json:"graduation_candidates,omitempty"`
	ImpactSummary        []string                 `json:"impact_summary,omitempty"`
	StaleObligations     []task.StaleSpecObligation `json:"stale_obligations,omitempty"`
	StrictMode           bool                     `json:"strict_mode"`
	BrownfieldTopology   *BrownfieldTopologyBrief `json:"brownfield_topology,omitempty"`
	NextActions          []types.ActionAffordance `json:"next_actions,omitempty"`
	DeprecatedTerms      map[string]string        `json:"deprecated_terms,omitempty"`
	Timestamp            string                   `json:"timestamp,omitempty"`
}

// DirectiveFrontmatter defines the authoritative schema for JIT directives (#DirectiveFrontmatter).
type DirectiveFrontmatter struct {
	Title          string           `json:"title" yaml:"title"`
	Description    string           `json:"description,omitempty" yaml:"description,omitempty"`
	Authors        []string         `json:"authors" yaml:"authors"`
	Classification string           `json:"classification" yaml:"classification"`
	Date           string           `json:"date" yaml:"date"`
	LastUpdated    string           `json:"last_updated" yaml:"last_updated"`
	Plane          string           `json:"plane" yaml:"plane"`
	Status         string           `json:"status" yaml:"status"`
	Type           string           `json:"type" yaml:"type"`
	Phases         []DirectivePhase `json:"phases" yaml:"phases"`
	Category       string           `json:"category,omitempty" yaml:"category,omitempty"`
	Priority       string           `json:"priority,omitempty" yaml:"priority,omitempty"`
	Items          []string         `json:"items,omitempty" yaml:"items,omitempty"`
	Summary        string           `json:"summary,omitempty" yaml:"summary,omitempty"`
}

// SynthesizeDynamicHandshake inspects live workspace state, databases, and quality gates.
// @intent INT-840 #HandshakeEvent
func SynthesizeDynamicHandshake(repoRoot string) (*HandshakeEvent, error) {
	treeRoot, primaryRoot, err := resolveHandshakeRoots(repoRoot)
	if err != nil {
		return nil, err
	}
	seat := resolveHandshakeSeat()
	branch, activeTask := detectBranchAndTask(treeRoot)
	phase, plane, leaseID, stepBudget, tokensUsed := queryTaskAndLease(activeTask)
	wavefront := queryReadyWavefront(task.ResolveRepoProject(primaryRoot))
	directives := loadPhaseDirectives(primaryRoot, phase)
	modifiedFiles, impactSummary := computeActiveTaskImpact(treeRoot, activeTask)
	pitfalls, patterns, graduations := recallMnemeHandshakeContext(primaryRoot, activeTask, modifiedFiles)
	strictMode, intentTwinOff, bfTopology := resolveHandshakeGovernance(treeRoot, primaryRoot)
	nextActions := resolveHandshakeNextActions(handshakeNextActionsInput{
		treeRoot: treeRoot, primaryRoot: primaryRoot, branch: branch, phase: phase,
		activeTask: activeTask, wtRoot: activeWorktreeRoot(treeRoot, activeTask),
		directives: directives, wavefront: wavefront, intentTwinOff: intentTwinOff,
	})
	event := &HandshakeEvent{
		Seat:                 seat,
		Repository:           filepath.Base(primaryRoot),
		Plane:                plane,
		Phase:                phase,
		ActiveTask:           activeTask,
		TokenID:              leaseID,
		StepBudget:           stepBudget,
		TokensUsed:           tokensUsed,
		DatabasesHealthy:     checkDatabasesHealth(),
		GateCount:            verify.RegisteredGateCount(),
		ReadyWavefront:       wavefront,
		Directives:           directives,
		LearnedPitfalls:      pitfalls,
		VerifiedPatterns:     patterns,
		GraduationCandidates: graduations,
		ImpactSummary:        impactSummary,
		StaleObligations:     resolveHandshakeStaleObligations(primaryRoot),
		StrictMode:           strictMode,
		BrownfieldTopology:   bfTopology,
		NextActions:          nextActions,
		Timestamp:            time.Now().UTC().Format(time.RFC3339),
	}
	if valErr := ValidateHandshakeEvent(event); valErr != nil {
		return nil, valErr
	}
	return event, nil
}

func activeWorktreeRoot(treeRoot, activeTask string) string {
	if activeTask != "" && strings.Contains(filepath.ToSlash(treeRoot), "/worktrees/") {
		return treeRoot
	}
	return ""
}

func resolveHandshakeSeat() string {
	if seat := os.Getenv("NOMOS_SEAT"); seat != "" {
		return seat
	}
	return "workstation-local"
}

func resolveHandshakeStaleObligations(primaryRoot string) []task.StaleSpecObligation {
	stale, err := task.ComputeStaleSpecObligations(primaryRoot, "")
	if err != nil {
		return nil
	}
	return stale
}

func resolveHandshakeRoots(repoRoot string) (string, string, error) {
	if repoRoot == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", "", types.NewNomosError(types.ErrCodeBoundary, "failed resolving working directory: %v", err)
		}
		repoRoot = cwd
	}
	treeRoot := repoRoot
	if tr, tErr := workspace.FindWorkingTreeRoot(repoRoot); tErr == nil {
		treeRoot = tr
	}
	primaryRoot := treeRoot
	if pr, pErr := workspace.FindRepoRoot(repoRoot); pErr == nil {
		primaryRoot = pr
	}
	return treeRoot, primaryRoot, nil
}

func computeActiveTaskImpact(treeRoot, activeTask string) ([]string, []string) {
	if activeTask == "" {
		return nil, nil
	}
	modified := impact.ListModifiedWorktreeFiles(treeRoot, activeTask)
	return modified, impact.ComputeWorkspaceImpact(treeRoot, modified)
}

func detectBranchAndTask(repoRoot string) (string, string) {
	cmd := workspace.SafeGitCommand(repoRoot, "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "main", ""
	}
	branch := strings.TrimSpace(string(out))
	if strings.HasPrefix(branch, "feature/") {
		taskKey := strings.TrimPrefix(branch, "feature/")
		return branch, taskKey
	}
	return branch, ""
}

// ValidateHandshakeEvent asserts that HandshakeEvent satisfies structural invariants.
func ValidateHandshakeEvent(event *HandshakeEvent) error {
	if event == nil {
		return types.NewNomosError(types.ErrCodeValidationFailed, "handshake event cannot be nil")
	}
	if strings.TrimSpace(event.Seat) == "" {
		return types.NewNomosError(types.ErrCodeValidationFailed, "seat cannot be empty")
	}
	if strings.TrimSpace(event.Repository) == "" {
		return types.NewNomosError(types.ErrCodeValidationFailed, "repository cannot be empty")
	}
	if !types.IsValidPhase(event.Phase) {
		return types.NewNomosError(types.ErrCodePhaseViolation, "invalid phase %q", event.Phase)
	}
	return nil
}

type handshakeNextActionsInput struct {
	treeRoot      string
	primaryRoot   string
	branch        string
	phase         types.Phase
	activeTask    string
	wtRoot        string
	directives    []string
	wavefront     []string
	intentTwinOff bool
}

func resolveHandshakeNextActions(in handshakeNextActionsInput) []types.ActionAffordance {
	if in.activeTask != "" || in.wtRoot != "" {
		return types.BuildHandshakeNextActions(in.phase, in.activeTask, in.wtRoot, in.directives, in.wavefront, in.intentTwinOff)
	}
	cfg, err := config.LoadConfig(in.treeRoot)
	if err != nil || cfg == nil {
		cfg, err = config.LoadConfig(in.primaryRoot)
	}
	if err != nil || cfg == nil || cfg.Governance.Boundary.EffectiveIsolationMode() != config.IsolationModeBranch {
		return types.BuildHandshakeNextActions(in.phase, in.activeTask, in.wtRoot, in.directives, in.wavefront, in.intentTwinOff)
	}
	isProtected := in.branch == "" || in.branch == "HEAD" || workspace.IsProtectedBranch(in.branch) || strings.EqualFold(strings.TrimSpace(in.branch), strings.TrimSpace(cfg.DefaultBranch))
	if isProtected {
		return []types.ActionAffordance{
			{Command: "git switch -c feat/<slug>", Summary: "Create a feature branch before editing (protected trunk is guarded)", Phase: types.PhaseEdit},
			{Command: "nomos verify", Summary: "Run 42-gate verification baseline check", Phase: types.PhaseReview},
		}
	}
	return []types.ActionAffordance{
		{Command: "nomos verify", Summary: "Verify modified files on feature branch before ending turn or committing", Phase: types.PhaseReview},
		{Command: `git commit -m "<summary>"`, Summary: "Commit verified changes and distill lessons into Mneme", Phase: types.PhaseSync},
	}
}
