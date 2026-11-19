package session

import (
	"strings"
)

// TrajectoryStage models an individual causal milestone stage in an agent trajectory (#TrajectoryStage).
type TrajectoryStage struct {
	ID       string `json:"id"`       // BOOT, ALLOCATION, BASELINE, EXPLORATION, ORACLE, IMPLEMENTATION, RELEASE
	Label    string `json:"label"`    // Human-readable stage title
	Status   string `json:"status"`   // COMPLETED, ACTIVE, PENDING
	Evidence string `json:"evidence"` // Milestone evidence or command summary
}

// SessionTrajectory models the complete 7-stage causal arc of a Tier 1 conversation session (#SessionTrajectory).
type SessionTrajectory struct {
	CurrentStage   string            `json:"current_stage"`
	CompletedCount int               `json:"completed_count"`
	TotalCount     int               `json:"total_count"`
	Stages         []TrajectoryStage `json:"stages"`
}

// InferTrajectory reconstructs the 7-stage causal trajectory arc from transcript telemetry.
func InferTrajectory(detail *SessionDetail, rawLines []string) *SessionTrajectory {
	content := strings.Join(rawLines, "\n")

	stages := []TrajectoryStage{
		inferBootStage(content),
		inferAllocationStage(detail, content),
		inferBaselineStage(content),
		inferExplorationStage(content),
		inferOracleStage(content),
		inferImplementationStage(content),
		inferReleaseStage(content),
	}

	markActiveStage(stages, detail.Status)

	completed := 0
	curr := "IDLE"
	for _, s := range stages {
		if s.Status == "COMPLETED" {
			completed++
			curr = s.ID
		} else if s.Status == "ACTIVE" {
			curr = s.ID
		}
	}

	return &SessionTrajectory{
		CurrentStage:   curr,
		CompletedCount: completed,
		TotalCount:     len(stages),
		Stages:         stages,
	}
}

const (
	stageStatusCompleted = "COMPLETED"
	stageStatusPending   = "PENDING"
	stageStatusActive    = "ACTIVE"
)

func buildTrajectoryStage(id, label string, done bool, okEvidence, pendingEvidence string) TrajectoryStage {
	if done {
		return TrajectoryStage{ID: id, Label: label, Status: stageStatusCompleted, Evidence: okEvidence}
	}
	return TrajectoryStage{ID: id, Label: label, Status: stageStatusPending, Evidence: pendingEvidence}
}

func inferBootStage(content string) TrajectoryStage {
	done := strings.Contains(content, "nomos handshake") || strings.Contains(content, "nomos sys handshake") ||
		strings.Contains(content, "/Nomos Handshake") || strings.Contains(content, "/nomos-handshake")
	return buildTrajectoryStage("BOOT", "1. Handshake & Boot", done, "nomos handshake executed cleanly", "Awaiting JIT protocol initialization")
}

func inferAllocationStage(detail *SessionDetail, content string) TrajectoryStage {
	done := detail.TaskKey != "" || strings.Contains(content, "nomos task start") || strings.Contains(content, "nomos task chore")
	return buildTrajectoryStage("ALLOCATION", "2. Task Allocation", done, "CapBAC task lease and hermetic worktree provisioned", "No active task worktree allocated")
}

func inferBaselineStage(content string) TrajectoryStage {
	return buildTrajectoryStage("BASELINE", "3. Baseline Verification", strings.Contains(content, "nomos verify"), "Pre-flight sensor probes verified cleanly", "Baseline verification not yet invoked")
}

var (
	explorationMarkers = []string{
		"grep_search", "view_file", "read_file", "find_by_name", "list_dir",
		`"Read"`, `"Grep"`, `"Glob"`, `"read"`, `"grep"`, `"glob"`, "git log",
	}
	oracleMarkers = []string{
		"go test", "pytest", "npm test", "pnpm test", "yarn test", "bun test",
		"cargo test", "ctest", "unittest", "rspec", "vitest", "jest",
		"nomos verify", "./scripts/test", "test_headless", "oracle", "cargo-sim",
	}
	implementationMarkers = []string{
		"replace_file_content", "write_to_file", "multi_replace",
		"edit_file", "write_file", "apply_patch",
		`"Edit"`, `"Write"`, `"MultiEdit"`, `"edit"`, `"write"`,
	}
)

func hasAnyTrajectoryMarker(content string, markers []string) bool {
	for _, m := range markers {
		if strings.Contains(content, m) {
			return true
		}
	}
	return false
}

func inferExplorationStage(content string) TrajectoryStage {
	return buildTrajectoryStage("EXPLORATION", "4. Code Exploration", hasAnyTrajectoryMarker(content, explorationMarkers), "Target AST and source symbols explored", "Awaiting initial source investigation")
}

func inferOracleStage(content string) TrajectoryStage {
	return buildTrajectoryStage("ORACLE", "5. Test & Oracle", hasAnyTrajectoryMarker(content, oracleMarkers), "Automated test suite or deterministic oracle verified", "Awaiting test execution")
}

func inferImplementationStage(content string) TrajectoryStage {
	return buildTrajectoryStage("IMPLEMENTATION", "6. Implementation", hasAnyTrajectoryMarker(content, implementationMarkers), "Targeted AST modifications applied", "No file edits committed yet")
}

func inferReleaseStage(content string) TrajectoryStage {
	done := strings.Contains(content, "nomos task sync") || strings.Contains(content, "walkthrough.md")
	return buildTrajectoryStage("RELEASE", "7. 2PC Release Sync", done, "Merged to protected trunk via 2PC release certificate", "Release sync not yet executed")
}

func markActiveStage(stages []TrajectoryStage, sessionStatus string) {
	if sessionStatus != stageStatusActive {
		return
	}
	for i := len(stages) - 1; i >= 0; i-- {
		if stages[i].Status == stageStatusCompleted && i < len(stages)-1 {
			stages[i+1].Status = stageStatusActive
			return
		}
	}
	if len(stages) > 0 && stages[0].Status == stageStatusPending {
		stages[0].Status = stageStatusActive
	}
}
