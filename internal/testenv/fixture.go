package testenv

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/wenfengwang/cloud-agent-sdk/internal/fakeharness"
)

var update = flag.Bool("update", false, "rewrite golden files")

// Meta is a fixture's meta.json.
type Meta struct {
	Harness  string   `json:"harness"` // "claude" or "codex"
	Version  string   `json:"version"` // the CLI version recorded from
	Scenario string   `json:"scenario"`
	Prompts  []string `json:"prompts"`
}

// Recorded is one scenario fixture: testdata/<scenario>/ without its golden.
type Recorded struct {
	Meta Meta
	// Traffic holds one entry per harness process, in start order; entry
	// i is traffic-<i+1>.jsonl.
	Traffic [][]fakeharness.TrafficLine
	// Home maps paths relative to the harness home to file contents.
	Home map[string][]byte
}

// WriteFixture scrubs rec and writes it to dir, replacing meta.json, every
// traffic-<n>.jsonl, and home/; other files (the golden) are kept. If any
// credential survives scrubbing it fails and writes nothing.
func WriteFixture(dir string, rec Recorded, s *Scrubber) error {
	files := map[string][]byte{}
	meta, err := json.MarshalIndent(rec.Meta, "", "  ")
	if err != nil {
		return fmt.Errorf("testenv: encode meta.json: %w", err)
	}
	files["meta.json"] = append(s.Scrub(meta), '\n')
	for i, lines := range rec.Traffic {
		var buf bytes.Buffer
		for _, l := range lines {
			l.Line = s.Scrub(l.Line)
			data, err := json.Marshal(l)
			if err != nil {
				return fmt.Errorf("testenv: encode traffic: %w", err)
			}
			buf.Write(data)
			buf.WriteByte('\n')
		}
		files[fmt.Sprintf("traffic-%d.jsonl", i+1)] = buf.Bytes()
	}
	for name, data := range rec.Home {
		files[filepath.Join("home", s.ScrubPath(name))] = s.Scrub(data)
	}
	for name, data := range files {
		if err := s.Check([]byte(name)); err != nil {
			return fmt.Errorf("testenv: file name in %s: %w", dir, err)
		}
		if err := s.Check(data); err != nil {
			return fmt.Errorf("testenv: %s: %w", filepath.Join(dir, name), err)
		}
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("testenv: write fixture: %w", err)
	}
	stale, _ := filepath.Glob(filepath.Join(dir, "traffic-*.jsonl"))
	stale = append(stale, filepath.Join(dir, "home"), filepath.Join(dir, "meta.json"))
	for _, path := range stale {
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("testenv: write fixture: %w", err)
		}
	}
	for name, data := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("testenv: write fixture: %w", err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return fmt.Errorf("testenv: write fixture: %w", err)
		}
	}
	return nil
}

// ReadFixture reads the fixture in dir.
func ReadFixture(dir string) (Recorded, error) {
	var rec Recorded
	data, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return rec, fmt.Errorf("testenv: read fixture: %w", err)
	}
	if err := json.Unmarshal(data, &rec.Meta); err != nil {
		return rec, fmt.Errorf("testenv: parse %s/meta.json: %w", dir, err)
	}
	for n := 1; ; n++ {
		path := filepath.Join(dir, fmt.Sprintf("traffic-%d.jsonl", n))
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			break
		}
		lines, err := fakeharness.ReadTraffic(path)
		if err != nil {
			return rec, err
		}
		rec.Traffic = append(rec.Traffic, lines)
	}
	rec.Home = map[string][]byte{}
	home := filepath.Join(dir, "home")
	err = filepath.WalkDir(home, func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && path == home {
			return filepath.SkipDir
		}
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(home, path)
		if err != nil {
			return err
		}
		rec.Home[filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil {
		return rec, fmt.Errorf("testenv: read %s: %w", home, err)
	}
	return rec, nil
}

// HomeFiles lists the fixture's home/ files, sorted, as paths relative to the
// harness home (still holding placeholders).
func (r Recorded) HomeFiles() []string {
	var out []string
	for name := range r.Home {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// RequirePinnedFixture fails the test unless rec was recorded from the
// currently pinned version of its harness.
func RequirePinnedFixture(t testing.TB, rec Recorded) {
	t.Helper()
	v, err := LoadVersions(RepoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	pins := map[string]string{"claude": v.Claude, "codex": v.Codex}
	want, ok := pins[rec.Meta.Harness]
	if !ok {
		t.Fatalf("fixture %s: unknown harness %q", rec.Meta.Scenario, rec.Meta.Harness)
	}
	if rec.Meta.Version != want {
		t.Fatalf("fixture %s was recorded from %s %s, but the pin is %s: re-record it", rec.Meta.Scenario, rec.Meta.Harness, rec.Meta.Version, want)
	}
}

// CopyHome copies the fixture's home/ tree into dst, substituting
// placeholders in file names and contents.
func CopyHome(t testing.TB, fixtureDir, dst string, values map[string]string) {
	t.Helper()
	rec, err := ReadFixture(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range rec.Home {
		path := filepath.Join(dst, Substitute(name, values))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(Substitute(string(data), values)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Golden compares got, encoded as indented JSON, with the file at path. With
// -update it rewrites the file instead.
func Golden(t testing.TB, path string, got any) {
	t.Helper()
	if diff := goldenDiff(path, got, *update); diff != "" {
		t.Fatal(diff)
	}
}

func goldenDiff(path string, got any, rewrite bool) string {
	data, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		return fmt.Sprintf("encode golden %s: %v", path, err)
	}
	data = append(data, '\n')
	if rewrite {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err.Error()
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err.Error()
		}
		return ""
	}
	want, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("read golden (run with -update to create it): %v", err)
	}
	if !bytes.Equal(want, data) {
		return fmt.Sprintf("%s differs (run with -update after review)\ngot:\n%s\nwant:\n%s", path, data, want)
	}
	return ""
}
