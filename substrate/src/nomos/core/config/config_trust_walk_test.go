package config

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestEcosystemConfig_PreTrustExecutableSurfaceWalk(t *testing.T) {
	var paths []string
	walkConfigStructFields(reflect.TypeOf(EcosystemConfig{}), "", &paths)
	sort.Strings(paths)

	knownExecutableFields := map[string]bool{
		"Governance.QualityGates.CorpusCommand": true,
	}
	suspiciousTokens := []string{"Command", "Exec", "Script", "Hook", "Shell"}
	for _, fieldPath := range paths {
		if fieldPath == "Governance.Boundary.ProhibitedCommands" {
			continue
		}
		for _, tok := range suspiciousTokens {
			if strings.Contains(fieldPath, tok) && !knownExecutableFields[fieldPath] {
				t.Fatalf("unreviewed executable config key %q discovered in EcosystemConfig; update trunk-pin guard in gates_intent.go before trusting", fieldPath)
			}
		}
	}
	if len(paths) < 30 {
		t.Fatalf("expected recursive walk to visit >=30 EcosystemConfig leaf fields, got %d", len(paths))
	}
}

func walkConfigStructFields(rt reflect.Type, prefix string, out *[]string) {
	for rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
	}
	if rt.Kind() != reflect.Struct {
		*out = append(*out, prefix)
		return
	}
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		next := f.Name
		if prefix != "" {
			next = prefix + "." + f.Name
		}
		ft := f.Type
		for ft.Kind() == reflect.Pointer || ft.Kind() == reflect.Slice {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct {
			walkConfigStructFields(ft, next, out)
			continue
		}
		*out = append(*out, next)
	}
}
