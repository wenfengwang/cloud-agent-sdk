package cloudagent_test

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	cloudagent "github.com/wenfengwang/cloud-agent-sdk"
	"github.com/wenfengwang/cloud-agent-sdk/internal/fakeharness"
	"github.com/wenfengwang/cloud-agent-sdk/internal/testenv"
)

// TestConcurrentConsumers starts several Messages consumers at different
// points of a paced replay, all before the turn ends; every one sees the
// whole turn from its first message.
func TestConcurrentConsumers(t *testing.T) {
	for _, h := range harnesses {
		t.Run(string(h), func(t *testing.T) {
			e := newReplay(t, h, "shell", func(_ *replayEnv, invs []fakeharness.Invocation) []fakeharness.Invocation {
				for i := range invs {
					invs[i].PaceMs = 60 // the replay outlasts the last consumer's delay
				}
				return invs
			})
			want := goldenIDs(e.goldenTurns(t)[0])
			a := e.newAgent(t)
			turn, err := a.Turn(context.Background(), e.rec.Meta.Prompts[0], cloudagent.TurnOptions{})
			if err != nil {
				t.Fatalf("Turn: %v", err)
			}
			delays := []time.Duration{0, 0, 30 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond}
			seen := make([][]cloudagent.Message, len(delays))
			var wg sync.WaitGroup
			for i, d := range delays {
				wg.Add(1)
				go func() {
					defer wg.Done()
					time.Sleep(d)
					for m, err := range turn.Messages() {
						if err != nil {
							t.Errorf("consumer %d: %v", i, err)
							return
						}
						seen[i] = append(seen[i], m)
					}
				}()
			}
			wg.Wait()
			if _, err := result(t, turn); err != nil {
				t.Fatalf("Result: %v", err)
			}
			for i, msgs := range seen {
				if got := messageIDs(msgs); !slices.Equal(got, want) {
					t.Errorf("consumer %d saw %v, want %v", i, got, want)
				}
				if !reflect.DeepEqual(msgs, seen[0]) {
					t.Errorf("consumer %d saw different messages than consumer 0", i)
				}
			}
		})
	}
}

// scenarioRun is one scenario replayed to its end through the public API.
type scenarioRun struct {
	agent *cloudagent.AgentRuntime
	turns []*cloudagent.Turn
	live  [][]cloudagent.Message // the messages of each turn, read while it ran
}

// runScenario replays every prompt of the replay's scenario, each turn to
// completion.
func runScenario(t *testing.T, e *replayEnv) scenarioRun {
	t.Helper()
	run := scenarioRun{agent: e.newAgent(t)}
	for i, prompt := range e.rec.Meta.Prompts {
		turn, err := run.agent.Turn(context.Background(), prompt, cloudagent.TurnOptions{})
		if err != nil {
			t.Fatalf("turn %d: Turn: %v", i+1, err)
		}
		live := collect(t, turn, nil)
		if _, err := result(t, turn); err != nil {
			t.Fatalf("turn %d: Result: %v", i+1, err)
		}
		run.turns = append(run.turns, turn)
		run.live = append(run.live, live)
	}
	return run
}

// completedScenarios are the scenarios whose turns all run to completion.
func completedScenarios() []string {
	var out []string
	for _, s := range testenv.Scenarios() {
		if s.TerminateWhen == nil {
			out = append(out, s.Name)
		}
	}
	return out
}

// TestReadBackThroughAPI: once a turn has ended, Messages reads it back from
// the harness home and yields the live sequence, apart from the differences
// design 001 lists.
func TestReadBackThroughAPI(t *testing.T) {
	for _, h := range harnesses {
		for _, scenario := range completedScenarios() {
			t.Run(string(h)+"/"+scenario, func(t *testing.T) {
				e := newReplay(t, h, scenario, nil)
				run := runScenario(t, e)
				extra := 0
				for i, turn := range run.turns {
					back := collect(t, turn, nil)
					for _, m := range back {
						if len(m.Raw) == 0 {
							t.Errorf("turn %d: read-back message %s has no Raw", i+1, m.ID)
						}
					}
					back, n := withoutUnstreamed(t, h, back, run.live[i])
					extra += n
					// Codex tool items of one millisecond may be read back in
					// another order (design 001).
					back, live := testenv.SameMillisecondOrder(back), testenv.SameMillisecondOrder(run.live[i])
					// Message IDs are stable between live and read-back.
					if got, want := messageIDs(back), messageIDs(live); !slices.Equal(got, want) {
						t.Errorf("turn %d: read-back message IDs %v, want the live IDs %v", i+1, got, want)
					}
					if got, want := comparable(t, back), comparable(t, live); got != want {
						t.Errorf("turn %d: read back\n%s\nwant\n%s", i+1, got, want)
					}
				}
				// The subagent's final assistant message is the one message
				// Claude Code persists but never streams.
				if wantExtra := h == cloudagent.ClaudeCode && scenario == "subagent"; wantExtra != (extra > 0) {
					t.Errorf("read back %d messages that were not streamed", extra)
				}
			})
		}
	}
}

// withoutUnstreamed drops from back the messages the live turn did not
// stream, which design 001 allows only for Claude Code's subagent final
// assistant message (its text and any thinking), and reports how many it
// dropped.
func withoutUnstreamed(t *testing.T, h cloudagent.Harness, back, live []cloudagent.Message) ([]cloudagent.Message, int) {
	t.Helper()
	streamed := map[string]bool{}
	for _, m := range live {
		streamed[m.ID] = true
	}
	var out []cloudagent.Message
	for _, m := range back {
		if streamed[m.ID] {
			out = append(out, m)
			continue
		}
		subagentFinal := m.ParentToolUseID != "" && (m.Kind == cloudagent.KindAssistant || m.Kind == cloudagent.KindReasoning)
		if h != cloudagent.ClaudeCode || !subagentFinal {
			t.Errorf("read back message %s (%s) was not streamed", m.ID, m.Kind)
		}
	}
	return out, len(back) - len(out)
}

// comparable encodes msgs without the fields design 001 lets differ between
// a live read and a read-back: Raw, and the Raw of unknown tool inputs and
// outputs.
func comparable(t *testing.T, msgs []cloudagent.Message) string {
	t.Helper()
	out := make([]cloudagent.Message, len(msgs))
	for i, m := range msgs {
		m.Raw = nil
		if c := m.ToolCall; c != nil {
			if _, ok := c.Input.(*cloudagent.UnknownInput); ok {
				cc := *c
				cc.Input = &cloudagent.UnknownInput{}
				m.ToolCall = &cc
			}
		}
		if r := m.ToolResult; r != nil {
			if _, ok := r.Output.(*cloudagent.UnknownOutput); ok {
				rc := *r
				rc.Output = &cloudagent.UnknownOutput{}
				m.ToolResult = &rc
			}
		}
		out[i] = m
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
