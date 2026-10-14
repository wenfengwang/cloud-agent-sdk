package testenv

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// HarnessEnv is a credentialed, temporary harness home.
type HarnessEnv struct {
	// Home is the harness home: pass it as Options.Home or Config.Home.
	Home string
	// Secrets are the loaded credential values, for NewScrubber.
	Secrets []string
}

// LocalDir returns the git-ignored .harness-local/ directory.
func LocalDir(t testing.TB) string {
	t.Helper()
	return filepath.Join(RepoRoot(t), ".harness-local")
}

// ClaudeEnv sets every variable of .harness-local/claude.env in the test
// process environment, which Claude Code inherits, and sets CLAUDE_CONFIG_DIR
// to a fresh t.TempDir(), which it returns as Home. Call Isolate first.
func ClaudeEnv(t *testing.T) HarnessEnv {
	t.Helper()
	vars := readEnvFile(t, filepath.Join(LocalDir(t), "claude.env"))
	for _, v := range vars {
		t.Setenv(v.Key, v.Value)
	}
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	return HarnessEnv{Home: home, Secrets: secretValues(vars)}
}

// CodexEnv prepares a fresh CODEX_HOME: it copies
// .harness-local/codex-config.toml into it as config.toml and runs the pinned
// "codex login --with-api-key" with the key from .harness-local/codex.env on
// stdin. The key is never placed in the test process environment. Call
// Isolate and RequirePinnedCLIs first.
func CodexEnv(t *testing.T) HarnessEnv {
	t.Helper()
	dir := LocalDir(t)
	vars := readEnvFile(t, filepath.Join(dir, "codex.env"))
	var key string
	for _, v := range vars {
		if v.Key == "OPENAI_API_KEY" {
			key = v.Value
		}
	}
	if key == "" {
		t.Fatal("testenv: .harness-local/codex.env does not set OPENAI_API_KEY")
	}
	config, err := os.ReadFile(filepath.Join(dir, "codex-config.toml"))
	if err != nil {
		t.Fatalf("testenv: read .harness-local/codex-config.toml: %v", err)
	}
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), config, 0o600); err != nil {
		t.Fatalf("testenv: write config.toml: %v", err)
	}
	secrets := append(secretValues(vars), tomlSecretValues(config)...)

	cmd := exec.Command(filepath.Join(ToolsBin(t), "codex"), "login", "--with-api-key")
	cmd.Env = append(os.Environ(), "CODEX_HOME="+home)
	cmd.Stdin = strings.NewReader(key + "\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		s := NewScrubber(secrets)
		t.Fatalf("testenv: codex login: %v\n%s", err, s.Scrub(out))
	}
	return HarnessEnv{Home: home, Secrets: secrets}
}

type envVar struct{ Key, Value string }

func readEnvFile(t testing.TB, path string) []envVar {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("testenv: credentials file %s is missing or unreadable", path)
	}
	vars, err := parseEnvFile(data)
	if err != nil {
		t.Fatalf("testenv: %s: %v", path, err)
	}
	return vars
}

// parseEnvFile parses KEY=VALUE lines with optional "export " prefixes,
// quotes, comments, and blank lines. Errors never repeat line content.
func parseEnvFile(data []byte) ([]envVar, error) {
	var out []envVar
	sc := bufio.NewScanner(bytes.NewReader(data))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("line %d is not KEY=VALUE", n)
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		out = append(out, envVar{key, value})
	}
	return out, sc.Err()
}

// secretValues returns the values of variables whose names mark them as
// credentials or private endpoints.
func secretValues(vars []envVar) []string {
	var out []string
	for _, v := range vars {
		k := strings.ToUpper(v.Key)
		if v.Value != "" && containsAny(k, "TOKEN", "KEY", "SECRET", "PASSWORD", "URL") {
			out = append(out, v.Value)
		}
	}
	return out
}

// tomlSecretValues returns the quoted values of TOML keys that hold
// credentials or private endpoints (env_key names a variable and is kept).
func tomlSecretValues(data []byte) []string {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		key, value, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		k := strings.ToLower(strings.TrimSpace(key))
		v := strings.Trim(strings.TrimSpace(value), `"'`)
		if v == "" || k == "env_key" {
			continue
		}
		if containsAny(k, "url", "token", "secret", "password", "api_key") {
			out = append(out, v)
		}
	}
	return out
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
