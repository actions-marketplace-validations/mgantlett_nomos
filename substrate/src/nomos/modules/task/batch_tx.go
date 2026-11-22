// Package task defines domain models, schema constraints, and tracking mechanisms
// enforced by compiled Go SSoT contracts.
package task

import (
	"database/sql"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/types"
)

func buildBatchRecords(nodes []TaskBatchNode, keys []string, ctx *batchBuildCtx) ([]TaskRecord, [][2]string, error) {
	records := make([]TaskRecord, len(nodes))
	for i, n := range nodes {
		rec, err := synthesizeBatchTaskRecord(n, keys[i], ctx)
		if err != nil {
			return nil, nil, err
		}
		records[i] = rec
	}
	externalTwins, err := linkBatchTwins(records, ctx)
	if err != nil {
		return nil, nil, err
	}
	return records, externalTwins, nil
}

func linkBatchTwins(records []TaskRecord, ctx *batchBuildCtx) ([][2]string, error) {
	var externalTwins [][2]string
	for i, rec := range records {
		if rec.TwinKey == "" {
			continue
		}
		if peerIdx, inBatch := ctx.batchIdx[rec.TwinKey]; inBatch {
			if records[peerIdx].Plane == rec.Plane {
				return nil, types.NewNomosError(types.ErrCodeValidationFailed, "twin tasks %s and %s must occupy opposite planes", rec.Key, rec.TwinKey)
			}
			records[peerIdx].TwinKey = rec.Key
			continue
		}
		peer, ok := ctx.existing[rec.TwinKey]
		if !ok {
			return nil, types.NewNomosError(types.ErrCodeValidationFailed, "twin task %s does not exist", rec.TwinKey)
		}
		if peer.Plane == rec.Plane {
			return nil, types.NewNomosError(types.ErrCodeValidationFailed, "twin tasks %s and %s must occupy opposite planes", rec.Key, rec.TwinKey)
		}
		externalTwins = append(externalTwins, [2]string{records[i].Key, rec.TwinKey})
	}
	return externalTwins, nil
}

func synthesizeBatchTaskRecord(n TaskBatchNode, key string, ctx *batchBuildCtx) (TaskRecord, error) {
	plane, err := normalizeBatchPlane(n.Plane, key)
	if err != nil {
		return TaskRecord{}, err
	}
	paths := NormalizeScopePaths(n.ScopePaths)
	obligations, err := NormalizeObligations(n.Obligations)
	if err != nil {
		return TaskRecord{}, err
	}
	ac, err := normalizeACItems(n.AcceptanceCriteria)
	if err != nil {
		return TaskRecord{}, err
	}
	body, err := RenderTaskCardBody(TaskCardInput{
		Scope: n.Scope, ScopePaths: paths, Obligations: obligations,
		AcceptanceCriteria: ac, Verification: n.VerifyCmd,
	})
	if err != nil {
		return TaskRecord{}, err
	}
	burden, err := DeriveTaskBurden(n.Burden, body)
	if err != nil {
		return TaskRecord{}, err
	}
	deps, err := resolveBatchDependencies(key, n.DependsOn, ctx)
	if err != nil {
		return TaskRecord{}, err
	}
	twin, err := resolveBatchRefOrKey(n.TwinOf, ctx.refs)
	if err != nil {
		return TaskRecord{}, err
	}
	rec := TaskRecord{
		Key: key, Plane: plane, Project: ctx.project, SchemaType: SchemaType(plane),
		Title: strings.TrimSpace(n.Title), Description: body,
		ScopePaths: paths, Obligations: obligations, AcceptanceCriteria: ac,
		VerifyCmd: strings.TrimSpace(n.VerifyCmd), Status: StatusTodo,
		Tier: normalizeBatchTier(n.Tier), Priority: normalizeBatchPriority(n.Priority),
		CreatedAt: ctx.now, UpdatedAt: ctx.now, Dependencies: deps, TwinKey: twin, Burden: burden,
	}
	return rec, ValidateTaskRecord(&rec)
}

func normalizeBatchTier(raw string) Tier {
	if strings.EqualFold(strings.TrimSpace(raw), "T2") {
		return Tier2
	}
	return Tier1
}

func normalizeBatchPriority(p int) int {
	if p <= 0 {
		return 50
	}
	return p
}

func resolveBatchDependencies(selfKey string, rawDeps []string, ctx *batchBuildCtx) ([]string, error) {
	seen := make(map[string]bool, len(rawDeps))
	var out []string
	for _, item := range rawDeps {
		for _, part := range strings.Split(item, ",") {
			if strings.TrimSpace(part) == "" {
				continue
			}
			depKey, err := resolveBatchRefOrKey(part, ctx.refs)
			if err != nil {
				return nil, err
			}
			if depKey == selfKey {
				return nil, types.NewNomosError(types.ErrCodeValidationFailed, "task %s cannot depend on itself", selfKey)
			}
			_, inBatch := ctx.batchIdx[depKey]
			_, inStore := ctx.existing[depKey]
			if !inBatch && !inStore {
				return nil, types.NewNomosError(types.ErrCodeValidationFailed, "dependency task %s does not exist", depKey)
			}
			if !seen[depKey] {
				seen[depKey] = true
				out = append(out, depKey)
			}
		}
	}
	return out, nil
}

func resolveBatchRefOrKey(raw string, refs map[string]string) (string, error) {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return "", nil
	}
	if strings.HasPrefix(clean, "$") {
		resolved, ok := refs[clean]
		if !ok {
			return "", types.NewNomosError(types.ErrCodeValidationFailed, "unknown symbolic batch ref %q", clean)
		}
		return resolved, nil
	}
	upper := strings.ToUpper(clean)
	if err := ValidateTaskKey(upper); err != nil {
		return "", err
	}
	return upper, nil
}

func validateAcyclicBatchGraph(records []TaskRecord, existing map[string]TaskRecord) error {
	adj := make(map[string][]string, len(existing)+len(records))
	for k, rec := range existing {
		adj[k] = rec.Dependencies
	}
	for _, rec := range records {
		adj[rec.Key] = rec.Dependencies
	}
	state := make(map[string]int, len(adj))
	for _, rec := range records {
		if state[rec.Key] == 0 {
			if err := visitBatchDAGNode(rec.Key, adj, state); err != nil {
				return err
			}
		}
	}
	return nil
}

func visitBatchDAGNode(k string, adj map[string][]string, state map[string]int) error {
	state[k] = 1
	for _, dep := range adj[k] {
		if state[dep] == 1 {
			return types.NewNomosError(types.ErrCodeValidationFailed, "causal DAG cycle detected involving %s -> %s", k, dep)
		}
		if state[dep] == 0 {
			if err := visitBatchDAGNode(dep, adj, state); err != nil {
				return err
			}
		}
	}
	state[k] = 2
	return nil
}

func commitBatchRecords(store TaskStore, records []TaskRecord, externalTwins [][2]string) error {
	if sqlStore, ok := store.(*SQLiteTaskStore); ok {
		return commitSQLiteBatchTx(sqlStore, records, externalTwins)
	}
	for _, rec := range records {
		if err := store.CreateTask(rec); err != nil {
			return err
		}
	}
	for _, pair := range externalTwins {
		if err := store.BindTwin(pair[0], pair[1]); err != nil {
			return err
		}
	}
	return nil
}

func commitSQLiteBatchTx(s *SQLiteTaskStore, records []TaskRecord, externalTwins [][2]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return types.NewNomosError(types.ErrCodeSubstrate, "failed starting task batch transaction: %v", err)
	}
	for _, rec := range records {
		if err := insertTaskTx(tx, rec); err != nil {
			rollbackBatchTx(tx)
			return err
		}
	}
	for _, pair := range externalTwins {
		if _, err := tx.Exec(`UPDATE tasks SET twin_key = ?, updated_at = ? WHERE key = ?`, pair[0], nowBatchTimestamp(), pair[1]); err != nil {
			rollbackBatchTx(tx)
			return types.NewNomosError(types.ErrCodeSubstrate, "failed binding external twin %s: %v", pair[1], err)
		}
	}
	if err := tx.Commit(); err != nil {
		return types.NewNomosError(types.ErrCodeSubstrate, "failed committing task batch transaction: %v", err)
	}
	return nil
}

func rollbackBatchTx(tx *sql.Tx) {
	if rbErr := tx.Rollback(); rbErr != nil {
		// transaction already closed or rolled back
	}
}

func insertTaskTx(tx *sql.Tx, t TaskRecord) error {
	query := `INSERT INTO tasks (
		key, plane, project, schema_type, title, description,
		scope_paths, obligations, acceptance_criteria, verify_cmd,
		status, tier, priority, created_at, updated_at, completed_at,
		dependencies, twin_key, arc_id, intent_delta, burden, context_burden, arch_layer
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := tx.Exec(query,
		t.Key, t.Plane, t.Project, string(t.SchemaType), t.Title, t.Description,
		marshalStringSlice(t.ScopePaths), marshalStringSlice(t.Obligations), marshalStringSlice(t.AcceptanceCriteria), t.VerifyCmd,
		string(t.Status), string(t.Tier), t.Priority, t.CreatedAt, t.UpdatedAt,
		t.CompletedAt, marshalStringSlice(t.Dependencies), t.TwinKey, t.ArcID,
		t.IntentDelta, t.Burden, t.ContextBurden, t.ArchLayer,
	)
	if err != nil {
		return types.NewNomosError(types.ErrCodeSubstrate, "failed inserting batch task %s: %v", t.Key, err)
	}
	return nil
}
