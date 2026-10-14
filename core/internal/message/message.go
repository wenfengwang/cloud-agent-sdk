// Package message defines the canonical message and tool types that every
// harness adapter produces. Package cloudagent re-exports them; see
// design/001-core-api.md.
package message

import "encoding/json"

// Kind identifies what a Message carries.
type Kind string

// The message kinds.
const (
	KindUser       Kind = "user"
	KindAssistant  Kind = "assistant"
	KindReasoning  Kind = "reasoning"
	KindToolCall   Kind = "tool_call"
	KindToolResult Kind = "tool_result"
)

// Message is one complete, typed canonical message of a turn. Exactly one of
// User, Assistant, Reasoning, ToolCall, and ToolResult is set, matching Kind.
type Message struct {
	ID              string
	TurnID          string
	ParentToolUseID string // non-empty when produced inside a subagent
	Kind            Kind

	User       *Text
	Assistant  *Text
	Reasoning  *Text
	ToolCall   *ToolCall
	ToolResult *ToolResult

	Raw json.RawMessage // the harness's original JSON for this message
}

// Text is the payload of user, assistant, and reasoning messages.
type Text struct{ Text string }

// ToolCall is a tool invocation issued by the model.
type ToolCall struct {
	ID      string
	Name    ToolName
	RawName string // the harness's tool name
	Input   ToolInput
}

// ToolResult is the completion of a tool call, correlated by CallID.
type ToolResult struct {
	CallID  string
	Name    ToolName
	Output  ToolOutput
	IsError bool
}
