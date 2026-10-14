package claude

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/wenfengwang/cloud-agent-sdk/core/internal/message"
)

// entry is the part of a stream-json line or transcript entry the
// translation reads. Stream lines carry parent_tool_use_id; transcript
// entries of a subagent get the parent from their subagent file instead.
type entry struct {
	Type            string  `json:"type"`
	UUID            string  `json:"uuid"`
	ParentToolUseID *string `json:"parent_tool_use_id"`
	IsReplay        bool    `json:"isReplay"`
	Message         struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// block is one content block of an assistant or user message.
type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// translator turns Claude Code messages into canonical messages. It remembers
// tool calls so that results carry the name of their call; use one
// translator per turn, fed in harness order.
type translator struct {
	calls map[string]message.ToolName
}

func newTranslator() *translator {
	return &translator{calls: map[string]message.ToolName{}}
}

// streamLine translates one stdout line of the stream-json protocol. The
// echoed prompt (isReplay) becomes the turn's user message; every other user
// text (subagent prompts, interruption notices) is not a canonical message.
func (t *translator) streamLine(line []byte) ([]message.Message, error) {
	var e entry
	if err := json.Unmarshal(line, &e); err != nil {
		return nil, fmt.Errorf("claude: parse stream line: %w", err)
	}
	parent := ""
	if e.ParentToolUseID != nil {
		parent = *e.ParentToolUseID
	}
	return t.translate(e, line, parent, e.IsReplay)
}

// transcriptEntry translates one transcript entry; prompt marks the turn's
// user message.
func (t *translator) transcriptEntry(line []byte, parent string, prompt bool) ([]message.Message, error) {
	var e entry
	if err := json.Unmarshal(line, &e); err != nil {
		return nil, fmt.Errorf("claude: parse transcript entry: %w", err)
	}
	return t.translate(e, line, parent, prompt)
}

func (t *translator) translate(e entry, raw []byte, parent string, prompt bool) ([]message.Message, error) {
	if e.Type != "assistant" && e.Type != "user" {
		return nil, nil
	}
	raw = append([]byte(nil), raw...)
	base := message.Message{ParentToolUseID: parent, Raw: raw}

	if e.Type == "user" && prompt {
		text, err := contentText(e.Message.Content)
		if err != nil {
			return nil, fmt.Errorf("claude: user message %s: %w", e.UUID, err)
		}
		m := base
		m.ID = e.UUID
		m.Kind = message.KindUser
		m.User = &message.Text{Text: text}
		return []message.Message{m}, nil
	}

	var blocks []block
	if len(e.Message.Content) == 0 || e.Message.Content[0] != '[' {
		return nil, nil // plain-text user content: not the turn's prompt
	}
	if err := json.Unmarshal(e.Message.Content, &blocks); err != nil {
		return nil, fmt.Errorf("claude: %s message %s: %w", e.Type, e.UUID, err)
	}
	var out []message.Message
	for i, b := range blocks {
		m := base
		m.ID = e.UUID + ":" + strconv.Itoa(i)
		switch {
		case e.Type == "assistant" && b.Type == "text":
			m.Kind = message.KindAssistant
			m.Assistant = &message.Text{Text: b.Text}
		case e.Type == "assistant" && b.Type == "thinking":
			m.Kind = message.KindReasoning
			m.Reasoning = &message.Text{Text: b.Thinking}
		case e.Type == "assistant" && b.Type == "tool_use":
			name, input := toolInput(b.Name, b.Input)
			t.calls[b.ID] = name
			m.Kind = message.KindToolCall
			m.ToolCall = &message.ToolCall{ID: b.ID, Name: name, RawName: b.Name, Input: input}
		case e.Type == "user" && b.Type == "tool_result":
			name, ok := t.calls[b.ToolUseID]
			if !ok {
				name = message.ToolNameUnknown
			}
			m.Kind = message.KindToolResult
			m.ToolResult = &message.ToolResult{CallID: b.ToolUseID, Name: name, Output: toolOutput(name, b), IsError: b.IsError}
		default:
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

// contentText returns the text of message content: a string, or the text
// blocks of a block list joined by newlines.
func contentText(content json.RawMessage) (string, error) {
	if len(content) == 0 || string(content) == "null" {
		return "", nil
	}
	if content[0] == '"' {
		var s string
		err := json.Unmarshal(content, &s)
		return s, err
	}
	var blocks []block
	if err := json.Unmarshal(content, &blocks); err != nil {
		return "", err
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n"), nil
}

// toolInput maps a Claude Code tool call to its canonical name and input.
// Input that does not decode into the canonical shape falls back to unknown.
func toolInput(rawName string, input json.RawMessage) (message.ToolName, message.ToolInput) {
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	decode := func(v any) bool { return json.Unmarshal(input, v) == nil }
	switch rawName {
	case "Bash":
		var in struct {
			Command     string `json:"command"`
			Description string `json:"description"`
			Timeout     int64  `json:"timeout"`
		}
		if decode(&in) {
			return message.ToolNameShell, &message.ShellInput{Command: in.Command, Description: in.Description, TimeoutMs: in.Timeout}
		}
	case "Read":
		var in struct {
			FilePath string `json:"file_path"`
			Offset   int    `json:"offset"`
			Limit    int    `json:"limit"`
		}
		if decode(&in) {
			return message.ToolNameFileRead, &message.FileReadInput{Path: in.FilePath, Offset: in.Offset, Limit: in.Limit}
		}
	case "Write":
		var in struct {
			FilePath string `json:"file_path"`
			Content  string `json:"content"`
		}
		if decode(&in) {
			return message.ToolNameFileWrite, &message.FileWriteInput{Path: in.FilePath, Content: in.Content}
		}
	case "Edit":
		var in struct {
			FilePath   string `json:"file_path"`
			OldString  string `json:"old_string"`
			NewString  string `json:"new_string"`
			ReplaceAll bool   `json:"replace_all"`
		}
		if decode(&in) {
			return message.ToolNameFileEdit, &message.FileEditInput{Path: in.FilePath, OldString: in.OldString, NewString: in.NewString, ReplaceAll: in.ReplaceAll}
		}
	case "Glob":
		var in struct {
			Pattern string `json:"pattern"`
			Path    string `json:"path"`
		}
		if decode(&in) {
			return message.ToolNameGlob, &message.GlobInput{Pattern: in.Pattern, Path: in.Path}
		}
	case "Grep":
		var in struct {
			Pattern string `json:"pattern"`
			Path    string `json:"path"`
			Glob    string `json:"glob"`
		}
		if decode(&in) {
			return message.ToolNameGrep, &message.GrepInput{Pattern: in.Pattern, Path: in.Path, Glob: in.Glob}
		}
	case "WebFetch":
		var in struct {
			URL    string `json:"url"`
			Prompt string `json:"prompt"`
		}
		if decode(&in) {
			return message.ToolNameWebFetch, &message.WebFetchInput{URL: in.URL, Prompt: in.Prompt}
		}
	case "WebSearch":
		var in struct {
			Query string `json:"query"`
		}
		if decode(&in) {
			return message.ToolNameWebSearch, &message.WebSearchInput{Query: in.Query}
		}
	case "Agent", "Task":
		var in struct {
			Description  string `json:"description"`
			Prompt       string `json:"prompt"`
			SubagentType string `json:"subagent_type"`
		}
		if decode(&in) {
			return message.ToolNameSubagent, &message.SubagentInput{Description: in.Description, Prompt: in.Prompt, Type: in.SubagentType}
		}
	default:
		if server, tool, ok := mcpName(rawName); ok {
			return message.ToolNameMCP, &message.MCPInput{Server: server, Tool: tool, Arguments: input}
		}
	}
	return message.ToolNameUnknown, &message.UnknownInput{Raw: input}
}

// mcpName splits mcp__<server>__<tool>.
func mcpName(rawName string) (server, tool string, ok bool) {
	rest, ok := strings.CutPrefix(rawName, "mcp__")
	if !ok {
		return "", "", false
	}
	server, tool, ok = strings.Cut(rest, "__")
	if !ok || server == "" || tool == "" {
		return "", "", false
	}
	return server, tool, true
}

// exitCodeLine is the first line Claude Code puts in the result of a Bash
// call that exited non-zero.
var exitCodeLine = regexp.MustCompile(`^Exit code (-?\d+)(?:\n|$)`)

// toolOutput maps a tool_result block to the output type of name.
func toolOutput(name message.ToolName, b block) message.ToolOutput {
	content := b.Content
	if len(content) == 0 {
		content = json.RawMessage(`""`)
	}
	switch name {
	case message.ToolNameUnknown:
		return &message.UnknownOutput{Raw: content}
	case message.ToolNameMCP:
		return &message.MCPOutput{Content: content}
	}
	text, err := contentText(content)
	if err != nil {
		text = string(content)
	}
	switch name {
	case message.ToolNameShell:
		return shellOutput(text, b.IsError)
	case message.ToolNameFileRead:
		return &message.FileReadOutput{Content: text}
	}
	return &message.TextOutput{Text: text}
}

// shellOutput reads the exit code Claude Code reports for a Bash call: a
// successful result means exit code 0, a failed one starts with "Exit code
// N". Any other failure (a rejected or interrupted call) reports none.
func shellOutput(text string, isError bool) *message.ShellOutput {
	if !isError {
		code := 0
		return &message.ShellOutput{Output: text, ExitCode: &code}
	}
	if m := exitCodeLine.FindStringSubmatch(text); m != nil {
		if code, err := strconv.Atoi(m[1]); err == nil {
			return &message.ShellOutput{Output: text[len(m[0]):], ExitCode: &code}
		}
	}
	return &message.ShellOutput{Output: text}
}
