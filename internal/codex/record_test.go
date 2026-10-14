package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wenfengwang/cloud-agent-sdk/internal/fakeharness"
	"github.com/wenfengwang/cloud-agent-sdk/internal/harness"
	"github.com/wenfengwang/cloud-agent-sdk/internal/message"
	"github.com/wenfengwang/cloud-agent-sdk/internal/testenv"
)

// recordTurnTimeout bounds one recorded turn.
const recordTurnTimeout = 5 * time.Minute

// TestRecord records every scenario of design 002 from the pinned Codex
// into testdata/<scenario>/. It needs the pinned CLI and the credentials in
// .harness-local/, so it runs only with CLOUDAGENT_RECORD=1.
func TestRecord(t *testing.T) {
	if os.Getenv("CLOUDAGENT_RECORD") != "1" {
		t.Skip("set CLOUDAGENT_RECORD=1 to record fixtures from the pinned Codex")
	}
	only := os.Getenv("CLOUDAGENT_SCENARIO")
	for _, s := range testenv.Scenarios() {
		if only != "" && only != s.Name {
			continue
		}
		t.Run(s.Name, func(t *testing.T) { recordScenario(t, s) })
	}
}

func recordScenario(t *testing.T, s testenv.Scenario) {
	home := testenv.Isolate(t)
	testenv.RequirePinnedCLIs(t)
	env := testenv.CodexEnv(t)
	versions, err := testenv.LoadVersions(testenv.RepoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	workDir := t.TempDir()
	testenv.SeedWorkDir(t, workDir, s)

	var mu sync.Mutex
	var traffic [][]fakeharness.TrafficLine
	h := New(harness.Config{WorkDir: workDir, Home: env.Home})
	h.config = recordConfig
	h.tap = func(process int, dir string, line []byte) {
		mu.Lock()
		defer mu.Unlock()
		for len(traffic) < process {
			traffic = append(traffic, nil)
		}
		traffic[process-1] = append(traffic[process-1], fakeharness.TrafficLine{Dir: dir, Line: json.RawMessage(append([]byte(nil), line...))})
	}
	for i, prompt := range s.Prompts {
		ctx, cancel := context.WithTimeout(context.Background(), recordTurnTimeout)
		r, err := h.StartTurn(ctx, prompt)
		cancel()
		if err != nil {
			t.Fatalf("turn %d: StartTurn: %v", i+1, err)
		}
		stopped := false
		var live []message.Message
		for m := range r.Messages() {
			live = append(live, m)
			if !stopped && s.TerminateWhen != nil && s.TerminateWhen(m) {
				stopped = true
				r.Interrupt()
				go func() {
					time.Sleep(5 * time.Second)
					r.Kill()
				}()
			}
		}
		if _, err := r.Wait(); err != nil && !stopped {
			t.Fatalf("turn %d: %v", i+1, err)
		}
		if s.TerminateWhen != nil && !stopped {
			t.Fatalf("turn %d: the terminate condition never held", i+1)
		}
		// Read back right after the turn, as the agent does: the rollout
		// must already hold the turn, including a tool call still running
		// at a stop.
		back, err := h.ReadTurn(context.Background(), r.Ref())
		if err != nil {
			t.Fatalf("turn %d: ReadTurn: %v", i+1, err)
		}
		if !reflect.DeepEqual(withoutRaw(back), withoutRaw(live)) {
			t.Fatalf("turn %d read back as\n%s\nlive\n%s\n%s", i+1, kinds(back), kinds(live), firstDifference(withoutRaw(back), withoutRaw(live)))
		}
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}

	rec := testenv.Recorded{
		Meta:    testenv.Meta{Harness: "codex", Version: versions.Codex, Scenario: s.Name, Prompts: s.Prompts},
		Traffic: traffic,
		Home:    map[string][]byte{},
	}
	sessions := filepath.Join(env.Home, "sessions")
	err = filepath.WalkDir(sessions, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(env.Home, path)
		if err != nil {
			return err
		}
		rec.Home[filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil {
		t.Fatalf("copy rollouts: %v", err)
	}
	if err := testenv.WriteFixture(filepath.Join("testdata", s.Name), rec, recordScrubber(t, env, home, workDir)); err != nil {
		t.Fatal(err)
	}
}

// recordConfig is the test-only configuration override of design 002: the
// developer instructions that make file writes and edits arrive as
// fileChange items.
var recordConfig = map[string]any{
	"developer_instructions": testenv.CodexApplyPatchInstructions,
}

// recordScrubber scrubs credentials and replaces paths and the host name.
func recordScrubber(t *testing.T, env testenv.HarnessEnv, home, workDir string) *testenv.Scrubber {
	t.Helper()
	s := testenv.NewScrubber(credentialValues(env.Secrets))
	s.AddPath(testenv.PlaceholderHarnessHome, env.Home)
	s.AddPath(testenv.PlaceholderWorkDir, workDir)
	s.AddPath(testenv.PlaceholderHome, home)
	if host, err := os.Hostname(); err == nil && len(host) > 3 {
		s.AddPath(placeholderHost, host)
	}
	return s
}

// placeholderHost replaces the recording machine's host name, which Codex
// reports in remoteControl/status/changed.
const placeholderHost = "@HOST@"

// credentialValues drops loaded "secrets" that are plain TOML scalars (for
// example a feature flag named *api_key* set to true): replacing every
// "true" would corrupt the recorded JSON without protecting anything.
func credentialValues(secrets []string) []string {
	var out []string
	for _, v := range secrets {
		if v == "true" || v == "false" {
			continue
		}
		if _, err := strconv.ParseFloat(v, 64); err == nil {
			continue
		}
		out = append(out, v)
	}
	return out
}

// kinds summarizes messages as "kind[:tool]" words, for failure output.
func kinds(msgs []message.Message) string {
	var parts []string
	for _, m := range msgs {
		p := string(m.Kind)
		if m.ToolCall != nil {
			p += ":" + string(m.ToolCall.Name)
		}
		if m.ToolResult != nil {
			p += ":" + string(m.ToolResult.Name)
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, " ")
}

// firstDifference describes the first message that differs, as JSON.
func firstDifference(a, b []message.Message) string {
	for i := 0; i < len(a) && i < len(b); i++ {
		if !reflect.DeepEqual(a[i], b[i]) {
			ja, _ := json.Marshal(goldenView(a[i : i+1]))
			jb, _ := json.Marshal(goldenView(b[i : i+1]))
			return fmt.Sprintf("message %d:\n%s\n%s", i, ja, jb)
		}
	}
	return "lengths differ"
}
