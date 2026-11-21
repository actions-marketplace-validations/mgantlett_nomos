// Package mem consolidates multi-scope convention pitfalls and legacy habit patterns into scrubbed repository axioms.
package mem

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// conventionGateRule maps a convention slug prefix to its canonical slug and display title.
type conventionGateRule struct {
	prefix    string
	canonical string
	title     string
}

// conventionAxiomSanitizers compile scrubbers for worktree paths, stack offsets, and stats clauses.
var (
	worktreePrefixScrubRe = regexp.MustCompile(`\S*/worktrees/[^/\s]+/`)
	absRepoSubpathScrubRe = regexp.MustCompile(`(/[^:\s]+?/)((?:substrate|intent|packaging|integrations|strategy)/[A-Za-z0-9_./-]+)`)
	stackOffsetScrubRe    = regexp.MustCompile(`\s*\+0x[0-9a-fA-F]+\S*`)
	axiomStatsClauseRe    = regexp.MustCompile(`\(\d+x across \d+ scopes\)`)
)

// canonicalConventionGateRules enumerates the 24 compiled convention gates eligible for axiom consolidation.
var canonicalConventionGateRules = []conventionGateRule{
	{prefix: "broken-wire", canonical: "broken-wire-link-invariant", title: "Broken Wire & Link Invariant"},
	{prefix: "unchecked-error", canonical: "unchecked-error-hygiene-invariant", title: "Unchecked Error Hygiene Invariant"},
	{prefix: "magic-strings-&-topology", canonical: "magic-string-duplication-gate", title: "Magic String Duplication Gate"},
	{prefix: "magic-strings-topology", canonical: "magic-string-duplication-gate", title: "Magic String Duplication Gate"},
	{prefix: "magic-string", canonical: "magic-string-duplication-gate", title: "Magic String Duplication Gate"},
	{prefix: "threshold-integrity", canonical: "threshold-integrity-gate", title: "Threshold Integrity Gate"},
	{prefix: "git-subprocess", canonical: "git-subprocess-hygiene-invariant", title: "Git Subprocess Hygiene Invariant"},
	{prefix: "intent-traceability", canonical: "intent-traceability-validation-gate", title: "Intent Traceability Validation Gate"},
	{prefix: "intent-steel-man", canonical: "intent-steel-man-parity-gate", title: "Intent Steel-Man Parity Gate"},
	{prefix: "intent-substrate-parity", canonical: "intent-substrate-parity-gate", title: "Intent Substrate Parity Gate"},
	{prefix: "intent-config-immutability", canonical: "intent-config-immutability-gate", title: "Intent Config Immutability Gate"},
	{prefix: "open-core-boundary", canonical: "open-core-boundary-gate", title: "Open-Core Boundary Gate"},
	{prefix: "zero-filesystem-alias", canonical: "zero-filesystem-alias-gate", title: "Zero Filesystem Alias Gate"},
	{prefix: "docstring-density", canonical: "docstring-density-gate", title: "Docstring Density Gate"},
	{prefix: "tdd-test-pair", canonical: "tdd-test-pair-invariant", title: "TDD Test Pair Invariant"},
	{prefix: "anti-shim", canonical: "anti-shim-ast-invariant", title: "Anti-Shim AST Invariant"},
	{prefix: "dead-code", canonical: "dead-code-gate", title: "Dead Code Gate"},
	{prefix: "go-toolchain", canonical: "go-toolchain-gate", title: "Go Toolchain Gate"},
	{prefix: "polyglot-toolchain", canonical: "polyglot-toolchain-gate", title: "Polyglot Toolchain Gate"},
	{prefix: "bilateral-parity", canonical: "bilateral-parity-gate", title: "Bilateral Parity Gate"},
	{prefix: "plane-separation", canonical: "plane-separation-gate", title: "Plane Separation Gate"},
	{prefix: "planeseparationgate", canonical: "plane-separation-gate", title: "Plane Separation Gate"},
	{prefix: "hermetic", canonical: "hermetic-worktree-invariant", title: "Hermetic Worktree Invariant"},
	{prefix: "contract-schema", canonical: "contract-schema-parity-gate", title: "Contract Schema Parity Gate"},
	{prefix: "modern-secret", canonical: "secret-hygiene-gate", title: "Secret Hygiene Gate"},
	{prefix: "secret-hygiene", canonical: "secret-hygiene-gate", title: "Secret Hygiene Gate"},
	{prefix: "canonical-singularity", canonical: "canonical-singularity-gate", title: "Canonical Singularity Gate"},
}

// conventionFamilyBucket aggregates pitfall occurrences per (repo_id, canonical_gate_slug).
type conventionFamilyBucket struct {
	repoID      string
	gateSlug    string
	title       string
	activeCards []*Memory
	scopes      map[string]bool
	totalHits   int
	newest      *Memory
}

// matchConventionGateRule matches a raw topic or slug against the 24 compiled convention gate rules.
func matchConventionGateRule(raw string) (conventionGateRule, bool) {
	clean := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(raw)), "_", "-")
	clean = strings.TrimPrefix(strings.TrimPrefix(clean, "axiom:"), habitTopicPrefix)
	if isAstCeilingGateSlug(clean) {
		return conventionGateRule{}, false
	}
	for _, rule := range canonicalConventionGateRules {
		if strings.HasPrefix(clean, rule.prefix) {
			return rule, true
		}
	}
	return conventionGateRule{}, false
}

// NormalizeConventionGateSlug maps truncated or underscored convention gate slugs onto canonical hyphenated identifiers (@intent: #Memory).
func NormalizeConventionGateSlug(raw string) string {
	if rule, ok := matchConventionGateRule(raw); ok {
		return rule.canonical
	}
	clean := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(raw)), "_", "-")
	return strings.TrimPrefix(strings.TrimPrefix(clean, "axiom:"), habitTopicPrefix)
}

// conventionConsolidationCounts pairs superseded record count with updated convention family count.
type conventionConsolidationCounts struct {
	superseded int
	families   int
}

// ConsolidateConventionAxioms collapses multi-scope convention pitfalls and legacy habit patterns into scrubbed repository axioms (@intent: #Memory).
func ConsolidateConventionAxioms(store *Store) int {
	return consolidateConventionAxiomsFiltered(store, "").superseded
}

// consolidateRepoConventionAxioms consolidates convention pitfalls for a single repository and returns updated family count.
func consolidateRepoConventionAxioms(store *Store, normRepo string) int {
	return consolidateConventionAxiomsFiltered(store, normRepo).families
}

// consolidateConventionAxiomsFiltered deduplicates axioms, promotes qualifying convention families, and retires habit clones.
func consolidateConventionAxiomsFiltered(store *Store, filterNormRepo string) conventionConsolidationCounts {
	if store == nil {
		return conventionConsolidationCounts{}
	}
	records, err := store.ListAll()
	if err != nil {
		return conventionConsolidationCounts{}
	}
	canonAxioms, superseded := deduplicateAndScrubConventionAxioms(store, records, filterNormRepo)
	families := collectOpenCoreConventionFamilies(records, filterNormRepo)
	familiesUpdated := 0
	for _, fam := range families {
		updated, sup := promoteConventionFamilyBucket(store, fam, canonAxioms)
		if updated {
			familiesUpdated++
			superseded += sup
		}
	}
	superseded += retireConventionHabitPatterns(store, records, canonAxioms, filterNormRepo)
	return conventionConsolidationCounts{superseded: superseded, families: familiesUpdated}
}

// promoteConventionFamilyBucket upserts the canonical convention axiom for a qualifying family and supersedes sibling cards.
func promoteConventionFamilyBucket(store *Store, fam *conventionFamilyBucket, canonAxioms map[string]*Memory) (bool, int) {
	if len(fam.activeCards) == 0 {
		return false, 0
	}
	axTopic := "axiom:" + fam.gateSlug
	axKey := normalizeConventionRepoKey(fam.repoID) + "|" + axTopic
	existing := canonAxioms[axKey]
	if existing == nil && len(fam.scopes) < 2 && fam.totalHits < 3 {
		return false, 0
	}
	canonRec := upsertOpenCoreConventionAxiom(store, fam, axTopic, existing)
	if canonRec == nil {
		return false, 0
	}
	canonAxioms[axKey] = canonRec
	return true, store.supersedeRecordsByIDs(fam.activeCards, canonRec.ID)
}

// deduplicateAndScrubConventionAxioms normalizes convention axiom topics, supersedes duplicates, and scrubs truncated/path-polluted bodies.
func deduplicateAndScrubConventionAxioms(store *Store, records []*Memory, filterNormRepo string) (map[string]*Memory, int) {
	canonical := make(map[string]*Memory)
	superseded := 0
	for _, rec := range records {
		rule, repoKey, ok := matchActiveConventionAxiom(rec, filterNormRepo)
		if !ok {
			continue
		}
		normTopic := "axiom:" + rule.canonical
		store.normalizeAxiomTopicAndScrubContent(rec, normTopic, rule.title)
		superseded += mergeCanonicalAxiomSlot(store, canonical, repoKey+"|"+normTopic, rec)
	}
	return canonical, superseded
}

// matchActiveConventionAxiom checks whether rec is an active convention axiom in the target repository filter.
func matchActiveConventionAxiom(rec *Memory, filterNormRepo string) (conventionGateRule, string, bool) {
	if !isActiveMemoryStatus(rec) || rec.Kind != KindAxiom {
		return conventionGateRule{}, "", false
	}
	repoKey := normalizeConventionRepoKey(rec.RepoID)
	if filterNormRepo != "" && repoKey != filterNormRepo {
		return conventionGateRule{}, "", false
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(rec.Topic)), "axiom:") {
		return conventionGateRule{}, "", false
	}
	rule, ok := matchConventionGateRule(rec.Topic)
	return rule, repoKey, ok
}

// mergeCanonicalAxiomSlot retains the highest-ID active axiom per key and supersedes duplicates.
func mergeCanonicalAxiomSlot(store *Store, canonical map[string]*Memory, mapKey string, rec *Memory) int {
	cur, exists := canonical[mapKey]
	if !exists {
		canonical[mapKey] = rec
		return 0
	}
	if rec.ID > cur.ID {
		canonical[mapKey] = rec
		if store.supersedeSingleRecord(cur.ID, rec.ID) {
			return 1
		}
		return 0
	}
	if store.supersedeSingleRecord(rec.ID, cur.ID) {
		return 1
	}
	return 0
}

// rebuildScrubbedAutonomicAxiomContent repairs 32-char truncated gate labels and scrubs workstation paths/stack offsets.
func rebuildScrubbedAutonomicAxiomContent(content, title string) string {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "[Autonomic Convention Axiom") {
		return scrubConventionAxiomBody(trimmed)
	}
	idx := strings.Index(trimmed, "] ")
	if idx < 0 {
		return scrubConventionAxiomBody(trimmed)
	}
	stats := axiomStatsClauseRe.FindString(trimmed[:idx+1])
	body := scrubConventionAxiomBody(trimmed[idx+2:])
	if stats == "" {
		return fmt.Sprintf("[Autonomic Convention Axiom · %s] %s", title, body)
	}
	return fmt.Sprintf("[Autonomic Convention Axiom · %s %s] %s", title, stats, body)
}

// scrubConventionAxiomBody strips ephemeral worktree paths, absolute checkout prefixes, and hex stack frame offsets.
func scrubConventionAxiomBody(body string) string {
	cleaned := worktreePrefixScrubRe.ReplaceAllString(strings.TrimSpace(body), "")
	cleaned = absRepoSubpathScrubRe.ReplaceAllString(cleaned, "$2")
	cleaned = stackOffsetScrubRe.ReplaceAllString(cleaned, "")
	return strings.TrimSpace(cleaned)
}

// collectOpenCoreConventionFamilies groups non-tombstoned convention pitfalls by (repo, canonical_gate_slug).
func collectOpenCoreConventionFamilies(records []*Memory, filterNormRepo string) map[string]*conventionFamilyBucket {
	out := make(map[string]*conventionFamilyBucket)
	for _, rec := range records {
		rule, repoKey, ok := matchEligibleConventionPitfall(rec, filterNormRepo)
		if ok {
			appendConventionPitfallToBucket(out, rec, repoKey, rule)
		}
	}
	return out
}

// matchEligibleConventionPitfall checks whether rec is a non-tombstoned, non-ephemeral convention pitfall in scope.
func matchEligibleConventionPitfall(rec *Memory, filterNormRepo string) (conventionGateRule, string, bool) {
	if rec == nil || rec.Kind != KindPitfall || rec.Status == StatusTombstoned || isEphemeralTestAssertionContent(rec.Content) {
		return conventionGateRule{}, "", false
	}
	repoKey := normalizeConventionRepoKey(rec.RepoID)
	if filterNormRepo != "" && repoKey != filterNormRepo {
		return conventionGateRule{}, "", false
	}
	rule, ok := matchConventionGateRule(extractGateSlugFromPitfallTopic(rec.Topic))
	return rule, repoKey, ok
}

// appendConventionPitfallToBucket records a convention pitfall hit inside its (repo, gate) bucket.
func appendConventionPitfallToBucket(out map[string]*conventionFamilyBucket, rec *Memory, repoKey string, rule conventionGateRule) {
	key := repoKey + "|" + rule.canonical
	fam := out[key]
	if fam == nil {
		fam = &conventionFamilyBucket{repoID: resolveCleanRepoLabel(rec.RepoID), gateSlug: rule.canonical, title: rule.title, scopes: make(map[string]bool)}
		out[key] = fam
	}
	fam.totalHits++
	if sc := extractScopeFromPitfallTopic(rec.Topic); sc != "" && sc != workspaceScopeLabel {
		fam.scopes[sc] = true
	}
	if isActiveMemoryStatus(rec) {
		fam.activeCards = append(fam.activeCards, rec)
		if fam.newest == nil || rec.ID > fam.newest.ID {
			fam.newest = rec
		}
	}
}

// upsertOpenCoreConventionAxiom creates or returns the canonical scrubbed convention axiom for a qualifying family.
func upsertOpenCoreConventionAxiom(store *Store, fam *conventionFamilyBucket, axTopic string, existing *Memory) *Memory {
	if existing != nil && existing.ID > 0 {
		return existing
	}
	if fam.newest == nil {
		return nil
	}
	scopes := make([]string, 0, len(fam.scopes))
	for sc := range fam.scopes {
		scopes = append(scopes, sc)
	}
	sort.Strings(scopes)
	body := fam.newest.Content
	if idx := strings.Index(body, "] "); idx >= 0 && idx+2 < len(body) {
		body = body[idx+2:]
	}
	taskID := strings.TrimSpace(fam.newest.TaskID)
	if taskID == "" {
		taskID = "MNEME-GOVERNOR"
	}
	rec := &Memory{
		RepoID:  fam.repoID,
		Kind:    KindAxiom,
		Topic:   axTopic,
		Content: fmt.Sprintf("[Autonomic Convention Axiom · %s (%dx across %d scopes)] %s", fam.title, fam.totalHits, max(1, len(scopes)), scrubConventionAxiomBody(body)),
		TaskID:  taskID,
		Status:  StatusActive,
	}
	if err := store.Save(rec); err != nil {
		return nil
	}
	return rec
}

// retireConventionHabitPatterns supersedes active pattern:habit:* records in favor of canonical convention axioms.
func retireConventionHabitPatterns(store *Store, records []*Memory, canonAxioms map[string]*Memory, filterNormRepo string) int {
	retired := 0
	for _, rec := range records {
		canonID, ok := resolveHabitRetirementTarget(rec, canonAxioms, filterNormRepo)
		if ok && store.supersedeSingleRecord(rec.ID, canonID) {
			retired++
		}
	}
	return retired
}

// resolveHabitRetirementTarget identifies an active pattern:habit:* row and resolves its canonical axiom ID when present.
func resolveHabitRetirementTarget(rec *Memory, canonAxioms map[string]*Memory, filterNormRepo string) (int, bool) {
	if !isActiveMemoryStatus(rec) || rec.Kind != KindPattern {
		return 0, false
	}
	lowerTopic := strings.ToLower(strings.TrimSpace(rec.Topic))
	if !strings.HasPrefix(lowerTopic, habitTopicPrefix) {
		return 0, false
	}
	repoKey := normalizeConventionRepoKey(rec.RepoID)
	if filterNormRepo != "" && repoKey != filterNormRepo {
		return 0, false
	}
	if rule, ok := matchConventionGateRule(lowerTopic); ok {
		if ax := canonAxioms[repoKey+"|axiom:"+rule.canonical]; ax != nil {
			return ax.ID, true
		}
	}
	return 0, true
}

// isActiveMemoryStatus returns true when rec is non-nil with empty or active lifecycle status.
func isActiveMemoryStatus(rec *Memory) bool {
	return rec != nil && (rec.Status == "" || rec.Status == StatusActive)
}

// normalizeConventionRepoKey normalizes a repository identifier for partition grouping.
func normalizeConventionRepoKey(raw string) string {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return "global"
	}
	return strings.ToLower(filepath.Base(clean))
}

// resolveCleanRepoLabel preserves clean repository identifiers while defaulting blanks to global.
func resolveCleanRepoLabel(raw string) string {
	base := strings.TrimSpace(filepath.Base(strings.TrimSpace(raw)))
	if base == "" || base == "." {
		return "global"
	}
	return base
}
