# Audit Round 10: Code vs Spec Gaps

**Date:** 2026-04-29
**Spec files reviewed:** spec-state-schema.md, spec-control-plane.md, spec-controller.md, spec-web.md, spec-helm.md, spec-session-agent.md
**Code files reviewed:** mclaude-control-plane/*.go, mclaude-controller-k8s/*.go, mclaude-session-agent/{agent,session,state,events,quota_monitor,daemon_jobs,daemon,main,terminal}.go, mclaude-common/pkg/subj/subj.go, mclaude-web/src/lib/subj.ts, mclaude-web/src/stores/session-store.ts, mclaude-web/src/viewmodels/{conversation-vm,session-list-vm}.ts, mclaude-web/src/components/App.tsx

---

## GAP 1 — authMiddleware does not enforce cross-user or cross-host access boundaries

| Field | Value |
|-------|-------|
| **Code** | `mclaude-control-plane/auth.go` → `authMiddleware()` |
| **Spec** | `docs/mclaude-control-plane/spec-control-plane.md` → "Authentication and JWT Signing" |
| **Detail** | Spec states: "The auth middleware on protected routes decodes and validates the JWT against the account public key, extracts the user slug + host slug, and enforces two access boundaries: (1) rejects with 403 when the JWT's sub user slug does not match the URL's {uslug} (cross-user access), and (2) rejects with 403 when the request targets a different host than the JWT was issued for." Code only extracts `claims.Name` (user UUID) and injects it into context. There is no comparison of the JWT's user identity against URL path parameters (`{uslug}`), and no host-scope enforcement whatsoever. A user could call `/api/users/{other-uslug}/hosts` — the handler uses the JWT's userID for DB queries so the result is the caller's own data (not a data leak), but the spec's 403 rejection never fires. |

## GAP 2 — Daemon job dispatcher uses UUIDs where slugs are expected for NATS subjects and KV keys

| Field | Value |
|-------|-------|
| **Code** | `mclaude-session-agent/daemon_jobs.go` → `dispatchQueuedJob()`, `processDispatch()`, `startupRecovery()` |
| **Spec** | `docs/mclaude-session-agent/spec-session-agent.md` → "Daemon: Job Dispatcher"; `docs/spec-state-schema.md` → NATS Subjects table |
| **Detail** | `dispatchQueuedJob()` constructs the sessions.create subject with `slug.ProjectSlug(job.ProjectID)` — casting the UUID to a ProjectSlug. The session-agent subscribes to slug-based subjects (`mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.*`), so messages published to UUID-based subjects are never delivered. Same UUID→slug casting for sessions.input, lifecycle subjects, and KV key lookups (`subj.SessionsKVKey(...)` with UUID values). Also in `startupRecovery()`: `slug.UserSlug(job.UserID)` casts user UUID to UserSlug. The `JobEntry` struct has `ProjectSlug` and `UserSlug` fields, but the dispatcher ignores them and uses the UUID fields instead. |

## GAP 3 — SPA createProject() omits hostSlug from the NATS payload

| Field | Value |
|-------|-------|
| **Code** | `mclaude-web/src/viewmodels/session-list-vm.ts` → `createProject()` |
| **Spec** | `docs/mclaude-control-plane/spec-control-plane.md` → "Project Creation Flow" step 1 |
| **Detail** | Spec says SPA publishes to `mclaude.users.{uslug}.api.projects.create` with `{name, hostSlug, gitUrl?, gitIdentityId?}`. Code only sends `{name, gitUrl?, gitIdentityId?}` — `hostSlug` is missing. The control-plane handler checks `if req.HostSlug != ""` before publishing the provisioning request to the controller. When hostSlug is empty, the project row and KV entry are created but no provisioning request is sent — no MCProject CR, no session-agent pod, no namespace. SPA-created projects are inert until something triggers provisioning. The `createProject()` method doesn't accept a `hostSlug` parameter at all. |

## GAP 4 — Restart/delete NATS payload uses `sessionId` (UUID) but spec says `sessionSlug`

| Field | Value |
|-------|-------|
| **Code** | `mclaude-session-agent/agent.go` → `handleDelete()`, `handleRestart()`; `mclaude-web/src/viewmodels/session-list-vm.ts` → `deleteSession()`, `restartSession()` |
| **Spec** | `docs/spec-state-schema.md` → NATS Subjects table, `sessions.delete` and `sessions.restart` rows |
| **Detail** | Spec defines the delete payload as `{sessionSlug, requestId}` and the restart payload as `{sessionSlug, extraFlags?, requestId}`. Both the SPA and the agent use the JSON field name `sessionId` (not `sessionSlug`), and the value is a UUID (not a slug). The session-agent's in-memory map is keyed by UUID so lookups work, but the wire format disagrees with the spec. This is both a field-name mismatch (`sessionId` vs `sessionSlug`) and a value-type mismatch (UUID vs typed slug). |

## GAP 5 — SPA restartSession() omits requestId from the NATS payload

| Field | Value |
|-------|-------|
| **Code** | `mclaude-web/src/viewmodels/session-list-vm.ts` → `restartSession()` |
| **Spec** | `docs/spec-state-schema.md` → NATS Subjects table, `sessions.restart` row |
| **Detail** | Spec defines the restart payload as `{sessionSlug, extraFlags?, requestId}`. The SPA sends `{sessionId, extraFlags?}` — `requestId` is absent. The session-agent's restart handler calls `a.publishAPIError(req.RequestID, "restart", errMsg)` — when `requestId` is empty string, the api_error event has an empty `request_id` field, which the SPA has no listener for anyway (only `createSession` subscribes to api_error). Restart errors are silently lost from the SPA's perspective. |
