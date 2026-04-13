#!/bin/bash
# PostToolUse session manager for spec-first enforcement.
#
# Two responsibilities:
#
# 1. AUTH: when dev-harness runs `touch .claude/.feature-ok`, intercept and
#    create a per-session marker .claude/.feature-ok-{session_id} instead.
#    This scopes authorization to exactly this session — parallel agents each
#    get their own marker and can't interfere with each other.
#
# 2. CLEANUP: when this session runs `git push`, delete its session marker.
#    The next change requires a fresh /dev-harness invocation.

set -euo pipefail

INPUT=$(cat)

COMMAND=$(python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    print(d.get('tool_input', {}).get('command', ''))
except:
    print('')
" <<< "$INPUT" 2>/dev/null || echo "")

SESSION_ID=$(python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    print(d.get('session_id', ''))
except:
    print('')
" <<< "$INPUT" 2>/dev/null || echo "")

[[ -z "$SESSION_ID" ]] && exit 0

REPO_DIR=$(git rev-parse --show-toplevel 2>/dev/null || echo "")
[[ -z "$REPO_DIR" ]] && exit 0

MARKER="$REPO_DIR/.claude/.feature-ok-$SESSION_ID"

# AUTH: dev-harness signals authorization via `touch .claude/.feature-ok`
if echo "$COMMAND" | grep -qE 'touch\s+\.claude/\.feature-ok$'; then
  touch "$MARKER"
  echo "spec-session: session $SESSION_ID authorized for source code writes" >&2
  exit 0
fi

# CLEANUP: revoke authorization after push — next change needs /dev-harness again
if echo "$COMMAND" | grep -qE '(^|[;&|[:space:]])\s*git\s+push\b'; then
  if [[ -f "$MARKER" ]]; then
    rm -f "$MARKER"
    echo "spec-session: session $SESSION_ID authorization revoked after push" >&2
  fi
  exit 0
fi

exit 0
