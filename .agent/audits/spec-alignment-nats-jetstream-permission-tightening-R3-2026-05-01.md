## Run: 2026-05-01T12:00:00Z (Round 3)

ADR: `docs/adr-0054-nats-jetstream-permission-tightening.md` (status: draft)

Specs checked:
- `docs/spec-state-schema.md`
- `docs/spec-nats-payload-schema.md`
- `docs/spec-nats-activity.md`
- `docs/spec-nats-data-taxonomy.md`
- `docs/mclaude-control-plane/spec-control-plane.md`
- `docs/mclaude-session-agent/spec-session-agent.md`
- `docs/mclaude-common/spec-common.md`

Purpose: Verify all Round 2 gaps are fixed, then full forward pass for remaining issues.

---

### Round 2 Gap Verification

All 7 Round 2 gaps have been fixed:

| R2 # | Issue | Status |
|-------|-------|--------|
| 44 | Stale divergence notice in spec-nats-activity.md | FIXED — notice removed |
| 45 | User JWT Pub.Allow incorrectly included `mclaude.hosts.{hslug}.users.alice.>` | FIXED — now shows only `mclaude.users.alice.hosts.*.>` |
| 46 | spec-nats-activity §6: SPA published directly to host-scoped NATS for project create | FIXED — shows SPA→HTTP POST→CP→NATS fan-out, states "CP itself publishes" |
| 47 | spec-nats-activity §13b: SPA published directly to host-scoped NATS for project delete | FIXED — shows SPA→HTTP DELETE→CP→NATS fan-out |
| 48 | spec-nats-payload-schema: project create/delete listed SPA/CLI as publisher | FIXED — now "control-plane (fan-out after HTTP validation)" |
| 54 | spec-nats-payload-schema: host type enum was `machine \| kubernetes` | FIXED — now `machine \| cluster` |
| 55 | spec-nats-activity §16 summary: Alice could publish to host provisioning subjects | FIXED — now "None (project create/delete via HTTP to CP)" |

---

### Phase 1 — ADR → Spec (forward pass)

All R2 items 1-53 (REFLECTED) were re-verified and remain correct. Only new findings below.

| # | ADR (section) | ADR text | Spec location | Verdict | Direction | Notes |
|---|---------------|----------|---------------|---------|-----------|-------|
| 1 | Decisions: Isolation model | "Per-user JetStream resources" | spec-state-schema.md, spec-nats-data-taxonomy.md, spec-nats-activity.md: all three per-user resources documented | REFLECTED | — | |
| 2 | Decisions: KV buckets | Per-user sessions/projects, shared hosts | All specs: consistent | REFLECTED | — | |
| 3 | Decisions: Sessions stream | `MCLAUDE_SESSIONS_{uslug}` consolidates events, commands, lifecycle | All specs: consistent | REFLECTED | — | |
| 4 | Decisions: Host subject scheme | `mclaude.hosts.{hslug}.>` | All specs: consistent | REFLECTED | — | |
| 5 | Decisions: Session-agent scope | Per-project | All specs: consistent | REFLECTED | — | |
| 6 | Decisions: Credential auth | HTTP challenge-response for all identity types | spec-control-plane.md, spec-nats-payload-schema.md, spec-nats-activity.md: documented | REFLECTED | — | |
| 7 | Decisions: NATS topology | Direct hub connection, leaf-node removed | spec-state-schema.md, spec-session-agent.md: documented | REFLECTED | — | |
| 8 | Full Permission Specs: User JWT | Complete Pub.Allow/Sub.Allow | spec-state-schema.md §User JWT permissions: matches ADR | REFLECTED | — | |
| 9 | Full Permission Specs: Session-agent JWT | Complete per-project Pub.Allow/Sub.Allow | spec-state-schema.md §Session-agent JWT: matches ADR | REFLECTED | — | |
| 10 | Full Permission Specs: Host/Controller JWT | `mclaude.hosts.{hslug}.>` + `$SYS` in Sub.Allow, zero JetStream | spec-state-schema.md §Host/controller JWT: matches ADR | REFLECTED | — | |
| 11 | Session subject hierarchy | Consolidated `sessions.>` with all verbs | All specs: consistent | REFLECTED | — | |
| 12 | Session slug blocklist | `create`, `delete`, `input`, `config`, `control` added | spec-state-schema.md, spec-common.md: all five listed | REFLECTED | — | |
| 13 | Consumer patterns | Dashboard (DeliverNew lifecycle), Chat (DeliverAll per-session), Agent (DeliverNew commands) | spec-state-schema.md, spec-nats-data-taxonomy.md, spec-nats-activity.md: all three documented | REFLECTED | — | |
| 14 | KV key formats | Hierarchical with literal type-tokens | All specs: consistent | REFLECTED | — | |
| 15 | Postgres: `host_access` | `(host_id, user_id, granted_at)` with composite PK | spec-state-schema.md: matches ADR | REFLECTED | — | |
| 16 | Postgres: `agent_credentials` | `nkey_public TEXT NOT NULL UNIQUE` | spec-state-schema.md: `nkey_public` is `TEXT NOT NULL` but **no UNIQUE constraint** listed | GAP | SPEC→FIX | ADR says `nkey_public TEXT NOT NULL UNIQUE` and "enables fast lookup by public key for `POST /api/auth/challenge`". Spec §agent_credentials Constraints lists only `UNIQUE (user_id, host_slug, project_slug)`. Missing `UNIQUE` on `nkey_public`. |
| 17 | Postgres: `hosts.nkey_public` UNIQUE | "`public_key` column: add `UNIQUE` constraint" | spec-state-schema.md §hosts: `nkey_public` column has no constraint. Constraints section lists only `UNIQUE (slug)`. | GAP | SPEC→FIX | ADR explicitly adds UNIQUE to `hosts.public_key` (now `nkey_public`) for index-backed lookup in `/api/auth/challenge`. Spec is missing this constraint. |
| 18 | Host Lifecycle Subjects | `manage.grant` and `manage.revoke-access` subjects | spec-nats-data-taxonomy.md §Resource Hierarchy + §Subject Namespaces: **missing** `grant` and `revoke-access`. Has stale `manage.bind` instead. | GAP | SPEC→FIX | ADR-0054 defines `manage.grant` and `manage.revoke-access` as host access management subjects. spec-nats-data-taxonomy.md lists `manage.bind` (not in ADR-0054) and omits `grant` / `revoke-access`. Other specs (spec-state-schema.md, spec-nats-payload-schema.md, spec-control-plane.md, spec-nats-activity.md) all correctly document these subjects. Only the data taxonomy spec has the stale `manage.bind`. |
| 19 | Stream configuration | LimitsPolicy, MaxAge 30d, FileStorage, DiscardOld | spec-state-schema.md: matches ADR | REFLECTED | — | |
| 20 | Host registration | `mclaude.users.{uslug}.hosts._.register` → `{ok, slug}` — no JWT | All specs: consistent | REFLECTED | — | |
| 21 | Host access grants/revocation | `manage.grant` / `manage.revoke-access` with JWT revocation | spec-state-schema.md, spec-nats-payload-schema.md, spec-control-plane.md, spec-nats-activity.md: all correct | REFLECTED | — | |
| 22 | Host deregistration + emergency revocation | `manage.deregister` and `manage.revoke` | All specs: correct | REFLECTED | — | |
| 23 | Agent registration | `mclaude.hosts.{hslug}.api.agents.register` | All specs: correct | REFLECTED | — | |
| 24 | CP project fan-out | HTTP→CP→NATS for project create/delete | All specs: correct (verified R2 fixes) | REFLECTED | — | |
| 25 | Bucket lifecycle | CP creates per-user buckets on registration, shared on deployment | spec-control-plane.md: correct | REFLECTED | — | |
| 26 | NATS resolver | `resolver: nats` replacing `resolver: MEMORY` | spec-state-schema.md: correct | REFLECTED | — | |
| 27 | HostKVState | `{slug, name, type, online, lastSeenAt}` — type is `machine \| cluster` | spec-state-schema.md, spec-nats-payload-schema.md: both use `machine \| cluster` | REFLECTED | — | |
| 28 | Session-agent: ordered push consumers | Replaces pull consumers | spec-session-agent.md: correct | REFLECTED | — | |
| 29 | Session-agent: no stream creation | CP creates streams | spec-session-agent.md: correct | REFLECTED | — | |
| 30 | Session-agent: HTTP challenge-response auth | 5-min TTL, proactive refresh | spec-session-agent.md: correct | REFLECTED | — | |
| 31 | Common: `FilterMclaudeSessions` | Replaces three legacy filters | spec-common.md: correct | REFLECTED | — | |
| 32 | Common: KV key helpers | New signatures with literal type-tokens | spec-common.md: correct | REFLECTED | — | |
| 33 | Common: reserved words | Five new reserved words added | spec-common.md: correct | REFLECTED | — | |
| 34 | `users.nkey_public` UNIQUE | New UNIQUE column | spec-state-schema.md: UNIQUE documented on `users.nkey_public` | REFLECTED | — | |
| 35 | Hosts table: `owner_id` rename | `user_id` → `owner_id` | spec-state-schema.md: `owner_id` used | REFLECTED | — | |
| 36 | Hosts table: removed columns | `role`, `js_domain`, `leaf_url`, `account_jwt`, `direct_nats_url` | spec-state-schema.md: none of these columns present | REFLECTED | — | |
| 37 | Hosts table: `user_jwt` → `nats_jwt` | Column rename | spec-state-schema.md: `nats_jwt` used | REFLECTED | — | |
| 38 | Error handling: permission violation triggers refresh | Agent refreshes on permission error | spec-session-agent.md: documented | REFLECTED | — | |

### Summary

- Reflected: 35
- Gap: 3
- Partial: 0

### Gap Details

**GAP 1 [SPEC→FIX]** (#16): `agent_credentials.nkey_public` missing UNIQUE constraint
- ADR text: "`nkey_public TEXT NOT NULL UNIQUE` enables fast lookup by public key for `POST /api/auth/challenge`"
- Spec location: `docs/spec-state-schema.md` §agent_credentials — Constraints section lists only `UNIQUE (user_id, host_slug, project_slug)`, missing `UNIQUE` on `nkey_public`.
- Fix: Add `nkey_public` UNIQUE to the agent_credentials column constraint and/or Constraints section.

**GAP 2 [SPEC→FIX]** (#17): `hosts.nkey_public` missing UNIQUE constraint
- ADR text: "`public_key` column: add `UNIQUE` constraint (enables index-backed lookup in `/api/auth/challenge`)"
- Spec location: `docs/spec-state-schema.md` §hosts — `nkey_public` column has no constraint listed. Constraints section lists only `UNIQUE (slug)`.
- Fix: Add UNIQUE constraint to `hosts.nkey_public` column.

**GAP 3 [SPEC→FIX]** (#18): `spec-nats-data-taxonomy.md` has stale `manage.bind` subject, missing `manage.grant` and `manage.revoke-access`
- ADR text: Host Lifecycle Subjects table lists `manage.grant` ("grant another user access") and `manage.revoke-access` ("revoke another user's access"). No `manage.bind` subject exists in ADR-0054.
- Spec location: `docs/spec-nats-data-taxonomy.md` lines 44-50 (Resource Hierarchy) and lines 118-124 (Subject Namespaces) — `manage.bind` present, `manage.grant` and `manage.revoke-access` absent.
- Fix: Replace `manage.bind` with `manage.grant` and `manage.revoke-access` in both the Resource Hierarchy tree and the Subject Namespaces listing.
