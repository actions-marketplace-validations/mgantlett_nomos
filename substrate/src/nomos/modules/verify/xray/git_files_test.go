package xray_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/core/workspace"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/xray"
)

func TestScanRepository_GitIndexedParallelParity(t *testing.T) {
	dir := t.TempDir()
	if out, err := workspace.SafeGitCommand(dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v (%s)", err, string(out))
	}
	for i := 0; i < 12; i++ {
		rel := filepath.Join(dir, "pkg", "svc_"+string(rune('a'+i))+".go")
		if err := os.MkdirAll(filepath.Dir(rel), 0o755); err != nil {
			t.Fatal(err)
		}
		body := "package pkg\n\nfunc Run" + string(rune('A'+i)) + "() int { return 42 }\n"
		if err := os.WriteFile(rel, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := workspace.SafeGitCommand(dir, "add", ".").CombinedOutput(); err != nil {
		t.Fatalf("git add failed: %v (%s)", err, string(out))
	}

	rep, err := xray.ScanRepository(dir, xray.XrayScanOptions{TopHotspots: 5})
	if err != nil {
		t.Fatalf("ScanRepository error: %v", err)
	}
	if rep.FilesScanned != 12 || rep.Languages["go"] != 12 {
		t.Fatalf("expected 12 scanned Go files, got %+v", rep)
	}
	subRep, subErr := xray.ScanRepository(filepath.Join(dir, "pkg"), xray.XrayScanOptions{TopHotspots: 5})
	if subErr != nil || subRep.FilesScanned != 12 {
		t.Fatalf("expected scoped subdir ScanRepository parity, got err=%v rep=%+v", subErr, subRep)
	}
}
