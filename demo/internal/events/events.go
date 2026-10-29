// Package events serves the one SSE stream of each browser tab. A
// connection carries every session change and the events of the turns it
// subscribed to. Each subscription has a pump that reads the turn's record;
// one writer per connection writes everything to the response.
package events

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/wenfengwang/cloud-agent-sdk/demo/internal/session"
	"github.com/wenfengwang/cloud-agent-sdk/demo/internal/wire"
)

// DefaultKeepalive is the silence after which a keepalive comment is sent.
const DefaultKeepalive = 15 * time.Second

// changeBuffer is how many session changes a connection may fall behind
// before it is dropped; the client then reconnects and refetches.
const changeBuffer = 256

// Config configures a Hub.
type Config struct {
	Store     *session.Store
	Keepalive time.Duration // 0 means DefaultKeepalive
}

// Hub holds every open event connection.
type Hub struct {
	cfg      Config
	shutdown chan struct{}
	once     sync.Once

	mu    sync.Mutex
	conns map[string]*conn
}

type event struct {
	name string
	data any
}

type conn struct {
	id      string
	out     chan event    // turn events from pumps, in record order per turn
	changes chan event    // session changes, never blocking the publisher
	dropped chan struct{} // closed when changes overflowed
	done    chan struct{} // closed when the handler returns
	drop    sync.Once

	mu   sync.Mutex
	subs map[string]chan struct{} // turn ID to the subscription's done
}

// NewHub returns a Hub.
func NewHub(cfg Config) *Hub {
	if cfg.Keepalive <= 0 {
		cfg.Keepalive = DefaultKeepalive
	}
	return &Hub{cfg: cfg, shutdown: make(chan struct{}), conns: map[string]*conn{}}
}

// Shutdown ends every stream and pump. It is idempotent.
func (h *Hub) Shutdown() {
	h.once.Do(func() { close(h.shutdown) })
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// ServeHTTP serves GET /api/events.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	select {
	case <-h.shutdown:
		http.Error(w, "shutting down", http.StatusServiceUnavailable)
		return
	default:
	}

	c := &conn{
		id:      newID(),
		out:     make(chan event),
		changes: make(chan event, changeBuffer),
		dropped: make(chan struct{}),
		done:    make(chan struct{}),
		subs:    map[string]chan struct{}{},
	}
	h.mu.Lock()
	h.conns[c.id] = c
	h.mu.Unlock()
	unsubscribe := h.cfg.Store.Subscribe(func(ch session.Change) {
		ev := event{name: "session.upserted", data: ch.Session}
		if ch.Session == nil {
			ev = event{name: "session.deleted", data: map[string]string{"id": ch.DeletedID}}
		}
		select {
		case c.changes <- ev:
		default:
			c.drop.Do(func() { close(c.dropped) })
		}
	})
	defer func() {
		unsubscribe()
		h.mu.Lock()
		delete(h.conns, c.id)
		h.mu.Unlock()
		close(c.done)
	}()

	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	if _, err := fmt.Fprint(w, "retry: 3000\n\n"); err != nil {
		return
	}
	if err := write(w, event{name: "hello", data: map[string]string{"connectionId": c.id}}); err != nil {
		return
	}
	flusher.Flush()

	keepalive := time.NewTimer(h.cfg.Keepalive)
	defer keepalive.Stop()
	for {
		var err error
		select {
		case <-r.Context().Done():
			return
		case <-h.shutdown:
			return
		case <-c.dropped:
			return
		case <-keepalive.C:
			_, err = fmt.Fprint(w, ": keepalive\n\n")
		case ev := <-c.changes:
			err = write(w, ev)
		case ev := <-c.out:
			err = write(w, ev)
		}
		if err != nil {
			return
		}
		flusher.Flush()
		keepalive.Reset(h.cfg.Keepalive)
	}
}

func write(w http.ResponseWriter, ev event) error {
	data, err := json.Marshal(ev.data)
	if err != nil {
		return fmt.Errorf("encode %s: %w", ev.name, err)
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.name, data)
	return err
}

func (h *Hub) conn(id string) (*conn, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.conns[id]
	if !ok {
		return nil, &session.Error{Code: session.CodeNotFound, Message: "unknown event connection " + id}
	}
	return c, nil
}

// Subscribe delivers turnID's messages after afterMessageID (all of them
// when it is empty or unknown), then new ones, then stream.error if the
// recorder's iteration failed, then turn.ended. Subscribing to a turn the
// connection already follows is a no-op.
func (h *Hub) Subscribe(connID, sessionID, turnID, afterMessageID string) error {
	c, err := h.conn(connID)
	if err != nil {
		return err
	}
	rec, err := h.cfg.Store.Record(sessionID, turnID)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.subs[turnID]; ok {
		return nil
	}
	done := make(chan struct{})
	c.subs[turnID] = done
	go h.pump(c, rec, done, rec.After(afterMessageID))
	return nil
}

// Unsubscribe stops a subscription. It is a no-op when not subscribed.
func (h *Hub) Unsubscribe(connID, turnID string) error {
	c, err := h.conn(connID)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if done, ok := c.subs[turnID]; ok {
		close(done)
		delete(c.subs, turnID)
	}
	return nil
}

// pump sends one turn's events to c from position pos. Every send and every
// wait also selects on the subscription, the connection, and the hub, so
// the pump exits as soon as any of them is gone.
func (h *Hub) pump(c *conn, rec *session.Record, done chan struct{}, pos int) {
	defer func() {
		c.mu.Lock()
		if c.subs[rec.ID()] == done {
			delete(c.subs, rec.ID())
		}
		c.mu.Unlock()
	}()
	send := func(ev event) bool {
		select {
		case c.out <- ev:
			return true
		case <-done:
		case <-c.done:
		case <-h.shutdown:
		}
		return false
	}
	for {
		msgs, final, wake := rec.Since(pos)
		for _, m := range msgs {
			if !send(event{name: "message", data: messageEvent{TurnID: rec.ID(), Message: m}}) {
				return
			}
		}
		pos += len(msgs)
		if final {
			if e := rec.IterErr(); e != "" {
				if !send(event{name: "stream.error", data: map[string]string{"turnId": rec.ID(), "message": e}}) {
					return
				}
			}
			send(event{name: "turn.ended", data: rec.Summary()})
			return
		}
		select {
		case <-wake:
		case <-done:
			return
		case <-c.done:
			return
		case <-h.shutdown:
			return
		}
	}
}

type messageEvent struct {
	TurnID  string       `json:"turnId"`
	Message wire.Message `json:"message"`
}
