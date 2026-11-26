// Package verify implements the deterministic Definition of Done (DoD) quality gate pipeline.
package verify

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/config"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/storage"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/types"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/task"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/polygate"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/ratchet"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
)

func init() {
	RegisterGate("capbac_lease", func() vscope.Gate { return &CapBACLeaseGate{} })
	RegisterGate("release_receipt", func() vscope.Gate { return &ReleaseReceiptGate{} })
	RegisterGate("zero_filesystem_alias", func() vscope.Gate { return &ZeroFilesystemAliasGate{} })
	RegisterGate("plane_separation", func() vscope.Gate { return &PlaneSeparationGate{} })
}

// CapBACLeaseGate verifies active capability lease authorization (Cell 1.3, Law 1).
type CapBACLeaseGate struct{}

// Name returns the canonical display name.
func (g *CapBACLeaseGate) Name() string {
	return "CapBAC Task Lease Authorization Invariant"
}

// Evaluate asserts that mutations in an active task worktree possess an active capability lease.
func (g *CapBACLeaseGate) Evaluate(ctx *vscope.VerifyContext) (*vscope.GateResult, error) {
	if ctx == nil || ctx.IsBaseline || ctx.TaskID == "" || ctx.Phase == string(types.PhaseIdle) {
		return &vscope.GateResult{
			GateName: g.Name(),
			Passed:   true,
			Message:  "no active task lease required in trunk baseline or idle state",
		}, nil
	}

	dbPath := config.ResolveGlobalDBPath()
	db, err := storage.OpenDB(dbPath)
	if err != nil {
		return &vscope.GateResult{
			GateName: g.Name(),
			Passed:   false,
			Message:  fmt.Sprintf("failed opening living ledger db: %v", err),
			Remedy:   "verify SQLite database integrity at " + dbPath,
		}, nil
	}
	defer func() {
		if cErr := db.Close(); cErr != nil {
			// database closed
		}
	}()

	leaseID, phase, stepBudget, tokensUsed, lErr := storage.GetActiveLease(db, ctx.TaskID)
	if lErr != nil {
		return &vscope.GateResult{
			GateName: g.Name(),
			Passed:   false,
			Message:  fmt.Sprintf("no active CapBAC capability lease found for task %s: %v", ctx.TaskID, lErr),
			Remedy:   fmt.Sprintf("run 'nomos task start %s' to acquire an active capability lease", ctx.TaskID),
		}, nil
	}

	remaining := stepBudget - tokensUsed
	return &vscope.GateResult{
		GateName: g.Name(),
		Passed:   true,
		Message:  fmt.Sprintf("active CapBAC lease %s verified for %s [phase %s, %d steps remaining]", leaseID, ctx.TaskID, phase, remaining),
	}, nil
}

// ReleaseReceiptGate verifies release provenance and Git Notes infrastructure (Cell 3.3, Law 3).
type ReleaseReceiptGate struct{}

// Name returns the canonical display name.
func (g *ReleaseReceiptGate) Name() string {
	return "Two-Phase Commit Release Receipt Invariant"
}

// Evaluate verifies that release provenance and git notes infrastructure operate cleanly.
// @intent intent.md task_lifecycle_cli_spec.md #ReleaseNotePayload
func (g *ReleaseReceiptGate) Evaluate(ctx *vscope.VerifyContext) (*vscope.GateResult, error) {
	repoRoot := "."
	evalRoot := "."
	if ctx != nil {
		if ctx.RepoRoot != "" {
			repoRoot = ctx.RepoRoot
			evalRoot = ctx.RepoRoot
		}
		if ctx.WorktreePath != "" {
			evalRoot = ctx.WorktreePath
		}
	}

	if airlockMsg, airlockRemedy := vscope.EvaluatePreTagReleaseAirlock(ctx, evalRoot); airlockMsg != "" {
		return &vscope.GateResult{
			GateName: g.Name(),
			Passed:   false,
			Message:  airlockMsg,
			Remedy:   airlockRemedy,
		}, nil
	}

	showCmd := workspace.SafeGitCommand(repoRoot, "notes", "--ref=refs/notes/nomos/releases", "show", "HEAD")
	if rawNote, showErr := showCmd.Output(); showErr == nil && len(strings.TrimSpace(string(rawNote))) > 0 {
		var receipt struct {
			TaskKey       string `json:"task_key"`
			SHA256Receipt string `json:"sha256_receipt"`
		}
		if jErr := json.Unmarshal(rawNote, &receipt); jErr != nil || receipt.TaskKey == "" || len(receipt.SHA256Receipt) != 64 {
			return &vscope.GateResult{
				GateName: g.Name(), Passed: false,
				Message: "corrupted Git Note receipt on HEAD: missing valid task_key or 64-char sha256_receipt",
				Remedy:  "re-attest HEAD release provenance via 'nomos task sync'",
			}, nil
		}
	}

	return &vscope.GateResult{
		GateName: g.Name(),
		Passed:   true,
		Message:  "release receipt provenance verified via Git Notes (refs/notes/nomos/releases)",
	}, nil
}

func scanGoTypeAliases(root string) []string {
	subDir := filepath.Join(root, "substrate")
	if _, err := os.Stat(subDir); os.IsNotExist(err) {
		subDir = root
	}
	fset := token.NewFileSet()
	var aliases []string
	if wErr := filepath.Walk(subDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		aliases = append(aliases, extractTypeAliasesFromFile(fset, subDir, path)...)
		return nil
	}); wErr != nil {
		return aliases
	}
	return aliases
}

func extractTypeAliasesFromFile(fset *token.FileSet, subDir, path string) []string {
	node, pErr := parser.ParseFile(fset, path, nil, 0)
	if pErr != nil {
		return nil
	}
	var aliases []string
	for _, decl := range node.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			if ts, ok := spec.(*ast.TypeSpec); ok && ts.Assign.IsValid() {
				rel, rErr := filepath.Rel(subDir, path)
				if rErr != nil {
					rel = path
				}
				aliases = append(aliases, fmt.Sprintf("%s:%s", filepath.ToSlash(rel), ts.Name.Name))
			}
		}
	}
	return aliases
}

func collectGoStringLiterals(fset *token.FileSet, path string, counts map[string]map[string]int) {
	collectGoStringLiteralsFromSrc(fset, path, nil, counts)
}

func collectGoStringLiteralsFromSrc(fset *token.FileSet, path string, src any, counts map[string]map[string]int) {
	node, pErr := parser.ParseFile(fset, path, src, 0)
	if pErr != nil {
		return
	}
	pkg := node.Name.Name
	if counts[pkg] == nil {
		counts[pkg] = make(map[string]int)
	}
	ast.Inspect(node, func(n ast.Node) bool {
		if _, isImp := n.(*ast.ImportSpec); isImp {
			return false
		}
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			val := strings.Trim(lit.Value, "\"`")
			if !isIgnoredLiteral(val) {
				counts[pkg][val]++
			}
		}
		return true
	})
}

var (
	polyglotStringLiteralRe = regexp.MustCompile(`"([^"\\]{4,})"`)
	svelteScriptBlockRe     = regexp.MustCompile(`(?is)<script\b[^>]*>(.*?)</script>`)
)

func extractSvelteScriptSource(raw string) string {
	matches := svelteScriptBlockRe.FindAllStringSubmatch(raw, -1)
	if len(matches) == 0 {
		return ""
	}
	var parts []string
	for _, m := range matches {
		if len(m) > 1 {
			parts = append(parts, m[1])
		}
	}
	return strings.Join(parts, "\n")
}

func collectPolyglotStringLiterals(path string, counts map[string]map[string]int) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	collectPolyglotStringLiteralsFromBytes(path, data, counts)
}

func collectPolyglotStringLiteralsFromBytes(path string, data []byte, counts map[string]map[string]int) {
	source := string(data)
	if strings.EqualFold(filepath.Ext(path), ".svelte") {
		source = extractSvelteScriptSource(source)
	}
	pkg := filepath.Base(filepath.Dir(path))
	if counts[pkg] == nil {
		counts[pkg] = make(map[string]int)
	}
	for _, line := range strings.Split(source, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "import ") {
			continue
		}
		for _, m := range polyglotStringLiteralRe.FindAllStringSubmatch(trimmed, -1) {
			val := m[1]
			if !isIgnoredLiteral(val) {
				counts[pkg][val]++
			}
		}
	}
}

func isIgnoredLiteral(val string) bool {
	if len(val) <= 3 {
		return true
	}
	if strings.Contains(val, "%") || strings.HasPrefix(val, "http") || strings.HasPrefix(val, "SELECT") {
		return true
	}
	if strings.Contains(val, "/") || strings.Contains(val, "\n") || strings.Contains(val, `\n`) || strings.Contains(val, "- ") || strings.HasSuffix(val, ".go") || strings.Contains(val, `:"`) {
		return true
	}
	common := map[string]bool{
		"intent": true, "substrate": true, "main": true, "REVIEW": true,
		"EDIT": true, "PLAN": true, "IDLE": true, "SYNC": true, "DONE": true,
		"strings": true, "status": true, "error": true, "nomos": true,
		"message": true, "id": true, "data": true,
		"type": true, "name": true, "json": true, "ok": true, "text": true,
	}
	return common[val]
}

func countPolyglotFiles(root string, dirs []string) (int, error) {
	total := 0
	for _, relDir := range dirs {
		fullDir := filepath.Join(root, relDir)
		err := filepath.Walk(fullDir, func(p string, info os.FileInfo, err error) error {
			if err == nil && info != nil && !info.IsDir() && ratchet.IsPolyglotTargetFile(p) {
				total++
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return 0, err
		}
	}
	return total, nil
}

// ZeroFilesystemAliasGate strictly enforces Law 6 by rejecting filesystem symlinks.
type ZeroFilesystemAliasGate struct{}

// Name returns the canonical display name.
func (g *ZeroFilesystemAliasGate) Name() string { return "Zero Filesystem Alias Invariant" }

// Evaluate rejects filesystem symlinks across target workspace paths.
func (g *ZeroFilesystemAliasGate) Evaluate(ctx *vscope.VerifyContext) (*vscope.GateResult, error) {
	root := ctx.WorktreePath
	if root == "" {
		root = ctx.RepoRoot
	}
	var symlinks []string
	err := vscope.WalkTargetFiles(ctx, root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			rel, rErr := filepath.Rel(root, path)
			if rErr != nil {
				rel = path
			}
			symlinks = append(symlinks, rel)
		}
		return nil
	})
	if err != nil {
		return nil, types.NewNomosError(types.ErrCodeCore, "zero filesystem alias walk failed: %v", err)
	}

	violations := polygate.EvaluateZeroFilesystemAliasViolations(symlinks)
	if len(violations) > 0 {
		return &vscope.GateResult{
			GateName: g.Name(),
			Passed:   false,
			Message:  violations[0],
			Remedy:   "replace all filesystem symlinks with canonical physical files (Law 6)",
		}, nil
	}
	return &vscope.GateResult{GateName: g.Name(), Passed: true, Message: "zero filesystem aliases (symlinks) detected"}, nil
}

// PlaneSeparationGate enforces Law 7 by rejecting cross-plane mutations (SUB mutating intent, INT mutating substrate).
type PlaneSeparationGate struct{}

// Name returns the canonical display name.
func (g *PlaneSeparationGate) Name() string { return "Plane Separation Invariant" }

// Evaluate rejects cross-plane mutations between Intent and Substrate roots.
func (g *PlaneSeparationGate) Evaluate(ctx *vscope.VerifyContext) (*vscope.GateResult, error) {
	if ctx.TaskID == "" || ctx.IsBaseline {
		return &vscope.GateResult{GateName: g.Name(), Passed: true, Message: "plane separation verified (baseline or unassigned task)"}, nil
	}
	workDir := ctx.WorktreePath
	if workDir == "" {
		workDir = ctx.RepoRoot
	}
	modified := vscope.GetModifiedFilesInScope(ctx, workDir)

	cfg := vscope.ResolveVerifyConfig(ctx)
	var intentRoots, substrateRoots, immutableTests []string
	if cfg != nil {
		intentRoots = cfg.Governance.QualityGates.IntentSourceRoots
		substrateRoots = cfg.Governance.QualityGates.SubstrateSourceRoots
		immutableTests = cfg.Governance.QualityGates.ImmutableTestPaths
	}

	violations := EvaluatePlaneViolationsWithRoots(ctx.TaskID, modified, intentRoots, substrateRoots)
	violations = append(violations, evaluateWorktreeOracleAndScopeViolations(workDir, ctx.TaskID, modified, immutableTests)...)
	if len(violations) > 0 {
		return &vscope.GateResult{
			GateName: g.Name(),
			Passed:   false,
			Message:  violations[0],
			Remedy:   PlaneSeparationRemedy(ctx.TaskID),
		}, nil
	}
	return &vscope.GateResult{GateName: g.Name(), Passed: true, Message: "plane separation intact: zero cross-plane mutations"}, nil
}

func evaluateWorktreeOracleAndScopeViolations(workDir, taskKey string, modified, immutableTests []string) []string {
	if len(modified) == 0 {
		return nil
	}
	var rec *task.TaskRecord
	if store, err := task.NewDefaultSQLiteTaskStore(); err == nil && store != nil {
		defer func() {
			if cErr := store.Close(); cErr != nil {
				// best-effort read-only store close
			}
		}()
		if loaded, gErr := store.GetTask(taskKey); gErr == nil {
			rec = loaded
		}
	}
	var out []string
	for _, f := range modified {
		if reason := task.EvaluateTaskWriteBoundaries(workDir, taskKey, f, rec, immutableTests); reason != "" {
			out = append(out, reason)
		}
	}
	return out
}
