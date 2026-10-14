package cloudagent_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/wenfengwang/cloud-agent-sdk/internal/fakeharness"
)

// fakeBin holds the fake claude and codex executables built for this test
// binary; lifecycle tests put it on PATH with fakeharness.Use.
var fakeBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "cloudagent-fake-")
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
