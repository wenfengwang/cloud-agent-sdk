package cloudagent

import "github.com/wenfengwang/cloud-agent-sdk/internal/message"

// Kind identifies what a Message carries.
type Kind = message.Kind

// The message kinds.
const (
	KindUser       Kind = message.KindUser
	KindAssistant  Kind = message.KindAssistant
	KindReasoning  Kind = message.KindReasoning
	KindToolCall   Kind = message.KindToolCall
	KindToolResult Kind = message.KindToolResult
)

// Message is one complete, typed canonical message of a turn.
//
//	type Message struct {
//		ID              string
//		TurnID          string
//		ParentToolUseID string // non-empty when produced inside a subagent
//		Kind            Kind
//
//		// Exactly one is set, matching Kind.
//		User       *Text
//		Assistant  *Text
//		Reasoning  *Text
//		ToolCall   *ToolCall
//		ToolResult *ToolResult
//
//		Raw json.RawMessage // the harness's original JSON for this message
//	}
type Message = message.Message

// Text is the payload of user, assistant, and reasoning messages.
type Text = message.Text

// ToolCall is a tool invocation issued by the model: ID, Name, RawName (the
// harness's tool name), and the typed Input. It is emitted when the harness
// issues the call.
type ToolCall = message.ToolCall

// ToolResult is the completion of a tool call: CallID (the ToolCall's ID),
// Name, the typed Output, and IsError. It is emitted when the call completes.
type ToolResult = message.ToolResult
