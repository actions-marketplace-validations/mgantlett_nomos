// Package impact enforces ratcheted Zero-Dust reachability on net-new worktree source files.
package impact

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

var (
	goMainDeclRe      = regexp.MustCompile(`(?m)^\s*package\s+main\b`)
	exemptRatchetDirs = map[string]bool{"routes": true, "pages": true, "cmd": true, "bin": true, "scripts": true}
	exemptRatchetStem = map[string]bool{"index": true, "main": true, "mod": true, "lib": true, "__init__": true, "__main__": true}
)

// EvaluateWorktreeUnreachedFiles detects net-new worktree source files with zero incoming imports or test callers (@intent nomos_intent_traceability_spec.md #ArchitectureBoundaryRule).
func EvaluateWorktreeUnreachedFiles(ctx *vscope.VerifyContext, root string) []string {
	if root == "" {
		return nil
	}
	modified := vscope.GetModifiedFilesInScope(ctx, root)
	if len(modified) == 0 {
		return nil
	}
	netNew := filterNetNewNonEntrypointFiles(ctx, root, modified)
	if len(netNew) == 0 {
		return nil
	}
	tracked := listBoundaryCandidateFiles(root)
	if len(tracked) <= 1 {
		return nil
	}
	activeEco := collectEntrypointEcosystems(root, tracked)
	importedTokens := collectRepositoryImportTokens(root, tracked)
	var violations []string
	for _, rel := range netNew {
		if !activeEco[ratchetEcosystem(rel)] {
			continue
		}
		if !isCandidateReferenced(root, rel, tracked, importedTokens) {
			violations = append(violations, fmt.Sprintf("%s: newly added source file is unreachable (0 incoming imports or test callers)", rel))
		}
	}
	sort.Strings(violations)
	return violations
}

func collectEntrypointEcosystems(root string, tracked []string) map[string]bool {
	out := make(map[string]bool)
	for _, rel := range tracked {
		if IsTestSourceFile(rel) {
			continue
		}
		full := filepath.Join(root, filepath.FromSlash(rel))
		if isExplicitAppEntrypoint(rel, full) {
			out[ratchetEcosystem(rel)] = true
		}
	}
	return out
}

func isExplicitAppEntrypoint(relSlash, fullPath string) bool {
	return hasExemptSegment(relSlash) || hasExemptStemOrConvention(relSlash) || isGoMainSourceFile(relSlash, fullPath)
}

func ratchetEcosystem(relSlash string) string {
	switch strings.ToLower(path.Ext(relSlash)) {
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".svelte":
		return "web"
	case ".c", ".cc", ".cpp", ".cxx", ".h", ".hpp":
		return "c_cpp"
	case ".java", ".kt", ".kts":
		return "jvm"
	default:
		return strings.ToLower(path.Ext(relSlash))
	}
}

func filterNetNewNonEntrypointFiles(ctx *vscope.VerifyContext, root string, modified []string) []string {
	baseRef := resolveGitMergeBaseRef(ctx, root)
	var out []string
	for _, entry := range modified {
		rel, full, ok := resolveBoundaryTargetFile(root, entry)
		if !ok || IsTestSourceFile(rel) || isExemptReachabilityRoot(rel, full) {
			continue
		}
		if baseRef != "" && fileExistedAtRef(root, baseRef, rel) {
			continue
		}
		out = append(out, rel)
	}
	return out
}

func resolveGitMergeBaseRef(ctx *vscope.VerifyContext, root string) string {
	if !vscope.HasGitMetadata(root) {
		return ""
	}
	for _, ref := range []string{"main", "origin/main", "master", "origin/master"} {
		if out, err := workspace.RunSafeGit(root, "merge-base", "HEAD", ref); err == nil {
			if sha := strings.TrimSpace(string(out)); sha != "" {
				return sha
			}
		}
	}
	_ = ctx
	return ""
}

func fileExistedAtRef(root, baseRef, relSlash string) bool {
	_, err := workspace.RunSafeGit(root, "cat-file", "-e", baseRef+":"+relSlash)
	return err == nil
}

func isExemptReachabilityRoot(relSlash, fullPath string) bool {
	if !strings.Contains(relSlash, "/") || hasExemptSegment(relSlash) || hasExemptStemOrConvention(relSlash) {
		return true
	}
	return isGoMainSourceFile(relSlash, fullPath)
}

func hasExemptSegment(relSlash string) bool {
	for _, seg := range strings.Split(path.Dir(relSlash), "/") {
		if exemptRatchetDirs[seg] {
			return true
		}
	}
	return false
}

func hasExemptStemOrConvention(relSlash string) bool {
	rawBase := path.Base(relSlash)
	lowerBase := strings.ToLower(rawBase)
	stem := strings.TrimSuffix(lowerBase, path.Ext(lowerBase))
	return strings.HasPrefix(rawBase, "+") || strings.Contains(lowerBase, ".config.") || exemptRatchetStem[stem]
}

func isGoMainSourceFile(relSlash, fullPath string) bool {
	if !strings.HasSuffix(strings.ToLower(relSlash), ".go") {
		return false
	}
	raw, err := os.ReadFile(fullPath)
	return err == nil && goMainDeclRe.Match(raw)
}

func collectRepositoryImportTokens(root string, tracked []string) map[string]bool {
	tokens := make(map[string]bool)
	for _, rel := range tracked {
		full := filepath.Join(root, filepath.FromSlash(rel))
		for _, imp := range ExtractPolyglotFileImports(full) {
			for _, cand := range buildImportMatchCandidates(rel, imp) {
				tokens[cand] = true
				tokens[stripExtIfCode(cand)] = true
			}
		}
	}
	return tokens
}

func stripExtIfCode(p string) string {
	ext := path.Ext(p)
	if ext == "" {
		return p
	}
	return strings.TrimSuffix(p, ext)
}

func isCandidateReferenced(root, rel string, tracked []string, importedTokens map[string]bool) bool {
	if strings.HasSuffix(rel, ".go") {
		return isGoFilePackageReached(rel, tracked, importedTokens)
	}
	noExt := stripExtIfCode(rel)
	if importedTokens[rel] || importedTokens[noExt] {
		return true
	}
	return ResolvePairedTestStatus(root, rel) != "no paired unit test"
}

func isGoFilePackageReached(rel string, tracked []string, importedTokens map[string]bool) bool {
	pkgDir := path.Dir(rel)
	for _, other := range tracked {
		if other != rel && path.Dir(other) == pkgDir && strings.HasSuffix(other, ".go") {
			return true
		}
	}
	return importedTokens[pkgDir]
}
