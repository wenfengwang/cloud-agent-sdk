// Package api is the demo's gin router: JSON handlers over the session
// store and event hub, request validation, error mapping, the host, origin,
// and content-type checks, and the built frontend.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/wenfengwang/cloud-agent-sdk/demo/internal/events"
	"github.com/wenfengwang/cloud-agent-sdk/demo/internal/gaps"
	"github.com/wenfengwang/cloud-agent-sdk/demo/internal/gitwork"
	"github.com/wenfengwang/cloud-agent-sdk/demo/internal/session"
	"github.com/wenfengwang/cloud-agent-sdk/demo/internal/wire"
)

// Config configures the router.
type Config struct {
	Store          *session.Store
	Hub            *events.Hub
	Info           Info
	ProjectRoots   []string
	AllowedOrigins []string    // origins allowed besides the served origin, such as the Vite dev server
	WebDir         string      // built frontend; empty serves the API only
	ShuttingDown   func() bool // true once shutdown began; every POST then gets 503
	Logger         *slog.Logger
}

// Info is the static part of GET /api/config.
type Info struct {
	DisplayName  string        `json:"displayName"`
	Harnesses    []HarnessInfo `json:"harnesses"`
	HomeMode     string        `json:"homeMode"`
	WorktreeRoot string        `json:"worktreeRoot"`
	ProjectRoots []string      `json:"projectRoots"`
	DebugRaw     bool          `json:"debugRaw"`
}

// HarnessInfo describes one harness in GET /api/config.
type HarnessInfo struct {
	Harness          string   `json:"harness"`
	Label            string   `json:"label"`
	Available        bool     `json:"available"`
	Version          string   `json:"version"`
	PinnedVersion    string   `json:"pinnedVersion"`
	Home             string   `json:"home"`
	LoginHint        string   `json:"loginHint"`
	DefaultModel     string   `json:"defaultModel"`
	ModelSuggestions []string `json:"modelSuggestions"`
}

type configResponse struct {
	Info
	Gaps []gaps.Gap `json:"gaps"`
}

// Project is one entry of GET /api/projects.
type Project struct {
	Path            string `json:"path"`
	Name            string `json:"name"`
	IsGitRepository bool   `json:"isGitRepository"`
	CurrentBranch   string `json:"currentBranch"`
}

type handler struct{ cfg Config }

// New returns the router.
func New(cfg Config) http.Handler {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.ShuttingDown == nil {
		cfg.ShuttingDown = func() bool { return false }
	}
	h := &handler{cfg: cfg}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), h.checkHost, h.checkOrigin)

	api := r.Group("/api", h.checkMutation)
	api.GET("/config", h.config)
	api.GET("/projects", h.projects)
	api.GET("/projects/branches", h.branches)
	api.GET("/sessions", h.listSessions)
	api.POST("/sessions", h.createSession)
	api.GET("/sessions/:sessionId", h.getSession)
	api.DELETE("/sessions/:sessionId", h.deleteSession)
	api.GET("/sessions/:sessionId/changes", h.changes)
	api.POST("/sessions/:sessionId/turns", h.startTurn)
	api.POST("/sessions/:sessionId/stop", h.stop)
	api.GET("/sessions/:sessionId/turns/:turnId/messages", h.messages)
	api.GET("/events", func(c *gin.Context) { cfg.Hub.ServeHTTP(c.Writer, c.Request) })
	api.POST("/events/:connectionId/subscriptions", h.subscribe)
	api.DELETE("/events/:connectionId/subscriptions/:turnId", h.unsubscribe)

	r.NoRoute(h.static)
	return r
}

// Error responses.

func abort(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}

var statusByCode = map[string]int{
	session.CodeInvalidRequest:     http.StatusBadRequest,
	session.CodeNotFound:           http.StatusNotFound,
	session.CodeTurnInProgress:     http.StatusConflict,
	session.CodeSessionClosed:      http.StatusConflict,
	session.CodeStartCancelled:     http.StatusConflict,
	session.CodeTurnRunning:        http.StatusConflict,
	session.CodeHarnessStartFailed: http.StatusBadGateway,
	session.CodeGitFailed:          http.StatusInternalServerError,
	session.CodeShuttingDown:       http.StatusServiceUnavailable,
	session.CodeInternal:           http.StatusInternalServerError,
}

func (h *handler) fail(c *gin.Context, err error) {
	var e *session.Error
	if !errors.As(err, &e) {
		h.cfg.Logger.Error("request failed", "path", c.Request.URL.Path, "err", err)
		abort(c, http.StatusInternalServerError, session.CodeInternal, err.Error())
		return
	}
	status, ok := statusByCode[e.Code]
	if !ok {
		status = http.StatusInternalServerError
	}
	abort(c, status, e.Code, e.Message)
}

func invalid(c *gin.Context, message string) {
	abort(c, http.StatusBadRequest, session.CodeInvalidRequest, message)
}

// bind decodes a JSON body; an empty body leaves v unchanged.
func bind(c *gin.Context, v any) bool {
	if err := json.NewDecoder(c.Request.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		invalid(c, "malformed body: "+err.Error())
		return false
	}
	return true
}

// Safety checks.

func isLoopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// checkHost rejects requests whose Host is not a loopback name, which
// defeats DNS rebinding.
func (h *handler) checkHost(c *gin.Context) {
	if !isLoopbackHost(c.Request.Host) {
		abort(c, http.StatusForbidden, "forbidden", "host "+c.Request.Host+" is not a loopback name")
	}
}

// checkOrigin rejects cross-site requests: an Origin must be the served
// origin or a configured one.
func (h *handler) checkOrigin(c *gin.Context) {
	origin := c.GetHeader("Origin")
	if origin == "" || origin == "http://"+c.Request.Host {
		return
	}
	for _, allowed := range h.cfg.AllowedOrigins {
		if origin == allowed {
			return
		}
	}
	abort(c, http.StatusForbidden, "forbidden", "origin "+origin+" is not allowed")
}

// checkMutation requires a JSON content type on every mutating request and
// refuses POSTs during shutdown.
func (h *handler) checkMutation(c *gin.Context) {
	if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead {
		return
	}
	if mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type")); err != nil || mediaType != "application/json" {
		abort(c, http.StatusUnsupportedMediaType, "unsupported_media_type", "mutating requests need Content-Type: application/json")
		return
	}
	if c.Request.Method == http.MethodPost && h.cfg.ShuttingDown() {
		abort(c, http.StatusServiceUnavailable, session.CodeShuttingDown, "the server is shutting down")
	}
}

// Configuration and projects.

func (h *handler) config(c *gin.Context) {
	c.JSON(http.StatusOK, configResponse{Info: h.cfg.Info, Gaps: gaps.All()})
}

func (h *handler) projects(c *gin.Context) {
	seen := map[string]bool{}
	var paths []string
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	for _, root := range h.cfg.ProjectRoots {
		entries, err := os.ReadDir(root)
		if err != nil {
			h.cfg.Logger.Warn("read project root", "root", root, "err", err)
			continue
		}
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				add(filepath.Join(root, e.Name()))
			}
		}
	}
	for _, p := range h.cfg.Store.ProjectPaths() {
		add(p)
	}

	projects := make([]Project, 0, len(paths))
	for _, p := range paths {
		project := Project{Path: p, Name: filepath.Base(p)}
		if gitwork.IsRepository(c, p) {
			project.IsGitRepository = true
			project.CurrentBranch, _ = gitwork.CurrentBranch(c, p)
		}
		projects = append(projects, project)
	}
	sort.Slice(projects, func(i, j int) bool {
		if projects[i].Name == projects[j].Name {
			return projects[i].Path < projects[j].Path
		}
		return projects[i].Name < projects[j].Name
	})
	c.JSON(http.StatusOK, gin.H{"projects": projects})
}

func (h *handler) branches(c *gin.Context) {
	path := c.Query("path")
	if !filepath.IsAbs(path) {
		invalid(c, "path must be an absolute path")
		return
	}
	if !gitwork.IsRepository(c, path) {
		invalid(c, path+" is not a git repository")
		return
	}
	current, branches, err := gitwork.Branches(c, path)
	if err != nil {
		abort(c, http.StatusInternalServerError, session.CodeGitFailed, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"current": current, "branches": branches})
}

// Sessions and turns.

func (h *handler) listSessions(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"sessions": h.cfg.Store.List()})
}

func (h *handler) createSession(c *gin.Context) {
	var req wire.CreateSession
	if !bind(c, &req) {
		return
	}
	d, err := h.cfg.Store.Create(c, req)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, d)
}

func (h *handler) getSession(c *gin.Context) {
	d, err := h.cfg.Store.Get(c.Param("sessionId"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, d)
}

func (h *handler) deleteSession(c *gin.Context) {
	res, err := h.cfg.Store.Delete(c, c.Param("sessionId"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}

func (h *handler) changes(c *gin.Context) {
	ch, err := h.cfg.Store.Changes(c, c.Param("sessionId"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, ch)
}

func (h *handler) startTurn(c *gin.Context) {
	var req struct {
		Prompt string `json:"prompt"`
	}
	if !bind(c, &req) {
		return
	}
	ts, err := h.cfg.Store.StartTurn(c.Param("sessionId"), req.Prompt)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, ts)
}

func (h *handler) stop(c *gin.Context) {
	if err := h.cfg.Store.Stop(c.Param("sessionId")); err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{})
}

func (h *handler) messages(c *gin.Context) {
	list, err := h.cfg.Store.Messages(c.Param("sessionId"), c.Param("turnId"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, list)
}

// Event subscriptions.

func (h *handler) subscribe(c *gin.Context) {
	var req struct {
		SessionID      string `json:"sessionId"`
		TurnID         string `json:"turnId"`
		AfterMessageID string `json:"afterMessageId"`
	}
	if !bind(c, &req) {
		return
	}
	if err := h.cfg.Hub.Subscribe(c.Param("connectionId"), req.SessionID, req.TurnID, req.AfterMessageID); err != nil {
		h.fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *handler) unsubscribe(c *gin.Context) {
	if err := h.cfg.Hub.Unsubscribe(c.Param("connectionId"), c.Param("turnId")); err != nil {
		h.fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// static serves the built frontend, falling back to index.html for client
// routes. Unknown /api paths are 404.
func (h *handler) static(c *gin.Context) {
	path := c.Request.URL.Path
	if path == "/api" || strings.HasPrefix(path, "/api/") || h.cfg.WebDir == "" {
		abort(c, http.StatusNotFound, session.CodeNotFound, "no route "+path)
		return
	}
	file := filepath.Join(h.cfg.WebDir, filepath.FromSlash(filepath.Clean("/"+path)))
	if info, err := os.Stat(file); err == nil && !info.IsDir() {
		c.File(file)
		return
	}
	c.File(filepath.Join(h.cfg.WebDir, "index.html"))
}
