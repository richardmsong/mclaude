#!/bin/bash
# PreToolUse guard: block git apply (bypasses the /feature-change loop).
# Receives tool input JSON on stdin.

COMMAND=$(python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    print(d.get('tool_input', {}).get('command', ''))
except:
    print('')
")

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

# Block: git apply (bypasses /feature-change workflow)
if echo "$COMMAND" | grep -qE '(^|[;&|])\s*git\s+apply\b'; then
  deny "BLOCKED: 'git apply' bypasses the spec-first /feature-change loop. Use /feature-change to make code changes."
fi

exit 0
