package cloudagent_test

import (
	"encoding/json"
	"testing"

	cloudagent "github.com/wenfengwang/cloud-agent-sdk"
)

func TestKindValues(t *testing.T) {
	want := map[cloudagent.Kind]string{
		cloudagent.KindUser:       "user",
		cloudagent.KindAssistant:  "assistant",
		cloudagent.KindReasoning:  "reasoning",
		cloudagent.KindToolCall:   "tool_call",
		cloudagent.KindToolResult: "tool_result",
	}
	for k, v := range want {
		if string(k) != v {
			t.Errorf("kind %q, want %q", k, v)
		}
	}
}

func TestToolNameValues(t *testing.T) {
	want := map[cloudagent.ToolName]string{
		cloudagent.ToolNameShell:     "shell",
		cloudagent.ToolNameFileRead:  "file_read",
		cloudagent.ToolNameFileWrite: "file_write",
		cloudagent.ToolNameFileEdit:  "file_edit",
		cloudagent.ToolNameFilePatch: "file_patch",
		cloudagent.ToolNameGlob:      "glob",
		cloudagent.ToolNameGrep:      "grep",
		cloudagent.ToolNameWebFetch:  "web_fetch",
		cloudagent.ToolNameWebSearch: "web_search",
		cloudagent.ToolNameMCP:       "mcp",
		cloudagent.ToolNameSubagent:  "subagent",
		cloudagent.ToolNameTodo:      "todo",
		cloudagent.ToolNameUnknown:   "unknown",
	}
	if len(want) != 13 {
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
func inputName(in cloudagent.ToolInput) cloudagent.ToolName {
	switch in.(type) {
	case *cloudagent.ShellInput:
		return cloudagent.ToolNameShell
	case *cloudagent.FileReadInput:
		return cloudagent.ToolNameFileRead
	case *cloudagent.FileWriteInput:
		return cloudagent.ToolNameFileWrite
	case *cloudagent.FileEditInput:
		return cloudagent.ToolNameFileEdit
	case *cloudagent.FilePatchInput:
		return cloudagent.ToolNameFilePatch
	case *cloudagent.GlobInput:
		return cloudagent.ToolNameGlob
	case *cloudagent.GrepInput:
		return cloudagent.ToolNameGrep
	case *cloudagent.WebFetchInput:
		return cloudagent.ToolNameWebFetch
	case *cloudagent.WebSearchInput:
		return cloudagent.ToolNameWebSearch
	case *cloudagent.MCPInput:
		return cloudagent.ToolNameMCP
	case *cloudagent.SubagentInput:
		return cloudagent.ToolNameSubagent
	case *cloudagent.TodoInput:
		return cloudagent.ToolNameTodo
	case *cloudagent.UnknownInput:
		return cloudagent.ToolNameUnknown
	}
	return ""
}

func outputName(out cloudagent.ToolOutput) string {
	switch out.(type) {
	case *cloudagent.ShellOutput:
		return "shell"
	case *cloudagent.FileReadOutput:
		return "file_read"
	case *cloudagent.TextOutput:
		return "text"
	case *cloudagent.MCPOutput:
		return "mcp"
	case *cloudagent.UnknownOutput:
		return "unknown"
	}
	return ""
}

func TestToolInputTypes(t *testing.T) {
	exitCode := 0
	cases := map[cloudagent.ToolName]cloudagent.ToolInput{
		cloudagent.ToolNameShell:     &cloudagent.ShellInput{Command: "ls", Description: "list", TimeoutMs: 1000},
		cloudagent.ToolNameFileRead:  &cloudagent.FileReadInput{Path: "a", Offset: 1, Limit: 2},
		cloudagent.ToolNameFileWrite: &cloudagent.FileWriteInput{Path: "a", Content: "b"},
		cloudagent.ToolNameFileEdit:  &cloudagent.FileEditInput{Path: "a", OldString: "b", NewString: "c", ReplaceAll: true},
		cloudagent.ToolNameFilePatch: &cloudagent.FilePatchInput{Changes: []cloudagent.FileChange{{Path: "a", Kind: "add", Diff: "+b"}}},
		cloudagent.ToolNameGlob:      &cloudagent.GlobInput{Pattern: "*.go", Path: "."},
		cloudagent.ToolNameGrep:      &cloudagent.GrepInput{Pattern: "x", Path: ".", Glob: "*.go"},
		cloudagent.ToolNameWebFetch:  &cloudagent.WebFetchInput{URL: "https://example.com", Prompt: "p"},
		cloudagent.ToolNameWebSearch: &cloudagent.WebSearchInput{Query: "q"},
		cloudagent.ToolNameMCP:       &cloudagent.MCPInput{Server: "s", Tool: "t", Arguments: json.RawMessage(`{}`)},
		cloudagent.ToolNameSubagent:  &cloudagent.SubagentInput{Description: "d", Prompt: "p", Type: "general"},
		cloudagent.ToolNameTodo:      &cloudagent.TodoInput{Items: []cloudagent.TodoItem{{Text: "a", Status: "pending"}}},
		cloudagent.ToolNameUnknown:   &cloudagent.UnknownInput{Raw: json.RawMessage(`{}`)},
	}
	for name, in := range cases {
		if got := inputName(in); got != name {
			t.Errorf("input for %q switched to %q", name, got)
		}
	}

	outputs := map[string]cloudagent.ToolOutput{
		"shell":     &cloudagent.ShellOutput{Output: "ok", ExitCode: &exitCode},
		"file_read": &cloudagent.FileReadOutput{Content: "c"},
		"text":      &cloudagent.TextOutput{Text: "t"},
		"mcp":       &cloudagent.MCPOutput{Content: json.RawMessage(`[]`)},
		"unknown":   &cloudagent.UnknownOutput{Raw: json.RawMessage(`{}`)},
	}
	for name, out := range outputs {
		if got := outputName(out); got != name {
			t.Errorf("output %q switched to %q", name, got)
		}
	}
}

func TestMessageShape(t *testing.T) {
	m := cloudagent.Message{
		ID:              "m1",
		TurnID:          "t1",
		ParentToolUseID: "p1",
		Kind:            cloudagent.KindToolCall,
		ToolCall: &cloudagent.ToolCall{
			ID:      "c1",
			Name:    cloudagent.ToolNameShell,
			RawName: "Bash",
			Input:   &cloudagent.ShellInput{Command: "ls"},
		},
		Raw: json.RawMessage(`{}`),
	}
	if m.User != nil || m.Assistant != nil || m.Reasoning != nil || m.ToolResult != nil {
		t.Fatal("unexpected payload")
	}
	r := cloudagent.ToolResult{CallID: "c1", Name: cloudagent.ToolNameShell, Output: &cloudagent.TextOutput{}, IsError: true}
	if r.CallID != m.ToolCall.ID {
		t.Fatal("call ID mismatch")
	}
	_ = cloudagent.Text{Text: "x"}
}
