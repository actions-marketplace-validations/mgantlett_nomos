package inspectcmd_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/cli"
	"github.com/mgantlett/nomos/substrate/cli/inspectcmd"
)

func TestResolveCapabilityDetailInDir_TextCardAndNotFound(t *testing.T) {
	repo := seedContractLookupFixtureRepo(t)
	detail, err := inspectcmd.ResolveCapabilityDetailInDir(repo, "#DemoOrder")
	if err != nil || detail == nil || detail.Status != "GROUNDED" {
		t.Fatalf("expected GROUNDED detail via ResolveCapabilityDetailInDir, got %+v (err=%v)", detail, err)
	}
	if _, missErr := inspectcmd.ResolveCapabilityDetailInDir(repo, "#MissingGhostStruct"); missErr == nil {
		t.Fatalf("expected error on unknown contract symbol")
	}

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		if chErr := os.Chdir(origWD); chErr != nil {
			t.Logf("restore wd: %v", chErr)
		}
	})

	root := cli.NewRootCmd()
	var textBuf bytes.Buffer
	root.SetOut(&textBuf)
	root.SetArgs([]string{"capabilities", "#DemoOrder"})
	if err := root.Execute(); err != nil {
		t.Fatalf("capabilities #DemoOrder text render: %v", err)
	}
	if !strings.Contains(textBuf.String(), "demo_engine_spec.md") || !strings.Contains(textBuf.String(), "order_test.go") {
		t.Fatalf("expected spec and test paths in card output:\n%s", textBuf.String())
	}
}
