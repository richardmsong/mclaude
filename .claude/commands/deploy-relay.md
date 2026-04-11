# Deploy MClaude Relay

Build and deploy the mclaude-relay binary to a remote VM.

Credentials are stored in environment variables or local files — never hardcoded here.
- `RELAY_HOST`: hostname or IP of the relay VM
- `RELAY_SSH_USER`: SSH username for the VM
- SSH auth: key-based or password file at `~/.unixpassword`
- Tokens: set in the deploy script on the VM

## Steps

1. **Cross-compile** the relay for linux/amd64:
   ```
   cd mclaude-relay && GOOS=linux GOARCH=amd64 go build -o relay-linux .
   ```

2. **Upload** the binary to the VM:
   ```
   scp mclaude-relay/relay-linux $RELAY_SSH_USER@$RELAY_HOST:/tmp/relay-linux-new
   ```

3. **Run the deploy script** remotely:
   ```
   ssh $RELAY_SSH_USER@$RELAY_HOST 'sudo bash /tmp/deploy-relay.sh 2>&1'
   ```

   If the deploy script doesn't exist or is stale, create and upload it. The script should:
   - Kill the running relay process
   - Copy relay-linux-new over relay-linux
   - Restart with env vars: `TUNNEL_TOKEN`, `WEB_TOKEN`, `PORT`, `TUNNEL_STATIC`
   - Log to `/tmp/relay.log`

4. **Verify** the relay is running:
   ```
   ssh $RELAY_SSH_USER@$RELAY_HOST 'curl -s http://localhost/health'
   ```

## VM Details

- **Relay binary location**: `/tmp/relay-linux`
- **Relay runs as**: root (required for port 80)
- **Deploy script**: `/tmp/deploy-relay.sh`
- **Logs**: `/tmp/relay.log`

## Notes

- The relay requires root to bind port 80. Cannot restart without sudo.
- `TUNNEL_STATIC=true` proxies static file requests through the tunnel to the connector for hot-reload dev mode.
- `TUNNEL_STATIC_HOST` pins static serving to a specific laptop hostname.
- When `TUNNEL_STATIC` is off, the relay serves embedded static files from the binary.
