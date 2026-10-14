package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wenfengwang/cloud-agent-sdk/core/internal/fakeharness"
	"github.com/wenfengwang/cloud-agent-sdk/core/internal/harness"
	"github.com/wenfengwang/cloud-agent-sdk/core/internal/message"
	"github.com/wenfengwang/cloud-agent-sdk/core/internal/testenv"
)

// replay prepares the fake to replay the fixture in dir, one invocation per
// recorded process, writing the fixture's transcripts at the end of the last
// one. edit may replace the invocations.
func replay(t *testing.T, dir string, p paths, edit func([]fakeharness.Invocation) []fakeharness.Invocation) fakeharness.State {
	t.Helper()
	rec := readFixture(t, dir)
	var files []fakeharness.File
	for _, name := range rec.HomeFiles() {
		from, err := filepath.Abs(filepath.Join(dir, "home", filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, fakeharness.File{From: from, To: filepath.Join(p.harnessHome, filepath.FromSlash(name))})
	}
	invs := make([]fakeharness.Invocation, len(rec.Traffic))
	for i := range rec.Traffic {
		abs, err := filepath.Abs(filepath.Join(dir, fmt.Sprintf("traffic-%d.jsonl", i+1)))
		if err != nil {
			t.Fatal(err)
		}
		invs[i] = fakeharness.Invocation{Traffic: abs}
	}
	invs[len(invs)-1].Files = files
	if edit != nil {
		invs = edit(invs)
	}
	values := p.values("")
	delete(values, placeholderSessionID)
	t.Setenv("HOME", p.home)
	return fakeharness.Use(t, fakeBin, fakeharness.Script{
		Substitutions:    values,
		ArgSubstitutions: map[string][]string{placeholderSessionID: {"--session-id", "--resume"}},
		Invocations:      invs,
	})
}

// drain runs one turn to its end, calling onMessage for every message.
func drain(t *testing.T, h *Harness, prompt string, onMessage func(harness.Run, message.Message)) ([]message.Message, harness.Run, harness.Outcome, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	run, err := h.StartTurn(ctx, prompt)
	if err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	if run.Ref().SessionID == "" {
		t.Fatal("Ref().SessionID is empty at acceptance")
	}
	var msgs []message.Message
	timeout := time.After(20 * time.Second)
	for {
		select {
		case m, ok := <-run.Messages():
			if !ok {
				out, err := run.Wait()
				return msgs, run, out, err
			}
			msgs = append(msgs, m)
			if onMessage != nil {
				onMessage(run, m)
			}
		case <-timeout:
			run.Kill()
			t.Fatal("the run did not end")
		}
	}
}

// liveView encodes msgs as the golden does, scrubbing p back to placeholders.
func liveView(t *testing.T, p paths, sessionID string, msgs []message.Message) []byte {
	t.Helper()
	data, err := json.MarshalIndent(msgs, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return p.scrubber(sessionID).Scrub(data)
}

func encode(t *testing.T, msgs []message.Message) []byte {
	t.Helper()
	data, err := json.MarshalIndent(msgs, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestRunReplay drives the adapter's process and protocol layer against the
// fake replaying each completed scenario, then reads every turn back.
func TestRunReplay(t *testing.T) {
	for _, s := range testenv.Scenarios() {
		if s.TerminateWhen != nil {
			continue
		}
		t.Run(s.Name, func(t *testing.T) {
			dir := filepath.Join("testdata", s.Name)
			p := newPaths(t)
			state := replay(t, dir, p, nil)
			h := New(harness.Config{WorkDir: p.work, Home: p.harnessHome})
			var live []message.Message
			var refs []harness.Ref
			for i, prompt := range readFixture(t, dir).Meta.Prompts {
				msgs, run, out, err := drain(t, h, prompt, nil)
				if err != nil {
					t.Fatalf("turn %d: Wait: %v", i+1, err)
				}
				if out.Usage.OutputTokens == 0 {
					t.Errorf("turn %d: no usage reported", i+1)
				}
				refs = append(refs, run.Ref())
				live = append(live, msgs...)
			}
			if err := h.Close(); err != nil {
				t.Fatal(err)
			}
			if pids := state.LivePIDs(t); len(pids) != 0 {
				t.Fatalf("processes alive after Close: %v", pids)
			}
			want := goldenMessages(t, dir)
			compareJSON(t, encode(t, want), liveView(t, p, h.sessionID, live))

			for i := range refs {
				info := state.Info(t, i+1)
				requireArgs(t, info.Args, i == 0, h.sessionID)
				if info.Dir != resolve(p.work) && info.Dir != p.work {
					t.Errorf("invocation %d: dir %s, want %s", i+1, info.Dir, p.work)
				}
				if info.Env["CLAUDE_CONFIG_DIR"] != p.harnessHome {
					t.Errorf("invocation %d: CLAUDE_CONFIG_DIR %q", i+1, info.Env["CLAUDE_CONFIG_DIR"])
				}
			}
			var back []message.Message
			for _, ref := range refs {
				msgs, err := h.ReadTurn(context.Background(), ref)
				if err != nil {
					t.Fatal(err)
				}
				back = append(back, msgs...)
			}
			back = withoutUnstreamed(t, back, want)
			compareJSON(t, readBackView(t, want), p.scrubber(h.sessionID).Scrub(readBackView(t, back)))
		})
	}
}

func requireArgs(t *testing.T, args []string, first bool, sessionID string) {
	t.Helper()
	for _, want := range []string{"--print", "--verbose", "--replay-user-messages"} {
		if !slices.Contains(args, want) {
			t.Errorf("args %q lack %s", args, want)
		}
	}
	pairs := map[string]string{"--output-format": "stream-json", "--input-format": "stream-json", "--permission-mode": "bypassPermissions"}
	if first {
		pairs["--session-id"] = sessionID
	} else {
		pairs["--resume"] = sessionID
	}
	for flag, value := range pairs {
		i := slices.Index(args, flag)
		if i < 0 || i+1 >= len(args) || args[i+1] != value {
			t.Errorf("args %q: want %s %s", args, flag, value)
		}
	}
	if !first && slices.Contains(args, "--session-id") {
		t.Errorf("args %q: a later turn passes --session-id", args)
	}
}

func TestRunModel(t *testing.T) {
	p := newPaths(t)
	state := replay(t, filepath.Join("testdata", "text"), p, nil)
	h := New(harness.Config{WorkDir: p.work, Home: p.harnessHome, Model: "some-model"})
	if _, _, _, err := drain(t, h, "hello", nil); err != nil {
		t.Fatal(err)
	}
	args := state.Info(t, 1).Args
	if i := slices.Index(args, "--model"); i < 0 || args[i+1] != "some-model" {
		t.Errorf("args %q lack --model some-model", args)
	}
}

func TestRunNoHome(t *testing.T) {
	p := newPaths(t)
	state := replay(t, filepath.Join("testdata", "text"), p, nil)
	t.Setenv("CLAUDE_CONFIG_DIR", p.harnessHome)
	h := New(harness.Config{WorkDir: p.work})
	msgs, run, _, err := drain(t, h, "hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Info(t, 1).Env["CLAUDE_CONFIG_DIR"]; got != p.harnessHome {
		t.Errorf("CLAUDE_CONFIG_DIR = %q, want the inherited %q", got, p.harnessHome)
	}
	back, err := h.ReadTurn(context.Background(), run.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != len(msgs) {
		t.Errorf("read back %d messages, want %d", len(back), len(msgs))
	}
}

// TestRunInterrupt replays the terminate scenario: Interrupt sends the
// interrupt control request and the run ends.
func TestRunInterrupt(t *testing.T) {
	dir := filepath.Join("testdata", "terminate")
	s, _ := testenv.ScenarioByName("terminate")
	p := newPaths(t)
	state := replay(t, dir, p, nil)
	h := New(harness.Config{WorkDir: p.work, Home: p.harnessHome})
	interrupted := false
	msgs, run, _, _ := drain(t, h, s.Prompts[0], func(run harness.Run, m message.Message) {
		if !interrupted && s.TerminateWhen(m) {
			interrupted = true
			run.Interrupt()
			run.Interrupt()
		}
	})
	if !interrupted {
		t.Fatal("the terminate condition never matched")
	}
	compareJSON(t, encode(t, goldenMessages(t, dir)), liveView(t, p, h.sessionID, msgs))
	received := state.Received(t, 1)
	n := 0
	for _, line := range received {
		if strings.Contains(string(line), `"interrupt"`) {
			n++
		}
	}
	if n != 1 {
		t.Errorf("sent %d interrupt requests, want 1: %s", n, received)
	}
	run.Interrupt()
	run.Kill()
	if pids := state.LivePIDs(t); len(pids) != 0 {
		t.Fatalf("processes alive: %v", pids)
	}
}

// replayedLine returns the 1-based index, among recv lines, of the echoed
// user message in the fixture's first traffic.
func replayedLine(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	for _, l := range readFixture(t, dir).Traffic[0] {
		if l.Dir != fakeharness.DirRecv {
			continue
		}
		n++
		if strings.Contains(string(l.Line), `"isReplay":true`) {
			return n
		}
	}
	t.Fatal("no replayed user message")
	return 0
}

// TestRunKill hangs the fake after acceptance, ignoring the interrupt: Kill
// ends the run promptly, and the next turn starts a fresh process that
// resumes the session.
func TestRunKill(t *testing.T) {
	dir := filepath.Join("testdata", "text")
	p := newPaths(t)
	lines := replayedLine(t, dir)
	rec := readFixture(t, dir)
	state := replay(t, dir, p, func(invs []fakeharness.Invocation) []fakeharness.Invocation {
		// The same recorded process twice: first hung, then completing.
		hung := invs[0]
		hung.Files = nil
		hung.HangAfter = &fakeharness.Hang{Lines: lines, OnInterrupt: fakeharness.OnInterruptIgnore}
		return append([]fakeharness.Invocation{hung}, invs...)
	})

	h := New(harness.Config{WorkDir: p.work, Home: p.harnessHome})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	run, err := h.StartTurn(ctx, rec.Meta.Prompts[0])
	if err != nil {
		t.Fatal(err)
	}
	run.Interrupt()
	time.Sleep(200 * time.Millisecond)
	start := time.Now()
	run.Kill()
	for range run.Messages() {
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("Messages closed %s after Kill", d)
	}
	run.Wait()
	run.Kill()
	if pids := state.LivePIDs(t); len(pids) != 0 {
		t.Fatalf("processes alive after Kill: %v", pids)
	}

	if _, _, _, err := drain(t, h, "again", nil); err != nil {
		t.Fatal(err)
	}
	requireArgs(t, state.Info(t, 2).Args, false, h.sessionID)
}

func TestRunCrash(t *testing.T) {
	dir := filepath.Join("testdata", "text")
	p := newPaths(t)
	lines := replayedLine(t, dir)
	state := replay(t, dir, p, func(invs []fakeharness.Invocation) []fakeharness.Invocation {
		invs[0].CrashAfter = &fakeharness.Crash{Lines: lines + 1, ExitCode: 3}
		return invs
	})
	h := New(harness.Config{WorkDir: p.work, Home: p.harnessHome})
	_, _, _, err := drain(t, h, "hello", nil)
	if err == nil || !strings.Contains(err.Error(), "scripted crash") {
		t.Fatalf("Wait after a crash: %v", err)
	}
	if pids := state.LivePIDs(t); len(pids) != 0 {
		t.Fatalf("processes alive: %v", pids)
	}
}

func TestStartRejected(t *testing.T) {
	p := newPaths(t)
	state := replay(t, filepath.Join("testdata", "text"), p, func(invs []fakeharness.Invocation) []fakeharness.Invocation {
		invs[0].ExitAtStart = &fakeharness.Exit{Code: 1, Stderr: "rejected by script"}
		return invs
	})
	h := New(harness.Config{WorkDir: p.work, Home: p.harnessHome})
	_, err := h.StartTurn(context.Background(), "hello")
	if err == nil || !strings.Contains(err.Error(), "rejected by script") {
		t.Fatalf("StartTurn: %v", err)
	}
	if pids := state.LivePIDs(t); len(pids) != 0 {
		t.Fatalf("processes alive: %v", pids)
	}
}

func TestStartContextDone(t *testing.T) {
	dir := filepath.Join("testdata", "text")
	p := newPaths(t)
	state := replay(t, dir, p, func(invs []fakeharness.Invocation) []fakeharness.Invocation {
		invs[0].HangAfter = &fakeharness.Hang{Lines: 0, OnInterrupt: fakeharness.OnInterruptIgnore}
		return invs
	})
	h := New(harness.Config{WorkDir: p.work, Home: p.harnessHome})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := h.StartTurn(ctx, "hello"); err == nil {
		t.Fatal("StartTurn succeeded without acceptance")
	}
	if pids := state.LivePIDs(t); len(pids) != 0 {
		t.Fatalf("processes alive: %v", pids)
	}
}

func TestStartNotInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	h := New(harness.Config{WorkDir: t.TempDir(), Home: t.TempDir()})
	if _, err := h.StartTurn(context.Background(), "hello"); err == nil {
		t.Fatal("StartTurn succeeded without a claude executable")
	}
}

// TestStartResumesExistingSession: when a failed start already created the
// session transcript, the next turn must resume the session instead of
// creating it again, which Claude Code rejects.
func TestStartResumesExistingSession(t *testing.T) {
	p := newPaths(t)
	state := replay(t, filepath.Join("testdata", "text"), p, nil)
	h := New(harness.Config{WorkDir: p.work, Home: p.harnessHome})
	dir := filepath.Join(p.harnessHome, "projects", EncodeWorkDir(resolve(p.work)))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, h.sessionID+".jsonl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := drain(t, h, "hello", nil); err != nil {
		t.Fatal(err)
	}
	requireArgs(t, state.Info(t, 1).Args, false, h.sessionID)
}
