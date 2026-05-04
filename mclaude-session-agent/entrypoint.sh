#!/bin/bash
set -e

# Bootstrap Nix store on first boot (empty PVC).
# The image ships /nix-bootstrap.tar.gz created during docker build.
# If the PVC is empty (no /nix/store), extract the tarball to seed it.
if [ ! -d "/nix/store" ] && [ -f "/nix-bootstrap.tar.gz" ]; then
    echo "[entrypoint] Seeding /nix from bootstrap tarball..."
    tar xzf /nix-bootstrap.tar.gz -C /
    echo "[entrypoint] Nix store seeded"
fi

# Add nix profile dirs to PATH for all subsequent commands
export PATH="/nix/var/nix/profiles/default/bin:/home/node/.nix-profile/bin:$PATH"
# Source nix env if available
[ -f "/nix/var/nix/profiles/default/etc/profile.d/nix.sh" ] && \
    . "/nix/var/nix/profiles/default/etc/profile.d/nix.sh" || true

# Consume secrets
[ -f "/home/node/.user-secrets/id_rsa" ] && {
    mkdir -p "$HOME/.ssh" && chmod 700 "$HOME/.ssh"
    cp /home/node/.user-secrets/id_rsa "$HOME/.ssh/id_rsa"
    chmod 600 "$HOME/.ssh/id_rsa"
    ssh-keyscan github.com >> "$HOME/.ssh/known_hosts" 2>/dev/null || true
}
[ -f "/home/node/.user-secrets/.gitconfig" ] && \
    cp /home/node/.user-secrets/.gitconfig "$HOME/.gitconfig"

# Read model-provider token from the per-user secret mounted by the user-agent
# (ADR-0091). Fail fast if the token file is missing or empty — user-agent
# should never have created this pod without a valid token; a missing file
# indicates a control-plane bug.
if [ -z "${TOKEN_PROVIDER}" ]; then
    echo "[entrypoint] FATAL: TOKEN_PROVIDER env var is not set" >&2
    exit 1
fi
token_file="/secrets/tokens/${TOKEN_PROVIDER}"
if [ ! -f "${token_file}" ]; then
    echo "[entrypoint] FATAL: token file not found: ${token_file}" >&2
    exit 1
fi
export CLAUDE_CODE_OAUTH_TOKEN=$(cat "${token_file}")
if [ -z "${CLAUDE_CODE_OAUTH_TOKEN}" ]; then
    echo "[entrypoint] FATAL: token file is empty: ${token_file}" >&2
    exit 1
fi

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

# Hand off to session agent — no tmux, spawns Claude as child processes.
# --auth-url activates HTTP challenge-response self-bootstrap (ADR-0054/ADR-0091);
# --nats-creds is NOT passed — the per-project nats-creds Secret key was removed
# by ADR-0063 and the session-agent self-bootstraps via HTTP challenge-response.
exec session-agent \
    --nats-url    "${NATS_URL}" \
    --auth-url    "${CONTROL_PLANE_URL}" \
    --user-id     "${USER_ID}" \
    --project-id  "${PROJECT_ID}" \
    --data-dir    /data \
    --mode        k8s
