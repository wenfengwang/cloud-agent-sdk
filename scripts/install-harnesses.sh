#!/usr/bin/env bash
# Installs the harness CLIs pinned in harness-versions.json into the
# git-ignored .tools/ directory. It never touches a global install.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

claude_version="$(node -p "require('./harness-versions.json').claude")"
codex_version="$(node -p "require('./harness-versions.json').codex")"

mkdir -p .tools
pnpm add --dir .tools "@anthropic-ai/claude-code@${claude_version}" "@openai/codex@${codex_version}"
# pnpm skips dependency build scripts by default; run Claude Code's
# postinstall, which puts the platform-native binary in place.
(cd .tools && node node_modules/@anthropic-ai/claude-code/install.cjs)

bin="$root/.tools/node_modules/.bin"
check() {
  local name="$1" want="$2" out
  out="$("$bin/$name" --version)"
  if ! printf '%s\n' "$out" | tr -s '[:space:]' '\n' | grep -qx -- "$want"; then
    echo "install-harnesses: $name --version reported '$out', want $want" >&2
    exit 1
  fi
  echo "$name $want installed at $bin/$name"
}
check claude "$claude_version"
check codex "$codex_version"
