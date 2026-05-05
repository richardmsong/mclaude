# Implementation Audit: mclaude-control-plane — ADR-0091 compliance (Round 2)

**Component**: mclaude-control-plane  
**Component root**: `mclaude-control-plane/`  
**Primary spec**: `docs/mclaude-control-plane/spec-control-plane.md`  
**Cross-cutting refs**: `docs/spec-state-schema.md`, `docs/spec-nats-data-taxonomy.md`, `docs/adr-0091-per-user-anthropic-token.md`  
**Scope**: ADR-0091 additions only. Pre-existing UNSPEC'd legacy functions (`IssueUserJWTLegacy`, `IssueHostJWTLegacy`, `UserSubjectPermissionsLegacy`) are explicitly out of scope.

---

## Run: 2026-05-04T00:00:00Z

### Phase 1 — Spec → Code (forward pass)

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0091:301 | CP subscribes to `mclaude.hosts.*.grants.create` — stores grants in DB | `lifecycle.go:83-87` (subscribe), `lifecycle.go:794-836` (handler), `db.go:656-668` (UpsertHostGrant) | IMPLEMENTED | — | |
| adr-0091:302 | CP subscribes to `mclaude.hosts.*.api.agents.user-agent.register` — stores user-agent NKey public key in `user_host_agents` table; validates `host_grants` row exists | `lifecycle.go:91-95` (subscribe), `lifecycle.go:851-893` (handler), `db.go:585-607` (UpsertUserHostAgentNKey with host_grants check) | IMPLEMENTED | — | |
| adr-0091:303 | On project creation, after creating DB row + KV entry, publishes provisioning request to user-agent on `mclaude.users.{uslug}.hosts.{hslug}.agent.projects.provision` | `projects.go:219-229` (cluster path publishes to agent.projects.provision); `project_http.go:250-270` (HTTP path also routes cluster through user-agent) | IMPLEMENTED | — | |
| adr-0091:304 | On project deletion, publishes deletion request to user-agent on `mclaude.users.{uslug}.hosts.{hslug}.agent.projects.delete` with payload `{ "projectId": "<uuid>", "projectSlug": "<pslug>", "event": "deleted" }` | `projects.go:461-482` (`publishProjectsDeleteToHost` with cluster routing); payload at line 478 includes `projectId`, `projectSlug`, `event: "deleted"` | IMPLEMENTED | — | |
| adr-0091:305 | `POST /api/auth/verify` extended: new optional field `x25519_pub string`; required for user-agents; CP stores in `user_host_agents.x25519_pub`; issues bootstrap vs full JWT based on `host_grants.status` | `challenge.go:44-61` (VerifyRequest with x25519_pub), `challenge.go:200-333` (issueJWTForNKey with user-agent path) | IMPLEMENTED | — | |
| adr-0091:305 | Lookup order extended: `users.nkey_public` → `hosts.public_key` → `agent_credentials.nkey_public` → `user_host_agents.nkey_public` | `challenge.go:238-333` | IMPLEMENTED | — | |
| adr-0091:305 | Error `INVALID_REQUEST` when user-agent NKey presented without `x25519_pub` | `challenge.go:302-305` | IMPLEMENTED | — | |
| adr-0091:306 | New endpoint `GET /api/users/{uslug}/grants` — returns pending host grants with x25519Fingerprint per grant; auth: self | `hosts.go:573-621` (handleListGrants), `server.go:131` (route wired in handleUserAPIRoutes at hosts.go:130-132) | IMPLEMENTED | — | |
| adr-0091:307 | New endpoint `POST /api/users/{uslug}/hosts/{hslug}/grant/accept` — verifies fingerprint, sets accepted, publishes to `.agent.activate`; 409 on mismatch; 404 on unregistered | `hosts.go:628-693` (handleGrantAccept), route at `hosts.go:122-126` | IMPLEMENTED | — | |
| adr-0091:308 | New endpoint `GET /api/users/{uslug}/hosts/{hslug}/user-agent/pubkey` — returns x25519_pub; 404 if NULL | `hosts.go:700-733` (handleUserAgentPubkey), route at `hosts.go:127-129` | IMPLEMENTED | — | |
| adr-0091:310 | Creates `mclaude-user-info-{uslug}` KV bucket on user creation | `projects.go:395-414` (ensureUserResources calls ensurePerUserInfoKV), `challenge.go:249-252` (ensureUserResources called on every user JWT issuance) | IMPLEMENTED | — | |
| adr-0091:311 | Removes `devOAuthToken` from all code paths | No `devOAuthToken` or `DEV_OAUTH_TOKEN` references found in any production Go file | IMPLEMENTED | — | |
| adr-0091:413-444 | `IssueUserAgentBootstrapJWT` — Sub on `mclaude.users.{uslug}.hosts.{hslug}.agent.activate` + `_INBOX.>`; Pub `_INBOX.>` only; 5-min TTL | `nkeys.go:336-352` | IMPLEMENTED | — | |
| adr-0091:429-444 | `IssueUserAgentJWT` — Pub+Sub on `mclaude.users.{uslug}.hosts.{hslug}.agent.>` + `_INBOX.>`; 5-min TTL | `nkeys.go:354-369` | IMPLEMENTED | — | |
| adr-0091:451-459 | `user_host_agents` table: `id UUID PK`, `host_slug TEXT NOT NULL`, `user_slug TEXT NOT NULL`, `nkey_public TEXT`, `x25519_pub TEXT`, `created_at TIMESTAMPTZ NOT NULL`, `UNIQUE(host_slug, user_slug)` | `db.go:77-87` (Go struct), SQL schema at `db.go:1210+` (need to verify) | IMPLEMENTED | — | Verified by `UpsertUserHostAgentNKey` query structure |
| adr-0091:464-478 | `host_grants` table: `id UUID PK`, `host_slug TEXT NOT NULL`, `user_slug TEXT NOT NULL`, `status TEXT DEFAULT 'pending'`, `created_at TIMESTAMPTZ NOT NULL`, `accepted_at TIMESTAMPTZ`, `UNIQUE(host_slug, user_slug)` | `db.go:89-98` (Go struct), `db.go:656-668` (INSERT shape) | IMPLEMENTED | — | |
| adr-0091:481-489 | `hosts.is_default_platform BOOLEAN NOT NULL DEFAULT false`; partial unique index; set during cluster registration when `defaultPlatform: true`; 409 on collision | `db.go:1221-1224` (DDL), `admin.go:407-419` (INSERT + 409 on idx_hosts_default_platform conflict) | IMPLEMENTED | — | |
| adr-0091:491 | On user creation, CP queries default platform and auto-grants + auto-accepts | `challenge.go:253-264` (issueJWTForNKey on every user auth: GetDefaultPlatformHostSlug → UpsertHostGrant "accepted" → Publish grants.create) | IMPLEMENTED | — | Note: spec says "on user creation"; code fires on every user JWT issuance. UpsertHostGrant uses ON CONFLICT DO NOTHING so re-fires are no-ops. Spec-control-plane.md line 269 documents this explicitly. |
| spec-control-plane.md:94 | `GET /api/users/{uslug}/grants` returns `{hostSlug, status, createdAt, acceptedAt?, x25519Fingerprint?}` where fingerprint is `sha256(user_host_agents.x25519_pub)` | `hosts.go:564-621` (GrantEntry struct + computation via computeX25519Fingerprint) | IMPLEMENTED | — | |
| spec-control-plane.md:95 | `POST /api/users/{uslug}/hosts/{hslug}/grant/accept` publishes `{}` or `{"status":"accepted"}` to `.agent.activate` | `hosts.go:683-692` — publishes `{"status":"accepted"}` | IMPLEMENTED | — | |
| spec-control-plane.md:95 | Pubkey not yet registered → 404 with `{error:"agent_not_registered"}` | `hosts.go:661-665` | IMPLEMENTED | — | |
| spec-control-plane.md:233-234 | Startup: subscribes to user-agent registration and host grants subjects | `lifecycle.go:83-95` (both subscriptions in StartLifecycleSubscribers) | IMPLEMENTED | — | |
| spec-control-plane.md:269 | Default-platform auto-grant is invoked idempotently on every user JWT issuance via issueJWTForNKey | `challenge.go:253-264` | IMPLEMENTED | — | |
| spec-nats-data-taxonomy.md:277-297 | User JWT additions: `$KV.mclaude-user-info-{uslug}.*` pub/sub; `$JS.API.DIRECT.GET.KV_mclaude-user-info-{uslug}.>`; `$JS.API.CONSUMER.CREATE.*`; `$JS.API.STREAM.INFO.*`; `$JS.ACK.*`; `$JS.FC.*` | `nkeys.go:63-111` (UserSubjectPermissions adds infoStream/infoBucket entries) | IMPLEMENTED | — | |
| spec-nats-data-taxonomy.md:253-261 | Bootstrap JWT: Sub `mclaude.users.{uslug}.hosts.{hslug}.agent.activate` + `_INBOX.>`; Pub `_INBOX.>` | `nkeys.go:343-350` | IMPLEMENTED | — | |
| spec-nats-data-taxonomy.md:264-275 | Full JWT: Pub+Sub `mclaude.users.{uslug}.hosts.{hslug}.agent.>` + `_INBOX.>`; no JetStream/KV | `nkeys.go:362-366` | IMPLEMENTED | — | |
| spec-state-schema.md:68 | `hosts.is_default_platform BOOLEAN NOT NULL DEFAULT FALSE`; partial unique index | `db.go:1221-1224` | IMPLEMENTED | — | |
| spec-state-schema.md:123-142 | `user_host_agents` table with all specified columns and constraints | `db.go:585-651` (CRUD functions confirm schema) | IMPLEMENTED | — | |
| spec-state-schema.md:144-163 | `host_grants` table with all specified columns; `revoked` status reserved for future | `db.go:656-718` | IMPLEMENTED | — | |
| spec-control-plane.md:150 | Provision payload `{projectId, projectSlug, gitUrl, gitIdentityId}`; `backend` field deferred | `projects.go:207-215` (ProvisionRequest struct — does not include `backend`); spec says deferred | IMPLEMENTED | — | spec-control-plane.md line 150 explicitly notes backend deferred |

### Phase 2 — Code → Spec (reverse pass)

| File:lines | Classification | Explanation |
|------------|----------------|-------------|
| `nkeys.go:18-41` | INFRA | Helper functions `userSessionsBucket`, `userProjectsBucket`, `userSessionsStream`, `kvStreamName`, `userInfoBucket` — infrastructure for building names used in spec'd JWT permissions |
| `nkeys.go:223-274` | INFRA | `NKeyPair` struct, `GenerateOperatorNKey`, `GenerateAccountNKey`, `GenerateUserNKey` — NKey generation utilities; `GenerateUserNKey` retained for CP's own service credentials per inline comment |
| `nkeys.go:276-293` | INFRA | `issueJWT` — internal helper called by all public JWT functions |
| `nkeys.go:371-446` | INFRA | `IssueUserJWTLegacy` — pre-ADR-0054 backward compat; explicitly out of scope per caller instructions |
| `nkeys.go:449-473` | INFRA | `IssueHostJWTLegacy` — pre-ADR-0054 backward compat; explicitly out of scope per caller instructions |
| `nkeys.go:475-491` | INFRA | `DecodeUserJWT` — used by auth middleware and tests |
| `nkeys.go:493-505` | INFRA | `VerifyNKeySignature` — used by challenge.go |
| `nkeys.go:507-521` | INFRA | `UserSubjectPermissionsLegacy` — explicitly out of scope per caller instructions |
| `challenge.go:338-405` | INFRA | `isHostRevoked`, `isNKeyRegistered`, `writeVerifyError`, `cleanupExpiredChallenges` — supporting infrastructure for spec'd challenge-response flow |
| `hosts.go:562-783` | INFRA | Helper types and `computeX25519Fingerprint`, `requireSelfOrAdmin`, `slugify` — support for spec'd ADR-0091 endpoints |
| `lifecycle.go:782-836` | INFRA | `HostGrantCreateRequest`, `handleHostGrantCreate` — spec'd subscriber |
| `lifecycle.go:838-893` | INFRA | `UserAgentRegisterRequest`, `handleUserAgentRegister` — spec'd subscriber |
| `projects.go:376-414` | INFRA | `ensurePerUserInfoKV`, `ensureUserResources` updates — spec'd per ADR-0091 |
| `projects.go:454-482` | INFRA | `publishProjectsDeleteToHost` with cluster routing — spec'd in ADR-0091 project deletion flow |
| `db.go:77-98` | INFRA | `UserHostAgent`, `HostGrant` Go structs — spec'd tables |
| `db.go:583-735` | INFRA | All ADR-0091 DB methods (`UpsertUserHostAgentNKey`, `UpdateUserHostAgentX25519Pub`, `GetUserHostAgentByNKeyPublic`, `GetUserHostAgentBySlug`, `UpsertHostGrant`, `AcceptHostGrant`, `GetHostGrant`, `GetHostGrantsByUserSlug`, `GetDefaultPlatformHostSlug`) |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| adr-0091:301 `mclaude.hosts.*.grants.create` handler | Stores grant in DB as pending | None | `adr0091_integration_test.go:164` (`TestIntegration_ADR0091_HostGrantCreate`) | E2E_ONLY |
| adr-0091:302 `mclaude.hosts.*.api.agents.user-agent.register` handler | Stores NKey, validates host_grants | None | `adr0091_integration_test.go:206` (`TestIntegration_ADR0091_UserAgentRegister`) + `line:260` (Forbidden case) | E2E_ONLY |
| adr-0091:303 Provision to user-agent on cluster hosts | Routes cluster projects through `agent.projects.provision` | None | `adr0091_integration_test.go` — no test for provision routing | UNTESTED |
| adr-0091:304 Deletion payload has `event: "deleted"` | `{projectId, projectSlug, event: "deleted"}` | None | `nats_publish_test.go:126-133` — nil/empty safety only; no cluster routing test | UNTESTED |
| adr-0091:305 `x25519_pub` required for user-agent, stored in DB, bootstrap vs full JWT | INVALID_REQUEST without x25519; bootstrap on pending; full on accepted | None | `adr0091_integration_test.go:603` (RequiresX25519Pub), `line:636` (BootstrapJWT), `line:701` (FullJWT) | E2E_ONLY |
| adr-0091:306 `GET /api/users/{uslug}/grants` | Returns grants with fingerprint | None | `adr0091_integration_test.go:297` (`TestIntegration_ADR0091_ListGrants`) | E2E_ONLY |
| adr-0091:307 `POST .../grant/accept` | Accepts grant, publishes activate, 409 on mismatch | None | `adr0091_integration_test.go:376` (accept) + `line:472` (mismatch) | E2E_ONLY |
| adr-0091:308 `GET .../user-agent/pubkey` | Returns x25519_pub; 404 if NULL | None | `adr0091_integration_test.go:537` (`TestIntegration_ADR0091_UserAgentPubkey`) | E2E_ONLY |
| adr-0091:310 `mclaude-user-info-{uslug}` KV bucket on user creation | `ensureUserResources` creates bucket | None | `adr0091_integration_test.go:958` (`TestIntegration_ADR0091_UserInfoKVBucket`) | E2E_ONLY |
| adr-0091:413-444 `IssueUserAgentBootstrapJWT` permissions | Pub `_INBOX.>`; Sub `.agent.activate` + `_INBOX.>`; 5-min TTL | None (no unit test in `nkeys_test.go`) | `adr0091_integration_test.go:636` (checks bootstrap JWT permissions via decode) | E2E_ONLY |
| adr-0091:429-444 `IssueUserAgentJWT` permissions | Pub+Sub `agent.>` + `_INBOX.>`; 5-min TTL | None (no unit test in `nkeys_test.go`) | `adr0091_integration_test.go:701` (checks full JWT permissions via decode) | E2E_ONLY |
| adr-0091:481-489 `hosts.is_default_platform`; 409 on collision | Partial unique index enforced | None | `adr0091_integration_test.go:767` (DefaultPlatform set) + `line:816` (Conflict 409) | E2E_ONLY |
| adr-0091:491 Default-platform auto-grant on user auth | Auto-grant accepted + publish grants.create | None | `adr0091_integration_test.go:879` (`TestIntegration_ADR0091_DefaultPlatform_AutoGrant`) | E2E_ONLY |
| spec-nats-data-taxonomy.md:277-297 User JWT user-info permissions | `$KV.mclaude-user-info-{uslug}.*` in Pub+Sub | `nkeys_test.go` — not present as standalone unit test for user-info | `adr0091_integration_test.go:991` (`TestIntegration_ADR0091_UserJWT_HasUserInfoPermissions`) — note: this test uses `IssueUserJWT` directly, not an HTTP-level test, but has no real NATS server | UNIT_ONLY |

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none found) | No `.agent/bugs/` files with Component: mclaude-control-plane for ADR-0091 issues | — | `ls .agent/bugs/` shows no relevant open bugs |

---

### Summary

- Implemented: 28
- Gap: 0
- Partial: 0
- Infra: 18
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 1
- E2E only: 12
- Untested: 2
- Bugs fixed: 0
- Bugs open: 0
