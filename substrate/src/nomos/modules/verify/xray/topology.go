// Package xray implements polyglot dependency spine, branch, and unreached file cartography.
package xray

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

const (
	maxSampledEntrypoints = 16
	maxSampledBranches    = 120
	maxSampledDust        = 120
)

type topologyBuildInput struct {
	absRoot        string
	nonTestSources map[string][]byte
	hotspotsByFile map[string]*XrayHotspot
	testFiles      map[string][]byte
}

type topologyGraphState struct {
	files        []string
	fileSet      map[string]bool
	adj          map[string][]string
	inDegree     map[string]int
	outDegree    map[string]int
	prodRoots    []string
	auxTestReach map[string]bool
}

type spineWalker struct {
	adj      map[string][]string
	depthMap map[string]int
	memoLen  map[string]int
	memoNext map[string]string
	visiting map[string]bool
}

// EvaluateUnreachedFilesGate scans rootDir and returns violations when unreached files exceed baselineUnreached (@intent tree_sitter_ast_gate_spec.md #XrayTopologyGraph).
func EvaluateUnreachedFilesGate(rootDir string, baselineUnreached int) ([]string, error) {
	rep, err := ScanRepository(rootDir, DefaultScanOptions())
	if err != nil {
		return nil, err
	}
	ceil := max(baselineUnreached, 0)
	if rep.UnreachedFiles <= ceil {
		return nil, nil
	}
	return formatUnreachedViolations(rep, ceil), nil
}

func formatUnreachedViolations(rep *XrayReport, ceil int) []string {
	var violations []string
	if rep.Topology != nil {
		for _, d := range rep.Topology.Dust {
			violations = append(violations, fmt.Sprintf("%s: unreached source file (0 incoming imports or test callers)", d.File))
		}
	}
	if len(violations) == 0 {
		violations = append(violations, fmt.Sprintf("unreached source files (%d) exceed baseline ceiling (%d)", rep.UnreachedFiles, ceil))
	}
	sort.Strings(violations)
	return violations
}

func applyTopologyMetrics(absRoot string, acc *scanAccumulator) {
	if len(acc.nonTestSources) == 0 {
		acc.report.Topology = &XrayTopologyGraph{}
		return
	}
	in := topologyBuildInput{
		absRoot:        absRoot,
		nonTestSources: acc.nonTestSources,
		hotspotsByFile: acc.hotspotsByFile,
		testFiles:      acc.testSources,
	}
	st := buildTopologyGraphState(in)
	depthMap, parentMap := traverseProductionReachability(st)
	spine := extractCriticalSpine(st, depthMap)
	spineSet := buildFileLookupSet(spine)
	branches, dust, reachableCount, unreachedCount := classifyBranchesAndDust(in, st, depthMap, parentMap, spineSet)

	entrypoints := append([]string(nil), st.prodRoots...)
	if len(entrypoints) > maxSampledEntrypoints {
		entrypoints = entrypoints[:maxSampledEntrypoints]
	}
	acc.report.SpineDepth = len(spine)
	acc.report.ReachableFiles = reachableCount
	acc.report.UnreachedFiles = unreachedCount
	acc.report.BaselineEnvelope.ObservedUnreachedFiles = unreachedCount
	acc.report.Topology = &XrayTopologyGraph{
		Entrypoints: entrypoints,
		Spine:       spine,
		Branches:    branches,
		Dust:        dust,
	}
}

func buildFileLookupSet(nodes []XrayTopologyNode) map[string]bool {
	out := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		out[n.File] = true
	}
	return out
}

func traverseProductionReachability(st *topologyGraphState) (map[string]int, map[string]string) {
	depthMap := make(map[string]int, len(st.files))
	parentMap := make(map[string]string, len(st.files))
	queue := make([]string, 0, len(st.prodRoots))
	for _, r := range st.prodRoots {
		if _, seen := depthMap[r]; !seen {
			depthMap[r] = 0
			queue = append(queue, r)
		}
	}
	for head := 0; head < len(queue); head++ {
		cur := queue[head]
		nextDepth := depthMap[cur] + 1
		for _, nb := range st.adj[cur] {
			if _, seen := depthMap[nb]; !seen {
				depthMap[nb] = nextDepth
				parentMap[nb] = cur
				queue = append(queue, nb)
			}
		}
	}
	return depthMap, parentMap
}

func extractCriticalSpine(st *topologyGraphState, depthMap map[string]int) []XrayTopologyNode {
	if len(depthMap) == 0 {
		return nil
	}
	w := &spineWalker{
		adj:      st.adj,
		depthMap: depthMap,
		memoLen:  make(map[string]int, len(depthMap)),
		memoNext: make(map[string]string, len(depthMap)),
		visiting: make(map[string]bool, len(depthMap)),
	}
	bestRoot := ""
	bestTotal := 0
	for _, r := range st.prodRoots {
		cand := w.walk(r)
		if isBetterChainCandidate(cand, r, bestTotal, bestRoot) {
			bestTotal = cand
			bestRoot = r
		}
	}
	return buildSpineNodesFromRoot(bestRoot, w.memoNext, st)
}

func (w *spineWalker) walk(u string) int {
	if val, ok := w.memoLen[u]; ok {
		return val
	}
	if w.visiting[u] {
		return 0
	}
	w.visiting[u] = true
	bestLen, bestChild := 1, ""
	for _, v := range w.adj[u] {
		if w.depthMap[v] <= w.depthMap[u] {
			continue
		}
		cand := 1 + w.walk(v)
		if isBetterChainCandidate(cand, v, bestLen, bestChild) {
			bestLen = cand
			bestChild = v
		}
	}
	w.visiting[u] = false
	w.memoLen[u] = bestLen
	w.memoNext[u] = bestChild
	return bestLen
}

func isBetterChainCandidate(candLen int, candKey string, bestLen int, bestKey string) bool {
	if candLen != bestLen {
		return candLen > bestLen
	}
	return bestKey == "" || candKey < bestKey
}

func buildSpineNodesFromRoot(bestRoot string, memoNext map[string]string, st *topologyGraphState) []XrayTopologyNode {
	if bestRoot == "" {
		return nil
	}
	var out []XrayTopologyNode
	cur, prev := bestRoot, ""
	seen := make(map[string]bool)
	for depth := 0; cur != "" && !seen[cur]; depth++ {
		seen[cur] = true
		out = append(out, XrayTopologyNode{
			File:       cur,
			Label:      compactTopologyLabel(cur),
			Depth:      depth,
			ParentFile: prev,
			InDegree:   st.inDegree[cur],
			OutDegree:  st.outDegree[cur],
		})
		prev = cur
		cur = memoNext[cur]
	}
	return out
}

func classifyBranchesAndDust(
	in topologyBuildInput,
	st *topologyGraphState,
	depthMap map[string]int,
	parentMap map[string]string,
	spineSet map[string]bool,
) ([]XrayTopologyNode, []XrayTopologyNode, int, int) {
	var allBranches, allDust []XrayTopologyNode
	reachableCount, unreachedCount := 0, 0

	for _, f := range st.files {
		node := buildNodeFromHotspot(f, in.hotspotsByFile[f], st)
		if d, ok := depthMap[f]; ok {
			reachableCount++
			node.Depth = d
			node.ParentFile = parentMap[f]
			if !spineSet[f] {
				allBranches = append(allBranches, node)
			}
			continue
		}
		if st.auxTestReach[f] {
			reachableCount++
			node.Depth = 1
			allBranches = append(allBranches, node)
			continue
		}
		unreachedCount++
		node.Depth = -1
		allDust = append(allDust, node)
	}
	return capSortedNodes(allBranches, maxSampledBranches), capSortedNodes(allDust, maxSampledDust), reachableCount, unreachedCount
}

func buildNodeFromHotspot(relFile string, hs *XrayHotspot, st *topologyGraphState) XrayTopologyNode {
	node := XrayTopologyNode{
		File:      relFile,
		Label:     compactTopologyLabel(relFile),
		InDegree:  st.inDegree[relFile],
		OutDegree: st.outDegree[relFile],
	}
	if hs != nil {
		node.Language = hs.Language
		node.LOC = hs.LOC
		node.CDS = hs.CDS
	}
	return node
}

func capSortedNodes(nodes []XrayTopologyNode, limit int) []XrayTopologyNode {
	sort.Slice(nodes, func(i, j int) bool {
		mi := nodes[i].LOC + nodes[i].CDS*4 + nodes[i].InDegree*10
		mj := nodes[j].LOC + nodes[j].CDS*4 + nodes[j].InDegree*10
		if mi != mj {
			return mi > mj
		}
		return nodes[i].File < nodes[j].File
	})
	if len(nodes) > limit {
		return nodes[:limit]
	}
	return nodes
}

func compactTopologyLabel(relSlash string) string {
	base := path.Base(relSlash)
	stem := strings.TrimSuffix(base, path.Ext(base))
	if stem == "index" || stem == "main" || stem == "mod" || stem == "lib" || strings.HasPrefix(stem, "+") {
		parent := path.Base(path.Dir(relSlash))
		if parent != "." && parent != "/" && parent != "" {
			return parent + "/" + stem
		}
	}
	return stem
}
