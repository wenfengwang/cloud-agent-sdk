package cloudagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wenfengwang/cloud-agent-sdk/internal/agent"
	"github.com/wenfengwang/cloud-agent-sdk/internal/claude"
	"github.com/wenfengwang/cloud-agent-sdk/internal/codex"
	"github.com/wenfengwang/cloud-agent-sdk/internal/harness"
)

// Harness selects the agent harness an AgentRuntime drives.
type Harness string

// The supported harnesses.
const (
	Claude Harness = "claude" // Claude Code
	OpenAI Harness = "openai" // Codex
)

// Options configures an AgentRuntime.
type Options struct {
	Model   string // empty selects the harness's default model
	WorkDir string // required; the harness's working directory
	Home    string // harness home; empty selects ~/.claude or ~/.codex
}

// TurnOptions configures a turn. It has no fields yet.
type TurnOptions struct{}

// The sentinel errors. All other errors are wrapped with context and can be
// inspected with errors.Is and errors.As.
var (
	// ErrTurnInProgress is returned by AgentRuntime.Turn while another turn
	// is active.
	ErrTurnInProgress = agent.ErrTurnInProgress
	// ErrTerminated is returned by Turn.Result after Turn.Terminate.
	ErrTerminated = agent.ErrTerminated
	// ErrClosed is returned by AgentRuntime.Turn after AgentRuntime.Close,
	// and by Turn.Result for a turn that Close stopped.
	ErrClosed = agent.ErrClosed
)

// AgentRuntime is one agent backed by one harness, holding one linear
// conversation. It is safe for concurrent use; at most one turn is active at
// a time.
type AgentRuntime struct {
	rt *agent.Runtime
}

// New validates opts, assigns an agent ID, and writes the agent's entry under
// ~/.cloudagent. It starts no process and creates no session; a missing
// harness CLI surfaces as a start error from the first Turn.
func New(h Harness, opts Options) (*AgentRuntime, error) {
	if opts.WorkDir == "" {
		return nil, errors.New("cloudagent: Options.WorkDir is required")
	}
	workDir, err := filepath.Abs(opts.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("cloudagent: resolve WorkDir: %w", err)
	}
	info, err := os.Stat(workDir)
	if err != nil {
		return nil, fmt.Errorf("cloudagent: WorkDir: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("cloudagent: WorkDir %s is not a directory", workDir)
	}
	home := opts.Home
	if home != "" {
		if home, err = filepath.Abs(home); err != nil {
			return nil, fmt.Errorf("cloudagent: resolve Home: %w", err)
		}
	}

	cfg := harness.Config{Model: opts.Model, WorkDir: workDir, Home: home}
	var adapter harness.Harness
	switch h {
	case Claude:
		adapter = claude.New(cfg)
	case OpenAI:
		adapter = codex.New(cfg)
	default:
		return nil, fmt.Errorf("cloudagent: unknown harness %q", h)
	}

	rt, err := agent.New(agent.Config{Harness: string(h), Model: opts.Model, WorkDir: workDir, Home: home, Adapter: adapter})
	if err != nil {
		return nil, err
	}
	return &AgentRuntime{rt: rt}, nil
}

// ID returns the agent ID, a UUIDv7.
func (a *AgentRuntime) ID() string { return a.rt.ID() }

// Turn starts one turn and returns as soon as the harness has accepted the
// prompt. It returns a start error when the agent is closed (ErrClosed),
// another turn is active (ErrTurnInProgress), the prompt is empty, the
// harness CLI cannot be started, or the harness rejects the prompt. Once it
// returns a *Turn, every later failure is reported by Turn.Result.
//
// ctx governs the turn's execution, not only the call: cancelling it after
// Turn returns stops the turn. Cancelling it while Turn still waits for the
// harness to accept the prompt makes Turn return ctx.Err(); the prompt may
// already have entered the harness conversation.
func (a *AgentRuntime) Turn(ctx context.Context, prompt string, opts TurnOptions) (*Turn, error) {
	t, err := a.rt.Turn(ctx, prompt)
	if err != nil {
		return nil, err
	}
	return &Turn{t: t}, nil
}

// Close stops the active turn, if any, through the graceful stop path, waits
// for it to end, and stops the harness process. That turn's Result returns
// ErrClosed. A Turn call still waiting for the harness to accept its prompt
// is cancelled and returns ErrClosed. Close is idempotent; afterwards Turn fails with ErrClosed, and
// turns that already ended can still be read.
func (a *AgentRuntime) Close() error { return a.rt.Close() }
