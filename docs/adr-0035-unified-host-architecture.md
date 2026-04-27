# ADR: Unified Host Architecture — BYOH + Cluster Topology + Controller Separation

**Status**: accepted
**Status history**:
- 2026-04-26: draft
- 2026-04-26: accepted — design-evaluator CLEAN (6 rounds), spec co-commit verified

> Supersedes:
> - `adr-0004-multi-laptop.md` (BYOH) — folded in: `hosts` table, host-scoped NATS credentials, `.hosts.{hslug}.` subject scheme, device-code + authed registration, $SYS-based liveness, force re-register migration.
> - `adr-0011-multi-cluster.md` (Multi-Cluster) — folded in: leaf-node NATS topology, JetStream domains, dual-NATS SPA strategy, hub JWT trust chain, single-cluster degenerate case, separate Helm charts. The user_clusters/RBAC sections were already superseded by 0004 and remain so.
> - `adr-0014-controller-separation.md` (Controller Separation) — folded in: binary boundary (control-plane = pure HTTP+NATS+Postgres, zero K8s; `mclaude-controller` = kubebuilder operator + BYOH-machine local variant), NATS-based command flow, $SYS presence for component liveness.
>
> The three ADRs above are marked `superseded` by this ADR in their status history. Their decisions are not edited (immutable); they're absorbed by reference here.

## Overview

Unifies the host identity model (per-user `hosts` table with `type ∈ {machine, cluster}`), the cluster runtime topology (hub NATS + leaf-node worker NATS, JetStream domains, dual-NATS SPA), and the controller separation (control-plane has zero K8s; `mclaude-controller` is the sole K8s operator with a corresponding local variant for BYOH machines) into a single coherent architecture. All project-scoped subjects, KV keys, and HTTP URLs follow the `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.…` scheme per ADR-0004 and ADR-0024. NATS auth is a 3-tier operator → account → user JWT chain on hub and every worker, with `resolver: MEMORY` and pre-loaded account JWT. Single-host deployments work as a degenerate case with `hosts.length === 1` and a default `local` machine host per user.

This ADR also closes the implementation gap: the codebase under `mclaude-common` was updated to host-scoped helpers (`UserHostProject*`, 4-arg `SessionsKVKey`, 3-arg `ProjectsKVKey`) but `mclaude-session-agent`, `mclaude-control-plane`, `mclaude-cli`, `mclaude-web` were not — `mclaude-session-agent` does not currently compile. Closing that gap is in scope for the implementation plan that follows acceptance.

## Motivation

The host story today is fragmented across three ADRs (0004 accepted but with messy edit history; 0011 draft, partially superseded by 0004; 0014 draft) plus ADR-0024 (typed slugs, accepted, prerequisite for 0004's subject scheme). The fragmentation creates real harm:

1. **Living specs disagree with the ADRs.** `spec-control-plane.md` still describes single-cluster `MCProject` CR creation directly (pre-0014). `spec-state-schema.md` says "no auth resolver configured" (pre-0011). `spec-session-agent.md` has no `HOST_SLUG` configuration (pre-0004). Anyone reading the specs gets a coherent but obsolete picture.
2. **The codebase is in a half-migrated state.** `mclaude-common` already exports the host-scoped helpers required by ADR-0004; downstream services still call helper names that no longer exist. `mclaude-session-agent` does not compile against the canonical `subj` package. The existing scheduler implementation (ADR-0009 → ADR-0034) cannot be exercised end-to-end until host-scoping is plumbed through.
3. **ADR-0011's surviving decisions are stale relative to ADR-0024.** Subject-shape mismatch: 0011 uses `mclaude.clusters.{clusterId}.projects.provision` (untyped, no `.api.` segment); the canonical state schema uses `mclaude.clusters.{cslug}.api.projects.provision`. A developer following 0011 would write the wrong subjects.
4. **ADR-0014's binary boundary is the only way the host model works.** A control-plane that holds K8s permissions cannot host BYOH-machine deployments where a laptop is the host. Without the controller split, "hosts as first-class peers" doesn't compose.

The cheapest fix is one ADR that owns the whole story end-to-end. Older ADRs become historical reference; the unified ADR is the single source of truth going forward.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Host identity | Single `hosts` table; per-user unique slug; `type ∈ {'machine', 'cluster'}` discriminator; `role ∈ {'owner', 'user'}` per-user. Cluster-shared fields (`js_domain`, `leaf_url`, `account_jwt`, `direct_nats_url`, and `public_key` — the cluster controller's NKey) are NULL for machine hosts and populated (duplicated across users granted to the same cluster) for cluster hosts. The `online` boolean lives in `mclaude-hosts` KV (single-writer control-plane, sourced from `$SYS`), not in Postgres — `last_seen_at` is the durable historical record. No separate `clusters` table. | Unifies BYOH machines and K8s clusters as first-class peers under one schema. The duplication of cluster-shared fields across user rows is acceptable at mclaude's scale and avoids a parallel infrastructure-vs-access table split. KV-not-Postgres for `online` keeps the live-state path on a single source (NATS) and avoids dual writes. |
| Subject scheme | `mclaude.users.{uslug}.hosts.{hslug}.…` is the **only** project-scoped subject family. No `mclaude.clusters.{cslug}.>` subjects exist. Sessions API, events, lifecycle, terminal, and project provisioning all route through user/host scope. | Unifies machine and cluster hosts under a single subject family. The cluster-scoped subjects from ADR-0011 are eliminated entirely. ADR-0024 typed-slug invariant maintained. |
| Cluster runtime topology | Hub NATS on the control-plane cluster; each worker cluster runs its own NATS connected as a leaf node. JetStream is per-cluster with a unique domain per worker. Worker controllers run K8s reconciler + session-agent pods. | Inherits ADR-0011's surviving topology unchanged in shape; only the subject scheme is normalized to ADR-0024 form. |
| Single-cluster degenerate case | When `hosts` contains exactly one row of type `cluster` (or one of type `machine` for BYOH-only), the SPA still uses host-qualified subjects. JetStream domain qualification is conditional: SPA inspects `jsDomain` from the login response and includes it only when present. | Consistent subject scheme regardless of deployment shape (ADR-0024 invariant). Domain qualification is the only place the single-cluster case differs. |
| Default machine host | On user creation, control-plane writes a row to `hosts` with `slug='local'`, `type='machine'`, `role='owner'`. Existing projects backfill `host_id` to this row. | Supports the "I just want my laptop to work" flow without explicit host registration. |
| Migration / existing data | **No migration.** Existing users, projects, NATS credentials, and KV state are not preserved. Deployment of this ADR is a clean break: any existing rows are wiped or ignored; users re-register from scratch. | mclaude is small enough (single-user, no production traffic) that migration ceremony costs more than it's worth. Avoids dual-schema support and force-re-register error paths. |
| Binary boundary | `mclaude-control-plane`: HTTP + NATS + Postgres. Zero K8s client. Runs anywhere. `mclaude-controller-k8s`: kubebuilder operator. Reconciles `MCProject` CRs. Subscribes to `mclaude.users.*.hosts.{cluster-slug}.api.projects.>` (wildcard at user level — receives requests from all users granted access to its cluster). `mclaude-controller-local`: BYOH-machine variant. Manages session-agent processes via process supervision (not K8s). Subscribes to `mclaude.users.{uslug}.hosts.{hslug}.api.projects.>` (one user, one host). | Per ADR-0014. The local variant is what makes BYOH machines work — they cannot run K8s reconcilers but they can run a process manager. Both controllers share the same NATS subject family; only the subscription scope differs. |
| Provisioning subject | `mclaude.users.{uslug}.hosts.{hslug}.api.projects.provision` (request/reply, NATS) — same host-scoped pattern as everything else. Both controller variants subscribe to this scope. Machine controller subscribes to its own user/host (one subject); cluster controller subscribes with a wildcard at the user level: `mclaude.users.*.hosts.{cluster-slug}.api.projects.>`. | Unified subject family. The wildcard works because all users granted access to a cluster have host rows with the same slug (the cluster's name) — see Decisions row "Cluster slug uniqueness". |
| Cluster slug uniqueness | When admin grants a user access to a cluster, the user's new `hosts` row is created with the **same slug** as the cluster's canonical name (e.g., `us-east`). All users with access to the same cluster have host rows with the same slug, the same `jsDomain`, the same `leafUrl`, the same `accountJwt`. Per-user-unique constraint (`UNIQUE(user_id, slug)`) is preserved because the duplication is across users. | Enables the cluster controller's wildcard subscription `mclaude.users.*.hosts.us-east.…` to receive provisioning requests from all users granted access. Machine host slugs remain user-chosen (per-user-unique); cluster host slugs are admin-controlled and shared across users granted to that cluster. |
| NATS auth chain | 3-tier operator → account → user JWT. There is **one operator and one account** per mclaude install. Hub and every worker NATS are configured with `operator: $OPERATOR_JWT; resolver: MEMORY; resolver_preload: { $ACCOUNT_PUBLIC_KEY: $ACCOUNT_JWT }` — same trust chain everywhere. User JWTs (issued by control-plane, signed by the account key) are scoped to `mclaude.users.{uslug}.hosts.{hslug}.>`. Cluster controller JWTs (issued at cluster registration) are scoped to `mclaude.users.*.hosts.{cluster-slug}.>` — the wildcard at the user level lets the controller subscribe across all users granted access to its cluster. Worker NATS leaf-node JWTs use the same scope as the cluster controller JWT (or a superset, to allow JetStream domain ops). | One trust root; same JWTs validate at hub AND any worker, which is what makes hub-direct-to-worker connection swap work for the SPA. |
| Control-plane deployment topology | The control-plane is **always K8s-hosted** — there is no local/standalone variant. Hub NATS, control-plane, Postgres, and SPA all run in the central `mclaude-cp` Kubernetes cluster. BYOH machines (laptops) are pure clients: they run `mclaude-controller-local` + `mclaude-session-agent`, connect to the remote hub NATS as ordinary NATS clients using their per-host user JWT. They do not run their own control-plane and do not need access to the operator/account keys. | Simplifies the bootstrap problem (one place to generate keys, one source of truth), avoids parallel-implementation complexity for the control-plane, and matches the actual deployment story (mclaude-cp is a K8s service; laptops attach to it). |
| Operator/account key bootstrap | Helm pre-install hook runs `mclaude-cp init-keys` as a Job. The Job generates `operatorNKey` + `accountNKey` pairs + corresponding JWTs (account JWT signed by operator key, operator JWT self-signed) and writes them to K8s Secret `mclaude-system/operator-keys`. Hub NATS pod depends on the Secret existing; when it starts, its config references the Secret for `resolver_preload`. Control-plane Deployment also reads the Secret to sign per-host user JWTs. Subsequent deploys: the Job sees the Secret already exists and exits without regenerating. When admin runs `mclaude cluster register`, the control-plane returns `operatorJwt` + `accountJwt` so the new worker cluster's NATS can be deployed with the same trust chain. | Solves the chicken-and-egg ordering (NATS needs JWTs to validate; control-plane creates JWTs) by running key generation BEFORE NATS starts. Zero-touch on subsequent deploys. |
| First admin user | Helm values include `bootstrapAdminEmail`. The init-keys Job (same one that generates operator/account keys) also writes a `users` row with `email = bootstrapAdminEmail`, `is_admin = true`, `oauth_id = NULL`. When that user signs in via OAuth for the first time, control-plane matches their email to the bootstrap row and links the OAuth identity (sets `oauth_id`). Further admin promotion is via admin API (`POST /admin/users/{uslug}/promote`) or direct SQL. | Lets the first admin be created without requiring SQL surgery; matches the email-as-identity model the rest of mclaude uses. |
| Admin CLI auth | All admin commands (`mclaude cluster register`, `mclaude cluster grant`, `mclaude admin users …`) use the same bearer token issued by `mclaude login`. Token persisted to `~/.mclaude/auth.json` at 0600. CLI sends `Authorization: Bearer <token>` on every HTTP call. Control-plane checks `users.is_admin` for endpoints under `/admin/`; non-admin calls return 403. | Standard pattern. Server-side admin check means no separate token state on the client. |
| Heartbeat / liveness | **`$SYS.ACCOUNT.*.CONNECT`/`DISCONNECT` only.** Control-plane subscribes on hub NATS. The handler discriminates on `client.kind` and `client.nkey`: (1) `kind="Client"` + `client.nkey` matches a `hosts.public_key` row with `type='machine'` → update that single row's `last_seen_at` and KV `online=true`; (2) `kind="Leafnode"` + `client.nkey` matches a row with `type='cluster'` → update **all** rows where `slug=found.slug AND type='cluster'` (cluster-shared liveness across granted users); (3) no match (SPA's per-login ephemeral NKey, control-plane's own connection) → ignore. DISCONNECT mirrors with `online=false` and does not rewrite `last_seen_at`. No periodic heartbeat publishes; no `mclaude-heartbeats` bucket. | Per ADR-0004. Cleaner than periodic publishes; works uniformly for machine and cluster hosts. The `client.kind` switch correctly separates machine-daemon connections from cluster leaf-links and naturally ignores ephemeral SPA connections without extra bookkeeping. |
| SPA NATS connections | SPA opens a hub connection (always, for control-plane subjects + JetStream domain-qualified watches). On demand for active sessions, SPA opens a direct worker connection using `directNatsUrl` from the login response. Falls back to hub-via-leaf-node if direct is unreachable. | Per ADR-0011. Latency win for terminal I/O; works without direct connection. |
| Login response shape | `{ user, jwt, nkeySeed, hubUrl, hosts: [...], projects: [...] }`. Each host carries `{ slug, name, type, role, online, lastSeenAt, jsDomain?, directNatsUrl? }` (cluster-specific fields present only on `type='cluster'` entries). Each project carries `{ slug, name, hostSlug, hostType, jsDomain?, directNatsUrl? }` (cluster-specific fields present only when the project's host is a cluster). There is **no** top-level `clusters` array — the SPA derives it with `hosts.filter(h => h.type === 'cluster')`. | The SPA picks per-project NATS connection strategy from this. |
| Helm chart split | `mclaude-cp` Helm chart deploys control-plane + hub NATS + Postgres + SPA. `mclaude-worker` Helm chart deploys worker NATS (leaf-node config) + `mclaude-controller-k8s` + session-agent template. | Per ADR-0011. Single-cluster deployments install both into the same cluster with the leaf-node config pointing at localhost. |
| Session-agent host slug source | Set via `HOST_SLUG` env var (cluster pods, injected by `buildPodTemplate` from `projects.host_id`'s slug); `--host <hslug>` flag for BYOH daemon (read from CLI arg or `~/.mclaude/active-host` symlink). Required, not derived. | Hard-fail on absence. Avoids subtle routing bugs that would land messages on the wrong host. |
| Code-gap closure | This ADR's implementation plan covers: `Agent.hostSlug` field plumbing, `DaemonConfig.HostSlug` field, all 7 enumerated `subj` call-site fixes in `agent.go`/`daemon_jobs.go`, `state.go` struct/wrapper updates, `mclaude-control-plane`'s `hosts` HTTP endpoints + Postgres DDL, `mclaude-cli`'s `host` subcommand, `mclaude-web`'s `subj.ts` host-scoped builders + per-cluster connection logic, `mclaude-controller-local` binary scaffold, removal of `mclaude-heartbeats` bucket. | Single ADR owning the whole story; implementation closes the half-migrated state under one workstream. |

## User Flow

### A. New user, BYOH machine

1. User runs `mclaude host register` on their laptop.
2. CLI prompts: hostname (default = `hostname` output, slugified). Generates an NKey pair locally — the **private seed never leaves the laptop**, written to `~/.mclaude/hosts/{hslug}/nkey.seed` (mode 0600). Calls `POST /api/users/{uslug}/hosts/code` with `{publicKey}` to get a 6-char device code; the server stores the public key with the code. CLI prints "Open `<dashboard>/host/code` and enter `XXXX-YY`."
3. User opens dashboard, signs in (existing auth), enters code + display name. Dashboard calls `POST /api/hosts/register` with `{code, name}`. Control-plane looks up the stored `publicKey` from the code record, creates the `hosts` row with `public_key = publicKey`, mints a per-host user JWT (signed by the account key) scoped to `mclaude.users.{uslug}.hosts.{hslug}.>`, and returns `{slug, jwt, hubUrl}` to the dashboard. The seed is never sent over the wire.
4. CLI polls `GET /api/users/{uslug}/hosts/code/{code}` until the status changes from `pending` to `completed`. On completion, the response includes `{slug, jwt, hubUrl}`. CLI writes `~/.mclaude/hosts/{hslug}/{nats.creds, config.json}` from the JWT + the locally-stored seed, and symlinks `~/.mclaude/active-host → {hslug}`.
5. User runs `mclaude daemon --host <hslug>` (or invokes the launchd / systemd unit they configured) — the local controller reads `--host` (from `active-host` symlink if unset), connects to hub NATS using the host JWT, subscribes to `mclaude.users.{uslug}.hosts.{hslug}.api.projects.>`, and starts session-agent subprocesses for each provisioned project.
6. SPA login response includes the new host. User can launch sessions on their laptop from the dashboard.

### B. Cluster host (multi-cluster operator)

1. Admin runs `mclaude cluster register --slug us-east --jetstream-domain us-east --leaf-url nats-leaf://hub.mclaude.example:7422 --direct-nats-url wss://us-east.mclaude.example/nats`.
2. CLI calls `POST /admin/clusters` (admin-only). Control-plane generates a per-cluster NKey pair (the private seed will be returned to the admin once and stored in the worker NATS Secret), then creates a `hosts` row for the admin with `type='cluster'`, `slug='us-east'`, `js_domain='us-east'`, `leaf_url='nats-leaf://…'`, `direct_nats_url='wss://…/nats'`, `public_key='<cluster NKey pubkey>'`, `role='owner'`. Control-plane mints a per-user JWT for the admin scoped to `mclaude.users.{adminSlug}.hosts.us-east.>` and a per-cluster leaf JWT scoped to `mclaude.users.*.hosts.us-east.>` (this is both the JWT the worker NATS uses to leaf-link into the hub **and** the credentials `mclaude-controller-k8s` uses for its NATS connection). All JWTs are signed by the account key from `mclaude-system/operator-keys`. The existing deployment-level `accountJwt` and `operatorJwt` are read from the Secret and returned in the response (not regenerated — there is one operator and one account per install).
3. Control-plane returns `{slug: "us-east", leafJwt, leafSeed, accountJwt, operatorJwt, jsDomain: "us-east", directNatsUrl: "wss://…/nats"}`. Admin places these into the worker cluster's NATS Secret + Helm values.
4. Worker cluster comes up. Worker NATS connects to the hub as a leaf node. Hub `$SYS.ACCOUNT.*.CONNECT` fires for the cluster's account key; control-plane updates `hosts.last_seen_at` for the admin's `us-east` row.
5. Admin grants user access: `mclaude cluster grant us-east bob-gmail`. Control-plane creates a NEW `hosts` row for bob with `type='cluster'`, `slug='us-east'`, and the cluster-shared fields **copied** from admin's row (`js_domain`, `leaf_url`, `account_jwt`, `direct_nats_url`, `public_key`), `role='user'`. Generates a per-user JWT for bob scoped to `mclaude.users.bob-gmail.hosts.us-east.>`.
6. Bob's next login response includes `us-east` as a host. SPA can launch sessions there. The cluster-controller-k8s subscribes with wildcard `mclaude.users.*.hosts.us-east.api.projects.provision` and routes both alice's and bob's project requests.

### C. Project creation (control-plane → controller)

1. SPA POSTs `/api/users/{uslug}/projects` with `{name, hostSlug}`.
2. Control-plane writes Postgres `projects` row (`host_id` resolved from `(user_id, hostSlug)`), writes the `mclaude-projects` KV (`{uslug}.{hslug}.{pslug}` → ProjectState), and publishes a NATS request to `mclaude.users.{uslug}.hosts.{hslug}.api.projects.provision`.
3. The relevant controller (controller-k8s if the host is a cluster type, controller-local if it's a machine type) receives the provisioning request via:
   - controller-k8s: subscribed with wildcard `mclaude.users.*.hosts.{cluster-slug}.api.projects.>` — receives provisions for all users granted to its cluster.
   - controller-local: subscribed with `mclaude.users.{uslug}.hosts.{hslug}.api.projects.>` — receives only its own user/host's provisions.
4. Controller creates the resource (K8s `MCProject` CR for cluster hosts, local `~/.mclaude/projects/{pslug}/worktree/` directory for machine hosts) and replies with success/failure.
5. SPA receives the reply via the NATS request roundtrip; UI updates.

### D. Session lifecycle (host-scoped routing throughout)

1. Session create: SPA → `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.create`. Worker session-agent (or BYOH local controller) on that host handles it.
2. Events: session-agent publishes to `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.events.{sslug}`. SPA subscribes via the appropriate connection (direct worker if available, hub-via-leaf otherwise).
3. Lifecycle: session-agent publishes to `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.lifecycle.{sslug}`. Control-plane subscribers (e.g., job-queue lifecycle subscriber per ADR-0034) receive these via the leaf-node link.
4. KV: session state at `mclaude-sessions` key `{uslug}.{hslug}.{pslug}.{sslug}`.

## Component Changes

### `mclaude-common/pkg/subj`

The host-scoped helpers exist and stay (4-arg `SessionsKVKey`, 3-arg `ProjectsKVKey`, etc.). Listed for completeness:
- `UserHostProjectAPISessionsCreate(u, h, p)`, `…Input`, `…Delete`, `…Control`
- `UserHostProjectAPITerminal(u, h, p, suffix)`
- `UserHostProjectLifecycle(u, h, p, s)`
- `UserHostProjectEvents(u, h, p, s)`
- `SessionsKVKey(u, h, p, s)`, `ProjectsKVKey(u, h, p)`, `HostsKVKey(u, h)`

**Removals required by this ADR** (these helpers exist in the package today but produce subjects that are eliminated):
- `ClusterAPIProjectsProvision` — emits `mclaude.clusters.{cslug}.api.projects.provision`. Eliminated; cluster controllers subscribe to user-level wildcards on host-scoped subjects instead.
- `ClusterAPIStatus` — emits `mclaude.clusters.{cslug}.api.status`. Eliminated; cluster liveness flows through `$SYS` events, not a dedicated subject.
- `UserHostStatus` — emits `mclaude.users.{uslug}.hosts.{hslug}.status`. Eliminated; daemon does not publish heartbeats anymore.

Also moves: `FormatNATSCredentials` from `mclaude-control-plane` into `mclaude-common/pkg/nats/creds.go` so the CLI can reuse it for BYOH bootstrap.

Adds: `mclaude-common/pkg/nats/operator-keys.go` — used by the `mclaude-cp init-keys` Helm Job to generate operator + account NKey pairs and JWTs.

### `mclaude-session-agent`

- **`state.go`**: add `HostSlug` to `DaemonConfig`; add `hostSlug` to `Agent` (already required by ADR-0034); update `JobEntry`, `ProjectState`, `SessionState` if they're missing `hostSlug` (ADR-0034 already adds it to JobEntry); update `sessionKVKey` and `heartbeatKVKey` wrappers to take host slug (ADR-0034 already covers this).
- **`agent.go`**: 7 enumerated call sites — replace `subj.UserProject…` with `subj.UserHostProject…`, source `hostSlug` from `a.hostSlug`. Specifically: line 285 (sessions.create), line 547 (terminal), lines 1136–1179 (4× lifecycle publishes).
- **`daemon.go`** — these are the full set of edits required to make the daemon compile:
  - **Add** `HostSlug` to `DaemonConfig`; populate from `HOST_SLUG` env (K8s) or `--host` flag (BYOH); fail-fast at startup on absence with `FATAL: HOST_SLUG required`.
  - **Remove** `UserAPIProjectsCreate` subscription (currently at line 126) — project provisioning is owned by `mclaude-controller-{k8s,local}` per this ADR; the session-agent daemon no longer subscribes to project-create.
  - **Remove** `LaptopsKVKey` invocations (currently at lines 148 and 170) — replaced by nothing; the daemon does not write to host KV.
  - **Remove** `laptopsKV` field on the daemon struct (line 57) and any `mclaude-laptops` bucket open call (line 85). The bucket no longer exists.
  - **Remove** the `runLaptopHeartbeat` goroutine entirely. Liveness moves to `$SYS` (control-plane subscribes on hub).
- **`daemon_jobs.go`**: 5 enumerated call sites at lines 342, 377, 438, 491, 635 — already covered by ADR-0034's "Modify all subject/KV key construction" section.
- **`main.go`**: hardcoded lifecycle init subject at line 200 — replace with the host-scoped form.
- **Constants / package-level**: `kvBucketHeartbeats` constant deleted; `hbKV` field removed from daemon; any reference to `mclaude-laptops` / `mclaude-heartbeats` deleted.
- **Pod env vars**: when running as a K8s session-agent, the pod must be launched with `USER_SLUG`, `HOST_SLUG`, `PROJECT_SLUG` env vars set. The cluster controller injects these via `buildPodTemplate` (see `mclaude-controller` changes).

### `mclaude-control-plane` → splits into `mclaude-control-plane` + `mclaude-controller-k8s` + `mclaude-controller-local`

- **`mclaude-control-plane`** (existing binary, scope reduced):
  - Remove all K8s client code (`reconciler.go`, `nkeys.go` partial — `IssueHostJWT` stays here as it's auth, not K8s).
  - Add `hosts` table DDL + migration in `db.go`. Schema per Data Model below.
  - Add `host_id` column to `projects` table; new `UNIQUE(user_id, host_id, slug)` constraint; remove old `UNIQUE(user_id, slug)` if present.
  - Add HTTP endpoints: `GET/POST/PUT/DELETE /api/users/{uslug}/hosts`, `POST /api/users/{uslug}/hosts/code`, `GET /api/users/{uslug}/hosts/code/{code}` (poll status), `POST /api/hosts/register`, admin endpoints `POST/GET /admin/clusters`, `POST /admin/clusters/{cslug}/grants` (manage user-cluster access).
  - Add `IssueHostJWT(userId, hostSlug)` — issues per-host user JWT scoped to `mclaude.users.{uslug}.hosts.{hslug}.>`.
  - Add NATS publisher: on project create/update/delete, publish to `mclaude.users.{uslug}.hosts.{hslug}.api.projects.{create,update,delete}` (request/reply, 10s timeout) instead of touching K8s. The host-scoped subject ensures the request routes to the appropriate controller (controller-k8s subscribes via wildcard for cluster hosts; controller-local subscribes specifically for its own user/host).
  - Subscribe to `$SYS.ACCOUNT.{accountKey}.CONNECT/DISCONNECT` on hub NATS; map account-id-to-host-slug via the host's NKey public key; update `hosts.last_seen_at`.
  - Move `FormatNATSCredentials` from `mclaude-control-plane` into `mclaude-common/pkg/nats/creds.go` so the CLI can reuse it for BYOH bootstrap.
- **`mclaude-controller-k8s`** (new binary, kubebuilder operator):
  - Reconciles `MCProject` CRs (existing behavior, moved out of control-plane).
  - Subscribes to `mclaude.users.*.hosts.{cluster-slug}.api.projects.>` (wildcard at user level — receives provisioning requests for all users granted access to this cluster). The cluster's slug is configured at deploy time (Helm value `clusterSlug=us-east`).
  - `buildPodTemplate` injects `USER_SLUG`, `HOST_SLUG`, `PROJECT_SLUG` env vars from the request's subject (parsed) and the project's host row.
  - Worker cluster's NATS leaf-link via `$SYS.ACCOUNT.{accountKey}.CONNECT` automatically updates the admin's host row's `last_seen_at` (control-plane subscribes on hub NATS). No separate cluster status subject needed.
- **`mclaude-controller-local`** (new binary, BYOH-machine variant):
  - Subscribes to `mclaude.users.{uslug}.hosts.{hslug}.api.projects.>` — its own user/host only. Configured at startup from `--host` flag (or `~/.mclaude/active-host` symlink).
  - Manages `mclaude-session-agent` subprocesses via process supervision (`exec.Cmd`, restart-on-crash) instead of K8s reconciler.
  - Maintains `~/.mclaude/projects/{pslug}/worktree/` directories that mirror what the cluster controller would create as PVCs.

### Provisioning request/reply contract

All four `mclaude.users.{uslug}.hosts.{hslug}.api.projects.{provision,create,update,delete}` subjects use NATS request/reply with a 10s timeout (`PROVISION_TIMEOUT_SECONDS`). The contract is shared by both controller variants.

**Request payload** (control-plane → controller):
```json
{
  "userSlug":      "alice-gmail",
  "hostSlug":      "us-east",
  "projectSlug":   "billing",
  "gitUrl":        "https://github.com/alice/billing.git",
  "gitIdentityId": "uuid-or-null"
}
```

**Reply on success:**
```json
{ "ok": true, "projectSlug": "billing" }
```

**Reply on failure:**
```json
{ "ok": false, "error": "human-readable description", "code": "rbac_failed | image_pull_failed | git_clone_failed | not_found | …" }
```

The control-plane treats a NATS request timeout the same as a 503-style reply, surfaces `503 Service Unavailable` to the SPA with `{error: "host {hslug} unreachable"}`, and marks `projects.status = 'failed'` (or rolls back the row, implementation choice). Delete is idempotent: if the project is already gone, the controller still replies `{ok: true}`.

The controller-spec at `docs/mclaude-controller/spec-controller.md` is the authoritative reference; the schema here is repeated so the ADR is self-sufficient.

### `mclaude-cli`

- New `host` subcommand:
  - `mclaude host register [--name <name>]` — device-code registration flow.
  - `mclaude host list` — show all hosts the user owns / has access to.
  - `mclaude host use <hslug>` — symlink `~/.mclaude/active-host`.
  - `mclaude host rm <hslug>` — call `DELETE /api/users/{uslug}/hosts/{hslug}`.
- New `cluster` subcommand (admin-only):
  - `mclaude cluster register --slug <cslug> [--name <display>] --jetstream-domain <jsd> --leaf-url <url> [--direct-nats-url <wss>]` — calls `POST /admin/clusters` with `{slug, name, jsDomain, leafUrl, directNatsUrl?}`. `--slug` is required (it becomes the `hosts.slug` for every user granted to this cluster, and is the literal token in the controller's wildcard subscription `mclaude.users.*.hosts.{slug}.>`). `--name` defaults to the slug when omitted. Returns `{slug, leafJwt, leafSeed, accountJwt, operatorJwt, jsDomain, directNatsUrl}` for the admin to drop into the worker cluster's NATS Secret + `mclaude-worker` Helm values.
  - `mclaude cluster grant <cluster-slug> <uslug>` — grants a user access. Calls `POST /admin/clusters/{cluster-slug}/grants` with `{userSlug}`. Control-plane creates a new `hosts` row for that user with `slug=<cluster-slug>`, `type='cluster'`, `role='user'`, copies the cluster-shared fields (`js_domain`, `leaf_url`, `account_jwt`, `direct_nats_url`, `public_key`) from the existing cluster host row, and mints a per-user JWT.
- Daemon mode: `mclaude daemon --host <hslug>` (or read from `~/.mclaude/active-host`).

### `mclaude-web` (SPA)

- **`src/lib/subj.ts`**: every project-scoped builder takes `hslug` as an additional parameter. Update `subjSessionsInput`, `subjSessionsCreate`, `subjSessionsDelete`, `subjSessionsControl`, `subjTerminal`, `subjLifecycle`, `subjEvents`, `kvKeySession`, `kvKeyProject`. Rename `kvKeyLaptop` → `kvKeyHost`.
- **AuthStore**: new accessors `getProjects()`, `getHosts()`, `getClusters()`, `getJwt()`, `getNkeySeed()`. Login response shape per Data Model.
- **SessionStore**: open per-cluster JetStream KV watch with `domain` from project's `jsDomain`; aggregate session lists across hosts.
- **EventStore**: dual-NATS strategy. Hub connection always open. On project selection, attempt direct worker connection using `directNatsUrl`; fall back to hub-via-leaf if unreachable.
- **Routes**: `/u/{uslug}/h/{hslug}/p/{pslug}/s/{sslug}` for project/session detail; host picker in dashboard; Settings → Hosts screen.
- **Component-local spec required**: `docs/ui/mclaude-web/spec-host-picker.md` is created in this commit (host picker UI behavior). The `docs/ui/mclaude-web/` folder already exists for SPA-screen specs; the host picker joins the dashboard / session-detail / settings-web siblings.

### `mclaude-helm`

- Split into two charts:
  - `charts/mclaude-cp/` — control-plane + hub NATS + Postgres + SPA. Hub NATS configured with `leafnodes { listen: 0.0.0.0:7422 }` + JWT auth chain.
  - `charts/mclaude-worker/` — worker NATS + `mclaude-controller-k8s` + session-agent template. Worker NATS configured with `leafnodes { remotes: [{url: nats-leaf://hub:7422, credentials: /etc/nats/leaf.creds}] }` + `jetstream { domain: $JS_DOMAIN }`. The `.creds` file contains the leaf JWT + NKey seed returned by `mclaude cluster register` (the 3-tier JWT chain requires `credentials:`, not raw `nkey:`).
- Single-cluster install: deploy both charts into the same K8s cluster with `leaf-url=localhost:7422`. SPA login response returns one cluster, JetStream domain present, behavior identical to multi-cluster except domain values are local.

## Data Model

### Postgres `hosts` table (single source of truth)

```sql
CREATE TABLE hosts (
  id              TEXT PRIMARY KEY,
  user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  slug            TEXT NOT NULL,
  name            TEXT NOT NULL,
  type            TEXT NOT NULL CHECK (type IN ('machine', 'cluster')),
  role            TEXT NOT NULL DEFAULT 'owner' CHECK (role IN ('owner', 'user')),

  -- Cluster-shared infrastructure fields (NULL for machine hosts).
  -- For cluster hosts, these are duplicated across all user rows referencing
  -- the same cluster. Admin update propagates via UPDATE WHERE slug='...'.
  js_domain       TEXT,
  leaf_url        TEXT,
  account_jwt     TEXT,
  direct_nats_url TEXT,                       -- WebSocket URL for SPA direct-to-worker (NULL for machine; optional for cluster)

  -- NATS identity.
  -- Machine: host-generated NKey pubkey (private seed never leaves machine).
  -- Cluster: cluster controller / leaf JWT NKey pubkey (CP-generated at register;
  -- duplicated across user rows for the same cluster — see decisions table).
  public_key      TEXT,
  user_jwt        TEXT,                       -- per-user JWT scoped to mclaude.users.{uslug}.hosts.{hslug}.>

  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  last_seen_at    TIMESTAMPTZ,                -- updated by $SYS subscriber; durable historical record

  UNIQUE (user_id, slug),
  CHECK (type = 'machine' OR (js_domain IS NOT NULL AND leaf_url IS NOT NULL AND account_jwt IS NOT NULL))
);
```

The `online` boolean is **not** in this table — it lives in the `mclaude-hosts` KV bucket (single-writer control-plane, sourced from `$SYS.ACCOUNT.*.CONNECT/DISCONNECT`).

The `users` table (canonical reference: `docs/spec-state-schema.md`) gains two columns at the same migration: `oauth_id TEXT NULL` (provider's stable user identifier; populated on first OAuth callback whose email matches) and `is_admin BOOLEAN NOT NULL DEFAULT FALSE` (gate for `/admin/*` endpoints; set TRUE for the bootstrap admin email by the `init-keys` Helm Job).

**Key invariants:**
- `(user_id, slug)` is unique. Slugs are user-scoped, but for cluster hosts the admin grant flow ensures all users granted to a cluster receive the **same** slug — by convention, the cluster's canonical name. This is what enables the wildcard subscription `mclaude.users.*.hosts.us-east.…` to route across all users.
- For machine hosts: `cluster-shared fields are NULL`; `slug` is user-chosen during `mclaude host register`.
- For cluster hosts: shared fields populated; `slug` is admin-controlled (shared across users) and matches the cluster's canonical name.

The previous `clusters` table is **dropped**. Admin operations on cluster shared fields update all `hosts` rows where `slug = '<cluster-slug>'` and `type = 'cluster'` in a single statement.

### Postgres `projects` table changes

```sql
ALTER TABLE projects ADD COLUMN host_id TEXT NOT NULL REFERENCES hosts(id) ON DELETE CASCADE;
DROP INDEX IF EXISTS projects_user_id_slug_uniq;
CREATE UNIQUE INDEX projects_user_id_host_id_slug_uniq ON projects (user_id, host_id, slug);
```

Migration: backfill `host_id` to the user's default `local` machine host (created in the same migration).

### Operator + account NKeys (deployment-level, K8s-only)

The operator and account keys live in a single K8s Secret in the central `mclaude-cp` cluster:
```
Secret: mclaude-system/operator-keys
Keys:   operatorJwt, accountJwt, accountSeed, operatorSeed
Mode:   0600 / type: Opaque
```

Generated on first deploy by the Helm pre-install Job (`mclaude-cp init-keys`). Subsequent deploys check existence and exit without regenerating. Hub NATS Helm template references the Secret for its `resolver_preload`. Worker NATS gets `accountJwt` + `operatorJwt` injected at cluster-registration time (returned in the cluster register response, placed into the worker's NATS Secret by the admin during `helm install mclaude-worker`).

BYOH machines do NOT receive these keys. They get only their per-host user JWT (signed by the account key in control-plane), which is sufficient to authenticate as a NATS client to hub NATS.

### NKey / JWT issuance

Per-host user JWT is signed by the account signing key with these permissions:
```
publish:   mclaude.users.{uslug}.hosts.{hslug}.>, _INBOX.>, $JS.*.API.>, $SYS.ACCOUNT.*.CONNECT, $SYS.ACCOUNT.*.DISCONNECT
subscribe: mclaude.users.{uslug}.hosts.{hslug}.>, _INBOX.>, $JS.*.API.>
```

Cluster controller JWT (issued at cluster registration; doubles as the worker NATS leaf-node JWT):
```
publish:   mclaude.users.*.hosts.{cluster-slug}.>, _INBOX.>, $JS.*.API.>, $SYS.ACCOUNT.*.CONNECT, $SYS.ACCOUNT.*.DISCONNECT
subscribe: mclaude.users.*.hosts.{cluster-slug}.>, _INBOX.>, $JS.*.API.>
```
The wildcard `users.*` is what enables the controller to receive provisioning requests from every user granted access to its cluster. Hub NATS validates the JWT signature against the operator/account chain on connection.

### `mclaude-hosts` KV

Replaces `mclaude-laptops`. Created by control-plane.

```
Key:   {uslug}.{hslug}
Value: { slug, name, type, role, lastSeenAt, online, ... }
History: 1
```

### Login response shape

```json
{
  "user": { "id": "...", "slug": "alice-gmail" },
  "jwt": "...",
  "nkeySeed": "...",
  "hubUrl": "wss://hub.mclaude.example/nats",
  "hosts": [
    {
      "slug": "mbp16",
      "name": "alice's MBP",
      "type": "machine",
      "role": "owner",
      "online": true,
      "lastSeenAt": "..."
    },
    {
      "slug": "us-east",
      "name": "Production K8s",
      "type": "cluster",
      "role": "user",
      "online": true,
      "lastSeenAt": "...",
      "jsDomain": "us-east",
      "directNatsUrl": "wss://us-east.mclaude.example/nats"
    }
  ],
  "projects": [
    { "slug": "myrepo", "name": "My Repo", "hostSlug": "mbp16", "hostType": "machine" },
    { "slug": "billing", "name": "billing service", "hostSlug": "us-east", "hostType": "cluster", "jsDomain": "us-east", "directNatsUrl": "wss://us-east.mclaude.example/nats" }
  ]
}
```

The `hosts` array is the single source of truth. SPA filters `hosts.filter(h => h.type === 'cluster')` to discover clusters when needed (e.g., for direct-worker connection setup using `directNatsUrl` and `jsDomain` fields on cluster-type hosts).

## Error Handling

| Failure | Handling |
|---------|----------|
| Session-agent or daemon starts without `HOST_SLUG`/`--host` | Hard fail at startup with `FATAL: HOST_SLUG required (set via env or --host flag)`. No fallback. |
| User JWT signed for the wrong host | NATS auth rejects the publish/subscribe; SPA receives a connection error; host picker surfaces "credentials invalid for this host." |
| Worker NATS leaf-node link drops | JetStream domain reads via the hub fail (NATS routes domain queries through the leaf). SPA falls back to direct worker connection if `directNatsUrl` is reachable; if not, marks the cluster offline. Sessions on that worker continue running locally and resync when the leaf reconnects. |
| `$SYS` presence event arrives for unknown account | Logged at info level; ignored. Likely a misconfigured client. |
| Device-code registration: code expired (>10 min) | `POST /api/hosts/register` returns 410 Gone with `{"error": "code expired, restart registration"}`. |
| Device-code: code already redeemed | Returns 409 Conflict. CLI prompts user to restart. |
| Project create on offline cluster | Control-plane's NATS request to the worker times out (10s); HTTP returns 503 with `{"error": "cluster {cslug} unreachable"}`. SPA shows the host as offline; project creation queued for retry not implemented in v1. |
| Force re-register: legacy creds in use | NATS auth rejects; client gets clear error message ("legacy credentials no longer valid; run `mclaude host register`"). |

## Security

- Per-host NATS credentials limit blast radius: a leaked host JWT only allows access to that host's subjects.
- Cluster controller credentials are admin-issued and rotate via re-registration. Stored on disk at `/etc/mclaude/cluster.creds` (cluster) or `~/.mclaude/hosts/{hslug}/creds.json` (BYOH machine), `0600` permissions.
- Operator + account keys are the bootstrap root. Stored only as a K8s Secret (`mclaude-system/operator-keys`) in the central `mclaude-cp` cluster, at `0600`. Access mediated by control-plane only — no other component reads them. BYOH machines never possess these keys; they hold only their per-host user JWT (signed by the account key remotely).
- `$SYS.ACCOUNT.>` subscription on control-plane is read-only and account-scoped.
- The 3-tier JWT chain provides crypto-verified isolation: even if a worker NATS is compromised, the operator key can revoke an account JWT and re-issue, invalidating all derived user JWTs.

## Impact

**Specs updated in this commit:**
- `docs/spec-state-schema.md` — `hosts` table DDL, `projects.host_id` column, NATS auth resolver config (3-tier JWT chain on hub + workers), JetStream domain config, `mclaude-hosts` bucket, removal of `mclaude-heartbeats` references, login response shape.
- `docs/mclaude-control-plane/spec-control-plane.md` — project creation flow rewritten (NATS-based provisioning to controller, no K8s touch); admin endpoints for clusters; host endpoints; remove all K8s reconciler content.
- `docs/mclaude-session-agent/spec-session-agent.md` — `HOST_SLUG` configuration; host-scoped subject patterns; remove `mclaude-laptops` references.
- `docs/ui/mclaude-web/spec-host-picker.md` — created (component-local spec for host picker UI; joins existing `docs/ui/mclaude-web/` SPA specs).
- `docs/charts-mclaude/spec-helm.md` — updated for the chart split (mclaude-cp vs mclaude-worker). The existing single-chart spec is rewritten in place.
- `docs/mclaude-controller/spec-controller.md` — created (new component folder; covers both K8s and local variants).

**Components implementing the change:**
- `mclaude-common` — only the move of `FormatNATSCredentials` into `pkg/nats/creds.go` (mclaude-common is already host-scoped).
- `mclaude-control-plane` — reduced scope: drop K8s client, add host endpoints, add NATS publishing of project commands, add `IssueHostJWT`.
- `mclaude-controller-k8s` (new binary) — kubebuilder operator extracted from control-plane.
- `mclaude-controller-local` (new binary) — process supervisor for BYOH machines.
- `mclaude-session-agent` — host-slug plumbing in `Agent`/`DaemonConfig`/`state.go`/all subj call sites; remove heartbeat code.
- `mclaude-cli` — `host` and `cluster` subcommands; `daemon --host` flag; `~/.mclaude/hosts/` directory management.
- `mclaude-web` — host-scoped subj.ts; AuthStore/SessionStore/EventStore extensions; host picker; routes with `{hslug}`.
- `mclaude-helm` — chart split.

**ADRs marked superseded:**
- `adr-0004-multi-laptop.md` — supersession note prepended.
- `adr-0011-multi-cluster.md` — status changed to `superseded`; note prepended.
- `adr-0014-controller-separation.md` — status changed to `superseded`; note prepended.

## Scope

**v1 (in scope):**
- `hosts` table DDL + Postgres migration (single table, no `clusters`).
- `projects.host_id` column + new `UNIQUE(user_id, host_id, slug)` index.
- Operator/account NKey auto-generation by Helm pre-install Job; persistence to K8s Secret `mclaude-system/operator-keys`.
- Per-host user JWT issuance (`IssueHostJWT`).
- Hub NATS Helm config: 3-tier JWT trust chain (`operator + resolver: MEMORY + resolver_preload`).
- Worker NATS Helm config: leaf-node remote + JetStream domain.
- Separate `mclaude-cp` and `mclaude-worker` Helm charts.
- Single-cluster degenerate case: both charts in the same K8s cluster, leaf URL = `localhost:7422`.
- `mclaude-controller-k8s` extracted as new binary (kubebuilder operator from existing reconciler code).
- `mclaude-controller-local` new binary (BYOH process supervisor).
- `mclaude-control-plane` reduced to HTTP+NATS+Postgres; K8s client removed; NATS-based provisioning publish.
- Host endpoints: `GET/POST/PUT/DELETE /api/users/{uslug}/hosts`, `POST /api/users/{uslug}/hosts/code`, `GET /api/users/{uslug}/hosts/code/{code}` (device-code poll), `POST /api/hosts/register`.
- Admin endpoints: `POST/GET /admin/clusters`, `POST /admin/clusters/{cslug}/grants`.
- `$SYS.ACCOUNT.*.CONNECT/DISCONNECT` subscription on hub; `hosts.last_seen_at` updates.
- `mclaude-session-agent`: `Agent.hostSlug` field, `DaemonConfig.HostSlug` field, all 7 enumerated subj call-site fixes (closes the compile failure), `state.go` struct + wrapper updates.
- `mclaude-cli`: `host` subcommand (register/list/use/rm), `cluster` subcommand (register/grant), `daemon --host` flag.
- `mclaude-web`: host-scoped `subj.ts` builders; AuthStore/SessionStore/EventStore extensions; host picker; routes with `{hslug}`; `/api/users/{uslug}/h/{hslug}/p/{pslug}/...` URL scheme.
- `mclaude-helm` chart split.
- Spec updates: `spec-state-schema.md`, `spec-control-plane.md`, `spec-session-agent.md`. Spec creates: `spec-controller.md`, `spec-host-picker.md`, `spec-helm.md`.
- ADR-0004, ADR-0011, ADR-0014 marked `superseded` with supersession notes prepended.

**Out of scope (deferred):**
- Migration of existing data (clean break — no migration support).
- Backwards-compatible support for legacy user-scoped credentials (deployment is a hard cutover).
- Cluster auto-discovery / federated clusters / inter-cluster routing beyond the hub-via-leaf-node baseline.
- Per-user heartbeat with finer-than-`$SYS` granularity.
- Cluster removal / decommissioning workflows beyond `DELETE /admin/clusters/{cslug}` (which simply deletes the row; cleanup of in-flight sessions is manual).
- Multi-region hubs (single hub assumed).

## Open questions

(All resolved during planning Q&A 2026-04-26. None remaining.)

Resolutions:
- Supersession scope → ADR-0035 supersedes ADR-0004, ADR-0011, AND ADR-0014.
- v1 implementation scope → Full multi-cluster from v1.
- hosts vs clusters table split → Single `hosts` table; cluster-shared fields are columns.
- Machine host provisioning subject → `mclaude.users.{uslug}.hosts.{hslug}.api.projects.provision`. No `mclaude.clusters.{cslug}.>` subjects exist. Cluster controllers subscribe with user-level wildcards.
- Operator/account NKey bootstrap → Helm pre-install Job auto-generates; persists to K8s Secret only. BYOH laptops do not run a control-plane variant.
- First admin user → Helm value `bootstrapAdminEmail`; pre-install Job creates the row.
- Admin CLI auth → Bearer token from `mclaude login`; standard `Authorization: Bearer` header on `/admin/...` endpoints.
- Migration → No migration; clean break. Existing users/projects/credentials are not preserved.
- Heartbeat → `$SYS` only.
- Implementation order → Bottom-up (schema → common → session-agent → control-plane → CLI/web).
- Spec file creation → Yes, `spec-controller.md`, `spec-host-picker.md`, `spec-helm.md` are created as part of this ADR's commit (component-local specs introduced lazily per ADR-0020).

## Implementation Plan

Bottom-up merge order — each commit must compile. Stages 1–3 unblock everything else; stages 4+ can run in parallel.

| Stage | Component | Work | New/changed lines (est.) | Dev-harness tokens (est.) |
|-------|-----------|------|--------------------------|---------------------------|
| 1 | `mclaude-control-plane` (Postgres only) | New `hosts` table DDL + migration; drop `clusters` table from migration script; alter `projects` for `host_id`. | ~80 | 40k |
| 2 | `mclaude-common` | Move `FormatNATSCredentials` from control-plane to `pkg/nats/creds.go`. Add `pkg/nats/operator-keys.go` for auto-generation logic (used by control-plane on first boot). | ~120 | 50k |
| 3 | `mclaude-session-agent` | `Agent.hostSlug` + `DaemonConfig.HostSlug` plumbing; all 7 enumerated subj call-site fixes (lines 285, 547, 1136, 1149, 1163, 1179 in agent.go; 342, 377, 438, 491, 635 in daemon_jobs.go); `state.go` struct/wrapper updates per ADR-0034 + this ADR; `main.go` lifecycle init subject; remove `mclaude-laptops`/`mclaude-heartbeats` references. **Compiles after this stage.** | ~250 | 130k |
| 4 | `mclaude-control-plane` (logic) | Remove K8s client; add NATS publisher for `mclaude.users.*.hosts.*.api.projects.{create,update,delete}`; add `IssueHostJWT`; add operator-key auto-generation on first boot; add `$SYS` subscription; add host CRUD HTTP endpoints; add admin cluster endpoints; remove `MCProject` CR creation logic. | ~600 | 250k |
| 5 | `mclaude-controller-k8s` (new binary) | Extract reconciler from control-plane; add NATS subscriber for `mclaude.users.*.hosts.{cluster-slug}.api.projects.>`; `buildPodTemplate` injects `USER_SLUG`/`HOST_SLUG`/`PROJECT_SLUG`. | ~500 | 200k |
| 6 | `mclaude-controller-local` (new binary) | Process supervisor for session-agent; NATS subscriber for `mclaude.users.{uslug}.hosts.{hslug}.api.projects.>`; manages `~/.mclaude/projects/{pslug}/worktree/`; restart-on-crash. | ~400 | 200k |
| 7 | `mclaude-cli` | `host` subcommand (register/list/use/rm); `cluster` subcommand (register/grant); `daemon --host` flag; `~/.mclaude/hosts/`/`active-host` directory management. | ~400 | 150k |
| 8 | `mclaude-web` | Host-scoped `subj.ts` builders; AuthStore extensions (`getHosts`, `getJwt`, `getNkeySeed`); SessionStore per-host KV watches with `domain` qualification; EventStore dual-NATS strategy; host picker component; routes with `{hslug}`; Settings → Hosts screen. | ~700 | 300k |
| 9 | `mclaude-helm` | Split into `mclaude-cp` and `mclaude-worker` charts; hub NATS leafnodes config; worker NATS leaf-node remote + JetStream domain; templates reference operator-keys Secret. | ~300 | 100k |
| 10 | Specs | Update `spec-state-schema.md`, `spec-control-plane.md`, `spec-session-agent.md`. Create `spec-controller.md`, `spec-host-picker.md`, `spec-helm.md`. | ~600 | 100k |
| 11 | ADR supersession notes | Prepend supersession blocks to ADR-0004, ADR-0011, ADR-0014; flip their statuses to `superseded`. Mechanical edits. | ~30 | 20k |

**Total estimated tokens:** ~1.54M
**Estimated wall-clock:** ~5-7h of dev-harness sustained work, possibly across multiple sessions if quota fires.

The bottom-up order ensures the codebase compiles at every commit:
- After stage 1 (Postgres only): no Go code changes, schema-only commit.
- After stage 2 (mclaude-common): control-plane + session-agent still don't compile (missing host slug plumbing), but mclaude-common itself does.
- **After stage 3 (mclaude-session-agent):** session-agent compiles. This is the critical milestone — it unblocks ADR-0034's implementation.
- Stages 4-10 can interleave; each is independently mergeable as long as the API contract (host endpoints, NATS subjects, JWT format) matches what was agreed in this ADR.
