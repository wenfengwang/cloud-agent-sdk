# internal/codex — Codex Adapter

Drives Codex through `codex app-server`. Behavior is owned by [design 001](../../design/001-core-api.md).

## Protocol Contract

- The pinned Codex version lives in [harness-versions.json](../../harness-versions.json).
- [schema/app-server.v2.schema.json](schema/app-server.v2.schema.json) is the app-server protocol of that version. Code in this package must match it; never edit it by hand.
- When bumping the pinned version, regenerate the schema in the same change and review its diff for protocol changes:

  ```bash
  out=$(mktemp -d)
  pnpm dlx @openai/codex@<version> app-server generate-json-schema --out "$out"
  cp "$out/codex_app_server_protocol.v2.schemas.json" internal/codex/schema/app-server.v2.schema.json
  ```

## Layout

| File | Owns |
|---|---|
| `harness.go` | `New`, `StartTurn` (process start, `initialize`, `thread/start` or `thread/resume`, `turn/start`), `ReadTurn`, `Close` |
| `process.go` | The `app-server` process (own process group) and its JSON-RPC connection; unbounded notification queue |
| `run.go` | One accepted turn: the notification `stream` (the turn's thread and its subagent threads), interrupt and the app-server shutdown after it, kill, usage |
| `translate.go` | App-server items to canonical messages; the only mapping code, including subagent calls and `ParentToolUseID` |
| `rollout.go` | Read-back from `<home>/sessions/**/rollout-*-<thread>.jsonl` and the rollouts of the turn's subagent threads, converted into app-server items |
| `shell.go` | Login-shell wrapper removal for both the stream's string and the rollout's argv |
| `testdata/<scenario>/` | Recorded fixtures (design 002) and `messages.golden.json` |

## Rules

- Non-test code imports only the standard library, `internal/harness`, and `internal/message`.
- Live runs and read-back share `translate.go`: read-back converts each rollout item into the app-server item shape first. Never map a tool in two places.
- A turn's outcome comes from `turn/completed` alone. `configWarning`, `warning`, and `error` notifications (retrying or not) never fail a turn and are never emitted.
- A turn follows the subagent threads it starts (`subAgentActivity` kind `started`); their items carry the spawn call's ID as `ParentToolUseID`. Notifications of every other thread are ignored, and usage counts the turn's own thread only.
- Server-initiated requests are answered at once with a JSON-RPC error, so the harness never waits on the adapter.
- Fixtures are never edited by hand. Record them with `CLOUDAGENT_RECORD=1 go test ./internal/codex -run TestRecord` (one scenario: add `CLOUDAGENT_SCENARIO=<name>`), then `go test ./internal/codex -run TestTranslate -update` and review the golden diff.
- `Harness.config` is the test-only configuration override sent with `thread/start` and `thread/resume`; production code never sets it.

## Harness Quirks

- `initialize` sends `clientInfo.name` `codex_cli_rs`, the pinned CLI's default originator. Codex forwards the name as the originator of model requests, and providers that accept only official Codex clients reject any other name with HTTP 403 ("This account only allows Codex official clients").
- Codex reports the configuration warnings as `configWarning` (before the thread exists) and `warning` (per turn) notifications.
- The rollout holds only item completions (`event_msg` `item_completed`), with the same item IDs and the start and completion times of the live notifications; read-back rebuilds the live order from those times. Codex writes it asynchronously, so `ReadTurn` waits for the turn's `task_complete` or `turn_aborted` event when the turn completed live.
- `turn/interrupt` ends the turn but not its running commands: Codex tells the model they "may still be running in the background", and a command such as `sleep 30` keeps running and keeps its item open. Commands run in their own process groups, so killing the app-server's process group does not reach them; Codex ends them when the app-server shuts down (stdin closed) and only then writes their `item_completed`, after the `turn_aborted` event. That is why the run shuts the app-server down after an interrupted turn and ends only once the process is gone: when the turn's result is known, no command survives and the rollout is complete. Read-back yields the tool call but not that late result, as the live run did.
- Codex offers the model `apply_patch`, but the model writes and edits files with shell commands unless told otherwise, so the recording override (`recordConfig`) adds developer instructions that ask for `apply_patch`; with them, writes and edits arrive as `fileChange` items.
- The rollout's `FileChange.changes` is a map from path to `{type, content | unified_diff}`; read-back converts it into the app-server list, and the translator sorts changes by path because the map order is not stable.
- With the local provider, the model catalog does not support the reasoning summary parameter, so the `summary: "auto"` of `turn/start` takes no effect (the rollout's `turn_context` shows `summary: "none"`) and no fixture has a `reasoning` item.
- The subagent's task is encrypted in the spawn call, and `subAgentActivity` carries only the agent path; `SubagentInput.Description` is that path and `Prompt` is empty. The subagent result is the text of the subagent thread's last `agentMessage`.
- A subagent rollout starts with a copy of its parent's history; read-back takes from it only the items of its own thread in turns whose `root_turn_id` is the read turn.
- The `.harness-local/codex-config.toml` flag `api_key_model_discovery = true` makes `testenv` treat `true` as a credential; the recording test drops plain TOML scalars from the scrub list so that recorded JSON stays valid.
