package testenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wenfengwang/cloud-agent-sdk/internal/message"
)

// Scenario is one harness-neutral recording scenario of design 002. The
// per-harness structural expectations live with each adapter's tests.
type Scenario struct {
	Name string
	// Prompts are sent as consecutive turns of one agent.
	Prompts []string
	// Seed maps paths relative to the working directory to file contents
	// written before the first turn.
	Seed map[string]string
	// TerminateWhen, when set, terminates the turn at the first message
	// for which it returns true.
	TerminateWhen func(message.Message) bool
}

const notesFile = "alpha line\nbravo line\ncharlie line\n"

var scenarios = []Scenario{
	{
		Name:    "text",
		Prompts: []string{"Reply with exactly this sentence and nothing else: The quick brown fox jumps over the lazy dog. Do not use any tools."},
	},
	{
		Name:    "shell",
		Prompts: []string{"Run the shell command `echo cloudagent-shell-check` once, then reply with its output. Do not use any other tool."},
	},
	{
		Name:    "read-file",
		Prompts: []string{"Read the file notes.txt in the current directory and reply with its second line, verbatim."},
		Seed:    map[string]string{"notes.txt": notesFile},
	},
	{
		Name:    "write-file",
		Prompts: []string{"Create a new file named created.txt in the current directory whose entire content is the single line: hello from cloudagent"},
	},
	{
		Name:    "edit-file",
		Prompts: []string{"In the file greeting.txt in the current directory, replace the word world with cloudagent. Change nothing else."},
		Seed:    map[string]string{"greeting.txt": "Hello, world!\n"},
	},
	{
		Name:    "glob-grep",
		Prompts: []string{"First list the files that match the pattern src/*.go. Then search the src directory for the word needle and tell me which file contains it."},
		Seed: map[string]string{
			"src/alpha.go": "package src\n\nconst Alpha = 1\n",
			"src/bravo.go": "package src\n\n// needle is here.\nconst Bravo = 2\n",
			"src/notes.md": "no match here\n",
		},
	},
	{
		Name:    "subagent",
		Prompts: []string{"Delegate this task to a subagent: count the lines of the file notes.txt in the current directory. Then report the number it found."},
		Seed:    map[string]string{"notes.txt": notesFile},
	},
	{
		Name:    "terminate",
		Prompts: []string{"Run the shell command `sleep 30`, then reply with done."},
		TerminateWhen: func(m message.Message) bool {
			if m.ToolCall == nil {
				return false
			}
			sh, ok := m.ToolCall.Input.(*message.ShellInput)
			return ok && strings.Contains(sh.Command, "sleep 30")
		},
	},
	{
		Name: "two-turn",
		Prompts: []string{
			"Remember the code word marmalade. Reply only with OK.",
			"What code word did I ask you to remember? Reply with the word only.",
		},
	},
}

// Scenarios returns every scenario, in the order of design 002.
func Scenarios() []Scenario { return scenarios }

// ScenarioByName returns the scenario with the given name.
func ScenarioByName(name string) (Scenario, bool) {
	for _, s := range scenarios {
		if s.Name == name {
			return s, true
		}
	}
	return Scenario{}, false
}

// SeedWorkDir writes the scenario's seed files into dir.
func SeedWorkDir(t testing.TB, dir string, s Scenario) {
	t.Helper()
	for name, content := range s.Seed {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
