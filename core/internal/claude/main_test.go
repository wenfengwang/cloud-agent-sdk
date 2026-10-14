package claude

import (
	"fmt"
	"os"
	"testing"

	"github.com/wenfengwang/cloud-agent-sdk/core/internal/fakeharness"
)

// fakeBin holds the fake claude executable built for this test binary.
var fakeBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "cloudagent-claude-fake-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := fakeharness.Build(dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	fakeBin = dir
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
