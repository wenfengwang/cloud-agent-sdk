// Package codex is the Codex harness adapter. It implements harness.Harness;
// behavior is owned by design/001-core-api.md.
package codex

import (
	"context"
	"errors"

	"github.com/wenfengwang/cloud-agent-sdk/internal/harness"
	"github.com/wenfengwang/cloud-agent-sdk/internal/message"
)

var errNotImplemented = errors.New("codex: adapter not implemented")

// Harness drives Codex for one agent runtime.
type Harness struct {
	cfg harness.Config
}

var _ harness.Harness = (*Harness)(nil)

// New returns an adapter for cfg. It starts no process.
func New(cfg harness.Config) *Harness {
	return &Harness{cfg: cfg}
}

// StartTurn implements harness.Harness.
func (h *Harness) StartTurn(ctx context.Context, prompt string) (harness.Run, error) {
	return nil, errNotImplemented
}

// ReadTurn implements harness.Harness.
func (h *Harness) ReadTurn(ctx context.Context, ref harness.Ref) ([]message.Message, error) {
	return nil, errNotImplemented
}

// Close implements harness.Harness.
func (h *Harness) Close() error {
	return nil
}
