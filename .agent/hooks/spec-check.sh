#!/bin/bash
# Spec-first guard: block source code edits unless /feature-change has been run recently.
# Fires on Edit and Write tool use. Checks for .claude/.feature-ok marker touched by
# the /feature-change skill. Marker expires after 6 hours.

set -euo pipefail

INPUT=$(cat)
FILE_PATH=$(python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    print(d.get('tool_input', {}).get('file_path', ''))
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

MARKER="$REPO_DIR/.claude/.feature-ok"

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

# Check marker exists and is < 6 hours old
if [[ -f "$MARKER" ]]; then
  MARKER_MTIME=$(stat -f %m "$MARKER" 2>/dev/null || echo 0)
  MARKER_AGE=$(( $(date +%s) - MARKER_MTIME ))
  if [[ $MARKER_AGE -lt 21600 ]]; then
    exit 0
  fi
fi

deny "SPEC-FIRST VIOLATION: /feature-change has not been run for this change.

File: $FILE_PATH

REQUIRED: Run /feature-change <description> before writing implementation code.
  - New feature: /feature-change updates spec first, then calls /dev-harness
  - Bug fix (spec already correct): /feature-change verifies spec, then calls /dev-harness

Running /feature-change will touch .claude/.feature-ok and unlock code writes for 6 hours."
