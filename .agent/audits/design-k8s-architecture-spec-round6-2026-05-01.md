## Run: 2026-05-01T00:00:00Z

**Gaps found: 2**

1. **`CONTROL_PLANE_URL` injection into session-agent pods is specified ambiguously — "ConfigMap template must include" contradicts the existing code pattern** — The ADR states: "Session-agent ConfigMap template must include `CONTROL_PLANE_URL` env var (passed from the controller's own `CONTROL_PLANE_URL` env) so the session-agent pod can self-bootstrap its NATS JWT via challenge-response at pod startup." The existing `session-agent-template.yaml` ConfigMap (`charts/mclaude-worker/templates/session-agent-template.yaml`) contains only static Helm-rendered values: image, imagePullPolicy, resources, PVC sizes, and corporate CA flags. It does not contain any env var entries. The reconciler at `reconciler.go:313` already injects `NATS_URL` directly from `r.sessionAgentNATSURL` (a controller struct field populated from its own env) — it does not read `NATS_URL` from the ConfigMap. There are two incompatible interpretations of the ADR's instruction: (a) add a `controlPlaneURL` field to the ConfigMap template (populated from `.Values.controlPlane.url` at Helm install time), requiring the reconciler to read it from the ConfigMap before injecting it as a pod env var; or (b) add a `controlPlaneURL` field to the reconciler struct and inject it directly into pod env vars at reconcile time, mirroring the existing `NATS_URL` pattern. The ADR says "ConfigMap template must include" but the code pattern is reconciler-direct injection. A developer implementing the session-agent bootstrap cannot determine which mechanism to use without stopping to ask.
   - **Doc**: "Session-agent ConfigMap template must include `CONTROL_PLANE_URL` env var (passed from the controller's own `CONTROL_PLANE_URL` env)"
   - **Code**: `reconciler.go:313` — `{Name: "NATS_URL", Value: r.sessionAgentNATSURL}` injected directly from struct field, not from ConfigMap; `charts/mclaude-worker/templates/session-agent-template.yaml` — no env var fields in ConfigMap data

2. **`spec-state-schema.md` hosts table lists `nkey_public`, `owner_id`, and `UNIQUE (slug)` — none of these match the live DDL, and the ADR's spec update only corrects lines 409 and 431** — The ADR's Impact section says it will update `spec-state-schema.md` at: (1) line 431 (`"Leafnode"` → `"Client"`) and (2) line 409 (`nkeyPublic` → `nkey_public` NATS payload). But `spec-state-schema.md` has three additional field-level inconsistencies with the live DB schema that are NOT in the ADR's update list: (a) line 64 — schema shows `nkey_public` as the hosts column name; actual DDL at `db.go:853` uses `public_key`; (b) line 60 — schema shows `owner_id` as the FK column; actual DDL at `db.go:844` uses `user_id`; (c) line 61/70 — schema says `UNIQUE NOT NULL` on slug (globally unique); actual DDL at `db.go:858` is `UNIQUE (user_id, slug)` (per-user). The ADR explicitly instructs updating `sys_subscriber.go` to remove the type filter from the `"Client"` branch — the developer will be writing a query against the hosts table and will need to know whether to use `public_key` or `nkey_public`. The Component Changes section in the ADR itself is correct (it says "look up the `hosts` row by `public_key` only"), but the `spec-state-schema.md` it instructs the developer to consult will remain inconsistent with the DDL after this ADR's spec updates are applied. Since `spec-state-schema.md` is listed as authoritative for schemas per project convention, a developer referencing it will find the wrong column name for the `sys_subscriber.go` change.
   - **Doc**: Impact section — "docs/spec-state-schema.md — two updates: (1) line 431 … (2) line 409"; Component Changes — "look up the `hosts` row by `public_key` only"
   - **Code**: `db.go:853` — `public_key TEXT`; `db.go:844` — `user_id TEXT NOT NULL`; `db.go:858` — `UNIQUE (user_id, slug)`; `spec-state-schema.md:64` — `nkey_public TEXT UNIQUE`; `spec-state-schema.md:60` — `owner_id TEXT NOT NULL FK→users`; `spec-state-schema.md:70` — `UNIQUE (slug) — hosts are globally unique by slug`

---

## Run: 2026-05-01T00:01:00Z (round 6 — evaluating current spec state)

ADR: docs/adr-0063-k8s-architecture-spec.md
Status: draft (allowed for plan-feature flow)

Specs checked (current state):
- docs/mclaude-controller-k8s/spec-k8s-architecture.md
- docs/mclaude-controller-local/spec-controller-local.md
- docs/charts-mclaude/spec-helm.md
- docs/spec-state-schema.md

Cross-spec consistency check also examined:
- docs/spec-nats-payload-schema.md
- docs/spec-nats-activity.md
- docs/mclaude-control-plane/spec-control-plane.md

---

### Phase 1 — ADR → Spec Forward Pass

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| 42 | Worker chart fate: Strip + retain as independently installable chart. Drop NATS StatefulSet/ConfigMap/Service, gen-leaf-creds-job, leafCreds/leafUrl values. | spec-helm.md: "no local NATS StatefulSet, no leaf-node credentials"; worker section has no NATS StatefulSet, no gen-leaf-creds-job. | REFLECTED | — | |
| 43 | New spec file path: docs/mclaude-controller-k8s/spec-k8s-architecture.md (component-local) | File exists at that exact path. | REFLECTED | — | |
| 44 | Existing spec-controller.md fate: Delete umbrella. Delete docs/mclaude-controller/spec-controller.md. | File absent from glob output — deleted. | REFLECTED | — | |
| 44 | Migrate Variant 2 (BYOH) content to docs/mclaude-controller-local/spec-controller-local.md | spec-controller-local.md exists with BYOH content. | REFLECTED | — | |
| 45 | spec-helm.md updates: Drop worker NATS, leaf creds, gen-leaf-creds-job; describe mclaude-worker as standalone-installable. | spec-helm.md worker section is hub-direct, no leaf nodes, standalone installable. | REFLECTED | — | |
| 46 | NATS topology: Hub-only. K8s controller connects to hub NATS. No leaf nodes in K8s. | spec-k8s-architecture.md line 5: "The controller connects directly to hub NATS... no local NATS StatefulSet; the worker chart no longer ships a leaf node." | REFLECTED | — | |
| 47 | Multi-cluster K8s: Supported via publicly-exposed hub NATS. | spec-helm.md line 130: "Independently installable into any Kubernetes cluster that can reach the control-plane over HTTPS and hub NATS over WebSocket (port 443)." | REFLECTED | — | |
| 48 | Hub NATS public exposure: Ingress with NATS WebSocket on 443. Operators use nats-wss://nats.mclaude.example:443 as host.hubNatsUrl. | spec-helm.md: Ingress {release}-nats-ws; host.hubNatsUrl with example URL. spec-k8s-architecture.md line 46: same URL example. | REFLECTED | — | |
| 49 | Host JWT cache: In-memory only. Never writes JWT to Secret. Only NKey seed persists at rest. | spec-k8s-architecture.md line 141: "JWT is not stored here — acquired in-memory at boot." spec-helm.md line 153: same. | REFLECTED | — | |
| 50 | Per-user namespace reaper: Deferred to a follow-up ADR. | Deferred — not expected in spec. | REFLECTED | — | |
| 51 | Migration from leaf-NATS worker chart: helm uninstall + helm install cutover. Brief downtime. | spec-helm.md lines 206-214: "Migration from Leaf-NATS Worker Chart" section. "Brief downtime for K8s-hosted projects during cutover. No in-place upgrade path." | REFLECTED | — | |
| 52 | Namespace naming: mclaude-{userSlug} per ADR-0062, userSlug = slugify(full email) | spec-k8s-architecture.md line 91. spec-state-schema.md line 480. | REFLECTED | — | |
| 53 | MCProject CRD location: mclaude-system namespace. Per-user resources in mclaude-{userSlug}. | spec-k8s-architecture.md line 76. spec-state-schema.md line 454. | REFLECTED | — | |
| 54 | Owner references: Project Deployments/PVCs/Secrets carry ownerReferences → MCProject CR via controllerutil.SetControllerReference. | spec-k8s-architecture.md line 84. spec-state-schema.md lines 533, 542, 548. | REFLECTED | — | |
| 69 | gen-host-nkey pre-install Job (weight -10). Generates NKey pair, writes seed to {release}-host-creds, prints public key to Job log + NOTES.txt. Idempotent. | spec-helm.md line 152-153. spec-k8s-architecture.md line 28. | REFLECTED | — | |
| 70-71 | Operator runs mclaude host register --type cluster --name 'us-east' --nkey-public UABC... Publishes mclaude.users.{uslug}.hosts._.register {name, type, nkey_public}. CP slugifies name, creates hosts row by public_key, returns {ok, slug}. | spec-k8s-architecture.md line 40. spec-state-schema.md line 412: {name, type, nkey_public}. | REFLECTED | — | |
| 74 | Both NATS payloads and HTTP bodies use snake_case (nkey_public) — confirmed by lifecycle.go:240 and auth.go:20. spec-state-schema.md:409 corrected from nkeyPublic to nkey_public. | spec-state-schema.md line 412: "{name, type, nkey_public}" — corrected. | REFLECTED | — | Cross-spec gap in spec-nats-payload-schema.md; see Phase 0b. |
| 75 | Controller pod runs HTTP challenge-response (POST /api/auth/challenge + verify), CP returns host JWT scoped to mclaude.hosts.{hslug}.>. | spec-k8s-architecture.md lines 13-20: Boot Sequence. | REFLECTED | — | |
| 79 | Session-agent pod boots, generates NKey, host controller registers agent's public key with CP via mclaude.hosts.{hslug}.api.agents.register. | spec-k8s-architecture.md lines 101-108: Session-Agent Auth section. | REFLECTED | — | |
| 80 | User deletes project. Controller deletes MCProject CR; ownerReferences cascade to Deployment/PVC/Secret. | spec-k8s-architecture.md line 70. | REFLECTED | — | |
| 81 | Operator deregisters K8s host: either mclaude host deregister from workstation, or Helm pre-delete Job using host's JWT — both publish mclaude.users.{uslug}.hosts.{hslug}.manage.deregister. CP soft-deletes host row + adds JWT to revocation list. | spec-k8s-architecture.md: No deregistration section. spec-helm.md: No pre-delete Job documented. | GAP | SPEC→FIX | Neither spec-k8s-architecture.md nor spec-helm.md documents the deregistration flow: the two options (workstation vs. Helm pre-delete Job) and that CP soft-deletes the host row + revokes JWT. A developer implementing the worker chart from spec alone would not know to add a pre-delete Job or document the deregister path. |
| 89 | gen-host-nkey Job: idempotent — skips if Secret exists. | spec-helm.md line 152. spec-k8s-architecture.md line 28. | REFLECTED | — | |
| 90 | Attestation publishes {name, type: 'cluster', nkey_public} with operator's user JWT. | spec-k8s-architecture.md line 40. spec-state-schema.md line 412. | REFLECTED | — | |
| 91-93 | JWT acquisition via challenge-response. JWT refresh background loop before 5-min TTL expiry. Reuses mclaude.io/common/pkg/hostauth. | spec-k8s-architecture.md lines 13-24. spec-controller-local.md line 60. | REFLECTED | — | |
| 94 | Deregister: Helm pre-delete Job using controller's current JWT — publishes mclaude.users.{uslug}.hosts.{hslug}.manage.deregister. CP soft-deletes + revokes. | Same gap as line 81. Neither spec documents the pre-delete Job. | GAP | SPEC→FIX | Duplicate of line 81 gap — same missing deregistration section. |
| 100-102 | API endpoints: POST /api/auth/challenge {nkey_public}, POST /api/auth/verify {nkey_public, challenge, signature}. NATS subject mclaude.users.{uslug}.hosts._.register with {name, type, nkey_public}. | spec-k8s-architecture.md references the endpoints. spec-state-schema.md line 412: correct payload. | REFLECTED | — | |
| 123-128 | Helm chart artifacts: Job, Secret (field nkey_seed), Deployment with env HUB_NATS_URL/CONTROL_PLANE_URL/HOST_NKEY_SEED_PATH, ConfigMap session-agent-template, ClusterRole/ClusterRoleBinding, NOTES.txt. | spec-helm.md lines 152-163. spec-k8s-architecture.md lines 135-148. | REFLECTED | — | |
| 125 | host.name Helm value is rendered into NOTES.txt only — controller decodes actual slug from JWT. | spec-k8s-architecture.md line 21: "do not read host.name from Helm." spec-helm.md line 177: "Display name rendered into NOTES.txt only. Controller derives its actual slug from the JWT." | REFLECTED | — | |
| 133 | sys_subscriber.go: Client branch looks up hosts by public_key with no type filter. Leafnode branch removed. | spec-state-schema.md lines 431-436: SELECT with public_key, no type filter. Leafnode kind removed; unexpected events log warning and drop. | REFLECTED | — | |
| 134 | spec-state-schema.md line 431: cluster host $SYS CONNECT event kind Leafnode → Client. | spec-state-schema.md line 431-436: Client branch only. | REFLECTED | — | |
| 139-146 | Drop env vars NATS_ACCOUNT_SEED, NATS_CREDENTIALS_PATH, JS_DOMAIN, CLUSTER_SLUG. Drop loadAccountKey(), IssueSessionAgentJWT(), accountKP. Add HUB_NATS_URL, CONTROL_PLANE_URL, HOST_NKEY_SEED_PATH. | spec-k8s-architecture.md lines 44-61: Configuration table + Dropped section. | REFLECTED | — | |
| 144 | JWT lifetime: held in memory only, never persisted back to Secret. | spec-k8s-architecture.md line 141. | REFLECTED | — | |
| 145 | Session-agent JWT issuance moves to control-plane. Agent registers NKey via mclaude.hosts.{hslug}.api.agents.register. Controller no longer in JWT-minting path. | spec-k8s-architecture.md lines 101-109. | REFLECTED | — | |
| 146 | reconcileSecrets: Remove IssueSessionAgentJWT()/FormatNATSCredentials(). user-secrets retains only oauth-token. nats-creds field not populated. | spec-k8s-architecture.md line 109. spec-state-schema.md line 556. | REFLECTED | — | |
| 147 | CONTROL_PLANE_URL injection: add controlPlaneURL to reconciler struct. Inject into session-agent pod spec directly. Do NOT add to session-agent-template.yaml ConfigMap. | spec-k8s-architecture.md line 96: "CONTROL_PLANE_URL (injected from the controller's own CONTROL_PLANE_URL env, not from the session-agent-template ConfigMap)." spec-state-schema.md line 550: same. | REFLECTED | — | Previous round gap is now closed — spec now correctly says "not from the session-agent-template ConfigMap." |
| 148-152 | Move host_auth.go to mclaude-common/pkg/hostauth/. Add NewHostAuthFromSeed(seedPath, cpURL, log). Both controllers import mclaude.io/common/pkg/hostauth. | spec-k8s-architecture.md line 14. spec-controller-local.md line 60. | REFLECTED | — | |
| 154-159 | Boot loop: On Refresh() with 404, log NKey not registered, retry 5s interval doubling to 60s cap. Controller waits for JWT before connecting to NATS. | spec-k8s-architecture.md lines 15-20: exact match. | REFLECTED | — | |
| 160 | Host slug derived from JWT (decoded after acquisition), not from Helm value. | spec-k8s-architecture.md line 21. | REFLECTED | — | |
| 161 | Drop sessionAgentNATSURL() derivation function. Session-agents inherit HUB_NATS_URL directly. | spec-k8s-architecture.md line 61. | REFLECTED | — | |
| 162 | Owner references on Deployment, PVC, Secret → MCProject CR via controllerutil.SetControllerReference. | spec-k8s-architecture.md line 84. spec-state-schema.md lines 533, 542, 548. | REFLECTED | — | |
| 169-171 | charts/mclaude-cp: No template changes. NATS WS Ingress already present. Operator sets ingress.natsHost for multi-host deployment. | spec-helm.md lines 76-78, 113. | REFLECTED | — | |
| 174-193 | charts/mclaude-worker: Strip NATS resources, add gen-host-nkey-job + host-creds-secret + NOTES.txt. Add controlPlane.url/host.name/host.hubNatsUrl values. | spec-helm.md worker section: all documented. | REFLECTED | — | |
| 196-197 | docs/mclaude-controller/spec-controller.md: Delete. Folder removed. | File absent from glob output. | REFLECTED | — | |
| 199-203 | docs/charts-mclaude/spec-helm.md: Drop worker NATS subsection, gen-leaf-creds-job, leafCreds config docs. Update single-cluster paragraph. | spec-helm.md: no worker NATS section, no gen-leaf-creds-job, single-cluster section updated. | REFLECTED | — | |
| 208-215 | hosts table legacy constraint CHECK (type = 'machine' OR (js_domain IS NOT NULL...)) must be dropped. Migration drops js_domain, leaf_url, account_jwt. | spec-state-schema.md lines 70-73: "the legacy constraint... was dropped as part of the ADR-0063 migration." | REFLECTED | — | |
| 217 | Slug uniqueness is per-user: UNIQUE (user_id, slug). Host lookup uses public_key. | spec-state-schema.md line 55: "Host slugs are unique per user (UNIQUE (user_id, slug))... Host lookup by CP uses public_key." | REFLECTED | — | |
| 248-253 | spec-state-schema.md five corrections: (1) CONNECT kind Leafnode→Client; (2) nkeyPublic→nkey_public payload; (3) nkey_public→public_key column; (4) owner_id→user_id; (5) UNIQUE(slug)→UNIQUE(user_id,slug) | spec-state-schema.md: (1) line 431-436 ✓; (2) line 412 ✓; (3) line 64 public_key ✓; (4) line 60 user_id ✓; (5) lines 70-71 UNIQUE(user_id,slug) ✓. All five applied. | REFLECTED | — | |
| 219-228 | Error Handling: 8 failure modes including Hub NATS unreachable, namespace creation fails, ownerRef cascade, empty namespace, stale leaf-creds, unregistered operator. | spec-k8s-architecture.md lines 122-134: all 8 cases covered. | REFLECTED | — | |

---

### Phase 0b — Cross-Spec Consistency Check

ADR-0063 line 74 declares: "Both NATS payloads and HTTP bodies use snake_case (nkey_public) — confirmed by lifecycle.go:240 and auth.go:20."

ADR-0063 corrected spec-state-schema.md from `nkeyPublic` to `nkey_public`. Grepping other specs reveals the same concept is still camelCase in three other specs, and the `owner_id` vs `user_id` correction was not propagated either.

**docs/spec-nats-payload-schema.md** — uses `nkeyPublic` throughout:
- Line 922: host register request JSON `"nkeyPublic": "UABC..."`
- Line 938: host register field table `nkeyPublic`
- Line 940: Postgres column named `owner_id` ("creates the host in Postgres (slug, name, type, owner_id, nkey_public)")
- Line 1012: host rekey request JSON `"nkeyPublic": "UNEW..."`
- Line 1344: agent register request JSON `"nkeyPublic": "UABC..."`
- Line 1365: agent register field table `nkeyPublic`
- Line 1402: POST /api/auth/challenge request JSON `"nkeyPublic": "UABC..."`
- Line 1415: challenge field description `nkeyPublic`
- Line 1425: POST /api/auth/verify request JSON `"nkeyPublic": "UABC..."`
- Line 1441: verify field description `nkeyPublic`

**docs/spec-nats-activity.md** — line 191: `owner_id: alice` in Postgres host row creation narrative.

**docs/mclaude-control-plane/spec-control-plane.md**:
- Line 231: `SELECT … FROM hosts WHERE owner_id = ?` (SQL query uses old column name)
- Line 282: `Resolves host_id from (owner_id/host_access, hostSlug)` (old column name)

| Cross-spec concept | Stale spec | Old value | Correct value per ADR-0063 | Verdict | Direction | Notes |
|---|---|---|---|---|---|---|
| Host register NATS payload field | spec-nats-payload-schema.md:922,938 | `nkeyPublic` | `nkey_public` | GAP | SPEC→FIX | ADR-0063 line 74 confirms snake_case from lifecycle.go:240. |
| Agent register NATS payload field | spec-nats-payload-schema.md:1344,1365 | `nkeyPublic` | `nkey_public` | GAP | SPEC→FIX | Same confirmation. |
| HTTP challenge/verify request field | spec-nats-payload-schema.md:1402,1415,1425,1441 | `nkeyPublic` | `nkey_public` | GAP | SPEC→FIX | ADR-0063 line 74 confirms auth.go:20 also uses snake_case. |
| Host rekey NATS payload field | spec-nats-payload-schema.md:1012 | `nkeyPublic` | `nkey_public` | GAP | SPEC→FIX | Same pattern. |
| hosts table FK column (narrative) | spec-nats-payload-schema.md:940 | `owner_id` | `user_id` | GAP | SPEC→FIX | ADR-0063 Impact correction #4: owner_id → user_id (confirmed db.go:844). |
| hosts table FK column (narrative) | spec-nats-activity.md:191 | `owner_id: alice` | `user_id: alice` | GAP | SPEC→FIX | Same correction not propagated. |
| hosts table FK column (SQL query) | spec-control-plane.md:231 | `WHERE owner_id = ?` | `WHERE user_id = ?` | GAP | SPEC→FIX | Same correction not propagated to control-plane spec SQL narrative. |
| hosts table FK column (reference) | spec-control-plane.md:282 | `owner_id/host_access` | `user_id/host_access` | GAP | SPEC→FIX | Same. |

---

### Summary

- Reflected: 43
- Gap: 9 (1 ADR-direct gap + 8 cross-spec inconsistencies)
- Partial: 0

**ADR decision gap (spec-k8s-architecture.md / spec-helm.md):**

GAP [SPEC→FIX]: "Operator deregisters K8s host... Either (a) mclaude host deregister --slug us-east from the workstation... then helm uninstall mclaude-worker; or (b) helm uninstall runs a pre-delete Job that publishes the same deregister subject using the host's own JWT before Helm tears down the controller." Neither spec-k8s-architecture.md nor spec-helm.md documents this deregistration flow. A developer implementing the worker chart from spec alone would not know to add a Helm pre-delete Job, what subject to publish, or that CP soft-deletes the host row and adds the JWT to the revocation list.

**Cross-spec inconsistencies triggered by ADR-0063's snake_case / owner_id corrections:**

GAP [SPEC→FIX]: spec-nats-payload-schema.md still uses `nkeyPublic` (camelCase) for host register (line 922/938), agent register (line 1344/1365), host rekey (line 1012), and HTTP challenge/verify payloads (lines 1402/1415/1425/1441). ADR-0063 line 74 confirms all are snake_case per lifecycle.go:240 and auth.go:20. A developer implementing the CP handler or CLI from spec-nats-payload-schema.md would use the wrong field name.

GAP [SPEC→FIX]: spec-nats-payload-schema.md:940, spec-nats-activity.md:191, and spec-control-plane.md:231,282 still reference `owner_id` as the hosts table FK column. ADR-0063 Impact correction #4 confirms the column is `user_id` (matching db.go:844). A developer writing SQL from any of these specs would use the wrong column name.
