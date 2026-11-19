package shellguard

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/capbac"
)

// ShellCommandInvocation captures a parsed shell command executable, raw token, segment, and arguments.
type ShellCommandInvocation struct {
	Exe    string
	RawExe string
	Raw    string
	Args   []string
}

var (
	subshellCmdSubstRe   = regexp.MustCompile(`\$\(([^)]+)\)`)
	subshellBacktickRe   = regexp.MustCompile("`([^`]+)`")
	pythonSubprocessHint = regexp.MustCompile(`(?i)\b(?:subprocess|os\.system|os\.popen|pty\.spawn|popen)\b`)
	nodeSubprocessHint   = regexp.MustCompile(`(?i)\b(?:child_process|execsync|spawnsync|exec\(|spawn\()`)
	heredocHeaderDelimRe = regexp.MustCompile(`(?:^|[^<])<<-?\s*['"]?([A-Za-z_][A-Za-z0-9_]*)['"]?`)
	heredocPipeExecRe    = regexp.MustCompile(`(?i)\|\s*(?:sqlite3|psql|mysql|mariadb|duckdb|sqlcmd|mongosh|redis-cli|sh|bash|dash|zsh|ksh|python3?|ruby|node)\b`)
	hiddenDotScriptRe    = regexp.MustCompile(`(?i)(?:^|[\s"'=/])(\.[A-Za-z0-9_-]+)/([^\s"'` + "`" + `]+\.(?:py|sh|rb|pl|js))\b`)

	allowedHiddenDotDirs = map[string]bool{
		".venv": true, ".tox": true, ".pytest_cache": true, ".nomos-bin": true, ".pyenv": true,
	}
)

// SplitUnquotedShellSegments delegates POSIX unquoted segment splitting to shared capbac shell lexing.
func SplitUnquotedShellSegments(cmdLine string) []string {
	return capbac.SplitUnquotedShellSegments(cmdLine)
}

// TokenizeQuotedShellWords delegates quoted shell word tokenization to shared capbac shell lexing.
func TokenizeQuotedShellWords(seg string) []string {
	return capbac.TokenizeQuotedShellWords(seg)
}

// ExtractLeadingExeAndArgs strips env assignments and wrapper prefixes to isolate the target command and arguments.
func ExtractLeadingExeAndArgs(seg string) (string, string, []string) {
	fields := TokenizeQuotedShellWords(seg)
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

func isShellEnvAssignmentToken(tok string) bool {
	eq := strings.IndexByte(tok, '=')
	return eq > 0 && !strings.HasPrefix(tok, "-") && !strings.Contains(tok[:eq], "/")
}

// ExtractAllContainerCommandInvocations recursively expands nested shell wrappers and command substitutions.
func ExtractAllContainerCommandInvocations(cmdLine string) []ShellCommandInvocation {
	var out []ShellCommandInvocation
	collectNestedCommandInvocations(cmdLine, 0, &out)
	return out
}

func collectNestedCommandInvocations(cmdLine string, depth int, out *[]ShellCommandInvocation) {
	if depth > 4 || strings.TrimSpace(cmdLine) == "" {
		return
	}
	for _, seg := range SplitUnquotedShellSegments(cmdLine) {
		exe, rawExe, args := ExtractLeadingExeAndArgs(seg)
		if exe == "" {
			continue
		}
		*out = append(*out, ShellCommandInvocation{Exe: exe, RawExe: rawExe, Raw: seg, Args: args})
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

func isShellWrapperExe(exe string) bool {
	return exe == "sh" || exe == "bash" || exe == "zsh" || exe == "dash" || exe == "ksh" || exe == "eval"
}

func extractInlineFlagScript(args []string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-c" || args[i] == "-lc" || args[i] == "-ic" {
			return args[i+1]
		}
	}
	return ""
}

// HasInterpreterSubprocessEscape detects inline Python or Node child-process execution calls.
func HasInterpreterSubprocessEscape(inv ShellCommandInvocation) bool {
	lowerRaw := strings.ToLower(inv.Raw)
	if IsPythonExe(inv.Exe) && pythonSubprocessHint.MatchString(lowerRaw) {
		return true
	}
	return inv.Exe == "node" && nodeSubprocessHint.MatchString(lowerRaw)
}

// HasDisallowedHiddenDotScript blocks hidden dot-directory scripts except trusted project .tmp test suites.
func HasDisallowedHiddenDotScript(raw string, untrustedProject bool) bool {
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

func isAllowedScratchTmpTestScript(subPath string) bool {
	if strings.Contains(subPath, "..") || strings.HasSuffix(subPath, ".sh") || strings.HasSuffix(subPath, ".pl") {
		return false
	}
	return strings.HasPrefix(subPath, "tests/") || strings.HasPrefix(subPath, "test_")
}

// StripSafeFileWriteHeredocBodies removes inert source-write heredoc contents before downstream regex checks.
func StripSafeFileWriteHeredocBodies(cmdLine string) string {
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

func extractSafeHeredocDelim(line string) string {
	if m := heredocHeaderDelimRe.FindStringSubmatch(strings.TrimSpace(line)); len(m) == 2 {
		return m[1]
	}
	return ""
}

func isSafeSourceHeredocHeader(line string) bool {
	if heredocPipeExecRe.MatchString(line) {
		return false
	}
	for _, seg := range SplitUnquotedShellSegments(line) {
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

func extractHeredocHeaderExe(line string) string {
	for _, tok := range TokenizeQuotedShellWords(line) {
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
