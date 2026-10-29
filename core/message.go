package core

import "github.com/wenfengwang/cloud-agent-sdk/core/internal/message"

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

// Event is one item of Turn.Events: a complete Message, or a Delta of
// assistant text still being generated. Exactly one is set; on a Message
// event, StreamID names the deltas the message completes.
//
//	type Event struct {
//		Message  *Message
//		Delta    *Delta
//		StreamID string
//	}
type Event = message.Event

// Delta is a piece of assistant text as the harness generates it. The deltas
// of one text share a StreamID, which is not a Message.ID; concatenated in
// order they give the text so far.
//
//	type Delta struct {
//		StreamID        string
//		TurnID          string
//		ParentToolUseID string
//		Kind            Kind // KindAssistant
//		Text            string
//	}
type Delta = message.Delta
