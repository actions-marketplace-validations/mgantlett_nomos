// Package verify orchestrates configured obligation, contract signature, stale receipt,
// and directional architecture boundary checks inside IntentTraceabilityGate.
package verify

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/config"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/task"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/impact"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/intentlint"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

// VerifyExecutableObligations checks that TC-* vectors declared in Markdown tables
// across specPaths appear in repository verification suites.
// @intent nomos_intent_traceability_spec.md #ContractsConfig
func VerifyExecutableObligations(root string, specPaths []string) []string {
	return intentlint.VerifyExecutableObligations(root, specPaths)
}

// VerifySpecTypeSignatures checks Go and polyglot contract types in fenced specification code blocks
// against compiled Go AST and polyglot source declarations.
// @intent nomos_intent_traceability_spec.md #ContractsConfig
func VerifySpecTypeSignatures(root, targetDir string, specPaths []string) []string {
	return intentlint.VerifySpecTypeSignatures(root, targetDir, specPaths)
}

// ResolveActiveWorktreeSpecRoots scopes specification paths when running inside an isolated worktree.
// @intent nomos_intent_traceability_spec.md #ContractsConfig
func ResolveActiveWorktreeSpecRoots(scanRoot string, specRoots []string) []string {
	return intentlint.ResolveActiveWorktreeSpecRoots(scanRoot, specRoots)
}

// ResolveTaskContractSpecRoots returns the contract specification files applicable to taskKey.
// @intent nomos_intent_traceability_spec.md #ContractsConfig
func ResolveTaskContractSpecRoots(scanRoot, taskKey string, specRoots []string) []string {
	return intentlint.ResolveTaskContractSpecRoots(scanRoot, taskKey, specRoots)
}

// evaluateConfiguredObligations evaluates architecture boundaries and specification contracts
// declared in nomos.yaml for the active repository or task worktree.
func evaluateConfiguredObligations(gateName string, ctx *vscope.VerifyContext, root, targetDir string) *vscope.GateResult {
	cfg := vscope.ResolveVerifyConfig(ctx)
	if cfg == nil {
		return nil
	}
	if len(cfg.Governance.QualityGates.ArchitectureBoundaries) > 0 {
		if boundaryFailure := evaluateConfiguredArchitectureBoundaries(gateName, ctx, root, cfg.Governance.QualityGates.ArchitectureBoundaries); boundaryFailure != nil {
			return boundaryFailure
		}
	}
	if contractRes := evaluateContractObligationChecks(gateName, ctx, cfg, root, targetDir); contractRes != nil {
		return contractRes
	}
	return evaluateWorktreeZeroDustGate(gateName, ctx, root)
}

func evaluateWorktreeZeroDustGate(gateName string, ctx *vscope.VerifyContext, root string) *vscope.GateResult {
	scanRoot := root
	if ctx != nil && ctx.WorktreePath != "" {
		scanRoot = ctx.WorktreePath
	}
	unreached := impact.EvaluateWorktreeUnreachedFiles(ctx, scanRoot)
	if len(unreached) == 0 {
		return nil
	}
	return &vscope.GateResult{
		GateName: gateName, ProbeID: probeIDIntentTraceability, Law: 7, Ring: RingSyntactic,
		Passed:  false,
		Message: fmt.Sprintf("unreached worktree file(s): %s", strings.Join(unreached, "; ")),
		Remedy:  "wire newly added source files into an entrypoint/caller, add a paired unit test, or prune dead AI dust (Law 6 & Law 7)",
	}
}

// evaluateContractObligationChecks enforces bijective contract struct parity, executable TC-*
// bindings, orphan-free tests, normative surjection, and stale-digest freshness.
func evaluateContractObligationChecks(gateName string, ctx *vscope.VerifyContext, cfg *config.EcosystemConfig, root, targetDir string) *vscope.GateResult {
	contracts := cfg.Governance.QualityGates.GetContractsBoundary().Contracts
	if !contracts.Enabled || isIntentPlaneTask(ctx) {
		return nil
	}
	activeTaskID, scanRoot := resolveContractScanScope(ctx, root)
	siblingDelta := intentlint.CollectActiveSiblingTwinDeltas(scanRoot, activeTaskID)
	deferredSpecs := intentlint.CollectActiveSiblingTwinSpecs(scanRoot, activeTaskID)
	scopedSpecRoots := intentlint.ResolveTaskContractSpecRoots(scanRoot, activeTaskID, contracts.SpecRoots)
	if contracts.StrictTypes {
		if sigRes := checkContractTypeParityWithDelta(gateName, scanRoot, targetDir, scopedSpecRoots, siblingDelta); sigRes != nil {
			return sigRes
		}
		if strings.EqualFold(strings.TrimSpace(cfg.Project), "nomos") {
			if surjRes := checkNormativeRegistrySurjection(gateName, scanRoot, contracts.SpecRoots); surjRes != nil {
				return surjRes
			}
		}
	}
	if contracts.VerifyObligations {
		if oblRes := checkExecutableObligationsWithDelta(gateName, scanRoot, scopedSpecRoots, siblingDelta); oblRes != nil {
			return oblRes
		}
		if orphanRes := checkNoOrphanedTestObligations(gateName, scanRoot, contracts.SpecRoots); orphanRes != nil {
			return orphanRes
		}
	}
	if contracts.FailOnStaleObligations {
		return checkStaleSpecObligations(gateName, ctx, root, cfg.Governance.QualityGates.IntentSourceRoots, deferredSpecs)
	}
	return nil
}

// resolveContractScanScope determines the active SUB-* task key and filesystem scan root.
func resolveContractScanScope(ctx *vscope.VerifyContext, root string) (string, string) {
	if ctx == nil {
		return intentlint.ExtractWorktreeSubTaskKey(root), root
	}
	scanRoot := root
	if ctx.WorktreePath != "" {
		scanRoot = ctx.WorktreePath
	}
	taskID := strings.TrimSpace(ctx.TaskID)
	if taskID == "" {
		taskID = intentlint.ExtractWorktreeSubTaskKey(scanRoot)
	}
	return taskID, scanRoot
}

func checkContractTypeParityWithDelta(gateName, root, targetDir string, specRoots []string, delta *impact.SiblingDelta) *vscope.GateResult {
	sigIssues := intentlint.VerifySpecTypeSignaturesWithDelta(root, targetDir, specRoots, delta)
	if len(sigIssues) == 0 {
		return nil
	}
	return &vscope.GateResult{
		GateName: gateName, ProbeID: probeIDIntentTraceability, Law: 4, Ring: RingSyntactic,
		Passed:  false,
		Message: fmt.Sprintf("specification contract signature drift: %s", strings.Join(sigIssues, "; ")),
		Remedy:  "align source struct/interface/class fields and types bijectively with specification contract blocks (Law 4)",
	}
}

func checkNormativeRegistrySurjection(gateName, root string, specRoots []string) *vscope.GateResult {
	surjIssues := intentlint.VerifyNormativeRegistrySurjection(root, specRoots)
	if len(surjIssues) == 0 {
		return nil
	}
	return &vscope.GateResult{
		GateName: gateName, ProbeID: probeIDIntentTraceability, Law: 4, Ring: RingSyntactic,
		Passed:  false,
		Message: fmt.Sprintf("undocumented normative substrate surface: %s", strings.Join(surjIssues, "; ")),
		Remedy:  "document every Gate ProbeID, core/config struct, CLI --flag, and ERR_* code in canonical Layer 2 specs (Law 4 & Law 6)",
	}
}

func checkExecutableObligationsWithDelta(gateName, root string, specRoots []string, delta *impact.SiblingDelta) *vscope.GateResult {
	oblIssues := intentlint.VerifyExecutableObligationsWithDelta(root, specRoots, delta)
	if len(oblIssues) > 0 {
		return &vscope.GateResult{
			GateName: gateName, ProbeID: probeIDIntentTraceability, Law: 4, Ring: RingSyntactic,
			Passed:  false,
			Message: fmt.Sprintf("unbound specification obligation vector(s): %s", strings.Join(oblIssues, "; ")),
			Remedy:  "bind each TC-* vector in a unit test via '// @verifies <TC-ID>' (or '# @verifies <TC-ID>') (Law 2 & Law 4)",
		}
	}
	return nil
}

func checkNoOrphanedTestObligations(gateName, root string, specRoots []string) *vscope.GateResult {
	orphanIssues := intentlint.VerifyNoOrphanedTestObligations(root, specRoots)
	if len(orphanIssues) > 0 {
		return &vscope.GateResult{
			GateName: gateName, ProbeID: probeIDIntentTraceability, Law: 4, Ring: RingSyntactic,
			Passed:  false,
			Message: fmt.Sprintf("orphaned @verifies test obligation vector(s) absent from specs: %s", strings.Join(orphanIssues, "; ")),
			Remedy:  "declare each @verifies TC-* vector in its canonical Layer 2 specification table or remove stale test tags (Law 2 & Law 4)",
		}
	}
	return nil
}

func isIntentPlaneTask(ctx *vscope.VerifyContext) bool {
	return ctx != nil && strings.HasPrefix(strings.TrimSpace(ctx.TaskID), "INT-")
}

// checkStaleSpecObligations reports specifications whose SHA-256 digest drifted since release sealing.
func checkStaleSpecObligations(gateName string, ctx *vscope.VerifyContext, root string, intentRoots []string, deferredSpecs map[string]bool) *vscope.GateResult {
	stale, err := task.ComputeStaleSpecObligations(root, "")
	if err != nil || len(stale) == 0 {
		return nil
	}
	inFlightSpecs := collectInFlightSubSpecDigests(ctx, root, intentRoots)
	var items []string
	for _, s := range stale {
		if !inFlightSpecs[s.SpecPath] && !deferredSpecs[s.SpecPath] {
			items = append(items, fmt.Sprintf("%s (last verified by %s)", s.SpecPath, s.LastVerifiedBy))
		}
	}
	if len(items) == 0 {
		return nil
	}
	return &vscope.GateResult{
		GateName: gateName, ProbeID: probeIDIntentTraceability, Law: 4, Ring: RingCryptographic,
		Passed:  false,
		Message: fmt.Sprintf("stale specification obligation(s) detected: %s", strings.Join(items, "; ")),
		Remedy:  "execute and sync a downstream SUB-* task referencing the updated specification to refresh spec_digests in refs/notes/nomos/releases (Law 4)",
	}
}

func collectInFlightSubSpecDigests(ctx *vscope.VerifyContext, root string, intentRoots []string) map[string]bool {
	subKey, wt := resolveContractScanScope(ctx, root)
	if !strings.HasPrefix(subKey, "SUB-") {
		return nil
	}
	trunkRoot := resolvePrimaryObligationRoot(wt, root)
	twinKey := resolveActiveSubTwinKey(trunkRoot, subKey)
	return collectInFlightSubSpecDigestsWithTwin(ctx, wt, twinKey, intentRoots)
}

func collectInFlightSubSpecDigestsWithTwin(ctx *vscope.VerifyContext, wt, twinKey string, intentRoots []string) map[string]bool {
	var modified []string
	if ctx != nil {
		modified = vscope.GetModifiedFilesInScope(ctx, wt)
	}
	if len(modified) == 0 && twinKey == "" {
		return nil
	}
	active := make(map[string]bool)
	for k := range task.CollectReferencedSpecDigestsWithRoots(wt, modified, twinKey, intentRoots) {
		active[k] = true
	}
	trunkRoot := resolvePrimaryObligationRoot(wt, wt)
	if twinKey != "" && trunkRoot != "" && trunkRoot != wt {
		for k := range task.CollectReferencedSpecDigestsWithRoots(trunkRoot, nil, twinKey, intentRoots) {
			active[k] = true
		}
	}
	return active
}

func resolvePrimaryObligationRoot(wt, fallback string) string {
	if pr, err := workspace.FindRepoRoot(wt); err == nil && pr != "" {
		return pr
	}
	return fallback
}

func resolveActiveSubTwinKey(trunkRoot, subKey string) string {
	store, err := task.NewDefaultSQLiteTaskStore()
	if err != nil || store == nil {
		return ""
	}
	defer func() {
		if cErr := store.Close(); cErr != nil {
			// best-effort read-only store close
		}
	}()
	project := task.ResolveRepoProject(trunkRoot)
	for _, rec := range task.FilterTasksByProject(store.ListTasks(), project, true) {
		if rec.Key == subKey && strings.HasPrefix(strings.TrimSpace(rec.TwinKey), "INT-") {
			return strings.TrimSpace(rec.TwinKey)
		}
	}
	return ""
}

func evaluateConfiguredArchitectureBoundaries(gateName string, ctx *vscope.VerifyContext, root string, rules []config.ArchitectureBoundaryRule) *vscope.GateResult {
	scanRoot := root
	if ctx != nil && ctx.WorktreePath != "" {
		scanRoot = ctx.WorktreePath
	}
	violations := impact.EvaluateArchitectureBoundaries(scanRoot, nil, rules)
	if len(violations) == 0 {
		return nil
	}
	return &vscope.GateResult{
		GateName: gateName, ProbeID: probeIDIntentTraceability, Law: 7, Ring: RingSyntactic,
		Passed:  false,
		Message: fmt.Sprintf("architecture boundary violation(s): %s", strings.Join(violations, "; ")),
		Remedy:  "remove forbidden cross-layer imports or route calls through an allowed interface package per nomos.yaml architecture_boundaries (Law 7)",
	}
}

// EvaluatePlaneViolations checks if a task mutated files across prohibited plane boundaries.
// It defaults to standard "intent" and "substrate" roots.
func EvaluatePlaneViolations(taskKey string, modifiedFiles []string) []string {
	return EvaluatePlaneViolationsWithRoots(taskKey, modifiedFiles, nil, nil)
}

// EvaluatePlaneViolationsWithRoots evaluates plane boundaries dynamically using declared roots (Law 5).
func EvaluatePlaneViolationsWithRoots(taskKey string, modifiedFiles []string, intentRoots, substrateRoots []string) []string {
	if len(intentRoots) == 0 {
		intentRoots = []string{"intent"}
	}
	if len(substrateRoots) == 0 {
		substrateRoots = []string{"substrate"}
	}

	var violations []string
	isSub := strings.HasPrefix(taskKey, "SUB-")
	isInt := strings.HasPrefix(taskKey, "INT-")

	matchesRoot := func(clean string, roots []string) bool {
		for _, r := range roots {
			rootClean := strings.Trim(filepath.ToSlash(r), "/")
			if rootClean == "" {
				continue
			}
			if clean == rootClean || strings.HasPrefix(clean, rootClean+"/") {
				return true
			}
		}
		return false
	}

	for _, f := range modifiedFiles {
		clean := strings.TrimPrefix(filepath.ToSlash(f), "/")
		if isSub && matchesRoot(clean, intentRoots) {
			violations = append(violations, fmt.Sprintf("Law 7 violation: Substrate task %s cannot modify intent plane files (detected: %s)", taskKey, f))
		}
		if isInt && matchesRoot(clean, substrateRoots) {
			violations = append(violations, fmt.Sprintf("Law 7 violation: Intent task %s cannot modify substrate plane files (detected: %s)", taskKey, f))
		}
	}
	return violations
}

// PlaneSeparationRemedy provides deterministic, actionable JIT instructions for Law 7 plane separation recovery.
func PlaneSeparationRemedy(taskKey string) string {
	targetPlane, oppPlane := "substrate", "intent"
	if strings.HasPrefix(taskKey, "INT-") {
		targetPlane, oppPlane = "intent", "substrate"
	}
	return fmt.Sprintf("Law 7 strictly forbids cross-plane mutations. Protocol Recovery Steps:\n"+
		"  1. Revert cross-plane edits in active %s worktree: git checkout -- <file>\n"+
		"  2. Provision a twin task in the %s plane: nomos task create \"<title>\" --plane %s\n"+
		"  3. Execute and sync the twin task in its isolated worktree: nomos task start <KEY>",
		targetPlane, oppPlane, oppPlane)
}
