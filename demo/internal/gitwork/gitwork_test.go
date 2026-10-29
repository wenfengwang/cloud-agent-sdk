package gitwork

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// newRepo creates a repository with one commit on main holding a.txt and
// b.txt.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "a.txt", "one\ntwo\nthree\n")
	write(t, dir, "b.txt", "bee\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "initial")
	return dir
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestIsRepository(t *testing.T) {
	ctx := context.Background()
	if !IsRepository(ctx, newRepo(t)) {
		t.Error("IsRepository(repo) = false")
	}
	if IsRepository(ctx, t.TempDir()) {
		t.Error("IsRepository(plain dir) = true")
	}
}

func TestBranches(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	git(t, repo, "branch", "feature/login")
	current, branches, err := Branches(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if current != "main" {
		t.Errorf("current = %q, want main", current)
	}
	if !slices.Equal(branches, []string{"feature/login", "main"}) {
		t.Errorf("branches = %v", branches)
	}
	if _, _, err := Branches(ctx, t.TempDir()); err == nil {
		t.Error("Branches(plain dir) succeeded")
	}
}

func TestWorktreeAddAndRemove(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	root := t.TempDir()

	wt, err := AddWorktree(ctx, repo, root, "storefront", "main")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(wt.Path, filepath.Join(root, "storefront")+string(filepath.Separator)) {
		t.Errorf("Path = %s, want under %s/storefront", wt.Path, root)
	}
	if !strings.HasPrefix(wt.Branch, "cloudagent-demo/") || wt.BaseBranch != "main" {
		t.Errorf("Branch = %q, BaseBranch = %q", wt.Branch, wt.BaseBranch)
	}
	if _, err := os.Stat(filepath.Join(wt.Path, "a.txt")); err != nil {
		t.Errorf("worktree has no checkout: %v", err)
	}

	removed, reason, err := RemoveWorktree(ctx, repo, wt.Path)
	if err != nil || !removed || reason != "" {
		t.Fatalf("RemoveWorktree = %v, %q, %v; want removed", removed, reason, err)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Errorf("worktree directory still exists: %v", err)
	}
	// The branch is kept.
	if out := git(t, repo, "branch", "--list", wt.Branch); out == "" {
		t.Errorf("branch %s was deleted", wt.Branch)
	}
}

func TestWorktreeRemoveRefusesDirty(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	wt, err := AddWorktree(ctx, repo, t.TempDir(), "storefront", "main")
	if err != nil {
		t.Fatal(err)
	}
	write(t, wt.Path, "new.txt", "untracked\n")

	removed, reason, err := RemoveWorktree(ctx, repo, wt.Path)
	if err != nil {
		t.Fatal(err)
	}
	if removed || reason != "uncommitted changes" {
		t.Fatalf("RemoveWorktree = %v, %q; want kept for uncommitted changes", removed, reason)
	}
	if _, err := os.Stat(filepath.Join(wt.Path, "new.txt")); err != nil {
		t.Errorf("dirty worktree was touched: %v", err)
	}
}

func TestWorktreeRollback(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	wt, err := AddWorktree(ctx, repo, t.TempDir(), "storefront", "main")
	if err != nil {
		t.Fatal(err)
	}
	if err := Rollback(ctx, repo, wt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Errorf("worktree directory still exists: %v", err)
	}
	if out := git(t, repo, "branch", "--list", wt.Branch); out != "" {
		t.Errorf("branch %s still exists", wt.Branch)
	}
}

func TestWorktreeAddUnknownBranch(t *testing.T) {
	if _, err := AddWorktree(context.Background(), newRepo(t), t.TempDir(), "storefront", "nope"); err == nil {
		t.Fatal("AddWorktree with unknown base branch succeeded")
	}
}

func TestDiff(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	base, err := HeadCommit(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}

	// A commit after the start counts too: changes are against the start.
	write(t, repo, "a.txt", "one\n2\nthree\nfour\n")
	git(t, repo, "commit", "-q", "-am", "edit a")
	git(t, repo, "mv", "b.txt", "c.txt")
	write(t, repo, "dir/new file.txt", "hello\nworld\n")
	indexBefore, err := os.ReadFile(filepath.Join(repo, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}

	changes, err := Diff(ctx, repo, base)
	if err != nil {
		t.Fatal(err)
	}
	if changes.BaseCommit != base {
		t.Errorf("BaseCommit = %s, want %s", changes.BaseCommit, base)
	}
	byPath := map[string]File{}
	for _, f := range changes.Files {
		byPath[f.Path] = f
	}
	if len(byPath) != 3 {
		t.Fatalf("files = %+v, want 3", changes.Files)
	}
	check := func(path, status string, add, del int) {
		t.Helper()
		f, ok := byPath[path]
		if !ok {
			t.Errorf("missing %s", path)
			return
		}
		if f.Status != status || f.Additions != add || f.Deletions != del {
			t.Errorf("%s = %s +%d -%d, want %s +%d -%d", path, f.Status, f.Additions, f.Deletions, status, add, del)
		}
		if !strings.Contains(f.Diff, "@@") && f.Status != "renamed" {
			t.Errorf("%s diff has no hunk:\n%s", path, f.Diff)
		}
	}
	check("a.txt", "modified", 2, 1)
	check("c.txt", "renamed", 0, 0)
	check("dir/new file.txt", "added", 2, 0)

	indexAfter, err := os.ReadFile(filepath.Join(repo, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(indexBefore, indexAfter) {
		t.Error("Diff modified the index")
	}
}

func TestDiffDeleted(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	base, err := HeadCommit(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, "b.txt")); err != nil {
		t.Fatal(err)
	}
	changes, err := Diff(ctx, repo, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes.Files) != 1 || changes.Files[0].Path != "b.txt" || changes.Files[0].Status != "deleted" || changes.Files[0].Deletions != 1 {
		t.Fatalf("files = %+v", changes.Files)
	}
}

func TestDiffNone(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	base, err := HeadCommit(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := Diff(ctx, repo, base)
	if err != nil {
		t.Fatal(err)
	}
	if changes.Files == nil || len(changes.Files) != 0 {
		t.Fatalf("files = %#v, want empty non-nil", changes.Files)
	}
}
