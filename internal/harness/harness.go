// Package harness defines the contract between the harness-neutral turn
// lifecycle in internal/agent and the harness adapters in internal/claude and
// internal/codex. Behavior is owned by design/001-core-api.md.
//
// Division of work: an adapter starts and talks to its harness process,
// translates harness output into complete canonical messages, and reads
// finished turns back from the harness home. The agent owns everything else:
// the single-active-turn rule, IDs, stamping Message.TurnID, the in-memory
// buffer and its fan-out, the stop causes and their errors, escalation from
// Interrupt to Kill, Result.FinalText, and the ~/.cloudagent index.
package harness

import (
	"context"
	"encoding/json"

	"github.com/wenfengwang/cloud-agent-sdk/internal/message"
)

// Config configures an adapter. It is fixed for the adapter's lifetime.
type Config struct {
	// Model is passed to the harness; empty selects the harness's default.
	Model string
	// WorkDir is the harness's working directory. It is absolute and exists.
	WorkDir string
	// Home is the harness home, passed as CLAUDE_CONFIG_DIR or CODEX_HOME.
	// Empty leaves the variable as inherited, which selects ~/.claude or
	// ~/.codex.
	Home string
}

// Ref identifies a turn inside the harness home.
type Ref struct {
	// SessionID is the harness conversation: the Claude Code session ID or
	// the Codex thread ID. It is written to agent.json as
	// harness_session_id.
	SessionID string
	// Data is the turn reference of design 001, written verbatim to
	// turns.jsonl as harness_ref: {"session_id","user_message_uuid"} for
	// Claude Code, {"thread_id","turn_id"} for Codex. It must be valid JSON
	// or nil.
	Data json.RawMessage
}

// Usage is the token usage of one turn, normalized across harnesses:
// InputTokens excludes tokens read from the prompt cache, which are counted
// in CacheReadTokens; CacheWriteTokens counts tokens written to the cache.
type Usage struct {
	InputTokens      int64 // input tokens not read from the cache
	OutputTokens     int64
	CacheReadTokens  int64 // input tokens read from the cache
	CacheWriteTokens int64 // input tokens written to the cache
}

// Outcome is what a finished run reports besides its messages.
type Outcome struct {
	// Usage is the turn's token usage, zero when the harness reported none.
	Usage Usage
	// Ref is the final turn reference. A zero Ref keeps Run.Ref.
	Ref Ref
}

// Harness is one adapter instance, owned by one agent runtime. The agent
// calls StartTurn only when no Run of this Harness is active, calls Close
// once, after every Run has ended, and may call ReadTurn at any time,
// concurrently with anything else, including after Close.
type Harness interface {
	// StartTurn starts or reuses the harness process and submits prompt as
	// the next turn of the conversation. It returns once the harness has
	// accepted the prompt (design 001, "Turn accepted"). Any error is a
	// start error, and no Run is active after it.
	//
	// ctx bounds only the start. The process must not be bound to ctx (no
	// exec.CommandContext with it): the agent watches the turn's context
	// itself and stops the run through Interrupt.
	//
	// The first turn creates the conversation; every later turn continues
	// it, including after a turn that was interrupted, killed, or failed.
	StartTurn(ctx context.Context, prompt string) (Run, error)

	// ReadTurn reads the messages of a finished turn back from the harness
	// home, in the order they were produced, translated exactly as a live
	// Run translates them (the same Message.ID for the same harness item).
	// TurnID is left empty. It starts no process.
	ReadTurn(ctx context.Context, ref Ref) ([]message.Message, error)

	// Close stops the harness process, if any (the Codex app-server), and
	// releases its resources. It must leave no process running.
	Close() error
}

// Run is one accepted turn.
//
// Messages must be drained until it is closed, and only then is Wait called,
// once. Interrupt and Kill may be called at any time from any goroutine,
// including after the run ended, and more than once.
type Run interface {
	// Ref is the turn reference known at acceptance. It is written to the
	// index while the turn runs; SessionID must already be set.
	Ref() Ref

	// Messages yields the turn's complete canonical messages in harness
	// order: the user message first, then everything the harness produced.
	// Message.ID is deterministic from harness IDs so that ReadTurn yields
	// the same IDs; TurnID is left empty; Raw is always set. The channel is
	// closed when the turn has ended: the harness finished, was
	// interrupted or killed, or its process died.
	Messages() <-chan message.Message

	// Interrupt asks the harness to stop the turn gracefully (Claude Code:
	// interrupt control request; Codex: turn/interrupt). It does not block.
	Interrupt()

	// Kill ends the turn immediately by killing the harness process (and
	// its process group). The agent calls it when a run has not ended within
	// its grace period after Interrupt, and when the index cannot be
	// written after acceptance. After Kill, Messages is closed promptly. A
	// later StartTurn must start a fresh process that continues the
	// conversation.
	Kill()

	// Wait returns the outcome after Messages has been closed. A non-nil
	// error means the harness or its process failed: a non-zero exit, a
	// protocol or RPC error, or a turn the harness reported as failed. After
	// Interrupt or Kill the agent ignores the error.
	Wait() (Outcome, error)
}
