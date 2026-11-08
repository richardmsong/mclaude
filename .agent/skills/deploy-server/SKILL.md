---
name: deploy-server
description: Build, test, and restart mclaude-server (headless Claude runner, launchd service com.mclaude.server). Use when the user wants to deploy, rebuild, or restart the server, or to run a throwaway test server.
---

# Deploy MClaude Server

`mclaude-server` is a Swift/Hummingbird HTTPS server running as the launch agent `com.mclaude.server`. It runs each
session headless (`claude --print --input-format stream-json ...`), with no tmux. The iOS app and the `mclaude` MCP
both talk to it at `https://home-server.taild44daa.ts.net:8377`.

**You are probably running inside it.** MClaude sessions are child processes of this server. Restarting it ends
your current turn and kills your background tasks and watchers. So do every other step first, send your final
message, and restart **last** with the delayed, detached restart in step 4.

## Steps

### 1. Build, sign and test

```bash
cd ~/work/mclaude-v1/mclaude-server && swift build -c release 2>&1 | grep -E "error:|Compiling|Build complete" | tail -3
# Sign right after every release build, before any restart (see Gotchas → Code signing). Sign a copy, then
# rename it into place: never codesign the binary the live server is running from.
B=.build/arm64-apple-macosx/release/mclaude-server
cp "$B" "$B.signing" && codesign --force --sign 6F9EAA6FE681C4080AB8F1A4A14021260D37CFBE \
  --identifier com.rsong.mclaude.server "$B.signing" && mv -f "$B.signing" "$B"
codesign -dv "$B" 2>&1 | grep -E "^Identifier|^Signature|^TeamIdentifier"
#   expect Identifier=com.rsong.mclaude.server and TeamIdentifier=Y63FV5M5D9 (never Signature=adhoc)
swift test 2>&1 | grep -E "error: -\[|Executed .* tests" | cut -c1-140 | tail -3
```

`JSONLParserTests.testSkipsLocalCommand` was already failing before this work. Any other failure blocks the deploy.
If `codesign -dv` still says `Signature=adhoc` or `TeamIdentifier=not set`, don't restart: the Accessibility grant
won't apply to that binary (see Gotchas).

### 2. (Optional) Verify against a throwaway test server

Never test against the live server. Run a copy on port 8399 with its own state folder and a cheap model:

```bash
N=<tag>; cp .build/arm64-apple-macosx/release/mclaude-server /tmp/mclaude-server-$N
mkdir -p /tmp/mclaude-uitest && (MCLAUDE_PORT=8399 MCLAUDE_STATE_DIR=/tmp/mclaude-uitest MCLAUDE_CLAUDE_ARGS="--model haiku" \
  nohup /tmp/mclaude-server-$N >> /tmp/mclaude-uitest/server.log 2>&1 &)
curl -sk https://home-server.taild44daa.ts.net:8399/health
```

- **Copy the binary to `/tmp/mclaude-server-<tag>` first.** Then `pkill -f "^/tmp/mclaude-server-<tag>"` matches only
  the test copy. Never `pkill -f` the `.build/...` path, because that's the live launch agent's path too.
- To stop a test server you started from `.build`, stop it by port, after checking it isn't the live pid:
  ```bash
  TP=$(lsof -nP -iTCP:8399 -sTCP:LISTEN -t | head -1); LP=$(launchctl list | awk '/com.mclaude.server/{print $1}')
  [ -n "$TP" ] && [ "$TP" != "$LP" ] && kill -TERM $TP
  ```
- A separate `MCLAUDE_STATE_DIR` isolates the managed sessions and the APNs config and device tokens (`<stateDir>/apns`),
  so the test server can't push to the user's phone. It does **not** isolate the auth files. `SessionStore` always uses
  `~/mclaude-users.json`, `~/mclaude-session-owners.json`, `~/mclaude-config.json` and `~/mclaude-session-tokens.json`,
  so the test server shares them with the live server. Any request with an unknown token auto-registers a user there,
  and creating a session writes an owner entry. To keep the test server from writing:
  - Point the simulator at the test server with the live owner's existing token (look it up in
    `~/mclaude-session-tokens.json`; never print it). **Set it before launching the app**, or an old process will
    reconnect with a stale token and register a junk user.
  - Reuse a session that already exists in the test state dir instead of creating new ones.
  - Take `md5` of the four files before you start the test server and compare them afterwards. If anything changed,
    stop the test server first, then remove the stray users or owners (a running server rewrites the files from memory).
  - **Session ids collide.** Test and live servers number sessions independently, and owners are keyed by bare id (`s11`).
    Resuming or creating a test session overwrites that id's owner entry. Ending one (`DELETE /sessions/:id`) **removes**
    it, even when the live server has a session with the same id. The live server only notices when it next reloads the
    file (on restart), and then that live session has no owner. After ending a test session, check that every live
    session id is still in `~/mclaude-session-owners.json`, and put back any that's missing before restarting the live
    server. (2026-11-07: test s11 deleted live s11's entry.)
- To point the simulator at it: `xcrun simctl spawn <udid> defaults write com.rsong.mclaude.app serverPort -int 8399`.
  Reset it to 8377 afterwards.

- **Listener restarts in-process.** Hummingbird stops accepting for good after any accept error. On macOS a client that resets mid-accept triggers one (`NIOFcntlFailedError`); that's what restarted the server at 14:10 on 2026-11-04 and interrupted the running turn. `main.swift`'s `serveForever` now rebinds the listener within about a second without touching sessions, and exits for launchd only after more than 5 failures in a minute. Look for `[server] listener stopped; restarting it` in the log. To test it on a :8399 server, set `MCLAUDE_TEST_CLOSE_LISTENER_AFTER=<secs>`, which closes the listening socket after that many seconds on each start.

### 3. Check compatibility with the installed app

The phone runs whichever TestFlight build the user last installed, not necessarily the one you just uploaded. Don't
remove or rename response fields that the installed build decodes until the user is on a build that no longer needs
them. Swift `Codable` fails the whole decode on one missing non-optional field.

### 4. Restart (last action of the turn)

Before restarting, commit your own server changes and tag the deploy: `git tag server-$(date +%Y-%m-%d)` (add `-2`, `-3` and so on for further deploys the same day). Local only, no push.

On SIGTERM the server drains, waiting up to 300 s for running turns (the plist sets `ExitTimeOut=330`). A plain
`kickstart -k` from inside a session would therefore kill your own turn mid-message. Schedule it detached, with a
delay, and end your turn:

```bash
cat > /tmp/mclaude-restart.sh <<'EOF'
#!/bin/zsh
LOG=/tmp/mclaude-restart.log
B=$HOME/work/mclaude-v1/mclaude-server/.build/arm64-apple-macosx/release/mclaude-server
sleep 20
# Any swift build since step 1 (even a no-op) relinks and drops the signature: re-sign before the restart
if ! codesign -dv "$B" 2>&1 | grep -q "TeamIdentifier=Y63FV5M5D9"; then
  cp "$B" "$B.signing" && codesign --force --sign 6F9EAA6FE681C4080AB8F1A4A14021260D37CFBE \
    --identifier com.rsong.mclaude.server "$B.signing" && mv -f "$B.signing" "$B"
fi
echo "$(date +%T) before: $(launchctl list | grep com.mclaude.server) sig: $(codesign -dv "$B" 2>&1 | grep -E '^TeamIdentifier')" >> $LOG
launchctl kickstart -k gui/$(id -u)/com.mclaude.server >> $LOG 2>&1
for i in {1..80}; do curl -sk -m 3 https://home-server.taild44daa.ts.net:8377/health >/dev/null && break; sleep 5; done
echo "$(date +%T) after: $(launchctl list | grep com.mclaude.server) health=$(curl -sk -m 3 https://home-server.taild44daa.ts.net:8377/health)" >> $LOG
EOF
chmod +x /tmp/mclaude-restart.sh; nohup /tmp/mclaude-restart.sh >/dev/null 2>&1 &!
```

Tell the user that the server restarts about 20 s after your message and that the session will pause briefly. When
you next run, check `/tmp/mclaude-restart.log` and `tail -20 /tmp/mclaude-server.log`.

If you are **not** inside an MClaude session (e.g. a plain terminal), restart directly:
`launchctl kickstart -k gui/$(id -u)/com.mclaude.server`.

If you changed the plist (env vars etc.), reload it instead of kickstarting. This also kills running sessions, so use
the same delayed pattern:
```bash
launchctl bootout gui/$(id -u)/com.mclaude.server; launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.mclaude.server.plist
```

### 5. Verify

```bash
curl -sk https://home-server.taild44daa.ts.net:8377/health      # {"status":"ok"}
tail -20 /tmp/mclaude-server.log
```

The server only speaks HTTPS: `http://localhost:8377` returns an empty reply (curl exit 52). The cert is issued for the
Tailscale hostname, so use that hostname, not `127.0.0.1`.

## Service management

| Action | Command |
|--------|---------|
| Status | `launchctl print gui/$(id -u)/com.mclaude.server` |
| Restart | `launchctl kickstart -k gui/$(id -u)/com.mclaude.server` (delayed pattern if inside a session) |
| Stop | `launchctl bootout gui/$(id -u) ~/Library/LaunchAgents/com.mclaude.server.plist` |
| Start | `launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.mclaude.server.plist` |
| Logs | `tail -f /tmp/mclaude-server.log` |

- Plist: `~/Library/LaunchAgents/com.mclaude.server.plist` (`KeepAlive`, `RunAtLoad`, `ExitTimeOut=330`, explicit `PATH`
  so that node-based MCP servers work inside sessions)
- Binary: `.build/arm64-apple-macosx/release/mclaude-server`, launched via `scripts/launch-signed.sh` (see Gotchas)

## Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `MCLAUDE_HOST` | `0.0.0.0` | Listen address |
| `MCLAUDE_PORT` | `8377` | Listen port (test servers use 8399) |
| `MCLAUDE_STATE_DIR` | `~/.mclaude` | `managed-sessions.json`, `apns/` (push key, config, devices) |
| `MCLAUDE_CLAUDE_ARGS` | — | Extra flags for every `claude` process, e.g. `--model haiku` in tests |
| `MCLAUDE_SIGNAL_RECIPIENT` | — | Enables Signal notifications |
| `MCLAUDE_POLL_INTERVAL` | `1` | Poll interval in seconds |

`MCLAUDE_TMUX_TARGET` is still in the plist, but the server doesn't use it any more.

## State and certs

- `~/.mclaude/managed-sessions.json` is the session list. Conversations are Claude's own JSONL under `~/.claude/projects/`.
- TLS cert: `~/mclaude-certs/` (Tailscale/Let's Encrypt, 90-day life). It's renewed weekly by the launch agent
  `com.mclaude.cert-renew` (`~/.local/bin/mclaude-cert-renew`), which restarts the server only when the cert changed.
  If the app shows "TLS error", check this first.

## Gotchas

- **Code signing keeps the TCC grants (added 2026-11-04).**
  - Claude sessions are child processes of this server, so macOS charges their TCC permissions (Accessibility, Screen
    Recording) to the server binary.
  - `swift build` leaves it **ad-hoc, linker-signed**. That gives it a new identity on every build, so each rebuild lost
    the Accessibility grant. Computer-use screenshots kept working, but mouse and keyboard input was silently dropped.
  - Step 1 therefore re-signs every release build with the stable identity **"Apple Development: Created via API
    (2PXR2WU2PK)"**: SHA1 `6F9EAA6FE681C4080AB8F1A4A14021260D37CFBE`, login keychain, team `Y63FV5M5D9`, valid until
    **2027-11-02**. The identifier stays fixed at `--identifier com.rsong.mclaude.server`.
  - TCC then keys on the designated requirement (`codesign -d -r-`: the identifier plus the certificate's CN), and that
    survives rebuilds.
  - **One-time grant:** System Settings → Privacy & Security → Accessibility (and Screen Recording if prompted) → add
    `~/work/mclaude-v1/mclaude-server/.build/arm64-apple-macosx/release/mclaude-server`, then restart the server.
  - Grant it again only if the certificate is replaced (it expires 2027-11-02) or the identifier changes.
- **Any `swift build -c release` drops the signature, even a no-op.** SwiftPM sees the signed output as modified
  and relinks it (`[1/2] Linking mclaude-server`), leaving it ad-hoc again. So sign after the *last* build, and step
  4's restart script re-checks and re-signs right before the kickstart.
- **The launcher is the backstop for every launch (added 2026-11-05).** The plist's `ProgramArguments` runs
  `mclaude-server/scripts/launch-signed.sh`, not the binary. On every start the launcher checks
  `Identifier=com.rsong.mclaude.server` + `TeamIdentifier=Y63FV5M5D9`, re-signs the binary if either is missing (copy,
  sign, then mv), and then `exec`s it. That covers crash relaunches (`KeepAlive`), the cert-renew kickstart and manual
  kickstarts too.
  - `exec` keeps the pid, so `sudo -n launchctl procinfo <pid> | grep 'responsible path'` still shows the
    mclaude-server binary. This was verified on 2026-11-05, and it means the existing grant still applies.
  - Do **not** change the launcher to spawn the server as a child process. zsh would then become the responsible
    process, and the grants would be lost.
  - Its log lines go to `/tmp/mclaude-server.log`, prefixed `[launch-signed]`. If you see `WARNING: re-sign failed`,
    the keychain or the cert is the problem.
  - Still sign in step 1: that's what lets you verify before a restart.
- **Sign a copy and `mv` it into place.** `codesign --force` rewrites the file in place. Changing the pages of a binary
  the live server is executing can get it SIGKILLed for an invalid code signature. A rename leaves the running
  process on the old file.
- **No hardened runtime** (`--options runtime`). TCC doesn't need it, and it's untested with what the server spawns.
  The plain certificate signature was checked on 2026-11-04 with a signed copy on :8398. `POST /videos` ran ffmpeg, a
  session turn started claude and ran Bash, and claude loaded its node MCP servers (computer-use, gmail,
  google-calendar, mclaude, playwright, chrome-devtools).
- Signing ran non-interactively from a session (no keychain prompt). If it ever hangs, the key's access control list
  needs `codesign` allowed. Fix that once in Keychain Access; don't work around it with an ad-hoc signature.

