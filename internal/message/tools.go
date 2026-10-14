package message

import "encoding/json"

// ToolName is the canonical name of a tool. Each ToolName has one input type
// and one output type.
type ToolName string

// The canonical tool names.
const (
	ToolNameShell     ToolName = "shell"
	ToolNameFileRead  ToolName = "file_read"
	ToolNameFileWrite ToolName = "file_write"
	ToolNameFileEdit  ToolName = "file_edit"
	ToolNameFilePatch ToolName = "file_patch"
	ToolNameGlob      ToolName = "glob"
	ToolNameGrep      ToolName = "grep"
	ToolNameWebFetch  ToolName = "web_fetch"
	ToolNameWebSearch ToolName = "web_search"
	ToolNameMCP       ToolName = "mcp"
	ToolNameSubagent  ToolName = "subagent"
	ToolNameTodo      ToolName = "todo"
	ToolNameUnknown   ToolName = "unknown"
)

// ToolInput is the typed input of a tool call. It is sealed: the
// implementations are the *...Input types of this package.
type ToolInput interface{ isToolInput() }

// ToolOutput is the typed output of a tool result. It is sealed: the
// implementations are the *...Output types of this package.
type ToolOutput interface{ isToolOutput() }

// ShellInput is the input of a shell tool call. Command has any login-shell
// wrapper removed.
type ShellInput struct {
	Command     string
	Description string
	TimeoutMs   int64
}

// FileReadInput is the input of a file_read tool call.
type FileReadInput struct {
	Path   string
	Offset int
	Limit  int
}

// FileWriteInput is the input of a file_write tool call.
type FileWriteInput struct {
	Path    string
	Content string
}

// FileEditInput is the input of a file_edit tool call.
type FileEditInput struct {
	Path       string
	OldString  string
	NewString  string
	ReplaceAll bool
}

// FilePatchInput is the input of a file_patch tool call.
type FilePatchInput struct {
	Changes []FileChange
}

// FileChange is one file of a file_patch call. Kind is "add", "delete", or
// "update".
type FileChange struct {
	Path string
	Kind string
	Diff string
}

// GlobInput is the input of a glob tool call.
type GlobInput struct {
	Pattern string
	Path    string
}

// GrepInput is the input of a grep tool call.
type GrepInput struct {
	Pattern string
	Path    string
	Glob    string
}

// WebFetchInput is the input of a web_fetch tool call.
type WebFetchInput struct {
	URL    string
	Prompt string
}

// WebSearchInput is the input of a web_search tool call.
type WebSearchInput struct {
	Query string
}

// MCPInput is the input of an MCP tool call. Arguments is the JSON the
// harness passed to the tool.
type MCPInput struct {
	Server    string
	Tool      string
	Arguments json.RawMessage
}

// SubagentInput is the input of a subagent tool call.
type SubagentInput struct {
	Description string
	Prompt      string
	Type        string
}

// TodoInput is the input of a todo tool call.
type TodoInput struct {
	Items []TodoItem
}

// TodoItem is one entry of a todo list. Status is the harness's status
// string, for example "pending", "in_progress", or "completed".
type TodoItem struct {
	Text   string
	Status string
}

// UnknownInput is the input of a tool call that has no canonical mapping.
// Raw is the harness's input JSON.
type UnknownInput struct {
	Raw json.RawMessage
}

// ShellOutput is the output of a shell tool call. ExitCode is nil when the
// harness does not report one.
type ShellOutput struct {
	Output   string
	ExitCode *int
}

// FileReadOutput is the output of a file_read tool call.
type FileReadOutput struct {
	Content string
}

// TextOutput is the text output of a tool call.
type TextOutput struct {
	Text string
}

// MCPOutput is the output of an MCP tool call. Content is the JSON content
// the tool returned.
type MCPOutput struct {
	Content json.RawMessage
}

// UnknownOutput is the output of a tool call that has no canonical mapping.
// Raw is the harness's output JSON.
type UnknownOutput struct {
	Raw json.RawMessage
}

func (*ShellInput) isToolInput()     {}
func (*FileReadInput) isToolInput()  {}
func (*FileWriteInput) isToolInput() {}
func (*FileEditInput) isToolInput()  {}
func (*FilePatchInput) isToolInput() {}
func (*GlobInput) isToolInput()      {}
func (*GrepInput) isToolInput()      {}
func (*WebFetchInput) isToolInput()  {}
func (*WebSearchInput) isToolInput() {}
func (*MCPInput) isToolInput()       {}
func (*SubagentInput) isToolInput()  {}
func (*TodoInput) isToolInput()      {}
func (*UnknownInput) isToolInput()   {}

func (*ShellOutput) isToolOutput()    {}
func (*FileReadOutput) isToolOutput() {}
func (*TextOutput) isToolOutput()     {}
func (*MCPOutput) isToolOutput()      {}
func (*UnknownOutput) isToolOutput()  {}
