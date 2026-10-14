# go — Go SDK

Package `cloudagent` (`github.com/wenfengwang/cloud-agent-sdk/go`) is the Go SDK. It is a thin portal over [core](../core/AGENTS.md).

## Rules

- No logic: every exported identifier is an alias, constant, variable, or one-line forwarding function for its counterpart in `core`.
- Export exactly what `core` exports; `reexport_test.go` fails when the two diverge.
- Doc comments here describe the API for users; implementation notes belong in `core`.
