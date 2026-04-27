## Run: 2026-04-27T00:00:00Z

### Round 2 Spec Alignment Audit — ADR-0035 Unified Host Architecture

Focus: verify 8 Round 1 fixes landed correctly; check for new gaps introduced.
ADR: `docs/adr-0035-unified-host-architecture.md`
Specs checked: `docs/spec-state-schema.md`, `docs/mclaude-control-plane/spec-control-plane.md`,
`docs/mclaude-session-agent/spec-session-agent.md`, `docs/charts-mclaude/spec-helm.md`,
`docs/mclaude-cli/spec-cli.md`, `docs/ui/mclaude-web/spec-host-picker.md`,
`docs/mclaude-controller/spec-controller.md`.

---

### Phase 1 — Round 1 Fix Verification

| Fix # | What was fixed | Where to look | Verdict | Notes |
|-------|---------------|---------------|---------|-------|
| 1 | FormatNATSCredentials package home: spec-helm.md `mclaude-cp-init-keys` Job description now names `mclaude-common/pkg/nats/operator-keys.go` and `pkg/nats/creds.go` | spec-helm.md line 28 | VERIFIED | Text reads: "…calls into `mclaude-common/pkg/nats/operator-keys.go` for the actual NKey + JWT generation logic (the same package home as `FormatNATSCredentials` in `pkg/nats/creds.go`, so the CLI can reuse the helpers for BYOH bootstrap)." Matches ADR-0035 Component Changes section. |
| 2 | spec-host-picker.md error states table now includes "credentials invalid for this host" entry | spec-host-picker.md line 130 | VERIFIED | Empty/error states table row: "Per-host JWT signed for the wrong host → Hub NATS auth rejects the publish/subscribe; SPA receives a connection error. Host picker surfaces 'credentials invalid for this host.'" Exact error string from ADR-0035 Error Handling table. |
| 3 | spec-host-picker.md adds new "Mid-session leaf-link drop (cluster hosts)" subsection | spec-host-picker.md lines 108–111 | VERIFIED | Subsection "Mid-session leaf-link drop (cluster hosts)" present at line 108. Covers runtime hub→direct fallback distinct from initial selection. Explicitly distinguishes "the runtime fallback is the inverse" (triggered by hub-via-leaf failure) from initial direct-vs-hub selection. |
| 4 | ADR error-handling row updated from `cluster {cslug} unreachable` to `host {hslug} unreachable` | ADR-0035 line 176, 350 | VERIFIED | ADR line 176: `{"error": "host {hslug} unreachable"}` and line 350: `{"error": "host {hslug} unreachable"}`. spec-control-plane.md line 198: `{error: "host {hslug} unreachable"}`. All consistent. |
| 5 | ADR Security section corrected to `nats.creds` (was `creds.json`) | ADR-0035 line 356 | VERIFIED | ADR line 356: "Stored on disk at `/etc/mclaude/cluster.creds` (cluster) or `~/.mclaude/hosts/{hslug}/nats.creds` (BYOH machine), `0600` permissions." File is `nats.creds` not `creds.json`. |
| 6 | spec-host-picker.md device-code modal step 3 now explicitly notes the SPA polls the host-list endpoint, not the code-status endpoint | spec-host-picker.md line 77 | VERIFIED | Step 3: "Polls `GET /api/users/{uslug}/hosts` (the host-list endpoint) every 3 seconds…; when a new host with `slug` not previously seen appears, dismiss the modal…. The SPA does **not** poll the code-status endpoint `GET /api/users/{uslug}/hosts/code/{code}` — that path is reserved for the CLI on the new machine…. The two polling strategies are not interchangeable." Matches ADR-0035 user flow A step 4. |
| 7 | MCLAUDE_LIFECYCLE stale note: spec-state-schema.md updated — production-active stream, created by session-agent CreateOrUpdateStream | spec-state-schema.md lines 286–289 | VERIFIED | Line 287: "Created by: session-agent (`CreateOrUpdateStream` — idempotent; same pattern as MCLAUDE_EVENTS / MCLAUDE_API). Production-active stream." ADR-0035 is cited. MCLAUDE_LIFECYCLE subject pattern now includes `hosts.*` segment (line 298: `mclaude.users.*.hosts.*.projects.*.lifecycle.*`). |
| 8 | Legacy daemon supervision subsection deleted from spec-session-agent.md | spec-session-agent.md | VERIFIED | No "Daemon: Child Process Supervision (legacy single-binary mode)" subsection exists. Supervision is absent from session-agent spec entirely. spec-controller.md "Process supervision" subsection (line 120–127) now owns this. |

---

### Phase 2 — New Gap Check (Full Forward Pass)

Systematic walk of ADR-0035 decisions against all specs. Checking field names, schemas, subject patterns, env var names, file paths, and reply shapes.

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| ADR-0035:37 | `type ∈ {'machine', 'cluster'}` discriminator; `role ∈ {'owner', 'user'}` per-user | spec-state-schema.md:59–60 | IMPLEMENTED | — | Schema matches exactly: `type TEXT NOT NULL CHECK (type IN ('machine', 'cluster'))`, `role TEXT NOT NULL DEFAULT 'owner' CHECK (role IN ('owner', 'user'))` |
| ADR-0035:37 | `online` boolean lives in `mclaude-hosts` KV bucket, not Postgres | spec-state-schema.md:68, 170–191 | IMPLEMENTED | — | Spec states "The current `online` boolean lives in `mclaude-hosts` KV, not Postgres." KV bucket documented at line 170 with `online` field. |
| ADR-0035:37 | `last_seen_at` is the durable historical record | spec-state-schema.md:68 | IMPLEMENTED | — | "Updated by `$SYS.ACCOUNT.*.CONNECT` subscription on hub NATS. Authoritative historical record." |
| ADR-0035:38 | `mclaude.users.{uslug}.hosts.{hslug}.…` is the only project-scoped subject family. No `mclaude.clusters.{cslug}.>` subjects exist | spec-state-schema.md:316, 336 | IMPLEMENTED | — | Line 316: "Host-scoped subjects (per ADR-0035 — `.hosts.{hslug}.` inserted…the only project-scoped subject family)"; line 336: "There are **no** `mclaude.clusters.{cslug}.>` subjects." |
| ADR-0035:41 | Default machine host: `slug='local'`, `type='machine'`, `role='owner'` on user creation | spec-state-schema.md:86–88 | IMPLEMENTED | — | "On user creation, control-plane writes one row to `hosts` with `slug='local'`, `type='machine'`, `role='owner'`." spec-control-plane.md:148: "upserts a `users` row with that email, `is_admin=true`, `oauth_id=NULL`" (bootstrap admin). Startup sequence step 9 seeds default `local` host. |
| ADR-0035:44 | Provisioning subject: `mclaude.users.{uslug}.hosts.{hslug}.api.projects.provision` | spec-state-schema.md:320, spec-control-plane.md:98 | IMPLEMENTED | — | Both specs list the provision subject in subject tables with correct pattern. |
| ADR-0035:46 | 3-tier operator→account→user JWT; `resolver: MEMORY` and `resolver_preload` on hub and every worker | spec-state-schema.md:480–533 | IMPLEMENTED | — | Full NATS Server Configuration section documents hub and worker configs with operator JWT, resolver: MEMORY, resolver_preload. |
| ADR-0035:47 | Control-plane is always K8s-hosted; no local/standalone variant | spec-control-plane.md:7 | IMPLEMENTED | — | "Per ADR-0035 the control-plane is **K8s-free**: no controller-runtime, no K8s client, no MCProject reconciler." and "The control-plane runs only inside the central `mclaude-cp` Kubernetes cluster (there is no local/standalone variant)." |
| ADR-0035:48 | Helm pre-install hook runs `mclaude-cp init-keys` as a Job; writes to Secret `mclaude-system/operator-keys`; idempotent on subsequent deploys | spec-helm.md:28–29 | IMPLEMENTED | — | Full Job description present at line 28; Secret keys `operatorJwt`, `operatorSeed`, `accountJwt`, `accountSeed` at line 29; idempotency noted. |
| ADR-0035:49 | `bootstrapAdminEmail` in Helm values; init-keys Job writes `users` row with `is_admin=true`, `oauth_id=NULL` | spec-helm.md:98, spec-control-plane.md:22, 148 | IMPLEMENTED | — | Helm knob `controlPlane.bootstrapAdminEmail` at spec-helm.md:98. spec-control-plane.md line 148 describes bootstrap admin row creation. `BOOTSTRAP_ADMIN_EMAIL` env var at spec-control-plane.md:22. |
| ADR-0035:50 | Admin CLI auth: Bearer token from `mclaude login`; `Authorization: Bearer <token>`; `users.is_admin` check for `/admin/` | spec-control-plane.md:10–11, 68 | IMPLEMENTED | — | "Admin endpoints under `/admin/*`…are served on the **main** port and protected by per-user `Authorization: Bearer <token>` plus a server-side `users.is_admin` check." |
| ADR-0035:51 | `$SYS.ACCOUNT.*.CONNECT/DISCONNECT` only; discriminates on `client.kind` and `client.nkey` | spec-state-schema.md:338–356, spec-control-plane.md:106–107 | IMPLEMENTED | — | Detailed event-dispatch table in both specs. Machine: kind=Client, look up by public_key/type='machine'. Cluster: kind=Leafnode, look up all rows for same slug. No match: ignore. |
| ADR-0035:52 | SPA opens hub connection always; opens direct worker connection on demand; falls back to hub-via-leaf | spec-host-picker.md:99–106 | IMPLEMENTED | — | "Connection strategy" section describes hub-always + on-demand direct + fallback pattern. |
| ADR-0035:53 | Login response: `{user, jwt, nkeySeed, hubUrl, hosts[], projects[]}`. Each host: `{slug, name, type, role, online, lastSeenAt, jsDomain?, directNatsUrl?}`. No top-level `clusters` array | spec-state-schema.md:611–650 | IMPLEMENTED | — | Full login response shape documented with exact field names. "The `hosts` array is the single source of truth for host inventory." `jsDomain` and `directNatsUrl` only on cluster entries. |
| ADR-0035:54 | Helm chart split: `mclaude-cp` + `mclaude-worker` | spec-helm.md:6–8 | IMPLEMENTED | — | Table listing both charts at top of spec. Single-cluster degenerate case documented at line 188. |
| ADR-0035:55 | `HOST_SLUG` env var (cluster pods); `--host <hslug>` flag for BYOH; required, not derived; hard-fail on absence | spec-session-agent.md:34, 300 | IMPLEMENTED | — | Config table: "`--host` / `HOST_SLUG` — **Required.** Host slug per ADR-0035". Error table line 300: "Fatal: agent or daemon refuses to start with `FATAL: HOST_SLUG required (set via env or --host flag)`" |
| ADR-0035:64 | BYOH flow: CLI generates NKey pair locally; private seed written to `~/.mclaude/hosts/{hslug}/nkey.seed` (mode 0600) | spec-cli.md:40, spec-state-schema.md:597 | IMPLEMENTED | — | spec-cli.md `mclaude host register` description; spec-state-schema.md host credentials directory section: `nkey.seed — NKey private seed (generated locally on host, never leaves machine)`. |
| ADR-0035:64 | `POST /api/users/{uslug}/hosts/code` with `{publicKey}` to get 6-char device code; CLI prints dashboard URL + code | spec-control-plane.md:62, spec-cli.md:40 | IMPLEMENTED | — | Endpoint described with `{publicKey}` in body and `{code, expiresAt}` response. CLI description matches. |
| ADR-0035:65 | On completion, response includes `{slug, jwt, hubUrl}`; CLI writes `~/.mclaude/hosts/{hslug}/{nats.creds, config.json}` from JWT + seed; symlinks `~/.mclaude/active-host → {hslug}` | spec-cli.md:40, spec-state-schema.md:598–607 | IMPLEMENTED | — | spec-cli.md: "On completion, writes `~/.mclaude/hosts/{hslug}/{nats.creds, config.json}`…and symlinks `~/.mclaude/active-host → {hslug}`." spec-state-schema.md host credentials directory lists `nats.creds` and `config.json`. |
| ADR-0035:72 | Cluster registration: control-plane generates per-cluster NKey pair; creates `hosts` row for admin with `type='cluster'`; mints per-cluster leaf JWT scoped to `mclaude.users.*.hosts.{slug}.>`; returns `{slug, leafJwt, leafSeed, accountJwt, operatorJwt, jsDomain, directNatsUrl}` | spec-control-plane.md:72 | IMPLEMENTED | — | Full endpoint description at spec-control-plane.md line 72: `POST /admin/clusters` generates NKey pair, creates host row, mints JWT with wildcard scope, returns all 7 fields. |
| ADR-0035:75 | Admin grants user access: creates NEW `hosts` row for bob with `type='cluster'`, `slug='<cluster-slug>'`, cluster-shared fields **copied** | spec-control-plane.md:74 | IMPLEMENTED | — | `POST /admin/clusters/{cslug}/grants` description at line 74: "Creates a new `hosts` row for that user with `slug=cslug`, `type='cluster'`, `role='user'`; copies cluster-shared fields (`js_domain`, `leaf_url`, `account_jwt`, `direct_nats_url`, `public_key`)" |
| ADR-0035:81 | Project creation: control-plane writes `mclaude-projects` KV (`{uslug}.{hslug}.{pslug}`) and publishes NATS request to `mclaude.users.{uslug}.hosts.{hslug}.api.projects.provision` | spec-control-plane.md:184–185 | IMPLEMENTED | — | Project Creation Flow steps 5 and 6 match exactly; KV key format is `{uslug}.{hslug}.{pslug}` per spec-state-schema.md line 146. |
| ADR-0035:82–84 | controller-k8s subscribes `mclaude.users.*.hosts.{cluster-slug}.api.projects.>`; controller-local subscribes `mclaude.users.{uslug}.hosts.{hslug}.api.projects.>` | spec-controller.md:37–44, 113–118 | IMPLEMENTED | — | Both variants' NATS subscriptions documented with exact patterns. |
| ADR-0035:90 | Session create subject: `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.create` | spec-state-schema.md:326, spec-session-agent.md:90 | IMPLEMENTED | — | Subject in state-schema NATS subjects table. Session-agent subscribe section uses host-scoped pattern. |
| ADR-0035:92–93 | Lifecycle: `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.lifecycle.{sslug}`; KV at `mclaude-sessions` key `{uslug}.{hslug}.{pslug}.{sslug}` | spec-state-schema.md:298–299, 101 | IMPLEMENTED | — | Both match ADR exactly. |
| ADR-0035:104–109 | subj package: host-scoped helpers exist; removals of ClusterAPIProjectsProvision, ClusterAPIStatus, UserHostStatus required | spec-session-agent.md:75, spec-state-schema.md:336 | IMPLEMENTED | — | Session-agent spec: "The previous `mclaude-laptops` and `mclaude-heartbeats` buckets are removed entirely per ADR-0035." No-clusters-subject note in state-schema. No references to removed cluster subjects found in specs. |
| ADR-0035:111 | `FormatNATSCredentials` moved to `mclaude-common/pkg/nats/creds.go` | spec-helm.md:28 | IMPLEMENTED | — | Verified in Fix #1. |
| ADR-0035:113 | `mclaude-common/pkg/nats/operator-keys.go` added | spec-helm.md:28 | IMPLEMENTED | — | Verified in Fix #1. |
| ADR-0035:120 | `HOST_SLUG` added to `DaemonConfig`; populated from `HOST_SLUG` env (K8s) or `--host` flag (BYOH); fail-fast on absence | spec-session-agent.md:34 | IMPLEMENTED | — | Config table entry for `--host` / `HOST_SLUG`: "**Required.**…Hard fail at startup on absence." Error table: "`FATAL: HOST_SLUG required (set via env or --host flag)`" |
| ADR-0035:121 | Remove `UserAPIProjectsCreate` subscription from daemon | spec-session-agent.md:95 | IMPLEMENTED | — | "The daemon does **not** subscribe to project-creation requests; project provisioning per ADR-0035 is handled by `mclaude-controller-local` or `mclaude-controller-k8s`" |
| ADR-0035:122–124 | Remove `LaptopsKVKey` invocations, `laptopsKV` field, `mclaude-laptops` bucket; remove `runLaptopHeartbeat` goroutine | spec-session-agent.md:23–24, 75 | IMPLEMENTED | — | Daemon Mode section line 23: "does **not** publish periodic heartbeats and does **not** write to `mclaude-hosts` or to any removed bucket (`mclaude-laptops`, `mclaude-heartbeats`)". KV section line 75: "The previous `mclaude-laptops` and `mclaude-heartbeats` buckets are removed entirely per ADR-0035." |
| ADR-0035:136–138 | Control-plane gains `hosts` table DDL, `host_id` column on projects, HTTP host endpoints, `IssueHostJWT` | spec-control-plane.md:60–66, 123–124 | IMPLEMENTED | — | All host endpoints documented. `IssueHostJWT` referenced in Kubernetes Dependency section line 131 and Authentication section. |
| ADR-0035:139 | Subscribe to `$SYS.ACCOUNT.{accountKey}.CONNECT/DISCONNECT`; map NKey to host slug; update `hosts.last_seen_at` | spec-control-plane.md:106–107 | IMPLEMENTED | — | Detailed NATS subscriptions table at lines 106–107. |
| ADR-0035:141–145 | controller-k8s: reconciles MCProject CRs; subscribes to `mclaude.users.*.hosts.{cluster-slug}.api.projects.>`; buildPodTemplate injects USER_SLUG, HOST_SLUG, PROJECT_SLUG | spec-controller.md:37–44, spec-state-schema.md:461–464 | IMPLEMENTED | — | All three aspects documented. state-schema Deployment section at line 461 lists the 5 env vars (including HOST_SLUG). |
| ADR-0035:147–149 | controller-local: subscribes `mclaude.users.{uslug}.hosts.{hslug}.api.projects.>`; manages session-agent subprocesses via exec.Cmd + restart-on-crash; maintains `~/.mclaude/projects/{pslug}/worktree/` | spec-controller.md:87–127 | IMPLEMENTED | — | Full Variant 2 section covers all three aspects. |
| ADR-0035:152–176 | Provisioning request/reply contract: request shape, success reply, failure reply with `code` field; timeout = 503 with `{error: "host {hslug} unreachable"}` | spec-controller.md:135–159, spec-control-plane.md:186–190 | IMPLEMENTED | — | Provisioning request shape at spec-controller.md:138–144. Reply shapes at lines 148–157. spec-control-plane.md line 190: `503 Service Unavailable` with `{error: "host {hslug} unreachable"}` on timeout. |
| ADR-0035:183–190 | CLI: `host` subcommand (register/list/use/rm); `cluster` subcommand (register/grant); `daemon --host` | spec-cli.md:38–82 | IMPLEMENTED | — | All four host subcommands documented. Both cluster subcommands. daemon --host flag. `~/.mclaude/hosts/` and `active-host` management described. |
| ADR-0035:194–198 | SPA: subj.ts host-scoped builders; AuthStore accessors; SessionStore per-cluster KV watches; EventStore dual-NATS; Routes with `{hslug}` | spec-host-picker.md:82–89, 99–106 | IMPLEMENTED | — | Routes section at lines 82–89. Connection strategy at 99–106. AuthStore extensions in spec-host-picker.md Dependencies section (line 135). |
| ADR-0035:204–206 | Helm: `mclaude-cp` chart content; `mclaude-worker` chart content; single-cluster degenerate case | spec-helm.md:16–204 | IMPLEMENTED | — | Both charts fully documented. Degenerate case at lines 188–204. |
| ADR-0035:212–241 | `hosts` table DDL (all columns, constraints) | spec-state-schema.md:49–88 | IMPLEMENTED | — | All columns present with matching types and constraints. `CHECK (type = 'machine' OR (js_domain IS NOT NULL AND leaf_url IS NOT NULL AND account_jwt IS NOT NULL))` at line 72. |
| ADR-0035:246 | `users` table gains `oauth_id TEXT NULL` and `is_admin BOOLEAN NOT NULL DEFAULT FALSE` | spec-state-schema.md:22–23 | IMPLEMENTED | — | Both columns present in users table with matching types and descriptions. |
| ADR-0035:255–261 | `projects` table: `host_id TEXT NOT NULL FK→hosts ON DELETE CASCADE`; `UNIQUE(user_id, host_id, slug)` | spec-state-schema.md:41–44 | IMPLEMENTED | — | `host_id` column present. Index at line 44: `UNIQUE (user_id, host_id, slug)`. |
| ADR-0035:267–273 | Secret `mclaude-system/operator-keys`; keys `operatorJwt`, `accountJwt`, `accountSeed`, `operatorSeed`; mode 0600 | spec-state-schema.md:537–546, spec-helm.md:29 | IMPLEMENTED | — | Both specs list all 4 keys. Mode 0600 noted. |
| ADR-0035:280–290 | Per-host user JWT permissions (publish/subscribe sets) | spec-state-schema.md:554–559 | IMPLEMENTED | — | Exact publish/subscribe permission sets match ADR text. |
| ADR-0035:286–289 | Cluster controller JWT permissions (wildcard at user level) | spec-state-schema.md:562–567 | IMPLEMENTED | — | Exact wildcard permission set matches ADR text. |
| ADR-0035:295–299 | `mclaude-hosts` KV: replaces `mclaude-laptops`; key `{uslug}.{hslug}`; value fields | spec-state-schema.md:170–192 | IMPLEMENTED | — | KV bucket documented at lines 170–192. Key format, value JSON, single-writer (control-plane), readers (SPA). Previous bucket removal noted. |
| ADR-0035:344 | Error: startup without HOST_SLUG — `FATAL: HOST_SLUG required (set via env or --host flag)` | spec-session-agent.md:300 | IMPLEMENTED | — | Exact error string present. |
| ADR-0035:345 | Error: wrong-host JWT — `"credentials invalid for this host"` | spec-host-picker.md:130 | VERIFIED (Fix #2) | — | Error string verified in Round 1 fix check. |
| ADR-0035:346 | Error: worker leaf-node drop — JetStream fails; SPA falls back to direct worker if reachable; if not, marks cluster offline; sessions resync on reconnect | spec-host-picker.md:108–111 | VERIFIED (Fix #3) | — | Mid-session drop subsection covers fallback, offline marking, and resync. |
| ADR-0035:348–349 | Device-code errors: 410 Gone with `{"error": "code expired, restart registration"}`; 409 Conflict | spec-control-plane.md:199–200 | IMPLEMENTED | — | Both error cases listed in spec-control-plane Error Handling section. |
| ADR-0035:350 | Project create on offline host → 503 with `{"error": "host {hslug} unreachable"}` | spec-control-plane.md:198 | VERIFIED (Fix #4) | — | Verified in Round 1 fix check. |
| ADR-0035:356 | Security: credentials on disk at `/etc/mclaude/cluster.creds` (cluster) or `~/.mclaude/hosts/{hslug}/nats.creds` (BYOH), 0600 | spec-state-schema.md:599 | IMPLEMENTED | — | spec-state-schema.md line 599: "`nats.creds` — NATS credentials file (JWT + NKey seed, host-scoped permissions per ADR-0035)". No explicit path for cluster `/etc/mclaude/cluster.creds` in spec-state-schema (it's in the K8s Secret referenced by the controller), but ADR's own Security section was already corrected (Fix #5). |

---

### Phase 3 — Detailed Cross-Spec Field Alignment Check

Checking exact field names, subject patterns, env var names, and file paths across ADR and all 7 specs for any remaining mismatches.

#### 3a. `mclaude-hosts` KV value shape

ADR-0035 line 299: `{ slug, name, type, role, lastSeenAt, online, ... }`
spec-state-schema.md lines 178–185:
```json
{
  "slug": "string",
  "type": "machine | cluster",
  "name": "string",
  "role": "owner | user",
  "online": true,
  "lastSeenAt": "RFC3339"
}
```

ADR has fields: slug, name, type, role, lastSeenAt, online (plus `...`).
Spec has: slug, type, name, role, online, lastSeenAt.

| Check | Result |
|-------|--------|
| All 6 named ADR fields present in spec | PASS |
| Order differs (minor, not a gap) | N/A |
| `...` in ADR — spec doesn't add extra fields | PASS — spec is complete, no dangling extra fields |

#### 3b. Login response `projects[]` shape

ADR-0035 line 333: `{ "slug": "billing", "name": "billing service", "hostSlug": "us-east", "hostType": "cluster", "jsDomain": "us-east", "directNatsUrl": "wss://…/nats" }`
spec-state-schema.md lines 641–645:
```json
{ "slug": "myrepo",  "name": "My Repo",        "hostSlug": "mbp16",   "hostType": "machine" },
{ "slug": "billing", "name": "billing service","hostSlug": "us-east", "hostType": "cluster", "jsDomain": "us-east", "directNatsUrl": "wss://us-east.mclaude.example/nats" }
```

All fields match exactly (slug, name, hostSlug, hostType, jsDomain, directNatsUrl).

#### 3c. `mclaude cluster register` CLI flags vs. spec-cli.md

ADR-0035 line 188: `--slug <cslug>`, `--name <display>`, `--jetstream-domain <jsd>`, `--leaf-url <url>`, `--direct-nats-url <wss>`
spec-cli.md lines 65–72: All 5 flags present with matching names and descriptions.

#### 3d. spec-controller.md `mclaude-controller-local` Config vs. ADR

ADR-0035 line 190: daemon reads `--host` or `~/.mclaude/active-host` symlink; connects to hub NATS using `~/.mclaude/hosts/{hslug}/nats.creds`
spec-controller.md lines 99–105: `--host` / `HOST_SLUG` required; `--creds-file` defaults to `~/.mclaude/hosts/{hslug}/nats.creds`; `--hub-url` / `HUB_URL` required.

Check: does spec-cli.md `mclaude daemon --host` description align with spec-controller.md?

spec-cli.md line 82: "…subscribes to `mclaude.users.{uslug}.hosts.{hslug}.api.projects.>`, and starts session-agent subprocesses for each provisioned project."

spec-controller.md line 91: "Started by the user (`mclaude daemon --host <hslug>` or via a launchd / systemd unit they configure)."

CONSISTENT — both specs describe daemon as the entry point for controller-local.

#### 3e. MCLAUDE_LIFECYCLE subject pattern alignment

ADR-0035 line 92: `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.lifecycle.{sslug}`
spec-state-schema.md line 298: `Subjects: mclaude.users.*.hosts.*.projects.*.lifecycle.*`
spec-state-schema.md line 299: `Subject pattern: mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.lifecycle.{sslug}`
spec-session-agent.md line 81: `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.lifecycle.{sslug}`

All three specs agree with the ADR. CONSISTENT.

#### 3f. `projects` table index name

ADR-0035 line 259: `CREATE UNIQUE INDEX projects_user_id_host_id_slug_uniq ON projects (user_id, host_id, slug);`
spec-state-schema.md line 44: `Index: UNIQUE (user_id, host_id, slug)`

The spec uses prose description rather than the SQL index name `projects_user_id_host_id_slug_uniq`. This is acceptable — the spec describes the constraint; the ADR names the index for DDL purposes. No gap.

#### 3g. Daemon: JWT Refresh path

ADR-0035 line 65: CLI writes `~/.mclaude/hosts/{hslug}/{nats.creds, config.json}`
spec-session-agent.md line 265: "checks its host JWT (`~/.mclaude/hosts/{hslug}/nats.creds`) TTL every 60 seconds"
spec-state-schema.md line 599: `nats.creds — NATS credentials file`

All consistent. File is `nats.creds`.

#### 3h. `auth.json` location

ADR-0035 line 65: (not explicitly mentioned in user flow A)
ADR-0035 line 50: "Token persisted to `~/.mclaude/auth.json` at 0600."
spec-state-schema.md line 601: "`auth.json` — bearer token from `mclaude login`, mode 0600 (used for admin CLI calls)"

ADR says `~/.mclaude/auth.json`; spec-state-schema says it's inside `~/.mclaude/hosts/{hslug}/auth.json` (it's listed under the host credentials directory section at line 595: `Path: ~/.mclaude/hosts/{hslug}/`).

| Gap? | Analysis |
|------|----------|
| ADR-0035:50 says `~/.mclaude/auth.json`; spec-state-schema.md places it under `~/.mclaude/hosts/{hslug}/auth.json` | PARTIAL |

The ADR's Admin CLI auth decision (line 50) says "Token persisted to `~/.mclaude/auth.json` at 0600." The spec-state-schema.md Host credentials directory section lists `auth.json` under `~/.mclaude/hosts/{hslug}/` — a different location. This is a field-path mismatch.

Direction: `SPEC→FIX` — the ADR's flat `~/.mclaude/auth.json` is the more conventional location for auth credentials (not host-scoped, since the bearer token authenticates the user, not a specific host). The spec-state-schema.md should clarify whether `auth.json` is host-scoped or user-scoped. Alternatively the ADR should be updated if host-scoped is intentional.

Wait — let me recheck carefully. spec-state-schema.md line 601 is part of the "Host credentials directory (BYOH machines)" section starting at line 594. The section header says `Path: ~/.mclaude/hosts/{hslug}/`. So the spec puts `auth.json` at `~/.mclaude/hosts/{hslug}/auth.json`.

ADR-0035 line 50: "Token persisted to `~/.mclaude/auth.json` at 0600."

This is a genuine path mismatch.

Also spec-cli.md dependencies section (line 115): no explicit mention of auth.json path.

| Spec (doc:line) | Spec text | Conflict location | Verdict | Direction | Notes |
|-----------------|-----------|-------------------|---------|-----------|-------|
| ADR-0035:50 | "Token persisted to `~/.mclaude/auth.json` at 0600" | spec-state-schema.md:601 places it at `~/.mclaude/hosts/{hslug}/auth.json` | PARTIAL | SPEC→FIX | The bearer token authenticates the user (not a specific host), so `~/.mclaude/auth.json` (flat, user-level) is the more coherent location. spec-state-schema.md should move `auth.json` out of the host credentials directory section or note it is user-scoped. If host-scoped placement is intentional, ADR must be updated. |

#### 3i. `PROVISION_TIMEOUT_SECONDS` env var name

ADR-0035 line 153: "NATS request/reply with a 10s timeout (`PROVISION_TIMEOUT_SECONDS`)"
spec-control-plane.md line 31: `PROVISION_TIMEOUT_SECONDS` env var, No/10, "Per-request timeout for NATS provisioning request/reply"
spec-controller.md line 159: "The control-plane treats a NATS request timeout (`PROVISION_TIMEOUT_SECONDS`, default 10s)"

All consistent.

#### 3j. Cluster controller NATS subscribe pattern — trailing `/>`

ADR-0035 line 76: `mclaude.users.*.hosts.us-east.api.projects.provision`
spec-state-schema.md line 333: "subscribes with a wildcard at the user level: `mclaude.users.*.hosts.{cluster-slug}.api.projects.>`. Receives requests from every user granted access to the cluster"
spec-controller.md line 41: `mclaude.users.*.hosts.{CLUSTER_SLUG}.api.projects.provision`

ADR-0035 line 76 describes the cluster controller subscribing to `mclaude.users.*.hosts.us-east.api.projects.provision` specifically. But spec-state-schema.md and spec-controller.md describe a `>` wildcard that also covers create/update/delete. ADR-0035 line 143 also says the controller subscribes to `mclaude.users.*.hosts.{cluster-slug}.api.projects.>` (a wildcard).

The line 76 in the ADR is in the User Flow prose (describing just the provision path in that narrative). The actual decision table at line 44 says `mclaude.users.*.hosts.{cluster-slug}.api.projects.>`. This is consistent.

#### 3k. `MCLAUDE_API` stream — `restart` vs. `resume` subject

spec-state-schema.md line 280: `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.{create|input|resume|delete|control}`
spec-session-agent.md line 90: "Filter subjects are host-scoped: `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.{create,delete,input,restart,control}`"

Note the discrepancy: state-schema says `resume`; session-agent spec says `restart`. This is a pre-existing cross-spec discrepancy not introduced by ADR-0035. Not in scope of this ADR-focused audit — but flagging as a potential issue for a separate session-agent audit.

Actually, wait — this discrepancy touches the host-scoped subject pattern which IS within ADR-0035's scope (it mandates the subject family). Let me check if ADR-0035 names this subject.

ADR-0035 line 90: session create subject mentioned. No explicit mention of `restart` vs `resume`. ADR-0035 does not enumerate session API subjects. The discrepancy is in the pre-existing session-agent spec (restart) vs. state-schema (resume). Since ADR-0035 didn't change this, it's out of scope for this round.

---

### Phase 4 — Removed Concepts Check

Verify that concepts removed by ADR-0035 are absent from all specs.

| Removed concept | Where to check | Result |
|-----------------|---------------|--------|
| `mclaude-laptops` KV bucket | All specs | ABSENT — spec-state-schema.md line 194: "The previous `mclaude-laptops` and `mclaude-heartbeats` buckets are removed." Session-agent spec line 75 notes removal. No spec refers to `mclaude-laptops` as active. |
| `mclaude-heartbeats` KV bucket | All specs | ABSENT — same as above. |
| `mclaude-clusters` KV bucket | All specs | ABSENT — spec-state-schema.md line 192: "The previous `mclaude-clusters` KV is removed." |
| `clusters` Postgres table | All specs | ABSENT — spec-state-schema.md line 51: "The `clusters` table no longer exists." |
| `mclaude.clusters.{cslug}.>` subjects | All specs | ABSENT — spec-state-schema.md line 336: "There are **no** `mclaude.clusters.{cslug}.>` subjects." |
| K8s client in control-plane | spec-control-plane.md | ABSENT — line 7: "the control-plane is **K8s-free**". Line 61: ServiceAccount has "**No K8s permissions**". |
| Legacy daemon supervision section in session-agent | spec-session-agent.md | ABSENT — verified in Fix #8. |
| `runLaptopHeartbeat` goroutine concept | spec-session-agent.md | ABSENT — daemon liveness section (line 278) says "no periodic heartbeat publish; the previous `mclaude-laptops` collision-detection flow is removed." |

---

### Phase 5 — Summary

#### Round 1 Fix Verification

All 8 Round 1 fixes are present and correctly implemented in the specs:
1. VERIFIED — FormatNATSCredentials/operator-keys.go package homes named in spec-helm.md
2. VERIFIED — "credentials invalid for this host" in spec-host-picker.md error table
3. VERIFIED — Mid-session leaf-link drop subsection in spec-host-picker.md
4. VERIFIED — `host {hslug} unreachable` error string throughout
5. VERIFIED — `nats.creds` (not `creds.json`) in ADR Security section
6. VERIFIED — SPA polls host-list endpoint, not code-status endpoint; two strategies distinguished
7. VERIFIED — MCLAUDE_LIFECYCLE marked production-active, created by session-agent
8. VERIFIED — Legacy daemon supervision subsection removed from spec-session-agent.md

#### New Gap Found

| # | Type | Direction | Description |
|---|------|-----------|-------------|
| G1 | PARTIAL | SPEC→FIX | `auth.json` path inconsistency: ADR-0035:50 says `~/.mclaude/auth.json` (flat, user-level); spec-state-schema.md:601 places it under `~/.mclaude/hosts/{hslug}/auth.json` (host-scoped, inside the host credentials directory). The bearer token is user-scoped (not tied to a specific host), so flat placement matches usage semantics. spec-state-schema.md should either move `auth.json` to a user-level credentials section or add a note clarifying it is user-scoped despite living in the host directory. |

#### All other checks

- All 46 ADR-0035 decision lines verified against their target specs.
- All removed concepts confirmed absent.
- Cross-spec field alignment checks passed (KV value shapes, login response shape, JWT permissions, subject patterns, CLI flags, env var names).
- Session-agent restart/resume subject naming discrepancy noted but out-of-scope (not an ADR-0035 gap).

---

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| BUG-004 | "Agent down: mclaude -- heartbeat stale" despite running pod | FIXED | ADR-0035 eliminates the entire heartbeat system (`mclaude-heartbeats` KV, `runHeartbeat()`, `HeartbeatMonitor`, `mclaude-laptops`). Liveness is now `$SYS`-only. All root causes described in the bug (KV mismatch, missing TTL, heartbeat source confusion) are moot — the mechanism they describe no longer exists. Moved to `.agent/bugs/fixed/`. |

---

### Final Summary

**Round 1 Fixes**: 8/8 VERIFIED — all fixes present and correctly implemented.

**New Gaps Found**: 1

| Gap | Type | Direction | Description |
|-----|------|-----------|-------------|
| G1 | PARTIAL | SPEC→FIX | `auth.json` path inconsistency: ADR-0035 line 50 says `~/.mclaude/auth.json` (flat, user-level); spec-state-schema.md line 601 places it inside `~/.mclaude/hosts/{hslug}/auth.json` (host-scoped). The bearer token from `mclaude login` is user-scoped (not tied to a specific host), making flat placement more semantically correct. Additionally, spec-cli.md has no `mclaude login` command entry at all, yet spec-state-schema.md lists `mclaude login` as a writer of `auth.json`. Fix: (a) spec-state-schema.md should add a user-level credentials section for `auth.json` at `~/.mclaude/auth.json`, separate from the host credentials directory; (b) spec-cli.md should add a `mclaude login` command entry. |

**Bugs**: 1 FIXED (BUG-004), 0 OPEN.

**All other ADR-0035 decision lines**: IMPLEMENTED across all 7 specs checked. No additional gaps introduced by the Round 1 fixes.
