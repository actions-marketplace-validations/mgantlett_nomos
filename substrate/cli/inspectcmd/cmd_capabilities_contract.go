// Package inspectcmd resolves specification contract slices, TC-* obligations, and SHA-256 digests.
// @intent software_physics_engine_spec.md #ContractSlice
package inspectcmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/config"
)

const (
	contractPlaneIntent  = "intent"
	contractTypeLabel    = "contract"
	contractStatusGround = "GROUNDED"
	contractStatusDecl   = "DECLARED"
	maxContractBlockLen  = 1600
	maxObligationsSlice  = 12
)

var (
	tcQueryTokenRe  = regexp.MustCompile(`^TC-[A-Z0-9]+-[0-9]+$`)
	errQueryTokenRe = regexp.MustCompile(`^ERR_[A-Z0-9_]+$`)
	tcRowExtractRe  = regexp.MustCompile(`\b(TC-[A-Z0-9]+-[0-9]+)\b`)
	codeFenceSpanRe = regexp.MustCompile("(?s)```([a-zA-Z0-9_-]*)\\s*\\n(.*?)```")
)

// ContractSlice models a resolved specification contract, TC-* obligations, and reverse AST bindings (#ContractSlice).
type ContractSlice struct {
	Target         string   `json:"target"`
	SpecFile       string   `json:"spec_file"`
	SpecSHA256     string   `json:"spec_sha256"`
	SectionHeading string   `json:"section_heading,omitempty"`
	ContractBlock  string   `json:"contract_block,omitempty"`
	Obligations    []string `json:"obligations,omitempty"`
	SourceFiles    []string `json:"source_files,omitempty"`
	VerifyingTests []string `json:"verifying_tests,omitempty"`
}

// specDocument holds a loaded specification Markdown file and its SHA-256 digest.
type specDocument struct {
	RelPath string
	Base    string
	Content string
	SHA256  string
}

// ResolveContractCapabilityDetail resolves query as an intent contract, TC-* obligation, ERR_* code, or spec file.
// @intent software_physics_engine_spec.md #CapabilityDetail
func ResolveContractCapabilityDetail(repoRoot, query string) *CapabilityDetail {
	slice := ResolveContractSlice(repoRoot, query)
	if slice == nil {
		return nil
	}
	status := contractStatusDecl
	if len(slice.SourceFiles) > 0 || len(slice.VerifyingTests) > 0 {
		status = contractStatusGround
	}
	desc := fmt.Sprintf("Specification contract in %s", slice.SpecFile)
	if slice.SectionHeading != "" {
		desc = fmt.Sprintf("%s (%s)", slice.SectionHeading, slice.SpecFile)
	}
	return &CapabilityDetail{
		ID:          slice.Target,
		Name:        strings.TrimPrefix(slice.Target, "#"),
		Type:        contractTypeLabel,
		Plane:       contractPlaneIntent,
		Status:      status,
		Law:         4,
		Ring:        "syntactic",
		Domain:      "Contracts & Schemas",
		Invocation:  fmt.Sprintf("nomos capabilities %q", slice.Target),
		Description: desc,
		Contract:    slice,
	}
}

// ResolveContractSlice locates and slices a specification contract, TC-* row set, and reverse AST bindings for query.
// @intent software_physics_engine_spec.md #ContractSlice
func ResolveContractSlice(repoRoot, query string) *ContractSlice {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" || strings.TrimSpace(repoRoot) == "" {
		return nil
	}
	docs := loadRepoSpecDocuments(repoRoot)
	if len(docs) == 0 {
		return nil
	}
	upper := strings.ToUpper(trimmed)
	if tcQueryTokenRe.MatchString(upper) {
		return resolveTCObligationSlice(repoRoot, docs, upper)
	}
	if errQueryTokenRe.MatchString(upper) {
		return resolveErrCodeSlice(repoRoot, docs, upper)
	}
	if strings.HasSuffix(strings.ToLower(trimmed), ".md") {
		return resolveSpecFileSlice(repoRoot, docs, trimmed)
	}
	return resolveSymbolAnchorSlice(repoRoot, docs, trimmed)
}

// loadRepoSpecDocuments discovers and hashes Markdown specification files configured for repoRoot.
func loadRepoSpecDocuments(repoRoot string) []specDocument {
	roots := []string{"intent/intent.md", "intent/architecture/specs"}
	if cfg, err := config.LoadConfig(filepath.Join(repoRoot, "nomos.yaml")); err == nil && cfg != nil {
		cRoots := cfg.Governance.QualityGates.GetContractsBoundary().Contracts.SpecRoots
		if len(cRoots) > 0 {
			roots = cRoots
		}
	}
	var paths []string
	seen := make(map[string]bool)
	for _, r := range roots {
		collectMarkdownPaths(filepath.Join(repoRoot, filepath.FromSlash(r)), seen, &paths)
	}
	sort.Strings(paths)
	out := make([]specDocument, 0, len(paths))
	for _, abs := range paths {
		if doc, ok := readSpecDocument(repoRoot, abs); ok {
			out = append(out, doc)
		}
	}
	return out
}

// readSpecDocument loads one Markdown file and stamps its SHA-256 hex digest.
func readSpecDocument(repoRoot, abs string) (specDocument, bool) {
	data, err := os.ReadFile(abs)
	if err != nil {
		return specDocument{}, false
	}
	sum := sha256.Sum256(data)
	relSlash := toRepoRelSlash(repoRoot, abs)
	return specDocument{
		RelPath: relSlash,
		Base:    filepath.Base(relSlash),
		Content: string(data),
		SHA256:  hex.EncodeToString(sum[:]),
	}, true
}

// collectMarkdownPaths appends unique Markdown file paths beneath target into out.
func collectMarkdownPaths(target string, seen map[string]bool, out *[]string) {
	info, err := os.Stat(target)
	if err != nil || info == nil {
		return
	}
	if !info.IsDir() {
		if strings.HasSuffix(strings.ToLower(target), ".md") && !seen[target] {
			seen[target] = true
			*out = append(*out, target)
		}
		return
	}
	if walkErr := filepath.WalkDir(target, func(path string, d os.DirEntry, wErr error) error {
		if wErr != nil || d == nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(d.Name()), ".md") && !seen[path] {
			seen[path] = true
			*out = append(*out, path)
		}
		return nil
	}); walkErr != nil {
		return
	}
}

// resolveTCObligationSlice resolves a TC-* compliance vector across specification tables and verifying tests.
func resolveTCObligationSlice(repoRoot string, docs []specDocument, tcID string) *ContractSlice {
	for _, doc := range docs {
		if !strings.Contains(doc.Content, tcID) {
			continue
		}
		rows := extractMatchingTableRows(doc.Content, tcID)
		sources, tests := scanReverseWorkspaceBindings(repoRoot, doc.Base, "", []string{tcID})
		return &ContractSlice{
			Target:         tcID,
			SpecFile:       doc.RelPath,
			SpecSHA256:     doc.SHA256,
			SectionHeading: findNearestHeading(doc.Content, tcID),
			ContractBlock:  clampBlock(extractLineContextBlock(doc.Content, tcID)),
			Obligations:    rows,
			SourceFiles:    sources,
			VerifyingTests: tests,
		}
	}
	return nil
}

// resolveErrCodeSlice resolves a canonical ERR_* error code across specs, source definitions, and tests.
func resolveErrCodeSlice(repoRoot string, docs []specDocument, errCode string) *ContractSlice {
	for _, doc := range docs {
		if !strings.Contains(doc.Content, errCode) {
			continue
		}
		rows := extractMatchingTableRows(doc.Content, errCode)
		sources, tests := scanReverseWorkspaceBindings(repoRoot, doc.Base, errCode, extractTCIDsFromRows(rows))
		return &ContractSlice{
			Target:         errCode,
			SpecFile:       doc.RelPath,
			SpecSHA256:     doc.SHA256,
			SectionHeading: findNearestHeading(doc.Content, errCode),
			ContractBlock:  clampBlock(extractLineContextBlock(doc.Content, errCode)),
			Obligations:    rows,
			SourceFiles:    sources,
			VerifyingTests: tests,
		}
	}
	return nil
}

// resolveSpecFileSlice resolves a specification Markdown document summary by filename or suffix.
func resolveSpecFileSlice(repoRoot string, docs []specDocument, rawPath string) *ContractSlice {
	norm := strings.ToLower(filepath.ToSlash(strings.TrimSpace(rawPath)))
	for _, doc := range docs {
		if strings.ToLower(doc.Base) != norm && !strings.HasSuffix(strings.ToLower(doc.RelPath), norm) {
			continue
		}
		rows := extractMatchingTableRows(doc.Content, "TC-")
		sources, tests := scanReverseWorkspaceBindings(repoRoot, doc.Base, "", extractTCIDsFromRows(rows))
		return &ContractSlice{
			Target:         doc.Base,
			SpecFile:       doc.RelPath,
			SpecSHA256:     doc.SHA256,
			SectionHeading: extractTopDocumentHeading(doc.Content),
			ContractBlock:  clampBlock(extractFirstCodeFence(doc.Content)),
			Obligations:    rows,
			SourceFiles:    sources,
			VerifyingTests: tests,
		}
	}
	return nil
}

// resolveSymbolAnchorSlice resolves a #TypeName or TypeName contract declaration across specification code blocks.
func resolveSymbolAnchorSlice(repoRoot string, docs []specDocument, rawSymbol string) *ContractSlice {
	sym := strings.TrimPrefix(strings.TrimSpace(rawSymbol), "#")
	if sym == "" {
		return nil
	}
	anchorTok := "#" + sym
	for _, doc := range docs {
		block, needle := findSymbolContractInDoc(doc.Content, sym, anchorTok)
		if needle == "" {
			continue
		}
		rows := extractMatchingTableRows(doc.Content, sym)
		sources, tests := scanReverseWorkspaceBindings(repoRoot, doc.Base, sym, extractTCIDsFromRows(rows))
		return &ContractSlice{
			Target:         anchorTok,
			SpecFile:       doc.RelPath,
			SpecSHA256:     doc.SHA256,
			SectionHeading: findNearestHeading(doc.Content, needle),
			ContractBlock:  clampBlock(block),
			Obligations:    rows,
			SourceFiles:    sources,
			VerifyingTests: tests,
		}
	}
	return nil
}

// findSymbolContractInDoc locates a fenced type/function snippet or anchor context for sym inside markdown.
func findSymbolContractInDoc(markdown, sym, anchorTok string) (string, string) {
	for _, m := range codeFenceSpanRe.FindAllStringSubmatch(markdown, -1) {
		if len(m) >= 3 {
			if snippet := extractSymbolSnippetFromFence(m[2], sym, anchorTok); snippet != "" {
				return snippet, sym
			}
		}
	}
	if strings.Contains(markdown, anchorTok) {
		return extractLineContextBlock(markdown, anchorTok), anchorTok
	}
	return "", ""
}

// extractSymbolSnippetFromFence isolates the declaration paragraph mentioning sym or anchorTok inside a fenced block.
func extractSymbolSnippetFromFence(fenceBody, sym, anchorTok string) string {
	for _, p := range []string{"type " + sym + " ", "interface " + sym + " ", "class " + sym + " ", "func " + sym + "(", anchorTok} {
		if strings.Contains(fenceBody, p) {
			for _, ch := range strings.Split(strings.TrimSpace(fenceBody), "\n\n") {
				if strings.Contains(ch, sym) {
					return strings.TrimSpace(ch)
				}
			}
			return strings.TrimSpace(fenceBody)
		}
	}
	return ""
}
