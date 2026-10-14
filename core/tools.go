package core

import "github.com/wenfengwang/cloud-agent-sdk/core/internal/message"

// ToolName is the canonical name of a tool. Each ToolName has one input type
// and one output type.
type ToolName = message.ToolName

// The canonical tool names, with their input and output types.
const (
	ToolNameShell     ToolName = message.ToolNameShell     // ShellInput, ShellOutput
	ToolNameFileRead  ToolName = message.ToolNameFileRead  // FileReadInput, FileReadOutput
	ToolNameFileWrite ToolName = message.ToolNameFileWrite // FileWriteInput, TextOutput
	ToolNameFileEdit  ToolName = message.ToolNameFileEdit  // FileEditInput, TextOutput
	ToolNameFilePatch ToolName = message.ToolNameFilePatch // FilePatchInput, TextOutput
	ToolNameGlob      ToolName = message.ToolNameGlob      // GlobInput, TextOutput
	ToolNameGrep      ToolName = message.ToolNameGrep      // GrepInput, TextOutput
	ToolNameWebFetch  ToolName = message.ToolNameWebFetch  // WebFetchInput, TextOutput
	ToolNameWebSearch ToolName = message.ToolNameWebSearch // WebSearchInput, TextOutput
	ToolNameMCP       ToolName = message.ToolNameMCP       // MCPInput, MCPOutput
	ToolNameSubagent  ToolName = message.ToolNameSubagent  // SubagentInput, TextOutput
	ToolNameUnknown   ToolName = message.ToolNameUnknown   // UnknownInput, UnknownOutput
)

// ToolInput is the typed input of a tool call. It is sealed; use a type
// switch over the *...Input types of this package.
type ToolInput = message.ToolInput

// ToolOutput is the typed output of a tool result. It is sealed; use a type
// switch over the *...Output types of this package.
type ToolOutput = message.ToolOutput

// ShellInput is the input of a shell call: Command (with any login-shell
// wrapper removed), Description, and TimeoutMs.
type ShellInput = message.ShellInput

// FileReadInput is the input of a file_read call: Path, Offset, and Limit.
type FileReadInput = message.FileReadInput

// FileWriteInput is the input of a file_write call: Path and Content.
type FileWriteInput = message.FileWriteInput

// FileEditInput is the input of a file_edit call: Path, OldString,
// NewString, and ReplaceAll.
type FileEditInput = message.FileEditInput

// FilePatchInput is the input of a file_patch call: the list of Changes.
type FilePatchInput = message.FilePatchInput

// FileChange is one file of a file_patch call: Path, Kind ("add", "delete",
// or "update"), and Diff.
type FileChange = message.FileChange

// GlobInput is the input of a glob call: Pattern and Path.
type GlobInput = message.GlobInput

// GrepInput is the input of a grep call: Pattern, Path, and Glob.
type GrepInput = message.GrepInput

// WebFetchInput is the input of a web_fetch call: URL and Prompt.
type WebFetchInput = message.WebFetchInput

// WebSearchInput is the input of a web_search call: Query.
type WebSearchInput = message.WebSearchInput

// MCPInput is the input of an MCP call: Server, Tool, and the JSON
// Arguments.
type MCPInput = message.MCPInput

// SubagentInput is the input of a subagent call: Description, Prompt, and
// Type.
type SubagentInput = message.SubagentInput

// UnknownInput is the input of a tool without a canonical mapping: the
// harness's Raw input JSON.
type UnknownInput = message.UnknownInput

// ShellOutput is the output of a shell call: Output and ExitCode, which is
// nil when the harness does not report one.
type ShellOutput = message.ShellOutput

// FileReadOutput is the output of a file_read call: Content.
type FileReadOutput = message.FileReadOutput

// TextOutput is the text output of a tool call: Text.
type TextOutput = message.TextOutput

// MCPOutput is the output of an MCP call: the JSON Content the tool
// returned.
type MCPOutput = message.MCPOutput

// UnknownOutput is the output of a tool without a canonical mapping: the
// harness's Raw output JSON.
type UnknownOutput = message.UnknownOutput
