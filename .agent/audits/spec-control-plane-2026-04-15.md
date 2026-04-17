## Run: 2026-04-15T00:00:00Z

Focus: docs/plan-github-oauth.md — 6 previously-identified gaps, plus any remaining github-oauth gaps.
Component root: mclaude-control-plane/

### Phase 1 — Spec → Code (github-oauth focused)

| Spec (doc:line) | Spec text | Code location | Verdict | Notes |
|-----------------|-----------|---------------|---------|-------|
| plan-github-oauth.md:208 | `PATCH /api/projects/{id}` updates DB row, MCProject CRD `GitIdentityID`, AND writes updated `ProjectKVState` to NATS KV | providers.go:675-757, reconciler.go:683-703 | IMPLEMENTED | handlePatchProject updates DB (line 729), patches CRD via PatchMCProjectGitIdentity (line 740), writes KV via writeProjectKV (line 750-754). Returns 200. |
| plan-github-oauth.md:281 | When a connection is deleted, control-plane reconciles all affected MCProject CRDs: sets `GitIdentityID` to empty | reconciler.go:658-681 (ClearMCProjectGitIdentityForConnection), providers.go:584-586 | IMPLEMENTED | handleDeleteConnection calls ClearMCProjectGitIdentityForConnection at line 585. The function lists MCProjects in namespace, clears GitIdentityID for any that match connID. |
| plan-github-oauth.md:281 | When connection deleted, NATS KV updated so SPA reflects cleared git identity in real-time | providers.go:588-603 | IMPLEMENTED | After DB delete, code fetches all user projects, writes KV for those where GitIdentityID is now nil (lines 591-601). |
| plan-github-oauth.md:98 | PAT detection: if both return 401/403, returns `400` with `"invalid token — check that the token has at least read access"`. If both return connection errors/404, returns `400` with `"could not reach provider — check the base URL"` | providers.go:985-1018 | IMPLEMENTED | detectPATProvider uses patError.isAuthError to distinguish the two cases. Lines 1014-1017 match exact spec error messages. |
| plan-github-oauth.md:285 | returnUrl allowlist: `provider`, `connected`, `goto`, `error` — any other params are stripped | providers.go:763-771, 791-809 | IMPLEMENTED | returnURLAllowedParams map contains exactly those 4 params. sanitizeReturnURL strips others before storing. |
| plan-github-oauth.md:277 | `GitIdentityID string` added to MCProjectSpec; propagated as `GIT_IDENTITY_ID` env var; when empty, env var omitted entirely | mcproject_types.go:43-48, reconciler.go:306, 357-360 | IMPLEMENTED | MCProjectSpec.GitIdentityID at line 43. reconcileDeployment reads it at line 306 and conditionally appends env var only when non-empty (lines 357-360). Comment confirms spec intent. |
| plan-github-oauth.md:393 | `conn-{connection_id}-username` key written by OAuth callback and PAT handler | providers.go:375-376, 474-476 | IMPLEMENTED | OAuth callback writes `conn-{connID}-username` in secretKeys map (line 375-376). PAT handler writes it at lines 474-476. Both in same patchUserSecret call. |
| plan-github-oauth.md:208 | PATCH endpoint returns `200` | providers.go:756 | IMPLEMENTED | `w.WriteHeader(http.StatusOK)` at line 756. |
| plan-github-oauth.md:61 | OAuth callback writes `conn-{connection_id}-token`, `conn-{connection_id}-username`, and `conn-{connection_id}-refresh-token` (GitLab only) in a single patch | providers.go:373-385 | IMPLEMENTED | secretKeys map assembled with token and username; refresh-token added if non-empty. patchUserSecret called once. |
| plan-github-oauth.md:62 | Control-plane upserts row in `oauth_connections` (UNIQUE on `user_id, base_url, provider_user_id`) | db.go:197-215 | IMPLEMENTED | ON CONFLICT (user_id, base_url, provider_user_id) DO UPDATE clause. |
| plan-github-oauth.md:63 | Control-plane calls `reconcileUserCLIConfig(userId)` after connect | providers.go:419-422 | IMPLEMENTED | Called after DB CreateOAuthConnection succeeds. |
| plan-github-oauth.md:100-102 | PAT: validates no duplicate (base_url, provider_user_id), rejects with 409 | providers.go:462-469 | IMPLEMENTED | Iterates existing connections, returns 409 on match. |
| plan-github-oauth.md:112-114 | Disconnect: revoke token (OAuth), remove keys from Secret, delete DB row, reconcile CLI config | providers.go:558-607 | IMPLEMENTED | Steps match spec order: revoke (559-563), removeSecretKeys (566-572), DeleteOAuthConnection (577), ClearMCProjectCRDs (584), KV update (588), reconcileUserCLIConfig (604). |
| plan-github-oauth.md:116 | If projects had git_identity_id pointing to deleted connection, git_identity_id is set to NULL (cascade) | db.go:348 (schema), providers.go:584-601 | IMPLEMENTED | DB schema has ON DELETE SET NULL. Code also explicitly reconciles KV for affected projects. |
| plan-github-oauth.md:293 | reconcileUserCLIConfig: optimistic concurrency on K8s Secret patch — if 409, retry once | providers.go:1249-1265 | IMPLEMENTED | On k8serrors.IsConflict, re-fetches Secret and retries Update. |
| plan-github-oauth.md:54 | OAuth state stored in memory (10-min TTL), key is 32 bytes crypto/rand hex-encoded (64 chars) | providers.go:79-94 | IMPLEMENTED | rand.Read(32 bytes), hex.EncodeToString, 10-min TTL in Put(). |
| plan-github-oauth.md:203 | POST /api/providers/{id}/connect: returns 400 if `{id}` is "pat" | providers.go:257-260 | IMPLEMENTED | Explicit check `if providerID == "pat"` returns 400. |
| plan-github-oauth.md:285 | returnUrl must start with `/`, must not contain `://`, must not be `//` (protocol-relative) | providers.go:778-788 | IMPLEMENTED | validateReturnURL checks all three conditions. |
| plan-github-oauth.md:205 | GET /api/connections/{connection_id}/repos returns 404 `{"error": "not_connected"}` if connection doesn't exist or doesn't belong to user | providers.go:633-646 | IMPLEMENTED | Both cases return 404 with JSON error body. |
| plan-github-oauth.md:324-333 | GitLab token refresh goroutine: runs every 15 min, queries expiring tokens, refreshes | providers.go:1584-1676 | IMPLEMENTED | StartGitLabRefreshGoroutine ticks every 15 minutes, calls refreshExpiringGitLabTokens which queries GetExpiringGitLabConnections within 30 min. |
| plan-github-oauth.md:335 | On 401 from refresh: retry once; if still 401, delete connection, remove Secret keys, reconcile CLI config | providers.go:1632-1649 | IMPLEMENTED | Retries once with potentially-updated refresh token. On persistent failure, removes keys and deletes DB row. |
| plan-github-oauth.md:261 | GitLab one-identity-per-host: API-level enforcement, 409 response | providers.go:356-367 | IMPLEMENTED | Before creating OAuth connection, checks existing GitLab connections for same base_url. Returns redirectWithError("gitlab_one_identity"). Note: this is a redirect error, not a 409 HTTP status — acceptable since this is in the OAuth callback flow. |
| plan-github-oauth.md:339 | Startup reconcile: detect orphaned oauth connections (admin provider removed from Helm), delete them | providers.go:1719-1774 | IMPLEMENTED | ReconcileAllUserCLIConfigs checks each connection's ProviderID against adminProviderIDs map; removes orphans. |
| plan-github-oauth.md:43 | Startup reconcile failure: best-effort, 10-second timeout per user | providers.go:1747-1773 | IMPLEMENTED | context.WithTimeout(ctx, 10*time.Second) per user iteration. Errors logged as warnings. |
| plan-github-oauth.md:211-212 | Provider config: Helm creates ConfigMap with providers.json; control-plane reads at startup | providers.go:131-164 | IMPLEMENTED | LoadProviders reads /etc/mclaude/providers.json, resolves client secrets from K8s. |
| plan-github-oauth.md:295-309 | gh-hosts.yml format: per-host users map, most-recently-connected as default user | providers.go:1269-1332 | IMPLEMENTED | buildGHHostsYAML groups by host, tracks latestTime to pick defaultUser, writes `users:` map and `user:` default. |
| plan-github-oauth.md:311-318 | glab-config.yml format with hosts, token, api_host, user | providers.go:1334-1373 | IMPLEMENTED | buildGlabConfigYAML writes that exact format. |
| plan-github-oauth.md:271-274 | projects table: git_identity_id column, ON DELETE SET NULL | db.go:348 | IMPLEMENTED | Schema line adds column with FK to oauth_connections ON DELETE SET NULL. |
| plan-github-oauth.md:206 | POST /api/providers/pat returns 201 with {connectionId, providerType, displayName, username} | providers.go:514-521 | IMPLEMENTED | w.WriteHeader(201), encodes all four fields. |
| plan-github-oauth.md:460 | ProjectKVState gains `gitIdentityId` field | projects.go:16-23 | IMPLEMENTED | ProjectKVState struct has GitIdentityID *string `json:"gitIdentityId,omitempty"`. |
| plan-github-oauth.md:205 | PATCH /api/projects/{id} validates: connection belongs to user, connection's base_url hostname matches project's GIT_URL hostname | providers.go:712-727 | IMPLEMENTED | Checks conn.UserID != userID and extractHost comparison. |
| plan-github-oauth.md:278 | NATS projects.create payload adds gitIdentityId; control-plane validates same checks as PATCH | projects.go:56-85 | IMPLEMENTED | Unmarshals GitIdentityID *string, validates connection belongs to user and hostname matches. |
| plan-github-oauth.md:278 | CreateMCProject CRD creation propagates gitIdentityID | projects.go:106, reconciler.go:635-655 | IMPLEMENTED | CreateMCProject accepts gitIdentityIDStr; sets MCProjectSpec.GitIdentityID. |
| plan-github-oauth.md:462-512 | /auth/me response gains connectedProviders array with connectionId, providerId, providerType, authType, displayName, baseUrl, username, connectedAt | auth.go:173-229 | IMPLEMENTED | connectedProviderEntry struct and handleMe populate all required fields. |
| plan-github-oauth.md:517-530 | GET /api/providers returns admin OAuth providers only (source: "admin"), not user PATs | providers.go:210-237 | IMPLEMENTED | handleGetProviders iterates s.providers.providers (Helm-configured only). |
| plan-github-oauth.md:192 | control-plane exits on startup if EXTERNAL_URL is empty | main.go (not yet read) | NEEDS-CHECK | Need to verify main.go fatal on empty EXTERNAL_URL. |

### Phase 2 — Code → Spec (reverse pass)

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| providers.go:1-35 | INFRA | Package declaration, imports — standard Go boilerplate. |
| providers.go:41-52 | INFRA | ProviderConfig struct — implements Helm provider config spec (plan-github-oauth.md §Instance configuration). |
| providers.go:59-121 | INFRA | OAuthStateStore struct and methods — direct implementation of in-memory state spec. |
| providers.go:166-182 | INFRA | inClusterK8sClient helper — infrastructure for loading K8s client secrets. |
| providers.go:188-204 | INFRA | providerRegistry and findProvider — internal lookup table, necessary plumbing. |
| providers.go:830-849 | INFRA | buildAuthorizeURL — builds provider-specific OAuth authorize URL per spec §GitLab OAuth URLs. |
| providers.go:855-908 | INFRA | exchangeCode — token exchange, spec plan-github-oauth.md line 59. |
| providers.go:914-970 | INFRA | fetchUserProfile — profile fetch step (spec line 60). |
| providers.go:1071-1110 | INFRA | revokeToken — token revocation per disconnect spec (line 111). |
| providers.go:1116-1194 | INFRA | patchUserSecret, removeSecretKeys, readSecretKey — K8s Secret write helpers, spec §K8s Secret writes. |
| providers.go:1375-1385 | INFRA | extractHost — URL parsing utility used throughout. |
| providers.go:1390-1575 | INFRA | repoEntry, repoListResult, listRepos, listGitHubRepos, listGitLabRepos — repo proxy (spec §GET /api/connections/{connection_id}/repos). |
| providers.go:1679-1710 | INFRA | exchangeRefreshToken — GitLab token refresh call (spec §GitLab token refresh). |
| db.go:1-312 | INFRA | All DB methods — direct implementations of spec §New DB table and §Projects table change. |
| db.go:314-349 | INFRA | schema constant — implements exact DDL from spec. |
| reconciler.go:36-47 | INFRA | MCProjectReconciler struct — spec plan-k8s-integration.md reconciler. |
| reconciler.go:51-128 | INFRA | Reconcile() — spec-driven provisioning loop. |
| reconciler.go:131-212 | INFRA | reconcileNamespace, reconcileRBAC — spec plan-k8s-integration.md. |
| reconciler.go:214-296 | INFRA | reconcileSecrets — spec plan-k8s-integration.md; oauth-token from DEV_OAUTH_TOKEN is UNSPEC'd in github-oauth but spec'd in k8s-integration plan as dev token. |
| reconciler.go:447-500 | INFRA | ensurePVCCR, ensureOwned — provisioning helpers. |
| reconciler.go:502-541 | INFRA | loadTemplate — reads session-agent-template ConfigMap. |
| reconciler.go:543-629 | INFRA | setPhase, updateCondition, setCondition, SetupWithManager — reconciler plumbing. |
| reconciler.go:705-733 | INFRA | defaultTemplate, applyDefaultResources — dev defaults. |
| mcproject_types.go:1-147 | INFRA | CRD type definitions — spec §CRD change. |
| auth.go:1-291 | INFRA | Auth handlers — spec plan-k8s-integration.md. handleMe gains connectedProviders per github-oauth spec. |
| server.go:1-123 | INFRA | Route registration and Server struct — wires all spec-defined endpoints. |
| projects.go:1-211 | INFRA | Projects subscriber, KV helpers — spec-defined project creation and KV bucket. |

### Summary

- Implemented: 35
- Gap: 1 (EXTERNAL_URL fatal startup check — needs main.go verification)
- Partial: 0
- Infra: 26
- Unspec'd: 0
- Dead: 0

### State Schema Consistency Checks

| State element | plan-state-schema.md | Code | Verdict |
|---------------|---------------------|------|---------|
| `mclaude-projects` KV `ProjectState` has `gitIdentityId` | Not in schema (schema shows only: id, name, gitUrl, status, sessionCount, worktrees, createdAt, lastActiveAt) | `projects.go:16-23` — `ProjectKVState` has `gitIdentityId` | GAP: state schema lacks `gitIdentityId` field; also schema lists `sessionCount`, `worktrees`, `lastActiveAt` which code does not emit |
| `user-secrets` Secret has github-oauth keys | Schema (line 332-339) only lists `nats-creds` and `oauth-token` | Code writes `gh-hosts.yml`, `glab-config.yml`, `conn-{id}-token`, `conn-{id}-refresh-token`, `conn-{id}-username` | GAP: state schema not updated to document OAuth credential keys |

### Updated Summary

- Implemented: 36 (EXTERNAL_URL fatal check confirmed at main.go:67-70)
- Gap: 2 (state schema: ProjectKVState missing gitIdentityId; user-secrets missing oauth keys)
- Partial: 0
- Infra: 26
- Unspec'd: 0
- Dead: 0
