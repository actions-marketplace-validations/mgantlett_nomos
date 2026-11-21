package xray_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/xray"
)

func TestScanRepository_WalkFallbackDeterminism(t *testing.T) {
	dir := t.TempDir()
	pySrc := "def alpha(items):\n    return [x for x in items if x]\n"
	for _, name := range []string{"mod_a.py", "mod_b.py", "test_mod_a.py"} {
		p := filepath.Join(dir, "src", name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(pySrc), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	rep1, err := xray.ScanRepository(dir, xray.XrayScanOptions{TopHotspots: 5})
	if err != nil {
		t.Fatalf("ScanRepository #1: %v", err)
	}
	rep2, err := xray.ScanRepository(dir, xray.XrayScanOptions{TopHotspots: 5})
	if err != nil {
		t.Fatalf("ScanRepository #2: %v", err)
	}
	if rep1.FilesScanned != 3 || rep1.TotalLOC != rep2.TotalLOC || rep1.HangoverScore != rep2.HangoverScore {
		t.Fatalf("non-deterministic walk scan: %+v vs %+v", rep1, rep2)
	}
}
