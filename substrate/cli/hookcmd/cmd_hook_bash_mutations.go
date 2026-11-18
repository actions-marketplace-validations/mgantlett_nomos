// Package hookcmd tracks workspace file mutations performed via shell commands
// across PostToolUse AST radar and Stop anti-phantom checks (Law 2 & Law 4).
package hookcmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/constants"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
)

const (
	maxShellRadarFiles   = 4
	maxShellDigestBytes  = 512 * 1024
	maxFallbackWalkDepth = 2
)

var (
	mutatingShellHints = []string{
		">", "tee ", "sed ", "perl ", "cat ", "python", "node", "ruby",
		"cp ", "mv ", "patch", "apply", "touch ", "sh ", "bash ", "gofmt", "ruff", "black",
	}
	shellSkippedDirNames = map[string]bool{
		constants.DirGit: true, constants.DirNomosHome: true, constants.DirTmp: true, ".claude": true,
		".evaluation_tests": true, ".venv": true, "venv": true, "node_modules": true,
		"__pycache__": true, "vendor": true,
	}
	shellSkippedRelFiles = map[string]bool{
		constants.FileNomosYAML: true, constants.FileNomosBaselineJSON: true,
		"AGENTS.md": true, "CLAUDE.md": true,
	}
)

func isPotentialMutatingShellCommand(cmdLine string) bool {
	trimmed := strings.TrimSpace(cmdLine)
	if trimmed == "" || isNomosVerificationCommand(trimmed) {
		return false
	}
	lower := strings.ToLower(trimmed)
	for _, hint := range mutatingShellHints {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}

func computeFileContentDigest(absPath string) string {
	info, err := os.Stat(absPath)
	if err != nil || info.IsDir() || info.Size() > maxShellDigestBytes {
		return ""
	}
	raw, err := os.ReadFile(absPath)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%d:%s", len(raw), hex.EncodeToString(sum[:4]))
}

func refreshTraceObservedDigests(wsRoot string, tr *HookSessionTrace) {
	if wsRoot == "" || tr == nil {
		return
	}
	candidates := append([]string{}, tr.MutatedFiles...)
	candidates = append(candidates, listWorkspaceDirtyCandidateFiles(wsRoot)...)
	if tr.ObservedDigests == nil {
		tr.ObservedDigests = make(map[string]string, len(candidates))
	}
	for _, rel := range candidates {
		dig := computeFileContentDigest(filepath.Join(wsRoot, filepath.FromSlash(rel)))
		if dig != "" {
			tr.ObservedDigests[rel] = dig
			tr.MutatedFiles = appendUniqueBounded(tr.MutatedFiles, rel, 64)
		}
	}
	tr.PendingShellRadar = nil
}

func discoverShellMutatedFiles(wsRoot string, tr *HookSessionTrace) []string {
	if wsRoot == "" || tr == nil {
		return nil
	}
	if tr.ObservedDigests == nil {
		tr.ObservedDigests = make(map[string]string, 16)
	}
	var changedAbs []string
	for _, rel := range listWorkspaceDirtyCandidateFiles(wsRoot) {
		absPath := filepath.Join(wsRoot, filepath.FromSlash(rel))
		dig := computeFileContentDigest(absPath)
		if dig == "" || tr.ObservedDigests[rel] == dig {
			continue
		}
		tr.ObservedDigests[rel] = dig
		tr.MutatedFiles = appendUniqueBounded(tr.MutatedFiles, rel, 64)
		tr.PendingShellRadar = appendUniqueBounded(tr.PendingShellRadar, rel, 8)
		tr.DirtyUnverifiedEdits = true
		if isExecutableSourceRelPath(rel) && !hasWorkspaceAutomatedTests(wsRoot) {
			tr.DirtyBehavioralEdits = true
		}
		if len(changedAbs) < maxShellRadarFiles {
			changedAbs = append(changedAbs, absPath)
		}
	}
	return changedAbs
}

func listWorkspaceDirtyCandidateFiles(wsRoot string) []string {
	if _, err := os.Stat(filepath.Join(wsRoot, constants.DirGit)); err == nil {
		return listGitDirtySourceFiles(wsRoot)
	}
	return listShallowWorkspaceFiles(wsRoot)
}

func listGitDirtySourceFiles(wsRoot string) []string {
	out, err := workspace.SafeGitCommand(wsRoot, "status", "--porcelain", "-uall").Output()
	if err != nil {
		return nil
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		if rel := parsePorcelainRelPath(line); rel != "" && !isSkippedShellRelPath(rel) {
			files = append(files, rel)
		}
	}
	sort.Strings(files)
	return files
}

func parsePorcelainRelPath(line string) string {
	if len(line) < 4 {
		return ""
	}
	if strings.HasPrefix(line, "D ") || strings.HasPrefix(line, " D") {
		return ""
	}
	raw := strings.TrimSpace(line[3:])
	if arrow := strings.Index(raw, " -> "); arrow >= 0 {
		raw = strings.TrimSpace(raw[arrow+4:])
	}
	raw = strings.Trim(raw, `"`)
	return filepath.ToSlash(raw)
}

func listShallowWorkspaceFiles(wsRoot string) []string {
	var files []string
	cleanRoot := filepath.Clean(wsRoot)
	_ = filepath.WalkDir(cleanRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil
		}
		if path == cleanRoot {
			return nil
		}
		rel, rErr := filepath.Rel(cleanRoot, path)
		if rErr != nil {
			return nil
		}
		slashRel := filepath.ToSlash(rel)
		if d.IsDir() {
			if shellSkippedDirNames[d.Name()] || strings.Count(slashRel, "/") >= maxFallbackWalkDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if !isSkippedShellRelPath(slashRel) && len(files) < 64 {
			files = append(files, slashRel)
		}
		return nil
	})
	sort.Strings(files)
	return files
}

func isSkippedShellRelPath(slashRel string) bool {
	if slashRel == "" || shellSkippedRelFiles[slashRel] {
		return true
	}
	for dirTok := range shellSkippedDirNames {
		if strings.HasPrefix(slashRel, dirTok+"/") || strings.Contains(slashRel, "/"+dirTok+"/") {
			return true
		}
	}
	return strings.HasSuffix(slashRel, "/")
}

func popShellMutatedFilesForRadar(p *HookPayload, hermeticDir string, containerMode bool) (string, []string) {
	traceKey, wsRoot, branchLabel := resolveTraceScopeAndWorkspace(p, hermeticDir)
	if wsRoot == "" && (hermeticDir != "" || containerMode || branchLabel != "") {
		wsRoot = firstNonEmpty(p.Cwd, resolveHookWorkspaceDir(p, hermeticDir))
	}
	if wsRoot == "" {
		return "", nil
	}
	if traceKey == "" {
		return wsRoot, listRecentContainerShellTargets(wsRoot)
	}
	path := resolveTraceStateFilePath(traceKey)
	if path == "" {
		return wsRoot, nil
	}
	tr := loadOrInitHookSessionTrace(path, wsRoot)
	if isPotentialMutatingShellCommand(p.CommandLine) {
		discoverShellMutatedFiles(wsRoot, &tr)
	}
	var out []string
	seen := make(map[string]bool, len(tr.PendingShellRadar))
	for _, rel := range tr.PendingShellRadar {
		if len(out) >= maxShellRadarFiles {
			break
		}
		absPath := filepath.Join(wsRoot, filepath.FromSlash(rel))
		if !seen[absPath] {
			seen[absPath] = true
			out = append(out, absPath)
		}
	}
	tr.PendingShellRadar = nil
	saveHookSessionTrace(path, tr)
	return wsRoot, out
}

func listRecentContainerShellTargets(wsRoot string) []string {
	var out []string
	for _, rel := range listWorkspaceDirtyCandidateFiles(wsRoot) {
		if len(out) >= maxShellRadarFiles {
			break
		}
		out = append(out, filepath.Join(wsRoot, filepath.FromSlash(rel)))
	}
	return out
}

func evaluateShellMutatedPaths(p *HookPayload, hermeticDir string, containerMode bool) string {
	if p == nil || !isPotentialMutatingShellCommand(p.CommandLine) {
		return ""
	}
	wsRoot, targets := popShellMutatedFilesForRadar(p, hermeticDir, containerMode)
	if len(targets) == 0 {
		return ""
	}
	var msgs []string
	for _, resolved := range targets {
		if msg := inspectMutatedFilePath(resolved, wsRoot, hermeticDir, containerMode); msg != "" {
			msgs = append(msgs, msg)
		}
	}
	return strings.Join(msgs, "\n")
}

func inspectMutatedFilePath(resolved, baseCwd, hermeticDir string, containerMode bool) string {
	hygieneMsg := evaluateTestPlacementHygieneAdvisory(resolved, baseCwd)
	if containerMode {
		if snoop := validateContainerFileVerifierClean(resolved); snoop != "" {
			return snoop
		}
		if syn := validateStructuredFileInMemory(resolved); syn != "" {
			return joinHookAdvisories(syn, hygieneMsg)
		}
		if shim := evaluateContainerAntiShimInMemory(resolved, baseCwd); shim != "" {
			return joinHookAdvisories(shim, hygieneMsg)
		}
		if hasExplicitNomosConfig(resolved, baseCwd) {
			return joinHookAdvisories(evaluateFastRadarInMemory(resolved, baseCwd), hygieneMsg)
		}
		return hygieneMsg
	}
	if !isPostToolTargetInScope(resolved, hermeticDir) {
		return ""
	}
	if syn := validateStructuredFileInMemory(resolved); syn != "" {
		return joinHookAdvisories(syn, hygieneMsg)
	}
	return joinHookAdvisories(evaluateFastRadarInMemory(resolved, baseCwd), hygieneMsg)
}

func loadOrSyncStopTrace(traceKey, wsRoot string) *HookSessionTrace {
	tr := loadHookSessionTrace(traceKey)
	if tr == nil && wsRoot != "" {
		if dirty := listGitDirtySourceFiles(wsRoot); len(dirty) > 0 {
			dirtyBeh := anyExecutableSourceRel(dirty) && !hasWorkspaceAutomatedTests(wsRoot)
			seeded := HookSessionTrace{
				WorktreeRoot:         wsRoot,
				MutatedFiles:         dirty,
				DirtyUnverifiedEdits: true,
				DirtyBehavioralEdits: dirtyBeh,
			}
			refreshTraceObservedDigests(wsRoot, &seeded)
			if path := resolveTraceStateFilePath(traceKey); path != "" {
				saveHookSessionTrace(path, seeded)
			}
			return &seeded
		}
		return nil
	}
	if tr != nil && wsRoot != "" {
		if changed := discoverShellMutatedFiles(wsRoot, tr); len(changed) > 0 {
			if anyExecutableSourceAbs(wsRoot, changed) && !hasWorkspaceAutomatedTests(wsRoot) {
				tr.DirtyBehavioralEdits = true
			}
			if path := resolveTraceStateFilePath(traceKey); path != "" {
				saveHookSessionTrace(path, *tr)
			}
		}
	}
	return tr
}

func collectStopInlineRadarSummary(wsRoot string, mutated []string) string {
	if wsRoot == "" || len(mutated) == 0 {
		return ""
	}
	var breaches []string
	checked := 0
	for _, rel := range mutated {
		if checked >= maxShellRadarFiles {
			break
		}
		absPath := filepath.Join(wsRoot, filepath.FromSlash(rel))
		if !hasExplicitNomosConfig(absPath, wsRoot) {
			continue
		}
		checked++
		radarOut := evaluateFastRadarInMemory(absPath, wsRoot)
		for _, line := range strings.Split(radarOut, "\n") {
			if strings.Contains(line, "Radar Breach") {
				breaches = append(breaches, line)
			}
		}
	}
	joined := strings.Join(breaches, "\n")
	const maxRadarBytes = 1600
	if len(joined) > maxRadarBytes {
		return joined[:maxRadarBytes] + "\n..."
	}
	return joined
}
