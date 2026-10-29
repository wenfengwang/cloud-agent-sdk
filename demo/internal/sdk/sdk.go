// Package sdk is the only demo package that imports the cloudagent SDK. It
// re-exports the SDK types the backend needs and puts thin interfaces over
// AgentRuntime and Turn so the rest of the backend can be tested with fakes.
package sdk

import (
	"context"
	"iter"

	cloudagent "github.com/wenfengwang/cloud-agent-sdk/go"
)

// Harnesses and options.
type (
	Harness     = cloudagent.Harness
	Options     = cloudagent.Options
	TurnOptions = cloudagent.TurnOptions
	Result      = cloudagent.Result
	Usage       = cloudagent.Usage
)

// The supported harnesses.
const (
	ClaudeCode = cloudagent.ClaudeCode
	Codex      = cloudagent.Codex
)

// The SDK's sentinel errors.
var (
	ErrTurnInProgress = cloudagent.ErrTurnInProgress
	ErrTerminated     = cloudagent.ErrTerminated
	ErrClosed         = cloudagent.ErrClosed
)

// Messages and tools.
type (
	Message    = cloudagent.Message
	Event      = cloudagent.Event
	Delta      = cloudagent.Delta
	Kind       = cloudagent.Kind
	Text       = cloudagent.Text
	ToolCall   = cloudagent.ToolCall
	ToolResult = cloudagent.ToolResult
	ToolName   = cloudagent.ToolName
	ToolInput  = cloudagent.ToolInput
	ToolOutput = cloudagent.ToolOutput

	ShellInput     = cloudagent.ShellInput
	ShellOutput    = cloudagent.ShellOutput
	FileReadInput  = cloudagent.FileReadInput
	FileReadOutput = cloudagent.FileReadOutput
	FileWriteInput = cloudagent.FileWriteInput
	FileEditInput  = cloudagent.FileEditInput
	FilePatchInput = cloudagent.FilePatchInput
	FileChange     = cloudagent.FileChange
	GlobInput      = cloudagent.GlobInput
	GrepInput      = cloudagent.GrepInput
	WebFetchInput  = cloudagent.WebFetchInput
	WebSearchInput = cloudagent.WebSearchInput
	MCPInput       = cloudagent.MCPInput
	MCPOutput      = cloudagent.MCPOutput
	SubagentInput  = cloudagent.SubagentInput
	TextOutput     = cloudagent.TextOutput
	UnknownInput   = cloudagent.UnknownInput
	UnknownOutput  = cloudagent.UnknownOutput
)

// The message kinds.
const (
	KindUser       = cloudagent.KindUser
	KindAssistant  = cloudagent.KindAssistant
	KindReasoning  = cloudagent.KindReasoning
	KindToolCall   = cloudagent.KindToolCall
	KindToolResult = cloudagent.KindToolResult
)

// The canonical tool names.
const (
	ToolNameShell     = cloudagent.ToolNameShell
	ToolNameFileRead  = cloudagent.ToolNameFileRead
	ToolNameFileWrite = cloudagent.ToolNameFileWrite
	ToolNameFileEdit  = cloudagent.ToolNameFileEdit
	ToolNameFilePatch = cloudagent.ToolNameFilePatch
	ToolNameGlob      = cloudagent.ToolNameGlob
	ToolNameGrep      = cloudagent.ToolNameGrep
	ToolNameWebFetch  = cloudagent.ToolNameWebFetch
	ToolNameWebSearch = cloudagent.ToolNameWebSearch
	ToolNameMCP       = cloudagent.ToolNameMCP
	ToolNameSubagent  = cloudagent.ToolNameSubagent
	ToolNameUnknown   = cloudagent.ToolNameUnknown
)

// Runtime is the part of cloudagent.AgentRuntime the backend uses.
type Runtime interface {
	ID() string
	Turn(ctx context.Context, prompt string) (Turn, error)
	Close() error
}

// Turn is the part of cloudagent.Turn the backend uses.
type Turn interface {
	ID() string
	Events() iter.Seq2[Event, error]
	Result() (Result, error)
	Terminate() error
}

// Factory creates runtimes. The backend receives one so tests can inject
// fakes.
type Factory interface {
	New(h Harness, opts Options) (Runtime, error)
}

// SDK is the Factory backed by the real cloudagent SDK.
type SDK struct{}

// New calls cloudagent.New.
func (SDK) New(h Harness, opts Options) (Runtime, error) {
	agent, err := cloudagent.New(h, opts)
	if err != nil {
		return nil, err
	}
	return runtime{agent}, nil
}

type runtime struct{ agent *cloudagent.AgentRuntime }

func (r runtime) ID() string   { return r.agent.ID() }
func (r runtime) Close() error { return r.agent.Close() }

func (r runtime) Turn(ctx context.Context, prompt string) (Turn, error) {
	turn, err := r.agent.Turn(ctx, prompt, cloudagent.TurnOptions{})
	if err != nil {
		return nil, err
	}
	return turn, nil
}
