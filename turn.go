package cloudagent

import (
	"iter"

	"github.com/wenfengwang/cloud-agent-sdk/internal/agent"
)

// Turn is one exchange of an AgentRuntime's conversation.
type Turn struct {
	t *agent.Turn
}

// Result is the outcome of a turn.
type Result struct {
	TurnID    string
	FinalText string // text of the last assistant message
	Usage     Usage
}

// Usage is the token usage of one turn.
type Usage struct {
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
}

// ID returns the turn ID, a UUIDv7.
func (t *Turn) ID() string { return t.t.ID() }

// Messages yields every message of the turn from the first, then blocks for
// new ones until the turn ends. It may be called any number of times,
// concurrently, during or after the turn. A non-nil error means messages
// could not be read; it ends the iteration.
func (t *Turn) Messages() iter.Seq2[Message, error] { return t.t.Messages() }

// Result blocks until the turn ends and returns the same values on every
// call. It returns ErrTerminated after Terminate, ctx.Err() after the turn's
// context was cancelled, ErrClosed after AgentRuntime.Close, and a wrapped
// harness error after a failure.
func (t *Turn) Result() (Result, error) {
	r, err := t.t.Result()
	return Result{TurnID: r.TurnID, FinalText: r.FinalText, Usage: Usage(r.Usage)}, err
}

// Terminate requests a graceful stop and returns without waiting; call
// Result to wait. Calling it on a finished turn is a no-op.
func (t *Turn) Terminate() error { return t.t.Terminate() }
