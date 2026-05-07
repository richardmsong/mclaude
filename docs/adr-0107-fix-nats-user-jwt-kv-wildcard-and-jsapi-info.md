# ADR: Fix NATS User JWT — KV Wildcard and $JS.API.INFO

**Status**: accepted
**Status history**:
- 2026-05-07: accepted

## Overview

Correct two publish-permission bugs in `UserSubjectPermissions` (the function that builds NATS user JWTs for SPA/CLI clients) that prevent the `mclaude-user-info-{uslug}` KV bucket from working in the browser. The first bug uses the NATS single-token wildcard `*` where the multi-token wildcard `>` is required. The second bug omits `$JS.API.INFO`, which NATS.js publishes on every startup to discover JetStream. Together these cause a "Publish Violation" on every KV write attempt and prevent JetStream from initialising in the browser.

## Motivation

NATS server logs report two violations for every authenticated browser session:

```
Publish Violation - Subject "$KV.mclaude-user-info-richard-song-rbc-com.tokens.anthropic"
Publish Violation - Subject "$JS.API.INFO"
```

The first violation occurs because `$KV.mclaude-user-info-{uslug}.*` only matches subjects with exactly one token after the bucket prefix. The KV key `tokens.anthropic` maps to the NATS subject `$KV.mclaude-user-info-{uslug}.tokens.anthropic`, which has **two** tokens after the prefix — `*` cannot match it. The wildcard must be `>` (one or more tokens, including dots).

The second violation occurs because NATS.js publishes to `$JS.API.INFO` during its JetStream bootstrap phase (before issuing any consumer-create or direct-get requests). This is a fixed subject, not per-bucket — every NATS.js client needs publish permission on it. Without it, the JetStream init handshake fails silently and all subsequent KV watch and consumer creation calls time out, so tokens can never be read or written from the browser.

ADR-0091 introduced the `mclaude-user-info-{uslug}` KV bucket and the associated permission table. The KV layout section of that ADR correctly showed the example subject `$KV.mclaude-user-info-alice.tokens.anthropic`, but the permission table used `.*`, which is inconsistent with dotted keys. `$JS.API.INFO` was not enumerated because the spec was written against the NATS Go client (which does not publish `$JS.API.INFO`); it was omitted for browser clients.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| KV pub wildcard | `$KV.mclaude-user-info-{uslug}.>` | `>` matches one or more subject tokens; required for any key containing a dot (e.g. `tokens.anthropic`) |
| KV sub wildcard | `$KV.mclaude-user-info-{uslug}.>` | Same reason — KV push delivery subjects carry the full key as subject tokens |
| `$JS.API.INFO` | Add to pub allow | NATS.js publishes this once per connection on JetStream init; without it the browser client cannot use JetStream at all |
| Scope | `auth/nkeys.go` `UserSubjectPermissions` only | Bugs are isolated to the user JWT builder; no other JWT type or config is affected |

## User Flow

No user-visible change in the happy path. The fix unblocks what was always the intended behavior: SPA writes `tokens.anthropic` to `mclaude-user-info-{uslug}` on token entry, reads it back on page load, and delivers it to hosts. Before this fix those writes were silently denied.

## Component Changes

### mclaude-control-plane

**File modified:**
- `auth/nkeys.go` — `UserSubjectPermissions`:
  1. Change `"$KV." + infoBucket + ".*"` (pub allow, KV put) to `"$KV." + infoBucket + ".>"`.
  2. Change `"$KV." + infoBucket + ".*"` (sub allow, KV watch push delivery) to `"$KV." + infoBucket + ".>"`.
  3. Add `"$JS.API.INFO"` to pub allow.

## Data Model

No schema changes.

## Error Handling

No change. After the fix, `ErrImagePull` for KV puts is eliminated. Any future NATS permission gap will still surface as a "Publish Violation" in NATS server logs.

## Security

The `>` wildcard grants publish access to all keys in the user's own `mclaude-user-info-{uslug}` bucket — identical in effective scope to the original intent of the `*` wildcard (which incorrectly excluded dotted keys). No additional subjects are granted. `$JS.API.INFO` is a read-only discovery endpoint; it returns JetStream metadata and does not mutate state. NATS RBAC ensures only the owning user's JWT can access their bucket.

## Impact

**Specs updated in this commit:**
- `docs/spec-nats-data-taxonomy.md` — correct `$KV.mclaude-user-info-{uslug}.*` to `.>` in the permission block (pub + sub) and the summary table; add `$JS.API.INFO` to the pub allow block.

**Components implementing the change:**
- `mclaude-control-plane` (auth/nkeys.go)

## Scope

**In scope:**
- Fix `UserSubjectPermissions` wildcard and add `$JS.API.INFO`
- Update `docs/spec-nats-data-taxonomy.md`

**Explicitly deferred / out of scope:**
- Auditing other KV buckets (`mclaude-sessions-{uslug}`, `mclaude-projects-{uslug}`) for the same wildcard issue — those buckets use dotted keys too (e.g. `hosts.laptop-a.projects.foo.sessions.bar`). A separate ADR will address those if violations are observed. The session/project KV paths use different subject patterns and their existing `>` entries already cover the deep paths — only the user-info bucket uses the bare `.*`.

## Integration Test Cases

No integration tests — change is a NATS JWT permission correction. The fix is validated by successful `helm upgrade`, user login, and observing that `tokens.anthropic` persists across page reload (no "Publish Violation" in NATS server logs).

## Implementation Plan

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| mclaude-control-plane | 3 lines changed | ~15k | Wildcard and missing subject in auth/nkeys.go |

**Total estimated tokens:** ~15k
**Estimated wall-clock:** 0.05h
