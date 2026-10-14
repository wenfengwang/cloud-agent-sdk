package agent

import (
	"context"
	"fmt"
	"iter"
	"sync"
	"time"

	"github.com/wenfengwang/cloud-agent-sdk/internal/harness"
	"github.com/wenfengwang/cloud-agent-sdk/internal/message"
)

// Result is the outcome of a turn.
type Result struct {
	TurnID    string
	FinalText string
	Usage     harness.Usage
}

// Turn is one turn of a Runtime.
type Turn struct {
	id   string
	rt   *Runtime
	done chan struct{} // closed when the turn has ended or its start failed
	// cancelStart cancels the harness start; set before the turn becomes
	// active and safe to call after the start returned.
	cancelStart context.CancelFunc

	mu        sync.Mutex
	cond      *sync.Cond
	run       harness.Run
	ref       harness.Ref
	buf       []message.Message
	stopCause error // ErrTerminated, ErrClosed, or the context's error
	finishing bool  // the run has ended; stops are no-ops from here on
	ended     bool  // the result and the index are final
	result    Result
	err       error
}

func newTurn(rt *Runtime, id string) *Turn {
	t := &Turn{id: id, rt: rt, done: make(chan struct{})}
	t.cond = sync.NewCond(&t.mu)
	return t
}

// ID returns the turn ID.
func (t *Turn) ID() string { return t.id }

// Messages yields every message of the turn from the first. While the turn
// runs it reads the in-memory buffer and blocks for new messages; once the
// turn has ended, a new call reads the turn back from the harness home.
func (t *Turn) Messages() iter.Seq2[message.Message, error] {
	return func(yield func(message.Message, error) bool) {
		t.mu.Lock()
		ended, ref := t.ended, t.ref
		t.mu.Unlock()
		if ended {
			t.readBack(ref, yield)
			return
		}
		for i := 0; ; i++ {
			t.mu.Lock()
			for i >= len(t.buf) && !t.ended {
				t.cond.Wait()
			}
			if i >= len(t.buf) {
				t.mu.Unlock()
				return
			}
			m := t.buf[i]
			t.mu.Unlock()
			if !yield(m, nil) {
				return
			}
		}
	}
}

func (t *Turn) readBack(ref harness.Ref, yield func(message.Message, error) bool) {
	msgs, err := t.rt.adapter.ReadTurn(context.Background(), ref)
	if err != nil {
		yield(message.Message{}, fmt.Errorf("cloudagent: read turn %s: %w", t.id, err))
		return
	}
	for _, m := range msgs {
		m.TurnID = t.id
		if !yield(m, nil) {
			return
		}
	}
}

// Result blocks until the turn ends and returns its outcome.
func (t *Turn) Result() (Result, error) {
	<-t.done
	return t.result, t.err
}

// Terminate requests a graceful stop and returns without waiting. It is a
// no-op on a finished turn.
func (t *Turn) Terminate() error {
	t.stop(ErrTerminated)
	return nil
}

// abandon ends a turn whose start failed.
func (t *Turn) abandon() {
	t.rt.release(t)
	close(t.done)
}

// begin starts consuming an accepted run and watching ctx.
func (t *Turn) begin(ctx context.Context, run harness.Run, ref harness.Ref) {
	t.mu.Lock()
	t.run = run
	t.ref = ref
	stopped := t.stopCause != nil
	t.mu.Unlock()
	if stopped {
		t.interrupt(run)
	}
	go t.pump(run)
	go func() {
		select {
		case <-ctx.Done():
			t.stop(ctx.Err())
		case <-t.done:
		}
	}()
}

// stop records the first stop cause and interrupts the run. Before the run
// exists, it cancels the start, and begin interrupts a run whose start
// succeeded anyway.
func (t *Turn) stop(cause error) {
	t.mu.Lock()
	if t.finishing || t.stopCause != nil {
		t.mu.Unlock()
		return
	}
	t.stopCause = cause
	run := t.run
	t.mu.Unlock()
	if run != nil {
		t.interrupt(run)
		return
	}
	t.cancelStart()
}

// stoppedBy returns the recorded stop cause, or nil.
func (t *Turn) stoppedBy() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stopCause
}

// interrupt asks the run to stop and kills it if it has not ended within the
// grace period.
func (t *Turn) interrupt(run harness.Run) {
	run.Interrupt()
	go func() {
		timer := time.NewTimer(t.rt.stopGrace)
		defer timer.Stop()
		select {
		case <-timer.C:
			run.Kill()
		case <-t.done:
		}
	}()
}

// pump buffers the run's messages, then records the outcome.
func (t *Turn) pump(run harness.Run) {
	for m := range run.Messages() {
		m.TurnID = t.id
		t.mu.Lock()
		t.buf = append(t.buf, m)
		t.cond.Broadcast()
		t.mu.Unlock()
	}
	out, runErr := run.Wait()

	t.mu.Lock()
	t.finishing = true
	cause := t.stopCause
	ref := t.ref
	if out.Ref.SessionID != "" || len(out.Ref.Data) > 0 {
		ref = out.Ref
	}
	res := Result{TurnID: t.id, FinalText: finalText(t.buf), Usage: out.Usage}
	t.mu.Unlock()

	status, err := outcome(cause, runErr)
	if ixErr := t.rt.index.endTurn(t.id, ref.Data, ref.SessionID, status, time.Now()); ixErr != nil && err == nil {
		err = ixErr
	}

	t.mu.Lock()
	t.ref = ref
	t.result = res
	t.err = err
	t.ended = true
	t.cond.Broadcast()
	t.mu.Unlock()
	t.rt.release(t)
	close(t.done)
}

// outcome maps why a run ended to its index status and Result error. A
// requested stop wins over whatever the run reported.
func outcome(cause, runErr error) (string, error) {
	switch {
	case cause == ErrTerminated:
		return statusTerminated, ErrTerminated
	case cause == ErrClosed:
		return statusClosed, ErrClosed
	case cause != nil:
		return statusCancelled, cause
	case runErr != nil:
		return statusFailed, fmt.Errorf("cloudagent: turn failed: %w", runErr)
	}
	return statusCompleted, nil
}

// finalText returns the text of the last top-level assistant message.
func finalText(msgs []message.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Kind == message.KindAssistant && m.ParentToolUseID == "" && m.Assistant != nil {
			return m.Assistant.Text
		}
	}
	return ""
}
