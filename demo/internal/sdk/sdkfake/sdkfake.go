// Package sdkfake is a scriptable in-memory implementation of the sdk
// interfaces for backend tests.
package sdkfake

import (
	"context"
	"fmt"
	"iter"
	"sync"

	"github.com/wenfengwang/cloud-agent-sdk/demo/internal/sdk"
)

// Factory creates fake runtimes and records them.
type Factory struct {
	mu       sync.Mutex
	runtimes []*Runtime
	// NewErr, when set, is returned by New.
	NewErr error
	// Configure, when set, is called on every new runtime before it is
	// returned.
	Configure func(*Runtime)
}

// New implements sdk.Factory.
func (f *Factory) New(h sdk.Harness, opts sdk.Options) (sdk.Runtime, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.NewErr != nil {
		return nil, f.NewErr
	}
	r := &Runtime{id: fmt.Sprintf("agent-%d", len(f.runtimes)+1), Harness: h, Options: opts}
	if f.Configure != nil {
		f.Configure(r)
	}
	f.runtimes = append(f.runtimes, r)
	return r, nil
}

// Runtimes returns every runtime created so far.
func (f *Factory) Runtimes() []*Runtime {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*Runtime(nil), f.runtimes...)
}

// Runtime is a fake sdk.Runtime.
type Runtime struct {
	id      string
	Harness sdk.Harness
	Options sdk.Options

	// StartTurn, when set, replaces the default start: it receives the
	// turn's context and a fresh Turn bound to it, and returns what Turn
	// returns. It may block, for example until ctx is cancelled.
	StartTurn func(ctx context.Context, prompt string, turn *Turn) (sdk.Turn, error)
	// CloseErr is returned by Close.
	CloseErr error

	mu     sync.Mutex
	turns  []*Turn
	closed bool
}

// ID implements sdk.Runtime.
func (r *Runtime) ID() string { return r.id }

// Turn implements sdk.Runtime. By default it returns a running Turn at once.
func (r *Runtime) Turn(ctx context.Context, prompt string) (sdk.Turn, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, sdk.ErrClosed
	}
	turn := newTurn(ctx, fmt.Sprintf("%s-turn-%d", r.id, len(r.turns)+1), prompt)
	r.turns = append(r.turns, turn)
	start := r.StartTurn
	r.mu.Unlock()

	if start == nil {
		return turn, nil
	}
	t, err := start(ctx, prompt, turn)
	if err != nil {
		turn.End(sdk.Result{}, err)
		return nil, err
	}
	return t, nil
}

// Close implements sdk.Runtime: it ends every unfinished turn with
// ErrClosed and waits for them.
func (r *Runtime) Close() error {
	r.mu.Lock()
	r.closed = true
	turns := append([]*Turn(nil), r.turns...)
	r.mu.Unlock()
	for _, t := range turns {
		t.End(sdk.Result{}, sdk.ErrClosed)
	}
	return r.CloseErr
}

// Closed reports whether Close was called.
func (r *Runtime) Closed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

// Turns returns every turn started so far, including failed starts.
func (r *Runtime) Turns() []*Turn {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*Turn(nil), r.turns...)
}

// Turn is a fake sdk.Turn. A turn whose context is cancelled ends with
// ctx.Err().
type Turn struct {
	id     string
	Prompt string
	// TerminateErr, when set, is returned by Terminate, which then does not
	// end the turn.
	TerminateErr error

	mu        sync.Mutex
	messages  []sdk.Message
	iterErr   error
	ended     bool
	result    sdk.Result
	resultErr error
	changed   chan struct{}
	done      chan struct{}
}

func newTurn(ctx context.Context, id, prompt string) *Turn {
	t := &Turn{id: id, Prompt: prompt, changed: make(chan struct{}), done: make(chan struct{})}
	go func() {
		select {
		case <-ctx.Done():
			t.End(sdk.Result{TurnID: id}, ctx.Err())
		case <-t.done:
		}
	}()
	return t
}

// ID implements sdk.Turn.
func (t *Turn) ID() string { return t.id }

// Emit appends a message with the next ID.
func (t *Turn) Emit(m sdk.Message) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ended {
		return
	}
	if m.ID == "" {
		m.ID = fmt.Sprintf("%s-msg-%d", t.id, len(t.messages)+1)
	}
	m.TurnID = t.id
	t.messages = append(t.messages, m)
	t.wake()
}

// EmitText appends an assistant message.
func (t *Turn) EmitText(text string) {
	t.Emit(sdk.Message{Kind: sdk.KindAssistant, Assistant: &sdk.Text{Text: text}})
}

// FailIteration makes Messages end with err after the messages so far.
func (t *Turn) FailIteration(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.iterErr = err
	t.wake()
}

// End ends the turn; Result then returns res and err. Later calls are
// no-ops.
func (t *Turn) End(res sdk.Result, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ended {
		return
	}
	res.TurnID = t.id
	t.ended, t.result, t.resultErr = true, res, err
	close(t.done)
	t.wake()
}

// Done is closed when the turn ends.
func (t *Turn) Done() <-chan struct{} { return t.done }

func (t *Turn) wake() {
	close(t.changed)
	t.changed = make(chan struct{})
}

// Messages implements sdk.Turn.
func (t *Turn) Messages() iter.Seq2[sdk.Message, error] {
	return func(yield func(sdk.Message, error) bool) {
		for i := 0; ; {
			t.mu.Lock()
			pending := t.messages[i:]
			iterErr, ended, changed := t.iterErr, t.ended, t.changed
			t.mu.Unlock()
			for _, m := range pending {
				if !yield(m, nil) {
					return
				}
				i++
			}
			if len(pending) > 0 {
				continue
			}
			if iterErr != nil {
				yield(sdk.Message{}, iterErr)
				return
			}
			if ended {
				return
			}
			<-changed
		}
	}
}

// Result implements sdk.Turn.
func (t *Turn) Result() (sdk.Result, error) {
	<-t.done
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.result, t.resultErr
}

// Terminate implements sdk.Turn.
func (t *Turn) Terminate() error {
	if t.TerminateErr != nil {
		return t.TerminateErr
	}
	go t.End(sdk.Result{}, sdk.ErrTerminated)
	return nil
}
