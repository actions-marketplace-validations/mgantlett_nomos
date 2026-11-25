package xray_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/xray"
)

func TestTopologyImports_RustModAndCHeaderPairing(t *testing.T) {
	root := filepath.Join(t.TempDir(), "rust-c-app")
	mustWrite := func(rel, body string) {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mustWrite("src/main.rs", "mod engine;\nfn main() { let _ = engine::tick(); }\n")
	mustWrite("src/engine.rs", "use crate::math;\npub fn tick() -> i32 { math::step() }\n")
	mustWrite("src/math.rs", "pub fn step() -> i32 { 1 }\n")
	mustWrite("native/main.c", "#include \"codec.h\"\nint main(void) { return decode_frame(); }\n")
	mustWrite("native/codec.h", "int decode_frame(void);\n")
	mustWrite("native/codec.c", "#include \"codec.h\"\nint decode_frame(void) { return 0; }\n")

	rep, err := xray.ScanRepository(root, xray.DefaultScanOptions())
	if err != nil {
		t.Fatalf("ScanRepository failed: %v", err)
	}
	if rep.UnreachedFiles != 0 || rep.SpineDepth < 3 {
		t.Fatalf("expected 0 unreached files and SpineDepth>=3, got unreached=%d spine=%d dust=%+v",
			rep.UnreachedFiles, rep.SpineDepth, rep.Topology.Dust)
	}
}
