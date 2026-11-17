// Package hookcmd renders compact declared-dependency and workspace module hulls during SessionStart.
package hookcmd

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	maxHullDeps    = 10
	maxHullModules = 6
	maxHullSymbols = 4
	maxHullFiles   = 16
)

var (
	hullPyDepBlockRe  = regexp.MustCompile(`(?s)dependencies\s*=\s*\[(.*?)\]`)
	hullQuotedTokenRe = regexp.MustCompile(`["']([A-Za-z0-9][A-Za-z0-9._-]*)`)
	hullReqLineRe     = regexp.MustCompile(`^\s*([A-Za-z0-9][A-Za-z0-9._-]*)`)
	hullCargoDepSecRe = regexp.MustCompile(`(?ms)^\[dependencies\]\s*\n(.*?)(?:^\[|\z)`)
	hullCargoKeyRe    = regexp.MustCompile(`^\s*([A-Za-z0-9_-]+)\s*=`)
	hullSymbolDefRe   = regexp.MustCompile(`^(?:def|class|export\s+(?:async\s+)?function|export\s+class|pub\s+(?:async\s+)?fn|fn)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	hullSkipDirs      = map[string]bool{
		".git": true, ".venv": true, "venv": true, "node_modules": true,
		"vendor": true, "worktrees": true, "substrate": true, "intent": true,
		"__pycache__": true, ".tmp": true,
		"testdata": true, "fixtures": true, "examples": true, "samples": true, "files": true, "assets": true,
	}
	hullSourceExts = map[string]bool{".py": true, ".ts": true, ".js": true, ".rs": true}
)

// @intent nomos_hermetic_worktree_spec.md
func appendPlainWorkspaceHullWithMode(b *strings.Builder, hookRoot string, containerSandbox bool) {
	if strings.TrimSpace(hookRoot) == "" {
		return
	}
	if deps := discoverManifestDeps(hookRoot); len(deps) > 0 {
		b.WriteString(fmt.Sprintf("  Manifest Deps     : %s\n", strings.Join(deps, ", ")))
	}
	mods := discoverWorkspaceModuleHull(hookRoot)
	if len(mods) > 0 {
		b.WriteString(fmt.Sprintf("  Workspace Modules : %s\n", strings.Join(mods, ", ")))
	}
	if (len(mods) > 0 || containerSandbox) && !hasWorkspaceAutomatedTests(hookRoot) {
		b.WriteString("  Regression Suite  : none detected - create stdlib unittest.TestCase tests in .tmp/tests/test_<module>.py covering boundary, multi-trigger/multi-item, and error cases before 'nomos verify' (keeps workspace git-clean and enforces >=80% coverage)\n")
	}
}

func discoverManifestDeps(root string) []string {
	seen := make(map[string]bool)
	var out []string
	addDep := func(raw string) {
		name := strings.TrimSpace(raw)
		if name != "" && !seen[name] && len(out) < maxHullDeps {
			seen[name] = true
			out = append(out, name)
		}
	}
	collectPythonManifestDeps(root, addDep)
	collectNodeManifestDeps(root, addDep)
	collectCargoManifestDeps(root, addDep)
	return out
}

func collectPythonManifestDeps(root string, addDep func(string)) {
	if raw, err := os.ReadFile(filepath.Join(root, "pyproject.toml")); err == nil {
		for _, blk := range hullPyDepBlockRe.FindAllStringSubmatch(string(raw), -1) {
			for _, m := range hullQuotedTokenRe.FindAllStringSubmatch(blk[1], -1) {
				addDep(m[1])
			}
		}
	}
	if raw, err := os.ReadFile(filepath.Join(root, "requirements.txt")); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			if idx := strings.IndexByte(line, '#'); idx >= 0 {
				line = line[:idx]
			}
			if m := hullReqLineRe.FindStringSubmatch(line); len(m) == 2 {
				addDep(m[1])
			}
		}
	}
}

func collectNodeManifestDeps(root string, addDep func(string)) {
	raw, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return
	}
	var pkg struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if json.Unmarshal(raw, &pkg) != nil || len(pkg.Dependencies) == 0 {
		return
	}
	names := make([]string, 0, len(pkg.Dependencies))
	for k := range pkg.Dependencies {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, n := range names {
		addDep(n)
	}
}

func collectCargoManifestDeps(root string, addDep func(string)) {
	raw, err := os.ReadFile(filepath.Join(root, "Cargo.toml"))
	if err != nil {
		return
	}
	m := hullCargoDepSecRe.FindStringSubmatch(string(raw))
	if len(m) != 2 {
		return
	}
	for _, line := range strings.Split(m[1], "\n") {
		if km := hullCargoKeyRe.FindStringSubmatch(line); len(km) == 2 {
			addDep(km[1])
		}
	}
}

func discoverWorkspaceModuleHull(root string) []string {
	cleanRoot := filepath.Clean(root)
	var files []string
	tooMany := false
	_ = filepath.WalkDir(cleanRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil
		}
		if d.IsDir() {
			if path != cleanRoot && hullSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rErr := filepath.Rel(cleanRoot, path)
		if rErr != nil {
			return nil
		}
		slash := filepath.ToSlash(rel)
		if !hullSourceExts[strings.ToLower(filepath.Ext(slash))] || isHullTestFile(slash) {
			return nil
		}
		files = append(files, slash)
		if len(files) > maxHullFiles {
			tooMany = true
			return fs.SkipAll
		}
		return nil
	})
	if len(files) == 0 || tooMany {
		return nil
	}
	sort.Strings(files)
	var summaries []string
	for i := 0; i < len(files) && i < maxHullModules; i++ {
		summaries = append(summaries, formatModuleSymbols(cleanRoot, files[i]))
	}
	return summaries
}

func isHullTestFile(slash string) bool {
	base := filepath.Base(slash)
	return strings.HasPrefix(base, "test_") || strings.Contains(base, "_test.") || strings.Contains(base, ".test.") ||
		strings.HasPrefix(slash, "tests/") || strings.Contains(slash, "/tests/")
}

func formatModuleSymbols(root, slashRel string) string {
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(slashRel)))
	if err != nil || len(raw) > 256*1024 {
		return slashRel
	}
	var syms []string
	for _, line := range strings.Split(string(raw), "\n") {
		if len(syms) >= maxHullSymbols {
			break
		}
		if m := hullSymbolDefRe.FindStringSubmatch(line); len(m) == 2 && !strings.HasPrefix(m[1], "__") {
			syms = append(syms, m[1])
		}
	}
	if len(syms) == 0 {
		return slashRel
	}
	return fmt.Sprintf("%s (%s)", slashRel, strings.Join(syms, ", "))
}
