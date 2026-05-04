# ADR: Per-User Anthropic OAuth Token — User-Agent Encryption

**Status**: draft
**Status history**:
- 2026-05-04: draft

## Overview

Each mclaude user stores their own Anthropic OAuth token, encrypted client-side with a symmetric key K that only the user's own user-agent holds. The control plane stores ciphertext only — it cannot decrypt the token at any point. A per-user user-agent process runs in the user's namespace on each trusted host, holds K in memory, and decrypts the token for session-agents on demand. The global `DEV_OAUTH_TOKEN` mechanism is removed.

## Motivation

The current `DEV_OAUTH_TOKEN` is a single shared Anthropic credential that flows through the controller-k8s deployment env var, making it visible in `kubectl get deploy -o yaml`. There is no per-user concept: all users share one token and cannot bring their own Claude account. Even an encrypted-at-rest approach that gives the host controller the key is insufficient — the host operator can read any k8s Secret in the cluster.

The new design provides a cryptographic guarantee: the CP and the host operator cannot read any user's Anthropic token even with full DB and cluster access.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Encryption model | Client-side: `token_ct = AES-256-GCM(K, token)` | CP stores ciphertext only |
| K — cardinality | One K per user (not ephemeral per operation) | Trusting a new host requires only wrapping K with the new host's user-agent pubkey; no token re-upload |
| K — derivation | (OPEN — see open questions) | NKey-coupled vs independent random K |
| User-agent X25519 keypair | Generated at user-agent startup, stored in k8s Secret in user namespace | Separate from host NKey; private key never leaves user namespace |
| K delivery path | Client wraps K with user-agent X25519 pubkey → CP relays K_wrapped → user-agent decrypts | Host controller never touches K; CP is blind relay |
| K storage | User-agent holds K in memory; authoritative K_wrapped in DB | Restart recovery: user-agent re-decrypts K from K_wrapped via CP |
| Decryption point | User-agent process in user namespace | Session-agent never holds K; calls user-agent ClusterIP API |
| CP role | Stores token_ct (user_tokens) and K_wrapped (user_host_key_grants) as opaque blobs | Blind to plaintext |
| User-agent deployment | Spun up by controller-k8s when user namespace is created | One user-agent per (user, host) pair |
| Token entry — OAuth | Claude.ai PKCE flow from SPA or CLI | Browser/CLI obtains token from Anthropic, then runs same client-side encrypt path |
| Token entry — manual paste | Write-only input in SPA or `--token` flag in CLI | User pastes token; same encrypt path as OAuth; never displayed again |
| Token origin | OAuth access token from claude.ai — not a static API key | Ephemeral; re-auth required on expiry |
| Token re-auth | User re-runs OAuth flow or re-pastes token; new token_ct replaces old row | No silent refresh in v1 |
| Missing token at session start | Session creation fails with clear error | Fail fast; user sees "Connect Claude account in Settings" |
| DEV_OAUTH_TOKEN | Removed from controller-k8s, CP chart, CI workflow | Per-user encrypted token replaces global dev token |
| CoCo/Kata upgrade path | Replace k8s Secret K delivery with remote attestation | Under TEE: user-agent proves its code identity before receiving K; host operator cannot inspect memory; session-agent interface unchanged |

## Key Establishment Flow

### One-time: user-agent startup

When controller-k8s creates a user namespace, it deploys a user-agent pod. On startup:
1. User-agent generates an X25519 keypair (or reads existing from `user-agent-key` Secret).
2. Registers X25519 public key with CP: `PUT /api/users/{uslug}/hosts/{hslug}/agent-pubkey`.
3. Fetches K_wrapped for this (user, host) from CP if available; decrypts K using X25519 private key; holds K in memory.
4. If no K_wrapped yet: waits for NATS notification `mclaude.users.{uslug}.hosts.{hslug}.key.grant`.

### Token setup (first time, or re-auth)

**Client-side encrypt (shared by OAuth and manual-paste paths):**
1. Client generates or loads K (32 bytes; see open question on derivation).
2. `token_ct = AES-256-GCM(K, access_token)`.
3. For each trusted host: fetches user-agent X25519 public key from CP (`GET /api/users/{uslug}/hosts/{hslug}/agent-pubkey`).
4. `K_wrapped = X25519-ECDH-encrypt(user_agent_x25519_pub, K)` — one K_wrapped per host.
5. Client POSTs to CP: `POST /api/users/{uslug}/anthropic-token` body `{ "token_ct": "..." }`.
6. For each trusted host: `POST /api/users/{uslug}/hosts/{hslug}/key-grant` body `{ "k_wrapped": "..." }`.
7. CP stores blobs; notifies each user-agent via NATS.
8. User-agent decrypts K from K_wrapped; holds in memory.

**OAuth path (SPA):**
- User clicks "Connect Claude account" → SPA redirects to claude.ai (PKCE).
- User approves → redirected back with `code`.
- SPA exchanges code for access token client-side → runs encrypt+wrap+POST.

**OAuth path (CLI):**
- `mclaude auth anthropic [--host <hslug>]` opens browser to claude.ai; handles PKCE callback on loopback → runs encrypt+wrap+POST.

**Manual paste path (SPA):**
- User clicks "Enter token manually" → write-only password input → SPA runs encrypt+wrap+POST. Input cleared immediately.

**Manual paste path (CLI):**
- `mclaude auth anthropic --token <value> [--host <hslug>]` → runs encrypt+wrap+POST directly.

### Trusting a new host (K already exists)

1. Controller-k8s creates user namespace on new host; deploys user-agent.
2. User-agent generates X25519 keypair; registers public key with CP.
3. CP notifies client (or user goes to Settings → Trusted Hosts → Trust).
4. Client fetches new user-agent X25519 public key.
5. Client wraps existing K: `K_wrapped_new = X25519-ECDH-encrypt(new_user_agent_pub, K)`.
6. Client POSTs `K_wrapped_new` to CP: `POST /api/users/{uslug}/hosts/{hslug}/key-grant`.
7. CP stores, notifies user-agent; user-agent decrypts K.
8. No token re-upload required.

## Session Start Flow

1. CP creates session pod for user U on host H.
2. CP writes `token_ct` into k8s Secret `user-anthropic-{uslug}` in user namespace.
3. Session-agent pod mounts `/secrets/anthropic/token_ct`.
4. Session-agent calls user-agent ClusterIP: `POST http://user-agent.mclaude-user-{uslug}.svc/decrypt` body `{ "token_ct": "<value>" }`.
5. User-agent decrypts using K in memory; returns `{ "token": "<plaintext>" }`.
6. Session-agent exports `CLAUDE_CODE_OAUTH_TOKEN=<token>`.
7. Claude Code uses `CLAUDE_CODE_OAUTH_TOKEN` for all Anthropic API calls.

**Failure modes:**
- User-agent not running → session-agent returns error; pod fails with log "user-agent unavailable".
- K not loaded (user-agent restarted, K_wrapped not yet delivered) → user-agent returns 503; session-agent retries with backoff.
- No token_ct for user → CP rejects session creation with 4xx: "Anthropic token not configured".

## Component Changes

### mclaude-controller-k8s

- Creates user namespace + deploys user-agent Deployment on first user session per host (or on explicit host trust action).
- Removes all `devOAuthToken` / `oauth-token` writes from user-secrets reconciler paths.
- No changes to X25519 keypair handling (that belongs to user-agent, not host controller).

### mclaude-user-agent (NEW component)

- Deployed in user namespace `mclaude-user-{uslug}` on each trusted host.
- Generates X25519 keypair on first boot; stores in k8s Secret `user-agent-key` (key: `x25519-private`).
- Registers X25519 public key with CP at startup.
- Subscribes to NATS `mclaude.users.{uslug}.hosts.{hslug}.key.grant` — on receipt: decrypts K from K_wrapped, holds in memory.
- Serves `POST /decrypt` on ClusterIP (no external exposure): input `{ "token_ct": "..." }`, output `{ "token": "..." }`.
- On restart: re-fetches K_wrapped from CP and re-decrypts.
- CoCo/Kata upgrade: K delivered via remote attestation instead of k8s Secret; interface to session-agent unchanged.

### mclaude-control-plane

- New DB tables: `user_tokens` and `user_host_key_grants` (see Data Model).
- New endpoint: `POST /api/users/{uslug}/anthropic-token` — stores token_ct. Auth: self only.
- New endpoint: `POST /api/users/{uslug}/hosts/{hslug}/key-grant` — stores K_wrapped, publishes NATS `mclaude.users.{uslug}.hosts.{hslug}.key.grant`. Auth: self only.
- New endpoint: `GET /api/users/{uslug}/hosts/{hslug}/agent-pubkey` — returns user-agent X25519 public key.
- New endpoint: `PUT /api/users/{uslug}/hosts/{hslug}/agent-pubkey` — user-agent registers its public key. Auth: user-agent service account.
- New endpoint: `GET /api/users/{uslug}/anthropic-token/status` — returns `{ "set": true/false, "updated_at": "..." }`. No token data.
- New endpoint: `DELETE /api/users/{uslug}/anthropic-token` — removes token_ct + all K_wrapped grants; publishes deletion notification.
- Session creation: writes token_ct into k8s Secret `user-anthropic-{uslug}`. Fails session start if no token row.
- Removes `devOAuthToken` from all code paths.

### mclaude-cli

- New command `mclaude auth anthropic [--host <hslug>]`:
  1. Prompts user to paste token OR performs OAuth redirect.
  2. Generates/loads K (see open question on derivation).
  3. `token_ct = AES-256-GCM(K, token)`.
  4. For each trusted host: fetches user-agent X25519 pubkey; wraps K.
  5. POSTs token_ct + all K_wrappeds to CP.
- New command `mclaude auth anthropic --status [--host <hslug>]` — shows connected/not set.
- New command `mclaude auth anthropic --revoke [--host <hslug>]` — calls DELETE endpoint.

### mclaude-session-agent

- `entrypoint.sh`: replaces current `oauth-token` file read with HTTP call to user-agent:
  ```sh
  token_ct=$(cat /secrets/anthropic/token_ct)
  export CLAUDE_CODE_OAUTH_TOKEN=$(curl -sf -X POST http://user-agent.mclaude-user-${USER_SLUG}.svc/decrypt \
    -H "Content-Type: application/json" \
    -d "{\"token_ct\": \"$token_ct\"}" | jq -r .token)
  ```
- Mounts `user-anthropic-{uslug}` Secret (CP writes at session creation).
- Does NOT mount host-key Secret.

### mclaude-web (SPA)

- Settings → Anthropic Account: "Connect Claude account" (PKCE OAuth) + "Enter token manually" (write-only input).
- Shows connection status (set / not set / last updated).
- Trusted Hosts panel: shows pending hosts awaiting K_wrapped grant; user approves to wrap K and send.

### charts/mclaude-cp

- Remove `controlPlane.devOAuthToken` value and env var.

### charts/mclaude-worker

- Remove `controller.config.devOAuthToken` and `DEV_OAUTH_TOKEN` env var.
- Add user-agent Deployment template to user namespace provisioning.

### .github/workflows/deploy-main.yml

- Remove `DEV_OAUTH_TOKEN` from Bitwarden fetch step and all `--set` references.

## Data Model

### user_tokens table (new)

```sql
CREATE TABLE user_tokens (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE UNIQUE,
    token_ct    TEXT NOT NULL,   -- base64(nonce || AES-256-GCM ciphertext of Anthropic token)
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

### user_host_key_grants table (new)

```sql
CREATE TABLE user_host_key_grants (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    host_id     UUID NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    k_wrapped   TEXT NOT NULL,   -- base64(X25519-ECDH encrypted symmetric key K)
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, host_id)
);
```

### hosts table — new column

```sql
ALTER TABLE hosts ADD COLUMN user_agent_x25519_public_key TEXT;
-- base64-encoded X25519 public key registered by user-agent at startup
-- (per-user, per-host — consider moving to user_host_key_grants or separate table if cardinality demands)
```

Note: `user_agent_x25519_public_key` is per (user, host), not per host. This column placement needs revision — likely a separate `user_host_agent_keys (user_id, host_id, x25519_public_key)` table.

### user-agent-key k8s Secret (in user namespace on host)

| Key | Value |
|-----|-------|
| `x25519-private` | base64(X25519 private key, 32 bytes) |

### user-anthropic-{uslug} k8s Secret (in user namespace on host, written by CP at session start)

| Key | Value |
|-----|-------|
| `token_ct` | base64(nonce \|\| AES-256-GCM ciphertext of Anthropic token) |

## Error Handling

- No `user_tokens` row for user at session start → CP returns 4xx; session not created; UI shows "Anthropic token not configured".
- User-agent not ready (K not loaded) → `POST /decrypt` returns 503; session-agent retries with backoff (3 attempts, 2s apart); final failure exits non-zero with clear log.
- Decryption failure (wrong key) → user-agent returns 500; session-agent exits; pod enters Error state; user must re-run auth.
- User-agent pod restart → on startup re-fetches K_wrapped from CP and re-decrypts K; sessions that started before restart are not affected (token already exported).
- Host re-registration (new user-agent keypair) → old K_wrapped unrecoverable; CP marks `user_host_key_grants` for that (user, host) stale; user re-runs trust flow (wrap K with new pubkey).

## Security

- CP stores only `(token_ct, k_wrapped)` — cannot decrypt without user-agent's X25519 private key.
- User-agent X25519 private key never leaves the user namespace k8s Secret.
- K never touches the host controller or CP in plaintext.
- Decryption happens inside the user-agent process; plaintext token is returned only to the requesting session-agent over ClusterIP (no external exposure).
- `user-anthropic-{uslug}` Secret contains only ciphertext; if read by attacker without K it is useless.
- Session-agent never stores K — only receives the decrypted token for the duration of the process.
- CoCo/Kata upgrade: user-agent runs in TEE; K delivered via remote attestation; host operator cannot inspect memory even with cluster-admin.

## Key Loss Recovery

If user-agent restarts (K lost from memory):
1. User-agent re-fetches K_wrapped from CP on startup.
2. Decrypts K from K_wrapped using its X25519 private key.
3. No user action required.

If user-agent X25519 private key is lost (`user-agent-key` Secret deleted):
1. User-agent generates new keypair; registers new public key with CP.
2. Old K_wrapped is unrecoverable.
3. User re-runs trust flow: wraps K with new user-agent public key; sends to CP.
4. No token re-upload required (token_ct unchanged; K unchanged).

If K is lost (user has no record of K — e.g., all clients wiped):
1. User re-runs OAuth flow or re-pastes token.
2. New K generated; new token_ct + K_wrapped for all trusted hosts.

## Impact

- `docs/mclaude-control-plane/spec-control-plane.md`: add `user_tokens` + `user_host_key_grants` tables, 5 new endpoints, session creation token write, remove DEV_OAUTH_TOKEN.
- `docs/mclaude-controller-k8s/spec-k8s-architecture.md`: add user-agent deployment on namespace creation, remove DEV_OAUTH_TOKEN.
- `docs/mclaude-cli/spec-cli.md`: add `mclaude auth anthropic` command.
- `docs/mclaude-session-agent/spec-session-agent.md`: replace file-read with user-agent HTTP call, new Secret mount.
- `docs/charts-mclaude/spec-helm.md`: remove devOAuthToken from both charts; add user-agent Deployment template.
- `docs/spec-state-schema.md`: add `user_tokens`, `user_host_key_grants`, update user-anthropic Secret description.
- NEW: `docs/mclaude-user-agent/spec-user-agent.md` (new component spec).

## Scope

**In v1:**
- User-agent deployment per (user, host) namespace.
- Claude.ai PKCE OAuth flow in SPA and CLI.
- Manual token paste in SPA (write-only) and CLI (`--token` flag).
- `mclaude auth anthropic` CLI command.
- `user_tokens` + `user_host_key_grants` DB tables.
- CP endpoints for store/status/delete/grant/pubkey.
- CP writes `token_ct` to per-session k8s Secret.
- Session-agent HTTP call to user-agent for decryption.
- Remove `DEV_OAUTH_TOKEN` everywhere.

**Deferred:**
- Silent token refresh (OAuth refresh token storage and auto-renewal).
- Multi-host token management UI (trusted hosts list + pending grants).
- CoCo/Kata remote attestation K delivery (replaces k8s Secret; user-agent interface unchanged).
- CP-side stale grant detection on user-agent re-registration.

## Open questions

- **K derivation**: NKey-coupled (HKDF from NKey seed + host_id, so NKey rotation forces token re-auth) vs independent random K (NKey and token key are orthogonal; K loss requires full re-auth). Trade-off: coupling enforces hygiene at cost of bigger NKey rotation; independence is simpler but loss recovery is heavier.
- **user_agent_x25519_public_key storage**: currently noted as a column on `hosts` table but is actually per (user, host) — needs a separate `user_host_agent_keys` table or merged into `user_host_key_grants`.
- **User-agent bootstrapping sequence**: controller-k8s creates user namespace on first session or on explicit host-trust action? First session is seamless but means a cold-start gap (user-agent not ready when first session starts). Explicit trust action in Settings is safer but adds a step.

## Integration Test Cases

| Test case | What it verifies | Setup/teardown | Components exercised |
|-----------|------------------|----------------|----------------------|
| CLI sets token → session-agent exports CLAUDE_CODE_OAUTH_TOKEN | Full end-to-end: `mclaude auth anthropic` → CP stores (token_ct, k_wrapped) → user-agent receives grant → session-agent decrypts via user-agent | Create test user + host; delete after | CLI, CP, user-agent, session-agent |
| CP cannot read plaintext | After token set, query `user_tokens` + `user_host_key_grants` — values are opaque blobs | Create test user + host | CP, DB |
| Session without token fails | Create session for user+host with no token row → CP returns 4xx | Create test user + host with no token | CP |
| Trust new host | Set token on host A; trust host B → only K_wrapped upload needed; no token re-upload | Create test user + 2 hosts | CLI, CP, user-agent (host B) |
| User-agent restart recovery | Delete user-agent pod; restart; session-agent still decrypts successfully | Requires test host cluster | CP, user-agent, session-agent |

## Implementation Plan

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| mclaude-user-agent (new) | ~200 lines | 120k | X25519 keypair gen, NATS subscription, /decrypt HTTP endpoint |
| mclaude-control-plane | ~300 lines | 150k | DB migration, 5 endpoints, session write, remove devOAuthToken |
| mclaude-controller-k8s | ~80 lines | 70k | User-agent deployment, remove devOAuthToken |
| mclaude-cli | ~150 lines | 90k | `auth anthropic` command, AES-GCM, X25519 wrap |
| mclaude-session-agent | ~20 lines | 40k | Replace file read with HTTP call to user-agent |
| mclaude-web | ~100 lines | 80k | OAuth flow, manual paste, trusted hosts panel |
| charts (cp + worker) | ~50 lines | 30k | Add user-agent template, remove devOAuthToken |
| .github/workflows/deploy-main.yml | ~5 lines removed | 10k | Remove DEV_OAUTH_TOKEN |

**Total estimated tokens:** 590k
**Estimated wall-clock:** 6–8h
