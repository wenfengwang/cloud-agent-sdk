package fakeharness

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

const cmdPackage = "github.com/wenfengwang/cloud-agent-sdk/internal/fakeharness/cmd/fakeharness"

// Build compiles the fake into dir as both claude and codex. Call it from
// TestMain, before any test changes PATH.
func Build(dir string) error {
	goBin := filepath.Join(runtime.GOROOT(), "bin", "go")
	if _, err := os.Stat(goBin); err != nil {
		if goBin, err = exec.LookPath("go"); err != nil {
			return fmt.Errorf("fakeharness: find go: %w", err)
		}
	}
	claude := filepath.Join(dir, "claude")
	cmd := exec.Command(goBin, "build", "-o", claude, cmdPackage)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("fakeharness: build: %w\n%s", err, out)
	}
	data, err := os.ReadFile(claude)
	if err != nil {
		return fmt.Errorf("fakeharness: copy: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "codex"), data, 0o755); err != nil {
		return fmt.Errorf("fakeharness: copy: %w", err)
	}
	return nil
}

// Use writes s for the rest of the test: it sets PATH to binDir alone (pass
// an empty directory to simulate a missing CLI) and points EnvScript at the
// script. An empty StateDir becomes a t.TempDir().
func Use(t testing.TB, binDir string, s Script) State {
	t.Helper()
	if s.StateDir == "" {
		s.StateDir = t.TempDir()
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		t.Fatalf("fakeharness: encode script: %v", err)
	}
	path := filepath.Join(t.TempDir(), "script.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("fakeharness: write script: %v", err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv(EnvScript, path)
	return State{Dir: s.StateDir}
}

// WriteTraffic writes lines as a traffic-<n>.jsonl file at path.
func WriteTraffic(t testing.TB, path string, lines []TrafficLine) {
	t.Helper()
	var buf bytes.Buffer
	for _, l := range lines {
		data, err := json.Marshal(l)
		if err != nil {
			t.Fatalf("fakeharness: encode traffic: %v", err)
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("fakeharness: write traffic: %v", err)
	}
}

// ReadTraffic reads a traffic-<n>.jsonl file.
func ReadTraffic(path string) ([]TrafficLine, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("fakeharness: read traffic: %w", err)
	}
	var out []TrafficLine
	for i, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var tl TrafficLine
		if err := json.Unmarshal(line, &tl); err != nil {
			return nil, fmt.Errorf("fakeharness: %s line %d: %w", path, i+1, err)
		}
		if tl.Dir != DirSend && tl.Dir != DirRecv {
			return nil, fmt.Errorf("fakeharness: %s line %d: unknown dir %q", path, i+1, tl.Dir)
		}
		out = append(out, tl)
	}
	return out, nil
}

// State reads what the fake recorded in a script's StateDir. Invocations are
// numbered from 1 in start order.
//
//	pids               one process ID per line, in start order
//	info-<n>.json      Info of invocation n
//	received-<n>.jsonl every line invocation n read from stdin
type State struct {
	Dir string
}

// Invocations returns how many processes of the fake started.
func (s State) Invocations(t testing.TB) int {
	t.Helper()
	return len(s.PIDs(t))
}

// PIDs returns the process IDs of every invocation, in start order.
func (s State) PIDs(t testing.TB) []int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(s.Dir, "pids"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("fakeharness: read pids: %v", err)
	}
	var out []int
	for _, f := range strings.Fields(string(data)) {
		pid, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("fakeharness: parse pids: %v", err)
		}
		out = append(out, pid)
	}
	return out
}

// LivePIDs returns the recorded process IDs that still exist, including
// unreaped zombies: a process the SDK started and never waited for counts as
// alive.
func (s State) LivePIDs(t testing.TB) []int {
	t.Helper()
	var out []int
	for _, pid := range s.PIDs(t) {
		if err := syscall.Kill(pid, 0); err == nil || errors.Is(err, syscall.EPERM) {
			out = append(out, pid)
		}
	}
	return out
}

// Info returns what invocation n recorded about its process.
func (s State) Info(t testing.TB, n int) Info {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(s.Dir, fmt.Sprintf("info-%d.json", n)))
	if err != nil {
		t.Fatalf("fakeharness: read info of invocation %d: %v", n, err)
	}
	var info Info
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatalf("fakeharness: parse info of invocation %d: %v", n, err)
	}
	return info
}

// Received returns every line invocation n read from stdin.
func (s State) Received(t testing.TB, n int) []json.RawMessage {
	t.Helper()
	f, err := os.Open(filepath.Join(s.Dir, fmt.Sprintf("received-%d.jsonl", n)))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("fakeharness: read received lines: %v", err)
	}
	defer f.Close()
	var out []json.RawMessage
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 64<<20)
	for sc.Scan() {
		out = append(out, json.RawMessage(append([]byte(nil), sc.Bytes()...)))
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("fakeharness: read received lines: %v", err)
	}
	return out
}
