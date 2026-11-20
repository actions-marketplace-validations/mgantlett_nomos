// Package astdebt tests web TypeScript, JavaScript, and Svelte contract surface fingerprinting.
package astdebt

import "testing"

// TestParseWebSurfaceFingerprintForPath verifies comment stripping, test exclusion, and TS/Svelte export extraction.
func TestParseWebSurfaceFingerprintForPath(t *testing.T) {
	if IsNonTestWebSourcePath("ui/src/lib/__tests__/foo.ts") || IsNonTestWebSourcePath("ui/src/foo.test.ts") || IsNonTestWebSourcePath("ui/src/env.d.ts") {
		t.Fatal("expected test suites and .d.ts stubs to be excluded from web surface fingerprinting")
	}
	if !IsNonTestWebSourcePath("ui/src/lib/AutonomicBanner.svelte") || !IsNonTestWebSourcePath("ui/src/lib/triage.ts") {
		t.Fatal("expected .svelte and non-test .ts files to be included in web surface fingerprinting")
	}

	tsSrc := []byte(`
// export function commentedOut() {}
/* export const blockCommented = 1; */
export const CORE_SCOPE = 'nomos';
export async function resolveScope(): Promise<string> { return CORE_SCOPE; }
export interface TriageVerdict { label: string; }
function privateHelper() {}
`)
	fp := ParseWebSurfaceFingerprintForPath("ui/src/lib/triage.ts", tsSrc)
	for _, want := range []string{
		"export_ts:ui/src/lib.CORE_SCOPE",
		"export_ts:ui/src/lib.resolveScope",
		"export_type_ts:ui/src/lib.TriageVerdict",
	} {
		if !fp[want] {
			t.Fatalf("missing expected token %q in %+v", want, fp)
		}
	}
	if len(fp) != 3 {
		t.Fatalf("expected strictly 3 exported tokens (comments & private skipped), got %+v", fp)
	}

	svFp := ParseWebSurfaceFingerprintForPath("ui/src/lib/AutonomicBanner.svelte", []byte("<script lang=\"ts\">\nexport function focusBanner() {}\n</script>\n<div>ok</div>"))
	if !svFp["svelte_component:ui/src/lib:AutonomicBanner"] || !svFp["export_ts:ui/src/lib.focusBanner"] {
		t.Fatalf("expected svelte_component and export_ts tokens, got %+v", svFp)
	}
}
