package cloudagent_test

import (
	"context"
	"slices"
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

// TestTurnCompletes replays the text scenario through New: the turn ends as
// completed with the recorded messages, final text, and usage.
func TestTurnCompletes(t *testing.T) {
	for _, h := range harnesses {
		t.Run(string(h), func(t *testing.T) {
			e := newReplay(t, h, "text", nil)
			want := e.goldenTurns(t)[0]
			a := e.newAgent(t)
			turn, err := a.Turn(context.Background(), e.rec.Meta.Prompts[0], cloudagent.TurnOptions{})
			if err != nil {
				t.Fatalf("Turn: %v", err)
			}
			msgs := collect(t, turn, nil)
			res, err := result(t, turn)
			if err != nil {
				t.Fatalf("Result: %v", err)
			}
			if got := messageIDs(msgs); !slices.Equal(got, goldenIDs(want)) {
				t.Errorf("message IDs %v, want %v", got, goldenIDs(want))
			}
			if res.TurnID != turn.ID() || res.FinalText != goldenFinalText(want) || res.FinalText == "" {
				t.Errorf("Result %+v, want final text %q", res, goldenFinalText(want))
			}
			if res.Usage.OutputTokens == 0 {
				t.Errorf("Result has no usage: %+v", res.Usage)
			}
			e.requireStatus(t, a, turn, "completed")
		})
	}
}
