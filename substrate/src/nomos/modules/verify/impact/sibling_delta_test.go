package impact

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// @verifies TC-OBL-015
func TestComputeMarkdownSpecDelta_TracksAddedTCsStructsAndFields(t *testing.T) {
	beforeMD := "# Shared Spec\n\n```go\npackage sample\ntype Alpha struct {\n\tID string\n}\n```\n\n| ID | Desc |\n| :--- | :--- |\n| `TC-SHR-01` | Base |\n"
	afterMD := "# Shared Spec\n\n```go\npackage sample\ntype Alpha struct {\n\tID string\n\tNewSiblingField bool\n}\ntype SiblingOnlyStruct struct {\n\tCount int\n}\n```\n\n| ID | Desc |\n| :--- | :--- |\n| `TC-SHR-01` | Base |\n| `TC-SHR-02` | Sibling vector |\n"

	delta := NewSiblingDelta()
	ComputeMarkdownSpecDelta(beforeMD, afterMD, delta)

	if delta.HasAddedTC("TC-SHR-01") {
		t.Fatal("did not expect pre-existing TC-SHR-01 in sibling delta")
	}
	if !delta.HasAddedTC("TC-SHR-02") {
		t.Fatal("expected newly added TC-SHR-02 in sibling delta")
	}
	if !delta.HasAddedStruct("SiblingOnlyStruct") {
		t.Fatal("expected SiblingOnlyStruct in sibling delta")
	}
	if !delta.HasAddedField("Alpha", "NewSiblingField") {
		t.Fatal("expected Alpha.NewSiblingField in sibling delta")
	}
	if delta.HasAddedField("Alpha", "ID") {
		t.Fatal("did not expect unchanged Alpha.ID in sibling delta")
	}
}

// @verifies TC-OBL-015
func TestComputeMarkdownSpecDelta_TracksRemovedFieldsStructsAndTCsAndDefersCompiledDrift(t *testing.T) {
	beforeMD := "# Portal Spec\n\n```go\npackage telemetry\ntype TrafficTelemetryResponse struct {\n\tActive bool\n\tCheckpoint string\n}\ntype LegacySummary struct {\n\tCount int\n}\n```\n\n| ID | Desc |\n| :--- | :--- |\n| `TC-TEL-01` | Base |\n| `TC-TEL-02` | Retired checkpoint |\n"
	afterMD := "# Portal Spec\n\n```go\npackage telemetry\ntype TrafficTelemetryResponse struct {\n\tActive bool\n}\n```\n\n| ID | Desc |\n| :--- | :--- |\n| `TC-TEL-01` | Base |\n"

	delta := NewSiblingDelta()
	ComputeMarkdownSpecDelta(beforeMD, afterMD, delta)

	if !delta.HasRemovedTC("TC-TEL-02") || delta.HasRemovedTC("TC-TEL-01") {
		t.Fatalf("expected only TC-TEL-02 in RemovedTCs, got: %+v", delta.RemovedTCs)
	}
	if !delta.HasRemovedField("TrafficTelemetryResponse", "Checkpoint") {
		t.Fatalf("expected TrafficTelemetryResponse.Checkpoint in RemovedFields, got: %+v", delta.RemovedFields)
	}
	if delta.HasRemovedField("TrafficTelemetryResponse", "Active") {
		t.Fatal("did not expect retained Active field in RemovedFields")
	}
	if !delta.HasRemovedField("LegacySummary", "Count") {
		t.Fatalf("expected removed struct field LegacySummary.Count in RemovedFields, got: %+v", delta.RemovedFields)
	}

	tmp := t.TempDir()
	pkgDir := filepath.Join(tmp, "pkg", "telemetry")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	goSrc := "package telemetry\ntype TrafficTelemetryResponse struct {\n\tActive bool\n\tCheckpoint string\n\tUnrelatedExtra int\n}\n"
	if err := os.WriteFile(filepath.Join(pkgDir, "resp.go"), []byte(goSrc), 0o644); err != nil {
		t.Fatalf("write go: %v", err)
	}
	compiled := CollectCompiledStructIndex(tmp)
	specIdx := ExtractSpecStructIndex(afterMD)

	// Without sibling delta, Checkpoint fails as undocumented compiled field.
	withoutDelta := strings.Join(DiffSpecStructsBijective("portal_spec.md", specIdx, compiled, nil), " | ")
	if !strings.Contains(withoutDelta, "undocumented compiled field Checkpoint") {
		t.Fatalf("expected Checkpoint drift without delta, got: %s", withoutDelta)
	}

	// With sibling delta, Checkpoint is deferred while UnrelatedExtra still fails closed.
	withDelta := strings.Join(DiffSpecStructsBijective("portal_spec.md", specIdx, compiled, delta), " | ")
	if strings.Contains(withDelta, "Checkpoint") {
		t.Fatalf("expected sibling-removed Checkpoint to be deferred, got: %s", withDelta)
	}
	if !strings.Contains(withDelta, "undocumented compiled field UnrelatedExtra") {
		t.Fatalf("expected UnrelatedExtra to still fail closed, got: %s", withDelta)
	}
}
