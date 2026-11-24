package xray_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/xray"
)

func TestTopologyRoots_SvelteKitAliasesAndPythonEntrypoints(t *testing.T) {
	webRoot := filepath.Join(t.TempDir(), "svelte-app")
	mustWrite := func(rel, body string) {
		full := filepath.Join(webRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mustWrite("src/routes/+page.svelte", "<script lang=\"ts\">\nimport { loadFeed } from '$lib/api';\nconst items = loadFeed();\n</script>\n<p>{items}</p>\n")
	mustWrite("src/lib/api.ts", "import { normalize } from './format';\nexport function loadFeed(): string { return normalize('ok'); }\n")
	mustWrite("src/lib/format.ts", "export function normalize(v: string): string { return v.trim(); }\n")
	mustWrite("src/lib/dead_agent_helper.ts", "export function neverCalled(): number { return 99; }\n")

	rep, err := xray.ScanRepository(webRoot, xray.DefaultScanOptions())
	if err != nil {
		t.Fatalf("ScanRepository failed: %v", err)
	}
	if rep.SpineDepth != 3 || rep.ReachableFiles != 3 || rep.UnreachedFiles != 1 {
		t.Fatalf("expected SpineDepth=3 Reachable=3 Unreached=1, got spine=%d reach=%d dust=%d",
			rep.SpineDepth, rep.ReachableFiles, rep.UnreachedFiles)
	}
	if rep.Topology.Dust[0].File != "src/lib/dead_agent_helper.ts" {
		t.Fatalf("expected dead_agent_helper.ts in dust, got %+v", rep.Topology.Dust)
	}
}
