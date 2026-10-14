package codex

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/wenfengwang/cloud-agent-sdk/core/internal/message"
)

// resultIDSuffix distinguishes the tool_result message of a tool item from
// its tool_call message, which carries the item ID itself.
const resultIDSuffix = "/result"

// item is the union of the app-server ThreadItem variants this adapter
// reads. Fields that the variants name alike but type differently (content)
// are kept raw and decoded per type.
type item struct {
	Type string `json:"type"`
	ID   string `json:"id"`

	Content json.RawMessage `json:"content"` // userMessage: []UserInput; reasoning: []string
	Text    string          `json:"text"`    // agentMessage
	Summary []string        `json:"summary"` // reasoning

	// commandExecution
	Command          string  `json:"command"`
	AggregatedOutput *string `json:"aggregatedOutput"`
	ExitCode         *int    `json:"exitCode"`
	Status           string  `json:"status"`

	// fileChange
	Changes []fileUpdateChange `json:"changes"`

	// mcpToolCall
	Server    string          `json:"server"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
	Result    *mcpResult      `json:"result"`
	Error     *struct {
		Message string `json:"message"`
	} `json:"error"`

	// webSearch
	Query   string          `json:"query"`
	Results json.RawMessage `json:"results"`

	// subAgentActivity
	Kind          string `json:"kind"`
	AgentThreadID string `json:"agentThreadId"`
	AgentPath     string `json:"agentPath"`
}

type fileUpdateChange struct {
	Path string `json:"path"`
	Kind struct {
		Type string `json:"type"`
	} `json:"kind"`
	Diff string `json:"diff"`
}

type mcpResult struct {
	Content json.RawMessage `json:"content"`
}

type userInput struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Item types. Tool items yield a tool_call when they start and a
// tool_result when they complete; text items yield one message when they
// complete; subAgentActivity items yield the subagent call and result; every
// other item type is not emitted (design 001).
const (
	itemUserMessage  = "userMessage"
	itemAgentMessage = "agentMessage"
	itemReasoning    = "reasoning"
	itemCommand      = "commandExecution"
	itemFileChange   = "fileChange"
	itemMCPToolCall  = "mcpToolCall"
	itemWebSearch    = "webSearch"
	itemCollabAgent  = "collabAgentToolCall"
	itemImageView    = "imageView"
	itemDynamicTool  = "dynamicToolCall"
	itemSleep        = "sleep"
	itemSubAgent     = "subAgentActivity"
)

// subAgentActivity kinds. Only started and completed are emitted.
const (
	subAgentStarted   = "started"
	subAgentCompleted = "completed"
)

func isToolItem(typ string) bool {
	switch typ {
	case itemCommand, itemFileChange, itemMCPToolCall, itemWebSearch, itemCollabAgent, itemImageView, itemDynamicTool, itemSleep:
		return true
	}
	return false
}

// translator turns the items of one turn into canonical messages. Items
// arrive from the turn's thread and from the threads of the subagents it
// starts; the latter carry the spawn call's ID as ParentToolUseID.
type translator struct {
	started  map[string]bool   // tool items (and subagent results) already emitted, by ID
	spawns   map[string]string // subagent thread ID -> ID of the subagent call that started it
	lastText map[string]string // thread ID -> text of its last agentMessage
}

func newTranslator() *translator {
	return &translator{started: map[string]bool{}, spawns: map[string]string{}, lastText: map[string]string{}}
}

// follows reports whether thread is a subagent thread of this turn.
func (t *translator) follows(thread string) bool {
	_, ok := t.spawns[thread]
	return ok
}

// itemStarted translates an item/started notification of thread; raw is
// the whole notification line, which becomes Message.Raw.
func (t *translator) itemStarted(thread string, it item, itemJSON, raw json.RawMessage) []message.Message {
	switch {
	case it.Type == itemSubAgent:
		return t.subAgent(thread, it, raw)
	case isToolItem(it.Type) && !t.started[it.ID]:
		t.started[it.ID] = true
		return t.parented(thread, toolCall(it, itemJSON, raw))
	}
	return nil
}

// itemCompleted translates an item/completed notification of thread.
func (t *translator) itemCompleted(thread string, it item, itemJSON, raw json.RawMessage) []message.Message {
	if it.Type == itemSubAgent {
		return t.subAgent(thread, it, raw)
	}
	if isToolItem(it.Type) {
		var out []message.Message
		if !t.started[it.ID] {
			t.started[it.ID] = true
			out = append(out, toolCall(it, itemJSON, raw))
		}
		return t.parented(thread, append(out, toolResult(it, itemJSON, raw))...)
	}
	switch it.Type {
	case itemUserMessage:
		var inputs []userInput
		_ = json.Unmarshal(it.Content, &inputs)
		var texts []string
		for _, in := range inputs {
			if in.Type == "text" {
				texts = append(texts, in.Text)
			}
		}
		return t.parented(thread, message.Message{ID: it.ID, Kind: message.KindUser, User: &message.Text{Text: strings.Join(texts, "\n")}, Raw: raw})
	case itemAgentMessage:
		t.lastText[thread] = it.Text
		return t.parented(thread, message.Message{ID: it.ID, Kind: message.KindAssistant, Assistant: &message.Text{Text: it.Text}, Raw: raw})
	case itemReasoning:
		text := strings.Join(it.Summary, "\n\n")
		if text == "" {
			return nil
		}
		return t.parented(thread, message.Message{ID: it.ID, Kind: message.KindReasoning, Reasoning: &message.Text{Text: text}, Raw: raw})
	}
	return nil
}

// subAgent translates a subAgentActivity item, reported both when it starts
// and when it completes. Kind started is the subagent call, whose ID is the
// spawn call's ID; kind completed is its result, whose text is the
// subagent's last agentMessage. Other kinds are not emitted.
func (t *translator) subAgent(thread string, it item, raw json.RawMessage) []message.Message {
	if t.started[it.ID] {
		return nil
	}
	switch it.Kind {
	case subAgentStarted:
		t.started[it.ID] = true
		t.spawns[it.AgentThreadID] = it.ID
		return t.parented(thread, message.Message{
			ID:       it.ID,
			Kind:     message.KindToolCall,
			ToolCall: &message.ToolCall{ID: it.ID, Name: message.ToolNameSubagent, RawName: it.Type, Input: &message.SubagentInput{Description: it.AgentPath}},
			Raw:      raw,
		})
	case subAgentCompleted:
		call, ok := t.spawns[it.AgentThreadID]
		if !ok {
			// Started by an earlier turn: there is no call to answer.
			return nil
		}
		t.started[it.ID] = true
		return t.parented(thread, message.Message{
			ID:         it.ID,
			Kind:       message.KindToolResult,
			ToolResult: &message.ToolResult{CallID: call, Name: message.ToolNameSubagent, Output: &message.TextOutput{Text: t.lastText[it.AgentThreadID]}},
			Raw:        raw,
		})
	}
	return nil
}

// parented sets ParentToolUseID on messages of a subagent thread.
func (t *translator) parented(thread string, msgs ...message.Message) []message.Message {
	parent := t.spawns[thread]
	for i := range msgs {
		msgs[i].ParentToolUseID = parent
	}
	return msgs
}

func toolCall(it item, itemJSON, raw json.RawMessage) message.Message {
	name, input := toolInput(it, itemJSON)
	return message.Message{
		ID:       it.ID,
		Kind:     message.KindToolCall,
		ToolCall: &message.ToolCall{ID: it.ID, Name: name, RawName: it.Type, Input: input},
		Raw:      raw,
	}
}

func toolResult(it item, itemJSON, raw json.RawMessage) message.Message {
	name, output, isError := toolOutput(it, itemJSON)
	return message.Message{
		ID:         it.ID + resultIDSuffix,
		Kind:       message.KindToolResult,
		ToolResult: &message.ToolResult{CallID: it.ID, Name: name, Output: output, IsError: isError},
		Raw:        raw,
	}
}

func toolInput(it item, itemJSON json.RawMessage) (message.ToolName, message.ToolInput) {
	switch it.Type {
	case itemCommand:
		return message.ToolNameShell, &message.ShellInput{Command: unwrapShell(it.Command)}
	case itemFileChange:
		in := &message.FilePatchInput{}
		for _, c := range it.Changes {
			in.Changes = append(in.Changes, message.FileChange{Path: c.Path, Kind: c.Kind.Type, Diff: c.Diff})
		}
		// Codex keeps the changes in a map, so their order is not
		// stable; sort them so that live runs and read-back agree.
		sort.Slice(in.Changes, func(i, j int) bool { return in.Changes[i].Path < in.Changes[j].Path })
		return message.ToolNameFilePatch, in
	case itemMCPToolCall:
		return message.ToolNameMCP, &message.MCPInput{Server: it.Server, Tool: it.Tool, Arguments: it.Arguments}
	case itemWebSearch:
		return message.ToolNameWebSearch, &message.WebSearchInput{Query: it.Query}
	}
	return message.ToolNameUnknown, &message.UnknownInput{Raw: itemJSON}
}

func toolOutput(it item, itemJSON json.RawMessage) (message.ToolName, message.ToolOutput, bool) {
	failed := it.Status == "failed" || it.Status == "declined" || it.Status == "interrupted"
	switch it.Type {
	case itemCommand:
		out := &message.ShellOutput{ExitCode: it.ExitCode}
		if it.AggregatedOutput != nil {
			out.Output = *it.AggregatedOutput
		}
		return message.ToolNameShell, out, failed || (it.ExitCode != nil && *it.ExitCode != 0)
	case itemFileChange:
		return message.ToolNameFilePatch, &message.TextOutput{Text: it.Status}, failed
	case itemMCPToolCall:
		out := &message.MCPOutput{}
		if it.Result != nil {
			out.Content = it.Result.Content
		}
		return message.ToolNameMCP, out, failed || it.Error != nil
	case itemWebSearch:
		return message.ToolNameWebSearch, &message.TextOutput{Text: string(nullToEmpty(it.Results))}, failed
	}
	return message.ToolNameUnknown, &message.UnknownOutput{Raw: itemJSON}, failed
}

func nullToEmpty(raw json.RawMessage) json.RawMessage {
	if string(raw) == "null" {
		return nil
	}
	return raw
}
