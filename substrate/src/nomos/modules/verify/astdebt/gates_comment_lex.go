// Package astdebt provides comment-only lexical extractors and same-variable branch-chain scanners.
package astdebt

import (
	"fmt"
	"strings"
)

type pyHashScanState struct {
	docDelim string
}

func extractPythonHashComments(text string) []string {
	lines := strings.Split(text, "\n")
	out := make([]string, len(lines))
	st := &pyHashScanState{}
	for i, line := range lines {
		out[i] = st.scanLineComment(line)
	}
	return out
}

func (s *pyHashScanState) scanLineComment(line string) string {
	quote := byte(0)
	escaped := false
	for i := 0; i < len(line); i++ {
		if s.docDelim != "" {
			if strings.HasPrefix(line[i:], s.docDelim) {
				s.docDelim = ""
				i += 2
			}
			continue
		}
		if escaped {
			escaped = false
			continue
		}
		ch := line[i]
		if quote != 0 {
			quote, escaped = stepSingleQuoteByte(ch, quote)
			continue
		}
		if delim := matchTripleQuoteAt(line, i); delim != "" {
			s.docDelim = delim
			i += 2
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			continue
		}
		if ch == '#' {
			return line[i:]
		}
	}
	return ""
}

func stepSingleQuoteByte(ch, quote byte) (byte, bool) {
	if ch == '\\' {
		return quote, true
	}
	if ch == quote {
		return 0, false
	}
	return quote, false
}

func matchTripleQuoteAt(line string, idx int) string {
	if strings.HasPrefix(line[idx:], `"""`) {
		return `"""`
	}
	if strings.HasPrefix(line[idx:], `'''`) {
		return `'''`
	}
	return ""
}

func splitSlashLineCommentOutsideStrings(rawLine string) (string, string) {
	quote := byte(0)
	escaped := false
	for i := 0; i < len(rawLine); i++ {
		if escaped {
			escaped = false
			continue
		}
		ch := rawLine[i]
		if quote != 0 {
			quote, escaped = stepSingleQuoteByte(ch, quote)
			continue
		}
		if ch == '"' || ch == '\'' || ch == '`' {
			quote = ch
			continue
		}
		if ch == '/' && i+1 < len(rawLine) && rawLine[i+1] == '/' {
			return rawLine[:i], rawLine[i:]
		}
	}
	return rawLine, ""
}

type slashCommentCursor struct {
	buf     strings.Builder
	inBlock bool
	quote   byte
	escaped bool
}

func extractSlashCommentsFromLine(rawLine string, inBlock bool) (string, bool) {
	cur := &slashCommentCursor{inBlock: inBlock}
	for i := 0; i < len(rawLine); i++ {
		if advance, stop := cur.step(rawLine, i); stop {
			break
		} else {
			i += advance
		}
	}
	return cur.buf.String(), cur.inBlock
}

func (c *slashCommentCursor) step(line string, idx int) (int, bool) {
	if c.inBlock {
		if strings.HasPrefix(line[idx:], "*/") {
			c.inBlock = false
			return 1, false
		}
		c.buf.WriteByte(line[idx])
		return 0, false
	}
	if c.escaped {
		c.escaped = false
		return 0, false
	}
	ch := line[idx]
	if c.quote != 0 {
		c.quote, c.escaped = stepSingleQuoteByte(ch, c.quote)
		return 0, false
	}
	return c.stepCodeToken(line, idx, ch)
}

func (c *slashCommentCursor) stepCodeToken(line string, idx int, ch byte) (int, bool) {
	if ch == '"' || ch == '\'' || ch == '`' {
		c.quote = ch
		return 0, false
	}
	if strings.HasPrefix(line[idx:], "//") {
		c.buf.WriteString(line[idx:])
		return 0, true
	}
	if strings.HasPrefix(line[idx:], "/*") {
		c.inBlock = true
		return 1, false
	}
	return 0, false
}

type ifChainRun struct {
	indent    int
	chainKey  string
	label     string
	startLine int
	length    int
	allJump   bool
}

func findConsecutiveBranchChains(body []string, startLineBase int) []ifChainRun {
	var cur *ifChainRun
	var chains []ifChainRun
	for i := 0; i < len(body); i++ {
		trimmed := strings.TrimSpace(body[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := countLeadingIndentCols(body[i])
		if cur != nil && indent > cur.indent {
			continue
		}
		next, nextIdx, found := stepIfChainLine(body, i, indent, trimmed, startLineBase, cur)
		if found != nil {
			chains = append(chains, *found)
		}
		cur = next
		i = nextIdx
	}
	if isFlaggedIfChain(cur) {
		chains = append(chains, *cur)
	}
	return chains
}

func matchSameVarBranchHeader(line string) (string, string, bool) {
	if m := pyIfEqVarRe.FindStringSubmatch(line); len(m) == 3 {
		return "dispatch:" + m[2], fmt.Sprintf("consecutive 'if %s ==' checks", m[2]), true
	}
	if m := pyIfInVarRe.FindStringSubmatch(line); len(m) == 3 {
		return "dispatch:" + m[2], fmt.Sprintf("consecutive 'if %s == / in' checks", m[2]), true
	}
	if m := pyIfIsinstanceVarRe.FindStringSubmatch(line); len(m) == 3 {
		return "is:" + m[2], fmt.Sprintf("consecutive 'if isinstance(%s, ...)' checks", m[2]), true
	}
	return "", "", false
}

func stepIfChainLine(body []string, i, indent int, trimmed string, startLineBase int, cur *ifChainRun) (*ifChainRun, int, *ifChainRun) {
	if key, label, ok := matchSameVarBranchHeader(body[i]); ok {
		blockEnd := findPythonBlockEnd(body, i+1, indent)
		isJump := pythonIfBlockEndsInJump(body[i:blockEnd])
		if cur != nil && cur.indent == indent && cur.chainKey == key {
			cur.length++
			cur.allJump = cur.allJump && isJump
			return cur, blockEnd - 1, nil
		}
		var flagged *ifChainRun
		if isFlaggedIfChain(cur) {
			flagged = cur
		}
		return &ifChainRun{
			indent: indent, chainKey: key, label: label,
			startLine: startLineBase + i, length: 1, allJump: isJump,
		}, blockEnd - 1, flagged
	}
	if cur != nil && indent == cur.indent && pyElseOrElifPrefixRe.MatchString(trimmed) {
		return nil, findPythonBlockEnd(body, i+1, indent) - 1, nil
	}
	if isFlaggedIfChain(cur) {
		return nil, i, cur
	}
	return nil, i, nil
}

func isFlaggedIfChain(cur *ifChainRun) bool {
	if cur == nil {
		return false
	}
	return cur.length >= 3 || (cur.length >= 2 && cur.allJump)
}

func pythonIfBlockEndsInJump(block []string) bool {
	if len(block) == 0 {
		return false
	}
	header := strings.TrimSpace(block[0])
	if colon := strings.IndexByte(header, ':'); colon >= 0 {
		if tail := strings.TrimSpace(header[colon+1:]); tail != "" {
			return isPythonJumpStmt(tail)
		}
	}
	lastExec := ""
	for i := 1; i < len(block); i++ {
		t := strings.TrimSpace(block[i])
		if t != "" && !strings.HasPrefix(t, "#") {
			lastExec = t
		}
	}
	return isPythonJumpStmt(lastExec)
}

func isPythonJumpStmt(stmt string) bool {
	for _, kw := range []string{"return", "raise", "break", "continue"} {
		if stmt == kw || strings.HasPrefix(stmt, kw+" ") || strings.HasPrefix(stmt, kw+"(") {
			return true
		}
	}
	return false
}
