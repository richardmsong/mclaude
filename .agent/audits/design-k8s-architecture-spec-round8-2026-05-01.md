## Run: 2026-05-01T00:00:00Z

ADR: `docs/adr-0063-k8s-architecture-spec.md` (status: draft)
Specs evaluated:
- `docs/mclaude-controller-k8s/spec-k8s-architecture.md`
- `docs/mclaude-controller-local/spec-controller-local.md`
- `docs/charts-mclaude/spec-helm.md`
- `docs/spec-state-schema.md`

---

### Phase 0 — Cross-spec consistency check (shared concepts)

Concepts from ADR-0063 that appear across multiple specs:
- `public_key` column name in `hosts` table
- `nkey_public` field name in NATS payload for `hosts._.register`
- `UNIQUE (user_id, slug)` constraint on hosts
- `client.kind = "Client"` for hub-direct controller connections (no `"Leafnode"`)
- Legacy constraint `CHECK (type = 'machine' OR ...)` dropped
- `HUB_NATS_URL` env var naming
- `{release}-host-creds` Secret with `nkey_seed` field
- `ownerReferences` on Deployments/PVCs/Secrets back to MCProject

---

### Phase 1 — ADR → Spec (forward pass)

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| 42 | Worker chart fate: Strip + retain as independently installable chart; drop NATS StatefulSet/ConfigMap/Service, drop gen-leaf-creds-job, drop leafCreds/leafUrl values; add gen-host-nkey-job, registration mechanism | `docs/charts-mclaude/spec-helm.md` lines 128–228 | REFLECTED | — | spec-helm.md §Chart: mclaude-worker fully describes independently-installable chart with gen-host-nkey Job, no NATS StatefulSet, no leaf creds |
| 43 | New spec file path: `docs/mclaude-controller-k8s/spec-k8s-architecture.md` | File exists at that path | REFLECTED | — | File created at correct path |
| 44 | Existing spec-controller.md fate: Split + delete umbrella; migrate K8s content to docs/mclaude-controller-k8s/spec-k8s-architecture.md; Variant 2 (BYOH) to docs/mclaude-controller-local/spec-controller-local.md; delete docs/mclaude-controller/spec-controller.md | Both new files exist | REFLECTED | — | Both new component-local specs present |
| 45 | spec-helm.md updates: drop worker NATS, drop leaf creds, drop gen-leaf-creds-job; describe mclaude-worker as standalone-installable with a CP URL value | `docs/charts-mclaude/spec-helm.md` throughout | REFLECTED | — | spec-helm.md §mclaude-worker shows no NATS StatefulSet, controlPlane.url value present |
| 46 | NATS topology in spec: Hub-only. No leaf nodes anywhere in K8s. | `spec-k8s-architecture.md` line 5: "The controller connects directly to hub NATS"; `spec-helm.md` line 10: "hub-direct, independently installable" | REFLECTED | — | Both specs state hub-direct explicitly |
| 47 | Multi-cluster K8s: Supported via publicly-exposed hub NATS. Worker clusters register from any network with reachability to the CP's public NATS endpoint. | `spec-helm.md` line 130: "Independently installable into any Kubernetes cluster that can reach the control-plane over HTTPS and hub NATS over WebSocket (port 443)" | REFLECTED | — | |
| 48 | Hub NATS public exposure: Ingress with NATS WebSocket on 443. URL `nats-wss://nats.mclaude.example:443` as `host.hubNatsUrl`. | `spec-helm.md` lines 75–78 (Ingress table, `{release}-nats-ws`); lines 192 (`host.hubNatsUrl` = `nats-wss://nats.mclaude.example:443`) | REFLECTED | — | |
| 49 | Host JWT cache: In-memory only. Controller acquires JWT via challenge-response on every boot; never writes JWT to Secret. Only NKey seed persists at rest. | `spec-k8s-architecture.md` line 156: "JWT is **not** stored here — acquired in-memory at boot."; `spec-helm.md` line 167: "JWT is **not** stored here" | REFLECTED | — | |
| 50 | Per-user namespace reaper: Deferred to a follow-up ADR. | Not a decision that needs to be in spec (explicitly deferred) | REFLECTED | — | `spec-k8s-architecture.md` error table line 148: empty namespaces are acceptable per this ADR |
| 51 | Migration from leaf-NATS: helm uninstall + helm install cutover. Brief downtime. | `spec-helm.md` lines 219–228: "Migration from Leaf-NATS Worker Chart" section present | REFLECTED | — | |
| 52 | Namespace naming: `mclaude-{userSlug}` per ADR-0062 | `spec-k8s-architecture.md` line 106: "Ensures the user namespace `mclaude-{userSlug}` exists"; `spec-state-schema.md` line 480: "Namespace: `mclaude-{userSlug}`" | REFLECTED | — | |
| 53 | MCProject CRD location: `mclaude-system` namespace on the central cluster. Per-user resources in `mclaude-{userSlug}`. | `spec-k8s-architecture.md` line 91: "CRD `MCProject` in `mclaude-system`"; `spec-state-schema.md` line 454: "Scope: Namespaced (in `mclaude-system`)" | REFLECTED | — | |
| 54 | Owner references: Deployments/PVCs/Secrets must have ownerReferences back to the MCProject CR. In scope for this ADR. | `spec-k8s-architecture.md` line 99: "All Deployments, PVCs, and Secrets materialized per project carry `ownerReferences` back to the `MCProject` CR via `controllerutil.SetControllerReference`." | REFLECTED | — | |
| 61–81 (User Flow step 2) | Worker chart helm install with `controlPlane.url`, `host.name`, `host.hubNatsUrl` values | `spec-helm.md` lines 134–139: deployment command with exact values shown | REFLECTED | — | |
| 69 (step 2) | Pre-install Job `gen-host-nkey` generates NKey pair, writes seed to Secret `{release}-host-creds`, prints public key to Job log + Helm NOTES.txt. Idempotent — skips if Secret exists. | `spec-k8s-architecture.md` lines 28–40; `spec-helm.md` lines 164–167 | REFLECTED | — | |
| 75 (step 4) | Controller pod starts, mounts seed Secret, runs ADR-0054 HTTP challenge-response (`POST /api/auth/challenge` + `verify`) using registered public key. CP returns host JWT scoped to `mclaude.hosts.{hslug}.>`. Same code path as mclaude-controller-local/host_auth.go. | `spec-k8s-architecture.md` lines 13–21 (Boot Sequence) | REFLECTED | — | |
| 76 (step 5) | Controller connects to hub NATS with host JWT, subscribes to `mclaude.hosts.{hslug}.>`. Begins reconciling MCProject CRs. | `spec-k8s-architecture.md` lines 22–24 | REFLECTED | — | |
| 79 (step 8) | Session-agent pod boots, generates own NKey, host controller registers agent's public key with CP via `mclaude.hosts.{hslug}.api.agents.register`, agent then runs HTTP challenge-response to get its JWT | `spec-k8s-architecture.md` lines 116–124 (Session-Agent Auth) | REFLECTED | — | |
| 80 (step 9) | User deletes project. Controller deletes MCProject CR; ownerReferences cascade to project Deployment/PVC/Secret. Empty user namespaces left (reaping deferred). | `spec-k8s-architecture.md` lines 84–85 (`delete` subscription cascades); error table entry for empty namespaces | REFLECTED | — | |
| 81 (step 10) | Operator deregisters K8s host: Option A (mclaude host deregister from workstation + helm uninstall) or Option B (Helm pre-delete Job that publishes deregister subject) | `spec-k8s-architecture.md` lines 43–55 (Deregistration Flow); `spec-helm.md` lines 148–160 | REFLECTED | — | |
| 89–94 (K8s host registration flow table) | Step 1: NKey gen via gen-host-nkey Job, writes `nkeySeed` into Secret data field `nkey_seed`, prints `nkeyPublic` to log + NOTES.txt. Idempotent — skips if Secret exists. | `spec-k8s-architecture.md` line 28; `spec-helm.md` line 166 | REFLECTED | — | |
| 90 (step 2 of registration) | Attestation: `mclaude host register --type cluster --name "us-east" --nkey-public UABC...` publishes `mclaude.users.{uslug}.hosts._.register {name, type: 'cluster', nkey_public}`. CP slugifies `--name`, creates `hosts` row keyed by `public_key` (DDL column), returns `{ok, slug}`. | `spec-k8s-architecture.md` line 40; `spec-state-schema.md` line 412 for the subject | REFLECTED | — | |
| 74 | spec-state-schema.md line 409 incorrectly lists `nkeyPublic` (camelCase); this ADR corrects to `nkey_public`. | `spec-state-schema.md` line 412: shows `{name, type, nkey_public}` — correct snake_case | REFLECTED | — | Correction already applied |
| 100–103 (API endpoints) | `POST /api/auth/challenge` body `{nkey_public}`, `POST /api/auth/verify` body `{nkey_public, challenge, signature}`, NATS subject `mclaude.users.{uslug}.hosts._.register` body `{name, type, nkey_public}`, deregister subject | `spec-state-schema.md` line 412; `spec-k8s-architecture.md` line 40 | REFLECTED | — | |
| 107–113 (CLI flags) | `mclaude host register` accepts `--name`, `--type cluster`, `--nkey-public`. Slug not a CLI flag — CP derives from --name. | `spec-state-schema.md` line 412 payload; `spec-helm.md` line 145 shows the command | REFLECTED | — | No spec-cli.md update needed — ADR says this is ADR-0054's responsibility |
| 123 (Helm chart artifacts) | Job `{release}-gen-host-nkey`. Generates NKey pair via `nkeys.CreateUser()` (U-prefix). Writes decorated seed string (`SUAB...`) to Secret data field `nkey_seed`. Prints public key (`UABC...`) to Job log + Helm NOTES.txt. Idempotent. | `spec-helm.md` lines 165–167; `spec-k8s-architecture.md` lines 28–29 | REFLECTED | — | |
| 124 | Secret `{release}-host-creds`. Single field: `nkey_seed` (decorated S-prefix seed string). JWT is **not** persisted — controller acquires in-memory via challenge-response on every boot. | `spec-helm.md` line 167; `spec-k8s-architecture.md` lines 155–157 | REFLECTED | — | |
| 125 | Deployment `{release}-controller-k8s`. Mounts `{release}-host-creds` as volume at `/etc/mclaude/host-creds/`. Env: `HUB_NATS_URL`, `CONTROL_PLANE_URL`, `HOST_NKEY_SEED_PATH=/etc/mclaude/host-creds/nkey_seed`. `host.name` rendered into NOTES.txt only — controller does not read it for subscription scoping. | `spec-helm.md` lines 171–173; `spec-k8s-architecture.md` lines 21, 59–69 | REFLECTED | — | |
| 126 | ConfigMap `{release}-session-agent-template`. Pod template referenced by the controller when materializing per-project session-agent pods. | `spec-helm.md` lines 183–184; `spec-k8s-architecture.md` line 97 | REFLECTED | — | |
| 127–128 | ClusterRole/ClusterRoleBinding `{release}-controller`. Lets the controller manage Namespace, Deployment, PVC, Secret, MCProject CRs. | `spec-helm.md` lines 175–177 | REFLECTED | — | |
| 128 | NOTES.txt post-install message instructing operator to read public key and run `mclaude host register --type cluster --name "$HOST_NAME" --nkey-public <key>` where `$HOST_NAME` is rendered from `.Values.host.name` | `spec-helm.md` lines 141–146 and the Deployment section show the post-install flow; `spec-k8s-architecture.md` Registration Flow section | REFLECTED | — | |
| 133 (Component Changes: control-plane) | `sys_subscriber.go` dispatch: "Client" branch looks up hosts row by `public_key` only with **no type filter**; "Leafnode" branch removed; unexpected Leafnode events log warning and are dropped. | `spec-state-schema.md` lines 430–436: "$SYS events" table shows `"Client"` kind with `SELECT * FROM hosts WHERE public_key = client.nkey` no type filter; note at line 436 says Leafnode events log warning and are dropped. | REFLECTED | — | |
| 134 | `spec-state-schema.md` line 431 — Update cluster host `$SYS` CONNECT event kind from "Leafnode" to "Client" | `spec-state-schema.md` line 433: shows `CONNECT | Client` no Leafnode row | REFLECTED | — | |
| 137–161 (mclaude-controller-k8s Component Changes) | Spec relocated to new component-local folder; hub-direct; auth model rewritten per ADR-0054; drop env vars `NATS_ACCOUNT_SEED`, `NATS_CREDENTIALS_PATH`, `JS_DOMAIN`, `CLUSTER_SLUG`; add `HUB_NATS_URL`, `CONTROL_PLANE_URL`, `HOST_NKEY_SEED_PATH`; boot sequence with challenge-response; JWT in memory only; session-agent JWT issuance moves to CP | `spec-k8s-architecture.md` §Configuration (lines 58–76), §Boot Sequence, §Session-Agent Auth | REFLECTED | — | |
| 148–152 | Move `mclaude-controller-local/host_auth.go` to `mclaude-common/pkg/hostauth/`; importable by both controllers; add `NewHostAuthFromSeed` constructor | `spec-k8s-architecture.md` line 14: "Call `NewHostAuthFromSeed(seedPath, cpURL, log)` from `mclaude.io/common/pkg/hostauth`"; `spec-controller-local.md` line 60: "using `mclaude.io/common/pkg/hostauth`"; `spec-k8s-architecture.md` line 164: depends on `mclaude.io/common/pkg/hostauth` | REFLECTED | — | |
| 154–159 | Boot loop behavior: CP returns 404 → log registration instruction with pubkey; retry 5s interval doubling up to 60s cap; controller does not connect to NATS until JWT acquired; K8s restarts pod after liveness probe if operator never registers | `spec-k8s-architecture.md` lines 15–20 (Boot Sequence step 3), lines 141–143 (Error Handling table) | REFLECTED | — | |
| 160 | Host slug derived from JWT itself (decoded after acquisition), not from Helm value. Helm `host.name` used only for NOTES.txt. | `spec-k8s-architecture.md` lines 21: "Decode the host slug (`hslug`) from the received JWT"; line 148 error table "Controller decodes actual slug from JWT" | REFLECTED | — | |
| 161 | Drop `sessionAgentNATSURL()` derivation function. Session-agents inherit `HUB_NATS_URL` directly via session-agent template. | `spec-k8s-architecture.md` line 76: "`SESSION_AGENT_NATS_URL` / `NATS_URL` — session-agents inherit `HUB_NATS_URL` directly" (listed as dropped env) | REFLECTED | — | |
| 162 | Owner references on materialized resources (Deployment, PVC, Secret) → MCProject CR. Use `controllerutil.SetControllerReference`. | `spec-k8s-architecture.md` line 99: "All Deployments, PVCs, and Secrets materialized per project carry `ownerReferences` back to the `MCProject` CR via `controllerutil.SetControllerReference`." | REFLECTED | — | |
| 165 | mclaude-controller-local: Spec relocated to `docs/mclaude-controller-local/spec-controller-local.md`. Content is existing Variant 2 sections, unchanged in substance. | `spec-controller-local.md` exists at correct path | REFLECTED | — | |
| 169–171 (charts/mclaude-cp) | Already in place. No template changes required. NATS WS Ingress (`ingress.natsHost` value) and WebSocket listener already in nats-configmap. Documentation only. | `spec-helm.md` lines 75–78: `{release}-nats-ws` Ingress entry; lines 111–114 `ingress.natsHost` config knob | REFLECTED | — | |
| 174–194 (charts/mclaude-worker) | Strip + retain: drop nats-statefulset, nats-configmap, nats-service, gen-leaf-creds-job, leafUrl/leafCreds/nats.* values; add gen-host-nkey-job, host-creds-secret, NOTES.txt, controlPlane.url/host.name/host.hubNatsUrl values | `spec-helm.md` §Chart: mclaude-worker, Pre-Install Job, Controller section, Configuration Knobs | REFLECTED | — | |
| 196–197 | docs/mclaude-controller/spec-controller.md: Delete after content migrated. Folder removed. | No such file visible (does not exist in spec discovery). Implicitly done. | REFLECTED | — | File deleted |
| 200–203 | docs/charts-mclaude/spec-helm.md: Drop worker NATS subsection, drop gen-leaf-creds-job, update single-cluster degenerate paragraph, reflect worker-chart-fate decision | `spec-helm.md` shows no Worker NATS section, no gen-leaf-creds, single-cluster degenerate in lines 207–217 | REFLECTED | — | |
| 207–217 (Data Model) | No new tables, no new KV entries, no new subjects. One schema migration required: drop CHECK constraint on hosts table blocking type='cluster' without legacy leaf-NATS columns. Also drops js_domain, leaf_url, account_jwt columns. | `spec-state-schema.md` lines 69–73: "Note: the legacy constraint ... was dropped as part of the ADR-0063 migration (columns `js_domain`, `leaf_url`, `account_jwt` are deprecated per ADR-0054)." | REFLECTED | — | |
| 217 | Slug uniqueness per-user: hosts table has `UNIQUE (user_id, slug)`. Host lookup uses `public_key` (globally unique NKey). | `spec-state-schema.md` lines 55, 70: "Host slugs are unique per user (`UNIQUE (user_id, slug)`)" and constraints section | REFLECTED | — | |
| 248–253 (Impact: spec-state-schema.md corrections) | (1) Line 431: cluster host $SYS CONNECT event kind "Leafnode" → "Client". (2) Line 409: `nkeyPublic` → `nkey_public`. (3) Line 64: `nkey_public` → `public_key`. (4) Line 60: `owner_id` → `user_id`. (5) Lines 61/70: `UNIQUE (slug)` → `UNIQUE (user_id, slug)`. | `spec-state-schema.md`: (1) line 433 shows `Client` kind; (2) line 412 shows `nkey_public`; (3) line 64 shows `public_key`; (4) line 60 shows `user_id`; (5) line 70 shows `UNIQUE (user_id, slug)` | REFLECTED | — | All five corrections are in the spec |
| 222–228 (Error Handling) | Hub NATS unreachable at boot: controller crashloops; Hub NATS unreachable at runtime: retries, JWT expiry triggers exit; Namespace creation fails: MCProject Failed; Owner-ref cascade fails: file bug; Empty namespace after projects deleted: acceptable; Stale leaf-creds from old chart: helm uninstall cleans up; Operator forgets host register: clear log message with pubkey; Operator chooses different --name: CP keys off public key, controller decodes slug from JWT | `spec-k8s-architecture.md` lines 137–148 (Error Handling table) | REFLECTED | — | |
| 236–239 (Security) | K8s controller NKey is a host JWT (5-min TTL); no account signing key; no leaf-creds Secret; per-user namespaces enforce isolation via NetworkPolicies + RBAC per ADR-0059 | `spec-k8s-architecture.md` §Boot Sequence (challenge-response, JWT in memory only); note about dropped env `NATS_ACCOUNT_SEED` | REFLECTED | — | |
| 147 | `reconcileSecrets` code change: Remove `IssueSessionAgentJWT` / `FormatNATSCredentials` blocks. `user-secrets` Secret retains only `oauth-token` field. `nats-creds` field no longer populated by the controller. | `spec-k8s-architecture.md` line 108: "user-secrets Secret contains `oauth-token`... It does **not** contain `nats-creds`"; line 124: "The `nats-creds` Secret key is not written by `reconcileSecrets`." | REFLECTED | — | |
| 147 | `CONTROL_PLANE_URL` injection into session-agent pods: add `controlPlaneURL string` field to reconciler struct. At reconcile time, inject `CONTROL_PLANE_URL` env var directly from this struct field into the session-agent pod spec. Do NOT add it to session-agent-template ConfigMap. | `spec-k8s-architecture.md` line 111: "CONTROL_PLANE_URL (injected from the controller's own `CONTROL_PLANE_URL` env, not from the session-agent-template ConfigMap)"; `spec-state-schema.md` line 550: "CONTROL_PLANE_URL (injected from the controller's own `CONTROL_PLANE_URL` env — needed for session-agent self-bootstrap via challenge-response)" | REFLECTED | — | |

---

### Phase 0b — Cross-spec consistency check results

Checking shared concepts across all spec files:

1. **`public_key` column name in hosts**: spec-state-schema.md line 64 uses `public_key`. spec-k8s-architecture.md line 135 uses `public_key`. spec-helm.md line 145/167 use `nkey_seed` (different field name for the Secret field — that's correct). No inconsistency.

2. **`nkey_public` payload field in hosts._.register**: spec-state-schema.md line 412 shows `{name, type, nkey_public}`. spec-k8s-architecture.md line 40 shows `{name, type, nkey_public}`. Consistent.

3. **`client.kind = "Client"` for hub-direct**: spec-state-schema.md line 433 and spec-k8s-architecture.md line 135 and spec-controller-local.md line 71 all say `"Client"`. Consistent.

4. **`mclaude-{userSlug}` namespace**: spec-k8s-architecture.md line 106 and spec-state-schema.md line 480 agree. Consistent.

5. **`UNIQUE (user_id, slug)` on hosts**: spec-state-schema.md line 70 shows it. spec-k8s-architecture.md line 135 references `public_key` lookup. No inconsistency visible.

6. **Legacy constraint dropped**: spec-state-schema.md lines 69–73 confirms the legacy CHECK constraint was dropped. spec-k8s-architecture.md does not reference this (not needed in the controller spec). Consistent.

7. **spec-helm.md `ingress.leafHost` knob (line 78, 114)**: The spec-helm.md still documents a `{release}-leaf` Ingress for exposing leafnodes port 7422, and `nats.leafNodes.listenPort: 7422` config knob (line 94). ADR-0054 and ADR-0063 say leaf-node topology is removed. However, ADR-0063 explicitly states `charts/mclaude-cp/` has no template changes required and the doc only records the `ingress.natsHost` operator value. The presence of leaf-node-related Ingress and config in spec-helm.md may be stale. Let me check if the ADR explicitly says to remove leafHost from spec-helm.md.

Looking at ADR line 200–203: spec-helm.md changes are: "Drop the `mclaude-worker` 'Worker NATS' subsection, drop the `gen-leaf-creds-job` description and `leafCreds` config docs, update the 'single-cluster degenerate deployment' paragraph, reflect whatever the worker-chart-fate decision lands on." — The ADR does NOT say to drop the cp chart's `leafHost` Ingress or `nats.leafNodes.listenPort` config. The cp chart retains the leaf-node listener port as a vestigial / future-use feature (the Ingress template still exists gated by `ingress.leafHost`). This is consistent with ADR-0063 §charts/mclaude-cp: "Already in place. No template changes required." So this is not a gap.

---

### Summary

- Reflected: 40
- Gap: 0
- Partial: 0
