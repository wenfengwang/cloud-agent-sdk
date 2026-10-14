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
