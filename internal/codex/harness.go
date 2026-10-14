// Package codex is the Codex harness adapter. It implements harness.Harness
// over one long-lived "codex app-server" process per agent runtime, speaking
// the JSON-RPC protocol of schema/app-server.v2.schema.json. Behavior is
// owned by design/001-core-api.md.
package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/wenfengwang/cloud-agent-sdk/internal/harness"
	"github.com/wenfengwang/cloud-agent-sdk/internal/message"
)

// clientName is the clientInfo.name sent in initialize. Codex uses it as the
// originator of its model requests, and some providers accept only the
// originators of official Codex clients, so the adapter sends the
// originator the pinned CLI uses by default.
const (
	clientName    = "codex_cli_rs"
	clientTitle   = "cloudagent"
	clientVersion = "0.1.0"
)

// Thread settings of design 001: no permission prompts.
const (
	approvalPolicy = "never"
	sandboxMode    = "danger-full-access"
)

// reasoningSummary is the reasoning summary turn/start requests, so that
// reasoning items carry text (design 001).
const reasoningSummary = "auto"

var errClosed = errors.New("codex: adapter closed")

// Harness drives Codex for one agent runtime.
type Harness struct {
	cfg harness.Config
	tap func(process int, dir string, line []byte) // recording tests only
	// config holds configuration overrides sent with thread/start and
	// thread/resume. Only tests set it (design 002 enables apply_patch for
	// recording); production runs send none.
	config map[string]any

	mu        sync.Mutex
	srv       *appServer
	processes int
	thread    string // the conversation; empty until the first thread/start
	loaded    bool   // the thread is started or resumed on srv
	closed    bool

	// endedMu guards ended apart from mu, which StartTurn holds while it
	// waits on the app-server, so that ReadTurn never waits for a start.
	endedMu sync.Mutex
	ended   map[string]bool // turns observed to complete, by turn ID
}

var _ harness.Harness = (*Harness)(nil)

// New returns an adapter for cfg. It starts no process.
func New(cfg harness.Config) *Harness {
	return &Harness{cfg: cfg, ended: map[string]bool{}}
}

// StartTurn implements harness.Harness. It starts the app-server if none is
// running (the first turn, or after a Kill or crash), creates the thread on
// the first turn or resumes it on a new process, and returns once
// turn/start has returned.
func (h *Harness) StartTurn(ctx context.Context, prompt string) (harness.Run, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, errClosed
	}
	if h.srv != nil && h.srv.dead() {
		h.srv.close()
		h.srv = nil
	}
	if h.srv == nil {
		srv, err := h.startServer(ctx)
		if err != nil {
			return nil, err
		}
		h.srv = srv
		h.loaded = false
	}
	if !h.loaded {
		if err := h.loadThread(ctx); err != nil {
			h.dropServer()
			return nil, err
		}
		h.loaded = true
	}
	params := map[string]any{
		"threadId": h.thread,
		"input":    []map[string]any{{"type": "text", "text": prompt, "text_elements": []any{}}},
		"summary":  reasoningSummary,
	}
	result, err := h.srv.request(ctx, "turn/start", params)
	if err != nil {
		var rpcErr *rpcError
		if !errors.As(err, &rpcErr) {
			// The turn may have started without our knowing it; a new
			// process resumes the thread from a known state.
			h.dropServer()
		}
		return nil, err
	}
	var resp struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		h.dropServer()
		return nil, fmt.Errorf("codex: decode turn/start response: %w", err)
	}
	if resp.Turn.ID == "" {
		h.dropServer()
		return nil, fmt.Errorf("codex: turn/start returned no turn id: %s", result)
	}
	return newRun(h, h.srv, h.thread, resp.Turn.ID), nil
}

func (h *Harness) startServer(ctx context.Context) (*appServer, error) {
	h.processes++
	var tap tapFunc
	if h.tap != nil {
		n, f := h.processes, h.tap
		tap = func(dir string, line []byte) { f(n, dir, line) }
	}
	srv, err := startAppServer(h.cfg.WorkDir, h.cfg.Home, tap)
	if err != nil {
		return nil, err
	}
	params := map[string]any{"clientInfo": map[string]any{"name": clientName, "title": clientTitle, "version": clientVersion}}
	if _, err := srv.request(ctx, "initialize", params); err != nil {
		srv.kill()
		srv.close()
		return nil, err
	}
	if err := srv.notify("initialized"); err != nil {
		srv.kill()
		srv.close()
		return nil, err
	}
	return srv, nil
}

// loadThread starts the conversation's thread on the first turn and resumes
// it on every later process.
func (h *Harness) loadThread(ctx context.Context) error {
	params := map[string]any{
		"cwd":            h.cfg.WorkDir,
		"approvalPolicy": approvalPolicy,
		"sandbox":        sandboxMode,
	}
	if h.cfg.Model != "" {
		params["model"] = h.cfg.Model
	}
	if h.config != nil {
		params["config"] = h.config
	}
	method := "thread/start"
	if h.thread != "" {
		method = "thread/resume"
		params["threadId"] = h.thread
	}
	result, err := h.srv.request(ctx, method, params)
	if err != nil {
		return err
	}
	var resp struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return fmt.Errorf("codex: decode %s response: %w", method, err)
	}
	if resp.Thread.ID == "" {
		return fmt.Errorf("codex: %s returned no thread id", method)
	}
	h.thread = resp.Thread.ID
	return nil
}

// dropServer kills and reaps the current app-server. h.mu is held.
func (h *Harness) dropServer() {
	if h.srv == nil {
		return
	}
	h.srv.kill()
	h.srv.close()
	h.srv = nil
}

// markEnded records that a turn completed while its app-server ran, so that
// ReadTurn waits for its end to reach the rollout.
func (h *Harness) markEnded(turn string) {
	h.endedMu.Lock()
	h.ended[turn] = true
	h.endedMu.Unlock()
}

func (h *Harness) completedLive(turn string) bool {
	h.endedMu.Lock()
	defer h.endedMu.Unlock()
	return h.ended[turn]
}

// ReadTurn implements harness.Harness by reading the thread's rollout file
// in the harness home.
func (h *Harness) ReadTurn(ctx context.Context, ref harness.Ref) ([]message.Message, error) {
	var data struct {
		ThreadID string `json:"thread_id"`
		TurnID   string `json:"turn_id"`
	}
	if err := json.Unmarshal(ref.Data, &data); err != nil {
		return nil, fmt.Errorf("codex: parse turn reference: %w", err)
	}
	if data.ThreadID == "" || data.TurnID == "" {
		return nil, fmt.Errorf("codex: incomplete turn reference %s", ref.Data)
	}
	home, err := codexHome(h.cfg.Home)
	if err != nil {
		return nil, err
	}
	return readRollout(ctx, home, data.ThreadID, data.TurnID, h.completedLive(data.TurnID))
}

// Close implements harness.Harness: it stops the app-server, if any.
func (h *Harness) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	if h.srv != nil {
		h.srv.close()
		h.srv = nil
	}
	return nil
}
