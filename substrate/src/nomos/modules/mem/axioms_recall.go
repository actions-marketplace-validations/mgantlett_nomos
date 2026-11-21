// Package mem implements repository-scoped axiom recall and task-deduplicated reinforcement for Nomos handshakes.
package mem

import (
	"path/filepath"
	"sort"
	"strings"
)

// RecallRepoAxiomsReadOnly retrieves up to limit active repository-scoped axioms without incrementing access_count (@intent: #Memory).
func RecallRepoAxiomsReadOnly(repoRoot, queryHint string, touchedFiles []string, limit int) []string {
	return recallRepoAxiomsWithTask(repoRoot, "", queryHint, touchedFiles, limit, false)
}

// RecallRepoAxiomsForTask retrieves up to limit active repository-scoped axioms and reinforces each at most once per taskKey (@intent: #Memory).
func RecallRepoAxiomsForTask(repoRoot, taskKey, queryHint string, touchedFiles []string, limit int) []string {
	return recallRepoAxiomsWithTask(repoRoot, taskKey, queryHint, touchedFiles, limit, true)
}

// recallRepoAxiomsWithTask ranks active KindAxiom memories partitioned to repoRoot and optionally records task access.
func recallRepoAxiomsWithTask(repoRoot, taskKey, queryHint string, touchedFiles []string, limit int, recordAccess bool) []string {
	if strings.TrimSpace(repoRoot) == "" {
		return nil
	}
	if limit <= 0 {
		limit = 2
	}
	store, err := NewStore("")
	if err != nil {
		return nil
	}
	defer func() {
		if cErr := store.Close(); cErr != nil {
			// closed
		}
	}()

	all, err := store.ListAll()
	if err != nil {
		return nil
	}
	repoID := strings.ToLower(filepath.Base(filepath.Clean(repoRoot)))
	candidates := filterActiveRepoAxioms(all, repoID)
	if len(candidates) == 0 {
		return nil
	}
	combinedHint := buildAxiomQueryHint(queryHint, touchedFiles)
	rankRepoAxioms(candidates, combinedHint)
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return collectRecalledAxiomContents(store, repoRoot, taskKey, candidates, recordAccess)
}

// filterActiveRepoAxioms returns active KindAxiom records strictly matching repoID.
func filterActiveRepoAxioms(all []*Memory, repoID string) []*Memory {
	var out []*Memory
	for _, m := range all {
		if m == nil || m.Kind != KindAxiom || m.Status != StatusActive {
			continue
		}
		if strings.ToLower(strings.TrimSpace(m.RepoID)) == repoID {
			out = append(out, m)
		}
	}
	return out
}

// buildAxiomQueryHint joins task title and modified path tokens into a lexical/subword ranking query.
func buildAxiomQueryHint(queryHint string, touchedFiles []string) string {
	parts := make([]string, 0, 1+len(touchedFiles))
	if trimmed := strings.TrimSpace(queryHint); trimmed != "" {
		parts = append(parts, trimmed)
	}
	for _, f := range touchedFiles {
		if clean := strings.TrimSpace(filepath.ToSlash(f)); clean != "" {
			parts = append(parts, clean)
		}
	}
	return strings.Join(parts, " ")
}

// rankRepoAxioms orders candidate repo axioms by query similarity, reinforcement, and recency.
func rankRepoAxioms(candidates []*Memory, combinedHint string) {
	var queryEmb []float32
	if combinedHint != "" {
		if emb, err := GenerateEmbedding(combinedHint); err == nil {
			queryEmb = emb
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if len(queryEmb) > 0 {
			iSim := CombinedScore(combinedHint, queryEmb, candidates[i].Topic+" "+candidates[i].Content, candidates[i].Embedding)
			jSim := CombinedScore(combinedHint, queryEmb, candidates[j].Topic+" "+candidates[j].Content, candidates[j].Embedding)
			if iSim != jSim {
				return iSim > jSim
			}
		}
		if candidates[i].AccessCount != candidates[j].AccessCount {
			return candidates[i].AccessCount > candidates[j].AccessCount
		}
		return candidates[i].ID > candidates[j].ID
	})
}

// collectRecalledAxiomContents extracts axiom text and reinforces access_count at most once per (taskKey, axiomID).
func collectRecalledAxiomContents(store *Store, repoRoot, taskKey string, candidates []*Memory, recordAccess bool) []string {
	out := make([]string, 0, len(candidates))
	hasTask := strings.TrimSpace(taskKey) != ""
	for _, c := range candidates {
		if recordAccess && hasTask && shouldRecordTaskAccess(repoRoot, taskKey, c.ID) {
			if aErr := store.RecordAccess(c.ID); aErr != nil {
				// best effort
			}
		}
		out = append(out, c.Content)
	}
	return out
}
