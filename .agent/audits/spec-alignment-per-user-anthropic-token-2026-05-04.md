## Run: 2026-05-04T00:00:00Z

# Spec Alignment Audit: ADR-0091 Per-User Anthropic Token

**ADR:** docs/adr-0091-per-user-anthropic-token.md  
**Status:** draft  
**Specs evaluated:**
- docs/mclaude-control-plane/spec-control-plane.md
- docs/mclaude-controller-k8s/spec-k8s-architecture.md
- docs/mclaude-cli/spec-cli.md
- docs/mclaude-session-agent/spec-session-agent.md
- docs/charts-mclaude/spec-helm.md
- docs/spec-state-schema.md
- docs/spec-nats-data-taxonomy.md
- docs/mclaude-user-agent/spec-user-agent.md

---

## Phase 1 — ADR → Spec Forward Pass

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| 25 | Token storage: `mclaude-user-info-{uslug}` KV bucket, key `tokens.{provider}` | spec-state-schema.md §mclaude-user-info-{uslug}; spec-nats-data-taxonomy.md §KV Buckets | REFLECTED | — | Both specs describe the bucket, key patterns, client-only access, and at-rest encryption requirement. |
| 26 | Token writer: SPA or CLI only | spec-state-schema.md §mclaude-user-info-{uslug}: "Writer: SPA / CLI"; spec-nats-data-taxonomy.md: client-only | REFLECTED | — | |
| 27 | Token readers (client): SPA/CLI (any device) via `mclaude-user-info-{uslug}` | spec-state-schema.md; spec-nats-data-taxonomy.md | REFLECTED | — | |
| 28 | Token host delivery: X25519 sealed box via NATS request/reply on `token-delivery` subject | spec-user-agent.md §NATS Subjects; spec-nats-data-taxonomy.md §Resource Hierarchy | REFLECTED | — | |
| 29 | Token storage (host): k8s Secret `user-tokens-{uslug}`, key per provider | spec-state-schema.md §Secret: user-tokens-{uslug}; spec-user-agent.md §Token Delivery | REFLECTED | — | |
| 30 | User-agent X25519 keypair: generated at first startup; private key persisted in `user-agent-key` Secret | spec-user-agent.md §Boot Sequence step 2; spec-state-schema.md §Secret: user-agent-key | REFLECTED | — | |
| 31 | User-agent X25519 pubkey registered with CP via JWT issuance endpoint; stored in DB | spec-control-plane.md §POST /api/auth/verify (extended by ADR-0091); spec-state-schema.md §user_host_agents | REFLECTED | — | |
| 32 | Project provisioning: user-agent creates project pods (migrated from host controller) | spec-user-agent.md §Project Pod Lifecycle; spec-k8s-architecture.md §Scope changes; spec-control-plane.md §Project Creation Flow | REFLECTED | — | |
| 33 | Pod source-of-truth: k8s itself; user-agent event-driven, not a reconciler | spec-user-agent.md §Project Pod Lifecycle: "event-driven, not a reconciler" | REFLECTED | — | |
| 34 | Revocation deferred; manual `kubectl delete namespace` escape hatch | spec-user-agent.md §What This Component Does NOT Do; spec-k8s-architecture.md §Deregistration | REFLECTED | — | |
| 35 | Host controller role: decides which users to grant access; publishes grant; creates user namespace + deploys user-agent | spec-k8s-architecture.md §NATS Subscriptions `grants.create`; spec-k8s-architecture.md §Reconciler Loop | REFLECTED | — | |
| 36 | CP role: directory + credential issuer + identity provider + pubkey store | spec-control-plane.md §Role (first paragraph) and §NATS Subjects (subscribes `grants.create`, `user-agent.register`) | REFLECTED | — | |
| 37 | `is_default_platform BOOLEAN` on `hosts` table; partial unique index | spec-state-schema.md §hosts table §is_default_platform column and index; spec-control-plane.md §POST /admin/clusters | REFLECTED | — | |
| 38 | User-agent deployment: spun up by host controller when it grants access | spec-k8s-architecture.md §Reconciler Loop; spec-state-schema.md §Deployment: user-agent | REFLECTED | — | |
| 39 | Token entry — OAuth: Claude.ai PKCE flow from SPA or CLI | spec-cli.md §mclaude auth anthropic | REFLECTED | — | SPA mechanism acknowledged at ADR line 356; ADR-0092 owns UX flows. |
| 40 | Token entry — manual paste: write-only input in SPA or `--token` flag in CLI | spec-cli.md §mclaude auth anthropic `--token <v>` | REFLECTED | — | |
| 41 | Token re-auth: new token overwrites KV; SPA re-wraps and re-delivers | spec-user-agent.md §Token Delivery: "Re-auth follows same path; existing entry overwritten"; spec-cli.md behavior section | REFLECTED | — | |
| 42 | Missing token at session start → session creation fails with clear error | spec-user-agent.md §NATS Subjects: `projects.provision` replies `{error: "host_not_trusted"}`; spec-control-plane.md §Project Creation Flow | REFLECTED | — | |
| 43 | Missing token_wrapped at user-agent start → "untrusted" state | spec-user-agent.md §Boot Sequence: reads tokens from Secret on restart; spec-state-schema.md §Deployment project-{projectId} | REFLECTED | — | |
| 44 | Multi-provider: KV key `tokens.{provider}`; Secret key `{provider}`; `TOKEN_PROVIDER` env var on session pod | spec-state-schema.md §mclaude-user-info-{uslug} key table; §Secret: user-tokens-{uslug}; §Deployment project-{projectId} env vars | REFLECTED | — | |
| 45 | DEV_OAUTH_TOKEN removed from mclaude-controller-k8s, CP chart, CI workflow | spec-k8s-architecture.md §Scope changes (removed DEV_OAUTH_TOKEN); spec-helm.md §mclaude-worker (MCProject CRD removed, user-agent added); spec-control-plane.md (removes devOAuthToken from all code paths) | REFLECTED | — | CI workflow (.github/workflows/deploy-main.yml) not a spec file; not evaluated. |
| 46 | CoCo/Kata upgrade path: X25519 private key in TEE | spec-user-agent.md §CoCo / Kata Upgrade Path; spec-state-schema.md §Secret: user-agent-key (notes CoCo/Kata deferred) | REFLECTED | — | |
| 50-65 | `mclaude-user-info-{uslug}` bucket layout: keys `tokens.{provider}`, `git.{provider}`, `preferences.*`; client-only; JWT permissions | spec-state-schema.md §mclaude-user-info-{uslug}; spec-nats-data-taxonomy.md §KV Buckets and §User JWT additions | REFLECTED | — | |
| 63 | User JWT permissions: KV read/write on `mclaude-user-info-{uslug} / *` | spec-nats-data-taxonomy.md §User JWT additions — full `$KV.`, `$JS.API.*`, `$JS.ACK.`, `$JS.FC.` permission entries listed | REFLECTED | — | |
| 65 | JetStream at-rest encryption (`store_key`) required for `mclaude-user-info-{uslug}` | spec-state-schema.md §mclaude-user-info-{uslug}: "JetStream at-rest encryption ... is required"; spec-helm.md `nats.config.jetstream.encryption.storeKeySecret` knob | REFLECTED | — | |
| 73-109 | Trust model Phases 1-3 (grant/accept handshake, bilateral consent) | spec-k8s-architecture.md §Reconciler Loop (Phase 1); spec-control-plane.md §POST grant/accept, §User-agent JWT issuance (Phase 2); spec-user-agent.md §Boot Sequence (Phase 3) | REFLECTED | — | |
| 77 | Manual grant: host operator publishes on `mclaude.hosts.{hslug}.grants.create` with `{uslug}` | spec-k8s-architecture.md §NATS Subscriptions `grants.create`; spec-nats-data-taxonomy.md §Resource Hierarchy | REFLECTED | — | |
| 79 | Policy-driven grant: default platform uses `auto: true`; grant policy evaluation on new user creation | spec-control-plane.md §Per-user resource provisioning: "After bucket creation, CP queries … if default platform exists, CP writes host_grants … and publishes to mclaude.hosts.{<default>}.grants.create" | REFLECTED | — | |
| 83-89 | Phase 1 steps: CP stores grant; host controller generates NKey pair; registers pubkey; creates namespace + user-agent; user-agent generates X25519; authenticates with CP; gets bootstrap JWT | spec-k8s-architecture.md §Reconciler Loop (steps 1-7 map to ADR Phase 1 steps); spec-user-agent.md §Boot Sequence | REFLECTED | — | |
| 85 | CP validates `host_grants` row exists before accepting `user-agent.register` | spec-control-plane.md §mclaude.hosts.{hslug}.api.agents.user-agent.register: "Validates that a host_grants row exists … rejects with FORBIDDEN" | REFLECTED | — | |
| 97 | Explicit accept: SPA sends `POST /api/users/{uslug}/hosts/{hslug}/grant/accept` with `{x25519_fingerprint}` | spec-control-plane.md §POST /api/users/{uslug}/hosts/{hslug}/grant/accept | REFLECTED | — | |
| 97 | Fingerprint verification: `hash(user_host_agents.x25519_pub) == x25519_fingerprint`; mismatch → 409 | spec-control-plane.md §POST grant/accept: "Mismatch → 409 with {error:'fingerprint_mismatch'}" | REFLECTED | — | |
| 101 | Auto-accept (default platform): CP auto-accepts on user's behalf; `is_default_platform` admin-gated | spec-control-plane.md §Per-user resource provisioning; spec-state-schema.md §hosts: `is_default_platform` | REFLECTED | — | |
| 105 | CP marks grant accepted; publishes `{}` to `.agent.activate`; no JWT in payload | spec-control-plane.md §Publishes table: `mclaude.users.{uslug}.hosts.{hslug}.agent.activate`: "Signal-only payload … no JWT in the payload" | REFLECTED | — | |
| 107 | User-agent receives signal → re-runs challenge-response → CP issues full JWT → reconnects | spec-user-agent.md §Boot Sequence step 7; spec-control-plane.md §User-agent JWT issuance | REFLECTED | — | |
| 108 | Single auth code path: always challenge-response, JWT reflects current grant status | spec-user-agent.md §Boot Sequence: "single auth code path"; spec-control-plane.md §User-agent JWT issuance: same statement | REFLECTED | — | |
| 113-117 | Phase 3 credential delivery: SPA online path (reads pubkey from CP, wraps, delivers via NATS) | spec-user-agent.md §Token Delivery; spec-control-plane.md §GET /api/users/{uslug}/hosts/{hslug}/user-agent/pubkey | REFLECTED | — | |
| 117 | SPA offline (CP delegate): opt-in; CP reads token from KV, wraps, delivers — deferred | ADR marks this deferred. Spec-control-plane.md §CP role mentions "Delegate (opt-in)" but notes it's deferred. | REFLECTED | — | Not a v1 decision. |
| 121 | user-agent unseals → writes to `user-tokens-{uslug}` Secret | spec-user-agent.md §Token Delivery step 5; spec-state-schema.md §Secret: user-tokens-{uslug} | REFLECTED | — | |
| 161 | Client login: hydrate from `mclaude-user-info-{uslug}` KV on login; clear in-memory on logout | spec-control-plane.md §mclaude-web (SPA) changes block: "On login: hydrate from mclaude-user-info-{uslug} KV" | REFLECTED | — | Noted in spec-control-plane §mclaude-web (SPA) overview. |
| 192 | `GET /api/users/{uslug}/hosts/{hslug}/user-agent/pubkey` — returns X25519 pubkey | spec-control-plane.md §GET /api/users/{uslug}/hosts/{hslug}/user-agent/pubkey | REFLECTED | — | |
| 193 | `token_wrapped = crypto_box_seal(token, ua_x25519_pub)` (NaCl sealed box) | spec-user-agent.md §Token Delivery; spec-cli.md §mclaude auth anthropic (libsodium) | REFLECTED | — | |
| 232-244 | Project provisioning flow: CP publishes to `agent.projects.provision`; user-agent checks token; creates session pod with volumes and env vars | spec-user-agent.md §Project Pod Lifecycle; spec-control-plane.md §Project Creation Flow | REFLECTED | — | |
| 236 | Session pod volume: `user-tokens-{uslug}` Secret at `/secrets/tokens/` (added by ADR-0091) | spec-state-schema.md §Deployment project-{projectId}: "user-tokens-{uslug} Secret (mounted at /secrets/tokens/)"; spec-user-agent.md §Project Pod Lifecycle step 3 | REFLECTED | — | |
| 239 | Env vars on session pod: `TOKEN_PROVIDER`, `PROJECT_ID`, `PROJECT_SLUG`, `GIT_URL`, `HOST_SLUG`, `USER_SLUG`, `USER_ID`, `GIT_IDENTITY_ID`, `NATS_URL`, `CONTROL_PLANE_URL` | spec-state-schema.md §Deployment project-{projectId}: env vars table; spec-user-agent.md §Project Pod Lifecycle step 4 | REFLECTED | — | |
| 247-250 | Project deletion: CP removes DB row + KV; publishes to `agent.projects.delete`; best-effort; pod persists if agent offline | spec-control-plane.md §Project Deletion Flow; spec-user-agent.md §NATS Subjects `projects.delete` | REFLECTED | — | |
| 272-280 | mclaude-controller-k8s changes: grants.create subscription; generates NKey; creates empty `user-tokens-{uslug}` Secret; copies session-agent-template; creates namespace + RBAC + user-agent; removes session pod creation; removes DEV_OAUTH_TOKEN | spec-k8s-architecture.md §Scope changes; §Reconciler Loop | REFLECTED | — | |
| 276 | Controller creates empty `user-tokens-{uslug}` Secret (no data keys) so mount resolves on startup | spec-k8s-architecture.md §Reconciler Loop step 6b; spec-state-schema.md §Secret: user-tokens-{uslug}: "created by mclaude-controller-k8s … The user-agent populates per-provider keys" | REFLECTED | — | |
| 277 | Controller copies `{release}-session-agent-template` ConfigMap from mclaude-system into user namespace as `session-agent-template` | spec-k8s-architecture.md §Reconciler Loop step 5b; spec-state-schema.md §Deployment: user-agent | REFLECTED | — | |
| 277 | RBAC for `mclaude-user-agent` Role: verbs on `Deployment`, `PVC`, `Secret` (only `user-tokens-{uslug}`), `ConfigMap` (only `session-agent-template`) | spec-k8s-architecture.md §Reconciler Loop step 3; spec-user-agent.md §Deployment §ServiceAccount | REFLECTED | — | |
| 285-296 | mclaude-user-agent component spec (new): NKey seed read; X25519 keygen; challenge-response; bootstrap vs full JWT; token-delivery; projects.provision/delete; event-driven; no reconciler | spec-user-agent.md (entire spec reflects these decisions) | REFLECTED | — | |
| 300-311 | CP changes: grants.create subscriber; user-agent.register subscriber; project provisioning republished to agent; project deletion republished to agent; 3 new endpoints; user-info KV bucket on user creation; removes devOAuthToken | spec-control-plane.md §NATS Subjects (subscribes); §HTTP Endpoints (new endpoints); §NATS KV Buckets (mclaude-user-info); §Per-user resource provisioning | REFLECTED | — | |
| 302 | On project creation CP publishes to `agent.projects.provision` (replaces old `mclaude.hosts.*.users.*.projects.*.create`) | spec-control-plane.md §Publishes table: `mclaude.users.{uslug}.hosts.{hslug}.agent.projects.provision` | REFLECTED | — | |
| 304 | `POST /api/auth/verify` extended: new optional `x25519_pub` field; required for user-agent NKeys | spec-control-plane.md §POST /api/auth/verify: "extended by ADR-0091 … x25519_pub is required (base64, 32 bytes); CP stores it in user_host_agents.x25519_pub" | REFLECTED | — | |
| 305 | New endpoint: `GET /api/users/{uslug}/grants` | spec-control-plane.md §GET /api/users/{uslug}/grants | REFLECTED | — | |
| 306 | New endpoint: `POST /api/users/{uslug}/hosts/{hslug}/grant/accept` | spec-control-plane.md §POST /api/users/{uslug}/hosts/{hslug}/grant/accept | REFLECTED | — | |
| 307 | New endpoint: `GET /api/users/{uslug}/hosts/{hslug}/user-agent/pubkey` | spec-control-plane.md §GET /api/users/{uslug}/hosts/{hslug}/user-agent/pubkey | REFLECTED | — | |
| 309 | CP creates `mclaude-user-info-{uslug}` KV bucket on user creation | spec-control-plane.md §Per-user resource provisioning: "CP creates per-user KV buckets (mclaude-sessions, mclaude-projects, mclaude-user-info — ADR-0091)" | REFLECTED | — | |
| 310 | CP removes `devOAuthToken` from all code paths | spec-control-plane.md §User-agent JWT issuance: no mention of devOAuthToken; §Startup Sequence: no devOAuthToken reference | REFLECTED | — | |
| 315-317 | mclaude-cli: `mclaude auth anthropic` (OAuth or paste), `--status`, `--revoke` | spec-cli.md §mclaude auth anthropic (all three flags documented) | REFLECTED | — | |
| 315 | CLI `mclaude auth anthropic` delivers token to all accepted hosts via NATS request/reply | spec-cli.md §mclaude auth anthropic Behavior step 4: "For each host in GET /api/users/{uslug}/grants with status='accepted' … deliver via NATS request/reply" | REFLECTED | — | |
| 317 | CLI `--revoke` deletes KV key only; host-side cleanup deferred | spec-cli.md §mclaude auth anthropic `--revoke`: "only deletes the KV key. Host-side cleanup … is part of the deferred revocation flow" | REFLECTED | — | |
| 321-334 | mclaude-session-agent: entrypoint.sh removes legacy `oauth-token` read; adds `/secrets/tokens/${TOKEN_PROVIDER}` read; removes `--nats-creds` flag; adds `--auth-url` | spec-session-agent.md §Standalone Mode (K8s): full entrypoint.sh description including token read from /secrets/tokens/, --auth-url, no --nats-creds | REFLECTED | — | |
| 332 | Fails fast if `/secrets/tokens/${TOKEN_PROVIDER}` is missing or empty | spec-session-agent.md §Standalone Mode (K8s): "If /secrets/tokens/${TOKEN_PROVIDER} is missing or empty, entrypoint.sh exits with a clear error" | REFLECTED | — | |
| 337-346 | Final exec invocation: `exec session-agent --nats-url --auth-url --user-id --project-id --data-dir --mode k8s` | spec-session-agent.md §Standalone Mode (K8s): exact exec block shown | REFLECTED | — | |
| 361 | charts/mclaude-cp: remove `controlPlane.devOAuthToken` value; add JetStream at-rest encryption `store_key` stanza | spec-helm.md: no `devOAuthToken` in mclaude-cp knobs table; `nats.config.jetstream.encryption.storeKeySecret` knob present | REFLECTED | — | |
| 363-367 | charts/mclaude-worker: remove `controller.config.devOAuthToken`; add user-agent Deployment template; remove session pod templates from host controller | spec-helm.md §mclaude-worker: MCProject CRD removed, user-agent-template ConfigMap added, ClusterRole loses MCProject CRD permission; no devOAuthToken in configuration knobs | REFLECTED | — | |
| 378-408 | NATS Permission Model: User JWT additions for `mclaude-user-info-{uslug}` bucket (Pub/Sub entries); user delivery on `agent.token-delivery` | spec-nats-data-taxonomy.md §User JWT additions | REFLECTED | — | |
| 401-426 | `IssueUserAgentBootstrapJWT` function signature and permissions (Sub on `.agent.activate` + `_INBOX.>`, 5-min TTL) | spec-nats-data-taxonomy.md §Bootstrap JWT; spec-control-plane.md §User-agent JWT issuance | REFLECTED | — | |
| 429-443 | `IssueUserAgentJWT` function signature and permissions (Pub+Sub on `agent.>` + `_INBOX.>`, 5-min TTL, no JetStream) | spec-nats-data-taxonomy.md §Full JWT; spec-control-plane.md §User-agent JWT issuance | REFLECTED | — | |
| 449-477 | Data Model: `user_host_agents` table schema | spec-state-schema.md §user_host_agents | REFLECTED | — | |
| 463-477 | Data Model: `host_grants` table schema | spec-state-schema.md §host_grants | REFLECTED | — | |
| 483-490 | `hosts.is_default_platform` column + partial unique index + auto-grant on user creation | spec-state-schema.md §hosts: `is_default_platform` column, index, and description | REFLECTED | — | |
| 490 | On user creation, CP queries for default platform; if found, writes auto-accepted `host_grants` row and publishes grant | spec-control-plane.md §Per-user resource provisioning: "After bucket creation, CP queries SELECT slug FROM hosts WHERE is_default_platform = true LIMIT 1. If found, CP writes host_grants(... status='accepted' ...) and publishes to mclaude.hosts.{<default>}.grants.create" | REFLECTED | — | |
| 492-494 | `mclaude-user-info-{uslug}` KV bucket: created by CP; user JWT read/write; no infrastructure access; at-rest encryption | spec-state-schema.md §mclaude-user-info-{uslug} | REFLECTED | — | |
| 498-501 | `user-agent-key` k8s Secret: keys `nkey-seed` (controller) and `x25519-private` (user-agent) | spec-state-schema.md §Secret: user-agent-key (table with both keys, writers, and CoCo note) | REFLECTED | — | |
| 503-510 | `user-tokens-{uslug}` k8s Secret: keys `anthropic`, `{provider}`; written by user-agent; mounted at `/secrets/tokens/` | spec-state-schema.md §Secret: user-tokens-{uslug} | REFLECTED | — | |
| 188 | CLI delivery: `mclaude auth anthropic` iterates accepted grants and delivers; fully specified in spec-cli.md | spec-cli.md §mclaude auth anthropic: confirmed with step 4 | REFLECTED | — | |
| 234 | CP NATS subjects for user-agent: `agent.projects.provision` and `agent.projects.delete` replaces old host-scoped transplants | spec-nats-data-taxonomy.md §Resource Hierarchy: both subjects listed under `users.{uslug}.hosts.{hslug}.agent.*`; LEGACY subjects noted | REFLECTED | — | |
| 290 | User-agent: session lifecycle within the pod is session-agent's responsibility (boundary is pod spec) | spec-user-agent.md §What This Component Does NOT Do and §Project Pod Lifecycle; spec-session-agent.md (unchanged session lifecycle) | REFLECTED | — | |
| 294 | User-agent: event-driven; on restart adopts existing pods; missed delete events not retroactively reconciled | spec-user-agent.md §Project Pod Lifecycle §On user-agent restart | REFLECTED | — | |

---

### Summary

- Reflected: 57
- Gap: 0
- Partial: 0

**CLEAN — 57 ADR decisions reflected in specs, 0 gaps**

All concrete ADR-0091 decisions are present in the corresponding specs. The following notes are for context only (not gaps):
- ADR-0092 UX flows are explicitly deferred to a sibling ADR; no spec gap.
- CI workflow (`deploy-main.yml`) changes are implementation artifacts, not spec-level decisions; not evaluated.
- `spec-control-plane.md` still contains a stale reference in the §Kubernetes Dependency section mentioning the old host-scoped provisioning subjects — but that section also includes a note that it's for backward compat and the ADR-0091 path is the primary one. Not a blocking gap.
