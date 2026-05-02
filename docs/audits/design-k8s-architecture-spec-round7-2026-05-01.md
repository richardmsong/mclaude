## Run: 2026-05-01T00:00:00Z

# Spec Alignment Audit: ADR-0063 (K8s Architecture Spec)

ADR: `docs/adr-0063-k8s-architecture-spec.md`
Specs checked:
- `docs/mclaude-controller-k8s/spec-k8s-architecture.md`
- `docs/mclaude-controller-local/spec-controller-local.md`
- `docs/charts-mclaude/spec-helm.md`
- `docs/spec-state-schema.md`

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| 42 | "Worker chart fate: Strip + retain as independently installable chart…" | spec-helm.md — "mclaude-worker: Independently installable into any Kubernetes cluster…" | REFLECTED | | Chart shape, rationale, and install pattern all present. |
| 43 | "New spec file path: `docs/mclaude-controller-k8s/spec-k8s-architecture.md`" | File exists | REFLECTED | | File is present and populated. |
| 44 | "Existing `spec-controller.md` fate: Split + delete umbrella." | `docs/mclaude-controller/` folder does not exist; `spec-k8s-architecture.md` and `spec-controller-local.md` both exist | REFLECTED | | Migration complete; old folder removed. |
| 45 | "`spec-helm.md` updates: drop worker NATS, drop leaf creds, drop `gen-leaf-creds-job`; describe `mclaude-worker` as standalone-installable" | spec-helm.md — mclaude-worker section has no NATS StatefulSet, no leaf creds, no gen-leaf-creds-job; describes standalone install | REFLECTED | | Worker NATS subsection removed; new shape documented. |
| 46 | "NATS topology in spec: Hub-only. K8s controller connects to hub NATS. No leaf nodes anywhere in K8s." | spec-k8s-architecture.md line 5: "connects directly to hub NATS … no local NATS StatefulSet; the worker chart no longer ships a leaf node." | REFLECTED | | |
| 47 | "Multi-cluster K8s: Supported via publicly-exposed hub NATS" | spec-helm.md — worker chart section shows `nats-wss://nats.mclaude.example:443`; spec-k8s-architecture.md line 160 — "Hub NATS reachable from the worker cluster (via `HUB_NATS_URL` Ingress at 443)" | REFLECTED | | |
| 48 | "Hub NATS public exposure: Ingress with NATS WebSocket on 443. URL `nats-wss://nats.mclaude.example:443`" | spec-helm.md line 77: Ingress `{release}-nats-ws` for NATS WebSocket; spec-k8s-architecture.md line 61 shows example URL | REFLECTED | | |
| 49 | "Host JWT cache: In-memory only. Controller acquires JWT via challenge-response on every boot; never writes JWT to Secret." | spec-k8s-architecture.md line 156: "JWT is **not** stored here — acquired in-memory at boot." spec-helm.md line 167: same. | REFLECTED | | |
| 50 | "Per-user namespace reaper: Deferred to a follow-up ADR." | Not in spec (correctly deferred) | REFLECTED | | Deferred scope; no spec gap expected. |
| 51 | "Migration: `helm uninstall` + `helm install` cutover. Brief downtime." | spec-helm.md lines 219–228: Migration from Leaf-NATS Worker Chart section with helm uninstall/install instructions; "Brief downtime for K8s-hosted projects during cutover." | REFLECTED | | |
| 52 | "Namespace naming: `mclaude-{userSlug}` per ADR-0062" | spec-k8s-architecture.md line 106: "ensures the user namespace `mclaude-{userSlug}` exists"; spec-state-schema.md line 480: "Namespace: `mclaude-{userSlug}`" | REFLECTED | | |
| 53 | "MCProject CRD location: `mclaude-system` namespace on the central cluster." | spec-state-schema.md line 454: "Scope: Namespaced (in `mclaude-system`)" | REFLECTED | | |
| 54 | "Owner references: Project Deployments/PVCs/Secrets must have `ownerReferences` back to the MCProject CR." | spec-k8s-architecture.md line 99: "All Deployments, PVCs, and Secrets materialized per project carry `ownerReferences` back to the `MCProject` CR via `controllerutil.SetControllerReference`." spec-state-schema.md lines 533, 542, 548 confirm for each resource type. | REFLECTED | | |
| 74 | "Both NATS payloads and HTTP bodies use snake_case (`nkey_public`)… `spec-state-schema.md:409` incorrectly lists `nkeyPublic` (camelCase); this ADR's spec update corrects it to `nkey_public`." | spec-state-schema.md line 412: `{name, type, nkey_public}` (snake_case) | REFLECTED | | Field is now snake_case as required. |
| 133 | "After this ADR, K8s cluster controllers connect hub-direct and appear as `'Client'` kind — not `'Leafnode'`." | spec-state-schema.md line 433: CONNECT table — `Client` lookup with "no type filter — matches both `type='machine'` and `type='cluster'`" | REFLECTED | | |
| 133 | "`'Leafnode'` branch should be removed… unexpected `'Leafnode'` events log a warning and are dropped." | spec-state-schema.md line 436: "the `Leafnode` kind is no longer used for host presence tracking… Unexpected `Leafnode` events log a warning and are dropped." | REFLECTED | | |
| 134 | "spec-state-schema.md line 431 — Update cluster host `$SYS` CONNECT event kind from `'Leafnode'` to `'Client'`" | spec-state-schema.md lines 430–436: `$SYS.ACCOUNT.*.CONNECT` table shows `Client` kind only, with note about Leafnode being dropped | REFLECTED | | |
| 142–147 | "Drop env vars: `NATS_ACCOUNT_SEED`, `NATS_CREDENTIALS_PATH`, `JS_DOMAIN`, `CLUSTER_SLUG`" | spec-k8s-architecture.md lines 72–75: "Dropped from prior spec: `NATS_ACCOUNT_SEED`, `NATS_CREDENTIALS_PATH`, `JS_DOMAIN`, `CLUSTER_SLUG`" | REFLECTED | | |
| 142 | "Add env vars: `HUB_NATS_URL`, `CONTROL_PLANE_URL`, `HOST_NKEY_SEED_PATH`" | spec-k8s-architecture.md lines 61–63: Configuration table lists all three as required | REFLECTED | | |
| 143–144 | "Boot sequence: read nkey_seed → challenge-response → host JWT → NATS connect" | spec-k8s-architecture.md lines 13–24: Boot Sequence section steps 1–7 | REFLECTED | | |
| 145 | "JWT lifetime: held in memory only, never persisted back to the Secret." | spec-k8s-architecture.md line 156: same | REFLECTED | | |
| 145 | "Session-agent JWT issuance: moves to control-plane per ADR-0054" | spec-k8s-architecture.md lines 116–124: Session-Agent Auth section; controller no longer mints JWTs | REFLECTED | | |
| 146 | "`reconcileSecrets`: Remove `IssueSessionAgentJWT`/`FormatNATSCredentials` blocks; `user-secrets` Secret retains only `oauth-token`; `nats-creds` field no longer populated." | spec-k8s-architecture.md line 124: "`nats-creds` Secret key is not written by `reconcileSecrets`. `user-secrets` Secret retains only `oauth-token` and OAuth connection entries." | REFLECTED | | |
| 147 | "`CONTROL_PLANE_URL` injection into session-agent pods: add `controlPlaneURL string` field to reconciler struct… inject `CONTROL_PLANE_URL` env var directly from struct field. Do **not** add it to `session-agent-template.yaml` ConfigMap." | spec-k8s-architecture.md line 111: "`CONTROL_PLANE_URL` (injected from the controller's own `CONTROL_PLANE_URL` env, not from the session-agent-template ConfigMap)." | REFLECTED | | |
| 148 | "Move `mclaude-controller-local/host_auth.go` to `mclaude-common/pkg/hostauth/`; add `NewHostAuthFromSeed` constructor" | spec-common.md lines 123–146: Package `hostauth` section with both constructors | REFLECTED | | |
| 160 | "Host slug derived from the JWT itself (decoded after acquisition), not from a Helm value." | spec-k8s-architecture.md line 21: "Decode the host slug (`hslug`) from the received JWT." | REFLECTED | | |
| 160 | "Controller does not read `host.name` for subscription scoping." | spec-k8s-architecture.md line 21: "do not read `host.name` from Helm." | REFLECTED | | |
| 160 | `spec-state-schema.md` line 76 still says: "uses cluster slug as a configured value" | spec-state-schema.md line 76 | GAP | SPEC→FIX | ADR-0063 explicitly decides slug comes from the JWT, not from a configured Helm value. The `Readers:` note on the `hosts` table at line 76 still says "`mclaude-controller-k8s` (subscribes to `mclaude.hosts.{cluster-slug}.>` and uses cluster slug as a configured value)" — this contradicts the ADR-0063 decision. Should read "uses cluster slug decoded from the host JWT." |
| 161 | "Drop `sessionAgentNATSURL()` derivation function. Session-agents inherit `HUB_NATS_URL` directly." | spec-k8s-architecture.md line 76: "`SESSION_AGENT_NATS_URL` / `NATS_URL` — session-agents inherit `HUB_NATS_URL` directly" | REFLECTED | | |
| 162 | "Owner references on materialized resources (Deployment, PVC, Secret) → MCProject CR using `controllerutil.SetControllerReference`." | spec-k8s-architecture.md lines 99, 110–111 | REFLECTED | | |
| 209–215 | "Schema migration required: drop `CHECK (type = 'machine' OR (js_domain IS NOT NULL …))` constraint; drop `js_domain`, `leaf_url`, `account_jwt` columns." | spec-state-schema.md line 73: "Note: the legacy constraint `CHECK (type = 'machine' OR …)` was dropped as part of the ADR-0063 migration (columns `js_domain`, `leaf_url`, `account_jwt` are deprecated per ADR-0054)." | REFLECTED | | |
| 217 | "`UNIQUE (user_id, slug)` — slugs are unique per user, not globally." | spec-state-schema.md lines 70–71: `UNIQUE (user_id, slug)` constraint explicitly stated | REFLECTED | | |
| 217 | "Host lookup uses `public_key` (NKey public key column — globally unique by NKey cryptography)" | spec-state-schema.md line 64: column `public_key` with description "globally unique NKey" | REFLECTED | | |
| 248–253 | "Five corrections to spec-state-schema.md: (1) line 431 Leafnode→Client, (2) line 409 nkeyPublic→nkey_public, (3) line 64 nkey_public→public_key, (4) line 60 owner_id→user_id, (5) lines 61/70 UNIQUE(slug)→UNIQUE(user_id,slug)" | spec-state-schema.md: (1) line 433 Client/no-type-filter ✓; (2) line 412 nkey_public ✓; (3) line 64 public_key ✓; (4) line 60 user_id ✓; (5) lines 70–71 UNIQUE(user_id,slug) ✓ | REFLECTED | | All five corrections applied. |
| 40 (spec-helm.md) | Cross-spec: spec-helm.md line 40 says `{release}-nats-config` uses `resolver: MEMORY` | spec-state-schema.md line 588 says `resolver: nats` (ADR-0054 decision) | GAP | SPEC→FIX | Cross-spec inconsistency (Phase 0b). The `resolver: MEMORY` text in spec-helm.md's hub NATS ConfigMap row contradicts spec-state-schema's `resolver: nats`. Both describe the same NATS ConfigMap. ADR-0063's scope includes updating spec-helm.md to match ADR-0054 reality; this inconsistency was not fixed. spec-helm.md line 40 should read `resolver: nats` to match spec-state-schema.md:588. |

### Summary

- Reflected: 32
- Gap: 2
- Partial: 0
