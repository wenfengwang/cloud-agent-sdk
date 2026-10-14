package testenv

import (
	"encoding/json"
	"slices"
	"sort"

	"github.com/wenfengwang/cloud-agent-sdk/core/internal/message"
)

// SameMillisecondOrder returns msgs with every group of Codex tool calls and
// results that share one millisecond put into a canonical order: calls
// before results, each by message ID. Design 001 lets a Codex read-back
// order such a group differently from the live turn, so tests compare
// live and read-back messages after applying it to both; everything else
// keeps its order. A group is a run of consecutive tool messages with the
// same time, taken from Raw: the time of the notification's event live, and
// in a rollout line a call's start time and a result's completion time. Messages without such
// a time, as on Claude Code, form no group.
func SameMillisecondOrder(msgs []message.Message) []message.Message {
	out := slices.Clone(msgs)
	for start := 0; start < len(out); {
		at, ok := toolTime(out[start])
		end := start + 1
		for ok && end < len(out) {
			next, nextOK := toolTime(out[end])
			if !nextOK || next != at {
				break
			}
			end++
		}
		group := out[start:end]
		sort.SliceStable(group, func(i, j int) bool {
			ci, cj := group[i].Kind == message.KindToolCall, group[j].Kind == message.KindToolCall
			if ci != cj {
				return ci
			}
			return group[i].ID < group[j].ID
		})
		start = end
	}
	return out
}

// toolTime returns the Codex time of a tool call (its start) or a tool
// result (its completion), in milliseconds.
func toolTime(m message.Message) (int64, bool) {
	if m.Kind != message.KindToolCall && m.Kind != message.KindToolResult {
		return 0, false
	}
	var raw struct {
		Params struct {
			StartedAtMs   *int64 `json:"startedAtMs"`
			CompletedAtMs *int64 `json:"completedAtMs"`
		} `json:"params"`
		Payload struct {
			StartedAtMs   *int64 `json:"started_at_ms"`
			CompletedAtMs *int64 `json:"completed_at_ms"`
		} `json:"payload"`
	}
	if json.Unmarshal(m.Raw, &raw) != nil {
		return 0, false
	}
	// A notification carries the time of its own event: startedAtMs on
	// item/started (which also yields the Codex subagent result),
	// completedAtMs on item/completed.
	if t := raw.Params.StartedAtMs; t != nil {
		return *t, true
	}
	if t := raw.Params.CompletedAtMs; t != nil {
		return *t, true
	}
	t := raw.Payload.StartedAtMs
	if m.Kind == message.KindToolResult {
		t = raw.Payload.CompletedAtMs
	}
	if t == nil {
		return 0, false
	}
	return *t, true
}
