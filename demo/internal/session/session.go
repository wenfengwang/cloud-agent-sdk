// Package session holds the demo's sessions in memory: one sdk.Runtime per
// session, a state machine around the SDK's turn lifecycle, one recorder per
// turn, and change notifications for the event stream.
//
// Lock order: Store.pubMu, then Store.mu or a session's mu, then a Record's
// mu. No lock is held across an SDK call.
package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/wenfengwang/cloud-agent-sdk/demo/internal/gitwork"
	"github.com/wenfengwang/cloud-agent-sdk/demo/internal/sdk"
	"github.com/wenfengwang/cloud-agent-sdk/demo/internal/wire"
)

// Error codes, as served by the API.
const (
	CodeInvalidRequest     = "invalid_request"
	CodeNotFound           = "not_found"
	CodeTurnInProgress     = "turn_in_progress"
	CodeSessionClosed      = "session_closed"
	CodeStartCancelled     = "start_cancelled"
	CodeTurnRunning        = "turn_running"
	CodeHarnessStartFailed = "harness_start_failed"
	CodeGitFailed          = "git_failed"
	CodeShuttingDown       = "shutting_down"
	CodeInternal           = "internal"
)

// Error is an error with an API code.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func fail(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Session statuses.
const (
	statusIdle     = "idle"
	statusStarting = "starting"
	statusRunning  = "running"
	statusClosing  = "closing"
)

// Turn statuses.
const (
	turnRunning    = "running"
	turnCompleted  = "completed"
	turnTerminated = "terminated"
	turnCancelled  = "cancelled"
	turnClosed     = "closed"
	turnFailed     = "failed"
)

// Change is one session change: an upsert carries the session's latest
// summary, a deletion its ID.
type Change struct {
	Session   *wire.SessionSummary
	DeletedID string
}

// Config configures a Store.
type Config struct {
	Factory      sdk.Factory
	Homes        map[sdk.Harness]string // Options.Home per harness; empty keeps the harness default
	WorktreeRoot string
	Converter    wire.Converter
	Now          func() time.Time // defaults to time.Now
	Logger       *slog.Logger     // defaults to slog.Default()
}

// Store holds every session of the running backend.
type Store struct {
	cfg Config

	// pubMu orders publications: a change is snapshotted and delivered
	// under it, so the last change delivered for a session is its latest
	// state.
	pubMu sync.Mutex

	mu           sync.Mutex
	sessions     map[string]*session
	shuttingDown bool
	subs         map[int]func(Change)
	nextSub      int
}

type session struct {
	id          string
	harness     sdk.Harness
	projectPath string
	workDir     string
	model       string
	baseCommit  string // empty when WorkDir is not a git repository
	worktree    *wire.Worktree
	createdAt   time.Time
	runtime     sdk.Runtime

	mu            sync.Mutex
	status        string
	title         string
	records       []*Record
	active        *Record  // set while starting has produced a turn, until its recorder ends
	activeTurn    sdk.Turn // the SDK turn of active
	cancel        context.CancelFunc
	stopRequested bool
	lastActivity  time.Time
}

// NewStore returns an empty Store.
func NewStore(cfg Config) *Store {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Store{cfg: cfg, sessions: map[string]*session{}, subs: map[int]func(Change){}}
}

// Subscribe calls fn for every change until the returned function is
// called. fn must not block.
func (s *Store) Subscribe(fn func(Change)) (unsubscribe func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextSub
	s.nextSub++
	s.subs[id] = fn
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.subs, id)
	}
}

func (s *Store) subscribers() []func(Change) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fns := make([]func(Change), 0, len(s.subs))
	for _, fn := range s.subs {
		fns = append(fns, fn)
	}
	return fns
}

// publish delivers sess's latest summary, unless sess was removed.
func (s *Store) publish(sess *session) {
	s.pubMu.Lock()
	defer s.pubMu.Unlock()
	s.mu.Lock()
	_, live := s.sessions[sess.id]
	s.mu.Unlock()
	if !live {
		return
	}
	sum := sess.summary()
	for _, fn := range s.subscribers() {
		fn(Change{Session: &sum})
	}
}

// remove deletes sess and delivers the deletion.
func (s *Store) remove(sess *session) {
	s.pubMu.Lock()
	defer s.pubMu.Unlock()
	s.mu.Lock()
	delete(s.sessions, sess.id)
	s.mu.Unlock()
	for _, fn := range s.subscribers() {
		fn(Change{DeletedID: sess.id})
	}
}

func (s *Store) isShuttingDown() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shuttingDown
}

func (s *Store) get(id string) (*session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return nil, fail(CodeNotFound, "unknown session %s", id)
	}
	return sess, nil
}

// Create validates req, creates the worktree when asked, and calls New.
func (s *Store) Create(ctx context.Context, req wire.CreateSession) (wire.SessionDetail, error) {
	if s.isShuttingDown() {
		return wire.SessionDetail{}, fail(CodeShuttingDown, "the server is shutting down")
	}
	h := sdk.Harness(req.Harness)
	if h != sdk.ClaudeCode && h != sdk.Codex {
		return wire.SessionDetail{}, fail(CodeInvalidRequest, "unknown harness %q", req.Harness)
	}
	if req.WorkDir == "" || !filepath.IsAbs(req.WorkDir) {
		return wire.SessionDetail{}, fail(CodeInvalidRequest, "workDir must be an absolute path")
	}
	projectPath := filepath.Clean(req.WorkDir)
	if info, err := os.Stat(projectPath); err != nil || !info.IsDir() {
		return wire.SessionDetail{}, fail(CodeInvalidRequest, "workDir %s is not a directory", projectPath)
	}

	workDir := projectPath
	var wt *gitwork.Worktree
	if req.Worktree != nil && req.Worktree.Enabled {
		if !gitwork.IsRepository(ctx, projectPath) {
			return wire.SessionDetail{}, fail(CodeInvalidRequest, "a worktree needs a git repository; %s is not one", projectPath)
		}
		base := req.Worktree.BaseBranch
		if base == "" {
			var err error
			if base, err = gitwork.CurrentBranch(ctx, projectPath); err != nil {
				return wire.SessionDetail{}, fail(CodeGitFailed, "%v", err)
			}
		}
		created, err := gitwork.AddWorktree(ctx, projectPath, s.cfg.WorktreeRoot, filepath.Base(projectPath), base)
		if err != nil {
			return wire.SessionDetail{}, fail(CodeGitFailed, "%v", err)
		}
		wt, workDir = &created, created.Path
	}
	rollback := func() {
		if wt == nil {
			return
		}
		if err := gitwork.Rollback(context.WithoutCancel(ctx), projectPath, *wt); err != nil {
			s.cfg.Logger.Error("roll back worktree", "path", wt.Path, "err", err)
		}
	}

	baseCommit := ""
	if gitwork.IsRepository(ctx, workDir) {
		baseCommit, _ = gitwork.HeadCommit(ctx, workDir) // empty in a repository without commits
	}

	runtime, err := s.cfg.Factory.New(h, sdk.Options{Model: req.Model, WorkDir: workDir, Home: s.cfg.Homes[h]})
	if err != nil {
		rollback()
		return wire.SessionDetail{}, fail(CodeInternal, "create agent: %v", err)
	}

	now := s.cfg.Now()
	sess := &session{
		id:           runtime.ID(),
		harness:      h,
		projectPath:  projectPath,
		workDir:      workDir,
		model:        req.Model,
		baseCommit:   baseCommit,
		createdAt:    now,
		runtime:      runtime,
		status:       statusIdle,
		lastActivity: now,
	}
	if wt != nil {
		sess.worktree = &wire.Worktree{Path: wt.Path, Branch: wt.Branch, BaseBranch: wt.BaseBranch}
	}

	s.mu.Lock()
	if s.shuttingDown {
		s.mu.Unlock()
		if err := runtime.Close(); err != nil {
			s.cfg.Logger.Error("close agent", "session", sess.id, "err", err)
		}
		rollback()
		return wire.SessionDetail{}, fail(CodeShuttingDown, "the server is shutting down")
	}
	s.sessions[sess.id] = sess
	s.mu.Unlock()

	s.publish(sess)
	return sess.detail(), nil
}

// List returns every session, newest activity first.
func (s *Store) List() []wire.SessionSummary {
	s.mu.Lock()
	all := make([]*session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		all = append(all, sess)
	}
	s.mu.Unlock()

	sums := make([]wire.SessionSummary, len(all))
	for i, sess := range all {
		sums[i] = sess.summary()
	}
	sort.SliceStable(sums, func(i, j int) bool {
		a, b := time.Time(sums[i].LastActivityAt), time.Time(sums[j].LastActivityAt)
		if a.Equal(b) {
			return sums[i].ID > sums[j].ID
		}
		return a.After(b)
	})
	return sums
}

// ProjectPaths returns the project path of every live session.
func (s *Store) ProjectPaths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	paths := make([]string, 0, len(s.sessions))
	for _, sess := range s.sessions {
		paths = append(paths, sess.projectPath)
	}
	return paths
}

// Get returns one session with its turns.
func (s *Store) Get(id string) (wire.SessionDetail, error) {
	sess, err := s.get(id)
	if err != nil {
		return wire.SessionDetail{}, err
	}
	return sess.detail(), nil
}

// StartTurn starts a turn and returns once the SDK's Turn returned.
func (s *Store) StartTurn(id, prompt string) (wire.TurnSummary, error) {
	if s.isShuttingDown() {
		return wire.TurnSummary{}, fail(CodeShuttingDown, "the server is shutting down")
	}
	if strings.TrimSpace(prompt) == "" {
		return wire.TurnSummary{}, fail(CodeInvalidRequest, "prompt is empty")
	}
	sess, err := s.get(id)
	if err != nil {
		return wire.TurnSummary{}, err
	}

	sess.mu.Lock()
	switch sess.status {
	case statusStarting, statusRunning:
		sess.mu.Unlock()
		return wire.TurnSummary{}, fail(CodeTurnInProgress, "a turn is already in progress")
	case statusClosing:
		sess.mu.Unlock()
		return wire.TurnSummary{}, fail(CodeSessionClosed, "the session is closing")
	}
	// A turn never runs on a request context.
	ctx, cancel := context.WithCancel(context.Background())
	sess.status, sess.cancel, sess.stopRequested = statusStarting, cancel, false
	sess.mu.Unlock()
	s.publish(sess)

	turn, err := sess.runtime.Turn(ctx, prompt)

	sess.mu.Lock()
	if sess.status == statusClosing {
		// Close is already stopping a returned turn and waits for it.
		sess.mu.Unlock()
		cancel()
		return wire.TurnSummary{}, fail(CodeSessionClosed, "the session was closed while the turn was starting")
	}
	if err != nil {
		stopped := sess.stopRequested
		sess.status, sess.cancel = statusIdle, nil
		sess.mu.Unlock()
		cancel()
		s.publish(sess)
		switch {
		case stopped && errors.Is(err, context.Canceled):
			return wire.TurnSummary{}, fail(CodeStartCancelled, "the turn was stopped while starting; the prompt may still reach the next turn")
		case errors.Is(err, sdk.ErrTurnInProgress):
			return wire.TurnSummary{}, fail(CodeTurnInProgress, "%v", err)
		case errors.Is(err, sdk.ErrClosed):
			return wire.TurnSummary{}, fail(CodeSessionClosed, "%v", err)
		default:
			return wire.TurnSummary{}, fail(CodeHarnessStartFailed, "%v", err)
		}
	}

	now := s.cfg.Now()
	rec := newRecord(turn.ID(), sess.id, prompt, now)
	sess.records = append(sess.records, rec)
	sess.active, sess.activeTurn = rec, turn
	sess.status = statusRunning
	if sess.title == "" {
		sess.title = makeTitle(prompt)
	}
	sess.lastActivity = now
	sess.mu.Unlock()
	s.publish(sess)

	go s.record(sess, rec, turn, cancel)
	return rec.Summary(), nil
}

// record is the turn's recorder: the only reader of turn.Events().
func (s *Store) record(sess *session, rec *Record, turn sdk.Turn, cancel context.CancelFunc) {
	defer close(rec.done)
	for e, err := range turn.Events() {
		if err != nil {
			rec.fail(err.Error())
			break
		}
		if e.Delta != nil {
			rec.delta(*e.Delta)
			continue
		}
		m := s.cfg.Converter.Message(*e.Message, s.cfg.Now())
		m.StreamID = e.StreamID
		rec.append(m)
	}

	res, err := turn.Result()
	status, errMsg := classify(err)
	now := s.cfg.Now()
	rec.setOutcome(status, now, res, errMsg)
	cancel()

	sess.mu.Lock()
	sess.active, sess.activeTurn, sess.cancel = nil, nil, nil
	if sess.status != statusClosing {
		sess.status = statusIdle
	}
	sess.lastActivity = now
	sess.mu.Unlock()

	rec.finalize()
	s.publish(sess)
}

func classify(err error) (status, message string) {
	switch {
	case err == nil:
		return turnCompleted, ""
	case errors.Is(err, sdk.ErrTerminated):
		return turnTerminated, ""
	case errors.Is(err, sdk.ErrClosed):
		return turnClosed, ""
	case errors.Is(err, context.Canceled):
		return turnCancelled, ""
	default:
		return turnFailed, err.Error()
	}
}

// Stop cancels a starting turn or terminates a running one. It is a no-op
// when the session is idle or closing.
func (s *Store) Stop(id string) error {
	sess, err := s.get(id)
	if err != nil {
		return err
	}
	sess.mu.Lock()
	switch sess.status {
	case statusStarting:
		sess.stopRequested = true
		cancel := sess.cancel
		sess.mu.Unlock()
		cancel()
	case statusRunning:
		turn := sess.activeTurn
		sess.mu.Unlock()
		if err := turn.Terminate(); err != nil {
			s.cfg.Logger.Error("terminate turn", "session", id, "turn", turn.ID(), "err", err)
			return fail(CodeInternal, "terminate: %v", err)
		}
	default:
		sess.mu.Unlock()
	}
	return nil
}

// beginClose moves sess to closing. It reports false when sess was already
// closing.
func (sess *session) beginClose() (wasStarting bool, cancel context.CancelFunc, active *Record, ok bool) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.status == statusClosing {
		return false, nil, nil, false
	}
	wasStarting = sess.status == statusStarting
	sess.status = statusClosing
	return wasStarting, sess.cancel, sess.active, true
}

// close calls Close, cancelling a pending start right after Close begins,
// because Close alone waits for a start that may never finish. It then
// waits for the active turn's recorder.
func (sess *session) close(wasStarting bool, cancel context.CancelFunc, active *Record) error {
	done := make(chan error, 1)
	go func() { done <- sess.runtime.Close() }()
	if wasStarting && cancel != nil {
		cancel()
	}
	err := <-done
	if active != nil {
		<-active.done
	}
	return err
}

// Delete closes the session's runtime, removes its worktree when clean, and
// removes the session.
func (s *Store) Delete(ctx context.Context, id string) (wire.DeleteResult, error) {
	sess, err := s.get(id)
	if err != nil {
		return wire.DeleteResult{}, err
	}
	wasStarting, cancel, active, ok := sess.beginClose()
	if !ok {
		return wire.DeleteResult{}, fail(CodeSessionClosed, "the session is already closing")
	}
	s.publish(sess)

	closeErr := sess.close(wasStarting, cancel, active)
	if closeErr != nil {
		s.cfg.Logger.Error("close agent", "session", id, "err", closeErr)
	}

	var res wire.DeleteResult
	if sess.worktree != nil {
		removal := &wire.WorktreeRemoval{Path: sess.worktree.Path}
		removed, reason, err := gitwork.RemoveWorktree(context.WithoutCancel(ctx), sess.projectPath, sess.worktree.Path)
		if err != nil {
			s.cfg.Logger.Error("remove worktree", "path", sess.worktree.Path, "err", err)
			reason = "git failed: " + err.Error()
		}
		removal.Removed, removal.Reason = removed, reason
		res.Worktree = removal
	}

	s.remove(sess)
	if closeErr != nil {
		return res, fail(CodeInternal, "close agent: %v", closeErr)
	}
	return res, nil
}

// Shutdown refuses new work, closes every runtime in parallel, and cancels
// every pending start. It returns when all are closed or ctx is done.
func (s *Store) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.shuttingDown = true
	all := make([]*session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		all = append(all, sess)
	}
	s.mu.Unlock()

	var wg sync.WaitGroup
	for _, sess := range all {
		wasStarting, cancel, active, ok := sess.beginClose()
		if !ok {
			continue // being deleted; Delete closes it
		}
		s.publish(sess)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := sess.close(wasStarting, cancel, active); err != nil {
				s.cfg.Logger.Error("close agent", "session", sess.id, "err", err)
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("close sessions: %w", ctx.Err())
	}
}

// Changes returns the session's WorkDir against its start commit.
func (s *Store) Changes(ctx context.Context, id string) (gitwork.Changes, error) {
	sess, err := s.get(id)
	if err != nil {
		return gitwork.Changes{}, err
	}
	if sess.baseCommit == "" {
		return gitwork.Changes{}, fail(CodeInvalidRequest, "%s is not a git repository with a commit", sess.workDir)
	}
	changes, err := gitwork.Diff(ctx, sess.workDir, sess.baseCommit)
	if err != nil {
		return gitwork.Changes{}, fail(CodeGitFailed, "%v", err)
	}
	return changes, nil
}

// Record returns the record of one turn.
func (s *Store) Record(sessionID, turnID string) (*Record, error) {
	sess, err := s.get(sessionID)
	if err != nil {
		return nil, err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	for _, rec := range sess.records {
		if rec.id == turnID {
			return rec, nil
		}
	}
	return nil, fail(CodeNotFound, "unknown turn %s", turnID)
}

// Messages returns the record of an ended turn.
func (s *Store) Messages(sessionID, turnID string) (wire.MessageList, error) {
	rec, err := s.Record(sessionID, turnID)
	if err != nil {
		return wire.MessageList{}, err
	}
	msgs, final, _ := rec.Since(0)
	if !final {
		return wire.MessageList{}, fail(CodeTurnRunning, "the turn is still running")
	}
	// History never refers to streams.
	list := wire.MessageList{Messages: make([]wire.Message, len(msgs))}
	for i, m := range msgs {
		m.StreamID = ""
		list.Messages[i] = m
	}
	if e := rec.IterErr(); e != "" {
		list.Error = &wire.ErrorMessage{Message: e}
	}
	return list, nil
}

func (sess *session) summary() wire.SessionSummary {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sum := wire.SessionSummary{
		ID:             sess.id,
		Harness:        string(sess.harness),
		Title:          sess.title,
		ProjectPath:    sess.projectPath,
		ProjectName:    filepath.Base(sess.projectPath),
		WorkDir:        sess.workDir,
		Model:          sess.model,
		Worktree:       sess.worktree,
		Status:         sess.status,
		CreatedAt:      wire.Time(sess.createdAt),
		LastActivityAt: wire.Time(sess.lastActivity),
	}
	if sess.status == statusRunning && sess.active != nil {
		id := sess.active.id
		sum.ActiveTurnID = &id
	}
	for i := len(sess.records) - 1; i >= 0; i-- {
		if status := sess.records[i].status(); status != turnRunning {
			sum.LastTurnStatus = &status
			break
		}
	}
	return sum
}

func (sess *session) detail() wire.SessionDetail {
	d := wire.SessionDetail{SessionSummary: sess.summary(), Turns: []wire.TurnSummary{}}
	sess.mu.Lock()
	records := append([]*Record(nil), sess.records...)
	sess.mu.Unlock()
	for _, rec := range records {
		d.Turns = append(d.Turns, rec.Summary())
	}
	return d
}

// makeTitle returns the first line of prompt with whitespace collapsed, cut
// to 60 characters.
func makeTitle(prompt string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(prompt), "\n")
	title := strings.Join(strings.Fields(line), " ")
	if utf8.RuneCountInString(title) > 60 {
		title = string([]rune(title)[:60])
	}
	return title
}
