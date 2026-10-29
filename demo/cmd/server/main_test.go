package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckLoopback(t *testing.T) {
	for addr, ok := range map[string]bool{
		"127.0.0.1:8787": true,
		"[::1]:8787":     true,
		"localhost:0":    true,
		"0.0.0.0:8787":   false,
		":8787":          false,
		"10.0.0.2:8787":  false,
		"example.com:80": false,
		"127.0.0.1":      false,
	} {
		if err := checkLoopback(addr); (err == nil) != ok {
			t.Errorf("checkLoopback(%q) = %v, want ok=%v", addr, err, ok)
		}
	}
}

func TestParseVersion(t *testing.T) {
	for out, want := range map[string]string{
		"2.1.300 (Claude Code)\n": "2.1.300",
		"codex-cli 0.162.1\n":     "0.162.1",
		"no version here":         "",
	} {
		if got := parseVersion(out); got != want {
			t.Errorf("parseVersion(%q) = %q, want %q", out, got, want)
		}
	}
}

func TestPinnedVersions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness-versions.json")
	if err := os.WriteFile(path, []byte(`{"claude": "2.1.300", "codex": "0.162.1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readPinnedVersions(path)
	if err != nil || got["claude"] != "2.1.300" || got["codex"] != "0.162.1" {
		t.Fatalf("readPinnedVersions = %v, %v", got, err)
	}
	if got, err := readPinnedVersions(""); err != nil || len(got) != 0 {
		t.Fatalf("readPinnedVersions(\"\") = %v, %v; want empty", got, err)
	}
}

func TestIsolatedHomes(t *testing.T) {
	data, userHome := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(userHome, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"config.toml": "model_provider = \"local\"\n", "auth.json": "secret"} {
		if err := os.WriteFile(filepath.Join(userHome, ".codex", name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	homes, err := prepareIsolatedHomes(data, userHome)
	if err != nil {
		t.Fatal(err)
	}
	codexHome := filepath.Join(data, "homes", "codex")
	if homes.claude != filepath.Join(data, "homes", "claude") || homes.codex != codexHome {
		t.Fatalf("homes = %+v", homes)
	}
	if info, err := os.Stat(homes.claude); err != nil || !info.IsDir() {
		t.Errorf("claude home not created: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(codexHome, "config.toml")); string(got) != "model_provider = \"local\"\n" {
		t.Errorf("config.toml = %q, want copied", got)
	}
	if _, err := os.Stat(filepath.Join(codexHome, "auth.json")); !os.IsNotExist(err) {
		t.Errorf("auth.json was copied: %v", err)
	}

	// An existing config.toml is never overwritten, and an existing home is
	// not re-seeded.
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareIsolatedHomes(data, userHome); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(codexHome, "config.toml")); string(got) != "mine" {
		t.Errorf("config.toml = %q, want kept", got)
	}
}

func TestIsolatedHomesWithoutUserConfig(t *testing.T) {
	data := t.TempDir()
	homes, err := prepareIsolatedHomes(data, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(homes.codex, "config.toml")); !os.IsNotExist(err) {
		t.Errorf("config.toml exists without a user config: %v", err)
	}
}
