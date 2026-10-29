package testenv

import (
	"fmt"
	"strings"

	"github.com/wenfengwang/cloud-agent-sdk/core/internal/message"
)

// DeltaProblems checks the delta rules of design 001 (Deltas) over one
// turn's events and describes every violation: each event is a message or a
// delta, deltas stream assistant text, the message that completes a stream
// follows all of its deltas and holds their concatenated text, and a stream
// is completed at most once. A stream left incomplete is allowed, because a
// stopped turn may end mid-stream.
func DeltaProblems(events []message.Event) []string {
	var problems []string
	text := map[string]*strings.Builder{}
	completed := map[string]bool{}
	for i, e := range events {
		if (e.Message == nil) == (e.Delta == nil) {
			problems = append(problems, fmt.Sprintf("event %d: exactly one of Message and Delta must be set", i))
			continue
		}
		if d := e.Delta; d != nil {
			if d.Kind != message.KindAssistant {
				problems = append(problems, fmt.Sprintf("event %d: delta of kind %q, want assistant", i, d.Kind))
				continue
			}
			if completed[d.StreamID] {
				problems = append(problems, fmt.Sprintf("event %d: delta of stream %s after its message", i, d.StreamID))
				continue
			}
			if text[d.StreamID] == nil {
				text[d.StreamID] = &strings.Builder{}
			}
			text[d.StreamID].WriteString(d.Text)
			continue
		}
		if e.StreamID == "" {
			continue
		}
		switch {
		case completed[e.StreamID]:
			problems = append(problems, fmt.Sprintf("event %d: stream %s completed twice", i, e.StreamID))
		case text[e.StreamID] == nil:
			problems = append(problems, fmt.Sprintf("event %d: message %s completes stream %s, which has no delta", i, e.Message.ID, e.StreamID))
		case e.Message.Kind != message.KindAssistant || e.Message.Assistant == nil:
			problems = append(problems, fmt.Sprintf("event %d: message %s of kind %q completes stream %s", i, e.Message.ID, e.Message.Kind, e.StreamID))
		case e.Message.Assistant.Text != text[e.StreamID].String():
			problems = append(problems, fmt.Sprintf("event %d: deltas of stream %s add up to %q, message %s has %q", i, e.StreamID, text[e.StreamID].String(), e.Message.ID, e.Message.Assistant.Text))
		}
		completed[e.StreamID] = true
	}
	return problems
}

// MessagesOf returns the messages of events, in order.
func MessagesOf(events []message.Event) []message.Message {
	var msgs []message.Message
	for _, e := range events {
		if e.Message != nil {
			msgs = append(msgs, *e.Message)
		}
	}
	return msgs
}

// StreamCount returns how many delta streams events complete.
func StreamCount(events []message.Event) int {
	n := 0
	for _, e := range events {
		if e.Message != nil && e.StreamID != "" {
			n++
		}
	}
	return n
}
