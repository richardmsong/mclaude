## Audit: 2026-05-04T22:00:00Z

**Document:** docs/adr-0091-per-user-anthropic-token.md

### Round 1

**Gaps found: 9**

1. **Namespace name conflicts with existing code and state schema** — ADR uses `mclaude-user-{uslug}` but code uses `mclaude-{uslug}`.
2. **Session creation subject changes without specifying what happens to existing JetStream consumer** — ADR conflates project provisioning (pod creation) with session lifecycle (CLI processes within pod).
3. **User-agent pubkey registration authentication is unresolved — TBD** — No auth mechanism for pre-NATS pubkey registration.
4. **X25519 pubkey storage schema is explicitly deferred as TBD** — No definitive table or column.
5. **Session-agent JWT issuance path after migration is an open question** — Unclear if user-agent takes over or host controller path remains.
6. **Revocation flow deferred but v1 integration test depends on it** — "Revoke clears token" test has no backing design.
7. **NATS JetStream at-rest encryption asserted but not configured** — No `store_key` in NATS server config.
8. **User JWT NATS permissions for `mclaude-user-info-{uslug}` underspecified** — "Same pattern" not implementable without exact subject list.
9. **`is_default_platform` set path underspecified** — No field in request struct, no migration SQL, no uniqueness constraint.

#### Fixes applied

| # | Gap | Cause | Resolution | Type |
|---|-----|-------|-----------|------|
| 1 | Namespace naming | ADR introduced `mclaude-user-{uslug}` but codebase uses `mclaude-{uslug}` | Fixed all occurrences to `mclaude-{uslug}` | factual |
| 2 | Session vs project | ADR said `.agent.sessions.create` but project provisioning (pod creation) and session lifecycle (CLI processes) are distinct concerns | Renamed to `.agent.projects.create`. Added "Project Provisioning Flow" section with explicit split: user-agent = infrastructure (pods, secrets), session-agent = application (git, CLI, sessions). Session-agent subjects/JetStream consumers untouched. | decision |
| 3 | User-agent auth | No mechanism for pre-NATS authentication | User-agent uses NKey pair (same as hosts/session-agents). Host controller pre-registers NKey via `mclaude.hosts.{hslug}.agents.user-agent.register`. User-agent authenticates via existing challenge-response flow. CoCo upgrade adds TEE attestation. | decision |
| 4 | Pubkey storage | Two options listed, no decision | User chose `user_host_agents` table. Added full schema with `nkey_public` (set by host controller) and `x25519_pub` (set by user-agent). | decision |
| 5 | Session-agent JWT | Unclear if user-agent takes over | CP already handles session-agent registration directly (`mclaude.hosts.*.api.agents.register`). Host controller was never in this path. No change needed. Added explicit note in Project Provisioning Flow and failure modes. | factual |
| 6 | Revocation test | Test listed as v1 but revocation flow undesigned | Simplified test to "Revoke clears client token" (KV deletion only). Host-side cleanup deferred with revocation flow. Updated CLI --revoke description. | factual |
| 7 | At-rest encryption | Claim without implementation | Changed from assertion to requirement. Added `store_key` stanza to Helm chart changes. Reworded security section. | factual |
| 8 | User JWT permissions | "Same pattern" not implementable | Enumerated full JetStream API subject list for `KV_mclaude-user-info-{uslug}` stream (DIRECT.GET, CONSUMER.CREATE, STREAM.INFO, CONSUMER.INFO, ACK, FC). | factual |
| 9 | `is_default_platform` | Missing field, migration, constraint | Added partial unique index, `AdminClusterRegisterRequest` field, user creation auto-invite logic. | factual |

## Run: 2026-05-04T23:30:00Z

**Gaps found: 7**

1. **`user-agent-key` Secret schema omits the NKey private seed** — The Secret schema at the "Data Model" section documents only one key (`x25519-private`), but the text in Phase 1 step 3, the Key Establishment Flow section (steps 1–2), and the Component Changes section all state the NKey Ed25519 private seed is also written to this Secret. A developer implementing the user-agent would not know the Secret key name for the NKey seed, which private-key material is stored there, or the encoding format (the X25519 key is specified as `base64(32 bytes)` but there is no equivalent spec for the NKey seed format).
   - **Doc**: "Read NKey private key + X25519 private key from k8s Secret `user-agent-key`" (Key Establishment Flow, step 1); Secret schema table shows only `x25519-private` (Data Model §user-agent-key).
   - **Code**: No existing user-agent code — new component. The omission would force the implementer to invent the Secret key name.

2. **`host_invites.x25519_pub` is redundant with `user_host_agents.x25519_pub` and the relationship between the two tables is unspecified** — The `host_invites` table has an `x25519_pub` column described as "user-agent's pubkey, set on registration." The `user_host_agents` table has the same field. The ADR does not specify which table is authoritative for the pubkey, when `host_invites.x25519_pub` is set (vs `user_host_agents.x25519_pub`), or whether the two must stay in sync. The `GET /api/users/{uslug}/hosts/{hslug}/user-agent/pubkey` endpoint says it reads from `user_host_agents`, making `host_invites.x25519_pub` appear unused. An implementer cannot resolve this without asking.
   - **Doc**: `host_invites` schema (Data Model §host_invites); `user_host_agents` schema (Data Model §user_host_agents); endpoint description "returns user-agent's X25519 pubkey from `user_host_agents`" (mclaude-control-plane component changes).

3. **Bootstrap JWT scoping for user-agent is unspecified** — The ADR states the user-agent receives a "bootstrap JWT (scoped to `.agent.activate` subscribe only)" but does not specify the full `Sub.Allow` subject for this JWT. The actual subscribe subject is `mclaude.users.{uslug}.hosts.{hslug}.agent.activate` but the dot-prefix notation used in the ADR (`.agent.activate`) is ambiguous — is it a partial subject with the full prefix omitted, and if so what is the full subject? The `IssueUserAgentJWT` function specified in the ADR issues the full JWT (`mclaude.users.{uslug}.hosts.{hslug}.agent.>` + `_INBOX.>`), but no equivalent `IssueUserAgentBootstrapJWT` function is specified with its permission list.
   - **Doc**: "CP issues bootstrap JWT (scoped to `.agent.activate` subscribe only)" (Phase 1, step 5); "Subscribes to `.agent.activate`" (Key Establishment, step 4); NATS Permission Model section defines only `IssueUserAgentJWT` (full JWT) with no bootstrap variant.
   - **Code**: Existing `issueJWT` in `mclaude-control-plane/nkeys.go:258` can issue scoped JWTs, but no `IssueUserAgentBootstrapJWT` function or corresponding `UserAgentBootstrapSubjectPermissions` exists.

4. **`POST /api/auth/verify` extension for X25519 pubkey registration is unspecified** — The ADR states "X25519 pubkey registered at this step" during challenge-response authentication, but the existing `VerifyRequest` struct (`mclaude-control-plane/challenge.go:46`) has no `x25519_pub` field. The ADR does not specify: (a) how the X25519 pubkey is conveyed in the verify request — as a new field in `VerifyRequest`, a separate header, or a second call; (b) whether the field is required or optional; (c) what happens if a user-agent authenticates without providing an X25519 pubkey (invite pending, no token delivery possible). An implementer cannot extend `handleAuthVerify` without these answers.
   - **Doc**: "Also registers X25519 pubkey at this step" (Phase 1, step 5; Key Establishment, step 3); Component Changes: "X25519 pubkey registered during this step."
   - **Code**: `VerifyRequest` at `mclaude-control-plane/challenge.go:46` has no `x25519_pub` field; `issueJWTForNKey` at `challenge.go:222` has no user-agent lookup path (only users, hosts, session-agents).

5. **`mclaude.hosts.{hslug}.invites.create` subject is described inconsistently and its NATS permission source is missing** — The ADR defines this as the subject host controllers publish invites on, but: (a) the spec-nats-data-taxonomy.md Resource Hierarchy and Subject Namespace sections do not include this subject; (b) the host controller JWT (`HostSubjectPermissions` in `mclaude-control-plane/nkeys.go:190`) only allows `mclaude.hosts.{hslug}.>` pub/sub, so the host controller already has permission to publish here — but (c) the ADR says "CP subscribes to `mclaude.hosts.*.invites.create`" which means CP must add this subscription, which is not reflected in any existing CP subscription list in `spec-control-plane.md`. A developer implementing the CP invite subscriber cannot tell whether to add a new subscription, update a wildcard, or derive it from existing `mclaude.hosts.{hslug}.>` coverage.
   - **Doc**: "Publishes invites on `mclaude.hosts.{hslug}.invites.create`" (Phase 1); "CP subscribes to `mclaude.hosts.*.invites.create`" (Component Changes §mclaude-control-plane); taxonomy doc has no `invites.create` subject.
   - **Code**: `spec-nats-data-taxonomy.md` resource hierarchy at line 55–58 lists only `users.*.projects.*.create`, `users.*.projects.*.delete`, `api.agents.register` under `hosts.{hslug}`.

6. **`AdminClusterRegisterRequest` in the codebase has no `defaultPlatform` field** — The ADR specifies `is_default_platform` is "set during cluster registration (`POST /admin/clusters`) when registering admin passes `defaultPlatform: true` in `AdminClusterRegisterRequest`", but the existing struct at `mclaude-control-plane/admin.go:57` has no such field. The ADR says to check the unique index and return 409, but the migration SQL uses `IF NOT EXISTS` (no error for re-runs). An implementer must add the field and the conflict-check logic, but the ADR does not specify the field name in the JSON payload (`defaultPlatform`, `isDefaultPlatform`, or `is_default_platform`).
   - **Doc**: "Set during cluster registration (`POST /admin/clusters`) when registering admin passes `defaultPlatform: true` in `AdminClusterRegisterRequest`" (Data Model §hosts table).
   - **Code**: `AdminClusterRegisterRequest` at `mclaude-control-plane/admin.go:57`; no `DefaultPlatform` or equivalent field exists.

7. **`spec-state-schema.md` is not updated with the new tables and resources despite the Impact section promising it** — The Impact section lists `docs/spec-state-schema.md` as requiring updates. The state schema is the canonical reference. Until updated, implementers will build against the old model.
   - **Doc**: Impact section: "docs/spec-state-schema.md: add user-agent pubkey storage, mclaude-user-info-{uslug} KV bucket, user-tokens-{uslug} k8s Secret."
   - **Code**: `docs/spec-state-schema.md` — no new tables, no new KV bucket, `user-secrets` still shows `oauth-token`.

#### Fixes applied

| # | Gap | Cause | Resolution | Type |
|---|-----|-------|-----------|------|
| 1 | Secret schema missing NKey seed | Only X25519 key was in the table; NKey was added to text but not schema | Added `nkey-seed` key to `user-agent-key` Secret table with NATS `S...` encoding format | factual |
| 2 | Redundant `x25519_pub` columns | `host_invites` copied from earlier draft that predated `user_host_agents` | Removed `x25519_pub` from `host_invites`. Added note: `user_host_agents` is authoritative. | factual |
| 3 | Bootstrap JWT unspecified | Only full JWT was defined in NATS Permission Model | Added `IssueUserAgentBootstrapJWT` function with explicit Sub.Allow: `mclaude.users.{uslug}.hosts.{hslug}.agent.activate` + `_INBOX.>`, Pub: `_INBOX.>` | factual |
| 4 | Verify extension unspecified | X25519 pubkey registration was described narratively, not structurally | Specified `x25519_pub` field in `VerifyRequest` (base64, required for user-agent, ignored otherwise). Added `issueJWTForNKey` user-agent lookup path. | factual |
| 5 | Invite subject missing from taxonomy | New subjects added in ADR but not reflected in Impact's taxonomy update list | Sharpened Impact section to explicitly list `mclaude.hosts.{hslug}.invites.create` and `mclaude.hosts.{hslug}.agents.user-agent.register` subjects for taxonomy update | factual |
| 6 | `defaultPlatform` field name | Go struct field and JSON key not specified | Added `"defaultPlatform": true` (JSON) / `DefaultPlatform bool` (Go) to ADR text | factual |
| 7 | `spec-state-schema.md` stale | ADR lists it as Impact item but schema not updated in-ADR | Sharpened Impact item to enumerate every table, column, bucket, Secret, and removal (`oauth-token`) that must be reflected. Schema update happens during implementation. | factual |

## Run: 2026-05-04T23:59:00Z

**Gaps found: 5**

1. **Bootstrap JWT 5-minute TTL with no renewal path** — The user-agent receives a bootstrap JWT (5-min TTL per the `IssueUserAgentBootstrapJWT` spec, matching `nkeys.go:299` which uses `5 * 60` seconds) and then sits idle watching `.agent.activate` "until invite is accepted." For non-default hosts the user must explicitly accept via SPA. There is no mechanism specified for the user-agent to re-run challenge-response to get a fresh bootstrap JWT if the invite is not accepted within 5 minutes. The user-agent will lose its NATS connection when the JWT expires, cannot reconnect without a new JWT, and will never receive the activation event. A developer implementing the user-agent cannot determine whether to (a) re-run challenge-response on JWT expiry loop, (b) poll CP for JWT renewal, or (c) do nothing and require a pod restart.
   - **Doc**: "5-minute TTL. User-agent watches `.agent.activate` for the full JWT delivery." (NATS Permission Model §IssueUserAgentBootstrapJWT); "Sits idle until invite is accepted." (Phase 1, step 6)
   - **Code**: `mclaude-control-plane/nkeys.go:299` — `const hostTTLSecs = 5 * 60`; all other long-lived watchers (host controller, session-agent) have explicit refresh loops (e.g., `agent_auth.go:agentJWTRefreshBuffer`)

2. **Host controller cannot know user-agent NKey public key before pre-registering it** — Phase 1 step 3 says the user-agent generates its NKey pair on first startup and writes it to `user-agent-key` Secret. Phase 1 step 4 says "Host controller pre-registers user-agent's NKey public key with CP." The host controller deploys the user-agent pod in step 2 — before the keys are generated. The host controller therefore cannot know the NKey until after the pod starts and writes to the Secret. No mechanism is described for the host controller to discover the NKey: no NATS message from user-agent to host controller, no shared Secret read by the controller, no polling path. A developer implementing the host controller cannot determine when or how to read the NKey and publish step 4.
   - **Doc**: "Host controller pre-registers user-agent's NKey public key with CP via `mclaude.hosts.{hslug}.agents.user-agent.register`" (Phase 1, step 4); "User-agent starts → generates NKey pair... → writes both private keys to k8s Secret" (Phase 1, step 3)
   - **Code**: No existing inter-component path for host controller to read a per-user Secret in the user namespace; `mclaude-controller-k8s/reconciler.go` does not read from user namespaces

3. **Project creation bypasses CP but session-agent requires `PROJECT_ID` UUID from Postgres** — The ADR routes project creation directly from SPA to user-agent via NATS (`mclaude.users.{uslug}.hosts.{hslug}.agent.projects.create`), explicitly stating "CP is not in the project provisioning path." However, the session-agent binary requires `--project-id` (a UUID, line 82 of `mclaude-session-agent/main.go`) and the existing `agent_credentials` table schema requires `project_slug` (not UUID) for CP's challenge-response lookup. The `projects` table Postgres row (with UUID) is currently written by CP on project creation. If user-agent creates the pod without CP involvement, either (a) user-agent must generate the UUID and create the Postgres row itself (requiring DB access, which is not described), or (b) CP must still create the Postgres row via some path (not described), or (c) the session-agent must be changed to not need a UUID (not described). The ADR specifies `PROJECT_SLUG` as a pod env var but not `PROJECT_ID`, yet the session-agent binary requires both (`main.go:93` also reads `PROJECT_SLUG`).
   - **Doc**: "SPA sends project creation request directly to user-agent via NATS"; pod env vars listed: `PROJECT_SLUG, GIT_URL, HOST_SLUG, USER_SLUG, GIT_IDENTITY_ID` — no `PROJECT_ID` (Project Provisioning Flow §Project creation); "CP is not in the project provisioning path."
   - **Code**: `mclaude-session-agent/main.go:82` — `--project-id` required in standalone mode; `spec-state-schema.md:377` — current project creation flow goes through CP HTTP → Postgres → NATS fan-out

4. **Session-agent `entrypoint.sh` `--nats-creds` flag is unresolvable in user-agent-created pods** — The ADR specifies a one-line change to `entrypoint.sh` (token read from `/secrets/tokens/${TOKEN_PROVIDER}`) but does not address that the existing `entrypoint.sh:73` passes `--nats-creds /home/node/.user-secrets/nats-creds` to the session-agent binary. In pods created by the k8s controller, `user-secrets` Secret is mounted at `/home/node/.user-secrets` (containing `nats-creds` written by the controller). In pods created by the user-agent, the user-agent mounts only `user-tokens-{uslug}` at `/secrets/tokens/` — `user-secrets` is not described as being mounted. The session-agent will fail to start because `--nats-creds` points to a nonexistent file. The ADR also does not specify `NATS_URL`, `CONTROL_PLANE_URL`, `USER_ID`, or `USER_SLUG` env var injection for user-agent-created pods (the current controller sets these; see `spec-state-schema.md:550`). An implementer cannot determine what mounts and env vars user-agent-created pods must carry.
   - **Doc**: "entrypoint.sh: reads token from mounted Secret: `export CLAUDE_CODE_OAUTH_TOKEN=$(cat /secrets/tokens/${TOKEN_PROVIDER})`" (Component Changes §mclaude-session-agent); Pod spec includes `user-tokens-{uslug}` Secret mount, `TOKEN_PROVIDER`, `PROJECT_SLUG`, `GIT_URL`, `HOST_SLUG`, `USER_SLUG`, `GIT_IDENTITY_ID` — no `NATS_URL`, `CONTROL_PLANE_URL`, `USER_ID`, no `nats-creds` mount (Project Provisioning Flow §Project creation, step 3)
   - **Code**: `mclaude-session-agent/entrypoint.sh:72-77` — `--nats-url "${NATS_URL}"`, `--nats-creds "/home/node/.user-secrets/nats-creds"`, `--user-id "${USER_ID}"`, `--project-id "${PROJECT_ID}"`, `--data-dir /data`; `spec-state-schema.md:550` — current controller injects `USER_ID`, `PROJECT_ID`, `USER_SLUG`, `PROJECT_SLUG`, `HOST_SLUG`, `NATS_URL`, `CONTROL_PLANE_URL`

5. **`mclaude.hosts.{hslug}.agents.user-agent.register` subject drops `api.` prefix** — Inconsistent with existing `api.agents.register`.

#### Fixes applied

| # | Gap | Cause | Resolution | Type |
|---|-----|-------|-----------|------|
| 1 | Bootstrap JWT TTL expiry | No renewal specified for 5-min JWT while waiting for accept | Added re-run challenge-response before TTL expiry (same pattern as session-agent `agentJWTRefreshBuffer`). Specified in both user-agent startup flow and component section. | factual |
| 2 | NKey generation ordering | User-agent generates NKey inside pod, but host controller must register it before deploy | Reversed: host controller generates NKey pair, writes seed to Secret, registers pubkey with CP — all before deploying user-agent pod. User-agent reads seed from Secret on startup. | factual |
| 3 | Project creation needs DB row | ADR said "CP not in path" but session-agent needs PROJECT_ID from Postgres | Split: CP creates project (DB row + KV, existing handler), then publishes provisioning request to user-agent on `.agent.projects.provision`. CP = data, user-agent = infrastructure. Full env var list with PROJECT_ID. | factual |
| 4 | Missing env vars + nats-creds | Pod spec only listed 5 env vars; session-agent needs ~10 + nats-creds mount | Enumerated full env var list: TOKEN_PROVIDER, PROJECT_ID, PROJECT_SLUG, GIT_URL, HOST_SLUG, USER_SLUG, USER_ID, GIT_IDENTITY_ID, NATS_URL, CONTROL_PLANE_URL. Session-agent bootstraps its own NATS JWT via challenge-response (existing flow). | factual |
| 5 | Subject `api.` prefix dropped | New subject diverged from existing hierarchy | Aligned to `mclaude.hosts.{hslug}.api.agents.user-agent.register`. Consistent with existing `api.agents.register`. | factual |
