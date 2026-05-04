# ADR-0050 Spec Alignment Audit — All 16 Decisions

**Date**: 2026-04-28
**ADR**: `docs/adr-0050-fix-session-creation-pipeline.md`
**Specs evaluated**:
- `docs/spec-state-schema.md`
- `docs/mclaude-control-plane/spec-control-plane.md`
- `docs/mclaude-controller/spec-controller.md`
- `docs/charts-mclaude/spec-helm.md`
- `docs/mclaude-session-agent/spec-session-agent.md`
- `docs/mclaude-web/spec-web.md`

---

## Summary

| Status | Count |
|--------|-------|
| REFLECTED | 9 |
| GAP [SPEC→FIX] | 7 |
| GAP [ADR→FIX] | 0 |

---

## D1 — Add slug fields to `ProjectKVState`

**REFLECTED**

`spec-state-schema.md` → `mclaude-projects` KV value `ProjectState` includes `slug`, `userSlug`, `hostSlug`:
```json
{
  "id": "string (UUID v4)",
  "slug": "string",
  "userSlug": "string",
  "hostSlug": "string",
  ...
}
```

---

## D2 — Add `UserID`/`ProjectID` (UUID) to `ProvisionRequest`

**GAP [SPEC→FIX]** in `docs/mclaude-controller/spec-controller.md`

`spec-state-schema.md` NATS Subjects table for `…api.projects.provision` correctly shows payload: `{userID, userSlug, hostSlug, projectID, projectSlug, gitUrl, gitIdentityId}` ✅

`spec-control-plane.md` NATS Publishes table correctly shows: `Payload: {userID, userSlug, hostSlug, projectID, projectSlug, gitUrl, gitIdentityId}` ✅

**But** `spec-controller.md` → "Shared Behavior / Provisioning request shape" is **stale**:
```json
{
  "userSlug":    "alice-gmail",
  "hostSlug":    "us-east",
  "projectSlug": "billing",
  "gitUrl":      "https://github.com/alice/billing.git",
  "gitIdentityId": "uuid"
}
```
Missing `userID` and `projectID` fields. Needs update to:
```json
{
  "userID":      "uuid",
  "userSlug":    "alice-gmail",
  "hostSlug":    "us-east",
  "projectID":   "uuid",
  "projectSlug": "billing",
  "gitUrl":      "https://github.com/alice/billing.git",
  "gitIdentityId": "uuid"
}
```

---

## D3 — Add slug fields to `MCProjectSpec`

**REFLECTED**

`spec-state-schema.md` MCProject CRD spec shows:
```yaml
spec:
  userId: string         # required — UUID v4
  projectId: string      # required — UUID v4
  userSlug: string       # required — typed slug (ADR-0050)
  projectSlug: string    # required — typed slug (ADR-0050)
```

---

## D4 — Fix reconciler env vars

**REFLECTED**

`spec-state-schema.md` → Deployment section: "env vars `USER_ID`, `PROJECT_ID` (UUIDs for FK joins), `USER_SLUG`, `PROJECT_SLUG`, `HOST_SLUG` (slugs for NATS subject and KV key construction per ADR-0024 + ADR-0035)"

`spec-controller.md` → Reconciler loop step 7: "Pod env vars include `USER_ID`, `USER_SLUG`, `HOST_SLUG`, `PROJECT_ID`, `PROJECT_SLUG`"

---

## D5 — Fix session-agent JWT permissions

**REFLECTED** (base structure)

`spec-state-schema.md` → "Per-session-agent JWT permissions" section exists with:
```
publish:   mclaude.{userID}.>, mclaude.users.{userSlug}.hosts.*.>, _INBOX.>, $JS.*.API.>
subscribe: mclaude.{userID}.>, mclaude.users.{userSlug}.hosts.*.>, _INBOX.>, $JS.*.API.>
```

Includes `IssueSessionAgentJWT(userID, userSlug, accountKP)` signature. Note: D10 and D11 identify specific missing permission entries within this section.

---

## D6 — `seedDev` publishes provisioning request

**REFLECTED**

`spec-control-plane.md` → Startup Sequence step 9: "After writing KV entries, publishes a NATS provisioning request (`mclaude.users.{uslug}.hosts.local.api.projects.create`) for the default project so the K8s controller creates the MCProject CR and session-agent pod (ADR-0050). Non-fatal on failure (controller may not be running yet during startup race)."

---

## D7 — `seedDev` resolves host slug from DB

**REFLECTED**

`spec-control-plane.md` → Startup Sequence step 9 describes creating a "default `local` machine host" and then using it in the provisioning request. The host slug `local` is embedded in the subject and the flow implicitly requires resolving the host row. The spec also documents the `mclaude-hosts` KV write with `online=true` for the dev host.

---

## D8 — MCProject CRD schema includes slug fields

**REFLECTED**

`spec-state-schema.md` → MCProject CRD spec explicitly shows `userSlug` and `projectSlug` as `required` with `(ADR-0050)` annotation:
```yaml
spec:
  userSlug: string       # required — typed slug (ADR-0050)
  projectSlug: string    # required — typed slug (ADR-0050)
```

---

## D9 — Reconciler reads `SESSION_AGENT_TEMPLATE_CM` env var

**GAP [SPEC→FIX]** in `docs/mclaude-controller/spec-controller.md`

ADR-0050 introduces `SESSION_AGENT_TEMPLATE_CM` as an env var the controller reads to explicitly name the session-agent-template ConfigMap (falling back to `releaseName + "-session-agent-template"`). This was needed because `HELM_RELEASE_NAME` was never set, causing fallback to `"mclaude"` and wrong ConfigMap name.

`spec-controller.md` Configuration table lists only `HELM_RELEASE_NAME` — does **not** list `SESSION_AGENT_TEMPLATE_CM`.

**Fix needed**: Add to the Configuration table:
```
| `SESSION_AGENT_TEMPLATE_CM` | No | `{HELM_RELEASE_NAME}-session-agent-template` | Explicit ConfigMap name for the session-agent pod template. Takes precedence over the derived name from `HELM_RELEASE_NAME`. |
```

---

## D10 — Session-agent JWT includes `$JS.API.>` for direct connections

**GAP [SPEC→FIX]** in `docs/spec-state-schema.md`

ADR-0050 D10 says `SessionAgentSubjectPermissions` must return **both** `$JS.API.>` (direct worker NATS, no domain prefix) **and** `$JS.*.API.>` (domain-qualified through hub). The rationale: session-agents connect directly to worker NATS where `$JS.*.API.>` alone requires a domain token that isn't present on direct connections.

Current spec-state-schema.md "Per-session-agent JWT permissions" lists only `$JS.*.API.>`, missing `$JS.API.>`.

**Fix needed**: Update the permission lists to:
```
publish:   mclaude.{userID}.>, mclaude.users.{userSlug}.hosts.*.>, _INBOX.>, $JS.API.>, $JS.*.API.>
subscribe: mclaude.{userID}.>, mclaude.users.{userSlug}.hosts.*.>, _INBOX.>, $JS.API.>, $JS.*.API.>
```

---

## D11 — Session-agent JWT includes KV + ACK permissions

**GAP [SPEC→FIX]** in `docs/spec-state-schema.md`

ADR-0050 D11 says the session-agent JWT must include `$KV.mclaude-sessions.>`, `$KV.mclaude-projects.>`, `$KV.mclaude-hosts.>`, `$KV.mclaude-job-queue.>`, `$JS.ACK.>`, `$JS.FC.>`, `$JS.API.DIRECT.GET.>` for both publish and subscribe. Rationale: NATS KV writes use `$KV.{bucket}.{key}` subjects; JetStream acks use `$JS.ACK.{stream}.{consumer}.>`.

Current spec-state-schema.md "Per-session-agent JWT permissions" lists none of these.

**Fix needed**: Update the permission lists to include all KV and ACK subjects:
```
publish:   mclaude.{userID}.>, mclaude.users.{userSlug}.hosts.*.>, _INBOX.>, $JS.API.>, $JS.*.API.>, $KV.mclaude-sessions.>, $KV.mclaude-projects.>, $KV.mclaude-hosts.>, $KV.mclaude-job-queue.>, $JS.ACK.>, $JS.FC.>, $JS.API.DIRECT.GET.>
subscribe: mclaude.{userID}.>, mclaude.users.{userSlug}.hosts.*.>, _INBOX.>, $JS.API.>, $JS.*.API.>, $KV.mclaude-sessions.>, $KV.mclaude-projects.>, $KV.mclaude-hosts.>, $KV.mclaude-job-queue.>, $JS.ACK.>, $JS.FC.>, $JS.API.DIRECT.GET.>
```

---

## D12 — Session-agent NATS URL points at hub NATS

**GAP [SPEC→FIX]** in `docs/mclaude-controller/spec-controller.md` and `docs/charts-mclaude/spec-helm.md`

ADR-0050 D12 says:
- Helm chart gains `sessionAgentNatsUrl` value.
- Controller reads `SESSION_AGENT_NATS_URL` env var (falling back to worker NATS URL).
- Reconciler injects this as the session-agent pod's `NATS_URL`.

`spec-controller.md` Configuration table does **not** list `SESSION_AGENT_NATS_URL`.

`spec-helm.md` mclaude-worker Configuration knobs do **not** list `sessionAgentNatsUrl`.

**Fix needed for spec-controller.md**: Add to Configuration table:
```
| `SESSION_AGENT_NATS_URL` | No | Value of `NATS_URL` | NATS URL injected into session-agent pods as `NATS_URL`. Points at hub NATS where KV buckets live, rather than worker NATS. |
```

**Fix needed for spec-helm.md**: Add to mclaude-worker Configuration knobs:
```
| `controller.config.sessionAgentNatsUrl` | `""` | NATS URL injected into session-agent pods. When set, overrides the default worker NATS URL. Session-agents need hub NATS where KV buckets (`mclaude-sessions`, `mclaude-projects`) are created. |
```

---

## D13 — SessionStore uses split KV prefixes

**GAP [SPEC→FIX]** in `docs/mclaude-web/spec-web.md`

ADR-0050 D13 says: `SessionStore` watches `mclaude-sessions` with `userSlug` prefix (slug-based keys) and `mclaude-projects` with `userId` prefix (UUID-based keys). `App.tsx` passes `authState.userSlug ?? authState.userId` as the `userSlug` constructor param.

`spec-web.md` → "State Management" says only:
> "The SPA watches NATS KV buckets for live updates: `mclaude-sessions` — session state; `mclaude-projects` — project list and status"

No mention of different watch prefixes.

**Fix needed**: Update State Management section to:
> The SPA watches NATS KV buckets for live updates:
> - `mclaude-sessions` — session state. Watch prefix: `{userSlug}` (slug-based keys written by session-agent, format `{uslug}.{hslug}.{pslug}.{sslug}`).
> - `mclaude-projects` — project list and status. Watch prefix: `{userId}` (UUID-based keys written by control-plane, format `{userId}.{projectId}` — key format migration deferred per ADR-0050).
>
> `SessionStore` constructor receives `userSlug` from `authState.userSlug ?? authState.userId` (fallback for pre-migration sessions).

---

## D14 — Session-agent event subjects include `.hosts.{hslug}.`

**REFLECTED**

`spec-session-agent.md`: "Per ADR-0035, every NATS subject the agent uses is host-scoped: `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.…`."

`spec-state-schema.md` → All three JetStream streams (MCLAUDE_EVENTS, MCLAUDE_API, MCLAUDE_LIFECYCLE) show `hosts.*` in their subject patterns. All NATS subject tables include `.hosts.{hslug}.` between user and project.

---

## D15 — ConversationVM uses resolved session UUID

**GAP [SPEC→FIX]** in `docs/mclaude-web/spec-web.md`

ADR-0050 D15 says: `App.tsx` resolves the session UUID via `session?.id ?? route.sessionId` and passes the UUID (not the route slug) to `ConversationVM`. The VM includes `session_id` in every NATS `sessions.input` payload.

`spec-state-schema.md` → The `sessions.input` payload says `session_id` is "the mclaude UUID from sessions.create" — the API contract requires UUID ✅. But `spec-web.md` does not document the resolution step in the SPA.

**Fix needed**: Add to spec-web.md (e.g. in a new "ConversationVM" or "Session Detail" section):
> When navigating to a session, `App.tsx` resolves the session UUID from the KV-watched session map: `session?.id ?? route.sessionId`. The resolved UUID — not the URL slug — is passed to `ConversationVM` and included as `session_id` in every `sessions.input` NATS payload (the session-agent indexes sessions by UUID, not slug).

---

## D16 — `DEV_OAUTH_TOKEN` passed to worker controller

**GAP [SPEC→FIX]** in `docs/mclaude-controller/spec-controller.md` and `docs/charts-mclaude/spec-helm.md`

ADR-0050 D16 says:
- `charts/mclaude-worker/templates/controller-deployment.yaml` conditionally injects `DEV_OAUTH_TOKEN` env var.
- `controller.config.devOAuthToken` is a new Helm value.
- `.github/workflows/deploy-main.yml` passes the GitHub secret.
- The controller writes it as `oauth-token` in per-user `user-secrets` Secret.

`spec-control-plane.md` documents `DEV_OAUTH_TOKEN` as a control-plane env var ✅, and `spec-state-schema.md` documents `oauth-token` in the `user-secrets` Secret with "From DEV_OAUTH_TOKEN env" ✅.

**But** `spec-controller.md` Configuration table does **not** list `DEV_OAUTH_TOKEN` as an env var for the K8s controller.

**And** `spec-helm.md` mclaude-worker Configuration knobs do **not** list `controller.config.devOAuthToken`.

**Fix needed for spec-controller.md**: Add to Configuration table:
```
| `DEV_OAUTH_TOKEN` | No | (empty) | OAuth token for dev environments. When set, written as `oauth-token` key in per-user `user-secrets` Secret so session-agent pods have Claude API credentials. |
```

**Fix needed for spec-helm.md**: Add to mclaude-worker Configuration knobs:
```
| `controller.config.devOAuthToken` | `""` | OAuth token injected as `DEV_OAUTH_TOKEN` env var into the controller Deployment. Controller writes it into per-user `user-secrets` Secret. |
```

---

## Gaps Summary — Files to Fix

| File | Decisions with gaps |
|------|-------------------|
| `docs/spec-state-schema.md` | D10, D11 (session-agent JWT permissions missing `$JS.API.>`, KV, ACK subjects) |
| `docs/mclaude-controller/spec-controller.md` | D2 (provisioning request shape), D9 (`SESSION_AGENT_TEMPLATE_CM`), D12 (`SESSION_AGENT_NATS_URL`), D16 (`DEV_OAUTH_TOKEN`) |
| `docs/charts-mclaude/spec-helm.md` | D12 (`sessionAgentNatsUrl`), D16 (`controller.config.devOAuthToken`) |
| `docs/mclaude-web/spec-web.md` | D13 (split KV prefixes), D15 (ConversationVM UUID resolution) |
