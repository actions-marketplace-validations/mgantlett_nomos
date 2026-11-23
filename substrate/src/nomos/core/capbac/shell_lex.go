// Package capbac provides shared stateless POSIX shell quote tracking, segment splitting,
// and quoted word tokenization for container and host security airlocks.
// @intent nomos_hermetic_worktree_spec.md
package capbac

import "strings"

type shellQuoteState struct {
	inSingle bool
	inDouble bool
	escaped  bool
}

func (q *shellQuoteState) step(ch byte) (toggled, wasEscaped bool) {
	if q.escaped {
		q.escaped = false
		return false, true
	}
	if ch == '\\' && !q.inSingle {
		q.escaped = true
		return false, false
	}
	if ch == '\'' && !q.inDouble {
		q.inSingle = !q.inSingle
		return true, false
	}
	if ch == '"' && !q.inSingle {
		q.inDouble = !q.inDouble
		return true, false
	}
	return false, false
}

// SplitUnquotedShellSegments splits a compound shell command on unquoted ';', '\n', '|', '||', and '&&'.
// @intent nomos_hermetic_worktree_spec.md
func SplitUnquotedShellSegments(cmdLine string) []string {
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
		toggled, wasEsc := qs.step(ch)
		if !wasEsc && !toggled && !qs.inSingle && !qs.inDouble {
			if adv, split := unquotedSeparatorStep(cmdLine, i); split {
				flush()
				i += adv
				continue
			}
		}
		cur.WriteByte(ch)
	}
	flush()
	return segs
}

func unquotedSeparatorStep(cmdLine string, idx int) (int, bool) {
	ch := cmdLine[idx]
	if ch == ';' || ch == '\n' {
		return 0, true
	}
	if ch == '|' {
		if idx+1 < len(cmdLine) && cmdLine[idx+1] == '|' {
			return 1, true
		}
		return 0, true
	}
	if ch == '&' && idx+1 < len(cmdLine) && cmdLine[idx+1] == '&' {
		return 1, true
	}
	return 0, false
}

// TokenizeQuotedShellWords tokenizes a shell segment into words while stripping outer quotes and escapes.
// @intent nomos_hermetic_worktree_spec.md
func TokenizeQuotedShellWords(seg string) []string {
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
		toggled, wasEsc := qs.step(ch)
		if (!wasEsc && qs.escaped) || toggled {
			haveTok = true
			continue
		}
		if !wasEsc && !qs.inSingle && !qs.inDouble && (ch == ' ' || ch == '\t') {
			flush()
			continue
		}
		cur.WriteByte(ch)
		haveTok = true
	}
	flush()
	return out
}

// HasUnquotedShellChainingOrSub reports whether cmd contains unquoted shell separators,
// command/process substitution, ANSI-C escape quoting, or brace expansion.
// @intent nomos_hermetic_worktree_spec.md
func HasUnquotedShellChainingOrSub(cmd string) bool {
	if hasUnquotedBraceExpansion(cmd) {
		return true
	}
	var qs shellQuoteState
	for i := 0; i < len(cmd); i++ {
		toggled, wasEsc := qs.step(cmd[i])
		if wasEsc || toggled || qs.inSingle {
			continue
		}
		if isCmdSubstAt(cmd, i) || (!qs.inDouble && isUnquotedChainOrDialectAt(cmd, i)) {
			return true
		}
	}
	return false
}

func isCmdSubstAt(cmd string, i int) bool {
	return cmd[i] == '`' || (cmd[i] == '$' && i+1 < len(cmd) && cmd[i+1] == '(')
}

var unquotedChainSepBytes = [256]bool{
	';': true, '|': true, '&': true, '\n': true, '\r': true,
}

func isUnquotedChainOrDialectAt(cmd string, i int) bool {
	ch := cmd[i]
	if unquotedChainSepBytes[ch] {
		return true
	}
	if i+1 >= len(cmd) {
		return false
	}
	next := cmd[i+1]
	return ((ch == '<' || ch == '>') && next == '(') || (ch == '$' && next == '\'')
}

func hasUnquotedBraceExpansion(cmd string) bool {
	var qs shellQuoteState
	depth := 0
	commaAtDepth := false
	for i := 0; i < len(cmd); i++ {
		toggled, wasEsc := qs.step(cmd[i])
		if wasEsc || toggled || qs.inSingle || qs.inDouble {
			continue
		}
		var prev byte
		if i > 0 {
			prev = cmd[i-1]
		}
		var matched bool
		depth, commaAtDepth, matched = stepBraceExpansion(cmd[i], prev, depth, commaAtDepth)
		if matched {
			return true
		}
	}
	return false
}

func stepBraceExpansion(ch, prev byte, depth int, hasComma bool) (int, bool, bool) {
	if ch == '{' && prev != '$' {
		return depth + 1, hasComma, false
	}
	if depth == 0 {
		return 0, false, false
	}
	if ch == ',' {
		return depth, true, false
	}
	if ch == '}' {
		return depth - 1, false, hasComma
	}
	return depth, hasComma, false
}
