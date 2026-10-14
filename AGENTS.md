# AGENTS.md

cloud-agent-sdk gives Claude Code and Codex one programmable Go API. The only public package is the module root, `cloudagent` (`github.com/wenfengwang/cloud-agent-sdk`); everything else lives under `internal/`.

Read the nearest `AGENTS.md` for every directory you touch.

## Where to Look

| Need | Start here |
|---|---|
| Architecture, behavior, and rationale | [design/AGENTS.md](design/AGENTS.md) |

## Rules

- **English only.** Code, comments, documentation, design docs, commit messages, PR text, test fixtures, and every other artifact in this repository are written in English.
- **Design first.** Before changing behavior, read the owning design under [design/](design/) and update it in the same change. If no design owns the behavior, write one before the code.
- **Public API through design.** The exported surface of package `cloudagent` changes only together with its owning design.
- **Tests first.** New behavior ships with tests; bug fixes ship with a regression test. Run `go test ./...` before finishing.
- **Go conventions.** Format with `gofmt`; wrap propagated errors with context.
- **Harness credentials.** Local credentials live only in the git-ignored `.harness-local/` and are passed only to the pinned Claude Code and Codex CLIs. Never call a model API directly, and never commit, log, or print a credential.
- **Commits.** Conventional Commits (`type(scope): message`), one coherent change per commit that keeps the tree buildable.

## Editing These Instructions

- This file holds only repository-wide rules. Local rules live in the nearest subdirectory `AGENTS.md`; design rationale and invariants live in `design/`.
- Give each fact one owner. Link to it instead of restating it.
- `CLAUDE.md` is a symlink to this file; edit `AGENTS.md`.
