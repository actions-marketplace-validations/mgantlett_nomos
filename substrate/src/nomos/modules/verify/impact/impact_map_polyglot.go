// Package impact implements 11-language paired-test resolution for ImpactMap and TDDGate.
package impact

import (
	"bytes"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/astdebt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const PairedTestPrefix = "paired test: "

// ResolvePairedTestStatus executes deterministic impact verification logic (@intent nomos_intent_traceability_spec.md).
func ResolvePairedTestStatus(root, rel string) string {
	ext := strings.ToLower(filepath.Ext(rel))
	stem := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
	dir := filepath.Dir(rel)

	if ext == astdebt.ExtRS && HasInlineRustTestModule(filepath.Join(root, rel)) {
		return PairedTestPrefix + "inline #[cfg(test)]"
	}

	for _, c := range buildPairedTestCandidates(rel, dir, stem, ext) {
		if _, err := os.Stat(filepath.Join(root, c)); err == nil {
			return PairedTestPrefix + filepath.Base(c)
		}
	}
	if matched := searchNestedLanguageTest(root, stem, ext); matched != "" {
		return PairedTestPrefix + matched
	}
	return "no paired unit test"
}

func buildPairedTestCandidates(rel, dir, stem, ext string) []string {
	if astdebt.IsJSFamilyExt(ext) {
		return jsFamilyPairedTestCandidates(dir, stem, ext)
	}
	switch ext {
	case astdebt.ExtGo:
		return []string{filepath.Join(dir, stem+astdebt.SuffixGoTest)}
	case astdebt.ExtPy:
		return pythonPairedTestCandidates(dir, stem)
	case astdebt.ExtRB, astdebt.ExtRake:
		return rubyPairedTestCandidates(dir, stem)
	case astdebt.ExtRS:
		return []string{
			filepath.Join(astdebt.DirTests, stem+astdebt.ExtRS),
			filepath.Join(astdebt.DirTests, stem+"_test.rs"),
			filepath.Join(dir, stem+"_test.rs"),
		}
	default:
		return nativePairedTestCandidates(rel, dir, stem, ext)
	}
}

func pythonPairedTestCandidates(dir, stem string) []string {
	out := []string{
		filepath.Join(dir, astdebt.PrefixPyTest+stem+astdebt.ExtPy),
		filepath.Join(dir, stem+astdebt.SuffixPyTest),
		filepath.Join(astdebt.DirTests, dir, astdebt.PrefixPyTest+stem+astdebt.ExtPy),
		filepath.Join(astdebt.DirTests, dir, stem+astdebt.SuffixPyTest),
		filepath.Join(astdebt.DirTests, astdebt.PrefixPyTest+stem+astdebt.ExtPy),
		filepath.Join(astdebt.DirTests, stem+astdebt.SuffixPyTest),
		filepath.Join(astdebt.DirTest, astdebt.PrefixPyTest+stem+astdebt.ExtPy),
		filepath.Join(".tmp", astdebt.DirTests, astdebt.PrefixPyTest+stem+astdebt.ExtPy),
		filepath.Join(".tmp", astdebt.DirTests, stem+astdebt.SuffixPyTest),
		filepath.Join(".tmp", astdebt.DirTest, astdebt.PrefixPyTest+stem+astdebt.ExtPy),
	}
	slashDir := filepath.ToSlash(dir)
	if strings.HasPrefix(slashDir, "src/") {
		sub := strings.TrimPrefix(slashDir, "src/")
		out = append(out,
			filepath.Join(astdebt.DirTests, sub, astdebt.PrefixPyTest+stem+astdebt.ExtPy),
			filepath.Join(astdebt.DirTests, sub, stem+astdebt.SuffixPyTest),
		)
	}
	return out
}

func rubyPairedTestCandidates(dir, stem string) []string {
	out := []string{
		filepath.Join(dir, stem+"_test.rb"),
		filepath.Join(dir, stem+"_spec.rb"),
		filepath.Join(astdebt.DirTest, stem+"_test.rb"),
		filepath.Join(astdebt.DirSpec, stem+"_spec.rb"),
	}
	slashDir := filepath.ToSlash(dir)
	if strings.HasPrefix(slashDir, "app/") {
		sub := strings.TrimPrefix(slashDir, "app/")
		out = append(out,
			filepath.Join(astdebt.DirTest, sub, stem+"_test.rb"),
			filepath.Join(astdebt.DirSpec, sub, stem+"_spec.rb"),
		)
	}
	return out
}

func tryGitTargetedGoReverseIndex(root string, pkgDirs []string) (map[string][]string, bool) {
	if _, err := workspace.FindWorkingTreeRoot(root); err != nil {
		return nil, false
	}
	cands, ok := collectTargetedGoImporterCandidates(root, pkgDirs)
	if !ok {
		return nil, false
	}
	if len(cands) == 0 {
		return map[string][]string{}, true
	}
	index := make(map[string]map[string]bool)
	for _, ent := range parseGoImportsParallel(root, cands) {
		for _, imp := range ent.imports {
			if index[imp] == nil {
				index[imp] = make(map[string]bool)
			}
			index[imp][ent.relDir] = true
		}
	}
	return flattenGoReverseIndex(index), true
}

func collectTargetedGoImporterCandidates(root string, pkgDirs []string) ([]string, bool) {
	candSet := make(map[string]bool)
	for _, pkgDir := range pkgDirs {
		exact := resolveExactGoImportPath(root, pkgDir)
		if exact == "" {
			return nil, false
		}
		out, err := workspace.SafeGitCommand(
			root, "grep", "-I", "-l", "-z", "-F", "--untracked", "--", "\""+exact+"\"", "*.go",
		).Output()
		if err != nil && len(out) == 0 {
			continue
		}
		for _, p := range filterGitGoImplPaths(out) {
			candSet[p] = true
		}
	}
	cands := make([]string, 0, len(candSet))
	for p := range candSet {
		cands = append(cands, p)
	}
	return cands, true
}

func nativePairedTestCandidates(rel, dir, stem, ext string) []string {
	switch ext {
	case astdebt.ExtJava:
		return jvmPairedTestCandidates(rel, dir, stem, "java", astdebt.ExtJava, []string{"Test", "Tests"})
	case astdebt.ExtKT, astdebt.ExtKTS:
		return jvmPairedTestCandidates(rel, dir, stem, "kotlin", astdebt.ExtKT, []string{"Test", "Tests", "Spec"})
	case astdebt.ExtCS:
		return []string{filepath.Join(dir, stem+"Tests.cs"), filepath.Join(dir, stem+"Test.cs")}
	case astdebt.ExtC, astdebt.ExtH, astdebt.ExtCPP, astdebt.ExtCC, astdebt.ExtCXX, astdebt.ExtHPP:
		return cFamilyPairedTestCandidates(dir, stem)
	default:
		return nil
	}
}

func jsFamilyPairedTestCandidates(dir, stem, ext string) []string {
	testExts := []string{ext, astdebt.ExtTS, astdebt.ExtTSX, astdebt.ExtJS, astdebt.ExtJSX, astdebt.ExtMJS}
	var out []string
	seen := make(map[string]bool)
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, te := range testExts {
		if te == astdebt.ExtSvelte || te == "" {
			continue
		}
		add(filepath.Join(dir, stem+".test"+te))
		add(filepath.Join(dir, stem+".spec"+te))
		add(filepath.Join(dir, "__tests__", stem+".test"+te))
		add(filepath.Join(dir, "__tests__", stem+".spec"+te))
		add(filepath.Join(astdebt.DirTests, stem+".test"+te))
		add(filepath.Join(astdebt.DirTest, stem+".test"+te))
		add(filepath.Join(".tmp", astdebt.DirTests, stem+".test"+te))
		add(filepath.Join(".tmp", astdebt.DirTests, stem+".spec"+te))
	}
	return out
}

func jvmPairedTestCandidates(rel, dir, stem, langDir, ext string, suffixes []string) []string {
	var out []string
	for _, sfx := range suffixes {
		out = append(out, filepath.Join(dir, stem+sfx+ext))
	}
	slashRel := filepath.ToSlash(rel)
	mainMarker := "src/main/" + langDir + "/"
	testMarker := "src/" + astdebt.DirTest + "/" + langDir + "/"
	if idx := strings.Index(slashRel, mainMarker); idx >= 0 {
		prefix := slashRel[:idx]
		pkgSub := filepath.Dir(slashRel[idx+len(mainMarker):])
		for _, sfx := range suffixes {
			out = append(out, filepath.FromSlash(prefix+testMarker+filepath.ToSlash(filepath.Join(pkgSub, stem+sfx+ext))))
		}
	}
	return out
}

func cFamilyPairedTestCandidates(dir, stem string) []string {
	exts := []string{astdebt.ExtC, astdebt.ExtCPP, astdebt.ExtCC, astdebt.ExtCXX}
	dirs := []string{dir, astdebt.DirTests, astdebt.DirTest}
	var out []string
	for _, d := range dirs {
		for _, e := range exts {
			out = append(out, filepath.Join(d, stem+"_test"+e), filepath.Join(d, astdebt.PrefixPyTest+stem+e))
		}
	}
	return out
}

func searchNestedLanguageTest(root, stem, ext string) string {
	targetNames := nestedLanguageTestNames(stem, ext)
	if len(targetNames) == 0 {
		return ""
	}
	if match, ok := tryGitIndexedNestedTest(root, targetNames); ok {
		return match
	}
	found := ""
	if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d == nil {
			return nil
		}
		if d.IsDir() {
			if IsSkippableSecurityDir(d.Name()) && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if targetNames[d.Name()] {
			found = d.Name()
			return fs.SkipAll
		}
		return nil
	}); err != nil {
		return found
	}
	return found
}

func tryGitIndexedNestedTest(root string, targetNames map[string]bool) (string, bool) {
	if _, err := workspace.FindWorkingTreeRoot(root); err != nil {
		return "", false
	}
	out, err := workspace.SafeGitCommand(
		root, "ls-files", "-z", "--cached", "--others", "--exclude-standard",
	).Output()
	if err != nil {
		return "", false
	}
	for _, raw := range bytes.Split(out, []byte{0}) {
		rel := filepath.ToSlash(strings.TrimSpace(string(raw)))
		if rel == "" || hasSkippableImpactSegment(rel) {
			continue
		}
		base := filepath.Base(rel)
		if targetNames[base] {
			return base, true
		}
	}
	return "", true
}

func nestedLanguageTestNames(stem, ext string) map[string]bool {
	switch ext {
	case astdebt.ExtCS:
		return map[string]bool{stem + "Tests.cs": true, stem + "Test.cs": true}
	case astdebt.ExtJava:
		return map[string]bool{stem + "Test.java": true, stem + "Tests.java": true}
	case astdebt.ExtKT, astdebt.ExtKTS:
		return map[string]bool{stem + "Test.kt": true, stem + "Tests.kt": true, stem + "Spec.kt": true}
	default:
		return nil
	}
}
