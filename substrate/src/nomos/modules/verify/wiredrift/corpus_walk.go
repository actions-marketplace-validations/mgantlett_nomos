// Package wiredrift provides workspace source walking for Law 4 contract wire-boundary checks.
package wiredrift

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/impact"
)

const maxContractSourceBytes = 1024 * 1024

var skippedContractDirs = map[string]bool{
	".git": true, ".tmp": true, "worktrees": true, "node_modules": true,
	"vendor": true, ".venv": true, "venv": true, "site-packages": true,
	"dist": true, "webdist": true, "build": true, "out": true,
	".svelte-kit": true, "testdata": true, "__tests__": true, "tests": true, "test": true,
}

// VerifyCorpusWireBoundary scans root for untyped DTO escapes and phantom wire keys (@intent tree_sitter_ast_gate_spec.md #BoundaryDriftFinding).
func VerifyCorpusWireBoundary(root string) string {
	sources := collectContractCorpusSources(root)
	findings := ScanCorpusBoundaryWireDrift(sources)
	if len(findings) == 0 {
		return ""
	}
	limit := len(findings)
	if limit > 5 {
		limit = 5
	}
	parts := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		parts = append(parts, FormatBoundaryDriftFinding(findings[i]))
	}
	return fmt.Sprintf("detected %d wire-boundary contract drift violation(s): %s", len(findings), strings.Join(parts, "; "))
}

func collectContractCorpusSources(root string) map[string][]byte {
	sources := make(map[string][]byte)
	walkErr := workspace.WalkScope(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.IsDir() {
			if path != root && skippedContractDirs[strings.ToLower(info.Name())] {
				return filepath.SkipDir
			}
			return nil
		}
		if !isContractScanCandidate(root, path, info) {
			return nil
		}
		rel, rErr := filepath.Rel(root, path)
		if rErr != nil {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr == nil {
			sources[filepath.ToSlash(rel)] = data
		}
		return nil
	})
	if walkErr != nil {
		return sources
	}
	return sources
}

func isContractScanCandidate(root, path string, info os.FileInfo) bool {
	if info.Size() > maxContractSourceBytes || (info.Mode()&os.ModeSymlink != 0) {
		return false
	}
	name := info.Name()
	lowerName := strings.ToLower(name)
	if impact.IsVerificationSuiteFile(name) || strings.HasSuffix(lowerName, ".test.ts") || strings.HasSuffix(lowerName, ".spec.ts") {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err == nil {
		slash := strings.ToLower(filepath.ToSlash(rel))
		if strings.Contains(slash, "/__tests__/") || strings.Contains(slash, "/testdata/") {
			return false
		}
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".go", ".ts", ".tsx", ".js", ".svelte", ".py":
		return true
	default:
		return false
	}
}
