package sarif

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/xray"
)

func TestFromXrayReport_ValidSarifLog(t *testing.T) {
	// @verifies TC-SARIF-01
	rep := &xray.XrayReport{
		ProjectName: "demo-repo",
		Hotspots: []xray.XrayHotspot{
			{
				File:          "pkg/core/monolith.go",
				Language:      "go",
				LOC:           520,
				MaxComplexity: 24,
				SecretLeaks:   1,
				Reasons:       []string{"file_loc=520>400", "secret_leaks=1"},
			},
			{
				File:     "pkg/core/clean.go",
				Language: "go",
				LOC:      40,
			},
		},
	}

	log := FromXrayReport(rep, "4.21.1")
	raw, err := MarshalIndent(log)
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	var decoded map[string]any
	if uErr := json.Unmarshal(raw, &decoded); uErr != nil {
		t.Fatalf("unmarshal SARIF JSON: %v", uErr)
	}
	if decoded["version"] != SarifVersion || decoded["$schema"] != SarifSchemaURI {
		t.Fatalf("unexpected SARIF version/schema: %v / %v", decoded["version"], decoded["$schema"])
	}
	if len(log.Runs) != 1 || log.Runs[0].Tool.Driver.Name != "Nomos" || len(log.Runs[0].Results) != 1 {
		t.Fatalf("expected 1 SarifRun and 1 finding for hotspot with reasons, got %+v", log)
	}
	res := log.Runs[0].Results[0]
	if res.Level != "error" || res.Locations[0].PhysicalLocation.ArtifactLocation.URI != "pkg/core/monolith.go" {
		t.Fatalf("unexpected xray SARIF result: %+v", res)
	}
}

func TestFromVerifyReport_MapsGateFailureLocations(t *testing.T) {
	// @verifies TC-SARIF-01
	rep := &verify.PipelineReport{
		Passed:      false,
		GatesTotal:  2,
		GatesPassed: 1,
		GateResults: []*vscope.GateResult{
			{GateName: "Clean Gate", ProbeID: "probe-clean", Passed: true, Message: "ok"},
			{
				GateName: "Unchecked Error Hygiene Invariant",
				ProbeID:  "probe-err",
				Passed:   false,
				Message:  "substrate/cli/foo.go:42 discarded error; pkg/bar.go:19 unhandled",
				Remedy:   "inspect returned errors explicitly",
			},
		},
	}

	log := FromVerifyReport(rep, "4.21.1")
	if len(log.Runs) != 1 || len(log.Runs[0].Tool.Driver.Rules) != 2 || len(log.Runs[0].Results) != 1 {
		t.Fatalf("unexpected verify SARIF output: %+v", log)
	}
	res := log.Runs[0].Results[0]
	if res.RuleID != "probe-err" || !strings.Contains(res.Message.Text, "remedy: inspect returned errors explicitly") {
		t.Fatalf("unexpected verify SARIF result message/rule: %+v", res)
	}
	if len(res.Locations) != 2 {
		t.Fatalf("expected 2 extracted file:line locations, got %d", len(res.Locations))
	}
	first := res.Locations[0].PhysicalLocation
	if first.ArtifactLocation.URI != "substrate/cli/foo.go" || first.Region == nil || first.Region.StartLine != 42 {
		t.Fatalf("unexpected first SARIF physicalLocation: %+v", first)
	}
}
