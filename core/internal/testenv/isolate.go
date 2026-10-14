package testenv

import (
	"os"
	"testing"
)

// Isolate sets HOME to a fresh t.TempDir() and unsets CLAUDE_CONFIG_DIR and
// CODEX_HOME for the rest of the test, so that nothing reads or writes the
// developer's ~/.cloudagent, ~/.claude, or ~/.codex. It returns the new HOME.
func Isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, key := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME"} {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}
	return home
}
