package core_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wenfengwang/cloud-agent-sdk/core"
)

// e2eCheck checks one scenario's structural expectations of design 002
// against the live messages of each of its turns. Model wording varies, so
// the checks name tool rows and facts the scenario must produce, not exact
// sequences.
type e2eCheck func(t *testing.T, e *replayEnv, turns [][]core.Message)

// e2eExpectations are the per-harness structural expectations of design 002.
var e2eExpectations = map[core.Harness]map[string]e2eCheck{
	core.ClaudeCode: {
		"text":  expectText,
		"shell": expectShell,
		"read-file": func(t *testing.T, e *replayEnv, turns [][]core.Message) {
			call, res := requireToolCall(t, turns[0], core.ToolNameFileRead)
			if out, ok := res.Output.(*core.FileReadOutput); !ok || !strings.Contains(out.Content, "bravo line") {
				t.Errorf("file_read of %+v returned %+v", call.Input, res.Output)
			}
			requireFinalTextContains(t, turns[0], "bravo line")
		},
		"write-file": func(t *testing.T, e *replayEnv, turns [][]core.Message) {
			requireToolCall(t, turns[0], core.ToolNameFileWrite)
			requireFileContent(t, e, "created.txt", "hello from cloudagent")
		},
		"edit-file": func(t *testing.T, e *replayEnv, turns [][]core.Message) {
			requireToolCall(t, turns[0], core.ToolNameFileEdit)
			requireFileContent(t, e, "greeting.txt", "Hello, cloudagent!")
		},
		"glob-grep": expectGlobGrep,
		"subagent": func(t *testing.T, e *replayEnv, turns [][]core.Message) {
			expectSubagent(t, turns[0])
		},
		"terminate": expectTerminate,
		"two-turn":  expectTwoTurn,
	},
	core.Codex: {
		"text":  expectText,
		"shell": expectShell,
		"read-file": func(t *testing.T, e *replayEnv, turns [][]core.Message) {
			in, out := requireShellCall(t, turns[0], func(cmd string) bool { return strings.Contains(cmd, "notes.txt") })
			if out == nil || !strings.Contains(out.Output, "bravo line") {
				t.Errorf("shell read %q returned %+v", in.Command, out)
			}
			requireFinalTextContains(t, turns[0], "bravo line")
		},
		"write-file": func(t *testing.T, e *replayEnv, turns [][]core.Message) {
			requirePatch(t, turns[0], "created.txt", "add")
			requireFileContent(t, e, "created.txt", "hello from cloudagent")
		},
		"edit-file": func(t *testing.T, e *replayEnv, turns [][]core.Message) {
			requirePatch(t, turns[0], "greeting.txt", "update")
			requireFileContent(t, e, "greeting.txt", "Hello, cloudagent!")
		},
		"glob-grep": expectGlobGrep,
		"subagent": func(t *testing.T, e *replayEnv, turns [][]core.Message) {
			call := expectSubagent(t, turns[0])
			if call.RawName != "subAgentActivity" {
				t.Errorf("subagent call RawName %q", call.RawName)
			}
			wait := false
			for _, m := range turns[0] {
				if c := m.ToolCall; c != nil && c.Name == core.ToolNameUnknown && c.RawName == "collabAgentToolCall" {
					wait = true
				}
			}
			if !wait {
				t.Errorf("the wait step is no unknown call: %s", kindList(turns[0]))
			}
		},
		"terminate": expectTerminate,
		"two-turn":  expectTwoTurn,
	},
}

func expectText(t *testing.T, e *replayEnv, turns [][]core.Message) {
	for _, m := range turns[0] {
		if m.ToolCall != nil {
			t.Errorf("the text scenario called a tool: %s", kindList(turns[0]))
			break
		}
	}
	requireFinalTextContains(t, turns[0], "quick brown fox")
}

func expectShell(t *testing.T, e *replayEnv, turns [][]core.Message) {
	in, out := requireShellCall(t, turns[0], func(cmd string) bool { return strings.Contains(cmd, "echo cloudagent-shell-check") })
	if out == nil || out.ExitCode == nil || *out.ExitCode != 0 || !strings.Contains(out.Output, "cloudagent-shell-check") {
		t.Errorf("shell %q returned %+v", in.Command, out)
	}
}

func expectGlobGrep(t *testing.T, e *replayEnv, turns [][]core.Message) {
	// Neither pinned harness offers glob or grep by default: both search
	// with shell commands.
	requireShellCall(t, turns[0], func(string) bool { return true })
	requireFinalTextContains(t, turns[0], "bravo.go")
}

func expectSubagent(t *testing.T, msgs []core.Message) *core.ToolCall {
	call, _ := requireToolCall(t, msgs, core.ToolNameSubagent)
	inside := 0
	for _, m := range msgs {
		if m.ParentToolUseID == call.ID {
			inside++
		}
	}
	if inside == 0 {
		t.Errorf("no message carries the subagent call as ParentToolUseID: %s", kindList(msgs))
	}
	requireFinalTextContains(t, msgs, "3")
	return call
}

func expectTerminate(t *testing.T, e *replayEnv, turns [][]core.Message) {
	requireShellCall(t, turns[0], func(cmd string) bool { return strings.Contains(cmd, "sleep 30") })
}

func expectTwoTurn(t *testing.T, e *replayEnv, turns [][]core.Message) {
	if len(turns) != 2 {
		t.Fatalf("%d turns, want 2", len(turns))
	}
	requireFinalTextContains(t, turns[1], "marmalade")
}

// requireToolCall finds the first call of name and its result, which must
// not be an error.
func requireToolCall(t *testing.T, msgs []core.Message, name core.ToolName) (*core.ToolCall, *core.ToolResult) {
	t.Helper()
	for _, m := range msgs {
		if m.ToolCall == nil || m.ToolCall.Name != name {
			continue
		}
		for _, r := range msgs {
			if res := r.ToolResult; res != nil && res.CallID == m.ToolCall.ID {
				if res.IsError {
					t.Errorf("%s call failed: %+v", name, res.Output)
				}
				return m.ToolCall, res
			}
		}
		t.Fatalf("%s call %s has no result: %s", name, m.ToolCall.ID, kindList(msgs))
	}
	t.Fatalf("no %s call: %s", name, kindList(msgs))
	return nil, nil
}

// requireShellCall finds the first shell call whose command matches and
// returns its input and output (nil without a result). Every shell command
// must have its login-shell wrapper removed.
func requireShellCall(t *testing.T, msgs []core.Message, match func(string) bool) (*core.ShellInput, *core.ShellOutput) {
	t.Helper()
	for _, m := range msgs {
		if !isShellCall(m) {
			continue
		}
		in := m.ToolCall.Input.(*core.ShellInput)
		if strings.Contains(in.Command, " -lc ") {
			t.Errorf("shell command %q keeps its wrapper", in.Command)
		}
		if !match(in.Command) {
			continue
		}
		for _, r := range msgs {
			if res := r.ToolResult; res != nil && res.CallID == m.ToolCall.ID {
				return in, res.Output.(*core.ShellOutput)
			}
		}
		return in, nil
	}
	t.Fatalf("no matching shell call: %s", kindList(msgs))
	return nil, nil
}

// requirePatch requires a successful file_patch with a change of kind on a
// path ending in name.
func requirePatch(t *testing.T, msgs []core.Message, name, kind string) {
	t.Helper()
	for _, m := range msgs {
		c := m.ToolCall
		if c == nil || c.Name != core.ToolNameFilePatch {
			continue
		}
		for _, ch := range c.Input.(*core.FilePatchInput).Changes {
			if strings.HasSuffix(ch.Path, name) && ch.Kind == kind {
				requireToolCall(t, msgs, core.ToolNameFilePatch)
				return
			}
		}
	}
	t.Fatalf("no file_patch %s of %s: %s", kind, name, kindList(msgs))
}

func requireFinalTextContains(t *testing.T, msgs []core.Message, want string) {
	t.Helper()
	final := ""
	for _, m := range msgs {
		if m.Assistant != nil && m.ParentToolUseID == "" {
			final = m.Assistant.Text
		}
	}
	if !strings.Contains(strings.ToLower(final), strings.ToLower(want)) {
		t.Errorf("final text %q does not contain %q", final, want)
	}
}

// requireFileContent checks that the work directory's file holds want.
func requireFileContent(t *testing.T, e *replayEnv, name, want string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(e.work, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if strings.TrimSpace(string(data)) != want {
		t.Errorf("%s = %q, want %q", name, data, want)
	}
}
