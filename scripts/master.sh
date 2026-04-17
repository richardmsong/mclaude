#!/bin/bash
# Launch the mclaude master/orchestrator Claude Code session.
#
# Master session: restricted to read-only exploration, docs editing,
# git/kubectl/gh/helm ops, and spawning agents.
#
# Subagents (dev-harness, etc.) inherit only .claude/settings.json
# which allows everything, so they can edit/build/test freely.

# Prefer Opus 4.7 1M context if available on this machine/plan.
# Result is cached per-machine; delete the cache file to re-probe.
CACHE_FILE="$HOME/.cache/mclaude/master-model"
if [ -r "$CACHE_FILE" ]; then
  MODEL=$(cat "$CACHE_FILE")
else
  MODEL="claude-opus-4-7[1m]"
  timeout 10 claude --model "$MODEL" -p "probe" --max-turns 0 </dev/null &>/dev/null || MODEL="opus"
  mkdir -p "$(dirname "$CACHE_FILE")"
  printf '%s' "$MODEL" > "$CACHE_FILE"
fi

exec claude \
  --model "$MODEL" \
  --disallowedTools \
    "Edit(mclaude-control-plane/**/*.go)" \
    "Write(mclaude-control-plane/**/*.go)" \
    "Edit(mclaude-session-agent/**/*.go)" \
    "Write(mclaude-session-agent/**/*.go)" \
    "Edit(mclaude-cli/**/*.go)" \
    "Write(mclaude-cli/**/*.go)" \
    "Edit(mclaude-relay/**/*.go)" \
    "Write(mclaude-relay/**/*.go)" \
    "Edit(mclaude-connector/**/*.go)" \
    "Write(mclaude-connector/**/*.go)" \
    "Edit(mclaude-web/src/**)" \
    "Write(mclaude-web/src/**)" \
  $@
