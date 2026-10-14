package claude

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/wenfengwang/cloud-agent-sdk/core/internal/fakeharness"
	"github.com/wenfengwang/cloud-agent-sdk/core/internal/harness"
	"github.com/wenfengwang/cloud-agent-sdk/core/internal/testenv"
)

// placeholderSessionID replaces the recorded session ID in fixtures. Replays
// through the fake substitute the adapter's own session ID for it with
// fakeharness ArgSubstitutions on --session-id and --resume.
const placeholderSessionID = "@SESSION_ID@"

// recorder is the traffic tap of a recording: one traffic segment per
// harness process.
type recorder struct {
	mu       sync.Mutex
	segments [][]fakeharness.TrafficLine
}

func (r *recorder) newSegment() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.segments = append(r.segments, nil)
}

func (r *recorder) tap(dir string, line []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(r.segments) - 1
	r.segments[n] = append(r.segments[n], fakeharness.TrafficLine{Dir: dir, Line: json.RawMessage(append([]byte(nil), line...))})
}

// TestRecord records every scenario of design 002 from the pinned Claude Code
// CLI into testdata/<scenario>/. It runs only with CLOUDAGENT_RECORD=1; set
// CLOUDAGENT_SCENARIO to record a single scenario.
func TestRecord(t *testing.T) {
	if os.Getenv("CLOUDAGENT_RECORD") != "1" {
		t.Skip("set CLOUDAGENT_RECORD=1 to record fixtures from the pinned CLI")
	}
	only := os.Getenv("CLOUDAGENT_SCENARIO")
	for _, s := range testenv.Scenarios() {
		if only != "" && s.Name != only {
			continue
		}
		t.Run(s.Name, func(t *testing.T) { record(t, s) })
	}
}

func record(t *testing.T, s testenv.Scenario) {
	home := testenv.Isolate(t)
	testenv.RequirePinnedCLIs(t)
	env := testenv.ClaudeEnv(t)
	versions, err := testenv.LoadVersions(testenv.RepoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	testenv.SeedWorkDir(t, work, s)

	rec := &recorder{}
	h := New(harness.Config{WorkDir: work, Home: env.Home})
	h.tap = rec.tap
	for i, prompt := range s.Prompts {
		rec.newSegment()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		run, err := h.StartTurn(ctx, prompt)
		cancel()
		if err != nil {
			t.Fatalf("turn %d: start: %v", i+1, err)
		}
		killer := time.AfterFunc(5*time.Minute, run.Kill)
		stopped := false
		for m := range run.Messages() {
			if !stopped && s.TerminateWhen != nil && s.TerminateWhen(m) {
				stopped = true
				run.Interrupt()
				time.AfterFunc(10*time.Second, run.Kill)
			}
		}
		killer.Stop()
		if _, err := run.Wait(); err != nil && !stopped {
			t.Fatalf("turn %d: %v", i+1, err)
		}
		if s.TerminateWhen != nil && !stopped {
			t.Fatalf("turn %d: the terminate condition never matched", i+1)
		}
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}

	files := map[string][]byte{}
	projects := filepath.Join(env.Home, "projects")
	err = filepath.WalkDir(projects, func(path string, d fs.DirEntry, err error) error {
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
		files[filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil {
		t.Fatalf("copy transcripts: %v", err)
	}

	scrub := testenv.NewScrubber(env.Secrets)
	scrub.AddPath(testenv.PlaceholderHome, home)
	scrub.AddPath(testenv.PlaceholderHarnessHome, env.Home)
	scrub.AddPath(testenv.PlaceholderWorkDir, work)
	scrub.AddPath(testenv.PlaceholderWorkDirEncoded, EncodeWorkDir(work))
	if resolved, err := filepath.EvalSymlinks(work); err == nil {
		scrub.AddPath(testenv.PlaceholderWorkDirEncoded, EncodeWorkDir(resolved))
	}
	scrub.AddPath(placeholderSessionID, h.sessionID)

	fixture := testenv.Recorded{
		Meta: testenv.Meta{
			Harness:  "claude",
			Version:  versions.Claude,
			Scenario: s.Name,
			Prompts:  s.Prompts,
		},
		Traffic: rec.segments,
		Home:    files,
	}
	if err := testenv.WriteFixture(filepath.Join("testdata", s.Name), fixture, scrub); err != nil {
		t.Fatal(err)
	}
}
