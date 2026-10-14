package codex

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wenfengwang/cloud-agent-sdk/internal/fakeharness"
	"github.com/wenfengwang/cloud-agent-sdk/internal/harness"
	"github.com/wenfengwang/cloud-agent-sdk/internal/message"
	"github.com/wenfengwang/cloud-agent-sdk/internal/testenv"
)

// fakeEnv is an isolated environment in which "codex" is the fake.
type fakeEnv struct {
	home, workDir string
	state         fakeharness.State
	h             *Harness
}

// useFake installs the fake with one Invocation per entry; Traffic paths
// that are relative name fixture traffic files under testdata.
func useFake(t *testing.T, invocations ...fakeharness.Invocation) *fakeEnv {
	t.Helper()
	testenv.Isolate(t)
	e := &fakeEnv{home: t.TempDir(), workDir: t.TempDir()}
	for i, inv := range invocations {
		if inv.Traffic != "" && !filepath.IsAbs(inv.Traffic) {
			abs, err := filepath.Abs(filepath.Join("testdata", inv.Traffic))
			if err != nil {
				t.Fatal(err)
			}
			invocations[i].Traffic = abs
		}
	}
	e.state = fakeharness.Use(t, fakeBin, fakeharness.Script{
		Substitutions: map[string]string{
			testenv.PlaceholderHarnessHome: e.home,
			testenv.PlaceholderWorkDir:     e.workDir,
			testenv.PlaceholderHome:        t.TempDir(),
		},
		Invocations: invocations,
	})
	e.h = New(harness.Config{WorkDir: e.workDir, Home: e.home})
	t.Cleanup(func() {
		e.h.Close()
		if live := e.state.LivePIDs(t); len(live) > 0 {
			t.Errorf("fake processes alive after Close: %v", live)
		}
	})
	return e
}

func (e *fakeEnv) start(t *testing.T, prompt string) harness.Run {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, err := e.h.StartTurn(ctx, prompt)
	if err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	return r
}

// drain collects a run's messages, failing if they do not end in time.
func drain(t *testing.T, r harness.Run, each func(message.Message)) []message.Message {
	t.Helper()
	var out []message.Message
	timeout := time.After(20 * time.Second)
	for {
		select {
		case m, ok := <-r.Messages():
			if !ok {
				return out
			}
			out = append(out, m)
			if each != nil {
				each(m)
			}
		case <-timeout:
			t.Fatal("Messages was not closed in time")
		}
	}
}

// requests returns the params of every request or notification with method
// that invocation n received.
func requests(t *testing.T, s fakeharness.State, n int, method string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range s.Received(t, n) {
		var m struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if json.Unmarshal(line, &m) == nil && m.Method == method {
			out = append(out, m.Params)
		}
	}
	return out
}

func ids(msgs []message.Message) []string {
	var out []string
	for _, m := range msgs {
		out = append(out, m.ID)
	}
	return out
}

// recvLinesThrough counts the recv lines of a fixture traffic file up to and
// including the first one that contains marker.
func recvLinesThrough(t *testing.T, traffic, marker string) int {
	t.Helper()
	lines, err := fakeharness.ReadTraffic(filepath.Join("testdata", traffic))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, l := range lines {
		if l.Dir != fakeharness.DirRecv {
			continue
		}
		n++
		if strings.Contains(string(l.Line), marker) {
			return n
		}
	}
	t.Fatalf("%s has no recv line containing %s", traffic, marker)
	return 0
}

func TestReplayCompletes(t *testing.T) {
	for _, name := range []string{"text", "shell", "subagent"} {
		t.Run(name, func(t *testing.T) {
			rec := readFixture(t, filepath.Join("testdata", name))
			want := fixtureTurns(t, rec)[0]
			e := useFake(t, fakeharness.Invocation{Traffic: name + "/traffic-1.jsonl"})
			r := e.start(t, rec.Meta.Prompts[0])
			if r.Ref().SessionID != want.thread {
				t.Errorf("Ref().SessionID = %q, want %q", r.Ref().SessionID, want.thread)
			}
			var data map[string]string
			if err := json.Unmarshal(r.Ref().Data, &data); err != nil || data["thread_id"] != want.thread || data["turn_id"] != want.turn {
				t.Errorf("Ref().Data = %s", r.Ref().Data)
			}
			got := drain(t, r, nil)
			out, err := r.Wait()
			if err != nil {
				t.Fatalf("Wait: %v", err)
			}
			if !reflect.DeepEqual(ids(got), ids(want.msgs)) {
				t.Errorf("messages %s, want %s", kinds(got), kinds(want.msgs))
			}
			if out.Usage != want.usage {
				t.Errorf("usage %+v, want %+v", out.Usage, want.usage)
			}

			info := e.state.Info(t, 1)
			if !reflect.DeepEqual(info.Args, []string{"app-server"}) || info.Dir != resolved(t, e.workDir) || info.Env["CODEX_HOME"] != e.home {
				t.Errorf("process info %+v", info)
			}
			init := requests(t, e.state, 1, "initialize")
			if len(init) != 1 || init[0]["clientInfo"].(map[string]any)["name"] != clientName {
				t.Errorf("initialize = %v", init)
			}
			start := requests(t, e.state, 1, "thread/start")
			if len(start) != 1 || start[0]["approvalPolicy"] != "never" || start[0]["sandbox"] != "danger-full-access" || start[0]["cwd"] != e.workDir {
				t.Errorf("thread/start = %v", start)
			}
			if _, ok := start[0]["model"]; ok {
				t.Error("thread/start names a model although Config.Model is empty")
			}
			if _, ok := start[0]["config"]; ok {
				t.Error("thread/start carries a configuration override")
			}
			turn := requests(t, e.state, 1, "turn/start")
			if len(turn) != 1 || turn[0]["summary"] != "auto" || turn[0]["threadId"] != want.thread {
				t.Errorf("turn/start = %v", turn)
			}
		})
	}
}

func resolved(t *testing.T, path string) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestModelIsPassed(t *testing.T) {
	e := useFake(t, fakeharness.Invocation{Traffic: "text/traffic-1.jsonl"})
	e.h = New(harness.Config{WorkDir: e.workDir, Home: e.home, Model: "some-model"})
	r := e.start(t, "hi")
	drain(t, r, nil)
	r.Wait()
	start := requests(t, e.state, 1, "thread/start")
	if len(start) != 1 || start[0]["model"] != "some-model" {
		t.Errorf("thread/start = %v", start)
	}
}

func TestTwoTurnsShareTheThread(t *testing.T) {
	rec := readFixture(t, filepath.Join("testdata", "two-turn"))
	turns := fixtureTurns(t, rec)
	e := useFake(t, fakeharness.Invocation{Traffic: "two-turn/traffic-1.jsonl"})
	for i, prompt := range rec.Meta.Prompts {
		r := e.start(t, prompt)
		got := drain(t, r, nil)
		if _, err := r.Wait(); err != nil {
			t.Fatalf("turn %d: %v", i+1, err)
		}
		if !reflect.DeepEqual(ids(got), ids(turns[i].msgs)) {
			t.Errorf("turn %d: messages %s, want %s", i+1, kinds(got), kinds(turns[i].msgs))
		}
	}
	if n := e.state.Invocations(t); n != 1 {
		t.Errorf("%d app-server processes, want 1", n)
	}
	if n := len(requests(t, e.state, 1, "thread/start")); n != 1 {
		t.Errorf("%d thread/start requests, want 1", n)
	}
	for _, p := range requests(t, e.state, 1, "turn/start") {
		if p["threadId"] != turns[0].thread {
			t.Errorf("turn/start on thread %v", p["threadId"])
		}
	}
}

func TestInterruptHonored(t *testing.T) {
	rec := readFixture(t, filepath.Join("testdata", "terminate"))
	want := fixtureTurns(t, rec)[0]
	hang := recvLinesThrough(t, "terminate/traffic-1.jsonl", `"command":"/bin/zsh -lc 'sleep 30'"`)
	e := useFake(t, fakeharness.Invocation{
		Traffic:   "terminate/traffic-1.jsonl",
		HangAfter: &fakeharness.Hang{Lines: hang, OnInterrupt: fakeharness.OnInterruptHonor},
	})
	r := e.start(t, rec.Meta.Prompts[0])
	got := drain(t, r, func(m message.Message) {
		if m.ToolCall != nil && m.ToolCall.Name == message.ToolNameShell {
			r.Interrupt()
			r.Interrupt() // idempotent
		}
	})
	if _, err := r.Wait(); err != nil {
		t.Fatalf("Wait after an honored interrupt: %v", err)
	}
	if !reflect.DeepEqual(ids(got), ids(want.msgs)) {
		t.Errorf("messages %s, want %s", kinds(got), kinds(want.msgs))
	}
	interrupts := requests(t, e.state, 1, "turn/interrupt")
	if len(interrupts) != 1 || interrupts[0]["threadId"] != want.thread || interrupts[0]["turnId"] != want.turn {
		t.Errorf("turn/interrupt = %v", interrupts)
	}
}

// resumeTraffic derives the traffic of a process that resumes a thread from
// a recorded first process: the app-server answers thread/resume with the
// thread object it returns for thread/start.
func resumeTraffic(t *testing.T, traffic string) string {
	t.Helper()
	lines, err := fakeharness.ReadTraffic(filepath.Join("testdata", traffic))
	if err != nil {
		t.Fatal(err)
	}
	for i, l := range lines {
		if l.Dir == fakeharness.DirSend && strings.Contains(string(l.Line), `"method":"thread/start"`) {
			lines[i].Line = json.RawMessage(strings.Replace(string(l.Line), `"method":"thread/start"`, `"method":"thread/resume"`, 1))
		}
	}
	path := filepath.Join(t.TempDir(), "traffic-resume.jsonl")
	fakeharness.WriteTraffic(t, path, lines)
	return path
}

func TestKillThenResume(t *testing.T) {
	rec := readFixture(t, filepath.Join("testdata", "terminate"))
	stopped := fixtureTurns(t, rec)[0]
	hang := recvLinesThrough(t, "terminate/traffic-1.jsonl", `"command":"/bin/zsh -lc 'sleep 30'"`)
	e := useFake(t,
		fakeharness.Invocation{
			Traffic:   "terminate/traffic-1.jsonl",
			HangAfter: &fakeharness.Hang{Lines: hang, OnInterrupt: fakeharness.OnInterruptIgnore},
		},
		fakeharness.Invocation{Traffic: resumeTraffic(t, "text/traffic-1.jsonl")},
	)
	r := e.start(t, rec.Meta.Prompts[0])
	var killedAt time.Time
	drain(t, r, func(m message.Message) {
		if m.ToolCall != nil && m.ToolCall.Name == message.ToolNameShell {
			r.Interrupt()
			killedAt = time.Now()
			r.Kill()
		}
	})
	if d := time.Since(killedAt); d > 2*time.Second {
		t.Errorf("Messages closed %v after Kill", d)
	}
	if _, err := r.Wait(); err == nil {
		t.Error("Wait after Kill reported no error")
	}
	pids := e.state.PIDs(t)
	for _, live := range e.state.LivePIDs(t) {
		if live == pids[0] {
			t.Fatal("the killed app-server is still alive")
		}
	}

	r = e.start(t, "next turn")
	drain(t, r, nil)
	if _, err := r.Wait(); err != nil {
		t.Fatalf("turn after Kill: %v", err)
	}
	resume := requests(t, e.state, 2, "thread/resume")
	if len(resume) != 1 || resume[0]["threadId"] != stopped.thread || resume[0]["approvalPolicy"] != "never" || resume[0]["sandbox"] != "danger-full-access" {
		t.Errorf("thread/resume = %v", resume)
	}
	if n := len(requests(t, e.state, 2, "thread/start")); n != 0 {
		t.Errorf("the new process started %d threads", n)
	}
}

func TestCrashFailsTheTurn(t *testing.T) {
	crash := recvLinesThrough(t, "shell/traffic-1.jsonl", `"type":"commandExecution"`)
	e := useFake(t, fakeharness.Invocation{
		Traffic:    "shell/traffic-1.jsonl",
		CrashAfter: &fakeharness.Crash{Lines: crash, ExitCode: 3},
	})
	r := e.start(t, "crash")
	drain(t, r, nil)
	_, err := r.Wait()
	if err == nil || !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("Wait = %v, want the exit status", err)
	}
	// The next turn starts a new process, which is not scripted here.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := e.h.StartTurn(ctx, "again"); err == nil {
		t.Error("StartTurn reused the crashed process")
	}
	if n := e.state.Invocations(t); n != 2 {
		t.Errorf("%d processes, want 2", n)
	}
}

func TestStartRejected(t *testing.T) {
	e := useFake(t, fakeharness.Invocation{Traffic: "text/traffic-1.jsonl", RejectMethod: "turn/start"})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := e.h.StartTurn(ctx, "rejected"); err == nil || !strings.Contains(err.Error(), "scripted rejection") {
		t.Fatalf("StartTurn = %v, want the rejection", err)
	}
}

func TestStartTimesOut(t *testing.T) {
	e := useFake(t, fakeharness.Invocation{
		Traffic:   "text/traffic-1.jsonl",
		HangAfter: &fakeharness.Hang{Lines: 0, OnInterrupt: fakeharness.OnInterruptIgnore},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := e.h.StartTurn(ctx, "never answered"); err == nil {
		t.Fatal("StartTurn succeeded without an initialize response")
	}
	if live := e.state.LivePIDs(t); len(live) != 0 {
		t.Errorf("a failed start left processes alive: %v", live)
	}
}

func TestMissingCLI(t *testing.T) {
	testenv.Isolate(t)
	t.Setenv("PATH", t.TempDir())
	h := New(harness.Config{WorkDir: t.TempDir(), Home: t.TempDir()})
	if _, err := h.StartTurn(context.Background(), "hi"); err == nil {
		t.Fatal("StartTurn succeeded without a codex executable")
	}
}

func TestClose(t *testing.T) {
	e := useFake(t, fakeharness.Invocation{Traffic: "text/traffic-1.jsonl"})
	r := e.start(t, "hi")
	drain(t, r, nil)
	r.Wait()
	if err := e.h.Close(); err != nil {
		t.Fatal(err)
	}
	if live := e.state.LivePIDs(t); len(live) != 0 {
		t.Errorf("processes alive after Close: %v", live)
	}
	if err := e.h.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if _, err := e.h.StartTurn(context.Background(), "after close"); err == nil {
		t.Error("StartTurn succeeded after Close")
	}
}

// TestReadTurnDuringStart: ReadTurn may run concurrently with anything, so
// it must not wait for a StartTurn that is blocked on the app-server.
func TestReadTurnDuringStart(t *testing.T) {
	e := useFake(t, fakeharness.Invocation{
		Traffic:   "text/traffic-1.jsonl",
		HangAfter: &fakeharness.Hang{Lines: 0, OnInterrupt: fakeharness.OnInterruptIgnore},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started := make(chan struct{})
	go func() {
		defer close(started)
		e.h.StartTurn(ctx, "never answered")
	}()
	time.Sleep(200 * time.Millisecond)
	begin := time.Now()
	ref := harness.Ref{SessionID: "t", Data: json.RawMessage(`{"thread_id":"t","turn_id":"u"}`)}
	e.h.ReadTurn(context.Background(), ref)
	if d := time.Since(begin); d > time.Second {
		t.Errorf("ReadTurn waited %s for a blocked StartTurn", d)
	}
	<-started
}
