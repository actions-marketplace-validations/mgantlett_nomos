// Package handshake implements SQLite task, lease, wavefront, and database health queries for JIT handshakes.
package handshake

import (
	"regexp"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/config"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/storage"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/types"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/mem"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/task"
)

var scopePathHintRe = regexp.MustCompile(`[a-zA-Z0-9_.-]+(?:/[a-zA-Z0-9_.-]+)+\.[a-zA-Z0-9]+`)

func recallMnemeHandshakeContext(primaryRoot, activeTask string, modifiedFiles []string) ([]string, []string, []string) {
	var pitfalls, patterns []string
	taskTitle, scopeHints := resolveActiveTaskContext(activeTask)
	if strings.TrimSpace(activeTask) == "" || len(modifiedFiles) == 0 {
		pitfalls = mem.RecallRepoPitfallsReadOnly(primaryRoot, scopeHints, 3)
		patterns = mem.RecallRepoPatternsReadOnly(primaryRoot, taskTitle, scopeHints, 3)
	} else {
		pitfalls = mem.RecallRepoPitfallsForTask(primaryRoot, activeTask, modifiedFiles, 3)
		patterns = mem.RecallRepoPatternsForTask(primaryRoot, activeTask, taskTitle, modifiedFiles, 3)
	}
	graduations := mem.RecallGraduationCandidates(primaryRoot, 3)
	return pitfalls, patterns, graduations
}

func resolveActiveTaskContext(activeTask string) (string, []string) {
	if strings.TrimSpace(activeTask) == "" {
		return "", nil
	}
	store, err := task.NewDefaultSQLiteTaskStore()
	if err != nil {
		return "", nil
	}
	defer func() {
		if cErr := store.Close(); cErr != nil {
			// closed
		}
	}()
	if rec, tErr := store.GetTask(activeTask); tErr == nil && rec != nil {
		hints := scopePathHintRe.FindAllString(rec.Title+"\n"+rec.Description, 8)
		return rec.Title, hints
	}
	return "", nil
}

func checkDatabasesHealth() bool {
	globalDB := config.ResolveGlobalDBPath()
	db1, err := storage.OpenDB(globalDB)
	if err != nil {
		return false
	}
	if pErr := db1.Ping(); pErr != nil {
		if cErr := db1.Close(); cErr != nil {
			// closed
		}
		return false
	}
	if cErr := db1.Close(); cErr != nil {
		// closed
	}

	brainDB := config.ResolveMnemeDBPath()
	memStore, err := mem.NewStore(brainDB)
	if err != nil {
		return false
	}
	if cErr := memStore.Close(); cErr != nil {
		// closed
	}
	return true
}

func queryTaskAndLease(taskKey string) (types.Phase, string, string, int, int) {
	if taskKey == "" {
		return types.PhaseIdle, "substrate", "", 50, 0
	}

	phase := types.PhasePlan
	plane := "substrate"
	if strings.HasPrefix(taskKey, "INT-") {
		plane = "intent"
	}

	var taskStatusPhase types.Phase
	store, err := task.NewDefaultSQLiteTaskStore()
	if err == nil {
		defer func() {
			if cErr := store.Close(); cErr != nil {
				// closed
			}
		}()
		if rec, tErr := store.GetTask(taskKey); tErr == nil && rec != nil {
			if rec.Plane != "" {
				plane = string(rec.Plane)
			}
			if types.IsValidPhase(types.Phase(rec.Status)) {
				taskStatusPhase = types.Phase(rec.Status)
				phase = taskStatusPhase
			}
		}
	}

	db, err := storage.OpenDB(config.ResolveGlobalDBPath())
	if err != nil {
		return phase, plane, "", 50, 0
	}
	defer func() {
		if cErr := db.Close(); cErr != nil {
			// closed
		}
	}()

	leaseID, lPhase, budget, used, lErr := storage.GetActiveLease(db, taskKey)
	if lErr == nil {
		if taskStatusPhase != "" {
			phase = taskStatusPhase
		} else if types.IsValidPhase(types.Phase(lPhase)) {
			phase = types.Phase(lPhase)
		}
		return phase, plane, leaseID, budget, used
	}

	return phase, plane, "", 50, 0
}

func queryReadyWavefront(activeProject string) []string {
	store, err := task.NewDefaultSQLiteTaskStore()
	if err != nil {
		return []string{}
	}
	defer func() {
		if cErr := store.Close(); cErr != nil {
			// closed
		}
	}()

	candidates, err := store.GetWavefront()
	if err != nil {
		return []string{}
	}

	var wavefront []string
	for _, cand := range candidates {
		if task.IsInProgressStatus(cand.Status) || !task.MatchesProject(cand.Project, activeProject, true) {
			continue
		}
		wavefront = append(wavefront, cand.Key)
		if len(wavefront) >= 5 {
			break
		}
	}
	return wavefront
}
