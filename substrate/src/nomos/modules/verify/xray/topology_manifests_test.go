package xray_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/xray"
)

func TestTopologyManifests_LibraryExportsAndScriptsZeroFalsePositives(t *testing.T) {
	libRoot := filepath.Join(t.TempDir(), "clean-library")
	mustWrite := func(rel, body string) {
		full := filepath.Join(libRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mustWrite("go.mod", "module example.com/cleanlib\n\ngo 1.26\n")
	mustWrite("mux.go", "package cleanlib\n\n// NewMux creates a router.\nfunc NewMux() int { return 1 }\n")
	mustWrite("middleware/logger.go", "package middleware\n\n// Logger wraps a route.\nfunc Logger() int { return 2 }\n")
	mustWrite("package.json", "{\"name\":\"cleanlib\",\"exports\":{\"./cors\":\"./src/cors.ts\"},\"scripts\":{\"postinstall\":\"node tools/postinstall.mjs\"}}\n")
	mustWrite("src/cors.ts", "import { parseHeader } from './headers.js';\nexport function cors(): number { return parseHeader(); }\n")
	mustWrite("src/headers.ts", "export function parseHeader(): number { return 1; }\n")
	mustWrite("tools/postinstall.mjs", "export function verifyInstall() { return true; }\n")

	rep, err := xray.ScanRepository(libRoot, xray.DefaultScanOptions())
	if err != nil {
		t.Fatalf("ScanRepository failed: %v", err)
	}
	if rep.UnreachedFiles != 0 || rep.HangoverScore != 0 {
		t.Fatalf("expected 0 unreached files and score 0, got unreached=%d score=%d dust=%+v",
			rep.UnreachedFiles, rep.HangoverScore, rep.Topology.Dust)
	}
}
