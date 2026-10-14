package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/wenfengwang/cloud-agent-sdk/internal/proc"
)

// closeGrace is how long a shutdown waits for the app-server to exit after
// its stdin is closed before the process group is killed: the grace period
// of design 001.
const closeGrace = 5 * time.Second

// tapFunc observes every line exchanged with an app-server process: dir is
// "send" or "recv". The recording tests use it; it is nil otherwise.
type tapFunc func(dir string, line []byte)

// rpcError is a JSON-RPC error object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("code %d: %s", e.Code, e.Message)
}

// wireMessage is any JSON-RPC message of the app-server protocol: a request
// (method and id), a notification (method only), or a response (id only).
type wireMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcResult struct {
	result json.RawMessage
	err    error
}

// appServer is one running "codex app-server" process and its JSON-RPC
// connection. Notifications are queued without bound, so that a slow or
// absent consumer never stalls responses.
type appServer struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	tap   tapFunc

	writeMu   sync.Mutex
	closeOnce sync.Once

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan rpcResult
	queue   [][]byte
	wake    chan struct{} // capacity 1; signalled when queue grows
	killed  bool
	exitErr error         // set before done is closed
	done    chan struct{} // closed once stdout ended and the process was reaped
}

// startAppServer starts "codex app-server" in workDir. A non-empty home is
// passed as CODEX_HOME. The process runs in its own process group so that
// kill reaches every process it started.
func startAppServer(workDir, home string, tap tapFunc) (*appServer, error) {
	cmd := exec.Command("codex", "app-server")
	cmd.Dir = workDir
	cmd.Env = os.Environ()
	if home != "" {
		cmd.Env = append(withoutEnv(cmd.Env, "CODEX_HOME"), "CODEX_HOME="+home)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("codex: app-server stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("codex: app-server stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("codex: start app-server: %w", err)
	}
	s := &appServer{
		cmd:     cmd,
		stdin:   stdin,
		tap:     tap,
		pending: map[int64]chan rpcResult{},
		wake:    make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
	go s.read(stdout)
	return s, nil
}

func withoutEnv(env []string, key string) []string {
	out := env[:0:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, key+"=") {
			out = append(out, kv)
		}
	}
	return out
}

// read dispatches every stdout line until EOF, then reaps the process.
func (s *appServer) read(stdout io.Reader) {
	r := bufio.NewReaderSize(stdout, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if trimmed := trimLine(line); len(trimmed) > 0 {
			s.dispatch(trimmed)
		}
		if err != nil {
			break
		}
	}
	waitErr := s.cmd.Wait()
	s.mu.Lock()
	switch {
	case s.killed:
		s.exitErr = errors.New("codex: app-server was killed")
	case waitErr != nil:
		s.exitErr = fmt.Errorf("codex: app-server exited: %w", waitErr)
	default:
		s.exitErr = errors.New("codex: app-server exited")
	}
	pending := s.pending
	s.pending = map[int64]chan rpcResult{}
	s.mu.Unlock()
	for _, ch := range pending {
		ch <- rpcResult{err: s.exitErr}
	}
	close(s.done)
}

func trimLine(line []byte) []byte {
	for len(line) > 0 && (line[len(line)-1] == '\n' || line[len(line)-1] == '\r') {
		line = line[:len(line)-1]
	}
	return line
}

func (s *appServer) dispatch(line []byte) {
	line = append([]byte(nil), line...)
	if s.tap != nil {
		s.tap("recv", line)
	}
	var m wireMessage
	if err := json.Unmarshal(line, &m); err != nil {
		// Not JSON-RPC; the protocol has nothing else on stdout.
		return
	}
	switch {
	case m.Method != "" && len(m.ID) > 0:
		// A server request. With approval policy never none is expected;
		// answer at once so the harness never waits on this client.
		s.respondError(m.ID, -32601, "cloudagent does not handle server requests: "+m.Method)
	case m.Method != "":
		s.mu.Lock()
		s.queue = append(s.queue, line)
		s.mu.Unlock()
		select {
		case s.wake <- struct{}{}:
		default:
		}
	default:
		id, err := strconv.ParseInt(string(m.ID), 10, 64)
		if err != nil {
			return
		}
		s.mu.Lock()
		ch := s.pending[id]
		delete(s.pending, id)
		s.mu.Unlock()
		if ch == nil {
			return
		}
		if m.Error != nil {
			ch <- rpcResult{err: m.Error}
		} else {
			ch <- rpcResult{result: m.Result}
		}
	}
}

func (s *appServer) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("codex: encode message: %w", err)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.tap != nil {
		s.tap("send", data)
	}
	if _, err := s.stdin.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("codex: write to app-server: %w", err)
	}
	return nil
}

func (s *appServer) respondError(id json.RawMessage, code int, msg string) {
	_ = s.write(map[string]any{"id": id, "error": rpcError{Code: code, Message: msg}})
}

// request sends a JSON-RPC request and waits for its response, for ctx, or
// for the process to exit.
func (s *appServer) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	ch := make(chan rpcResult, 1)
	s.mu.Lock()
	if s.exitErr != nil {
		err := s.exitErr
		s.mu.Unlock()
		return nil, err
	}
	s.nextID++
	id := s.nextID
	s.pending[id] = ch
	s.mu.Unlock()
	if err := s.write(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, err
	}
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, fmt.Errorf("codex: %s: %w", method, r.err)
		}
		return r.result, nil
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, fmt.Errorf("codex: %s: %w", method, ctx.Err())
	}
}

// notify sends a JSON-RPC notification.
func (s *appServer) notify(method string) error {
	return s.write(map[string]any{"method": method})
}

// next returns the oldest queued notification. It blocks until one arrives
// and returns false once the process has exited and the queue is empty.
func (s *appServer) next() ([]byte, bool) {
	for {
		s.mu.Lock()
		if len(s.queue) > 0 {
			line := s.queue[0]
			s.queue = s.queue[1:]
			s.mu.Unlock()
			return line, true
		}
		s.mu.Unlock()
		select {
		case <-s.wake:
		case <-s.done:
			s.mu.Lock()
			empty := len(s.queue) == 0
			s.mu.Unlock()
			if empty {
				return nil, false
			}
		}
	}
}

// err returns why the process ended, or nil while it runs.
func (s *appServer) err() error {
	select {
	case <-s.done:
		return s.exitErr
	default:
		return nil
	}
}

// dead reports whether the process was killed or has exited.
func (s *appServer) dead() bool {
	s.mu.Lock()
	killed := s.killed
	s.mu.Unlock()
	return killed || s.err() != nil
}

// kill kills the process group at once, together with the process groups
// of every descendant (the commands Codex runs in their own groups). It
// does not wait.
func (s *appServer) kill() {
	s.mu.Lock()
	s.killed = true
	s.mu.Unlock()
	proc.KillTree(s.cmd.Process.Pid)
}

// close ends the process: it lists the process groups of its descendants,
// closes stdin, waits up to closeGrace for the process to exit, kills it
// with its descendants if it has not, and in any case kills its process
// group and the listed groups, so that no process it started survives, not
// even a command in its own process group. It then waits until the process
// is reaped. It is safe to call more than once and concurrently; every call
// returns once the process is reaped.
func (s *appServer) close() {
	s.closeOnce.Do(func() {
		var groups []int
		if s.err() == nil {
			groups = proc.DescendantGroups(s.cmd.Process.Pid)
		}
		_ = s.stdin.Close()
		select {
		case <-s.done:
			_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
		case <-time.After(closeGrace):
			s.kill()
		}
		proc.KillGroups(groups)
		<-s.done
	})
	<-s.done
}
