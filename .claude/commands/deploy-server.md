# Deploy MClaude Server

Build and restart mclaude-server as a launchd service.

## Steps

1. **Build** the server (release mode):
   ```
   cd mclaude-server && swift build -c release
   ```

2. **Restart the service** (rebuild only — no plist changes):
   ```
   launchctl kickstart -k gui/$(id -u)/com.mclaude.server
   ```
   If you changed the plist (env vars etc.), do a full reload instead:
   ```
   launchctl bootout gui/$(id -u)/com.mclaude.server
   launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.mclaude.server.plist
   ```

3. **Verify**:
   ```
   curl -s http://localhost:8377/health
   tail -10 /tmp/mclaude-server.log
   ```

## Service Management

| Action | Command |
|--------|---------|
| Status | `launchctl print gui/$(id -u)/com.mclaude.server` |
| Restart | `launchctl kickstart -k gui/$(id -u)/com.mclaude.server` |
| Stop | `launchctl bootout gui/$(id -u) ~/Library/LaunchAgents/com.mclaude.server.plist` |
| Start | `launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.mclaude.server.plist` |
| Logs | `tail -f /tmp/mclaude-server.log` |

## Plist Location

`~/Library/LaunchAgents/com.mclaude.server.plist`

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `MCLAUDE_HOST` | `127.0.0.1` | Listen address |
| `MCLAUDE_PORT` | `8377` | Listen port |
| `MCLAUDE_TMUX_TARGET` | `mclaude` | tmux session to monitor |
| `MCLAUDE_POLL_INTERVAL` | `1` | Poll interval in seconds |

## Binary Location

`.build/arm64-apple-macosx/release/mclaude-server`

## Notes

- Service auto-restarts on crash (`KeepAlive: true`)
- Service auto-starts on login (`RunAtLoad: true`)
- Monitors the `mclaude` tmux session for active Claude sessions
- Logs at `/tmp/mclaude-server.log`
