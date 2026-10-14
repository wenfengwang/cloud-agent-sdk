package testenv

import (
	"os"
	"path/filepath"
	"testing"
)

// RepoRoot returns the repository root: the nearest ancestor of the working
// directory that contains harness-versions.json.
func RepoRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, VersionsFile)); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("repository root not found: no %s above the working directory", VersionsFile)
		}
		dir = parent
	}
}
