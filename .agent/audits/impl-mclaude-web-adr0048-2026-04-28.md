# Implementation Audit: mclaude-web vs ADR-0048

**Component**: mclaude-web  
**ADR**: docs/adr-0048-fix-session-store-slug-regression.md  
**Date**: 2026-04-28  
**Auditor**: implementation-evaluator subagent

## Run: 2026-04-28T00:00:00Z

---

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0048 §Decisions row 1 | "`SessionStore` uses `userId` (UUID) for KV watches — Pass `authState.userId` (UUID) as the `userSlug` argument to `SessionStore`. `HeartbeatMonitor` continues to receive `authState.userSlug` (slug)." | App.tsx:~227 `new SessionStore(natsClient, authState.userId, authState.userId)` | IMPLEMENTED | — | Both the second and third ctor args are `authState.userId` (UUID). The comment `uslug = authState.userSlug ?? authState.userId` is computed but not passed to SessionStore. |
| adr-0048 §Decisions row 2 | "`mclaude-hosts` KV watch keeps using `userSlug` — `HeartbeatMonitor` keeps receiving `userSlug` (`"dev"`)." | App.tsx:~228 `new HeartbeatMonitor(natsClient, authState.userId, uslug)` where `uslug = authState.userSlug ?? authState.userId` | IMPLEMENTED | — | HeartbeatMonitor receives slug (not UUID) for hosts KV prefix. |
| adr-0048 §Impact | "Components implementing the change: `mclaude-web`: `App.tsx` — pass `authState.userId` (not `authState.userSlug`) as `userSlug` to `SessionStore` constructor." | App.tsx:~227 | IMPLEMENTED | — | Matches exactly. |

---

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|----------------|-------------|
| App.tsx: `const uslug = authState.userSlug ?? authState.userId` (inside bootstrap effect) | INFRA | Fallback computation used only by HeartbeatMonitor; not passed to SessionStore. Necessary plumbing for the HeartbeatMonitor slug requirement. |
| session-store.ts: entire file | INFRA | SessionStore class unchanged by ADR-0048; receives `userSlug` param which is now always UUID from App.tsx per the fix. No unspec'd behavior. |
| heartbeat-monitor.ts: entire file | INFRA | HeartbeatMonitor unchanged by ADR-0048; continues to use whatever `userSlug` is passed in. |

---

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| adr-0048 §Decisions row 1 | SessionStore uses userId (UUID) for KV watches | `session-store.test.ts` — "ADR-0048 regression" describe block: 4 tests verifying UUID-prefix construction | No integration test | UNIT_ONLY | Critical data-flow behavior. Integration test bar requires real KV with real NATS, but absence of integration test is acceptable for SPA components that can't easily run against a real cluster in CI. |
| adr-0048 §Decisions row 2 | HeartbeatMonitor uses userSlug for mclaude-hosts | `heartbeat-monitor.test.ts` — "ADR-0048 regression" describe block: 3 tests verifying slug-prefix vs UUID-prefix behavior | No integration test | UNIT_ONLY | Same rationale as above — SPA unit tests cover the fix adequately. |

**ADR Integration Test Cases section**: ADR-0048 has no `## Integration Test Cases` section. No additional test gaps to flag.

---

### Phase 4 — Bug Triage

No `.agent/bugs/` files found for `mclaude-web` component.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| — | — | — | No open bugs for this component |

---

### Summary

- Implemented: 3
- Gap: 0
- Partial: 0
- Infra: 3
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 2
- E2E only: 0
- Untested: 0
- Bugs fixed: 0
- Bugs open: 0
