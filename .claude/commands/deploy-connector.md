# Deploy MClaude Connector

Build and restart the mclaude-connector as a launchd service.

Credentials come from your secrets vault and are baked into the plist at `~/Library/LaunchAgents/com.mclaude.connector.plist`.

## Steps

1. **Build** the connector:
   ```
   cd mclaude-connector && go build -o mclaude-connector .
   ```

2. **Restart the service** (rebuild only — no plist changes):
   ```
   launchctl kickstart -k gui/$(id -u)/com.mclaude.connector
   ```
   If you changed the plist (env vars etc.), do a full reload instead:
   ```
   launchctl bootout gui/$(id -u)/com.mclaude.connector
   launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.mclaude.connector.plist
   ```

3. **Verify** (check logs and tunnel status):
   ```
   tail -10 /tmp/mclaude-connector.log
   curl -s $RELAY_URL/health
   ```

## Service Management

| Action | Command |
|--------|---------|
| Status | `launchctl print gui/$(id -u)/com.mclaude.connector` |
| Restart | `launchctl kickstart -k gui/$(id -u)/com.mclaude.connector` |
| Stop | `launchctl bootout gui/$(id -u) ~/Library/LaunchAgents/com.mclaude.connector.plist` |
| Start | `launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.mclaude.connector.plist` |
| Logs | `tail -f /tmp/mclaude-connector.log` |

## Plist Location

`~/Library/LaunchAgents/com.mclaude.connector.plist`

To update env vars (e.g. after rotating `TUNNEL_TOKEN`), edit the plist and restart the service.

## Environment Variables

| Variable | Description |
|----------|-------------|
| `RELAY_URL` | URL of the mclaude relay |
| `TUNNEL_TOKEN` | Auth token for the relay tunnel |
| `MCLAUDE_URL` | Local server URL (default: `http://127.0.0.1:8377`) |
| `STATIC_DIR` | Path to `mclaude-relay/static` for dev mode |

## Notes

- Service auto-restarts on crash (`KeepAlive: true`)
- Service auto-starts on login (`RunAtLoad: true`)
- Connector auto-detects hostname and sends it to relay
- Logs at `/tmp/mclaude-connector.log`
