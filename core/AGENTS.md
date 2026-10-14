# core — Implementation

Package `core` and everything under `core/internal/` implement cloud-agent-sdk: the agent runtime, the turn lifecycle, the index, and the harness adapters. Behavior is owned by [design 001](../design/001-core-api.md); tests follow [design 002](../design/002-testing.md).

## Rules

- All logic lives here. Language SDKs never add behavior; they only expose this package.
- Applications, examples, and the demo never import `core`; they import an SDK such as [go/](../go/AGENTS.md).
- A change to the exported surface of `core` changes the Go SDK in the same commit; `go/reexport_test.go` fails until both match.
- Adapter-specific rules live in [internal/claude/AGENTS.md](internal/claude/AGENTS.md) and [internal/codex/AGENTS.md](internal/codex/AGENTS.md).
