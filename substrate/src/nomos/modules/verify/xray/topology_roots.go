// Package xray implements polyglot entrypoint classification and test-suite reachability.
package xray

import (
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/impact"
)

var (
	goPackageMainRe = regexp.MustCompile(`(?m)^\s*package\s+main\b`)
	cMainFuncRe     = regexp.MustCompile(`(?m)\b(?:public\s+static\s+void\s+main|static\s+(?:async\s+)?(?:Task(?:<int>)?|void|int)\s+Main|fun\s+main|int\s+main)\s*\(`)
	entrypointDirs  = map[string]bool{"routes": true, "pages": true, "cmd": true, "bin": true, "scripts": true}
	jsEntryStems    = map[string]bool{"index": true, "mod": true, "main": true, "cli": true, "server": true, "app": true}
	pyEntryBases    = map[string]bool{
		"__main__.py": true, "main.py": true, "app.py": true, "cli.py": true,
		"manage.py": true, "wsgi.py": true, "asgi.py": true, "setup.py": true,
	}
	rustEntryBases = map[string]bool{"main.rs": true, "lib.rs": true, "build.rs": true}
	webHookBases   = map[string]bool{"service-worker.ts": true, "service-worker.js": true, "middleware.ts": true}
	extLangMap     = map[string]string{".go": "go", ".svelte": "svelte", ".py": "python", ".rs": "rust"}
)

func buildTopologyGraphState(in topologyBuildInput) *topologyGraphState {
	files, idx := buildTopologyIndexes(in)
	adj := buildPolyglotAdjacency(in, files, idx)
	inDeg, outDeg := computeGraphDegrees(files, adj)
	manifestEntries := discoverManifestEntrypoints(in.absRoot, idx.fileSet)
	prodRoots := discoverProductionRoots(in, files, idx.dirToFiles, inDeg, manifestEntries)
	auxReach := computeTestSuiteReachability(in, idx, adj)

	return &topologyGraphState{
		files:        files,
		fileSet:      idx.fileSet,
		adj:          adj,
		inDegree:     inDeg,
		outDegree:    outDeg,
		prodRoots:    prodRoots,
		auxTestReach: auxReach,
	}
}

func buildTopologyIndexes(in topologyBuildInput) ([]string, *topologyIndexMaps) {
	files := make([]string, 0, len(in.nonTestSources))
	fileSet := make(map[string]bool, len(in.nonTestSources))
	dirToFiles := make(map[string][]string)
	stemIndex := make(map[string][]string)

	for rel := range in.nonTestSources {
		files = append(files, rel)
		fileSet[rel] = true
		d := path.Dir(rel)
		dirToFiles[d] = append(dirToFiles[d], rel)
		baseStem := strings.TrimSuffix(path.Base(rel), path.Ext(rel))
		stemIndex[baseStem] = append(stemIndex[baseStem], rel)
	}
	sort.Strings(files)
	for d := range dirToFiles {
		sort.Strings(dirToFiles[d])
	}
	idx := &topologyIndexMaps{
		fileSet:    fileSet,
		dirToFiles: dirToFiles,
		goPkgDirs:  indexGoModulePackages(in.absRoot, dirToFiles),
		goAnchor:   selectGoPackageAnchors(dirToFiles, in.hotspotsByFile),
		stemIndex:  stemIndex,
	}
	return files, idx
}

func computeGraphDegrees(files []string, adj map[string][]string) (map[string]int, map[string]int) {
	inDeg := make(map[string]int, len(files))
	outDeg := make(map[string]int, len(files))
	for _, u := range files {
		outDeg[u] = len(adj[u])
		for _, v := range adj[u] {
			inDeg[v]++
		}
	}
	return inDeg, outDeg
}

func discoverProductionRoots(
	in topologyBuildInput,
	files []string,
	dirToFiles map[string][]string,
	inDeg map[string]int,
	manifestRoots map[string]bool,
) []string {
	explicitByEco := make(map[string][]string)
	filesByEco := make(map[string][]string)
	rootSet := make(map[string]bool)

	for _, f := range files {
		lang := languageForTopologyFile(f, in.hotspotsByFile)
		eco := moduleEcosystemForLanguage(lang)
		filesByEco[eco] = append(filesByEco[eco], f)
		if manifestRoots[f] || isExplicitLanguageEntrypoint(f, lang, in.nonTestSources[f]) {
			explicitByEco[eco] = append(explicitByEco[eco], f)
			rootSet[f] = true
		}
	}
	for eco, ecoFiles := range filesByEco {
		if len(explicitByEco[eco]) > 0 {
			addRootGoExports(eco, dirToFiles, rootSet)
			continue
		}
		for _, f := range selectPureLibraryRoots(eco, ecoFiles, inDeg) {
			rootSet[f] = true
		}
	}
	roots := make([]string, 0, len(rootSet))
	for f := range rootSet {
		roots = append(roots, f)
	}
	sort.Strings(roots)
	return roots
}

func moduleEcosystemForLanguage(lang string) string {
	switch lang {
	case "typescript", "svelte":
		return "web"
	case "c", "cpp":
		return "c_cpp"
	case "java", "kotlin":
		return "jvm"
	default:
		return lang
	}
}

func addRootGoExports(lang string, dirToFiles map[string][]string, rootSet map[string]bool) {
	if lang != "go" {
		return
	}
	for _, f := range dirToFiles["."] {
		if strings.HasSuffix(f, ".go") {
			rootSet[f] = true
		}
	}
}

func selectPureLibraryRoots(lang string, langFiles []string, inDeg map[string]int) []string {
	var publicZeroIn []string
	minSlash := 999
	for _, f := range langFiles {
		if containsInternalSegment(f) {
			continue
		}
		slashes := strings.Count(f, "/")
		if slashes < minSlash {
			minSlash = slashes
		}
		if inDeg[f] == 0 {
			publicZeroIn = append(publicZeroIn, f)
		}
	}
	if lang == "go" && len(publicZeroIn) > 0 {
		return publicZeroIn
	}
	return filterShallowPublicFiles(langFiles, minSlash)
}

func filterShallowPublicFiles(langFiles []string, minSlash int) []string {
	var shallow []string
	for _, f := range langFiles {
		if !containsInternalSegment(f) && strings.Count(f, "/") == minSlash {
			shallow = append(shallow, f)
		}
	}
	if len(shallow) > 0 {
		return shallow
	}
	return langFiles[:min(1, len(langFiles))]
}

func containsInternalSegment(relSlash string) bool {
	for _, seg := range strings.Split(path.Dir(relSlash), "/") {
		if seg == "internal" || seg == "_internal" || seg == "private" {
			return true
		}
	}
	return false
}

func isExplicitLanguageEntrypoint(relSlash, lang string, src []byte) bool {
	if hasEntrypointDirOrWebConvention(relSlash) {
		return true
	}
	return matchesLanguageEntrypointRule(relSlash, lang, src)
}

func hasEntrypointDirOrWebConvention(relSlash string) bool {
	for _, seg := range strings.Split(path.Dir(relSlash), "/") {
		if entrypointDirs[seg] {
			return true
		}
	}
	base := path.Base(relSlash)
	lowerBase := strings.ToLower(base)
	if strings.HasPrefix(base, "+") || strings.Contains(lowerBase, ".config.") || webHookBases[lowerBase] {
		return true
	}
	return strings.HasPrefix(lowerBase, "hooks.server.") || strings.HasPrefix(lowerBase, "hooks.client.")
}

func matchesLanguageEntrypointRule(relSlash, lang string, src []byte) bool {
	lowerBase := strings.ToLower(path.Base(relSlash))
	stem := strings.TrimSuffix(lowerBase, path.Ext(lowerBase))
	switch lang {
	case "go":
		return goPackageMainRe.Match(src)
	case "typescript", "svelte":
		return strings.Count(relSlash, "/") <= 1 && jsEntryStems[stem]
	case "python":
		return pyEntryBases[lowerBase] || (lowerBase == "__init__.py" && strings.Count(relSlash, "/") <= 2)
	case "rust":
		return rustEntryBases[lowerBase]
	default:
		return stem == "main" || stem == "program" || cMainFuncRe.Match(src)
	}
}

func languageForTopologyFile(relSlash string, hotspots map[string]*XrayHotspot) string {
	if hs := hotspots[relSlash]; hs != nil && hs.Language != "" {
		return hs.Language
	}
	if mapped, ok := extLangMap[strings.ToLower(path.Ext(relSlash))]; ok {
		return mapped
	}
	return "typescript"
}

func computeTestSuiteReachability(in topologyBuildInput, idx *topologyIndexMaps, adj map[string][]string) map[string]bool {
	reached := make(map[string]bool)
	var queue []string
	enqueue := func(target string) {
		if idx.fileSet[target] && !reached[target] {
			reached[target] = true
			queue = append(queue, target)
		}
	}
	for testRel := range in.testFiles {
		seedSingleTestReachability(in.absRoot, testRel, idx, enqueue)
	}
	for head := 0; head < len(queue); head++ {
		for _, nb := range adj[queue[head]] {
			enqueue(nb)
		}
	}
	return reached
}

func seedSingleTestReachability(absRoot, testRel string, idx *topologyIndexMaps, enqueue func(string)) {
	if strings.HasSuffix(testRel, "_test.go") {
		for _, sib := range idx.dirToFiles[path.Dir(testRel)] {
			if strings.HasSuffix(sib, ".go") {
				enqueue(sib)
			}
		}
	}
	full := filepath.Join(absRoot, filepath.FromSlash(testRel))
	for _, imp := range impact.ExtractPolyglotFileImports(full) {
		for _, target := range resolveImportTargets(testRel, imp, idx) {
			enqueue(target)
		}
	}
}
