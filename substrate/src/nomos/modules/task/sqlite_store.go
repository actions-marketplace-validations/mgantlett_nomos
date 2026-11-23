// Package task defines domain models, schema constraints, and tracking mechanisms
// enforced by compiled Go SSoT contracts.
package task

import (
	"database/sql"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/storage"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/types"
)

// SQLiteTaskStore implements TaskStore backed by pure-Go SQLite.
type SQLiteTaskStore struct {
	db *sql.DB
	mu sync.Mutex
}

// NewSQLiteTaskStore instantiates a new SQLite-backed TaskStore.
func NewSQLiteTaskStore(db *sql.DB) *SQLiteTaskStore {
	return &SQLiteTaskStore{db: db}
}

// NewDefaultSQLiteTaskStore opens the global Nomos database dynamically.
func NewDefaultSQLiteTaskStore() (*SQLiteTaskStore, error) {
	db, err := storage.OpenDB("")
	if err != nil {
		return nil, err
	}
	return NewSQLiteTaskStore(db), nil
}

// CreateTask registers a new task in SQLite, enforcing structural schema invariants.
func (s *SQLiteTaskStore) CreateTask(t TaskRecord) error {
	if err := ValidateTaskRecord(&t); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	depJSON := marshalStringSlice(t.Dependencies)
	pathsJSON := marshalStringSlice(t.ScopePaths)
	oblJSON := marshalStringSlice(t.Obligations)
	acJSON := marshalStringSlice(t.AcceptanceCriteria)
	now := time.Now().UTC().Format(time.RFC3339)
	if t.CreatedAt == "" {
		t.CreatedAt = now
	}
	if t.UpdatedAt == "" {
		t.UpdatedAt = now
	}
	if t.Status == "" {
		t.Status = StatusReady
	}
	if t.Tier == "" {
		t.Tier = Tier1
	}

	query := `INSERT INTO tasks (
		key, plane, project, schema_type, title, description,
		scope_paths, obligations, acceptance_criteria, verify_cmd,
		status, tier, priority, created_at, updated_at, completed_at,
		dependencies, twin_key, arc_id, intent_delta, burden, context_burden, arch_layer
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := s.db.Exec(query,
		t.Key, t.Plane, t.Project, string(t.SchemaType), t.Title, t.Description,
		pathsJSON, oblJSON, acJSON, t.VerifyCmd,
		string(t.Status), string(t.Tier), t.Priority, t.CreatedAt, t.UpdatedAt,
		t.CompletedAt, depJSON, t.TwinKey, t.ArcID,
		t.IntentDelta, t.Burden, t.ContextBurden, t.ArchLayer,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return types.NewNomosError(types.ErrCodeInvariant, "task %s already exists", t.Key)
		}
		return types.NewNomosError(types.ErrCodeSubstrate, "failed to insert task %s: %v", t.Key, err)
	}
	return nil
}

const selectTaskColumns = `SELECT id, key, plane, project, schema_type, title, description,
	scope_paths, obligations, acceptance_criteria, verify_cmd,
	status, tier, priority, created_at, updated_at, completed_at, dependencies,
	twin_key, arc_id, intent_delta, burden, context_burden, arch_layer FROM tasks`

// GetTask retrieves a task record by canonical key.
func (s *SQLiteTaskStore) GetTask(key string) (*TaskRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	row := s.db.QueryRow(selectTaskColumns+" WHERE key = ?", key)
	rec, err := scanTaskRow(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, types.NewNomosError(types.ErrCodeValidationFailed, "task %s not found", key)
		}
		return nil, types.NewNomosError(types.ErrCodeSubstrate, "failed to get task %s: %v", key, err)
	}
	return rec, nil
}

// UpdateTask updates an existing task record.
func (s *SQLiteTaskStore) UpdateTask(t TaskRecord) error {
	if err := ValidateTaskRecord(&t); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	depJSON := marshalStringSlice(t.Dependencies)
	pathsJSON := marshalStringSlice(t.ScopePaths)
	oblJSON := marshalStringSlice(t.Obligations)
	acJSON := marshalStringSlice(t.AcceptanceCriteria)
	t.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	query := `UPDATE tasks SET
		project = ?, schema_type = ?, title = ?, description = ?,
		scope_paths = ?, obligations = ?, acceptance_criteria = ?, verify_cmd = ?,
		status = ?, tier = ?, priority = ?, updated_at = ?, completed_at = ?, dependencies = ?,
		twin_key = ?, arc_id = ?, intent_delta = ?, burden = ?,
		context_burden = ?, arch_layer = ?
		WHERE key = ?`

	res, err := s.db.Exec(query,
		t.Project, string(t.SchemaType), t.Title, t.Description,
		pathsJSON, oblJSON, acJSON, t.VerifyCmd,
		string(t.Status), string(t.Tier), t.Priority, t.UpdatedAt, t.CompletedAt, depJSON,
		t.TwinKey, t.ArcID, t.IntentDelta, t.Burden,
		t.ContextBurden, t.ArchLayer, t.Key,
	)
	if err != nil {
		return types.NewNomosError(types.ErrCodeSubstrate, "failed to update task %s: %v", t.Key, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return types.NewNomosError(types.ErrCodeSubstrate, "failed to verify rows affected: %v", err)
	}
	if rows == 0 {
		return types.NewNomosError(types.ErrCodeValidationFailed, "task %s not found", t.Key)
	}
	return nil
}

// ListTasks returns all registered task records.
func (s *SQLiteTaskStore) ListTasks() []TaskRecord {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(selectTaskColumns + " ORDER BY id ASC")
	if err != nil {
		return nil
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			// ignore or log
		}
	}()

	var list []TaskRecord
	for rows.Next() {
		rec, err := scanTaskRow(rows)
		if err == nil && rec != nil {
			list = append(list, *rec)
		}
	}
	return list
}

// AddDependency declares that targetKey depends on blockedByKey.
func (s *SQLiteTaskStore) AddDependency(targetKey, blockedByKey string) error {
	target, err := s.GetTask(targetKey)
	if err != nil {
		return err
	}
	if _, err := s.GetTask(blockedByKey); err != nil {
		return err
	}

	for _, dep := range target.Dependencies {
		if dep == blockedByKey {
			return nil
		}
	}
	target.Dependencies = append(target.Dependencies, blockedByKey)
	return s.UpdateTask(*target)
}

// GetWavefront returns unblocked candidate tasks ready for concurrent execution.
func (s *SQLiteTaskStore) GetWavefront() ([]WavefrontCandidate, error) {
	allTasks := s.ListTasks()
	allMap := make(map[string]TaskRecord, len(allTasks))
	for _, t := range allTasks {
		allMap[t.Key] = t
	}

	var wavefront []WavefrontCandidate
	for _, t := range allTasks {
		if IsTerminalStatus(t.Status) {
			continue
		}
		if isTaskUnblocked(t, allMap) {
			wavefront = append(wavefront, WavefrontCandidate{
				Key:            t.Key,
				Title:          t.Title,
				Plane:          t.Plane,
				Project:        t.Project,
				SchemaType:     t.SchemaType,
				Status:         t.Status,
				Tier:           t.Tier,
				TwinKey:        t.TwinKey,
				ScopePaths:     t.ScopePaths,
				Obligations:    t.Obligations,
				OverlapsActive: ComputeOverlappingActiveKeys(t, allTasks),
				IsUnblocked:    true,
			})
		}
	}
	return wavefront, nil
}

func marshalStringSlice(items []string) string {
	if len(items) == 0 {
		return "[]"
	}
	b, err := json.Marshal(items)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func unmarshalStringSlice(ns sql.NullString) []string {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(ns.String), &out); err != nil || len(out) == 0 {
		return nil
	}
	return out
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTaskRow(scanner rowScanner) (*TaskRecord, error) {
	var (
		rec TaskRecord
		row scannedTaskRow
	)
	err := scanner.Scan(
		&row.nullID, &rec.Key, &rec.Plane, &row.project, &row.schemaType, &rec.Title, &row.desc,
		&row.pathsJSON, &row.oblJSON, &row.acJSON, &row.verifyCmd,
		&row.status, &row.tier, &row.priority, &rec.CreatedAt, &rec.UpdatedAt,
		&row.completedAt, &row.depJSON, &row.twinKey, &row.arcID,
		&row.intentDelta, &row.burden, &row.ctxBurden, &row.archLayer,
	)
	if err != nil {
		return nil, err
	}
	rec.Project = row.project.String
	mapScannedFields(&rec, &row)
	mapScannedMetadata(&rec, &row)
	return &rec, nil
}

type scannedTaskRow struct {
	nullID      sql.NullInt64
	project     sql.NullString
	schemaType  sql.NullString
	desc        sql.NullString
	pathsJSON   sql.NullString
	oblJSON     sql.NullString
	acJSON      sql.NullString
	verifyCmd   sql.NullString
	status      sql.NullString
	tier        sql.NullString
	priority    sql.NullInt64
	completedAt sql.NullString
	depJSON     sql.NullString
	twinKey     sql.NullString
	arcID       sql.NullString
	intentDelta sql.NullString
	burden      sql.NullInt64
	ctxBurden   sql.NullInt64
	archLayer   sql.NullInt64
}

func mapScannedFields(rec *TaskRecord, row *scannedTaskRow) {
	if row.nullID.Valid {
		rec.ID = int(row.nullID.Int64)
	}
	rec.Description = row.desc.String
	rec.ScopePaths = unmarshalStringSlice(row.pathsJSON)
	rec.Obligations = unmarshalStringSlice(row.oblJSON)
	rec.AcceptanceCriteria = unmarshalStringSlice(row.acJSON)
	rec.VerifyCmd = row.verifyCmd.String
	rec.TwinKey = row.twinKey.String
	rec.SchemaType = SchemaType(row.schemaType.String)
	if rec.SchemaType == "" {
		rec.SchemaType = SchemaType(rec.Plane)
	}
	rec.Status = Status(row.status.String)
	rec.Tier = Tier(row.tier.String)
	if rec.Tier == "" {
		rec.Tier = Tier1
	}
	rec.Priority = 50
	if row.priority.Valid {
		rec.Priority = int(row.priority.Int64)
	}
}

func mapScannedMetadata(rec *TaskRecord, row *scannedTaskRow) {
	if row.completedAt.Valid {
		s := row.completedAt.String
		rec.CompletedAt = &s
	}
	rec.ArcID = row.arcID.String
	rec.IntentDelta = row.intentDelta.String
	rec.Burden = int(row.burden.Int64)
	rec.ContextBurden = int(row.ctxBurden.Int64)
	rec.ArchLayer = int(row.archLayer.Int64)
	rec.Dependencies = []string{}
	if row.depJSON.Valid && row.depJSON.String != "" {
		if err := json.Unmarshal([]byte(row.depJSON.String), &rec.Dependencies); err != nil {
			rec.Dependencies = []string{}
		}
	}
}

// Close terminates the underlying SQLite database connection.
func (s *SQLiteTaskStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// BindTwin establishes bilateral twin links between two tasks.
func (s *SQLiteTaskStore) BindTwin(keyA, keyB string) error {
	tA, errA := s.GetTask(keyA)
	if errA != nil {
		return errA
	}
	tB, errB := s.GetTask(keyB)
	if errB != nil {
		return errB
	}
	tA.TwinKey = keyB
	tB.TwinKey = keyA
	if err := s.UpdateTask(*tA); err != nil {
		return err
	}
	return s.UpdateTask(*tB)
}


