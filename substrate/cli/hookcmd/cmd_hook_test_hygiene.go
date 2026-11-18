// Package hookcmd provides non-blocking deliverable hygiene steering for scratch tests in zero-baseline repos (Law 2).
package hookcmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
)

// evaluateTestPlacementHygieneAdvisory emits a one-time non-blocking tip when a net-new
// test file is created outside .tmp/ inside an initialized workspace (nomos.yaml) with zero baseline committed tests.
func evaluateTestPlacementHygieneAdvisory(resolved, baseCwd string) string {
	wsRoot := resolveHygieneRoot(resolved, baseCwd)
	if wsRoot == "" || !isWorkspaceTestPath(resolved, wsRoot) {
		return ""
	}
	if !hasExplicitNomosConfig(resolved, baseCwd) && !hasExplicitNomosConfig(resolved, wsRoot) {
		return ""
	}
	rel, err := filepath.Rel(wsRoot, resolved)
	if err != nil {
		return ""
	}
	slashRel := filepath.ToSlash(filepath.Clean(rel))
	if slashRel == "." || strings.HasPrefix(slashRel, "..") || strings.HasPrefix(strings.ToLower(slashRel), ".tmp/") {
		return ""
	}
	if !workspaceHasZeroTrackedTests(wsRoot) {
		return ""
	}
	if !markTestHygieneAdvisedOnce(wsRoot) {
		return ""
	}
	return fmt.Sprintf(
		"💡 [Nomos Deliverable Hygiene] Created '%s' outside '.tmp/tests/' in a workspace with zero baseline tests; keep deliverable files clean by placing scratch verification tests under '.tmp/tests/test_<module>.py' (or '.tmp/tests/') unless tests were explicitly requested.",
		slashRel,
	)
}

func resolveHygieneRoot(resolved, baseCwd string) string {
	if pr, err := workspace.FindRepoRoot(resolved); err == nil && pr != "" {
		return pr
	}
	if pr, err := workspace.FindRepoRoot(baseCwd); err == nil && pr != "" {
		return pr
	}
	return baseCwd
}

func workspaceHasZeroTrackedTests(wsRoot string) bool {
	if strings.TrimSpace(wsRoot) == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(wsRoot, ".git")); err != nil {
		return true
	}
	out, gErr := workspace.RunSafeGit(wsRoot, "ls-files")
	if gErr != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		rel := strings.TrimSpace(line)
		if rel != "" && isWorkspaceTestPath(filepath.Join(wsRoot, filepath.FromSlash(rel)), wsRoot) {
			return false
		}
	}
	return true
}

func markTestHygieneAdvisedOnce(wsRoot string) bool {
	sum := sha256.Sum256([]byte(filepath.Clean(wsRoot)))
	marker := filepath.Join(os.TempDir(), "nomos-test-hygiene-"+hex.EncodeToString(sum[:8])+".seen")
	if _, err := os.Stat(marker); err == nil {
		return false
	}
	if wErr := os.WriteFile(marker, []byte("1"), 0600); wErr != nil {
		return true
	}
	return true
}
