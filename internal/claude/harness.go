// Package claude is the Claude Code harness adapter. It implements
// harness.Harness; behavior is owned by design/001-core-api.md.
//
// Each turn runs one claude process speaking stream-json on stdin and
// stdout. The first turn creates the session with --session-id; later turns
// continue it with --resume. Finished turns are read back from the session
// transcript in the harness home.
package claude

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/wenfengwang/cloud-agent-sdk/internal/harness"
	"github.com/wenfengwang/cloud-agent-sdk/internal/message"
)

// Traffic directions reported to a tap.
const (
	dirSend = "send" // written to the harness's stdin
	dirRecv = "recv" // read from the harness's stdout
)

// Harness drives Claude Code for one agent runtime.
type Harness struct {
	cfg       harness.Config
	sessionID string

	mu      sync.Mutex
	resumed bool // a turn was accepted, so the session exists

	// tap, when set by tests, observes every line written to or read from
	// a harness process.
	tap func(dir string, line []byte)
}

var _ harness.Harness = (*Harness)(nil)

// New returns an adapter for cfg. It starts no process.
func New(cfg harness.Config) *Harness {
	return &Harness{cfg: cfg, sessionID: newUUID()}
}

// turnRef is the Claude Code turn reference of design 001.
type turnRef struct {
	SessionID       string `json:"session_id"`
	UserMessageUUID string `json:"user_message_uuid"`
}

// StartTurn implements harness.Harness. The turn is accepted once Claude Code
// echoes the prompt back (--replay-user-messages).
func (h *Harness) StartTurn(ctx context.Context, prompt string) (harness.Run, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	args := []string{
		"--print",
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		"--verbose",
		"--replay-user-messages",
		"--permission-mode", "bypassPermissions",
	}
	if h.cfg.Model != "" {
		args = append(args, "--model", h.cfg.Model)
	}
	// A start that failed may still have created the session, and Claude
	// Code rejects --session-id for an existing one.
	if h.resumed || h.sessionExists() {
		args = append(args, "--resume", h.sessionID)
	} else {
		args = append(args, "--session-id", h.sessionID)
	}
	r, err := startRun(ctx, h, args, prompt)
	if err != nil {
		return nil, err
	}
	h.resumed = true
	return r, nil
}

// ReadTurn implements harness.Harness.
func (h *Harness) ReadTurn(ctx context.Context, ref harness.Ref) ([]message.Message, error) {
	var turn turnRef
	if err := json.Unmarshal(ref.Data, &turn); err != nil {
		return nil, fmt.Errorf("claude: parse turn reference: %w", err)
	}
	if turn.SessionID == "" || turn.UserMessageUUID == "" {
		return nil, fmt.Errorf("claude: incomplete turn reference %s", ref.Data)
	}
	return readTurn(ctx, h.home(), h.cfg.WorkDir, turn)
}

// Close implements harness.Harness. No Claude Code process outlives its
// turn, so there is nothing to stop.
func (h *Harness) Close() error {
	return nil
}

// env is the process environment of a harness process.
func (h *Harness) env() []string {
	env := os.Environ()
	if h.cfg.Home != "" {
		env = append(env, "CLAUDE_CONFIG_DIR="+h.cfg.Home)
	}
	return env
}

// home is the harness home: Config.Home, else the inherited
// CLAUDE_CONFIG_DIR, else ~/.claude.
func (h *Harness) home() string {
	if h.cfg.Home != "" {
		return h.cfg.Home
	}
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

// sessionExists reports whether the session transcript exists.
func (h *Harness) sessionExists() bool {
	_, err := transcriptPath(h.home(), h.cfg.WorkDir, h.sessionID)
	return err == nil
}

func (h *Harness) observe(dir string, line []byte) {
	if h.tap != nil {
		h.tap(dir, line)
	}
}

// newUUID returns a random (version 4) UUID.
func newUUID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// EncodeWorkDir returns the name Claude Code gives the transcript directory
// of a working directory under <home>/projects: every character other than
// an ASCII letter or digit becomes '-'. Claude Code encodes the
// symlink-resolved path.
func EncodeWorkDir(dir string) string {
	b := []byte(dir)
	for i, c := range b {
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9') {
			b[i] = '-'
		}
	}
	return string(b)
}

// resolve returns dir with symlinks resolved, or dir when that fails.
func resolve(dir string) string {
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		return r
	}
	return dir
}
