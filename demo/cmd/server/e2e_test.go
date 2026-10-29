package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestRealHarnesses starts the backend against the pinned CLIs and runs one
// prompt per harness through the HTTP API. It runs only with DEMO_E2E=1 and
// needs the repository's .harness-local/ credentials and .tools/ CLIs.
// Credentials go only to the CLIs, and server output is scrubbed of them.
func TestRealHarnesses(t *testing.T) {
	if os.Getenv("DEMO_E2E") != "1" {
		t.Skip("set DEMO_E2E=1 to run against the pinned harness CLIs")
	}
	root := repoRoot(t)
	local := filepath.Join(root, ".harness-local")
	bin := filepath.Join(root, ".tools", "node_modules", ".bin")
	for _, cli := range []string{"claude", "codex"} {
		if _, err := os.Stat(filepath.Join(bin, cli)); err != nil {
			t.Fatalf("pinned %s missing; run scripts/install-harnesses.sh", cli)
		}
	}

	home, data := t.TempDir(), t.TempDir()
	env := isolatedEnv(home)
	claudeVars := readEnv(t, filepath.Join(local, "claude.env"))
	codexVars := readEnv(t, filepath.Join(local, "codex.env"))
	secrets := []string{}
	for _, v := range append(append([]envVar{}, claudeVars...), codexVars...) {
		if len(v.value) >= 6 {
			secrets = append(secrets, v.value)
		}
	}
	for _, v := range claudeVars {
		env = append(env, v.key+"="+v.value)
	}
	prepareCodexHome(t, local, bin, data, env, codexVars, &secrets)

	server := filepath.Join(t.TempDir(), "server")
	build := exec.Command("go", "build", "-o", server, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build server: %v\n%s", err, out)
	}

	addr := freeAddr(t)
	var logs bytes.Buffer
	cmd := exec.Command(server,
		"--addr", addr,
		"--data-dir", data,
		"--harness-bin-dir", bin,
		"--harness-versions", filepath.Join(root, "harness-versions.json"),
	)
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	defer func() {
		if t.Failed() {
			t.Logf("server output:\n%s", scrub(logs.String(), secrets))
		}
	}()
	defer func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(40 * time.Second):
			_ = cmd.Process.Kill()
			t.Error("server did not exit after SIGTERM")
		}
	}()

	base := "http://" + addr
	waitReady(t, base, exited)

	var cfg struct {
		Harnesses []struct {
			Harness, Version, PinnedVersion string
			Available                       bool
		}
	}
	getJSON(t, base+"/api/config", &cfg)
	for _, h := range cfg.Harnesses {
		if !h.Available || h.Version == "" || h.Version != h.PinnedVersion {
			t.Fatalf("harness %+v is not the pinned CLI", h)
		}
	}

	for _, harness := range []string{"claudecode", "codex"} {
		t.Run(harness, func(t *testing.T) { runOnePrompt(t, base, harness) })
	}

	// Shutdown closes every runtime and exits cleanly.
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-exited:
		if err != nil {
			t.Errorf("server exit: %v", err)
		}
		exited <- err // for the deferred wait
	case <-time.After(40 * time.Second):
		t.Fatal("server did not exit after SIGTERM")
	}
}

func runOnePrompt(t *testing.T, base, harness string) {
	workDir := t.TempDir()
	var sess struct{ ID string }
	postJSON(t, base+"/api/sessions", map[string]any{"harness": harness, "workDir": workDir}, http.StatusCreated, &sess)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", base+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	events := readSSE(resp.Body)
	hello := <-events
	var h struct{ ConnectionID string }
	if err := json.Unmarshal([]byte(hello.data), &h); err != nil || hello.name != "hello" {
		t.Fatalf("first event %+v", hello)
	}

	var turn struct{ ID string }
	postJSON(t, base+"/api/sessions/"+sess.ID+"/turns", map[string]string{"prompt": "Reply with exactly the word pong and nothing else. Do not use any tools."}, http.StatusCreated, &turn)
	postJSON(t, base+"/api/events/"+h.ConnectionID+"/subscriptions", map[string]string{"sessionId": sess.ID, "turnId": turn.ID}, http.StatusNoContent, nil)

	var assistant []string
	for ev := range events {
		switch ev.name {
		case "message":
			var m struct {
				Message struct {
					Kind string
					Text *string
				}
			}
			if err := json.Unmarshal([]byte(ev.data), &m); err != nil {
				t.Fatal(err)
			}
			if m.Message.Kind == "assistant" && m.Message.Text != nil {
				assistant = append(assistant, *m.Message.Text)
			}
		case "stream.error":
			t.Errorf("stream.error: %s", ev.data)
		case "turn.ended":
			var ts struct{ Status string }
			_ = json.Unmarshal([]byte(ev.data), &ts)
			if ts.Status != "completed" {
				t.Fatalf("turn ended %s", ev.data)
			}
			if !strings.Contains(strings.ToLower(strings.Join(assistant, " ")), "pong") {
				t.Errorf("assistant said %q, want pong", assistant)
			}
			var list struct{ Messages []json.RawMessage }
			getJSON(t, base+"/api/sessions/"+sess.ID+"/turns/"+turn.ID+"/messages", &list)
			if len(list.Messages) == 0 {
				t.Error("history is empty")
			}
			deleteSession(t, base, sess.ID)
			return
		}
	}
	t.Fatal("event stream ended before turn.ended")
}

func deleteSession(t *testing.T, base, id string) {
	t.Helper()
	req, _ := http.NewRequest("DELETE", base+"/api/sessions/"+id, nil)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("delete: %d", resp.StatusCode)
	}
}

type sse struct{ name, data string }

func readSSE(r io.Reader) <-chan sse {
	out := make(chan sse, 256)
	go func() {
		defer close(out)
		s := bufio.NewScanner(r)
		s.Buffer(make([]byte, 1<<20), 8<<20)
		var ev sse
		for s.Scan() {
			line := s.Text()
			switch {
			case line == "":
				if ev.name != "" {
					out <- ev
				}
				ev = sse{}
			case strings.HasPrefix(line, "event: "):
				ev.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				ev.data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	return out
}

func postJSON(t *testing.T, url string, body any, want int, out any) {
	t.Helper()
	data, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("POST %s: %d %s", url, resp.StatusCode, got)
	}
	if out != nil {
		if err := json.Unmarshal(got, out); err != nil {
			t.Fatal(err)
		}
	}
}

func getJSON(t *testing.T, url string, out any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
}

func waitReady(t *testing.T, base string, exited <-chan error) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-exited:
			t.Fatalf("server exited: %v", err)
		default:
		}
		if resp, err := http.Get(base + "/api/config"); err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("server not ready")
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// isolatedEnv is the process environment with HOME replaced and the
// harness home variables removed, so nothing touches the developer's homes.
func isolatedEnv(home string) []string {
	var env []string
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		switch key {
		case "HOME", "CLAUDE_CONFIG_DIR", "CODEX_HOME":
			continue
		}
		env = append(env, kv)
	}
	return append(env, "HOME="+home)
}

// prepareCodexHome creates the isolated Codex home the server will use,
// with the local config.toml, and logs the pinned CLI in with the key on
// stdin; the key never enters an environment.
func prepareCodexHome(t *testing.T, local, bin, data string, env []string, vars []envVar, secrets *[]string) string {
	t.Helper()
	var key string
	for _, v := range vars {
		if v.key == "OPENAI_API_KEY" {
			key = v.value
		}
	}
	if key == "" {
		t.Fatal(".harness-local/codex.env does not set OPENAI_API_KEY")
	}
	config, err := os.ReadFile(filepath.Join(local, "codex-config.toml"))
	if err != nil {
		t.Fatalf("read .harness-local/codex-config.toml: %v", err)
	}
	for _, line := range strings.Split(string(config), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok && strings.Contains(strings.ToLower(k), "url") {
			if v = strings.Trim(strings.TrimSpace(v), `"'`); v != "" {
				*secrets = append(*secrets, v)
			}
		}
	}
	home := filepath.Join(data, "homes", "codex")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), config, 0o600); err != nil {
		t.Fatal(err)
	}
	login := exec.Command(filepath.Join(bin, "codex"), "login", "--with-api-key")
	login.Env = append(append([]string{}, env...), "CODEX_HOME="+home)
	login.Stdin = strings.NewReader(key + "\n")
	if out, err := login.CombinedOutput(); err != nil {
		t.Fatalf("codex login: %v\n%s", err, scrub(string(out), *secrets))
	}
	return home
}

type envVar struct{ key, value string }

// readEnv parses KEY=VALUE lines; errors never repeat line content.
func readEnv(t *testing.T, path string) []envVar {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("credentials file %s is missing or unreadable", path)
	}
	var vars []envVar
	for n, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		if !ok || strings.TrimSpace(key) == "" {
			t.Fatalf("%s: line %d is not KEY=VALUE", path, n+1)
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		vars = append(vars, envVar{strings.TrimSpace(key), value})
	}
	return vars
}

func scrub(s string, secrets []string) string {
	for i, secret := range secrets {
		s = strings.ReplaceAll(s, secret, fmt.Sprintf("<secret-%d>", i))
	}
	return s
}
