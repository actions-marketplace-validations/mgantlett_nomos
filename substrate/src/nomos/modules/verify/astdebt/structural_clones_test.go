package astdebt_test

import (
	"fmt"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/astdebt"
)

func TestDetectStructuralClones_ParallelLargeCorpusDeterminism(t *testing.T) {
	pyBody := []byte(`def transform_alpha(items):
    out = []
    for item in items:
        if item is None:
            continue
        val = str(item).strip()
        if len(val) > 3:
            out.append(val.lower())
        else:
            out.append("default")
    return out
`)
	goBody := []byte(`package demo

func computeOne(xs []int) int {
	total := 0
	count := 0
	for _, v := range xs {
		if v < 0 {
			continue
		}
		count++
		if v > 10 {
			total += v * 2
		} else {
			total += v
		}
	}
	if count == 0 || total == 0 {
		return -1
	}
	return total + count
}
`)
	files := make(map[string][]byte, 48)
	for i := 0; i < 24; i++ {
		files[fmt.Sprintf("pkg/mod_%02d.py", i)] = pyBody
		files[fmt.Sprintf("cmd/mod_%02d.go", i)] = goBody
	}

	first := astdebt.DetectStructuralClones(files, astdebt.DefaultMinCloneLines)
	second := astdebt.DetectStructuralClones(files, astdebt.DefaultMinCloneLines)
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("expected 2 deterministic clone groups across 48 files, got %d and %d", len(first), len(second))
	}
	if first[0].Hash != second[0].Hash || len(first[0].Occurrences) != 24 {
		t.Fatalf("expected deterministic 24-occurrence clone bucket, got %+v", first[0])
	}
}
