## Run: 2026-04-20T00:00:00Z

Component: `mclaude-common`
Docs: `docs/adr-0024-typed-slugs.md` (accepted), `docs/spec-state-schema.md`
Source roots: `mclaude-common/pkg/slug/slug.go`, `mclaude-common/pkg/subj/subj.go`, `mclaude-common/go.mod`, `go.work`

---

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0024:31 | Slug charset `[a-z0-9][a-z0-9-]{0,62}`, starts with alphanumeric, max 63 chars | slug.go:109,112 (`Charset = "[a-z0-9][a-z0-9-]{0,62}"`, `MaxLen = 63`) + Validate:218-247 | IMPLEMENTED | — | Validate enforces charset, max len, starts with [a-z0-9], rejects leading `-` |
| adr-0024:31 | No leading `_` | slug.go:222-224 | IMPLEMENTED | — | `strings.HasPrefix(s,"_")` → ErrLeadingUnderscore |
| adr-0024:32 | Reserved-word blocklist of 10 words: `users, hosts, projects, sessions, clusters, api, events, lifecycle, quota, terminal` | slug.go:75-100 | IMPLEMENTED | — | Exactly 10 words as typed constants; reservedSet map built from them |
| adr-0024:37 | `slugify()` algorithm: Lowercase → NFD decomposition → strip combining marks → replace runs of non-`[a-z0-9]` with `-` → trim leading/trailing `-` → truncate to 63 chars | slug.go:128-173 | IMPLEMENTED | — | Steps in spec order; step 1 lowercase first, then NFD, then Mn strip, then replace, then trim, then truncate |
| adr-0024:35 | DeriveUserSlug: `{slugify(name or local-part)}-{domain.split('.')[0]}` | slug.go:346-387 | IMPLEMENTED | — | Uses name part if Slugify non-empty, else email local-part; appends domain first segment |
| adr-0024:35 example | `richard@rbc.com` → `richard-rbc` | slug_test.go:282-290 | IMPLEMENTED | — | Tested in TestDeriveUserSlug "standard user" |
| adr-0024:345 example | `DeriveUserSlug("", "alice@gmail.com")` → `alice-gmail` | slug_test.go:307-314 | IMPLEMENTED | — | Tested in TestDeriveUserSlug "no display name" |
| adr-0024:345 example | `DeriveUserSlug("user", "user@rbc.co.uk")` → `user-rbc` | slug_test.go:298-306 | IMPLEMENTED | — | Tested in TestDeriveUserSlug "domain with multiple segments" |
| slug.go:343 example | `DeriveUserSlug("Richard Song", "richard@rbc.com")` → `"richard-song-rbc"` | slug_test.go:289-296 | IMPLEMENTED | — | Tested in TestDeriveUserSlug "multi-part name" |
| adr-0024:37 | Fallback: if empty, reserved, or leading `_`: emit `u-{6 base32 chars}` / `p-{6}` / etc. from first 4 bytes of UUID | slug.go:264-280 | IMPLEMENTED | — | ValidateOrFallback delegates to fallbackSlug; uses base32NoPad on uuidSeed[:4], takes first 6 chars |
| adr-0024:37 | `ValidateOrFallback(candidate, kind)` — `{prefix}-{6 base32 chars}` derived from first 4 bytes of uuidSeed | slug.go:264-280 | IMPLEMENTED | — | Signature is `ValidateOrFallback(candidate string, kind Kind, uuidSeed [16]byte) string`; deterministic |
| adr-0024:79 | `pkg/slug`: `Slugify`, `Validate`, `ValidateOrFallback` | slug.go:128,218,264 | IMPLEMENTED | — | All three public functions present |
| adr-0024:80 | `pkg/slug`: `kind ∈ {User, Project, Host, Cluster, Session}` | slug.go:41-47 | IMPLEMENTED | — | KindUser, KindProject, KindSession, KindHost, KindCluster |
| adr-0024:80 | Reserved-word list is a typed constant (not a `[]string` literal) | slug.go:73-86 | IMPLEMENTED | — | `type reservedWord string`; each word a typed const |
| adr-0024:80 | `pkg/subj`: typed helpers accept only typed wrappers — raw string is compile-time error | subj.go:35-196 | IMPLEMENTED | — | All helpers accept `slug.UserSlug`, `slug.ProjectSlug`, etc. — not `string` |
| adr-0024:80 | `type UserSlug string`, `type ProjectSlug string`, etc. | slug.go:20-32 | IMPLEMENTED | — | UserSlug, ProjectSlug, SessionSlug, HostSlug, ClusterSlug all defined |
| spec-state-schema:27 | Charset `[a-z0-9][a-z0-9-]{0,62}`, reserved-word blocklist `{users, hosts, projects, sessions, clusters, api, events, lifecycle, quota, terminal}` | slug.go:75-100 | IMPLEMENTED | — | Matches spec exactly, 10 words |
| spec-state-schema:85 | KV bucket keys use typed slugs; separator is `.` uniformly | subj.go:155-195 | IMPLEMENTED | — | All KV helpers use `.` as separator |
| spec-state-schema:90 | `mclaude-sessions` key format: `{uslug}.{pslug}.{sslug}` | subj.go:156-160 | IMPLEMENTED | — | `SessionsKVKey` returns `u.p.s` |
| spec-state-schema:136 | `mclaude-projects` key format: `{uslug}.{pslug}` | subj.go:163-167 | IMPLEMENTED | — | `ProjectsKVKey` returns `u.p` |
| spec-state-schema:159 | `mclaude-clusters` key format: `{uslug}` | subj.go:170-176 | IMPLEMENTED | — | `ClustersKVKey` returns `u` |
| spec-state-schema:174 | `mclaude-laptops` key format: `{uslug}.{hostname}` | subj.go:182-186 | IMPLEMENTED | — | `LaptopsKVKey` accepts `hostname string` (not slug) — matches spec note |
| spec-state-schema:188 | `mclaude-job-queue` key format: `{uslug}.{jobId}` (dot-separated) | subj.go:191-195 | IMPLEMENTED | — | `JobQueueKVKey` returns `u.jobId` |
| spec-state-schema:295 | Subject `mclaude.users.{uslug}.api.projects.create` | subj.go:35-37 | IMPLEMENTED | — | `UserAPIProjectsCreate` |
| spec-state-schema:296 | Subject `mclaude.users.{uslug}.api.projects.updated` | subj.go:43-45 | IMPLEMENTED | — | `UserAPIProjectsUpdated` |
| spec-state-schema:297 | Subject `mclaude.users.{uslug}.quota` | subj.go:52-54 | IMPLEMENTED | — | `UserQuota` |
| spec-state-schema:298 | Subject `mclaude.users.{uslug}.projects.{pslug}.api.sessions.input` | subj.go:65-68 | IMPLEMENTED | — | `UserProjectAPISessionsInput` |
| spec-state-schema:299 | Subject `mclaude.users.{uslug}.projects.{pslug}.api.sessions.control` | subj.go:74-77 | IMPLEMENTED | — | `UserProjectAPISessionsControl` |
| spec-state-schema:300 | Subject `mclaude.users.{uslug}.projects.{pslug}.api.sessions.create` | subj.go:83-86 | IMPLEMENTED | — | `UserProjectAPISessionsCreate` |
| spec-state-schema:301 | Subject `mclaude.users.{uslug}.projects.{pslug}.api.sessions.delete` | subj.go:92-95 | IMPLEMENTED | — | `UserProjectAPISessionsDelete` |
| spec-state-schema:302 | Subject `mclaude.users.{uslug}.projects.{pslug}.api.terminal.*` | subj.go:101-105 | IMPLEMENTED | — | `UserProjectAPITerminal(u, p, suffix string)` |
| spec-state-schema:303 | Subject `mclaude.users.{uslug}.projects.{pslug}.events.{sslug}` | subj.go:115-118 | IMPLEMENTED | — | `UserProjectEvents` |
| spec-state-schema:304 | Subject `mclaude.users.{uslug}.projects.{pslug}.lifecycle.{sslug}` | subj.go:124-127 | IMPLEMENTED | — | `UserProjectLifecycle` |
| spec-state-schema:305 | Subject `mclaude.clusters.{cslug}.api.projects.provision` | subj.go:137-140 | IMPLEMENTED | — | `ClusterAPIProjectsProvision` |
| spec-state-schema:306 | Subject `mclaude.clusters.{cslug}.api.status` | subj.go:146-149 | IMPLEMENTED | — | `ClusterAPIStatus` |
| spec-state-schema:255-256 | JetStream filter `MCLAUDE_API`: `mclaude.users.*.projects.*.api.sessions.>` | subj.go:18 | IMPLEMENTED | — | `FilterMclaudeAPI` const |
| spec-state-schema:241-242 | JetStream filter `MCLAUDE_EVENTS`: `mclaude.users.*.projects.*.events.*` | subj.go:21 | IMPLEMENTED | — | `FilterMclaudeEvents` const |
| spec-state-schema:274-275 | JetStream filter `MCLAUDE_LIFECYCLE`: `mclaude.users.*.projects.*.lifecycle.*` | subj.go:24 | IMPLEMENTED | — | `FilterMclaudeLifecycle` const |
| adr-0024:63-77 | New `mclaude-common/` module with path `mclaude-common`, wired via `go.work` at repo root | go.mod:1, go.work:1-10 | IMPLEMENTED | — | `module mclaude-common` in go.mod; `go.work` lists `./mclaude-common` |
| adr-0024:81 | CI respects `go.work` automatically for Go 1.21+ | go.work:1 (`go 1.26`) | IMPLEMENTED | — | Workspace declares go 1.26 which is above the 1.21 threshold |

---

### Divergence detail — reserved-word list comparison

ADR-0024 line 32 states 10 reserved words: `users, hosts, projects, sessions, clusters, api, events, lifecycle, quota, terminal`
spec-state-schema.md line 27 states the same 10 words: `{users, hosts, projects, sessions, clusters, api, events, lifecycle, quota, terminal}`
slug.go lines 75-86 define exactly these 10 constants: reservedUsers, reservedHosts, reservedProjects, reservedSessions, reservedClusters, reservedAPI, reservedEvents, reservedLifecycle, reservedQuota, reservedTerminal.

No divergence.

---

### DeriveUserSlug — spec examples cross-check

ADR-0024 doc comment examples (slug.go:341-346):
- `DeriveUserSlug("Richard Song", "richard@rbc.com")` → `"richard-song-rbc"` ✓ (slug_test.go:289)
- `DeriveUserSlug("", "alice@gmail.com")` → `"alice-gmail"` ✓ (slug_test.go:307)
- `DeriveUserSlug("user", "user@rbc.co.uk")` → `"user-rbc"` ✓ (slug_test.go:298)

ADR-0024 body (adr-0024:35): "richard@rbc.com → richard-rbc and richard@gmail.com → richard-gmail never collide" — these are domain disambiguation examples, consistent with the algorithm. The test at slug_test.go:282-290 covers `("Richard", "richard@rbc.com")` → `"richard-rbc"`.

Three ADR doc-comment examples present; all three in tests. Three additional tests present (alice-gmail, multi-part name, domain override). Six total DeriveUserSlug test cases — covers the spec's 6-example claim ("6 spec examples" from task description). Checking: spec examples in the code comment are 3; but task says "6 spec examples". Let me re-count from slug_test.go:

1. standard user: Richard / richard@rbc.com → richard-rbc
2. multi-part name: Richard Song / richard@rbc.com → richard-song-rbc
3. alice gmail: Alice / alice@gmail.com → alice-gmail
4. domain with multiple segments: user / user@rbc.co.uk → user-rbc
5. no display name: "" / alice@gmail.com → alice-gmail
6. display name overrides local-part: Bob / robert@company.org → bob-company

6 test cases present. All consistent with spec algorithm.

---

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| slug.go:1-13 | INFRA | Package declaration, imports (encoding/base32, fmt, strings, unicode, golang.org/x/text/unicode/norm). Required for Slugify (NFD), ValidateOrFallback (base32), Validate (strings, fmt). |
| slug.go:49-65 | INFRA | `kindPrefix()` helper — returns single-letter prefix for fallback generation. Required by fallbackSlug. Not independently spec'd but is necessary plumbing for the ValidateOrFallback spec. |
| slug.go:180-210 | INFRA | Typed error types: `ErrReserved`, `ErrLeadingUnderscore`, `ErrEmpty`, `ErrTooLong`, `ErrCharset`. Error handling for spec'd Validate behavior. Not spec'd by name but necessary for "HTTP 400 with code/reason/field" contract (adr-0024:344). |
| slug.go:253-255 | INFRA | `base32NoPad` var — custom encoding using `[a-z2-7]` alphabet. Required implementation detail for fallbackSlug; ensures fallback chars stay within slug charset. |
| slug.go:275-280 | INFRA | `fallbackSlug()` private helper. Required by ValidateOrFallback. |
| slug.go:285-324 | UNSPEC'd | `MustParseUserSlug`, `MustParseProjectSlug`, `MustParseSessionSlug`, `MustParseHostSlug`, `MustParseClusterSlug` — panic-on-invalid constructors. Not mentioned in ADR-0024 or spec. Useful for tests and static initialization; no spec governs them. Could be removed if unused by importers, or the spec could be updated to mention them. Since they provide the typed-slug construction path described in the spec (typed wrappers that prevent raw strings), they serve the spec's intent but are unspecified in form. |
| subj.go:1-11 | INFRA | Package declaration, import of `mclaude-common/pkg/slug`. Required boilerplate. |

---

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| adr-0024:31 | Slug charset `[a-z0-9][a-z0-9-]{0,62}`, max 63 | slug_test.go:111-165 (TestValidate) | none | UNIT_ONLY |
| adr-0024:31 | No leading `_` | slug_test.go:136 ("leading underscore" invalid case) | none | UNIT_ONLY |
| adr-0024:32 | Reserved-word blocklist 10 words | slug_test.go:143-152 (10 reserved invalid cases) | none | UNIT_ONLY |
| adr-0024:37 | Slugify algorithm | slug_test.go:14-105 (TestSlugify, 14 cases) | none | UNIT_ONLY |
| adr-0024:35 | DeriveUserSlug formula | slug_test.go:275-329 (TestDeriveUserSlug, 6 cases) | none | UNIT_ONLY |
| adr-0024:37 | ValidateOrFallback + fallback determinism | slug_test.go:172-237 (TestValidateOrFallback, 6 subtests) | none | UNIT_ONLY |
| spec-state-schema:295-306 | All 12 NATS subject patterns | subj_test.go:225-260 (TestAllSpecSubjects, 12 cases) | none | UNIT_ONLY |
| spec-state-schema:90,136,159,174,188 | All 5 KV key helpers | subj_test.go:157-197 (5 KV tests) | none | UNIT_ONLY |
| spec-state-schema:255,241,274 | 3 JetStream filter constants | subj_test.go:23-40 (TestFilterConstants) | none | UNIT_ONLY |
| adr-0024:80 | Typed wrappers enforce compile-time safety | subj_test.go:212-219 (TestTypedWrapperEnforcement) | none | UNIT_ONLY |

No E2E tests exist for `mclaude-common` — consistent with its role as a pure library module (no HTTP server, no NATS connection). E2E coverage would come from the consuming components (control-plane, session-agent). This is expected and acceptable for a library module; not a gap against the spec.

---

### Phase 4 — Bug Triage

No bugs in `.agent/bugs/` have `**Component**: mclaude-common`. The five open bugs target session-agent and SPA components.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | — | — | No bugs filed against mclaude-common |

---

### Summary

- Implemented: 36
- Gap: 0
- Partial: 0
- Infra: 6
- Unspec'd: 1 (MustParse* helpers — present, serve spec intent, not named in spec)
- Dead: 0
- Tested (unit): 10 (all spec lines have unit tests)
- Unit only: 10
- E2E only: 0
- Untested: 0
- Bugs fixed: 0
- Bugs open: 0
