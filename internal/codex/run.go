package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/wenfengwang/cloud-agent-sdk/internal/harness"
	"github.com/wenfengwang/cloud-agent-sdk/internal/message"
)

// Notification methods the run reads. Every other notification, including
// configWarning, warning, and error (retries and warnings alike), is
// ignored: the turn's outcome is taken from turn/completed alone.
const (
	methodItemStarted   = "item/started"
	methodItemCompleted = "item/completed"
	methodTokenUsage    = "thread/tokenUsage/updated"
	methodTurnCompleted = "turn/completed"
)

// Turn statuses of turn/completed.
const (
	turnCompleted   = "completed"
	turnInterrupted = "interrupted"
	turnFailed      = "failed"
)

// notification holds the fields of every notification the run reads.
type notification struct {
	Method string `json:"method"`
	Params struct {
		ThreadID   string          `json:"threadId"`
		TurnID     string          `json:"turnId"`
		Item       json.RawMessage `json:"item"`
		TokenUsage *struct {
			Last tokenUsage `json:"last"`
		} `json:"tokenUsage"`
		Turn *struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"turn"`
	} `json:"params"`
}

type tokenUsage struct {
	InputTokens           int64 `json:"inputTokens"`
	CachedInputTokens     int64 `json:"cachedInputTokens"`
	CacheWriteInputTokens int64 `json:"cacheWriteInputTokens"`
	OutputTokens          int64 `json:"outputTokens"`
}

// run is one accepted turn on an app-server.
type run struct {
	h      *Harness
	srv    *appServer
	thread string
	turn   string
	ref    harness.Ref

	msgs        chan message.Message
	interrupt   sync.Once
	interrupted chan struct{} // closed by the first Interrupt
	done        chan struct{}
	outcome     harness.Outcome
	err         error
}

var _ harness.Run = (*run)(nil)

func newRun(h *Harness, srv *appServer, thread, turn string) *run {
	data, _ := json.Marshal(map[string]string{"thread_id": thread, "turn_id": turn})
	r := &run{
		h:           h,
		srv:         srv,
		thread:      thread,
		turn:        turn,
		ref:         harness.Ref{SessionID: thread, Data: data},
		msgs:        make(chan message.Message, 64),
		interrupted: make(chan struct{}),
		done:        make(chan struct{}),
	}
	go r.loop()
	return r
}

func (r *run) Ref() harness.Ref                 { return r.ref }
func (r *run) Messages() <-chan message.Message { return r.msgs }

// Interrupt sends turn/interrupt once, without waiting for its response.
// Once the interrupted turn has ended, the run shuts the app-server down
// before it ends (see loop).
func (r *run) Interrupt() {
	r.interrupt.Do(func() {
		close(r.interrupted)
		go func() {
			_, _ = r.srv.request(context.Background(), "turn/interrupt", map[string]string{"threadId": r.thread, "turnId": r.turn})
		}()
	})
}

// Kill kills the app-server. The next StartTurn starts a new one that
// resumes the thread.
func (r *run) Kill() { r.srv.kill() }

func (r *run) Wait() (harness.Outcome, error) {
	<-r.done
	return r.outcome, r.err
}

// loop reads the app-server's notifications until this turn completes or
// the process ends.
func (r *run) loop() {
	defer close(r.done)
	defer close(r.msgs)
	st := newStream(r.thread, r.turn)
	for {
		line, ok := r.srv.next()
		if !ok {
			r.outcome.Usage = st.usage
			r.err = fmt.Errorf("codex: turn %s did not complete: %w", r.turn, r.srv.err())
			return
		}
		msgs, done := st.handle(line)
		for _, m := range msgs {
			r.msgs <- m
		}
		if done {
			r.outcome.Usage = st.usage
			r.err = st.err
			r.h.markEnded(r.turn)
			r.shutdownIfStopped()
			return
		}
	}
}

// shutdownIfStopped shuts the app-server down after an interrupted turn
// (design 001, "Graceful stop"). Codex leaves the turn's running commands
// alive after turn/interrupt and ends them only when the app-server shuts
// down, which also flushes the rollout; so the run ends only once the
// process is gone. The next StartTurn starts a new app-server that resumes
// the thread.
func (r *run) shutdownIfStopped() {
	select {
	case <-r.interrupted:
		r.srv.close()
	default:
	}
}

// stream translates the notifications of one turn: those of the turn on its
// thread and those of the subagent threads the turn starts. Notifications
// of other threads and turns are ignored.
type stream struct {
	thread, turn string
	tr           *translator
	usage        harness.Usage
	err          error // the turn's outcome once handle reported done
	protoErr     error
}

func newStream(thread, turn string) *stream {
	return &stream{thread: thread, turn: turn, tr: newTranslator()}
}

// handle translates one notification line. done reports that the turn
// completed; st.err then holds its outcome.
func (st *stream) handle(line []byte) (msgs []message.Message, done bool) {
	var n notification
	if err := json.Unmarshal(line, &n); err != nil {
		return nil, false
	}
	own := n.Params.ThreadID == st.thread
	if !own && !st.tr.follows(n.Params.ThreadID) {
		return nil, false
	}
	switch n.Method {
	case methodItemStarted, methodItemCompleted:
		if own && n.Params.TurnID != st.turn {
			return nil, false
		}
		var it item
		if err := json.Unmarshal(n.Params.Item, &it); err != nil {
			if st.protoErr == nil {
				st.protoErr = fmt.Errorf("codex: decode %s: %w", n.Method, err)
			}
			return nil, false
		}
		raw := append(json.RawMessage(nil), line...)
		if n.Method == methodItemStarted {
			return st.tr.itemStarted(n.Params.ThreadID, it, n.Params.Item, raw), false
		}
		return st.tr.itemCompleted(n.Params.ThreadID, it, n.Params.Item, raw), false
	case methodTokenUsage:
		// Usage counts the turn's own thread; Codex reports usage of
		// cached input as part of the input (design 001).
		if !own || n.Params.TurnID != st.turn || n.Params.TokenUsage == nil {
			return nil, false
		}
		u := n.Params.TokenUsage.Last
		st.usage.InputTokens += u.InputTokens - u.CachedInputTokens
		st.usage.OutputTokens += u.OutputTokens
		st.usage.CacheReadTokens += u.CachedInputTokens
		st.usage.CacheWriteTokens += u.CacheWriteInputTokens
	case methodTurnCompleted:
		t := n.Params.Turn
		if !own || t == nil || t.ID != st.turn {
			return nil, false
		}
		switch t.Status {
		case turnFailed:
			msg := "no error reported"
			if t.Error != nil {
				msg = t.Error.Message
			}
			st.err = fmt.Errorf("codex: turn %s failed: %s", st.turn, msg)
		case turnCompleted, turnInterrupted:
			st.err = st.protoErr
		default:
			st.err = fmt.Errorf("codex: turn %s ended with status %q", st.turn, t.Status)
		}
		return nil, true
	}
	return nil, false
}
