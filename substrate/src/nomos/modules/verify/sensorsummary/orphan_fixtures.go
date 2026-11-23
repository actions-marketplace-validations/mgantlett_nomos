// Package sensorsummary implements diff-ratcheted orphaned testdata and fixture hygiene checks.
// @intent nomos_intent_traceability_spec.md
package sensorsummary

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

var fixtureRootSegments = map[string]bool{
	"testdata":    true,
	"fixtures":    true,
	"conformance": true,
	"examples":    true,
}

var exemptFixtureDocNames = map[string]bool{
	"readme.md":  true,
	"license":    true,
	"license.md": true,
	".gitkeep":   true,
	".gitignore": true,
}

type fixtureConsumerCorpus struct {
	allText       string
	dirWalkedDirs map[string]bool
}

// ScanOrphanedFixtures flags added/modified files in testdata/, fixtures/, conformance/, or examples/
// (or all such files under ctx.Strict) that have zero test, go:embed, or CI references.
// @intent nomos_intent_traceability_spec.md #AntiShimDTO
func ScanOrphanedFixtures(ctx *vscope.VerifyContext, root string) []string {
	cleanRoot := strings.TrimSpace(root)
	if cleanRoot == "" {
		return nil
	}
	candidates := collectCandidateFixtures(ctx, cleanRoot)
	if len(candidates) == 0 {
		return nil
	}
	corpus := buildFixtureConsumerCorpus(cleanRoot, candidates)
	var violations []string
	for _, rel := range candidates {
		if !isFixtureReferenced(rel, corpus) {
			violations = append(violations, fmt.Sprintf("orphaned_fixture (%s has zero test, go:embed, or CI references)", rel))
		}
	}
	return violations
}

func collectCandidateFixtures(ctx *vscope.VerifyContext, root string) []string {
	if ctx != nil && ctx.Strict {
		return collectAllRepoFixtures(root)
	}
	if !vscope.IsWorktreeContext(ctx, root) {
		return nil
	}
	modified := vscope.GetModifiedFilesInScope(ctx, root)
	if len(modified) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var out []string
	for _, m := range modified {
		rel := normalizeFixtureRelPath(root, m)
		if rel == "" || seen[rel] || !isCandidateFixtureRelPath(rel) {
			continue
		}
		if fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil && !fi.IsDir() {
			seen[rel] = true
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out
}

func collectAllRepoFixtures(root string) []string {
	var out []string
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil
		}
		if d.IsDir() {
			if shouldSkipFixtureWalkDir(path, root, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rErr := filepath.Rel(root, path)
		if rErr != nil {
			return nil
		}
		slash := filepath.ToSlash(rel)
		if isCandidateFixtureRelPath(slash) {
			out = append(out, slash)
		}
		return nil
	})
	if walkErr != nil {
		sort.Strings(out)
		return out
	}
	sort.Strings(out)
	return out
}

func shouldSkipFixtureWalkDir(path, root, name string) bool {
	if path == root {
		return false
	}
	low := strings.ToLower(name)
	if strings.HasPrefix(low, ".") && low != ".github" {
		return true
	}
	return low == "worktrees" || low == "node_modules" || low == "vendor" || low == "dist" || low == "build" || low == "__pycache__" || low == ".venv" || low == "venv"
}

func normalizeFixtureRelPath(root, raw string) string {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return ""
	}
	if filepath.IsAbs(clean) {
		rel, err := filepath.Rel(root, clean)
		if err != nil {
			return ""
		}
		clean = rel
	}
	slash := strings.TrimPrefix(filepath.ToSlash(clean), "./")
	if strings.HasPrefix(slash, "../") {
		return ""
	}
	return slash
}

func isCandidateFixtureRelPath(relSlash string) bool {
	baseLow := strings.ToLower(filepath.Base(relSlash))
	if exemptFixtureDocNames[baseLow] || isConsumerTestOrBuildFile(relSlash) {
		return false
	}
	parts := strings.Split(relSlash, "/")
	if len(parts) < 2 {
		return false
	}
	for _, seg := range parts[:len(parts)-1] {
		if fixtureRootSegments[strings.ToLower(seg)] {
			return true
		}
	}
	return false
}

func isConsumerTestOrBuildFile(relSlash string) bool {
	low := strings.ToLower(relSlash)
	base := filepath.Base(low)
	if strings.HasSuffix(low, "_test.go") || strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") ||
		strings.HasPrefix(base, "test_") || strings.HasSuffix(low, "_test.py") || strings.HasSuffix(low, "_spec.rb") {
		return true
	}
	if strings.HasPrefix(low, ".github/workflows/") || strings.HasPrefix(low, "scripts/") {
		return true
	}
	return base == "makefile" || base == "justfile" || base == "package.json" || base == "pyproject.toml" || base == "cargo.toml"
}

func isEmbedCapableSourceFile(relSlash string) bool {
	ext := strings.ToLower(filepath.Ext(relSlash))
	return ext == ".go" || ext == ".rs" || ext == ".ts" || ext == ".js" || ext == ".py"
}

func buildFixtureConsumerCorpus(root string, candidates []string) fixtureConsumerCorpus {
	candSet := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		candSet[c] = true
	}
	var sb strings.Builder
	walkedDirs := make(map[string]bool)
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil
		}
		if d.IsDir() {
			if shouldSkipFixtureWalkDir(path, root, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		appendConsumerFileContent(root, path, candSet, &sb, walkedDirs)
		return nil
	})
	if walkErr != nil {
		return fixtureConsumerCorpus{allText: sb.String(), dirWalkedDirs: walkedDirs}
	}
	return fixtureConsumerCorpus{allText: sb.String(), dirWalkedDirs: walkedDirs}
}

func appendConsumerFileContent(root, path string, candSet map[string]bool, sb *strings.Builder, walkedDirs map[string]bool) {
	rel, rErr := filepath.Rel(root, path)
	if rErr != nil {
		return
	}
	slash := filepath.ToSlash(rel)
	if candSet[slash] {
		return
	}
	isConsumer := isConsumerTestOrBuildFile(slash)
	if !isConsumer && !isEmbedCapableSourceFile(slash) {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 || len(raw) > 1024*1024 {
		return
	}
	txt := string(raw)
	if !isConsumer && !strings.Contains(txt, "go:embed") && !strings.Contains(txt, "include_str!") && !strings.Contains(txt, "include_bytes!") {
		return
	}
	sb.WriteString(txt)
	sb.WriteByte('\n')
	recordWalkedFixtureDirs(slash, txt, walkedDirs)
}

func recordWalkedFixtureDirs(consumerSlash, txt string, walkedDirs map[string]bool) {
	hasWalkSignal := strings.Contains(txt, "ReadDir") || strings.Contains(txt, "Walk") ||
		strings.Contains(txt, "Glob") || strings.Contains(txt, "readdir") ||
		strings.Contains(txt, "iterdir") || strings.Contains(txt, "glob(") ||
		strings.Contains(txt, "go:embed")
	if !hasWalkSignal {
		return
	}
	pkgDir := filepath.ToSlash(filepath.Dir(consumerSlash))
	if pkgDir == "." {
		pkgDir = ""
	}
	for rootSeg := range fixtureRootSegments {
		if strings.Contains(txt, rootSeg) {
			if pkgDir == "" {
				walkedDirs[rootSeg] = true
			} else {
				walkedDirs[pkgDir+"/"+rootSeg] = true
			}
		}
	}
}

func isFixtureReferenced(relSlash string, corpus fixtureConsumerCorpus) bool {
	base := filepath.Base(relSlash)
	if strings.Contains(corpus.allText, relSlash) || strings.Contains(corpus.allText, base) {
		return true
	}
	for dirPrefix := range corpus.dirWalkedDirs {
		if strings.HasPrefix(relSlash, dirPrefix+"/") {
			return true
		}
	}
	parentSlash := filepath.ToSlash(filepath.Dir(relSlash))
	parentBase := filepath.Base(parentSlash)
	if !fixtureRootSegments[strings.ToLower(parentBase)] && len(parentBase) >= 3 && strings.Contains(corpus.allText, parentSlash) {
		return true
	}
	return false
}
