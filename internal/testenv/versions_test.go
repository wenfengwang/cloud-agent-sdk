package testenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadVersions(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "harness-versions.json"), `{"claude": "1.2.3", "codex": "4.5.6"}`)

	got, err := LoadVersions(dir)
	if err != nil {
		t.Fatalf("LoadVersions: %v", err)
	}
	want := Versions{Claude: "1.2.3", Codex: "4.5.6"}
	if got != want {
		t.Fatalf("LoadVersions = %+v, want %+v", got, want)
	}
}

func TestLoadVersionsErrors(t *testing.T) {
	cases := map[string]string{
		"missing claude": `{"codex": "4.5.6"}`,
		"missing codex":  `{"claude": "1.2.3"}`,
		"invalid json":   `{`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "harness-versions.json"), content)
			if _, err := LoadVersions(dir); err == nil {
				t.Fatal("LoadVersions succeeded, want an error")
			}
		})
	}
	t.Run("missing file", func(t *testing.T) {
		if _, err := LoadVersions(t.TempDir()); err == nil {
			t.Fatal("LoadVersions succeeded, want an error")
		}
	})
}

func TestRepoPins(t *testing.T) {
	v, err := LoadVersions(RepoRoot(t))
	if err != nil {
		t.Fatalf("LoadVersions: %v", err)
	}
	if v.Claude == "" || v.Codex == "" {
		t.Fatalf("repository pins are incomplete: %+v", v)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
