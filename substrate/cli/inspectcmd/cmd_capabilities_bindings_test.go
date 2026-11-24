package inspectcmd

import (
	"strings"
	"testing"
)

func TestExtractMatchingTableRowsAndClampBlock(t *testing.T) {
	md := "# Title Header\n\n## Section A\n\n| ID | Desc |\n| :--- | :--- |\n| `TC-A-01` | Verifies `Widget` |\n| `TC-A-02` | Verifies `Other` |\n"
	rows := extractMatchingTableRows(md, "Widget")
	if len(rows) != 1 || !strings.Contains(rows[0], "TC-A-01") {
		t.Fatalf("expected 1 Widget row, got %+v", rows)
	}
	tcs := extractTCIDsFromRows(rows)
	if len(tcs) != 1 || tcs[0] != "TC-A-01" {
		t.Fatalf("expected TC-A-01 extracted, got %+v", tcs)
	}
	if h := findNearestHeading(md, "TC-A-01"); h != "Section A" {
		t.Fatalf("expected nearest heading 'Section A', got %q", h)
	}
	long := strings.Repeat("x", maxContractBlockLen+50)
	clamped := clampBlock(long)
	if !strings.HasSuffix(clamped, "...") || len(clamped) <= maxContractBlockLen {
		t.Fatalf("expected clamped block suffix, len=%d", len(clamped))
	}
}
