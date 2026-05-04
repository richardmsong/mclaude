#!/usr/bin/env bash
# register-platform-host.sh — Phase 2 CI entry point for registering a platform host.
#
# Implements both sides of the BYOH device-code exchange:
#   1. Admin login  → adminJWT
#   2. GET /auth/me  → userSlug
#   3. Generate NKey pair (or accept HOST_NKEY_PUBLIC from caller)
#   4. POST /api/users/{userSlug}/hosts/code  → code
#   5. POST /api/hosts/register              → slug, jwt, hubUrl
#   6. Print: export HOST_SLUG=... HOST_JWT=... HOST_HUB_URL=...
#
# Required env vars:
#   CP_URL                    Control-plane base URL (e.g. https://dev.mclaude.example.com)
#   BOOTSTRAP_ADMIN_EMAIL     Bootstrap admin email
#   BOOTSTRAP_ADMIN_PASSWORD  Bootstrap admin password
#
# Optional env vars:
#   HOST_NAME         Display name for the host (default: k3d-dev)
#   HOST_NKEY_PUBLIC  Pre-generated NATS NKey public key for the host.
#                     If unset, the script attempts to call `mclaude nkeys generate`
#                     (if the binary is on PATH) to generate an ephemeral key pair.
#                     The seed is printed as: export HOST_NKEY_SEED=...
#                     If neither is available, the script exits with an error.
#
# Usage (CI):
#   eval "$(scripts/register-platform-host.sh)"
#   helm upgrade --install ... \
#     --set host.slug="$HOST_SLUG" \
#     --set host.jwt="$HOST_JWT" \
#     --set hub.url="$HOST_HUB_URL"

set -euo pipefail

# ---------------------------------------------------------------------------
# Validate required env vars
# ---------------------------------------------------------------------------
: "${CP_URL:?Required env var CP_URL is not set}"
: "${BOOTSTRAP_ADMIN_EMAIL:?Required env var BOOTSTRAP_ADMIN_EMAIL is not set}"
: "${BOOTSTRAP_ADMIN_PASSWORD:?Required env var BOOTSTRAP_ADMIN_PASSWORD is not set}"

HOST_NAME="${HOST_NAME:-k3d-dev}"

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
if [[ -z "$ADMIN_JWT" ]]; then
  echo "ERROR: POST /auth/login succeeded but response contains no jwt field" >&2
  echo "Response body: $RESP_BODY" >&2
  exit 1
fi

# ---------------------------------------------------------------------------
# Step 2: Determine user slug
# ---------------------------------------------------------------------------
echo "# Step 2: Fetching user slug..." >&2

raw="$(curl_json GET "${CP_URL}/auth/me" \
  -H "Authorization: Bearer ${ADMIN_JWT}")"
split_response "$raw"
check_response "GET /auth/me" "$RESP_CODE" "$RESP_BODY"

USER_SLUG="$(echo "$RESP_BODY" | jq -r '.user.slug // .slug // empty')"
if [[ -z "$USER_SLUG" ]]; then
  echo "ERROR: GET /auth/me succeeded but response contains no user slug" >&2
  echo "Response body: $RESP_BODY" >&2
  exit 1
fi
echo "# User slug: ${USER_SLUG}" >&2

# ---------------------------------------------------------------------------
# Step 3: Obtain NKey public key
#
# NATS NKeys are Ed25519 keys with a type-prefixed base32 encoding.
# The caller SHOULD pre-generate the key pair and pass HOST_NKEY_PUBLIC
# (and optionally HOST_NKEY_SEED) as env vars.
#
# If HOST_NKEY_PUBLIC is not set, this script attempts to call
# `mclaude nkeys generate`, which prints:
#   HOST_NKEY_PUBLIC=<nkey-public>
#   HOST_NKEY_SEED=<nkey-seed>
# The seed is captured and re-exported so the caller can use it for the
# k8s controller Helm values.
#
# If neither is available, the script fails with instructions.
# ---------------------------------------------------------------------------
echo "# Step 3: Obtaining NKey public key..." >&2

HOST_NKEY_SEED="${HOST_NKEY_SEED:-}"

if [[ -z "${HOST_NKEY_PUBLIC:-}" ]]; then
  if command -v mclaude >/dev/null 2>&1 && mclaude nkeys generate >/dev/null 2>&1; then
    # mclaude nkeys generate prints: HOST_NKEY_PUBLIC=... and HOST_NKEY_SEED=...
    nkey_output="$(mclaude nkeys generate)"
    HOST_NKEY_PUBLIC="$(echo "$nkey_output" | grep '^HOST_NKEY_PUBLIC=' | cut -d= -f2-)"
    HOST_NKEY_SEED="$(echo "$nkey_output" | grep '^HOST_NKEY_SEED=' | cut -d= -f2-)"
    if [[ -z "$HOST_NKEY_PUBLIC" ]]; then
      echo "ERROR: mclaude nkeys generate did not output HOST_NKEY_PUBLIC" >&2
      echo "Output was: $nkey_output" >&2
      exit 1
    fi
    echo "# NKey pair generated via mclaude nkeys generate" >&2
  else
    echo "ERROR: HOST_NKEY_PUBLIC is not set and mclaude nkeys generate is unavailable." >&2
    echo "" >&2
    echo "To fix: generate a NATS NKey pair and export HOST_NKEY_PUBLIC (and HOST_NKEY_SEED)" >&2
    echo "before running this script. Example using the mclaude CLI:" >&2
    echo "" >&2
    echo "  eval \"\$(mclaude nkeys generate)\"" >&2
    echo "  export HOST_NKEY_PUBLIC HOST_NKEY_SEED" >&2
    echo "" >&2
    echo "Or with nsc (if installed):" >&2
    echo "  nkey_output=\"\$(nsc generate nkey --host-key)\"" >&2
    echo "  export HOST_NKEY_PUBLIC=\"\$(echo \"\$nkey_output\" | grep '^public:' | awk '{print \$2}')\"" >&2
    exit 1
  fi
else
  echo "# Using pre-supplied HOST_NKEY_PUBLIC" >&2
fi

# ---------------------------------------------------------------------------
# Step 4: Get device code
# ---------------------------------------------------------------------------
echo "# Step 4: Requesting device code for host registration..." >&2

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
# Step 5: Redeem code — register the host
# ---------------------------------------------------------------------------
echo "# Step 5: Registering host '${HOST_NAME}'..." >&2

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
# Step 6: Output — eval-able export statements for CI
# ---------------------------------------------------------------------------
echo "export HOST_SLUG=${HOST_SLUG}"
echo "export HOST_JWT=${HOST_JWT}"
echo "export HOST_HUB_URL=${HOST_HUB_URL}"
if [[ -n "$HOST_NKEY_SEED" ]]; then
  echo "export HOST_NKEY_SEED=${HOST_NKEY_SEED}"
fi
