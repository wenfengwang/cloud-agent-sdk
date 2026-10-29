package events

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/wenfengwang/cloud-agent-sdk/demo/internal/sdk"
	"github.com/wenfengwang/cloud-agent-sdk/demo/internal/sdk/sdkfake"
	"github.com/wenfengwang/cloud-agent-sdk/demo/internal/session"
	"github.com/wenfengwang/cloud-agent-sdk/demo/internal/wire"
)

const wait = 5 * time.Second

type fixture struct {
	t       *testing.T
	factory *sdkfake.Factory
	store   *session.Store
	hub     *Hub
	server  *httptest.Server
}

func newFixture(t *testing.T, opts ...func(*Config)) *fixture {
	t.Helper()
	f := &fixture{t: t, factory: &sdkfake.Factory{}}
	f.store = session.NewStore(session.Config{Factory: f.factory, WorktreeRoot: t.TempDir()})
	cfg := Config{Store: f.store}
	for _, o := range opts {
		o(&cfg)
	}
	f.hub = NewHub(cfg)
	f.server = httptest.NewServer(http.HandlerFunc(f.hub.ServeHTTP))
	t.Cleanup(func() {
		f.hub.Shutdown()
		f.server.Close()
	})
	return f
}

// startTurn creates a session and starts one turn on it.
func (f *fixture) startTurn() (sessionID, turnID string, turn *sdkfake.Turn) {
	f.t.Helper()
	d, err := f.store.Create(context.Background(), wire.CreateSession{Harness: "codex", WorkDir: f.t.TempDir()})
	if err != nil {
		f.t.Fatal(err)
	}
	ts, err := f.store.StartTurn(d.ID, "go")
	if err != nil {
		f.t.Fatal(err)
	}
	rs := f.factory.Runtimes()
	turns := rs[len(rs)-1].Turns()
	return d.ID, ts.ID, turns[len(turns)-1]
}

type sseEvent struct {
	name string
	data string
}

type client struct {
	t      *testing.T
	id     string
	cancel context.CancelFunc
	events chan sseEvent
	retry  string
}

func (f *fixture) connect() *client {
	f.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.server.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		f.t.Fatalf("Content-Type = %q", ct)
	}
	c := &client{t: f.t, cancel: cancel, events: make(chan sseEvent, 1024)}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	go func() {
		defer resp.Body.Close()
		defer close(c.events)
		var ev sseEvent
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case line == "":
				if ev.name != "" {
					c.events <- ev
				}
				ev = sseEvent{}
			case strings.HasPrefix(line, "retry: "):
				c.events <- sseEvent{name: "retry", data: strings.TrimPrefix(line, "retry: ")}
			case strings.HasPrefix(line, ": "):
				c.events <- sseEvent{name: "comment", data: strings.TrimPrefix(line, ": ")}
			case strings.HasPrefix(line, "event: "):
				ev.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				ev.data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	f.t.Cleanup(cancel)
	if got := c.next(); got.name != "retry" || got.data != "3000" {
		f.t.Fatalf("first line = %+v, want retry: 3000", got)
	}
	hello := c.expect("hello")
	var h struct{ ConnectionID string }
	if err := json.Unmarshal([]byte(hello.data), &h); err != nil || h.ConnectionID == "" {
		f.t.Fatalf("hello = %s", hello.data)
	}
	c.id = h.ConnectionID
	return c
}

func (c *client) next() sseEvent {
	c.t.Helper()
	select {
	case ev, ok := <-c.events:
		if !ok {
			c.t.Fatal("stream ended")
		}
		return ev
	case <-time.After(wait):
		c.t.Fatal("timed out waiting for an event")
	}
	return sseEvent{}
}

// expect skips session and comment events until one named name arrives.
func (c *client) expect(name string) sseEvent {
	c.t.Helper()
	for {
		ev := c.next()
		if ev.name == name {
			return ev
		}
		if ev.name != "session.upserted" && ev.name != "comment" {
			c.t.Fatalf("got %s %s, want %s", ev.name, ev.data, name)
		}
	}
}

type gotMessage struct {
	TurnID  string       `json:"turnId"`
	Message wire.Message `json:"message"`
}

func (c *client) expectMessage() gotMessage {
	c.t.Helper()
	ev := c.expect("message")
	var m gotMessage
	if err := json.Unmarshal([]byte(ev.data), &m); err != nil {
		c.t.Fatal(err)
	}
	return m
}

func TestReplayThenLiveThenEnded(t *testing.T) {
	f := newFixture(t)
	sid, tid, turn := f.startTurn()
	turn.EmitText("a")
	turn.EmitText("b")
	waitRecorded(t, f.store, sid, tid, 2)

	c := f.connect()
	if err := f.hub.Subscribe(c.id, sid, tid, ""); err != nil {
		t.Fatal(err)
	}
	if m := c.expectMessage(); m.TurnID != tid || *m.Message.Text != "a" {
		t.Fatalf("first = %+v", m)
	}
	if m := c.expectMessage(); *m.Message.Text != "b" {
		t.Fatalf("second = %+v", m)
	}
	turn.EmitText("c")
	if m := c.expectMessage(); *m.Message.Text != "c" {
		t.Fatalf("live = %+v", m)
	}
	turn.End(sdk.Result{FinalText: "c"}, nil)
	ended := c.expect("turn.ended")
	var ts wire.TurnSummary
	if err := json.Unmarshal([]byte(ended.data), &ts); err != nil || ts.Status != "completed" || ts.ID != tid {
		t.Fatalf("turn.ended = %s", ended.data)
	}
}

func TestAfterMessageID(t *testing.T) {
	f := newFixture(t)
	sid, tid, turn := f.startTurn()
	for _, s := range []string{"a", "b", "c"} {
		turn.EmitText(s)
	}
	waitRecorded(t, f.store, sid, tid, 3)
	turn.End(sdk.Result{}, nil)

	c := f.connect()
	if err := f.hub.Subscribe(c.id, sid, tid, turn.ID()+"-msg-2"); err != nil {
		t.Fatal(err)
	}
	if m := c.expectMessage(); *m.Message.Text != "c" {
		t.Fatalf("after msg-2 = %+v, want c", m)
	}
	c.expect("turn.ended")

	// An unknown ID replays everything.
	c2 := f.connect()
	if err := f.hub.Subscribe(c2.id, sid, tid, "unknown"); err != nil {
		t.Fatal(err)
	}
	if m := c2.expectMessage(); *m.Message.Text != "a" {
		t.Fatalf("unknown after = %+v, want a", m)
	}
}

func TestSubscribeTwiceIsNoOp(t *testing.T) {
	f := newFixture(t)
	sid, tid, turn := f.startTurn()
	turn.EmitText("a")
	c := f.connect()
	for i := 0; i < 2; i++ {
		if err := f.hub.Subscribe(c.id, sid, tid, ""); err != nil {
			t.Fatal(err)
		}
	}
	c.expectMessage()
	turn.End(sdk.Result{}, nil)
	c.expect("turn.ended") // not a second "a"
}

func TestIdenticalReceivedAt(t *testing.T) {
	f := newFixture(t)
	sid, tid, turn := f.startTurn()
	a, b := f.connect(), f.connect()
	for _, c := range []*client{a, b} {
		if err := f.hub.Subscribe(c.id, sid, tid, ""); err != nil {
			t.Fatal(err)
		}
	}
	turn.EmitText("x")
	ma, mb := a.expectMessage(), b.expectMessage()
	if ma.Message.ID != mb.Message.ID || time.Time(ma.Message.ReceivedAt) != time.Time(mb.Message.ReceivedAt) {
		t.Fatalf("readers differ: %+v vs %+v", ma.Message, mb.Message)
	}
}

func TestStreamErrorThenEnded(t *testing.T) {
	f := newFixture(t)
	sid, tid, turn := f.startTurn()
	c := f.connect()
	if err := f.hub.Subscribe(c.id, sid, tid, ""); err != nil {
		t.Fatal(err)
	}
	turn.EmitText("a")
	c.expectMessage()
	turn.FailIteration(errors.New("transcript unreadable"))
	turn.End(sdk.Result{}, nil)
	se := c.expect("stream.error")
	if !strings.Contains(se.data, "transcript unreadable") || !strings.Contains(se.data, tid) {
		t.Fatalf("stream.error = %s", se.data)
	}
	c.expect("turn.ended")
}

func TestSessionEventsReachEveryTab(t *testing.T) {
	f := newFixture(t)
	other := f.connect()
	sid, tid, _ := f.startTurn()
	// The second tab learns the running turn from activeTurnId.
	for {
		ev := other.expect("session.upserted")
		var s wire.SessionSummary
		if err := json.Unmarshal([]byte(ev.data), &s); err != nil {
			t.Fatal(err)
		}
		if s.ID == sid && s.ActiveTurnID != nil && *s.ActiveTurnID == tid {
			break
		}
	}
	if _, err := f.store.Delete(context.Background(), sid); err != nil {
		t.Fatal(err)
	}
	for {
		ev := other.next()
		if ev.name == "session.deleted" {
			if !strings.Contains(ev.data, sid) {
				t.Fatalf("session.deleted = %s", ev.data)
			}
			break
		}
	}
}

func TestSubscribeErrors(t *testing.T) {
	f := newFixture(t)
	sid, tid, _ := f.startTurn()
	c := f.connect()
	for name, err := range map[string]error{
		"unknown connection": f.hub.Subscribe("nope", sid, tid, ""),
		"unknown session":    f.hub.Subscribe(c.id, "nope", tid, ""),
		"unknown turn":       f.hub.Subscribe(c.id, sid, "nope", ""),
	} {
		var e *session.Error
		if !errors.As(err, &e) || e.Code != session.CodeNotFound {
			t.Errorf("%s: err = %v, want not_found", name, err)
		}
	}
	if err := f.hub.Unsubscribe(c.id, "not-subscribed"); err != nil {
		t.Errorf("unsubscribe not subscribed: %v", err)
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	f := newFixture(t)
	sid, tid, turn := f.startTurn()
	c := f.connect()
	if err := f.hub.Subscribe(c.id, sid, tid, ""); err != nil {
		t.Fatal(err)
	}
	turn.EmitText("a")
	c.expectMessage()
	if err := f.hub.Unsubscribe(c.id, tid); err != nil {
		t.Fatal(err)
	}
	turn.EmitText("b")
	turn.End(sdk.Result{}, nil)
	deadline := time.After(200 * time.Millisecond)
	for {
		select {
		case ev := <-c.events:
			if ev.name == "message" || ev.name == "turn.ended" {
				t.Fatalf("got %s after unsubscribe", ev.name)
			}
		case <-deadline:
			return
		}
	}
}

func TestKeepalive(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.Keepalive = 20 * time.Millisecond })
	c := f.connect()
	if ev := c.next(); ev.name != "comment" || ev.data != "keepalive" {
		t.Fatalf("got %+v, want keepalive", ev)
	}
}

func TestDisconnectReleasesGoroutines(t *testing.T) {
	f := newFixture(t)
	sid, tid, turn := f.startTurn()
	waitGoroutines := func(want int) {
		t.Helper()
		deadline := time.Now().Add(wait)
		for runtime.NumGoroutine() > want {
			if time.Now().After(deadline) {
				buf := make([]byte, 1<<20)
				t.Fatalf("goroutines = %d, want <= %d\n%s", runtime.NumGoroutine(), want, buf[:runtime.Stack(buf, true)])
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	// Let the client transport settle before measuring.
	warm := f.connect()
	warm.cancel()
	time.Sleep(50 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	c := f.connect()
	if err := f.hub.Subscribe(c.id, sid, tid, ""); err != nil {
		t.Fatal(err)
	}
	turn.EmitText("a")
	c.expectMessage()
	c.cancel()
	turn.EmitText("b") // the pump must exit, not block on a gone connection
	waitRecorded(t, f.store, sid, tid, 2)
	waitGoroutines(baseline)
}

func TestShutdownEndsStreams(t *testing.T) {
	f := newFixture(t)
	c := f.connect()
	f.hub.Shutdown()
	deadline := time.After(wait)
	for {
		select {
		case _, ok := <-c.events:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("stream still open after Shutdown")
		}
	}
}

func waitRecorded(t *testing.T, store *session.Store, sid, tid string, n int) {
	t.Helper()
	rec, err := store.Record(sid, tid)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(wait)
	for {
		msgs, _, _ := rec.Since(0)
		if len(msgs) >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("recorded %d messages, want %d", len(msgs), n)
		}
		time.Sleep(time.Millisecond)
	}
}

type gotPartial struct {
	TurnID          string `json:"turnId"`
	StreamID        string `json:"streamId"`
	ParentToolUseID string `json:"parentToolUseId"`
	Text            string `json:"text"`
}

func (c *client) expectPartial() gotPartial {
	c.t.Helper()
	ev := c.expect("message.partial")
	var p gotPartial
	if err := json.Unmarshal([]byte(ev.data), &p); err != nil {
		c.t.Fatal(err)
	}
	return p
}

func TestPartialSnapshots(t *testing.T) {
	f := newFixture(t)
	sid, tid, turn := f.startTurn()
	c := f.connect()
	if err := f.hub.Subscribe(c.id, sid, tid, ""); err != nil {
		t.Fatal(err)
	}
	turn.EmitDelta("s1", "Hel")
	if p := c.expectPartial(); p.TurnID != tid || p.StreamID != "s1" || p.Text != "Hel" {
		t.Fatalf("first partial = %+v", p)
	}
	turn.EmitDelta("s1", "lo")
	if p := c.expectPartial(); p.Text != "Hello" {
		t.Fatalf("second partial = %+v, want the accumulated text", p)
	}
	turn.EmitCompleting(sdk.Message{Kind: sdk.KindAssistant, Assistant: &sdk.Text{Text: "Hello"}}, "s1")
	m := c.expectMessage()
	if m.Message.StreamID != "s1" || *m.Message.Text != "Hello" {
		t.Fatalf("completing message = %+v", m.Message)
	}
	turn.End(sdk.Result{}, nil)
	// Nothing about s1 follows its completing message.
	c.expect("turn.ended")
}

func TestLateSubscriberGetsOpenStream(t *testing.T) {
	f := newFixture(t)
	sid, tid, turn := f.startTurn()
	turn.Emit(sdk.Message{Kind: sdk.KindUser, User: &sdk.Text{Text: "go"}})
	turn.EmitDelta("s1", "Hel")
	turn.EmitDelta("s1", "lo")
	rec, _ := f.store.Record(sid, tid)
	deadline := time.Now().Add(wait)
	for len(rec.Partials()) == 0 || rec.Partials()[0].Text != "Hello" {
		if time.Now().After(deadline) {
			t.Fatal("stream not recorded")
		}
		time.Sleep(time.Millisecond)
	}

	c := f.connect()
	if err := f.hub.Subscribe(c.id, sid, tid, ""); err != nil {
		t.Fatal(err)
	}
	c.expectMessage()
	// The open stream arrives as one snapshot of its current text.
	if p := c.expectPartial(); p.Text != "Hello" {
		t.Fatalf("partial = %+v, want Hello", p)
	}
}
