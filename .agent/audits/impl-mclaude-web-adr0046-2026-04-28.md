# Implementation Audit: mclaude-web (ADR-0046)

**Component**: mclaude-web
**ADR**: docs/adr-0046-fix-host-liveness-kv-writes.md (status: accepted)
**Specs**: docs/spec-state-schema.md, docs/mclaude-control-plane/spec-control-plane.md
**Focus files**: heartbeat-monitor.ts, session-list-vm.ts, App.tsx
**Date**: 2026-04-28

## Run: 2026-04-28T00:00:00Z

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| ADR-0046 Decisions: HeartbeatMonitor format fix | "Change KV entry parse from `{ts: string}` to `{online: boolean}`" | heartbeat-monitor.ts:41 `const { online } = JSON.parse(...) as { online: boolean }` | IMPLEMENTED | — | Entry parsed as `{online: boolean}` |
| ADR-0046 Decisions: HeartbeatMonitor format fix | "Set health map key to the entry key's second segment (the host slug)" | heartbeat-monitor.ts:39 `const hostSlug = entry.key.split('.')[1]` | IMPLEMENTED | — | Second segment extracted correctly |
| ADR-0046 Decisions: HeartbeatMonitor format fix | "`isHealthy(hostSlug)` returns the `online` boolean directly — no time-delta check" | heartbeat-monitor.ts:52 `return this._health.get(hostSlug)?.online ?? false` | IMPLEMENTED | — | Returns boolean, no time-delta logic anywhere |
| ADR-0046 Decisions: HeartbeatMonitor format fix | KV watch on `mclaude-hosts` bucket for this user's hosts | heartbeat-monitor.ts:35-36 `this.natsClient.kvWatch('mclaude-hosts', kvKeyHostsForUser(this.userSlug as UserSlug), ...)` | IMPLEMENTED | — | Watches `mclaude-hosts` bucket with `{uslug}.*` pattern |
| ADR-0046 Decisions: SessionListVM key fix | "Change `heartbeatMonitor.isHealthy(p.id)` to `heartbeatMonitor.isHealthy(p.hostSlug ?? 'local')`" | session-list-vm.ts:78 `healthy: this.heartbeatMonitor.isHealthy(p.hostSlug ?? 'local')` | IMPLEMENTED | — | Passes host slug, falls back to 'local' |
| spec-state-schema.md: mclaude-hosts KV format | Key format: `{uslug}.{hslug}` | heartbeat-monitor.ts:39 `entry.key.split('.')[1]` — expects `{uslug}.{hslug}` format | IMPLEMENTED | — | Correctly extracts hslug as second segment |
| spec-state-schema.md: mclaude-hosts KV value | Value `{online: true/false}` read by SPA | heartbeat-monitor.ts:41 `const { online } = JSON.parse(...)` | IMPLEMENTED | — | Reads `online` boolean field |
| spec-state-schema.md: mclaude-hosts Readers | "SPA (KV watch for host list + status)" | heartbeat-monitor.ts:35-36, App.tsx (HeartbeatMonitor.start() called on login) | IMPLEMENTED | — | SPA watches hosts KV on login |
| ADR-0046 Scope: "HeartbeatMonitor reads `online: bool`, keys by host slug" | HeartbeatMonitor reads `online: bool`, keys by host slug | heartbeat-monitor.ts:39-44 | IMPLEMENTED | — | |
| ADR-0046 Scope: "SessionListVM calls `isHealthy(p.hostSlug ?? 'local')`" | SessionListVM calls `isHealthy(p.hostSlug ?? 'local')` | session-list-vm.ts:78 | IMPLEMENTED | — | |
| ADR-0046: SPA userSlug for KV key construction | HeartbeatMonitor should use user slug (not UUID) as KV key prefix | heartbeat-monitor.ts:29 constructor param `userSlug: string = userId`; App.tsx bootstrap: `const uslug = authState.userSlug ?? authState.userId; const hb = new HeartbeatMonitor(natsClient, authState.userId, uslug)` | IMPLEMENTED | — | Falls back to userId if userSlug not present (graceful degradation) |
| ADR-0046: SPA userSlug for SessionListVM | SessionListVM should use user slug for subject construction | session-list-vm.ts:57 `userSlug: string = userId`; App.tsx: `new SessionListVM(sessionStore, heartbeatMonitor, natsClient, authState.userId, undefined, authState.userSlug ?? authState.userId)` | IMPLEMENTED | — | |
| ADR-0046: AuthStore reads userSlug from login response | Login response gains `userSlug`; SPA uses it | auth-store.ts:69 `const userSlug = tokens.userSlug ?? this._parseSlugFromJwt(tokens.jwt) ?? tokens.userId` | IMPLEMENTED | — | Reads from `tokens.userSlug`, falls back to JWT slug claim, then userId |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| heartbeat-monitor.ts:1-4 (imports) | INFRA | Standard TypeScript imports |
| heartbeat-monitor.ts:6-9 (HeartbeatHealth interface, HealthListener type) | INFRA | Type definitions supporting the specified behavior |
| heartbeat-monitor.ts:11-19 (class declaration, fields, constructor) | INFRA | Class scaffolding; `_listeners` and `_unwatcher` fields are necessary plumbing for the KV watch and notification system specified in ADR-0046 |
| heartbeat-monitor.ts:21-50 (start method) | INFRA | Implementation of specified `start()` behavior — KV watch setup, online parse, listener notification on change |
| heartbeat-monitor.ts:52-54 (stop method) | INFRA | Specified cleanup behavior |
| heartbeat-monitor.ts:56-58 (isHealthy method) | INFRA | Specified lookup method |
| heartbeat-monitor.ts:60-64 (onHealthChanged method) | INFRA | Listener registration — needed for SessionListVM to react to host status changes; implicitly required by the reactivity described in ADR-0046 |
| session-list-vm.ts:1-10 (imports) | INFRA | Standard TypeScript imports |
| session-list-vm.ts:12-24 (IStorage interface) | INFRA | Test-injectable localStorage abstraction (existing pattern) |
| session-list-vm.ts:26-50 (SessionVM, ProjectVM, ProjectGroup, SessionListListener types) | INFRA | Type definitions for the specified view model shape |
| session-list-vm.ts:52-75 (constructor, fields) | INFRA | Standard VM construction pattern |
| session-list-vm.ts:77-104 (projects getter) | INFRA | The projects getter is the primary implementation of the ADR-0046 fix (hostSlug-based health check) plus broader SessionListVM functionality |
| session-list-vm.ts:106-148 (filter state methods) | INFRA | Existing filter feature, not changed by ADR-0046; necessary for dashboard filter behavior |
| session-list-vm.ts:150-200 (sortedGroups getter) | INFRA | Sorting/filtering logic; not changed by ADR-0046 |
| session-list-vm.ts:202-255 (createProject, createSession, deleteSession, restartSession) | INFRA | Existing NATS publish/subscribe methods; not changed by ADR-0046 |
| session-list-vm.ts:257-275 (onProjectsChanged, destroy, _notify) | INFRA | Standard listener management plumbing |
| App.tsx:1-30 (imports, getRoute, navigate) | INFRA | Standard React app scaffolding; routing helpers |
| App.tsx:32-75 (UpdateBanner, Toast, readAndClearRedirectParams) | INFRA | Existing UI components and utility functions |
| App.tsx:77-end (App component) | INFRA | Main app component; HeartbeatMonitor instantiation with userSlug is the ADR-0046-relevant section; rest is existing app structure |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| ADR-0046: HeartbeatMonitor parses `{online: boolean}` | Parse KV entry as `{online: boolean}` | heartbeat-monitor.test.ts:21-26 "returns true after online:true KV entry arrives" | None | UNIT_ONLY | No integration test against real NATS KV bucket |
| ADR-0046: HeartbeatMonitor keys by host slug | Health map keyed by host slug (second segment) | heartbeat-monitor.test.ts:35-45 "fires when a host transitions from unknown to online" — uses `user-1.host-1` key, checks `hostSlug === 'host-1'` | None | UNIT_ONLY | No integration test |
| ADR-0046: `isHealthy(hostSlug)` returns online boolean | `isHealthy` returns `online` directly | heartbeat-monitor.test.ts:21-29 | None | UNIT_ONLY | |
| ADR-0046: HeartbeatMonitor no time-delta check | Online/offline is immediate, no threshold | Implicitly tested by heartbeat-monitor.test.ts — returns correct value immediately after KV set | None | UNIT_ONLY | |
| ADR-0046: onHealthChanged fires on transition | Listener fires on `online` value change only | heartbeat-monitor.test.ts:45-75 (does not fire on same-value) | None | UNIT_ONLY | |
| ADR-0046: SessionListVM `isHealthy(p.hostSlug ?? 'local')` | `healthy` derived from host slug | session-list-vm.test.ts:86-92 "P6: healthy is true after online:true heartbeat arrives for the host slug" | None | UNIT_ONLY | Tests `hostSlug: 'host-1'`, verifies `project.healthy === true` after KV entry |
| ADR-0046: SessionListVM `healthy` false when no heartbeat | Default to false | session-list-vm.test.ts:82-85 "P6: healthy is false when no heartbeat seen" | None | UNIT_ONLY | |

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none found) | No open bugs with Component: mclaude-web in .agent/bugs/ | — | `.agent/bugs/` directory not found or empty |

### Summary

- Implemented: 13
- Gap: 0
- Partial: 0
- Infra: 19
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 7
- E2E only: 0
- Untested: 0
- Bugs fixed: 0
- Bugs open: 0
