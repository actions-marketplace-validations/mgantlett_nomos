package xray

import "testing"

func TestCountExplicitPolyglotStubs_CSharpAndGo(t *testing.T) {
	csSrc := []byte("public class Handler {\n\tpublic void Run() => throw new NotImplementedException();\n}\n")
	if got := countExplicitPolyglotStubs("csharp", csSrc); got != 1 {
		t.Fatalf("expected 1 expression-bodied C# stub, got %d", got)
	}
	goSrc := []byte("package p\nfunc Stub() { panic(\"todo\") }\nfunc Real() { x := 1; _ = x }\n")
	if got := countExplicitPolyglotStubs("go", goSrc); got != 1 {
		t.Fatalf("expected 1 Go panic stub, got %d", got)
	}
}
