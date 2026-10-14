package cloudagent_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	cloudagent "github.com/wenfengwang/cloud-agent-sdk"
)

// minContentLen is the shortest message string the index test looks for;
// shorter strings (exit codes, change kinds) match by coincidence.
const minContentLen = 8

// TestIndexHasNoContent replays every completed scenario and checks that
// ~/.cloudagent holds exactly the fields of design 001 and none of the run's
// prompts, message text, tool inputs, or tool outputs.
func TestIndexHasNoContent(t *testing.T) {
	for _, h := range harnesses {
		for _, scenario := range completedScenarios() {
			t.Run(string(h)+"/"+scenario, func(t *testing.T) {
				e := newReplay(t, h, scenario, nil)
				run := runScenario(t, e)
				if err := run.agent.Close(); err != nil {
					t.Fatalf("Close: %v", err)
				}
				requireIndexFields(t, e, run)
				requireNoIndexContent(t, e, e.rec.Meta.Prompts, run.live)
			})
		}
	}
}

// requireNoIndexContent checks that no file under the isolated ~/.cloudagent
// contains a prompt or any message text, tool input, or tool output of live.
func requireNoIndexContent(t *testing.T, e *replayEnv, prompts []string, live [][]cloudagent.Message) {
	t.Helper()
	// Paths and status words the index legitimately records.
	allowed := map[string]bool{e.work: true, e.harnessHome: true, e.home: true}
	for _, status := range []string{"running", "completed", "terminated", "cancelled", "closed", "failed"} {
		allowed[status] = true
	}
	var content []string
	add := func(s string) {
		if len(s) >= minContentLen && !allowed[s] {
			content = append(content, s)
		}
	}
	for _, p := range prompts {
		add(p)
	}
	for _, msgs := range live {
		for _, m := range msgs {
			for _, s := range messageContent(t, m) {
				add(s)
			}
		}
	}
	if len(content) == 0 {
		t.Fatal("the run produced no content to look for")
	}

	root := filepath.Join(e.home, ".cloudagent")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, s := range content {
			if strings.Contains(string(data), s) || strings.Contains(string(data), jsonEscaped(s)) {
				t.Errorf("%s contains message content %q", path, s)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// requireIndexFields checks that agent.json and turns.jsonl hold exactly the
// fields of design 001, with the values of this run.
func requireIndexFields(t *testing.T, e *replayEnv, run scenarioRun) {
	t.Helper()
	dir := e.indexDir(run.agent)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if !slices.Equal(names, []string{"agent.json", "turns.jsonl"}) {
		t.Errorf("index files %v, want agent.json and turns.jsonl", names)
	}

	agent := readJSONObject(t, filepath.Join(dir, "agent.json"))
	requireKeys(t, "agent.json", agent, "harness", "model", "workdir", "home", "harness_session_id")
	want := map[string]any{"harness": string(e.harness), "model": "", "workdir": e.work, "home": e.harnessHome}
	for k, v := range want {
		if agent[k] != v {
			t.Errorf("agent.json %s = %v, want %v", k, agent[k], v)
		}
	}
	if agent["harness_session_id"] == "" {
		t.Error("agent.json has no harness_session_id")
	}

	data, err := os.ReadFile(filepath.Join(dir, "turns.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != len(run.turns) {
		t.Fatalf("turns.jsonl has %d lines, want %d", len(lines), len(run.turns))
	}
	refKeys := map[cloudagent.Harness][]string{
		cloudagent.Claude: {"session_id", "user_message_uuid"},
		cloudagent.OpenAI: {"thread_id", "turn_id"},
	}[e.harness]
	for i, line := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("turns.jsonl line %d: %v", i+1, err)
		}
		requireKeys(t, "turns.jsonl", rec, "turn_id", "harness_ref", "status", "started_at", "ended_at")
		if rec["turn_id"] != run.turns[i].ID() || rec["status"] != "completed" || rec["ended_at"] == nil {
			t.Errorf("turns.jsonl line %d: %s", i+1, line)
		}
		ref, _ := rec["harness_ref"].(map[string]any)
		requireKeys(t, "harness_ref", ref, refKeys...)
	}
}

func readJSONObject(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return obj
}

func requireKeys(t *testing.T, what string, obj map[string]any, keys ...string) {
	t.Helper()
	var got []string
	for k := range obj {
		got = append(got, k)
	}
	slices.Sort(got)
	want := slices.Clone(keys)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("%s has fields %v, want %v", what, got, want)
	}
}

// messageContent returns the content strings of m: its text, and every
// string in its tool input or output except harness JSON (Raw).
func messageContent(t *testing.T, m cloudagent.Message) []string {
	t.Helper()
	var out []string
	for _, text := range []*cloudagent.Text{m.User, m.Assistant, m.Reasoning} {
		if text != nil {
			out = append(out, text.Text)
		}
	}
	var payload any
	switch {
	case m.ToolCall != nil:
		payload = m.ToolCall.Input
	case m.ToolResult != nil:
		payload = m.ToolResult.Output
	default:
		return out
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return append(out, leafStrings(v)...)
}

// leafStrings returns the string values in v, skipping Raw fields.
func leafStrings(v any) []string {
	switch v := v.(type) {
	case string:
		return []string{v}
	case []any:
		var out []string
		for _, e := range v {
			out = append(out, leafStrings(e)...)
		}
		return out
	case map[string]any:
		var out []string
		for k, e := range v {
			if k != "Raw" {
				out = append(out, leafStrings(e)...)
			}
		}
		return out
	}
	return nil
}

// jsonEscaped is s as it appears inside a JSON string.
func jsonEscaped(s string) string {
	data, _ := json.Marshal(s)
	return string(data[1 : len(data)-1])
}
