package treesitter_test

import (
	"reflect"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/verify/treesitter"
)

// TestExtractExportedSymbols_ManagedAndPolyglot verifies TC-POLY-006 symbol scanning.
// @verifies TC-POLY-006
func TestExtractExportedSymbols_ManagedAndPolyglot(t *testing.T) {
	javaSrc := `
package com.example.orders;
public class OrderService {
	public OrderService() {}
	private void helper(int x) { return; }
	public int calculateFee(int cents) { return cents + 10; }
	public static String formatReceipt(String id) { return id; }
}
`
	gotJava := treesitter.ExtractExportedSymbols("java", []byte(javaSrc))
	wantJava := []string{"OrderService", "calculateFee", "formatReceipt"}
	if !reflect.DeepEqual(gotJava, wantJava) {
		t.Fatalf("ExtractExportedSymbols(java) = %v, want %v", gotJava, wantJava)
	}

	csSrc := `
namespace Example.Orders;
public interface IOrderClock { }
public class OrderController {
	internal void Reset() { }
	public async Task<int> SubmitAsync(string id) { return id.Length; }
	public string GetTag(string code) => code;
}
`
	gotCS := treesitter.ExtractExportedSymbols("csharp", []byte(csSrc))
	wantCS := []string{"GetTag", "IOrderClock", "OrderController", "SubmitAsync"}
	if !reflect.DeepEqual(gotCS, wantCS) {
		t.Fatalf("ExtractExportedSymbols(csharp) = %v, want %v", gotCS, wantCS)
	}

	gotTS := treesitter.ExtractExportedSymbols("typescript", []byte("export class Client {}\nexport function load() { return 1; }\n"))
	if !reflect.DeepEqual(gotTS, []string{"Client", "load"}) {
		t.Fatalf("ExtractExportedSymbols(typescript) = %v", gotTS)
	}

	gotGo := treesitter.ExtractExportedSymbols("go", []byte("package p\ntype Ledger struct{}\nfunc Run() {}\nfunc hidden() {}\n"))
	if !reflect.DeepEqual(gotGo, []string{"Ledger", "Run"}) {
		t.Fatalf("ExtractExportedSymbols(go) = %v", gotGo)
	}
}
