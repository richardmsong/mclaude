# Full Implementation Audit: mclaude-common

**Date:** 2026-04-29
**Component:** `mclaude-common/`
**Specs evaluated:** `docs/mclaude-common/spec-common.md`, `docs/spec-state-schema.md`
**Source files reviewed:** All 6 `.go` files in `mclaude-common/`

---

## Summary

**Total gaps found: 7**
- MISSING: 4
- MISMATCH: 3
- PARTIAL: 0

---

## Gap 1: MISSING — `UserHostStatus` subject helper

**Spec says (spec-common.md):**
> **Host-scoped subject helper:**
> `UserHostStatus(u UserSlug, h HostSlug) string` -- host presence heartbeat.

**Code does:** No `UserHostStatus` function exists anywhere in `mclaude-common/pkg/subj/subj.go`. Grepping the entire repo for `UserHostStatus` returns zero results.

**Expected pattern:** Based on spec-state-schema.md, this would likely be something like `mclaude.users.{uslug}.hosts.{hslug}.status` — though the exact subject is not explicitly listed in spec-state-schema.md's NATS subjects table (see Gap 7). The spec-common.md explicitly requires this helper.

**Severity:** MISSING

---

## Gap 2: MISSING — `UserHostProjectAPISessionsRestart` subject helper

**Spec says (spec-state-schema.md NATS subjects table):**
> `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.restart` — Publisher: SPA. Subscriber: session-agent (JetStream, cmd consumer)

**Spec says (spec-common.md):**
> **User+host+project-scoped subject helpers (ADR-0035):**
> `UserHostProjectAPISessionsInput`, `UserHostProjectAPISessionsControl`, `UserHostProjectAPISessionsCreate`, `UserHostProjectAPISessionsDelete`, `UserHostProjectAPITerminal`, `UserHostProjectEvents`, `UserHostProjectLifecycle`

**Code does:** There is no `UserHostProjectAPISessionsRestart` helper in `subj.go`. The spec-common.md subject helper list omits it too, but the spec-state-schema.md NATS subjects table clearly defines this subject. A shared helper is needed for type-safe construction of this subject.

**Severity:** MISSING

---

## Gap 3: MISSING — Host-scoped project API subject helpers (provision, create, update, delete)

**Spec says (spec-state-schema.md NATS subjects table):**
> `mclaude.users.{uslug}.hosts.{hslug}.api.projects.provision` — control-plane → controller
> `mclaude.users.{uslug}.hosts.{hslug}.api.projects.create` — SPA, control-plane → controller
> `mclaude.users.{uslug}.hosts.{hslug}.api.projects.update` — control-plane → controller
> `mclaude.users.{uslug}.hosts.{hslug}.api.projects.delete` — control-plane → controller

**Code does:** The `subj` package has `UserAPIProjectsCreate(u UserSlug)` (user-scoped, no host), but no host-scoped project subject helpers (`UserHostAPIProjectsProvision`, `UserHostAPIProjectsCreate`, `UserHostAPIProjectsUpdate`, `UserHostAPIProjectsDelete`). These host-scoped subjects are a different family from the user-scoped `UserAPIProjectsCreate` — they route to controllers, not the control-plane.

Note: spec-common.md does not explicitly list these helpers either (it only lists `UserAPIProjectsCreate` and `UserAPIProjectsUpdated` as user-scoped helpers). These subjects are defined in spec-state-schema.md. Whether they should be in mclaude-common vs. inlined by consumers is an architectural choice, but the spec-state-schema.md clearly defines them as typed-slug subjects.

**Severity:** MISSING (lower priority — spec-common.md omits them, but spec-state-schema.md defines them)

---

## Gap 4: MISMATCH — ADR-0004 references should be ADR-0035

**Spec says (spec-common.md):**
> ADR-0035 (Unified Host Architecture) extends ADR-0024 by inserting `.hosts.{hslug}.` between the user and project levels

**Code does:** All comments in `subj.go` and `subj_test.go` reference "ADR-0004" as the source of the `.hosts.{hslug}.` insertion. For example:
> `// ADR-0004 inserts .hosts.{hslug}. between the user and project levels`
> `// ADR-0004: .hosts.*. inserted between user and project.`

Similarly, `slug.go` line 28: `// HostSlug is a validated, URL-safe slug for a host (BYOH, ADR-0004).`

The spec says this is ADR-0035 (Unified Host Architecture), not ADR-0004. The comments throughout are stale references to the pre-unification ADR.

**Severity:** MISMATCH (documentation/comments only, not behavioral)

---

## Gap 5: MISMATCH — `ClustersKVKey` is dead code per spec

**Spec says (spec-common.md):**
> Note: `ClustersKVKey` exists in code as dead code but the `mclaude-clusters` KV bucket was removed per ADR-0035. `LaptopsKVKey` (pre-ADR-0035) is also removed. Use `HostsKVKey` with a typed `HostSlug`.

**Spec says (spec-state-schema.md):**
> The previous `mclaude-clusters` KV is removed. [...] **Dead code:** `subj.ClustersKVKey()` in `mclaude-common/pkg/subj/subj.go` [...] still reference this removed bucket.

**Code does:** `ClustersKVKey` is still present and exported in `subj.go`:
```go
func ClustersKVKey(u slug.UserSlug) string {
    return string(u)
}
```
It is tested in `subj_test.go` as if it were a live, valid helper. The spec explicitly flags it as dead code referencing a removed bucket. The function should either be removed or annotated as deprecated.

**Severity:** MISMATCH (dead code the spec says should be removed)

---

## Gap 6: MISMATCH — `nats` package not mentioned in spec-common.md

**Spec says (spec-common.md):**
> `mclaude-common` is a zero-dependency Go library (module `mclaude.io/common`)

And under Dependencies:
> - `golang.org/x/text` -- Unicode normalization
> - Go standard library only (`encoding/base32`, `fmt`, `strings`, `unicode`).

**Code does:** The `mclaude-common` module includes `pkg/nats/` with two files:
- `creds.go` — `FormatNATSCredentials` helper
- `operator_keys.go` — `GenerateOperatorAccount` with `OperatorAccount` struct (depends on `github.com/nats-io/jwt/v2` and `github.com/nats-io/nkeys`)

The `go.mod` declares dependencies on `github.com/nats-io/jwt/v2` and `github.com/nats-io/nkeys`, contradicting the spec's "zero-dependency" claim (beyond `golang.org/x/text`).

The spec does not mention `pkg/nats`, `FormatNATSCredentials`, `GenerateOperatorAccount`, or the `OperatorAccount` type. The comment in `creds.go` says: "Moved from mclaude-control-plane per ADR-0035 so the CLI can reuse FormatNATSCredentials for BYOH bootstrap." — but the spec was never updated to reflect this.

**Severity:** MISMATCH (spec is stale — code has additional package and dependencies not documented)

---

## Gap 7: MISSING — Shared types (events, session state, lifecycle payloads)

**Spec says (spec-common.md):**
> provides typed slug identifiers and NATS subject/KV key construction helpers shared by all mclaude components

**Spec says (spec-state-schema.md):**
Defines numerous shared types including `SessionState`, `ProjectState`, `HostState`, `JobEntry`, lifecycle event payloads (`session_created`, `session_stopped`, etc.), and `QuotaStatus`. These are JSON-serialized types used across multiple components (session-agent, SPA, daemon, control-plane).

**Code does:** The `mclaude-common` library does NOT contain any shared Go struct types for:
- `SessionState` (KV value type)
- `ProjectState` (KV value type)
- Host KV value type
- `JobEntry` (KV value type)
- Lifecycle event payload types
- `QuotaStatus` type
- Event type constants (e.g., `"session_created"`, `"session_stopped"`, etc.)

Note: spec-common.md specifically scopes mclaude-common to "typed slug identifiers and NATS subject/KV key construction helpers" and does not explicitly require shared payload types. However, spec-state-schema.md defines these shared types that are used across components. They are currently likely duplicated in each component rather than centralized. This is a potential improvement but not directly a spec-common.md violation.

**Severity:** MISSING (low priority — not explicitly required by spec-common.md, but a natural fit for a shared library)

---

## Verified Correct (No Gaps)

### Package `slug` — All spec requirements met:

| Spec Requirement | Status |
|---|---|
| 5 typed wrappers (`UserSlug`, `ProjectSlug`, `SessionSlug`, `HostSlug`, `ClusterSlug`) | ✅ Implemented as `type T string` |
| 5 Kind constants (`KindUser`, `KindProject`, `KindSession`, `KindHost`, `KindCluster`) | ✅ |
| Fallback prefixes (`u-`, `p-`, `s-`, `h-`, `c-`) | ✅ via `kindPrefix()` |
| `Slugify` algorithm (6 steps: lowercase → NFD → strip Mn → replace runs → trim → truncate to 63) | ✅ Exact match |
| `Validate` — charset `[a-z0-9][a-z0-9-]{0,62}`, max 63, no leading `_`, reserved-word blocklist | ✅ |
| Reserved-word blocklist: `users`, `hosts`, `projects`, `sessions`, `clusters`, `api`, `events`, `lifecycle`, `quota`, `terminal` | ✅ All 10 present |
| Typed errors: `ErrEmpty`, `ErrCharset`, `ErrTooLong`, `ErrLeadingUnderscore`, `ErrReserved` | ✅ |
| `ValidateOrFallback` — `{prefix}-{6 base32 chars}` from first 4 bytes of UUID seed, base32 alphabet `a-z2-7` no padding | ✅ |
| `DeriveUserSlug` — `{slugify(displayName)}-{first domain segment}`, fallback to email local-part | ✅ |
| `MustParseUserSlug`, `MustParseProjectSlug`, `MustParseSessionSlug`, `MustParseHostSlug`, `MustParseClusterSlug` | ✅ All 5 present |
| `golang.org/x/text` dependency for Unicode normalization | ✅ |

### Package `subj` — Subject helpers verified correct:

| Spec Requirement | Status |
|---|---|
| `FilterMclaudeAPI` = `mclaude.users.*.hosts.*.projects.*.api.sessions.>` | ✅ |
| `FilterMclaudeEvents` = `mclaude.users.*.hosts.*.projects.*.events.*` | ✅ |
| `FilterMclaudeLifecycle` = `mclaude.users.*.hosts.*.projects.*.lifecycle.*` | ✅ |
| `UserAPIProjectsCreate(u)` | ✅ |
| `UserAPIProjectsUpdated(u)` | ✅ |
| `UserQuota(u)` | ✅ |
| `UserHostProjectAPISessionsInput(u, h, p)` | ✅ |
| `UserHostProjectAPISessionsControl(u, h, p)` | ✅ |
| `UserHostProjectAPISessionsCreate(u, h, p)` | ✅ |
| `UserHostProjectAPISessionsDelete(u, h, p)` | ✅ |
| `UserHostProjectAPITerminal(u, h, p, suffix)` | ✅ |
| `UserHostProjectEvents(u, h, p, s)` | ✅ |
| `UserHostProjectLifecycle(u, h, p, s)` | ✅ |
| `SessionsKVKey(u, h, p, s)` = `{uslug}.{hslug}.{pslug}.{sslug}` | ✅ |
| `ProjectsKVKey(u, h, p)` = `{uslug}.{hslug}.{pslug}` | ✅ |
| `HostsKVKey(u, h)` = `{uslug}.{hslug}` | ✅ |
| `JobQueueKVKey(u, jobId)` = `{uslug}.{jobId}` | ✅ |
| Type-safe: all helpers accept only typed slug wrappers, not raw strings | ✅ |

### Tests: All pass (slug, subj packages)
