#!/usr/bin/env bash
# register-platform-host.sh — Phase 2 CI entry point for registering a platform host.
#
# Authenticates as the bootstrap admin via POST /auth/login, then registers
# the first platform host using the BYOH device-code flow. This is the same
# flow a human operator runs manually.
#
# Flow:
#   1. POST /auth/login                          → adminJWT + userSlug
#   2. POST /api/users/{userSlug}/hosts/code     → device code
#   3. POST /api/hosts/register (no auth)        → slug, jwt, hubUrl
#   4. Print: export HOST_SLUG=... HOST_JWT=... HOST_HUB_URL=... HOST_NKEY_SEED=...
#
# Required env vars:
#   CP_URL                    Control-plane base URL (e.g. https://dev.mclaude.example.com)
#   BOOTSTRAP_ADMIN_EMAIL     Bootstrap admin email
#   BOOTSTRAP_ADMIN_PASSWORD  Bootstrap admin password
#   HOST_NKEY_PUBLIC          Pre-generated NATS NKey public key for the host
#   HOST_NKEY_SEED            Corresponding NKey seed (passed through to output)
#
# Optional env vars:
#   HOST_NAME         Display name for the host (default: k3d-dev)
#
# Generating HOST_NKEY_PUBLIC / HOST_NKEY_SEED:
#   Use nsc or nk to generate a NATS User NKey pair before calling this script:
#     nk -gen user
#   Or use the nsc tool:
#     nsc generate nkey --account
#
# Usage (CI):
#   eval "$(CP_URL=... BOOTSTRAP_ADMIN_EMAIL=... BOOTSTRAP_ADMIN_PASSWORD=... \
#             HOST_NKEY_PUBLIC=... HOST_NKEY_SEED=... \
#             scripts/register-platform-host.sh)"

set -euo pipefail

# ---------------------------------------------------------------------------
# Validate required env vars
# ---------------------------------------------------------------------------
: "${CP_URL:?Required env var CP_URL is not set}"
: "${BOOTSTRAP_ADMIN_EMAIL:?Required env var BOOTSTRAP_ADMIN_EMAIL is not set}"
: "${BOOTSTRAP_ADMIN_PASSWORD:?Required env var BOOTSTRAP_ADMIN_PASSWORD is not set}"
: "${HOST_NKEY_PUBLIC:?Required env var HOST_NKEY_PUBLIC is not set (generate with: nk -gen user)}"

HOST_NAME="${HOST_NAME:-k3d-dev}"
HOST_NKEY_SEED="${HOST_NKEY_SEED:-}"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

# check_response <step_name> <http_code> <body>
# Exits with a descriptive error if http_code is not 2xx.
check_response() {
  local step="$1"
  local code="$2"
  local body="$3"
  if [[ "$code" -lt 200 || "$code" -ge 300 ]]; then
    echo "ERROR: $step returned HTTP $code" >&2
    echo "Response body: $body" >&2
    exit 1
  fi
}

# curl_json <method> <url> [extra curl args...]
# Prints: <http_status_code>\n<response_body>
# Uses a temp file so the body is captured even when the status code is non-2xx.
curl_json() {
  local method="$1"
  local url="$2"
  shift 2
  local tmp
  tmp="$(mktemp)"
  local code
  code="$(curl -s -o "$tmp" -w '%{http_code}' -X "$method" "$url" \
    -H 'Content-Type: application/json' \
    "$@")"
  local body
  body="$(cat "$tmp")"
  rm -f "$tmp"
  printf '%s\n%s' "$code" "$body"
}

# split_response <output_of_curl_json>
# Sets RESP_CODE and RESP_BODY.
split_response() {
  RESP_CODE="$(echo "$1" | head -n1)"
  RESP_BODY="$(echo "$1" | tail -n +2)"
}

# ---------------------------------------------------------------------------
# Step 1: Admin login
# ---------------------------------------------------------------------------
echo "# Step 1: Authenticating as bootstrap admin..." >&2

raw="$(curl_json POST "${CP_URL}/auth/login" \
  -d "{\"email\":\"${BOOTSTRAP_ADMIN_EMAIL}\",\"password\":\"${BOOTSTRAP_ADMIN_PASSWORD}\"}")"
split_response "$raw"
check_response "POST /auth/login" "$RESP_CODE" "$RESP_BODY"

ADMIN_JWT="$(echo "$RESP_BODY" | jq -r '.jwt // empty')"
USER_SLUG="$(echo "$RESP_BODY" | jq -r '.userSlug // empty')"
if [[ -z "$ADMIN_JWT" ]]; then
  echo "ERROR: POST /auth/login succeeded but response contains no jwt field" >&2
  echo "Response body: $RESP_BODY" >&2
  exit 1
fi
if [[ -z "$USER_SLUG" ]]; then
  echo "ERROR: POST /auth/login succeeded but response contains no userSlug field" >&2
  echo "Response body: $RESP_BODY" >&2
  exit 1
fi
echo "# Logged in as user slug: ${USER_SLUG}" >&2

# ---------------------------------------------------------------------------
# Step 2: Get device code
# ---------------------------------------------------------------------------
echo "# Step 2: Requesting device code for host registration..." >&2

raw="$(curl_json POST "${CP_URL}/api/users/${USER_SLUG}/hosts/code" \
  -H "Authorization: Bearer ${ADMIN_JWT}" \
  -d "{\"publicKey\":\"${HOST_NKEY_PUBLIC}\"}")"
split_response "$raw"
check_response "POST /api/users/${USER_SLUG}/hosts/code" "$RESP_CODE" "$RESP_BODY"

DEVICE_CODE="$(echo "$RESP_BODY" | jq -r '.code // empty')"
if [[ -z "$DEVICE_CODE" ]]; then
  echo "ERROR: POST /api/users/${USER_SLUG}/hosts/code succeeded but response contains no code field" >&2
  echo "Response body: $RESP_BODY" >&2
  exit 1
fi
echo "# Device code obtained" >&2

# ---------------------------------------------------------------------------
# Step 3: Redeem code — register the host
# ---------------------------------------------------------------------------
echo "# Step 3: Registering host '${HOST_NAME}'..." >&2

raw="$(curl_json POST "${CP_URL}/api/hosts/register" \
  -d "{\"code\":\"${DEVICE_CODE}\",\"name\":\"${HOST_NAME}\"}")"
split_response "$raw"
check_response "POST /api/hosts/register" "$RESP_CODE" "$RESP_BODY"

HOST_SLUG="$(echo "$RESP_BODY" | jq -r '.slug // empty')"
HOST_JWT="$(echo "$RESP_BODY" | jq -r '.jwt // empty')"
HOST_HUB_URL="$(echo "$RESP_BODY" | jq -r '.hubUrl // empty')"

if [[ -z "$HOST_SLUG" ]]; then
  echo "ERROR: POST /api/hosts/register succeeded but response contains no slug field" >&2
  echo "Response body: $RESP_BODY" >&2
  exit 1
fi
if [[ -z "$HOST_JWT" ]]; then
  echo "ERROR: POST /api/hosts/register succeeded but response contains no jwt field" >&2
  echo "Response body: $RESP_BODY" >&2
  exit 1
fi

echo "# Host registered: ${HOST_SLUG}" >&2

# ---------------------------------------------------------------------------
# Step 4: Output — eval-able export statements for CI
# ---------------------------------------------------------------------------
echo "export HOST_SLUG=${HOST_SLUG}"
echo "export HOST_JWT=${HOST_JWT}"
echo "export HOST_HUB_URL=${HOST_HUB_URL}"
if [[ -n "$HOST_NKEY_SEED" ]]; then
  echo "export HOST_NKEY_SEED=${HOST_NKEY_SEED}"
fi
