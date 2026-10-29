// Package gitwork runs the demo's git commands: repository detection,
// branches, worktree add and remove, the start commit, and changed files.
//
// The only commands that change a repository are worktree add, worktree
// remove without --force, and branch -D in Rollback. Every other call is
// read-only and never writes the index.
package gitwork

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Worktree is a worktree the backend created for a session.
type Worktree struct {
	Path       string `json:"path"`
	Branch     string `json:"branch"`
	BaseBranch string `json:"baseBranch"`
}

// Changes is the working tree of a directory against a base commit.
type Changes struct {
	BaseCommit string `json:"baseCommit"`
	Files      []File `json:"files"`
}

// File is one changed file.
type File struct {
	Path      string `json:"path"`
	Status    string `json:"status"` // added, modified, deleted, or renamed
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Diff      string `json:"diff"`
}

// run runs git in dir. GIT_OPTIONAL_LOCKS=0 keeps read-only commands from
// refreshing the index. Exit codes listed in okCodes are not errors.
func run(ctx context.Context, dir string, okCodes []int, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C", "GIT_TERMINAL_PROMPT=0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			for _, code := range okCodes {
				if exit.ExitCode() == code {
					return stdout.String(), nil
				}
			}
		}
		return stdout.String(), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// IsRepository reports whether path is inside a git work tree.
func IsRepository(ctx context.Context, path string) bool {
	out, err := run(ctx, path, nil, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// CurrentBranch returns the checked-out branch of path, or "HEAD" when
// detached.
func CurrentBranch(ctx context.Context, path string) (string, error) {
	out, err := run(ctx, path, nil, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Branches returns the current branch and all local branches of path,
// sorted.
func Branches(ctx context.Context, path string) (current string, branches []string, err error) {
	current, err = CurrentBranch(ctx, path)
	if err != nil {
		return "", nil, err
	}
	out, err := run(ctx, path, nil, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return "", nil, err
	}
	branches = []string{}
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			branches = append(branches, line)
		}
	}
	sort.Strings(branches)
	return current, branches, nil
}

// HeadCommit returns the full commit ID of HEAD in dir.
func HeadCommit(ctx context.Context, dir string) (string, error) {
	out, err := run(ctx, dir, nil, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// AddWorktree creates a worktree of repo at <root>/<project>/<suffix> on a
// new branch cloudagent-demo/<suffix> started from baseBranch.
func AddWorktree(ctx context.Context, repo, root, project, baseBranch string) (Worktree, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return Worktree{}, fmt.Errorf("worktree suffix: %w", err)
	}
	suffix := hex.EncodeToString(b[:])
	path := filepath.Join(root, project, suffix)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Worktree{}, fmt.Errorf("worktree root: %w", err)
	}
	branch := "cloudagent-demo/" + suffix
	if _, err := run(ctx, repo, nil, "worktree", "add", "-q", "-b", branch, path, baseBranch); err != nil {
		return Worktree{}, err
	}
	return Worktree{Path: path, Branch: branch, BaseBranch: baseBranch}, nil
}

// RemoveWorktree removes the worktree at path without --force. When git
// refuses because the worktree has local changes, the worktree is kept and
// reason says why. The branch is always kept.
func RemoveWorktree(ctx context.Context, repo, path string) (removed bool, reason string, err error) {
	if _, err := run(ctx, repo, nil, "worktree", "remove", path); err != nil {
		if status, statusErr := run(ctx, path, nil, "status", "--porcelain"); statusErr == nil && strings.TrimSpace(status) != "" {
			return false, "uncommitted changes", nil
		}
		return false, "", err
	}
	return true, "", nil
}

// Rollback removes a worktree created in the same request and deletes its
// branch, which has no commits of its own.
func Rollback(ctx context.Context, repo string, wt Worktree) error {
	if _, err := run(ctx, repo, nil, "worktree", "remove", wt.Path); err != nil {
		return err
	}
	_, err := run(ctx, repo, nil, "branch", "-D", wt.Branch)
	return err
}

// Diff returns the working tree of dir, including untracked files,
// against base.
func Diff(ctx context.Context, dir, base string) (Changes, error) {
	changes := Changes{BaseCommit: base, Files: []File{}}

	out, err := run(ctx, dir, nil, "diff", "--name-status", "-z", "-M", base)
	if err != nil {
		return changes, err
	}
	fields := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	for i := 0; i+1 < len(fields); {
		code := fields[i]
		file := File{Path: fields[i+1]}
		paths := []string{fields[i+1]}
		i += 2
		switch code[0] {
		case 'A':
			file.Status = "added"
		case 'D':
			file.Status = "deleted"
		case 'R', 'C':
			if i >= len(fields) {
				return changes, fmt.Errorf("git diff --name-status: truncated rename entry")
			}
			file.Status = "renamed"
			file.Path = fields[i]
			paths = append(paths, fields[i])
			i++
		default:
			file.Status = "modified"
		}
		diff, err := run(ctx, dir, nil, append([]string{"diff", "--no-color", "--no-ext-diff", "-M", base, "--"}, paths...)...)
		if err != nil {
			return changes, err
		}
		file.Diff = diff
		file.Additions, file.Deletions = countLines(diff)
		changes.Files = append(changes.Files, file)
	}

	out, err = run(ctx, dir, nil, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return changes, err
	}
	for _, path := range strings.Split(out, "\x00") {
		if path == "" {
			continue
		}
		// --no-index exits 1 when the files differ.
		diff, err := run(ctx, dir, []int{1}, "diff", "--no-color", "--no-ext-diff", "--no-index", "--", "/dev/null", path)
		if err != nil {
			return changes, err
		}
		file := File{Path: path, Status: "added", Diff: diff}
		file.Additions, file.Deletions = countLines(diff)
		changes.Files = append(changes.Files, file)
	}

	sort.Slice(changes.Files, func(i, j int) bool { return changes.Files[i].Path < changes.Files[j].Path })
	return changes, nil
}

// countLines counts added and deleted lines in the hunks of a diff.
func countLines(diff string) (additions, deletions int) {
	inHunk := false
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			inHunk = false
		case strings.HasPrefix(line, "@@"):
			inHunk = true
		case inHunk && strings.HasPrefix(line, "+"):
			additions++
		case inHunk && strings.HasPrefix(line, "-"):
			deletions++
		}
	}
	return additions, deletions
}
