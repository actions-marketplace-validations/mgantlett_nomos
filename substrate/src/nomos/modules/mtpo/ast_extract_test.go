package mtpo_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mgantlett/nomos/substrate/src/nomos/modules/mtpo"
)

func TestExtractFileASTSets_JavaAndCSharpExportsAndImports(t *testing.T) {
	dir := t.TempDir()
	javaPath := filepath.Join(dir, "BillingService.java")
	javaSrc := "import com.acme.ledger.Invoice;\npublic class BillingService {\n\tpublic int compute(int x) { return Formatter.Apply(x); }\n}\n"
	if err := os.WriteFile(javaPath, []byte(javaSrc), 0644); err != nil {
		t.Fatal(err)
	}
	wJava, rJava, iJava := mtpo.ExtractFileASTSets(javaPath, "src/BillingService.java")
	if !reflect.DeepEqual(wJava, []string{"BillingService.BillingService", "BillingService.compute"}) {
		t.Fatalf("unexpected java writeSyms: %v", wJava)
	}
	if !reflect.DeepEqual(iJava, []string{"com.acme.ledger.Invoice"}) || len(rJava) == 0 {
		t.Fatalf("unexpected java imports/reads: imps=%v reads=%v", iJava, rJava)
	}

	csPath := filepath.Join(dir, "OrderEndpoint.cs")
	csSrc := "using Acme.Payments;\npublic class OrderEndpoint {\n\tpublic Task<int> Submit(string id) { return Gateway.Charge(id); }\n}\n"
	if err := os.WriteFile(csPath, []byte(csSrc), 0644); err != nil {
		t.Fatal(err)
	}
	wCS, rCS, iCS := mtpo.ExtractFileASTSets(csPath, "Api/OrderEndpoint.cs")
	if !reflect.DeepEqual(wCS, []string{"OrderEndpoint.OrderEndpoint", "OrderEndpoint.Submit"}) {
		t.Fatalf("unexpected csharp writeSyms: %v", wCS)
	}
	if !reflect.DeepEqual(iCS, []string{"Acme.Payments"}) || len(rCS) == 0 {
		t.Fatalf("unexpected csharp imports/reads: imps=%v reads=%v", iCS, rCS)
	}
}
