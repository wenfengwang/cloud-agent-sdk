# internal/claude — Claude Code Adapter

Drives Claude Code through `claude --print` with stream-json on stdin and stdout. Behavior is owned by [design 001](../../../design/001-core-api.md); fixtures and tests follow [design 002](../../../design/002-testing.md).

## Protocol Contract

- One process per turn: `claude --print --output-format stream-json --input-format stream-json --verbose --replay-user-messages --permission-mode bypassPermissions [--model <m>]`, plus `--session-id <id>` on the first turn and `--resume <id>` afterwards, or whenever the session transcript already exists (a failed start may have created it). The adapter generates the session ID in `New`. Claude Code writes the transcript before it echoes the prompt, so a turn killed after acceptance can be resumed.
- The prompt is one stream-json `user` line. The turn is accepted when Claude Code echoes it back (`isReplay: true`); the echo is the turn's user message and its `uuid` is `user_message_uuid`.
- The `result` line ends the turn: the adapter closes stdin and the process exits.
- Graceful stop is the `interrupt` control request. Before sending it, `Interrupt` lists the process groups of the harness's descendants (`proc.DescendantGroups`); once the process has exited, those groups are killed, so a tool command in its own group does not survive an honored stop. Kill uses `proc.KillTree`: SIGKILL to the process group and to the group of every descendant, listed before the kill. The group is also killed after every normal exit.
- Message IDs: the user message is the entry `uuid`; every content block is `<uuid>:<block index>`. Stream lines and transcript entries share uuids.
- Read-back: `<home>/projects/<EncodeWorkDir(resolved workdir)>/<session-id>.jsonl`. A turn is the user entry with `user_message_uuid` and everything after it until a user entry with another `promptId`. Subagent entries come from `<session-id>/subagents/agent-*.jsonl`, matched to their call by `toolUseId` in the `.meta.json`, and are merged in by timestamp.

## Behavior of the Pinned Version (2.1.300)

- `Glob` and `Grep` are not in the default tool set, so the `glob-grep` scenario records `Bash` calls. The adapter keeps the default tool set (no `--tools`); the `Glob` and `Grep` mappings apply when a user's settings enable them.
- `TodoWrite` does not exist; `TaskCreate` and `TaskUpdate` replace it, outside the default set. Task tools are `unknown` tool calls (design 001, Non-Goals).
- In bypass mode Claude Code steers the model toward `Bash`, so `read-file`, `write-file`, and `edit-file` sometimes record `Bash` calls. Re-record a scenario whose structural expectations fail.
- `thinking` blocks carry only a signature; their text is empty in both the stream and the transcript.
- A successful `Bash` result reports no exit code, so it is mapped to exit code 0. A failed one starts with `Exit code N`, which becomes `ExitCode` and is removed from `Output`. A rejected or interrupted call has no exit code.
- The subagent tool is listed as `Task` in the `system` init tool list but called as `Agent`; both names map to `subagent` (design 001).
- A subagent's final assistant message, thinking included, is written to its transcript but not streamed; its text arrives as the `Agent` tool result. Read-back returns that extra message, as design 001 lists under live and read-back differences.
- Subagent prompts and `[Request interrupted by user ...]` notices are user text, not the turn's user message, and are not emitted.

## Fixtures

- Record with `scripts/install-harnesses.sh` and then `CLOUDAGENT_RECORD=1 go test ./internal/claude -run 'TestRecord$'`. Set `CLOUDAGENT_SCENARIO=<name>` to record one scenario, then run `go test ./internal/claude -run TestTranslate -update` and review the golden diff.
- Besides the placeholders of design 002, fixtures use `@SESSION_ID@` for the recorded session ID. Fake replays substitute the adapter's own ID for it from `--session-id` or `--resume`.
- Never edit fixtures by hand.
