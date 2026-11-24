// Package mem attaches superseded lineage and same-topic dead-end guardrails during episodic recall.
// @intent mneme_memory_spec.md #RecallGuardrail
package mem

import (
	"sort"
	"strings"
)

const (
	relationSupersededLineage = "superseded_lineage"
	relationTopicDeadEnd      = "topic_dead_end"
	maxGuardrailsPerResult    = 3
	maxLineageWalkHops        = 2
	maxRetentionChainHops     = 8
	guardrailSnippetMaxLen    = 110
	defaultGeneralTopicLower  = "general"
)

// RecallGuardrail summarizes a superseded ancestor or topic dead-end warning attached during recall (#RecallGuardrail).
type RecallGuardrail struct {
	MemoryID int        `json:"memory_id"`
	Kind     MemoryKind `json:"kind"`
	Relation string     `json:"relation"`
	Summary  string     `json:"summary"`
}

// guardrailIndex caches reverse SupersededBy edges and active topic-scoped dead-end/pitfall records.
type guardrailIndex struct {
	bySuccessor map[int][]*Memory
	byTopicWarn map[string][]*Memory
}

// attachRecallGuardrails enriches ranked SearchResult entries with superseded lineage and topic dead-end warnings.
// @intent mneme_memory_spec.md #SearchResult
func attachRecallGuardrails(results []*SearchResult, all []*Memory) {
	if len(results) == 0 || len(all) == 0 {
		return
	}
	idx := buildGuardrailIndex(all)
	for _, res := range results {
		if res == nil || res.Memory == nil {
			continue
		}
		res.Guardrails = collectResultGuardrails(res.Memory, idx)
	}
}

// buildGuardrailIndex indexes superseded ancestors by successor ID and active warnings by normalized topic.
func buildGuardrailIndex(all []*Memory) *guardrailIndex {
	idx := &guardrailIndex{
		bySuccessor: make(map[int][]*Memory),
		byTopicWarn: make(map[string][]*Memory),
	}
	for _, rec := range all {
		if rec == nil {
			continue
		}
		if rec.Status == StatusSuperseded && rec.SupersededBy > 0 {
			idx.bySuccessor[rec.SupersededBy] = append(idx.bySuccessor[rec.SupersededBy], rec)
			continue
		}
		if (rec.Status == StatusActive || rec.Status == "") && isWarningMemoryKind(rec.Kind) {
			if key := normalizeTopicKey(rec.Topic); key != "" {
				idx.byTopicWarn[key] = append(idx.byTopicWarn[key], rec)
			}
		}
	}
	for succID := range idx.bySuccessor {
		list := idx.bySuccessor[succID]
		sort.Slice(list, func(i, j int) bool { return list[i].ID > list[j].ID })
	}
	for tKey := range idx.byTopicWarn {
		list := idx.byTopicWarn[tKey]
		sort.Slice(list, func(i, j int) bool { return list[i].ID > list[j].ID })
	}
	return idx
}

// collectResultGuardrails gathers up to maxGuardrailsPerResult lineage and dead-end warnings for target.
func collectResultGuardrails(target *Memory, idx *guardrailIndex) []RecallGuardrail {
	seen := map[int]bool{target.ID: true}
	var out []RecallGuardrail
	targetSig := NormalizeMemorySignature(target.RepoID, target.Kind, target.Content)

	appendLineageGuardrails(target.ID, targetSig, idx, seen, &out)
	if len(out) < maxGuardrailsPerResult && isGuardrailEligibleKind(target.Kind) {
		appendTopicDeadEndGuardrails(target, idx, seen, &out)
	}
	return out
}

// appendLineageGuardrails walks up to maxLineageWalkHops backward along SupersededBy while skipping duplicate clones.
func appendLineageGuardrails(startID int, targetSig string, idx *guardrailIndex, seen map[int]bool, out *[]RecallGuardrail) {
	frontier := []int{startID}
	for hop := 0; hop < maxLineageWalkHops && len(frontier) > 0 && len(*out) < maxGuardrailsPerResult; hop++ {
		var next []int
		for _, curID := range frontier {
			for _, anc := range idx.bySuccessor[curID] {
				if seen[anc.ID] {
					continue
				}
				seen[anc.ID] = true
				next = append(next, anc.ID)
				if NormalizeMemorySignature(anc.RepoID, anc.Kind, anc.Content) == targetSig {
					continue
				}
				*out = append(*out, RecallGuardrail{
					MemoryID: anc.ID,
					Kind:     anc.Kind,
					Relation: relationSupersededLineage,
					Summary:  formatSnippet(anc.Content, guardrailSnippetMaxLen),
				})
				if len(*out) >= maxGuardrailsPerResult {
					return
				}
			}
		}
		frontier = next
	}
}

// appendTopicDeadEndGuardrails attaches same-repo active dead_end and pitfall warnings matching target.Topic.
func appendTopicDeadEndGuardrails(target *Memory, idx *guardrailIndex, seen map[int]bool, out *[]RecallGuardrail) {
	tKey := normalizeTopicKey(target.Topic)
	if tKey == "" {
		return
	}
	for _, cand := range idx.byTopicWarn[tKey] {
		if seen[cand.ID] || !sameOrGlobalRepo(target.RepoID, cand.RepoID) {
			continue
		}
		seen[cand.ID] = true
		*out = append(*out, RecallGuardrail{
			MemoryID: cand.ID,
			Kind:     cand.Kind,
			Relation: relationTopicDeadEnd,
			Summary:  formatSnippet(cand.Content, guardrailSnippetMaxLen),
		})
		if len(*out) >= maxGuardrailsPerResult {
			return
		}
	}
}

// isProtectedEvolvedLineageIDSet identifies superseded ancestors whose SupersededBy chain reaches a distinct active record.
// @intent mneme_memory_spec.md #MaintainReport
func isProtectedEvolvedLineageIDSet(all []*Memory) map[int]bool {
	byID := make(map[int]*Memory, len(all))
	for _, rec := range all {
		if rec != nil {
			byID[rec.ID] = rec
		}
	}
	protected := make(map[int]bool)
	for _, rec := range all {
		if rec == nil || rec.Status != StatusSuperseded || rec.SupersededBy <= 0 {
			continue
		}
		if term := resolveActiveLineageTerminal(rec, byID); term != nil {
			if NormalizeMemorySignature(rec.RepoID, rec.Kind, rec.Content) != NormalizeMemorySignature(term.RepoID, term.Kind, term.Content) {
				protected[rec.ID] = true
			}
		}
	}
	return protected
}

// resolveActiveLineageTerminal follows SupersededBy pointers up to maxRetentionChainHops to locate an active successor.
func resolveActiveLineageTerminal(start *Memory, byID map[int]*Memory) *Memory {
	cur := start
	visited := map[int]bool{start.ID: true}
	for hop := 0; hop < maxRetentionChainHops && cur.SupersededBy > 0; hop++ {
		next := byID[cur.SupersededBy]
		if next == nil || visited[next.ID] {
			return nil
		}
		if next.Status == StatusActive || next.Status == "" {
			return next
		}
		if next.Status != StatusSuperseded {
			return nil
		}
		visited[next.ID] = true
		cur = next
	}
	return nil
}

// isWarningMemoryKind reports whether k represents a dead_end or statutory pitfall warning.
func isWarningMemoryKind(k MemoryKind) bool {
	norm := NormalizeMemoryKind(k)
	return norm == KindDeadEnd || norm == KindPitfall
}

// isGuardrailEligibleKind reports whether recalled kind k should receive same-topic dead_end guardrails.
func isGuardrailEligibleKind(k MemoryKind) bool {
	switch NormalizeMemoryKind(k) {
	case KindDecision, KindAxiom, KindArchitecture, KindPattern, KindReflection:
		return true
	default:
		return false
	}
}

// normalizeTopicKey canonicalizes non-generic memory topics for guardrail matching.
func normalizeTopicKey(topic string) string {
	norm := strings.ToLower(strings.TrimSpace(topic))
	if norm == "" || norm == defaultGeneralTopicLower {
		return ""
	}
	return norm
}

// sameOrGlobalRepo reports whether candidateRepo applies to targetRepo during repository-scoped recall.
func sameOrGlobalRepo(targetRepo, candidateRepo string) bool {
	t := strings.ToLower(strings.TrimSpace(targetRepo))
	c := strings.ToLower(strings.TrimSpace(candidateRepo))
	if t == "" || c == "" || c == defaultGlobalRepoTag {
		return true
	}
	return t == c
}
