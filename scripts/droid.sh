#!/bin/bash
# Launch the mclaude master/orchestrator Droid session.
#
# Master session: restricted to read-only exploration, docs editing,
# git/kubectl/gh/helm ops, and spawning agents.
#
# Subagents (dev-harness, etc.) inherit only .factory/droids
# which allows everything, so they can edit/build/test freely.

exec droid \
  --disabled-tools \
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
  "$@"
