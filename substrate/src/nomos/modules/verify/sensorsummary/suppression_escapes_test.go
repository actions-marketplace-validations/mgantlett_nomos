package sensorsummary

import (
	"strings"
	"testing"
)

// @verifies TC-AGT-12
func TestScanSuppressionEscapes_TC_AGT_12(t *testing.T) {
	cases := []struct {
		name     string
		relPath  string
		src      string
		wantHits int
		wantSub  string
	}{
		{
			name:     "go_nolint_comment_flagged",
			relPath:  "pkg/svc/handler.go",
			src:      "package svc\nfunc run() { //nolint:errcheck\n\t_ = 1\n}\n",
			wantHits: 1,
			wantSub:  "go //nolint",
		},
		{
			name:     "go_string_literal_not_flagged",
			relPath:  "pkg/svc/rules.go",
			src:      "package svc\nconst pat = \"//nolint and eslint-disable and as any\"\n",
			wantHits: 0,
		},
		{
			name:     "ts_ignore_and_eslint_and_any_flagged",
			relPath:  "src/lib/client.ts",
			src:      "// eslint-disable-next-line no-console\n// @ts-ignore\nexport function parse(v: any) { return v as any; }\n",
			wantHits: 3,
			wantSub:  "suppression_escape",
		},
		{
			name:     "ts_clean_typed_code_passes",
			relPath:  "src/lib/client.ts",
			src:      "// anything in a comment is fine\nexport function parse(v: unknown): string { return String(v); }\n",
			wantHits: 0,
		},
		{
			name:     "python_noqa_and_type_ignore_flagged",
			relPath:  "app/worker.py",
			src:      "import os  # noqa: F401\nx = load()  # type: ignore\n",
			wantHits: 2,
			wantSub:  "python lint or type suppression pragma",
		},
		{
			name:     "rust_allow_attr_flagged",
			relPath:  "src/lib.rs",
			src:      "#[allow(dead_code)]\nfn helper() {}\n",
			wantHits: 1,
			wantSub:  "rust #[allow(...)]",
		},
		{
			name:     "test_files_exempted",
			relPath:  "src/lib/client.test.ts",
			src:      "// @ts-ignore\nconst mock = {} as any;\n",
			wantHits: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ScanSuppressionEscapes(tc.relPath, []byte(tc.src))
			if len(got) != tc.wantHits {
				t.Fatalf("ScanSuppressionEscapes(%s) hits=%d want=%d (%v)", tc.relPath, len(got), tc.wantHits, got)
			}
			if tc.wantSub != "" && !strings.Contains(strings.Join(got, "; "), tc.wantSub) {
				t.Fatalf("expected %q in %v", tc.wantSub, got)
			}
		})
	}
}
