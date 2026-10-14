package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/wenfengwang/cloud-agent-sdk/internal/message"
)

// rolloutFlushWait bounds how long ReadTurn waits for a turn that completed
// live to reach its rollout file, which Codex writes asynchronously.
const rolloutFlushWait = 5 * time.Second

// codexHome resolves the harness home exactly as Codex does: Config.Home,
// else CODEX_HOME, else ~/.codex.
func codexHome(home string) (string, error) {
	if home != "" {
		return home, nil
	}
	if v := os.Getenv("CODEX_HOME"); v != "" {
		return v, nil
	}
	dir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("codex: locate harness home: %w", err)
	}
	return filepath.Join(dir, ".codex"), nil
}

// rolloutPath finds <home>/sessions/<yyyy>/<mm>/<dd>/rollout-*-<thread>.jsonl.
func rolloutPath(home, thread string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(home, "sessions", "*", "*", "*", "rollout-*-"+thread+".jsonl"))
	if err != nil {
		return "", fmt.Errorf("codex: find rollout of thread %s: %w", thread, err)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("codex: no rollout of thread %s in %s", thread, home)
	}
	sort.Strings(matches)
	return matches[0], nil
}

// rolloutLine is one line of a rollout file.
type rolloutLine struct {
	Type    string `json:"type"`
	Payload struct {
		Type        string          `json:"type"`
		ThreadID    string          `json:"thread_id"`
		TurnID      string          `json:"turn_id"`
		RootTurnID  string          `json:"root_turn_id"`
		Item        json.RawMessage `json:"item"`
		StartedAt   int64           `json:"started_at_ms"`
		CompletedAt int64           `json:"completed_at_ms"`
	} `json:"payload"`
}

// Rollout event_msg payload types the reader uses.
const (
	rolloutItemCompleted = "item_completed"
	rolloutTaskStarted   = "task_started"
	rolloutTaskComplete  = "task_complete"
	rolloutTurnAborted   = "turn_aborted"
)

// readRollout reads the messages of one turn from the thread's rollout.
// When waitEnd is set, the turn is known to have ended live, and the reader
// waits up to rolloutFlushWait for its end event to be written.
func readRollout(ctx context.Context, home, thread, turn string, waitEnd bool) ([]message.Message, error) {
	deadline := time.Now().Add(rolloutFlushWait)
	for {
		msgs, ended, err := readRolloutOnce(home, thread, turn)
		if err != nil || ended || !waitEnd || time.Now().After(deadline) {
			return msgs, err
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("codex: read rollout: %w", ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// rolloutEvent is one live notification reconstructed from a rollout
// item_completed line: the item's start or its completion.
type rolloutEvent struct {
	at      int64
	seq     int
	started bool
	thread  string
	it      item
	itemRaw json.RawMessage
	line    json.RawMessage
}

// readRolloutOnce reads a turn's messages, including those of the subagents
// it started, in live order. Codex persists only item completions, each
// with the start and completion time the live item/started and
// item/completed carried, so the live sequence is rebuilt by time. ended
// reports whether the turn's end event is written.
func readRolloutOnce(home, thread, turn string) ([]message.Message, bool, error) {
	r := rolloutReader{home: home, root: turn, read: map[string]bool{}, tr: newTranslator()}
	ended, err := r.readThread(thread, "")
	if err != nil {
		return nil, false, err
	}
	orderEvents(r.events)
	var out []message.Message
	for _, e := range r.events {
		if e.started {
			out = append(out, r.tr.itemStarted(e.thread, e.it, e.itemRaw, e.line)...)
		} else {
			out = append(out, r.tr.itemCompleted(e.thread, e.it, e.itemRaw, e.line)...)
		}
	}
	return out, ended, nil
}

// orderEvents sorts events into live order: by time, then by rollout
// position. Tool items that run in parallel can start and complete in the
// same millisecond, and the rollout keeps only their completion order, so
// their live order cannot be recovered; within one millisecond, tool starts
// (calls) are placed before tool completions (results), and every other
// event keeps its place (design 001, live and read-back differences).
func orderEvents(events []rolloutEvent) {
	sort.Slice(events, func(i, j int) bool {
		if events[i].at != events[j].at {
			return events[i].at < events[j].at
		}
		return events[i].seq < events[j].seq
	})
	for start := 0; start < len(events); {
		end := start
		for end < len(events) && events[end].at == events[start].at {
			end++
		}
		var slots []int
		var tools []rolloutEvent
		for i := start; i < end; i++ {
			if isToolItem(events[i].it.Type) {
				slots = append(slots, i)
				tools = append(tools, events[i])
			}
		}
		sort.SliceStable(tools, func(i, j int) bool { return tools[i].started && !tools[j].started })
		for k, i := range slots {
			events[i] = tools[k]
		}
		start = end
	}
}

// rolloutReader collects the events of one turn across the rollouts of its
// thread and its subagent threads.
type rolloutReader struct {
	home, root string
	read       map[string]bool // threads already read
	events     []rolloutEvent
	tr         *translator
}

// readThread collects the events of thread that belong to the root turn,
// then those of the subagent threads they start. spawn is the ID of the
// subagent call that started thread, empty for the turn's own thread, whose
// events are those of the root turn itself. A subagent thread's events are
// those of its turns that the root turn caused. A tool item that completed
// after the root turn's end event yields its tool_call only: the live run
// had ended before its completion.
func (r *rolloutReader) readThread(thread, spawn string) (bool, error) {
	if r.read[thread] {
		return false, nil
	}
	r.read[thread] = true
	path, err := rolloutPath(r.home, thread)
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("codex: read rollout: %w", err)
	}
	if spawn != "" {
		// Read-back follows the subagent's items before it reaches its
		// subAgentActivity item.
		r.tr.spawns[thread] = spawn
	}
	turns := map[string]bool{}
	if spawn == "" {
		turns[r.root] = true
	}
	children := map[string]string{} // subagent thread -> spawn call ID
	ended := false
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(nil, 256<<20)
	for sc.Scan() {
		line := sc.Bytes()
		var l rolloutLine
		if err := json.Unmarshal(line, &l); err != nil {
			// A partially written last line.
			continue
		}
		if l.Type != "event_msg" {
			continue
		}
		p := l.Payload
		if spawn != "" && p.Type == rolloutTaskStarted && p.RootTurnID == r.root {
			turns[p.TurnID] = true
		}
		if !turns[p.TurnID] {
			continue
		}
		switch p.Type {
		case rolloutTaskComplete, rolloutTurnAborted:
			if spawn == "" {
				ended = true
			}
		case rolloutItemCompleted:
			if p.ThreadID != thread {
				continue
			}
			it, itemRaw, err := rolloutItem(p.Item)
			if err != nil {
				return false, fmt.Errorf("codex: %s: %w", path, err)
			}
			if it.Type == itemSubAgent && it.Kind == subAgentStarted {
				children[it.AgentThreadID] = it.ID
			}
			raw := append(json.RawMessage(nil), line...)
			n := len(r.events)
			if isToolItem(it.Type) {
				r.events = append(r.events, rolloutEvent{at: p.StartedAt, seq: n, started: true, thread: thread, it: it, itemRaw: itemRaw, line: raw})
				if ended {
					continue
				}
			}
			r.events = append(r.events, rolloutEvent{at: p.CompletedAt, seq: n + 1, thread: thread, it: it, itemRaw: itemRaw, line: raw})
		}
	}
	if err := sc.Err(); err != nil {
		return false, fmt.Errorf("codex: read rollout: %w", err)
	}
	for child, id := range children {
		if _, err := r.readThread(child, id); err != nil {
			return false, err
		}
	}
	return ended, nil
}

// rolloutItem converts a rollout TurnItem (PascalCase type, snake_case
// fields) into the app-server item it was reported as live, so that both
// paths share one translation; itemJSON is the rollout item itself. The
// conversion renames the type and the top-level fields, then fixes the
// fields whose shape differs: an argv command, AgentMessage content parts,
// Reasoning summary_text, and FileChange changes. Nested values (tool arguments and results)
// are kept as they are.
func rolloutItem(raw json.RawMessage) (item, json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return item{}, nil, fmt.Errorf("decode rollout item: %w", err)
	}
	conv := make(map[string]json.RawMessage, len(fields))
	for k, v := range fields {
		conv[camelCase(k)] = v
	}
	var typ string
	_ = json.Unmarshal(fields["type"], &typ)
	typ = lowerFirst(typ)
	conv["type"], _ = json.Marshal(typ)
	switch typ {
	case itemCommand:
		var argv []string
		if json.Unmarshal(fields["command"], &argv) == nil {
			conv["command"], _ = json.Marshal(joinArgv(argv))
		}
	case itemAgentMessage:
		var parts []struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(fields["content"], &parts)
		var text strings.Builder
		for _, p := range parts {
			text.WriteString(p.Text)
		}
		conv["text"], _ = json.Marshal(text.String())
		delete(conv, "content")
	case itemReasoning:
		conv["summary"] = fields["summary_text"]
		conv["content"] = fields["raw_content"]
	case itemFileChange:
		// A map from path to change, where the app-server reports a list
		// of {path, kind, diff}; the diff of an added file is its content.
		var changes map[string]struct {
			Type        string `json:"type"`
			Content     string `json:"content"`
			UnifiedDiff string `json:"unified_diff"`
		}
		if json.Unmarshal(fields["changes"], &changes) == nil {
			var list []fileUpdateChange
			for path, c := range changes {
				ch := fileUpdateChange{Path: path, Diff: c.UnifiedDiff}
				ch.Kind.Type = c.Type
				if c.Type != "update" {
					ch.Diff = c.Content
				}
				list = append(list, ch)
			}
			conv["changes"], _ = json.Marshal(list)
		}
	}
	data, err := json.Marshal(conv)
	if err != nil {
		return item{}, nil, fmt.Errorf("encode rollout item: %w", err)
	}
	var it item
	if err := json.Unmarshal(data, &it); err != nil {
		// A field whose rollout shape differs from the app-server one:
		// keep what identifies the item.
		it = item{Type: typ}
		_ = json.Unmarshal(fields["id"], &it.ID)
	}
	return it, raw, nil
}

// camelCase turns snake_case into camelCase.
func camelCase(s string) string {
	parts := strings.Split(s, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}
