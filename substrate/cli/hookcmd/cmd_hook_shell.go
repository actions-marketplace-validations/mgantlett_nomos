// Package hookcmd provides shell segment extraction, heredoc stripping, and hidden script filtering for container PreToolUse checks.
package hookcmd

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/capbac"
)

type shellCommandInvocation struct {
	Exe    string
	RawExe string
	Raw    string
	Args   []string
}

var (
	inlineShellSubCmdRe  = regexp.MustCompile(`(?:^|\s)-(?:c|lc)\s+["']([^"']+)["']`)
	subshellCmdSubstRe   = regexp.MustCompile(`\$\(([^)]+)\)`)
	subshellBacktickRe   = regexp.MustCompile("`([^`]+)`")
	pythonSubprocessHint = regexp.MustCompile(`(?i)\b(?:subprocess|os\.system|os\.popen|pty\.spawn|popen)\b`)
	nodeSubprocessHint   = regexp.MustCompile(`(?i)\b(?:child_process|execsync|spawnsync|exec\(|spawn\()`)
)

// splitUnquotedShellSegments delegates POSIX unquoted segment splitting to shared capbac shell lexing.
func splitUnquotedShellSegments(cmdLine string) []string {
	return capbac.SplitUnquotedShellSegments(cmdLine)
}

// tokenizeQuotedShellWords delegates quoted shell word tokenization to shared capbac shell lexing.
func tokenizeQuotedShellWords(seg string) []string {
	return capbac.TokenizeQuotedShellWords(seg)
}

// extractLeadingExeAndArgs strips env assignments and wrapper prefixes to isolate the target command and arguments.
func extractLeadingExeAndArgs(seg string) (string, string, []string) {
	fields := tokenizeQuotedShellWords(seg)
	idx := 0
	for idx < len(fields) && isShellEnvAssignmentToken(fields[idx]) {
		idx++
	}
	for idx < len(fields) {
		rawExe := fields[idx]
		base := strings.ToLower(filepath.Base(rawExe))
		if base == "env" || base == "command" || base == "nohup" || base == "sudo" {
			idx++
			for idx < len(fields) && (strings.HasPrefix(fields[idx], "-") || isShellEnvAssignmentToken(fields[idx])) {
				idx++
			}
			continue
		}
		if base == "timeout" && idx+1 < len(fields) {
			idx += 2
			continue
		}
		return base, rawExe, fields[idx+1:]
	}
	return "", "", nil
}

// isShellEnvAssignmentToken reports whether tok sets an environment variable ahead of an executable.
func isShellEnvAssignmentToken(tok string) bool {
	eq := strings.IndexByte(tok, '=')
	return eq > 0 && !strings.HasPrefix(tok, "-") && !strings.Contains(tok[:eq], "/")
}

// extractAllContainerCommandInvocations recursively expands nested shell wrappers and command substitutions.
func extractAllContainerCommandInvocations(cmdLine string) []shellCommandInvocation {
	var out []shellCommandInvocation
	collectNestedCommandInvocations(cmdLine, 0, &out)
	return out
}

// collectNestedCommandInvocations traverses compound segments, -c scripts, $() substitutions, and backticks.
func collectNestedCommandInvocations(cmdLine string, depth int, out *[]shellCommandInvocation) {
	if depth > 4 || strings.TrimSpace(cmdLine) == "" {
		return
	}
	for _, seg := range splitUnquotedShellSegments(cmdLine) {
		exe, rawExe, args := extractLeadingExeAndArgs(seg)
		if exe == "" {
			continue
		}
		*out = append(*out, shellCommandInvocation{Exe: exe, RawExe: rawExe, Raw: seg, Args: args})
		if isShellWrapperExe(exe) {
			if sub := extractInlineFlagScript(args); sub != "" {
				collectNestedCommandInvocations(sub, depth+1, out)
			}
		}
	}
	for _, pat := range []*regexp.Regexp{subshellCmdSubstRe, subshellBacktickRe} {
		for _, m := range pat.FindAllStringSubmatch(cmdLine, -1) {
			if len(m) > 1 {
				collectNestedCommandInvocations(m[1], depth+1, out)
			}
		}
	}
}

// isShellWrapperExe reports whether exe evaluates inline command strings via wrapper flags.
func isShellWrapperExe(exe string) bool {
	return exe == "sh" || exe == "bash" || exe == "zsh" || exe == "dash" || exe == "ksh" || exe == "eval"
}

// extractInlineFlagScript extracts the inline command argument following -c, -lc, or -ic.
func extractInlineFlagScript(args []string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-c" || args[i] == "-lc" || args[i] == "-ic" {
			return args[i+1]
		}
	}
	return ""
}

// hasInterpreterSubprocessEscape detects inline Python or Node child-process execution calls.
func hasInterpreterSubprocessEscape(inv shellCommandInvocation) bool {
	lowerRaw := strings.ToLower(inv.Raw)
	if isPythonExe(inv.Exe) && pythonSubprocessHint.MatchString(lowerRaw) {
		return true
	}
	return inv.Exe == "node" && nodeSubprocessHint.MatchString(lowerRaw)
}

// hasDisallowedHiddenDotScript blocks hidden dot-directory scripts except trusted project .tmp test suites.
func hasDisallowedHiddenDotScript(raw string, untrustedProject bool) bool {
	for _, m := range hiddenDotScriptRe.FindAllStringSubmatch(raw, -1) {
		if len(m) < 3 {
			continue
		}
		dotDir := strings.ToLower(m[1])
		if allowedHiddenDotDirs[dotDir] {
			continue
		}
		if !untrustedProject && dotDir == ".tmp" && isAllowedScratchTmpTestScript(strings.ToLower(m[2])) {
			continue
		}
		return true
	}
	return false
}

// isAllowedScratchTmpTestScript allows non-shell scratch test scripts strictly under .tmp/tests/ or .tmp/test_*.
func isAllowedScratchTmpTestScript(subPath string) bool {
	if strings.Contains(subPath, "..") || strings.HasSuffix(subPath, ".sh") || strings.HasSuffix(subPath, ".pl") {
		return false
	}
	return strings.HasPrefix(subPath, "tests/") || strings.HasPrefix(subPath, "test_")
}

// stripSafeFileWriteHeredocBodies removes inert source-write heredoc contents before downstream regex checks.
func stripSafeFileWriteHeredocBodies(cmdLine string) string {
	lines := strings.Split(cmdLine, "\n")
	out := make([]string, 0, len(lines))
	delim, skipBody := "", false
	for _, raw := range lines {
		if delim != "" {
			if strings.TrimSpace(raw) == delim {
				delim, skipBody = "", false
				out = append(out, raw)
				continue
			}
			if !skipBody {
				out = append(out, raw)
			}
			continue
		}
		out = append(out, raw)
		if d := extractSafeHeredocDelim(raw); d != "" {
			delim = d
			skipBody = isSafeSourceHeredocHeader(raw)
		}
	}
	return strings.Join(out, "\n")
}

// extractSafeHeredocDelim returns the heredoc delimiter token on a shell header line, if present.
func extractSafeHeredocDelim(line string) string {
	if m := heredocHeaderDelimRe.FindStringSubmatch(strings.TrimSpace(line)); len(m) == 2 {
		return m[1]
	}
	return ""
}

// isSafeSourceHeredocHeader verifies that a heredoc segment writes source text via cat/tee without pipe execution.
func isSafeSourceHeredocHeader(line string) bool {
	if heredocPipeExecRe.MatchString(line) {
		return false
	}
	for _, seg := range splitUnquotedShellSegments(line) {
		if extractSafeHeredocDelim(seg) == "" {
			continue
		}
		if heredocPipeExecRe.MatchString(seg) || strings.Contains(seg, "|") {
			return false
		}
		exe := extractHeredocHeaderExe(seg)
		if exe == "tee" {
			return true
		}
		return exe == "cat" && strings.Contains(seg, ">")
	}
	return false
}

// extractHeredocHeaderExe resolves the leading non-wrapper command executable of a heredoc header segment.
func extractHeredocHeaderExe(line string) string {
	for _, tok := range tokenizeQuotedShellWords(line) {
		if isShellEnvAssignmentToken(tok) {
			continue
		}
		base := strings.ToLower(filepath.Base(tok))
		if base == "env" || base == "command" || base == "nohup" || base == "sudo" {
			continue
		}
		return base
	}
	return ""
}
