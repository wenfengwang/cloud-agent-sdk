package testenv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/wenfengwang/cloud-agent-sdk/internal/fakeharness"
)

func TestIsolate(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/developer/claude")
	t.Setenv("CODEX_HOME", "/developer/codex")
	home := Isolate(t)
	if info, err := os.Stat(home); err != nil || !info.IsDir() || os.Getenv("HOME") != home {
		t.Fatalf("HOME = %q, home = %q, %v", os.Getenv("HOME"), home, err)
	}
	for _, key := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME"} {
		if _, ok := os.LookupEnv(key); ok {
			t.Fatalf("%s is still set", key)
		}
	}
}

func TestParseEnvFile(t *testing.T) {
	data := "# comment\n\nexport A=1\nB = \"two words\"\nC='three'\nD=x=y\n"
	got, err := parseEnvFile([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	want := []envVar{{"A", "1"}, {"B", "two words"}, {"C", "three"}, {"D", "x=y"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseEnvFile = %v, want %v", got, want)
	}
	if _, err := parseEnvFile([]byte("no equals sign\n")); err == nil {
		t.Fatal("parseEnvFile accepted a malformed line")
	} else if strings.Contains(err.Error(), "no equals sign") {
		t.Fatal("the error repeats the line, which may hold a credential")
	}
}

func TestSecretValues(t *testing.T) {
	vars := []envVar{
		{"ANTHROPIC_BASE_URL", "https://proxy.example.test"},
		{"ANTHROPIC_AUTH_TOKEN", "token-value-1"},
		{"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "1"},
		{"ANTHROPIC_MODEL", "some-model"},
		{"OPENAI_API_KEY", "key-value-2"},
	}
	got := secretValues(vars)
	want := []string{"https://proxy.example.test", "token-value-1", "key-value-2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("secretValues = %v, want %v", got, want)
	}
	toml := "model = \"m\"\n[model_providers.p]\nbase_url = \"https://codex.example.test/v1\"\nenv_key = \"OPENAI_API_KEY\"\n"
	if got := tomlSecretValues([]byte(toml)); !reflect.DeepEqual(got, []string{"https://codex.example.test/v1"}) {
		t.Fatalf("tomlSecretValues = %v", got)
	}
}

// fakeKey builds an API-key-shaped string at run time so that no
// key-shaped literal is committed.
func fakeKey() string { return "sk-" + strings.Repeat("A1b2", 8) }

func TestScrub(t *testing.T) {
	home := t.TempDir()
	workDir := filepath.Join(home, "work")
	s := NewScrubber([]string{"token-value-1", "https://proxy.example.test"})
	s.AddPath("@HOME@", home)
	s.AddPath("@WORKDIR@", workDir)

	in := strings.Join([]string{
		`{"token":"token-value-1","url":"https://proxy.example.test/v1"}`,
		`{"headers":{"Authorization":"Basic abc"},"auth":"Bearer abc.def-ghi"}`,
		`{"key":"` + fakeKey() + `"}`,
		`{"cwd":"` + workDir + `/sub","home":"` + home + `"}`,
	}, "\n")
	got := string(s.Scrub([]byte(in)))
	want := strings.Join([]string{
		`{"token":"REDACTED","url":"REDACTED/v1"}`,
		`{"headers":{"Authorization":"REDACTED"},"auth":"Bearer REDACTED"}`,
		`{"key":"REDACTED"}`,
		`{"cwd":"@WORKDIR@/sub","home":"@HOME@"}`,
	}, "\n")
	if got != want {
		t.Fatalf("Scrub =\n%s\nwant\n%s", got, want)
	}
	if err := s.Check([]byte(got)); err != nil {
		t.Fatalf("Check after Scrub: %v", err)
	}
	err := s.Check([]byte("leaked token-value-1"))
	if err == nil {
		t.Fatal("Check missed a credential")
	}
	if strings.Contains(err.Error(), "token-value-1") {
		t.Fatal("Check error repeats the credential")
	}
	if err := s.Check([]byte(fakeKey())); err == nil {
		t.Fatal("Check missed an API key pattern")
	}
}

func TestFixtureRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "text")
	rec := Recorded{
		Meta: Meta{Harness: "claude", Version: "1.0.0", Scenario: "text", Prompts: []string{"hi"}},
		Traffic: [][]fakeharness.TrafficLine{{
			{Dir: fakeharness.DirSend, Line: json.RawMessage(`{"type":"user","token":"token-value-1"}`)},
			{Dir: fakeharness.DirRecv, Line: json.RawMessage(`{"type":"result"}`)},
		}},
		Home: map[string][]byte{"projects/x/s.jsonl": []byte(`{"cwd":"/w"}` + "\n")},
	}
	s := NewScrubber([]string{"token-value-1"})
	s.AddPath("@WORKDIR@", "/w")
	if err := WriteFixture(dir, rec, s); err != nil {
		t.Fatalf("WriteFixture: %v", err)
	}
	got, err := ReadFixture(dir)
	if err != nil {
		t.Fatalf("ReadFixture: %v", err)
	}
	if !reflect.DeepEqual(got.Meta, rec.Meta) {
		t.Fatalf("meta = %+v", got.Meta)
	}
	if string(got.Traffic[0][0].Line) != `{"type":"user","token":"REDACTED"}` {
		t.Fatalf("traffic line = %s", got.Traffic[0][0].Line)
	}
	if string(got.Home["projects/x/s.jsonl"]) != `{"cwd":"@WORKDIR@"}`+"\n" {
		t.Fatalf("home file = %s", got.Home["projects/x/s.jsonl"])
	}

	dst := t.TempDir()
	CopyHome(t, dir, dst, map[string]string{"@WORKDIR@": "/elsewhere"})
	data, err := os.ReadFile(filepath.Join(dst, "projects/x/s.jsonl"))
	if err != nil || string(data) != `{"cwd":"/elsewhere"}`+"\n" {
		t.Fatalf("copied home file = %s, %v", data, err)
	}

	// A fixture is rewritten as a whole: stale traffic files disappear.
	rec.Traffic = append(rec.Traffic, rec.Traffic[0])
	if err := WriteFixture(dir, rec, s); err != nil {
		t.Fatal(err)
	}
	rec.Traffic = rec.Traffic[:1]
	if err := WriteFixture(dir, rec, s); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "traffic-2.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("stale traffic-2.jsonl: %v", err)
	}
}

func TestWriteFixtureRefusesSecrets(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "text")
	rec := Recorded{
		Meta:    Meta{Harness: "codex", Version: "1", Scenario: "text"},
		Traffic: [][]fakeharness.TrafficLine{{{Dir: fakeharness.DirRecv, Line: json.RawMessage(`{"k":"` + fakeKey() + `"}`)}}},
	}
	// The API key pattern is scrubbed even when no credential was loaded.
	if err := WriteFixture(dir, rec, NewScrubber(nil)); err != nil {
		t.Fatalf("WriteFixture: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "traffic-1.jsonl"))
	if err != nil || strings.Contains(string(data), fakeKey()) {
		t.Fatalf("traffic = %s, %v", data, err)
	}

	// File names are not rewritten; a credential in one fails the
	// recording, and nothing is written.
	rec.Home = map[string][]byte{"sessions/token-value-1.jsonl": []byte("{}\n")}
	dir2 := filepath.Join(parent, "leak")
	if err := WriteFixture(dir2, rec, NewScrubber([]string{"token-value-1"})); err == nil {
		t.Fatal("WriteFixture wrote a credential")
	} else if strings.Contains(err.Error(), "token-value-1") {
		t.Fatal("the error repeats the credential")
	}
	if _, err := os.Stat(dir2); !os.IsNotExist(err) {
		t.Fatalf("fixture directory exists after a refused write: %v", err)
	}
}

func TestGolden(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.golden.json")
	value := map[string]any{"a": 1}
	if err := os.WriteFile(path, []byte("{\n  \"a\": 1\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if diff := goldenDiff(path, value, false); diff != "" {
		t.Fatalf("goldenDiff = %s", diff)
	}
	if diff := goldenDiff(path, map[string]any{"a": 2}, false); diff == "" {
		t.Fatal("goldenDiff missed a difference")
	}
	if diff := goldenDiff(path, map[string]any{"a": 2}, true); diff != "" {
		t.Fatalf("update: %s", diff)
	}
	if diff := goldenDiff(path, map[string]any{"a": 2}, false); diff != "" {
		t.Fatalf("after update: %s", diff)
	}
}

func TestScenarios(t *testing.T) {
	want := []string{"text", "shell", "read-file", "write-file", "edit-file", "glob-grep", "subagent", "todo", "terminate", "two-turn"}
	var names []string
	for _, s := range Scenarios() {
		names = append(names, s.Name)
		if len(s.Prompts) == 0 {
			t.Errorf("scenario %s has no prompt", s.Name)
		}
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("scenarios = %v, want %v", names, want)
	}
	s, ok := ScenarioByName("terminate")
	if !ok || s.TerminateWhen == nil {
		t.Fatal("terminate scenario has no TerminateWhen")
	}
	if two, _ := ScenarioByName("two-turn"); len(two.Prompts) != 2 {
		t.Fatalf("two-turn prompts = %v", two.Prompts)
	}
	edit, _ := ScenarioByName("edit-file")
	dir := t.TempDir()
	SeedWorkDir(t, dir, edit)
	for name := range edit.Seed {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
}
