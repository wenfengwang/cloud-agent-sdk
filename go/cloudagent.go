package cloudagent

import "github.com/wenfengwang/cloud-agent-sdk/core"

// Agents and turns.
type (
	// AgentRuntime is one agent backed by one harness, holding one linear
	// conversation.
	AgentRuntime = core.AgentRuntime
	// Harness selects the agent harness an AgentRuntime drives.
	Harness = core.Harness
	// Options configures an AgentRuntime.
	Options = core.Options
	// Turn is one exchange of an AgentRuntime's conversation.
	Turn = core.Turn
	// TurnOptions configures one turn.
	TurnOptions = core.TurnOptions
	// Result is the outcome of a turn.
	Result = core.Result
	// Usage is the token usage of one turn, normalized across harnesses.
	Usage = core.Usage
)

// The supported harnesses.
const (
	ClaudeCode = core.ClaudeCode
	Codex      = core.Codex
)

// Errors returned by AgentRuntime and Turn.
var (
	ErrTurnInProgress = core.ErrTurnInProgress
	ErrTerminated     = core.ErrTerminated
	ErrClosed         = core.ErrClosed
)

// New returns an AgentRuntime for harness h.
func New(h Harness, opts Options) (*AgentRuntime, error) {
	return core.New(h, opts)
}

// Messages.
type (
	// Message is one complete, typed canonical message of a turn.
	Message = core.Message
	// Kind identifies which payload of a Message is set.
	Kind = core.Kind
	// Text is the payload of user, assistant, and reasoning messages.
	Text = core.Text
	// ToolCall is a tool invocation issued by the model.
	ToolCall = core.ToolCall
	// ToolResult is the completion of a tool call, correlated by CallID.
	ToolResult = core.ToolResult
)

// The message kinds.
const (
	KindUser       = core.KindUser
	KindAssistant  = core.KindAssistant
	KindReasoning  = core.KindReasoning
	KindToolCall   = core.KindToolCall
	KindToolResult = core.KindToolResult
)

// Tools.
type (
	// ToolName is the canonical name of a tool.
	ToolName = core.ToolName
	// ToolInput is the typed input of a tool call.
	ToolInput = core.ToolInput
	// ToolOutput is the typed output of a tool result.
	ToolOutput = core.ToolOutput

	ShellInput     = core.ShellInput
	ShellOutput    = core.ShellOutput
	FileReadInput  = core.FileReadInput
	FileReadOutput = core.FileReadOutput
	FileWriteInput = core.FileWriteInput
	FileEditInput  = core.FileEditInput
	FilePatchInput = core.FilePatchInput
	FileChange     = core.FileChange
	GlobInput      = core.GlobInput
	GrepInput      = core.GrepInput
	WebFetchInput  = core.WebFetchInput
	WebSearchInput = core.WebSearchInput
	MCPInput       = core.MCPInput
	MCPOutput      = core.MCPOutput
	SubagentInput  = core.SubagentInput
	TextOutput     = core.TextOutput
	UnknownInput   = core.UnknownInput
	UnknownOutput  = core.UnknownOutput
)

// The canonical tool names.
const (
	ToolNameShell     = core.ToolNameShell
	ToolNameFileRead  = core.ToolNameFileRead
	ToolNameFileWrite = core.ToolNameFileWrite
	ToolNameFileEdit  = core.ToolNameFileEdit
	ToolNameFilePatch = core.ToolNameFilePatch
	ToolNameGlob      = core.ToolNameGlob
	ToolNameGrep      = core.ToolNameGrep
	ToolNameWebFetch  = core.ToolNameWebFetch
	ToolNameWebSearch = core.ToolNameWebSearch
	ToolNameMCP       = core.ToolNameMCP
	ToolNameSubagent  = core.ToolNameSubagent
	ToolNameUnknown   = core.ToolNameUnknown
)
