#!/bin/bash
# Launch the mclaude master/orchestrator Claude Code session.
#
# Master session: restricted to read-only exploration, docs editing,
# git/kubectl/gh/helm ops, and spawning agents.
#
# Subagents (dev-harness, etc.) inherit only .claude/settings.json
# which allows everything, so they can edit/build/test freely.

exec claude \
  --allowedTools \
    "Read" \
    "Glob" \
    "Grep" \
    "Agent" \
    "WebFetch" \
    "WebSearch" \
    "Edit(docs/**)" \
    "Write(docs/**)" \
    "Edit(.agent/**)" \
    "Write(.agent/**)" \
    "Bash(git *)" \
    "Bash(kubectl *)" \
    "Bash(gh *)" \
    "Bash(helm *)" \
    "Bash(ls *)" \
    "Bash(cat *)" \
  "$@"
