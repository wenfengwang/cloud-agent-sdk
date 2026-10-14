package cloudagent_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	cloudagent "github.com/wenfengwang/cloud-agent-sdk"
	"github.com/wenfengwang/cloud-agent-sdk/internal/fakeharness"
)

func TestTurnFailsWhenCLIIsMissing(t *testing.T) {
	for _, h := range []cloudagent.Harness{cloudagent.Claude, cloudagent.OpenAI} {
		t.Run(string(h), func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			state := fakeharness.Use(t, t.TempDir(), fakeharness.Script{})
			a, err := cloudagent.New(h, cloudagent.Options{WorkDir: t.TempDir(), Home: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			if _, err := a.Turn(context.Background(), "hello", cloudagent.TurnOptions{}); err == nil {
				t.Fatal("Turn succeeded without a harness CLI on PATH")
			}
			if n := state.Invocations(t); n != 0 {
				t.Fatalf("%d harness processes started", n)
			}
		})
	}
}

// TestTurnCompletes replays the text scenario through New: the turn ends as
// completed with the recorded messages, final text, and usage.
func TestTurnCompletes(t *testing.T) {
	for _, h := range harnesses {
		t.Run(string(h), func(t *testing.T) {
			e := newReplay(t, h, "text", nil)
			want := e.goldenTurns(t)[0]
			a := e.newAgent(t)
			turn, err := a.Turn(context.Background(), e.rec.Meta.Prompts[0], cloudagent.TurnOptions{})
			if err != nil {
				t.Fatalf("Turn: %v", err)
			}
			msgs := collect(t, turn, nil)
			res, err := result(t, turn)
			if err != nil {
				t.Fatalf("Result: %v", err)
			}
			if got := messageIDs(msgs); !slices.Equal(got, goldenIDs(want)) {
				t.Errorf("message IDs %v, want %v", got, goldenIDs(want))
			}
			if res.TurnID != turn.ID() || res.FinalText != goldenFinalText(want) || res.FinalText == "" {
				t.Errorf("Result %+v, want final text %q", res, goldenFinalText(want))
			}
			if res.Usage.OutputTokens == 0 {
				t.Errorf("Result has no usage: %+v", res.Usage)
			}
			e.requireStatus(t, a, turn, "completed")
		})
	}
}

// TestStartError: a harness that rejects the prompt makes Turn fail with a
// start error, records no turn, and leaves the agent ready for the next turn.
func TestStartError(t *testing.T) {
	for _, h := range harnesses {
		t.Run(string(h), func(t *testing.T) {
			e := newReplay(t, h, "text", func(e *replayEnv, invs []fakeharness.Invocation) []fakeharness.Invocation {
				if h == cloudagent.Claude {
					// Claude Code exits before echoing the prompt.
					reject := fakeharness.Invocation{ExitAtStart: &fakeharness.Exit{Code: 1, Stderr: "rejected by script"}}
					return append([]fakeharness.Invocation{reject}, invs...)
				}
				invs[0].RejectMethod = "turn/start"
				return invs
			})
			a := e.newAgent(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err := a.Turn(ctx, e.rec.Meta.Prompts[0], cloudagent.TurnOptions{}); err == nil {
				t.Fatal("Turn succeeded although the harness rejected the prompt")
			} else if !strings.Contains(err.Error(), "reject") {
				t.Errorf("start error %q does not carry the harness's reason", err)
			}
			if recs := e.turnRecords(t, a); len(recs) != 0 {
				t.Errorf("a rejected turn was recorded: %+v", recs)
			}

			turn, err := a.Turn(ctx, e.rec.Meta.Prompts[0], cloudagent.TurnOptions{})
			if err != nil {
				t.Fatalf("Turn after a start error: %v", err)
			}
			collect(t, turn, nil)
			if res, err := result(t, turn); err != nil || res.FinalText != goldenFinalText(e.goldenTurns(t)[0]) {
				t.Errorf("Result after a start error = %+v, %v", res, err)
			}
		})
	}
}

// TestStartCancelled: when ctx is cancelled while the harness has not yet
// accepted the prompt, Turn returns ctx.Err(), records no turn, and leaves
// no harness process of the start behind.
func TestStartCancelled(t *testing.T) {
	for _, h := range harnesses {
		t.Run(string(h), func(t *testing.T) {
			e := newReplay(t, h, "text", func(e *replayEnv, invs []fakeharness.Invocation) []fakeharness.Invocation {
				// Stall right before the line that accepts the prompt.
				invs[0].HangAfter = &fakeharness.Hang{Lines: e.recvLinesThrough(t, 1, markers[h].acceptance) - 1, OnInterrupt: fakeharness.OnInterruptHonor}
				return invs
			})
			a := e.newAgent(t)
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			turn, err := a.Turn(ctx, e.rec.Meta.Prompts[0], cloudagent.TurnOptions{})
			if turn != nil || err == nil || err != ctx.Err() {
				t.Fatalf("Turn = %v, %v; want nil, %v", turn, err, ctx.Err())
			}
			if recs := e.turnRecords(t, a); len(recs) != 0 {
				t.Errorf("a cancelled start was recorded: %+v", recs)
			}
			if pids := e.state.LivePIDs(t); len(pids) != 0 {
				t.Errorf("harness processes alive after the cancelled start: %v", pids)
			}
			// The agent is free for the next turn (which the script does
			// not let the fake accept).
			if _, err := a.Turn(context.Background(), "next", cloudagent.TurnOptions{}); errors.Is(err, cloudagent.ErrTurnInProgress) {
				t.Errorf("Turn after a cancelled start: %v", err)
			}
		})
	}
}

// TestStop replays the terminate scenario and stops the turn once its shell
// call appears, through each of the three stop causes of design 001.
func TestStop(t *testing.T) {
	causes := []struct {
		name   string
		status string
		want   error
	}{
		{"terminate", "terminated", cloudagent.ErrTerminated},
		{"cancel", "cancelled", context.Canceled},
		{"close", "closed", cloudagent.ErrClosed},
	}
	for _, h := range harnesses {
		for _, c := range causes {
			t.Run(string(h)+"/"+c.name, func(t *testing.T) {
				e := newReplay(t, h, "terminate", nil)
				want := e.goldenTurns(t)[0]
				a := e.newAgent(t)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				turn, err := a.Turn(ctx, e.rec.Meta.Prompts[0], cloudagent.TurnOptions{})
				if err != nil {
					t.Fatalf("Turn: %v", err)
				}
				closed := make(chan error, 1)
				stopped := false
				msgs := collect(t, turn, func(m cloudagent.Message) {
					if stopped || !isShellCall(m) {
						return
					}
					stopped = true
					switch c.name {
					case "terminate":
						if err := turn.Terminate(); err != nil {
							t.Errorf("Terminate: %v", err)
						}
					case "cancel":
						cancel()
					case "close":
						go func() { closed <- a.Close() }()
					}
				})
				if !stopped {
					t.Fatal("the shell call never appeared")
				}
				_, err = result(t, turn)
				if !errors.Is(err, c.want) {
					t.Fatalf("Result error %v, want %v", err, c.want)
				}
				if c.name == "close" {
					if err := <-closed; err != nil {
						t.Fatalf("Close: %v", err)
					}
				}
				// No process of the turn outlives its stop: Claude Code runs
				// one process per turn, and Codex shuts its app-server down
				// after a stopped turn.
				if pids := e.state.LivePIDs(t); len(pids) != 0 {
					t.Errorf("harness processes alive after the stop: %v", pids)
				}
				if got := messageIDs(msgs); !slices.Equal(got, goldenIDs(want)) {
					t.Errorf("message IDs %v, want %v", got, goldenIDs(want))
				}
				e.requireStatus(t, a, turn, c.status)
				// Terminate on a finished turn is a no-op, and Result is stable.
				if err := turn.Terminate(); err != nil {
					t.Errorf("Terminate after the end: %v", err)
				}
				if _, again := turn.Result(); again != err {
					t.Errorf("second Result error %v, want %v", again, err)
				}
			})
		}
	}
}

// TestStopIgnored: a harness that ignores the graceful stop is killed once
// the grace period of design 001 has passed, and the turn ends as stopped.
// The tool process it started in its own process group is killed with it.
func TestStopIgnored(t *testing.T) {
	for _, h := range harnesses {
		t.Run(string(h), func(t *testing.T) {
			e := newReplay(t, h, "terminate", func(e *replayEnv, invs []fakeharness.Invocation) []fakeharness.Invocation {
				invs[0].HangAfter = &fakeharness.Hang{Lines: e.recvLinesThrough(t, 1, markers[h].shellCall), OnInterrupt: fakeharness.OnInterruptIgnore, SpawnChild: true}
				return invs
			})
			a := e.newAgent(t)
			turn, err := a.Turn(context.Background(), e.rec.Meta.Prompts[0], cloudagent.TurnOptions{})
			if err != nil {
				t.Fatalf("Turn: %v", err)
			}
			t.Cleanup(func() {
				for _, pid := range e.state.Children(t) {
					syscall.Kill(pid, syscall.SIGKILL)
				}
			})
			var stoppedAt time.Time
			collect(t, turn, func(m cloudagent.Message) {
				if stoppedAt.IsZero() && isShellCall(m) {
					stoppedAt = time.Now()
					turn.Terminate()
				}
			})
			if _, err := result(t, turn); !errors.Is(err, cloudagent.ErrTerminated) {
				t.Fatalf("Result error %v, want ErrTerminated", err)
			}
			if d := time.Since(stoppedAt); d < 4*time.Second {
				t.Errorf("the turn ended %s after Terminate, before the grace period", d)
			}
			if pids := e.state.LivePIDs(t); len(pids) != 0 {
				t.Errorf("harness processes alive after the kill: %v", pids)
			}
			if len(e.state.Children(t)) != 1 {
				t.Fatal("the fake spawned no tool process")
			}
			if pids := e.state.LiveChildren(t); len(pids) != 0 {
				t.Errorf("tool processes alive after the kill: %v", pids)
			}
			e.requireStatus(t, a, turn, "terminated")
		})
	}
}

// TestProcessCrash: a harness process that dies after accepting the prompt
// fails the turn with a wrapped harness error.
func TestProcessCrash(t *testing.T) {
	for _, h := range harnesses {
		t.Run(string(h), func(t *testing.T) {
			e := newReplay(t, h, "text", func(e *replayEnv, invs []fakeharness.Invocation) []fakeharness.Invocation {
				invs[0].CrashAfter = &fakeharness.Crash{Lines: e.recvLinesThrough(t, 1, markers[h].accepted), ExitCode: 3}
				return invs
			})
			a := e.newAgent(t)
			turn, err := a.Turn(context.Background(), e.rec.Meta.Prompts[0], cloudagent.TurnOptions{})
			if err != nil {
				t.Fatalf("Turn: %v", err)
			}
			collect(t, turn, nil)
			_, err = result(t, turn)
			if err == nil {
				t.Fatal("Result reported no error after the harness crashed")
			}
			for _, sentinel := range []error{cloudagent.ErrTerminated, cloudagent.ErrClosed, context.Canceled} {
				if errors.Is(err, sentinel) {
					t.Errorf("crash reported as %v", sentinel)
				}
			}
			e.requireStatus(t, a, turn, "failed")
		})
	}
}

// TestTurnInProgress: a second Turn while one runs fails with
// ErrTurnInProgress and leaves the running turn alone.
func TestTurnInProgress(t *testing.T) {
	for _, h := range harnesses {
		t.Run(string(h), func(t *testing.T) {
			e := newReplay(t, h, "terminate", nil)
			a := e.newAgent(t)
			turn, err := a.Turn(context.Background(), e.rec.Meta.Prompts[0], cloudagent.TurnOptions{})
			if err != nil {
				t.Fatalf("Turn: %v", err)
			}
			checked := false
			collect(t, turn, func(m cloudagent.Message) {
				if checked || !isShellCall(m) {
					return
				}
				checked = true
				if _, err := a.Turn(context.Background(), "second", cloudagent.TurnOptions{}); !errors.Is(err, cloudagent.ErrTurnInProgress) {
					t.Errorf("second Turn error %v, want ErrTurnInProgress", err)
				}
				turn.Terminate()
			})
			if _, err := result(t, turn); !errors.Is(err, cloudagent.ErrTerminated) {
				t.Fatalf("Result error %v, want ErrTerminated", err)
			}
			if n := len(e.turnRecords(t, a)); n != 1 {
				t.Errorf("%d turns recorded, want 1", n)
			}
		})
	}
}

// TestClosed: after Close, Turn fails with ErrClosed, Close is idempotent,
// and a turn that ended before can still be read.
func TestClosed(t *testing.T) {
	for _, h := range harnesses {
		t.Run(string(h), func(t *testing.T) {
			e := newReplay(t, h, "text", nil)
			a := e.newAgent(t)
			turn, err := a.Turn(context.Background(), e.rec.Meta.Prompts[0], cloudagent.TurnOptions{})
			if err != nil {
				t.Fatalf("Turn: %v", err)
			}
			live := collect(t, turn, nil)
			res, err := result(t, turn)
			if err != nil {
				t.Fatalf("Result: %v", err)
			}
			if err := a.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			if pids := e.state.LivePIDs(t); len(pids) != 0 {
				t.Errorf("harness processes alive after Close: %v", pids)
			}
			if _, err := a.Turn(context.Background(), "after close", cloudagent.TurnOptions{}); !errors.Is(err, cloudagent.ErrClosed) {
				t.Errorf("Turn after Close: %v, want ErrClosed", err)
			}
			if err := a.Close(); err != nil {
				t.Errorf("second Close: %v", err)
			}
			if got := messageIDs(collect(t, turn, nil)); !slices.Equal(got, messageIDs(live)) {
				t.Errorf("read back after Close %v, want %v", got, messageIDs(live))
			}
			if again, err := turn.Result(); err != nil || again != res {
				t.Errorf("Result after Close = %+v, %v; want %+v", again, err, res)
			}
		})
	}
}

// TestContinuity replays the two-turn scenario: the second turn continues
// the first's conversation, and the harness never asks for permission.
func TestContinuity(t *testing.T) {
	for _, h := range harnesses {
		t.Run(string(h), func(t *testing.T) {
			e := newReplay(t, h, "two-turn", nil)
			golden := e.goldenTurns(t)
			a := e.newAgent(t)
			for i, prompt := range e.rec.Meta.Prompts {
				turn, err := a.Turn(context.Background(), prompt, cloudagent.TurnOptions{})
				if err != nil {
					t.Fatalf("turn %d: Turn: %v", i+1, err)
				}
				collect(t, turn, nil)
				res, err := result(t, turn)
				if err != nil {
					t.Fatalf("turn %d: Result: %v", i+1, err)
				}
				if want := goldenFinalText(golden[i]); res.FinalText != want {
					t.Errorf("turn %d: final text %q, want %q", i+1, res.FinalText, want)
				}
			}
			session := e.agentRecord(t, a)["harness_session_id"]
			if session == "" {
				t.Fatal("agent.json has no harness_session_id")
			}
			switch h {
			case cloudagent.Claude:
				if n := e.state.Invocations(t); n != 2 {
					t.Fatalf("%d claude processes, want one per turn", n)
				}
				first, second := e.state.Info(t, 1).Args, e.state.Info(t, 2).Args
				requireFlag(t, first, "--session-id", session)
				requireFlag(t, second, "--resume", session)
				if slices.Contains(second, "--session-id") {
					t.Errorf("second turn passes --session-id: %q", second)
				}
				for _, args := range [][]string{first, second} {
					requireFlag(t, args, "--permission-mode", "bypassPermissions")
				}
			case cloudagent.OpenAI:
				if n := e.state.Invocations(t); n != 1 {
					t.Fatalf("%d app-server processes, want 1", n)
				}
				starts := requests(t, e.state, "thread/start")
				if len(starts) != 1 || starts[0]["approvalPolicy"] != "never" || starts[0]["sandbox"] != "danger-full-access" {
					t.Errorf("thread/start requests %v", starts)
				}
				turns := requests(t, e.state, "turn/start")
				if len(turns) != 2 {
					t.Fatalf("%d turn/start requests, want 2", len(turns))
				}
				for _, p := range turns {
					if p["threadId"] != session {
						t.Errorf("turn/start on thread %v, want %s", p["threadId"], session)
					}
				}
			}
		})
	}
}

func requireFlag(t *testing.T, args []string, flag, value string) {
	t.Helper()
	i := slices.Index(args, flag)
	if i < 0 || i+1 >= len(args) || args[i+1] != value {
		t.Errorf("args %q lack %s %s", args, flag, value)
	}
}

// requests returns the params of every JSON-RPC request with method that
// the first fake process received.
func requests(t *testing.T, state fakeharness.State, method string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range state.Received(t, 1) {
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
