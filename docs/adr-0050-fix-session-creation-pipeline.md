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
| 8 | MCProject CRD schema includes slug fields | `charts/mclaude-worker/templates/mcproject-crd.yaml` adds `userSlug` and `projectSlug` to the OpenAPI v3 schema `spec.properties`. | Without these, Kubernetes structural schema validation prunes the fields on write, and the reconciler reads empty strings for `Spec.UserSlug`/`Spec.ProjectSlug`. |
| 9 | Reconciler reads `SESSION_AGENT_TEMPLATE_CM` env var | `loadTemplate()` uses `r.sessionAgentTemplateCM` (from `SESSION_AGENT_TEMPLATE_CM` env var, falling back to `releaseName + "-session-agent-template"`). | The Helm release name for the worker chart is `mclaude-worker`, but the reconciler's `HELM_RELEASE_NAME` env var was never set, causing fallback to `"mclaude"` and a ConfigMap-not-found → default template → wrong image. |
| 10 | Session-agent JWT includes `$JS.API.>` for direct connections | `SessionAgentSubjectPermissions` returns both `$JS.API.>` (direct worker NATS) and `$JS.*.API.>` (domain-qualified through hub). | Session-agents connect directly to worker NATS (or hub NATS), so they need the non-domain-qualified JetStream API prefix. `$JS.*.API.>` alone requires a domain token that isn't present on direct connections. |
| 11 | Session-agent JWT includes KV + ACK permissions | `SessionAgentSubjectPermissions` adds `$KV.mclaude-sessions.>`, `$KV.mclaude-projects.>`, `$KV.mclaude-hosts.>`, `$KV.mclaude-job-queue.>`, `$JS.ACK.>`, `$JS.FC.>`, `$JS.API.DIRECT.GET.>` to both PubAllow and SubAllow. | Session-agent writes to `mclaude-sessions` KV, reads projects/hosts/job-queue KV, and must acknowledge JetStream messages. NATS KV writes use `$KV.{bucket}.{key}` subjects. JetStream ACKs use `$JS.ACK.{stream}.{consumer}.>`. |
| 12 | Session-agent NATS URL points at hub NATS | Helm chart gains `sessionAgentNatsUrl` value. Controller reads `SESSION_AGENT_NATS_URL` env var (falling back to worker NATS URL). Reconciler injects this as the session-agent pod's `NATS_URL`. | KV buckets (`mclaude-sessions`, `mclaude-projects`) are created on hub NATS. Session-agents connecting to worker NATS can't find them without domain-qualified JetStream. Pointing directly at hub NATS is the simplest correct path for single-cluster and works for multi-cluster when the hub is reachable. |
| 13 | SessionStore uses split KV prefixes | `SessionStore` watches `mclaude-sessions` with `userSlug` prefix (slug-based keys written by session-agent) and `mclaude-projects` with `userId` prefix (UUID-based keys written by control-plane). `App.tsx` passes `authState.userSlug ?? authState.userId` as the `userSlug` constructor param. | Session KV keys are slug-based (`dev.local.default-project.{sslug}`), project KV keys are still UUID-based (`{userId}.{projectId}`). A single prefix can't match both; the store needs separate prefixes until the project KV key migration lands. |
| 14 | Session-agent event subjects include `.hosts.{hslug}.` | All project-scoped NATS subjects in `mclaude-session-agent` (events, lifecycle, terminal, API sessions) include `.hosts.{hslug}.` between the user and project segments per ADR-0035. | The session-agent predates ADR-0035 and constructed subjects without the host segment, causing NATS permission violations on every publish. |
| 15 | ConversationVM uses resolved session UUID | `App.tsx` resolves the session UUID via `session?.id ?? route.sessionId` and passes the UUID (not the route slug) to `ConversationVM`. The VM includes `session_id` in every NATS `sessions.input` payload. | The session-agent stores sessions in a `map[string]*Session` keyed by UUID. Sending the slug as `session_id` caused "session not found" errors on every input message. |
| 16 | `DEV_OAUTH_TOKEN` passed to worker controller | `charts/mclaude-worker/templates/controller-deployment.yaml` conditionally injects `DEV_OAUTH_TOKEN` env var from `controller.config.devOAuthToken` Helm value. `.github/workflows/deploy-main.yml` passes the GitHub secret to the worker Helm install. The controller writes it as `oauth-token` in per-user `user-secrets` Secret. | Without the OAuth token, Claude Code in session-agent pods has no API credentials and silently produces no output. The entrypoint reads `/home/node/.user-secrets/oauth-token` and exports `CLAUDE_CODE_OAUTH_TOKEN`. |

## Impact

**Specs updated in this commit:**
- `docs/spec-state-schema.md` — `ProjectKVState` value schema (add slug fields to Go-side note), `MCProject` CRD spec (add `userSlug`/`projectSlug`), session-agent JWT permissions section (new), dev-seed provisioning note.
- `docs/mclaude-control-plane/spec-control-plane.md` — `seedDev` provisioning step, `writeProjectKV` signature, `ProvisionRequest` schema, session-agent JWT permissions.

**Components implementing the change:**
- `mclaude-control-plane`: `projects.go` (`ProjectKVState`, `writeProjectKV`, project-create handler), `main.go` (`seedDev`), `nkeys.go` (`IssueSessionAgentJWT`, `SessionAgentSubjectPermissions`).
- `mclaude-controller-k8s`: `nats_subscriber.go` (`handleCreate` — populate MCProject slug fields), `mcproject_types.go` (`MCProjectSpec` new fields), `reconciler.go` (`buildPodTemplate` env vars, `loadTemplate` ConfigMap lookup, `SESSION_AGENT_NATS_URL`), `nkeys.go` (`IssueSessionAgentJWT`, `SessionAgentSubjectPermissions`), `main.go` (`SESSION_AGENT_NATS_URL`, `SESSION_AGENT_TEMPLATE_CM`).
- `mclaude-session-agent`: `session.go`, `terminal.go`, `agent.go` (host-scoped subject construction per ADR-0035).
- `mclaude-web`: `session-store.ts` (split KV watch prefixes), `App.tsx` (pass `userSlug` to SessionStore, resolve session UUID for ConversationVM).
- `charts/mclaude-worker`: `mcproject-crd.yaml` (slug fields in CRD schema), `controller-deployment.yaml` (`SESSION_AGENT_NATS_URL`, `DEV_OAUTH_TOKEN`), `values.yaml`/`values-dev.yaml`/`values-k3d-ghcr.yaml` (`sessionAgentNatsUrl`, `devOAuthToken`).
- `.github/workflows/deploy-main.yml`: passes `DEV_OAUTH_TOKEN` secret to worker Helm install.

## Scope

**In v1:**
- ProjectKVState slug fields in value (not key migration)
- ProvisionRequest + MCProjectSpec slug fields
- Reconciler env var fix
- Session-agent JWT: host-scoped permissions, JetStream API, KV write/read, ACK
- seedDev provisioning request
- MCProject CRD schema updated with slug fields
- Reconciler template ConfigMap lookup via `SESSION_AGENT_TEMPLATE_CM`
- Session-agent NATS URL points at hub NATS (where KV buckets live)
- SessionStore split KV prefixes (slug for sessions, UUID for projects)
- Session-agent event/lifecycle/terminal subjects include `.hosts.{hslug}.` per ADR-0035
- ConversationVM resolves session UUID before sending input
- `DEV_OAUTH_TOKEN` plumbed through worker Helm chart + CI to session-agent pods
- All sixteen decisions implemented; session creation works end-to-end

**Explicitly deferred:**
- KV key format migration from `{UUID}.{UUID}` to `{uslug}.{hslug}.{pslug}` for project KV (separate ADR — affects SessionStore project watch prefix, HeartbeatMonitor, all KV readers)
- Default onboarding session (separate ADR)
- Error surfacing in DashboardScreen (the `catch {}` swallowing errors)
