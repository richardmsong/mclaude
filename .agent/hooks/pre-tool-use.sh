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
# Exception: set LOCAL_DEPLOY=1 to allow local helm installs (e.g. k3d dev cluster).
if echo "$COMMAND" | grep -qE '(^|[;&|])\s*helm\s+(upgrade|install)\b'; then
  if [ "${LOCAL_DEPLOY:-}" = "1" ]; then
    : # allow — local deploy mode
  else
    deny "BLOCKED: 'helm upgrade/install' must run via CI, not locally. Push to branch and let deploy-preview.yml handle it. Set LOCAL_DEPLOY=1 to override for local k3d deploys."
  fi
fi

# Block: local docker builds
# Exception: set LOCAL_DEPLOY=1 to allow local builds (e.g. k3d dev cluster).
if echo "$COMMAND" | grep -qE '(^|[;&|])\s*docker\s+build\b'; then
  if [ "${LOCAL_DEPLOY:-}" = "1" ]; then
    : # allow — local deploy mode
  else
    deny "BLOCKED: 'docker build' must run via CI. Push to branch — the workflow builds and pushes to GHCR. Set LOCAL_DEPLOY=1 to override for local k3d builds."
  fi
fi

# Block: loading images into k3d manually
# Exception: set LOCAL_DEPLOY=1 to allow local imports (e.g. k3d dev cluster).
if echo "$COMMAND" | grep -qE '(^|[;&|])\s*k3d\s+image\s+import\b'; then
  if [ "${LOCAL_DEPLOY:-}" = "1" ]; then
    : # allow — local deploy mode
  else
    deny "BLOCKED: 'k3d image import' bypasses CI. Push to branch — CI pushes to GHCR and the cluster pulls from there. Set LOCAL_DEPLOY=1 to override for local k3d deploys."
  fi
fi

# Block: gh run watch (hangs for full run duration)
if echo "$COMMAND" | grep -qE '(^|[;&|])\s*gh\s+run\s+watch\b'; then
  deny "BLOCKED: 'gh run watch' blocks until timeout. Use 'gh run view {id}' (one-shot) to poll status instead."
fi

# Block: git apply (bypasses /feature-change workflow)
if echo "$COMMAND" | grep -qE '(^|[;&|])\s*git\s+apply\b'; then
  deny "BLOCKED: 'git apply' bypasses the spec→dev-harness→evaluator loop. Use /feature-change to make code changes."
fi

# Block: mutating kubectl commands (create, apply, patch, delete, rollout restart, exec)
# Read-only commands (get, logs, describe, port-forward, rollout status, config, cluster-info) are always allowed.
# Exception: set KUBECTL_MUTATE=1 for one-off debugging.
if echo "$COMMAND" | grep -qE '(^|[;&|])\s*kubectl\s+(create|apply|patch|delete|replace|edit|scale|rollout\s+restart|exec)\b'; then
  if [ "${KUBECTL_MUTATE:-}" = "1" ]; then
    : # allow — debug mode
  else
    deny "BLOCKED: mutating kubectl command. Cluster state should be managed by Helm + the reconciler, not manual kubectl. Use /feature-change for lasting changes. Set KUBECTL_MUTATE=1 if you solemnly swear this is just for debugging."
  fi
fi

exit 0
