package core_test

import (
	"encoding/json"
	"testing"

	"github.com/wenfengwang/cloud-agent-sdk/core"
)

func TestKindValues(t *testing.T) {
	want := map[core.Kind]string{
		core.KindUser:       "user",
		core.KindAssistant:  "assistant",
		core.KindReasoning:  "reasoning",
		core.KindToolCall:   "tool_call",
		core.KindToolResult: "tool_result",
	}
	for k, v := range want {
		if string(k) != v {
			t.Errorf("kind %q, want %q", k, v)
		}
	}
}

func TestToolNameValues(t *testing.T) {
	want := map[core.ToolName]string{
		core.ToolNameShell:     "shell",
		core.ToolNameFileRead:  "file_read",
		core.ToolNameFileWrite: "file_write",
		core.ToolNameFileEdit:  "file_edit",
		core.ToolNameFilePatch: "file_patch",
		core.ToolNameGlob:      "glob",
		core.ToolNameGrep:      "grep",
		core.ToolNameWebFetch:  "web_fetch",
		core.ToolNameWebSearch: "web_search",
		core.ToolNameMCP:       "mcp",
		core.ToolNameSubagent:  "subagent",
		core.ToolNameUnknown:   "unknown",
	}
	if len(want) != 12 {
		t.Fatalf("tool names are not distinct: %d", len(want))
	}
	for k, v := range want {
		if string(k) != v {
			t.Errorf("tool name %q, want %q", k, v)
		}
	}
}

// inputName and outputName exercise the type switches consumers write; a
// missing or renamed type fails to compile.
func inputName(in core.ToolInput) core.ToolName {
	switch in.(type) {
	case *core.ShellInput:
		return core.ToolNameShell
	case *core.FileReadInput:
		return core.ToolNameFileRead
	case *core.FileWriteInput:
		return core.ToolNameFileWrite
	case *core.FileEditInput:
		return core.ToolNameFileEdit
	case *core.FilePatchInput:
		return core.ToolNameFilePatch
	case *core.GlobInput:
		return core.ToolNameGlob
	case *core.GrepInput:
		return core.ToolNameGrep
	case *core.WebFetchInput:
		return core.ToolNameWebFetch
	case *core.WebSearchInput:
		return core.ToolNameWebSearch
	case *core.MCPInput:
		return core.ToolNameMCP
	case *core.SubagentInput:
		return core.ToolNameSubagent
	case *core.UnknownInput:
		return core.ToolNameUnknown
	}
	return ""
}

func outputName(out core.ToolOutput) string {
	switch out.(type) {
	case *core.ShellOutput:
		return "shell"
	case *core.FileReadOutput:
		return "file_read"
	case *core.TextOutput:
		return "text"
	case *core.MCPOutput:
		return "mcp"
	case *core.UnknownOutput:
		return "unknown"
	}
	return ""
}

func TestToolInputTypes(t *testing.T) {
	exitCode := 0
	cases := map[core.ToolName]core.ToolInput{
		core.ToolNameShell:     &core.ShellInput{Command: "ls", Description: "list", TimeoutMs: 1000},
		core.ToolNameFileRead:  &core.FileReadInput{Path: "a", Offset: 1, Limit: 2},
		core.ToolNameFileWrite: &core.FileWriteInput{Path: "a", Content: "b"},
		core.ToolNameFileEdit:  &core.FileEditInput{Path: "a", OldString: "b", NewString: "c", ReplaceAll: true},
		core.ToolNameFilePatch: &core.FilePatchInput{Changes: []core.FileChange{{Path: "a", Kind: "add", Diff: "+b"}}},
		core.ToolNameGlob:      &core.GlobInput{Pattern: "*.go", Path: "."},
		core.ToolNameGrep:      &core.GrepInput{Pattern: "x", Path: ".", Glob: "*.go"},
		core.ToolNameWebFetch:  &core.WebFetchInput{URL: "https://example.com", Prompt: "p"},
		core.ToolNameWebSearch: &core.WebSearchInput{Query: "q"},
		core.ToolNameMCP:       &core.MCPInput{Server: "s", Tool: "t", Arguments: json.RawMessage(`{}`)},
		core.ToolNameSubagent:  &core.SubagentInput{Description: "d", Prompt: "p", Type: "general"},
		core.ToolNameUnknown:   &core.UnknownInput{Raw: json.RawMessage(`{}`)},
	}
	for name, in := range cases {
		if got := inputName(in); got != name {
			t.Errorf("input for %q switched to %q", name, got)
		}
	}

	outputs := map[string]core.ToolOutput{
		"shell":     &core.ShellOutput{Output: "ok", ExitCode: &exitCode},
		"file_read": &core.FileReadOutput{Content: "c"},
		"text":      &core.TextOutput{Text: "t"},
		"mcp":       &core.MCPOutput{Content: json.RawMessage(`[]`)},
		"unknown":   &core.UnknownOutput{Raw: json.RawMessage(`{}`)},
	}
	for name, out := range outputs {
		if got := outputName(out); got != name {
			t.Errorf("output %q switched to %q", name, got)
		}
	}
}

func TestMessageShape(t *testing.T) {
	m := core.Message{
		ID:              "m1",
		TurnID:          "t1",
		ParentToolUseID: "p1",
		Kind:            core.KindToolCall,
		ToolCall: &core.ToolCall{
			ID:      "c1",
			Name:    core.ToolNameShell,
			RawName: "Bash",
			Input:   &core.ShellInput{Command: "ls"},
		},
		Raw: json.RawMessage(`{}`),
	}
	if m.User != nil || m.Assistant != nil || m.Reasoning != nil || m.ToolResult != nil {
		t.Fatal("unexpected payload")
	}
	r := core.ToolResult{CallID: "c1", Name: core.ToolNameShell, Output: &core.TextOutput{}, IsError: true}
	if r.CallID != m.ToolCall.ID {
		t.Fatal("call ID mismatch")
	}
	_ = core.Text{Text: "x"}
}
