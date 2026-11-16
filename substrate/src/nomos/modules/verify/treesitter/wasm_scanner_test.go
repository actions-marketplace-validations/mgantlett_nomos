package treesitter

import (
	"context"
	"testing"
)

func TestExecuteWasmVerify_ManagedGrammarDrivers(t *testing.T) {
	ctx := context.Background()
	eng, err := NewTreeSitterEngine(ctx)
	if err != nil {
		t.Fatalf("NewTreeSitterEngine failed: %v", err)
	}
	defer func() {
		if cErr := eng.Close(ctx); cErr != nil {
			t.Fatalf("Close failed: %v", cErr)
		}
	}()

	if vErr := eng.ExecuteWasmVerify(ctx); vErr != nil {
		t.Fatalf("ExecuteWasmVerify failed: %v", vErr)
	}
	if res, handled := eng.callManagedByteScanner("java", "scan_block_balance", "int[] bad = new int[1;"); !handled || res != 0 {
		t.Fatalf("expected unbalanced bracket to return res=0 handled=true, got res=%d handled=%v", res, handled)
	}
}
