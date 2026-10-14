package testenv

import (
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
	for name, want := range map[string]string{"claude": v.Claude, "codex": v.Codex} {
		out, err := exec.Command(filepath.Join(bin, name), "--version").CombinedOutput()
		if err != nil {
			t.Fatalf("%s --version: %v (run scripts/install-harnesses.sh)", name, err)
		}
		if !matchVersionOutput(string(out), want) {
			t.Fatalf("%s --version = %q, want pinned version %s (run scripts/install-harnesses.sh)", name, strings.TrimSpace(string(out)), want)
		}
	}
	t.Setenv("PATH", bin+string(filepath.ListSeparator)+os.Getenv("PATH"))
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
