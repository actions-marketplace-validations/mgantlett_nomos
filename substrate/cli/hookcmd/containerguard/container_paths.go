// Package containerguard enforces container sandbox policy, protected path confinement,
// outbound egress/SSRF airlocks, MCP taint tracking, and autonomous side-effect guards.
package containerguard

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mgantlett/nomos/substrate/cli/hookcmd/shellguard"
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

var (
	defaultExternalMutableStateRoots = []string{
		"/opt", "/var", "/srv", "/etc", "/root",
	}
	benignToolchainRootPrefixes = []string{
		"/opt/nomos", "/opt/launch", "/opt/conda", "/opt/python",
		"/opt/venv", "/opt/pipx", "/opt/homebrew", "/opt/hostedtoolcache",
	}
)

func evaluateContainerProtectedPathsGuard(p *shellguard.HookPayload, baseCwd string, pol SandboxPolicy) shellguard.HookDecision {
	if p == nil {
		return shellguard.AllowDecision()
	}
	roots := normalizeProtectedRoots(pol.ProtectedPaths, baseCwd)
	symlinks := collectWorkspaceEscapingSymlinks(baseCwd, roots)
	if len(roots) == 0 && len(symlinks) == 0 {
		return shellguard.AllowDecision()
	}
	if dec := checkTargetsAgainstProtectedRoots(p, baseCwd, roots, symlinks); dec.Decision == shellguard.DecisionDeny {
		return dec
	}
	if !shellguard.IsReadOnlyTool(p.ToolName) && p.WrittenContent != "" {
		if hit := matchProtectedReference(p.WrittenContent, roots, symlinks); hit != "" {
			return shellguard.DenyDecision(fmt.Sprintf("❌ [NOMOS INTERCEPT] Path Boundary Violation (Law 3): Script payload references restricted path or alias '%s'.", hit))
		}
	}
	if p.CommandLine != "" {
		return checkShellCommandAgainstProtectedRoots(p.CommandLine, baseCwd, roots, symlinks)
	}
	return shellguard.AllowDecision()
}

func checkTargetsAgainstProtectedRoots(p *shellguard.HookPayload, baseCwd string, roots []string, symlinks []workspaceEscapeLink) shellguard.HookDecision {
	targets := p.TargetFiles
	if len(targets) == 0 && p.TargetFile != "" {
		targets = []string{p.TargetFile}
	}
	for _, raw := range targets {
		realPath := ResolveRealPathDeep(raw, baseCwd)
		if isUnderAnyRoot(realPath, roots) {
			return shellguard.DenyDecision(fmt.Sprintf("❌ [NOMOS INTERCEPT] Path Boundary Violation (Law 3): Access to protected path '%s' (resolved '%s') is prohibited.", raw, realPath))
		}
		if link, ok := matchWorkspaceEscapeLink(shellguard.NormalizeCanonicalPath(raw, baseCwd), symlinks); ok {
			return shellguard.DenyDecision(fmt.Sprintf("❌ [NOMOS INTERCEPT] Path Boundary Violation (Law 3): Workspace symlink '%s' resolves into protected path '%s'.", raw, link.RealTarget))
		}
	}
	return shellguard.AllowDecision()
}

func matchWorkspaceEscapeLink(normPath string, symlinks []workspaceEscapeLink) (workspaceEscapeLink, bool) {
	for _, link := range symlinks {
		if normPath == link.LinkPath {
			return link, true
		}
	}
	return workspaceEscapeLink{}, false
}

func checkShellCommandAgainstProtectedRoots(cmdLine, baseCwd string, roots []string, symlinks []workspaceEscapeLink) shellguard.HookDecision {
	if hit := matchProtectedReference(cmdLine, roots, symlinks); hit != "" {
		return shellguard.DenyDecision(fmt.Sprintf("❌ [NOMOS INTERCEPT] Path Boundary Violation (Law 3): Shell command references protected path or escaping alias '%s'.", hit))
	}
	for _, inv := range shellguard.ExtractAllContainerCommandInvocations(cmdLine) {
		if hit := checkInvocationArgsProtected(inv, baseCwd, roots); hit != "" {
			return shellguard.DenyDecision(fmt.Sprintf("❌ [NOMOS INTERCEPT] Path Boundary Violation (Law 3): Command argument resolves to protected path '%s'.", hit))
		}
		if hit := checkNonStandardExeStaticEscape(inv.RawExe, baseCwd, roots, symlinks); hit != "" {
			return shellguard.DenyDecision(fmt.Sprintf("❌ [NOMOS INTERCEPT] Capability Alias Violation (Law 3): Executable '%s' statically accesses protected target or alias '%s'.", inv.Exe, hit))
		}
	}
	return shellguard.AllowDecision()
}

func checkInvocationArgsProtected(inv shellguard.ShellCommandInvocation, baseCwd string, roots []string) string {
	candidates := append([]string{inv.RawExe}, inv.Args...)
	for _, arg := range candidates {
		if arg == "" || strings.HasPrefix(arg, "-") {
			continue
		}
		realArg := ResolveRealPathDeep(arg, baseCwd)
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
	cleanBase := strings.TrimRight(ResolveRealPathDeep(baseCwd, baseCwd), "/")
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
		if d.IsDir() && path != baseCwd && isSkippedWorkspaceWalkDir(d.Name()) {
			return filepath.SkipDir
		}
		if d.Type()&os.ModeSymlink == 0 {
			return nil
		}
		target := ResolveRealPathDeep(path, baseCwd)
		if isEscapingProtectedOrStateSymlink(target, cleanBase, roots) {
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

func isSkippedWorkspaceWalkDir(name string) bool {
	return name == ".git" || name == "node_modules" || name == ".venv" || name == "venv" || name == "__pycache__"
}

func isEscapingProtectedOrStateSymlink(target, cleanBase string, roots []string) bool {
	if isUnderAnyRoot(target, roots) {
		return true
	}
	if cleanBase != "" && (target == cleanBase || strings.HasPrefix(target, cleanBase+"/")) {
		return false
	}
	if isUnderAnyRoot(target, benignToolchainRootPrefixes) {
		return false
	}
	return isUnderAnyRoot(target, defaultExternalMutableStateRoots)
}

func checkNonStandardExeStaticEscape(exe, baseCwd string, roots []string, symlinks []workspaceEscapeLink) string {
	base := strings.ToLower(filepath.Base(exe))
	if standardContainerTools[base] || shellguard.IsPythonExe(base) {
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
	defer func() {
		if cErr := f.Close(); cErr != nil {
			return
		}
	}()
	buf, rErr := io.ReadAll(io.LimitReader(f, maxInspectedBinaryBytes))
	return buf, rErr == nil && len(buf) > 0
}

func lookPathInWorkspace(exe, baseCwd string) (string, error) {
	if strings.Contains(exe, "/") {
		cand := shellguard.NormalizeCanonicalPath(exe, baseCwd)
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
			out = append(out, strings.TrimRight(ResolveRealPathDeep(trimmed, baseCwd), "/"))
		}
	}
	return out
}

// ResolveRealPathDeep resolves symlinks (including dangling leaf symlinks to not-yet-created files)
// even when trailing path components do not exist yet.
func ResolveRealPathDeep(raw, baseCwd string) string {
	return resolveRealPathBounded(raw, baseCwd, 8)
}

func resolveRealPathBounded(raw, baseCwd string, depth int) string {
	norm := shellguard.NormalizeCanonicalPath(raw, baseCwd)
	if depth <= 0 {
		return norm
	}
	cur := filepath.FromSlash(norm)
	suffix := make([]string, 0, 4)
	for {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			return joinCleanSlashSuffix(real, suffix)
		}
		if linkDest, ok := readDanglingSymlinkTarget(cur); ok {
			resolvedDest := resolveRealPathBounded(linkDest, filepath.ToSlash(filepath.Dir(cur)), depth-1)
			return joinCleanSlashSuffix(filepath.FromSlash(resolvedDest), suffix)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return norm
		}
		suffix = append(suffix, filepath.Base(cur))
		cur = parent
	}
}

func readDanglingSymlinkTarget(path string) (string, bool) {
	fi, err := os.Lstat(path)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return "", false
	}
	dest, rErr := os.Readlink(path)
	if rErr != nil || dest == "" {
		return "", false
	}
	return filepath.ToSlash(dest), true
}

func joinCleanSlashSuffix(base string, revSuffix []string) string {
	cur := base
	for i := len(revSuffix) - 1; i >= 0; i-- {
		cur = filepath.Join(cur, revSuffix[i])
	}
	return filepath.ToSlash(filepath.Clean(cur))
}

func isUnderAnyRoot(realPath string, roots []string) bool {
	for _, root := range roots {
		if realPath == root || strings.HasPrefix(realPath, root+"/") {
			return true
		}
	}
	return false
}

// IsProtectedContainerHookConfigPath reports whether resolved targets tamper-protected hook config or binary paths.
func IsProtectedContainerHookConfigPath(resolved string) bool {
	lower := strings.ToLower(filepath.ToSlash(resolved))
	return strings.HasSuffix(lower, "/.claude/settings.json") ||
		strings.HasSuffix(lower, "/.claude/settings.local.json") ||
		strings.Contains(lower, "/.nomos-bin/") ||
		strings.HasSuffix(lower, "/.nomos-bin")
}
