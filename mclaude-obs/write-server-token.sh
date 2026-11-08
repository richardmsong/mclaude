#!/bin/sh
# Writes the mclaude-server owner token to ~/.mclaude/obs/server-token (mode 600) for the obs-api
# Ops dispatcher. The owner token is the key in ~/mclaude-session-tokens.json whose value is
# ownerUserId in ~/mclaude-config.json. Runs once: an existing file is left alone unless --force.
# Never prints the token.
set -eu
DIR="$HOME/.mclaude/obs"
OUT="$DIR/server-token"
if [ -s "$OUT" ] && [ "${1:-}" != "--force" ]; then
  exit 0
fi
mkdir -p "$DIR"
chmod 700 "$DIR"
umask 077
if ! python3 - "$HOME/mclaude-config.json" "$HOME/mclaude-session-tokens.json" "$OUT" <<'PY'
import json, os, sys
cfg, toks, out = sys.argv[1:4]
try:
    owner = json.load(open(cfg)).get("ownerUserId")
    tokens = json.load(open(toks))
except (OSError, ValueError) as e:
    sys.exit(f"write-server-token: can't read the server's auth files: {e}")
match = [k for k, v in tokens.items() if v == owner] if owner else []
if not match:
    sys.exit("write-server-token: no session token belongs to ownerUserId")
tmp = out + ".tmp"
fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
with os.fdopen(fd, "w") as fh:
    fh.write(match[0] + "\n")
os.chmod(tmp, 0o600)
os.replace(tmp, out)
print(f"write-server-token: wrote {out}")
PY
then
  echo "write-server-token: skipped; the Ops dispatcher can't call mclaude-server until it's written" >&2
fi
