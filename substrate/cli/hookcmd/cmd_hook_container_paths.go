// Package hookcmd implements Tier 1 agent adapters, software physics telemetry,
// release readiness, upgrades, and CLI command dispatch contracts.
package hookcmd

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const maxInspectedBinaryBytes = 2 * 1024 * 1024

type workspaceEscapeLink struct {
	LinkPath   string
	LinkBase   string
	RealTarget string
}

var standardContainerTools = map[string]bool{
	"sh": true, "bash": true, "dash": true, "zsh": true, "env": true,
	"python": true, "python3": true, "pytest": true, "py.test": true,
	"git": true, "ls": true, "cat": true, "head": true, "tail": true,
	"grep": true, "rg": true, "find": true, "wc": true, "sort": true,
	"uniq": true, "cut": true, "sed": true, "awk": true, "jq": true,
	"sqlite3": true, "node": true, "npm": true, "npx": true, "ruby": true,
	"bundle": true, "go": true, "dotnet": true, "gcc": true, "g++": true,
	"cc": true, "clang": true, "make": true, "cmake": true, "ctest": true,
	"curl": true, "wget": true, "echo": true, "printf": true, "pwd": true,
	"cd": true, "test": true, "true": true, "false": true, "touch": true,
	"mkdir": true, "rm": true, "mv": true, "cp": true, "diff": true,
}

func evaluateContainerProtectedPathsGuard(p *HookPayload, baseCwd string, pol containerSandboxPolicy) HookDecision {
	if len(pol.ProtectedPaths) == 0 || p == nil {
		return HookDecision{Decision: hookDecisionAllow}
	}
	roots := normalizeProtectedRoots(pol.ProtectedPaths, baseCwd)
	symlinks := collectWorkspaceEscapingSymlinks(baseCwd, roots)
	if dec := checkTargetsAgainstProtectedRoots(p, baseCwd, roots, symlinks); dec.Decision == hookDecisionDeny {
		return dec
	}
	if !isReadOnlyTool(p.ToolName) && p.WrittenContent != "" {
		if hit := matchProtectedReference(p.WrittenContent, roots, symlinks); hit != "" {
			return denyDecision(fmt.Sprintf("❌ [NOMOS INTERCEPT] Path Boundary Violation (Law 3): Script payload references restricted path or alias '%s'.", hit))
		}
	}
	if p.CommandLine != "" {
		return checkShellCommandAgainstProtectedRoots(p.CommandLine, baseCwd, roots, symlinks)
	}
	return HookDecision{Decision: hookDecisionAllow}
}

func checkTargetsAgainstProtectedRoots(p *HookPayload, baseCwd string, roots []string, symlinks []workspaceEscapeLink) HookDecision {
	targets := p.TargetFiles
	if len(targets) == 0 && p.TargetFile != "" {
		targets = []string{p.TargetFile}
	}
	for _, raw := range targets {
		realPath := resolveRealPathDeep(raw, baseCwd)
		if isUnderAnyRoot(realPath, roots) {
			return denyDecision(fmt.Sprintf("❌ [NOMOS INTERCEPT] Path Boundary Violation (Law 3): Access to protected path '%s' (resolved '%s') is prohibited.", raw, realPath))
		}
		if link, ok := matchWorkspaceEscapeLink(normalizeCanonicalPath(raw, baseCwd), symlinks); ok {
			return denyDecision(fmt.Sprintf("❌ [NOMOS INTERCEPT] Path Boundary Violation (Law 3): Workspace symlink '%s' resolves into protected path '%s'.", raw, link.RealTarget))
		}
	}
	return HookDecision{Decision: hookDecisionAllow}
}

func matchWorkspaceEscapeLink(normPath string, symlinks []workspaceEscapeLink) (workspaceEscapeLink, bool) {
	for _, link := range symlinks {
		if normPath == link.LinkPath {
			return link, true
		}
	}
	return workspaceEscapeLink{}, false
}

func checkShellCommandAgainstProtectedRoots(cmdLine, baseCwd string, roots []string, symlinks []workspaceEscapeLink) HookDecision {
	if hit := matchProtectedReference(cmdLine, roots, symlinks); hit != "" {
		return denyDecision(fmt.Sprintf("❌ [NOMOS INTERCEPT] Path Boundary Violation (Law 3): Shell command references protected path or escaping alias '%s'.", hit))
	}
	for _, inv := range extractAllContainerCommandInvocations(cmdLine) {
		if hit := checkInvocationArgsProtected(inv, baseCwd, roots); hit != "" {
			return denyDecision(fmt.Sprintf("❌ [NOMOS INTERCEPT] Path Boundary Violation (Law 3): Command argument resolves to protected path '%s'.", hit))
		}
		if hit := checkNonStandardExeStaticEscape(inv.RawExe, baseCwd, roots, symlinks); hit != "" {
			return denyDecision(fmt.Sprintf("❌ [NOMOS INTERCEPT] Capability Alias Violation (Law 3): Executable '%s' statically accesses protected target or alias '%s'.", inv.Exe, hit))
		}
	}
	return HookDecision{Decision: hookDecisionAllow}
}

func checkInvocationArgsProtected(inv shellCommandInvocation, baseCwd string, roots []string) string {
	candidates := append([]string{inv.RawExe}, inv.Args...)
	for _, arg := range candidates {
		if arg == "" || strings.HasPrefix(arg, "-") {
			continue
		}
		realArg := resolveRealPathDeep(arg, baseCwd)
		if isUnderAnyRoot(realArg, roots) {
			return realArg
		}
	}
	return ""
}

func collectWorkspaceEscapingSymlinks(baseCwd string, roots []string) []workspaceEscapeLink {
	if baseCwd == "" {
		return nil
	}
	var out []workspaceEscapeLink
	visited := 0
	if wErr := filepath.WalkDir(baseCwd, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		visited++
		if visited > 600 {
			return fs.SkipAll
		}
		if d.IsDir() && path != baseCwd && (d.Name() == ".git" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if d.Type()&os.ModeSymlink == 0 {
			return nil
		}
		target := resolveRealPathDeep(path, baseCwd)
		if isUnderAnyRoot(target, roots) {
			out = append(out, workspaceEscapeLink{
				LinkPath:   filepath.ToSlash(filepath.Clean(path)),
				LinkBase:   filepath.Base(path),
				RealTarget: target,
			})
		}
		return nil
	}); wErr != nil {
		return out
	}
	return out
}

func checkNonStandardExeStaticEscape(exe, baseCwd string, roots []string, symlinks []workspaceEscapeLink) string {
	base := strings.ToLower(filepath.Base(exe))
	if standardContainerTools[base] || isPythonExe(base) {
		return ""
	}
	binPath, err := lookPathInWorkspace(exe, baseCwd)
	if err != nil || binPath == "" {
		return ""
	}
	buf, ok := readBoundedFileBytes(binPath)
	if !ok {
		return ""
	}
	for _, root := range roots {
		if bytes.Contains(buf, []byte(root)) {
			return root
		}
	}
	for _, link := range symlinks {
		if bytes.Contains(buf, []byte(link.LinkPath)) || (len(link.LinkBase) >= 5 && bytes.Contains(buf, []byte(link.LinkBase))) {
			return link.LinkPath
		}
	}
	return ""
}

func readBoundedFileBytes(path string) ([]byte, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer func() { _ = f.Close() }()
	buf, rErr := io.ReadAll(io.LimitReader(f, maxInspectedBinaryBytes))
	return buf, rErr == nil && len(buf) > 0
}

func lookPathInWorkspace(exe, baseCwd string) (string, error) {
	if strings.Contains(exe, "/") {
		cand := normalizeCanonicalPath(exe, baseCwd)
		if _, err := os.Stat(cand); err == nil {
			return cand, nil
		}
	}
	return exec.LookPath(exe)
}

func matchProtectedReference(text string, roots []string, symlinks []workspaceEscapeLink) string {
	for _, root := range roots {
		if strings.Contains(text, root) {
			return root
		}
	}
	for _, link := range symlinks {
		if link.LinkPath != "" && strings.Contains(text, link.LinkPath) {
			return link.LinkPath
		}
		if link.LinkBase != "" && strings.HasPrefix(link.LinkBase, ".") && strings.Contains(text, link.LinkBase) {
			return link.LinkBase
		}
	}
	return ""
}

func normalizeProtectedRoots(raw []string, baseCwd string) []string {
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			out = append(out, strings.TrimRight(resolveRealPathDeep(trimmed, baseCwd), "/"))
		}
	}
	return out
}

func resolveRealPathDeep(raw, baseCwd string) string {
	norm := normalizeCanonicalPath(raw, baseCwd)
	cur := filepath.FromSlash(norm)
	suffix := make([]string, 0, 4)
	for {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				real = filepath.Join(real, suffix[i])
			}
			return filepath.ToSlash(filepath.Clean(real))
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return norm
		}
		suffix = append(suffix, filepath.Base(cur))
		cur = parent
	}
}

func isUnderAnyRoot(realPath string, roots []string) bool {
	for _, root := range roots {
		if realPath == root || strings.HasPrefix(realPath, root+"/") {
			return true
		}
	}
	return false
}

func isProtectedContainerHookConfigPath(resolved string) bool {
	lower := strings.ToLower(filepath.ToSlash(resolved))
	return strings.HasSuffix(lower, "/.claude/settings.json") ||
		strings.HasSuffix(lower, "/.claude/settings.local.json") ||
		strings.Contains(lower, "/.nomos-bin/") ||
		strings.HasSuffix(lower, "/.nomos-bin")
}
