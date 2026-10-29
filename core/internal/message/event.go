package message

// Event is one item of a turn's event stream: a complete message, or a delta
// of assistant text still being generated. Exactly one of Message and Delta
// is set.
type Event struct {
	Message *Message
	Delta   *Delta
	// StreamID, on a Message event, is the StreamID of the deltas this
	// message completes; empty when the message was not streamed.
	StreamID string
}

// Delta is a piece of assistant text as the harness generates it.
type Delta struct {
	StreamID        string // shared by the deltas of one streamed text; not a Message.ID
	TurnID          string
	ParentToolUseID string // non-empty when produced inside a subagent
	Kind            Kind   // KindAssistant
	Text            string // text appended since the previous delta of the stream
}
