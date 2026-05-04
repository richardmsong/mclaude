## Run: 2026-04-28T00:00:00Z

**ADR**: `docs/adr-0050-fix-session-creation-pipeline.md`
**Status**: accepted
**Specs evaluated**:
- `docs/spec-state-schema.md`
- `docs/mclaude-control-plane/spec-control-plane.md`

### Phase 1 — ADR → Spec (forward pass)

| ADR (decision) | ADR text | Spec location | Verdict | Direction | Notes |
|----------------|----------|---------------|---------|-----------|-------|
| D1 — ProjectKVState slug fields | "Add `Slug`, `UserSlug`, `HostSlug` string fields to Go struct" | spec-state-schema: `mclaude-projects` KV Value `ProjectState` JSON includes `slug`, `userSlug`, `hostSlug` | REFLECTED | — | All three slug fields present in the KV value schema |
| D1 — writeProjectKV signature | "`writeProjectKV` signature gains `userSlug, hostSlug string` params" | spec-control-plane: Project Creation Flow step 5 writes project to KV; spec-state-schema: ProjectState value includes slug fields | REFLECTED | — | Function signature is implementation detail; spec captures the outcome (KV value has slugs) |
| D2 — ProvisionRequest UUID fields | "`ProvisionRequest` gains `UserID` and `ProjectID` string fields alongside existing `UserSlug`, `HostSlug`, `ProjectSlug`" | spec-state-schema: NATS Subjects table, `.provision` payload: `{userID, userSlug, hostSlug, projectID, projectSlug, gitUrl, gitIdentityId}` | REFLECTED | — | Both UUID and slug fields present |
| D2 — ProvisionRequest UUID fields (control-plane NATS) | Same as above | spec-control-plane: NATS Subjects (Publishes) table, `.provision` payload: `{userID, userSlug, hostSlug, projectID, projectSlug, gitUrl, gitIdentityId}` | REFLECTED | — | Matches ADR |
| D2 — ProvisionRequest UUID fields (Project Creation Flow) | Same as above | spec-control-plane: Project Creation Flow step 6: "with `{userSlug, hostSlug, projectSlug, gitUrl, gitIdentityId}`" | PARTIAL | SPEC→FIX | Step 6 omits `userID` and `projectID` from the provision payload description. The NATS Subjects section in the same spec includes them, but the narrative flow does not. |
| D3 — MCProjectSpec slug fields | "Add `UserSlug` and `ProjectSlug` string fields" | spec-state-schema: MCProject CRD spec YAML includes `userSlug: string # required — typed slug (ADR-0050)` and `projectSlug: string # required — typed slug (ADR-0050)` | REFLECTED | — | Both fields present with ADR-0050 attribution |
| D4 — Reconciler env vars | "`USER_ID = Spec.UserID`, `PROJECT_ID = Spec.ProjectID`, `USER_SLUG = Spec.UserSlug`, `PROJECT_SLUG = Spec.ProjectSlug`, `HOST_SLUG = tpl.hostSlug`" | spec-state-schema: Deployment section: "env vars `USER_ID`, `PROJECT_ID` (UUIDs for FK joins), `USER_SLUG`, `PROJECT_SLUG`, `HOST_SLUG` (slugs for NATS subject and KV key construction per ADR-0024 + ADR-0035)" | REFLECTED | — | All five env vars listed with correct semantics (UUID vs slug) |
| D5 — Session-agent JWT permissions | "`SessionAgentSubjectPermissions(userID, userSlug)` returns: PubAllow = `[mclaude.{UUID}.>, mclaude.users.{uslug}.hosts.*.>, _INBOX.>, $JS.*.API.>]`, SubAllow = same" | spec-state-schema: Per-session-agent JWT permissions: `publish: mclaude.{userID}.>, mclaude.users.{userSlug}.hosts.*.>, _INBOX.>, $JS.*.API.>` and `subscribe:` (same) | REFLECTED | — | Exact permission set matches |
| D5 — IssueSessionAgentJWT signature | "`IssueSessionAgentJWT` gains `userSlug string` param" | spec-state-schema: "Issued by `mclaude-controller-k8s` (`IssueSessionAgentJWT(userID, userSlug, accountKP)`)" | REFLECTED | — | Signature includes `userSlug` param |
| D6 — seedDev publishes provisioning request | "seedDev publishes a NATS request to `mclaude.users.{userSlug}.hosts.local.api.projects.create` with the full `ProvisionRequest` payload. Non-fatal on failure" | spec-control-plane: Startup Sequence step 9: "publishes a NATS provisioning request (`mclaude.users.{uslug}.hosts.local.api.projects.create`) for the default project… Non-fatal on failure" | REFLECTED | — | Subject, non-fatal semantics, and ADR-0050 attribution all present |
| D6 — seedDev `.create` subject in subjects table | control-plane is a publisher of `.create` subject | spec-state-schema: NATS Subjects table: `mclaude.users.{uslug}.hosts.{hslug}.api.projects.create` — Publisher: "SPA, control-plane" | REFLECTED | — | control-plane listed as publisher |
| D7 — seedDev resolves host slug from DB | "`seedDev` queries the `local` host's ID to populate `ProvisionRequest.HostSlug` and `ProjectKVState.HostSlug`" | spec-control-plane: Startup Sequence step 9 references `hosts.local` and the provision request subject with `local`; spec describes creating the local host in the same step | REFLECTED | — | Implicit: seedDev creates the local host, so it has the slug. DB query for ID is implementation detail. |

### Summary

- Reflected: 12
- Gap: 0
- Partial: 1

### Non-clean findings

PARTIAL [SPEC→FIX]: "ProvisionRequest gains UserID and ProjectID string fields" → spec-control-plane Project Creation Flow step 6 lists payload as `{userSlug, hostSlug, projectSlug, gitUrl, gitIdentityId}` but omits `userID` and `projectID`. The NATS Subjects section in the same spec correctly includes all fields. Step 6 should be updated to `{userID, userSlug, hostSlug, projectID, projectSlug, gitUrl, gitIdentityId}` for consistency.
