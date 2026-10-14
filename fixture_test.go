package cloudagent_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cloudagent "github.com/wenfengwang/cloud-agent-sdk"
	"github.com/wenfengwang/cloud-agent-sdk/internal/claude"
	"github.com/wenfengwang/cloud-agent-sdk/internal/fakeharness"
	"github.com/wenfengwang/cloud-agent-sdk/internal/testenv"
)

// harnesses are the harnesses every lifecycle test runs against.
var harnesses = []cloudagent.Harness{cloudagent.Claude, cloudagent.OpenAI}

// adapterPackage names the adapter package that holds a harness's fixtures.
var adapterPackage = map[cloudagent.Harness]string{
	cloudagent.Claude: "claude",
	cloudagent.OpenAI: "codex",
}

// markers are substrings of recorded recv lines, per harness, that tests use
// to script the fake at a point of a replay.
var markers = map[cloudagent.Harness]struct {
	acceptance string // the line that makes the turn accepted
	accepted   string // the line that makes the turn accepted, or follows it
	shellCall  string // the first line that carries a shell tool call
}{
	cloudagent.Claude: {acceptance: `"isReplay":true`, accepted: `"isReplay":true`, shellCall: `"type":"tool_use"`},
	cloudagent.OpenAI: {acceptance: `"result":{"turn":`, accepted: `"type":"userMessage"`, shellCall: `"type":"commandExecution"`},
}

// placeholderSessionID is the Claude fixtures' placeholder for the session ID
// the adapter generates.
const placeholderSessionID = "@SESSION_ID@"

// replayEnv is an isolated environment in which the fake claude and codex
// replay one scenario fixture.
type replayEnv struct {
	harness                 cloudagent.Harness
	dir                     string // the fixture directory
	rec                     testenv.Recorded
	home, harnessHome, work string
	state                   fakeharness.State
}

// newReplay isolates the test and scripts the fake to replay the fixture of
// scenario for h, one invocation per recorded process. The fixture's harness
// home files reach the harness home before the turn ends, as with the real
// CLI: Claude transcripts when the last process ends its traffic, Codex
// rollouts before the first turn, since Codex writes them while the turn
// runs. edit, if not nil, may change the invocations; it receives the
// environment so that it can locate lines of the fixture.
func newReplay(t *testing.T, h cloudagent.Harness, scenario string, edit func(*replayEnv, []fakeharness.Invocation) []fakeharness.Invocation) *replayEnv {
	t.Helper()
	e := &replayEnv{harness: h, dir: filepath.Join("internal", adapterPackage[h], "testdata", scenario)}
	rec, err := testenv.ReadFixture(e.dir)
	if err != nil {
		t.Fatal(err)
	}
	testenv.RequirePinnedFixture(t, rec)
	e.rec = rec
	e.home = testenv.Isolate(t)
	e.harnessHome, e.work = t.TempDir(), t.TempDir()

	values := map[string]string{
		testenv.PlaceholderHome:        e.home,
		testenv.PlaceholderHarnessHome: e.harnessHome,
		testenv.PlaceholderWorkDir:     e.work,
	}
	invs := make([]fakeharness.Invocation, len(rec.Traffic))
	for i := range rec.Traffic {
		invs[i].Traffic = absPath(t, filepath.Join(e.dir, fmt.Sprintf("traffic-%d.jsonl", i+1)))
	}
	script := fakeharness.Script{Substitutions: values}
	switch h {
	case cloudagent.Claude:
		values[testenv.PlaceholderWorkDirEncoded] = claude.EncodeWorkDir(resolved(t, e.work))
		script.ArgSubstitutions = map[string][]string{placeholderSessionID: {"--session-id", "--resume"}}
		var files []fakeharness.File
		for _, name := range rec.HomeFiles() {
			files = append(files, fakeharness.File{
				From: absPath(t, filepath.Join(e.dir, "home", filepath.FromSlash(name))),
				To:   filepath.Join(e.harnessHome, filepath.FromSlash(name)),
			})
		}
		invs[len(invs)-1].Files = files
	case cloudagent.OpenAI:
		testenv.CopyHome(t, e.dir, e.harnessHome, values)
	}
	if edit != nil {
		invs = edit(e, invs)
	}
	script.Invocations = invs
	e.state = fakeharness.Use(t, fakeBin, script)
	return e
}

// newAgent returns an agent on the replay's work directory and harness
// home. Cleanup closes it and fails the test if a fake process survives.
func (e *replayEnv) newAgent(t *testing.T) *cloudagent.AgentRuntime {
	t.Helper()
	a, err := cloudagent.New(e.harness, cloudagent.Options{WorkDir: e.work, Home: e.harnessHome})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
		if pids := e.state.LivePIDs(t); len(pids) != 0 {
			t.Errorf("harness processes alive after Close: %v", pids)
		}
	})
	return a
}

// recvLinesThrough counts the recv lines of the fixture's traffic n (from 1)
// up to and including the first that contains marker.
func (e *replayEnv) recvLinesThrough(t *testing.T, n int, marker string) int {
	t.Helper()
	count := 0
	for _, l := range e.rec.Traffic[n-1] {
		if l.Dir != fakeharness.DirRecv {
			continue
		}
		count++
		if strings.Contains(string(l.Line), marker) {
			return count
		}
	}
	t.Fatalf("%s traffic %d has no recv line containing %s", e.dir, n, marker)
	return 0
}

// goldenMessage holds the golden fields the root tests compare.
type goldenMessage struct {
	ID              string
	ParentToolUseID string
	Kind            cloudagent.Kind
	Assistant       *cloudagent.Text
}

// goldenTurns returns the fixture's golden messages split into turns; every
// turn starts with its top-level user message.
func (e *replayEnv) goldenTurns(t *testing.T) [][]goldenMessage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(e.dir, "messages.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	var all []goldenMessage
	if err := json.Unmarshal(data, &all); err != nil {
		t.Fatalf("parse golden: %v", err)
	}
	var turns [][]goldenMessage
	for _, m := range all {
		if m.Kind == cloudagent.KindUser && m.ParentToolUseID == "" {
			turns = append(turns, nil)
		}
		if len(turns) == 0 {
			t.Fatalf("golden of %s does not start with a user message", e.dir)
		}
		turns[len(turns)-1] = append(turns[len(turns)-1], m)
	}
	if len(turns) != len(e.rec.Meta.Prompts) {
		t.Fatalf("golden of %s has %d turns, want %d", e.dir, len(turns), len(e.rec.Meta.Prompts))
	}
	return turns
}

func goldenIDs(msgs []goldenMessage) []string {
	var out []string
	for _, m := range msgs {
		out = append(out, m.ID)
	}
	return out
}

// goldenFinalText is the text of a golden turn's last top-level assistant
// message.
func goldenFinalText(msgs []goldenMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if m := msgs[i]; m.Kind == cloudagent.KindAssistant && m.ParentToolUseID == "" && m.Assistant != nil {
			return m.Assistant.Text
		}
	}
	return ""
}

func messageIDs(msgs []cloudagent.Message) []string {
	var out []string
	for _, m := range msgs {
		out = append(out, m.ID)
	}
	return out
}

// collect iterates turn.Messages to its end, calling each (if not nil) for
// every message, and fails the test on an iteration error.
func collect(t *testing.T, turn *cloudagent.Turn, each func(cloudagent.Message)) []cloudagent.Message {
	t.Helper()
	var out []cloudagent.Message
	for m, err := range turn.Messages() {
		if err != nil {
			t.Fatalf("Messages: %v", err)
		}
		if m.TurnID != turn.ID() {
			t.Errorf("message %s has TurnID %q, want %q", m.ID, m.TurnID, turn.ID())
		}
		out = append(out, m)
		if each != nil {
			each(m)
		}
	}
	return out
}

// result waits for turn's Result, failing the test if it takes longer than
// the stop grace period of design 001 allows.
func result(t *testing.T, turn *cloudagent.Turn) (cloudagent.Result, error) {
	t.Helper()
	type outcome struct {
		res cloudagent.Result
		err error
	}
	ch := make(chan outcome, 1)
	go func() {
		res, err := turn.Result()
		ch <- outcome{res, err}
	}()
	select {
	case o := <-ch:
		return o.res, o.err
	case <-time.After(20 * time.Second):
		t.Fatal("Result did not return")
		return cloudagent.Result{}, nil
	}
}

// isShellCall reports whether m is a shell tool call.
func isShellCall(m cloudagent.Message) bool {
	return m.ToolCall != nil && m.ToolCall.Name == cloudagent.ToolNameShell
}

// turnRecord is one line of turns.jsonl.
type turnRecord struct {
	TurnID     string          `json:"turn_id"`
	HarnessRef json.RawMessage `json:"harness_ref"`
	Status     string          `json:"status"`
	StartedAt  time.Time       `json:"started_at"`
	EndedAt    *time.Time      `json:"ended_at"`
}

// indexDir is the agent's entry under the isolated ~/.cloudagent.
func (e *replayEnv) indexDir(a *cloudagent.AgentRuntime) string {
	return filepath.Join(e.home, ".cloudagent", "agents", a.ID())
}

// turnRecords reads the agent's turns.jsonl.
func (e *replayEnv) turnRecords(t *testing.T, a *cloudagent.AgentRuntime) []turnRecord {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(e.indexDir(a), "turns.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var out []turnRecord
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var r turnRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("parse turns.jsonl: %v", err)
		}
		out = append(out, r)
	}
	return out
}

// requireStatus checks the index status of turn.
func (e *replayEnv) requireStatus(t *testing.T, a *cloudagent.AgentRuntime, turn *cloudagent.Turn, want string) {
	t.Helper()
	for _, r := range e.turnRecords(t, a) {
		if r.TurnID == turn.ID() {
			if r.Status != want {
				t.Errorf("turn status %q, want %q", r.Status, want)
			}
			if r.EndedAt == nil {
				t.Error("ended turn has no ended_at")
			}
			return
		}
	}
	t.Errorf("turn %s is not in turns.jsonl", turn.ID())
}

func absPath(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func resolved(t *testing.T, path string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// agentRecord reads the agent's agent.json.
func (e *replayEnv) agentRecord(t *testing.T, a *cloudagent.AgentRuntime) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(e.indexDir(a), "agent.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rec map[string]string
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("parse agent.json: %v", err)
	}
	return rec
}
