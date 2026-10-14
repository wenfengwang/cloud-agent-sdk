# cloud-agent-sdk

A toolkit for running complete, programmable, controllable Claude Code and Codex instances in the cloud.

> **Status: v1.** The [core API](design/001-core-api.md) is implemented and tested against the real pinned CLIs.

## Why

Driving Claude Code or Codex from another system (a chat backend, a job scheduler, a review bot) means starting turns, stopping them, and knowing at any moment what the agent is doing and has done. The official SDKs are thin, inconsistent across languages and harnesses, and have no Go version, and the agent's state exists only in a live stream or in undocumented local files. cloud-agent-sdk closes that gap with one typed, controllable API over both harnesses. See [WHY.md](WHY.md) for the full reasoning.

## What it provides

| | |
|---|---|
| **Complete** | The real Claude Code and Codex CLIs at pinned versions, with their native tools, MCP servers, subagents, and configuration. Not a reimplementation. |
| **Programmable** | One typed object model for both harnesses: an agent, its turns, and the messages each turn produces, with typed tool calls and results. Normalized once, at the source. The same messages are readable while a turn runs and after it ends. |
| **Controllable** | The toolkit owns the harness process: start a turn, stop it gracefully, close the agent, and nothing outlives it. |

Conversation data is stored once, in the harness's own home directory. The toolkit keeps only an index that points into it.

## Quickstart

Requirements: Go 1.24 or later, and the `claude` or `codex` CLI on `PATH` and logged in. The supported versions are pinned in [harness-versions.json](harness-versions.json); `scripts/install-harnesses.sh` installs exactly those into `.tools/` without touching a global install.

```bash
git clone https://github.com/wenfengwang/cloud-agent-sdk.git
cd cloud-agent-sdk

# Optional: use the pinned CLIs.
./scripts/install-harnesses.sh
export PATH="$PWD/.tools/node_modules/.bin:$PATH"

go run ./examples/quickstart -harness claudecode "List the files in this directory"
go run ./examples/quickstart -harness codex -workdir /path/to/repo "Explain main.go"
```

The [quickstart](examples/quickstart/main.go) runs one turn and prints each message as it arrives; press Ctrl-C to stop the turn:

```
> List the files in this directory
[shell] $ ls -la
  => ok (exit 0)
This directory has one file:

- `main.go` (29 bytes)

--- 01a13caf-4e53-731c-bd74-bf49d1482e71: 4 input, 99 output, 22913 cache-read tokens
```

## Example

```go
import cloudagent "github.com/wenfengwang/cloud-agent-sdk/go"

agent, err := cloudagent.New(cloudagent.ClaudeCode, cloudagent.Options{WorkDir: "."})
if err != nil {
	return err
}
defer agent.Close()

turn, err := agent.Turn(ctx, "fix the failing test", cloudagent.TurnOptions{})
if err != nil {
	return err
}

for msg, err := range turn.Messages() {
	if err != nil {
		return err
	}
	if call := msg.ToolCall; call != nil {
		if sh, ok := call.Input.(*cloudagent.ShellInput); ok {
			fmt.Println("$", sh.Command)
		}
	}
}

res, err := turn.Result()
```

Swap `cloudagent.ClaudeCode` for `cloudagent.Codex` and the same code drives Codex.

## Supported harnesses

| Harness | Constant | Protocol |
|---|---|---|
| Claude Code | `cloudagent.ClaudeCode` | stream-json |
| Codex | `cloudagent.Codex` | `codex app-server` (JSON-RPC) |

Each harness is supported at exactly the version pinned in [harness-versions.json](harness-versions.json).

## Design

Behavior and rationale live in [design/](design/), starting with [001 — Core API](design/001-core-api.md).
