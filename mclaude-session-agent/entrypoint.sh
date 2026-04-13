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

# Git setup (bare repo — worktrees created by session agent)
# Every project gets a bare repo. Git-backed projects clone from GIT_URL;
# scratch projects (no GIT_URL) get an empty bare repo initialized in place.
# This means the session agent's worktree machinery works uniformly for all projects.
if [ ! -d "/data/repo/HEAD" ]; then
    if [ -n "$GIT_URL" ]; then
        git clone --bare "$GIT_URL" /data/repo || {
            echo "[entrypoint] Git clone failed — exiting for restart"
            exit 1
        }
    else
        git init --bare /data/repo
        # Set default branch to main (some git versions default to master).
        git -C /data/repo symbolic-ref HEAD refs/heads/main
        # Create an initial empty commit so worktrees have something to branch from.
        # git commit requires a working tree, so use plumbing commands instead.
        TREE=$(git -C /data/repo hash-object -t tree /dev/null)
        COMMIT=$(GIT_AUTHOR_NAME="mclaude" GIT_AUTHOR_EMAIL="mclaude@local" \
                 GIT_COMMITTER_NAME="mclaude" GIT_COMMITTER_EMAIL="mclaude@local" \
                 git -C /data/repo commit-tree "$TREE" -m "init")
        git -C /data/repo update-ref refs/heads/main "$COMMIT"
    fi
else
    if [ -n "$GIT_URL" ]; then
        git -C /data/repo fetch --all --prune || true
    fi
fi
mkdir -p /data/worktrees

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
