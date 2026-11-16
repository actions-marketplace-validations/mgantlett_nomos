package treesitter

import (
	"testing"
)

func TestPolyglotNativeScanners_JavaAndCSharpAnnotationsAndNullables(t *testing.T) {
	javaSrc := []byte("public class Service {\n\t/** Runs task */\n\t@Override\n\tpublic int run(int x) throws java.io.IOException {\n\t\treturn x + 1;\n\t}\n}\n")
	jMetrics, jErr := scanJava("Service.java", javaSrc)
	if jErr != nil || len(jMetrics) != 1 || !jMetrics[0].Documented || jMetrics[0].EmptyBlock {
		t.Fatalf("unexpected java metrics: err=%v metrics=%+v", jErr, jMetrics)
	}

	csSrc := []byte("public class Repo {\n\t/// Finds item\n\t[HttpGet]\n\tpublic async Task<Order?> FindAsync(string id) where T : class {\n\t\treturn await Load(id);\n\t}\n}\n")
	cMetrics, cErr := scanCSharp("Repo.cs", csSrc)
	if cErr != nil || len(cMetrics) != 1 || cMetrics[0].Name != "FindAsync" || !cMetrics[0].Documented {
		t.Fatalf("unexpected csharp metrics: err=%v metrics=%+v", cErr, cMetrics)
	}
}
