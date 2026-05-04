# Code-vs-Spec Gap Audit — Round 17

**Date:** 2026-04-29
**Scope:** All components, fresh full audit
**Auditor:** Implementation evaluator subagent

## Summary
- Total gaps found: 12
- New gaps (not in prior rounds): 3
- Known gaps (from ADR-0052): 4 (all confirmed still present)
- Previously reported gaps now FIXED: 30+

## Confirmed ADR-0052 Known Gaps

All 4 ADR-0052 gaps are verified as still present in code:

| # | ADR-0052 ID | Description | Status |
|---|-------------|-------------|--------|
| 1 | R7-G2 (partial) | `adminDeleteUser` sends NATS project-delete notifications to controllers now (fixed the resource-orphaning half), but does **not** revoke the user's NATS JWT — the JWT remains valid until its 8h expiry. | Still present |
| 2 | CP-04 (partial) | `handleDeleteProjectHTTP` (`project_http.go:157`) does a bare `DELETE FROM projects` SQL with **no** NATS `publishProjectsDeleteToHost()` or `publishProjectsUpdated()` calls — controller resources are orphaned. Compare to `handleDeleteHost` which correctly publishes before deleting. | Still present |
| 3 | R8-G6 | `QuotaMonitor.publishExitLifecycle` (`quota_monitor.go`) still includes `prUrl` in the `session_job_complete` lifecycle event. ADR-0034 target removes this field. | Still present |
| 4 | R7-G10 | Helm `mclaude-cp` chart injects `METRICS_PORT` env var (`control-plane-deployment.yaml:74`) but Go code does not read it — `/metrics` is served on the admin port (`ADMIN_PORT`). | Still present |

## Gaps by Component

### mclaude-control-plane

| # | Description | Severity | New/Known | Spec Section | Code Location |
|---|-------------|----------|-----------|--------------|---------------|
| CP-1 | **`handleDeleteProjectHTTP` bare SQL delete** — No NATS notification to controllers when a project is deleted via HTTP `DELETE /api/users/{uslug}/projects/{pslug}`. Controllers cannot tear down K8s resources (namespaces, Deployments, PVCs). `handleDeleteHost` correctly calls `publishProjectsDeleteToHost` before SQL delete, but `handleDeleteProjectHTTP` does not. | High | Known (ADR-0052 CP-04) | spec-control-plane §HTTP Endpoints | `project_http.go:157-172` |
| CP-2 | **`adminDeleteUser` no JWT revocation** — User deletion publishes NATS project-delete notifications (the orphaning half was fixed), but does NOT revoke the user's NATS JWT. The user can still connect to NATS and publish/subscribe until the JWT's 8h expiry. | Medium | Known (ADR-0052 R7-G2) | spec-control-plane §Admin Endpoints | `admin.go:172-190` |
| CP-3 | **`adminStopSession` publishes to orphan subject** — Handler publishes to `mclaude.users.{uslug}.api.sessions.stop`, a subject that no component subscribes to. Session-agent subscribes to `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.delete`. The break-glass stop is effectively non-functional via NATS. Spec documents this as a known bug (non-existent sessions table) — the table was removed, but the NATS replacement uses a subject no one listens on. | Medium | Known (updated) | spec-control-plane §Admin Endpoints | `admin.go:229-267` |
| CP-4 | **Helm `HTTP_PORT` vs Go `PORT` mismatch** — Chart injects `HTTP_PORT` but Go reads `PORT`. Both default to `8080` so deployments work, but an operator overriding `controlPlane.service.httpPort` would have no effect. | Low | Known (documented in spec) | spec-helm §Configuration knobs | `control-plane-deployment.yaml:70`, `main.go` reads `PORT` |
| CP-5 | **Helm phantom env vars** — `LOG_LEVEL`, `HELM_RELEASE_NAME`, `METRICS_PORT`, `JWT_REFRESH_THRESHOLD_SECONDS` are injected by the chart but not consumed by Go code. | Low | Known (documented in spec) | spec-control-plane §Environment Variables | `control-plane-deployment.yaml:68-80` |

### mclaude-session-agent

| # | Description | Severity | New/Known | Spec Section | Code Location |
|---|-------------|----------|-----------|--------------|---------------|
| SA-1 | **`session_job_complete` includes stale `prUrl` field** — `publishExitLifecycle` includes `prUrl` extracted from `SESSION_JOB_COMPLETE:{prUrl}` marker. ADR-0034 target removes this field. | Low | Known (ADR-0052 R8-G6) | spec-state-schema §session_job_complete | `quota_monitor.go:134-138` |
| SA-2 | **HOST_SLUG fallback chain instead of fatal** — Spec says `HOST_SLUG` absence should be fatal. Code falls back through `~/.mclaude/active-host` symlink → `slugify(hostname flag)` → `slugify(os.Hostname())` before fataling only on empty string. A non-empty hostname produces a derived slug that may cause sessions to publish to incorrect NATS subjects. The `"h-unknown"` fallback is removed, but hostname-derived fallback persists. | Medium | Known (partially fixed) | spec-session-agent §Configuration | `main.go:86-102` |
| SA-3 | **`publishExitLifecycle` quota event `outputTokensSinceSoftMark` semantically wrong** — When `stopReason == "quota"`, the code publishes `outputTokensSinceSoftMark: fmt.Sprintf("%d", m.lastU5)`. But `m.lastU5` is the 5h utilization percentage, not the count of output tokens since soft mark. The field name doesn't match the value. | Low | New | spec-state-schema §session_job_paused | `quota_monitor.go:141-146` |

### mclaude-web (SPA)

| # | Description | Severity | New/Known | Spec Section | Code Location |
|---|-------------|----------|-----------|--------------|---------------|
| WEB-1 | **`subjProjectsCreate` route mismatch** — SPA's `subjProjectsCreate(uslug, hslug)` now produces `mclaude.users.{uslug}.hosts.{hslug}.api.projects.create` (host-scoped). The control-plane subscribes to `mclaude.users.*.api.projects.create` (user-scoped, no host segment). The wildcard `*` matches exactly one token, so the host-scoped subject (with extra `.hosts.{hslug}.` tokens) does NOT match. SPA project creation requests now route to the controller instead of the control-plane. Consequence: (a) No Postgres `projects` row is created, (b) No `mclaude-projects` KV entry is written, (c) Controller reply `{ok, projectSlug}` doesn't include `id` which SPA expects → `createProject` throws "no id in reply". | High | **New** | spec-control-plane §Project Creation Flow; spec-state-schema §NATS Subjects | `mclaude-web/src/lib/subj.ts:19-21`, `mclaude-web/src/viewmodels/session-list-vm.ts:160-168`, `mclaude-control-plane/projects.go:101` |
| WEB-2 | **Dead code: `subjClusterProvision` / `subjClusterStatus` / `kvKeyUserClusters`** — These helpers reference removed `mclaude.clusters.{cslug}.*` subjects and the removed `mclaude-clusters` KV bucket. Never called from production code. | Low | Known (documented in spec) | spec-web §Dead Code | `mclaude-web/src/lib/subj.ts:108-117, 142-144` |

### mclaude-controller-k8s

| # | Description | Severity | New/Known | Spec Section | Code Location |
|---|-------------|----------|-----------|--------------|---------------|
| K8S-1 | **Controller `handleCreate` reply format vs SPA expectation** — Controller replies with `{ok: true, projectSlug}` but SPA `createProject` expects `{id}` in the reply. Related to WEB-1 above. If the SPA now routes project creation to the controller, the reply format is incompatible. | High | **New** (consequence of WEB-1) | spec-controller §Shared Behavior | `mclaude-controller-k8s/nats_subscriber.go:222-225`, `mclaude-web/src/viewmodels/session-list-vm.ts:166` |

### mclaude-common

No new gaps found. `ClustersKVKey` dead code was cleaned up (not present in `subj.go`). `LaptopsKVKey` is also gone, replaced by `HostsKVKey`. All subject helpers, KV key helpers, and type definitions match the spec.

### Helm Charts

| # | Description | Severity | New/Known | Spec Section | Code Location |
|---|-------------|----------|-----------|--------------|---------------|
| HELM-1 | **`METRICS_PORT` injected but not consumed** — Chart declares a `metrics` container port and injects `METRICS_PORT` env var. Go code does not create a third listener; `/metrics` is served on the admin port. Port has no listener in Helm deployments. Prometheus scrape config must target admin port, not metrics port. | Low | Known (ADR-0052 R7-G10) | spec-helm §Configuration knobs | `charts/mclaude-cp/templates/control-plane-deployment.yaml:63,74` |

### Cross-Component Gaps

| # | Description | Severity | New/Known | Spec Section | Code Location |
|---|-------------|----------|-----------|--------------|---------------|
| CROSS-1 | **SPA project creation flow broken end-to-end** — The SPA publishes project creation requests to a host-scoped subject (`mclaude.users.{uslug}.hosts.{hslug}.api.projects.create`) which routes to the controller, not the control-plane. The control-plane's user-scoped subscriber (`mclaude.users.*.api.projects.create`) never fires for the new subject. This breaks the project creation flow: no Postgres row, no KV entry, incompatible reply. The control-plane's backward-compatible subscriber is now orphaned. This is WEB-1 + K8S-1 combined. | High | **New** | spec-state-schema §NATS Subjects; spec-control-plane §Project Creation Flow | Multiple files (see WEB-1, K8S-1) |

## Major Fixes Verified Since ADR-0052

The following previously-reported gaps are confirmed **fixed** in code:

1. ✅ `/readyz` now checks Postgres connectivity (`server.go:41-47`)
2. ✅ `/auth/refresh` now returns `natsWsURL` (not `natsURL`) (`auth.go:175`)
3. ✅ `IssueUserJWT` no longer double-counts expiry (`nkeys.go:107`)
4. ✅ `AdminUserRequest.IsAdmin` field added and wired (`admin.go:27,148-151`)
5. ✅ `POST /admin/users/{uslug}/promote` now implemented (`admin.go:310-328`)
6. ✅ `DELETE /admin/clusters/{cslug}` now implemented (`admin.go:287-307`)
7. ✅ `adminGrantCluster` uses `GetUserBySlug` (not `GetUserByEmail`) (`admin.go:241`)
8. ✅ `IssueHostJWT` now receives user slug (not UUID) in all paths (`admin.go:247`, `hosts.go:179`, `hosts.go:320`)
9. ✅ `computeUserSlug` validates against reserved-word blocklist (`db.go:46-52`)
10. ✅ NATS connections use unlimited reconnects (control-plane, session-agent, controller)
11. ✅ SPA `createProject` now includes `hostSlug` in payload (`session-list-vm.ts:163`)
12. ✅ SPA KV DEL handling: slug→UUID lookup implemented (`session-store.ts:40-57`)
13. ✅ `PROVISION_TIMEOUT_SECONDS` now read from env (`projects.go:56-64`)
14. ✅ Device-code status response includes `expiresAt` (`hosts.go:222`)
15. ✅ MCLAUDE_LIFECYCLE stream now created by session-agent (`agent.go:129-139`)
16. ✅ `session_created` lifecycle includes `branch` field (`agent.go:872`)
17. ✅ `session_stopped` lifecycle includes `exitCode` field (`agent.go:956`)
18. ✅ Auto-restart on Claude process crash implemented (`agent.go:1296-1363`)
19. ✅ `QuotaMonitor` uses userSlug for subscription subject (`quota_monitor.go:42`)
20. ✅ `accumulateUsage` handles all 5 token fields (`state.go:86-91`)
21. ✅ Controller `handleUpdate` NATS handler implemented (`nats_subscriber.go:188-207`)
22. ✅ Controller leader election configured (`main.go:77`)
23. ✅ Controller reads `LOG_LEVEL` env var (`main.go:32-36`)
24. ✅ Controller reads `LEADER_ELECTION_NAMESPACE` env var (`main.go:73`)
25. ✅ `CLAUDE_CODE_TMPDIR` injected into pod template (`reconciler.go:318-319`)
26. ✅ Controller JWT permissions scoped to cluster slug (`main.go:155-165`)
27. ✅ `ClustersKVKey` dead code removed from mclaude-common
28. ✅ `LaptopsKVKey` dead code removed from mclaude-common
29. ✅ Session-agent `session_job_paused` event (previously `session_quota_interrupted`) now published correctly (`quota_monitor.go:140-146`)
30. ✅ SCIM endpoints wired (`server.go:99`)
31. ✅ SPA project KV DEL handling explicitly checks operation (`session-store.ts:82-90`)
32. ✅ Login response includes `hosts[]` and `projects[]` arrays (`auth.go:158-159`)
