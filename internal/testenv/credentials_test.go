package testenv

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLocalCredentials checks that .harness-local/ is complete and that the
// pinned Codex accepts its key. It needs the pinned CLIs and credentials, so
// it runs only with CLOUDAGENT_RECORD=1 or CLOUDAGENT_E2E=1.
func TestLocalCredentials(t *testing.T) {
	if os.Getenv("CLOUDAGENT_RECORD") != "1" && os.Getenv("CLOUDAGENT_E2E") != "1" {
		t.Skip("set CLOUDAGENT_RECORD=1 or CLOUDAGENT_E2E=1")
	}
	Isolate(t)
	RequirePinnedCLIs(t)

	claude := ClaudeEnv(t)
	if os.Getenv("CLAUDE_CONFIG_DIR") != claude.Home || len(claude.Secrets) == 0 {
		t.Fatal("ClaudeEnv did not prepare the harness home and credentials")
	}
	codex := CodexEnv(t)
	if _, err := os.Stat(filepath.Join(codex.Home, "auth.json")); err != nil {
		t.Fatalf("codex login wrote no auth.json: %v", err)
	}
	if len(codex.Secrets) == 0 {
		t.Fatal("CodexEnv loaded no credential")
	}
}

func TestSetCodexDeveloperInstructions(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "config.toml")
	writeFile(t, path, "model = \"m\"\n\n[features]\nflag = true\n")

	SetCodexDeveloperInstructions(t, home, `Use "apply_patch".`)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// A top-level key must precede every table to stay top-level.
	want := "developer_instructions = \"Use \\\"apply_patch\\\".\"\nmodel = \"m\"\n\n[features]\nflag = true\n"
	if string(data) != want {
		t.Fatalf("config.toml =\n%s\nwant\n%s", data, want)
	}
}
