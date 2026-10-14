package testenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMatchVersionOutput(t *testing.T) {
	cases := []struct {
		out, want string
		ok        bool
	}{
		{"2.1.300 (Claude Code)\n", "2.1.300", true},
		{"codex-cli 0.162.1\n", "0.162.1", true},
		{"codex-cli 0.162.10\n", "0.162.1", false},
		{"2.1.30 (Claude Code)", "2.1.300", false},
		{"", "2.1.300", false},
	}
	for _, c := range cases {
		if got := matchVersionOutput(c.out, c.want); got != c.ok {
			t.Errorf("matchVersionOutput(%q, %q) = %v, want %v", c.out, c.want, got, c.ok)
		}
	}
}

func TestCheckPinnedCLIs(t *testing.T) {
	pins := Versions{Claude: "2.1.300", Codex: "0.162.1"}
	cases := map[string]struct {
		claude, codex string // --version output; empty: no executable
		wantErr       string
	}{
		"pinned":         {claude: "2.1.300 (Claude Code)", codex: "codex-cli 0.162.1"},
		"claude differs": {claude: "2.1.301 (Claude Code)", codex: "codex-cli 0.162.1", wantErr: "claude --version"},
		"codex differs":  {claude: "2.1.300 (Claude Code)", codex: "codex-cli 0.162.0", wantErr: "codex --version"},
		"codex missing":  {claude: "2.1.300 (Claude Code)", wantErr: "codex --version"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			bin := t.TempDir()
			for cli, out := range map[string]string{"claude": c.claude, "codex": c.codex} {
				if out != "" {
					writeExecutable(t, filepath.Join(bin, cli), "#!/bin/sh\necho '"+out+"'\n")
				}
			}
			err := CheckPinnedCLIs(bin, pins)
			switch {
			case c.wantErr == "" && err != nil:
				t.Fatalf("CheckPinnedCLIs: %v", err)
			case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
				t.Fatalf("CheckPinnedCLIs = %v, want an error mentioning %q", err, c.wantErr)
			}
		})
	}
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}
