// Package hookcmd implements one-time PostToolUse 1-hop AST impact hints, compact
// co-processor handshake rendering, and PreToolUse symlink/secret-dump guards.
package hookcmd

import (
	"bytes"
	"fmt"
	"github.com/mgantlett/nomos/substrate/cli/hookcmd/shellguard"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/constants"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/handshake"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/impact"
)

var (
	symlinkCreateCmdRe = regexp.MustCompile(`(?i)(?:^|[;&|]\s*)ln\s+(?:-[a-z]*s[a-z]*|--symbolic)\b`)
	envSecretDumpCmdRe = regexp.MustCompile(`(?i)(?:^|[;&|]\s*)(?:printenv|env|export\s+-p)\b[^\n;|&]*(?:SECRET|TOKEN|PASSWORD|API_KEY|SIGNING_KEY)`)
	siblingEscapeSegRe = regexp.MustCompile(`(?i)(?:^|/)(?:verifier|solution_cache|secrets)(?:/|$)`)
)

func evaluateShellAliasAndRedirectGuard(cmdLine, baseCwd, hermeticDir string) shellguard.HookDecision {
	if symlinkCreateCmdRe.MatchString(cmdLine) {
		return shellguard.HookDecision{
			Decision: hookDecisionDeny,
			Reason:   "❌ [NOMOS INTERCEPT] Filesystem Alias Violation (Law 6 ZeroFilesystemAliasGate): Creating symlinks ('ln -s') is prohibited; materialize explicit canonical files instead.",
		}
	}
	if envSecretDumpCmdRe.MatchString(cmdLine) {
		return shellguard.HookDecision{
			Decision: hookDecisionDeny,
			Reason:   "❌ [NOMOS INTERCEPT] Environment Secret Airlock (Law 7): Dumping secret or token environment variables via shell is strictly prohibited.",
		}
	}
	if siblingEscapeSegRe.MatchString(cmdLine) && strings.Contains(cmdLine, "..") {
		return shellguard.HookDecision{
			Decision: hookDecisionDeny,
			Reason:   "❌ [NOMOS INTERCEPT] Hermetic Verifier/Cache Isolation (Law 2 & Law 3): Accessing parent '../verifier', '../solution_cache', or '../secrets' paths is strictly prohibited.",
		}
	}
	return evaluateTrunkShellRedirectGuard(cmdLine, baseCwd, hermeticDir)
}

func evaluateReadToolBoundaryAndBinary(p *shellguard.HookPayload, hermeticDir string) shellguard.HookDecision {
	if dec := shellguard.EvaluateBinaryReadGuard(p, hermeticDir != ""); dec.Decision == hookDecisionDeny {
		return dec
	}
	if p == nil || p.TargetFile == "" {
		return shellguard.HookDecision{Decision: hookDecisionAllow}
	}
	if strings.Contains(p.TargetFile, "..") && siblingEscapeSegRe.MatchString(filepath.ToSlash(p.TargetFile)) {
		return shellguard.HookDecision{
			Decision: hookDecisionDeny,
			Reason:   fmt.Sprintf("❌ [NOMOS INTERCEPT] Hermetic Verifier/Cache Isolation (Law 2 & Law 3): Reading '%s' outside the workspace boundary is strictly prohibited.", p.TargetFile),
		}
	}
	if hermeticDir == "" {
		return shellguard.HookDecision{Decision: hookDecisionAllow}
	}
	baseCwd := resolveHookWorkspaceDir(p, hermeticDir)
	resolved := shellguard.NormalizeCanonicalPath(p.TargetFile, baseCwd)
	return evaluateHermeticDirBoundary(p.TargetFile, resolved, hermeticDir)
}

// evaluateFirstEditImpactAdvisory emits a 1-line 1-hop AST impact and Go interface peer
// hint at most once per mutated source file in a worktree or initialized feature-branch session.
func evaluateFirstEditImpactAdvisory(p *shellguard.HookPayload, hermeticDir string) string {
	wtRoot, traceKey := resolveImpactTraceScope(p, hermeticDir)
	if wtRoot == "" || traceKey == "" {
		return ""
	}
	tracePath := resolveTraceStateFilePath(traceKey)
	if tracePath == "" {
		return ""
	}
	canonWt := shellguard.NormalizeCanonicalPath(wtRoot, "/")
	baseCwd := resolveHookWorkspaceDir(p, hermeticDir)
	tr := loadOrInitHookSessionTrace(tracePath, wtRoot)
	relFiles, dirty := collectUnseenImpactedFiles(p, &tr, canonWt, baseCwd)
	if dirty {
		saveHookSessionTrace(tracePath, tr)
	}
	var lines []string
	for _, rel := range relFiles {
		if hint := verify.ComputeFirstEditCoChangeHint(canonWt, rel); hint != "" {
			lines = append(lines, fmt.Sprintf("🧭 [Nomos 1-Hop Impact] %s", hint))
		}
	}
	return strings.Join(lines, "\n")
}

func resolveImpactTraceScope(p *shellguard.HookPayload, hermeticDir string) (string, string) {
	if wt := resolveWorktreeTraceRoot(p, hermeticDir); wt != "" {
		return wt, wt
	}
	return resolveFeatureBranchTraceScope(p, hermeticDir)
}

func resolveFeatureBranchTraceScope(p *shellguard.HookPayload, hermeticDir string) (string, string) {
	baseCwd := resolveHookWorkspaceDir(p, hermeticDir)
	probe := baseCwd
	if p != nil && strings.TrimSpace(p.TargetFile) != "" {
		probe = filepath.Dir(shellguard.NormalizeCanonicalPath(p.TargetFile, baseCwd))
	}
	wt, err := workspace.FindWorkingTreeRoot(probe)
	if err != nil || wt == "" {
		return "", ""
	}
	if _, statErr := os.Stat(filepath.Join(wt, constants.FileNomosYAML)); statErr != nil {
		return "", ""
	}
	out, bErr := workspace.SafeGitCommand(wt, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if bErr != nil {
		return "", ""
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" || branch == "main" || branch == "master" || branch == "HEAD" {
		return "", ""
	}
	return wt, wt + "@" + branch
}

func collectUnseenImpactedFiles(p *shellguard.HookPayload, tr *HookSessionTrace, canonWt, baseCwd string) ([]string, bool) {
	targets := p.TargetFiles
	if len(targets) == 0 && p.TargetFile != "" {
		targets = []string{p.TargetFile}
	}
	var unseen []string
	dirty := false
	for _, raw := range targets {
		resolved := shellguard.NormalizeCanonicalPath(raw, baseCwd)
		if !strings.HasPrefix(resolved, canonWt+"/") {
			continue
		}
		rel := strings.TrimPrefix(resolved, canonWt+"/")
		if isIgnoredPitfallScopeFile(rel) || containsTraceItem(tr.ImpactedFiles, rel) {
			continue
		}
		tr.ImpactedFiles = appendUniqueBounded(tr.ImpactedFiles, rel, 64)
		dirty = true
		unseen = append(unseen, rel)
	}
	return unseen, dirty
}

const (
	hotspotReadCeilingLOC = 500
	monorepoSeamFileFloor = 1000
	maxMonorepoSeamReads  = 5
	monoScaleYesToken     = "__mono:1"
	monoScaleNoToken      = "__mono:0"
	seamReadPrefix        = "seam:"
)

// evaluateFirstHotspotReadHint emits a one-time orientation advisory when an agent inside an
// active worktree reads a >=500-LOC brownfield hotspot file, or reads up to 5 coupled <500-LOC
// source files inside a >=1,000-file monorepo.
func evaluateFirstHotspotReadHint(p *shellguard.HookPayload, tr *HookSessionTrace, wtRoot string) string {
	resolved, rel, canonWt, ok := resolveUnseenHotspotReadTarget(p, tr, wtRoot)
	if !ok || impact.IsTestSourceFile(rel) {
		return ""
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return ""
	}
	lines := bytes.Count(data, []byte("\n")) + 1
	if lines < hotspotReadCeilingLOC {
		return evaluateMonorepoFirstReadSeamHint(canonWt, rel, tr)
	}
	tr.ImpactedFiles = appendUniqueBounded(tr.ImpactedFiles, rel, 64)
	msg := fmt.Sprintf("🧭 [Nomos Hotspot Read] %s (%d LOC >= %d ceiling): avoid growing in place; keep call-site edits minimal and place net-new helpers in a sibling file", rel, lines, hotspotReadCeilingLOC)
	if paired := resolveExistingPairedTestRel(canonWt, rel); paired != "" {
		msg += " | paired test: " + paired
	}
	if peers := verify.ComputeFirstEditCoChangeHint(canonWt, rel); peers != "" {
		msg += " | " + peers
	}
	return msg
}

func evaluateMonorepoFirstReadSeamHint(canonWt, rel string, tr *HookSessionTrace) string {
	if countMonorepoSeamReads(tr) >= maxMonorepoSeamReads || !isLargeMonorepoWorkspace(canonWt, tr) {
		return ""
	}
	var parts []string
	if paired := resolveExistingPairedTestRel(canonWt, rel); paired != "" {
		parts = append(parts, "paired test: "+paired)
	}
	if peers := verify.ComputeFirstEditCoChangeHint(canonWt, rel); peers != "" {
		parts = append(parts, peers)
	}
	if len(parts) == 0 {
		return ""
	}
	tr.ImpactedFiles = appendUniqueBounded(tr.ImpactedFiles, rel, 64)
	tr.ImpactedFiles = appendUniqueBounded(tr.ImpactedFiles, seamReadPrefix+rel, 64)
	return fmt.Sprintf("🧭 [Nomos Monorepo Seam] %s -> %s", rel, strings.Join(parts, " | "))
}

func countMonorepoSeamReads(tr *HookSessionTrace) int {
	n := 0
	for _, item := range tr.ImpactedFiles {
		if strings.HasPrefix(item, seamReadPrefix) {
			n++
		}
	}
	return n
}

func isLargeMonorepoWorkspace(canonWt string, tr *HookSessionTrace) bool {
	if containsTraceItem(tr.ImpactedFiles, monoScaleYesToken) {
		return true
	}
	if containsTraceItem(tr.ImpactedFiles, monoScaleNoToken) {
		return false
	}
	out, err := workspace.SafeGitCommand(canonWt, "ls-files", "-z", "--cached").Output()
	if err != nil {
		tr.ImpactedFiles = appendUniqueBounded(tr.ImpactedFiles, monoScaleNoToken, 64)
		return false
	}
	if bytes.Count(out, []byte{0}) >= monorepoSeamFileFloor {
		tr.ImpactedFiles = appendUniqueBounded(tr.ImpactedFiles, monoScaleYesToken, 64)
		return true
	}
	tr.ImpactedFiles = appendUniqueBounded(tr.ImpactedFiles, monoScaleNoToken, 64)
	return false
}

func resolveExistingPairedTestRel(canonWt, rel string) string {
	ext := strings.ToLower(filepath.Ext(rel))
	stem := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
	dir := filepath.ToSlash(filepath.Dir(rel))
	for _, cand := range monorepoPairedTestRelCandidates(dir, stem, ext) {
		if fi, err := os.Stat(filepath.Join(canonWt, filepath.FromSlash(cand))); err == nil && !fi.IsDir() {
			return cand
		}
	}
	return ""
}

func monorepoPairedTestRelCandidates(dir, stem, ext string) []string {
	joinClean := func(parts ...string) string { return filepath.ToSlash(filepath.Join(parts...)) }
	switch ext {
	case ".go":
		return []string{joinClean(dir, stem+"_test.go")}
	case ".py":
		cands := []string{
			joinClean(dir, "test_"+stem+".py"),
			joinClean(dir, stem+"_test.py"),
			joinClean("tests", dir, "test_"+stem+".py"),
			joinClean("tests", dir, stem+"_test.py"),
		}
		if strings.HasPrefix(dir, "src/") {
			sub := strings.TrimPrefix(dir, "src/")
			cands = append(cands, joinClean("tests", sub, "test_"+stem+".py"))
		}
		return cands
	case ".ts", ".tsx", ".js", ".jsx":
		return []string{joinClean(dir, stem+".test"+ext), joinClean(dir, stem+".spec"+ext), joinClean(dir, "__tests__", stem+".test"+ext)}
	case ".rs":
		return []string{joinClean(dir, "tests.rs"), joinClean(dir, stem+"_test.rs")}
	default:
		return nil
	}
}

func resolveUnseenHotspotReadTarget(p *shellguard.HookPayload, tr *HookSessionTrace, wtRoot string) (string, string, string, bool) {
	if p == nil || tr == nil || wtRoot == "" || p.CommandLine != "" || p.TargetFile == "" {
		return "", "", "", false
	}
	canonWt := shellguard.NormalizeCanonicalPath(wtRoot, "/")
	resolved := shellguard.NormalizeCanonicalPath(p.TargetFile, resolveHookWorkspaceDir(p, ""))
	if !strings.HasPrefix(resolved, canonWt+"/") {
		return "", "", "", false
	}
	rel := strings.TrimPrefix(resolved, canonWt+"/")
	if isIgnoredPitfallScopeFile(rel) || containsTraceItem(tr.ImpactedFiles, rel) {
		return "", "", "", false
	}
	return resolved, rel, canonWt, true
}

// RenderCompactHandshakeBanner emits a dense co-processor handshake banner to stdout.
// @intent task_lifecycle_cli_spec.md
func RenderCompactHandshakeBanner(event *handshake.HandshakeEvent) {
	if event == nil {
		return
	}
	taskLabel := "trunk"
	if event.ActiveTask != "" {
		taskLabel = event.ActiveTask
	}
	fmt.Printf("[Nomos Compact Handshake] repo=%s phase=%s task=%s gates=%d\n",
		event.Repository, event.Phase, taskLabel, event.GateCount)
	if event.BrownfieldTopology != nil {
		for _, item := range BuildBrownfieldTopologyPairs(event.BrownfieldTopology) {
			fmt.Printf("- %s: %s\n", item.Label, item.Value)
		}
	}
}
