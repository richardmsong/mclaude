# Spec: mclaude-user-agent

Per ADR-0091. The user-agent is a per-(user, host) Kubernetes Deployment that owns two responsibilities for one user inside one host:

1. **Token custodian.** Receives `crypto_box_seal`-wrapped Anthropic OAuth tokens (and future provider tokens) from authenticated client-side code (SPA/CLI) over NATS request/reply, unseals them with its X25519 private key, and writes plaintext to a per-user k8s Secret that project pods mount.
2. **Project pod lifecycle manager.** Reacts to NATS provision/delete events from CP and creates/deletes per-project `Deployment`s in the user namespace. Event-driven; no CRD; no reconciler — k8s itself is the source of truth for what pods exist.

The user-agent is the trust root within its (user, host) scope. After the bilateral grant/accept handshake completes, all subsequent operations between the SPA and the user namespace flow through the user-agent's NATS subjects, not through CP.

## Deployment

One Deployment per user namespace `mclaude-{uslug}` on each cluster host.

- **Created by:** `mclaude-controller-k8s` when the host grants the user access (Phase 1 of the bilateral handshake — `mclaude.hosts.{hslug}.grants.create`).
- **Replicas:** 1.
- **Strategy:** `RollingUpdate`. Project pods are unaffected by a user-agent restart (k8s is the source of truth).
- **Volumes:**
  - `user-agent-key` Secret (mounted at `/secrets/agent/`)
  - `user-tokens-{uslug}` Secret (mounted at `/secrets/tokens/`)
- **Env:** `USER_SLUG`, `HOST_SLUG`, `NAMESPACE` (= `mclaude-{uslug}`), `NATS_URL` (= `HUB_NATS_URL`), `CONTROL_PLANE_URL`.
- **ServiceAccount:** `mclaude-user-agent`. Role (namespace-scoped to `mclaude-{uslug}`):
  - `get/list/watch/create/update/patch/delete` on `Deployment`
  - `get/list/watch/create/update/patch/delete` on `PersistentVolumeClaim`
  - `get/list/create/update/patch` on `Secret` (only `user-tokens-{uslug}`)
  - `get/list/watch` on `ConfigMap` (only the locally-copied `session-agent-template`; see below)
  No ClusterRole / cross-namespace permissions. The pod-template ConfigMap is copied into the user namespace by `mclaude-controller-k8s` at provisioning time so the user-agent reads from its own namespace only.

## Boot Sequence

1. Read `nkey-seed` from `/secrets/agent/nkey-seed`. Fatal exit if missing — the controller did not write it (control-plane bug or RBAC misconfiguration).
2. Read `x25519-private` from `/secrets/agent/x25519-private`. If absent: generate a new X25519 keypair (libsodium `crypto_box_keypair`), write the private key to the Secret via the k8s API, hold the public key in memory. (Subsequent restarts skip generation.)
3. Authenticate to control-plane via challenge-response:
   - `POST /api/auth/challenge {nkey_public}` → receives nonce.
   - Sign nonce with NKey seed.
   - `POST /api/auth/verify {nkey_public, challenge, signature, x25519_pub}` → CP stores `x25519_pub` in `user_host_agents.x25519_pub` and returns either a bootstrap or full JWT depending on `host_grants.status`.
4. Connect to hub NATS at `NATS_URL` with the issued JWT.
5. Subscribe to all four agent subjects on every connect (`activate`, `token-delivery`, `projects.provision`, `projects.delete`). NATS RBAC enforces scope: with a bootstrap JWT, non-`activate` subscriptions are rejected and logged as warnings; with a full JWT, all four succeed. This single subscription path simplifies the lifecycle — the JWT scope is what gates which subjects deliver, not branching client-side logic.
6. Background loop: re-run challenge-response before TTL expiry (5-minute JWT TTL — same refresh pattern as session-agent's `agentJWTRefreshBuffer`). Each renewal returns whatever JWT matches current grant status.
7. On receipt of `.agent.activate` (signal-only payload, no JWT): re-run challenge-response to obtain the full JWT (since `host_grants.status='accepted'` now), reconnect with full permissions, re-subscribe to `mclaude.users.{uslug}.hosts.{hslug}.agent.>`.

The single auth code path means the user-agent never has separate credentials for the activation signal vs. normal operation — the activation channel just tells it "your grant is now accepted, re-auth and you'll get the bigger JWT."

## NATS Subjects

| Subject | Direction | Behavior |
|---------|-----------|----------|
| `mclaude.users.{uslug}.hosts.{hslug}.agent.activate` | Sub | Signal-only payload (`{}` or `{"status":"accepted"}`). Triggers re-auth; no token material. |
| `mclaude.users.{uslug}.hosts.{hslug}.agent.token-delivery` | Sub (request/reply) | Payload: `{provider: "anthropic", token_wrapped: "<base64>"}`. User-agent calls `crypto_box_seal_open(token_wrapped, x25519_priv)` and writes the plaintext to `user-tokens-{uslug}` Secret under key `{provider}`. Replies `{ok: true}` (or `{ok: false, error: "decrypt_failed"}` if seal-open fails). |
| `mclaude.users.{uslug}.hosts.{hslug}.agent.projects.provision` | Sub (request/reply) | Payload: `{projectId, projectSlug, gitUrl, gitIdentityId, backend}`. User-agent verifies a token exists for the requested provider; if not, replies `{error: "host_not_trusted"}` (CP surfaces this to the SPA as "Authorize this host"). Otherwise creates the project Deployment in the user namespace. Replies `{ok: true, status: "provisioning"}`. |
| `mclaude.users.{uslug}.hosts.{hslug}.agent.projects.delete` | Sub | Payload: `{projectId, projectSlug}`. Best-effort delete of the project Deployment. No reply expected. If user-agent is offline when CP publishes, the event is missed and the Deployment persists — manual `kubectl delete` is the operator escape hatch (see ADR-0091 Deferred). |

The user-agent has **no JetStream / KV access** at any point. It cannot read `mclaude-user-info-{uslug}` (client-only bucket) or any other bucket. All state it touches is in k8s (Deployments + the two Secrets).

## Project Pod Lifecycle

Per ADR-0091, the user-agent is **event-driven, not a reconciler**. There is no internal model of "what pods should exist" — k8s itself is the source of truth.

**On `agent.projects.provision`:**
1. Verify `/secrets/tokens/{provider}` is non-empty.
2. Build the Deployment using the `session-agent-template` ConfigMap **in the user's own namespace** (a copy of `mclaude-system/{release}-session-agent-template` written by `mclaude-controller-k8s` at provisioning time). User-agent does not read from `mclaude-system`.
3. Mount the full set of volumes the project pod requires (per `docs/spec-state-schema.md` Deployment `project-{projectId}`):
   - `project-{projectId}` PVC at `/data`
   - `nix-{projectId}` PVC at `/nix`
   - `user-config` ConfigMap at `/home/node/.claude-seed`
   - `user-secrets` Secret at `/home/node/.user-secrets` (git credential helpers — `gh-hosts.yml`, `glab-config.yml`, `conn-{id}-*`; required by session-agent for `git worktree`, `gh auth setup-git`, etc.)
   - `user-tokens-{uslug}` Secret at `/secrets/tokens/` (model-provider tokens)
   - `claude-home` emptyDir at `/home/node/.claude`
4. Set env `TOKEN_PROVIDER`, `PROJECT_ID`, `PROJECT_SLUG`, `GIT_URL`, `HOST_SLUG`, `USER_SLUG`, `USER_ID`, `GIT_IDENTITY_ID`, `NATS_URL`, `CONTROL_PLANE_URL`.
5. Create per-project PVCs (`project-{projectId}` and `nix-{projectId}`) using sizes from the template.
6. Apply the Deployment with `mclaude/managed=true` label.

The user-agent does **not** mint session-agent NKeys or write `agent-nkey-{projectId}` Secrets. Session-agent self-bootstrap is unchanged: agent generates its own NKey, registers via `mclaude.hosts.{hslug}.api.agents.register`, performs HTTP challenge-response with CP, gets per-project JWT.

**On `agent.projects.delete`:**
1. `kubectl delete deployment project-{projectId}` in the user namespace.
2. PVC cleanup is **omitted in v1** — `project-{projectId}` and `nix-{projectId}` PVCs persist after the Deployment is deleted. Operators reclaim storage with `kubectl delete pvc` manually. Tracked alongside the deferred revocation flow (ADR-0091).

**On user-agent restart:**
1. Existing Deployments in the namespace continue running (k8s self-heals via Deployment replicas regardless of user-agent).
2. User-agent does **not** query CP for "expected projects" or reconcile against KV. There is no expected-state lookup; the running Deployments are reality.
3. Project pods missed during user-agent downtime (e.g. delete events published while user-agent was offline) are **not** retroactively reconciled. Manual cleanup if needed.

## Token Delivery

When the SPA or CLI delivers a token:

1. SPA reads the user-agent's X25519 pubkey via `GET /api/users/{uslug}/hosts/{hslug}/user-agent/pubkey`.
2. SPA computes `token_wrapped = base64(crypto_box_seal(token, x25519_pub))` (NaCl sealed box; libsodium-wrappers in the browser).
3. SPA sends NATS request to `mclaude.users.{uslug}.hosts.{hslug}.agent.token-delivery` with `{provider: "anthropic", token_wrapped}`.
4. User-agent receives → `crypto_box_seal_open(token_wrapped, x25519_priv, x25519_pub)` → plaintext token.
5. User-agent writes plaintext to k8s Secret `user-tokens-{uslug}` at key `{provider}` using the k8s API. The Secret already exists (created empty by `mclaude-controller-k8s` at namespace-provisioning time) — user-agent only patches per-provider data keys.
6. User-agent replies `{ok: true}`.

Re-auth (token rotation) follows the same path; the existing `user-tokens-{uslug}` Secret entry is overwritten.

If `crypto_box_seal_open` fails, the user-agent replies `{ok: false, error: "decrypt_failed"}`. This indicates the SPA wrapped the token with a stale or wrong X25519 pubkey — SPA should re-fetch the pubkey from CP and retry.

## CoCo / Kata Upgrade Path

Per ADR-0091 (deferred). The X25519 private key currently lives in `user-agent-key` Secret in the user namespace; a host operator with cluster-admin can read it. The CoCo / Kata path:

1. User-agent runs in a TEE (Intel TDX, AMD SEV-SNP, Kata-Containers + AttestationAgent).
2. Before reading any private material, user-agent submits an attestation report to a Key Broker Service (KBS); KBS verifies code identity and releases the X25519 private key only to the attested workload.
3. Token serving to project pods uses an attested in-memory endpoint instead of the `/secrets/tokens/` Secret mount.

In this mode the `user-agent-key` Secret's `x25519-private` field is eliminated and the `user-tokens-{uslug}` Secret is replaced by the in-memory endpoint.

## Error Handling

| Failure | Behavior |
|---------|----------|
| `nkey-seed` missing in Secret | Fatal exit — controller did not provision correctly. |
| Challenge-response with CP fails (network, NKey not registered) | Retry with exponential backoff (cap 60s). User-agent never enters Ready until JWT acquired. |
| `crypto_box_seal_open` fails | Reply `{ok: false, error: "decrypt_failed"}` to SPA; do not write Secret. SPA prompts user to retry. |
| Project provision when no token in `/secrets/tokens/{provider}` | Reply `{error: "host_not_trusted"}`. CP surfaces "Authorize this host" to the SPA. |
| `kubectl create deployment` fails (RBAC, quota, image pull) | Reply with `ProvisionReply{Error: "<machine-readable code>: <human message>"}`. The `OK` field uses `omitempty` — it is present (`true`) on success and omitted on failure. Consumers must check for non-empty `error` to detect failure. Do not retry — let SPA re-issue. Pod creation is idempotent (same name) so a retry overwrites cleanly. |
| Project delete when Deployment doesn't exist | Treat as success (idempotent). |
| User-agent JWT TTL expires before refresh | NATS connection drops; on reconnect, JWT is re-acquired via challenge-response. Same as session-agent refresh pattern. |

## Dependencies

- Hub NATS reachable from the worker cluster.
- Control-plane HTTP reachable for challenge-response.
- Kubernetes API for `Deployment`, `Secret`, `PVC` operations within the user namespace.
- libsodium (or equivalent X25519 sealed-box implementation) in the user-agent binary.
- `session-agent-template` ConfigMap **in the user's own namespace** (copied from `mclaude-system/{release}-session-agent-template` by `mclaude-controller-k8s` at provisioning time). User-agent does not read from `mclaude-system`. If the per-namespace copy is absent, user-agent falls back to compiled-in defaults documented in the Project Pod Defaults section below.

## Project Pod Defaults

When fields in `session-agent-template` are absent, the user-agent applies the following compiled-in defaults to project Deployments and PVCs:

| Field | Default |
|-------|---------|
| `image` | `mclaude-session-agent:latest` |
| `imagePullPolicy` | `IfNotPresent` |
| `terminationGracePeriodSeconds` | `30` |
| `resourcesJson` | `{requests: {cpu: 200m, memory: 64Mi}, limits: {cpu: 2000m, memory: 2Gi}}` |
| `projectPvcSize` | `10Gi` |
| `projectPvcStorageClass` | (empty — uses cluster default) |
| `nixPvcSize` | `10Gi` |
| `nixPvcStorageClass` | (empty — uses cluster default) |

The defaults exist for tests and degraded-mode operation. Production clusters should always have a populated `session-agent-template` ConfigMap.

## What This Component Does NOT Do

- It does **not** subscribe to `mclaude.hosts.{hslug}.>` — that's the host controller's scope.
- It does **not** read `mclaude-user-info-{uslug}` or any other KV bucket — its JWT has no JetStream access.
- It does **not** mint session-agent NKeys — session-agents continue to self-bootstrap via `mclaude.hosts.{hslug}.api.agents.register` and HTTP challenge-response.
- It does **not** reconcile from an external "expected pods" list — k8s is the source of truth.
- It does **not** handle revocation — the v1 escape hatch is `kubectl delete namespace mclaude-{uslug}`. Self-serve revoke is deferred to a follow-up ADR.
