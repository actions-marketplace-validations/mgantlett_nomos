package wiredrift_test

import (
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/wiredrift"
)

// TestScanCorpusBoundaryWireDrift verifies TC-XRAY-03 across TypeScript, Go, and Python boundaries.
// @verifies TC-XRAY-03
func TestScanCorpusBoundaryWireDrift(t *testing.T) {
	cases := []struct {
		name        string
		sources     map[string][]byte
		wantUntyped int
		wantPhantom int
		wantClosest string
	}{
		{
			name: "typescript untyped DTO escape and phantom stem wire key",
			sources: map[string][]byte{
				"backend/types.go": []byte(`package backend
type ScanReport struct {
	HangoverScore int          ` + "`json:\"hangover_score\"`" + `
	Hotspots      []HotspotRow ` + "`json:\"hotspots,omitempty\"`" + `
	LastActiveAt  string       ` + "`json:\"last_active_at\"`" + `
}
type HotspotRow struct {
	File string ` + "`json:\"file\"`" + `
}
`),
				"web/contracts.ts": []byte(`export interface PublicScanResult {
	owner: string;
	report: Record<string, unknown>;
}
`),
				"web/render.ts": []byte(`import type { PublicScanResult } from './contracts';
export function renderMarkdown(result: PublicScanResult, rawSession: Record<string, any>): string {
	const r = result.report;
	const items = r.top_hotspots;
	const ts = rawSession.last_active;
	return String(items || ts || '');
}
`),
			},
			wantUntyped: 1,
			wantPhantom: 2,
			wantClosest: "hotspots",
		},
		{
			name: "go and python untyped DTO escapes flagged while open schema bags pass",
			sources: map[string][]byte{
				"api/dto.go": []byte(`package api
type ToolCallResult struct {
	StructuredContent map[string]any ` + "`json:\"structuredContent,omitempty\"`" + `
	ReportBody        map[string]any ` + "`json:\"report_body\"`" + `
}
`),
				"worker/models.py": []byte(`from typing import Any
class JobSnapshot:
    metadata: dict[str, Any]
    telemetry_bag: dict[str, Any]
`),
			},
			wantUntyped: 2,
			wantPhantom: 0,
		},
		{
			name: "typed contracts and generated declarations produce zero findings",
			sources: map[string][]byte{
				"backend/types.go": []byte(`package backend
type ScanReport struct {
	Hotspots []string ` + "`json:\"hotspots\"`" + `
}
`),
				"web/generated.d.ts": []byte(`// Auto-generated. DO NOT EDIT.
export interface LegacyResult {
	report: Record<string, unknown>;
}
`),
				"web/clean.ts": []byte(`export interface ScanReportPayload {
	hotspots?: string[];
	inputSchema?: Record<string, unknown>;
}
export function readClean(r: ScanReportPayload): number {
	return (r.hotspots || []).length;
}
`),
			},
			wantUntyped: 0,
			wantPhantom: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings := wiredrift.ScanCorpusBoundaryWireDrift(tc.sources)
			untyped, phantom := 0, 0
			var formatted []string
			for _, f := range findings {
				formatted = append(formatted, wiredrift.FormatBoundaryDriftFinding(f))
				if f.Kind == wiredrift.KindPhantomWireKey {
					phantom++
				} else if f.Kind == wiredrift.KindUntypedBoundaryRead {
					untyped++
				}
			}
			if untyped != tc.wantUntyped || phantom != tc.wantPhantom {
				t.Fatalf("got untyped=%d phantom=%d (%v), want untyped=%d phantom=%d",
					untyped, phantom, formatted, tc.wantUntyped, tc.wantPhantom)
			}
			if tc.wantClosest != "" && !strings.Contains(strings.Join(formatted, "\n"), "closest wire tag: "+tc.wantClosest) {
				t.Fatalf("expected closest wire tag %q in %v", tc.wantClosest, formatted)
			}
		})
	}
}
