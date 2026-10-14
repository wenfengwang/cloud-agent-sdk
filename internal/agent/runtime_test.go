package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wenfengwang/cloud-agent-sdk/internal/harness"
	"github.com/wenfengwang/cloud-agent-sdk/internal/message"
)

func newTestRuntime(t *testing.T, h *fakeHarness) *Runtime {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	runtime, err := New(Config{
		Harness:   "claudecode",
		Model:     "test-model",
		WorkDir:   "/work",
		Home:      "/harness-home",
		Adapter:   h,
		StopGrace: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { runtime.Close() })
	return runtime
}

func indexDir(t *testing.T, runtime *Runtime) string {
	t.Helper()
	return filepath.Join(os.Getenv("HOME"), ".cloudagent", "agents", runtime.ID())
}

func readAgentJSON(t *testing.T, runtime *Runtime) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(indexDir(t, runtime), "agent.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse agent.json: %v", err)
	}
	return m
}

func readTurns(t *testing.T, runtime *Runtime) []map[string]any {
	t.Helper()
	f, err := os.Open(filepath.Join(indexDir(t, runtime), "turns.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("parse turns.jsonl line %q: %v", sc.Text(), err)
		}
		out = append(out, m)
	}
	return out
}

func collect(t *testing.T, turn *Turn) []message.Message {
	t.Helper()
	var out []message.Message
	for m, err := range turn.Messages() {
		if err != nil {
			t.Fatalf("Messages: %v", err)
		}
		out = append(out, m)
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

func TestNewWritesAgentIndex(t *testing.T) {
	runtime := newTestRuntime(t, &fakeHarness{})

	id, err := uuid.Parse(runtime.ID())
	if err != nil || id.Version() != 7 {
		t.Fatalf("ID %q is not a UUIDv7 (err %v)", runtime.ID(), err)
	}
	got := readAgentJSON(t, runtime)
	want := map[string]any{
		"harness": "claudecode", "model": "test-model", "workdir": "/work",
		"home": "/harness-home", "harness_session_id": "",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("agent.json = %v, want %v", got, want)
	}
	if turns := readTurns(t, runtime); len(turns) != 0 {
		t.Fatalf("turns.jsonl has %d lines, want 0", len(turns))
	}
}

func TestNewFailsWhenIndexCannotBeWritten(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".cloudagent"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Config{Harness: "claudecode", WorkDir: "/work", Adapter: &fakeHarness{}}); err == nil {
		t.Fatal("New succeeded, want an index error")
	}
}

func TestTurnCompletes(t *testing.T) {
	run := &fakeRun{
		ref:      ref("s1", "u1"),
		finalRef: ref("s1", "u1-final"),
		msgs: []message.Message{
			userMsg("m1", "say hi"),
			assistantMsg("m2", "", "first"),
			assistantMsg("m3", "", "hi there"),
			assistantMsg("m4", "tool-1", "from a subagent"),
		},
		usage: harness.Usage{InputTokens: 10, OutputTokens: 5, CacheReadTokens: 2, CacheWriteTokens: 1},
	}
	h := &fakeHarness{runs: []*fakeRun{run}}
	runtime := newTestRuntime(t, h)

	turn, err := runtime.Turn(context.Background(), "say hi")
	if err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if _, err := uuid.Parse(turn.ID()); err != nil {
		t.Fatalf("turn ID %q: %v", turn.ID(), err)
	}
	msgs := collect(t, turn)
	if got, want := ids(msgs), []string{"m1", "m2", "m3", "m4"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("message IDs = %v, want %v", got, want)
	}
	for _, m := range msgs {
		if m.TurnID != turn.ID() {
			t.Fatalf("message %s TurnID = %q, want %q", m.ID, m.TurnID, turn.ID())
		}
	}

	res, err := turn.Result()
	if err != nil {
		t.Fatalf("Result: %v", err)
	}
	want := Result{TurnID: turn.ID(), FinalText: "hi there", Usage: run.usage}
	if res != want {
		t.Fatalf("Result = %+v, want %+v", res, want)
	}
	res2, err2 := turn.Result()
	if res2 != res || err2 != nil {
		t.Fatalf("second Result = %+v, %v", res2, err2)
	}
	if h.prompts[0] != "say hi" {
		t.Fatalf("prompt = %q", h.prompts[0])
	}

	turns := readTurns(t, runtime)
	if len(turns) != 1 {
		t.Fatalf("turns.jsonl has %d lines, want 1", len(turns))
	}
	rec := turns[0]
	if rec["turn_id"] != turn.ID() || rec["status"] != "completed" {
		t.Fatalf("turn record = %v", rec)
	}
	if hr, _ := json.Marshal(rec["harness_ref"]); string(hr) != `{"session_id":"s1","user_message_uuid":"u1-final"}` {
		t.Fatalf("harness_ref = %s", hr)
	}
	for _, field := range []string{"started_at", "ended_at"} {
		if _, err := time.Parse(time.RFC3339Nano, rec[field].(string)); err != nil {
			t.Fatalf("%s = %v: %v", field, rec[field], err)
		}
	}
	if len(rec) != 5 {
		t.Fatalf("turn record has fields %v, want exactly 5", rec)
	}
	if got := readAgentJSON(t, runtime)["harness_session_id"]; got != "s1" {
		t.Fatalf("harness_session_id = %v, want s1", got)
	}
}

func TestTurnRunsWithoutConsumer(t *testing.T) {
	run := &fakeRun{ref: ref("s1", "u1"), msgs: []message.Message{userMsg("m1", "p"), assistantMsg("m2", "", "done")}}
	runtime := newTestRuntime(t, &fakeHarness{runs: []*fakeRun{run}})
	turn, err := runtime.Turn(context.Background(), "p")
	if err != nil {
		t.Fatal(err)
	}
	res, err := turn.Result()
	if err != nil || res.FinalText != "done" {
		t.Fatalf("Result = %+v, %v", res, err)
	}
}

func TestTurnRecordsRunningStatus(t *testing.T) {
	run := &fakeRun{ref: ref("s1", "u1"), hold: true}
	runtime := newTestRuntime(t, &fakeHarness{runs: []*fakeRun{run}})
	turn, err := runtime.Turn(context.Background(), "p")
	if err != nil {
		t.Fatal(err)
	}
	turns := readTurns(t, runtime)
	if len(turns) != 1 || turns[0]["status"] != "running" || turns[0]["ended_at"] != nil {
		t.Fatalf("turns while running = %v", turns)
	}
	if hr, _ := json.Marshal(turns[0]["harness_ref"]); string(hr) != `{"session_id":"s1","user_message_uuid":"u1"}` {
		t.Fatalf("harness_ref = %s", hr)
	}
	if got := readAgentJSON(t, runtime)["harness_session_id"]; got != "s1" {
		t.Fatalf("harness_session_id while running = %v", got)
	}
	turn.Terminate()
	turn.Result()
}

func TestTurnStartErrors(t *testing.T) {
	startErr := errors.New("harness rejected the prompt")
	h := &fakeHarness{runs: []*fakeRun{{startErr: startErr}, {ref: ref("s1", "u1")}}}
	runtime := newTestRuntime(t, h)

	if _, err := runtime.Turn(context.Background(), ""); err == nil {
		t.Fatal("Turn with an empty prompt succeeded")
	}
	_, err := runtime.Turn(context.Background(), "p")
	if !errors.Is(err, startErr) {
		t.Fatalf("Turn = %v, want wrapped %v", err, startErr)
	}
	if turns := readTurns(t, runtime); len(turns) != 0 {
		t.Fatalf("start error recorded a turn: %v", turns)
	}
	turn, err := runtime.Turn(context.Background(), "p")
	if err != nil {
		t.Fatalf("Turn after a start error: %v", err)
	}
	if _, err := turn.Result(); err != nil {
		t.Fatal(err)
	}
}

func TestSingleActiveTurn(t *testing.T) {
	first := &fakeRun{ref: ref("s1", "u1"), hold: true}
	second := &fakeRun{ref: ref("s1", "u2")}
	runtime := newTestRuntime(t, &fakeHarness{runs: []*fakeRun{first, second}})

	turn, err := runtime.Turn(context.Background(), "p")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Turn(context.Background(), "q"); !errors.Is(err, ErrTurnInProgress) {
		t.Fatalf("second Turn = %v, want ErrTurnInProgress", err)
	}
	turn.Terminate()
	if _, err := turn.Result(); !errors.Is(err, ErrTerminated) {
		t.Fatalf("Result = %v, want ErrTerminated", err)
	}
	next, err := runtime.Turn(context.Background(), "q")
	if err != nil {
		t.Fatalf("Turn after the first ended: %v", err)
	}
	if _, err := next.Result(); err != nil {
		t.Fatal(err)
	}
}

func TestTerminate(t *testing.T) {
	run := &fakeRun{ref: ref("s1", "u1"), msgs: []message.Message{userMsg("m1", "p"), shellCall("c1", "sleep 30")}, hold: true}
	runtime := newTestRuntime(t, &fakeHarness{runs: []*fakeRun{run}})
	turn, err := runtime.Turn(context.Background(), "p")
	if err != nil {
		t.Fatal(err)
	}
	for m, err := range turn.Messages() {
		if err != nil {
			t.Fatal(err)
		}
		if m.Kind == message.KindToolCall {
			if err := turn.Terminate(); err != nil {
				t.Fatalf("Terminate: %v", err)
			}
		}
	}
	res, err := turn.Result()
	if err != ErrTerminated {
		t.Fatalf("Result error = %v, want ErrTerminated", err)
	}
	if res.TurnID != turn.ID() {
		t.Fatalf("Result.TurnID = %q", res.TurnID)
	}
	if run.interrupts.Load() == 0 || run.kills.Load() != 0 {
		t.Fatalf("interrupts %d kills %d, want a graceful stop", run.interrupts.Load(), run.kills.Load())
	}
	if status := readTurns(t, runtime)[0]["status"]; status != "terminated" {
		t.Fatalf("status = %v", status)
	}
	if err := turn.Terminate(); err != nil {
		t.Fatalf("Terminate on a finished turn: %v", err)
	}
	if _, err := turn.Result(); err != ErrTerminated {
		t.Fatalf("Result after a late Terminate = %v", err)
	}
}

func TestTerminateAfterCompletionIsNoOp(t *testing.T) {
	run := &fakeRun{ref: ref("s1", "u1"), msgs: []message.Message{assistantMsg("m1", "", "ok")}}
	runtime := newTestRuntime(t, &fakeHarness{runs: []*fakeRun{run}})
	turn, err := runtime.Turn(context.Background(), "p")
	if err != nil {
		t.Fatal(err)
	}
	turn.Result()
	turn.Terminate()
	if _, err := turn.Result(); err != nil {
		t.Fatalf("Result = %v, want nil", err)
	}
	if run.interrupts.Load() != 0 {
		t.Fatal("Terminate on a finished turn interrupted the run")
	}
}

func TestContextCancellation(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctx  func() (context.Context, context.CancelFunc)
		want error
	}{
		{"cancel", func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) }, context.Canceled},
		{"deadline", func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), 50*time.Millisecond)
		}, context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := &fakeRun{ref: ref("s1", "u1"), hold: true}
			runtime := newTestRuntime(t, &fakeHarness{runs: []*fakeRun{run}})
			ctx, cancel := tc.ctx()
			defer cancel()
			turn, err := runtime.Turn(ctx, "p")
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "cancel" {
				cancel()
			}
			if _, err := turn.Result(); err != tc.want {
				t.Fatalf("Result = %v, want %v", err, tc.want)
			}
			if run.interrupts.Load() == 0 {
				t.Fatal("cancellation did not interrupt the run")
			}
			if status := readTurns(t, runtime)[0]["status"]; status != "cancelled" {
				t.Fatalf("status = %v", status)
			}
		})
	}
}

func TestCloseDuringTurn(t *testing.T) {
	run := &fakeRun{ref: ref("s1", "u1"), msgs: []message.Message{userMsg("m1", "p")}, hold: true}
	h := &fakeHarness{runs: []*fakeRun{run}, readBack: []message.Message{userMsg("m1", "p")}}
	runtime := newTestRuntime(t, h)
	turn, err := runtime.Turn(context.Background(), "p")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Close waited for the turn: its result is already available.
	select {
	case <-turn.done:
	default:
		t.Fatal("Close returned before the turn ended")
	}
	if _, err := turn.Result(); err != ErrClosed {
		t.Fatalf("Result = %v, want ErrClosed", err)
	}
	if run.interrupts.Load() == 0 {
		t.Fatal("Close did not take the graceful stop path")
	}
	if h.closeCount() != 1 {
		t.Fatalf("harness Close called %d times, want 1", h.closeCount())
	}
	if status := readTurns(t, runtime)[0]["status"]; status != "closed" {
		t.Fatalf("status = %v", status)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if h.closeCount() != 1 {
		t.Fatal("second Close closed the harness again")
	}
	if _, err := runtime.Turn(context.Background(), "q"); err != ErrClosed {
		t.Fatalf("Turn after Close = %v, want ErrClosed", err)
	}
	if got := ids(collect(t, turn)); !reflect.DeepEqual(got, []string{"m1"}) {
		t.Fatalf("messages after Close = %v", got)
	}
}

func TestCloseWithoutTurn(t *testing.T) {
	h := &fakeHarness{}
	runtime := newTestRuntime(t, h)
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if h.closeCount() != 1 {
		t.Fatalf("harness Close called %d times", h.closeCount())
	}
}

// TestCloseCancelsStart closes the agent while the harness waits forever to
// accept the prompt: Close cancels the start and Turn returns ErrClosed.
func TestCloseCancelsStart(t *testing.T) {
	h := &fakeHarness{startBlocks: true, startEntered: make(chan struct{}), startRelease: make(chan struct{})}
	close(h.startRelease)
	runtime := newTestRuntime(t, h)
	turnErr := make(chan error, 1)
	go func() {
		_, err := runtime.Turn(context.Background(), "p")
		turnErr <- err
	}()
	<-h.startEntered
	closed := make(chan error, 1)
	go func() { closed <- runtime.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not cancel the start")
	}
	if err := <-turnErr; err != ErrClosed {
		t.Fatalf("Turn = %v, want ErrClosed", err)
	}
	if turns := readTurns(t, runtime); len(turns) != 0 {
		t.Fatalf("a cancelled start was recorded: %v", turns)
	}
}

func TestCloseDuringStart(t *testing.T) {
	run := &fakeRun{ref: ref("s1", "u1"), hold: true}
	h := &fakeHarness{
		runs:         []*fakeRun{run},
		startEntered: make(chan struct{}),
		startRelease: make(chan struct{}),
	}
	runtime := newTestRuntime(t, h)

	type turnResult struct {
		turn *Turn
		err  error
	}
	started := make(chan turnResult, 1)
	go func() {
		turn, err := runtime.Turn(context.Background(), "p")
		started <- turnResult{turn, err}
	}()
	<-h.startEntered
	closed := make(chan error, 1)
	go func() { closed <- runtime.Close() }()
	// Close cannot finish while the start is in flight.
	select {
	case err := <-closed:
		t.Fatalf("Close returned during the start: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(h.startRelease)

	got := <-started
	if got.err != nil {
		t.Fatalf("Turn = %v", got.err)
	}
	if err := <-closed; err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := got.turn.Result(); err != ErrClosed {
		t.Fatalf("Result = %v, want ErrClosed", err)
	}
	if run.interrupts.Load() == 0 {
		t.Fatal("the run was not stopped")
	}
}

func TestHarnessFailure(t *testing.T) {
	crash := errors.New("process exited with status 3")
	run := &fakeRun{ref: ref("s1", "u1"), msgs: []message.Message{userMsg("m1", "p")}, waitErr: crash}
	runtime := newTestRuntime(t, &fakeHarness{runs: []*fakeRun{run}})
	turn, err := runtime.Turn(context.Background(), "p")
	if err != nil {
		t.Fatal(err)
	}
	_, err = turn.Result()
	if !errors.Is(err, crash) || err == crash {
		t.Fatalf("Result = %v, want a wrapped %v", err, crash)
	}
	if status := readTurns(t, runtime)[0]["status"]; status != "failed" {
		t.Fatalf("status = %v", status)
	}
}

func TestIgnoredInterruptEscalatesToKill(t *testing.T) {
	run := &fakeRun{ref: ref("s1", "u1"), hold: true, ignoreInterrupt: true}
	runtime := newTestRuntime(t, &fakeHarness{runs: []*fakeRun{run}})
	turn, err := runtime.Turn(context.Background(), "p")
	if err != nil {
		t.Fatal(err)
	}
	turn.Terminate()
	done := make(chan error, 1)
	go func() { _, err := turn.Result(); done <- err }()
	select {
	case err := <-done:
		if err != ErrTerminated {
			t.Fatalf("Result = %v, want ErrTerminated", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not end after the grace period")
	}
	if run.kills.Load() == 0 {
		t.Fatal("the run was not killed")
	}
}

func TestConcurrentConsumers(t *testing.T) {
	var msgs []message.Message
	for i := range 20 {
		msgs = append(msgs, assistantMsg(string(rune('a'+i)), "", "x"))
	}
	run := &fakeRun{ref: ref("s1", "u1"), msgs: msgs, pace: 5 * time.Millisecond}
	runtime := newTestRuntime(t, &fakeHarness{runs: []*fakeRun{run}})
	turn, err := runtime.Turn(context.Background(), "p")
	if err != nil {
		t.Fatal(err)
	}
	want := ids(msgs)
	var wg sync.WaitGroup
	results := make([][]string, 5)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(time.Duration(i) * 15 * time.Millisecond)
			var got []string
			for m, err := range turn.Messages() {
				if err != nil {
					t.Error(err)
					return
				}
				got = append(got, m.ID)
			}
			results[i] = got
		}()
	}
	wg.Wait()
	for i, got := range results {
		if !reflect.DeepEqual(got, want) {
			t.Errorf("consumer %d saw %v, want %v", i, got, want)
		}
	}
}

func TestMessagesStopsWhenConsumerBreaks(t *testing.T) {
	run := &fakeRun{ref: ref("s1", "u1"), msgs: []message.Message{userMsg("m1", "p"), assistantMsg("m2", "", "x")}, hold: true}
	runtime := newTestRuntime(t, &fakeHarness{runs: []*fakeRun{run}})
	turn, err := runtime.Turn(context.Background(), "p")
	if err != nil {
		t.Fatal(err)
	}
	for range turn.Messages() {
		break
	}
	turn.Terminate()
	turn.Result()
}

func TestFinishedTurnIsReadBack(t *testing.T) {
	run := &fakeRun{
		ref:      ref("s1", "u1"),
		finalRef: ref("s1", "u1-final"),
		msgs:     []message.Message{userMsg("m1", "p"), assistantMsg("m2", "", "live")},
	}
	h := &fakeHarness{
		runs:     []*fakeRun{run},
		readBack: []message.Message{userMsg("m1", "p"), assistantMsg("m2", "", "stored")},
	}
	runtime := newTestRuntime(t, h)
	turn, err := runtime.Turn(context.Background(), "p")
	if err != nil {
		t.Fatal(err)
	}
	turn.Result()

	msgs := collect(t, turn)
	if len(msgs) != 2 || msgs[1].Assistant.Text != "stored" {
		t.Fatalf("read-back messages = %+v", msgs)
	}
	for _, m := range msgs {
		if m.TurnID != turn.ID() {
			t.Fatalf("read-back TurnID = %q", m.TurnID)
		}
	}
	if len(h.readRefs) != 1 || h.readRefs[0].SessionID != "s1" || string(h.readRefs[0].Data) != string(run.finalRef.Data) {
		t.Fatalf("ReadTurn refs = %+v", h.readRefs)
	}

	h.readErr = errors.New("transcript missing")
	var gotErr error
	for _, err := range turn.Messages() {
		gotErr = err
	}
	if !errors.Is(gotErr, h.readErr) {
		t.Fatalf("Messages error = %v, want wrapped %v", gotErr, h.readErr)
	}
}

func TestConversationContinuesAfterStop(t *testing.T) {
	first := &fakeRun{ref: ref("s1", "u1"), hold: true}
	second := &fakeRun{ref: ref("s1", "u2"), msgs: []message.Message{assistantMsg("m1", "", "still here")}}
	h := &fakeHarness{runs: []*fakeRun{first, second}}
	runtime := newTestRuntime(t, h)
	turn, err := runtime.Turn(context.Background(), "remember x")
	if err != nil {
		t.Fatal(err)
	}
	turn.Terminate()
	turn.Result()
	next, err := runtime.Turn(context.Background(), "what was x")
	if err != nil {
		t.Fatal(err)
	}
	res, err := next.Result()
	if err != nil || res.FinalText != "still here" {
		t.Fatalf("Result = %+v, %v", res, err)
	}
	turns := readTurns(t, runtime)
	if len(turns) != 2 || turns[0]["status"] != "terminated" || turns[1]["status"] != "completed" {
		t.Fatalf("turns = %v", turns)
	}
}

func TestIndexHoldsNoContent(t *testing.T) {
	run := &fakeRun{
		ref: ref("s1", "u1"),
		msgs: []message.Message{
			userMsg("m1", "secret prompt words"),
			shellCall("c1", "echo hidden-command"),
			assistantMsg("m2", "", "private answer text"),
		},
	}
	runtime := newTestRuntime(t, &fakeHarness{runs: []*fakeRun{run}})
	turn, err := runtime.Turn(context.Background(), "secret prompt words")
	if err != nil {
		t.Fatal(err)
	}
	turn.Result()
	err = filepath.Walk(filepath.Join(os.Getenv("HOME"), ".cloudagent"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, s := range []string{"secret prompt", "hidden-command", "private answer"} {
			if strings.Contains(string(data), s) {
				t.Errorf("%s contains %q", path, s)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
