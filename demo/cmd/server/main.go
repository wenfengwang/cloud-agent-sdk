// Command server runs the demo backend: the HTTP and SSE API over the
// cloudagent SDK, and the built frontend when --web-dir is set.
//
//	go run ./cmd/server --project-root ~/code --harness-versions ../harness-versions.json
//
// It listens on loopback only. Every session is an agent without permission
// prompts in its WorkDir, with this process's privileges.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/wenfengwang/cloud-agent-sdk/demo/internal/api"
	"github.com/wenfengwang/cloud-agent-sdk/demo/internal/events"
	"github.com/wenfengwang/cloud-agent-sdk/demo/internal/sdk"
	"github.com/wenfengwang/cloud-agent-sdk/demo/internal/session"
	"github.com/wenfengwang/cloud-agent-sdk/demo/internal/wire"
)

// closeDeadline bounds closing every runtime on shutdown.
const closeDeadline = 20 * time.Second

type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

type options struct {
	addr            string
	webDir          string
	dataDir         string
	homeMode        string
	harnessBinDir   string
	harnessVersions string
	projectRoots    listFlag
	displayName     string
	claudeModels    string
	codexModels     string
	devOrigin       string
	debugRaw        bool
}

func main() {
	var o options
	flag.StringVar(&o.addr, "addr", "127.0.0.1:8787", "listen address; must be a loopback address")
	flag.StringVar(&o.webDir, "web-dir", "", "directory of the built frontend; empty serves the API only")
	flag.StringVar(&o.dataDir, "data-dir", "", "demo data directory (default ~/.cloudagent-demo)")
	flag.StringVar(&o.homeMode, "home-mode", "isolated", `"isolated" for demo harness homes, "user" for the real ~/.claude and ~/.codex`)
	flag.StringVar(&o.harnessBinDir, "harness-bin-dir", "", "directory prepended to PATH to find claude and codex")
	flag.StringVar(&o.harnessVersions, "harness-versions", "", "harness-versions.json with the pinned CLI versions")
	flag.Var(&o.projectRoots, "project-root", "directory whose subdirectories are offered as projects (repeatable)")
	flag.StringVar(&o.displayName, "display-name", "", "name shown in the UI (default the OS user name)")
	flag.StringVar(&o.claudeModels, "claude-models", "", "comma-separated Claude Code model suggestions")
	flag.StringVar(&o.codexModels, "codex-models", "", "comma-separated Codex model suggestions")
	flag.StringVar(&o.devOrigin, "dev-origin", "http://localhost:3000", "extra allowed origin, such as the Next.js dev server")
	flag.BoolVar(&o.debugRaw, "debug-raw", false, "include each message's raw harness JSON")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(o, logger); err != nil {
		logger.Error("server failed", "err", err)
		os.Exit(1)
	}
}

func run(o options, logger *slog.Logger) error {
	if err := checkLoopback(o.addr); err != nil {
		return err
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("find home directory: %w", err)
	}
	if o.dataDir == "" {
		o.dataDir = filepath.Join(userHome, ".cloudagent-demo")
	}
	if o.harnessBinDir != "" {
		bin, err := filepath.Abs(o.harnessBinDir)
		if err != nil {
			return fmt.Errorf("resolve --harness-bin-dir: %w", err)
		}
		if err := os.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH")); err != nil {
			return fmt.Errorf("set PATH: %w", err)
		}
	}
	for i, root := range o.projectRoots {
		if o.projectRoots[i], err = filepath.Abs(root); err != nil {
			return fmt.Errorf("resolve --project-root: %w", err)
		}
	}
	if o.displayName == "" {
		if u, err := user.Current(); err == nil {
			o.displayName = u.Username
		}
	}

	var h homes
	var loginHints [2]string
	switch o.homeMode {
	case "isolated":
		if h, err = prepareIsolatedHomes(o.dataDir, userHome); err != nil {
			return err
		}
		loginHints = [2]string{"CLAUDE_CONFIG_DIR=" + h.claude + " claude", "CODEX_HOME=" + h.codex + " codex login"}
	case "user":
		loginHints = [2]string{"claude", "codex login"}
	default:
		return fmt.Errorf("--home-mode must be isolated or user, not %q", o.homeMode)
	}

	pinned, err := readPinnedVersions(o.harnessVersions)
	if err != nil {
		return err
	}
	shownHome := func(isolated, user string) string {
		if isolated != "" {
			return isolated
		}
		return filepath.Join(userHome, user)
	}
	harnesses := []api.HarnessInfo{
		probe("claudecode", "Claude Code", "claude", pinned["claude"], shownHome(h.claude, ".claude"), loginHints[0], o.claudeModels),
		probe("codex", "Codex", "codex", pinned["codex"], shownHome(h.codex, ".codex"), loginHints[1], o.codexModels),
	}
	for _, hi := range harnesses {
		switch {
		case !hi.Available:
			logger.Warn("harness CLI not found", "harness", hi.Harness)
		case hi.PinnedVersion != "" && hi.Version != hi.PinnedVersion:
			logger.Warn("harness version differs from the pinned version", "harness", hi.Harness, "version", hi.Version, "pinned", hi.PinnedVersion)
		}
	}

	worktreeRoot := filepath.Join(o.dataDir, "worktrees")
	store := session.NewStore(session.Config{
		Factory:      sdk.SDK{},
		Homes:        map[sdk.Harness]string{sdk.ClaudeCode: h.claude, sdk.Codex: h.codex},
		WorktreeRoot: worktreeRoot,
		Converter:    wire.Converter{DebugRaw: o.debugRaw},
		Logger:       logger,
	})
	hub := events.NewHub(events.Config{Store: store})
	var shuttingDown atomic.Bool
	var allowed []string
	if o.devOrigin != "" {
		allowed = append(allowed, o.devOrigin)
	}
	handler := api.New(api.Config{
		Store:          store,
		Hub:            hub,
		ProjectRoots:   o.projectRoots,
		AllowedOrigins: allowed,
		WebDir:         o.webDir,
		ShuttingDown:   shuttingDown.Load,
		Logger:         logger,
		Info: api.Info{
			DisplayName:  o.displayName,
			Harnesses:    harnesses,
			HomeMode:     o.homeMode,
			WorktreeRoot: worktreeRoot,
			ProjectRoots: append([]string{}, o.projectRoots...),
			DebugRaw:     o.debugRaw,
		},
	})

	ln, err := net.Listen("tcp", o.addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	logger.Info("listening", "url", "http://"+ln.Addr().String(), "homeMode", o.homeMode)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-serveErr:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}
	stop()
	logger.Info("shutting down")

	// Refuse new work, end every stream, then close every runtime; Codex
	// app-server processes outlive the backend otherwise.
	shuttingDown.Store(true)
	closeCtx, cancel := context.WithTimeout(context.Background(), closeDeadline)
	defer cancel()
	httpDone := make(chan error, 1)
	go func() { httpDone <- srv.Shutdown(closeCtx) }()
	hub.Shutdown()
	if err := store.Shutdown(closeCtx); err != nil {
		logger.Error("close sessions", "err", err)
	}
	if err := <-httpDone; err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("stop HTTP server", "err", err)
	}
	return nil
}

// checkLoopback rejects listen addresses that are not loopback.
func checkLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("--addr %q: %w", addr, err)
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("--addr %q is not a loopback address; the demo runs unrestricted agents and listens on loopback only", addr)
}

type homes struct{ claude, codex string }

// prepareIsolatedHomes creates the demo harness homes under dataDir. When
// the Codex home is first created, the user's ~/.codex/config.toml is copied
// in so their model provider applies; no other file is ever copied, and an
// existing config.toml is never overwritten.
func prepareIsolatedHomes(dataDir, userHome string) (homes, error) {
	h := homes{claude: filepath.Join(dataDir, "homes", "claude"), codex: filepath.Join(dataDir, "homes", "codex")}
	if err := os.MkdirAll(h.claude, 0o700); err != nil {
		return h, fmt.Errorf("create Claude Code home: %w", err)
	}
	_, statErr := os.Stat(h.codex)
	firstCreation := errors.Is(statErr, fs.ErrNotExist)
	if err := os.MkdirAll(h.codex, 0o700); err != nil {
		return h, fmt.Errorf("create Codex home: %w", err)
	}
	if !firstCreation {
		return h, nil
	}
	src, err := os.ReadFile(filepath.Join(userHome, ".codex", "config.toml"))
	if errors.Is(err, fs.ErrNotExist) {
		return h, nil
	}
	if err != nil {
		return h, fmt.Errorf("read Codex config.toml: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(h.codex, "config.toml"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return h, nil
	}
	if err != nil {
		return h, fmt.Errorf("copy Codex config.toml: %w", err)
	}
	if _, err := f.Write(src); err != nil {
		f.Close()
		return h, fmt.Errorf("copy Codex config.toml: %w", err)
	}
	return h, f.Close()
}

// readPinnedVersions reads harness-versions.json; an empty path yields no
// pinned versions.
func readPinnedVersions(path string) (map[string]string, error) {
	versions := map[string]string{}
	if path == "" {
		return versions, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read --harness-versions: %w", err)
	}
	if err := json.Unmarshal(data, &versions); err != nil {
		return nil, fmt.Errorf("parse --harness-versions: %w", err)
	}
	return versions, nil
}

var versionPattern = regexp.MustCompile(`\d+\.\d+\.\d+`)

func parseVersion(out string) string {
	return versionPattern.FindString(out)
}

// probe runs `<cli> --version` once, because the SDK has no availability
// or version API.
func probe(harness, label, cli, pinned, home, loginHint, models string) api.HarnessInfo {
	info := api.HarnessInfo{Harness: harness, Label: label, PinnedVersion: pinned, Home: home, LoginHint: loginHint, ModelSuggestions: []string{}}
	for _, m := range strings.Split(models, ",") {
		if m = strings.TrimSpace(m); m != "" {
			info.ModelSuggestions = append(info.ModelSuggestions, m)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, cli, "--version").Output()
	if err != nil {
		return info
	}
	info.Available = true
	info.Version = parseVersion(string(out))
	return info
}
