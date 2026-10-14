package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/wenfengwang/cloud-agent-sdk/core/internal/fakeharness"
	"github.com/wenfengwang/cloud-agent-sdk/core/internal/harness"
	"github.com/wenfengwang/cloud-agent-sdk/core/internal/message"
	"github.com/wenfengwang/cloud-agent-sdk/core/internal/testenv"
)

// recordedTurn is one turn of a fixture, translated from its recv lines by
// the same stream a live run uses.
type recordedTurn struct {
	thread, turn string
	status       string // the turn/completed status, empty if none
	msgs         []message.Message
	usage        harness.Usage
	err          error
}

// fixtureTurns translates every turn of a fixture, in order. The turns of
// each process are found from its turn/start responses.
func fixtureTurns(t *testing.T, rec testenv.Recorded) []recordedTurn {
	t.Helper()
	var out []recordedTurn
	for _, lines := range rec.Traffic {
		thread, turns := threadAndTurns(t, lines)
		for _, turn := range turns {
			stream := newStream(thread, turn)
			recorded := recordedTurn{thread: thread, turn: turn}
			for _, l := range lines {
				if l.Dir != fakeharness.DirRecv {
					continue
				}
				msgs, done := stream.handle(l.Line)
				recorded.msgs = append(recorded.msgs, msgs...)
				if done {
					recorded.status = turnStatus(l.Line)
					break
				}
			}
			recorded.usage, recorded.err = stream.usage, stream.err
			out = append(out, recorded)
		}
	}
	return out
}

// threadAndTurns finds the thread (from the thread/start or thread/resume
// response) and the turns (from the turn/start responses) of one process.
func threadAndTurns(t *testing.T, lines []fakeharness.TrafficLine) (string, []string) {
	t.Helper()
	methods := map[string]string{}
	var thread string
	var turns []string
	for _, l := range lines {
		var m wireMessage
		if err := json.Unmarshal(l.Line, &m); err != nil {
			t.Fatalf("traffic line: %v", err)
		}
		if l.Dir == fakeharness.DirSend && m.Method != "" && len(m.ID) > 0 {
			methods[string(m.ID)] = m.Method
			continue
		}
		if l.Dir != fakeharness.DirRecv || m.Method != "" || len(m.ID) == 0 {
			continue
		}
		var resp struct {
			Thread struct{ ID string } `json:"thread"`
			Turn   struct{ ID string } `json:"turn"`
		}
		_ = json.Unmarshal(m.Result, &resp)
		switch methods[string(m.ID)] {
		case "thread/start", "thread/resume":
			thread = resp.Thread.ID
		case "turn/start":
			turns = append(turns, resp.Turn.ID)
		}
	}
	if thread == "" || len(turns) == 0 {
		t.Fatal("traffic has no thread or no turn")
	}
	return thread, turns
}

func turnStatus(line []byte) string {
	var n notification
	_ = json.Unmarshal(line, &n)
	if n.Params.Turn == nil {
		return ""
	}
	return n.Params.Turn.Status
}

func fixtureDirs(t *testing.T) []string {
	t.Helper()
	var dirs []string
	for _, s := range testenv.Scenarios() {
		dir := filepath.Join("testdata", s.Name)
		if _, err := os.Stat(filepath.Join(dir, "meta.json")); err != nil {
			t.Fatalf("scenario %s has no fixture (record it with CLOUDAGENT_RECORD=1): %v", s.Name, err)
		}
		dirs = append(dirs, dir)
	}
	return dirs
}

func readFixture(t *testing.T, dir string) testenv.Recorded {
	t.Helper()
	rec, err := testenv.ReadFixture(dir)
	if err != nil {
		t.Fatal(err)
	}
	testenv.RequirePinnedFixture(t, rec)
	if rec.Meta.Harness != "codex" {
		t.Fatalf("%s: fixture of harness %q", dir, rec.Meta.Harness)
	}
	return rec
}

// TestTranslate compares every fixture's translation with its golden and
// checks the scenario's structural expectations.
func TestTranslate(t *testing.T) {
	for _, dir := range fixtureDirs(t) {
		name := filepath.Base(dir)
		t.Run(name, func(t *testing.T) {
			rec := readFixture(t, dir)
			turns := fixtureTurns(t, rec)
			var all []message.Message
			for _, recorded := range turns {
				all = append(all, recorded.msgs...)
			}
			testenv.Golden(t, filepath.Join(dir, "messages.golden.json"), goldenView(all))
			checkCommon(t, rec, turns)
			expect, ok := expectations[name]
			if !ok {
				t.Fatalf("scenario %s has no Codex expectations", name)
			}
			expect(t, turns)
		})
	}
}

// checkCommon checks what every Codex fixture must satisfy.
func checkCommon(t *testing.T, rec testenv.Recorded, turns []recordedTurn) {
	t.Helper()
	if len(turns) != len(rec.Meta.Prompts) {
		t.Fatalf("%d turns for %d prompts", len(turns), len(rec.Meta.Prompts))
	}
	ids := map[string]bool{}
	for i, recorded := range turns {
		if recorded.status == "" {
			t.Fatalf("turn %d never completed", i+1)
		}
		if recorded.status != turnInterrupted && recorded.err != nil {
			t.Fatalf("turn %d: %v", i+1, recorded.err)
		}
		if len(recorded.msgs) == 0 || recorded.msgs[0].Kind != message.KindUser || recorded.msgs[0].User.Text != rec.Meta.Prompts[i] {
			t.Fatalf("turn %d does not start with its prompt: %s", i+1, kinds(recorded.msgs))
		}
		if recorded.status == turnCompleted && (recorded.usage.InputTokens == 0 || recorded.usage.OutputTokens == 0) {
			t.Errorf("turn %d: usage %+v", i+1, recorded.usage)
		}
		if want := reportedUsage(t, rec, recorded); recorded.usage != want {
			t.Errorf("turn %d: usage %+v, want %+v", i+1, recorded.usage, want)
		}
		calls := map[string]message.ToolName{}
		for _, m := range recorded.msgs {
			checkShape(t, m)
			if ids[m.ID] {
				t.Errorf("duplicate message ID %s", m.ID)
			}
			ids[m.ID] = true
			// Only messages of a subagent, started earlier in the turn,
			// carry a ParentToolUseID.
			if p := m.ParentToolUseID; p != "" && calls[p] != message.ToolNameSubagent {
				t.Errorf("message %s has ParentToolUseID %q, which is no earlier subagent call", m.ID, p)
			}
			if m.ToolCall != nil {
				calls[m.ToolCall.ID] = m.ToolCall.Name
			}
			if r := m.ToolResult; r != nil {
				if name, ok := calls[r.CallID]; !ok || name != r.Name {
					t.Errorf("tool result %s has no earlier call named %s", r.CallID, r.Name)
				}
			}
		}
	}
	// Configuration warnings were reported and caused neither a message
	// nor a failure.
	if !hasMethod(rec, "configWarning") && !hasMethod(rec, "warning") {
		t.Error("the fixture has no configuration warning")
	}
}

// checkShape checks that exactly the payload matching Kind is set and that
// tool payloads use the types of design 001.
func checkShape(t *testing.T, m message.Message) {
	t.Helper()
	set := map[message.Kind]bool{
		message.KindUser:       m.User != nil,
		message.KindAssistant:  m.Assistant != nil,
		message.KindReasoning:  m.Reasoning != nil,
		message.KindToolCall:   m.ToolCall != nil,
		message.KindToolResult: m.ToolResult != nil,
	}
	for kind, ok := range set {
		if ok != (kind == m.Kind) {
			t.Errorf("message %s of kind %s: %s payload set = %v", m.ID, m.Kind, kind, ok)
		}
	}
	if m.ID == "" || len(m.Raw) == 0 || m.TurnID != "" {
		t.Errorf("message %+v: empty ID or Raw, or TurnID set", m)
	}
	if c := m.ToolCall; c != nil {
		want := map[message.ToolName]any{
			message.ToolNameShell:     &message.ShellInput{},
			message.ToolNameFilePatch: &message.FilePatchInput{},
			message.ToolNameMCP:       &message.MCPInput{},
			message.ToolNameWebSearch: &message.WebSearchInput{},
			message.ToolNameSubagent:  &message.SubagentInput{},
			message.ToolNameUnknown:   &message.UnknownInput{},
		}[c.Name]
		if want == nil || reflect.TypeOf(c.Input) != reflect.TypeOf(want) {
			t.Errorf("tool call %s %s has input %T", c.ID, c.Name, c.Input)
		}
	}
}

// reportedUsage sums the per-request token counts Codex reported for the
// turn's own thread, normalized as design 001 states: InputTokens excludes
// cached input.
func reportedUsage(t *testing.T, rec testenv.Recorded, recorded recordedTurn) harness.Usage {
	t.Helper()
	var u harness.Usage
	for _, lines := range rec.Traffic {
		for _, l := range lines {
			var n struct {
				Method string `json:"method"`
				Params struct {
					ThreadID   string `json:"threadId"`
					TurnID     string `json:"turnId"`
					TokenUsage struct {
						Last struct {
							InputTokens, CachedInputTokens, CacheWriteInputTokens, OutputTokens int64
						} `json:"last"`
					} `json:"tokenUsage"`
				} `json:"params"`
			}
			if l.Dir != fakeharness.DirRecv || json.Unmarshal(l.Line, &n) != nil || n.Method != "thread/tokenUsage/updated" {
				continue
			}
			if n.Params.ThreadID != recorded.thread || n.Params.TurnID != recorded.turn {
				continue
			}
			last := n.Params.TokenUsage.Last
			u.InputTokens += last.InputTokens - last.CachedInputTokens
			u.CacheReadTokens += last.CachedInputTokens
			u.CacheWriteTokens += last.CacheWriteInputTokens
			u.OutputTokens += last.OutputTokens
		}
	}
	return u
}

func hasMethod(rec testenv.Recorded, method string) bool {
	for _, lines := range rec.Traffic {
		for _, l := range lines {
			var m wireMessage
			if json.Unmarshal(l.Line, &m) == nil && l.Dir == fakeharness.DirRecv && m.Method == method {
				return true
			}
		}
	}
	return false
}

// expectations are the Codex column of design 002's scenario table.
var expectations = map[string]func(t *testing.T, turns []recordedTurn){
	"text": func(t *testing.T, turns []recordedTurn) {
		requireKinds(t, turns[0].msgs, message.KindUser, message.KindAssistant)
		requireFinalText(t, turns[0], "quick brown fox")
	},
	"shell": func(t *testing.T, turns []recordedTurn) {
		_, res := requireShell(t, turns[0].msgs, func(cmd string) bool { return cmd == "echo cloudagent-shell-check" })
		if res.ExitCode == nil || *res.ExitCode != 0 || !strings.Contains(res.Output, "cloudagent-shell-check") {
			t.Errorf("shell result = %+v", res)
		}
	},
	"read-file": func(t *testing.T, turns []recordedTurn) {
		_, res := requireShell(t, turns[0].msgs, func(cmd string) bool { return strings.Contains(cmd, "notes.txt") })
		if !strings.Contains(res.Output, "bravo line") {
			t.Errorf("read result = %q", res.Output)
		}
	},
	"write-file": func(t *testing.T, turns []recordedTurn) {
		requireFileWrite(t, turns[0].msgs, "created.txt", "add")
	},
	"edit-file": func(t *testing.T, turns []recordedTurn) {
		requireFileWrite(t, turns[0].msgs, "greeting.txt", "update")
	},
	"glob-grep": func(t *testing.T, turns []recordedTurn) {
		n := 0
		for _, m := range turns[0].msgs {
			if m.ToolCall != nil && m.ToolCall.Name == message.ToolNameShell {
				n++
			}
		}
		if n < 2 {
			t.Errorf("%d shell calls, want at least 2: %s", n, kinds(turns[0].msgs))
		}
	},
	"subagent": func(t *testing.T, turns []recordedTurn) {
		msgs := turns[0].msgs
		var call *message.ToolCall
		for _, m := range msgs {
			if m.ToolCall != nil && m.ToolCall.Name == message.ToolNameSubagent {
				call = m.ToolCall
				break
			}
		}
		if call == nil || call.RawName != itemSubAgent {
			t.Fatalf("no subagent call: %s", kinds(msgs))
		}
		var result *message.ToolResult
		inside, wait := 0, false
		for _, m := range msgs {
			if r := m.ToolResult; r != nil && r.CallID == call.ID {
				result = r
			}
			if m.ParentToolUseID == call.ID {
				inside++
			}
			if c := m.ToolCall; c != nil && c.Name == message.ToolNameUnknown && c.RawName == itemCollabAgent {
				wait = true
			}
		}
		if result == nil || result.Name != message.ToolNameSubagent {
			t.Fatalf("no subagent result for %s: %s", call.ID, kinds(msgs))
		}
		if out := result.Output.(*message.TextOutput); !strings.Contains(out.Text, "3") {
			t.Errorf("subagent result %q does not report the line count", out.Text)
		}
		if inside == 0 {
			t.Error("no message carries the subagent call as ParentToolUseID")
		}
		if !wait {
			t.Errorf("the wait step is no unknown call: %s", kinds(msgs))
		}
		requireFinalText(t, turns[0], "3")
	},
	"terminate": func(t *testing.T, turns []recordedTurn) {
		requireShell(t, turns[0].msgs, func(cmd string) bool { return cmd == "sleep 30" })
		if turns[0].status != turnInterrupted || turns[0].err != nil {
			t.Errorf("turn status %q, err %v", turns[0].status, turns[0].err)
		}
	},
	"two-turn": func(t *testing.T, turns []recordedTurn) {
		if turns[0].thread != turns[1].thread {
			t.Error("the second turn is on another thread")
		}
		requireFinalText(t, turns[1], "marmalade")
	},
}

func requireKinds(t *testing.T, msgs []message.Message, want ...message.Kind) {
	t.Helper()
	var got []message.Kind
	for _, m := range msgs {
		got = append(got, m.Kind)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("kinds = %v, want %v", got, want)
	}
}

func requireFinalText(t *testing.T, recorded recordedTurn, want string) {
	t.Helper()
	final := ""
	for _, m := range recorded.msgs {
		if m.Assistant != nil && m.ParentToolUseID == "" {
			final = m.Assistant.Text
		}
	}
	if !strings.Contains(final, want) {
		t.Errorf("final text %q does not contain %q", final, want)
	}
}

// requireShell finds a shell call whose command matches and returns it with
// its result, if any. The login-shell wrapper must be gone.
func requireShell(t *testing.T, msgs []message.Message, match func(string) bool) (*message.ShellInput, *message.ShellOutput) {
	t.Helper()
	for _, m := range msgs {
		if m.ToolCall == nil || m.ToolCall.Name != message.ToolNameShell {
			continue
		}
		in := m.ToolCall.Input.(*message.ShellInput)
		if strings.Contains(in.Command, " -lc ") {
			t.Errorf("shell command %q keeps its wrapper", in.Command)
		}
		if !match(in.Command) {
			continue
		}
		for _, r := range msgs {
			if r.ToolResult != nil && r.ToolResult.CallID == m.ToolCall.ID {
				return in, r.ToolResult.Output.(*message.ShellOutput)
			}
		}
		return in, &message.ShellOutput{}
	}
	t.Fatalf("no matching shell call: %s", kinds(msgs))
	return nil, nil
}

// requireFileWrite requires a file_patch call with a change of kind on
// path, followed by its successful result.
func requireFileWrite(t *testing.T, msgs []message.Message, path, kind string) {
	t.Helper()
	for _, m := range msgs {
		if m.ToolCall == nil {
			continue
		}
		in, ok := m.ToolCall.Input.(*message.FilePatchInput)
		if !ok {
			continue
		}
		for _, c := range in.Changes {
			if !strings.HasSuffix(c.Path, path) || c.Kind != kind || c.Diff == "" {
				continue
			}
			for _, r := range msgs {
				if r.ToolResult != nil && r.ToolResult.CallID == m.ToolCall.ID && !r.ToolResult.IsError {
					return
				}
			}
		}
	}
	t.Fatalf("no successful file_patch %s of %s: %s", kind, path, kinds(msgs))
}

// goldenMessage is a message as stored in messages.golden.json: tool inputs
// and outputs keep their Go type name, which plain JSON would lose.
type goldenMessage struct {
	ID              string
	ParentToolUseID string `json:",omitempty"`
	Kind            message.Kind
	User            *message.Text     `json:",omitempty"`
	Assistant       *message.Text     `json:",omitempty"`
	Reasoning       *message.Text     `json:",omitempty"`
	ToolCall        *goldenToolCall   `json:",omitempty"`
	ToolResult      *goldenToolResult `json:",omitempty"`
	Raw             json.RawMessage
}

type goldenToolCall struct {
	ID, RawName string
	Name        message.ToolName
	InputType   string
	Input       message.ToolInput
}

type goldenToolResult struct {
	CallID     string
	Name       message.ToolName
	OutputType string
	Output     message.ToolOutput
	IsError    bool
}

func goldenView(msgs []message.Message) []goldenMessage {
	out := []goldenMessage{}
	for _, m := range msgs {
		g := goldenMessage{ID: m.ID, ParentToolUseID: m.ParentToolUseID, Kind: m.Kind, User: m.User, Assistant: m.Assistant, Reasoning: m.Reasoning, Raw: m.Raw}
		if c := m.ToolCall; c != nil {
			g.ToolCall = &goldenToolCall{ID: c.ID, RawName: c.RawName, Name: c.Name, InputType: fmt.Sprintf("%T", c.Input), Input: c.Input}
		}
		if r := m.ToolResult; r != nil {
			g.ToolResult = &goldenToolResult{CallID: r.CallID, Name: r.Name, OutputType: fmt.Sprintf("%T", r.Output), Output: r.Output, IsError: r.IsError}
		}
		out = append(out, g)
	}
	return out
}
