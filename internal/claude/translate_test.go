package claude

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wenfengwang/cloud-agent-sdk/internal/fakeharness"
	"github.com/wenfengwang/cloud-agent-sdk/internal/message"
	"github.com/wenfengwang/cloud-agent-sdk/internal/testenv"
)

// expectations are the Claude Code structural expectations of design 002,
// checked against the translated messages of every turn of a scenario,
// independently of the golden.
var expectations = map[string]func(t *testing.T, turns [][]message.Message){
	"text": func(t *testing.T, turns [][]message.Message) {
		requireKinds(t, turns[0], message.KindUser, message.KindAssistant)
	},
	"shell": func(t *testing.T, turns [][]message.Message) {
		call := requireCall(t, turns[0], message.ToolNameShell)
		res := requireResult(t, turns[0], call)
		out := res.Output.(*message.ShellOutput)
		if out.ExitCode == nil {
			t.Error("shell result has no exit code")
		}
		if !strings.Contains(out.Output, "cloudagent-shell-check") {
			t.Errorf("shell output = %q", out.Output)
		}
	},
	"read-file": func(t *testing.T, turns [][]message.Message) {
		requireResult(t, turns[0], requireCall(t, turns[0], message.ToolNameFileRead))
	},
	"write-file": func(t *testing.T, turns [][]message.Message) {
		requireResult(t, turns[0], requireCall(t, turns[0], message.ToolNameFileWrite))
	},
	"edit-file": func(t *testing.T, turns [][]message.Message) {
		requireResult(t, turns[0], requireCall(t, turns[0], message.ToolNameFileEdit))
	},
	"glob-grep": func(t *testing.T, turns [][]message.Message) {
		// Claude Code 2.1.300 leaves Glob and Grep out of its default tools,
		// so the model searches with Bash.
		requireResult(t, turns[0], requireCall(t, turns[0], message.ToolNameShell))
	},
	"subagent": func(t *testing.T, turns [][]message.Message) {
		call := requireCall(t, turns[0], message.ToolNameSubagent)
		requireResult(t, turns[0], call)
		inside := 0
		for _, m := range turns[0] {
			if m.ParentToolUseID == call.ID {
				inside++
			}
		}
		if inside == 0 {
			t.Error("no message carries the subagent call as ParentToolUseID")
		}
	},
	"terminate": func(t *testing.T, turns [][]message.Message) {
		call := requireCall(t, turns[0], message.ToolNameShell)
		if !strings.Contains(call.Input.(*message.ShellInput).Command, "sleep 30") {
			t.Errorf("shell command = %q", call.Input.(*message.ShellInput).Command)
		}
	},
	"two-turn": func(t *testing.T, turns [][]message.Message) {
		if len(turns) != 2 {
			t.Fatalf("got %d turns, want 2", len(turns))
		}
		for _, turn := range turns {
			requireKinds(t, turn, message.KindUser, message.KindAssistant)
		}
		if got := finalText(turns[1]); !strings.Contains(strings.ToLower(got), "marmalade") {
			t.Errorf("second turn final text = %q", got)
		}
	},
}

// TestTranslate feeds each fixture's recorded stdout lines through the
// translation, compares the messages with messages.golden.json, and checks the
// scenario's structural expectations.
func TestTranslate(t *testing.T) {
	for _, s := range testenv.Scenarios() {
		t.Run(s.Name, func(t *testing.T) {
			dir := filepath.Join("testdata", s.Name)
			rec := readFixture(t, dir)
			var turns [][]message.Message
			var all []message.Message
			for _, traffic := range rec.Traffic {
				msgs := translateTraffic(t, traffic)
				turns = append(turns, msgs)
				all = append(all, msgs...)
			}
			testenv.Golden(t, filepath.Join(dir, "messages.golden.json"), all)
			check, ok := expectations[s.Name]
			if !ok {
				t.Fatalf("no structural expectations for scenario %s", s.Name)
			}
			check(t, turns)
			for _, m := range all {
				requireWellFormed(t, m)
			}
		})
	}
}

func readFixture(t *testing.T, dir string) testenv.Recorded {
	t.Helper()
	rec, err := testenv.ReadFixture(dir)
	if err != nil {
		t.Fatal(err)
	}
	testenv.RequirePinnedFixture(t, rec)
	if rec.Meta.Harness != "claude" {
		t.Fatalf("fixture %s is for harness %q", dir, rec.Meta.Harness)
	}
	return rec
}

func translateTraffic(t *testing.T, traffic []fakeharness.TrafficLine) []message.Message {
	t.Helper()
	translator := newTranslator()
	var out []message.Message
	for _, l := range traffic {
		if l.Dir != fakeharness.DirRecv {
			continue
		}
		msgs, err := translator.streamLine(l.Line)
		if err != nil {
			t.Fatalf("translate %s: %v", l.Line, err)
		}
		out = append(out, msgs...)
	}
	return out
}

func finalText(msgs []message.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if m := msgs[i]; m.Kind == message.KindAssistant && m.ParentToolUseID == "" {
			return m.Assistant.Text
		}
	}
	return ""
}

func requireKinds(t *testing.T, msgs []message.Message, kinds ...message.Kind) {
	t.Helper()
	if len(msgs) == 0 || msgs[0].Kind != message.KindUser {
		t.Fatalf("the first message is not the user message: %+v", msgs)
	}
	for _, k := range kinds {
		found := false
		for _, m := range msgs {
			found = found || m.Kind == k
		}
		if !found {
			t.Errorf("no %s message", k)
		}
	}
}

func requireCall(t *testing.T, msgs []message.Message, name message.ToolName) *message.ToolCall {
	t.Helper()
	for _, m := range msgs {
		if m.ToolCall != nil && m.ToolCall.Name == name {
			return m.ToolCall
		}
	}
	t.Fatalf("no %s tool call", name)
	return nil
}

func requireResult(t *testing.T, msgs []message.Message, call *message.ToolCall) *message.ToolResult {
	t.Helper()
	for _, m := range msgs {
		if m.ToolResult != nil && m.ToolResult.CallID == call.ID {
			if m.ToolResult.Name != call.Name {
				t.Errorf("result name %s, call name %s", m.ToolResult.Name, call.Name)
			}
			return m.ToolResult
		}
	}
	t.Fatalf("no result for %s call %s", call.Name, call.ID)
	return nil
}

// requireWellFormed checks the invariants every canonical message holds.
func requireWellFormed(t *testing.T, m message.Message) {
	t.Helper()
	set := 0
	for _, p := range []bool{m.User != nil, m.Assistant != nil, m.Reasoning != nil, m.ToolCall != nil, m.ToolResult != nil} {
		if p {
			set++
		}
	}
	if set != 1 {
		t.Errorf("message %s sets %d payloads", m.ID, set)
	}
	if m.ID == "" || m.TurnID != "" || !json.Valid(m.Raw) {
		t.Errorf("message %q: TurnID %q, valid Raw %v", m.ID, m.TurnID, json.Valid(m.Raw))
	}
	switch m.Kind {
	case message.KindToolCall:
		if !inputMatches(m.ToolCall.Name, m.ToolCall.Input) {
			t.Errorf("call %s: input %T does not match %s", m.ID, m.ToolCall.Input, m.ToolCall.Name)
		}
	case message.KindToolResult:
		if !outputMatches(m.ToolResult.Name, m.ToolResult.Output) {
			t.Errorf("result %s: output %T does not match %s", m.ID, m.ToolResult.Output, m.ToolResult.Name)
		}
	}
}

func inputMatches(name message.ToolName, in message.ToolInput) bool {
	switch in.(type) {
	case *message.ShellInput:
		return name == message.ToolNameShell
	case *message.FileReadInput:
		return name == message.ToolNameFileRead
	case *message.FileWriteInput:
		return name == message.ToolNameFileWrite
	case *message.FileEditInput:
		return name == message.ToolNameFileEdit
	case *message.GlobInput:
		return name == message.ToolNameGlob
	case *message.GrepInput:
		return name == message.ToolNameGrep
	case *message.WebFetchInput:
		return name == message.ToolNameWebFetch
	case *message.WebSearchInput:
		return name == message.ToolNameWebSearch
	case *message.MCPInput:
		return name == message.ToolNameMCP
	case *message.SubagentInput:
		return name == message.ToolNameSubagent
	case *message.UnknownInput:
		return name == message.ToolNameUnknown
	}
	return false
}

func outputMatches(name message.ToolName, out message.ToolOutput) bool {
	switch out.(type) {
	case *message.ShellOutput:
		return name == message.ToolNameShell
	case *message.FileReadOutput:
		return name == message.ToolNameFileRead
	case *message.MCPOutput:
		return name == message.ToolNameMCP
	case *message.UnknownOutput:
		return name == message.ToolNameUnknown
	case *message.TextOutput:
		switch name {
		case message.ToolNameShell, message.ToolNameFileRead, message.ToolNameMCP, message.ToolNameUnknown:
			return false
		}
		return true
	}
	return false
}
