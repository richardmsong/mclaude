# Code-vs-Spec Gap Audit — Round 18

**Date:** 2026-04-29
**Scope:** All components, fresh full audit
**Auditor:** Implementation evaluator subagent

## Summary
- Total gaps found: 13
- New gaps (not in rounds 1-17): 2
- Known gaps (from round 17): 11 confirmed still present
- Known gaps now FIXED since round 17: 0

## Gaps by Component

### mclaude-control-plane

| # | Description | Severity | New/Known | Spec Quote | Code Location |
|---|-------------|----------|-----------|------------|---------------|
| CP-1 | **`handleDeleteProjectHTTP` bare SQL delete** — No NATS notification to controllers when a project is deleted via HTTP `DELETE /api/users/{uslug}/projects/{pslug}`. Controllers cannot tear down K8s resources (namespaces, Deployments, PVCs). Compare to `handleDeleteHost` which correctly calls `publishProjectsDeleteToHost` before SQL delete, and the NATS project-create handler which calls `publishProjectsUpdated`. | High | Known (ADR-0052 CP-04) | spec-control-plane §HTTP Endpoints: "Project delete: Asks the controller to tear down per-project resources." | `project_http.go:157-172` — bare `DELETE FROM projects WHERE user_id = $1 AND slug = $2` with no NATS publish |
| CP-2 | **`adminDeleteUser` no JWT revocation** — User deletion publishes NATS project-delete notifications (the orphaning half was fixed), but does NOT revoke the user's NATS JWT. The user can still connect to NATS and publish/subscribe until the JWT's 8h expiry. | Medium | Known (ADR-0052 R7-G2) | spec-control-plane §Admin Endpoints: "does not revoke the user's NATS JWT (remains valid until expiry)" | `admin.go:172-190` — deletes user and publishes project-delete to controllers, but no JWT revocation |
| CP-3 | **`adminStopSession` publishes to orphan subject** — Handler publishes to `mclaude.users.{uslug}.api.sessions.stop`, a subject that no component subscribes to. Session-agent subscribes to `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.delete`. The break-glass stop is effectively non-functional. | Medium | Known | spec-control-plane §Admin Endpoints: "Known bug: handler executes UPDATE sessions … against a sessions table that does not exist" | `admin.go:229-253` — publishes to `"mclaude.users." + userSlug + ".api.sessions.stop"` which has no subscriber |
| CP-4 | **Helm `HTTP_PORT` vs Go `PORT` mismatch** — Chart injects `HTTP_PORT` but Go reads `PORT`. Both default to `8080` so deployments work, but an operator overriding `controlPlane.service.httpPort` would have no effect. | Low | Known | spec-control-plane §Environment Variables: "`PORT` — Main API listen port"; spec-helm: "`controlPlane.service.httpPort` — Injected as `HTTP_PORT`" | `main.go:49` reads `envOr("PORT", "8080")`; `control-plane-deployment.yaml:71` injects `HTTP_PORT` |
| CP-5 | **Helm phantom env vars** — `LOG_LEVEL`, `HELM_RELEASE_NAME`, `METRICS_PORT`, `JWT_REFRESH_THRESHOLD_SECONDS` are injected by the chart but not consumed by Go code. | Low | Known | spec-control-plane §Environment Variables documents each as "Not read by Go code" | `control-plane-deployment.yaml:68-80` injects vars; `main.go` has no `os.Getenv` for any of them |
| CP-6 | **`handleCreateProjectHTTP` creates Postgres row but skips KV write and provisioning** — The HTTP `POST /api/users/{uslug}/projects` handler creates a project in Postgres but does NOT: (a) write to `mclaude-projects` KV (SPA never sees the project), (b) publish a provisioning request to the controller (no session-agent pod), (c) publish `projects.updated` notification. Compare to the NATS-based handler which does all three. The spec says "HTTP project CRUD — not yet implemented" but the handlers DO exist and are wired to the router. | Medium | **New** | spec-control-plane §HTTP Endpoints: "HTTP project CRUD — not yet implemented: The following endpoints are spec targets but have no HTTP handlers in the code" — stale, handlers exist | `project_http.go:89-117` — calls `CreateProjectWithIdentity` then returns, with no `writeProjectKV`, `publishProjectsUpdated`, or provisioning request |

### mclaude-session-agent

| # | Description | Severity | New/Known | Spec Quote | Code Location |
|---|-------------|----------|-----------|------------|---------------|
| SA-1 | **`session_job_complete` includes stale `prUrl` field** — `publishExitLifecycle` includes `prUrl` extracted from `SESSION_JOB_COMPLETE:{prUrl}` marker. ADR-0034 target removes this field. | Low | Known (ADR-0052 R8-G6) | spec-state-schema §session_job_complete: "Known gap: code still includes prUrl… ADR-0034 target removes this field" | `quota_monitor.go:134-138` — `m.publishLifec(…, map[string]string{"prUrl": m.completionPR, …})` |
| SA-2 | **HOST_SLUG fallback chain instead of fatal** — Spec says `HOST_SLUG` absence should be fatal. Code falls back through `~/.mclaude/active-host` symlink → `slugify(hostname flag)` → `slugify(os.Hostname())` before fataling only on empty string. A non-empty hostname produces a derived slug that may cause sessions to publish to incorrect NATS subjects. Improvement since round 17: `"h-unknown"` fallback is gone and the code does fatal on truly empty string. | Medium | Known (partially improved) | spec-session-agent §Configuration: "HOST_SLUG — Required. Known bug: code does not fatal on absence — silently falls back" | `main.go:107-121` — fallback chain: `active-host` → `hostname` → `os.Hostname()` → fatal only if empty |
| SA-3 | **`publishExitLifecycle` quota event `outputTokensSinceSoftMark` contains utilization %, not token count** — When `stopReason == "quota"`, publishes `outputTokensSinceSoftMark: fmt.Sprintf("%d", m.lastU5)`, but `m.lastU5` is the 5-hour utilization percentage (set from `qs.U5` in the quota subscription handler), not output tokens since soft mark. | Low | Known | spec-state-schema §session_job_paused: "`outputTokensSinceSoftMark`: 12345" (integer token count) | `quota_monitor.go:141-146` — `"outputTokensSinceSoftMark": fmt.Sprintf("%d", m.lastU5)` where `m.lastU5 = qs.U5` (utilization %) at line 187 |
| SA-4 | **MCLAUDE_LIFECYCLE stream MaxAge mismatch** — Spec says 30 days, code creates with 7 days. | Low | **New** | spec-state-schema §MCLAUDE_LIFECYCLE: "MaxAge: 30 days" | `agent.go:135` — `MaxAge: 7 * 24 * time.Hour` (7 days, not 30) |

### mclaude-web (SPA)

| # | Description | Severity | New/Known | Spec Quote | Code Location |
|---|-------------|----------|-----------|------------|---------------|
| WEB-1 | **`subjProjectsCreate` host-scoped subject doesn't match control-plane subscriber** — SPA's `subjProjectsCreate(uslug, hslug)` produces `mclaude.users.{uslug}.hosts.{hslug}.api.projects.create` (host-scoped). The control-plane subscribes to `mclaude.users.*.api.projects.create` (user-scoped, no host segment). The NATS wildcard `*` matches exactly one token, so the host-scoped subject (with 2 extra tokens `.hosts.{hslug}.`) does NOT match. SPA project creation requests now route to the controller instead of the control-plane (if `hslug` matches a cluster slug) or go undelivered. | Critical | Known | spec-state-schema §NATS Subjects: "`mclaude.users.{uslug}.api.projects.create` — Publisher: SPA. Subscriber: control-plane" | `mclaude-web/src/lib/subj.ts:19-21` produces host-scoped subject; `mclaude-control-plane/projects.go:84` subscribes to `mclaude.users.*.api.projects.create` (user-scoped) |
| WEB-2 | **Dead code: `subjClusterProvision` / `subjClusterStatus` / `kvKeyUserClusters`** — These helpers reference removed `mclaude.clusters.{cslug}.*` subjects and the removed `mclaude-clusters` KV bucket. | Low | Known | spec-web §Dead Code: "should be cleaned up in a future commit" | `mclaude-web/src/lib/subj.ts:108-117, 140-142` |

### mclaude-controller-k8s

| # | Description | Severity | New/Known | Spec Quote | Code Location |
|---|-------------|----------|-----------|------------|---------------|
| K8S-1 | **Controller `handleCreate` reply format vs SPA expectation** — Controller replies with `{ok: true, projectSlug}` but SPA `createProject` expects `{id}` in the reply. Consequence of WEB-1: if the SPA routes project creation to the controller, the reply format is incompatible (`createProject` throws "no id in reply"). | High | Known (consequence of WEB-1) | spec-controller §Shared Behavior: "Reply on success: `{ok: true, projectSlug}`" | `mclaude-controller-k8s/nats_subscriber.go:237-241` — `ProvisionReply{OK: true, ProjectSlug: projectSlug}` vs SPA expecting `{id: uuid}` |

### mclaude-common

No gaps found. All subject helpers, KV key helpers, slug types, and lifecycle constants match specs.

### Helm Charts

| # | Description | Severity | New/Known | Spec Quote | Code Location |
|---|-------------|----------|-----------|------------|---------------|
| HELM-1 | **`METRICS_PORT` injected but not consumed** — Chart declares a `metrics` container port and injects `METRICS_PORT` env var. Go code does not create a third listener; `/metrics` is served on the admin port. Port has no listener in Helm deployments. | Low | Known (ADR-0052 R7-G10) | spec-helm §Configuration knobs: "Go code does not create a third listener — /metrics is served on the admin port. Port 9091 has no listener in Helm deployments." | `charts/mclaude-cp/templates/control-plane-deployment.yaml:63,74` — `containerPort: metricsPort` and env `METRICS_PORT`; `main.go` has no reader |

## Cross-Component Gaps

| # | Description | Severity | New/Known | Spec Section | Code Location |
|---|-------------|----------|-----------|--------------|---------------|
| CROSS-1 | **SPA project creation flow broken end-to-end** — The SPA publishes project creation requests to a host-scoped subject (`mclaude.users.{uslug}.hosts.{hslug}.api.projects.create`) which does NOT match the control-plane's user-scoped subscriber (`mclaude.users.*.api.projects.create`). The NATS `*` wildcard matches exactly one token — the extra `.hosts.{hslug}.` tokens prevent the match. If `hslug` matches a registered cluster slug, the message routes to the K8s controller instead, which replies with `{ok, projectSlug}` (no `id` field), causing the SPA to throw. If `hslug` doesn't match any cluster, the message goes undelivered. No Postgres row, no KV entry, no session-agent pod. | Critical | Known | spec-state-schema §NATS Subjects; spec-control-plane §Project Creation Flow | WEB-1 + K8S-1 combined — `mclaude-web/src/lib/subj.ts:19-21`, `mclaude-control-plane/projects.go:84`, `mclaude-controller-k8s/nats_subscriber.go:237-241` |

## Major Fixes Verified Since Round 17

No new fixes detected between round 17 and round 18. All 32 previously-verified fixes remain intact.

## Notes on Spec Staleness

Several spec documents contain "Known bug" annotations that are stale (the code has been fixed but the spec text wasn't updated). These are not code-vs-spec gaps (code is correct) but should be cleaned up:

1. **spec-control-plane §`/auth/refresh`** says "returns `s.natsURL` (internal broker URL)" — code returns `s.natsWsURL` (correct). Fixed in round 2.
2. **spec-control-plane §`/readyz`** says "returns 200 unconditionally" — code now checks Postgres connectivity. Fixed in round 2.
3. **spec-control-plane §`IssueUserJWT`** says "double-counts expiry (now + 16h)" — code uses `time.Now().Unix() + expirySecs` (correct single-count). Fixed in round 2.
4. **spec-control-plane §HTTP project CRUD** says "no HTTP handlers in the code" — handlers `handleCreateProjectHTTP`, `handleDeleteProjectHTTP`, `handleListProjectsHTTP`, `handleGetProjectHTTP` exist and are wired (though CREATE is incomplete — see CP-6).
5. **spec-web §KV Watch DEL/PURGE** says "`this._sessions.delete(sessionSlug)` is a no-op" — code now does proper slug→UUID lookup via `_sessions` iteration. Fixed in a prior round.
6. **spec-session-agent §MCLAUDE_LIFECYCLE** says "session-agent code does not currently create this stream on startup" — code now creates it (`agent.go:129-139`). Fixed in a prior round.
