#!/bin/bash
# PreToolUse guard: block deploy commands that must run via CI, not locally.
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

# Block: helm deploy operations (upgrade/install — but not uninstall/list/get)
if echo "$COMMAND" | grep -qE '(^|[;&|])\s*helm\s+(upgrade|install)\b'; then
  deny "BLOCKED: 'helm upgrade/install' must run via CI, not locally. Push to branch and let deploy-preview.yml handle it. Use 'helm uninstall' for cleanup, 'helm get manifest' to inspect."
fi

# Block: local docker builds
if echo "$COMMAND" | grep -qE '(^|[;&|])\s*docker\s+build\b'; then
  deny "BLOCKED: 'docker build' must run via CI. Push to branch — the workflow builds and pushes to GHCR."
fi

# Block: loading images into k3d manually
if echo "$COMMAND" | grep -qE '(^|[;&|])\s*k3d\s+image\s+import\b'; then
  deny "BLOCKED: 'k3d image import' bypasses CI. Push to branch — CI pushes to GHCR and the cluster pulls from there."
fi

# Block: gh run watch (hangs for full run duration)
if echo "$COMMAND" | grep -qE '(^|[;&|])\s*gh\s+run\s+watch\b'; then
  deny "BLOCKED: 'gh run watch' blocks until timeout. Use 'gh run view {id}' (one-shot) to poll status instead."
fi

exit 0
