# ADR: Typed Slug Scheme for Subjects, URLs, and Keys

**Status**: accepted
**Status history**:
- 2026-04-20: draft
- 2026-04-20: accepted — paired with spec-state-schema.md updates (subject inventory, KV key formats, JetStream filters, Postgres slug columns)

## Overview

Unify how identifiers appear in NATS subjects, HTTP URLs, and KV keys by (a) inserting a typed literal before every slug token (`users.{uslug}.projects.{pslug}.sessions.{sslug}`), (b) constraining all slugs to a safe charset, (c) separating a system-computed immutable slug from a user-editable display name, and (d) restricting `/api/*` HTTP routes to the same nested shape while leaving auth and infra routes flat. The effect is that `mclaude.users.alice-gmail.projects.mclaude.api.sessions.control` is self-describing: every slug is preceded by a word that says what it is, and no slug can ever collide with the reserved words (`users`, `projects`, `sessions`, ...) because the slug charset + reserved-word blocklist exclude them.

## Motivation

Today identifiers appear positionally:

- Subjects: `mclaude.{userId}.{projectId}.api.sessions.control` — `{userId}` and `{projectId}` are bare tokens. A log grep for `alice.mclaude.api.sessions` requires knowing the positional schema.
- KV keys: mixed separators — `{userId}.{projectId}.{sessionId}` for sessions, `{userId}/{jobId}` for the job queue.
- URLs: some routes are typed (`/auth/login`, `/api/projects/`, `/admin/`), others aren't.
- Subjects for clusters are already typed (`mclaude.clusters.{clusterId}.status`); user-space subjects aren't.
- BYOH (ADR-0004) adds another positional slug (`{hostId}`), compounding the problem.

User feedback 2026-04-20: *"we should probably rethink slugs in general. even now, grokking which namespace is which is difficult."* and *"the problem is how do you ensure the literals are sanitized for formatting and injection?"* — the first says the status quo is hard to read; the second says naive concatenation is unsafe.

This ADR is a prerequisite for ADR-0004 BYOH, which will land on the new scheme rather than extending the positional one.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Scheme | Typed literals between every slug | Subjects, URLs, and KV keys all read the same way. Matches REST URL conventions. 2-3 extra tokens per subject, still far under NATS limits (7-8 tokens, 150-250 chars — 6% of the 4096-byte hard limit). |
| Slug charset | `[a-z0-9][a-z0-9-]{0,62}` — lowercase alphanumerics + hyphen, starts with alphanumeric, max 63 chars. No leading `_` (reserved prefix) and no match against the reserved-word blocklist. | Compatible with DNS labels, NATS subject tokens, URL path segments, and K8s resource names. No dot/slash/wildcard/whitespace. Max 63 matches DNS. |
| Reserved literals | Bare literals + fixed blocklist of 10 words: `users, hosts, projects, sessions, clusters, api, events, lifecycle, quota, terminal` | Leading `_` remains reserved for future internal expansion. Subject/URL readability stays clean: `mclaude.users.alice.projects.mclaude.api.sessions.control`. Trade-off: a user cannot name something `users` or `api`. Acceptable. |
| Display name vs slug | Separate fields. Display name is free-form UTF-8 (max 128 chars), mutable. Slug is auto-derived and **immutable** — no API endpoint to rename. | UI shows display name; subjects/URLs/keys use slug. Renames are ops-level migrations only (direct DB + NATS update), never exposed as API. |
| Slug ownership | All slugs system-computed, never user-picked. Users provide display names; the system derives slugs silently. | Users shouldn't think about URL-safe naming. Editable display name covers the human-readable concern; the slug is an internal id that happens to be human-recognizable. |
| User slug derivation | `{slugify(name-or-local-part)}-{domain.split('.')[0]}` at user creation. Collision on same-(name,domain) pair → numeric suffix (`-2`, `-3`). Immutable after creation. | Deterministic, no DB-uniqueness pre-check in the common path. Domain segment always included so `richard@rbc.com` → `richard-rbc` and `richard@gmail.com` → `richard-gmail` never collide. Email changes after creation do **not** rewrite the slug — documented as static-at-creation. |
| Project / host / cluster slug derivation | `slugify(display_name)` at creation; collision within scope (per-user for project/host, global for cluster) → numeric suffix. Immutable. | Same no-user-prompt rule. Display name shown in UI; slug in subject/URL/key. |
| `slugify()` algorithm | Lowercase → NFD Unicode decomposition → strip combining marks → replace runs of non-`[a-z0-9]` with `-` → trim leading/trailing `-` → truncate to 63 chars. Fallback: if empty, reserved, or leading `_`, emit `u-{6 base32 chars}` for users, `p-{6}` / `h-{6}` / `c-{6}` for projects/hosts/clusters. | Handles non-ASCII, punctuation, emoji-only names. Fallback ensures every row has a valid slug even in pathological cases. |
| User slug uniqueness | Globally unique per instance (v1 is single-instance) | Enforced by a unique index on `users.slug`. |
| Cross-user URL access | Hard 403 when JWT `sub` ≠ URL `{uslug}` | Simple, predictable, audit-friendly. Admin subtree `/admin/users/{uslug}/...` bypasses the check with admin-role validation. |
| Cluster subtree | Migrate to `mclaude.clusters.{cslug}.api.*` | Full consistency with users/projects/hosts. `clusters.slug` column added; backfilled from `clusters.name` at cutover. |
| KV key separator | Uniform `.` across all buckets | Matches existing `mclaude-sessions` format and NATS convention. Enables wildcard key matching. `mclaude-job-queue` renames from `{uid}/{jobId}` to `{uslug}.{jobId}`. |
| Quota subject shape | Leaf `mclaude.users.{uslug}.quota` (not under `.api.`) | Quota is a broadcast signal, not a request/reply endpoint. `.api.` is reserved for client→service calls. Keeping quota as a leaf sibling of `api` makes subscription filters simpler. |
| HTTP URL scheme | `/api/*` nests under user scope: `/api/users/{uslug}/projects/{pslug}/...`. Auth + infra routes stay flat: `/auth/*`, `/version`, `/health*`, `/metrics`, `/readyz`. Admin subtree: `/admin/users/{uslug}/...`. | Logs read uniformly across NATS and HTTP. Auth and infra predate the user scope and have no per-user variant. |
| CLI identifier surface | Short-forms with context defaults in `~/.mclaude/context.json` (current user, current project). `@pslug` style in commands disambiguates from display names. | User never types `/api/users/alice-gmail/projects/mclaude/...` by hand. Context file is the default; flags override. |
| User identifier in Postgres | New `users.slug` TEXT column, unique, NOT NULL. `users.id` UUID stays as PK and foreign-key target. | Foreign keys stay UUID so joins don't change. Slug is a second, equally-required column. |
| Migration scope | Hard cutover — single spec commit + single dev-harness pass. No dual-path period. | Pre-GA, all components deploy together via CI. No external users. Dual-path doubles permission grants and complicates subject-construction helpers for no payoff. |

## User Flow

User-facing behavior changes in four places; all are cosmetic or navigational. The slug is never entered or edited by the user.

1. **Creating a project**: user types display name "My New Project" → control-plane derives slug `my-new-project` silently → UI shows `My New Project` everywhere; logs show `projects.my-new-project`.
2. **Creating a host (ADR-0004)**: same flow — display name "Work MBP 16-inch (2023)" → slug `work-mbp-16-inch-2023`.
3. **URL of a session**: browser URL becomes `/api/users/alice-gmail/projects/mclaude/sessions/s-42`. Display names render; slugs live in the path.
4. **Logs**: `mclaude.users.alice-gmail.projects.mclaude.api.sessions.control` — log scanners and humans both read it the same way without a schema legend.

Existing users, projects, sessions, and clusters are renamed in place at cutover time. Display names are preserved; slugs are derived from current `name` columns by the migration.

## Component Changes

### `mclaude-control-plane`

- New package `pkg/slug` (Go): `Slugify(displayName) string`, `Validate(slug) error`, `ValidateOrFallback(slug, kind) string`. Reserved-word list is a typed constant, not a string array.
- New package `pkg/subj` (Go): typed subject-construction helpers. Each helper takes a validated slug type (`UserSlug`, `ProjectSlug`, `SessionSlug`) — not raw strings — so an unvalidated slug fails at compile time. Example: `subj.UserAPI(u UserSlug, rest ...Literal) string` produces `mclaude.users.{u}.api.{rest...}`.
- Postgres migration:
  - `users`: add `slug TEXT NOT NULL UNIQUE`, backfilled via `slugify(name or email local-part) || '-' || split_part(email, '@', 2)`.
  - `projects`: add `slug TEXT NOT NULL`, unique per user (`UNIQUE (user_id, slug)`), backfilled from `name`.
  - `clusters`: add `slug TEXT NOT NULL UNIQUE`, backfilled from `name`.
  - Future `hosts` (ADR-0004): same pattern.
- `users.id`, `projects.id`, `clusters.id` UUID PKs stay. All foreign keys unchanged.
- All subject-publishing sites switch to `subj.*` helpers. Direct `fmt.Sprintf("mclaude.%s...", ...)` is removed; repo-level linter prevents reintroduction.
- HTTP handlers: `/api/projects/*`, `/api/sessions/*`, etc. move under `/api/users/{uslug}/...`. Auth and infra routes keep their current paths. Admin subtree at `/admin/users/{uslug}/...`.
- `POST /api/users/{uslug}/projects` returns `{id, slug, name, ...}` so the SPA can update URLs without a second roundtrip.

### `mclaude-session-agent`

- Subscriptions switch to the new subject shape via `pkg/subj` (shared with control-plane).
- KV key format changes from `{userId}.{projectId}.{sessionId}` to `{uslug}.{pslug}.{sslug}`.
- `handleControl` and other subject-matching code reads slugs out of the new token positions.
- Session state stored in `mclaude-sessions` gains `userSlug`, `projectSlug`, `slug` (session slug) string fields alongside the existing UUID `id` / `projectId` — session-agent's resume/recovery path constructs KV keys from these fields, not from UUIDs.

### `mclaude-session-agent` (daemon job dispatcher)

The daemon in `mclaude-session-agent` hosts the `runJobDispatcher` loop (ADR-0009). It polls `mclaude-sessions` KV to watch active sessions and manipulates jobs in `mclaude-job-queue`.

- `JobEntry` struct gains three new fields: `UserSlug string`, `ProjectSlug string`, `SessionSlug string`. Populated at job creation by the control-plane handler (which already has all three slugs in the request path).
- The dispatcher uses the slug fields (not the UUIDs) to construct KV keys: `{UserSlug}.{ProjectSlug}.{SessionSlug}` for `mclaude-sessions`, `{UserSlug}.{ID}` for `mclaude-job-queue`. UUID fields (`UserID`, `ProjectID`, `SessionID`) stay on the struct for Postgres foreign-key joins and logging compatibility.

### `mclaude-web`

- New TS module `src/lib/slug.ts` mirroring `pkg/slug` (Slugify + Validate + Fallback) for display consistency. No edit UI — the slug is never shown as an editable field.
- `src/lib/subj.ts` mirrors `pkg/subj`. Publishes via typed helpers only.
- Routes under `/session/*`, `/project/*` rewrite to include `{uslug}/{pslug}` path segments derived from the JWT + current project. React Router v6 parametric segments.
- Display name is the only field surfaced in project-creation and host-creation sheets; slug is shown as a grayed-out preview under the display-name input ("saved as: `my-new-project`") but not editable.

### `mclaude-cli`

- `~/.mclaude/context.json` gains `userSlug`, `projectSlug`, `hostSlug`.
- Commands accept short forms: `mclaude session list` uses the context file; `mclaude session list -p other-project` overrides. `@pslug` is accepted as a positional short form.
- Slug flags are validated locally before any API call.

### `charts/mclaude`

- NATS account permission templates switch to the new subject shape. Grants use `mclaude.users.{uslug}.>` for per-user scope and `mclaude.clusters.{cslug}.>` for cluster-scope. No dual grants — hard cutover.
- Postgres migration Job template runs the backfill migration on upgrade.

## Data Model

### Slug columns (Postgres)

Migration strategy: the backfill is performed by a **Go migration program**, not pure SQL. Reason: the `slugify()` algorithm requires Unicode NFD normalization + combining-mark stripping + the reserved-word blocklist + fallback generation — all cleaner in Go than plpgsql, and we already call the same helper at runtime from control-plane, so running it at migration time guarantees parity.

Schema migration (pure DDL):

```sql
-- users
ALTER TABLE users ADD COLUMN slug TEXT;         -- nullable during backfill
-- (backfill runs next, in Go)
ALTER TABLE users ALTER COLUMN slug SET NOT NULL;
CREATE UNIQUE INDEX idx_users_slug ON users (slug);

-- projects
ALTER TABLE projects ADD COLUMN slug TEXT;
ALTER TABLE projects ALTER COLUMN slug SET NOT NULL;
CREATE UNIQUE INDEX idx_projects_user_slug ON projects (user_id, slug);

-- clusters
ALTER TABLE clusters ADD COLUMN slug TEXT;
ALTER TABLE clusters ALTER COLUMN slug SET NOT NULL;
CREATE UNIQUE INDEX idx_clusters_slug ON clusters (slug);
```

Backfill algorithm (Go program `cmd/slug-backfill`, run between the `ADD COLUMN` and the `SET NOT NULL` steps, inside the same deploy transaction):

```
For each users row (ordered by created_at):
  base = slugify(row.name or email_local_part(row.email))
  candidate = base + "-" + domain_first_segment(row.email)
  suffix = 0
  while candidate already used in users.slug:
    suffix += 1
    candidate = base + "-" + domain_first_segment + "-" + str(suffix)
  row.slug = candidate
  mark candidate as used

For each projects row (ordered by created_at, grouped by user_id):
  base = slugify(row.name)
  candidate = base
  suffix = 0
  while candidate already used in projects.slug for this user_id:
    suffix += 1
    candidate = base + "-" + str(suffix)
  row.slug = candidate

For each clusters row (ordered by created_at):
  base = slugify(row.name)
  candidate = base
  suffix = 0
  while candidate already used in clusters.slug:
    suffix += 1
    candidate = base + "-" + str(suffix)
  row.slug = candidate
```

`slugify()` fallback: if `base` is empty after normalization, or is a reserved word, or starts with `_`, substitute `u-{6 base32 chars}` (users) / `p-{6}` / `c-{6}` — where the 6 chars derive from the first 30 bits of the row's UUID for determinism.

`users.id`, `projects.id`, `clusters.id` stay as UUID PKs. All existing foreign keys continue to reference `id`.

### NATS subject inventory

| Old | New |
|-----|-----|
| `mclaude.{userId}.api.projects.create` | `mclaude.users.{uslug}.api.projects.create` |
| `mclaude.{userId}.api.projects.updated` | `mclaude.users.{uslug}.api.projects.updated` |
| `mclaude.{userId}.quota` | `mclaude.users.{uslug}.quota` |
| `mclaude.{userId}.{projectId}.api.sessions.input` | `mclaude.users.{uslug}.projects.{pslug}.api.sessions.input` |
| `mclaude.{userId}.{projectId}.api.sessions.control` | `mclaude.users.{uslug}.projects.{pslug}.api.sessions.control` |
| `mclaude.{userId}.{projectId}.api.sessions.create` | `mclaude.users.{uslug}.projects.{pslug}.api.sessions.create` |
| `mclaude.{userId}.{projectId}.api.sessions.delete` | `mclaude.users.{uslug}.projects.{pslug}.api.sessions.delete` |
| `mclaude.{userId}.{projectId}.api.terminal.*` | `mclaude.users.{uslug}.projects.{pslug}.api.terminal.*` |
| `mclaude.{userId}.{projectId}.events.{sessionId}` | `mclaude.users.{uslug}.projects.{pslug}.events.{sslug}` |
| `mclaude.{userId}.{projectId}.lifecycle.{sessionId}` | `mclaude.users.{uslug}.projects.{pslug}.lifecycle.{sslug}` |
| `mclaude.clusters.{clusterId}.projects.provision` | `mclaude.clusters.{cslug}.api.projects.provision` |
| `mclaude.clusters.{clusterId}.status` | `mclaude.clusters.{cslug}.api.status` |

### JetStream stream filter inventory

Stream names stay unchanged. Subject filters update to a 5-token wildcard prefix for user-scoped streams.

| Stream | Old filter | New filter |
|--------|-----------|-----------|
| `MCLAUDE_API` | `mclaude.*.*.api.sessions.>` | `mclaude.users.*.projects.*.api.sessions.>` |
| `MCLAUDE_EVENTS` | `mclaude.*.*.events.*` | `mclaude.users.*.projects.*.events.*` |
| `MCLAUDE_LIFECYCLE` | `mclaude.*.*.lifecycle.*` | `mclaude.users.*.projects.*.lifecycle.*` |

Streams are recreated (not renamed) during the hard cutover because NATS JetStream does not support in-place filter change without a stream reset.

### NATS permission grant inventory

Grant strings per identity type, derived from the new subject tree:

**SPA (browser user)** — JWT minted on login, `sub = {uslug}`:
```
Publish allow:
  mclaude.users.{uslug}.>
  _INBOX.>
Subscribe allow:
  mclaude.users.{uslug}.>
  $KV.mclaude-sessions.>       # KV watch for session state
  $KV.mclaude-projects.>       # KV watch for project list
  $JS.API.DIRECT.GET.>         # KV direct get
  _INBOX.>
Publish deny:
  $KV.>
  $JS.>
  mclaude.system.>
```

**Control-plane** — static JWT signed with account seed. Unchanged grants (`mclaude.>`, `$KV.>`, `$JS.>`, `_INBOX.>`, `$SYS.ACCOUNT.>`). Control-plane already holds the full subject tree; the slug rename is a mechanical substitution in routing code, not in grants.

**K8s / BYOH Controller** — scoped to one cluster (cluster slug):
```
Publish allow:
  $KV.mclaude-projects.>       # see "mclaude-projects KV key (scope note)" below
  mclaude.clusters.{cslug}.>   # reply inbox / publish replies
  _INBOX.>
Subscribe allow:
  mclaude.clusters.{cslug}.api.>  # commands from control-plane
Publish deny:
  mclaude.users.*.>            # cannot publish user-level commands
  $KV.mclaude-sessions.>
  $JS.>
```

**Session-agent** — minted by controller's cluster signing key. Session-agents currently run per-project (not per-session) — see Note below. Ceiling inherited from signing key, JWT claims narrow further.

- **Signing key ceiling** (registered on the account at cluster registration time): `mclaude.users.*.projects.*.>` — clamps any JWT the controller mints. Replaces the old `mclaude.*.sessions.{clusterId}.*.>` ceiling from ADR-0016, which is superseded by this ADR's subject tree.
- **Session-agent JWT claims** (what each minted JWT asserts):
  ```
  Publish allow:
    mclaude.users.{uslug}.projects.{pslug}.events.>
    mclaude.users.{uslug}.projects.{pslug}.lifecycle.>
    _INBOX.>
  Subscribe allow:
    mclaude.users.{uslug}.projects.{pslug}.api.sessions.>
    mclaude.users.{uslug}.projects.{pslug}.api.terminal.>
  ```

> **Note on session-agent scope**: ADR-0016 specified a per-session JWT ceiling (`mclaude.*.sessions.{clusterId}.{sessionId}.>`). The live system (see `spec-state-schema.md`) runs session-agents at project granularity — a single agent handles every session in one project. ADR-0024 aligns JWT scope with live behavior: project-scoped grants. Moving to per-session JWTs is a separate concern (would be a new ADR on session isolation).

### `mclaude-projects` KV key (scope note)

`spec-state-schema.md` currently specifies key format `{userId}.{projectId}` (user-prefixed). ADR-0011 and ADR-0016 specify `{clusterId}.{projectId}` (cluster-prefixed) for controller write-scope isolation. These two are already inconsistent in the repo — a pre-existing drift unrelated to slugs.

ADR-0024 preserves the **current live shape** (user-prefixed). The mapping is `{userId}.{projectId}` → `{uslug}.{pslug}`. Reconciling with ADR-0011/ADR-0016's cluster-prefixed intent is out of scope for this ADR; it will be addressed by a future multi-cluster KV-partitioning ADR when cluster-scoped writes are actually rolled out. Until that ADR lands, the controller write grant is `$KV.mclaude-projects.>` (the same broad scope it has today per spec-state-schema) — not narrowed by cluster.

### KV key format

| Bucket | Old key | New key |
|--------|---------|---------|
| `mclaude-sessions` | `{userId}.{projectId}.{sessionId}` | `{uslug}.{pslug}.{sslug}` |
| `mclaude-projects` | `{userId}.{projectId}` | `{uslug}.{pslug}` (see "`mclaude-projects` KV key (scope note)" above) |
| `mclaude-clusters` | `{userId}` | `{uslug}` |
| `mclaude-laptops` | `{userId}.{hostname}` | `{uslug}.{hostname}` (pre-ADR-0004 — `{hostname}` is still the raw machine hostname; ADR-0004 later replaces the `hostname` column with `hslug`) |
| `mclaude-job-queue` | `{userId}/{jobId}` | `{uslug}.{jobId}` |

`{jobId}` and `{hostname}` remain UUID/opaque tokens (not slugs) — no change in token shape, only separator.

### Hard-cutover KV rekeying

Because the hard cutover rewrites every key, the migration job for each bucket:

1. Snapshots all existing keys at deploy time.
2. For each key, joins against Postgres (`users.id → users.slug`, `projects.id → projects.slug`, etc.) to compute the new slug-based key.
3. Writes the value under the new key.
4. Purges the old key.

For `mclaude-sessions`, sessions are ephemeral — sessions that exist at cutover time are stopped as part of the deploy (session-agents are restarted anyway when pods redeploy). The migration job can purge `mclaude-sessions` entirely rather than rekey it. For `mclaude-projects`, `mclaude-clusters`, `mclaude-laptops`, and `mclaude-job-queue`, rekeying is required because these hold durable state.

### HTTP URL inventory

Auth + infra routes (no per-user variant, stay flat):

| Route | Status |
|-------|--------|
| `POST /auth/login`, `POST /auth/refresh`, `POST /auth/logout` | unchanged |
| `GET /version`, `GET /health`, `GET /healthz`, `GET /metrics`, `GET /readyz` | unchanged |

User-scoped API routes (moved under `/api/users/{uslug}/...`):

| Old | New |
|-----|-----|
| `GET /api/projects` | `GET /api/users/{uslug}/projects` |
| `POST /api/projects` | `POST /api/users/{uslug}/projects` |
| `GET /api/projects/{pid}` | `GET /api/users/{uslug}/projects/{pslug}` |
| `DELETE /api/projects/{pid}` | `DELETE /api/users/{uslug}/projects/{pslug}` |
| `GET /api/projects/{pid}/sessions` | `GET /api/users/{uslug}/projects/{pslug}/sessions` |
| `POST /api/projects/{pid}/sessions` | `POST /api/users/{uslug}/projects/{pslug}/sessions` |
| `GET /api/projects/{pid}/sessions/{sid}` | `GET /api/users/{uslug}/projects/{pslug}/sessions/{sslug}` |
| `DELETE /api/projects/{pid}/sessions/{sid}` | `DELETE /api/users/{uslug}/projects/{pslug}/sessions/{sslug}` |
| `GET /api/jobs` | `GET /api/users/{uslug}/jobs` |
| `POST /api/jobs` | `POST /api/users/{uslug}/jobs` |
| `GET /api/jobs/{jobId}` | `GET /api/users/{uslug}/jobs/{jobId}` |
| `DELETE /api/jobs/{jobId}` | `DELETE /api/users/{uslug}/jobs/{jobId}` |

Admin routes — cluster admin endpoints keep cluster-scoped addressing (cluster membership is cluster-centric, not user-centric), migrated to cluster slugs:

| Old | New |
|-----|-----|
| `POST /admin/clusters` | `POST /admin/clusters` (create — no cluster id yet) |
| `GET /admin/clusters` | `GET /admin/clusters` |
| `GET /admin/clusters/{id}` | `GET /admin/clusters/{cslug}` |
| `POST /admin/clusters/{id}/members` | `POST /admin/clusters/{cslug}/members` |
| `DELETE /admin/clusters/{id}/members/{userId}` | `DELETE /admin/clusters/{cslug}/members/{uslug}` |
| `GET /admin/users` | `GET /admin/users` (list) |
| `GET /admin/users/{userId}` | `GET /admin/users/{uslug}` |

`{jobId}` stays UUID-shaped for now — jobs are transient and creating slugs for them adds no readability value. (Out of scope: revisiting job identifiers if logs become hard to parse in practice.)

## Error Handling

- **Slug validation at ingress**: control-plane returns HTTP 400 with `{code:"invalid_slug", reason:"reserved_word|charset|length", field:"slug"}`. SPA never hits this path in the normal flow (slugs are server-derived); it is a defense against forged requests.
- **Reserved-word match**: slugify fallback kicks in automatically, producing `u-{base32}`, `p-{base32}`, etc.
- **Unicode / empty / emoji-only display names**: slugify runs NFD + charset replacement; if result is empty, falls back to `{type}-{base32}`.
- **Subject-construction guardrail**: `pkg/subj` and `src/lib/subj.ts` accept only typed slug structs. Passing a raw string is a compile-time error in Go; a runtime assertion in TS dev builds. Never builds a subject from an unvalidated string.
- **Cross-user URL access**: control-plane middleware compares JWT `sub` claim's `uslug` with the URL's `{uslug}` path segment. Mismatch → 403 `{code:"forbidden", reason:"cross_user_access"}`.
- **Unknown slug in URL**: 404 at the resource-lookup step (no special-case — same as any other missing resource).

## Security

- **Injection defense**: typed literals are hardcoded constants; user-sourced slugs are constrained by charset. A user cannot craft a slug containing `.`, `*`, or `>` that would be interpreted as a subject delimiter or wildcard. Typed subject-construction helpers refuse raw strings — this is the primary security benefit.
- **Privilege boundaries**: NATS credentials grant by subject prefix (`mclaude.users.{uslug}.>`). With charset-constrained slugs, the boundary is literal-string-equal and cannot be escaped by crafted IDs.
- **Enumeration resistance**: slugs are human-readable by design, so enumeration is easier than with UUIDs. Acceptable trade-off — authorization is checked per-subject, not by obscurity. UUIDs remain the Postgres PK for foreign-key stability, not for secrecy.
- **Reserved-word blocklist is append-only**: removing a word from the list could allow a new subject to be shadowed. Additions are safe (they just reject new slugs that match; existing slugs are unaffected since all slugs are charset-valid at creation time).
- **Admin bypass**: `/admin/*` routes bypass the cross-user check but require an admin role claim in the JWT. Admin actions are logged by uslug + target uslug.

## Impact

Specs touched in this ADR's co-commit:

- `docs/spec-state-schema.md` — full subject inventory rewrite, KV key format update, Postgres slug columns.

Components implementing: `mclaude-control-plane`, `mclaude-session-agent`, `mclaude-cli`, `mclaude-web`, `charts/mclaude`.

Downstream: **ADR-0004 (BYOH) rebases on this scheme.** Other existing ADRs that reference subjects (ADR-0011 multi-cluster, ADR-0016 nats-security, ADR-0009 quota-aware-scheduling) get a mechanical subject-string update; their decisions don't change.

## Scope

In v1:
- `pkg/slug` + `pkg/subj` (Go), `src/lib/slug.ts` + `src/lib/subj.ts` (TS).
- New Postgres slug columns for `users`, `projects`, `clusters` with backfill migration.
- New subject schema applied to all existing subjects (the table above).
- KV key format unified on `.` (including `mclaude-job-queue` rename).
- HTTP `/api/*` restructured to nested `{uslug}/{pslug}` form; auth + infra stay flat.
- NATS account permission templates updated in the Helm chart.
- `~/.mclaude/context.json` with `userSlug` / `projectSlug` defaults; CLI short-forms.

Deferred:
- Slug-based fine-grained ACLs (e.g. per-session deny rules).
- Multi-tenant / per-org reserved-word policies.
- `hosts` table + `{hslug}` subjects — lands with ADR-0004.
- A rename API (if ever needed, would be a new ADR that carefully thinks through permission grant invalidation + cache invalidation).
- **Kubernetes resource naming.** `mclaude-{userId}` namespaces, `project-{projectId}` PVCs, `mclaude-session-agent-{projectId}` deployments, and `MCProject` CRDs continue to use UUIDs. K8s names have stricter constraints (DNS-1123 length/charset) and renaming them requires pod recreation + namespace moves — a larger scope than the slug-in-subjects migration. A separate future ADR can address K8s-resource naming if readability pressure warrants it.
- `mclaude-projects` KV key partitioning (user-prefix vs cluster-prefix) — pre-existing drift between spec-state-schema.md (`{userId}.{projectId}`) and ADR-0011/ADR-0016 (`{clusterId}.{projectId}`). ADR-0024 preserves the live user-prefixed shape; reconciliation is future work.

## Open questions

_All resolved — see Decisions table._

## Implementation Plan

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| mclaude-control-plane | ~1,200 | ~90k | `pkg/slug` (150) + `pkg/subj` (200) + Postgres migration (100) + handler restructuring (400) + subject-publish rewrites (200) + tests (150) |
| mclaude-session-agent | ~500 | ~60k | Subscription rewrites (200) + KV key rewrites (150) + tests (150) |
| mclaude-web | ~700 | ~65k | `slug.ts` + `subj.ts` (200) + route restructuring (150) + slug preview component (100) + publish-call migrations (150) + tests (100) |
| mclaude-cli | ~400 | ~40k | Context file (100) + flag validation (100) + short-form parser (100) + tests (100) |
| charts/mclaude | ~150 | ~25k | NATS permission templates (100) + migration Job template (50) |

**Total estimated tokens:** ~280k
**Estimated wall-clock:** ~1.5h of 5h budget (≈30%). Under the per-component ceiling; dev-harness can handle control-plane in one pass without context-pressure re-invocation.
