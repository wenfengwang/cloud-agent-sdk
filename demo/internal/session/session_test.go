package session

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/wenfengwang/cloud-agent-sdk/demo/internal/sdk"
	"github.com/wenfengwang/cloud-agent-sdk/demo/internal/sdk/sdkfake"
	"github.com/wenfengwang/cloud-agent-sdk/demo/internal/wire"
)

const wait = 5 * time.Second

type harness struct {
	t       *testing.T
	factory *sdkfake.Factory
	store   *Store
	mu      sync.Mutex
	changes []Change
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, factory: &sdkfake.Factory{}}
	h.store = NewStore(Config{
		Factory:      h.factory,
		Homes:        map[sdk.Harness]string{sdk.ClaudeCode: "/homes/claude", sdk.Codex: "/homes/codex"},
		WorktreeRoot: t.TempDir(),
	})
	h.store.Subscribe(func(c Change) {
		h.mu.Lock()
		h.changes = append(h.changes, c)
		h.mu.Unlock()
	})
	return h
}

func (h *harness) create(req wire.CreateSession) wire.SessionDetail {
	h.t.Helper()
	if req.Harness == "" {
		req.Harness = "claudecode"
	}
	if req.WorkDir == "" {
		req.WorkDir = h.t.TempDir()
	}
	d, err := h.store.Create(context.Background(), req)
	if err != nil {
		h.t.Fatalf("Create: %v", err)
	}
	return d
}

func (h *harness) runtime(i int) *sdkfake.Runtime {
	h.t.Helper()
	rs := h.factory.Runtimes()
	if i >= len(rs) {
		h.t.Fatalf("runtime %d not created", i)
	}
	return rs[i]
}

func (h *harness) snapshot() []Change {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Change(nil), h.changes...)
}

// eventually polls cond until it holds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(wait)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func (h *harness) waitStatus(id, status string) wire.SessionDetail {
	h.t.Helper()
	var d wire.SessionDetail
	eventually(h.t, "session "+status, func() bool {
		var err error
		d, err = h.store.Get(id)
		return err == nil && d.Status == status
	})
	return d
}

func TestCreateValidation(t *testing.T) {
	h := newHarness(t)
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, req := range map[string]wire.CreateSession{
		"unknown harness":   {Harness: "other", WorkDir: t.TempDir()},
		"relative workDir":  {Harness: "codex", WorkDir: "relative/dir"},
		"missing workDir":   {Harness: "codex", WorkDir: ""},
		"absent workDir":    {Harness: "codex", WorkDir: filepath.Join(t.TempDir(), "absent")},
		"workDir not a dir": {Harness: "codex", WorkDir: file},
		"worktree outside":  {Harness: "codex", WorkDir: t.TempDir(), Worktree: &wire.WorktreeOption{Enabled: true}},
	} {
		if _, err := h.store.Create(context.Background(), req); code(err) != CodeInvalidRequest {
			t.Errorf("%s: err = %v, want %s", name, err, CodeInvalidRequest)
		}
	}
	if len(h.factory.Runtimes()) != 0 {
		t.Error("an invalid request reached the SDK")
	}
}

func TestCreate(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	d := h.create(wire.CreateSession{Harness: "codex", WorkDir: dir, Model: "model-a"})
	r := h.runtime(0)
	if d.ID != r.ID() {
		t.Errorf("ID = %s, want runtime ID %s", d.ID, r.ID())
	}
	if r.Harness != sdk.Codex || r.Options.WorkDir != dir || r.Options.Home != "/homes/codex" || r.Options.Model != "model-a" {
		t.Errorf("New called with %s %+v", r.Harness, r.Options)
	}
	if d.Status != "idle" || d.Title != "" || d.ProjectPath != dir || d.ProjectName != filepath.Base(dir) || d.Worktree != nil || d.ActiveTurnID != nil || len(d.Turns) != 0 {
		t.Errorf("detail = %+v", d)
	}
	changes := h.snapshot()
	if len(changes) != 1 || changes[0].Session == nil || changes[0].Session.ID != d.ID {
		t.Errorf("changes = %+v, want one upsert", changes)
	}
}

func TestCreateNewFailsRollsBackWorktree(t *testing.T) {
	h := newHarness(t)
	repo := newRepo(t)
	h.factory.NewErr = errors.New("boom")
	_, err := h.store.Create(context.Background(), wire.CreateSession{Harness: "codex", WorkDir: repo, Worktree: &wire.WorktreeOption{Enabled: true}})
	if code(err) != CodeInternal {
		t.Fatalf("err = %v, want internal", err)
	}
	out, _ := exec.Command("git", "-C", repo, "worktree", "list", "--porcelain").Output()
	if n := countLines(string(out), "worktree "); n != 1 {
		t.Errorf("%d worktrees after rollback, want 1:\n%s", n, out)
	}
	out, _ = exec.Command("git", "-C", repo, "branch", "--list", "cloudagent-demo/*").Output()
	if len(out) != 0 {
		t.Errorf("branch left behind: %s", out)
	}
}

func TestCreateWithWorktree(t *testing.T) {
	h := newHarness(t)
	repo := newRepo(t)
	d := h.create(wire.CreateSession{Harness: "claudecode", WorkDir: repo, Worktree: &wire.WorktreeOption{Enabled: true}})
	if d.Worktree == nil || d.Worktree.BaseBranch != "main" || d.WorkDir != d.Worktree.Path || d.ProjectPath != repo {
		t.Fatalf("detail = %+v worktree %+v", d, d.Worktree)
	}
	if h.runtime(0).Options.WorkDir != d.Worktree.Path {
		t.Errorf("New WorkDir = %s, want the worktree", h.runtime(0).Options.WorkDir)
	}

	res, err := h.store.Delete(context.Background(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Worktree == nil || !res.Worktree.Removed || res.Worktree.Path != d.Worktree.Path {
		t.Errorf("delete result = %+v", res.Worktree)
	}
}

func TestTurnLifecycle(t *testing.T) {
	h := newHarness(t)
	d := h.create(wire.CreateSession{})

	if _, err := h.store.StartTurn(d.ID, "  \n "); code(err) != CodeInvalidRequest {
		t.Errorf("blank prompt: err = %v", err)
	}
	if _, err := h.store.StartTurn("nope", "hi"); code(err) != CodeNotFound {
		t.Errorf("unknown session: err = %v", err)
	}

	ts, err := h.store.StartTurn(d.ID, "Add  pagination\nto the order list")
	if err != nil {
		t.Fatal(err)
	}
	if ts.Status != "running" || ts.Prompt != "Add  pagination\nto the order list" || ts.EndedAt != nil {
		t.Errorf("turn summary = %+v", ts)
	}
	got, _ := h.store.Get(d.ID)
	if got.Status != "running" || got.ActiveTurnID == nil || *got.ActiveTurnID != ts.ID || got.Title != "Add pagination" {
		t.Errorf("session = %+v", got)
	}

	if _, err := h.store.StartTurn(d.ID, "again"); code(err) != CodeTurnInProgress {
		t.Errorf("second start: err = %v", err)
	}
	if _, err := h.store.Messages(d.ID, ts.ID); code(err) != CodeTurnRunning {
		t.Errorf("messages while running: err = %v", err)
	}

	turn := h.runtime(0).Turns()[0]
	turn.EmitText("one")
	turn.EmitText("two")
	rec, err := h.store.Record(d.ID, ts.ID)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "two messages", func() bool { msgs, _, _ := rec.Since(0); return len(msgs) == 2 })
	if _, final, _ := rec.Since(0); final {
		t.Error("record final before Result")
	}

	turn.End(sdk.Result{FinalText: "two", Usage: sdk.Usage{InputTokens: 3, OutputTokens: 4}}, nil)
	ended := h.waitStatus(d.ID, "idle")
	if ended.ActiveTurnID != nil || ended.LastTurnStatus == nil || *ended.LastTurnStatus != "completed" {
		t.Errorf("session after end = %+v", ended)
	}
	sum := ended.Turns[0]
	if sum.Status != "completed" || sum.EndedAt == nil || *sum.FinalText != "two" || sum.Usage.OutputTokens != 4 || sum.Error != nil {
		t.Errorf("turn after end = %+v", sum)
	}
	list, err := h.store.Messages(d.ID, ts.ID)
	if err != nil || len(list.Messages) != 2 || list.Messages[1].ID != turn.ID()+"-msg-2" || list.Error != nil {
		t.Errorf("messages = %+v, %v", list, err)
	}

	// A second turn keeps the first title.
	if _, err := h.store.StartTurn(d.ID, "Something else"); err != nil {
		t.Fatal(err)
	}
	if got, _ := h.store.Get(d.ID); got.Title != "Add pagination" || len(got.Turns) != 2 {
		t.Errorf("after second start: %+v", got)
	}
}

func TestTitleIsCut(t *testing.T) {
	long := ""
	for len(long) < 100 {
		long += "word "
	}
	if got := makeTitle("\n  " + long); len([]rune(got)) != 60 {
		t.Errorf("title %q has %d runes, want 60", got, len([]rune(got)))
	}
}

func TestOutcomes(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status string
	}{
		{nil, "completed"},
		{sdk.ErrTerminated, "terminated"},
		{sdk.ErrClosed, "closed"},
		{context.Canceled, "cancelled"},
		{errors.New("harness crashed"), "failed"},
	} {
		h := newHarness(t)
		d := h.create(wire.CreateSession{})
		if _, err := h.store.StartTurn(d.ID, "go"); err != nil {
			t.Fatal(err)
		}
		h.runtime(0).Turns()[0].End(sdk.Result{}, tc.err)
		got := h.waitStatus(d.ID, "idle").Turns[0]
		if got.Status != tc.status {
			t.Errorf("%v: status = %s, want %s", tc.err, got.Status, tc.status)
		}
		if (got.Error != nil) != (tc.status == "failed") {
			t.Errorf("%v: error = %+v", tc.err, got.Error)
		}
	}
}

func TestIterationError(t *testing.T) {
	h := newHarness(t)
	d := h.create(wire.CreateSession{})
	ts, _ := h.store.StartTurn(d.ID, "go")
	turn := h.runtime(0).Turns()[0]
	turn.EmitText("before")
	turn.FailIteration(errors.New("transcript unreadable"))
	turn.End(sdk.Result{}, nil)
	h.waitStatus(d.ID, "idle")
	list, err := h.store.Messages(d.ID, ts.ID)
	if err != nil || len(list.Messages) != 1 || list.Error == nil || list.Error.Message != "transcript unreadable" {
		t.Errorf("messages = %+v, %v", list, err)
	}
}

// blockingStart makes Turn wait until ctx is cancelled or release is
// closed, and reports when it was entered.
func blockingStart(entered chan<- struct{}, release <-chan struct{}) func(context.Context, string, *sdkfake.Turn) (sdk.Turn, error) {
	return func(ctx context.Context, _ string, turn *sdkfake.Turn) (sdk.Turn, error) {
		close(entered)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
			return turn, nil
		}
	}
}

func TestStopWhileStartingThenFails(t *testing.T) {
	h := newHarness(t)
	entered := make(chan struct{})
	h.factory.Configure = func(r *sdkfake.Runtime) { r.StartTurn = blockingStart(entered, nil) }
	d := h.create(wire.CreateSession{})

	errc := make(chan error, 1)
	go func() { _, err := h.store.StartTurn(d.ID, "go"); errc <- err }()
	<-entered
	if got, _ := h.store.Get(d.ID); got.Status != "starting" {
		t.Fatalf("status = %s, want starting", got.Status)
	}
	if err := h.store.Stop(d.ID); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; code(err) != CodeStartCancelled {
		t.Fatalf("start err = %v, want start_cancelled", err)
	}
	if got, _ := h.store.Get(d.ID); got.Status != "idle" || len(got.Turns) != 0 {
		t.Errorf("session = %+v", got)
	}
}

func TestStopWhileStartingThenSucceeds(t *testing.T) {
	h := newHarness(t)
	entered, release := make(chan struct{}), make(chan struct{})
	h.factory.Configure = func(r *sdkfake.Runtime) {
		r.StartTurn = func(ctx context.Context, _ string, turn *sdkfake.Turn) (sdk.Turn, error) {
			close(entered)
			<-release // the harness accepted the prompt before the cancel landed
			return turn, nil
		}
	}
	d := h.create(wire.CreateSession{})

	type res struct {
		ts  wire.TurnSummary
		err error
	}
	resc := make(chan res, 1)
	go func() { ts, err := h.store.StartTurn(d.ID, "go"); resc <- res{ts, err} }()
	<-entered
	if err := h.store.Stop(d.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	r := <-resc
	if r.err != nil || r.ts.Status != "running" {
		t.Fatalf("start = %+v, %v; want a running turn", r.ts, r.err)
	}
	if got := h.waitStatus(d.ID, "idle").Turns[0]; got.Status != "cancelled" {
		t.Errorf("turn status = %s, want cancelled", got.Status)
	}
}

func TestDeleteWhileStarting(t *testing.T) {
	h := newHarness(t)
	entered := make(chan struct{})
	h.factory.Configure = func(r *sdkfake.Runtime) { r.StartTurn = blockingStart(entered, nil) }
	d := h.create(wire.CreateSession{})

	errc := make(chan error, 1)
	go func() { _, err := h.store.StartTurn(d.ID, "go"); errc <- err }()
	<-entered
	if _, err := h.store.Delete(context.Background(), d.ID); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; code(err) != CodeSessionClosed {
		t.Fatalf("start err = %v, want session_closed", err)
	}
	if _, err := h.store.Get(d.ID); code(err) != CodeNotFound {
		t.Errorf("Get after delete: %v", err)
	}
	if !h.runtime(0).Closed() {
		t.Error("runtime not closed")
	}
	changes := h.snapshot()
	if last := changes[len(changes)-1]; last.DeletedID != d.ID {
		t.Errorf("last change = %+v, want deletion", last)
	}
}

func TestDeleteWhileRunning(t *testing.T) {
	h := newHarness(t)
	d := h.create(wire.CreateSession{})
	ts, _ := h.store.StartTurn(d.ID, "go")
	rec, _ := h.store.Record(d.ID, ts.ID)

	if _, err := h.store.Delete(context.Background(), d.ID); err != nil {
		t.Fatal(err)
	}
	if _, final, _ := rec.Since(0); !final {
		t.Error("record not final after delete")
	}
	if got := rec.Summary().Status; got != "closed" {
		t.Errorf("turn status = %s, want closed", got)
	}
	// Nothing about the session is published after its deletion.
	changes := h.snapshot()
	if last := changes[len(changes)-1]; last.DeletedID != d.ID {
		t.Errorf("last change = %+v, want deletion", last)
	}
	if _, err := h.store.StartTurn(d.ID, "go"); code(err) != CodeNotFound {
		t.Errorf("start after delete: %v", err)
	}
}

func TestDeleteCloseErrorStillRemoves(t *testing.T) {
	h := newHarness(t)
	h.factory.Configure = func(r *sdkfake.Runtime) { r.CloseErr = errors.New("stuck") }
	d := h.create(wire.CreateSession{})
	if _, err := h.store.Delete(context.Background(), d.ID); code(err) != CodeInternal {
		t.Errorf("err = %v, want internal", err)
	}
	if _, err := h.store.Get(d.ID); code(err) != CodeNotFound {
		t.Errorf("session kept after Close error")
	}
}

func TestStop(t *testing.T) {
	h := newHarness(t)
	d := h.create(wire.CreateSession{})
	if err := h.store.Stop(d.ID); err != nil {
		t.Errorf("stop while idle: %v", err)
	}
	if err := h.store.Stop("nope"); code(err) != CodeNotFound {
		t.Errorf("stop unknown: %v", err)
	}
	_, _ = h.store.StartTurn(d.ID, "go")
	if err := h.store.Stop(d.ID); err != nil {
		t.Fatal(err)
	}
	if got := h.waitStatus(d.ID, "idle").Turns[0].Status; got != "terminated" {
		t.Errorf("status = %s, want terminated", got)
	}
}

func TestStopTerminateError(t *testing.T) {
	h := newHarness(t)
	d := h.create(wire.CreateSession{})
	_, _ = h.store.StartTurn(d.ID, "go")
	h.runtime(0).Turns()[0].TerminateErr = errors.New("cannot signal")
	if err := h.store.Stop(d.ID); code(err) != CodeInternal {
		t.Errorf("err = %v, want internal", err)
	}
}

func TestStartErrors(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{errors.New("claude: executable file not found"), CodeHarnessStartFailed},
		{sdk.ErrTurnInProgress, CodeTurnInProgress},
		{sdk.ErrClosed, CodeSessionClosed},
	} {
		h := newHarness(t)
		h.factory.Configure = func(r *sdkfake.Runtime) {
			r.StartTurn = func(context.Context, string, *sdkfake.Turn) (sdk.Turn, error) { return nil, tc.err }
		}
		d := h.create(wire.CreateSession{})
		if _, err := h.store.StartTurn(d.ID, "go"); code(err) != tc.code {
			t.Errorf("%v: err = %v, want %s", tc.err, err, tc.code)
		}
		if got, _ := h.store.Get(d.ID); got.Status != "idle" || got.Title != "" {
			t.Errorf("%v: session = %+v", tc.err, got)
		}
	}
}

func TestShutdown(t *testing.T) {
	h := newHarness(t)
	entered := make(chan struct{})
	created := 0 // Configure runs under the factory's lock
	h.factory.Configure = func(r *sdkfake.Runtime) {
		if created++; created == 1 {
			r.StartTurn = blockingStart(entered, nil)
		}
	}
	starting := h.create(wire.CreateSession{})
	running := h.create(wire.CreateSession{})
	_ = h.create(wire.CreateSession{})

	errc := make(chan error, 1)
	go func() { _, err := h.store.StartTurn(starting.ID, "go"); errc <- err }()
	<-entered
	ts, err := h.store.StartTurn(running.ID, "go")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	if err := h.store.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	for i, r := range h.factory.Runtimes() {
		if !r.Closed() {
			t.Errorf("runtime %d not closed", i)
		}
	}
	if err := <-errc; code(err) != CodeSessionClosed {
		t.Errorf("pending start err = %v", err)
	}
	rec, _ := h.store.Record(running.ID, ts.ID)
	if _, final, _ := rec.Since(0); !final {
		t.Error("running turn not ended by shutdown")
	}
	if _, err := h.store.Create(context.Background(), wire.CreateSession{Harness: "codex", WorkDir: t.TempDir()}); code(err) != CodeShuttingDown {
		t.Errorf("create after shutdown: %v", err)
	}
	if _, err := h.store.StartTurn(running.ID, "go"); code(err) != CodeShuttingDown {
		t.Errorf("start after shutdown: %v", err)
	}
}

func TestRecordPositions(t *testing.T) {
	h := newHarness(t)
	d := h.create(wire.CreateSession{})
	ts, _ := h.store.StartTurn(d.ID, "go")
	turn := h.runtime(0).Turns()[0]
	turn.EmitText("a")
	turn.EmitText("b")
	turn.EmitText("c")
	rec, _ := h.store.Record(d.ID, ts.ID)
	eventually(t, "three messages", func() bool { msgs, _, _ := rec.Since(0); return len(msgs) == 3 })
	if got := rec.After(turn.ID() + "-msg-2"); got != 2 {
		t.Errorf("After(msg-2) = %d, want 2", got)
	}
	if got := rec.After("unknown"); got != 0 {
		t.Errorf("After(unknown) = %d, want 0", got)
	}
	if got := rec.After(""); got != 0 {
		t.Errorf("After(\"\") = %d, want 0", got)
	}
	if _, err := h.store.Record(d.ID, "nope"); code(err) != CodeNotFound {
		t.Errorf("unknown turn: %v", err)
	}
}

func TestChangesNotRepository(t *testing.T) {
	h := newHarness(t)
	d := h.create(wire.CreateSession{})
	if _, err := h.store.Changes(context.Background(), d.ID); code(err) != CodeInvalidRequest {
		t.Errorf("err = %v, want invalid_request", err)
	}
}

func TestChanges(t *testing.T) {
	h := newHarness(t)
	repo := newRepo(t)
	d := h.create(wire.CreateSession{WorkDir: repo})
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ch, err := h.store.Changes(context.Background(), d.ID)
	if err != nil || len(ch.Files) != 1 || ch.Files[0].Path != "new.txt" {
		t.Errorf("changes = %+v, %v", ch, err)
	}
}

func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "initial"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func countLines(s, prefix string) int {
	n := 0
	for _, line := range splitLines(s) {
		if len(line) >= len(prefix) && line[:len(prefix)] == prefix {
			n++
		}
	}
	return n
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	return append(lines, s[start:])
}

func partialTexts(rec *Record) map[string]string {
	out := map[string]string{}
	for _, p := range rec.Partials() {
		out[p.StreamID] = p.Text
	}
	return out
}

func TestStreamedText(t *testing.T) {
	h := newHarness(t)
	d := h.create(wire.CreateSession{})
	ts, _ := h.store.StartTurn(d.ID, "go")
	turn := h.runtime(0).Turns()[0]
	rec, _ := h.store.Record(d.ID, ts.ID)

	turn.EmitDelta("s1", "Hel")
	turn.EmitDelta("s1", "lo")
	eventually(t, "streamed text", func() bool { return partialTexts(rec)["s1"] == "Hello" })
	if msgs, _, _ := rec.Since(0); len(msgs) != 0 {
		t.Fatalf("deltas were recorded as messages: %+v", msgs)
	}

	turn.EmitCompleting(sdk.Message{Kind: sdk.KindAssistant, Assistant: &sdk.Text{Text: "Hello"}}, "s1")
	eventually(t, "completing message", func() bool { msgs, _, _ := rec.Since(0); return len(msgs) == 1 })
	msgs, _, _ := rec.Since(0)
	if msgs[0].StreamID != "s1" {
		t.Errorf("message StreamID = %q, want s1", msgs[0].StreamID)
	}
	if len(rec.Partials()) != 0 {
		t.Errorf("open streams after completion: %+v", rec.Partials())
	}

	// A stream the turn leaves incomplete is dropped at the end.
	turn.EmitDelta("s2", "cut ")
	eventually(t, "second stream", func() bool { return partialTexts(rec)["s2"] == "cut " })
	turn.End(sdk.Result{}, sdk.ErrTerminated)
	h.waitStatus(d.ID, "idle")
	if len(rec.Partials()) != 0 {
		t.Errorf("open streams after the turn: %+v", rec.Partials())
	}
	list, err := h.store.Messages(d.ID, ts.ID)
	if err != nil || len(list.Messages) != 1 || list.Messages[0].StreamID != "" {
		t.Errorf("history = %+v, %v; want one message without streamId", list, err)
	}
}
