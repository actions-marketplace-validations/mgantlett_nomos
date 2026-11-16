package verify

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestXrayParityHelpers_UncheckedSecretsAndMagicStrings(t *testing.T) {
	goDiscard := []byte("package demo\nfunc step() error { return nil }\nfunc run() {\n\t_ = step()\n}\n")
	if got := CountFileUncheckedErrors("pkg/demo.go", goDiscard); got != 1 {
		t.Fatalf("expected 1 Go unchecked error discard, got %d", got)
	}
	tsSwallow := []byte("export function run() {\n  try { work(); } catch (err) {}\n}\n")
	if got := CountFileUncheckedErrors("src/app.ts", tsSwallow); got != 1 {
		t.Fatalf("expected 1 TS swallowed catch block, got %d", got)
	}

	pathPrefix := "/" + "Users" + "/alice/project/config.yaml"
	goLeak := []byte("package demo\nconst cfgPath = \"" + pathPrefix + "\"\n")
	if got := CountFileSecretAndPathLeaks("pkg/leak.go", goLeak); got != 1 {
		t.Fatalf("expected 1 host path leak, got %d", got)
	}
	if sec, pth := SplitFileSecretAndPathLeaks("pkg/leak.go", goLeak); sec != 0 || pth != 1 {
		t.Fatalf("expected SplitFileSecretAndPathLeaks=(0,1) for path leak, got (%d,%d)", sec, pth)
	}
	tok := "ghp" + "_" + strings.Repeat("A", 36)
	tokLeak := []byte("package demo\nconst token = \"" + tok + "\"\n")
	if sec, pth := SplitFileSecretAndPathLeaks("pkg/token.go", tokLeak); sec != 1 || pth != 0 {
		t.Fatalf("expected SplitFileSecretAndPathLeaks=(1,0) for token leak, got (%d,%d)", sec, pth)
	}
	if sec, pth := SplitFileSecretAndPathLeaks("pkg/empty.go", nil); sec != 0 || pth != 0 {
		t.Fatalf("expected SplitFileSecretAndPathLeaks=(0,0) for nil input, got (%d,%d)", sec, pth)
	}

	var repeated strings.Builder
	repeated.WriteString("package cluster\nfunc labels() []string {\n\treturn []string{\n")
	for i := 0; i < 12; i++ {
		repeated.WriteString("\t\t\"UNCONFIGURED_MAGIC_TOKEN\",\n")
	}
	repeated.WriteString("\t}\n}\n")
	sources := map[string][]byte{
		"pkg/cluster/a.go": []byte(repeated.String()),
	}
	if groups := CountCorpusMagicStringGroups(sources, DefaultXrayMagicStringThreshold); groups != 1 {
		t.Fatalf("expected 1 magic string cluster, got %d", groups)
	}

	repoRoot := filepath.Clean("../../../..")
	if abs, err := filepath.Abs(repoRoot); err == nil {
		t.Logf("repoRoot=%s", abs)
	}
}
