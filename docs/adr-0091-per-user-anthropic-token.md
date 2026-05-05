# ADR: Per-User Anthropic OAuth Token

**Status**: accepted
**Status history**:
- 2026-05-04: draft
- 2026-05-04: accepted — paired with spec-state-schema.md, spec-nats-data-taxonomy.md, mclaude-control-plane/spec-control-plane.md, mclaude-controller-k8s/spec-k8s-architecture.md, mclaude-cli/spec-cli.md, mclaude-session-agent/spec-session-agent.md, charts-mclaude/spec-helm.md, mclaude-user-agent/spec-user-agent.md (new)

## Overview

Each mclaude user stores their own Anthropic OAuth token in a per-user client-only NATS KV bucket (`mclaude-user-info-{uslug}`). The token is delivered to user-agents via X25519 sealed-box wrapping over NATS request/reply; the user-agent unseals the token and writes the plaintext to a k8s Secret (`user-tokens-{uslug}`) that session pods mount. The user-agent also takes over session pod creation from the host controller, becoming the session lifecycle manager for its (user, host) scope. The global `DEV_OAUTH_TOKEN` mechanism is removed.

## Motivation

The current `DEV_OAUTH_TOKEN` is a single shared Anthropic credential that flows through the worker cluster controller deployment env var, making it visible in `kubectl get deploy -o yaml`. There is no per-user concept: all users share one token and cannot bring their own Claude account.

The new design ensures:
- The credential flow originates from the authenticated user's browser or CLI — no infrastructure party initiates it.
- Any authenticated mclaude session (any device, any browser) can participate in the flow without re-doing Anthropic OAuth.
- The token is never transmitted in plaintext on host-scoped subjects — user-agents receive it only as an X25519 sealed box.
- NATS RBAC enforces that only the owning user's client-side identities (SPA/CLI) can read the token from KV. NATS JetStream encryption at rest protects the token on disk.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Token storage (client) | `mclaude-user-info-{uslug}` KV bucket, key `tokens.{provider}` | Per-user bucket; NATS RBAC scoped to owning user; at-rest encryption via JetStream; provider-keyed for multi-backend support |
| Token writer | SPA or CLI only | Client is the deliberate party; token cannot be set without an authenticated user action |
| Token readers (client) | SPA/CLI (any device) via `mclaude-user-info-{uslug}` | Cross-device; no per-device re-auth needed; login hydrates from KV |
| Token host delivery | X25519 sealed box via NATS request/reply: `token_wrapped = crypto_box_seal(token, ua_x25519_pub)` | SPA sends directly to user-agent; plaintext never on host-scoped NATS subjects |
| Token storage (host) | k8s Secret `user-tokens-{uslug}` in user namespace, key per provider | Written by user-agent after unsealing; session pods mount directly; no HTTP endpoint needed |
| User-agent X25519 keypair | Generated at first startup; private key persisted in k8s Secret `user-agent-key` | Survives pod restarts without requiring SPA to be online; CoCo/Kata upgrade: TEE protects private key |
| User-agent X25519 pubkey | Registered with CP via JWT issuance endpoint; stored in DB | SPA reads pubkey from CP API; no KV bucket needed for pubkey exchange |
| Project provisioning | User-agent creates project pods (migrated from host controller). Session lifecycle within the pod stays with session-agent. | User-agent = infrastructure layer (pods, secrets). Session-agent = application layer (git, CLI processes, session state). Boundary is the pod spec. |
| Pod source-of-truth | k8s itself. User-agent is event-driven (provision/delete subjects), not a reconciler. | No external "expected pods" list. On user-agent restart, existing pods are adopted unchanged. Pods are Deployments so k8s self-heals. |
| Revocation | Deferred to a follow-up ADR. | v1 has no /grant/revoke endpoint or .grants.revoke subject. Manual cleanup via `kubectl delete namespace mclaude-{uslug}` if an operator needs to evict a user. Full design (JWT revocation + token Secret cleanup + namespace teardown + recovery) handled separately. |
| Host controller role | Decides which users to grant access to; publishes grant to CP; creates user namespace + deploys user-agent | Host initiates; constant-size JWT; session lifecycle fully delegated to user-agent |
| CP role | Directory (stores grants, visible to SPA); credential issuer (NATS JWTs after mutual consent); identity provider; pubkey store | Not an authorization gate; not in session creation path; does not store, wrap, or unwrap the token |
| Default platform flag | `is_default_platform BOOLEAN` on `hosts` table; set during cluster registration by admin | Exactly one cluster can be default; auto-grant + auto-accept for all users |
| User-agent deployment | Spun up by host controller when it grants a user access (Phase 1) | Automatic; host controller creates namespace + deploys user-agent as part of grant |
| Token entry — OAuth | Claude.ai PKCE flow from SPA or CLI | Browser/CLI obtains token, writes to KV, delivers to trusted hosts |
| Token entry — manual paste | Write-only input in SPA or `--token` flag in CLI | Same path; never displayed again |
| Token re-auth | User re-runs OAuth or re-pastes; new token overwrites KV key; SPA re-wraps and re-delivers to each trusted host | Both SPA and user-agents must be online for re-delivery |
| Missing token at session start | Session creation fails with clear error | Fail fast; user sees "Connect Claude account" |
| Missing token_wrapped at user-agent start | User-agent is in "untrusted" state; SPA shows host as needing authorization | User explicitly trusts host → same flow as first-time delivery; no special recovery path |
| Multi-provider support | KV key `tokens.{provider}`, k8s Secret key `{provider}`, delivery payload includes provider | Same mechanism for anthropic, google, devin, etc.; CP sets `TOKEN_PROVIDER` env var on session pod |
| DEV_OAUTH_TOKEN | Removed from mclaude-controller-k8s, CP chart, CI workflow | Per-user token replaces global dev token |
| CoCo/Kata upgrade path | X25519 private key protected by TEE attestation; token Secret replaced by in-memory serving | User-agent proves code identity before accessing private key; session pods get token via TEE-attested endpoint instead of Secret mount |

## KV Layout

### mclaude-user-info-{uslug} bucket (new, per-user)

Client-only bucket. One per user, created by CP on user creation. No infrastructure identity (user-agent, session-agent, host) has access. Follows the per-user bucket pattern (`mclaude-sessions-{uslug}`, `mclaude-projects-{uslug}`).

| Key | Writer | Readers | Value |
|-----|--------|---------|-------|
| `tokens.anthropic` | SPA/CLI | SPA/CLI (any device) | Anthropic OAuth access token (plaintext) |
| `tokens.{provider}` | SPA/CLI | SPA/CLI (any device) | Provider token (plaintext) — future providers |
| `git.{provider}` | SPA/CLI | SPA/CLI (any device) | Git credential (PAT/OAuth token) — future, separate ADR |
| `preferences.*` | SPA/CLI | SPA/CLI (any device) | User preferences (SPA-only, not delivered to hosts) |

The bucket and X25519 delivery mechanism are general-purpose. Any secret stored here can be wrapped and delivered to user-agents using the same flow. Git credentials (GitHub, GitLab, SSH keys) are the natural next candidate — separate ADR.

User JWT permissions: KV read/write on `mclaude-user-info-{uslug} / *`.

NATS RBAC ensures only the owning user's JWT can access the bucket. JetStream at-rest encryption (`store_key` in NATS server config) should be enabled to protect the token on disk — this requires adding the encryption stanza to `charts/mclaude-cp/templates/nats-configmap.yaml` and provisioning a key. Spec'd as a prerequisite in the Helm chart changes below.

## Trust Model

User-agent provisioning follows a bilateral grant/accept handshake. The host initiates, the user consents. CP acts as a directory and credential issuer — not an authorization gate.

**Scope note**: v1 implements the manual flows (Phase 1–3) plus default platform automation (auto-grant all users, auto-accept). Policy-driven grants for non-default hosts, user-configurable auto-accept, and CP-delegated credential delivery are future automation paths described here but out of scope for v1.

### Phase 1: Host grants user access

The host decides which users should have access. Two modes:

**Manual grant**: host operator (or host controller API) publishes grant on `mclaude.hosts.{hslug}.grants.create` with payload `{ "uslug": "alice" }`.

**Policy-driven grant**: host registers a grant policy with CP at host registration time (e.g., `grant_policy: { auto: true }`). On new user creation, CP evaluates matching policies and publishes grants on behalf of those hosts. The default platform uses this — `auto: true` as a built-in. Grant policy approval/governance is out of scope (org/admin concern).

In either case:

1. CP stores the grant in DB. CP is a directory — it doesn't approve or deny.
2. Host controller generates NKey pair (Ed25519) for the user-agent. Writes NKey seed to k8s Secret `user-agent-key` in user namespace.
3. Host controller registers user-agent's NKey public key with CP via `mclaude.hosts.{hslug}.api.agents.user-agent.register` with payload `{ "uslug": "alice", "nkey_public": "U..." }`. CP validates: `host_grants` row must exist for `(host_slug, uslug)` — rejects if the host hasn't granted this user access. This prevents a host from registering agents for arbitrary users. Trust chain: user → host → user-agent.
4. Host controller creates user namespace `mclaude-{uslug}` + deploys user-agent pod.
5. User-agent starts → reads NKey seed from Secret → generates X25519 keypair → writes X25519 private key to Secret.
6. User-agent authenticates with CP via existing challenge-response (`POST /api/auth/challenge` + `POST /api/auth/verify`). CP verifies NKey signature → issues bootstrap JWT. Also registers X25519 pubkey at this step.
7. User-agent connects to NATS with bootstrap JWT (scoped to `.agent.activate` subscribe only). Sits idle until grant is accepted.

CoCo/Kata upgrade: step 5 adds TEE attestation. User-agent proves code identity via attestation report. CP verifies both NKey signature and attestation before issuing JWT.

### Phase 2: User accepts grant

The user decides whether to trust the host. Two modes:

**Explicit accept**: SPA calls `GET /api/users/{uslug}/grants` → CP returns pending grants including each user-agent's X25519 pubkey fingerprint. SPA shows "Host Y granted you access. Agent fingerprint: `a1b2c3d4...` — [Accept] / [Ignore]." User clicks [Accept] → SPA sends `POST /api/users/{uslug}/hosts/{hslug}/grant/accept` with `{ "x25519_fingerprint": "a1b2c3d4..." }`. CP verifies: `hash(user_host_agents.x25519_pub) == x25519_fingerprint`. Match → accept. Mismatch → reject.

This pins the user's trust to a specific agent instance. If the host swaps the agent (new pubkey), the fingerprint changes and re-acceptance is required.

**Auto-accept (default platform)**: for the admin-designated default platform, CP auto-accepts on the user's behalf. The `is_default_platform` flag is admin-gated — a random host can't set it. Auto-accept for non-default hosts is deferred (user opt-in or enterprise policy).

In either case:

1. CP marks grant as accepted. Publishes a simple signal (empty or `{"status":"accepted"}`) to `mclaude.users.{uslug}.hosts.{hslug}.agent.activate`. No JWT in the payload.
2. User-agent receives the signal → re-runs challenge-response with CP. CP checks grant status → grant is accepted → issues full JWT.
3. User-agent reconnects to NATS with full JWT → subscribes to `mclaude.users.{uslug}.hosts.{hslug}.agent.>`.

This means user-agent has a single auth code path: always challenge-response, always get the JWT matching current grant status. The activate channel is just a notification — no sensitive material flows through it.

### Phase 3: Credential delivery

Trust is now established. Credentials are delivered to user-agent. Two modes:

**SPA online**: SPA reads user-agent's X25519 pubkey from CP → wraps each credential with `crypto_box_seal(token, ua_x25519_pub)` → delivers via NATS request/reply to `mclaude.users.{uslug}.hosts.{hslug}.agent.token-delivery` with payload `{ "provider": "anthropic", "token_wrapped": "<base64>" }`.

**SPA offline (CP delegate)**: if the user has enabled auto-accept for this host AND has a token in `mclaude-user-info-{uslug}` KV, CP completes the delivery on the user's behalf. CP reads the token from KV (full NATS access), reads user-agent's X25519 pubkey from DB, wraps, and delivers via NATS. This is opt-in — requires the user to have explicitly enabled auto-accept. Alternatively, an enterprise policy could configure this on behalf of the user (org/admin concern, out of scope). CP acting as delegate doesn't change the security model (CP can already read the KV).

In either case:

1. User-agent receives token_wrapped → unseals with X25519 private key → writes plaintext to `user-tokens-{uslug}` k8s Secret → ready.

### After trust is established

User-agent is the trust root within its (user, host) scope. All subsequent operations — project provisioning, credential updates — go directly between SPA and user-agent via NATS. Session lifecycle (within project pods) goes SPA → session-agent via existing per-project NATS subjects. CP is not in the data path for either.

NATS RBAC enforces that only the authorized user's JWT can publish to their user-agent's subjects. User-agent validates requests locally (has token? correct provider?).

### CP's role

CP is **not** an authorization gate. It is:
- **Directory**: stores grants, makes them visible to users. Both sides independently decide (host grants, user accepts). CP is where the two intentions meet.
- **Credential issuer**: issues NATS JWTs for user-agents after mutual consent. Holds the NATS account signing key.
- **Identity provider**: user accounts, authentication, host registration.
- **Pubkey store**: user-agent X25519 pubkeys, accessible to SPA for wrapping.
- **Delegate** (opt-in): delivers wrapped credentials on behalf of offline SPA when user has enabled auto-accept.
- **Policy executor**: evaluates host grant policies on user creation; publishes grants on behalf of hosts. Policy governance out of scope (org/admin).

## Key Establishment Flow

### User-agent startup

Deployed by host controller during Phase 1 (host grants user access). On startup:

1. Read NKey seed from k8s Secret `user-agent-key` in user namespace (written by host controller at Phase 1, step 2).
2. Read or generate X25519 keypair. If `x25519-private` absent in Secret: generate → write to Secret.
3. Authenticate with CP via challenge-response (`POST /api/auth/challenge` + `POST /api/auth/verify`). CP verifies NKey → issues bootstrap or full JWT depending on grant status. X25519 pubkey registered at this step.
4. Connect to NATS with issued JWT. Re-run challenge-response before TTL expiry to renew (same refresh pattern as session-agent's `agentJWTRefreshBuffer`). Each renewal gets whatever JWT matches current grant status — bootstrap or full.
5. Subscribe to all four `agent.>` subjects on every connect. NATS RBAC enforces scope: with a bootstrap JWT, only `.agent.activate` succeeds; non-`activate` subscriptions are rejected and logged as warnings. The single subscription path keeps the lifecycle simple — JWT scope decides what delivers, not client-side branching.
6. When signal received on `.agent.activate`: re-run challenge-response → CP issues full JWT (grant now accepted) → drain + reconnect with new JWT → re-subscribe to all four subjects (now all permitted).
7. If full JWT on initial auth (grant already accepted, e.g. restart): the four subscriptions all succeed immediately on first connect.
7. Token availability is checked at provision time via `os.ReadFile("/secrets/tokens/{provider}")` rather than cached in memory at startup. The `user-tokens-{uslug}` Secret is k8s-managed and updates propagate to the mount via kubelet.
8. Subscribe to `.agent.token-delivery` so subsequent SPA-initiated deliveries (re-auth, new providers) can be unsealed and patched into the Secret.
9. Subscribe to `.agent.projects.provision` and `.agent.projects.delete` for project pod lifecycle.

### Client login (session hydration)

On SPA or CLI login, after NATS connection is established:

1. Hydrate from `mclaude-user-info-{uslug}` KV — read all keys into client session state.
2. If `tokens.anthropic` key exists: token is available in memory. Client can deliver to hosts, check status, etc.
3. If `tokens.anthropic` key absent: no Anthropic token configured yet. Client shows "Connect Claude account" prompt.

On logout: clear all in-memory state. KV persists for other devices/sessions.

### Token setup (first time, or re-auth)

Precondition: client is logged in and session state is hydrated.

1. Obtain Anthropic access token (OAuth PKCE or manual paste).
2. Write token to `mclaude-user-info-{uslug} / tokens.anthropic`.
3. Client proceeds to host trust flow (below) for each accessible host.

**OAuth path (SPA):**
- User clicks "Connect Claude account" → PKCE redirect to claude.ai → `code` returned → SPA exchanges for `access_token` → runs steps 1–3.

**OAuth path (CLI):**
- `mclaude auth anthropic` opens browser; handles PKCE loopback → runs steps 1–3.

**Manual paste path (SPA):**
- Write-only password input → runs steps 1–3. Input cleared immediately.

**Manual paste path (CLI):**
- `mclaude auth anthropic --token <value>` → runs steps 1–3 directly.

### Host trust (SPA delivers token to user-agent)

When the SPA decides to trust a host. The SPA-side trigger conditions (auto-deliver vs. require [Authorize] click, copy, error states) are the UX concern of ADR-0092 — separate ADR, sibling not dependency. ADR-0091 only specifies the mechanism below; any ordering or dispatch policy is ADR-0092's. CLI delivery is fully specified in `mclaude-cli/spec-cli.md` (`mclaude auth anthropic` iterates accepted grants and delivers).

1. SPA uses token from session state (hydrated at login).
2. SPA reads user-agent's X25519 pubkey from CP: `GET /api/users/{uslug}/hosts/{hslug}/user-agent/pubkey`.
3. `token_wrapped = crypto_box_seal(token, ua_x25519_pub)` (NaCl sealed box).
4. SPA publishes token_wrapped via NATS request/reply to `mclaude.users.{uslug}.hosts.{hslug}.agent.token-delivery` with payload `{ "provider": "anthropic", "token_wrapped": "<base64>" }`.
5. User-agent receives → unseals with X25519 private key → writes plaintext token to k8s Secret `user-tokens-{uslug}` (key: `anthropic`) → holds in memory. Ready.

Both SPA and user-agent must be online. If user-agent is down, SPA shows an error; user retries when host is available.

### Second device (or new browser)

No special flow. Login hydration (above) reads token from `mclaude-user-info-{uslug}` via user NATS JWT. Token is available immediately — no Anthropic OAuth re-run required.

### New host added (token already exists)

Follows the grant/accept handshake (Phase 1–3). For the default platform, fully automated:

1. Host controller grants user access → CP stores grant → host controller creates namespace + deploys user-agent.
2. User-agent starts → generates X25519 keypair → registers pubkey → gets bootstrap JWT → watches `.agent.activate`.
3. Default platform: CP auto-accepts → publishes full JWT → user-agent activates.
4. SPA detects accepted host → wraps token → delivers token_wrapped via NATS request/reply.
5. User-agent unseals → writes to `user-tokens-{uslug}` Secret → ready.

For non-default hosts: user explicitly accepts grant (Phase 2), then explicitly authorizes (Phase 3).

### Token re-auth

1. User obtains new token (OAuth or paste) → overwrites `mclaude-user-info-{uslug} / tokens.anthropic`.
2. SPA re-wraps new token for each trusted host → delivers via NATS request/reply.
3. User-agents that are online receive, unseal, update the Secret.
4. User-agents that are offline retain old token in Secret. On next SPA visit, user can re-authorize the host (same flow as first-time trust).

### token_wrapped loss (k8s Secret deleted or corrupted)

No special recovery path. User-agent starts without tokens → enters "untrusted" state. SPA shows host as needing authorization. User clicks [Authorize] → same flow as first-time trust.

## Project Provisioning Flow

User-agent handles **project provisioning** (pod creation + secret mounting). Session-agent handles **session lifecycle** (git clone, CLI processes, session state) within the pod. The boundary is the pod spec.

### Project creation (CP = data, user-agent = infrastructure)

Project creation is a two-step flow: CP creates the data (DB row, KV entry), user-agent creates the infrastructure (pod).

1. SPA (or CLI) creates project via CP: `mclaude.users.{uslug}.hosts.{hslug}.api.projects.create` (existing NATS subject, existing handler). CP creates `projects` DB row, writes to `mclaude-projects-{uslug}` KV, returns `{ "id": "<uuid>", "slug": "<pslug>" }`.
2. CP publishes provisioning request to user-agent: `mclaude.users.{uslug}.hosts.{hslug}.agent.projects.provision` with payload `{ "projectId": "<uuid>", "projectSlug": "<pslug>", "gitUrl": "...", "gitIdentityId": "...", "backend": "claude_code" }`.
3. User-agent checks: do I have the token for the requested provider? If no → returns error ("host not trusted" or "token not configured").
4. User-agent creates session pod in user namespace. Pod spec mirrors the existing project pod template (`{release}-session-agent-template` ConfigMap), with one addition for ADR-0091:
   - **Volumes** (existing): `project-{projectId}` PVC at `/data`, `nix-{projectId}` PVC at `/nix`, `user-config` ConfigMap at `/home/node/.claude-seed`, `user-secrets` Secret at `/home/node/.user-secrets` (git credential helpers — `gh-hosts.yml`, `glab-config.yml`, `conn-{id}-*`), `claude-home` emptyDir at `/home/node/.claude`.
   - **Volumes** (added by this ADR): `user-tokens-{uslug}` Secret at `/secrets/tokens/`.
   - **Env vars**: `TOKEN_PROVIDER=anthropic`, `PROJECT_ID=<uuid>`, `PROJECT_SLUG=<pslug>`, `GIT_URL=<url>`, `HOST_SLUG=<hslug>`, `USER_SLUG=<uslug>`, `USER_ID=<uuid>`, `GIT_IDENTITY_ID=<id>`, `NATS_URL=<url>`, `CONTROL_PLANE_URL=<url>`.
   - Session-agent self-bootstraps NATS credentials via challenge-response (existing flow, unchanged).
5. Session-agent starts → reads token from mounted Secret → registers NKey with CP via existing `mclaude.hosts.{hslug}.api.agents.register` → gets scoped NATS JWT → clones repo → ready.

Note: CP is in the project *creation* path (DB write, KV write) but not the *provisioning* path (pod creation). The transplant from CP to user-agent is on `mclaude.users.{uslug}.hosts.{hslug}.agent.projects.provision` — within the user's subject space, not `mclaude.hosts.*`.

### Project deletion

1. SPA (or CLI) deletes project via CP: `mclaude.users.{uslug}.hosts.{hslug}.api.projects.delete` (existing handler). CP removes `projects` DB row and `mclaude-projects-{uslug}` KV entry.
2. CP publishes deletion request to user-agent: `mclaude.users.{uslug}.hosts.{hslug}.agent.projects.delete` with payload `{ "projectId": "<uuid>", "projectSlug": "<pslug>" }`.
3. User-agent deletes the project pod (Deployment) in user namespace.
4. If user-agent is offline when CP publishes: the delete event is missed. Pod persists. v1 accepts this; manual cleanup via `kubectl delete deployment` is the operator escape hatch. Reconcile-on-restart is out of scope (see Deferred).

### Session lifecycle (session-agent = application layer)

Session management within the pod is unchanged from the existing flow:

1. SPA sends session request to session-agent via existing per-project NATS subject: `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.create`.
2. Session-agent starts a CLI process (Claude Code, etc.) with the mounted token.
3. Session-agent writes state to `mclaude-sessions-{uslug}` KV (existing flow). SPA and CP learn about the session via KV watch.

The existing JetStream consumer and per-project NATS subjects are untouched. Session-agent doesn't know or care that user-agent created the pod.

CP is not in the project provisioning path. Token gating is user-agent's responsibility.

**Failure modes:**
- Token not available (host not trusted) → user-agent returns error on project create; SPA shows "Authorize this host".
- User-agent not running → NATS request times out; SPA shows host unavailable.
- Session-agent JWT issuance: unchanged. Session-agent registers via `mclaude.hosts.{hslug}.api.agents.register` → CP issues JWT. Same path as today.

## Component Changes

### mclaude-controller-k8s

- Publishes grants on `mclaude.hosts.{hslug}.grants.create` when the host operator (or automated policy) decides to grant access to a user.
- Generates NKey pair for user-agent → writes seed to `user-agent-key` k8s Secret → registers pubkey with CP via `mclaude.hosts.{hslug}.api.agents.user-agent.register`.
- Creates an **empty** `user-tokens-{uslug}` Secret in the user namespace (no data keys; populated later by user-agent on token delivery). This ensures the user-agent pod's required volume mount resolves on startup — without it, k8s refuses to schedule the pod (mount references nonexistent Secret).
- Copies `{release}-session-agent-template` ConfigMap from `mclaude-system` into the user namespace as `session-agent-template` so the user-agent can read pod-template values from its own namespace (no cross-namespace RBAC). Re-copied on Helm upgrade and user-agent re-rolled.
- Creates user namespace `mclaude-{uslug}` + RBAC (ServiceAccount, namespace-scoped Role granting verbs on `Deployment`, `PersistentVolumeClaim`, `Secret` (`user-tokens-{uslug}` only), `ConfigMap` (`session-agent-template` only) for `mclaude-user-agent`) + deploys user-agent Deployment on grant.
- **Session pod creation removed** — delegated to user-agent.
- Removes all `devOAuthToken` / `oauth-token` writes from user-secrets reconciler paths.
- Removes session pod templates and lifecycle management.

### mclaude-user-agent (NEW component)

- Deployed in user namespace `mclaude-{uslug}` on each host.
- On first startup: reads NKey seed from `user-agent-key` Secret (written by host controller). Generates X25519 keypair → writes X25519 private key to same Secret.
- On subsequent startups: reads NKey seed + X25519 private key from Secret; reads plaintext tokens from `user-tokens-{uslug}` Secret.
- Authenticates with CP via existing challenge-response flow (`POST /api/auth/challenge` + `POST /api/auth/verify`). X25519 pubkey registered during this step. CP issues bootstrap JWT (if grant pending) or full JWT (if grant accepted). Re-runs challenge-response before TTL expiry to renew (same pattern as session-agent refresh).
- If bootstrap JWT: watches `.agent.activate` for signal. On signal, re-runs challenge-response → CP issues full JWT → reconnects with full permissions on `mclaude.users.{uslug}.hosts.{hslug}.agent.>` + `_INBOX.>`. Single auth code path: always challenge-response, JWT reflects current grant status.
- Subscribes to `mclaude.users.{uslug}.hosts.{hslug}.agent.token-delivery` for token_wrapped delivery.
- Unseals token_wrapped with X25519 private key; writes plaintext to `user-tokens-{uslug}` k8s Secret (keyed by provider).
- Subscribes to `mclaude.users.{uslug}.hosts.{hslug}.agent.projects.provision` for project provisioning requests from CP (after CP creates the DB row).
- Subscribes to `mclaude.users.{uslug}.hosts.{hslug}.agent.projects.delete` for project deletion requests from CP.
- Creates session pods (one per project) in user namespace; mounts `user-tokens-{uslug}` Secret; sets `TOKEN_PROVIDER`, `PROJECT_SLUG`, `GIT_URL`, `HOST_SLUG`, `USER_SLUG`, `GIT_IDENTITY_ID` env vars.
- **Event-driven, not reconciling.** k8s is the source of truth for what pods exist. User-agent reacts to provision/delete events; on restart it adopts whatever pods are already in the namespace and does not query an external "expected pods" list. Pods missed during user-agent downtime (e.g. delete event published while user-agent was offline) are not retroactively reconciled — operator can clean up manually if needed. Pods are created as Deployments so k8s self-heals across node failures without user-agent involvement.
- Session lifecycle within the pod is session-agent's responsibility.
- CoCo/Kata upgrade: X25519 private key protected by TEE; token served via attested endpoint instead of Secret mount.

### mclaude-control-plane

- Subscribes to `mclaude.hosts.*.grants.create` — stores grants in DB. Directory, not gate.
- Subscribes to `mclaude.hosts.*.api.agents.user-agent.register` — stores user-agent NKey public key in `user_host_agents` table. Validates `host_grants` row exists. Same pattern as session-agent registration. Host controller vouches for the key.
- On project creation (existing `mclaude.users.*.hosts.*.api.projects.create` handler): after creating DB row + KV entry, publishes provisioning request to user-agent on `mclaude.users.{uslug}.hosts.{hslug}.agent.projects.provision`. Replaces the old transplant to `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.create`.
- On project deletion (existing `mclaude.users.*.hosts.*.api.projects.delete` handler): after removing DB row + KV entry, publishes deletion request to user-agent on `mclaude.users.{uslug}.hosts.{hslug}.agent.projects.delete` with payload `{ "projectId": "<uuid>", "projectSlug": "<pslug>" }`. Best-effort; if user-agent is offline the event is missed and the pod persists until manual cleanup (no retroactive reconcile in v1).
- Existing `POST /api/auth/challenge` + `POST /api/auth/verify` — extended to recognize user-agent NKeys (from `user_host_agents.nkey_public`). New optional field in `VerifyRequest`: `x25519_pub string` (base64-encoded, 32 bytes). Required when the caller is a user-agent (NKey matched in `user_host_agents`); ignored otherwise. On successful verify: stores `x25519_pub` in `user_host_agents.x25519_pub`; issues bootstrap JWT (if grant pending) or full JWT (if grant accepted). `issueJWTForNKey` extended with a user-agent lookup path alongside users, hosts, and session-agents.
- New endpoint: `GET /api/users/{uslug}/grants` — returns pending host grants for the user, including user-agent X25519 pubkey fingerprint per grant. Auth: self (SPA/CLI).
- New endpoint: `POST /api/users/{uslug}/hosts/{hslug}/grant/accept` — body: `{ "x25519_fingerprint": "..." }`. CP verifies fingerprint matches `user_host_agents.x25519_pub`. Match → marks grant accepted, publishes full JWT to `.agent.activate`. Mismatch → 409. Auth: self (SPA/CLI).
- New endpoint: `GET /api/users/{uslug}/hosts/{hslug}/user-agent/pubkey` — returns user-agent's X25519 pubkey from `user_host_agents`. Auth: self (SPA/CLI).
- Project provisioning no longer goes through CP — SPA sends directly to user-agent via NATS. Session-agent JWT issuance unchanged (existing `mclaude.hosts.{hslug}.api.agents.register` path).
- Creates `mclaude-user-info-{uslug}` KV bucket on user creation.
- Removes `devOAuthToken` from all code paths.
- CP's own NATS JWT unchanged (full access, per ADR-0054); scoping deferred to future ADR.

### mclaude-cli

- New command `mclaude auth anthropic`: OAuth or token paste → write token to `mclaude-user-info-{uslug}` KV → deliver token_wrapped to accessible hosts via NATS request/reply.
- New command `mclaude auth anthropic --status` — shows connected/not set.
- New command `mclaude auth anthropic --revoke` — delete token from `mclaude-user-info-{uslug}` KV. Client-side only; host-side cleanup (Secret deletion, JWT revocation, namespace teardown) is deferred to the revocation flow design.

### mclaude-session-agent

`entrypoint.sh` changes:

- **Remove** the legacy `oauth-token` read:
  ```sh
  # DELETE these lines (user-secrets no longer carries oauth-token per ADR-0091):
  [ -f "/home/node/.user-secrets/oauth-token" ] && \
      export CLAUDE_CODE_OAUTH_TOKEN=$(cat /home/node/.user-secrets/oauth-token)
  ```
- **Add** the per-user-tokens read:
  ```sh
  export CLAUDE_CODE_OAUTH_TOKEN=$(cat /secrets/tokens/${TOKEN_PROVIDER})
  ```
  Fails fast (`set -e`) if `/secrets/tokens/${TOKEN_PROVIDER}` is missing or empty — user-agent should never have created the pod without a token; this represents a control-plane bug.
- **Remove** the `--nats-creds` flag from the `exec session-agent` call. The `nats-creds` file is no longer written into `user-secrets` (per ADR-0063, restated by this ADR). Session-agent self-bootstraps NATS credentials via HTTP challenge-response with CP using its own NKey.
- **Add** `--auth-url "${CONTROL_PLANE_URL}"` to the `exec session-agent` call to activate the challenge-response path (`main.go` only takes the self-bootstrap path when `AUTH_URL != ""`).

The final `exec` is:
```sh
exec session-agent \
    --nats-url    "${NATS_URL}" \
    --auth-url    "${CONTROL_PLANE_URL}" \
    --user-id     "${USER_ID}" \
    --project-id  "${PROJECT_ID}" \
    --data-dir    /data \
    --mode        k8s
```

No HTTP call for the token. No user-agent dependency at runtime — token is on disk before the agent starts.

### mclaude-web (SPA)

Mechanism changes only. UX flows specified in ADR-0092.

- On login: hydrate from `mclaude-user-info-{uslug}` KV into session state. On logout: clear in-memory state.
- On token setup: write token to KV, deliver token_wrapped to trusted hosts.
- On host trust: use token from session state, read user-agent X25519 pubkey from CP API, wrap → deliver via NATS request/reply.
- Uses existing `NATSClient.request()` for delivery + `libsodium-wrappers` for sealed box.

### charts/mclaude-cp

- Remove `controlPlane.devOAuthToken` value and env var.
- Add JetStream at-rest encryption: `store_key` stanza in `nats-configmap.yaml`. Key provisioned via k8s Secret or Helm value.

### charts/mclaude-worker

- Remove `controller.config.devOAuthToken` and `DEV_OAUTH_TOKEN` env var.
- Add `{release}-user-agent-template` ConfigMap (rendered into the chart) and the `userAgent.*` knobs that feed it.
- Controller no longer uses `{release}-session-agent-template` for pod creation. The ConfigMap is **retained** in the chart (still rendered into `mclaude-system`) but is now consumed by `mclaude-user-agent` — the controller's per-user reconciler copies it into each user namespace as `session-agent-template`.

### .github/workflows/deploy-main.yml

- Remove `DEV_OAUTH_TOKEN` from Bitwarden fetch step and all `--set` references.

## NATS Permission Model

### User (SPA/CLI) — additions to `UserSubjectPermissions`

`mclaude-user-info-{uslug}` bucket (stream name: `KV_mclaude-user-info-{uslug}`):

```
Pub:
  $KV.mclaude-user-info-{uslug}.*                                    // KV put
  $JS.API.DIRECT.GET.KV_mclaude-user-info-{uslug}.>                  // KV get
  $JS.API.CONSUMER.CREATE.KV_mclaude-user-info-{uslug}.>             // KV watch (consumer create)
  $JS.API.STREAM.INFO.KV_mclaude-user-info-{uslug}                   // stream info (KV init)
  $JS.API.CONSUMER.INFO.KV_mclaude-user-info-{uslug}.*               // consumer info
  $JS.ACK.KV_mclaude-user-info-{uslug}.>                             // ack consumed messages
  $JS.FC.KV_mclaude-user-info-{uslug}.>                              // flow control

Sub:
  $KV.mclaude-user-info-{uslug}.*                                    // KV watch push delivery
  $JS.FC.KV_mclaude-user-info-{uslug}.>                              // flow control
```

Per accessible host (already covered by existing `mclaude.users.{uslug}.hosts.*.>` grant):

```
Pub: mclaude.users.{uslug}.hosts.{hslug}.agent.token-delivery    // send token_wrapped
```

### User-agent — new `UserAgentSubjectPermissions(uslug, hslug)`

```
Pub: mclaude.users.{uslug}.hosts.{hslug}.agent.>
     _INBOX.>
Sub: mclaude.users.{uslug}.hosts.{hslug}.agent.>
     _INBOX.>
```

No JetStream/KV access. User-agent uses only NATS core subjects.

### New function: `IssueUserAgentBootstrapJWT`

```go
func IssueUserAgentBootstrapJWT(publicKey string, uslug, hslug string, accountKP nkeys.KeyPair) (string, error)
```

Issued on challenge-response when grant is pending. Subscribe-only, minimal scope. The `.agent.activate` subject carries only a signal (no JWT) — user-agent re-authenticates via challenge-response on receipt:

```
Pub: _INBOX.>
Sub: mclaude.users.{uslug}.hosts.{hslug}.agent.activate
     _INBOX.>
```

5-minute TTL. User-agent watches `.agent.activate` for signal, then re-authenticates.

### New function: `IssueUserAgentJWT`

```go
func IssueUserAgentJWT(publicKey string, uslug, hslug string, accountKP nkeys.KeyPair) (string, error)
```

Issued after grant acceptance. Full permissions:

```
Pub: mclaude.users.{uslug}.hosts.{hslug}.agent.>
     _INBOX.>
Sub: mclaude.users.{uslug}.hosts.{hslug}.agent.>
     _INBOX.>
```

5-minute TTL (same as host and session-agent JWTs). No JetStream/KV access.

## Data Model

### user_host_agents table (new)

```sql
CREATE TABLE user_host_agents (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    host_slug   TEXT NOT NULL,
    user_slug   TEXT NOT NULL,
    nkey_public TEXT,       -- Ed25519 NKey public key, registered by host controller
    x25519_pub  TEXT,       -- X25519 public key, registered by user-agent via challenge-response
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (host_slug, user_slug)
);
```

One row per (user, host) pair. `nkey_public` set by host controller during Phase 1, step 3 (NKey generation + registration). `x25519_pub` set by user-agent during Phase 1, step 6 (challenge-response authentication). Supports multiple users per host (shared clusters) independently of the grant lifecycle.

### host_grants table (new)

```sql
CREATE TABLE host_grants (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    host_slug   TEXT NOT NULL,
    user_slug   TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'pending',  -- pending, accepted, revoked
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    accepted_at TIMESTAMPTZ,
    UNIQUE (host_slug, user_slug)
);
```

X25519 pubkey is stored in `user_host_agents` (authoritative source), not here. `host_grants` tracks only the grant lifecycle.
```

### hosts table — new column

```sql
ALTER TABLE hosts ADD COLUMN is_default_platform BOOLEAN NOT NULL DEFAULT false;
-- Uniqueness: at most one host can be default. Enforced by partial unique index:
CREATE UNIQUE INDEX idx_hosts_default_platform ON hosts (is_default_platform) WHERE is_default_platform = true;
```

Set during cluster registration (`POST /admin/clusters`) when registering admin passes `"defaultPlatform": true` (JSON field `defaultPlatform`, Go field `DefaultPlatform bool`) in `AdminClusterRegisterRequest`. Admin-only (existing `isAdminMiddleware`). CP checks the unique index; returns 409 if a default platform already exists.

On user creation (`CreateUser`), CP queries `SELECT slug FROM hosts WHERE is_default_platform = true LIMIT 1`. If found, CP publishes a grant on `mclaude.hosts.{hslug}.grants.create` on behalf of that host and auto-accepts it (marks `host_grants` row as `accepted`, issues user-agent JWT immediately when user-agent requests it).

### mclaude-user-info-{uslug} KV bucket (new, per-user)

Created by CP on user creation. User JWT has read/write on all keys. No infrastructure identity has access. JetStream at-rest encryption enabled.

### user-agent-key k8s Secret (in user namespace on host)

| Key | Value |
|-----|-------|
| `nkey-seed` | NKey Ed25519 seed (NATS `S...` encoded string, ~58 chars). Written by host controller at namespace creation. Used for challenge-response auth with CP. |
| `x25519-private` | base64(X25519 private key, 32 bytes). Written by user-agent on first startup. Used for unsealing token_wrapped from SPA. |

### user-tokens-{uslug} k8s Secret (in user namespace on host)

| Key | Value |
|-----|-------|
| `anthropic` | Anthropic OAuth access token (plaintext) |
| `{provider}` | Provider token (plaintext) — future providers |

Written by user-agent after unsealing token_wrapped. Mounted by session pods at `/secrets/tokens/`. Updated on re-auth. Deleted on revoke.

## Security

- Token in `mclaude-user-info-{uslug}` KV is protected by NATS RBAC (user JWT scoped to own bucket). JetStream at-rest encryption (`store_key`) must be enabled in the NATS server config to protect the token on disk. Only the owning user's client-side identities can read it via NATS.
- Token in `user-tokens-{uslug}` k8s Secret is scoped to the user namespace on the host. k8s RBAC limits access to user-agent and session pods in that namespace.
- On the NATS path: token_wrapped is opaque without the user-agent's X25519 private key. Plaintext never transmitted on host-scoped subjects.
- Token is written to KV exclusively by authenticated client-side code (SPA/CLI). No infrastructure party writes the token.
- token_wrapped is delivered via NATS request/reply exclusively by authenticated client-side code. The deliberate delivery requires an authenticated user action (or auto-trust per ADR-0092).
- token_wrapped delivery is transient (NATS core, no persistence).
- CP has full NATS access and can read the token from `mclaude-user-info-{uslug}`. CP is the trust root; this is accepted. Scoping CP's NATS JWT is deferred to a future ADR.
- CP stores user-agent X25519 pubkeys in DB. In v1, CP does not handle the token (SPA wraps and delivers). In the deferred CP-delegate mode (opt-in), CP reads the token from KV to deliver on behalf of offline SPA — accepted since CP can already read the KV.
- Host operator with cluster-admin can read `user-tokens-{uslug}` Secret directly. CoCo/Kata closes this: token served via TEE-attested endpoint instead of Secret mount; Secret eliminated.
- A compromised CP can issue itself any NATS JWT — no mitigation within the current model.

## Key Loss / Recovery

If user-agent restarts: reads X25519 private key from `user-agent-key` Secret + plaintext tokens from `user-tokens-{uslug}` Secret. No user action.

If `user-tokens-{uslug}` Secret is deleted:
1. User-agent enters "untrusted" state.
2. SPA shows host as needing authorization.
3. User clicks [Authorize] → same flow as first-time trust. No special recovery path.

If token is deleted from `mclaude-user-info-{uslug}` (or user revokes):
1. User re-runs OAuth or re-pastes token.
2. New token written to KV.
3. SPA delivers token_wrapped to each trusted host via NATS request/reply.
4. User-agents unseal → update `user-tokens-{uslug}` Secret → ready.

If X25519 private key is lost (`user-agent-key` Secret deleted):
1. User-agent generates new X25519 keypair on next startup → registers new pubkey with CP.
2. User-agent enters "untrusted" state (can't receive new token_wrapped with old key).
3. User clicks [Authorize] → SPA wraps token with new pubkey → delivers.

## Impact

- `docs/mclaude-control-plane/spec-control-plane.md`: add grant directory (subscribe + store), 5 new endpoints (grants, grant accept, user-agent pubkey store/read, user-agent JWT), user-info KV bucket, remove session creation responsibility, remove DEV_OAUTH_TOKEN.
- `docs/mclaude-controller-k8s/spec-k8s-architecture.md`: reduce to namespace + user-agent deployment only, remove session pod creation, remove DEV_OAUTH_TOKEN.
- `docs/mclaude-cli/spec-cli.md`: add `mclaude auth anthropic` command.
- `docs/mclaude-session-agent/spec-session-agent.md`: read token from mounted Secret, remove HTTP call.
- `docs/charts-mclaude/spec-helm.md`: remove devOAuthToken from both charts; add user-agent Deployment template; move session pod template to user-agent.
- `docs/spec-state-schema.md`: add `user_host_agents` table, `host_grants` table, `hosts.is_default_platform` column, `mclaude-user-info-{uslug}` KV bucket, `user-tokens-{uslug}` k8s Secret, `user-agent-key` Secret (with `nkey-seed` + `x25519-private`). Remove `oauth-token` from `user-secrets` Secret.
- `docs/spec-nats-data-taxonomy.md`: add `mclaude-user-info-{uslug}` bucket + key patterns; add `mclaude.hosts.{hslug}.grants.create` subject; add `mclaude.hosts.{hslug}.api.agents.user-agent.register` subject; add `UserAgentSubjectPermissions` + `UserAgentBootstrapSubjectPermissions`; add user JWT KV permissions for `mclaude-user-info-{uslug}`.
- NEW: `docs/mclaude-user-agent/spec-user-agent.md` (new component spec — X25519, token management, session pod lifecycle).

## Scope

**In v1:**
- Bilateral grant/accept handshake: manual host grant, explicit user accept for non-default hosts.
- Default platform: auto-grant all users on creation + auto-accept. User connects token once → immediately ready.
- User-agent per (user, host) namespace with X25519 keypair and session pod lifecycle.
- Claude.ai PKCE OAuth + manual paste in SPA and CLI.
- `mclaude auth anthropic` CLI command.
- `mclaude-user-info-{uslug}` KV bucket (client-only, plaintext token).
- `user-tokens-{uslug}` k8s Secret (written by user-agent, mounted by session pods).
- CP endpoints for grants, user-agent JWT/pubkey.
- SPA wraps token with X25519 and delivers via NATS request/reply when trusting a host.
- Session-agent reads token from mounted Secret.
- Project provisioning: SPA → user-agent directly via NATS (CP not in path). Session lifecycle within pods unchanged.
- Remove `DEV_OAUTH_TOKEN` everywhere.
- Multi-provider: `tokens.{provider}` key pattern in KV and Secret; `TOKEN_PROVIDER` env var on session pod.

**Deferred:**
- **Revocation flow** (separate ADR): user-revoke and host-revoke endpoints/subjects, JWT revocation via `$SYS.REQ.CLAIMS.UPDATE`, token Secret + namespace cleanup, recovery semantics. v1 ships without a self-serve revoke; manual `kubectl delete namespace mclaude-{uslug}` is the operator escape hatch.
- **Pod reconciliation**: no retroactive reconcile of pods missed during user-agent downtime. If a delete event is published while user-agent is offline, the pod persists until manual cleanup. v1 accepts this; reconcile-on-restart is a future addition if drift becomes a real failure mode.
- Policy-driven grants for non-default hosts (host registers auto-grant policy with CP; CP evaluates on user creation).
- User-configurable auto-accept for non-default hosts (opt-in per host or enterprise policy).
- CP-delegated credential delivery (CP wraps and delivers when SPA is offline).
- Silent token refresh.
- UX flows — see ADR-0092.
- CoCo/Kata attestation (TEE-protected X25519 private key; Secret mount replaced by attested endpoint).
- CP NATS JWT scoping.

## Open questions

_(none — all resolved; remaining work tracked under Deferred above)_

## Integration Test Cases

| Test case | What it verifies | Components |
|-----------|-----------------|------------|
| Default platform auto-grant + auto-accept | New user created → CP auto-grants on behalf of default platform → auto-accepts → user-agent gets full JWT | CP, host controller, user-agent |
| Grant → accept → activate | Host grants user access → SPA sees grant → user accepts → CP publishes full JWT → user-agent activates | host controller, CP, SPA, user-agent |
| Bootstrap JWT cannot read agent subjects | User-agent with bootstrap JWT cannot subscribe to `.agent.sessions.create` or `.agent.token-delivery` | NATS, user-agent |
| CLI sets token → SPA delivers to host → session reads from Secret | End-to-end: token in KV → token_wrapped via NATS → user-agent unseals → writes Secret → session pod mounts | CLI, SPA, CP, user-agent, session-agent |
| Second device reads token | Device B reads token from `mclaude-user-info-{uslug}` without re-auth → can deliver to hosts | SPA, NATS KV |
| User-agent pubkey registered | User-agent starts → pubkey stored in CP DB → SPA can read via API | user-agent, CP |
| SPA delivers token_wrapped on host trust | SPA reads pubkey from CP → wraps token → NATS request/reply → user-agent writes Secret | SPA, user-agent, CP, NATS |
| User-agent provisions project pod | SPA creates project via CP → CP publishes to `.agent.projects.provision` → user-agent creates pod with Secret mount + full env | SPA, CP, user-agent |
| User-agent deletes project pod | SPA deletes project via CP → CP publishes to `.agent.projects.delete` → user-agent deletes Deployment | SPA, CP, user-agent |
| User-agent restart adopts existing pods | Restart user-agent pod → existing project Deployments still running → no reconcile, no recreate, sessions continue | user-agent, k8s |
| Session-agent JWT issuance unchanged | Session-agent in user-agent-created pod registers via `mclaude.hosts.{hslug}.api.agents.register` → CP issues JWT | session-agent, CP |
| User-agent cannot read mclaude-user-info | User-agent JWT denied on `mclaude-user-info-{uslug}` subjects | NATS, user-agent |
| Session without token fails | No token in Secret → user-agent returns error on session create | SPA, user-agent |
| User-agent restart recovery | Delete pod → restart → reads tokens from Secret → sessions resume | user-agent, k8s Secret |
| token loss → re-trust | Delete `user-tokens-{uslug}` Secret → restart → untrusted state → user re-authorizes | user-agent, SPA, NATS |
| Revoke clears client token | `mclaude auth anthropic --revoke` deletes token from KV → SPA no longer has token to deliver | CLI, NATS KV |
| Multi-provider | Set tokens.anthropic + tokens.google → sessions for each backend get correct token | SPA, user-agent, session-agent |

## Implementation Plan

| Component | Est. lines | Est. tokens | Notes |
|-----------|-----------|-------------|-------|
| mclaude-user-agent (new) | ~300 lines | 120k | X25519 keygen, NATS token delivery, session pod creation, Secret management |
| mclaude-control-plane | ~200 lines | 110k | Grant subscriber + DB table, 5 endpoints (grants, accept, pubkey store/read, JWT), user-info KV bucket, bootstrap JWT, default platform auto-accept, remove session creation path, remove devOAuthToken |
| mclaude-controller-k8s | ~-100 lines | 70k | Remove session pod creation, keep namespace + user-agent deployment only |
| mclaude-cli | ~100 lines | 70k | `auth anthropic` command, X25519 wrap, NATS request/reply |
| mclaude-session-agent | ~5 lines | 20k | Read from mounted Secret (simpler than before) |
| mclaude-web | ~60 lines | 50k | Token setup, X25519 sealed box, NATS request/reply; UX in ADR-0092 |
| charts (cp + worker) | ~60 lines | 35k | User-agent template, remove session templates from controller, remove devOAuthToken |
| .github/workflows | ~5 lines removed | 10k | Remove DEV_OAUTH_TOKEN |

**Total estimated tokens:** 495k
**Estimated wall-clock:** 5–7h
