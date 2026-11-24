package xray_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/xray"
)

// TestScanRepository_TopologySpineLibraryExemplarsAndUnreachedDust verifies TC-XRAY-04.
// @verifies TC-XRAY-04
func TestScanRepository_TopologySpineLibraryExemplarsAndUnreachedDust(t *testing.T) {
	appRoot := filepath.Join(t.TempDir(), "spine-dust-app")
	mustWrite := func(base, rel, body string) {
		full := filepath.Join(base, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	mustWrite(appRoot, "go.mod", "module example.com/spinedust\n\ngo 1.26\n")
	mustWrite(appRoot, "cmd/server/main.go", "package main\n\nimport \"example.com/spinedust/internal/service\"\n\nfunc main() { _ = service.Run() }\n")
	mustWrite(appRoot, "internal/service/svc.go", "package service\n\nimport (\n\t\"example.com/spinedust/internal/store\"\n\t\"example.com/spinedust/internal/telemetry\"\n)\n\n// Run executes the service.\nfunc Run() int { return store.Query() + telemetry.Emit() }\n")
	mustWrite(appRoot, "internal/store/db.go", "package store\n\n// Query returns a count.\nfunc Query() int { return 42 }\n")
	mustWrite(appRoot, "internal/telemetry/metrics.go", "package telemetry\n\n// Emit returns 1.\nfunc Emit() int { return 1 }\n")
	mustWrite(appRoot, "internal/orphan/abandoned_draft.go", "package orphan\n\n// UnusedHelper is dead AI dust.\nfunc UnusedHelper() int { return 0 }\n")

	rep, err := xray.ScanRepository(appRoot, xray.DefaultScanOptions())
	if err != nil {
		t.Fatalf("ScanRepository failed: %v", err)
	}
	if rep.SpineDepth != 3 || rep.ReachableFiles != 4 || rep.UnreachedFiles != 1 {
		t.Fatalf("expected SpineDepth=3 ReachableFiles=4 UnreachedFiles=1, got spine=%d reach=%d dust=%d",
			rep.SpineDepth, rep.ReachableFiles, rep.UnreachedFiles)
	}
	if rep.Topology == nil || len(rep.Topology.Spine) != 3 || len(rep.Topology.Branches) != 1 || len(rep.Topology.Dust) != 1 {
		t.Fatalf("unexpected Topology payload: %+v", rep.Topology)
	}
	if rep.Topology.Dust[0].File != "internal/orphan/abandoned_draft.go" {
		t.Fatalf("expected dust file internal/orphan/abandoned_draft.go, got %+v", rep.Topology.Dust)
	}

	violations, gErr := xray.EvaluateUnreachedFilesGate(appRoot, 0)
	if gErr != nil || len(violations) != 1 {
		t.Fatalf("expected 1 Zero-Dust violation at baseline 0, got %v (err=%v)", violations, gErr)
	}
	ratcheted, rErr := xray.EvaluateUnreachedFilesGate(appRoot, 1)
	if rErr != nil || len(ratcheted) != 0 {
		t.Fatalf("expected 0 violations at baseline 1, got %v (err=%v)", ratcheted, rErr)
	}
}
