#!/bin/bash
# Auto-accept Claude Code's interactive prompts by monitoring and responding

# Start claude in background
claude --dangerously-skip-permissions &
CLAUDE_PID=$!

# Wait for claude to finish (all prompts should be handled by settings files)
# If it prompts anyway, the user will handle it via tmux send-keys from the server
wait $CLAUDE_PID
EXIT_CODE=$?

echo "EXIT CODE: $EXIT_CODE"
sleep 3600
