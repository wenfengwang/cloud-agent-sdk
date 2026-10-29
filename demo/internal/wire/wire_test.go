package wire

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wenfengwang/cloud-agent-sdk/demo/internal/sdk"
)

var update = flag.Bool("update", false, "rewrite golden files")

var at = time.Date(2026, 10, 14, 9, 13, 10, 4_000_000, time.UTC)

func msg(id string, kind sdk.Kind) sdk.Message {
	return sdk.Message{ID: id, TurnID: "turn-1", Kind: kind, Raw: json.RawMessage(`{"harness":"original"}`)}
}

func call(id string, name sdk.ToolName, raw string, input sdk.ToolInput) sdk.Message {
	m := msg("msg-"+id, sdk.KindToolCall)
	m.ToolCall = &sdk.ToolCall{ID: id, Name: name, RawName: raw, Input: input}
	return m
}

func result(id string, name sdk.ToolName, isError bool, output sdk.ToolOutput) sdk.Message {
	m := msg("msg-result-"+id, sdk.KindToolResult)
	m.ToolResult = &sdk.ToolResult{CallID: id, Name: name, Output: output, IsError: isError}
	return m
}

func sampleMessages() []sdk.Message {
	exit := 0
	user := msg("msg-user", sdk.KindUser)
	user.User = &sdk.Text{Text: "Run the tests"}
	assistant := msg("msg-assistant", sdk.KindAssistant)
	assistant.Assistant = &sdk.Text{Text: "All tests pass."}
	reasoning := msg("msg-reasoning", sdk.KindReasoning)
	reasoning.Reasoning = &sdk.Text{}
	nested := call("call-nested", sdk.ToolNameFileRead, "Read", &sdk.FileReadInput{Path: "/repo/b.go"})
	nested.ParentToolUseID = "call-subagent"

	return []sdk.Message{
		user,
		assistant,
		reasoning,
		call("call-shell", sdk.ToolNameShell, "Bash", &sdk.ShellInput{Command: "go test ./...", Description: "Run tests", TimeoutMs: 120000}),
		result("call-shell", sdk.ToolNameShell, false, &sdk.ShellOutput{Output: "ok", ExitCode: &exit}),
		result("call-shell-noexit", sdk.ToolNameShell, true, &sdk.ShellOutput{Output: "killed"}),
		call("call-read", sdk.ToolNameFileRead, "Read", &sdk.FileReadInput{Path: "/repo/a.go", Offset: 10, Limit: 20}),
		result("call-read", sdk.ToolNameFileRead, false, &sdk.FileReadOutput{Content: "package a"}),
		call("call-write", sdk.ToolNameFileWrite, "Write", &sdk.FileWriteInput{Path: "/repo/c.go", Content: "package c"}),
		call("call-edit", sdk.ToolNameFileEdit, "Edit", &sdk.FileEditInput{Path: "/repo/a.go", OldString: "a", NewString: "b", ReplaceAll: true}),
		call("call-patch", sdk.ToolNameFilePatch, "fileChange", &sdk.FilePatchInput{Changes: []sdk.FileChange{{Path: "/repo/d.go", Kind: "add", Diff: "+package d"}}}),
		call("call-glob", sdk.ToolNameGlob, "Glob", &sdk.GlobInput{Pattern: "**/*.go", Path: "/repo"}),
		call("call-grep", sdk.ToolNameGrep, "Grep", &sdk.GrepInput{Pattern: "func main", Path: "/repo", Glob: "*.go"}),
		call("call-fetch", sdk.ToolNameWebFetch, "WebFetch", &sdk.WebFetchInput{URL: "https://example.com", Prompt: "Summarize"}),
		call("call-search", sdk.ToolNameWebSearch, "WebSearch", &sdk.WebSearchInput{Query: "go iter"}),
		call("call-mcp", sdk.ToolNameMCP, "mcp__notes__find", &sdk.MCPInput{Server: "notes", Tool: "find", Arguments: json.RawMessage(`{"q":"x"}`)}),
		result("call-mcp", sdk.ToolNameMCP, false, &sdk.MCPOutput{Content: json.RawMessage(`[{"type":"text","text":"found"}]`)}),
		call("call-subagent", sdk.ToolNameSubagent, "Task", &sdk.SubagentInput{Description: "Explore", Prompt: "Find main", Type: "explore"}),
		nested,
		result("call-subagent", sdk.ToolNameSubagent, false, &sdk.TextOutput{Text: "main is in b.go"}),
		call("call-unknown", sdk.ToolNameUnknown, "TodoWrite", &sdk.UnknownInput{Raw: json.RawMessage(`{"todos":[]}`)}),
		result("call-unknown", sdk.ToolNameUnknown, false, &sdk.UnknownOutput{Raw: json.RawMessage(`"done"`)}),
	}
}

func TestConvertGolden(t *testing.T) {
	var got []Message
	for _, m := range sampleMessages() {
		got = append(got, Converter{}.Message(m, at))
	}
	compareGolden(t, "messages.golden.json", got)
}

func TestConvertDebugRaw(t *testing.T) {
	m := sampleMessages()[0]
	got := Converter{DebugRaw: true}.Message(m, at)
	if string(got.Raw) != `{"harness":"original"}` {
		t.Fatalf("Raw = %s, want the harness JSON", got.Raw)
	}
	if got := (Converter{}).Message(m, at); got.Raw != nil {
		t.Fatalf("Raw without DebugRaw = %s, want omitted", got.Raw)
	}
}

func TestConvertTruncatesString(t *testing.T) {
	m := msg("msg-long", sdk.KindAssistant)
	m.Assistant = &sdk.Text{Text: strings.Repeat("é", 10)} // 20 bytes
	got := Converter{Limit: 5}.Message(m, at)
	if !got.Truncated {
		t.Fatal("Truncated = false, want true")
	}
	// Cut at the limit, backed off to a rune boundary.
	if *got.Text != "éé" {
		t.Fatalf("Text = %q, want %q", *got.Text, "éé")
	}
}

func TestConvertReplacesLongJSONWithPreview(t *testing.T) {
	m := call("call-mcp", sdk.ToolNameMCP, "mcp__notes__find", &sdk.MCPInput{Server: "notes", Tool: "find", Arguments: json.RawMessage(`{"query":"a long query"}`)})
	got := Converter{Limit: 8}.Message(m, at)
	if !got.Truncated {
		t.Fatal("Truncated = false, want true")
	}
	input := got.ToolCall.Input.(MCPInput)
	if string(input.Arguments) != `"{\"query\""` {
		t.Fatalf("Arguments = %s, want a JSON string preview", input.Arguments)
	}
	if string(input.Server) != "notes" {
		t.Fatalf("Server = %q, want unchanged", input.Server)
	}
}

func TestConvertShortValuesNotTruncated(t *testing.T) {
	for _, m := range sampleMessages() {
		if got := (Converter{}).Message(m, at); got.Truncated {
			t.Errorf("%s: Truncated = true, want false", m.ID)
		}
	}
}

func TestTimeFormat(t *testing.T) {
	b, err := json.Marshal(Time(time.Date(2026, 10, 14, 11, 0, 0, 0, time.FixedZone("x", 2*3600))))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `"2026-10-14T09:00:00.000Z"` {
		t.Fatalf("Time = %s", b)
	}
}

func compareGolden(t *testing.T, name string, v any) {
	t.Helper()
	got, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s mismatch (run with -update to accept)\ngot:\n%s", name, got)
	}
}
