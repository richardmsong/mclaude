## Run: 2026-05-01T00:00:00Z

ADR: docs/adr-0063-k8s-architecture-spec.md
Status: draft (permitted during plan-feature flow)

Specs checked:
- docs/mclaude-controller-k8s/spec-k8s-architecture.md (new file)
- docs/mclaude-controller-local/spec-controller-local.md (new file)
- docs/mclaude-controller/spec-controller.md (deleted — verified absent)
- docs/charts-mclaude/spec-helm.md
- docs/spec-state-schema.md

Cross-spec checked (Phase 0b):
- docs/mclaude-control-plane/spec-control-plane.md (NOT listed in ADR Impact, but shares the $SYS dispatch and hosts.register concepts)

---

### Phase 0b — Cross-spec consistency check findings

Shared concepts touched by ADR-0063:
- `$SYS` CONNECT event kind for cluster hosts: `"Client"` (not `"Leafnode"`)
- `hosts` table column: `public_key` (not `nkey_public`)
- `hosts` table FK column: `user_id` (not `owner_id`)
- `hosts` UNIQUE constraint: `(user_id, slug)` (not global uniqueness)
- Host register payload field: `nkey_public` (snake_case, not camelCase)
- Hub-direct topology (no leaf nodes anywhere)
- Namespace naming: `mclaude-{userSlug}` per ADR-0062

Cross-spec grep of all spec files for these concepts found two inconsistencies in `docs/mclaude-control-plane/spec-control-plane.md`:

1. **Line 145**: `$SYS CONNECT` handler description still uses the old type-filtered dispatch: `Client → SELECT * FROM hosts WHERE public_key = client.nkey AND type = 'machine'` and `Leafnode → cluster lookup`. ADR-0063 decided: remove type filter (no `AND type` clause at all), drop Leafnode branch. `spec-state-schema.md` correctly reflects the new dispatch (no type filter, Leafnode dropped). `spec-control-plane.md` is inconsistent.

2. **Line 148**: `mclaude.users.*.hosts._.register` handler says CP creates host in Postgres with `owner_id`. ADR-0063 Impact item 4 corrects this to `user_id`. `spec-state-schema.md` uses `user_id` correctly. `spec-control-plane.md` still says `owner_id`.

Also noted (not a direct ADR-0063 decision but stale content exposed by the cross-spec check):
- **Line 204**: `control-plane gen-leaf-creds` subcommand still documented as an active entrypoint. Per ADR-0063 (and ADR-0054), the worker chart drops the gen-leaf-creds-job entirely — `gen-leaf-creds` should be described as removed/deprecated or the spec should note it's superseded. (Not a direct ADR-0063 decision about what the spec must say; the ADR's Component Changes section addresses only `sys_subscriber.go` in the CP. So this is informational, not a gap for this ADR.)

---

### Phase 1 — ADR → Spec Forward Pass

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| 42 | Worker chart fate: Strip + retain as independently installable chart that registers with a remote CP. Drop NATS StatefulSet/ConfigMap/Service, drop gen-leaf-creds-job, drop leafCreds/leafUrl values. | spec-helm.md: Chart mclaude-worker section — "no local NATS StatefulSet; worker chart no longer ships a leaf node" | REFLECTED | — | spec-helm.md explicitly describes mclaude-worker as hub-direct, independently installable with no leaf node |
| 43 | New spec file path: docs/mclaude-controller-k8s/spec-k8s-architecture.md | File exists | REFLECTED | — | File created at correct path |
| 44 | Existing spec-controller.md fate: Split + delete. Migrate K8s content to spec-k8s-architecture.md; migrate Variant 2 (BYOH) to spec-controller-local.md; delete docs/mclaude-controller/spec-controller.md | docs/mclaude-controller/ does not exist; new spec files exist | REFLECTED | — | Old spec deleted, new specs created |
| 45 | spec-helm.md updates: drop worker NATS, drop leaf creds, drop gen-leaf-creds-job; describe mclaude-worker as standalone-installable with a CP URL value | spec-helm.md: mclaude-worker section has controlPlane.url, host.hubNatsUrl values; no leaf creds or NATS StatefulSet described | REFLECTED | — | |
| 46 | NATS topology in spec: Hub-only. K8s controller connects to hub NATS. No leaf nodes anywhere in K8s. | spec-k8s-architecture.md line 5: "The controller connects directly to hub NATS via JWT credential (host-scoped per ADR-0054). There is no local NATS StatefulSet; the worker chart no longer ships a leaf node." | REFLECTED | — | |
| 47 | Multi-cluster K8s supported via publicly-exposed hub NATS. Worker clusters register from any network with reachability to CP's public NATS endpoint. Hub NATS Ingress + TLS termination + JWT auth. | spec-helm.md: "Independently installable into any Kubernetes cluster that can reach the control-plane over HTTPS and hub NATS over WebSocket (port 443)." | REFLECTED | — | |
| 48 | Hub NATS public exposure: Ingress with NATS WebSocket on 443. Single Ingress fronts both SPA and NATS. Operators use nats-wss://nats.mclaude.example:443 as host.hubNatsUrl. | spec-helm.md lines 77-78: Ingress {release}-nats-ws listed. Config knob ingress.natsHost documented. Example shows nats-wss://nats.mclaude.example:443. | REFLECTED | — | |
| 49 | Host JWT cache: In-memory only. Controller acquires JWT via challenge-response on every boot; never writes JWT to Secret. Only NKey seed persists at rest. | spec-k8s-architecture.md line 141: "JWT is not stored here — acquired in-memory at boot." spec-state-schema.md hosts table: nats_jwt column NULL until issued, 5-min TTL; nothing says it persists to the Secret. | REFLECTED | — | |
| 50 | Per-user namespace reaper: Deferred to a follow-up ADR. Empty namespace is cheap. | spec-k8s-architecture.md error handling: "Empty per-user namespace — Acceptable in this ADR." | REFLECTED | — | Correctly deferred |
| 51 | Migration from leaf-NATS worker chart: helm uninstall + helm install cutover. | spec-helm.md Migration from Leaf-NATS Worker Chart section with exact helm commands | REFLECTED | — | |
| 52 | Namespace naming: mclaude-{userSlug} per ADR-0062, where userSlug = slugify(full email) | spec-k8s-architecture.md line 92; spec-state-schema.md line 480 | REFLECTED | — | |
| 53 | MCProject CRD location: mclaude-system namespace on central cluster. Per-user resources in mclaude-{userSlug}. | spec-k8s-architecture.md line 76; spec-state-schema.md line 454 | REFLECTED | — | |
| 54 | Owner references on Deployments/PVCs/Secrets → MCProject CR. In scope. | spec-k8s-architecture.md line 84; spec-state-schema.md PVC/Deployment sections | REFLECTED | — | |
| 62 (User Flow step 1) | helm install mclaude-cp | spec-helm.md Deployment section | REFLECTED | — | |
| 63-68 (User Flow step 2) | helm install mclaude-worker with controlPlane.url, host.name, host.hubNatsUrl; gen-host-nkey Job; host-creds Secret with nkey_seed; NOTES.txt | spec-helm.md Worker Deployment and Pre-Install Job sections | REFLECTED | — | |
| 70 (User Flow step 3) | mclaude host register --type cluster --name "us-east" --nkey-public. Publishes mclaude.users.{uslug}.hosts._.register {name, type, nkey_public}. CP slugifies --name, creates hosts row keyed by public_key, returns {ok, slug}. | spec-k8s-architecture.md Registration Flow; spec-state-schema.md line 412 | REFLECTED | — | |
| 74 | spec-state-schema.md correction: nkeyPublic → nkey_public | spec-state-schema.md line 412: snake_case confirmed | REFLECTED | — | |
| 75-76 (User Flow steps 4-5) | Challenge-response boot; connect to hub NATS with JWT; subscribe mclaude.hosts.{hslug}.> | spec-k8s-architecture.md Boot Sequence steps 3-5 | REFLECTED | — | |
| 78 (User Flow step 7) | Controller materializes MCProject, namespace, Deployment+PVC+Secret with ownerReferences | spec-k8s-architecture.md Reconciler Loop steps 2-7 | REFLECTED | — | |
| 79 (User Flow step 8) | Session-agent self-bootstraps: NKey, register via mclaude.hosts.{hslug}.api.agents.register, challenge-response JWT, hub NATS connect | spec-k8s-architecture.md Session-Agent Auth steps 1-5 | REFLECTED | — | |
| 81 (User Flow step 10) | Deregister: publish mclaude.users.{uslug}.hosts.{hslug}.manage.deregister; CP soft-deletes + revokes | spec-state-schema.md line 416; spec-k8s-architecture.md error handling | REFLECTED | — | |
| 86 | K8s registration flow identical to BYOH; no new API surface | spec-k8s-architecture.md Registration Flow | REFLECTED | — | |
| 89 | gen-host-nkey Job: nkeys.CreateUser(), nkey_seed field, idempotent | spec-helm.md Pre-Install Job table | REFLECTED | — | |
| 90 | Attestation publishes {name, type: 'cluster', nkey_public}; CP slugifies --name, creates row keyed by public_key | spec-k8s-architecture.md + spec-state-schema.md | REFLECTED | — | |
| 93 | JWT refresh background loop, 5-min TTL | spec-k8s-architecture.md Boot Sequence step 7 | REFLECTED | — | |
| 119 | gen-host-nkey Job weight -10, nkeys.CreateUser(), idempotent | spec-helm.md Pre-Install Job | REFLECTED | — | |
| 122 | host-creds Secret: single field nkey_seed; JWT not stored | spec-helm.md + spec-k8s-architecture.md Helm Chart Artifacts | REFLECTED | — | |
| 123 | Deployment mounts host-creds at /etc/mclaude/host-creds/; HUB_NATS_URL, CONTROL_PLANE_URL, HOST_NKEY_SEED_PATH envs | spec-helm.md Controller table | REFLECTED | — | |
| 124 | ConfigMap {release}-session-agent-template | spec-helm.md Session-Agent Template; spec-state-schema.md | REFLECTED | — | |
| 125 | ClusterRole/ClusterRoleBinding for Namespace/Deployment/PVC/Secret/MCProject management | spec-helm.md Controller table | REFLECTED | — | |
| 126 | NOTES.txt post-install instructions; host.name rendered into NOTES.txt only | spec-helm.md + host.name config knob | REFLECTED | — | |
| 133 | Client branch: no type filter on public_key lookup; Leafnode branch removed | spec-state-schema.md lines 433, 436 | REFLECTED | — | |
| 133 | SAME decision in spec-control-plane.md: Client branch must drop AND type='machine'; Leafnode branch must be removed | spec-control-plane.md line 145: STILL says `Client → ... AND type = 'machine'` and `Leafnode → cluster lookup` | GAP | SPEC→FIX | spec-control-plane.md was not updated. A developer implementing sys_subscriber.go from spec-control-plane.md alone would still implement the old type-filtered, Leafnode-branch dispatch. |
| 134 | spec-state-schema.md line 431: "Leafnode" → "Client" for cluster host $SYS CONNECT | spec-state-schema.md: correct | REFLECTED | — | |
| 139-146 | Drop NATS_ACCOUNT_SEED, NATS_CREDENTIALS_PATH, JS_DOMAIN, CLUSTER_SLUG; drop loadAccountKey(), IssueSessionAgentJWT(), accountKP; add HUB_NATS_URL, CONTROL_PLANE_URL, HOST_NKEY_SEED_PATH; hub-direct boot sequence | spec-k8s-architecture.md Configuration + Boot Sequence | REFLECTED | — | |
| 147 | CONTROL_PLANE_URL injected into session-agent pods from reconciler struct field, NOT from session-agent-template ConfigMap | spec-k8s-architecture.md Reconciler Loop step 7; spec-state-schema.md Deployment section | REFLECTED | — | |
| 148-152 | Move host_auth.go to mclaude-common/pkg/hostauth/; add NewHostAuthFromSeed(seedPath, cpURL, log) | spec-k8s-architecture.md Boot Sequence step 2 + Dependencies; spec-controller-local.md Dependencies | REFLECTED | — | |
| 153-159 | Boot retry: 5s initial, exponential backoff, 60s cap; 404 → log registration instruction; no NATS connect until JWT | spec-k8s-architecture.md Boot Sequence step 3; Error Handling CP 404 row | REFLECTED | — | |
| 160 | Host slug decoded from JWT, not Helm host.name; host.name informational only | spec-k8s-architecture.md Boot Sequence step 4; spec-helm.md host.name knob | REFLECTED | — | |
| 161 | Drop sessionAgentNATSURL() derivation; session-agents inherit HUB_NATS_URL directly | spec-k8s-architecture.md Configuration dropped section | REFLECTED | — | |
| 162 | ownerReferences via controllerutil.SetControllerReference on Deployment/PVC/Secret | spec-k8s-architecture.md line 84; Reconciler Loop; spec-state-schema.md | REFLECTED | — | |
| 165-166 | spec-controller-local.md at docs/mclaude-controller-local/ | file exists | REFLECTED | — | |
| 169-171 | charts/mclaude-cp: no template changes; operator sets ingress.natsHost | spec-helm.md Ingress table + ingress.natsHost knob | REFLECTED | — | |
| 174-194 | Worker chart strip + retain: drop NATS resources; add gen-host-nkey-job, host-creds-secret, NOTES.txt, new values | spec-helm.md mclaude-worker section | REFLECTED | — | |
| 209-215 | DROP CHECK constraint; drop js_domain, leaf_url, account_jwt columns | spec-state-schema.md hosts table note line 73 | REFLECTED | — | |
| 217 | UNIQUE (user_id, slug); lookup by public_key | spec-state-schema.md hosts table constraint line 70 | REFLECTED | — | |
| 248-253 (Impact items 1-5) | Five spec-state-schema.md corrections (Leafnode→Client, nkeyPublic→nkey_public, nkey_public column→public_key, owner_id→user_id, global UNIQUE→per-user UNIQUE) | spec-state-schema.md: all five corrections present | REFLECTED | — | |
| 252 (Impact item 4) | spec-state-schema.md line 60: hosts FK column owner_id → user_id | spec-state-schema.md: correct (user_id) | REFLECTED | — | |
| 252 (Impact item 4) SAME decision in spec-control-plane.md | The same owner_id → user_id correction must propagate to spec-control-plane.md | spec-control-plane.md line 148: `mclaude.users.*.hosts._.register` handler says "CP creates host in Postgres (slug, name, type, owner_id, nkey_public)" — still uses owner_id | GAP | SPEC→FIX | spec-control-plane.md was not updated. Consistent with the field name in db.go being user_id (ADR-0063 Impact item 4). |
| 221-228 (Error Handling) | Hub NATS unreachable, namespace fails, ownerRef cascade, empty namespace deferred, stale leaf-creds, operator forgets register, mismatched --name | spec-k8s-architecture.md Error Handling table | REFLECTED | — | |

---

### Phase 2 — Summary

- Reflected: 49
- Gap: 2
- Partial: 0

---

### GAP Items (all SPEC→FIX, spec-control-plane.md not in ADR Impact but touched by the same decisions)

**GAP 1 [SPEC→FIX]**: ADR-0063 line 133 — `"Client" branch: lookup hosts row by public_key with NO type filter (matches both machine and cluster). Remove "Leafnode" branch.`
→ `docs/mclaude-control-plane/spec-control-plane.md` line 145 still describes: `Client → SELECT * FROM hosts WHERE public_key = client.nkey AND type = 'machine'` (type filter present) and `Leafnode → SELECT * FROM hosts WHERE public_key = client.nkey AND type = 'cluster' LIMIT 1, update all slug-matching rows` (Leafnode branch still present).
→ The spec must be updated to: `Client → SELECT * FROM hosts WHERE public_key = client.nkey LIMIT 1 (no type filter)`; Leafnode branch removed (unexpected Leafnode events log a warning and are dropped).
→ `spec-state-schema.md` has already been updated correctly; `spec-control-plane.md` has not.

**GAP 2 [SPEC→FIX]**: ADR-0063 Impact item 4 — `spec-state-schema.md line 60: hosts FK column owner_id → user_id (matching db.go:844)`
→ `docs/mclaude-control-plane/spec-control-plane.md` line 148 still says: `CP creates host in Postgres (slug, name, type, owner_id, nkey_public)` — still uses the stale `owner_id` column name.
→ Must be updated to `user_id` to match the actual DDL column name (`db.go:844`) and `spec-state-schema.md`.
