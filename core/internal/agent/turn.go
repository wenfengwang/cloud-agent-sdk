package agent

import (
	"context"
	"fmt"
	"iter"
	"sync"
	"time"

	"github.com/wenfengwang/cloud-agent-sdk/core/internal/harness"
	"github.com/wenfengwang/cloud-agent-sdk/core/internal/message"
)

// Result is the outcome of a turn.
type Result struct {
	TurnID    string
	FinalText string
	Usage     harness.Usage
}

// Turn is one turn of a Runtime.
type Turn struct {
	id      string
	runtime *Runtime
	done    chan struct{} // closed when the turn has ended or its start failed
	// cancelStart cancels the harness start; set before the turn becomes
	// active and safe to call after the start returned.
	cancelStart context.CancelFunc

	mu        sync.Mutex
	cond      *sync.Cond
	run       harness.Run
	ref       harness.Ref
	buf       []message.Event
	stopCause error // ErrTerminated, ErrClosed, or the context's error
	finishing bool  // the run has ended; stops are no-ops from here on
	ended     bool  // the result and the index are final
	result    Result
	err       error
}

func newTurn(runtime *Runtime, id string) *Turn {
	t := &Turn{id: id, runtime: runtime, done: make(chan struct{})}
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
		for e, err := range t.Events() {
			if err != nil {
				yield(message.Message{}, err)
				return
			}
			if e.Message != nil && !yield(*e.Message, nil) {
				return
			}
		}
	}
}

// Events yields every event of the turn from the first: messages and, while
// the turn runs, the buffered deltas. Once the turn has ended, a new call
// reads the turn back from the harness home and yields its messages only.
func (t *Turn) Events() iter.Seq2[message.Event, error] {
	return func(yield func(message.Event, error) bool) {
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
			e := t.buf[i]
			t.mu.Unlock()
			if !yield(e, nil) {
				return
			}
		}
	}
}

func (t *Turn) readBack(ref harness.Ref, yield func(message.Event, error) bool) {
	msgs, err := t.runtime.adapter.ReadTurn(context.Background(), ref)
	if err != nil {
		yield(message.Event{}, fmt.Errorf("cloudagent: read turn %s: %w", t.id, err))
		return
	}
	for i := range msgs {
		msgs[i].TurnID = t.id
		if !yield(message.Event{Message: &msgs[i]}, nil) {
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
	t.runtime.release(t)
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
		timer := time.NewTimer(t.runtime.stopGrace)
		defer timer.Stop()
		select {
		case <-timer.C:
			run.Kill()
		case <-t.done:
		}
	}()
}

// pump buffers the run's events, then records the outcome. Every buffered
// event gets its own copy of the message or delta, stamped with the turn ID.
func (t *Turn) pump(run harness.Run) {
	for e := range run.Events() {
		if e.Message != nil {
			m := *e.Message
			m.TurnID = t.id
			e.Message = &m
		}
		if e.Delta != nil {
			d := *e.Delta
			d.TurnID = t.id
			e.Delta = &d
		}
		t.mu.Lock()
		t.buf = append(t.buf, e)
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
	if indexErr := t.runtime.index.endTurn(t.id, ref.Data, ref.SessionID, status, time.Now()); indexErr != nil && err == nil {
		err = indexErr
	}

	t.mu.Lock()
	t.ref = ref
	t.result = res
	t.err = err
	t.ended = true
	t.cond.Broadcast()
	t.mu.Unlock()
	t.runtime.release(t)
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
func finalText(events []message.Event) string {
	for i := len(events) - 1; i >= 0; i-- {
		m := events[i].Message
		if m != nil && m.Kind == message.KindAssistant && m.ParentToolUseID == "" && m.Assistant != nil {
			return m.Assistant.Text
		}
	}
	return ""
}
