package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/wenfengwang/cloud-agent-sdk/internal/harness"
	"github.com/wenfengwang/cloud-agent-sdk/internal/message"
	"github.com/wenfengwang/cloud-agent-sdk/internal/proc"
)

// pipeGrace bounds how long a run waits for its stdout and stderr to close
// after the claude process exited, in case a leftover child holds them open.
const pipeGrace = 2 * time.Second

// run is one claude process running one turn.
type run struct {
	h      *Harness
	cmd    *exec.Cmd
	stdout *os.File // read end of the process's stdout
	stderr *tailBuffer
	msgs   chan message.Message

	accepted chan struct{} // closed when the prompt was echoed
	readDone chan struct{} // closed when stdout is exhausted and msgs closed
	exited   chan struct{} // closed when the process was reaped
	waitErr  error         // the process's exit; read after exited

	stdinMu     sync.Mutex
	stdin       io.WriteCloser
	stdinClosed bool

	interruptOnce sync.Once
	interrupted   atomic.Bool   // set by the first Interrupt
	stopListed    chan struct{} // closed once stopGroups is set
	stopGroups    []int         // descendant process groups at Interrupt

	// Set by the reader before readDone.
	ref    harness.Ref
	result *resultLine
	err    error // protocol error
}

// resultLine is the final stream-json line of a turn.
type resultLine struct {
	Subtype string   `json:"subtype"`
	IsError bool     `json:"is_error"`
	Result  string   `json:"result"`
	Errors  []string `json:"errors"`
	Usage   struct {
		InputTokens              int64 `json:"input_tokens"`
		OutputTokens             int64 `json:"output_tokens"`
		CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

// userLine is the stream-json input message that submits a prompt.
type userLine struct {
	Type            string  `json:"type"`
	SessionID       string  `json:"session_id"`
	ParentToolUseID *string `json:"parent_tool_use_id"`
	Message         struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
}

// startRun starts claude with args, submits prompt, and returns once Claude
// Code echoed it back. ctx bounds only the start.
func startRun(ctx context.Context, h *Harness, args []string, prompt string) (*run, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("claude: start: %w", err)
	}
	cmd := exec.Command("claude", args...)
	cmd.Dir = h.cfg.WorkDir
	cmd.Env = h.env()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = pipeGrace
	r := &run{
		h:          h,
		cmd:        cmd,
		stderr:     &tailBuffer{max: 4096},
		msgs:       make(chan message.Message),
		accepted:   make(chan struct{}),
		readDone:   make(chan struct{}),
		exited:     make(chan struct{}),
		stopListed: make(chan struct{}),
		ref:        harness.Ref{SessionID: h.sessionID},
	}
	cmd.Stderr = r.stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("claude: start: %w", err)
	}
	r.stdin = stdin
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("claude: start: %w", err)
	}
	cmd.Stdout = stdoutW
	r.stdout = stdoutR
	if err := cmd.Start(); err != nil {
		stdoutR.Close()
		stdoutW.Close()
		return nil, fmt.Errorf("claude: start: %w", err)
	}
	stdoutW.Close()
	go r.reap()
	go r.read()

	line := userLine{Type: "user"}
	line.Message.Role = "user"
	line.Message.Content = prompt
	if err := r.send(line); err != nil {
		r.abort()
		return nil, fmt.Errorf("claude: submit prompt: %w%s", err, r.stderr.suffix())
	}

	select {
	case <-r.accepted:
		return r, nil
	case <-r.readDone:
		r.abort()
		return nil, fmt.Errorf("claude: exited before accepting the prompt: %v%s", r.waitErr, r.stderr.suffix())
	case <-ctx.Done():
		r.abort()
		return nil, fmt.Errorf("claude: start: %w", ctx.Err())
	}
}

// abort kills a run that was never accepted and waits for it to end.
func (r *run) abort() {
	r.Kill()
	for range r.msgs {
	}
	<-r.exited
}

// reap waits for the process and then kills what is left of its process
// group, so that no child of the harness outlives the turn. After an
// Interrupt it also kills the process groups the harness's descendants had
// when the stop was requested: a tool command in its own group survives
// the harness otherwise.
func (r *run) reap() {
	r.waitErr = r.cmd.Wait()
	syscall.Kill(-r.cmd.Process.Pid, syscall.SIGKILL)
	if r.interrupted.Load() {
		<-r.stopListed
		proc.KillGroups(r.stopGroups)
	}
	close(r.exited)
	select {
	case <-r.readDone:
	case <-time.After(pipeGrace):
		r.stdout.Close()
	}
}

// read translates stdout until it is exhausted, then closes msgs once the
// process was reaped.
func (r *run) read() {
	defer func() {
		r.stdout.Close()
		<-r.exited
		close(r.msgs)
		close(r.readDone)
	}()
	translator := newTranslator()
	br := bufio.NewReader(r.stdout)
	for {
		line, readErr := br.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			r.h.observe(dirRecv, line)
			if err := r.handle(translator, line); err != nil && r.err == nil {
				r.err = err
			}
		}
		if readErr != nil {
			return
		}
	}
}

// handle processes one stdout line.
func (r *run) handle(translator *translator, line []byte) error {
	var head struct {
		Type     string `json:"type"`
		UUID     string `json:"uuid"`
		IsReplay bool   `json:"isReplay"`
	}
	if err := json.Unmarshal(line, &head); err != nil {
		return fmt.Errorf("claude: parse stream line: %w", err)
	}
	switch {
	case head.Type == "result":
		var res resultLine
		if err := json.Unmarshal(line, &res); err != nil {
			return fmt.Errorf("claude: parse result: %w", err)
		}
		r.result = &res
		r.closeStdin() // the turn is over; claude exits at end of input
		return nil
	case head.Type == "user" && head.IsReplay:
		if isClosed(r.accepted) {
			return nil // only the turn's own prompt is echoed
		}
		data, err := json.Marshal(turnRef{SessionID: r.h.sessionID, UserMessageUUID: head.UUID})
		if err != nil {
			return fmt.Errorf("claude: encode turn reference: %w", err)
		}
		r.ref.Data = data
		msgs, err := translator.streamLine(line)
		close(r.accepted)
		r.forward(msgs)
		return err
	}
	msgs, err := translator.streamLine(line)
	r.forward(msgs)
	return err
}

func (r *run) forward(msgs []message.Message) {
	for _, m := range msgs {
		r.msgs <- m
	}
}

// send writes one stream-json line to stdin.
func (r *run) send(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	r.stdinMu.Lock()
	defer r.stdinMu.Unlock()
	if r.stdinClosed {
		return errors.New("stdin closed")
	}
	r.h.observe(dirSend, data)
	_, err = r.stdin.Write(append(data, '\n'))
	return err
}

func (r *run) closeStdin() {
	r.stdinMu.Lock()
	defer r.stdinMu.Unlock()
	if !r.stdinClosed {
		r.stdinClosed = true
		r.stdin.Close()
	}
}

// Ref implements harness.Run.
func (r *run) Ref() harness.Ref { return r.ref }

// Messages implements harness.Run.
func (r *run) Messages() <-chan message.Message { return r.msgs }

// Interrupt implements harness.Run: once, without blocking, it lists the
// process groups of the harness's descendants while the harness still
// lives (reap kills them after it exits), then sends the interrupt control
// request.
func (r *run) Interrupt() {
	r.interruptOnce.Do(func() {
		r.interrupted.Store(true)
		go func() {
			if !isClosed(r.exited) {
				r.stopGroups = proc.DescendantGroups(r.cmd.Process.Pid)
			}
			close(r.stopListed)
			r.send(map[string]any{
				"type":       "control_request",
				"request_id": "cloudagent-interrupt",
				"request":    map[string]string{"subtype": "interrupt"},
			})
		}()
	})
}

// Kill implements harness.Run: it kills the process group, and the groups
// of every descendant, and stops reading.
func (r *run) Kill() {
	if isClosed(r.exited) {
		return
	}
	proc.KillTree(r.cmd.Process.Pid)
	r.stdout.Close()
	r.closeStdin()
}

// Wait implements harness.Run.
func (r *run) Wait() (harness.Outcome, error) {
	<-r.readDone
	var out harness.Outcome
	res := r.result
	if res != nil {
		out.Usage = harness.Usage{
			InputTokens:      res.Usage.InputTokens,
			OutputTokens:     res.Usage.OutputTokens,
			CacheReadTokens:  res.Usage.CacheReadInputTokens,
			CacheWriteTokens: res.Usage.CacheCreationInputTokens,
		}
	}
	switch {
	case r.err != nil:
		return out, r.err
	case res == nil:
		return out, fmt.Errorf("claude: the turn ended without a result: %v%s", r.waitErr, r.stderr.suffix())
	case res.IsError || res.Subtype != "success":
		detail := res.Result
		if len(res.Errors) > 0 {
			detail = strings.Join(res.Errors, "; ")
		}
		return out, fmt.Errorf("claude: turn failed (%s): %s", res.Subtype, detail)
	case r.waitErr != nil:
		return out, fmt.Errorf("claude: %w%s", r.waitErr, r.stderr.suffix())
	}
	return out, nil
}

func isClosed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.max {
		b.buf = b.buf[len(b.buf)-b.max:]
	}
	return len(p), nil
}

// suffix formats the captured stderr for an error message.
func (b *tailBuffer) suffix() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := strings.TrimSpace(string(b.buf))
	if s == "" {
		return ""
	}
	return ": " + s
}
