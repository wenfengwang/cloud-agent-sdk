package cloudagent_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	cloudagent "github.com/wenfengwang/cloud-agent-sdk"
)

func TestNewValidatesOptions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		harness cloudagent.Harness
		opts    cloudagent.Options
	}{
		{"unknown harness", "gemini", cloudagent.Options{WorkDir: t.TempDir()}},
		{"empty harness", "", cloudagent.Options{WorkDir: t.TempDir()}},
		{"empty workdir", cloudagent.Claude, cloudagent.Options{}},
		{"missing workdir", cloudagent.Claude, cloudagent.Options{WorkDir: filepath.Join(t.TempDir(), "missing")}},
		{"workdir is a file", cloudagent.OpenAI, cloudagent.Options{WorkDir: file}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if a, err := cloudagent.New(c.harness, c.opts); err == nil {
				a.Close()
				t.Fatal("New succeeded, want an error")
			}
		})
	}
}

func TestNewWritesIndex(t *testing.T) {
	for _, h := range []cloudagent.Harness{cloudagent.Claude, cloudagent.OpenAI} {
		t.Run(string(h), func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			workDir := t.TempDir()
			harnessHome := t.TempDir()

			a, err := cloudagent.New(h, cloudagent.Options{Model: "m", WorkDir: workDir, Home: harnessHome})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			defer a.Close()
			if a.ID() == "" {
				t.Fatal("empty agent ID")
			}
			dir := filepath.Join(home, ".cloudagent", "agents", a.ID())
			data, err := os.ReadFile(filepath.Join(dir, "agent.json"))
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"harness": string(h), "model": "m", "workdir": workDir, "home": harnessHome, "harness_session_id": ""}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("agent.json = %v, want %v", got, want)
			}
			if _, err := os.Stat(filepath.Join(dir, "turns.jsonl")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNewMakesPathsAbsolute(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	parent := t.TempDir()
	if err := os.Mkdir(filepath.Join(parent, "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(parent)

	a, err := cloudagent.New(cloudagent.Claude, cloudagent.Options{WorkDir: "work", Home: "harness-home"})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	data, err := os.ReadFile(filepath.Join(home, ".cloudagent", "agents", a.ID(), "agent.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ WorkDir, Home string }
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got.WorkDir) || !filepath.IsAbs(got.Home) {
		t.Fatalf("paths are not absolute: %+v", got)
	}
}

func TestCloseAndTurnErrors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a, err := cloudagent.New(cloudagent.Claude, cloudagent.Options{WorkDir: t.TempDir(), Home: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Turn(context.Background(), "", cloudagent.TurnOptions{}); err == nil {
		t.Fatal("Turn with an empty prompt succeeded")
	}
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := a.Turn(context.Background(), "hello", cloudagent.TurnOptions{}); !errors.Is(err, cloudagent.ErrClosed) {
		t.Fatalf("Turn after Close = %v, want ErrClosed", err)
	}
}

func TestErrorValues(t *testing.T) {
	want := map[error]string{
		cloudagent.ErrTurnInProgress: "cloudagent: a turn is already in progress",
		cloudagent.ErrTerminated:     "cloudagent: turn terminated",
		cloudagent.ErrClosed:         "cloudagent: agent closed",
	}
	for err, msg := range want {
		if err.Error() != msg {
			t.Errorf("error %q, want %q", err, msg)
		}
	}
}
