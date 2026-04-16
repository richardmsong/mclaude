#!/bin/bash
set -e

# Consume secrets
[ -f "/home/node/.user-secrets/id_rsa" ] && {
    mkdir -p "$HOME/.ssh" && chmod 700 "$HOME/.ssh"
    cp /home/node/.user-secrets/id_rsa "$HOME/.ssh/id_rsa"
    chmod 600 "$HOME/.ssh/id_rsa"
    ssh-keyscan github.com >> "$HOME/.ssh/known_hosts" 2>/dev/null || true
}
[ -f "/home/node/.user-secrets/.gitconfig" ] && \
    cp /home/node/.user-secrets/.gitconfig "$HOME/.gitconfig"
[ -f "/home/node/.user-secrets/oauth-token" ] && \
    export CLAUDE_CODE_OAUTH_TOKEN=$(cat /home/node/.user-secrets/oauth-token)

# Seed user config (emptyDir is fresh each boot — always copy)
for f in CLAUDE.md settings.json; do
    [ -f "/home/node/.claude-seed/$f" ] && \
        cp "/home/node/.claude-seed/$f" "$HOME/.claude/$f"
done
for d in commands skills; do
    [ -d "/home/node/.claude-seed/$d" ] && \
        cp -r "/home/node/.claude-seed/$d" "$HOME/.claude/$d"
done

# Link JSONL history to PVC (Claude's own persistence for --resume)
mkdir -p /data/projects
ln -sf /data/projects "$HOME/.claude/projects"

# Skip onboarding. bypassPermissions disables Claude Code's built-in permission dialogs —
# guard hooks (platform-level enforcement) are the permission layer in pods, not Claude Code's UI prompts.
echo '{"hasCompletedOnboarding":true,"bypassPermissions":true}' > "$HOME/.claude.json"

# Git setup (credential helper setup + bare repo clone/init) is handled by
# the Go session-agent binary. The agent reads GIT_URL and GIT_IDENTITY_ID
# env vars and performs: credential helper setup → initial clone (or scratch
# init if no GIT_URL) → NATS connection → session lifecycle.

# Shared memory — symlink each worktree's memory dir to /data/shared-memory/
mkdir -p /data/shared-memory
(while true; do
    for dir in "$HOME/.claude/projects"/*/; do
        [ -d "$dir" ] && [ ! -L "${dir}memory" ] && {
            rm -rf "${dir}memory"
            ln -s /data/shared-memory "${dir}memory"
        }
    done
    sleep 5
done) &

# Wait for dockerd if enabled
[ "${DOCKER_ENABLED}" = "true" ] && \
    while [ ! -S /var/run/docker.sock ]; do sleep 0.5; done

# Hand off to session agent — no tmux, spawns Claude as child processes
exec session-agent \
    --nats-url    "${NATS_URL}" \
    --nats-creds  "/home/node/.user-secrets/nats-creds" \
    --user-id     "${USER_ID}" \
    --project-id  "${PROJECT_ID}" \
    --data-dir    /data \
    --mode        k8s
