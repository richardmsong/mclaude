#!/bin/bash
set -e

# --- Git workspace setup ---
REPO_DIR="/repo"          # Shared PVC mount (bare repo)
WORKTREE_ID="${WORKTREE_ID:-$(date +%s)}"
WORKTREE_DIR="/workspace/${WORKTREE_ID}"
GIT_URL="${GIT_URL:-}"
GIT_BRANCH="${GIT_BRANCH:-main}"

if [ -n "$GIT_URL" ]; then
    # Initialize bare repo if needed (first pod for this project)
    if [ ! -d "$REPO_DIR/HEAD" ]; then
        echo "Cloning bare repo: $GIT_URL"
        git clone --bare "$GIT_URL" "$REPO_DIR"
    else
        echo "Fetching latest..."
        git -C "$REPO_DIR" fetch --all --prune 2>/dev/null || true
    fi

    # Create worktree
    echo "Creating worktree at $WORKTREE_DIR (branch: $GIT_BRANCH)"
    mkdir -p /workspace
    git -C "$REPO_DIR" worktree add "$WORKTREE_DIR" "$GIT_BRANCH" 2>/dev/null || \
        git -C "$REPO_DIR" worktree add "$WORKTREE_DIR" "origin/$GIT_BRANCH" 2>/dev/null || \
        git -C "$REPO_DIR" worktree add --detach "$WORKTREE_DIR"

    WORK_DIR="$WORKTREE_DIR"
else
    WORK_DIR="/workspace"
    mkdir -p "$WORK_DIR"
fi

# --- Claude setup ---
mkdir -p "$HOME/.claude"
cat > "$HOME/.claude.json" << 'EOF'
{"hasCompletedOnboarding": true, "bypassPermissions": true}
EOF

mkdir -p "$WORK_DIR/.claude"
cat > "$WORK_DIR/.claude/settings.local.json" << 'EOF'
{"isTrusted": true}
EOF

# Start tmux with claude in the worktree directory
tmux new-session -d -s claude -x 200 -y 50 -c "$WORK_DIR" \
    "claude --dangerously-skip-permissions; echo 'CLAUDE_EXITED'; sleep 3600"

echo "READY (workdir: $WORK_DIR)"

# Auto-accept startup prompts
sleep 3
tmux send-keys -t claude Enter
sleep 2
tmux send-keys -t claude Down
sleep 0.5
tmux send-keys -t claude Enter

# Keep alive
while tmux has-session -t claude 2>/dev/null; do
    sleep 5
done
