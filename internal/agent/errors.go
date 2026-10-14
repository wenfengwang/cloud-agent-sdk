// Package agent implements the harness-neutral part of design 001: the turn
// lifecycle, the single-active-turn rule, the stop path, the in-memory message
// buffer with replay from the first message, read-back delegation for
// finished turns, and the ~/.cloudagent index. Package cloudagent wraps it.
package agent

import "errors"

// The sentinel errors of design 001. Package cloudagent re-exports these
// values.
var (
	ErrTurnInProgress = errors.New("cloudagent: a turn is already in progress")
	ErrTerminated     = errors.New("cloudagent: turn terminated")
	ErrClosed         = errors.New("cloudagent: agent closed")
)
