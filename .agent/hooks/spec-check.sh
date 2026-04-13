#!/bin/bash
# PreToolUse guard: block source code edits unless this session has been authorized
# by running /dev-harness (which runs `touch .claude/.feature-ok`, intercepted by
# spec-session.sh PostToolUse hook to create a per-session .feature-ok-{session_id}).

set -euo pipefail

echo "spec-check: invoked at $(date)" >> /tmp/spec-check-debug.log

INPUT=$(cat)
echo "spec-check: input=$(echo "$INPUT" | head -c 200)" >> /tmp/spec-check-debug.log

FILE_PATH=$(python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    print(d.get('tool_input', {}).get('file_path', ''))
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

[[ -z "$FILE_PATH" ]] && exit 0

# Allow: docs, memory, skills, config, non-source files
case "$FILE_PATH" in
  */docs/*|*/memory/*|*CLAUDE.md|*SKILL.md|*MEMORY.md|*.md|*.json|*.yml|*.yaml|*.toml|*.sh|*.gitignore|*tsbuildinfo*)
    exit 0 ;;
esac

# Only enforce on app source code
case "$FILE_PATH" in
  */mclaude-web/src/*|*/mclaude-control-plane/*.go|*/mclaude-session-agent/*.go|*/mclaude-cli/*.go)
    : ;;
  *)
    exit 0 ;;
esac

# Find repo root
REPO_DIR=$(git -C "$(dirname "$FILE_PATH")" rev-parse --show-toplevel 2>/dev/null || echo "")
[[ -z "$REPO_DIR" ]] && exit 0

deny() {
  python3 -c "
import json, sys
print(json.dumps({
  'hookSpecificOutput': {
    'hookEventName': 'PreToolUse',
    'permissionDecision': 'deny',
    'permissionDecisionReason': sys.argv[1]
  }
}))" "$1"
  exit 0
}

# Check per-session marker (created by spec-session.sh PostToolUse hook)
if [[ -n "$SESSION_ID" ]]; then
  MARKER="$REPO_DIR/.claude/.feature-ok-$SESSION_ID"
  if [[ -f "$MARKER" ]]; then
    MARKER_AGE=$(( $(date +%s) - $(stat -f %m "$MARKER" 2>/dev/null || echo 0) ))
    if [[ $MARKER_AGE -lt 21600 ]]; then
      exit 0
    fi
  fi
fi

deny "SPEC-FIRST VIOLATION: this session is not authorized to write source code.

File: $FILE_PATH
Session: ${SESSION_ID:-unknown}

REQUIRED: Source code writes must go through /dev-harness, which reads the spec
and implements only what the spec describes.

  /feature-change <description>   — update spec if needed, then calls /dev-harness
  /dev-harness <component>        — implement spec gaps directly

/dev-harness authorizes this session (session-scoped, expires after 6 hours or push)."
