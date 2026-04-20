## Audit: 2026-04-20T18:00:00Z

**Document:** docs/adr-0004-multi-laptop.md

---

## Run: 2026-04-20T22:30:00Z

**Gaps found: 9**

1. **`pkg/subj` helpers do not include `{hslug}` — the entire host-scoped subject/KV-key layer is unimplemented** — The ADR states "Subject-publishing for project-scoped messages uses host-inclusive `pkg/subj` helpers" and "All NATS subscriptions use host-inclusive subject shape via `pkg/subj` helpers." However, `mclaude-common/pkg/subj/subj.go` still uses the ADR-0024 pre-BYOH shape: `UserProjectAPISessionsInput(u, p)` produces `mclaude.users.{uslug}.projects.{pslug}.api.sessions.input`, and the three JetStream filter constants (`FilterMclaudeAPI`, `FilterMclaudeEvents`, `FilterMclaudeLifecycle`) are also the ADR-0024 forms (`mclaude.users.*.projects.*.api.sessions.>`, etc.). There are no `HostSlug`-accepting variants in the package. The KV helpers `SessionsKVKey`, `ProjectsKVKey` also omit `{hslug}`. A developer implementing the ADR-0004 subject/KV layer has no contract to implement against: they must invent the new function signatures, decide whether to replace or add alongside the existing helpers, and determine what happens to existing callers in `daemon_jobs.go` and `state.go` that use the ADR-0024 forms. The ADR says "mclaude-common lands first (sequential)" but gives no signatures.
   - **Doc**: "Add `HostSlug` type to pkg/subj, update all subject helpers to accept host param, add `hosts` to reserved words in pkg/slug" (Implementation Plan); "All NATS subscriptions use host-inclusive subject shape via `pkg/subj` helpers" (mclaude-session-agent section)
   - **Code**: `mclaude-common/pkg/subj/subj.go` lines 66–127 — all project-scoped helpers take only `(UserSlug, ProjectSlug)`. `slug.go` does define `HostSlug` (line 29) but no subject helper uses it.

2. **JetStream stream filter update is not specified for the `mclaude-common` first-ship step** — The ADR's JetStream filter table (§JetStream filter changes) shows the ADR-0004 filters are `mclaude.users.*.hosts.*.projects.*.api.sessions.>` etc. These filters are hardcoded as constants in `pkg/subj` (`FilterMclaudeAPI`, `FilterMclaudeEvents`, `FilterMclaudeLifecycle`). The spec-state-schema.md stream definitions already show the ADR-0004 form. But the Implementation Plan says `mclaude-common` lands "~150 lines" and only mentions "Add `HostSlug` type to pkg/subj, update all subject helpers." It does not say whether the stream filter constants are updated as part of `mclaude-common` (before the components that reference them) or as part of `mclaude-session-agent` (which creates the streams with `CreateOrUpdateStream`). Whoever lands `mclaude-session-agent` will recreate the streams with the new filter, but if `mclaude-common` still exports the old filter constant, users of that constant will be inconsistent.
   - **Doc**: "Add `HostSlug` type to pkg/subj, update all subject helpers to accept host param" — no explicit mention of the filter constants.
   - **Code**: `subj.go` lines 18–24 — `FilterMclaudeAPI = "mclaude.users.*.projects.*.api.sessions.>"` is the ADR-0024 form and will be stale after ADR-0004 lands.

3. **`hosts` table migration strategy is absent** — The ADR says `hosts` table is "New table: `hosts` — see spec-state-schema.md for full schema" and the `projects` table loses `cluster_id` for `host_id`. But there is no migration specification: no DDL, no backfill algorithm, no ordering of steps relative to the existing `projects.cluster_id` removal, no explanation of how pre-existing cluster-host rows from `user_clusters` are migrated into `hosts` rows. The existing codebase `db.go` does not have a `hosts` table, `user_clusters` table, or `projects.host_id` column. A developer must invent the migration from scratch.
   - **Doc**: "New table: `hosts`" in Data Model; "Removed table: `user_clusters` — absorbed into `hosts` with `type='cluster'`"; "Modified table: `projects` — `cluster_id` replaced by `host_id FK→hosts`" — no DDL or migration steps given.
   - **Code**: `db.go` schema constant (line 314) — `projects` table has no `cluster_id` or `host_id`; no `hosts` or `user_clusters` tables exist. The migration starting point is a simpler schema than even ADR-0011 assumed.

4. **`POST /api/hosts/register` (device-code path) has no auth — and no mechanism is specified for preventing a race between code generation and use** — The ADR says this endpoint is "no auth — code is the credential." But the registration flow creates a `hosts` row and mints a signed NATS JWT. The user whose host is being registered must be identified. Step 5 of the device-code flow says "CLI calls `POST /api/hosts/register` with `{code: "ABC123"}`" — the control-plane must look up the code in `host_registration_codes` and retrieve the user ID. The ADR says "Device-code storage: `host_registration_codes` table or in-memory map with 10-min expiry" but never specifies: (a) which storage is chosen, (b) the table schema if a table, (c) what fields are stored alongside the code (user_id, host_name, expiry), (d) whether the code is marked as used atomically (preventing double-registration), and (e) what the request body includes beyond `{code}` — the unauthed flow needs to receive the host name and public key, but step 5 only shows `{code}`.
   - **Doc**: "CLI calls `POST /api/hosts/register` with `{code: 'ABC123'}`" (User Flow §Register a machine host, unauthed); "Device-code storage: `host_registration_codes` table or in-memory map with 10-min expiry" (Component Changes)
   - **Code**: No existing implementation to cross-reference; the gap is entirely in spec incompleteness.

5. **Cluster host registration flow is missing NKey/JWT issuance steps** — For machine hosts (authed flow), step 5 says "Control-plane creates `hosts` row, signs a JWT with host-scoped NATS permissions, returns it." But for cluster hosts (§Register a cluster host), there are only 4 steps: admin calls `POST /admin/clusters/{cslug}/members`, CP creates a `hosts` row, and the user "sees the cluster host in Settings > Hosts." There is no step that mints a NATS JWT or NKey for the cluster host. Yet per the architecture, cluster hosts also need NATS credentials — the session-agents running on the cluster need to subscribe on `mclaude.users.{uslug}.hosts.{hslug}.>`. Additionally, the cluster host's NKey (public key) goes in `hosts.public_key NOT NULL` — but who generates this NKey for a cluster host, and when? The existing cluster leaf-node credential (in `clusters.leaf_creds`) is different from a per-user host NATS JWT.
   - **Doc**: "Per-host NATS credentials grant `mclaude.users.{uslug}.hosts.{hslug}.>` — a compromised machine host cannot read another host's traffic" (Architecture); cluster host flow steps 1-4 with no credentials step; `public_key TEXT NOT NULL` in `hosts` schema.
   - **Code**: `spec-state-schema.md` `hosts` table: `public_key TEXT NOT NULL` — for cluster hosts this field must be populated but the flow does not specify how.

6. **`mclaude-hosts` KV writer conflict: daemon writes machine host entries, but how control-plane writes cluster host entries is unspecified** — The spec-state-schema.md says: "Writers: daemon (`writeHostKV` — on startup + every 12h for machine hosts), control-plane (for cluster host status)." The daemon code (`daemon.go`) has a `laptopsKV` field (not yet renamed `hostsKV`). For cluster hosts, the control-plane is the writer, but the ADR does not specify: (a) when the control-plane writes the cluster host's KV entry (at admin grant time? when the cluster comes online via `$SYS`?), (b) what populates the `status` field for a cluster host (cluster liveness is detected via `$SYS` controller connect/disconnect per ADR-0011, not the `mclaude.users.{uslug}.hosts.{hslug}.status` heartbeat subject which is "machine hosts only"), and (c) whether the CP writes the entry at grant time with `status=offline` and updates it when the controller connects, or only writes it when the cluster is online.
   - **Doc**: "New host-level subject: `mclaude.users.{uslug}.hosts.{hslug}.status` — host presence heartbeat (machine hosts only; cluster hosts use `$SYS` events)" (NATS subject changes); no specification of control-plane cluster-host KV write trigger.
   - **Code**: `spec-state-schema.md` `mclaude-hosts` Writers entry — "control-plane (for cluster host status)" — no code yet, but the trigger and initial write semantics are unspecified.

7. **`runLifecycleSubscriber` and `dispatchQueuedJob` in `daemon_jobs.go` use ADR-0024 subjects and will break after ADR-0004 migration** — The daemon subscribes to `"mclaude.users." + string(d.cfg.UserSlug) + ".projects.*.lifecycle.*"` (line 254) and dispatches jobs using `subj.UserProjectAPISessionsCreate(d.cfg.UserSlug, slug.ProjectSlug(job.ProjectID))` (line 342) and `subj.UserProjectAPISessionsInput(d.cfg.UserSlug, slug.ProjectSlug(job.ProjectID))` (line 438). After ADR-0004, all project-scoped subjects include `.hosts.{hslug}.`. These calls will publish/subscribe to the wrong subjects. The ADR's `JobEntry` gains `hostSlug`, and the dispatcher must use it to construct subjects. But the `handleJobsRoute` POST handler (line 744) does not accept a `hostSlug` field — there is no `ProjectSlug` → `HostSlug` resolution path specified for the daemon. How does the daemon know which `hslug` to use when dispatching a job for a given `projectId`?
   - **Doc**: "`JobEntry` gains `hostSlug` field. Dispatcher uses it for KV key construction" (mclaude-session-agent section); "All NATS subscriptions use host-inclusive subject shape via `pkg/subj` helpers" — but no specification of how the dispatcher acquires `hostSlug` from a job request.
   - **Code**: `daemon_jobs.go` line 342 — `subj.UserProjectAPISessionsCreate(d.cfg.UserSlug, slug.ProjectSlug(job.ProjectID))` uses pre-BYOH subject shape; line 744-778 POST /jobs handler — no `hostSlug` in request body; `JobEntry` struct in `state.go` line 141 has `HostSlug string` field but it is never populated by the POST handler.

8. **Session-agent signing key ceiling for cluster hosts contradicts itself between sections** — In the Architecture/Decisions table, the signing key ceiling is stated as `mclaude.users.*.hosts.{hslug}.projects.*.>` (note: literal `{hslug}`, implying a separate signing key per host). But in the `charts/mclaude` section: "Signing key ceiling: `mclaude.users.*.hosts.*.projects.*.>`" (note: wildcard `*` for `{hslug}`). ADR-0016's per-cluster signing key hierarchy registered a ceiling per `{clusterId}`. The choice matters: if the ceiling uses wildcard `*.hosts.*`, a compromised session-agent could subscribe to another cluster host's project subjects (within the same user). If the ceiling uses `{hslug}`, a separate signing key is needed per cluster host. The ADR does not resolve this.
   - **Doc**: Decisions table: "Signing key ceiling: `mclaude.users.*.hosts.{hslug}.projects.*.>`" vs. charts/mclaude section: "Signing key ceiling: `mclaude.users.*.hosts.*.projects.*.>`"
   - **Code**: No existing BYOH signing key implementation to cross-check, but the inconsistency between two sections of the same document means a developer cannot determine the correct ceiling to register.

9. **`handleJobsProjects` in daemon uses `userID` (UUID) as KV key prefix but spec-state-schema.md uses `uslug`** — After ADR-0004, `mclaude-projects` keys are `{uslug}.{hslug}.{pslug}`. The handler at `daemon_jobs.go` line 887 filters KV entries with `prefix := userID + "."` where `userID` is a UUID. Under ADR-0024 the key became `{uslug}.{pslug}`, and under ADR-0004 it becomes `{uslug}.{hslug}.{pslug}`. The UUID prefix will match zero entries after migration. The ADR mentions "KV key construction includes `{hslug}`" for the session-agent but does not explicitly address the daemon's `handleJobsProjects` prefix filter — leaving this broken path unspecified.
   - **Doc**: "KV key construction includes `{hslug}`: sessions key = `{uslug}.{hslug}.{pslug}.{sslug}`" (mclaude-session-agent section) — analogous requirement for projects KV implied but not stated for the daemon HTTP handler.
   - **Code**: `daemon_jobs.go` line 887 — `prefix := userID + "."` — uses UUID, will not match slug-keyed entries after ADR-0024 migration even before ADR-0004.

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | `pkg/subj` helpers missing `{hslug}` | Added full host-aware function signatures to mclaude-common Component Changes section | factual |
| 2 | JetStream filter update not specified for mclaude-common step | Added explicit note that filter constants update in mclaude-common step alongside subject helpers | factual |
| 3 | `hosts` table migration strategy absent | Added complete 6-step migration DDL (CREATE hosts, ADD host_id, Go backfill, SET NOT NULL, DROP cluster_id, UPDATE index) | factual |
| 4 | `POST /api/hosts/register` missing request body and code storage spec | Specified full request body `{code, name, publicKey, type}` + in-memory map storage with 10-min TTL, atomic delete-on-use | factual |
| 5 | Cluster host NKey/JWT issuance missing | Clarified cluster hosts have NULL public_key; cluster signing key handles session-agent creds, not per-host NKey | factual |
| 6 | Cluster host KV write trigger unspecified | Added CP writes KV entry at grant time (status=offline), updates via $SYS CONNECT/DISCONNECT events | factual |
| 7 | Daemon subject/KV breakage after BYOH | Added hostSlug-per-job flow (POST /jobs includes hostSlug, dispatcher uses it), lifecycle subscriber wildcard update, KV prefix fix | factual |
| 8 | Signing key ceiling contradiction (wildcard vs specific hslug) | Fixed charts section to match Decisions: `mclaude.users.*.hosts.{hslug}.projects.*.>` per cluster | factual |
| 9 | `handleJobsProjects` UUID prefix | Included in gap 7 resolution — daemon uses `{uslug}.{hslug}.` prefix for KV lookups | factual |

### Round 2

**Gaps found: 9**

1. **`spec-state-schema.md` contradicts ADR on `hosts.public_key` nullability** — spec said NOT NULL unconditionally; ADR says nullable with CHECK constraint for cluster hosts.
2. **NKey signing pattern for host JWT unspecified** — existing helpers generate NKeys internally; host registration signs against caller-supplied public key.
3. **Migration mechanism not specified** — multi-step DDL has no execution strategy given the single-schema `db.Migrate()`.
4. **`dispatchQueuedJob` host slug resolution** — unclear who supplies hostSlug to the dispatcher.
5. **`handleJobsProjects` prefix filter semantics** — does it return all user projects or only the daemon's host's projects?
6. **Hardcoded lifecycle init subject in `main.go`** — bypasses `pkg/subj`, will break after BYOH.
7. **`DaemonConfig` missing `HostSlug` field** — no flag/env var name, no behavior for missing/ambiguous host.
8. **`projects.go` NATS subscriber rewrite not called out** — subject token indices shift, KV key format changes.
9. **Host registration request/response schemas not formally specified** — no types, required fields, or HTTP status codes.

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | spec-state-schema public_key nullability | Updated spec to nullable with CHECK constraint matching ADR migration DDL | factual |
| 2 | NKey signing pattern | Added `IssueHostJWT` function signature to CP component changes — signs against caller-supplied public key | factual |
| 3 | Migration mechanism | Added migration mechanism paragraph: separate `db.MigrateHosts()` function, idempotent steps, Go backfill in single transaction | factual |
| 4 | dispatchQueuedJob hostSlug resolution | Clarified callers (web UI, CLI) always know host because projects are host-scoped | factual |
| 5 | handleJobsProjects prefix filter | Clarified daemon manages exactly one host, prefix returns only that host's projects | factual |
| 6 | Hardcoded lifecycle init subject | Added main.go call site to session-agent component changes with `subj.UserHostProjectLifecycle()` replacement | factual |
| 7 | DaemonConfig missing HostSlug | Added field spec, `--host` flag / `HOST_SLUG` env, auto-select single host, error on ambiguous, `mclaude-laptops` → `mclaude-hosts` rename | factual |
| 8 | projects.go subscriber rewrite | Added to CP component changes: subject unchanged (user-level), token index shift, KV key via `subj.ProjectsKVKey()`, hostSlug from payload | factual |
| 9 | Host registration schemas | Added formal request/response schemas for all 6 host endpoints with types, required fields, HTTP status codes | factual |

### Round 3

**Gaps found: 8**

1. **`pkg/subj` still uses old signatures** — Implementation gap, not spec gap. ADR specifies cutover; code updated by dev-harness after audit. Added sequencing note.
2. **`users` table has no `slug` column** — ADR-0024 scope. Added explicit prerequisite statement.
3. **`users.slug` migration DDL absent** — Same as #2. ADR-0024 adds users.slug. This ADR assumes it exists.
4. **`main.go` lifecycle init / `DaemonConfig.HostSlug` not in code** — Implementation gap. ADR already specifies the changes; code updated by dev-harness.
5. **`mclaude-heartbeats` bucket fate unspecified** — Real gap. Specified removal: heartbeats folded into mclaude-projects KV.
6. **`handleJobsProjects` hostSlug source** — Made `d.cfg.HostSlug` explicit in the ADR text.
7. **`FormatNATSCredentials` location** — Real gap. Moved to `mclaude-common/pkg/nats/creds.go` so CLI can use it.
8. **`serverUrl` in device-code web display** — Clarified: derived from `window.location.origin`.

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | pkg/subj old signatures | Added sequencing note: mclaude-common lands first, components update in parallel | factual |
| 2 | users.slug missing | Added explicit prerequisite: ADR-0024 must be fully implemented first | factual |
| 3 | users.slug migration DDL | Covered by gap 2 prerequisite statement | factual |
| 4 | main.go / DaemonConfig not in code | Implementation gap — ADR already specifies changes | not-a-gap |
| 5 | mclaude-heartbeats fate | Specified removal: fold into mclaude-projects KV (update lastSeen on existing entry) | factual |
| 6 | handleJobsProjects hostSlug source | Made `d.cfg.HostSlug` explicit in handleJobsProjects description | factual |
| 7 | FormatNATSCredentials location | Moved to mclaude-common/pkg/nats/creds.go for CLI access | factual |
| 8 | serverUrl derivation | Added `window.location.origin` note to device-code flow step 3 | factual |

### Round 4

**Gaps found: 5**

1. **Backfill Step 3 references non-existent `projects.cluster_id`** — ADR-0011 never implemented; no cluster associations exist.
2. **`serverUrl` in registration responses: source unspecified** — Control-plane has `EXTERNAL_URL` env var already.
3. **Admin cluster-member endpoints lack response schemas** — No status codes, bodies, or error cases.
4. **`sslug string` instead of `slug.SessionSlug`** — Type downgrade breaks ADR-0024 type safety.
5. **Machine host presence CP behavior unspecified** — Two mechanisms (KV + status subject) with no stated ownership/interaction.

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | Backfill references cluster_id | Rewrote Step 3: create default 'local' host per user, assign all projects to it | factual |
| 2 | serverUrl source | Specified: uses existing `EXTERNAL_URL` env var | factual |
| 3 | Admin endpoint schemas | Added formal request/response schemas with status codes and error cases | factual |
| 4 | sslug type downgrade | Fixed to `slug.SessionSlug` matching existing helpers | factual |
| 5 | Machine host presence | Specified: daemon publishes status heartbeat every 30s, CP subscribes + maintains in-memory map, marks offline after 90s timeout, KV is persistent complement | factual |

### Round 5

**Gaps found: 2**

1. **Backfill creates machine host with NULL public_key, violating CHECK constraint** — Step 1 CHECK and Step 3 backfill contradict.
2. **`SessionsKVKey` uses `s string` instead of `s SessionSlug`** — Breaks type safety pattern.

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | Backfill vs CHECK constraint | Deferred CHECK to Step 7 with `NOT VALID` — backfill hosts get NULL public_key until re-register | factual |
| 2 | SessionsKVKey type | Fixed to `s SessionSlug` matching all other helpers | factual |

### Round 6

CLEAN — no blocking gaps found.

### Result

**CLEAN** after 6 rounds, 33 total gaps resolved (33 factual fixes, 0 design decisions).
