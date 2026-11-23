package inspectcmd_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgantlett/nomos/substrate/cli"
	"github.com/mgantlett/nomos/substrate/cli/inspectcmd"
)

func seedContractLookupFixtureRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	specDir := filepath.Join(root, "intent", "architecture", "specs")
	subDir := filepath.Join(root, "substrate", "pkg", "demo")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatalf("mkdir specs: %v", err)
	}
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	specMD := "# Demo Engine Specification\n\n## 3. Compiled Go SSoT\n\n```go\npackage demo\n\n// DemoOrder models a verified order (#DemoOrder).\ntype DemoOrder struct {\n\tID string `json:\"id\"`\n}\n```\n\n## 4. Verification Vectors\n\n| Vector ID | Subject | Input | Expected |\n| :--- | :--- | :--- | :--- |\n| `TC-DEMO-01` | `DemoOrder` | invalid payload | Rejects `ERR_DEMO_INVALID` |\n"
	if err := os.WriteFile(filepath.Join(specDir, "demo_engine_spec.md"), []byte(specMD), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	srcGo := "package demo\n// @intent demo_engine_spec.md #DemoOrder\ntype DemoOrder struct { ID string `json:\"id\"` }\nconst ErrDemo = \"ERR_DEMO_INVALID\"\n"
	if err := os.WriteFile(filepath.Join(subDir, "order.go"), []byte(srcGo), 0o644); err != nil {
		t.Fatalf("write order.go: %v", err)
	}
	testGo := "package demo\nimport \"testing\"\n// @verifies TC-DEMO-01\nfunc TestDemoOrder(t *testing.T) {}\n"
	if err := os.WriteFile(filepath.Join(subDir, "order_test.go"), []byte(testGo), 0o644); err != nil {
		t.Fatalf("write order_test.go: %v", err)
	}
	return root
}

// @verifies TC-LAUNCH-005
func TestResolveContractSlice_StructTCAndErrTargets(t *testing.T) {
	repo := seedContractLookupFixtureRepo(t)

	structSlice := inspectcmd.ResolveContractSlice(repo, "#DemoOrder")
	if structSlice == nil || structSlice.Target != "#DemoOrder" || len(structSlice.SpecSHA256) != 64 {
		t.Fatalf("expected #DemoOrder slice with 64-hex SHA256, got %+v", structSlice)
	}
	if !strings.Contains(structSlice.ContractBlock, "type DemoOrder struct") || len(structSlice.Obligations) != 1 {
		t.Fatalf("expected struct snippet and 1 TC row, got %+v", structSlice)
	}
	if len(structSlice.SourceFiles) != 1 || len(structSlice.VerifyingTests) != 1 {
		t.Fatalf("expected 1 source and 1 verifying test, got %+v", structSlice)
	}

	tcSlice := inspectcmd.ResolveContractSlice(repo, "tc-demo-01")
	if tcSlice == nil || tcSlice.Target != "TC-DEMO-01" || len(tcSlice.VerifyingTests) != 1 {
		t.Fatalf("expected TC-DEMO-01 slice with test binding, got %+v", tcSlice)
	}

	errSlice := inspectcmd.ResolveContractSlice(repo, "ERR_DEMO_INVALID")
	if errSlice == nil || errSlice.Target != "ERR_DEMO_INVALID" || len(errSlice.SourceFiles) != 1 {
		t.Fatalf("expected ERR_DEMO_INVALID slice with source binding, got %+v", errSlice)
	}

	fileSlice := inspectcmd.ResolveContractSlice(repo, "demo_engine_spec.md")
	if fileSlice == nil || fileSlice.SectionHeading != "Demo Engine Specification" {
		t.Fatalf("expected spec file slice, got %+v", fileSlice)
	}
}

// @verifies TC-LAUNCH-005
func TestCapabilitiesCmd_ContractDetailCLIAndJSON(t *testing.T) {
	repo := seedContractLookupFixtureRepo(t)
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
	var jsonBuf bytes.Buffer
	root.SetOut(&jsonBuf)
	root.SetArgs([]string{"capabilities", "#DemoOrder", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("capabilities #DemoOrder --json: %v", err)
	}
	var detail inspectcmd.CapabilityDetail
	if err := json.Unmarshal(jsonBuf.Bytes(), &detail); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	if detail.Type != "contract" || detail.Plane != "intent" || detail.Contract == nil {
		t.Fatalf("unexpected contract CapabilityDetail: %+v", detail)
	}
}
