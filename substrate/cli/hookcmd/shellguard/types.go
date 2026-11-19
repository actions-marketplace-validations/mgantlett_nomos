// Package shellguard provides normalized PreToolUse hook payload types, POSIX shell
// invocation scanners, headless hang/binary guards, and Nomos install airlocks.
package shellguard

import (
	"path/filepath"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/capbac"
)

const (
	// DecisionAllow permits a PreToolUse or PostToolUse hook invocation.
	DecisionAllow = "allow"
	// DecisionDeny blocks a PreToolUse or PostToolUse hook invocation with a structured reason.
	DecisionDeny = "deny"
)

// HookDecision represents the JSON serialization response returned to Tier 1 agent harnesses.
type HookDecision struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`
}

// AllowDecision constructs an allow HookDecision.
func AllowDecision() HookDecision {
	return HookDecision{Decision: DecisionAllow}
}

// DenyDecision constructs a deny HookDecision with the provided diagnostic reason.
func DenyDecision(reason string) HookDecision {
	return HookDecision{Decision: DecisionDeny, Reason: reason}
}

// HookPayload normalizes incoming JSON hook events across Claude Code, Gemini CLI, Cursor, and Antigravity.
type HookPayload struct {
	SessionID          string
	ToolName           string
	TargetFile         string
	TargetFiles        []string
	TargetURL          string
	WrittenContent     string
	CommandLine        string
	ArgsDigest         string
	ToolResponseText   string
	TerminationReason  string
	Cwd                string
	WorkspacePaths     []string
	HasToolInput       bool
	StopHookActive     bool
	IsClaudeEvent      bool
	IsGeminiEvent      bool
	IsCursorEvent      bool
	IsAntigravityEvent bool
	IsPostInvoke       bool
}

// ReadOnlyTools enumerates read-only agent tool names across supported harnesses.
var ReadOnlyTools = map[string]bool{
	"read": true, "read_file": true, "view_file_outline": true, "view_code_item": true,
	"grep_search": true, "find_by_name": true, "list_dir": true, "list_directory": true, "list": true,
	"glob": true, "grep": true, "search_file_content": true, "lsp": true,
	"bash": true, "run_command": true, "execute_command": true, "run_shell_command": true,
}

// IsReadOnlyTool reports whether toolName is a non-mutating read, MCP read, or shell inspection tool.
func IsReadOnlyTool(toolName string) bool {
	lower := strings.ToLower(strings.TrimSpace(toolName))
	if mcpInv, ok := capbac.ParseMCPInvocation(lower, "", ""); ok {
		return mcpInv.Mode == capbac.MCPModeRead
	}
	return ReadOnlyTools[lower]
}

// IsPythonExe reports whether baseExe names a standard Python interpreter binary.
func IsPythonExe(baseExe string) bool {
	return baseExe == "python" || baseExe == "python3" || strings.HasPrefix(baseExe, "python3.")
}

// NormalizeCanonicalPath resolves symlinks when present and returns a clean slash-normalized path.
func NormalizeCanonicalPath(raw, base string) string {
	target := raw
	if !filepath.IsAbs(target) {
		if base == "" {
			base = "/"
		}
		target = filepath.Join(base, target)
	}
	cleaned := filepath.Clean(target)
	if eval, err := filepath.EvalSymlinks(cleaned); err == nil {
		return filepath.ToSlash(eval)
	}
	parent := filepath.Dir(cleaned)
	if evalParent, err := filepath.EvalSymlinks(parent); err == nil {
		return filepath.ToSlash(filepath.Join(evalParent, filepath.Base(cleaned)))
	}
	return filepath.ToSlash(cleaned)
}
