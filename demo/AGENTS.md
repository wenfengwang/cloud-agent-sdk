# demo/ — Desktop App Demo

A web app that recreates the Code mode of the Claude desktop app and the Codex desktop app on top of the cloudagent SDK: a gin backend that drives Claude Code and Codex through the public `cloudagent` API, and one browser frontend with two shells (`/claude` and `/codex`). It dogfoods the SDK, shows one backend driving both harnesses, and serves as a reference integration. Features the SDK cannot support yet are shown disabled with a reason, never hacked around.

The [root instructions](../AGENTS.md) apply here. This file adds only what is local to `demo/`.

## Where to Look

| Need | Start here |
|---|---|
| Design rules and numbering for this sub-project | [design/AGENTS.md](design/AGENTS.md) |
| Backend, HTTP and SSE API, sessions, worktrees, shutdown, the SDK gap registry | [design/001-architecture.md](design/001-architecture.md) |
| Frontend, the two shells, renderers, visual tokens | [design/002-ui-shells.md](design/002-ui-shells.md) |
| The SDK contract the demo consumes | [../design/001-core-api.md](../design/001-core-api.md) |

## Layout

| Path | Owns |
|---|---|
| `go.mod` | The demo's own Go module; see [Module](design/001-architecture.md#module) |
| `cmd/server/` | The backend executable: flags, wiring, graceful shutdown |
| `internal/` | Backend packages (the demo's own, not the SDK's) |
| `web/` | The frontend: a pnpm, TypeScript, Vite project |
| `design/` | Demo designs, numbered independently of the root `design/` |

## Rules

- **Public SDK API only.** Import only the root package `github.com/wenfengwang/cloud-agent-sdk`. Never import anything under the SDK's `internal/`, even though the Go toolchain would allow it from this module path.
- **Separate module.** gin and every other demo dependency stay in `demo/go.mod`; the SDK's `go.mod` never changes for the demo. Run `go test ./...` from `demo/`, because the root `go test ./...` does not include a nested module.
- **SDK gaps stay visible.** A desktop-app feature the SDK cannot support is a disabled control with a reason from the [gap registry](design/001-architecture.md#sdk-gaps), not a workaround. A new gap is added there first.
- **Frontend tooling.** pnpm only, TypeScript in strict mode, format with prettier, lint with eslint. Run the frontend's tests and lint before finishing.
- **No private data.** Fixtures, tests, screenshots, and docs use fictional names, projects, and session titles. Never copy content from real sessions or reference screenshots.
- **No official branding.** No logos, icons, mascots, brand glyphs, or product names as the demo's own branding. Naming the harnesses "Claude Code" and "Codex" is fine. Icons come from an open-source outline set. No font files are bundled; font stacks use only generic system keywords (`system-ui`, `-apple-system`, `ui-sans-serif`, `ui-serif`, `ui-monospace`, and the CSS generic families).
- **Commits** use the scope `demo`.
