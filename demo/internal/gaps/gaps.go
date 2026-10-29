// Package gaps is the registry of SDK v1 gaps, served by GET /api/config.
// Design 001 (SDK Gaps) owns the IDs and reasons; this list mirrors it.
package gaps

// Gap is one SDK gap: an ID the frontend maps to a control, and the reason
// shown to the user.
type Gap struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// All returns every gap in registry order.
func All() []Gap {
	return append([]Gap(nil), registry...)
}

var registry = []Gap{
	{"history", "Sessions need an SDK API to list and reopen agents."},
	{"permissions", "The SDK always runs harnesses without permission prompts."},
	{"effort", "Turn options have no reasoning effort yet."},
	{"model-switch", "The model is fixed when a session starts."},
	{"model-list", "The SDK has no model list; suggestions come from configuration."},
	{"context-usage", "The SDK reports per-turn tokens only, without window size or cost."},
	{"attachments", "Prompts are text only."},
	{"extra-directories", "A session has one WorkDir."},
	{"voice", "Voice input is not part of the SDK."},
	{"cloud", "The SDK runs harnesses on this machine."},
	{"search", "Searching needs an SDK transcript query API."},
	{"streaming-text", "Messages arrive complete, without deltas."},
	{"plan", "Plan and task items are not part of the message model."},
	{"steering", "A turn cannot take input while it runs, and history cannot be edited."},
	{"slash-commands", "Not part of the SDK."},
	{"chat-mode", "The SDK drives coding harnesses only."},
	{"notifications", "Not part of the SDK."},
	{"index-location", "The SDK index location cannot be configured."},
	{"titles", "The SDK exposes no title."},
	{"timestamps", "Messages and results carry no timestamps."},
	{"changed-files", "The SDK has no diff API."},
	{"stop-while-starting", "No turn handle exists until Turn returns."},
	{"cancellable-messages", "Messages() takes no context."},
	{"message-encoding", "Messages have no canonical JSON encoding."},
	{"harness-status", "The SDK has no API for harness availability or version."},
	{"pinned-versions", "The SDK exports no pinned versions."},
	{"stderr", "Shell output is one string."},
}
