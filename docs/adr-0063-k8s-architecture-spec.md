# ADR: Kubernetes Architecture Spec — Consolidate, Drop Worker NATS, Reflect ADR-0054 Reality

**Status**: accepted
**Status history**:
- 2026-05-01: draft
- 2026-05-01: accepted — paired with docs/mclaude-controller-k8s/spec-k8s-architecture.md (new), docs/mclaude-controller-local/spec-controller-local.md (new), docs/charts-mclaude/spec-helm.md (worker section rewrite), docs/spec-state-schema.md (5 DDL corrections, PVC ownerRefs, SYS dispatch, constraint drop note), docs/mclaude-control-plane/spec-control-plane.md (gen-leaf-creds removed, broken refs fixed, SYS dispatch updated), docs/mclaude-common/spec-common.md (pkg/hostauth section), docs/spec-nats-payload-schema.md (nkey_public snake_case, user_id column), docs/spec-nats-activity.md (user_id column)

## Overview

The project has no single component spec describing the K8s side of the architecture (controller, MCProject reconciliation, namespace derivation, Helm topology). The closest documents — `docs/mclaude-controller/spec-controller.md` and `docs/charts-mclaude/spec-helm.md` — predate ADR-0054 and ADR-0058 and still describe a leaf-node NATS topology that was explicitly removed in ADR-0054. The K8s controller code now lives in `mclaude-controller-k8s/` (separate from `mclaude-controller-local/`) but there is no `docs/mclaude-controller-k8s/` folder to match.

### Relationship to prior ADRs

| ADR | Status here |
|-----|-------------|
| ADR-0011 (multi-cluster) | Topology section already partially superseded by ADR-0035; this ADR finishes the supersession by making `mclaude-worker` an independently-installable, hub-direct chart |
| ADR-0035 (unified host architecture) | Subject scheme + `hosts` table preserved. The `mclaude cluster register` CLI subcommand and `POST /admin/clusters` endpoint described in ADR-0035 are **superseded** by the unified `mclaude host register --type cluster` flow per ADR-0054 § "Host registration" |
| ADR-0054 (NATS perm tightening) | Source of truth for hub-direct topology, host-scoped subjects, HTTP challenge-response credential protocol, and the unified `mclaude host register` flow for both machine and cluster hosts. ADR-0063 is the K8s packaging of these decisions |
| ADR-0058 (BYOH redesign) | Established the controller-k8s / controller-local code split; this ADR aligns the docs/folders accordingly |
| ADR-0059 (K8s multi-tenant security) | NetworkPolicy + RBAC isolation for per-user namespaces stays as specified |
| ADR-0061 (ADR-0054 migration gaps) | Sibling effort; this ADR closes the K8s slice |
| ADR-0062 (slug derivation + namespace naming) | Namespace = `mclaude-{slugify(full email)}`; this ADR documents how the controller materializes that |

### Host type model

Per ADR-0035 schema, `hosts.type ∈ {'machine', 'cluster'}`. K8s hosts use `type='cluster'`. No new enum value introduced.

Multi-user access to a single K8s cluster (admin registers, grants other users) follows whatever ADR-0054's deferred host-access design lands on. ADR-0063 does not invent a new access model — it inherits whatever's in place. For now, per ADR-0054 § "Host access model", the registering user owns the host.

This ADR (a) authors a new component spec at `docs/mclaude-controller-k8s/spec-k8s-architecture.md` describing the current and intended K8s topology, (b) updates `docs/mclaude-controller/spec-controller.md` and `docs/charts-mclaude/spec-helm.md` to match ADR-0054 (no worker NATS, no leaf creds, hub-direct), and (c) decides the fate of the `charts/mclaude-worker/` chart now that worker NATS is gone.

## Motivation

1. **Spec drift.** ADR-0054 decided "all agents (BYOH and K8s) connect directly to hub NATS — leaf-node topology removed from scope." The implementation has not caught up: `charts/mclaude-worker/` still deploys `mclaude-worker-nats-0` as a leaf node with a `gen-leaf-creds-job` pre-install hook. The live cluster confirms (`mclaude-worker-nats-0` 3d19h old). The spec docs still describe this topology as current.
2. **Controller dir mismatch.** ADR-0058 split the unified controller into `mclaude-controller-k8s/` and `mclaude-controller-local/`. The corresponding spec lives in `docs/mclaude-controller/spec-controller.md` (singular folder, "Variant 1 / Variant 2" sections). The mismatch confuses both humans and the implementation-evaluator agent (which expects `docs/<component>/spec-*.md`).
3. **No top-level K8s architecture document.** A reader who wants to understand "how does mclaude run on Kubernetes" has to assemble the picture from ADR-0011, ADR-0035, ADR-0054, ADR-0058, ADR-0059, ADR-0061, ADR-0062, and two stale spec files. There is no concise present-tense description of the topology.
4. **Recent spec→impl gap surfaced this in production.** During the 2026-05-01 deploy (factory session `ff873949`), the user repeatedly asked "why do I see so many nats instances?" — answer: spec drift. ADR-0061 was created to catch ADR-0054 migration gaps; this ADR closes the K8s slice of that work.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Worker chart fate | Strip + retain as **independently installable** chart that registers with a remote CP | Worker K8s clusters are treated as "BYOH at cluster scale" — installable into any K8s cluster by an admin, configured with a `controlPlane.url` value, registers itself with CP at install time. Drop NATS StatefulSet/ConfigMap/Service, drop `gen-leaf-creds-job`, drop `leafCreds`/`leafUrl` values. Add a registration mechanism (see new "K8s host registration flow" section). |
| New spec file path | `docs/mclaude-controller-k8s/spec-k8s-architecture.md` (component-local) | Matches the actual code-dir layout post-ADR-0058. |
| Existing `spec-controller.md` fate | **Split + delete umbrella.** Migrate K8s content to `docs/mclaude-controller-k8s/spec-k8s-architecture.md`; migrate Variant 2 (BYOH) content to `docs/mclaude-controller-local/spec-controller-local.md`; delete `docs/mclaude-controller/spec-controller.md` and remove the empty `docs/mclaude-controller/` folder. | Cleanest mapping to code dirs. Future agents searching `docs/<component>/spec-*.md` find what they expect. |
| `spec-helm.md` updates | Update in place — drop worker NATS, drop leaf creds, drop `gen-leaf-creds-job`; describe `mclaude-worker` as standalone-installable with a CP URL value | The Helm spec is cross-cutting (covers both charts) and remains a single doc. |
| NATS topology in spec | Hub-only. K8s controller connects to hub NATS. Session-agent pods connect to hub NATS (already does post-ADR-0061). No leaf nodes anywhere in K8s. | ADR-0054 is the source of truth. Spec must match. |
| Multi-cluster K8s | **Supported via publicly-exposed hub NATS.** Worker clusters register from any network with reachability to the CP's public NATS endpoint. Hub NATS Ingress + TLS termination + JWT auth (existing trust chain) is the canonical path. | Aligns with the "worker chart installs anywhere and points at CP" model. Tailscale/relay become deploy-flavor variations on top, not the primary path. |
| Hub NATS public exposure | **Ingress with NATS WebSocket on 443.** Single Ingress fronts both the SPA and NATS (NATS supports WebSocket transport natively). Reuses the existing cert-manager / Let's Encrypt pipeline. Operators consume the URL `nats-wss://nats.mclaude.example:443` as their `host.hubNatsUrl`. | Easiest firewall story (only 443 outbound from workers). Single TLS pipeline. Marginal latency overhead vs raw TCP is acceptable for control-plane traffic. |
| Host JWT cache | **In-memory only.** Controller acquires JWT via challenge-response on every boot; never writes JWT to Secret. Only NKey seed persists at rest. | Smallest persistent secret surface. CP reachability is a runtime requirement anyway (controller can't reconcile without it). Trades a single CP-outage-at-boot crashloop for a smaller blast radius if Secret leaks. |
| Per-user namespace reaper | **Deferred to a follow-up ADR.** Owner references on MCProject children already handle the pod/PVC cleanup that motivated today's manual `kubectl delete ns mclaude-dev`. An empty `mclaude-{uslug}` namespace is cheap. Reaping logic (when, how, restart behavior) deserves a focused ADR rather than a side decision here. | Empty namespaces are harmless until the user account is fully removed; user-deletion cascade is itself a future flow. |
| Migration from leaf-NATS worker chart | **`helm uninstall` + `helm install` cutover.** Document a clean break: existing deployments uninstall the leaf-NATS chart, operator runs `mclaude host register` from their workstation, then installs the new chart. Brief downtime for K8s-hosted projects during the cutover. | Simplest implementation; no transitional code. Avoids carrying leaf-NATS deletion debt into a follow-up release. |
| Namespace naming | `mclaude-{userSlug}` per ADR-0062, where `userSlug = slugify(full email)` | Already decided; spec must reflect it. |
| MCProject CRD location | `mclaude-system` namespace on the central cluster. Per-user resources (Deployments, PVCs, Secrets) materialize in `mclaude-{userSlug}`. | Existing behavior; document it. |
| Owner references | Project Deployments/PVCs/Secrets must have `ownerReferences` back to the MCProject CR. **In scope** for this ADR — controller code change handled by the `/feature-change` loop after acceptance. | Cleanup of MCProject should cascade to project resources (caught by today's manual `kubectl delete ns` cleanup, where Deployments showed `ownerReferences: none`). |
| Scope of this ADR | Spec authoring + worker-chart cleanup decision + worker registration flow + owner-references requirement. Code/chart/binary changes handled by `/feature-change` after ADR is accepted. | Keeps this ADR focused on documentation + topology decisions; the dev-harness loop closes the impl gap. |

## User Flow

(K8s deployment / operator perspective)

1. **Install central cluster.** `helm install mclaude-cp charts/mclaude-cp -n mclaude-system` — provisions hub NATS (publicly exposed; topology is an open question), Postgres, control-plane, SPA, operator/account NKey bootstrap.
2. **Install worker chart on a K8s cluster** (any cluster, anywhere with hub-NATS reachability):
   ```
   helm install mclaude-worker charts/mclaude-worker -n mclaude-system \
     --set controlPlane.url=https://cp.mclaude.example \
     --set host.name="us-east" \
     --set host.hubNatsUrl=nats-wss://nats.mclaude.example:443
   ```
   A pre-install Job (`gen-host-nkey`) generates an NKey pair, writes the seed to a Secret (`{release}-host-creds`), and prints the **public key** to the Job log + a Helm post-install NOTES.txt message. No CP communication yet.
3. **Operator attests the host** by running the existing BYOH command from their workstation, with `--nkey-public` set to the public key from step 2:
   ```
   mclaude host register --type cluster --name "us-east" --nkey-public UABC...
   ```
   This publishes `mclaude.users.{uslug}.hosts._.register {name, type, nkey_public}` over NATS using the operator's user JWT (the attestation — "I vouch for this public key as a host I own"). CP slugifies `--name` to produce the host slug (e.g. `"us-east"` → `us-east`), creates the `hosts` row keyed by the public key, and returns `{ok, slug}` per ADR-0054. **No new API surface; same flow BYOH uses on a laptop.** The operator chooses a memorable name; the slug falls out deterministically. Both NATS payloads and HTTP bodies use snake_case (`nkey_public`) — confirmed by the CP handler struct in `lifecycle.go:240` (`json:"nkey_public"`) and `auth.go:20`. Note: `spec-state-schema.md:409` incorrectly lists `nkeyPublic` (camelCase); this ADR's spec update corrects it to `nkey_public`.
4. **Controller pod starts**, mounts the seed Secret, runs the standard ADR-0054 HTTP challenge-response (`POST /api/auth/challenge` + `verify`) using the now-registered public key. CP returns a host JWT scoped to `mclaude.hosts.{hslug}.>`. Same code path as `mclaude-controller-local/host_auth.go`.
5. **Controller connects to hub NATS** with the host JWT, subscribes to `mclaude.hosts.{hslug}.>`. Begins reconciling MCProject CRs.
6. **User creates project via SPA/CLI.** Control-plane writes Postgres row, publishes provisioning fanout on `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.create`.
7. **K8s controller materializes resources.** MCProject CR in `mclaude-system`, then per-user namespace `mclaude-{uslug}` if absent, then project Deployment + PVC + Secret with `ownerReferences` back to the MCProject CR.
8. **Session-agent pod boots, generates own NKey**, host controller registers the agent's public key with CP via `mclaude.hosts.{hslug}.api.agents.register`, agent then runs HTTP challenge-response to get its JWT, publishes lifecycle events directly to hub NATS (per ADR-0054).
9. **User deletes project.** Control-plane publishes delete event. Controller deletes the MCProject CR; `ownerReferences` cascade to the project Deployment/PVC/Secret. Empty user namespaces are reaped after a grace period (open question: GC policy).
10. **Operator deregisters K8s host.** Either: (a) `mclaude host deregister --slug us-east` from the workstation (publishes `mclaude.users.{uslug}.hosts.{hslug}.manage.deregister` per ADR-0054), then `helm uninstall mclaude-worker`; or (b) `helm uninstall` runs a pre-delete Job that publishes the same deregister subject using the host's own JWT before Helm tears down the controller.

## K8s host registration flow

This is **the same flow as BYOH** in ADR-0054 § "Unified credential protocol" / § "Host registration". The only K8s-specific bits are: (a) where the NKey pair is generated (in-cluster, via Helm pre-install Job), and (b) how the public key surfaces to the operator (Helm NOTES + Job log). No new API endpoints, no new tables, no new admin surface.

| Step | Actor | Action | Mirrors BYOH |
|------|-------|--------|--------------|
| 1. NKey gen | `gen-host-nkey` pre-install Job | Generates NKey pair via `nkeys.CreateUser()`, writes `nkeySeed` into Secret `{release}-host-creds`, prints `nkeyPublic` to log + Helm NOTES.txt. Idempotent — skips if Secret exists. | `mclaude host register` on a laptop generates the pair locally and prints the public key |
| 2. Attestation | Operator on workstation | Runs `mclaude host register --type cluster --name "us-east" --nkey-public UABC...` — publishes `mclaude.users.{uslug}.hosts._.register {name, type: 'cluster', nkey_public}` with operator's user JWT as attestation. CP slugifies `--name` to produce the host slug, creates `hosts` row keyed by `public_key` (the DDL column), returns `{ok, slug}`. | Identical |
| 3. JWT acquisition | Controller pod (boot) | Reads `nkeySeed` from Secret, runs HTTP challenge-response (`POST /api/auth/challenge` + `verify`), receives host JWT scoped to `mclaude.hosts.{hslug}.>`. Reuses `mclaude-controller-local/host_auth.go`. | Identical |
| 4. NATS connect | Controller pod | Connects to hub NATS (URL from Helm value `host.hubNatsUrl`) with the host JWT, subscribes to `mclaude.hosts.{hslug}.>`. | Identical |
| 5. JWT refresh | Controller binary | Background loop refreshes host JWT before 5-min TTL expiry via the same challenge-response code path. | Identical |
| 6. Deregister | Operator workstation OR pre-delete Job | Either `mclaude host deregister --slug us-east` from the workstation, or a Helm pre-delete Job using the controller's current JWT — both publish `mclaude.users.{uslug}.hosts.{hslug}.manage.deregister` per ADR-0054. CP soft-deletes host row + adds JWT to revocation list. | Identical |

### API endpoints used (none new)

All endpoints already exist per ADR-0054 / ADR-0035:

- `POST /api/auth/challenge` — body `{nkey_public}` → returns `{challenge}`
- `POST /api/auth/verify` — body `{nkey_public, challenge, signature}` → returns `{jwt}`
- NATS subject `mclaude.users.{uslug}.hosts._.register` — body `{name, type, nkey_public}` → CP slugifies `name`, creates `hosts` row keyed by `public_key` (DDL column), returns `{ok, slug}` (per ADR-0054 line 499–508; field confirmed by `lifecycle.go:240` `json:"nkey_public"`)
- NATS subject `mclaude.users.{uslug}.hosts.{hslug}.manage.deregister` — body `{}` → CP soft-deletes + revokes

### CLI flags required by this flow (already specified in ADR-0054)

`mclaude host register` (per ADR-0054 line 499–508) accepts:

- `--name <display name>` — human-readable
- `--type cluster` — required for K8s hosts; `'cluster'` is the existing enum value from ADR-0035 schema (`hosts.type IN ('machine', 'cluster')`). Default remains `'machine'` for laptop registration.
- `--nkey-public <pubkey>` — public key supplied externally (in the K8s case, read from the controller pod's gen-host-nkey Job log). Per ADR-0054, this flag exists alongside the legacy device-code flow from ADR-0035. The CLI generates a key locally only if `--nkey-public` is omitted.

The slug is **not** a CLI flag — CP derives it by slugifying `--name`. Operators choose a memorable `--name` (`"us-east"`, `"prod"`, `"my-laptop"`); the slug falls out deterministically. This avoids an ADR-0054 amendment.

If the existing CLI implementation predates ADR-0054 (still uses device-code only), the `/feature-change` loop after this ADR is accepted is responsible for adding `--nkey-public` and `--type` flags. ADR-0063 surfaces this as a required CLI change but does not respec the flow itself — that's ADR-0054's responsibility.

This ADR adds **no new API surface**. The K8s worker chart is a packaging mechanism that reuses the existing protocol.

### Helm chart artifacts

| Kind | Name | Purpose |
|------|------|---------|
| Job | `{release}-gen-host-nkey` | Pre-install hook (weight `-10`). Generates NKey pair via `nkeys.CreateUser()` (`U`-prefix, matching `mclaude-controller-local/host_auth.go`'s `ParseDecoratedUserNKey`). Writes the **decorated seed string** (`SUAB...`) to Secret data field `nkey_seed`. Prints the **public key** (`UABC...`) to the Job log + Helm NOTES.txt. Idempotent — skips if Secret exists. |
| Secret | `{release}-host-creds` | Single field: `nkey_seed` (decorated `S`-prefix seed string). JWT is **not** persisted here — controller acquires it in-memory via challenge-response on every boot. |
| Deployment | `{release}-controller-k8s` | Mounts `{release}-host-creds` as a volume at `/etc/mclaude/host-creds/`. Env: `HUB_NATS_URL` (from `host.hubNatsUrl`), `CONTROL_PLANE_URL` (from `controlPlane.url`), `HOST_NKEY_SEED_PATH=/etc/mclaude/host-creds/nkey_seed`. The `host.name` Helm value is rendered into NOTES.txt only — controller decodes its actual slug from the JWT. |
| ConfigMap | `{release}-session-agent-template` | Pod template referenced by the controller when materializing per-project session-agent pods (existing). |
| ClusterRole / ClusterRoleBinding | `{release}-controller` | Lets the controller manage Namespace, Deployment, PVC, Secret, MCProject CRs. |
| NOTES.txt | (Helm template) | Post-install message instructing the operator to read the public key (`kubectl logs job/{release}-gen-host-nkey`) and run `mclaude host register --type cluster --name "$HOST_NAME" --nkey-public <key>`, where `$HOST_NAME` is rendered from `.Values.host.name` (a memorable display name; CP slugifies it). |

## Component Changes

### `mclaude-control-plane`
- **`sys_subscriber.go` — CONNECT event dispatch updated for hub-direct cluster hosts.** Currently `handleSysEvent` dispatches `"Client"` kind events to `type='machine'` rows only and `"Leafnode"` kind events to `type='cluster'` rows only (lines 82–157). After this ADR, K8s cluster controllers connect hub-direct and appear as `"Client"` kind — not `"Leafnode"`. The dispatch must be updated: (a) the `"Client"` branch should look up the `hosts` row by `public_key` only with **no type filter** (`public_key` is the actual DDL column name — the `hosts` table uses `public_key` not `nkey_public`; see `db.go:853`) — it updates `last_seen_at` and the `mclaude-hosts` KV `online` flag for both `type='machine'` and `type='cluster'` hosts; (b) the `"Leafnode"` branch should be removed (leaf topology is gone per ADR-0054; unexpected `"Leafnode"` events log a warning and are dropped). This ensures K8s cluster controllers connecting hub-direct are tracked correctly in the presence table.
- **`spec-state-schema.md` line 431** — Update cluster host `$SYS` CONNECT event kind from `"Leafnode"` to `"Client"` (hub-direct). This is a doc change committed with the ADR spec updates.

### `mclaude-controller-k8s`
- Spec relocated to new component-local folder `docs/mclaude-controller-k8s/spec-k8s-architecture.md`
- Documented as connecting **directly to hub NATS**. Subscriptions consolidated to ADR-0054 host-scoped pattern only (drop "dual subscription during migration" language).
- **Auth model rewritten per ADR-0054.** The controller no longer holds the account signing key. Specifically:
  - **Drop env vars:** `NATS_ACCOUNT_SEED`, `NATS_CREDENTIALS_PATH`, `JS_DOMAIN`, `CLUSTER_SLUG`.
  - **Drop functions:** `loadAccountKey()` in `main.go`, `IssueSessionAgentJWT()` in `nkeys.go`, the `accountKP` field on the reconciler, and any other code path that mints JWTs locally.
  - **Add env vars:** `HUB_NATS_URL` (replaces `NATS_URL`), `CONTROL_PLANE_URL`, `HOST_NKEY_SEED_PATH` (default `/etc/mclaude/host-creds/nkey_seed`).
  - **Boot sequence:** read `nkey_seed` file from mounted Secret → call `POST /api/auth/challenge` + `verify` (per ADR-0054) → receive host JWT scoped to `mclaude.hosts.{hslug}.>` → connect to hub NATS with the JWT.
  - **JWT lifetime:** held in memory only, never persisted back to the Secret. Refresh loop reuses the challenge-response code path before each 5-min TTL expiry.
  - **Session-agent JWT issuance:** moves to control-plane per ADR-0054 § "Session-agent JWT issuance | Control-plane only". When the controller materializes a session-agent pod, the agent registers its own NKey public key with CP via `mclaude.hosts.{hslug}.api.agents.register` (a NATS request the controller relays from the agent), then the agent itself runs HTTP challenge-response to get its JWT. The controller is no longer in the JWT-minting path.
  - **`reconcileSecrets` code change:** Remove the two `IssueSessionAgentJWT(...)` / `FormatNATSCredentials(...)` blocks (lines 218–222 and 239–245 of `reconciler.go`). The `user-secrets` Secret retains only the `oauth-token` field (dev OAuth token; unrelated to NATS auth). The `nats-creds` field is no longer populated by the controller.
  - **`CONTROL_PLANE_URL` injection into session-agent pods:** add a `controlPlaneURL string` field to the reconciler struct (mirroring the existing `sessionAgentNATSURL` field pattern at `reconciler.go:313`). At reconcile time, inject `CONTROL_PLANE_URL` env var directly from this struct field into the session-agent pod spec — the same way `NATS_URL` is currently injected. Do **not** add it to the `session-agent-template.yaml` ConfigMap (that ConfigMap holds only static Helm-rendered values like image and resource sizes). The reconciler struct field is populated at startup from the controller's own `CONTROL_PLANE_URL` env.
- **Move `mclaude-controller-local/host_auth.go` to `mclaude-common/pkg/hostauth/`**, importable by both controllers as `mclaude.io/common/pkg/hostauth`. The existing `mclaude-common` module already has `github.com/nats-io/nkeys` as a direct dependency. The `mclaude-controller-local` import path changes from the file-local package to `mclaude.io/common/pkg/hostauth`. The `NewHostAuthFromCredsFile` constructor is kept unchanged for `mclaude-controller-local`. For K8s, add a new constructor in the same package:
  ```go
  NewHostAuthFromSeed(seedPath string, cpURL string, log zerolog.Logger) (*HostAuth, error)
  ```
  This constructor reads the NKey seed from `seedPath`, derives the key pair, stores `cpURL`, and sets `jwt = ""`. No pre-existing JWT is required.

  **Boot loop behavior:** On `Refresh()` when `jwt == ""` (initial boot), the controller calls `POST /api/auth/challenge` + `verify`. If CP returns HTTP 404 (public key not in the `hosts` table — operator has not yet run `mclaude host register`), the method logs:
  ```
  NKey <pubkey> not registered with control-plane. To complete registration run:
    mclaude host register --type cluster --name <display-name> --nkey-public <pubkey>
  ```
  and returns a retryable sentinel error. The K8s controller's main boot loop retries `Refresh()` on a 5-second interval, doubling each attempt up to a 60-second cap. The controller does not attempt to connect to hub NATS until a valid JWT is acquired. K8s restarts the pod after the liveness probe deadline if the operator never registers — this is the expected failure mode (operationally visible via `kubectl describe pod`).
- **Host slug derived from the JWT itself** (decoded after acquisition), not from a Helm value. This prevents drift if the operator chose a different slug at registration time. The Helm `host.name` value is used only for NOTES.txt operator instructions; controller does not read it for subscription scoping.
- **Drop `sessionAgentNATSURL()` derivation function.** Now that the worker NATS StatefulSet is gone, session-agents inherit `HUB_NATS_URL` directly via the session-agent template — no derivation needed. The controller passes `HUB_NATS_URL` straight through to the agent pod's `NATS_URL` env.
- Owner references on materialized resources (Deployment, PVC, Secret) → MCProject CR. Current code does not set these (`kubectl get deploy -n mclaude-dev -o json | jq '.items[].metadata.ownerReferences'` returned `none` today). Use `controllerutil.SetControllerReference` from controller-runtime.

### `mclaude-controller-local`
- Spec relocated to new component-local folder `docs/mclaude-controller-local/spec-controller-local.md` (matches the code-dir name `mclaude-controller-local/`)
- Content is the existing "Variant 2" sections from `spec-controller.md`, unchanged in substance

### `charts/mclaude-cp`
- **Already in place.** The chart already has the NATS WebSocket Ingress (`templates/nats-ws-ingress.yaml`, gated by `.Values.ingress.natsHost`) and the WebSocket listener in the NATS ConfigMap (`templates/nats-configmap.yaml` lines 32–33). No template changes required.
- **Operator action only:** when installing the cp chart for a multi-host deployment, set `--set ingress.natsHost=nats.mclaude.example`. This renders the WS Ingress with cert-manager + external-dns annotations already in the template. The URL operators feed into the worker chart's `host.hubNatsUrl` is then `nats-wss://nats.mclaude.example:443`.
- This ADR documents the operator action; no code or template changes to the cp chart.

### `charts/mclaude-worker`
- **Strip + retain as independently-installable chart.** Drop:
  - `templates/nats-statefulset.yaml`
  - `templates/nats-configmap.yaml`
  - `templates/nats-service.yaml`
  - `templates/gen-leaf-creds-job.yaml`
  - `values.yaml` keys: `leafUrl`, `leafCreds`, `nats.*`
- Add:
  - `templates/gen-host-nkey-job.yaml` (pre-install hook)
  - `templates/host-creds-secret.yaml` (placeholder, populated by the Job)
  - `templates/NOTES.txt` (post-install operator instructions for `mclaude host register --nkey-public ...`)
  - `values.yaml` keys: `controlPlane.url`, `host.name`, `host.hubNatsUrl`
- Modify:
  - `templates/controller-deployment.yaml`:
    - Drop volume + mount for `leaf-creds` Secret (path `/etc/nats/leaf-creds`)
    - Drop env `NATS_ACCOUNT_SEED` (controller no longer holds the account key per ADR-0054)
    - Drop env `JS_DOMAIN`, `CLUSTER_SLUG`, `NATS_CREDENTIALS_PATH` (unused; per audit gaps)
    - Drop `SESSION_AGENT_NATS_URL` derivation in the template (session-agents inherit `HUB_NATS_URL` directly via the session-agent template ConfigMap)
    - Add volume + mount for `{release}-host-creds` Secret at `/etc/mclaude/host-creds/`
    - Add env `HUB_NATS_URL` (from `.Values.host.hubNatsUrl`), `CONTROL_PLANE_URL` (from `.Values.controlPlane.url`), `HOST_NKEY_SEED_PATH=/etc/mclaude/host-creds/nkey_seed`
    - Rename existing `NATS_URL` env to `HUB_NATS_URL` (consistent with `mclaude-controller-local`)
- The chart becomes installable into any K8s cluster that can reach the CP (multi-cluster K8s + single-cluster degenerate stay supported with the same chart).

### `docs/mclaude-controller/spec-controller.md`
- **Delete** after content is migrated to `docs/mclaude-controller-k8s/spec-k8s-architecture.md` (Variant 1 content) and `docs/mclaude-controller-local/spec-controller-local.md` (Variant 2 content). Folder removed if empty.

### `docs/charts-mclaude/spec-helm.md`
- Drop the `mclaude-worker` "Worker NATS" subsection
- Drop the `gen-leaf-creds-job` description and `leafCreds` config docs
- Update the "single-cluster degenerate deployment" paragraph
- Reflect whatever the worker-chart-fate decision lands on

## Data Model

No new tables, no new KV entries, no new subjects.

**One schema migration is required:**

The `hosts` table currently has a constraint that blocks inserting `type='cluster'` rows without legacy leaf-NATS columns:
```sql
CHECK (type = 'machine' OR (js_domain IS NOT NULL AND leaf_url IS NOT NULL AND account_jwt IS NOT NULL))
```
These columns (`js_domain`, `leaf_url`, `account_jwt`) are deprecated per ADR-0054. The constraint must be **dropped** via a migration before any new K8s cluster hosts can be registered. The migration also drops `js_domain`, `leaf_url`, `account_jwt` if not already removed.

**Slug uniqueness is per-user:** The `hosts` table has `UNIQUE (user_id, slug)` — slugs are unique per user, not globally. Two different users may register hosts with the same slug without conflict. Host lookup in `sys_subscriber.go` and challenge-response uses `public_key` (the NKey public key column — globally unique by NKey cryptography), not slug, so no unique-slug guarantee is needed for the flows described here.

## Error Handling

| Failure | Surface |
|---------|---------|
| Hub NATS unreachable from worker cluster at boot | Controller crashloops (no JWT can be acquired). K8s liveness probe surfaces the failure. JetStream queues provisioning events on the CP side until controller reconnects. |
| Hub NATS unreachable from worker cluster at runtime | Controller's NATS client retries with backoff; existing JWT continues to work until TTL expires; once TTL expires and controller can't reach CP for refresh, controller exits and crashloops. |
| Per-user namespace creation fails (RBAC, quota) | MCProject CR enters `Failed` phase; controller logs + control-plane surfaces error to user via NATS |
| Owner-reference cascade fails to delete project pods | Should not occur once owner refs are wired (in scope here). If observed in practice, file a bug — owner refs are the cleanup path. |
| Empty per-user namespace remains after all the user's MCProjects are deleted | **Acceptable in this ADR.** Empty K8s namespaces are essentially free. Eventual cleanup deferred to a follow-up ADR on namespace lifecycle / user-deletion cascade. |
| Stale leaf-creds Secret left over from old worker chart | Migration is `helm uninstall mclaude-worker` (cleans up the old release fully, including leaf-creds Secret) before re-installing the new chart shape. No special hook required. |
| Operator forgets to run `mclaude host register` after Helm install | Controller crashloops with a clear log message: "host JWT not available; run `mclaude host register --type cluster --name <name> --nkey-public <pubkey>` from a workstation". Pubkey is also in the gen-host-nkey Job log + Helm NOTES.txt. |
| Operator chooses a different `--name` than the `host.name` Helm value | Registration succeeds — CP keys off public key, not name. Controller decodes its actual slug (derived from whatever name the operator used) from the JWT and subscribes accordingly. The Helm `host.name` value is informational (NOTES.txt only). |

## Security

Unchanged from ADR-0054 + ADR-0059. This ADR is a documentation alignment + chart cleanup; security decisions stand.

- K8s controller's NKey is a host JWT issued by control-plane (5-min TTL, refreshed via HTTP challenge-response)
- No account signing key on the controller (per ADR-0054)
- No leaf-creds Secret if worker chart is updated/deleted (one fewer credential surface)
- Per-user namespaces continue to enforce isolation via NetworkPolicies + RBAC per ADR-0059

## Impact

Specs updated in this commit:
- `docs/mclaude-controller-k8s/spec-k8s-architecture.md` — **new file** (full K8s topology, registration flow, CRD schema, namespace rules, error handling)
- `docs/mclaude-controller-local/spec-controller-local.md` — **new file** (Variant 2 content migrated from existing `spec-controller.md`)
- `docs/mclaude-controller/spec-controller.md` — **deleted**; folder removed
- `docs/charts-mclaude/spec-helm.md` — updated to drop worker NATS / leaf creds, document new worker-chart shape
- `docs/spec-state-schema.md` — five corrections to align with live DDL (`db.go`):
  1. Line 431: cluster host `$SYS` CONNECT event kind `"Leafnode"` → `"Client"` (hub-direct)
  2. Line 409: host register payload field `nkeyPublic` → `nkey_public` (matching `lifecycle.go:240`)
  3. Line 64: hosts table NKey column `nkey_public` → `public_key` (matching `db.go:853`)
  4. Line 60: hosts FK column `owner_id` → `user_id` (matching `db.go:844`)
  5. Lines 61/70: `UNIQUE (slug) — hosts are globally unique` → `UNIQUE (user_id, slug)` (matching `db.go:858`)

Components implementing the change (handled by `/feature-change` after this ADR is accepted):
- `mclaude-control-plane/sys_subscriber.go` — update `handleSysEvent` to match `"Client"` kind for both `type='machine'` and `type='cluster'` hosts (no type filter on the `"Client"` branch); remove `"Leafnode"` branch
- `charts/mclaude-worker/` — strip + retain (drop NATS StatefulSet/ConfigMap/Service, drop gen-leaf-creds-job, add gen-host-nkey-job + host-creds Secret + NOTES.txt; controller env reshape)
- `charts/mclaude-cp/` — **no changes**; existing NATS WS Ingress and WebSocket listener are already in place. Documentation work only (note the `ingress.natsHost` operator value).
- `mclaude-controller-k8s/` — set owner references on materialized resources; decode host slug from JWT; drop dual-subscription code; drop unused env vars (`NATS_ACCOUNT_SEED`, `NATS_CREDENTIALS_PATH`, `JS_DOMAIN`, `CLUSTER_SLUG`); rename `NATS_URL`→`HUB_NATS_URL`; add `HOST_NKEY_SEED_PATH` + `CONTROL_PLANE_URL`; remove `loadAccountKey()`, `IssueSessionAgentJWT()`, `accountKP` field
- `mclaude-common/pkg/hostauth/` — **new package**: `host_auth.go` moved here from `mclaude-controller-local/`; add `NewHostAuthFromSeed` constructor with 5s/60s-capped retry boot loop; both controllers import `mclaude.io/common/pkg/hostauth`
- `mclaude-controller-local/` — remove local `host_auth.go`, update import to `mclaude.io/common/pkg/hostauth`

## Scope

**In v1:**
- Author the new `docs/mclaude-controller-k8s/spec-k8s-architecture.md`
- Migrate Variant 2 content to `docs/mclaude-controller-local/spec-controller-local.md`
- Delete `docs/mclaude-controller/spec-controller.md` and the empty folder
- Update `docs/charts-mclaude/spec-helm.md` to drop leaf-node language and document new shape
- Strip + retain the worker chart (independently installable, registers via existing BYOH flow)
- Add hub NATS WebSocket Ingress to the cp chart
- Implement owner references on MCProject children in the K8s controller
- Document the migration cutover path for existing leaf-NATS deployments

**Deferred:**
- **Per-user namespace lifecycle (reap-empty + user-deletion cascade)** — needs its own ADR. Empty namespaces are harmless until a focused design lands.
- HA controller (multiple replicas with leader election) — `LEADER_ELECTION` env var is set in Helm but not read by the binary today
- Tailscale / relay-fronted hub NATS as alternative to public Ingress (operators can layer this on top of the public NATS endpoint without spec changes)

## Open questions

(none — all decisions made; ready for design audit)

## Implementation Plan

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| `docs/mclaude-controller-k8s/spec-k8s-architecture.md` (new) | 320 spec lines | 60k | Full topology, CRD schema, subscription patterns, namespace rules, MCProject reconciliation, registration flow, error states |
| `docs/mclaude-controller-local/spec-controller-local.md` (new, content migration) | 200 spec lines | 30k | Variant 2 content extracted from `spec-controller.md` |
| `docs/charts-mclaude/spec-helm.md` (update) | -120 / +60 | 40k | Drop worker NATS subsections + leaf creds; add hub NATS WS Ingress + new worker-chart shape |
| `docs/mclaude-controller/spec-controller.md` (delete) | -500 / 0 | 10k | Removal after content migration |
| `charts/mclaude-worker/` (strip + retain) | -800 / +250 | 100k | Drop NATS StatefulSet/ConfigMap/Service; drop gen-leaf-creds-job; add gen-host-nkey-job, host-creds-secret, NOTES.txt; controller-deployment env reshape |
| `charts/mclaude-cp/` (no chart changes) | 0 | 0 | NATS WS Ingress + WebSocket listener already present; documentation only |
| `mclaude-controller-k8s/reconciler.go` (owner references) | +30 / -5 | 60k | `controllerutil.SetControllerReference` on Deployment/PVC/Secret + tests |
| `mclaude-controller-k8s/main.go` (slug from JWT, env reshape) | +40 / -80 | 50k | Decode hslug from JWT after challenge-response; remove `NATS_ACCOUNT_SEED`/`NATS_CREDENTIALS_PATH`/`JS_DOMAIN`/`CLUSTER_SLUG`; rename `NATS_URL`→`HUB_NATS_URL`; add `HOST_NKEY_SEED_PATH` + `CONTROL_PLANE_URL` |
| `mclaude-controller-k8s/nkeys.go` (drop account-key path) | -100 | 30k | Remove `loadAccountKey()`, `IssueSessionAgentJWT()`, `accountKP` field. Agent JWTs now issued by CP. |
| `mclaude-control-plane/sys_subscriber.go` (CONNECT dispatch fix) | +10 / -30 | 30k | Remove type filter from `"Client"` branch (lookup by `public_key`, no type filter); drop `"Leafnode"` branch |
| `mclaude-control-plane/db.go` + migration (drop CHECK constraint) | +15 / -5 | 20k | Drop `CHECK (type = 'machine' OR (...))` constraint; migrate `js_domain`/`leaf_url`/`account_jwt` columns if still present |
| `mclaude-common/pkg/hostauth/` (new package, move + extend) | +80 | 40k | Move `host_auth.go` from `mclaude-controller-local/`; add `NewHostAuthFromSeed` with 5s/60s-capped boot retry loop |
| `mclaude-controller-local/` (import update) | -50 / +5 | 10k | Remove local `host_auth.go`; update import to `mclaude.io/common/pkg/hostauth` |

**Total estimated tokens:** 430k
**Estimated wall-clock:** ~5.5h of an 8h budget (69%)
