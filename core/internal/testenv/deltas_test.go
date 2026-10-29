package testenv

import (
	"strings"
	"testing"

	"github.com/wenfengwang/cloud-agent-sdk/core/internal/message"
)

func delta(stream, text string) message.Event {
	return message.Event{Delta: &message.Delta{StreamID: stream, Kind: message.KindAssistant, Text: text}}
}

func completes(stream, id, text string) message.Event {
	return message.Event{StreamID: stream, Message: &message.Message{ID: id, Kind: message.KindAssistant, Assistant: &message.Text{Text: text}}}
}

func plain(id string) message.Event {
	return message.Event{Message: &message.Message{ID: id, Kind: message.KindUser, User: &message.Text{Text: "hi"}}}
}

func TestDeltaProblems(t *testing.T) {
	for name, tc := range map[string]struct {
		events []message.Event
		want   string // substring of the only problem; empty for none
	}{
		"valid":                    {[]message.Event{plain("u"), delta("s", "Hel"), delta("s", "lo"), completes("s", "a", "Hello")}, ""},
		"unstreamed message":       {[]message.Event{plain("u"), completes("", "a", "Hello")}, ""},
		"text mismatch":            {[]message.Event{delta("s", "Hel"), completes("s", "a", "Help")}, "deltas of stream s"},
		"delta after completion":   {[]message.Event{delta("s", "Hi"), completes("s", "a", "Hi"), delta("s", "!")}, "after its message"},
		"completes unknown stream": {[]message.Event{completes("s", "a", "Hi")}, "no delta"},
		"completed twice":          {[]message.Event{delta("s", "Hi"), completes("s", "a", "Hi"), completes("s", "b", "Hi")}, "completed twice"},
		"both set":                 {[]message.Event{{Message: &message.Message{ID: "x"}, Delta: &message.Delta{StreamID: "s"}}}, "exactly one"},
		"delta kind":               {[]message.Event{{Delta: &message.Delta{StreamID: "s", Kind: message.KindReasoning}}}, "kind"},
		"unfinished stream":        {[]message.Event{delta("s", "Hi")}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			problems := DeltaProblems(tc.events)
			if tc.want == "" {
				if len(problems) != 0 {
					t.Fatalf("problems = %q, want none", problems)
				}
				return
			}
			if len(problems) != 1 || !strings.Contains(problems[0], tc.want) {
				t.Fatalf("problems = %q, want one containing %q", problems, tc.want)
			}
		})
	}
}

func TestMessagesOf(t *testing.T) {
	got := MessagesOf([]message.Event{plain("u"), delta("s", "Hi"), completes("s", "a", "Hi")})
	if len(got) != 2 || got[0].ID != "u" || got[1].ID != "a" {
		t.Fatalf("MessagesOf = %+v", got)
	}
}
