package core_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/wenfengwang/cloud-agent-sdk/core"
	"github.com/wenfengwang/cloud-agent-sdk/core/internal/testenv"
)

// e2eTurnTimeout bounds one real turn.
const e2eTurnTimeout = 5 * time.Minute

// followUpPrompt is the turn the real CLI tests run after a stopped turn, to
// check that the conversation continues.
const followUpPrompt = "Reply with exactly the word ok and nothing else. Do not use any tools."

// requireE2E skips the test unless CLOUDAGENT_E2E=1.
func requireE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("CLOUDAGENT_E2E") != "1" {
		t.Skip("set CLOUDAGENT_E2E=1 to run against the pinned CLIs")
	}
}

// newE2E isolates the test, puts the pinned CLIs first on PATH (failing on a
// version mismatch), prepares a credentialed harness home, and seeds a work
// directory with the scenario's files. The returned environment has no
// fixture.
func newE2E(t *testing.T, h core.Harness, s testenv.Scenario) *replayEnv {
	t.Helper()
	e := &replayEnv{harness: h, home: testenv.Isolate(t)}
	testenv.RequirePinnedCLIs(t)
	switch h {
	case core.ClaudeCode:
		e.harnessHome = testenv.ClaudeEnv(t).Home
	case core.Codex:
		e.harnessHome = testenv.CodexEnv(t).Home
		testenv.SetCodexDeveloperInstructions(t, e.harnessHome, testenv.CodexApplyPatchInstructions)
	}
	e.work = t.TempDir()
	testenv.SeedWorkDir(t, e.work, s)
	return e
}

// e2eAgent returns an agent on the environment and a watch of the processes
// it starts. Cleanup closes the agent and fails the test if any process the
// watch saw is still alive.
func (e *replayEnv) e2eAgent(t *testing.T) (*core.AgentRuntime, *processWatch) {
	t.Helper()
	a, err := core.New(e.harness, core.Options{WorkDir: e.work, Home: e.harnessHome})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	w := watchProcesses(t)
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
		w.requireGone(t)
	})
	return a, w
}

// processWatch samples the descendants of the test process, so that a test
// can check that none survives Close, even one moved out of the process
// tree.
type processWatch struct {
	mu   sync.Mutex
	seen map[int]bool
	stop chan struct{}
	done chan struct{}
}

func watchProcesses(t *testing.T) *processWatch {
	t.Helper()
	w := &processWatch{seen: map[int]bool{}, stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			w.sample()
			select {
			case <-w.stop:
				return
			case <-tick.C:
			}
		}
	}()
	t.Cleanup(w.halt)
	return w
}

func (w *processWatch) sample() {
	pids := descendants(os.Getpid())
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, pid := range pids {
		w.seen[pid] = true
	}
}

func (w *processWatch) halt() {
	select {
	case <-w.stop:
	default:
		close(w.stop)
	}
	<-w.done
}

// requireGone stops the watch and fails the test if a process it saw, or a
// descendant of the test process, is still alive.
func (w *processWatch) requireGone(t *testing.T) {
	t.Helper()
	w.halt()
	w.requireNoneAlive(t, "Close")
}

// requireNoneAlive fails the test if a process the watch saw, or a
// descendant of the test process, is still alive after the event named by
// after. The watch keeps running.
func (w *processWatch) requireNoneAlive(t *testing.T, after string) {
	t.Helper()
	w.sample()
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.seen) == 0 {
		t.Error("no harness process was ever observed")
	}
	for pid := range w.seen {
		if alive(pid) {
			t.Errorf("process %d (%s) is alive after %s", pid, processCommand(pid), after)
		}
	}
}

// descendants lists the live descendants of pid.
func descendants(pid int) []int {
	out, _ := exec.Command("pgrep", "-P", strconv.Itoa(pid)).Output()
	var pids []int
	for _, f := range strings.Fields(string(out)) {
		child, err := strconv.Atoi(f)
		if err != nil {
			continue
		}
		pids = append(pids, child)
		pids = append(pids, descendants(child)...)
	}
	return pids
}

// alive reports whether pid names a running, non-zombie process.
func alive(pid int) bool {
	if err := syscall.Kill(pid, 0); err != nil {
		return false
	}
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	return err == nil && !strings.HasPrefix(strings.TrimSpace(string(out)), "Z")
}

func processCommand(pid int) string {
	out, _ := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	return strings.TrimSpace(string(out))
}

// e2eTurn runs one real turn to its end. terminateWhen, if not nil, makes the
// test call Terminate at the first message for which it holds.
func e2eTurn(t *testing.T, a *core.AgentRuntime, prompt string, terminateWhen func(core.Message) bool) (*core.Turn, []core.Message, core.Result, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), e2eTurnTimeout)
	defer cancel()
	turn, err := a.Turn(ctx, prompt, core.TurnOptions{})
	if err != nil {
		t.Fatalf("Turn: %v", err)
	}
	stopped := false
	live := collect(t, turn, func(m core.Message) {
		if terminateWhen != nil && !stopped && terminateWhen(m) {
			stopped = true
			if err := turn.Terminate(); err != nil {
				t.Errorf("Terminate: %v", err)
			}
		}
	})
	if terminateWhen != nil && !stopped {
		t.Errorf("the terminate condition never held: %s", kindList(live))
	}
	res, err := result(t, turn)
	return turn, live, res, err
}

// TestE2EScenarios runs every scenario of design 002 against the pinned
// CLIs through the public API and checks its structural expectations, the
// read-back of every turn from the harness home, and the index.
func TestE2EScenarios(t *testing.T) {
	requireE2E(t)
	for _, h := range harnesses {
		for _, s := range testenv.Scenarios() {
			t.Run(string(h)+"/"+s.Name, func(t *testing.T) {
				e := newE2E(t, h, s)
				a, watch := e.e2eAgent(t)
				run := scenarioRun{agent: a}
				for i, prompt := range s.Prompts {
					turn, live, res, err := e2eTurn(t, a, prompt, s.TerminateWhen)
					run.turns = append(run.turns, turn)
					run.live = append(run.live, live)
					requireWellFormed(t, live)
					if s.TerminateWhen != nil {
						if !errors.Is(err, core.ErrTerminated) {
							t.Fatalf("turn %d: Result error %v, want ErrTerminated", i+1, err)
						}
						e.requireStatus(t, a, turn, "terminated")
						// No tool process of the turn, such as its "sleep 30",
						// and no harness process outlives the stop.
						watch.requireNoneAlive(t, "the stop")
					} else {
						if err != nil {
							t.Fatalf("turn %d: Result: %v\n%s", i+1, err, kindList(live))
						}
						requireResult(t, turn, res, live)
						e.requireStatus(t, a, turn, "completed")
					}
					requireReadBack(t, h, turn, live)
				}
				e2eExpectations[h][s.Name](t, e, run.live)

				if s.TerminateWhen != nil {
					// After a stop, the conversation continues.
					turn, live, res, err := e2eTurn(t, a, followUpPrompt, nil)
					if err != nil {
						t.Fatalf("turn after the stop: Result: %v\n%s", err, kindList(live))
					}
					requireResult(t, turn, res, live)
					requireReadBack(t, h, turn, live)
					e.requireStatus(t, a, turn, "completed")
					run.live = append(run.live, live)
				}
				if err := a.Close(); err != nil {
					t.Fatalf("Close: %v", err)
				}
				if s.TerminateWhen == nil {
					requireIndexFields(t, e, run)
				}
				prompts := s.Prompts
				if s.TerminateWhen != nil {
					prompts = append(prompts[:len(prompts):len(prompts)], followUpPrompt)
				}
				requireNoIndexContent(t, e, prompts, run.live)
			})
		}
	}
}

// TestE2EClose closes the agent while a real turn runs "sleep 30": the turn
// ends as closed, no harness process survives, Turn fails with ErrClosed,
// and the ended turn can still be read.
func TestE2EClose(t *testing.T) {
	requireE2E(t)
	s, _ := testenv.ScenarioByName("terminate")
	for _, h := range harnesses {
		t.Run(string(h), func(t *testing.T) {
			e := newE2E(t, h, s)
			a, watch := e.e2eAgent(t)
			ctx, cancel := context.WithTimeout(context.Background(), e2eTurnTimeout)
			defer cancel()
			turn, err := a.Turn(ctx, s.Prompts[0], core.TurnOptions{})
			if err != nil {
				t.Fatalf("Turn: %v", err)
			}
			closed := make(chan error, 1)
			var once sync.Once
			live := collect(t, turn, func(m core.Message) {
				if s.TerminateWhen(m) {
					once.Do(func() { go func() { closed <- a.Close() }() })
				}
			})
			select {
			case err := <-closed:
				if err != nil {
					t.Fatalf("Close: %v", err)
				}
			case <-time.After(20 * time.Second):
				t.Fatalf("Close did not return; messages: %s", kindList(live))
			}
			if _, err := result(t, turn); !errors.Is(err, core.ErrClosed) {
				t.Fatalf("Result error %v, want ErrClosed", err)
			}
			watch.requireGone(t)
			e.requireStatus(t, a, turn, "closed")
			if _, err := a.Turn(ctx, followUpPrompt, core.TurnOptions{}); !errors.Is(err, core.ErrClosed) {
				t.Errorf("Turn after Close: %v, want ErrClosed", err)
			}
			requireReadBack(t, h, turn, live)
		})
	}
}

// TestE2EVersionMismatch: the pinned-CLI check that gates the real CLI tests
// fails when an installed CLI reports a version other than its pin.
func TestE2EVersionMismatch(t *testing.T) {
	requireE2E(t)
	pins, err := testenv.LoadVersions(testenv.RepoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	real := testenv.ToolsBin(t)
	if err := testenv.CheckPinnedCLIs(real, pins); err != nil {
		t.Fatalf("the installed CLIs do not match their pins: %v", err)
	}
	for _, name := range []string{"claude", "codex"} {
		t.Run(name, func(t *testing.T) {
			bin := t.TempDir()
			for _, other := range []string{"claude", "codex"} {
				// The pinned CLI itself, or one that reports another version.
				script := "#!/bin/sh\nexec '" + filepath.Join(real, other) + "' \"$@\"\n"
				if other == name {
					script = "#!/bin/sh\necho 0.0.1\n"
				}
				if err := os.WriteFile(filepath.Join(bin, other), []byte(script), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			err := testenv.CheckPinnedCLIs(bin, pins)
			if err == nil || !strings.Contains(err.Error(), name+" --version") {
				t.Fatalf("CheckPinnedCLIs = %v, want a %s version mismatch", err, name)
			}
		})
	}
}

// requireResult checks a completed turn's Result against its messages.
func requireResult(t *testing.T, turn *core.Turn, res core.Result, live []core.Message) {
	t.Helper()
	want := ""
	for _, m := range live {
		if m.Assistant != nil && m.ParentToolUseID == "" {
			want = m.Assistant.Text
		}
	}
	if res.TurnID != turn.ID() || res.FinalText != want || want == "" {
		t.Errorf("Result %+v, want final text %q", res, want)
	}
	if res.Usage.OutputTokens <= 0 || res.Usage.InputTokens+res.Usage.CacheReadTokens <= 0 {
		t.Errorf("Result usage %+v counts no tokens", res.Usage)
	}
}

// requireReadBack reads an ended turn back through Messages and compares it
// with the live messages, IDs included, apart from the differences design
// 001 lists.
func requireReadBack(t *testing.T, h core.Harness, turn *core.Turn, live []core.Message) {
	t.Helper()
	back := collect(t, turn, nil)
	for _, m := range back {
		if len(m.Raw) == 0 {
			t.Errorf("read-back message %s has no Raw", m.ID)
		}
	}
	back, _ = withoutUnstreamed(t, h, back, live)
	// Codex tool items of one millisecond may be read back in another
	// order (design 001); everything else must match in order.
	back, live = testenv.SameMillisecondOrder(back), testenv.SameMillisecondOrder(live)
	// Message IDs are stable between live and read-back (design 001).
	if got, want := messageIDs(back), messageIDs(live); !slices.Equal(got, want) {
		t.Errorf("read-back message IDs %v, want the live IDs %v", got, want)
	}
	if got, want := comparable(t, back), comparable(t, live); got != want {
		t.Errorf("read back\n%s\nwant\n%s", got, want)
	}
}

// requireWellFormed checks the message invariants of design 001 on one
// turn's live messages.
func requireWellFormed(t *testing.T, msgs []core.Message) {
	t.Helper()
	if len(msgs) == 0 || msgs[0].Kind != core.KindUser || msgs[0].ParentToolUseID != "" {
		t.Fatalf("the turn does not start with its user message: %s", kindList(msgs))
	}
	ids := map[string]bool{}
	calls := map[string]core.ToolName{}
	for _, m := range msgs {
		if m.ID == "" || ids[m.ID] {
			t.Errorf("message ID %q is empty or repeated", m.ID)
		}
		ids[m.ID] = true
		if len(m.Raw) == 0 {
			t.Errorf("message %s has no Raw", m.ID)
		}
		set := 0
		for _, ok := range []bool{m.User != nil, m.Assistant != nil, m.Reasoning != nil, m.ToolCall != nil, m.ToolResult != nil} {
			if ok {
				set++
			}
		}
		if set != 1 || !kindMatches(m) {
			t.Errorf("message %s of kind %s has %d payloads or the wrong one", m.ID, m.Kind, set)
		}
		if p := m.ParentToolUseID; p != "" && calls[p] != core.ToolNameSubagent {
			t.Errorf("message %s has ParentToolUseID %q, which names no earlier subagent call", m.ID, p)
		}
		if c := m.ToolCall; c != nil {
			if c.ID == "" || c.Input == nil || c.RawName == "" {
				t.Errorf("tool call %+v is incomplete", c)
			}
			calls[c.ID] = c.Name
		}
		if r := m.ToolResult; r != nil {
			if name, ok := calls[r.CallID]; !ok || name != r.Name || r.Output == nil {
				t.Errorf("tool result %+v matches no earlier call", r)
			}
		}
	}
}

func kindMatches(m core.Message) bool {
	switch m.Kind {
	case core.KindUser:
		return m.User != nil
	case core.KindAssistant:
		return m.Assistant != nil
	case core.KindReasoning:
		return m.Reasoning != nil
	case core.KindToolCall:
		return m.ToolCall != nil
	case core.KindToolResult:
		return m.ToolResult != nil
	}
	return false
}

// kindList summarizes messages as "kind[:tool]" words, for failure output.
func kindList(msgs []core.Message) string {
	var parts []string
	for _, m := range msgs {
		p := string(m.Kind)
		if m.ToolCall != nil {
			p += ":" + string(m.ToolCall.Name) + "(" + m.ToolCall.RawName + ")"
		}
		if m.ToolResult != nil {
			p += ":" + string(m.ToolResult.Name)
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, " ")
}
