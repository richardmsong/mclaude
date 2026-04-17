## Run: 2026-04-16T00:00:00Z

Component: mclaude-control-plane
Design docs: docs/plan-github-oauth.md, docs/plan-k8s-integration.md, docs/plan-state-schema.md

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Notes |
|-----------------|-----------|---------------|---------|-------|
| plan-github-oauth.md:54 | Control-plane stores {state, return_url, user_id, provider_id} in memory (10-minute TTL) | providers.go:67-121 | IMPLEMENTED | OAuthStateStore, 10-min TTL, 32-byte crypto/rand hex token |
| plan-github-oauth.md:55 | Returns {redirectUrl} with authorize URL | providers.go:296-297 | IMPLEMENTED | |
| plan-github-oauth.md:59 | Validates state, exchanges code for token | providers.go:318-342 | IMPLEMENTED | |
| plan-github-oauth.md:61 | Writes conn-{id}-token, conn-{id}-username, conn-{id}-refresh-token to user-secrets | providers.go:369-385 | IMPLEMENTED | |
| plan-github-oauth.md:62 | Upserts oauth_connections row UNIQUE(user_id, base_url, provider_user_id) | providers.go:394-413; db.go:196-216 | IMPLEMENTED | |
| plan-github-oauth.md:63 | Calls reconcileUserCLIConfig after connect | providers.go:420-422 | IMPLEMENTED | |
| plan-github-oauth.md:64 | Redirects browser to {externalUrl}{return_url} | providers.go:425 | IMPLEMENTED | |
| plan-github-oauth.md:98 | PAT: tries /api/v3/user for github, then /api/v4/user for gitlab | providers.go:994-1029 | IMPLEMENTED | |
| plan-github-oauth.md:98 | Auth errors (401/403) take priority — sawAuthError flag never cleared by later non-auth error | providers.go:1001-1028 | IMPLEMENTED | sawAuthError accumulates across both attempts |
| plan-github-oauth.md:98 | Returns 400 "invalid token" if any auth error; 400 "could not reach provider" if only connectivity | providers.go:1024-1028 | IMPLEMENTED | |
| plan-github-oauth.md:100 | Rejects 409 on duplicate (same base_url, provider_user_id) for PAT | providers.go:463-469 | IMPLEMENTED | |
| plan-github-oauth.md:101 | Writes conn-{id}-token and conn-{id}-username to Secret on PAT add | providers.go:473-479 | IMPLEMENTED | |
| plan-github-oauth.md:102 | Inserts oauth_connections row with auth_type='pat' | providers.go:488-513 | IMPLEMENTED | |
| plan-github-oauth.md:103 | Calls reconcileUserCLIConfig after PAT add | providers.go:508-511 | IMPLEMENTED | |
| plan-github-oauth.md:105-116 | DELETE /api/connections/{id}: revokes token, removes from Secret, deletes DB row, reconciles CLI | providers.go:524-609 | IMPLEMENTED | |
| plan-github-oauth.md:116 | Projects with git_identity_id pointing to deleted connection: SET NULL via DB cascade | db.go:348 | IMPLEMENTED | ON DELETE SET NULL |
| plan-github-oauth.md:116 | MCProject CRDs for deleted connection cleared | reconciler.go:659-681 | IMPLEMENTED | ClearMCProjectGitIdentityForConnection |
| plan-github-oauth.md:200-207 | All 7 new endpoints wired with correct auth | server.go:15-89 | IMPLEMENTED | |
| plan-github-oauth.md:203 | Returns 400 if {id} is a PAT provider on connect endpoint | providers.go:258-260 | IMPLEMENTED | |
| plan-github-oauth.md:205 | /api/connections/{id}/repos returns 404 {"error":"not_connected"} if not found or wrong user | providers.go:634-646 | IMPLEMENTED | |
| plan-github-oauth.md:206 | POST /api/providers/pat returns 201 with {connectionId, providerType, displayName, username} | providers.go:514-521 | IMPLEMENTED | |
| plan-github-oauth.md:207 | PATCH /api/projects/{id}: updates DB row, MCProject CRD spec, NATS KV ProjectKVState | providers.go:729-755 | IMPLEMENTED | |
| plan-github-oauth.md:207 | PATCH validates: connection belongs to user, hostname matches project GIT_URL | providers.go:712-727 | IMPLEMENTED | |
| plan-github-oauth.md:212 | Provider config from /etc/mclaude/providers.json; empty/missing = normal start | providers.go:131-164 | IMPLEMENTED | |
| plan-github-oauth.md:212 | Client secrets loaded from K8s Secrets (clientSecretRef) at startup | providers.go:149-163 | IMPLEMENTED | |
| plan-github-oauth.md:213-231 | GET /api/connections/{id}/repos response: repos[{name,fullName,private,description,cloneUrl,updatedAt}], nextPage, hasMore | providers.go:1402-1415 | IMPLEMENTED | |
| plan-github-oauth.md:233 | Empty q: sort by pushed/last_activity_at. With q: provider search ranking | providers.go:1438-1440; 1525-1527 | IMPLEMENTED | |
| plan-github-oauth.md:236 | Rate-limit responses from provider passed through | providers.go:660-662 | IMPLEMENTED | |
| plan-github-oauth.md:240-258 | oauth_connections table DDL with all columns and UNIQUE(user_id, base_url, provider_user_id) | db.go:332-346 | IMPLEMENTED | |
| plan-github-oauth.md:274-275 | ALTER TABLE projects ADD COLUMN IF NOT EXISTS git_identity_id TEXT REFERENCES oauth_connections(id) ON DELETE SET NULL | db.go:348 | IMPLEMENTED | |
| plan-github-oauth.md:277 | GitIdentityID field in MCProjectSpec | mcproject_types.go:47 | IMPLEMENTED | |
| plan-github-oauth.md:277 | When GitIdentityID empty, GIT_IDENTITY_ID env var omitted from pod spec | reconciler.go:356-360 | IMPLEMENTED | |
| plan-github-oauth.md:279 | gitIdentityId in projects.create validated (belongs to user, hostname match) | projects.go:70-85 | IMPLEMENTED | |
| plan-github-oauth.md:281 | On connection delete, reconcile affected MCProject CRDs to clear GIT_IDENTITY_ID | reconciler.go:659-681 | IMPLEMENTED | |
| plan-github-oauth.md:283 | OAuth state: in-memory map, 10-min TTL, single-replica limitation accepted | providers.go:67-121 | IMPLEMENTED | |
| plan-github-oauth.md:285 | returnUrl validation: starts with /, no ://, param allowlist: provider, connected, goto, error | providers.go:763-809 | IMPLEMENTED | |
| plan-github-oauth.md:289-291 | K8s Secret keys: conn-{id}-token, conn-{id}-refresh-token (GitLab only), conn-{id}-username | providers.go:372-379 | IMPLEMENTED | |
| plan-github-oauth.md:293 | reconcileUserCLIConfig: optimistic concurrency with single retry on 409 | providers.go:1260-1276 | IMPLEMENTED | |
| plan-github-oauth.md:295-309 | gh-hosts.yml format with users map, oauth_token, default user | providers.go:1280-1343 | IMPLEMENTED | |
| plan-github-oauth.md:311-318 | glab-config.yml format with hosts, token, api_host, user | providers.go:1345-1384 | IMPLEMENTED | |
| plan-github-oauth.md:320 | Most-recently-connected account is the default | providers.go:1305-1310 | IMPLEMENTED | |
| plan-github-oauth.md:324-333 | GitLab refresh goroutine: every 15 min, refreshes tokens expiring within 30 min | providers.go:1595-1610 | IMPLEMENTED | |
| plan-github-oauth.md:335 | On refresh 401: retry once with latest token from Secret; if still 401, delete connection | providers.go:1643-1659 | IMPLEMENTED | |
| plan-github-oauth.md:339 | Startup: remove orphaned oauth_connections where auth_type='oauth' and provider_id no longer in Helm | providers.go:1730-1785 | IMPLEMENTED | |
| plan-github-oauth.md:43 | Startup reconcile: 10s timeout per user, failures logged but non-fatal | providers.go:1757-1785 | IMPLEMENTED | |
| plan-github-oauth.md:192 | EXTERNAL_URL required — fatal exit if empty | main.go:67-70 | IMPLEMENTED | |
| plan-github-oauth.md:536 | User denies OAuth: replace connected=true with error=denied in return_url | providers.go:326-329 | IMPLEMENTED | |
| plan-github-oauth.md:541 | CSRF (state mismatch): redirect to {externalUrl}/?error=csrf | providers.go:319-321 | IMPLEMENTED | |
| plan-github-oauth.md:546 | GitHub always returns HTTP 200 even for errors — exchangeCode must parse error/error_description fields | providers.go:855-916 | IMPLEMENTED | tokenResponse has Error + ErrorDescription fields; checked at lines 905-913 |
| plan-github-oauth.md:546 | When error non-empty, return it as the error (not "empty access_token"); log includes provider error string | providers.go:905-915 | IMPLEMENTED | Error checked before empty-token check |
| plan-github-oauth.md:546 | Redirect with error=exchange_failed | providers.go:345-346 | IMPLEMENTED | |
| plan-github-oauth.md:547 | Profile fetch fails: discard token, redirect with error=profile_failed | providers.go:349-352 | IMPLEMENTED | |
| plan-github-oauth.md:544 | K8s Secret write fails: redirect with error=storage | providers.go:381-384 | IMPLEMENTED | |
| plan-github-oauth.md:545 | DB write fails after token stored: remove token from Secret, redirect with error=storage | providers.go:410-418 | IMPLEMENTED | |
| plan-github-oauth.md:261 | GitLab one-identity-per-host: API-level check, returns 409 with specific message | providers.go:357-366 | PARTIAL | OAuth callback redirects with error=gitlab_one_identity instead of HTTP 409. PAT add handler has no GitLab one-identity check at all. The spec describes this as an API-level enforcement (returns 409) but: (1) callback is a redirect, not an API, so redirect is correct; (2) the PAT add path is missing the check. |
| plan-k8s-integration.md:576-588 | Reconcile loop: 10 steps — namespace, RBAC, ConfigMap, secrets, imagePullSecrets, PVCs, Deployment, status | reconciler.go:51-127 | IMPLEMENTED | |
| plan-k8s-integration.md:544-570 | MCProject CRD: spec (userId, projectId, gitUrl), status (phase, userNamespace, conditions, lastReconciledAt) | mcproject_types.go | IMPLEMENTED | |
| plan-k8s-integration.md:577 | Namespace labels: mclaude.io/user-id={userId}, mclaude.io/managed=true | reconciler.go:139-157 | IMPLEMENTED | |
| plan-k8s-integration.md:596 | Watches MCProject + Deployment + Secrets + ConfigMaps + ServiceAccounts | reconciler.go:581-629 | IMPLEMENTED | |
| plan-k8s-integration.md:600 | session-agent-template ConfigMap watch re-enqueues all MCProjects | reconciler.go:597-628 | IMPLEMENTED | |
| plan-k8s-integration.md:619-667 | Auth endpoints: /auth/login, /auth/refresh, /auth/me | auth.go:80-230 | IMPLEMENTED | |
| plan-k8s-integration.md:625-633 | Admin endpoints: /admin/users CRUD | admin.go | IMPLEMENTED | |
| plan-k8s-integration.md:696-705 | Project creation: NATS handler creates MCProject CR + writes KV + replies immediately | projects.go:45-148 | IMPLEMENTED | |
| plan-k8s-integration.md:218-247 | JWT expiry 8h default, configurable via JWT_EXPIRY_SECONDS | main.go:41-45 | IMPLEMENTED | |
| plan-k8s-integration.md:190-199 | KV buckets created on startup by control-plane | projects.go:35-42; 178-202 | PARTIAL | Only mclaude-projects and mclaude-job-queue created. Spec says control-plane creates all four (mclaude-sessions, mclaude-projects, mclaude-heartbeats, mclaude-locations). mclaude-sessions, mclaude-heartbeats, mclaude-locations not created by control-plane. |
| plan-k8s-integration.md:707-724 | Postgres schema: users, nats_credentials | db.go:313-349 | PARTIAL | nats_credentials table absent from schema. NKey seed stored in K8s Secret instead of DB. This is a structural divergence from the spec's schema definition, though the functional behavior (session-agent gets NATS creds) is achieved via K8s Secret. |
| plan-state-schema.md:119-123 | mclaude-projects KV key format: {userId}.{projectId} | projects.go:141,174 | IMPLEMENTED | |
| plan-state-schema.md:126-138 | ProjectState schema: id, name, gitUrl, status, sessionCount, worktrees, createdAt, lastActiveAt, gitIdentityId | projects.go:16-23 | PARTIAL | ProjectKVState missing: sessionCount (always 0), worktrees (always nil), lastActiveAt fields. |
| plan-state-schema.md:29-36 | projects table has cluster_id FK→clusters | db.go:323-329 | GAP | No cluster_id column in projects table DDL. State schema canonical definition requires it. |
| plan-state-schema.md:40-72 | clusters and user_clusters tables | db.go (absent) | GAP | Neither table exists in schema DDL. State schema requires both. |
| plan-state-schema.md:386-393 | Deployment named mclaude-session-agent-{projectId} | reconciler.go:315 | GAP | Deployment is named "project-{projectId}", not "mclaude-session-agent-{projectId}" as in state schema. |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| providers.go:1-34 | INFRA | Package declaration, imports |
| providers.go:41-52 | INFRA | ProviderConfig struct — necessary type for spec'd provider loading |
| providers.go:59-65 | INFRA | oauthState struct — necessary type for spec'd state map |
| providers.go:67-121 | INFRA | OAuthStateStore — fully spec'd in plan-github-oauth.md |
| providers.go:131-164 | INFRA | LoadProviders — spec'd in plan-github-oauth.md §Provider config delivery |
| providers.go:167-182 | INFRA | inClusterK8sClient — helper for LoadProviders, necessary infra |
| providers.go:190-204 | INFRA | providerRegistry struct — necessary container for spec'd data |
| providers.go:197-203 | INFRA | findProvider helper — necessary lookup for spec'd provider matching |
| providers.go:211-237 | INFRA | handleGetProviders — spec'd endpoint |
| providers.go:240-298 | INFRA | handleConnectProvider — spec'd endpoint |
| providers.go:302-426 | INFRA | handleOAuthCallback — spec'd endpoint |
| providers.go:429-522 | INFRA | handleAddPAT — spec'd endpoint |
| providers.go:524-610 | INFRA | handleDeleteConnection — spec'd endpoint |
| providers.go:613-671 | INFRA | handleGetConnectionRepos — spec'd endpoint |
| providers.go:674-757 | INFRA | handlePatchProject — spec'd endpoint |
| providers.go:763-809 | INFRA | validateReturnURL + sanitizeReturnURL — spec'd in return URL validation section |
| providers.go:812-824 | INFRA | redirectWithError — helper for spec'd error redirect behavior |
| providers.go:830-849 | INFRA | buildAuthorizeURL — necessary for spec'd OAuth flow |
| providers.go:855-916 | INFRA | exchangeCode + tokenResponse — spec'd token exchange |
| providers.go:923-979 | INFRA | fetchUserProfile — spec'd profile fetch step |
| providers.go:985-1076 | INFRA | detectPATProvider + fetchProfileWithToken — spec'd PAT detection |
| providers.go:1082-1121 | INFRA | revokeToken — spec'd disconnect revocation |
| providers.go:1127-1205 | INFRA | patchUserSecret, removeSecretKeys, readSecretKey — spec'd K8s Secret ops |
| providers.go:1211-1278 | INFRA | reconcileUserCLIConfig — spec'd CLI config reconciliation |
| providers.go:1280-1396 | INFRA | buildGHHostsYAML + buildGlabConfigYAML + extractHost — spec'd CLI config formats |
| providers.go:1402-1587 | INFRA | repoEntry, repoListResult, listRepos, listGitHubRepos, listGitLabRepos — spec'd repo listing |
| providers.go:1592-1721 | INFRA | GitLab refresh goroutine and token refresh logic — spec'd |
| providers.go:1727-1786 | INFRA | ReconcileAllUserCLIConfigs — spec'd startup reconcile |
| auth.go:1-293 | INFRA | All of auth.go: login, refresh, /auth/me, authMiddleware, checkPassword — all spec'd |
| db.go:1-349 | INFRA | All of db.go: DB struct, User, Project, OAuthConnection types, CRUD methods, schema — all spec'd |
| server.go:1-123 | INFRA | RegisterRoutes, AdminMux, adminAuthMiddleware, handleMetrics, k8sClientWrapper — all spec'd or necessary infra |
| main.go:1-309 | INFRA | main(), seedDev, loadOrGenerateAccountKey, envOr — all spec'd startup flow |
| reconciler.go:1-733 | INFRA | All of reconciler.go: MCProjectReconciler, Reconcile, reconcile* helpers, CreateMCProject, ClearMCProjectGitIdentityForConnection, PatchMCProjectGitIdentity — all spec'd |
| provision.go:1-556 | INFRA | K8sProvisioner and all ensure* methods — spec'd in plan-k8s-integration.md provisioning section (legacy path, still used as fallback) |
| nkeys.go:1-192 | INFRA | NATS JWT issuance, NKey generation, credential formatting — spec'd |
| projects.go:1-211 | INFRA | NATS subscriber, writeProjectKV, ensureProjectsKV, ensureJobQueueKV, replyError — spec'd |
| mcproject_types.go:1-148 | INFRA | CRD type definitions — spec'd |
| context.go:1-12 | INFRA | contextKey type and helpers — necessary infra for auth middleware |
| metrics.go:1-64 | UNSPEC'd | Prometheus metrics (httpRequestDuration, provisioningErrors, natsReconnects, MetricsRegistry, RecordProvisioningError, RecordNATSReconnect). The spec mentions "OTEL stack" and metrics/observability in plan-k8s-integration.md §Observability but does not specify Prometheus metric names, counters, or the MetricsRegistry export pattern in control-plane. The metrics are defined but never actually incremented in any handler (RecordProvisioningError and RecordNATSReconnect are defined but not called anywhere in production code). |
| tracing.go:1-79 | UNSPEC'd | OpenTelemetry tracing setup (InitTracing, InitTracingWithSync, SpanFromHTTP, SpanDB, SpanProvisioning). The spec references OTEL in §Observability but doesn't specify span names or tracing helper API. More importantly, these functions are defined but none are called anywhere in the production handlers — the tracing infrastructure exists but is not wired in. |
| version.go:1-30 | INFRA | GET /version endpoint — referenced in plan-k8s-integration.md endpoint list |
| admin.go:149-187 | UNSPEC'd | adminStopSession implements POST /admin/sessions/stop updating a `sessions` table with status='stopped'. The spec (plan-k8s-integration.md:664) lists "POST /admin/sessions/{id}/stop" as a break-glass endpoint but describes it as "kill session (sends SIGTERM to pod)". The code instead writes to a `sessions` DB table that doesn't exist in the schema. This is a stub implementation that doesn't match spec behavior (no actual SIGTERM sent) and references a non-existent table. |

### Summary

- Implemented: 60
- Gap: 3
- Partial: 6
- Infra: 35
- Unspec'd: 3
- Dead: 0
