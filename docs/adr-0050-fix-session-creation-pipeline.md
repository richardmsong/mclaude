# ADR: Fix Session Creation Pipeline

**Status**: implemented
**Status history**:
- 2026-04-28: accepted
- 2026-04-28: implemented — all scope CLEAN

## Overview

Session creation silently fails end-to-end: the SPA publishes to NATS but no session-agent pod exists to handle the request. Six interrelated bugs prevent the pipeline from working. This ADR fixes them all so that clicking `+` in the SPA creates a session.

## Motivation

User clicks the `+` button on the dashboard. `DashboardScreen` takes the single-project fast-path and calls `createSession(projectId, 'main', 'new-session')`. The call publishes a NATS request, waits 30 seconds for a session to appear in KV, times out, and the `catch {}` block silently swallows the error. The user sees nothing happen.

Root causes (all must be fixed together):

1. **No session-agent pod exists.** `seedDev` creates the default project in Postgres and writes to `mclaude-projects` KV, but never publishes a provisioning request to the K8s controller. The controller is running and subscribed (`mclaude.users.*.hosts.local.api.projects.>`), but has received zero requests. Zero `MCProject` CRs exist.

2. **`ProjectKVState` missing slug fields.** The Go struct has `ID, Name, GitURL, Status, CreatedAt, GitIdentityID` but the spec (and TypeScript type) expects `slug, userSlug, hostSlug`. Without these, the SPA falls back to `projectId` (UUID) for the project slug in NATS subjects. The session-agent subscribes with the real slug → subject mismatch.

3. **`ProvisionRequest.UserSlug` set to UUID.** The HTTP project-creation handler sets `ProvisionRequest{UserSlug: userID}` where `userID` is the UUID from `claims.Name`. The controller stores this in `MCProjectSpec.UserID`, and the reconciler injects it as `USER_SLUG` env var in the session-agent pod. Result: session-agent uses UUID for user slug in subjects.

4. **MCProject CRD missing slug fields.** `MCProjectSpec` has `UserID` and `ProjectID` but no separate slug fields. The reconciler has TODO comments: `{Name: "USER_SLUG", Value: userID} // TODO: resolve slug from userID`. It cannot resolve slugs because they're not in the CR.

5. **Session-agent JWT missing host-scoped permissions.** `SessionAgentSubjectPermissions(userID)` returns only `mclaude.{UUID}.>`. The session-agent publishes/subscribes to `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.>` (host-scoped per ADR-0035). Also missing: `_INBOX.>` (request/reply) and `$JS.*.API.>` (JetStream KV + streams).

6. **`writeProjectKV` uses `userID.projectID` as key.** The spec says key format is `{uslug}.{hslug}.{pslug}` but the code uses `{UUID}.{UUID}`. This ADR does NOT migrate the key format (separate ADR) — instead it adds slug fields to the KV value so the SPA can construct correct subjects.

## Decisions

| # | Decision | Choice | Rationale |
|---|----------|--------|-----------|
| 1 | Add slug fields to `ProjectKVState` | Add `Slug`, `UserSlug`, `HostSlug` string fields to Go struct. `writeProjectKV` signature gains `userSlug, hostSlug string` params. All call sites updated. | SPA needs slugs in the value to build host-scoped NATS subjects. Key format migration deferred. |
| 2 | Add `UserID`/`ProjectID` (UUID) to `ProvisionRequest` | `ProvisionRequest` gains `UserID` and `ProjectID` string fields alongside existing `UserSlug`, `HostSlug`, `ProjectSlug`. HTTP handler populates all five: slug fields from `user.Slug`/`proj.Slug`/`req.HostSlug`, UUID fields from `user.ID`/`proj.ID`. | Controller needs both UUIDs (for K8s resource naming, namespace naming) and slugs (for NATS subjects, env vars). |
| 3 | Add slug fields to `MCProjectSpec` | Add `UserSlug` and `ProjectSlug` string fields. Controller's `handleCreate` populates from `ProvisionRequest`. | Reconciler needs slugs separate from IDs. |
| 4 | Fix reconciler env vars | `USER_ID = Spec.UserID`, `PROJECT_ID = Spec.ProjectID`, `USER_SLUG = Spec.UserSlug`, `PROJECT_SLUG = Spec.ProjectSlug`, `HOST_SLUG = tpl.hostSlug` (cluster slug, unchanged). Remove TODO comments. | Each env var gets the correct value type. |
| 5 | Fix session-agent JWT permissions | `IssueSessionAgentJWT` gains `userSlug string` param. `SessionAgentSubjectPermissions(userID, userSlug)` returns: PubAllow = `[mclaude.{UUID}.>, mclaude.users.{uslug}.hosts.*.>, _INBOX.>, $JS.*.API.>]`, SubAllow = same. | Same pattern as user JWT (ADR-0049) — both old UUID prefix and new host-scoped prefix, plus JetStream and inbox. |
| 6 | `seedDev` publishes provisioning request | After creating the default project, `seedDev` publishes a NATS request to `mclaude.users.{userSlug}.hosts.local.api.projects.create` with the full `ProvisionRequest` payload. Uses `nc.Request` with 30s timeout. Logs result. Non-fatal on failure (controller may not be running yet during startup race). | Triggers the K8s controller to create the MCProject CR and session-agent pod. |
| 7 | `seedDev` resolves host slug from DB | `seedDev` queries the `local` host's ID to populate `ProvisionRequest.HostSlug` and to set `ProjectKVState.HostSlug`. | The host row already exists from the migration backfill. |

## Impact

**Specs updated in this commit:**
- `docs/spec-state-schema.md` — `ProjectKVState` value schema (add slug fields to Go-side note), `MCProject` CRD spec (add `userSlug`/`projectSlug`), session-agent JWT permissions section (new), dev-seed provisioning note.
- `docs/mclaude-control-plane/spec-control-plane.md` — `seedDev` provisioning step, `writeProjectKV` signature, `ProvisionRequest` schema, session-agent JWT permissions.

**Components implementing the change:**
- `mclaude-control-plane`: `projects.go` (`ProjectKVState`, `writeProjectKV`, project-create handler), `main.go` (`seedDev`), `nkeys.go` (`IssueSessionAgentJWT` in control-plane copy if exists).
- `mclaude-controller-k8s`: `nats_subscriber.go` (`handleCreate` — populate MCProject slug fields), `mcproject_types.go` (`MCProjectSpec` new fields), `reconciler.go` (`buildPodTemplate` env vars), `nkeys.go` (`IssueSessionAgentJWT`, `SessionAgentSubjectPermissions`).

## Scope

**In v1:**
- ProjectKVState slug fields in value (not key migration)
- ProvisionRequest + MCProjectSpec slug fields
- Reconciler env var fix
- Session-agent JWT host-scoped permissions
- seedDev provisioning request
- All six bugs fixed; session creation works end-to-end

**Explicitly deferred:**
- KV key format migration from `{UUID}.{UUID}` to `{uslug}.{hslug}.{pslug}` (separate ADR — affects SessionStore watch prefix, HeartbeatMonitor, all KV readers)
- Default onboarding session (separate ADR)
- Error surfacing in DashboardScreen (the `catch {}` swallowing errors)
