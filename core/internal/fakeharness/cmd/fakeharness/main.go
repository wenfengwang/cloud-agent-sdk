// Command fakeharness is the fake claude and codex executable used by
// lifecycle tests. It is configured by the script named in
// CLOUDAGENT_FAKE_SCRIPT; see package fakeharness for the script format and
// the replay rules.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/wenfengwang/cloud-agent-sdk/core/internal/fakeharness"
)

func main() {
	os.Exit(run())
}

func run() int {
	path := os.Getenv(fakeharness.EnvScript)
	if path == "" {
		return fail("%s is not set", fakeharness.EnvScript)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fail("read script: %v", err)
	}
	var s fakeharness.Script
	if err := json.Unmarshal(data, &s); err != nil {
		return fail("parse script: %v", err)
	}
	protocol := filepath.Base(os.Args[0])
	if protocol != "claude" && protocol != "codex" {
		return fail("unknown protocol %q: the executable must be named claude or codex", protocol)
	}

	n, err := record(s.StateDir)
	if err != nil {
		return fail("record state: %v", err)
	}
	if n > len(s.Invocations) {
		return fail("invocation %d is not scripted", n)
	}
	inv := s.Invocations[n-1]
	if inv.ExitAtStart != nil {
		fmt.Fprintln(os.Stderr, inv.ExitAtStart.Stderr)
		return inv.ExitAtStart.Code
	}
	var lines []fakeharness.TrafficLine
	if inv.Traffic != "" {
		if lines, err = fakeharness.ReadTraffic(inv.Traffic); err != nil {
			return fail("%v", err)
		}
	}

	received, err := os.Create(filepath.Join(s.StateDir, fmt.Sprintf("received-%d.jsonl", n)))
	if err != nil {
		return fail("record state: %v", err)
	}
	defer received.Close()

	r := &replayer{
		stateDir: s.StateDir,
		protocol: protocol,
		inv:      inv,
		subst:    substitutions(s),
		ids:      map[string]json.RawMessage{},
		stdin:    make(chan []byte, 1024),
		signals:  make(chan os.Signal, 4),
	}
	signal.Notify(r.signals, syscall.SIGTERM, syscall.SIGINT)
	go readStdin(r.stdin, received)
	return r.replay(lines)
}

func fail(format string, args ...any) int {
	fmt.Fprintf(os.Stderr, "fakeharness: "+format+"\n", args...)
	return 2
}

// record numbers this invocation and writes its pid and Info.
func record(dir string) (int, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return 0, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return 0, err
	}
	pids, err := os.OpenFile(filepath.Join(dir, "pids"), os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return 0, err
	}
	defer pids.Close()
	existing, err := os.ReadFile(pids.Name())
	if err != nil {
		return 0, err
	}
	n := len(strings.Fields(string(existing))) + 1
	if _, err := fmt.Fprintln(pids, os.Getpid()); err != nil {
		return 0, err
	}

	wd, _ := os.Getwd()
	info := fakeharness.Info{Args: os.Args[1:], Dir: wd, Env: map[string]string{}}
	for _, key := range []string{"HOME", "CLAUDE_CONFIG_DIR", "CODEX_HOME"} {
		if v, ok := os.LookupEnv(key); ok {
			info.Env[key] = v
		}
	}
	data, err := json.Marshal(info)
	if err != nil {
		return 0, err
	}
	return n, os.WriteFile(filepath.Join(dir, fmt.Sprintf("info-%d.json", n)), data, 0o644)
}

// substitutions resolves the script's placeholders, including those taken
// from the command line.
func substitutions(s fakeharness.Script) map[string]string {
	out := map[string]string{}
	for k, v := range s.Substitutions {
		out[k] = v
	}
	args := os.Args[1:]
	for placeholder, flags := range s.ArgSubstitutions {
	search:
		for _, flag := range flags {
			for i, a := range args {
				if a == flag && i+1 < len(args) {
					out[placeholder] = args[i+1]
					break search
				}
				if v, ok := strings.CutPrefix(a, flag+"="); ok {
					out[placeholder] = v
					break search
				}
			}
		}
	}
	return out
}

// readStdin forwards stdin lines, recording each, and closes ch at EOF.
func readStdin(ch chan<- []byte, received *os.File) {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(nil, 64<<20)
	for sc.Scan() {
		line := append([]byte(nil), sc.Bytes()...)
		received.Write(append(line, '\n'))
		ch <- line
	}
	close(ch)
}

type replayer struct {
	stateDir string
	protocol string
	inv      fakeharness.Invocation
	subst    map[string]string
	ids      map[string]json.RawMessage // recorded request id -> actual id
	stdin    chan []byte
	pending  [][]byte // stdin lines read ahead of their send line
	signals  chan os.Signal
	rejected bool
	recvs    int
}

// exitError ends the replay with a status code.
type exitError int

func (e exitError) Error() string { return "exit " + strconv.Itoa(int(e)) }

func (r *replayer) replay(lines []fakeharness.TrafficLine) int {
	err := r.walk(lines)
	if err == nil {
		err = r.end()
	}
	var code exitError
	if errors.As(err, &code) {
		return int(code)
	}
	return fail("%v", err)
}

func (r *replayer) walk(lines []fakeharness.TrafficLine) error {
	if err := r.afterRecv(); err != nil {
		return err
	}
	for _, tl := range lines {
		if tl.Dir == fakeharness.DirSend {
			if err := r.expect(tl.Line); err != nil {
				return err
			}
			continue
		}
		if r.inv.PaceMs > 0 {
			if err := r.sleep(time.Duration(r.inv.PaceMs) * time.Millisecond); err != nil {
				return err
			}
		}
		if err := r.emit(tl.Line); err != nil {
			return err
		}
		r.recvs++
		if err := r.afterRecv(); err != nil {
			return err
		}
	}
	return nil
}

// afterRecv crashes or hangs once the scripted number of recv lines was
// written.
func (r *replayer) afterRecv() error {
	if c := r.inv.CrashAfter; c != nil && r.recvs == c.Lines {
		fmt.Fprintln(os.Stderr, "fakeharness: scripted crash")
		return exitError(c.ExitCode)
	}
	if h := r.inv.HangAfter; h != nil && r.recvs == h.Lines {
		if h.SpawnChild {
			if err := r.spawnChild(); err != nil {
				return err
			}
		}
		return r.hang(h.OnInterrupt)
	}
	return nil
}

// end writes the files and then exits or waits for EOF or a signal.
func (r *replayer) end() error {
	for _, f := range r.inv.Files {
		data, err := os.ReadFile(f.From)
		if err != nil {
			return fmt.Errorf("read file %s: %w", f.From, err)
		}
		to := r.apply(f.To, false)
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return fmt.Errorf("write file %s: %w", to, err)
		}
		if err := os.WriteFile(to, []byte(r.apply(string(data), true)), 0o644); err != nil {
			return fmt.Errorf("write file %s: %w", to, err)
		}
	}
	if r.inv.ExitAtEnd {
		return exitError(0)
	}
	for {
		line, err := r.next()
		if err != nil {
			return err
		}
		r.answerReject(line)
	}
}

// next returns the next stdin line, or the exit for EOF or a signal.
func (r *replayer) next() ([]byte, error) {
	if len(r.pending) > 0 {
		line := r.pending[0]
		r.pending = r.pending[1:]
		return line, nil
	}
	select {
	case line, ok := <-r.stdin:
		if !ok {
			return nil, exitError(0)
		}
		return line, nil
	case sig := <-r.signals:
		return nil, signalExit(sig)
	}
}

func signalExit(sig os.Signal) error {
	if sig == syscall.SIGINT {
		return exitError(130)
	}
	return exitError(143)
}

// expect blocks until stdin delivers a line of the same kind as recorded.
func (r *replayer) expect(recorded json.RawMessage) error {
	want := kind(r.protocol, recorded)
	for {
		line, err := r.next()
		if err != nil {
			return err
		}
		if r.answerReject(line) {
			continue
		}
		if kind(r.protocol, line) != want {
			continue
		}
		r.mapID(recorded, line)
		return nil
	}
}

// answerReject answers the first request of the rejected method with an
// error and reports whether it did.
func (r *replayer) answerReject(line []byte) bool {
	if r.inv.RejectMethod == "" || r.rejected || kind(r.protocol, line) != r.inv.RejectMethod {
		return false
	}
	r.rejected = true
	var req struct {
		ID json.RawMessage `json:"id"`
	}
	json.Unmarshal(line, &req)
	resp, _ := json.Marshal(map[string]any{
		"id":    req.ID,
		"error": map[string]any{"code": -32600, "message": "fakeharness: scripted rejection of " + r.inv.RejectMethod},
	})
	r.write(resp)
	return true
}

// spawnChild starts a long-running child in its own process group, out of
// reach of a kill of the fake's process group, and records its pid.
func (r *replayer) spawnChild() error {
	cmd := exec.Command("/bin/sleep", "300")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn child: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(r.stateDir, "children"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("record child: %w", err)
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, cmd.Process.Pid)
	return err
}

// hang stops the replay until an interrupt request (honor) or forever
// (ignore).
func (r *replayer) hang(mode string) error {
	if mode == fakeharness.OnInterruptIgnore {
		signal.Ignore(syscall.SIGTERM, syscall.SIGINT)
		for line := range r.stdin {
			r.answerReject(line)
		}
		for {
			time.Sleep(time.Hour)
		}
	}
	for {
		line, err := r.next()
		if err != nil {
			return err
		}
		if r.answerReject(line) {
			continue
		}
		if isInterrupt(r.protocol, line) {
			r.pending = append(r.pending, line)
			return nil
		}
	}
}

func (r *replayer) sleep(d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case sig := <-r.signals:
		return signalExit(sig)
	}
}

func (r *replayer) emit(recorded json.RawMessage) error {
	line := []byte(r.apply(string(recorded), true))
	line, err := r.rewriteID(line)
	if err != nil {
		return err
	}
	r.write(line)
	return nil
}

func (r *replayer) write(line []byte) {
	os.Stdout.Write(append(line, '\n'))
}

// apply replaces placeholders; jsonEscaped inserts values escaped for a
// JSON string.
func (r *replayer) apply(s string, jsonEscaped bool) string {
	for k, v := range r.subst {
		if jsonEscaped {
			b, _ := json.Marshal(v)
			v = string(b[1 : len(b)-1])
		}
		s = strings.ReplaceAll(s, k, v)
	}
	return s
}

// mapID remembers the actual id of a request recorded with another id.
func (r *replayer) mapID(recorded, actual []byte) {
	rec, act := requestID(r.protocol, recorded), requestID(r.protocol, actual)
	if rec != nil && act != nil {
		r.ids[string(rec)] = act
	}
}

// requestID returns the id of a client request: the JSON-RPC id (Codex) or
// the control request_id (Claude).
func requestID(protocol string, line []byte) json.RawMessage {
	var m struct {
		ID        json.RawMessage `json:"id"`
		Method    string          `json:"method"`
		Type      string          `json:"type"`
		RequestID json.RawMessage `json:"request_id"`
	}
	if json.Unmarshal(line, &m) != nil {
		return nil
	}
	if protocol == "codex" && m.Method != "" {
		return m.ID
	}
	if protocol == "claude" && m.Type == "control_request" {
		return m.RequestID
	}
	return nil
}

// rewriteID gives a recorded response the id of the actual request.
func (r *replayer) rewriteID(line []byte) ([]byte, error) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(line, &obj) != nil {
		return line, nil
	}
	if r.protocol == "codex" {
		if _, isRequest := obj["method"]; isRequest || obj["id"] == nil {
			return line, nil
		}
		actual, ok := r.ids[string(obj["id"])]
		if !ok {
			return line, nil
		}
		obj["id"] = actual
		return json.Marshal(obj)
	}
	var typ string
	json.Unmarshal(obj["type"], &typ)
	if typ != "control_response" {
		return line, nil
	}
	var resp map[string]json.RawMessage
	if json.Unmarshal(obj["response"], &resp) != nil {
		return line, nil
	}
	actual, ok := r.ids[string(resp["request_id"])]
	if !ok {
		return line, nil
	}
	resp["request_id"] = actual
	var err error
	if obj["response"], err = json.Marshal(resp); err != nil {
		return nil, err
	}
	return json.Marshal(obj)
}

// kind classifies a line for matching: Claude by type (plus the subtype of a
// control request), Codex by JSON-RPC method or "response".
func kind(protocol string, line []byte) string {
	var m struct {
		Type    string `json:"type"`
		Method  string `json:"method"`
		Request struct {
			Subtype string `json:"subtype"`
		} `json:"request"`
	}
	if json.Unmarshal(line, &m) != nil {
		return ""
	}
	if protocol == "codex" {
		if m.Method != "" {
			return m.Method
		}
		return "response"
	}
	if m.Type == "control_request" {
		return m.Type + "/" + m.Request.Subtype
	}
	return m.Type
}

func isInterrupt(protocol string, line []byte) bool {
	k := kind(protocol, line)
	return k == "control_request/interrupt" || (protocol == "codex" && k == "turn/interrupt")
}
