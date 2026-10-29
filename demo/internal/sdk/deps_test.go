package sdk

import (
	"bufio"
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

const (
	corePrefix = "github.com/wenfengwang/cloud-agent-sdk/core"
	sdkPackage = "github.com/wenfengwang/cloud-agent-sdk/go"
	thisPkg    = "github.com/wenfengwang/cloud-agent-sdk/demo/internal/sdk"
)

// TestImports enforces the demo's dependency rules: no demo package imports
// the SDK's core, and only this package imports the Go SDK.
func TestImports(t *testing.T) {
	out, err := exec.Command("go", "list", "-test", "-f", `{{.ImportPath}} {{join .Imports " "}}`, "github.com/wenfengwang/cloud-agent-sdk/demo/...").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 {
			continue
		}
		pkg := fields[0]
		for _, imp := range fields[1:] {
			if imp == corePrefix || strings.HasPrefix(imp, corePrefix+"/") {
				t.Errorf("%s imports %s; import the Go SDK package instead", pkg, imp)
			}
			if imp == sdkPackage && !strings.HasPrefix(pkg, thisPkg) {
				t.Errorf("%s imports %s; only internal/sdk may", pkg, imp)
			}
		}
	}
}
