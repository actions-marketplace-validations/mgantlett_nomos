// Package task defines domain models, schema constraints, and tracking mechanisms
// enforced by compiled Go SSoT contracts.
package task

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/types"
)

// TaskBatchNode declares one node inside an atomic Agent-to-Agent DAG subgraph (#TaskBatchNode).
type TaskBatchNode struct {
	Ref                string   `json:"ref,omitempty"`
	Key                string   `json:"key,omitempty"`
	Plane              string   `json:"plane"`
	Title              string   `json:"title"`
	Scope              string   `json:"scope"`
	ScopePaths         []string `json:"scope_paths,omitempty"`
	Obligations        []string `json:"obligations,omitempty"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	VerifyCmd          string   `json:"verify_cmd"`
	DependsOn          []string `json:"depends_on,omitempty"`
	TwinOf             string   `json:"twin_of,omitempty"`
	Tier               string   `json:"tier,omitempty"`
	Priority           int      `json:"priority,omitempty"`
	Burden             int      `json:"burden,omitempty"`
}

// TaskBatchManifest encapsulates a multi-node DAG subgraph submitted via nomos task batch (#TaskBatchManifest).
type TaskBatchManifest struct {
	Project string          `json:"project,omitempty"`
	Tasks   []TaskBatchNode `json:"tasks"`
}

// TaskBatchResult reports allocated canonical keys and resolved symbolic refs from nomos task batch (#TaskBatchResult).
type TaskBatchResult struct {
	Project      string            `json:"project"`
	CreatedKeys  []string          `json:"created_keys"`
	ResolvedRefs map[string]string `json:"resolved_refs,omitempty"`
	Tasks        []TaskRecord      `json:"tasks"`
}

type batchBuildCtx struct {
	project  string
	now      string
	nextInt  int
	nextSub  int
	existing map[string]TaskRecord
	usedKeys map[string]bool
	refs     map[string]string
	batchIdx map[string]int
}

// ExecuteTaskBatch validates an Agent-to-Agent DAG subgraph, rejects cycles, resolves $ref links,
// and commits all nodes atomically (@intent task_lifecycle_cli_spec.md #TaskBatchManifest).
func ExecuteTaskBatch(store TaskStore, rawJSON []byte, defaultProject string) (*TaskBatchResult, error) {
	manifest, err := decodeTaskBatchManifest(rawJSON)
	if err != nil {
		return nil, err
	}
	project := strings.TrimSpace(manifest.Project)
	if project == "" {
		project = strings.TrimSpace(defaultProject)
	}
	existing := indexTasksByKey(store.ListTasks())
	ctx := &batchBuildCtx{
		project:  project,
		now:      nowBatchTimestamp(),
		nextInt:  maxKeyNumber(existing, "INT-"),
		nextSub:  maxKeyNumber(existing, "SUB-"),
		existing: existing,
		usedKeys: make(map[string]bool, len(manifest.Tasks)),
		refs:     make(map[string]string),
		batchIdx: make(map[string]int, len(manifest.Tasks)),
	}
	keys, err := allocateBatchKeysAndRefs(manifest.Tasks, ctx)
	if err != nil {
		return nil, err
	}
	records, externalTwins, err := buildBatchRecords(manifest.Tasks, keys, ctx)
	if err != nil {
		return nil, err
	}
	if err := validateAcyclicBatchGraph(records, existing); err != nil {
		return nil, err
	}
	if err := commitBatchRecords(store, records, externalTwins); err != nil {
		return nil, err
	}
	return &TaskBatchResult{
		Project:      project,
		CreatedKeys:  keys,
		ResolvedRefs: ctx.refs,
		Tasks:        records,
	}, nil
}

func decodeTaskBatchManifest(rawJSON []byte) (*TaskBatchManifest, error) {
	trimmed := strings.TrimSpace(string(rawJSON))
	if trimmed == "" {
		return nil, types.NewNomosError(types.ErrCodeValidationFailed, "task batch JSON payload cannot be empty")
	}
	var manifest TaskBatchManifest
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal([]byte(trimmed), &manifest.Tasks); err != nil {
			return nil, types.NewNomosError(types.ErrCodeValidationFailed, "invalid task batch JSON array: %v", err)
		}
	} else if err := json.Unmarshal([]byte(trimmed), &manifest); err != nil {
		return nil, types.NewNomosError(types.ErrCodeValidationFailed, "invalid task batch JSON manifest: %v", err)
	}
	if len(manifest.Tasks) == 0 {
		return nil, types.NewNomosError(types.ErrCodeValidationFailed, "task batch manifest must declare at least one task")
	}
	return &manifest, nil
}

func indexTasksByKey(all []TaskRecord) map[string]TaskRecord {
	idx := make(map[string]TaskRecord, len(all))
	for _, rec := range all {
		idx[rec.Key] = rec
	}
	return idx
}

func allocateBatchKeysAndRefs(nodes []TaskBatchNode, ctx *batchBuildCtx) ([]string, error) {
	keys := make([]string, len(nodes))
	for i, n := range nodes {
		plane, err := normalizeBatchPlane(n.Plane, n.Key)
		if err != nil {
			return nil, err
		}
		key, err := chooseBatchNodeKey(n.Key, plane, ctx)
		if err != nil {
			return nil, err
		}
		ctx.usedKeys[key] = true
		ctx.batchIdx[key] = i
		keys[i] = key
		if err := registerBatchRef(n.Ref, key, ctx.refs); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

func normalizeBatchPlane(rawPlane, rawKey string) (string, error) {
	p := strings.ToLower(strings.TrimSpace(rawPlane))
	if p == "" && strings.HasPrefix(strings.ToUpper(strings.TrimSpace(rawKey)), "INT-") {
		p = "intent"
	} else if p == "" && strings.HasPrefix(strings.ToUpper(strings.TrimSpace(rawKey)), "SUB-") {
		p = "substrate"
	}
	if p != "intent" && p != "substrate" {
		return "", types.NewNomosError(types.ErrCodeValidationFailed, "batch task plane must be 'intent' or 'substrate', got %q", rawPlane)
	}
	return p, nil
}

func chooseBatchNodeKey(rawKey, plane string, ctx *batchBuildCtx) (string, error) {
	if clean := strings.ToUpper(strings.TrimSpace(rawKey)); clean != "" {
		if err := ValidateTaskKey(clean); err != nil {
			return "", err
		}
		if _, exists := ctx.existing[clean]; exists || ctx.usedKeys[clean] {
			return "", types.NewNomosError(types.ErrCodeInvariant, "batch task key %s already exists", clean)
		}
		return clean, nil
	}
	if plane == "intent" {
		ctx.nextInt++
		return FormatTaskKey("INT", ctx.nextInt)
	}
	ctx.nextSub++
	return FormatTaskKey("SUB", ctx.nextSub)
}

func registerBatchRef(rawRef, key string, refs map[string]string) error {
	clean := strings.TrimPrefix(strings.TrimSpace(rawRef), "$")
	if clean == "" {
		return nil
	}
	refToken := "$" + clean
	if _, dup := refs[refToken]; dup {
		return types.NewNomosError(types.ErrCodeValidationFailed, "duplicate symbolic batch ref %q", refToken)
	}
	refs[refToken] = key
	return nil
}

func maxKeyNumber(existing map[string]TaskRecord, prefix string) int {
	maxN := 0
	for k := range existing {
		if strings.HasPrefix(k, prefix) {
			parts := strings.Split(k, "-")
			if len(parts) >= 2 {
				if n, err := strconv.Atoi(parts[1]); err == nil && n > maxN {
					maxN = n
				}
			}
		}
	}
	return maxN
}

func nowBatchTimestamp() string {
	return time.Now().UTC().Format(time.RFC3339)
}
