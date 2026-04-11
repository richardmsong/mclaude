# Deploy MClaude Connector

Build and restart the mclaude-connector on the local laptop inside its tmux window.

Credentials are stored in Vault at `appcodes/0LL0/DEV/MCLAUDE` (case-sensitive).

Fetch them with:
```
vault kv get appcodes/0LL0/DEV/MCLAUDE
```

This provides `RELAY_URL` and `TUNNEL_TOKEN`. Other env vars:
- `MCLAUDE_URL`: Local mclaude-server (default: `http://localhost:8377`)
- `STATIC_DIR`: Path to local static files for hot-reload (optional)

## Steps

1. **Build** the connector:
   ```
   cd mclaude-connector && go build -o mclaude-connector .
   ```

2. **Kill** any running connector:
   ```
   pkill -f mclaude-connector || true
   ```

3. **Restart** in the tmux "connector" window (mclaude session):
   ```
   tmux send-keys -t mclaude:connector C-c
   sleep 1
   tmux send-keys -t mclaude:connector "RELAY_URL=$RELAY_URL TUNNEL_TOKEN=$TUNNEL_TOKEN MCLAUDE_URL=$MCLAUDE_URL STATIC_DIR=$STATIC_DIR $(pwd)/mclaude-connector/mclaude-connector" Enter
   ```

   If the tmux window doesn't exist yet, create it:
   ```
   tmux new-window -t mclaude -n connector "RELAY_URL=$RELAY_URL TUNNEL_TOKEN=$TUNNEL_TOKEN MCLAUDE_URL=$MCLAUDE_URL STATIC_DIR=$STATIC_DIR $(pwd)/mclaude-connector/mclaude-connector; exec zsh"
   ```

4. **Verify** (wait 2s for reconnect):
   ```
   curl -s $RELAY_URL/health
   ```

## Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `RELAY_URL` | Yes | Relay WebSocket URL |
| `TUNNEL_TOKEN` | Yes | Shared secret with relay |
| `MCLAUDE_URL` | No | Local mclaude-server (default: `http://localhost:8377`) |
| `STATIC_DIR` | No | Path to local static files for hot-reload dev mode |
| `CONNECTOR_NAME` | No | Override auto-detected hostname for multi-laptop display |
| `TLS_SKIP_VERIFY` | No | Set to `1` to skip TLS verification |

## Notes

- Connector runs in tmux window `mclaude:connector`
- It auto-detects hostname (short, truncated at first dot) and sends it to relay
- The relay shows the hostname in `/health` and `/laptops` responses
- The connector auto-reconnects if the tunnel drops
- STATIC_DIR enables hot-reload: edit index.html locally, browser auto-refreshes
