package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wenfengwang/cloud-agent-sdk/demo/internal/events"
	"github.com/wenfengwang/cloud-agent-sdk/demo/internal/sdk"
	"github.com/wenfengwang/cloud-agent-sdk/demo/internal/sdk/sdkfake"
	"github.com/wenfengwang/cloud-agent-sdk/demo/internal/session"
	"github.com/wenfengwang/cloud-agent-sdk/demo/internal/wire"
)

const wait = 5 * time.Second

type fixture struct {
	t        *testing.T
	factory  *sdkfake.Factory
	store    *session.Store
	hub      *events.Hub
	server   *httptest.Server
	shutdown atomic.Bool
	root     string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, factory: &sdkfake.Factory{}, root: t.TempDir()}
	f.store = session.NewStore(session.Config{Factory: f.factory, WorktreeRoot: t.TempDir()})
	f.hub = events.NewHub(events.Config{Store: f.store})
	handler := New(Config{
		Store:          f.store,
		Hub:            f.hub,
		ProjectRoots:   []string{f.root},
		AllowedOrigins: []string{"http://localhost:3000"},
		ShuttingDown:   f.shutdown.Load,
		Info: Info{
			DisplayName: "Alex Rivera",
			HomeMode:    "isolated",
			Harnesses:   []HarnessInfo{{Harness: "claudecode", Label: "Claude Code", Available: true, ModelSuggestions: []string{}}},
		},
	})
	f.server = httptest.NewServer(handler)
	t.Cleanup(func() {
		f.hub.Shutdown()
		f.server.Close()
	})
	return f
}

type response struct {
	status int
	body   []byte
}

func (r response) code() string {
	var e struct {
		Error struct{ Code, Message string }
	}
	_ = json.Unmarshal(r.body, &e)
	return e.Error.Code
}

func (f *fixture) do(method, path string, body any, mutate ...func(*http.Request)) response {
	f.t.Helper()
	var reader io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		reader = strings.NewReader(b)
	default:
		data, _ := json.Marshal(b)
		reader = bytes.NewReader(data)
	}
	req, _ := http.NewRequest(method, f.server.URL+path, reader)
	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, m := range mutate {
		m(req)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return response{resp.StatusCode, data}
}

func decode[T any](t *testing.T, r response) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(r.body, &v); err != nil {
		t.Fatalf("decode %s: %v", r.body, err)
	}
	return v
}

func (f *fixture) createSession(dir string) wire.SessionDetail {
	f.t.Helper()
	r := f.do("POST", "/api/sessions", map[string]any{"harness": "codex", "workDir": dir})
	if r.status != http.StatusCreated {
		f.t.Fatalf("create: %d %s", r.status, r.body)
	}
	return decode[wire.SessionDetail](f.t, r)
}

func (f *fixture) lastTurn() *sdkfake.Turn {
	rs := f.factory.Runtimes()
	turns := rs[len(rs)-1].Turns()
	return turns[len(turns)-1]
}

func (f *fixture) waitIdle(id string) wire.SessionDetail {
	f.t.Helper()
	deadline := time.Now().Add(wait)
	for {
		d := decode[wire.SessionDetail](f.t, f.do("GET", "/api/sessions/"+id, nil))
		if d.Status == "idle" {
			return d
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("session %s still %s", id, d.Status)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestSessionAndTurnFlow(t *testing.T) {
	f := newFixture(t)
	d := f.createSession(t.TempDir())

	list := decode[struct{ Sessions []wire.SessionSummary }](t, f.do("GET", "/api/sessions", nil))
	if len(list.Sessions) != 1 || list.Sessions[0].ID != d.ID {
		t.Fatalf("sessions = %+v", list.Sessions)
	}

	r := f.do("POST", "/api/sessions/"+d.ID+"/turns", map[string]string{"prompt": "Add pagination"})
	if r.status != http.StatusCreated {
		t.Fatalf("start: %d %s", r.status, r.body)
	}
	ts := decode[wire.TurnSummary](t, r)

	if r := f.do("GET", "/api/sessions/"+d.ID+"/turns/"+ts.ID+"/messages", nil); r.status != http.StatusConflict || r.code() != "turn_running" {
		t.Errorf("messages while running: %d %s", r.status, r.body)
	}
	if r := f.do("POST", "/api/sessions/"+d.ID+"/turns", map[string]string{"prompt": "again"}); r.status != http.StatusConflict || r.code() != "turn_in_progress" {
		t.Errorf("second start: %d %s", r.status, r.body)
	}

	turn := f.lastTurn()
	turn.EmitText("done")
	turn.End(sdk.Result{FinalText: "done"}, nil)
	f.waitIdle(d.ID)

	msgs := decode[wire.MessageList](t, f.do("GET", "/api/sessions/"+d.ID+"/turns/"+ts.ID+"/messages", nil))
	if len(msgs.Messages) != 1 || *msgs.Messages[0].Text != "done" {
		t.Fatalf("messages = %+v", msgs)
	}

	if r := f.do("POST", "/api/sessions/"+d.ID+"/stop", nil); r.status != http.StatusAccepted {
		t.Errorf("stop idle: %d %s", r.status, r.body)
	}
	r = f.do("DELETE", "/api/sessions/"+d.ID, nil)
	if r.status != http.StatusOK || !bytes.Contains(r.body, []byte(`"worktree":null`)) {
		t.Errorf("delete: %d %s", r.status, r.body)
	}
	if r := f.do("GET", "/api/sessions/"+d.ID, nil); r.status != http.StatusNotFound || r.code() != "not_found" {
		t.Errorf("get after delete: %d %s", r.status, r.body)
	}
}

func TestErrors(t *testing.T) {
	f := newFixture(t)
	d := f.createSession(t.TempDir())
	notDir := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(notDir, nil, 0o644)
	repo := newRepo(t)

	for _, tc := range []struct {
		name, method, path string
		body               any
		status             int
		code               string
	}{
		{"malformed body", "POST", "/api/sessions", "{", 400, "invalid_request"},
		{"unknown field type", "POST", "/api/sessions", `{"harness": 3}`, 400, "invalid_request"},
		{"unknown harness", "POST", "/api/sessions", map[string]string{"harness": "x", "workDir": t.TempDir()}, 400, "invalid_request"},
		{"relative workDir", "POST", "/api/sessions", map[string]string{"harness": "codex", "workDir": "a/b"}, 400, "invalid_request"},
		{"missing workDir", "POST", "/api/sessions", map[string]string{"harness": "codex"}, 400, "invalid_request"},
		{"workDir not a dir", "POST", "/api/sessions", map[string]string{"harness": "codex", "workDir": notDir}, 400, "invalid_request"},
		{"worktree outside git", "POST", "/api/sessions", map[string]any{"harness": "codex", "workDir": t.TempDir(), "worktree": map[string]any{"enabled": true}}, 400, "invalid_request"},
		{"git failed", "POST", "/api/sessions", map[string]any{"harness": "codex", "workDir": repo, "worktree": map[string]any{"enabled": true, "baseBranch": "nope"}}, 500, "git_failed"},
		{"empty prompt", "POST", "/api/sessions/" + d.ID + "/turns", map[string]string{"prompt": "  "}, 400, "invalid_request"},
		{"unknown session", "GET", "/api/sessions/nope", nil, 404, "not_found"},
		{"unknown session turn", "POST", "/api/sessions/nope/turns", map[string]string{"prompt": "x"}, 404, "not_found"},
		{"unknown turn", "GET", "/api/sessions/" + d.ID + "/turns/nope/messages", nil, 404, "not_found"},
		{"unknown connection", "POST", "/api/events/nope/subscriptions", map[string]string{"sessionId": d.ID, "turnId": "x"}, 404, "not_found"},
		{"unknown connection delete", "DELETE", "/api/events/nope/subscriptions/x", nil, 404, "not_found"},
		{"changes outside git", "GET", "/api/sessions/" + d.ID + "/changes", nil, 400, "invalid_request"},
		{"branches outside git", "GET", "/api/projects/branches?path=" + t.TempDir(), nil, 400, "invalid_request"},
		{"branches relative", "GET", "/api/projects/branches?path=rel", nil, 400, "invalid_request"},
	} {
		r := f.do(tc.method, tc.path, tc.body)
		if r.status != tc.status || r.code() != tc.code {
			t.Errorf("%s: %d %s, want %d %s", tc.name, r.status, r.body, tc.status, tc.code)
		}
	}
}

func TestStartErrors(t *testing.T) {
	f := newFixture(t)
	var startErr atomic.Value
	f.factory.Configure = func(r *sdkfake.Runtime) {
		r.StartTurn = func(ctx context.Context, _ string, turn *sdkfake.Turn) (sdk.Turn, error) {
			if err, _ := startErr.Load().(error); err != nil {
				return nil, err
			}
			return turn, nil
		}
	}
	d := f.createSession(t.TempDir())
	startErr.Store(errors.New("exec: claude: not found"))
	if r := f.do("POST", "/api/sessions/"+d.ID+"/turns", map[string]string{"prompt": "x"}); r.status != http.StatusBadGateway || r.code() != "harness_start_failed" {
		t.Errorf("start failure: %d %s", r.status, r.body)
	}
	startErr.Store(sdk.ErrClosed)
	if r := f.do("POST", "/api/sessions/"+d.ID+"/turns", map[string]string{"prompt": "x"}); r.status != http.StatusConflict || r.code() != "session_closed" {
		t.Errorf("closed: %d %s", r.status, r.body)
	}
}

func TestStopWhileStarting(t *testing.T) {
	f := newFixture(t)
	entered := make(chan struct{})
	f.factory.Configure = func(r *sdkfake.Runtime) {
		r.StartTurn = func(ctx context.Context, _ string, _ *sdkfake.Turn) (sdk.Turn, error) {
			close(entered)
			<-ctx.Done()
			return nil, ctx.Err()
		}
	}
	d := f.createSession(t.TempDir())
	resc := make(chan response, 1)
	go func() { resc <- f.do("POST", "/api/sessions/"+d.ID+"/turns", map[string]string{"prompt": "x"}) }()
	<-entered
	if r := f.do("POST", "/api/sessions/"+d.ID+"/stop", nil); r.status != http.StatusAccepted {
		t.Fatalf("stop: %d %s", r.status, r.body)
	}
	if r := <-resc; r.status != http.StatusConflict || r.code() != "start_cancelled" {
		t.Errorf("start: %d %s", r.status, r.body)
	}
}

func TestTerminateErrorIsInternal(t *testing.T) {
	f := newFixture(t)
	d := f.createSession(t.TempDir())
	if r := f.do("POST", "/api/sessions/"+d.ID+"/turns", map[string]string{"prompt": "x"}); r.status != http.StatusCreated {
		t.Fatal(r.status)
	}
	f.lastTurn().TerminateErr = errors.New("no")
	if r := f.do("POST", "/api/sessions/"+d.ID+"/stop", nil); r.status != http.StatusInternalServerError || r.code() != "internal" {
		t.Errorf("stop: %d %s", r.status, r.body)
	}
}

func TestShuttingDown(t *testing.T) {
	f := newFixture(t)
	f.shutdown.Store(true)
	if r := f.do("POST", "/api/sessions", map[string]string{"harness": "codex", "workDir": t.TempDir()}); r.status != http.StatusServiceUnavailable || r.code() != "shutting_down" {
		t.Errorf("post during shutdown: %d %s", r.status, r.body)
	}
	if r := f.do("GET", "/api/sessions", nil); r.status != http.StatusOK {
		t.Errorf("get during shutdown: %d", r.status)
	}
}

func TestSafetyChecks(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct {
		name   string
		method string
		mutate func(*http.Request)
		status int
		code   string
	}{
		{"foreign host", "GET", func(r *http.Request) { r.Host = "evil.example:8787" }, 403, "forbidden"},
		{"foreign origin", "GET", func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }, 403, "forbidden"},
		{"dev origin", "GET", func(r *http.Request) { r.Header.Set("Origin", "http://localhost:3000") }, 200, ""},
		{"served origin", "GET", func(r *http.Request) { r.Header.Set("Origin", "http://"+r.Host) }, 200, ""},
		{"localhost host", "GET", func(r *http.Request) { r.Host = "localhost:8787" }, 200, ""},
		{"no content type", "POST", func(r *http.Request) { r.Header.Del("Content-Type") }, 415, "unsupported_media_type"},
		{"form content type", "POST", func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, 415, "unsupported_media_type"},
		{"json with charset", "POST", func(r *http.Request) { r.Header.Set("Content-Type", "application/json; charset=utf-8") }, 400, "invalid_request"},
	} {
		path := "/api/sessions"
		var body any
		if tc.method == "POST" {
			body = map[string]string{}
		}
		r := f.do(tc.method, path, body, tc.mutate)
		if r.status != tc.status || r.code() != tc.code {
			t.Errorf("%s: %d %s, want %d %s", tc.name, r.status, r.body, tc.status, tc.code)
		}
	}
}

func TestConfig(t *testing.T) {
	f := newFixture(t)
	r := f.do("GET", "/api/config", nil)
	cfg := decode[map[string]any](t, r)
	if cfg["displayName"] != "Alex Rivera" || cfg["homeMode"] != "isolated" {
		t.Errorf("config = %s", r.body)
	}
	gaps, _ := cfg["gaps"].([]any)
	if len(gaps) == 0 {
		t.Errorf("config has no gaps: %s", r.body)
	}
}

func TestProjects(t *testing.T) {
	f := newFixture(t)
	repoPath := filepath.Join(f.root, "storefront")
	if err := os.Mkdir(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}
	initRepo(t, repoPath)
	_ = os.Mkdir(filepath.Join(f.root, "notes"), 0o755)
	_ = os.Mkdir(filepath.Join(f.root, ".hidden"), 0o755)
	_ = os.WriteFile(filepath.Join(f.root, "file.txt"), nil, 0o644)
	elsewhere := t.TempDir()
	f.createSession(elsewhere)

	got := decode[struct {
		Projects []struct {
			Path, Name, CurrentBranch string
			IsGitRepository           bool
		}
	}](t, f.do("GET", "/api/projects", nil))
	byName := map[string]bool{}
	for _, p := range got.Projects {
		byName[p.Name] = true
		if p.Name == "storefront" && (!p.IsGitRepository || p.CurrentBranch != "main") {
			t.Errorf("storefront = %+v", p)
		}
		if p.Name == "notes" && p.IsGitRepository {
			t.Errorf("notes = %+v", p)
		}
	}
	if len(got.Projects) != 3 || !byName["storefront"] || !byName["notes"] || !byName[filepath.Base(elsewhere)] {
		t.Errorf("projects = %+v", got.Projects)
	}

	br := decode[struct {
		Current  string
		Branches []string
	}](t, f.do("GET", "/api/projects/branches?path="+repoPath, nil))
	if br.Current != "main" || len(br.Branches) != 1 {
		t.Errorf("branches = %+v", br)
	}
}

func TestEventsOverHTTP(t *testing.T) {
	f := newFixture(t)
	d := f.createSession(t.TempDir())
	ts := decode[wire.TurnSummary](t, f.do("POST", "/api/sessions/"+d.ID+"/turns", map[string]string{"prompt": "x"}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", f.server.URL+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	lines := make(chan string, 256)
	go func() {
		s := bufio.NewScanner(resp.Body)
		for s.Scan() {
			lines <- s.Text()
		}
		close(lines)
	}()
	next := func(prefix string) string {
		t.Helper()
		for {
			select {
			case l, ok := <-lines:
				if !ok {
					t.Fatal("stream ended")
				}
				if strings.HasPrefix(l, prefix) {
					return strings.TrimPrefix(l, prefix)
				}
			case <-time.After(wait):
				t.Fatalf("no %q line", prefix)
			}
		}
	}
	var hello struct{ ConnectionID string }
	next("event: hello")
	_ = json.Unmarshal([]byte(next("data: ")), &hello)

	if r := f.do("POST", "/api/events/"+hello.ConnectionID+"/subscriptions", map[string]string{"sessionId": d.ID, "turnId": ts.ID}); r.status != http.StatusNoContent {
		t.Fatalf("subscribe: %d %s", r.status, r.body)
	}
	f.lastTurn().EmitText("hi")
	next("event: message")
	f.lastTurn().End(sdk.Result{}, nil)
	next("event: turn.ended")
	if r := f.do("DELETE", "/api/events/"+hello.ConnectionID+"/subscriptions/"+ts.ID, nil); r.status != http.StatusNoContent {
		t.Errorf("unsubscribe: %d %s", r.status, r.body)
	}
}

func TestStaticFrontend(t *testing.T) {
	web := t.TempDir()
	_ = os.WriteFile(filepath.Join(web, "index.html"), []byte("<html>app</html>"), 0o644)
	_ = os.MkdirAll(filepath.Join(web, "assets"), 0o755)
	_ = os.WriteFile(filepath.Join(web, "assets", "app.js"), []byte("js"), 0o644)
	_ = os.MkdirAll(filepath.Join(web, "settings"), 0o755)
	_ = os.WriteFile(filepath.Join(web, "settings", "index.html"), []byte("<html>settings</html>"), 0o644)
	store := session.NewStore(session.Config{Factory: &sdkfake.Factory{}})
	srv := httptest.NewServer(New(Config{Store: store, Hub: events.NewHub(events.Config{Store: store}), WebDir: web, ShuttingDown: func() bool { return false }}))
	defer srv.Close()
	for path, want := range map[string]string{"/": "app", "/?session=x": "app", "/unknown/route": "app", "/assets/app.js": "js", "/settings/": "settings", "/settings": "settings"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(string(body), want) {
			t.Errorf("%s = %d %q, want %q", path, resp.StatusCode, body, want)
		}
	}
	resp, _ := http.Get(srv.URL + "/api/nope")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("/api/nope = %d, want 404", resp.StatusCode)
	}
}

func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	initRepo(t, dir)
	return dir
}

func initRepo(t *testing.T, dir string) {
	t.Helper()
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
}
