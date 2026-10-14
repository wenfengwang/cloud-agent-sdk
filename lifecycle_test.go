package cloudagent_test

import (
	"context"
	"testing"

	cloudagent "github.com/wenfengwang/cloud-agent-sdk"
	"github.com/wenfengwang/cloud-agent-sdk/internal/fakeharness"
)

func TestTurnFailsWhenCLIIsMissing(t *testing.T) {
	for _, h := range []cloudagent.Harness{cloudagent.Claude, cloudagent.OpenAI} {
		t.Run(string(h), func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			state := fakeharness.Use(t, t.TempDir(), fakeharness.Script{})
			a, err := cloudagent.New(h, cloudagent.Options{WorkDir: t.TempDir(), Home: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			if _, err := a.Turn(context.Background(), "hello", cloudagent.TurnOptions{}); err == nil {
				t.Fatal("Turn succeeded without a harness CLI on PATH")
			}
			if n := state.Invocations(t); n != 0 {
				t.Fatalf("%d harness processes started", n)
			}
		})
	}
}
