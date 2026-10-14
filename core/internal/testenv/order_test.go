package testenv

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/wenfengwang/cloud-agent-sdk/core/internal/message"
)

func TestSameMillisecondOrder(t *testing.T) {
	call := func(id string, raw string) message.Message {
		return message.Message{ID: id, Kind: message.KindToolCall, ToolCall: &message.ToolCall{ID: id}, Raw: json.RawMessage(raw)}
	}
	res := func(id string, raw string) message.Message {
		return message.Message{ID: id + "-r", Kind: message.KindToolResult, ToolResult: &message.ToolResult{CallID: id}, Raw: json.RawMessage(raw)}
	}
	text := message.Message{ID: "m", Kind: message.KindAssistant, Raw: json.RawMessage(`{}`)}
	live := func(ms int64) string {
		return fmt.Sprintf(`{"method":"item/completed","params":{"item":{},"startedAtMs":%d,"completedAtMs":%d}}`, ms, ms)
	}
	rollout := func(ms int64) string {
		return fmt.Sprintf(`{"type":"event_msg","payload":{"started_at_ms":%d,"completed_at_ms":%d}}`, ms, ms)
	}
	a := []message.Message{text, call("b", live(5)), call("a", live(5)), res("b", live(5)), res("a", live(5)), call("c", live(6)), text}
	b := []message.Message{text, call("a", rollout(5)), res("a", rollout(5)), call("b", rollout(5)), res("b", rollout(5)), call("c", rollout(6)), text}
	if got, want := ids(SameMillisecondOrder(a)), ids(SameMillisecondOrder(b)); !reflect.DeepEqual(got, want) {
		t.Errorf("live order %v, read-back order %v", got, want)
	}
	if got := ids(SameMillisecondOrder(a)); !reflect.DeepEqual(got, []string{"m", "a", "b", "a-r", "b-r", "c", "m"}) {
		t.Errorf("order %v", got)
	}
	// A result yielded by item/started (the Codex subagent result) carries
	// only startedAtMs live.
	started := func(ms int64) string {
		return fmt.Sprintf(`{"method":"item/started","params":{"item":{},"startedAtMs":%d}}`, ms)
	}
	completed := func(ms int64) string {
		return fmt.Sprintf(`{"method":"item/completed","params":{"item":{},"completedAtMs":%d}}`, ms)
	}
	e := []message.Message{res("w", started(7)), res("a", completed(7))}
	f := []message.Message{res("a", rollout(7)), res("w", rollout(7))}
	if got, want := ids(SameMillisecondOrder(e)), ids(SameMillisecondOrder(f)); !reflect.DeepEqual(got, want) {
		t.Errorf("live order %v, read-back order %v", got, want)
	}
	// Different milliseconds keep their order.
	c := []message.Message{call("b", live(5)), call("a", live(6))}
	if got := ids(SameMillisecondOrder(c)); !reflect.DeepEqual(got, []string{"b", "a"}) {
		t.Errorf("order %v", got)
	}
	// Messages without a Codex time (Claude Code) keep their order.
	d := []message.Message{call("b", `{}`), call("a", `{}`)}
	if got := ids(SameMillisecondOrder(d)); !reflect.DeepEqual(got, []string{"b", "a"}) {
		t.Errorf("order %v", got)
	}
}

func ids(msgs []message.Message) []string {
	var out []string
	for _, m := range msgs {
		out = append(out, m.ID)
	}
	return out
}
