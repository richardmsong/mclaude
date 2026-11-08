#!/bin/sh
# Start (or update) the observability stack.
set -eu
cd "$(dirname "$0")"
./write-server-token.sh   # owner token for the Ops dispatcher (once; never printed)
docker compose up -d --build "$@"
