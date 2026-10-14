package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wenfengwang/cloud-agent-sdk/internal/harness"
	"github.com/wenfengwang/cloud-agent-sdk/internal/message"
)

// fakeHarness is a scripted in-process harness.Harness. Each StartTurn
// consumes the next fakeRun from runs.
type fakeHarness struct {
	mu       sync.Mutex
	runs     []*fakeRun
	prompts  []string
	closes   int
	readRefs []harness.Ref
	readBack []message.Message
	readErr  error

	// startEntered, when set, receives a value once StartTurn is entered;
	// StartTurn then blocks until startRelease is closed.
	startEntered chan struct{}
	startRelease chan struct{}
	// startBlocks makes StartTurn wait for ctx and return its error, as a
	// harness that never accepts the prompt.
	startBlocks bool
}

func (h *fakeHarness) StartTurn(ctx context.Context, prompt string) (harness.Run, error) {
	if h.startEntered != nil {
		h.startEntered <- struct{}{}
		<-h.startRelease
	}
	if h.startBlocks {
		<-ctx.Done()
		return nil, fmt.Errorf("fake: start: %w", ctx.Err())
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.prompts = append(h.prompts, prompt)
	if len(h.runs) == 0 {
		return nil, errors.New("fake: no run scripted")
	}
	r := h.runs[0]
	h.runs = h.runs[1:]
	if r.startErr != nil {
		return nil, r.startErr
	}
	r.start()
	return r, nil
}

func (h *fakeHarness) ReadTurn(ctx context.Context, ref harness.Ref) ([]message.Message, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.readRefs = append(h.readRefs, ref)
	if h.readErr != nil {
		return nil, h.readErr
	}
	return append([]message.Message(nil), h.readBack...), nil
}

func (h *fakeHarness) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closes++
	return nil
}

func (h *fakeHarness) closeCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closes
}

// fakeRun emits msgs, then either finishes or, with hold, waits for
// Interrupt (unless ignoreInterrupt) or Kill.
type fakeRun struct {
	ref             harness.Ref
	finalRef        harness.Ref
	msgs            []message.Message
	pace            time.Duration
	hold            bool
	ignoreInterrupt bool
	usage           harness.Usage
	waitErr         error
	startErr        error

	ch          chan message.Message
	interruptCh chan struct{}
	killCh      chan struct{}
	interruptMu sync.Once
	killMu      sync.Once
	interrupts  atomic.Int32
	kills       atomic.Int32
	waits       atomic.Int32
	err         error
	stopped     bool
}

func (r *fakeRun) start() {
	r.ch = make(chan message.Message)
	r.interruptCh = make(chan struct{})
	r.killCh = make(chan struct{})
	go r.loop()
}

func (r *fakeRun) loop() {
	defer close(r.ch)
	r.err = r.waitErr
	for _, m := range r.msgs {
		if r.pace > 0 {
			select {
			case <-time.After(r.pace):
			case <-r.killCh:
				r.err = errors.New("fake: killed")
				return
			}
		}
		select {
		case r.ch <- m:
		case <-r.killCh:
			r.err = errors.New("fake: killed")
			return
		}
	}
	if !r.hold {
		return
	}
	interrupted := r.interruptCh
	if r.ignoreInterrupt {
		interrupted = nil
	}
	select {
	case <-interrupted:
		r.stopped = true
	case <-r.killCh:
		r.err = errors.New("fake: killed")
	}
}

func (r *fakeRun) Ref() harness.Ref                 { return r.ref }
func (r *fakeRun) Messages() <-chan message.Message { return r.ch }

func (r *fakeRun) Interrupt() {
	r.interrupts.Add(1)
	r.interruptMu.Do(func() { close(r.interruptCh) })
}

func (r *fakeRun) Kill() {
	r.kills.Add(1)
	r.killMu.Do(func() { close(r.killCh) })
}

func (r *fakeRun) Wait() (harness.Outcome, error) {
	r.waits.Add(1)
	return harness.Outcome{Usage: r.usage, Ref: r.finalRef}, r.err
}

func ref(session, turn string) harness.Ref {
	data, _ := json.Marshal(map[string]string{"session_id": session, "user_message_uuid": turn})
	return harness.Ref{SessionID: session, Data: data}
}

func userMsg(id, text string) message.Message {
	return message.Message{ID: id, Kind: message.KindUser, User: &message.Text{Text: text}, Raw: json.RawMessage(`{}`)}
}

func assistantMsg(id, parent, text string) message.Message {
	return message.Message{ID: id, ParentToolUseID: parent, Kind: message.KindAssistant, Assistant: &message.Text{Text: text}, Raw: json.RawMessage(`{}`)}
}

func shellCall(id, command string) message.Message {
	return message.Message{
		ID:   id,
		Kind: message.KindToolCall,
		ToolCall: &message.ToolCall{
			ID: id, Name: message.ToolNameShell, RawName: "Bash",
			Input: &message.ShellInput{Command: command},
		},
		Raw: json.RawMessage(`{}`),
	}
}
