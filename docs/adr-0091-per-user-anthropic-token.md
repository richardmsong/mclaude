# ADR: Per-User Anthropic OAuth Token — Host-Key Encryption

**Status**: draft
**Status history**:
- 2026-05-04: draft

## Overview

Each mclaude user stores their own Anthropic OAuth token, encrypted client-side using a symmetric key that only the host cluster holds. The control plane stores ciphertext only — it cannot decrypt the token at any point. At session start the session-agent pod decrypts the token itself and exports `CLAUDE_CODE_OAUTH_TOKEN`. Controller-k8s is removed from the token delivery path entirely. The global `DEV_OAUTH_TOKEN` mechanism is removed.

## Motivation

The current `DEV_OAUTH_TOKEN` is a single shared Anthropic credential that flows through the controller-k8s deployment env var, making it visible in `kubectl get deploy -o yaml`. There is no per-user concept: all users share one token and cannot bring their own Claude account.

The new design gives the host cluster a cryptographic guarantee of privacy: the CP platform operator cannot read any user's Anthropic token even with full DB access.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Encryption model | Client-side hybrid encryption (symmetric key K wrapped with host X25519 public key) | CP stores ciphertext only; cannot decrypt without K |
| Host encryption keypair | Separate X25519 keypair generated at host registration | Clean separation: NKey for NATS auth, X25519 for encryption; Ed25519 NKeys are signing-only |
| Symmetric key K | Fresh ephemeral K generated per token-set operation (not derived from seed) | Simpler; K_wrapped in DB is sufficient for recovery |
| K storage on host | k8s Secret `mclaude-host-key` in host namespace, key `sym-key` (cache); authoritative copy is K_wrapped in DB | If k8s Secret lost, host re-decrypts K from K_wrapped using X25519 private key |
| Decryption point | Session-agent process (mounts ciphertext Secret + host key Secret) | Controller-k8s never sees plaintext; decryption stays inside the session boundary |
| CP role | Stores (token_ct, K_wrapped) as opaque blobs in `user_host_tokens` table | Blind to plaintext; only routes ciphertext to the target host |
| DEV_OAUTH_TOKEN | Removed from controller-k8s, CP chart, CI workflow | Per-user host-encrypted token replaces the global dev token |
| Token entry — primary | Claude.ai OAuth PKCE flow from SPA | Browser generates K, fetches host X25519 public key, encrypts token client-side, wraps K — CP never sees plaintext |
| Token entry — CLI | `mclaude auth anthropic` CLI command (also OAuth, opens browser) | Same crypto as SPA |
| Token origin | OAuth access token from claude.ai — not a static API key | Token is ephemeral; expiry or revocation requires re-running the OAuth flow |
| Token re-auth | When token expires/revoked, user re-runs OAuth flow in SPA or CLI; new token overwrites old row | Expected explicit user action; no silent refresh in v1 |
| Missing token at session start | Session creation fails with clear error | Fail fast; user sees "Connect Claude account in Settings" |

## Key Establishment Flow (one-time per user per host)

1. During host registration, host generates an X25519 keypair and registers the public key with CP.

**Via SPA (primary):**
2. User navigates to Settings → Anthropic → clicks "Connect Claude account".
3. SPA redirects to claude.ai OAuth authorization endpoint (PKCE flow; code_challenge sent).
4. User approves on claude.ai → redirected back to SPA with `code`.
5. SPA exchanges `code` for access token directly with Anthropic (client-side; PKCE verifier).
6. SPA generates fresh ephemeral symmetric key K (32 random bytes).
7. SPA fetches host X25519 public key from CP (public info endpoint).
8. SPA encrypts: `token_ct = AES-256-GCM(K, access_token)`.
9. SPA wraps: `K_wrapped = X25519-ECDH-encrypt(host_x25519_public, K)`.
10. SPA POSTs `(token_ct, K_wrapped)` to CP: `POST /api/users/{uslug}/hosts/{hslug}/anthropic-token`.
11. CP stores blobs; cannot decrypt. Notifies host via NATS: `mclaude.hosts.{hslug}.token.updated`.
12. Host controller decrypts K from `K_wrapped` using X25519 private key, stores K in `mclaude-host-key.sym-key`.

**Via CLI (alternate):**
2. `mclaude auth anthropic [--host <hslug>]` opens browser to claude.ai OAuth; handles PKCE callback locally.
3. CLI generates K, encrypts token, wraps K with host X25519 public key; POSTs to CP.

**Re-auth (token expired or revoked):**
User re-runs OAuth flow in SPA or CLI. New `(token_ct, K_wrapped)` overwrites old row. Host re-caches new K.

## Session Start Flow (every session)

1. CP creates session pod for user U on host H.
2. CP fetches `(token_ct, K_wrapped)` for (U, H) from DB and writes both into a k8s Secret `user-anthropic-{uslug}` in the user's namespace.
3. Session-agent pod mounts:
   - `/secrets/anthropic/token_ct` — from `user-anthropic-{uslug}`
   - `/secrets/host-key/sym-key` — from `mclaude-host-key` (host-level Secret, read-only)
4. `entrypoint.sh` decrypts:
   ```sh
   K=$(cat /secrets/host-key/sym-key)
   token_ct=$(cat /secrets/anthropic/token_ct)
   export CLAUDE_CODE_OAUTH_TOKEN=$(aes_gcm_decrypt "$K" "$token_ct")
   ```
5. Claude Code uses `CLAUDE_CODE_OAUTH_TOKEN` for all Anthropic API calls.

## Component Changes

### mclaude-controller-k8s

- Host generates X25519 keypair at startup (or on first run); stores in k8s Secret `mclaude-host-key` (keys: `x25519-private`, `sym-key`).
- Registers X25519 public key with CP on host heartbeat / registration.
- Subscribes to `mclaude.hosts.{hslug}.token.updated` — on receipt: decrypts K from `K_wrapped` using `x25519-private`, writes K to `mclaude-host-key.sym-key`.
- Removes all `devOAuthToken` / `oauth-token` writes from user-secrets reconciler paths.

### mclaude-control-plane

- New DB table `user_host_tokens (id, user_id, host_id, token_ct, k_wrapped, updated_at)`.
- New endpoint: `POST /api/users/{uslug}/hosts/{hslug}/anthropic-token` — body `{ "token_ct": "...", "k_wrapped": "..." }`. Stores blobs, publishes NATS notification. Auth: self only.
- New endpoint: `GET /api/users/{uslug}/hosts/{hslug}/anthropic-token/status` — returns `{ "set": true/false, "updated_at": "..." }`. No token data.
- New endpoint: `DELETE /api/users/{uslug}/hosts/{hslug}/anthropic-token` — removes row, publishes deletion notification.
- Host registration: accepts and stores `x25519_public_key` field on host record.
- Session creation: writes `(token_ct, k_wrapped)` into k8s Secret `user-anthropic-{uslug}` in user namespace. Fails session start if no token row exists.
- Removes `devOAuthToken` from all code paths.

### mclaude-cli

- New command `mclaude auth anthropic [--host <hslug>]`:
  1. Prompts user to paste Anthropic token (or performs OAuth redirect).
  2. Reads user NKey seed from local config.
  3. Derives K = HKDF(seed, "anthropic-token-key-v1", host_id).
  4. Encrypts: `token_ct = AES-256-GCM(K, token)`.
  5. Fetches host X25519 public key from CP.
  6. Wraps: `K_wrapped = X25519-ECDH-encrypt(host_x25519_pub, K)`.
  7. POSTs `(token_ct, K_wrapped)` to CP.
- New command `mclaude auth anthropic --status [--host <hslug>]` — shows connected/not set.
- New command `mclaude auth anthropic --revoke [--host <hslug>]` — calls DELETE endpoint.

### mclaude-session-agent

- `entrypoint.sh`: replaces current `oauth-token` file read with in-process AES-256-GCM decryption:
  ```sh
  K=$(cat /secrets/host-key/sym-key | base64 -d | xxd -p)
  token_ct=$(cat /secrets/anthropic/token_ct)
  export CLAUDE_CODE_OAUTH_TOKEN=$(echo "$token_ct" | base64 -d | openssl enc -d -aes-256-gcm -K "$K" -nosalt)
  ```
  (exact openssl flags TBD based on GCM nonce encoding chosen in CLI)
- Mounts added in session pod spec (CP-side): `user-anthropic-{uslug}` Secret + `mclaude-host-key` Secret.

### charts/mclaude-cp

- Remove `controlPlane.devOAuthToken` value and env var from CP deployment template.
- No `TOKEN_ENCRYPTION_KEY` needed.

### charts/mclaude-worker

- Remove `controller.config.devOAuthToken` and `DEV_OAUTH_TOKEN` env var from controller deployment.

### .github/workflows/deploy-main.yml

- Remove `DEV_OAUTH_TOKEN` from Bitwarden fetch step and all `--set` references.

## Data Model

### user_host_tokens table (new)

```sql
CREATE TABLE user_host_tokens (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    host_id     UUID NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    token_ct    TEXT NOT NULL,   -- base64(nonce || AES-256-GCM ciphertext)
    k_wrapped   TEXT NOT NULL,   -- base64(X25519-ECDH encrypted symmetric key)
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, host_id)
);
```

### hosts table — new column

```sql
ALTER TABLE hosts ADD COLUMN x25519_public_key TEXT;
-- base64-encoded X25519 public key registered at host startup
```

### mclaude-host-key k8s Secret (on host cluster)

| Key | Value |
|-----|-------|
| `x25519-private` | base64(X25519 private key, 32 bytes) |
| `sym-key` | base64(symmetric key K, 32 bytes) — written after first token setup |

### user-anthropic-{uslug} k8s Secret (in user namespace on host)

| Key | Value |
|-----|-------|
| `token_ct` | base64(nonce \|\| AES-256-GCM ciphertext of Anthropic token) |

## Error Handling

- No `user_host_tokens` row for (user, host) at session start → CP returns error; session not created; UI shows "Anthropic token not configured for this host".
- `mclaude-host-key` Secret missing `sym-key` at session start → session-agent exits non-zero; pod fails with clear log message.
- Decryption failure (wrong key) → session-agent exits with error; pod enters `Error` state; user must re-run `mclaude auth anthropic`.
- Host X25519 keypair lost (cluster reset) → `sym-key` gone → all existing ciphertexts unrecoverable; user must re-run `mclaude auth anthropic` (re-derives K from NKey seed, re-encrypts token).

## Security

- CP stores only `(token_ct, k_wrapped)` — cannot decrypt without host's X25519 private key.
- X25519 private key never leaves the host cluster's k8s Secret.
- Decryption happens inside the session-agent process; plaintext never written to any persistent store.
- K is deterministically derivable from user's NKey seed + host_id — recovery path if `sym-key` is lost without cluster reset.
- `user-anthropic-{uslug}` Secret contains only ciphertext; if read by an attacker without K it is useless.
- Controller-k8s holds K in `mclaude-host-key` after the first token setup, but never sees the plaintext token at session start (CP writes ciphertext to k8s Secret; session-agent decrypts).

## Key Loss Recovery

If `mclaude-host-key.sym-key` is lost (cluster reset but not host re-registration):
1. Host re-decrypts K from `K_wrapped` in DB using its X25519 private key.
2. Host re-populates `mclaude-host-key.sym-key`. No user action required.

If host X25519 private key is lost (full cluster reset, host re-registers):
1. Host generates new X25519 keypair.
2. All existing `k_wrapped` blobs are unrecoverable (wrong host key).
3. CP detects host re-registration (new public key) and marks all `user_host_tokens` for that host as stale.
4. Users re-run `mclaude auth anthropic` — CLI re-derives K, wraps with new host public key.

## Impact

- `docs/mclaude-control-plane/spec-control-plane.md`: add `user_host_tokens` table, 3 new endpoints, session creation token write, x25519_public_key on hosts, remove DEV_OAUTH_TOKEN.
- `docs/mclaude-controller-k8s/spec-k8s-architecture.md`: add X25519 keypair generation, NATS token.updated subscription, remove DEV_OAUTH_TOKEN.
- `docs/mclaude-cli/spec-cli.md`: add `mclaude auth anthropic` command.
- `docs/mclaude-session-agent/spec-session-agent.md`: add decryption at startup, new Secret mounts.
- `docs/charts-mclaude/spec-helm.md`: remove devOAuthToken from both charts.
- `docs/spec-state-schema.md`: add `user_host_tokens`, update user-secrets/user-anthropic Secrets description.

## Scope

**In v1:**
- X25519 keypair at host registration.
- Claude.ai PKCE OAuth flow in SPA (Settings → Anthropic).
- `mclaude auth anthropic` CLI command (also OAuth).
- `user_host_tokens` DB table.
- CP endpoints for store/status/delete.
- CP writes `(token_ct, K_wrapped)` to per-session k8s Secret.
- Session-agent decryption in `entrypoint.sh`.
- Remove `DEV_OAUTH_TOKEN` everywhere.

**Deferred:**
- Silent token refresh (OAuth refresh token storage and auto-renewal).
- Multi-host token management UI.
- CP-side stale token detection on host re-registration.
- CP-side stale token detection on host re-registration.

## Open questions

<!-- none -->

## Integration Test Cases

| Test case | What it verifies | Setup/teardown | Components exercised |
|-----------|------------------|----------------|----------------------|
| CLI sets token → session-agent exports CLAUDE_CODE_OAUTH_TOKEN | Full end-to-end: `mclaude auth anthropic` → CP stores (token_ct, k_wrapped) → CP writes to k8s Secret → session-agent decrypts correctly | Create test user + host; delete after | CLI, CP, controller-k8s, session-agent |
| CP cannot read plaintext | After token set, query `user_host_tokens` directly — values are opaque blobs | Create test user + host | CP, DB |
| Session without token fails | Create session for user+host with no token row → CP returns 4xx | Create test user + host with no token | CP |
| Key loss recovery | Delete `sym-key`, re-run `mclaude auth anthropic`, session-agent decrypts successfully | Requires test host cluster | CLI, CP, session-agent |

## Implementation Plan

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| mclaude-control-plane | ~250 lines | 130k | DB migration, 3 endpoints, session write, host x25519 field |
| mclaude-controller-k8s | ~100 lines | 80k | X25519 keypair gen, NATS subscription, remove devOAuthToken |
| mclaude-cli | ~150 lines | 90k | `auth anthropic` command, HKDF, AES-GCM, X25519 wrap |
| mclaude-session-agent | ~30 lines | 40k | entrypoint.sh decryption, new Secret mounts |
| charts (cp + worker) | ~10 lines removed | 20k | Remove devOAuthToken from both charts |
| .github/workflows/deploy-main.yml | ~5 lines removed | 10k | Remove DEV_OAUTH_TOKEN |

**Total estimated tokens:** 370k
**Estimated wall-clock:** 4–5h
