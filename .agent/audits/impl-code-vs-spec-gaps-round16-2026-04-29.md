# Audit: Code vs Spec — Round 16

**Date:** 2026-04-29
**Scope:** All 6 spec files cross-referenced against all production code files.
**Result:** **CLEAN** — no unflagged gaps found.
**Confidence:** 95%

## Specs Audited

1. `docs/spec-state-schema.md` — Postgres, KV buckets, JetStream streams, NATS subjects, K8s resources, login response, lifecycle payloads, quota status
2. `docs/mclaude-control-plane/spec-control-plane.md` — HTTP endpoints, NATS subscriptions, startup sequence, auth/JWT, OAuth, project creation flow, error handling
3. `docs/mclaude-controller/spec-controller.md` — K8s + local controller variants, NATS subscriptions, reconciler loop, provisioning protocol
4. `docs/mclaude-web/spec-web.md` — SPA state management, KV watches, session/project CRUD, event rendering, routing
5. `docs/charts-mclaude/spec-helm.md` — mclaude-cp + mclaude-worker chart resources, values, pre-install Jobs
6. `docs/mclaude-session-agent/spec-session-agent.md` — Agent lifecycle, JetStream consumers, session state machine, graceful shutdown, quota monitor, daemon mode

## Code Files Audited

- `mclaude-control-plane/`: main.go, server.go, auth.go, admin.go, db.go, hosts.go, projects.go, nkeys.go, sys_subscriber.go
- `mclaude-controller-k8s/`: main.go, reconciler.go, nats_subscriber.go, nkeys.go, mcproject_types.go
- `mclaude-session-agent/`: main.go, agent.go, session.go, state.go, events.go, quota_monitor.go, daemon.go
- `mclaude-common/pkg/subj/subj.go`
- `mclaude-web/src/lib/subj.ts`, `src/stores/session-store.ts`, `src/viewmodels/session-list-vm.ts`, `src/viewmodels/conversation-vm.ts`
- `charts/mclaude-cp/templates/control-plane-deployment.yaml`, `charts/mclaude-cp/values*.yaml`
- `charts/mclaude-worker/templates/*.yaml`, `charts/mclaude-worker/values*.yaml`

## Cross-Reference Summary

Every discrepancy between spec and code was verified against the flagged-marker list. All gaps found are already documented in specs with one or more of the recognized markers (Known bug, Known gap, Not yet implemented, Dead code, etc.).

### Key areas verified (all flagged):

| Area | Status | Flag(s) |
|------|--------|---------|
| `handleRefresh` returns `s.natsURL` | Already flagged | Known bug |
| `/readyz` returns 200 unconditionally | Already flagged | Known bug |
| `IssueUserJWT` double expiry (`expiresAt + expirySecs`) | Already flagged | Known bug |
| Auth middleware no cross-user/host enforcement | Already flagged | Known gap |
| `computeUserSlug` no blocklist check | Already flagged | Known bug |
| `POST /admin/users` no `IsAdmin` field | Already flagged | Known gap |
| `adminGrantCluster` calls `GetUserByEmail(req.UserSlug)` | Already flagged | Known bug |
| `IssueHostJWT` receives UUID instead of slug (multiple paths) | Already flagged | Known bug |
| `adminStopSession` queries nonexistent `sessions` table | Already flagged | non-functional |
| `CreateProjectWithIdentity` missing slug + host_id columns | Already flagged | Known bug |
| `POST /admin/users/{uslug}/promote` not wired | Already flagged | Not yet implemented |
| `DELETE /admin/clusters/{cslug}` not wired | Already flagged | Not yet implemented / deferred per |
| Admin user delete: no NATS JWT revocation or K8s cleanup | Already flagged | Known gap |
| Cluster register response missing `accountJwt`/`operatorJwt` | Already flagged | Known bug |
| HTTP project CRUD not implemented | Already flagged | Not yet implemented |
| SCIM endpoints not implemented | Already flagged | Not yet implemented |
| Helm `HTTP_PORT` vs Go `PORT` mismatch | Already flagged | Known mismatch |
| Helm `metricsPort` 9091 has no listener | Already flagged | Known issue |
| `JWT_REFRESH_THRESHOLD_SECONDS` phantom env var | Already flagged | phantom |
| `LOG_LEVEL`, `HELM_RELEASE_NAME`, `METRICS_PORT` not read by Go | Already flagged | Not read by Go code |
| `MCLAUDE_LIFECYCLE` stream not created by session-agent | Already flagged | known gap |
| `natsConnect()` missing `MaxReconnects(-1)` + `RetryOnFailedConnect` | Already flagged | Known bug |
| `clearPendingControlsForResume` sets idle not restarting | Already flagged | Known bug |
| Only 4 of 9 session state constants defined | Already flagged | Known bug (under clearPendingControlsForResume) |
| `handleSideEffect` missing cache token accumulation | Already flagged | Known bug |
| `accumulateUsage()` is dead code | Already flagged | Dead code |
| 30s startup timer does nothing | Already flagged | Known gap |
| Auto-restart not implemented | Already flagged | auto-restart is not implemented |
| HOST_SLUG fallback to `h-unknown` | Already flagged | silently falls back / h-unknown |
| QuotaMonitor subscribes to UUID-based subject | Already flagged | Known bug / never match |
| `publishExitLifecycle` uses `session_quota_interrupted` | Already flagged | superseded |
| `session_job_cancelled` lifecycle not published | Already flagged | Known bug |
| `session_job_paused` missing spec fields | Already flagged | Known bugs |
| `session_created` / `session_stopped` missing extra fields | Already flagged | Known bug |
| Controller-k8s: no `update` handler | Already flagged | Not yet implemented |
| Controller-k8s: `LEADER_ELECTION` not read | Already flagged | not yet read |
| Controller-k8s: `JS_DOMAIN` not read | Already flagged | not yet read |
| Controller-k8s: `NATS_CREDENTIALS_PATH` not read | Already flagged | not read by |
| `CLAUDE_CODE_TMPDIR` not injected in pod template | Already flagged | not yet injected |
| In-flight shell tracking not implemented | Already flagged | Not yet implemented / target design |
| SPA `createProject` missing `hostSlug` | Already flagged | Known bug |
| SPA `deleteSession` sends `sessionId` not `sessionSlug` | Already flagged | Known gap |
| SPA `restartSession` missing `requestId` | Already flagged | Known gaps |
| SPA `subjSessionsRestart` has no Go counterpart | Already flagged | no Go counterpart |
| SPA dead code: `subjClusterProvision`, `subjClusterStatus`, `kvKeyUserClusters` | Already flagged | Dead code |
| Go dead code: `ClustersKVKey` | Already flagged | Dead code |
| KV DEL handler deletes by slug, map keyed by UUID | Already flagged | ghost sessions |
| Login response flat shape vs spec target | Already flagged | Spec target |
| ProjectKVState missing `sessionCount`/`worktrees`/`lastActiveAt` | Already flagged | not yet implemented |
| Project KV key UUID-based (not slug-based) | Already flagged | deferred per ADR-0050 |
| JobEntry struct ADR-0009 fields, missing ADR-0034 fields | Already flagged | ADR-0034 |
| Daemon job dispatch UUID-based subjects | Already flagged | UUID-as-slug / always miss |
| Device-code pending response missing `expiresAt` | Already flagged | Known gap |

## Borderline Exclusions

1. **`adminPort` default 9090 (chart) vs 9091 (Go):** Documented as a "Note" in spec without an explicit "Known" marker, but the Helm template overrides the default via `ADMIN_PORT`, so deployments use the chart value. Not a functional gap.

2. **`PROVISION_TIMEOUT_SECONDS` hardcoded:** Spec notes "not yet read from env" — matches skip marker "not yet read".

3. **Controller-k8s `LEADER_ELECTION_NAMESPACE`:** Flagged as "Not yet implemented" — both binary and Helm template don't use it.
