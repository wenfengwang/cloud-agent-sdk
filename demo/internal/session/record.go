package session

import (
	"sync"
	"time"

	"github.com/wenfengwang/cloud-agent-sdk/demo/internal/sdk"
	"github.com/wenfengwang/cloud-agent-sdk/demo/internal/wire"
)

// Record is one turn's message record, written by its recorder and read by
// history and every subscription. Messages are only appended.
type Record struct {
	id        string
	sessionID string
	prompt    string
	startedAt time.Time
	done      chan struct{} // closed when the recorder returns

	mu        sync.Mutex
	turnState string
	endedAt   time.Time
	finalText string
	usage     wire.Usage
	errMsg    string
	messages  []wire.Message
	index     map[string]int // message ID to position
	iterErr   string
	final     bool
	changed   chan struct{} // closed and replaced on every change
	streams   []*Partial    // open delta streams, in the order they opened
}

// Partial is the accumulated text of an assistant message still being
// streamed. Version grows with every delta.
type Partial struct {
	StreamID        string
	ParentToolUseID string
	Text            string
	Version         int
}

func newRecord(id, sessionID, prompt string, startedAt time.Time) *Record {
	return &Record{
		id:        id,
		sessionID: sessionID,
		prompt:    prompt,
		startedAt: startedAt,
		done:      make(chan struct{}),
		turnState: turnRunning,
		index:     map[string]int{},
		changed:   make(chan struct{}),
	}
}

// ID returns the turn ID.
func (r *Record) ID() string { return r.id }

func (r *Record) wake() {
	close(r.changed)
	r.changed = make(chan struct{})
}

func (r *Record) append(m wire.Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.index[m.ID] = len(r.messages)
	r.messages = append(r.messages, m)
	if m.StreamID != "" {
		r.dropStream(m.StreamID)
	}
	r.wake()
}

// delta appends d's text to its open stream.
func (r *Record) delta(d sdk.Delta) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var p *Partial
	for _, s := range r.streams {
		if s.StreamID == d.StreamID {
			p = s
		}
	}
	if p == nil {
		p = &Partial{StreamID: d.StreamID, ParentToolUseID: d.ParentToolUseID}
		r.streams = append(r.streams, p)
	}
	p.Text += d.Text
	p.Version++
	r.wake()
}

func (r *Record) dropStream(id string) {
	for i, s := range r.streams {
		if s.StreamID == id {
			r.streams = append(r.streams[:i:i], r.streams[i+1:]...)
			return
		}
	}
}

// Partials returns a copy of the open streams, in the order they opened.
func (r *Record) Partials() []Partial {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Partial, len(r.streams))
	for i, s := range r.streams {
		out[i] = *s
	}
	return out
}

func (r *Record) fail(message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.iterErr = message
}

func (r *Record) setOutcome(status string, endedAt time.Time, res sdk.Result, errMsg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.turnState, r.endedAt, r.finalText, r.usage, r.errMsg = status, endedAt, res.FinalText, wire.ConvertUsage(res.Usage), errMsg
}

func (r *Record) finalize() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.final = true
	r.streams = nil // a stream the turn left incomplete is dropped
	r.wake()
}

func (r *Record) status() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.turnState
}

// Since returns the messages from position pos on, whether the record is
// final, and a channel closed on the next change. Positions are internal
// and never sent to clients.
func (r *Record) Since(pos int) (msgs []wire.Message, final bool, wake <-chan struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if pos < len(r.messages) {
		msgs = r.messages[pos:len(r.messages):len(r.messages)]
	}
	return msgs, r.final, r.changed
}

// After returns the position just after message id, or 0 when id is empty
// or not in the record.
func (r *Record) After(id string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i, ok := r.index[id]; ok {
		return i + 1
	}
	return 0
}

// IterErr returns the recorder's iteration error, or "".
func (r *Record) IterErr() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.iterErr
}

// Summary returns the turn's TurnSummary.
func (r *Record) Summary() wire.TurnSummary {
	r.mu.Lock()
	defer r.mu.Unlock()
	sum := wire.TurnSummary{ID: r.id, SessionID: r.sessionID, Prompt: r.prompt, Status: r.turnState, StartedAt: wire.Time(r.startedAt)}
	if r.turnState != turnRunning {
		ended, text, usage := wire.Time(r.endedAt), r.finalText, r.usage
		sum.EndedAt, sum.FinalText, sum.Usage = &ended, &text, &usage
		if r.turnState == turnFailed {
			sum.Error = &wire.ErrorMessage{Message: r.errMsg}
		}
	}
	return sum
}
