package codex

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/wenfengwang/cloud-agent-sdk/core/internal/harness"
	"github.com/wenfengwang/cloud-agent-sdk/core/internal/message"
	"github.com/wenfengwang/cloud-agent-sdk/core/internal/testenv"
)

// TestReadBack reads every fixture turn back from the fixture's rollout
// files and compares it with the live translation. The only expected
// differences are the fields holding harness JSON (see withoutRaw), which
// hold the rollout's JSON instead of the app-server's.
func TestReadBack(t *testing.T) {
	for _, dir := range fixtureDirs(t) {
		name := filepath.Base(dir)
		t.Run(name, func(t *testing.T) {
			rec := readFixture(t, dir)
			testenv.Isolate(t)
			home, workDir := t.TempDir(), t.TempDir()
			values := map[string]string{
				testenv.PlaceholderHarnessHome: home,
				testenv.PlaceholderWorkDir:     workDir,
				testenv.PlaceholderHome:        t.TempDir(),
			}
			testenv.CopyHome(t, dir, home, values)
			// The live side sees the same paths as the copied home.
			for _, lines := range rec.Traffic {
				for j := range lines {
					lines[j].Line = json.RawMessage(testenv.Substitute(string(lines[j].Line), values))
				}
			}
			h := New(harness.Config{WorkDir: workDir, Home: home})
			for i, recorded := range fixtureTurns(t, rec) {
				data, _ := json.Marshal(map[string]string{"thread_id": recorded.thread, "turn_id": recorded.turn})
				got, err := h.ReadTurn(context.Background(), harness.Ref{SessionID: recorded.thread, Data: data})
				if err != nil {
					t.Fatalf("turn %d: %v", i+1, err)
				}
				for _, m := range got {
					if len(m.Raw) == 0 || m.TurnID != "" {
						t.Errorf("read-back message %s: empty Raw or TurnID set", m.ID)
					}
				}
				// Tool items of one millisecond may be read back in another
				// order (design 001).
				got, live := testenv.SameMillisecondOrder(got), testenv.SameMillisecondOrder(recorded.msgs)
				if want := withoutRaw(live); !reflect.DeepEqual(withoutRaw(got), want) {
					t.Fatalf("turn %d read back as\n%s\nlive\n%s", i+1, kinds(got), kinds(live))
				}
			}
		})
	}
}

func TestReadTurnErrors(t *testing.T) {
	testenv.Isolate(t)
	h := New(harness.Config{WorkDir: t.TempDir(), Home: t.TempDir()})
	if _, err := h.ReadTurn(context.Background(), harness.Ref{Data: json.RawMessage(`{}`)}); err == nil {
		t.Error("ReadTurn accepted an empty reference")
	}
	ref := harness.Ref{SessionID: "t", Data: json.RawMessage(`{"thread_id":"t","turn_id":"u"}`)}
	if _, err := h.ReadTurn(context.Background(), ref); err == nil {
		t.Error("ReadTurn found a rollout in an empty home")
	}
}

// withoutRaw clears the fields that hold the harness's own JSON: Raw, and
// the Raw of unknown tool inputs and outputs. A live run holds app-server
// JSON there, a read-back the rollout's.
func withoutRaw(msgs []message.Message) []message.Message {
	out := make([]message.Message, len(msgs))
	for i, m := range msgs {
		m.Raw = nil
		if c := m.ToolCall; c != nil {
			if _, ok := c.Input.(*message.UnknownInput); ok {
				cc := *c
				cc.Input = &message.UnknownInput{}
				m.ToolCall = &cc
			}
		}
		if r := m.ToolResult; r != nil {
			if _, ok := r.Output.(*message.UnknownOutput); ok {
				rc := *r
				rc.Output = &message.UnknownOutput{}
				m.ToolResult = &rc
			}
		}
		out[i] = m
	}
	return out
}

// TestOrderEvents: events are ordered by time, then by rollout position,
// except that within one millisecond tool starts (calls) come before tool
// completions (results); other items keep their place (design 001).
func TestOrderEvents(t *testing.T) {
	ev := func(at int64, seq int, started bool, typ, id string) rolloutEvent {
		return rolloutEvent{at: at, seq: seq, started: started, it: item{Type: typ, ID: id}}
	}
	// Two parallel commands a and b start and complete in millisecond 5;
	// the rollout holds a's completion first, so a's start and completion
	// precede b's start by position.
	events := []rolloutEvent{
		ev(5, 3, true, itemCommand, "b"),
		ev(9, 6, false, itemAgentMessage, "m2"),
		ev(5, 0, true, itemCommand, "a"),
		ev(5, 1, false, itemCommand, "a"),
		ev(5, 4, false, itemCommand, "b"),
		ev(1, 2, false, itemAgentMessage, "m1"),
		ev(5, 5, false, itemAgentMessage, "m"),
	}
	orderEvents(events)
	var got []string
	for _, e := range events {
		kind := "done"
		if e.started {
			kind = "start"
		}
		got = append(got, e.it.ID+":"+kind)
	}
	want := []string{"m1:done", "a:start", "b:start", "a:done", "b:done", "m:done", "m2:done"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order %v, want %v", got, want)
	}
}
