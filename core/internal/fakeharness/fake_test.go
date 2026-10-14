package fakeharness

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

var binDir string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "fakeharness-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := Build(dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	binDir = dir
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type proc struct {
	t      *testing.T
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	lines  chan string
	stderr strings.Builder
}

// start runs the fake as name with s, the way an adapter would.
func start(t *testing.T, name string, s Script, args ...string) (*proc, State) {
	t.Helper()
	state := Use(t, binDir, s)
	cmd := exec.Command(name, args...)
	cmd.Dir = t.TempDir()
	p := &proc{t: t, cmd: cmd, lines: make(chan string, 100)}
	cmd.Stderr = &p.stderr
	var err error
	if p.stdin, err = cmd.StdinPipe(); err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			p.lines <- sc.Text()
		}
		close(p.lines)
	}()
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})
	return p, state
}

func (p *proc) send(line string) {
	p.t.Helper()
	if _, err := io.WriteString(p.stdin, line+"\n"); err != nil {
		p.t.Fatalf("write stdin: %v", err)
	}
}

func (p *proc) recv() string {
	p.t.Helper()
	select {
	case l, ok := <-p.lines:
		if !ok {
			p.t.Fatalf("stdout closed; stderr: %s", p.stderr.String())
		}
		return l
	case <-time.After(5 * time.Second):
		p.t.Fatal("timed out waiting for a stdout line")
	}
	return ""
}

func (p *proc) noLine(d time.Duration) {
	p.t.Helper()
	select {
	case l, ok := <-p.lines:
		if ok {
			p.t.Fatalf("unexpected stdout line %s", l)
		}
	case <-time.After(d):
	}
}

// wait returns the exit code, failing after a timeout.
func (p *proc) wait() int {
	p.t.Helper()
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case err := <-done:
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		if err != nil {
			p.t.Fatalf("wait: %v", err)
		}
		return 0
	case <-time.After(5 * time.Second):
		p.t.Fatal("process did not exit")
	}
	return -1
}

func (p *proc) alive(d time.Duration) bool {
	time.Sleep(d)
	return syscall.Kill(p.cmd.Process.Pid, 0) == nil && p.cmd.ProcessState == nil
}

func traffic(t *testing.T, lines ...string) string {
	t.Helper()
	var tl []TrafficLine
	for _, l := range lines {
		dir, line, _ := strings.Cut(l, " ")
		tl = append(tl, TrafficLine{Dir: dir, Line: json.RawMessage(line)})
	}
	path := filepath.Join(t.TempDir(), "traffic-1.jsonl")
	WriteTraffic(t, path, tl)
	return path
}

func TestClaudeReplay(t *testing.T) {
	workDir := t.TempDir()
	home := t.TempDir()
	fixture := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(fixture, []byte(`{"cwd":"@WORKDIR@","sessionId":"@SESSION_ID@"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	trafficPath := traffic(t,
		`send {"type":"user","message":{"role":"user","content":"hi"}}`,
		`recv {"type":"system","subtype":"init","cwd":"@WORKDIR@","session_id":"@SESSION_ID@"}`,
		`recv {"type":"assistant","message":{"content":[{"type":"text","text":"hello"}]}}`,
		`send {"type":"control_request","request_id":"rec-1","request":{"subtype":"interrupt"}}`,
		`recv {"type":"control_response","response":{"subtype":"success","request_id":"rec-1"}}`,
		`recv {"type":"result","subtype":"success"}`,
	)
	p, state := start(t, "claude", Script{
		Substitutions:    map[string]string{"@WORKDIR@": workDir},
		ArgSubstitutions: map[string][]string{"@SESSION_ID@": {"--session-id", "--resume"}},
		Invocations: []Invocation{{
			Traffic: trafficPath,
			Files:   []File{{From: fixture, To: filepath.Join(home, "projects", "@SESSION_ID@.jsonl")}},
		}},
	}, "--print", "--session-id", "sess-123")

	// A line of another kind is recorded and skipped.
	p.send(`{"type":"keep_alive"}`)
	p.send(`{"type":"user","message":{"role":"user","content":"hi"}}`)
	if got, want := p.recv(), `{"type":"system","subtype":"init","cwd":"`+workDir+`","session_id":"sess-123"}`; got != want {
		t.Fatalf("line 1 = %s, want %s", got, want)
	}
	p.recv()
	p.noLine(100 * time.Millisecond)
	p.send(`{"type":"control_request","request_id":"live-9","request":{"subtype":"interrupt"}}`)
	var resp struct {
		Response struct {
			RequestID string `json:"request_id"`
		} `json:"response"`
	}
	if err := json.Unmarshal([]byte(p.recv()), &resp); err != nil || resp.Response.RequestID != "live-9" {
		t.Fatalf("control_response request_id = %q (%v), want live-9", resp.Response.RequestID, err)
	}
	p.recv()

	// Files are written at the end of the traffic; the process then waits
	// for stdin EOF.
	deadline := time.Now().Add(5 * time.Second)
	written := filepath.Join(home, "projects", "sess-123.jsonl")
	for {
		data, err := os.ReadFile(written)
		if err == nil {
			if want := `{"cwd":"` + workDir + `","sessionId":"sess-123"}` + "\n"; string(data) != want {
				t.Fatalf("file = %s, want %s", data, want)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("file not written: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !p.alive(50 * time.Millisecond) {
		t.Fatal("process exited before stdin EOF")
	}
	p.stdin.Close()
	if code := p.wait(); code != 0 {
		t.Fatalf("exit code %d; stderr %s", code, p.stderr.String())
	}

	if n := state.Invocations(t); n != 1 {
		t.Fatalf("invocations = %d", n)
	}
	info := state.Info(t, 1)
	if strings.Join(info.Args, " ") != "--print --session-id sess-123" {
		t.Fatalf("args = %v", info.Args)
	}
	if info.Dir != p.cmd.Dir {
		resolved, _ := filepath.EvalSymlinks(p.cmd.Dir)
		if info.Dir != resolved {
			t.Fatalf("dir = %q, want %q", info.Dir, p.cmd.Dir)
		}
	}
	if got := len(state.Received(t, 1)); got != 3 {
		t.Fatalf("received %d lines, want 3", got)
	}
	if live := state.LivePIDs(t); len(live) != 0 {
		t.Fatalf("live pids %v", live)
	}
}

func TestCodexReplayRewritesResponseIDs(t *testing.T) {
	trafficPath := traffic(t,
		`send {"id":0,"method":"initialize","params":{}}`,
		`recv {"id":0,"result":{"userAgent":"x"}}`,
		`send {"method":"initialized"}`,
		`send {"id":1,"method":"thread/start","params":{}}`,
		`recv {"id":1,"result":{"thread":{"id":"th"}}}`,
		`recv {"method":"thread/started","params":{}}`,
	)
	t.Setenv("CODEX_HOME", "/codex-home")
	p, state := start(t, "codex", Script{Invocations: []Invocation{{Traffic: trafficPath, ExitAtEnd: true}}}, "app-server")
	p.send(`{"id":41,"method":"initialize","params":{}}`)
	if got := p.recv(); got != `{"id":41,"result":{"userAgent":"x"}}` {
		t.Fatalf("initialize response = %s", got)
	}
	p.send(`{"method":"initialized"}`)
	p.send(`{"id":"req-2","method":"thread/start","params":{}}`)
	if got := p.recv(); got != `{"id":"req-2","result":{"thread":{"id":"th"}}}` {
		t.Fatalf("thread/start response = %s", got)
	}
	p.recv()
	if code := p.wait(); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if env := state.Info(t, 1).Env; env["CODEX_HOME"] != "/codex-home" {
		t.Fatalf("env = %v", env)
	}
}

func TestCodexRejectMethod(t *testing.T) {
	trafficPath := traffic(t,
		`send {"id":0,"method":"turn/start","params":{}}`,
		`recv {"id":0,"result":{}}`,
	)
	p, _ := start(t, "codex", Script{Invocations: []Invocation{{Traffic: trafficPath, RejectMethod: "turn/start"}}}, "app-server")
	p.send(`{"id":5,"method":"turn/start","params":{}}`)
	var resp struct {
		ID    int `json:"id"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(p.recv()), &resp); err != nil || resp.ID != 5 || resp.Error == nil {
		t.Fatalf("response = %+v (%v), want an error for id 5", resp, err)
	}
}

func TestExitAtStart(t *testing.T) {
	p, _ := start(t, "claude", Script{Invocations: []Invocation{{ExitAtStart: &Exit{Code: 1, Stderr: "Invalid API key"}}}})
	if code := p.wait(); code != 1 {
		t.Fatalf("exit code %d", code)
	}
	if !strings.Contains(p.stderr.String(), "Invalid API key") {
		t.Fatalf("stderr = %q", p.stderr.String())
	}
}

func TestCrashAfter(t *testing.T) {
	trafficPath := traffic(t,
		`recv {"type":"system"}`,
		`recv {"type":"assistant"}`,
		`recv {"type":"result"}`,
	)
	p, _ := start(t, "claude", Script{Invocations: []Invocation{{Traffic: trafficPath, CrashAfter: &Crash{Lines: 2, ExitCode: 3}}}})
	p.recv()
	p.recv()
	if code := p.wait(); code != 3 {
		t.Fatalf("exit code %d", code)
	}
}

func TestHangHonorsInterrupt(t *testing.T) {
	trafficPath := traffic(t,
		`recv {"type":"system"}`,
		`send {"type":"control_request","request_id":"r","request":{"subtype":"interrupt"}}`,
		`recv {"type":"result","subtype":"error_during_execution"}`,
	)
	p, _ := start(t, "claude", Script{Invocations: []Invocation{{Traffic: trafficPath, ExitAtEnd: true, HangAfter: &Hang{Lines: 1, OnInterrupt: OnInterruptHonor}}}})
	p.recv()
	p.noLine(100 * time.Millisecond)
	p.send(`{"type":"control_request","request_id":"x","request":{"subtype":"interrupt"}}`)
	if got := p.recv(); !strings.Contains(got, "error_during_execution") {
		t.Fatalf("line after interrupt = %s", got)
	}
	if code := p.wait(); code != 0 {
		t.Fatalf("exit code %d", code)
	}
}

func TestHangHonorsSignal(t *testing.T) {
	trafficPath := traffic(t, `recv {"type":"system"}`, `recv {"type":"result"}`)
	p, _ := start(t, "claude", Script{Invocations: []Invocation{{Traffic: trafficPath, HangAfter: &Hang{Lines: 1, OnInterrupt: OnInterruptHonor}}}})
	p.recv()
	p.cmd.Process.Signal(syscall.SIGTERM)
	if code := p.wait(); code != 143 {
		t.Fatalf("exit code %d", code)
	}
}

func TestHangIgnoresInterrupt(t *testing.T) {
	trafficPath := traffic(t, `recv {"method":"turn/started"}`, `send {"id":1,"method":"turn/interrupt"}`)
	p, _ := start(t, "codex", Script{Invocations: []Invocation{{Traffic: trafficPath, HangAfter: &Hang{Lines: 1, OnInterrupt: OnInterruptIgnore}}}}, "app-server")
	p.recv()
	p.send(`{"id":9,"method":"turn/interrupt"}`)
	p.cmd.Process.Signal(syscall.SIGTERM)
	p.cmd.Process.Signal(syscall.SIGINT)
	p.stdin.Close()
	if !p.alive(200 * time.Millisecond) {
		t.Fatal("process exited although it ignores interrupts")
	}
	p.cmd.Process.Kill()
	p.wait()
}

func TestInvocationsAndPace(t *testing.T) {
	trafficPath := traffic(t, `recv {"type":"a"}`, `recv {"type":"b"}`)
	s := Script{StateDir: t.TempDir(), Invocations: []Invocation{
		{Traffic: trafficPath, ExitAtEnd: true},
		{Traffic: trafficPath, ExitAtEnd: true, PaceMs: 100},
	}}
	p, state := start(t, "claude", s)
	p.recv()
	p.recv()
	p.wait()

	p, _ = start(t, "claude", s)
	began := time.Now()
	p.recv()
	p.recv()
	if elapsed := time.Since(began); elapsed < 150*time.Millisecond {
		t.Fatalf("paced replay took %v", elapsed)
	}
	p.wait()

	p, _ = start(t, "codex", s)
	if code := p.wait(); code != 2 {
		t.Fatalf("exit code of an unscripted invocation = %d, want 2", code)
	}
	if n := state.Invocations(t); n != 3 {
		t.Fatalf("invocations = %d", n)
	}
}

func TestUseSetsPath(t *testing.T) {
	Use(t, binDir, Script{})
	for _, name := range []string{"claude", "codex"} {
		path, err := exec.LookPath(name)
		if err != nil || filepath.Dir(path) != binDir {
			t.Fatalf("LookPath(%s) = %q, %v", name, path, err)
		}
	}
	Use(t, t.TempDir(), Script{})
	if _, err := exec.LookPath("claude"); err == nil {
		t.Fatal("claude found on an empty PATH")
	}
}
