// Package hookcmd implements Tier 1 agent adapters, software physics telemetry,
// release readiness, upgrades, and CLI command dispatch contracts.
package hookcmd

import (
	"path/filepath"
	"regexp"
	"strings"
)

type shellCommandInvocation struct {
	Exe    string
	RawExe string
	Raw    string
	Args   []string
}

type shellQuoteState struct {
	inSingle bool
	inDouble bool
	escaped  bool
}

var (
	inlineShellSubCmdRe  = regexp.MustCompile(`(?:^|\s)-(?:c|lc)\s+["']([^"']+)["']`)
	subshellCmdSubstRe   = regexp.MustCompile(`\$\(([^)]+)\)`)
	subshellBacktickRe   = regexp.MustCompile("`([^`]+)`")
	pythonSubprocessHint = regexp.MustCompile(`(?i)\b(?:subprocess|os\.system|os\.popen|pty\.spawn|popen)\b`)
	nodeSubprocessHint   = regexp.MustCompile(`(?i)\b(?:child_process|execsync|spawnsync|exec\(|spawn\()`)
)

func (q *shellQuoteState) step(ch byte) bool {
	if q.escaped {
		q.escaped = false
		return false
	}
	if ch == '\\' && !q.inSingle {
		q.escaped = true
		return false
	}
	if ch == '\'' && !q.inDouble {
		q.inSingle = !q.inSingle
		return true
	}
	if ch == '"' && !q.inSingle {
		q.inDouble = !q.inDouble
		return true
	}
	return false
}

func splitUnquotedShellSegments(cmdLine string) []string {
	var segs []string
	var cur strings.Builder
	var qs shellQuoteState
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			segs = append(segs, s)
		}
		cur.Reset()
	}
	for i := 0; i < len(cmdLine); i++ {
		ch := cmdLine[i]
		toggled := qs.step(ch)
		if !toggled && !qs.inSingle && !qs.inDouble {
			if ch == ';' || ch == '\n' || ch == '|' {
				flush()
				if ch == '|' && i+1 < len(cmdLine) && cmdLine[i+1] == '|' {
					i++
				}
				continue
			}
			if ch == '&' && i+1 < len(cmdLine) && cmdLine[i+1] == '&' {
				flush()
				i++
				continue
			}
		}
		cur.WriteByte(ch)
	}
	flush()
	return segs
}

func tokenizeQuotedShellWords(seg string) []string {
	var out []string
	var cur strings.Builder
	var qs shellQuoteState
	haveTok := false
	flush := func() {
		if haveTok || cur.Len() > 0 {
			out = append(out, cur.String())
		}
		cur.Reset()
		haveTok = false
	}
	for i := 0; i < len(seg); i++ {
		ch := seg[i]
		prevEsc := qs.escaped
		if qs.step(ch) && !prevEsc {
			haveTok = true
			continue
		}
		if !prevEsc && ch == '\\' && !qs.inSingle {
			haveTok = true
			continue
		}
		if !qs.inSingle && !qs.inDouble && (ch == ' ' || ch == '\t') {
			flush()
			continue
		}
		cur.WriteByte(ch)
		haveTok = true
	}
	flush()
	return out
}

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

func isShellEnvAssignmentToken(tok string) bool {
	eq := strings.IndexByte(tok, '=')
	return eq > 0 && !strings.HasPrefix(tok, "-") && !strings.Contains(tok[:eq], "/")
}

func extractAllContainerCommandInvocations(cmdLine string) []shellCommandInvocation {
	var out []shellCommandInvocation
	collectNestedCommandInvocations(cmdLine, 0, &out)
	return out
}

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

func hasInterpreterSubprocessEscape(inv shellCommandInvocation) bool {
	lowerRaw := strings.ToLower(inv.Raw)
	if isPythonExe(inv.Exe) && pythonSubprocessHint.MatchString(lowerRaw) {
		return true
	}
	return inv.Exe == "node" && nodeSubprocessHint.MatchString(lowerRaw)
}
