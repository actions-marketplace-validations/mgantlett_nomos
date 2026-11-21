// Package verify provides Definition of Done (DoD) quality gate pipeline execution.
package verify

import (
	"bytes"
	"fmt"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/astdebt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/config"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/storage"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/task"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/ratchet"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

const embeddedTemplatesRelPrefix = "substrate/cli/templates/"

var templateSchemaSemVerPinRe = regexp.MustCompile(`(#\s*yaml-language-server:\s*\$schema=\S+/nomos/)v[0-9]+\.[0-9]+\.[0-9]+(/nomos\.schema\.json)`)

// NormativeSurfaceDelta captures AST and template contract changes between trunk and worktree (#NormativeSurfaceDelta).
type NormativeSurfaceDelta struct {
	CLISurface      []string `json:"cli_surface,omitempty"`
	ConfigSchema    []string `json:"config_schema,omitempty"`
	GovernanceRules []string `json:"governance_rules,omitempty"`
	TemplateAssets  []string `json:"template_assets,omitempty"`
}

// HasChanges reports whether any normative public surface changed relative to trunk.
// @intent nomos_hermetic_worktree_spec.md #NormativeSurfaceDelta
func (d NormativeSurfaceDelta) HasChanges() bool {
	return len(d.CLISurface) > 0 || len(d.ConfigSchema) > 0 || len(d.GovernanceRules) > 0 || len(d.TemplateAssets) > 0
}

// Summary formats a deterministic, bounded description of changed normative contract surfaces.
// @intent nomos_hermetic_worktree_spec.md #NormativeSurfaceDelta
func (d NormativeSurfaceDelta) Summary() string {
	rows := []struct {
		label string
		items []string
	}{
		{"cli_surface", d.CLISurface},
		{"config_schema", d.ConfigSchema},
		{"governance_rules", d.GovernanceRules},
		{"template_assets", d.TemplateAssets},
	}
	var parts []string
	for _, row := range rows {
		if s := formatBoundedTokens(row.label, row.items); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, ", ")
}

func formatBoundedTokens(label string, items []string) string {
	if len(items) == 0 {
		return ""
	}
	limit := len(items)
	suffix := ""
	if limit > 3 {
		suffix = fmt.Sprintf(" +%d", limit-3)
		limit = 3
	}
	return fmt.Sprintf("%s[%s%s]", label, strings.Join(items[:limit], ", "), suffix)
}

// InspectWorktreeNormativeSurface diffs trunk and worktree Go ASTs and embedded templates to detect contract expansion.
// @intent nomos_hermetic_worktree_spec.md #NormativeSurfaceDelta
func InspectWorktreeNormativeSurface(worktreeDir string) NormativeSurfaceDelta {
	if strings.TrimSpace(worktreeDir) == "" {
		return NormativeSurfaceDelta{}
	}
	baseRef := ratchet.ResolveClosestTrunkBaseRef(worktreeDir)
	if baseRef == "" {
		return NormativeSurfaceDelta{}
	}
	changedPaths := ratchet.CollectChangedPathsAgainstBase(worktreeDir, baseRef)
	if len(changedPaths) == 0 {
		return NormativeSurfaceDelta{}
	}

	cliSet := make(map[string]bool)
	cfgSet := make(map[string]bool)
	govSet := make(map[string]bool)
	tplSet := make(map[string]bool)

	for _, rel := range changedPaths {
		inspectChangedNormativeFile(worktreeDir, baseRef, rel, cliSet, cfgSet, govSet, tplSet)
	}
	cancelBalancedIntraPackageMoves(govSet)
	pruneSurvivingPackageGovTokens(worktreeDir, govSet)

	return NormativeSurfaceDelta{
		CLISurface:      ratchet.SortedSetKeys(cliSet),
		ConfigSchema:    ratchet.SortedSetKeys(cfgSet),
		GovernanceRules: ratchet.SortedSetKeys(govSet),
		TemplateAssets:  ratchet.SortedSetKeys(tplSet),
	}
}

// pruneSurvivingPackageGovTokens removes false-positive "-gov_..." or "-sql_dml:..." deltas when a sibling file in the same package still carries the token.
func pruneSurvivingPackageGovTokens(worktreeDir string, govSet map[string]bool) {
	var removedKeys []string
	for k := range govSet {
		if strings.HasPrefix(k, "-gov_rule:") || strings.HasPrefix(k, "-gov_const:") || strings.HasPrefix(k, "-sql_dml:") {
			removedKeys = append(removedKeys, k)
		}
	}
	if len(removedKeys) == 0 {
		return
	}
	survivingByDir := make(map[string]map[string]bool)
	for _, k := range removedKeys {
		tok := strings.TrimPrefix(k, "-")
		relDir := resolveGovRuleRelDir(worktreeDir, tok)
		if survivingByDir[relDir] == nil {
			survivingByDir[relDir] = collectDirSurvivingGovTokens(worktreeDir, relDir)
		}
		if survivingByDir[relDir][tok] {
			delete(govSet, k)
		}
	}
}

func resolveGovRuleRelDir(worktreeDir, tok string) string {
	rest := tok
	for _, pfx := range []string{"gov_rule:", "gov_const:", "sql_dml:"} {
		if strings.HasPrefix(rest, pfx) {
			rest = strings.TrimPrefix(rest, pfx)
			break
		}
	}
	parts := strings.SplitN(rest, ":", 2)
	if len(parts) == 2 && parts[0] != "" {
		if info, err := os.Stat(filepath.Join(worktreeDir, filepath.FromSlash(parts[0]))); err == nil && info.IsDir() {
			return parts[0]
		}
	}
	return "."
}

func collectDirSurvivingGovTokens(worktreeDir, relDir string) map[string]bool {
	out := make(map[string]bool)
	absDir := filepath.Join(worktreeDir, filepath.FromSlash(relDir))
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, rErr := os.ReadFile(filepath.Join(absDir, name))
		if rErr != nil {
			continue
		}
		relFile := name
		if relDir != "." {
			relFile = relDir + "/" + name
		}
		fp := astdebt.ParseGoSurfaceFingerprintForPath(relFile, raw)
		for tok := range fp.GovTokens {
			out[tok] = true
		}
	}
	return out
}

func cancelBalancedIntraPackageMoves(set map[string]bool) {
	for k := range set {
		if !strings.HasPrefix(k, "+") {
			continue
		}
		counterpart := "-" + strings.TrimPrefix(k, "+")
		if set[counterpart] {
			delete(set, k)
			delete(set, counterpart)
		}
	}
}

func inspectChangedNormativeFile(worktreeDir, baseRef, rel string, cliSet, cfgSet, govSet, tplSet map[string]bool) {
	normRel := filepath.ToSlash(strings.TrimSpace(rel))
	if normRel == "" {
		return
	}
	if strings.HasPrefix(normRel, embeddedTemplatesRelPrefix) {
		assetName := strings.TrimPrefix(normRel, embeddedTemplatesRelPrefix)
		baseBytes := normalizeEmbeddedTemplateBytes(assetName, readGitRevisionBytes(worktreeDir, baseRef, normRel))
		currBytes := normalizeEmbeddedTemplateBytes(assetName, readWorktreeDiskBytes(filepath.Join(worktreeDir, filepath.FromSlash(normRel))))
		if !bytes.Equal(baseBytes, currBytes) {
			tplSet[assetName] = true
		}
		return
	}
	if astdebt.IsNonTestWebSourcePath(normRel) {
		baseWeb := astdebt.ParseWebSurfaceFingerprintForPath(normRel, readGitRevisionBytes(worktreeDir, baseRef, normRel))
		currWeb := astdebt.ParseWebSurfaceFingerprintForPath(normRel, readWorktreeDiskBytes(filepath.Join(worktreeDir, filepath.FromSlash(normRel))))
		astdebt.MergeSymmetricDiff(govSet, baseWeb, currWeb)
		return
	}
	if !strings.HasSuffix(normRel, ".go") || strings.HasSuffix(normRel, "_test.go") {
		return
	}
	baseFP := astdebt.ParseGoSurfaceFingerprintForPath(normRel, readGitRevisionBytes(worktreeDir, baseRef, normRel))
	currFP := astdebt.ParseGoSurfaceFingerprintForPath(normRel, readWorktreeDiskBytes(filepath.Join(worktreeDir, filepath.FromSlash(normRel))))
	astdebt.MergeSymmetricDiff(cliSet, baseFP.CLITokens, currFP.CLITokens)
	astdebt.MergeSymmetricDiff(cfgSet, baseFP.YAMLFields, currFP.YAMLFields)
	astdebt.MergeSymmetricDiff(govSet, baseFP.GovTokens, currFP.GovTokens)
}

func normalizeEmbeddedTemplateBytes(assetName string, raw []byte) []byte {
	if assetName != "nomos.yaml" || len(raw) == 0 {
		return raw
	}
	return templateSchemaSemVerPinRe.ReplaceAll(raw, []byte("${1}v0.0.0${2}"))
}

// ValidateWorktreeIntentTwin enforces intent_twin_mode across commit headers, AST surface deltas, and Twin provenance.
// @intent nomos_hermetic_worktree_spec.md #CommitSchemaGate
func ValidateWorktreeIntentTwin(worktreeDir, commitMsg, mode string) string {
	if violation := ValidateCommitIntentTwinMode(commitMsg, mode); violation != "" {
		return violation
	}
	normMode := strings.ToLower(strings.TrimSpace(mode))
	rank, ok := config.IntentTwinModeRank(normMode)
	if !ok || rank == 0 || strings.TrimSpace(worktreeDir) == "" {
		return ""
	}
	subjectKey, commitType, twinKeys := extractSubCommitTwinContext(commitMsg)
	if !strings.HasPrefix(subjectKey, "SUB-") {
		return ""
	}
	if len(twinKeys) == 0 {
		delta := InspectWorktreeNormativeSurface(worktreeDir)
		if delta.HasChanges() {
			summary := delta.Summary()
			recordTwinRequiredLatch(worktreeDir, subjectKey, summary)
			return fmt.Sprintf(
				"Substrate %s commit [%s] modifies normative contract surface (%s) and requires a bound 'Twin: INT-<N>' trailer when governance.quality_gates.intent_twin_mode=%s (Law 2 & Law 4)",
				commitType, subjectKey, summary, normMode,
			)
		}
		if latched := readTwinRequiredLatch(worktreeDir, subjectKey); latched != "" {
			return fmt.Sprintf(
				"Substrate %s commit [%s] previously triggered normative surface expansion (%s); unexporting or hiding symbols to evade 'Twin: INT-<N>' is prohibited when governance.quality_gates.intent_twin_mode=%s (Law 1 & Law 4)",
				commitType, subjectKey, latched, normMode,
			)
		}
		return ""
	}
	for _, twinKey := range twinKeys {
		if provErr := verifyIntentTwinProvenance(worktreeDir, subjectKey, twinKey); provErr != "" {
			return provErr
		}
	}
	clearTwinRequiredLatch(worktreeDir, subjectKey)
	return ""
}

func extractSubCommitTwinContext(commitMsg string) (string, string, []string) {
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(commitMsg), "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return "", "", nil
	}
	firstLine := strings.TrimSpace(lines[0])
	if isMergeCommit(firstLine) {
		return "", "", nil
	}
	m := goldenSubjectSchema.FindStringSubmatch(firstLine)
	if m == nil {
		return "", "", nil
	}
	_, trailers, errMsg := splitBodyAndGitTrailers(lines[1:])
	if errMsg != "" {
		return m[1], m[2], nil
	}
	return m[1], m[2], collectIntentTwinTrailers(trailers)
}

func collectIntentTwinTrailers(trailers map[string][]string) []string {
	if trailers == nil {
		return nil
	}
	var twins []string
	for _, twin := range trailers["Twin"] {
		trimmed := strings.TrimSpace(twin)
		if strings.HasPrefix(trimmed, "INT-") && canonicalTaskTrailerValRe.MatchString(trimmed) {
			twins = append(twins, trimmed)
		}
	}
	return twins
}

// ValidateCommitIntentTwinMode enforces governance.quality_gates.intent_twin_mode
// ("off", "feat", "strict") against a hydrated or committed Golden Commit message.
// @intent nomos_hermetic_worktree_spec.md #QualityGatesConfig
func ValidateCommitIntentTwinMode(commitMsg, mode string) string {
	normMode := strings.ToLower(strings.TrimSpace(mode))
	rank, ok := config.IntentTwinModeRank(normMode)
	if !ok {
		return fmt.Sprintf("invalid governance.quality_gates.intent_twin_mode %q (expected off, feat, or strict)", mode)
	}
	if rank == 0 {
		return ""
	}
	subjectKey, commitType, twinKeys := extractSubCommitTwinContext(commitMsg)
	if !strings.HasPrefix(subjectKey, "SUB-") || (normMode == config.IntentTwinModeFeat && commitType != "feat") {
		return ""
	}
	if len(twinKeys) == 0 {
		return fmt.Sprintf(
			"Substrate %s commit [%s] requires a bound 'Twin: INT-<N>' trailer when governance.quality_gates.intent_twin_mode=%s (Law 4 & Law 7)",
			commitType, subjectKey, normMode,
		)
	}
	return ""
}

// IntentTwinModeRemedy returns the deterministic HATEOAS remediation string for missing INT twins.
// @intent nomos_hermetic_worktree_spec.md #QualityGatesConfig
func IntentTwinModeRemedy(subjectKey string) string {
	key := strings.TrimSpace(subjectKey)
	if key == "" {
		key = "SUB-<N>"
	}
	return fmt.Sprintf(
		"bind an Intent specification twin via 'nomos task create \"<spec title>\" --plane intent --scope \"<spec-path>\" --ac \"<contract>\" --verify \"nomos verify\" --twin-of %s' (or 'nomos task pair \"<title>\" --scope \"<paths>\" --ac \"<deliverable>\" --verify \"nomos verify\"') and re-commit via 'nomos task commit'",
		key,
	)
}

func resolveIntentTwinMode(ctx *vscope.VerifyContext) string {
	cfg := vscope.ResolveVerifyConfig(ctx)
	if cfg == nil {
		return config.IntentTwinModeOff
	}
	return cfg.Governance.QualityGates.EffectiveIntentTwinMode()
}

func verifyIntentTwinProvenance(worktreeDir, subjectKey, twinKey string) string {
	if hasGitCommitForTaskKey(worktreeDir, twinKey) || hasLedgerTaskRecord(twinKey) {
		return ""
	}
	return fmt.Sprintf(
		"Substrate commit [%s] declares unresolvable 'Twin: %s' absent from Git history and the Living Ledger (Law 4)",
		subjectKey, twinKey,
	)
}

func hasGitCommitForTaskKey(worktreeDir, taskKey string) bool {
	needle := "[" + strings.TrimSpace(taskKey) + "]"
	out, err := workspace.SafeGitCommand(worktreeDir, "log", "--all", "--fixed-strings", "--grep="+needle, "-n", "1", "--format=%H").Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

func hasLedgerTaskRecord(taskKey string) bool {
	dbPath := config.ResolveGlobalDBPath()
	if strings.TrimSpace(dbPath) == "" {
		return false
	}
	if _, err := os.Stat(dbPath); err != nil {
		return false
	}
	db, err := storage.OpenDB(dbPath)
	if err != nil {
		return false
	}
	defer func() {
		if cErr := db.Close(); cErr != nil {
			// connection closed
		}
	}()
	rec, gErr := task.NewSQLiteTaskStore(db).GetTask(strings.TrimSpace(taskKey))
	return gErr == nil && rec != nil && rec.Status != task.StatusAborted
}
