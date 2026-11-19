package impact

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/config"
)

func TestEvaluateArchitectureBoundaries_DottedModulePrefixCandidate(t *testing.T) {
	dir := t.TempDir()
	domainFile := filepath.Join(dir, "src", "domain", "order.py")
	infraFile := filepath.Join(dir, "src", "infra", "db.py")
	if err := os.MkdirAll(filepath.Dir(domainFile), 0755); err != nil {
		t.Fatalf("mkdir domain: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(infraFile), 0755); err != nil {
		t.Fatalf("mkdir infra: %v", err)
	}
	if err := os.WriteFile(domainFile, []byte("from mypkg.src.infra.db import save\n"), 0644); err != nil {
		t.Fatalf("write domain file: %v", err)
	}
	if err := os.WriteFile(infraFile, []byte("def save():\n    return True\n"), 0644); err != nil {
		t.Fatalf("write infra file: %v", err)
	}

	rules := []config.ArchitectureBoundaryRule{{
		Name:          "domain-purity",
		From:          "src/domain/**",
		ForbidImports: []string{"src/infra/**"},
	}}
	violations := EvaluateArchitectureBoundaries(dir, nil, rules)
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation for dotted module prefix import, got: %v", violations)
	}
}

// @verifies TC-COV-007
func TestCheckThresholdsDowngrade_RejectsCapIncreaseAndFloorDecrease(t *testing.T) {
	trunk := config.DefaultNomosConfig()
	trunk.Governance.QualityGates.Thresholds.MaxPackageFiles = 30
	trunk.Governance.QualityGates.Thresholds.MinCommentDensityPct = 90

	tighter := config.DefaultNomosConfig()
	tighter.Governance.QualityGates.Thresholds.MaxPackageFiles = 28
	tighter.Governance.QualityGates.Thresholds.MinCommentDensityPct = 92
	if msg := CheckThresholdsDowngrade(trunk, tighter); msg != "" {
		t.Fatalf("expected stricter worktree thresholds to pass, got: %s", msg)
	}

	relaxedCap := config.DefaultNomosConfig()
	relaxedCap.Governance.QualityGates.Thresholds.MaxPackageFiles = 40
	relaxedCap.Governance.QualityGates.Thresholds.MinCommentDensityPct = 90
	if msg := CheckThresholdsDowngrade(trunk, relaxedCap); msg == "" {
		t.Fatalf("expected relaxation of max_package_files (30 -> 40) to fail closed")
	}

	loweredFloor := config.DefaultNomosConfig()
	loweredFloor.Governance.QualityGates.Thresholds.MaxPackageFiles = 30
	loweredFloor.Governance.QualityGates.Thresholds.MinCommentDensityPct = 10
	if msg := CheckThresholdsDowngrade(trunk, loweredFloor); msg == "" {
		t.Fatalf("expected lowering of min_comment_density_pct (90 -> 10) to fail closed")
	}
}
