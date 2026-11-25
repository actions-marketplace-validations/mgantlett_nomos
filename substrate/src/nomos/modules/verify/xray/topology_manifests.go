// Package xray resolves package.json manifest entrypoints across polyglot workspaces.
package xray

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var scriptPathTokRe = regexp.MustCompile(`([A-Za-z0-9_./-]+\.(?:js|mjs|cjs|ts|tsx|py|rb|go))`)

func discoverManifestEntrypoints(absRoot string, fileSet map[string]bool) map[string]bool {
	roots := make(map[string]bool)
	seenDir := map[string]bool{".": true}
	dirs := []string{"."}
	for f := range fileSet {
		for cur := path.Dir(f); cur != "." && cur != "" && !seenDir[cur]; cur = path.Dir(cur) {
			seenDir[cur] = true
			dirs = append(dirs, cur)
		}
	}
	for _, dir := range dirs {
		collectPackageJSONEntrypoints(absRoot, dir, fileSet, roots)
	}
	return roots
}

func collectPackageJSONEntrypoints(absRoot, relDir string, fileSet, roots map[string]bool) {
	pkgPath := filepath.Join(absRoot, filepath.FromSlash(relDir), "package.json")
	raw, err := os.ReadFile(pkgPath)
	if err != nil {
		return
	}
	var parsed struct {
		Main    string            `json:"main"`
		Module  string            `json:"module"`
		Types   string            `json:"types"`
		Svelte  string            `json:"svelte"`
		Bin     json.RawMessage   `json:"bin"`
		Exports json.RawMessage   `json:"exports"`
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(raw, &parsed) != nil {
		return
	}
	candidates := []string{parsed.Main, parsed.Module, parsed.Types, parsed.Svelte}
	candidates = append(candidates, extractJSONStringLeaves(parsed.Bin)...)
	candidates = append(candidates, extractJSONStringLeaves(parsed.Exports)...)
	candidates = append(candidates, extractScriptFileTokens(parsed.Scripts)...)
	for _, c := range candidates {
		resolveManifestCandidate(relDir, c, fileSet, roots)
	}
}

func extractScriptFileTokens(scripts map[string]string) []string {
	var out []string
	for _, cmd := range scripts {
		for _, m := range scriptPathTokRe.FindAllStringSubmatch(cmd, -1) {
			if len(m) > 1 {
				out = append(out, m[1])
			}
		}
	}
	return out
}

func extractJSONStringLeaves(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil && s != "" {
		return []string{s}
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) == nil {
		var out []string
		for _, v := range m {
			out = append(out, extractJSONStringLeaves(v)...)
		}
		return out
	}
	return nil
}

func resolveManifestCandidate(relDir, cand string, fileSet, roots map[string]bool) {
	clean := strings.TrimSpace(cand)
	if clean == "" {
		return
	}
	joined := clean
	if relDir != "." && relDir != "" {
		joined = path.Join(relDir, clean)
	}
	joined = strings.TrimPrefix(path.Clean(joined), "./")
	variants := []string{joined, stripTypeScriptJSExt(joined)}
	if strings.Contains(joined, "dist/") || strings.Contains(joined, "build/") {
		srcSwap := strings.Replace(strings.Replace(joined, "dist/", "src/", 1), "build/", "src/", 1)
		variants = append(variants, srcSwap, stripTypeScriptJSExt(srcSwap))
	}
	for _, v := range variants {
		for _, suf := range sourceExtsTry {
			probe := strings.TrimPrefix(path.Clean(v+suf), "./")
			if fileSet[probe] {
				roots[probe] = true
			}
		}
	}
}
