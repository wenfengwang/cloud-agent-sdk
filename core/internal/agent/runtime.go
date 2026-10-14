package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/wenfengwang/cloud-agent-sdk/core/internal/harness"
)

// DefaultStopGrace is how long a run may take to end after Interrupt before
// it is killed.
const DefaultStopGrace = 5 * time.Second

// Config configures a Runtime.
type Config struct {
	// Harness, Model, WorkDir, and Home are written to agent.json.
	Harness string
	Model   string
	WorkDir string
	Home    string
	// Adapter drives the harness.
	Adapter harness.Harness
	// StopGrace is how long a run may take to end after Interrupt before it
	// is killed. Zero selects DefaultStopGrace.
	StopGrace time.Duration
}

// Runtime is one agent: one harness adapter and one linear conversation.
// It is safe for concurrent use.
type Runtime struct {
	id        string
	adapter   harness.Harness
	index     *index
	stopGrace time.Duration

	mu        sync.Mutex
	closed    bool
	active    *Turn // starting or running
	closeDone chan struct{}
	closeErr  error
}

// New assigns an agent ID and writes the agent's index entry. It starts no
// process.
func New(cfg Config) (*Runtime, error) {
	id, err := newID()
	if err != nil {
		return nil, err
	}
	index, err := createIndex(id, agentRecord{Harness: cfg.Harness, Model: cfg.Model, WorkDir: cfg.WorkDir, Home: cfg.Home})
	if err != nil {
		return nil, err
	}
	grace := cfg.StopGrace
	if grace == 0 {
		grace = DefaultStopGrace
	}
	return &Runtime{id: id, adapter: cfg.Adapter, index: index, stopGrace: grace, closeDone: make(chan struct{})}, nil
}

// ID returns the agent ID.
func (r *Runtime) ID() string { return r.id }

// Turn starts a turn and returns once the harness accepted the prompt.
// ctx governs the whole turn: cancelling it later stops the turn.
func (r *Runtime) Turn(ctx context.Context, prompt string) (*Turn, error) {
	id, err := newID()
	if err != nil {
		return nil, err
	}
	t := newTurn(r, id)

	r.mu.Lock()
	switch {
	case r.closed:
		r.mu.Unlock()
		return nil, ErrClosed
	case r.active != nil:
		r.mu.Unlock()
		return nil, ErrTurnInProgress
	case prompt == "":
		r.mu.Unlock()
		return nil, errors.New("cloudagent: prompt is empty")
	}
	// Close cancels the start through startCtx; ctx bounds it as well.
	startCtx, cancelStart := context.WithCancel(ctx)
	defer cancelStart()
	t.cancelStart = cancelStart
	r.active = t
	r.mu.Unlock()

	startedAt := time.Now()
	run, err := r.adapter.StartTurn(startCtx, prompt)
	if err != nil {
		t.abandon()
		// A start cut short by ctx reports ctx.Err(), as a running turn
		// would, and one cut short by Close reports ErrClosed; the prompt
		// may already be in the conversation.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if t.stoppedBy() == ErrClosed {
			return nil, ErrClosed
		}
		return nil, fmt.Errorf("cloudagent: start turn: %w", err)
	}
	ref := run.Ref()
	if err := r.index.startTurn(t.id, ref.Data, ref.SessionID, startedAt); err != nil {
		run.Kill()
		for range run.Messages() {
		}
		run.Wait()
		t.abandon()
		return nil, err
	}
	t.begin(ctx, run, ref)
	return t, nil
}

// Close stops the active turn through the graceful stop path, waits for it to
// end, and closes the harness. A turn that is still starting has its start
// cancelled. It is idempotent.
func (r *Runtime) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		<-r.closeDone
		return r.closeErr
	}
	r.closed = true
	t := r.active
	r.mu.Unlock()

	if t != nil {
		t.stop(ErrClosed)
		<-t.done
	}
	if err := r.adapter.Close(); err != nil {
		r.closeErr = fmt.Errorf("cloudagent: close harness: %w", err)
	}
	close(r.closeDone)
	return r.closeErr
}

// release frees the active-turn slot held by t.
func (r *Runtime) release(t *Turn) {
	r.mu.Lock()
	if r.active == t {
		r.active = nil
	}
	r.mu.Unlock()
}
