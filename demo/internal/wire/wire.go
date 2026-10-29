// Package wire defines the demo's JSON DTOs and converts SDK messages and
// results into them. Kind and tool values are the SDK's string values
// unchanged.
package wire

import (
	"encoding/json"
	"time"
	"unicode/utf8"

	"github.com/wenfengwang/cloud-agent-sdk/demo/internal/sdk"
)

// DefaultLimit is the largest field a DTO carries unchanged.
const DefaultLimit = 512 << 10

// Time marshals as an RFC 3339 UTC timestamp with milliseconds.
type Time time.Time

const timeLayout = "2006-01-02T15:04:05.000Z07:00"

// MarshalJSON implements json.Marshaler.
func (t Time) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Time(t).UTC().Format(timeLayout))
}

// UnmarshalJSON implements json.Unmarshaler.
func (t *Time) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	parsed, err := time.Parse(timeLayout, s)
	if err != nil {
		return err
	}
	*t = Time(parsed)
	return nil
}

// Message is the wire form of one SDK message.
type Message struct {
	ID              string          `json:"id"`
	TurnID          string          `json:"turnId"`
	ParentToolUseID string          `json:"parentToolUseId,omitempty"`
	Kind            string          `json:"kind"`
	ReceivedAt      Time            `json:"receivedAt"`
	Text            *string         `json:"text,omitempty"`
	ToolCall        *ToolCall       `json:"toolCall,omitempty"`
	ToolResult      *ToolResult     `json:"toolResult,omitempty"`
	StreamID        string          `json:"streamId,omitempty"` // the delta stream this message completes
	Truncated       bool            `json:"truncated"`
	Raw             json.RawMessage `json:"raw,omitempty"`
}

// ToolCall is the wire form of a tool call. Input is one of the *Input types
// of this package.
type ToolCall struct {
	ID      string `json:"id"`
	Tool    string `json:"tool"`
	RawName string `json:"rawName"`
	Input   any    `json:"input"`
}

// ToolResult is the wire form of a tool result. Output is one of the *Output
// types of this package.
type ToolResult struct {
	CallID  string `json:"callId"`
	Tool    string `json:"tool"`
	IsError bool   `json:"isError"`
	Output  any    `json:"output"`
}

// Tool inputs, discriminated by Type.
type (
	ShellInput struct {
		Type        string `json:"type"`
		Command     string `json:"command"`
		Description string `json:"description"`
		TimeoutMs   int64  `json:"timeoutMs"`
	}
	FileReadInput struct {
		Type   string `json:"type"`
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	FileWriteInput struct {
		Type    string `json:"type"`
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	FileEditInput struct {
		Type       string `json:"type"`
		Path       string `json:"path"`
		OldString  string `json:"oldString"`
		NewString  string `json:"newString"`
		ReplaceAll bool   `json:"replaceAll"`
	}
	FilePatchInput struct {
		Type    string       `json:"type"`
		Changes []FileChange `json:"changes"`
	}
	FileChange struct {
		Path string `json:"path"`
		Kind string `json:"kind"`
		Diff string `json:"diff"`
	}
	GlobInput struct {
		Type    string `json:"type"`
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	GrepInput struct {
		Type    string `json:"type"`
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Glob    string `json:"glob"`
	}
	WebFetchInput struct {
		Type   string `json:"type"`
		URL    string `json:"url"`
		Prompt string `json:"prompt"`
	}
	WebSearchInput struct {
		Type  string `json:"type"`
		Query string `json:"query"`
	}
	MCPInput struct {
		Type      string          `json:"type"`
		Server    string          `json:"server"`
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	SubagentInput struct {
		Type         string `json:"type"`
		Description  string `json:"description"`
		Prompt       string `json:"prompt"`
		SubagentType string `json:"subagentType"`
	}
	UnknownInput struct {
		Type string          `json:"type"`
		Raw  json.RawMessage `json:"raw"`
	}
)

// Tool outputs, discriminated by Type.
type (
	ShellOutput struct {
		Type     string `json:"type"`
		Output   string `json:"output"`
		ExitCode *int   `json:"exitCode"`
	}
	FileReadOutput struct {
		Type    string `json:"type"`
		Content string `json:"content"`
	}
	TextOutput struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	MCPOutput struct {
		Type    string          `json:"type"`
		Content json.RawMessage `json:"content"`
	}
	UnknownOutput struct {
		Type string          `json:"type"`
		Raw  json.RawMessage `json:"raw"`
	}
)

// Usage carries the SDK's Usage counts unchanged.
type Usage struct {
	InputTokens      int64 `json:"inputTokens"`
	OutputTokens     int64 `json:"outputTokens"`
	CacheReadTokens  int64 `json:"cacheReadTokens"`
	CacheWriteTokens int64 `json:"cacheWriteTokens"`
}

// ConvertUsage converts the SDK's Usage.
func ConvertUsage(u sdk.Usage) Usage {
	return Usage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CacheReadTokens: u.CacheReadTokens, CacheWriteTokens: u.CacheWriteTokens}
}

// Converter converts SDK messages into DTOs.
type Converter struct {
	DebugRaw bool // include the harness's raw JSON
	Limit    int  // largest field carried unchanged; 0 means DefaultLimit
}

// Message converts m, stamped with receivedAt.
func (c Converter) Message(m sdk.Message, receivedAt time.Time) Message {
	limit := c.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	cv := &conversion{limit: limit}
	out := Message{
		ID:              m.ID,
		TurnID:          m.TurnID,
		ParentToolUseID: m.ParentToolUseID,
		Kind:            string(m.Kind),
		ReceivedAt:      Time(receivedAt),
	}
	switch m.Kind {
	case sdk.KindUser:
		out.Text = cv.text(m.User)
	case sdk.KindAssistant:
		out.Text = cv.text(m.Assistant)
	case sdk.KindReasoning:
		out.Text = cv.text(m.Reasoning)
	case sdk.KindToolCall:
		if m.ToolCall != nil {
			out.ToolCall = &ToolCall{ID: m.ToolCall.ID, Tool: string(m.ToolCall.Name), RawName: m.ToolCall.RawName, Input: cv.input(m.ToolCall.Input)}
		}
	case sdk.KindToolResult:
		if m.ToolResult != nil {
			out.ToolResult = &ToolResult{CallID: m.ToolResult.CallID, Tool: string(m.ToolResult.Name), IsError: m.ToolResult.IsError, Output: cv.output(m.ToolResult.Output)}
		}
	}
	if c.DebugRaw {
		out.Raw = cv.json(m.Raw)
	}
	out.Truncated = cv.truncated
	return out
}

// conversion applies the size limit to every field of one message and
// records whether any field was cut.
type conversion struct {
	limit     int
	truncated bool
}

func (c *conversion) text(t *sdk.Text) *string {
	s := ""
	if t != nil {
		s = c.str(t.Text)
	}
	return &s
}

// str cuts s at the limit, backed off to a rune boundary.
func (c *conversion) str(s string) string {
	if len(s) <= c.limit {
		return s
	}
	c.truncated = true
	return cutRunes(s, c.limit)
}

// json replaces a value larger than the limit with a JSON string holding a
// preview of its first bytes; an embedded value is never cut in place.
func (c *conversion) json(v json.RawMessage) json.RawMessage {
	if v == nil {
		return json.RawMessage("null")
	}
	if len(v) <= c.limit {
		return v
	}
	c.truncated = true
	preview, _ := json.Marshal(cutRunes(string(v), c.limit))
	return preview
}

func cutRunes(s string, n int) string {
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func (c *conversion) input(in sdk.ToolInput) any {
	switch in := in.(type) {
	case *sdk.ShellInput:
		return ShellInput{Type: "shell", Command: c.str(in.Command), Description: c.str(in.Description), TimeoutMs: in.TimeoutMs}
	case *sdk.FileReadInput:
		return FileReadInput{Type: "fileRead", Path: c.str(in.Path), Offset: in.Offset, Limit: in.Limit}
	case *sdk.FileWriteInput:
		return FileWriteInput{Type: "fileWrite", Path: c.str(in.Path), Content: c.str(in.Content)}
	case *sdk.FileEditInput:
		return FileEditInput{Type: "fileEdit", Path: c.str(in.Path), OldString: c.str(in.OldString), NewString: c.str(in.NewString), ReplaceAll: in.ReplaceAll}
	case *sdk.FilePatchInput:
		changes := make([]FileChange, len(in.Changes))
		for i, ch := range in.Changes {
			changes[i] = FileChange{Path: c.str(ch.Path), Kind: ch.Kind, Diff: c.str(ch.Diff)}
		}
		return FilePatchInput{Type: "filePatch", Changes: changes}
	case *sdk.GlobInput:
		return GlobInput{Type: "glob", Pattern: c.str(in.Pattern), Path: c.str(in.Path)}
	case *sdk.GrepInput:
		return GrepInput{Type: "grep", Pattern: c.str(in.Pattern), Path: c.str(in.Path), Glob: c.str(in.Glob)}
	case *sdk.WebFetchInput:
		return WebFetchInput{Type: "webFetch", URL: c.str(in.URL), Prompt: c.str(in.Prompt)}
	case *sdk.WebSearchInput:
		return WebSearchInput{Type: "webSearch", Query: c.str(in.Query)}
	case *sdk.MCPInput:
		return MCPInput{Type: "mcp", Server: c.str(in.Server), Tool: c.str(in.Tool), Arguments: c.json(in.Arguments)}
	case *sdk.SubagentInput:
		return SubagentInput{Type: "subagent", Description: c.str(in.Description), Prompt: c.str(in.Prompt), SubagentType: c.str(in.Type)}
	case *sdk.UnknownInput:
		return UnknownInput{Type: "unknown", Raw: c.json(in.Raw)}
	default:
		return UnknownInput{Type: "unknown", Raw: json.RawMessage("null")}
	}
}

func (c *conversion) output(out sdk.ToolOutput) any {
	switch out := out.(type) {
	case *sdk.ShellOutput:
		return ShellOutput{Type: "shell", Output: c.str(out.Output), ExitCode: out.ExitCode}
	case *sdk.FileReadOutput:
		return FileReadOutput{Type: "fileRead", Content: c.str(out.Content)}
	case *sdk.TextOutput:
		return TextOutput{Type: "text", Text: c.str(out.Text)}
	case *sdk.MCPOutput:
		return MCPOutput{Type: "mcp", Content: c.json(out.Content)}
	case *sdk.UnknownOutput:
		return UnknownOutput{Type: "unknown", Raw: c.json(out.Raw)}
	default:
		return UnknownOutput{Type: "unknown", Raw: json.RawMessage("null")}
	}
}
