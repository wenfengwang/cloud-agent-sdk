# cloudagent demo

A local web app that drives Claude Code and Codex sessions through the cloudagent Go SDK: a gin backend ([design 001](design/001-architecture.md)) and a Next.js and shadcn/ui frontend ([design 003](design/003-web-app.md)).

Every session runs its agent without permission prompts in the chosen directory, with your user's privileges. The backend listens on loopback only.

## Run

Requirements: Go 1.24+, Node 22+, pnpm, git, and the `claude` and `codex` CLIs on `PATH` (or pass `--harness-bin-dir`).

```sh
cd demo/web && pnpm install && pnpm build && cd ..
go run ./cmd/server --web-dir web/out --project-root ~/code --harness-versions ../harness-versions.json
```

Open <http://127.0.0.1:8787>. Projects are the subdirectories of each `--project-root`; you can also type an absolute path in the project picker.

### Logging in

By default each harness runs in an isolated home under `~/.cloudagent-demo/homes/`, so demo sessions stay out of your own Claude Code and Codex history. Log in once per home; the home page and Settings show the exact command:

```sh
CLAUDE_CONFIG_DIR=~/.cloudagent-demo/homes/claude claude   # then /login
CODEX_HOME=~/.cloudagent-demo/homes/codex codex login
```

To use your existing logins instead, start the server with `--home-mode user`.

### Development

```sh
go run ./cmd/server --project-root ~/code      # terminal 1, API on :8787
cd web && pnpm dev                              # terminal 2, UI on http://localhost:3000
```

`next dev` proxies `/api` to the backend; set `DEMO_BACKEND` to point it elsewhere.

## Test

```sh
go test ./...                     # backend, from demo/
DEMO_E2E=1 go test ./cmd/server   # one real prompt per pinned harness; needs .harness-local/ and .tools/
cd web && pnpm check && pnpm build
```

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `--addr` | `127.0.0.1:8787` | Listen address; loopback only |
| `--web-dir` | — | Built frontend (`web/out`); empty serves the API only |
| `--project-root` | — | Directory whose subdirectories are offered as projects; repeatable |
| `--home-mode` | `isolated` | `isolated` demo homes or your real `user` homes |
| `--data-dir` | `~/.cloudagent-demo` | Isolated homes and worktrees |
| `--harness-bin-dir` | — | Prepended to `PATH` to find the CLIs |
| `--harness-versions` | — | `harness-versions.json`, to warn about version mismatches |
| `--claude-models`, `--codex-models` | — | Comma-separated model suggestions |
| `--display-name` | OS user | Name shown in the sidebar |
| `--dev-origin` | `http://localhost:3000` | Extra allowed origin for the dev server |
| `--debug-raw` | `false` | Include each message's raw harness JSON |
