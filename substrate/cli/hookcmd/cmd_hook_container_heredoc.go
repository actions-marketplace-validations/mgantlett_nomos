// Package hookcmd provides heredoc and hidden scratch test filtering for container PreToolUse checks.
package hookcmd

import (
	"path/filepath"
	"strings"
)

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

func isAllowedScratchTmpTestScript(subPath string) bool {
	if strings.Contains(subPath, "..") || strings.HasSuffix(subPath, ".sh") || strings.HasSuffix(subPath, ".pl") {
		return false
	}
	return strings.HasPrefix(subPath, "tests/") || strings.HasPrefix(subPath, "test_")
}

func stripSafeFileWriteHeredocBodies(cmdLine string) string {
	lines := strings.Split(cmdLine, "\n")
	out := make([]string, 0, len(lines))
	delim := ""
	skipBody := false
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
	m := heredocHeaderDelimRe.FindStringSubmatch(strings.TrimSpace(line))
	if len(m) == 2 {
		return m[1]
	}
	return ""
}

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
