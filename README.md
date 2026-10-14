# cloud-agent-sdk

A toolkit for running complete, programmable, controllable Claude Code and Codex instances in the cloud.

> **Status: design phase.** Nothing is implemented yet. The API below is the [draft core design](design/001-core-api.md).

## Why

Run Claude Code or Codex inside a cloud VM and drive it from another system: a chat backend, a job scheduler, a review bot. That system needs to start turns, stop them, and know at any moment what the agent is doing and what it has done. Neither harness is built for this, and the official SDKs do not close the gap.

### The official SDKs are thin

The Claude Agent SDK and the Codex SDKs wrap the CLI for one use: send a prompt, iterate over events. Anything beyond that is missing, inconsistent, or unstable.

- **Two unrelated contracts.** Claude Code speaks stream-json with a control protocol. Codex speaks JSON-RPC through `codex app-server`, or an unversioned JSONL stream through `codex exec --json`. The same action has different names and shapes (`Bash` vs `commandExecution`, `Edit` vs `fileChange`), so every consumer re-normalizes both, usually incompletely.
- **Inconsistent surfaces even within one vendor.** The TypeScript Codex SDK runs `codex exec` and offers start, resume, and run; the Python one runs `app-server` and also offers list, read, fork, steer, and interrupt. The Python Claude SDK drops fields and options the TypeScript one exposes ([claude-agent-sdk-python#1299](https://github.com/anthropics/claude-agent-sdk-python/issues/1299)).
- **Weak control.** Claude's `interrupt()` also kills backgrounded subagents and tasks ([claude-agent-sdk-typescript#352](https://github.com/anthropics/claude-agent-sdk-typescript/issues/352)). Cancelling a turn stops the CLI, not the process tree, so shell commands and MCP servers it started can outlive it.
- **Unstable foundations.** Codex documents `app-server` as experimental and not supported for production, has removed methods such as `thread/rollback`, and removed `codex mcp-server` entirely in 0.154. A schema change in `codex exec --json` silently broke parsers ([codex#41216](https://github.com/openai/codex/issues/41216)).
- **No Go.** Both vendors ship TypeScript and Python only ([claude-agent-sdk-python#498](https://github.com/anthropics/claude-agent-sdk-python/issues/498)).

### The state is not exposed

What an agent did and is doing exists in one of two places, and neither is an API.

- **Only in the live stream.** Tool calls in flight, subagent progress, background-task status, and per-turn usage pass through the stream once. A consumer that is not attached, or reconnects, loses them. When Claude hits `maxTurns`, no result message is emitted at all ([claude-agent-sdk-typescript#427](https://github.com/anthropics/claude-agent-sdk-typescript/issues/427)). Usage is reported cumulatively per session or thread, so per-turn metering means diffing counters yourself ([claude-agent-sdk-typescript#469](https://github.com/anthropics/claude-agent-sdk-typescript/issues/469)).
- **Only in local files.** Full history lives in harness-private JSONL: `~/.claude/projects/<encoded-workdir>/<session>.jsonl` and `~/.codex/sessions/.../rollout-*.jsonl`. Their formats are undocumented, and Codex maintainers advise against building on them ([codex#45251](https://github.com/openai/codex/issues/45251)). The SDK readers that exist are lossy projections: Claude's `getSessionMessages()` omits the parent links needed to rebuild the conversation ([#431](https://github.com/anthropics/claude-agent-sdk-typescript/issues/431)), and `listSessions()` spawns a full CLI process just to list files ([#268](https://github.com/anthropics/claude-agent-sdk-typescript/issues/268)). Codex history drops the per-item timestamps the live stream carries ([codex#46926](https://github.com/openai/codex/issues/46926)).
- **Resume depends on the filesystem.** A harness finds its transcript by a path derived from its home directory and working directory. Run a turn with a different home or cwd and resume fails with "No conversation found" while the file is still on disk.

In the cloud this is the whole problem. The system driving the agent is not on the VM, cannot tail its files, and cannot afford to miss a stream event.

## What it provides

| | |
|---|---|
| **Complete** | The real Claude Code and Codex CLIs at pinned versions, with their native tools, MCP servers, subagents, and configuration. Not a reimplementation. |
| **Programmable** | One typed object model for both harnesses: an agent, its turns, and the messages each turn produces, with typed tool calls and results. Normalized once, at the source. The same messages are readable while a turn runs and after it ends. |
| **Controllable** | The toolkit owns the harness process: start a turn, stop it gracefully, close the agent, and nothing outlives it. |

Conversation data is stored once, in the harness's own home directory. The toolkit keeps only an index that points into it.

## Example

```go
import cloudagent "github.com/wenfengwang/cloud-agent-sdk"

agent, err := cloudagent.New(cloudagent.Claude, cloudagent.Options{WorkDir: "."})
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

Swap `cloudagent.Claude` for `cloudagent.OpenAI` and the same code drives Codex.

## Supported harnesses

| Harness | Protocol |
|---|---|
| Claude Code | stream-json |
| Codex | `codex app-server` (JSON-RPC) |

Each harness is supported at exactly the version pinned in [harness-versions.json](harness-versions.json).

## Design

Behavior and rationale live in [design/](design/), starting with [001 — Core API](design/001-core-api.md).
