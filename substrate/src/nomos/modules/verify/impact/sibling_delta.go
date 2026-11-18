// Package impact provides commit-delta specification obligation isolation for parallel sibling worktrees.
package impact

import (
	"regexp"
	"strings"
)

var mdTableVectorRe = regexp.MustCompile(`TC-[A-Z0-9]+-[0-9]+\b`)

// SiblingDelta records specification obligations added or removed by pending sibling INT-* twin commits (#ContractsConfig).
type SiblingDelta struct {
	AddedTCs      map[string]bool
	RemovedTCs    map[string]bool
	AddedStructs  map[string]bool
	AddedFields   map[string]map[string]bool
	RemovedFields map[string]map[string]bool
}

// NewSiblingDelta allocates an initialized SiblingDelta container.
// @intent nomos_intent_traceability_spec.md #ContractsConfig
func NewSiblingDelta() *SiblingDelta {
	return &SiblingDelta{
		AddedTCs:      make(map[string]bool),
		RemovedTCs:    make(map[string]bool),
		AddedStructs:  make(map[string]bool),
		AddedFields:   make(map[string]map[string]bool),
		RemovedFields: make(map[string]map[string]bool),
	}
}

// HasAddedTC reports whether vec was introduced by a pending sibling INT-* commit.
// @intent nomos_intent_traceability_spec.md #ContractsConfig
func (d *SiblingDelta) HasAddedTC(vec string) bool {
	return d != nil && d.AddedTCs[vec]
}

// HasRemovedTC reports whether vec was removed by a pending sibling INT-* commit.
// @intent nomos_intent_traceability_spec.md #ContractsConfig
func (d *SiblingDelta) HasRemovedTC(vec string) bool {
	return d != nil && d.RemovedTCs[vec]
}

// HasAddedStruct reports whether typeName was newly introduced by a pending sibling INT-* commit.
// @intent nomos_intent_traceability_spec.md #ContractsConfig
func (d *SiblingDelta) HasAddedStruct(typeName string) bool {
	return d != nil && d.AddedStructs[typeName]
}

// HasAddedField reports whether field on typeName was added or retyped by a pending sibling INT-* commit.
// @intent nomos_intent_traceability_spec.md #ContractsConfig
func (d *SiblingDelta) HasAddedField(typeName, field string) bool {
	if d == nil {
		return false
	}
	return d.AddedFields[typeName][field]
}

// HasRemovedField reports whether field on typeName was removed by a pending sibling INT-* commit.
// @intent nomos_intent_traceability_spec.md #ContractsConfig
func (d *SiblingDelta) HasRemovedField(typeName, field string) bool {
	if d == nil {
		return false
	}
	return d.RemovedFields[typeName][field]
}

// ExtractMarkdownTableVectors extracts ordered TC-* identifiers from Markdown table rows.
// @intent nomos_intent_traceability_spec.md #ContractsConfig
func ExtractMarkdownTableVectors(markdown string) []string {
	seen := make(map[string]bool)
	var ordered []string
	for _, line := range strings.Split(markdown, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") {
			continue
		}
		for _, tok := range mdTableVectorRe.FindAllString(trimmed, -1) {
			if !seen[tok] {
				seen[tok] = true
				ordered = append(ordered, tok)
			}
		}
	}
	return ordered
}

// ComputeMarkdownSpecDelta records TC-* vectors, structs, and struct fields added or removed between beforeMD and afterMD.
// @intent nomos_intent_traceability_spec.md #ContractsConfig
func ComputeMarkdownSpecDelta(beforeMD, afterMD string, into *SiblingDelta) {
	if into == nil {
		return
	}
	beforeTCs := make(map[string]bool)
	for _, tc := range ExtractMarkdownTableVectors(beforeMD) {
		beforeTCs[tc] = true
	}
	afterTCs := make(map[string]bool)
	for _, tc := range ExtractMarkdownTableVectors(afterMD) {
		afterTCs[tc] = true
		if !beforeTCs[tc] {
			into.AddedTCs[tc] = true
		}
	}
	for tc := range beforeTCs {
		if !afterTCs[tc] {
			into.RemovedTCs[tc] = true
		}
	}
	beforeStructs := collapseSpecsByName(ExtractSpecStructIndex(beforeMD))
	afterStructs := collapseSpecsByName(ExtractSpecStructIndex(afterMD))
	recordStructAndFieldDeltas(beforeStructs, afterStructs, into)
	recordRemovedStructFields(beforeStructs, afterStructs, into)
}

func recordStructAndFieldDeltas(before, after map[string]map[string]string, into *SiblingDelta) {
	for typeName, afterFields := range after {
		beforeFields, existed := before[typeName]
		if !existed {
			into.AddedStructs[typeName] = true
			continue
		}
		for field, afterType := range afterFields {
			if beforeType, ok := beforeFields[field]; !ok || beforeType != afterType {
				markSiblingFieldMap(into.AddedFields, typeName, field)
			}
		}
	}
}

func recordRemovedStructFields(before, after map[string]map[string]string, into *SiblingDelta) {
	for typeName, beforeFields := range before {
		afterFields, existed := after[typeName]
		for field := range beforeFields {
			if !existed {
				markSiblingFieldMap(into.RemovedFields, typeName, field)
				continue
			}
			if _, ok := afterFields[field]; !ok {
				markSiblingFieldMap(into.RemovedFields, typeName, field)
			}
		}
	}
}

func markSiblingFieldMap(target map[string]map[string]bool, typeName, field string) {
	if target[typeName] == nil {
		target[typeName] = make(map[string]bool)
	}
	target[typeName][field] = true
}

func collapseSpecsByName(in map[SpecStructKey]map[string]string) map[string]map[string]string {
	out := make(map[string]map[string]string, len(in))
	for k, fields := range in {
		dst := out[k.Name]
		if dst == nil {
			dst = make(map[string]string, len(fields))
			out[k.Name] = dst
		}
		for fn, ft := range fields {
			dst[fn] = ft
		}
	}
	return out
}
