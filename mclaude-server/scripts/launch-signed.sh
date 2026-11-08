#!/bin/zsh
# launchd entry point for com.mclaude.server.
# Guarantees the binary carries the stable Apple Development signature before it runs, so the
# TCC grants (Accessibility, Screen Recording) keyed on its designated requirement survive any
# `swift build`, crash relaunch, cert-renew kickstart or manual kickstart. `exec` keeps the pid,
# so macOS still treats mclaude-server (not zsh) as the responsible process for sessions.
B="${MCLAUDE_SERVER_BIN:-$HOME/work/mclaude-v1/mclaude-server/.build/arm64-apple-macosx/release/mclaude-server}"
IDENTITY=6F9EAA6FE681C4080AB8F1A4A14021260D37CFBE   # Apple Development: Created via API (2PXR2WU2PK), exp 2027-11-02
IDENT=com.rsong.mclaude.server
log() { print -r -- "$(date '+%F %T') [launch-signed] $*" >&2; }

sig=$(codesign -dv "$B" 2>&1)
if [[ $sig != *"Identifier=$IDENT"* || $sig != *"TeamIdentifier=Y63FV5M5D9"* ]]; then
  log "binary not stably signed; re-signing $B"
  # Sign a copy and rename into place: never rewrite pages of a file something may be executing.
  if cp "$B" "$B.signing" && codesign --force --sign "$IDENTITY" --identifier "$IDENT" "$B.signing" 2>&2 \
     && mv -f "$B.signing" "$B"; then
    log "re-signed OK"
  else
    rm -f "$B.signing"
    log "WARNING: re-sign failed; launching unsigned (TCC grants will NOT apply)"
  fi
fi
exec "$B" "$@"
