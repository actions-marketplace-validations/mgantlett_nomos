// Package sarif implements OASIS SARIF 2.1.0 serialization for nomos xray and nomos verify.
// @intent task_lifecycle_cli_spec.md
package sarif

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/vscope"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/xray"
)

const (
	// SarifVersion identifies the OASIS SARIF 2.1.0 wire contract version.
	SarifVersion = "2.1.0"
	// SarifSchemaURI identifies the canonical OASIS SARIF 2.1.0 JSON schema.
	SarifSchemaURI = "https://json.schemastore.org/sarif-2.1.0.json"
	nomosRepoURI   = "https://github.com/mgantlett/nomos"
)

var fileLineLocRe = regexp.MustCompile(`([A-Za-z0-9_./-]+\.[A-Za-z0-9]+):(\d+)`)

// SarifLog models an OASIS SARIF 2.1.0 root log object (#SarifLog).
type SarifLog struct {
	Version string     `json:"version"`
	Schema  string     `json:"$schema"`
	Runs    []SarifRun `json:"runs"`
}

// SarifProperties carries typed Nomos gate sensor execution and extension coverage metadata in SARIF property bags.
type SarifProperties struct {
	ExecutionScope      string         `json:"executionScope,omitempty"`
	SensorSummary       map[string]int `json:"sensorSummary,omitempty"`
	UnindexedExtensions []string       `json:"unindexedExtensions,omitempty"`
}

// SarifRun captures a single Nomos analysis run in SARIF format.
type SarifRun struct {
	Tool       SarifTool        `json:"tool"`
	Results    []SarifResult    `json:"results"`
	Properties *SarifProperties `json:"properties,omitempty"`
}

// SarifTool identifies the analyzing tool driver inside a SARIF run.
type SarifTool struct {
	Driver SarifDriver `json:"driver"`
}

// SarifDriver declares the Nomos driver metadata and rule catalog.
type SarifDriver struct {
	Name           string      `json:"name"`
	Version        string      `json:"version,omitempty"`
	InformationURI string      `json:"informationUri,omitempty"`
	Rules          []SarifRule `json:"rules,omitempty"`
}

// SarifRule defines a SARIF rule descriptor mapped from a Nomos check or hotspot class.
type SarifRule struct {
	ID               string           `json:"id"`
	Name             string           `json:"name,omitempty"`
	ShortDescription SarifMessage     `json:"shortDescription"`
	FullDescription  *SarifMessage    `json:"fullDescription,omitempty"`
	Properties       *SarifProperties `json:"properties,omitempty"`
}

// SarifResult encodes a single finding emitted for GitHub Code Scanning ingestion.
type SarifResult struct {
	RuleID     string           `json:"ruleId"`
	Level      string           `json:"level"`
	Message    SarifMessage     `json:"message"`
	Locations  []SarifLocation  `json:"locations,omitempty"`
	Properties *SarifProperties `json:"properties,omitempty"`
}

// SarifMessage wraps human-readable text inside a SARIF object.
type SarifMessage struct {
	Text string `json:"text"`
}

// SarifLocation wraps a physical artifact location inside a SARIF result.
type SarifLocation struct {
	PhysicalLocation SarifPhysicalLocation `json:"physicalLocation"`
}

// SarifPhysicalLocation binds an artifact URI and source region.
type SarifPhysicalLocation struct {
	ArtifactLocation SarifArtifactLocation `json:"artifactLocation"`
	Region           *SarifRegion          `json:"region,omitempty"`
}

// SarifArtifactLocation identifies a repository-relative POSIX path.
type SarifArtifactLocation struct {
	URI string `json:"uri"`
}

// SarifRegion identifies a 1-based line coordinate inside an artifact.
type SarifRegion struct {
	StartLine int `json:"startLine"`
}

// FromXrayReport converts a read-only XrayReport into an OASIS SARIF 2.1.0 log (@intent task_lifecycle_cli_spec.md).
func FromXrayReport(rep *xray.XrayReport, version string) *SarifLog {
	rules := []SarifRule{{
		ID:               "NOMOS-XRAY-HOTSPOT",
		Name:             "NomosXrayHotspot",
		ShortDescription: SarifMessage{Text: "Brownfield structural debt or anti-cheat hotspot"},
	}}
	results := []SarifResult{}
	if rep != nil {
		for _, hs := range rep.Hotspots {
			if len(hs.Reasons) == 0 {
				continue
			}
			level := xrayHotspotSarifLevel(hs)
			msg := fmt.Sprintf("%s (%s, %d LOC, max V(G)=%d): %s", hs.File, hs.Language, hs.LOC, hs.MaxComplexity, strings.Join(hs.Reasons, ", "))
			results = append(results, SarifResult{
				RuleID:    "NOMOS-XRAY-HOTSPOT",
				Level:     level,
				Message:   SarifMessage{Text: msg},
				Locations: []SarifLocation{newSarifLocation(hs.File, 1)},
			})
		}
	}
	return buildSarifLog(version, rules, results)
}

func xrayHotspotSarifLevel(hs xray.XrayHotspot) string {
	hasPathOnly := false
	for _, r := range hs.Reasons {
		if strings.HasPrefix(r, "secret_leaks=") || strings.HasPrefix(r, "cwe_sinks=") {
			return "error"
		}
		if strings.HasPrefix(r, "path_leaks=") {
			hasPathOnly = true
		}
	}
	if hs.SecretLeaks > 0 && !hasPathOnly {
		return "error"
	}
	return "warning"
}

// FromVerifyReport converts a PipelineReport into an OASIS SARIF 2.1.0 log (@intent task_lifecycle_cli_spec.md).
func FromVerifyReport(rep *verify.PipelineReport, version string) *SarifLog {
	rules := []SarifRule{}
	results := []SarifResult{}
	seenRules := make(map[string]bool)
	if rep != nil {
		for _, res := range rep.GateResults {
			if res == nil {
				continue
			}
			ruleID := deriveGateRuleID(res)
			if !seenRules[ruleID] {
				seenRules[ruleID] = true
				rules = append(rules, buildGateRule(ruleID, res))
			}
			if !res.Passed && !res.Skipped {
				results = append(results, buildVerifyResult(ruleID, res))
			}
		}
	}
	log := buildSarifLog(version, rules, results)
	if rep != nil && (len(rep.SensorSummary) > 0 || len(rep.UnindexedExtensions) > 0) && len(log.Runs) > 0 {
		log.Runs[0].Properties = &SarifProperties{
			SensorSummary:       rep.SensorSummary,
			UnindexedExtensions: rep.UnindexedExtensions,
		}
	}
	return log
}

// MarshalIndent serializes a SarifLog as canonical formatted JSON.
func MarshalIndent(log *SarifLog) ([]byte, error) {
	return json.MarshalIndent(log, "", "  ")
}

func buildSarifLog(version string, rules []SarifRule, results []SarifResult) *SarifLog {
	return &SarifLog{
		Version: SarifVersion,
		Schema:  SarifSchemaURI,
		Runs: []SarifRun{{
			Tool: SarifTool{
				Driver: SarifDriver{
					Name:           "Nomos",
					Version:        strings.TrimSpace(version),
					InformationURI: nomosRepoURI,
					Rules:          rules,
				},
			},
			Results: results,
		}},
	}
}

func buildGateRule(ruleID string, res *vscope.GateResult) SarifRule {
	rule := SarifRule{
		ID:               ruleID,
		Name:             res.GateName,
		ShortDescription: SarifMessage{Text: res.GateName},
	}
	if strings.TrimSpace(res.Remedy) != "" {
		rule.FullDescription = &SarifMessage{Text: res.Remedy}
	}
	if strings.TrimSpace(res.ExecutionScope) != "" {
		rule.Properties = &SarifProperties{ExecutionScope: res.ExecutionScope}
	}
	return rule
}

func buildVerifyResult(ruleID string, res *vscope.GateResult) SarifResult {
	msg := res.Message
	if strings.TrimSpace(res.Remedy) != "" {
		msg = fmt.Sprintf("%s (remedy: %s)", res.Message, res.Remedy)
	}
	out := SarifResult{
		RuleID:    ruleID,
		Level:     "error",
		Message:   SarifMessage{Text: msg},
		Locations: extractSarifLocations(res.Message),
	}
	if strings.TrimSpace(res.ExecutionScope) != "" {
		out.Properties = &SarifProperties{ExecutionScope: res.ExecutionScope}
	}
	return out
}

func deriveGateRuleID(res *vscope.GateResult) string {
	if clean := strings.TrimSpace(res.ProbeID); clean != "" {
		return clean
	}
	var b strings.Builder
	b.WriteString("NOMOS-")
	lastDash := true
	for _, r := range strings.ToUpper(res.GateName) {
		isAlnum := (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if isAlnum {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

func extractSarifLocations(message string) []SarifLocation {
	matches := fileLineLocRe.FindAllStringSubmatch(message, 5)
	if len(matches) == 0 {
		return []SarifLocation{newSarifLocation("nomos.yaml", 1)}
	}
	locs := make([]SarifLocation, 0, len(matches))
	seen := make(map[string]bool)
	for _, m := range matches {
		uri := filepath.ToSlash(strings.TrimPrefix(m[1], "./"))
		line, err := strconv.Atoi(m[2])
		if err != nil || line < 1 {
			line = 1
		}
		key := fmt.Sprintf("%s:%d", uri, line)
		if seen[key] {
			continue
		}
		seen[key] = true
		locs = append(locs, newSarifLocation(uri, line))
	}
	return locs
}

func newSarifLocation(uri string, line int) SarifLocation {
	if line < 1 {
		line = 1
	}
	return SarifLocation{
		PhysicalLocation: SarifPhysicalLocation{
			ArtifactLocation: SarifArtifactLocation{URI: filepath.ToSlash(uri)},
			Region:           &SarifRegion{StartLine: line},
		},
	}
}
