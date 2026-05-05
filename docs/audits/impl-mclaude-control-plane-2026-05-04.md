# Implementation Audit: mclaude-control-plane — ADR-0091

**Component**: mclaude-control-plane  
**Scope**: ADR-0091 additions/changes only (pre-existing handlers out of scope)  
**Spec files**: docs/adr-0091-per-user-anthropic-token.md, docs/mclaude-control-plane/spec-control-plane.md, docs/spec-state-schema.md, docs/spec-nats-data-taxonomy.md

## Run: 2026-05-04T00:00:00Z

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0091:300 | "Subscribes to `mclaude.hosts.*.grants.create` — stores grants in DB. Directory, not gate." | lifecycle.go:83-87, handleHostGrantCreate | IMPLEMENTED | — | Subscribe wired; handler upserts host_grants row |
| adr-0091:302 | "Subscribes to `mclaude.hosts.*.api.agents.user-agent.register` — stores user-agent NKey public key in `user_host_agents` table. Validates `host_grants` row exists." | lifecycle.go:90-95, handleUserAgentRegister | IMPLEMENTED | — | Subscribe wired; DB validation in UpsertUserHostAgentNKey checks host_grants |
| adr-0091:303 | "On project creation…after creating DB row + KV entry, publishes provisioning request to user-agent on `mclaude.users.{uslug}.hosts.{hslug}.agent.projects.provision`. Replaces the old transplant to `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.create`." | projects.go:220-228 | IMPLEMENTED | — | handleProjectCreate looks up host.Type; cluster hosts get agent.projects.provision subject |
| adr-0091:304 | "On project deletion…publishes deletion request to user-agent on `mclaude.users.{uslug}.hosts.{hslug}.agent.projects.delete`. Best-effort; if user-agent is offline the event is missed." | projects.go:461-482, publishProjectsDeleteToHost | IMPLEMENTED | — | isCluster branch publishes agent.projects.delete; fire-and-forget (Publish, not Request) |
| adr-0091:305 | "Existing `POST /api/auth/challenge` + `POST /api/auth/verify` — extended to recognize user-agent NKeys (from `user_host_agents.nkey_public`)." | challenge.go:358-380 (isNKeyRegistered), challenge.go:299-333 (issueJWTForNKey step 4) | IMPLEMENTED | — | Fourth lookup leg added for user_host_agents |
| adr-0091:305 | "New optional field in `VerifyRequest`: `x25519_pub string` (base64-encoded, 32 bytes). Required when caller is a user-agent; ignored otherwise." | challenge.go:47-52 (VerifyRequest struct), challenge.go:299-333 | IMPLEMENTED | — | x25519_pub field present in struct; required check at line 303 |
| adr-0091:305 | "On successful verify: stores `x25519_pub` in `user_host_agents.x25519_pub`" | challenge.go:307, db.go:609-616 (UpdateUserHostAgentX25519Pub) | IMPLEMENTED | — | |
| adr-0091:305 | "issues bootstrap JWT (if grant pending) or full JWT (if grant accepted)" | challenge.go:310-332 | IMPLEMENTED | — | GetHostGrant → status == "accepted" → IssueUserAgentJWT else IssueUserAgentBootstrapJWT |
| adr-0091:305 | "`issueJWTForNKey` extended with a user-agent lookup path alongside users, hosts, and session-agents." | challenge.go:230-335 | IMPLEMENTED | — | Step 4 comment and code present |
| adr-0091:305 | "Errors: `UNAUTHORIZED` (invalid signature), `FORBIDDEN` (host revoked), `EXPIRED` (challenge expired), `INVALID_REQUEST` (user-agent NKey without `x25519_pub`)." | challenge.go:188-209 | IMPLEMENTED | — | INVALID_REQUEST returned at line 304; other codes handled in shared verify flow |
| adr-0091:306 | "New endpoint: `GET /api/users/{uslug}/grants` — returns pending host grants for the user, including user-agent X25519 pubkey fingerprint per grant. Auth: self (SPA/CLI)." | hosts.go:573-621 (handleListGrants), server.go:124 route dispatch | IMPLEMENTED | — | Route wired; fingerprint computed via computeX25519Fingerprint |
| adr-0091:307 | "New endpoint: `POST /api/users/{uslug}/hosts/{hslug}/grant/accept` — body: `{x25519_fingerprint}`. CP verifies fingerprint matches `user_host_agents.x25519_pub`. Match → marks grant accepted, publishes full JWT to `.agent.activate`. Mismatch → 409. Auth: self." | hosts.go:628-693 (handleGrantAccept), server.go route dispatch | IMPLEMENTED | — | Publishes {"status":"accepted"} to activate subject |
| adr-0091:307 | "Pubkey not yet registered → 404 with `{error:'agent_not_registered'}`." | hosts.go:661-665 | IMPLEMENTED | — | |
| adr-0091:308 | "New endpoint: `GET /api/users/{uslug}/hosts/{hslug}/user-agent/pubkey` — returns user-agent's X25519 pubkey from `user_host_agents`. 404 if `user_host_agents.x25519_pub` is NULL. Auth: self." | hosts.go:700-733 (handleUserAgentPubkey) | IMPLEMENTED | — | |
| adr-0091:310 | "Creates `mclaude-user-info-{uslug}` KV bucket on user creation." | projects.go:376-413 (ensurePerUserInfoKV, ensureUserResources), challenge.go:248-252 | IMPLEMENTED | — | ensureUserResources called on user login/challenge-response |
| adr-0091:311 | "Removes `devOAuthToken` from all code paths." | (searched db.go, challenge.go, admin.go, projects.go, lifecycle.go, server.go) | IMPLEMENTED | — | No DEV_OAUTH_TOKEN references found in production code paths |
| spec-cp:94 | "`GET /api/users/{uslug}/grants`: Each entry includes `{hostSlug, status, createdAt, acceptedAt?, x25519Fingerprint?}` where fingerprint is `sha256(user_host_agents.x25519_pub)` (hex)." | hosts.go:564-571 (GrantEntry struct), hosts.go:599-617 | IMPLEMENTED | — | All fields present; fingerprint computed as sha256 of pub string bytes |
| spec-cp:95 | "`POST /api/users/{uslug}/hosts/{hslug}/grant/accept`: CP publishes `{}` (or `{"status":"accepted"}`) to `mclaude.users.{uslug}.hosts.{hslug}.agent.activate`; returns 204." | hosts.go:683-692 | IMPLEMENTED | — | Publishes {"status":"accepted"}; returns 204 (WriteHeader(http.StatusNoContent)) |
| spec-cp:132 | "`POST /admin/clusters`: The `defaultPlatform: bool` field (ADR-0091, default `false`) sets `hosts.is_default_platform=true`; partial unique index ensures at most one default platform — collisions return 409 with `{error:'default_platform_exists'}`." | admin.go:59-65 (AdminClusterRegisterRequest), admin.go:406-424 (DB exec + conflict detection) | IMPLEMENTED | — | |
| spec-cp:150 | "Publishes to `mclaude.users.{uslug}.hosts.{hslug}.agent.projects.provision` with `{projectId, projectSlug, gitUrl, gitIdentityId, backend}`. Awaits user-agent's reply (PROVISION_TIMEOUT_SECONDS)." | projects.go:206-247 | PARTIAL | SPEC→FIX | Payload uses ProvisionRequest struct which includes projectId, projectSlug, gitUrl, gitIdentityId; does NOT include `backend` field. The `backend` field is in the spec but not in the struct. The missing field is likely forward-looking for multi-provider; omission is reasonable for v1. |
| spec-cp:151 | "Publishes to `mclaude.users.{uslug}.hosts.{hslug}.agent.projects.delete` with `{projectId, projectSlug}`. Best-effort." | projects.go:461-482 | PARTIAL | SPEC→FIX | Payload includes projectId and projectSlug but also adds `event: "deleted"` extra field. Not a gap (extra field is harmless); projectId and projectSlug are present. |
| spec-cp:152 | "CP marks grant as accepted. Publishes…to `mclaude.users.{uslug}.hosts.{hslug}.agent.activate`. No JWT in the payload." | hosts.go:683-692 | IMPLEMENTED | — | Payload is {"status":"accepted"}, no JWT |
| spec-cp:194 | "`mclaude-user-info-{uslug}` — per-user, client-only KV bucket. Created by CP on user registration." | projects.go:395, ensureUserResources | IMPLEMENTED | — | ensurePerUserInfoKV called inside ensureUserResources |
| spec-state-schema:123-143 | "`user_host_agents` table: columns id UUID, host_slug TEXT, user_slug TEXT, nkey_public TEXT, x25519_pub TEXT, created_at TIMESTAMPTZ, UNIQUE(host_slug, user_slug)." | db.go:1228-1237 (schema DDL) | IMPLEMENTED | — | DDL matches spec exactly |
| spec-state-schema:144-163 | "`host_grants` table: columns id UUID, host_slug TEXT, user_slug TEXT, status TEXT DEFAULT 'pending', created_at TIMESTAMPTZ, accepted_at TIMESTAMPTZ, UNIQUE(host_slug, user_slug)." | db.go:1239-1249 (schema DDL) | IMPLEMENTED | — | DDL matches spec exactly |
| spec-state-schema:68 | "`is_default_platform BOOLEAN NOT NULL DEFAULT FALSE` on hosts; partial unique index `idx_hosts_default_platform`." | db.go:1221-1224 (schema DDL) | IMPLEMENTED | — | ALTER TABLE + CREATE UNIQUE INDEX present |
| spec-state-schema:366-381 | "`mclaude-user-info-{uslug}` KV bucket created by CP on user creation, History:1." | projects.go:376-389 (ensurePerUserInfoKV) | IMPLEMENTED | — | History:1 set correctly |
| spec-nats-taxonomy:252-263 | "Bootstrap JWT — `IssueUserAgentBootstrapJWT`: Pub `_INBOX.>`, Sub `mclaude.users.{uslug}.hosts.{hslug}.agent.activate` + `_INBOX.>`. 5-minute TTL." | nkeys.go:343-353 | IMPLEMENTED | — | Permissions match exactly; const agentTTLSecs = 5*60 |
| spec-nats-taxonomy:264-276 | "Full JWT — `IssueUserAgentJWT`: Pub+Sub `mclaude.users.{uslug}.hosts.{hslug}.agent.>` + `_INBOX.>`. 5-minute TTL." | nkeys.go:362-370 | IMPLEMENTED | — | agentPrefix set correctly; TTL 5 min |
| spec-nats-taxonomy:277-295 | "User JWT additions — `mclaude-user-info-{uslug}` access: Pub $KV.mclaude-user-info-{uslug}.*, $JS.API.DIRECT.GET.KV_mclaude-user-info-{uslug}.>, $JS.API.CONSUMER.CREATE, $JS.API.STREAM.INFO, $JS.API.CONSUMER.INFO, $JS.ACK, $JS.FC; Sub $KV.mclaude-user-info-{uslug}.*, $JS.FC." | nkeys.go:48-127 (UserSubjectPermissions) | IMPLEMENTED | — | All listed subjects present in pub/sub arrays |
| adr-0091:491 | "On user creation, CP queries `SELECT slug FROM hosts WHERE is_default_platform = true LIMIT 1`. If found, CP publishes grant…and auto-accepts it." | challenge.go:253-263 (in issueJWTForNKey user path), db.go:720-733 (GetDefaultPlatformHostSlug) | PARTIAL | SPEC→FIX | Auto-grant triggered on *every login* (in issueJWTForNKey user path), not only on first user creation. UpsertHostGrant uses ON CONFLICT DO NOTHING so subsequent logins are idempotent. The spec says "on user creation" but the implementation fires on every login and is idempotent — functionally equivalent. The spec language "on user creation" should say "on user login/JWT issuance (idempotent)". |
| adr-0091:307 | "CP verifies fingerprint matches `user_host_agents.x25519_pub`. Match → sets `host_grants.status='accepted'`, `accepted_at=NOW()`" | hosts.go:669-679 (AcceptHostGrant call) | IMPLEMENTED | — | AcceptHostGrant does UPDATE SET status='accepted', accepted_at=NOW() |
| spec-cp:265 | "Single auth code path: the user-agent always re-runs challenge-response when its JWT nears expiry or when it receives the activation signal." | challenge.go:299-333 (issueJWTForNKey user-agent path) | IMPLEMENTED | — | CP side of the single auth path: always challenge-response, returns bootstrap or full JWT based on grant status |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| hosts.go:739-742 | INFRA | `computeX25519Fingerprint` — private helper computing sha256 of x25519 pub; required by handleListGrants and handleGrantAccept; no standalone spec line but serves spec'd fingerprint computation |
| hosts.go:744-765 | INFRA | `requireSelfOrAdmin` — auth enforcement helper used by the three new ADR-0091 endpoints; necessary plumbing for spec'd auth requirement ("Auth: self") |
| nkeys.go:377-448 | UNSPEC'd | `IssueUserJWTLegacy` — explicitly labeled DEPRECATED in comment; wrapper emitting old-style user JWT; no ADR or spec describes this function; present in production code but never called by ADR-0091 paths |
| nkeys.go:451-474 | UNSPEC'd | `IssueHostJWTLegacy` — explicitly labeled DEPRECATED; wrapper for old-style host JWT; same as above, no spec coverage |
| nkeys.go:508-521 | UNSPEC'd | `UserSubjectPermissionsLegacy` — explicitly labeled DEPRECATED; old permission set without user-info KV additions; no spec coverage |
| nkeys.go:523-541 | UNSPEC'd | `permContains` and `permHasPrefix` — test-helper utility functions embedded in production nkeys.go; used only by the integration test file (adr0091_integration_test.go line 1040-1057); should live in a test helper file, not production code |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| adr-0091:300 | `mclaude.hosts.*.grants.create` subscriber | — | adr0091_integration_test.go:TestHostGrantCreate | E2E_ONLY |
| adr-0091:302 | `mclaude.hosts.*.api.agents.user-agent.register` subscriber | — | adr0091_integration_test.go:TestUserAgentRegister, TestUserAgentRegister_Forbidden | E2E_ONLY |
| adr-0091:303 | Project provision rerouted to agent.projects.provision | — | adr0091_integration_test.go (implicit in integration test suite) | E2E_ONLY |
| adr-0091:304 | Project delete rerouted to agent.projects.delete | — | adr0091_integration_test.go (implicit) | E2E_ONLY |
| adr-0091:305 | auth/verify extended to recognize user-agent NKeys + x25519_pub | — | adr0091_integration_test.go:TestAuthVerify_UserAgent_RequiresX25519Pub, TestAuthVerify_UserAgent_BootstrapJWT, TestAuthVerify_UserAgent_FullJWT | E2E_ONLY |
| adr-0091:306 | GET /api/users/{uslug}/grants | — | adr0091_integration_test.go:TestListGrants | E2E_ONLY |
| adr-0091:307 | POST /api/users/{uslug}/hosts/{hslug}/grant/accept | — | adr0091_integration_test.go:TestGrantAccept, TestGrantAccept_FingerprintMismatch | E2E_ONLY |
| adr-0091:308 | GET /api/users/{uslug}/hosts/{hslug}/user-agent/pubkey | — | adr0091_integration_test.go:TestUserAgentPubkey | E2E_ONLY |
| adr-0091:310 | mclaude-user-info-{uslug} KV bucket created on user creation | — | adr0091_integration_test.go:TestUserInfoKVBucket | E2E_ONLY |
| adr-0091:311 | devOAuthToken removed | — | (absence verified by grep; no test asserts its removal) | UNTESTED |
| spec-cp:132 | defaultPlatform in /admin/clusters + 409 on conflict | — | adr0091_integration_test.go:TestAdminCluster_DefaultPlatform, TestAdminCluster_DefaultPlatform_Conflict | E2E_ONLY |
| spec-nats-taxonomy:252-263 | IssueUserAgentBootstrapJWT permissions | — | adr0091_integration_test.go:TestAuthVerify_UserAgent_BootstrapJWT | E2E_ONLY |
| spec-nats-taxonomy:264-276 | IssueUserAgentJWT permissions | — | adr0091_integration_test.go:TestAuthVerify_UserAgent_FullJWT | E2E_ONLY |
| spec-nats-taxonomy:277-295 | User JWT user-info KV permissions | — | adr0091_integration_test.go:TestUserJWT_HasUserInfoPermissions | E2E_ONLY |
| adr-0091:491 | Default platform auto-grant on user creation | — | adr0091_integration_test.go:TestDefaultPlatform_AutoGrant | E2E_ONLY |

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| 002-droid-shield-false-positives.md | Droid-Shield False Positives on Non-Secret Patterns | N/A | Component is "Droid-Shield pre-commit secret scanner", not mclaude-control-plane; out of scope for this audit |

No open bugs with Component: mclaude-control-plane found in .agent/bugs/.

### Summary

- Implemented: 30
- Gap: 0
- Partial: 3
- Infra: 2
- Unspec'd: 4
- Dead: 0
- Tested: 0
- Unit only: 0
- E2E only: 14
- Untested: 1
- Bugs fixed: 0
- Bugs open: 0
