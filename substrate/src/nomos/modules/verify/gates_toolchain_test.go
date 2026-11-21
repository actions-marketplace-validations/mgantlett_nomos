package verify_test

import (
	"fmt"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify"
)

func TestCountCorpusMagicStringGroups_ParallelDeterminism(t *testing.T) {
	sources := make(map[string][]byte, 24)
	for d := 0; d < 6; d++ {
		for f := 0; f < 4; f++ {
			sources[fmt.Sprintf("pkg/dir_%02d/file_%02d.go", d, f)] = []byte(`package sample

func label() string {
	return "shared-repeated-magic-literal"
}
`)
		}
	}
	got := verify.CountCorpusMagicStringGroups(sources, 3)
	if got != 6 {
		t.Fatalf("expected 6 magic string clusters across 6 directories, got %d", got)
	}
	if zero := verify.CountCorpusMagicStringGroups(nil, 3); zero != 0 {
		t.Fatalf("expected 0 for empty sources, got %d", zero)
	}
}
