// Package xray implements polyglot import graph construction across source modules.
package xray

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/impact"
)

var (
	rustModDeclRe = regexp.MustCompile(`(?m)^\s*(?:pub\s+)?mod\s+([A-Za-z0-9_]+)\s*;`)
	sourceExtsTry = []string{
		"", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".svelte",
		".py", ".rs", ".rb", ".go", ".h", ".hpp", ".c", ".cpp", ".cc",
		"/index.ts", "/index.tsx", "/index.js", "/index.mjs", "/mod.ts",
		"/__init__.py", "/mod.rs", "/lib.rs",
	}
)

type topologyIndexMaps struct {
	fileSet    map[string]bool
	dirToFiles map[string][]string
	goPkgDirs  map[string]string
	goAnchor   map[string]string
	stemIndex  map[string][]string
}

func indexGoModulePackages(absRoot string, dirToFiles map[string][]string) map[string]string {
	pkgMap := make(map[string]string)
	for dir, dirFiles := range dirToFiles {
		if !hasGoFile(dirFiles) {
			continue
		}
		pkgMap[dir] = dir
		modDir, modName := findNearestGoMod(absRoot, dir)
		if modName == "" {
			continue
		}
		sub := strings.TrimPrefix(strings.TrimPrefix(dir, modDir), "/")
		if sub == "" || dir == "." {
			pkgMap[modName] = dir
			continue
		}
		pkgMap[modName+"/"+sub] = dir
	}
	return pkgMap
}

func findNearestGoMod(absRoot, relDir string) (string, string) {
	for _, cand := range ancestorDirs(relDir) {
		if modName := readModuleDirective(filepath.Join(absRoot, filepath.FromSlash(cand), "go.mod")); modName != "" {
			if cand == "." {
				return "", modName
			}
			return cand, modName
		}
	}
	return "", ""
}

func ancestorDirs(relDir string) []string {
	if relDir == "." || relDir == "" {
		return []string{"."}
	}
	var dirs []string
	for cur := relDir; cur != "." && cur != ""; cur = path.Dir(cur) {
		dirs = append(dirs, cur)
		if !strings.Contains(cur, "/") {
			break
		}
	}
	return append(dirs, ".")
}

func readModuleDirective(modPath string) string {
	raw, err := os.ReadFile(modPath)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) >= 2 && fields[0] == "module" {
			return fields[1]
		}
	}
	return ""
}

func hasGoFile(files []string) bool {
	for _, f := range files {
		if strings.HasSuffix(f, ".go") {
			return true
		}
	}
	return false
}

func buildPolyglotAdjacency(in topologyBuildInput, files []string, idx *topologyIndexMaps) map[string][]string {
	adj := make(map[string][]string, len(files))
	for _, rel := range files {
		neighbors := collectFileEdges(in, rel, idx)
		if len(neighbors) > 0 {
			adj[rel] = neighbors
		}
	}
	return adj
}

func collectFileEdges(in topologyBuildInput, rel string, idx *topologyIndexMaps) []string {
	edgeSet := make(map[string]bool)
	full := filepath.Join(in.absRoot, filepath.FromSlash(rel))
	rawImports := impact.ExtractPolyglotFileImports(full)
	if strings.HasSuffix(rel, ".rs") {
		rawImports = appendRustModImports(rawImports, in.nonTestSources[rel])
	}
	for _, imp := range rawImports {
		for _, target := range resolveImportTargets(rel, imp, idx) {
			if target != rel {
				edgeSet[target] = true
			}
		}
	}
	addSamePackageAndHeaderPeers(rel, idx, edgeSet)
	if len(edgeSet) == 0 {
		return nil
	}
	out := make([]string, 0, len(edgeSet))
	for nb := range edgeSet {
		out = append(out, nb)
	}
	sort.Strings(out)
	return out
}

func appendRustModImports(imports []string, src []byte) []string {
	for _, m := range rustModDeclRe.FindAllStringSubmatch(string(src), -1) {
		if len(m) > 1 {
			imports = append(imports, "./"+m[1])
		}
	}
	return imports
}

func selectGoPackageAnchors(dirToFiles map[string][]string, hotspots map[string]*XrayHotspot) map[string]string {
	anchors := make(map[string]string)
	for dir, list := range dirToFiles {
		if best := pickAnchorInDir(list, hotspots); best != "" {
			anchors[dir] = best
		}
	}
	return anchors
}

func pickAnchorInDir(list []string, hotspots map[string]*XrayHotspot) string {
	best, bestLOC := "", -1
	for _, f := range list {
		if !strings.HasSuffix(f, ".go") {
			continue
		}
		base := path.Base(f)
		if base == "main.go" || base == "doc.go" {
			return f
		}
		loc := hotspotLOC(hotspots[f])
		if loc > bestLOC {
			bestLOC = loc
			best = f
		}
	}
	return best
}

func hotspotLOC(hs *XrayHotspot) int {
	if hs == nil {
		return 0
	}
	return hs.LOC
}

func addSamePackageAndHeaderPeers(rel string, idx *topologyIndexMaps, edgeSet map[string]bool) {
	dir := path.Dir(rel)
	if strings.HasSuffix(rel, ".go") && idx.goAnchor[dir] == rel {
		for _, sib := range idx.dirToFiles[dir] {
			if sib != rel && strings.HasSuffix(sib, ".go") {
				edgeSet[sib] = true
			}
		}
	}
	ext := strings.ToLower(path.Ext(rel))
	if ext != ".h" && ext != ".hpp" {
		return
	}
	stem := strings.TrimSuffix(rel, path.Ext(rel))
	for _, implExt := range []string{".c", ".cpp", ".cc", ".cxx"} {
		if idx.fileSet[stem+implExt] {
			edgeSet[stem+implExt] = true
		}
	}
}

func resolveImportTargets(relFile, rawImp string, idx *topologyIndexMaps) []string {
	if strings.HasSuffix(relFile, ".go") {
		targetDir, ok := idx.goPkgDirs[rawImp]
		if ok && idx.goAnchor[targetDir] != "" {
			return []string{idx.goAnchor[targetDir]}
		}
		return nil
	}
	out := matchPathCandidates(buildModulePathCandidates(relFile, rawImp), idx.fileSet)
	if len(out) == 0 && isManagedDotLanguage(relFile) {
		lastSeg := rawImp[strings.LastIndex(rawImp, ".")+1:]
		return append([]string(nil), idx.stemIndex[lastSeg]...)
	}
	return out
}

func matchPathCandidates(cands []string, fileSet map[string]bool) []string {
	seen := make(map[string]bool)
	var out []string
	for _, base := range cands {
		for _, suf := range sourceExtsTry {
			probe := strings.TrimPrefix(path.Clean(base+suf), "./")
			if fileSet[probe] && !seen[probe] {
				seen[probe] = true
				out = append(out, probe)
			}
		}
	}
	return out
}

func isManagedDotLanguage(relFile string) bool {
	return strings.HasSuffix(relFile, ".java") || strings.HasSuffix(relFile, ".kt") || strings.HasSuffix(relFile, ".cs")
}

func buildModulePathCandidates(relFile, rawImp string) []string {
	trimmed := strings.TrimSpace(rawImp)
	if trimmed == "" {
		return nil
	}
	relDir := path.Dir(relFile)
	srcPre := findEnclosingSegmentPrefix(relDir, "src")
	if strings.HasPrefix(trimmed, ".") {
		joined := path.Clean(path.Join(relDir, trimmed))
		return []string{joined, stripTypeScriptJSExt(joined)}
	}
	if aliasCands, ok := resolveAliasCandidates(trimmed, srcPre); ok {
		return aliasCands
	}
	return resolveBareSpecifierCandidates(relDir, trimmed, srcPre)
}

func resolveAliasCandidates(trimmed, srcPre string) ([]string, bool) {
	if strings.HasPrefix(trimmed, "$lib/") {
		rest := strings.TrimPrefix(trimmed, "$lib/")
		out := []string{path.Join("src/lib", rest), path.Join("lib", rest)}
		if srcPre != "" {
			p := path.Join(srcPre, "lib", rest)
			out = append([]string{p, stripTypeScriptJSExt(p)}, out...)
		}
		return out, true
	}
	if strings.HasPrefix(trimmed, "@/") || strings.HasPrefix(trimmed, "~/") {
		rest := trimmed[2:]
		out := []string{path.Join("src", rest), rest}
		if srcPre != "" {
			p := path.Join(srcPre, rest)
			out = append([]string{p, stripTypeScriptJSExt(p)}, out...)
		}
		return out, true
	}
	return nil, false
}

func resolveBareSpecifierCandidates(relDir, trimmed, srcPre string) []string {
	slashed := strings.TrimPrefix(strings.ReplaceAll(strings.ReplaceAll(trimmed, "crate::", ""), "::", "/"), "/")
	dotted := strings.ReplaceAll(slashed, ".", "/")
	out := []string{slashed, dotted, path.Join("src", slashed), path.Join("src", dotted), path.Join("lib", slashed)}
	if relDir != "." && relDir != "" {
		out = append([]string{path.Join(relDir, slashed), path.Join(relDir, dotted)}, out...)
	}
	if srcPre != "" {
		out = append(out, path.Join(srcPre, slashed), path.Join(srcPre, dotted))
	}
	return out
}

func stripTypeScriptJSExt(p string) string {
	for _, ext := range []string{".js", ".mjs", ".cjs"} {
		if strings.HasSuffix(p, ext) {
			return strings.TrimSuffix(p, ext)
		}
	}
	return p
}

func findEnclosingSegmentPrefix(relDir, targetSeg string) string {
	segs := strings.Split(relDir, "/")
	for i := len(segs) - 1; i >= 0; i-- {
		if segs[i] == targetSeg {
			return strings.Join(segs[:i+1], "/")
		}
	}
	return ""
}
