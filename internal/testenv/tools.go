package testenv

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ToolsBin returns the directory that holds the pinned CLIs installed by
// scripts/install-harnesses.sh.
func ToolsBin(t testing.TB) string {
	t.Helper()
	return filepath.Join(RepoRoot(t), ".tools", "node_modules", ".bin")
}

// RequirePinnedCLIs fails the test unless both pinned CLIs are installed in
// ToolsBin at exactly their pinned versions. It then puts ToolsBin first on
// PATH for the rest of the test.
func RequirePinnedCLIs(t *testing.T) {
	t.Helper()
	v, err := LoadVersions(RepoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	bin := ToolsBin(t)
	if err := CheckPinnedCLIs(bin, v); err != nil {
		t.Fatalf("%v (run scripts/install-harnesses.sh)", err)
	}
	t.Setenv("PATH", bin+string(filepath.ListSeparator)+os.Getenv("PATH"))
}

// CheckPinnedCLIs returns an error unless bin holds claude and codex
// executables whose --version output reports exactly the pinned versions.
func CheckPinnedCLIs(bin string, pins Versions) error {
	for _, cli := range []struct{ name, want string }{{"claude", pins.Claude}, {"codex", pins.Codex}} {
		out, err := exec.Command(filepath.Join(bin, cli.name), "--version").CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s --version: %w", cli.name, err)
		}
		if !matchVersionOutput(string(out), cli.want) {
			return fmt.Errorf("%s --version = %q, want pinned version %s", cli.name, strings.TrimSpace(string(out)), cli.want)
		}
	}
	return nil
}

// matchVersionOutput reports whether want appears as a whole
// whitespace-separated token of a CLI's --version output.
func matchVersionOutput(out, want string) bool {
	for _, field := range strings.Fields(out) {
		if field == want {
			return true
		}
	}
	return false
}
