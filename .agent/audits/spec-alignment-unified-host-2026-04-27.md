# Spec Alignment Audit: ADR-0035 Unified Host Architecture

**Date**: 2026-04-27
**ADR**: `docs/adr-0035-unified-host-architecture.md`
**Status**: `implemented`
**Supersedes**: ADR-0004, ADR-0011, ADR-0014

## Specs audited

1. `docs/spec-state-schema.md`
2. `docs/mclaude-control-plane/spec-control-plane.md`
3. `docs/mclaude-session-agent/spec-session-agent.md`
4. `docs/mclaude-controller/spec-controller.md`
5. `docs/charts-mclaude/spec-helm.md`
6. `docs/mclaude-cli/spec-cli.md`
7. `docs/ui/mclaude-web/spec-host-picker.md`

---

## Phase 1 — ADR Decision → Spec (forward pass)

| # | ADR section / line | ADR text | Spec location | Verdict | Direction | Notes |
|---|-------------------|----------|---------------|---------|-----------|-------|
| 1 | Decisions / Host identity | `hosts` table; `type IN ('machine','cluster')`; `role IN ('owner','user')` | spec-state-schema.md hosts table | IMPLEMENTED | — | Full DDL matches including role check |
| 2 | Decisions / Host identity | `js_domain`, `leaf_url`, `account_jwt`, `direct_nats_url`, `public_key` NULL for machine, populated for cluster | spec-state-schema.md hosts columns | IMPLEMENTED | — | All five columns present with correct nullability |
| 3 | Decisions / Host identity | `online` boolean in `mclaude-hosts` KV, not Postgres; `last_seen_at` in Postgres | spec-state-schema.md hosts table + mclaude-hosts bucket | IMPLEMENTED | — | `last_seen_at` in table; KV bucket has `online` |
| 4 | Decisions / Host identity | No separate `clusters` table | spec-state-schema.md | IMPLEMENTED | — | Schema explicitly drops clusters table |
| 5 | Decisions / Subject scheme | `mclaude.users.{uslug}.hosts.{hslug}.…` only; no `mclaude.clusters.{cslug}.>` subjects | spec-state-schema.md NATS Subjects | IMPLEMENTED | — | Explicit "no mclaude.clusters.*" callout |
| 6 | Decisions / Cluster runtime topology | Hub NATS on CP cluster; each worker NATS leaf-links in; JetStream per-cluster with unique domain | spec-state-schema.md NATS Server Configuration | IMPLEMENTED | — | Hub and worker configs spelled out |
| 7 | Decisions / Single-cluster degenerate case | SPA uses host-qualified subjects; domain qualification conditional on `jsDomain` presence | spec-host-picker.md Connection strategy; spec-state-schema.md NATS Server Config | IMPLEMENTED | — | Both docs cover degenerate case |
| 8 | Decisions / Default machine host | On user creation, write `hosts` row `slug='local'`, `type='machine'`, `role='owner'` | spec-state-schema.md hosts Default machine host | IMPLEMENTED | — | Exact wording matches |
| 9 | Decisions / Migration / existing data | No migration; clean break | spec-helm.md (slug-backfill Job removed) | IMPLEMENTED | — | Helm spec explicitly calls out removal of slug-backfill Job |
| 10 | Decisions / Binary boundary | control-plane: HTTP+NATS+Postgres, zero K8s | spec-control-plane.md Role + Kubernetes Dependency | IMPLEMENTED | — | "K8s-free" language in spec role section |
| 11 | Decisions / Binary boundary | `mclaude-controller-k8s`: kubebuilder operator; subscribes `mclaude.users.*.hosts.{cluster-slug}.api.projects.>` | spec-controller.md Variant 1 | IMPLEMENTED | — | Exact wildcard subscription documented |
| 12 | Decisions / Binary boundary | `mclaude-controller-local`: BYOH process supervisor; subscribes `mclaude.users.{uslug}.hosts.{hslug}.api.projects.>` | spec-controller.md Variant 2 | IMPLEMENTED | — | One-user/one-host subscription documented |
| 13 | Decisions / Provisioning subject | `mclaude.users.{uslug}.hosts.{hslug}.api.projects.provision`; request/reply; 10s timeout | spec-state-schema.md NATS Subjects + spec-controller.md Shared Behavior | IMPLEMENTED | — | Subject, payload, reply shapes match |
| 14 | Decisions / Cluster slug uniqueness | Same slug across all users granted to same cluster; `UNIQUE(user_id, slug)` preserved | spec-state-schema.md hosts constraints | IMPLEMENTED | — | Invariant documented in schema and cluster-shared fields section |
| 15 | Decisions / NATS auth chain | 3-tier operator → account → user JWT; `resolver: MEMORY`; `resolver_preload` | spec-state-schema.md NATS Server Configuration | IMPLEMENTED | — | Both hub and worker configs documented |
| 16 | Decisions / NATS auth chain | One operator and one account per mclaude install | spec-state-schema.md Operator + account NKeys | IMPLEMENTED | — | Explicit "one operator, one account" statement |
| 17 | Decisions / Control-plane deployment topology | Always K8s-hosted; no local/standalone variant | spec-control-plane.md Deployment | IMPLEMENTED | — | "Runs as a Kubernetes Deployment ... central mclaude-cp cluster" |
| 18 | Decisions / Operator/account key bootstrap | Helm pre-install Job `mclaude-cp init-keys`; writes to Secret `mclaude-system/operator-keys`; idempotent | spec-helm.md Pre-install Job + spec-state-schema.md Operator keys | IMPLEMENTED | — | Both docs match |
| 19 | Decisions / First admin user | `bootstrapAdminEmail` Helm value; `init-keys` Job creates `users` row `is_admin=true, oauth_id=NULL` | spec-helm.md init-keys table + spec-control-plane.md bootstrapAdminEmail env | IMPLEMENTED | — | Covered in both |
| 20 | Decisions / Admin CLI auth | Bearer token from `mclaude login`; `Authorization: Bearer`; `users.is_admin` check | spec-control-plane.md Admin-only endpoints + spec-cli.md | IMPLEMENTED | — | Admin-only section in spec-control-plane and CLI spec describe bearer auth |
| 21 | Decisions / Heartbeat / liveness | `$SYS.ACCOUNT.*.CONNECT/DISCONNECT` only; discriminate on `client.kind` + `client.nkey`; machine vs cluster vs ignore | spec-state-schema.md Hub-side system subjects table | IMPLEMENTED | — | Three-case dispatch table fully documented |
| 22 | Decisions / Heartbeat / liveness | DISCONNECT sets `online=false`, does not rewrite `last_seen_at` | spec-state-schema.md $SYS table + spec-control-plane.md DISCONNECT subscription | IMPLEMENTED | — | Explicit in both |
| 23 | Decisions / Heartbeat / liveness | No periodic heartbeat publishes; no `mclaude-heartbeats` bucket | spec-state-schema.md KV Buckets (removal note) + spec-session-agent.md daemon liveness | IMPLEMENTED | — | Removals explicitly documented |
| 24 | Decisions / SPA NATS connections | Hub always open; direct worker on demand; fallback hub-via-leaf | spec-host-picker.md Connection strategy | IMPLEMENTED | — | Both success and fallback paths documented |
| 25 | Decisions / Login response shape | `{user, jwt, nkeySeed, hubUrl, hosts[], projects[]}`; hosts carry `{slug, name, type, role, online, lastSeenAt, jsDomain?, directNatsUrl?}`; projects carry `{slug, name, hostSlug, hostType, jsDomain?, directNatsUrl?}`; no top-level `clusters` array | spec-state-schema.md Login Response Shape | IMPLEMENTED | — | Shape matches exactly including optional cluster fields |
| 26 | Decisions / Helm chart split | `mclaude-cp` chart (CP + hub NATS + Postgres + SPA); `mclaude-worker` chart (worker NATS + controller-k8s + SA template) | spec-helm.md | IMPLEMENTED | — | Both charts documented |
| 27 | Decisions / Session-agent host slug source | `HOST_SLUG` env var (K8s); `--host` flag (BYOH daemon); required; hard-fail on absence | spec-session-agent.md Configuration table + Error Handling | IMPLEMENTED | — | `--host` / `HOST_SLUG` row in config table; fatal error row |
| 28 | Component Changes / mclaude-common/pkg/subj | `UserHostProjectAPISessionsCreate`, `…Input`, `…Delete`, `…Control`, `…Terminal`, `…Lifecycle`, `…Events`, `SessionsKVKey(u,h,p,s)`, `ProjectsKVKey(u,h,p)`, `HostsKVKey(u,h)` | spec-state-schema.md (subject patterns use these forms) | IMPLEMENTED | — | All subject patterns in state schema use host-scoped 4-arg form |
| 29 | Component Changes / mclaude-common removals | `ClusterAPIProjectsProvision`, `ClusterAPIStatus`, `UserHostStatus` removed | spec-state-schema.md (no mclaude.clusters.* subjects; no UserHostStatus pattern) | IMPLEMENTED | — | No cluster-scoped or heartbeat subjects appear in state schema |
| 30 | Component Changes / mclaude-common | Move `FormatNATSCredentials` to `mclaude-common/pkg/nats/creds.go`; add `pkg/nats/operator-keys.go` | spec-control-plane.md Dependencies + spec-cli.md Dependencies | PARTIAL | SPEC→FIX | Neither spec mentions `pkg/nats/creds.go` or `pkg/nats/operator-keys.go` by filename. The move/add are implementation details (not behavioral), but the spec should note the `init-keys` logic location. The control-plane spec does reference `init-keys` behavior but not the common package location. Low-impact omission. |
| 31 | Component Changes / mclaude-session-agent | `HostSlug` in `DaemonConfig`; `hostSlug` in `Agent`; 7 enumerated call-site fixes in agent.go + daemon_jobs.go; `state.go` struct/wrapper updates | spec-session-agent.md Configuration + Internal Behavior | IMPLEMENTED | — | `HOST_SLUG` / `--host` in config table; host-scoped subjects throughout |
| 32 | Component Changes / mclaude-session-agent | Remove `UserAPIProjectsCreate` subscription from daemon; project provisioning owned by controller | spec-session-agent.md Subscribe section | IMPLEMENTED | — | Spec explicitly states daemon does not subscribe to project-creation requests |
| 33 | Component Changes / mclaude-session-agent | Remove `LaptopsKVKey`, `laptopsKV`, `mclaude-laptops` bucket; remove `runLaptopHeartbeat` goroutine | spec-session-agent.md KV Buckets + Daemon liveness | IMPLEMENTED | — | Spec calls out removal of mclaude-laptops and mclaude-heartbeats buckets; no heartbeat goroutine described |
| 34 | Component Changes / mclaude-session-agent | Remove `kvBucketHeartbeats` constant, `hbKV` field | spec-session-agent.md | IMPLEMENTED | — | Neither constant nor field appears in spec |
| 35 | Component Changes / mclaude-session-agent | Pod env vars `USER_SLUG`, `HOST_SLUG`, `PROJECT_SLUG` injected by `buildPodTemplate` | spec-state-schema.md Deployment pod env vars section | IMPLEMENTED | — | All three listed in Deployment pod env vars |
| 36 | Component Changes / mclaude-control-plane | Remove all K8s client code (`reconciler.go`); `IssueHostJWT` stays | spec-control-plane.md Kubernetes Dependency | IMPLEMENTED | — | Explicit "no K8s client" statement |
| 37 | Component Changes / mclaude-control-plane | `hosts` table DDL + migration; `host_id` column on `projects`; new UNIQUE constraint | spec-state-schema.md Postgres hosts + projects | IMPLEMENTED | — | Full DDL in state schema |
| 38 | Component Changes / mclaude-control-plane | HTTP endpoints: `GET/POST/PUT/DELETE /api/users/{uslug}/hosts`, `POST /api/users/{uslug}/hosts/code`, `GET /api/users/{uslug}/hosts/code/{code}`, `POST /api/hosts/register` | spec-control-plane.md HTTP Endpoints | IMPLEMENTED | — | All four patterns present |
| 39 | Component Changes / mclaude-control-plane | Admin endpoints: `POST/GET /admin/clusters`, `POST /admin/clusters/{cslug}/grants` | spec-control-plane.md Admin-only endpoints | IMPLEMENTED | — | Both present |
| 40 | Component Changes / mclaude-control-plane | `OPERATOR_KEYS_PATH` env var | spec-control-plane.md Environment Variables | IMPLEMENTED | — | Present in env table |
| 41 | Component Changes / mclaude-control-plane | Subscribe `$SYS.ACCOUNT.{accountKey}.CONNECT/DISCONNECT`; update `hosts.last_seen_at` | spec-control-plane.md NATS Subjects Subscribes + spec-state-schema.md | IMPLEMENTED | — | Both documents cover this |
| 42 | Component Changes / mclaude-controller-k8s | New binary; `CLUSTER_SLUG` env; wildcard subscription; `buildPodTemplate` injects `USER_SLUG`, `HOST_SLUG`, `PROJECT_SLUG` | spec-controller.md Variant 1 | IMPLEMENTED | — | All details documented |
| 43 | Component Changes / mclaude-controller-local | New binary; process supervisor; `~/.mclaude/projects/{pslug}/worktree/`; restart-on-crash | spec-controller.md Variant 2 | IMPLEMENTED | — | All details documented |
| 44 | Provisioning contract / Request payload | `{userSlug, hostSlug, projectSlug, gitUrl, gitIdentityId}` | spec-controller.md Shared Behavior | IMPLEMENTED | — | Payload matches exactly |
| 45 | Provisioning contract / Reply success | `{ok: true, projectSlug}` | spec-controller.md Shared Behavior | IMPLEMENTED | — | Matches |
| 46 | Provisioning contract / Reply failure | `{ok: false, error, code}` with enumerated code values | spec-controller.md Shared Behavior | IMPLEMENTED | — | Code values documented |
| 47 | Provisioning contract / Timeout handling | NATS timeout treated as 503; control-plane returns 503 `{error: "host {hslug} unreachable"}` | spec-control-plane.md Error Handling + spec-controller.md | IMPLEMENTED | — | Both specs cover timeout→503 |
| 48 | Provisioning contract / Delete idempotent | Controller replies `{ok: true}` even if already gone | spec-controller.md Error Handling | IMPLEMENTED | — | "Idempotent: reply {ok: true} even if already gone" |
| 49 | Component Changes / mclaude-cli | `mclaude host register [--name]` — device-code flow | spec-cli.md host register section | IMPLEMENTED | — | Full flow documented |
| 50 | Component Changes / mclaude-cli | `mclaude host list` | spec-cli.md host list section | IMPLEMENTED | — | Documented |
| 51 | Component Changes / mclaude-cli | `mclaude host use <hslug>` | spec-cli.md host use section | IMPLEMENTED | — | Documented |
| 52 | Component Changes / mclaude-cli | `mclaude host rm <hslug>` | spec-cli.md host rm section | IMPLEMENTED | — | Documented |
| 53 | Component Changes / mclaude-cli | `mclaude cluster register --slug --name --jetstream-domain --leaf-url [--direct-nats-url]` | spec-cli.md cluster register section | IMPLEMENTED | — | All flags present |
| 54 | Component Changes / mclaude-cli | `mclaude cluster grant <cluster-slug> <uslug>` | spec-cli.md cluster grant section | IMPLEMENTED | — | Documented |
| 55 | Component Changes / mclaude-cli | `mclaude daemon --host <hslug>` flag | spec-cli.md daemon section | IMPLEMENTED | — | Flag and fallback to active-host symlink documented |
| 56 | Component Changes / mclaude-web | `src/lib/subj.ts` builders take `hslug`; rename `kvKeyLaptop` → `kvKeyHost` | spec-host-picker.md Connection strategy; spec-state-schema.md (host-scoped patterns) | IMPLEMENTED | — | Connection strategy uses host-scoped patterns; kvKeyHost not named but the behavioral change is covered |
| 57 | Component Changes / mclaude-web | `AuthStore` accessors `getProjects()`, `getHosts()`, `getClusters()`, `getJwt()`, `getNkeySeed()` | spec-host-picker.md Dependencies | IMPLEMENTED | — | AuthStore extensions documented |
| 58 | Component Changes / mclaude-web | `SessionStore`: per-cluster JetStream KV watch with `domain`; aggregate across hosts | spec-host-picker.md Connection strategy + SessionStore extension row | IMPLEMENTED | — | jsDomain qualification documented |
| 59 | Component Changes / mclaude-web | `EventStore`: dual-NATS; hub always; direct worker on demand; fallback | spec-host-picker.md Connection strategy | IMPLEMENTED | — | All three paths covered |
| 60 | Component Changes / mclaude-web | Routes `/u/{uslug}/h/{hslug}/p/{pslug}/s/{sslug}`; host picker; Settings → Hosts | spec-host-picker.md Routes + Where it appears | IMPLEMENTED | — | Full URL scheme and all three surfaces documented |
| 61 | Data Model / hosts DDL | `CHECK (type = 'machine' OR (js_domain IS NOT NULL AND leaf_url IS NOT NULL AND account_jwt IS NOT NULL))` constraint | spec-state-schema.md hosts constraints | IMPLEMENTED | — | Exact constraint present |
| 62 | Data Model / projects changes | `ALTER TABLE projects ADD COLUMN host_id TEXT NOT NULL`; `UNIQUE(user_id, host_id, slug)` | spec-state-schema.md projects table | IMPLEMENTED | — | Column and index present |
| 63 | Data Model / users table | `oauth_id TEXT NULL`; `is_admin BOOLEAN NOT NULL DEFAULT FALSE` | spec-state-schema.md users table | IMPLEMENTED | — | Both columns in users table |
| 64 | Data Model / Operator keys Secret | `mclaude-system/operator-keys`; keys `operatorJwt, accountJwt, accountSeed, operatorSeed`; mode 0600 | spec-state-schema.md Operator keys + spec-helm.md | IMPLEMENTED | — | Both docs match |
| 65 | Data Model / Per-host user JWT permissions | `publish: mclaude.users.{uslug}.hosts.{hslug}.>, _INBOX.>, $JS.*.API.>, $SYS.ACCOUNT.*.CONNECT, $SYS.ACCOUNT.*.DISCONNECT`; `subscribe: mclaude.users.{uslug}.hosts.{hslug}.>, _INBOX.>, $JS.*.API.>` | spec-state-schema.md Per-host user JWT permissions | IMPLEMENTED | — | Exact permissions match |
| 66 | Data Model / Per-cluster leaf JWT permissions | `publish: mclaude.users.*.hosts.{cluster-slug}.>, …`; `subscribe: mclaude.users.*.hosts.{cluster-slug}.>, …` | spec-state-schema.md Per-cluster leaf / controller JWT permissions | IMPLEMENTED | — | Exact permissions match |
| 67 | Data Model / `mclaude-hosts` KV | Key `{uslug}.{hslug}`; value `{slug, name, type, role, lastSeenAt, online}`; history 1; replaces `mclaude-laptops` | spec-state-schema.md mclaude-hosts bucket | IMPLEMENTED | — | Matches including removal note |
| 68 | Data Model / Login response shape | No top-level `clusters` array; SPA derives via `hosts.filter(h => h.type === 'cluster')` | spec-state-schema.md Login Response Shape | IMPLEMENTED | — | Explicit callout |
| 69 | Error handling / HOST_SLUG absent | Hard fail: `FATAL: HOST_SLUG required (set via env or --host flag)` | spec-session-agent.md Error Handling + spec-controller.md Error Handling | IMPLEMENTED | — | Exact message in both |
| 70 | Error handling / Wrong host JWT | NATS auth rejects; host picker surfaces "credentials invalid for this host" | spec-session-agent.md Error Handling + spec-control-plane.md Error Handling | PARTIAL | SPEC→FIX | spec-control-plane.md error table has "User JWT signed for wrong host → NATS auth rejects publishes/subscribes; agent surfaces auth error". The host-picker-surfaced error string "credentials invalid for this host" is not documented in spec-host-picker.md. Minor: user-facing string should appear in host-picker spec. |
| 71 | Error handling / Leaf-node link drop | SPA falls back to direct if `directNatsUrl` reachable; else marks cluster offline; sessions resync on reconnect | spec-host-picker.md Empty/error states | PARTIAL | SPEC→FIX | spec-host-picker.md covers "Cluster host shows offline mid-session" toast and stale appearance, but does not describe the automatic fallback from direct to hub-via-leaf when the direct URL itself fails. The connection strategy section covers the initial direct-vs-hub logic but not the runtime fallback on leaf-link drop. |
| 72 | Error handling / $SYS unknown account | Logged at info, ignored | spec-control-plane.md Error Handling | IMPLEMENTED | — | Present in error table |
| 73 | Error handling / Device-code expired | `POST /api/hosts/register` returns 410 Gone + `{error: "code expired, restart registration"}` | spec-control-plane.md Error Handling | IMPLEMENTED | — | Present |
| 74 | Error handling / Device-code already redeemed | Returns 409 Conflict | spec-control-plane.md Error Handling | IMPLEMENTED | — | Present |
| 75 | Error handling / Project create on offline cluster | 503 `{error: "cluster {cslug} unreachable"}` | spec-control-plane.md Error Handling | PARTIAL | SPEC→FIX | ADR uses "cluster {cslug}" but spec-control-plane.md says "host {hslug} unreachable" consistently. The ADR itself says "cluster {cslug}" in this row but "host {hslug}" in the provisioning section — a small internal ADR inconsistency. The spec-control-plane.md wording (`host {hslug}`) is more general and correct (covers machine hosts too). The ADR should be treated as slightly imprecise here; no code change needed. |
| 76 | Security / Per-host JWT blast radius | Per-host creds limit blast radius | spec-controller.md Authentication + spec-session-agent.md | IMPLEMENTED | — | Scoping documented |
| 77 | Security / Cluster controller creds path | `/etc/mclaude/cluster.creds` (cluster) or `~/.mclaude/hosts/{hslug}/creds.json` (BYOH) | spec-state-schema.md Host credentials directory | PARTIAL | SPEC→FIX | ADR says `~/.mclaude/hosts/{hslug}/creds.json` for BYOH. spec-state-schema.md says `~/.mclaude/hosts/{hslug}/nats.creds` (not `creds.json`). The spec file is internally consistent (uses `nats.creds` throughout) and `nats.creds` is the standard NATS credential file format; the ADR's use of `creds.json` appears to be a typo/inconsistency in the ADR itself. The spec is correct. |
| 78 | Security / Operator keys K8s Secret only; BYOH machines don't receive them | spec-state-schema.md Host credentials + spec-control-plane.md | IMPLEMENTED | — | Documented |
| 79 | Impact / Specs updated | `spec-state-schema.md`, `spec-control-plane.md`, `spec-session-agent.md` updated; `spec-controller.md`, `spec-host-picker.md`, `spec-helm.md` created | All six spec files exist and reference ADR-0035 | IMPLEMENTED | — | All six files verified |
| 80 | Impact / ADRs superseded | ADR-0004, ADR-0011, ADR-0014 marked `superseded` | All three files start with supersession notice + Status: superseded | IMPLEMENTED | — | Verified |
| 81 | User flow A / mclaude host register | NKey pair generated locally; seed written to `~/.mclaude/hosts/{hslug}/nkey.seed` mode 0600 | spec-cli.md host register + spec-state-schema.md Host credentials | IMPLEMENTED | — | Path and mode documented |
| 82 | User flow A / device code | `POST /api/users/{uslug}/hosts/code` with `{publicKey}`; returns 6-char code; 10-min TTL | spec-control-plane.md host/code endpoint | IMPLEMENTED | — | Documented |
| 83 | User flow A / dashboard redeems code | `POST /api/hosts/register` with `{code, name}`; server looks up stored `publicKey`; creates `hosts` row; mints JWT; returns `{slug, jwt, hubUrl}` | spec-control-plane.md api/hosts/register endpoint | IMPLEMENTED | — | All details present |
| 84 | User flow A / CLI polls code | `GET /api/users/{uslug}/hosts/code/{code}` until `completed`; writes `nats.creds`, `config.json`; symlinks `active-host` | spec-cli.md host register + spec-control-plane.md code poll endpoint | IMPLEMENTED | — | Poll endpoint returns `{status, slug, jwt, hubUrl}` on completion |
| 85 | User flow B / cluster register | `POST /admin/clusters`; generates per-cluster NKey; creates `hosts` row `type='cluster'`; mints leaf JWT; returns full credential set | spec-control-plane.md Admin endpoints | IMPLEMENTED | — | Full response fields documented |
| 86 | User flow B / cluster grant | `POST /admin/clusters/{cslug}/grants`; copies cluster-shared fields; mints per-user JWT | spec-control-plane.md Admin endpoints | IMPLEMENTED | — | Documented |
| 87 | User flow C / project creation | SPA POSTs `/api/users/{uslug}/projects`; control-plane writes Postgres + KV; publishes NATS provision; awaits reply | spec-control-plane.md Project Creation Flow | IMPLEMENTED | — | Full 7-step flow documented |
| 88 | User flow D / session lifecycle subjects | `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.create` etc. | spec-state-schema.md NATS Subjects table | IMPLEMENTED | — | All session subjects present |
| 89 | User flow D / KV key | `mclaude-sessions` key `{uslug}.{hslug}.{pslug}.{sslug}` | spec-state-schema.md mclaude-sessions | IMPLEMENTED | — | Key format documented |
| 90 | Scope / `$JS_DOMAIN` hub domain | Hub JetStream domain is `hub` | spec-state-schema.md Hub NATS config | IMPLEMENTED | — | `jetstream.domain: hub` documented |
| 91 | Helm / Hub NATS leafnodes listen | `leafnodes { listen: 0.0.0.0:7422 }` | spec-state-schema.md Hub NATS config + spec-helm.md | IMPLEMENTED | — | Both docs show port 7422 |
| 92 | Helm / Worker NATS leaf-remote + credentials format | `leafnodes { remotes: [{url: $LEAF_URL, credentials: /etc/nats/leaf.creds}] }` (uses `credentials:`, not raw `nkey:`) | spec-state-schema.md Worker NATS config + spec-helm.md | IMPLEMENTED | — | `credentials:` format present in both |
| 93 | Helm / Worker JetStream domain | `jetstream { domain: $JS_DOMAIN }` | spec-state-schema.md Worker NATS + spec-helm.md | IMPLEMENTED | — | Present |
| 94 | Helm / init-keys Job weight -20 | Pre-install hook weight -20 | spec-helm.md Pre-install Job table | IMPLEMENTED | — | Weight -20 documented |
| 95 | Helm / `PROVISION_TIMEOUT_SECONDS` | Default 10s; env var on control-plane | spec-control-plane.md Environment Variables | IMPLEMENTED | — | Row in env table |
| 96 | host-picker / New Project host field | Required Host field above Name; default = `local`; dropdown ordered machine-first then cluster; disabled for offline hosts | spec-host-picker.md New Project sheet | IMPLEMENTED | — | All behaviors documented |
| 97 | host-picker / Dashboard project header pill | Host pill on right; clickable → Settings → Hosts | spec-host-picker.md Dashboard project header | IMPLEMENTED | — | Documented |
| 98 | host-picker / Settings → Hosts screen | Two sections (owner / shared); host detail; Register a new host; device-code modal | spec-host-picker.md Settings → Hosts | IMPLEMENTED | — | Full flow documented |
| 99 | host-picker / Registration modal polling | Polls `GET /api/users/{uslug}/hosts` every 3 seconds; max 10 min | spec-host-picker.md Settings → Hosts | PARTIAL | SPEC→FIX | ADR (User flow A) says CLI polls `GET /api/users/{uslug}/hosts/code/{code}` to detect completion. The spec-host-picker.md describes the SPA-side modal polling `GET /api/users/{uslug}/hosts` (not the code-specific endpoint) every 3s. This is intentionally a different poll path (SPA watches for new host appearance, CLI watches the code status). No gap in behavior, but neither spec cross-references the other's polling strategy explicitly. Minor clarity gap; spec is internally consistent. |
| 100 | host-picker / localStorage default host | `mclaude.defaultHostSlug` key | spec-host-picker.md Selection model | IMPLEMENTED | — | Documented |
| 101 | spec-cli / `mclaude daemon` describes as BYOH local controller | Daemon starts BYOH local controller; subscribes to `mclaude.users.{uslug}.hosts.{hslug}.api.projects.>` | spec-cli.md daemon section | IMPLEMENTED | — | CLI spec's daemon section points to controller behavior |
| 102 | spec-cli / `~/.mclaude/hosts/` directory structure | `nkey.seed`, `nats.creds`, `config.json` | spec-state-schema.md Host credentials directory + spec-cli.md host register | IMPLEMENTED | — | All three files documented |

---

## Phase 2 — Spec Content Without Explicit ADR-0035 Citation

This pass checks whether spec content related to this ADR's domain lacks proper ADR anchoring, producing unnecessary drift risk.

| Spec:section | Classification | Notes |
|---|---|---|
| spec-state-schema.md — `mclaude-job-queue` bucket | INFRA | Defined by ADR-0034; host slug is present in `JobEntry.hostSlug`. Not from ADR-0035 but correctly uses host-scoped pattern. |
| spec-state-schema.md — `MCLAUDE_EVENTS` stream subject pattern `mclaude.users.*.hosts.*.projects.*.events.*` | INFRA | Stream defined before ADR-0035 but updated to host-scoped form. Pattern matches ADR-0035. |
| spec-state-schema.md — `MCLAUDE_LIFECYCLE` stream "not yet created in production code" note | UNSPEC'd | The note "Created by: not yet created in production code (test-only in testutil/deps.go)" is a stale implementation note. ADR-0035 does not address this stream's production status. The spec should either remove this comment or update it to reflect actual production state. SPEC→FIX. |
| spec-state-schema.md — Lifecycle event payloads (session_created, _stopped, etc.) | INFRA | Defined by ADR-0034 lifecycle changes, not ADR-0035 directly; subject pattern now host-scoped per ADR-0035. |
| spec-control-plane.md — `DELETE /admin/clusters/{cslug}` endpoint | INFRA | Present in spec but ADR-0035's Scope section lists it as "out of scope for v1" ("Cluster removal / decommissioning workflows beyond `DELETE /admin/clusters/{cslug}`" is deferred). The endpoint itself is spec'd — only the cleanup semantics (in-flight sessions) are deferred. Consistent. |
| spec-control-plane.md — OAuth provider integration section | INFRA | Predates ADR-0035; updated to use host-scoped `mclaude.users.{uslug}.hosts.{hslug}.api.projects.update` subjects for credential sync. Consistent. |
| spec-session-agent.md — "Daemon: Child Process Supervision (legacy single-binary mode)" section | UNSPEC'd | This section explicitly calls the behavior "legacy" and "carryover behavior, used until mclaude-controller-local lands." Per ADR-0035, process supervision migrates to controller-local; this section should be removed or flagged as deprecated once controller-local is deployed. The spec itself notes this, so it's a soft SPEC→FIX (clean up this section once the migration is complete). |
| spec-controller.md — Corporate CA Support section | INFRA | Not in ADR-0035 scope; this is from earlier controller-runtime work. Consistent with the spec; no conflict. |
| spec-helm.md — `values-k3d-ghcr.yaml`, `values-aks.yaml` etc. values file table | INFRA | Values files from prior art, brought forward. Not contradicted by ADR-0035. |
| spec-host-picker.md — "Your hosts" / "Shared with you" emoji usage (🖥️ ☁️) | INFRA | Display detail, not in ADR-0035. No gap. |
| spec-cli.md — `mclaude-cli session list` command | INFRA | Pre-ADR-0035 command; CLI spec's context.json now includes `hostSlug`. |
| spec-cli.md — Context file `hostSlug` default | INFRA | `~/.mclaude/context.json` stores `hostSlug` — spec mentions this; ADR-0035 doesn't prescribe it explicitly but it follows from the overall host-slug model. |

---

## Phase 3 — Test Coverage

This audit is a spec-alignment audit only. Test file inspection is not within scope for this run (no `*_test.go` or `*.test.ts` files have been read — they are implementation artifacts not present in this codebase's docs layer). The component under audit (`docs/*` only) has no test files of its own. Test coverage of the implementing code should be evaluated in a per-component code-level spec-evaluator run.

---

## Phase 4 — Bug Triage

| Bug file | Verdict | Notes |
|---|---|---|
| (no bugs found in `.agent/bugs/` for unified-host / ADR-0035 domain) | — | `.agent/bugs/` directory not present or empty for this domain |

---

## Summary

### Phase 1 — ADR Decision → Spec

- **IMPLEMENTED**: 93
- **PARTIAL**: 6
- **GAP**: 0

### Partial items (all SPEC→FIX):

1. **Row 30** — `mclaude-common` package paths (`pkg/nats/creds.go`, `pkg/nats/operator-keys.go`) are not named in any spec. Implementation detail; spec should reference the init-keys logic location when describing the `init-keys` Job behavior.

2. **Row 70** — Host-picker error string "credentials invalid for this host" not documented in `spec-host-picker.md`. The control-plane and session-agent specs cover the NATS rejection; the user-visible string in the host picker is unspecced.

3. **Row 71** — `spec-host-picker.md` covers initial direct-vs-hub logic but does not describe runtime fallback when a live leaf-link drops mid-session (vs. the initial direct connection failure on project open).

4. **Row 75** — ADR error handling row says `"cluster {cslug} unreachable"` but spec-control-plane.md says `"host {hslug} unreachable"`. The spec wording is more general and correct; the ADR has a minor internal inconsistency. No code change needed; ADR wording is slightly imprecise.

5. **Row 77** — ADR security section says `~/.mclaude/hosts/{hslug}/creds.json` for BYOH; spec-state-schema.md consistently uses `nats.creds`. ADR has a typo; spec is correct.

6. **Row 99** — SPA modal polls `GET /api/users/{uslug}/hosts` (new host appearance) while CLI polls `GET /api/users/{uslug}/hosts/code/{code}` (code status). Both are correct for their respective consumers but neither cross-references the other. Clarity gap only.

### Phase 2 — Unspec'd / Dead

- **INFRA**: 9 (all necessary structure)
- **UNSPEC'd**: 2
  - `spec-state-schema.md` MCLAUDE_LIFECYCLE "not yet created in production code" stale note
  - `spec-session-agent.md` "legacy single-binary mode" child supervision section is kept as a transitional note but should be cleaned up post-controller-local landing

### Phase 3 — Test Coverage

Skipped (docs-layer audit only; no `*_test.go` files in scope).

### Phase 4 — Bugs

None found for this domain.

---

## Overall verdict

**NOT CLEAN** — 6 PARTIAL items, all `SPEC→FIX`. No `GAP` (CODE→FIX) items found. No removed concepts (`mclaude-laptops`, `mclaude-heartbeats`, `mclaude-clusters`, `runLaptopHeartbeat`, cluster-scoped subjects, `clusters` table) leak into any spec. All seven target specs exist and are correctly populated. The ADR's decisions are reflected in the specs at high fidelity with only minor wording precision gaps.
