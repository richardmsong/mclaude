# Git Provider OAuth — Repo Access

## Overview

Users connect their GitHub or GitLab account via OAuth so mclaude can clone private repos without manual SSH key setup. The control-plane handles the OAuth flow, stores tokens in K8s Secrets, and proxies provider API calls (repo listing) so the SPA never touches tokens. The architecture is provider-agnostic — GitHub and GitLab ship in v1, with Bitbucket and Azure DevOps planned.

**Multi-identity model:** Users can have multiple accounts per hostname (e.g., `@rsong-work` and `@rsong-personal` on github.com). Each project can be bound to a specific identity. The session-agent uses `gh`/`glab` CLI credential helpers — no custom `GIT_ASKPASS` scripts.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| OAuth type | GitHub OAuth App (not GitHub App) | Simpler. Tokens never expire, no refresh needed. GitHub App adds per-repo install complexity. |
| Callback routing | Control-plane HTTP | Same pattern as `/auth/login`. No new components. |
| Multi-environment callbacks | Dynamic via OAuth `state` param | GitHub doesn't support wildcard callback URLs. Encode return URL in state so one registered callback works for all environments. |
| Token storage | K8s Secret only — no tokens in DB | User requirement. Control-plane writes token to `user-secrets` Secret. Same delivery as `nats-creds`. |
| Connection metadata | Postgres `oauth_connections` table | Non-secret metadata (provider, username, connected status) in DB. Covers both OAuth connections and user PATs. Enables future SSO login (look up user by provider ID). |
| Repo picker | v1 — search-first with lazy load | Control-plane proxies GitHub API. SPA shows search input, fetches on type, paginates on scroll. |
| UX placement | Settings + New Project sheet | Settings has full connect/disconnect. New Project shows inline "Connect GitHub" button when not connected. Both optional. |
| Disconnect behavior | Remove token + revoke on GitHub | Calls `DELETE /applications/{client_id}/token` on GitHub. Existing cloned repos keep working (data on PVC). Future fetch/push fails until reconnected. |
| OAuth scope | `repo read:org gist workflow read:user user:email` | Match `gh auth login` scopes. Cloning, PRs, org repos in picker, Actions triggers, gists, profile display. Users expect the same experience as the GitHub CLI. |
| Error surfacing | Session status + banner | Session-agent marks session `failed` with `provider_auth_failed`. SPA shows banner linking to Settings. |
| Provider scope for v1 | GitHub + GitLab | Feature parity between the two. GitLab adds refresh token handling and self-hosted URL support. Bitbucket/Azure DevOps later. |
| SSO login | v2 | v1 is repo access only. v2 adds "Sign in with GitHub" on auth screen, user creation from provider profile, multi-provider account linking. |
| Token reads | K8s Secret on each request | Control-plane reads `user-secrets` Secret via K8s API for every GitHub API proxy call. No in-memory cache. Always fresh. |
| OAuth start | Authenticated endpoint returns redirect URL | SPA calls `POST /api/providers/{id}/connect` with JWT. Control-plane returns `{redirectUrl}`. SPA navigates to it. Avoids leaking JWT in URL params. |
| User namespace timing | Created at user creation, not project creation | Namespace + `user-secrets` Secret exist before any project. Token can be written immediately on OAuth connect. Prerequisite: move namespace creation from project reconciler to user creation handler (see "Prerequisite: User Namespace" section). |
| Running pods after connect | Live pickup via Secret mount sync | K8s auto-syncs Secret mounts (~1 min). Session-agent merges new tokens into CLI configs before next git operation. |
| Post-redirect UX | Query params in redirect URL | Callback redirects to `{externalUrl}/?provider=github&connected=true&goto=settings`. SPA reads query params on page load, shows toast, navigates to the hash route, then cleans the query string via `history.replaceState`. Query params (not hash fragments) because HTTP redirects discard fragments. Full redirect (not popup) — works on mobile Safari. |
| User PATs | Users can add their own provider instances via PAT | Covers GHES servers not admin-configured. User pastes a PAT in Settings, control-plane stores it in `user-secrets` Secret. No OAuth flow needed. |
| Credential resolution | Credential helpers (`gh`/`glab`) with active account switching | `gh auth setup-git` / `glab auth setup-git` register CLI as git credential helper. `gh auth switch --user {username}` sets the active account per project. Git operations automatically use the active account's token. |
| Multi-identity | Multiple accounts per hostname (GitHub only) | Users can connect `@rsong-work` and `@rsong-personal` to the same github.com. Per-project identity binding via `git_identity_id`. GitLab is limited to one identity per hostname — `glab` doesn't support per-host multi-account switching. |
| Credential mechanism | `gh`/`glab` credential helpers (not GIT_ASKPASS) | `gh auth setup-git` registers `gh` as git's credential helper for a given host. Git calls `gh auth git-credential` which returns the active account's token. Native, maintained by GitHub/GitLab, handles edge cases. |
| CLI tool installation | `gh` and `glab` baked into session image as system dependencies | Required for credential helper model. `gh` via `apk` (Alpine community repo, 2.40+ for multi-account), `glab` via binary download (not in Alpine repos). Not via Nix — `/nix/` PVC mount hides image-layer packages. |
| CLI config persistence | PVC-backed `~/.config/` | Symlink `/data/.config/` → `~/.config/` so `gh auth login` and `glab auth login` survive pod restarts. Manual CLI auth within sessions is respected. |
| Config merge strategy | Merge, not overwrite | Session-agent adds managed tokens to `hosts.yml` without removing entries from manual `gh auth login`. User's manual auth is preserved. |
| Provider list storage | Derived from `oauth_connections` DB at session start | No `git-providers.json` file. Session-agent receives connection metadata via environment or K8s Secret annotation. Control-plane writes `hosts.yml`-format data to Secret. |
| Provider config source | Admin OAuth from Helm, user PATs from DB | Helm values seed admin OAuth providers. Users add PAT instances at runtime via API. Both stored as metadata in `oauth_connections`. |
| Token key format | `conn-{connection_id}-token` | UUID-based keys avoid collision when multiple accounts exist for the same hostname. Connection ID is the `oauth_connections.id` UUID. |
| Identity selector | Repo picker shows "Browse as @username on host" | When multiple identities exist for a provider, user picks which one to browse repos as. |
| Per-project identity | `git_identity_id` column on `projects` table | References `oauth_connections.id`. Session-agent switches to this identity at session start. |
| GitLab multi-identity | One identity per GitLab host | `glab` doesn't support per-host multi-account switching (no `auth switch`, no `users:` map). Enforced at API level, not DB. GitHub gets full multi-identity. |
| Startup reconcile failure | Best-effort with 10s timeout per user | Failures logged, control-plane becomes ready regardless. Stale GitLab tokens retried on next 15-min refresh cycle. One unreachable GitLab instance shouldn't block the whole platform. |

## User Flow

All flows below are provider-agnostic — substitute any configured provider instance ID (e.g., `github`, `company-ghes`, `gitlab`). Examples use `github`.

### Connecting a provider (Settings)

1. User navigates to Settings
2. Sees list of configured providers from `GET /api/providers`. Clicks "Connect GitHub"
3. SPA calls `POST /api/providers/github/connect` with JWT and `{returnUrl: "/?provider=github&connected=true&goto=settings"}`
4. Control-plane generates random `state`, stores `{state, return_url, user_id, provider_id}` in memory (10-minute TTL)
5. Control-plane returns `{redirectUrl}` — the provider's authorize URL with client ID, scopes, state, and callback
6. SPA navigates to `redirectUrl` (full page redirect)
7. User authorizes on the provider
8. Provider redirects to `GET /auth/providers/github/callback?code={code}&state={state}`
9. Control-plane validates state, exchanges code for token at the provider's token endpoint (must include `redirect_uri` for GitLab — see GitLab OAuth URLs section)
10. Control-plane fetches user profile from the provider's API (username, provider user ID)
11. Control-plane generates a connection UUID and writes to `user-secrets` K8s Secret in a single patch: `conn-{connection_id}-token`, `conn-{connection_id}-username` (plain text username from profile), and `conn-{connection_id}-refresh-token` (GitLab only)
12. Control-plane upserts row in `oauth_connections` table (UNIQUE on `user_id, base_url, provider_user_id` — allows multiple accounts per hostname)
13. Control-plane calls `reconcileUserCLIConfig(userId)` to rebuild the `gh-hosts.yml` and `glab-config.yml` keys in the `user-secrets` Secret
14. Control-plane redirects browser to `{externalUrl}{return_url}` (e.g., `https://mclaude.internal/?provider=github&connected=true&goto=settings`)
15. SPA loads, reads query params, shows "{displayName} connected as @{username}" toast, navigates to the `goto` hash route, cleans query string via `history.replaceState`
16. SPA calls `GET /auth/me` — `connectedProviders` now includes the new connection
17. Settings shows "GitHub: @rsong-work" with a Disconnect button

**Connecting a second account on the same host:** The user clicks "Connect GitHub" again. A new OAuth flow starts. If they authorize with a different GitHub account, the callback creates a new `oauth_connections` row (different `provider_user_id`). Both accounts now appear in Settings. If they authorize with the same account, the existing row is updated (upsert on `user_id, base_url, provider_user_id`).

### Connecting a provider (New Project sheet)

1. User opens New Project sheet
2. If no providers connected: shows "Connect GitHub" / "Connect GitLab" buttons + manual URL field
3. User clicks "Connect GitLab" → same flow as above, returnUrl is `/?provider=gitlab&connected=true&goto=new-project`
4. After redirect, SPA reads query params on page load, stores them in memory, cleans query string via `history.replaceState`. After auth completes and the dashboard is rendered, the SPA checks the stored params: shows "{displayName} connected" toast, then programmatically opens the New Project sheet (deferred until auth + initial data load complete — the sheet needs the user's connection list). The `goto` param is a hint, not a route: `settings` → navigate to `#/settings`, `new-project` → open sheet programmatically.
5. Provider is now connected → repo picker appears

### Creating a project with repo picker

1. User opens New Project, one or more providers connected
2. Types project name
3. If multiple identities connected across providers, SPA shows identity selector: "Browse as @rsong-work on github.com" / "Browse as @rsong-personal on github.com" / "Browse as @rsong on gitlab.com"
4. Repo picker shows search input. User types to filter.
5. SPA calls `GET /api/providers/connections/{connection_id}/repos?q={query}` on control-plane
6. Control-plane reads `conn-{connection_id}-token` from K8s Secret, calls the provider's repo list API
7. Returns normalized repo list to SPA (name, full_name, private, description, clone_url)
8. User selects a repo → HTTPS clone URL populated, `git_identity_id` set to the selected connection ID
9. User can still type a manual URL instead (manual field always available) — `git_identity_id` is null (credential helpers use the active account, which is the most recently switched one)
10. Create Project → control-plane provisions pod with `GIT_URL=https://...` and `git_identity_id` on the project row

### Adding a PAT provider (Settings)

1. User navigates to Settings → "GIT PROVIDERS" section
2. Clicks "Add provider with PAT"
3. SPA shows form: Base URL (e.g., `https://github.acme.com`), Display Name, Personal Access Token
4. SPA calls `POST /api/providers/pat` with JWT and `{baseUrl, displayName, token}`
5. Control-plane auto-detects provider type **sequentially, first match wins**: calls `{baseUrl}/api/v3/user` with the token — if 200, type is `github`. Otherwise calls `{baseUrl}/api/v4/user` — if 200, type is `gitlab`. If both return 401/403, returns `400` with `"invalid token — check that the token has at least read access"`. If both return connection errors/404, returns `400` with `"could not reach provider — check the base URL"`. (In the unlikely case both succeed, GitHub wins — acceptable since no real GitLab instance responds to `/api/v3/user`.)
6. On success, the user profile response provides `username` and `provider_user_id`. **No scope validation** — the PAT may lack clone permissions. This is an accepted limitation; if the PAT has insufficient scopes, git operations will fail with a clear auth error and the user can replace the PAT with one that has the right scopes.
7. Control-plane generates a connection UUID. Validates that the user doesn't already have a connection with the same `base_url` and `provider_user_id` — rejects with `409 Conflict` if so. Writes token to `user-secrets` K8s Secret as `conn-{connection_id}-token`.
8. Control-plane inserts row in `oauth_connections` with `auth_type: 'pat'`
9. Control-plane calls `reconcileUserCLIConfig(userId)` to rebuild CLI config keys in Secret
10. Settings shows the new provider with "PAT" badge and "Remove" button

### Disconnecting a provider

1. User navigates to Settings
2. Clicks "Disconnect" next to a connection (e.g., "GitHub: @rsong-work")
3. SPA confirms: "Existing projects will keep their cloned repos but won't be able to fetch updates. Disconnect?"
4. SPA calls `DELETE /api/connections/{connection_id}`
5. Control-plane revokes the token on the provider if possible (OAuth: GitHub `DELETE /applications/{client_id}/token`, GitLab `POST {baseUrl}/oauth/revoke`; PAT: no revocation — user manages their own PAT on the provider). If the admin provider config is no longer in Helm (removed between connect and disconnect), revocation is skipped — the token is just deleted locally.
6. Control-plane removes `conn-{connection_id}-token` (and `conn-{connection_id}-refresh-token` if applicable) from `user-secrets` K8s Secret
7. Control-plane deletes row from `oauth_connections` table
8. Control-plane calls `reconcileUserCLIConfig(userId)` to rebuild CLI config keys in Secret
9. Returns success. SPA updates Settings.
10. If any projects had `git_identity_id` pointing to this connection, their `git_identity_id` is set to NULL (cascade). Next session start for those projects falls back to the default active account.

### Changing a project's identity

1. User opens project settings (or edits project)
2. Sees "Git Identity" dropdown showing all connected identities that match the project's repo hostname
3. Selects "@rsong-personal on github.com"
4. SPA calls `PATCH /api/projects/{id}` with `{gitIdentityId: "{connection_id}"}`
5. Control-plane validates the connection belongs to the user and the hostname matches the project's `GIT_URL`
6. Updates `projects.git_identity_id`
7. Next session start for this project switches to the selected identity

## Provider Instances

A provider instance is a specific installation of GitHub or GitLab — could be github.com, a GHES server, gitlab.com, or a self-hosted GitLab. Each instance is a separate OAuth App registration with its own client ID/secret and base URL.

### Supported instance types in v1

| Type | Examples | OAuth flow | Token expiry | Refresh? |
|------|----------|-----------|--------------|----------|
| `github` | github.com (free/GHEC), GHES at `github.company.com` | Standard OAuth 2.0 | Never | No |
| `gitlab` | gitlab.com, self-hosted at `gitlab.company.com` | Standard OAuth 2.0 (confidential app) | 2 hours | Yes |

**GitLab OAuth URLs** (derived from `baseUrl`):
- Authorize: `{baseUrl}/oauth/authorize`
- Token exchange: `{baseUrl}/oauth/token` (must include `redirect_uri` — GitLab requires it on token exchange, unlike GitHub)
- Revoke: `{baseUrl}/oauth/revoke`
- User profile: `{baseUrl}/api/v4/user`

When registering the GitLab OAuth App, select **Confidential** application type. This ensures `client_secret` is required on the token exchange (matches the PKCE section — server-side confidential client).

### Instance configuration (Helm values)

```yaml
controlPlane:
  externalUrl: "https://mclaude.internal"  # explicit, not derived from ingress.host
  providers:
    - id: github              # unique ID, used in URLs and DB
      type: github            # adapter type
      displayName: GitHub     # shown in UI
      baseUrl: https://github.com
      apiUrl: https://api.github.com
      clientId: "Ov23li..."
      clientSecretRef: github-oauth-secret  # K8s Secret name containing 'client-secret' key
      scopes: "repo read:org gist workflow read:user user:email"
    - id: company-ghes
      type: github
      displayName: "ACME GitHub"
      baseUrl: https://github.acme.com
      apiUrl: https://github.acme.com/api/v3
      clientId: "Iv1.abc..."
      clientSecretRef: ghes-oauth-secret
      scopes: "repo read:org gist workflow read:user user:email"
    - id: gitlab
      type: gitlab
      displayName: GitLab
      baseUrl: https://gitlab.com
      clientId: "app-id..."
      clientSecretRef: gitlab-oauth-secret
      scopes: "api"
```

GHES uses the same `github` adapter type — same OAuth flow, same API structure, just different URLs. Each instance gets its own OAuth App registration and K8s Secret for the client secret.

### Callback URL

The OAuth callback URL is derived from `controlPlane.externalUrl`:

```
{externalUrl}/auth/providers/{id}/callback
```

For preview environments: determined by `EXTERNAL_URL` (set by CI from branch slug and Tailscale DNS).

When registering the OAuth App on the provider, this is the URL entered as the "Authorization callback URL" (GitHub) or "Redirect URI" (GitLab). Each environment (main, preview) needs its own OAuth App registration OR the OAuth App must be configured with multiple callback URLs. For development, a single OAuth App on github.com with multiple callback URLs is acceptable.

The control-plane reads its external base URL from `controlPlane.externalUrl`, injected as the `EXTERNAL_URL` environment variable. **Required — control-plane exits on startup if `EXTERNAL_URL` is empty** (Go startup check, logged as fatal error). No default, no fallback from `ingress.host`. The Helm chart template also uses `required` to fail at install time. Must be set explicitly because the scheme (HTTP vs HTTPS) and full URL vary per environment. For main: `https://mclaude.internal`. For preview environments, the CI workflow computes the URL from the branch slug and Tailscale IP and passes it as `--set controlPlane.externalUrl=...` in the Helm command.

## Component Changes

### Control-plane

**New HTTP endpoints:**

| Method | Path | Auth | Purpose |
|--------|------|------|---------|
| `GET` | `/api/providers` | JWT required | List admin OAuth providers (from Helm config). Does NOT include user connections — those come from `/auth/me`'s `connectedProviders`. |
| `POST` | `/api/providers/{id}/connect` | JWT required | Start OAuth flow for admin OAuth providers only. Body: `{returnUrl}` (required — must start with `/` or `#`). Returns `{redirectUrl}`. Returns `400` if `{id}` is a PAT provider. If user is already connected with this provider account, starts a new OAuth flow — the callback upserts the `oauth_connections` row and overwrites the token in the Secret (effectively a reconnect/re-auth). |
| `GET` | `/auth/providers/{id}/callback` | None (redirects) | OAuth callback. Exchanges code for token, stores it, redirects to return URL. |
| `GET` | `/api/connections/{connection_id}/repos` | JWT required | Proxy: list/search repos using a specific connection's token. Query params: `q`, `page`. Verifies JWT user matches connection's `user_id` — returns `404 {"error": "not_connected"}` if connection doesn't exist or doesn't belong to user (404, not 403, to avoid leaking connection existence). |
| `POST` | `/api/providers/pat` | JWT required | Add user PAT connection. Body: `{baseUrl, displayName, token}`. Validates token, stores in Secret + DB. Returns `201` with `{connectionId, providerType, displayName, username}`. |
| `DELETE` | `/api/connections/{connection_id}` | JWT required | Disconnect: revoke token (OAuth) or delete token (PAT), remove from Secret + DB. Returns `204` on success. Verifies JWT user matches connection's `user_id`. |
| `PATCH` | `/api/projects/{project_id}` | JWT required | Update project identity. Body: `{"gitIdentityId": "{uuid}"}` or `{"gitIdentityId": null}` (explicit null clears binding). Omitting the field is a no-op. Validates: connection belongs to user, connection's `base_url` hostname matches project's `GIT_URL` hostname. Updates `projects.git_identity_id` in DB, MCProject CRD `GitIdentityID`, AND writes updated `ProjectKVState` to NATS KV (so SPA sees the change). Returns `200`. |

"JWT required" means the same NATS user JWT + `authMiddleware` used by existing endpoints like `/auth/me`. No new auth mechanism. The SPA already stores the JWT from login and sends it as `Authorization: Bearer {jwt}` for HTTP calls (same pattern as `/auth/me`). These provider endpoints are HTTP (not NATS request-reply) because they involve redirects and proxy calls that don't fit the NATS pub/sub model.

**Provider config delivery:** The Helm chart creates a ConfigMap named `{release}-provider-config` containing a `providers.json` key (JSON-serialized from `controlPlane.providers[]`). The control-plane Deployment mounts this ConfigMap as a volume at `/etc/mclaude/providers.json`. `controlPlane.externalUrl` is injected as the `EXTERNAL_URL` environment variable. The control-plane reads both at startup. For each provider, it reads the client secret from the referenced K8s Secret (`clientSecretRef`). The parsed provider list and external URL are held in memory for the lifetime of the process. If `providers.json` is empty or missing (no providers configured), the control-plane starts normally — provider endpoints return empty lists.

**`GET /api/connections/{connection_id}/repos` response format:**

```json
{
  "repos": [
    {
      "name": "mclaude",
      "fullName": "rsong/mclaude",
      "private": true,
      "description": "Multi-Claude orchestration platform",
      "cloneUrl": "https://github.com/rsong/mclaude.git",
      "updatedAt": "2026-04-10T12:00:00Z"
    }
  ],
  "nextPage": 2,
  "hasMore": true
}
```

Query params: `q` (search string, optional), `page` (1-indexed integer, default 1). Page size is 30 (matches GitHub/GitLab defaults). `nextPage` is null when `hasMore` is false. The control-plane translates provider-specific pagination (GitHub Link headers, GitLab `x-next-page` headers) into this uniform format.

**Sort order:** When `q` is empty (initial load), repos are sorted by most recently pushed (GitHub: `sort=pushed`, GitLab: `order_by=last_activity_at`). When `q` is provided, the provider's search ranking is used (GitHub: best-match, GitLab: relevance).

**Rate limiting:** The control-plane passes through 429/403 rate-limit responses from the provider. The SPA shows "Rate limited — try again shortly" with the retry-after value if provided. No server-side caching or throttling in v1.

**New DB table:**

```sql
CREATE TABLE IF NOT EXISTS oauth_connections (
    id               TEXT PRIMARY KEY,
    user_id          TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider_id      TEXT NOT NULL,           -- admin instance ID: 'github', 'company-ghes', 'gitlab' OR 'pat' for user PATs
    provider_type    TEXT NOT NULL,           -- adapter type: 'github' or 'gitlab'
    auth_type        TEXT NOT NULL DEFAULT 'oauth', -- 'oauth' or 'pat'
    base_url         TEXT NOT NULL,           -- e.g., 'https://github.com', 'https://github.acme.com'
    display_name     TEXT NOT NULL DEFAULT '',-- user-facing name (e.g., 'GitHub', 'ACME GitHub')
    provider_user_id TEXT NOT NULL,           -- provider's user ID (numeric string)
    username         TEXT NOT NULL,           -- display username on the provider (e.g., 'rsong-work')
    scopes           TEXT NOT NULL DEFAULT '',-- scopes granted (OAuth) or empty (PAT)
    token_expires_at TIMESTAMPTZ,            -- NULL for non-expiring tokens (GitHub, PATs). Set from GitLab's expires_in response.
    connected_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, base_url, provider_user_id)
);
```

The UNIQUE constraint is on `(user_id, base_url, provider_user_id)` — not on `(user_id, provider_id)`. This allows multiple accounts on the same hostname (e.g., two github.com accounts with different `provider_user_id` values). The `provider_id` field identifies which admin provider config (from Helm) was used for the OAuth flow; for PATs it's always `'pat'`.

**GitLab one-identity-per-host constraint:** The `glab` CLI stores one token per host and has no multi-account switching. The control-plane enforces `UNIQUE(user_id, base_url)` for GitLab-type connections at the API level (not a DB constraint — the DB allows it for forward-compatibility). Attempting to connect a second GitLab account on the same host returns `409` with `"Only one GitLab identity per host is supported — disconnect the existing one first"`. GitHub-type connections have no such limit.

**OAuth + PAT coexistence:** A user can have both an OAuth connection and a PAT connection for the same `base_url`, as long as they use different provider accounts (`provider_user_id`). For example, OAuth as `@rsong-work` + PAT as `@rsong-bot` on `github.com`. If the same account is used for both OAuth and PAT (same `base_url + provider_user_id`), the UNIQUE constraint prevents it — user must disconnect one before adding the other. The `provider_id` differs (`"github"` for OAuth vs `"pat"` for PAT) but the UNIQUE constraint is on `(user_id, base_url, provider_user_id)`, not `provider_id`.

**PAT update flow:** To update a PAT token, the user must remove the existing PAT connection and re-add it with the new token. There is no in-place update endpoint. This is intentional — PATs have no refresh mechanism, and the remove+add flow ensures the old token is cleaned up from the Secret.

The `token_expires_at` column is used by the GitLab refresh goroutine to determine which tokens need refreshing. On initial OAuth exchange, it's set to `NOW() + expires_in` from GitLab's token response. On each refresh, it's updated with the new expiry. For GitHub and PATs (tokens don't expire), it's NULL.

**Projects table change:**

```sql
ALTER TABLE projects ADD COLUMN git_identity_id TEXT REFERENCES oauth_connections(id) ON DELETE SET NULL;
```

Added to the `schema` constant in `db.go`. When a user selects an identity in the repo picker, this is set to the `oauth_connections.id`. When NULL, the session-agent uses the default active account for the hostname. ON DELETE SET NULL ensures that if a connection is removed, projects fall back to default behavior.

**CRD change:** Add `GitIdentityID string` to `MCProjectSpec` in `mcproject_types.go` (alongside existing `UserID`, `ProjectID`, `GitURL`). The reconciler propagates it as `GIT_IDENTITY_ID` environment variable on the Deployment (same pattern as `GIT_URL`). **When `GitIdentityID` is empty (no binding), the env var is omitted from the pod spec entirely** — not set to empty string. The session-agent checks `os.Getenv("GIT_IDENTITY_ID") != ""` to decide whether to switch accounts.

**NATS message schema change:** The `projects.create` request payload adds `gitIdentityId` (optional string, nullable). The control-plane validates `gitIdentityId` on create (same checks as PATCH: connection belongs to user, hostname matches `gitUrl`). If validation fails, returns error and does not create the project. The `CreateProject` DB function and `CreateMCProject` CRD creation both accept and propagate this field. The `PATCH /api/projects/{id}` HTTP endpoint (for changing identity post-creation) updates the DB row, MCProject CRD spec, and NATS KV `ProjectKVState` (so SPA sees the change). The SPA uses HTTP for identity changes (not NATS) because it's a control-plane-only operation.

**Stale `GIT_IDENTITY_ID` handling:** When a connection is deleted (`DELETE /api/connections/{id}`), the DB cascades `SET NULL` on `projects.git_identity_id`. The control-plane then reconciles all affected MCProject CRDs: sets `GitIdentityID` to empty, which causes the reconciler to update the Deployment env var. Running pods are not restarted — the stale env var is harmless because the session-agent checks for the connection's username key (`conn-{id}-username`) in the Secret mount. If the key is gone (connection deleted, Secret reconciled), the session-agent logs a warning and falls back to the default active account.

**OAuth state storage:** In-memory map with 10-minute TTL. Key: cryptographically random token (32 bytes from `crypto/rand`, hex-encoded → 64 character string). Value: `{return_url, user_id, provider_id}`. Cleared after use or on expiry. This is an accepted limitation: the control-plane runs as a single replica. If the pod restarts during the 10-minute OAuth window, the user must retry the connect flow. Multi-replica deployments (v2+) would need to move state to Postgres or Redis.

**Return URL validation:** The `returnUrl` in the connect request body must be a relative path starting with `/`. The control-plane validates: (1) starts with `/`, (2) contains no `://` (blocks absolute URL injection), (3) if query params are present, they are limited to an allowlist: `provider`, `connected`, `goto`, `error` — any other params are stripped. A bare `"/"` is valid (redirects to root with no toast/navigation). The SPA constructs the `returnUrl` with the appropriate query params. The control-plane prepends its own external base URL when constructing the full redirect target (e.g., `{externalUrl}/?provider=github&connected=true&goto=settings`). The SPA reads query params (`provider`, `connected`, `goto`, `error`) on page load, acts on them (toast, navigation), and cleans the query string via `history.replaceState`.

**PKCE:** Not used in v1. Both GitHub OAuth Apps and GitLab confidential apps authenticate via client secret on the token exchange, making PKCE optional. GitLab recommends PKCE for public clients (SPAs doing the flow directly), but since the control-plane is a confidential server-side client, client secret authentication is sufficient.

**K8s Secret writes:** The control-plane's ClusterRole already has full CRUD on Secrets across namespaces (used for creating `user-secrets` during provisioning). No new RBAC rules needed. On successful OAuth callback, patch `user-secrets` Secret with:
- `conn-{connection_id}-token` — OAuth access token or PAT
- `conn-{connection_id}-refresh-token` — OAuth refresh token (GitLab only)

**CLI config reconciliation (`reconcileUserCLIConfig`):** After any change to `oauth_connections` (connect, disconnect, add PAT, remove), the control-plane rebuilds two keys in the `user-secrets` Secret. This is a read-modify-write on the Secret: (1) query `oauth_connections` rows for this user from DB, (2) read current `user-secrets` Secret from K8s API, (3) for each connection, read the `conn-{id}-token` value from the Secret data, (4) build the `gh-hosts.yml` and `glab-config.yml` YAML, (5) patch the Secret with the new YAML keys. **Concurrency:** Since the control-plane is single-replica and Go HTTP handlers are sequential per-request, concurrent reconciles for the same user are unlikely but possible (e.g., two OAuth callbacks completing simultaneously). The reconcile uses `resourceVersion`-based optimistic concurrency on the K8s Secret patch — if a concurrent write changed the Secret, the patch fails with 409 and is retried once. This is sufficient for single-replica; multi-replica would need a distributed lock.

`gh-hosts.yml` — `gh` CLI config format with multi-account support:
```yaml
github.com:
    users:
        rsong-work:
            oauth_token: gho_abc123...
        rsong-personal:
            oauth_token: gho_xyz789...
    user: rsong-work
github.acme.com:
    users:
        rsong:
            oauth_token: ghp_acme123...
    user: rsong
```

`glab-config.yml` — `glab` CLI config format:
```yaml
hosts:
  gitlab.com:
    token: glpat_abc123...
    api_host: gitlab.com
    user: rsong
```

The `user` field under each host marks the default active account. The reconciler picks the most recently connected account as the default. Per-project identity binding overrides this at session start via `gh auth switch`.

For hosts with only one account, the format is simplified (no `users` map needed — `gh` accepts both formats).

**GitLab token refresh:** GitLab access tokens expire in 2 hours. The control-plane runs a background goroutine that:
1. Queries `oauth_connections WHERE provider_type = 'gitlab' AND token_expires_at < NOW() + interval '30 minutes'`
2. For each, reads the refresh token from `user-secrets` K8s Secret (`conn-{connection_id}-refresh-token`)
3. Calls `POST {baseUrl}/oauth/token` with `grant_type=refresh_token`
4. Writes the new access token + refresh token to the K8s Secret
5. Updates `token_expires_at` in the DB with the new expiry (`NOW() + expires_in`)
6. Calls `reconcileUserCLIConfig(userId)` to update `gh-hosts.yml` / `glab-config.yml` with the new token
7. Runs every 15 minutes

Steps 4-6 are not atomic. If the K8s Secret write (step 4) succeeds but the DB update (step 5) fails, the DB retains the old expiry. On the next cycle, the goroutine will attempt an unnecessary refresh — but since the K8s Secret already has a valid refresh token (written in step 4), the extra refresh succeeds harmlessly (GitLab issues a new token pair). This is a benign race, not a data-loss scenario.

**On refresh failure (401 from token endpoint):** The goroutine re-reads the refresh token from K8s Secret (in case another goroutine cycle already refreshed it) and retries once. If the retry also returns 401, the refresh token is genuinely expired/revoked. The goroutine then: (1) deletes the `oauth_connections` row, (2) removes `conn-{connection_id}-token` and `conn-{connection_id}-refresh-token` from `user-secrets` Secret, (3) calls `reconcileUserCLIConfig(userId)`. Next `/auth/me` shows that GitLab identity disconnected.

**Provider API proxy:** Authenticated endpoints read `conn-{connection_id}-token` from user's K8s Secret, call the provider's API using the correct base URL, return results. The adapter translates between provider-specific API responses and a common format.

**Removed admin provider cleanup:** If an admin removes a provider instance from Helm values and restarts the control-plane, the startup reconcile detects orphaned `oauth_connections` rows (where `auth_type = 'oauth'` and `provider_id` no longer matches any Helm provider). For each orphan: deletes the `oauth_connections` row, removes the token keys from `user-secrets` Secret (best-effort, no revocation since the client config is gone), and calls `reconcileUserCLIConfig(userId)`. The next `/auth/me` call reflects the removal.

### SPA

**Settings screen additions:**
- "GIT PROVIDERS" section below ACCOUNT
- Lists all connected identities grouped by provider, with each identity showing username and auth type
- Admin OAuth — Connected: shows "GitHub: @rsong-work" + "Disconnect" button. Not connected: shows "Connect GitHub" button. Can connect again to add another identity.
- User PAT — shows "{displayName}: @{username} (PAT)" + "Remove" button
- "Add provider with PAT" button at bottom — opens form for base URL, display name, token
- "Connect GitHub" button available even when already connected (adds another identity)

**New Project sheet changes:**
- When any provider connected: show repo search input above manual URL field
- If multiple identities exist, show identity selector above the search: "Browse as @rsong-work on github.com" / "Browse as @rsong-personal on github.com" / "Browse as @rsong on gitlab.com". Each option maps to a `connection_id`.
- Repo picker auto-loads the first page when opened (empty `q`). Typing triggers search with 300ms debounce via `GET /api/connections/{connection_id}/repos?q=...`
- Results shown as a dropdown list (name, description, private badge, provider icon)
- Selecting a repo populates the git URL field and sets `git_identity_id` to the selected connection
- Manual URL field always visible below: "or enter URL manually" — `git_identity_id` stays null
- When no providers connected but admin providers exist (from `GET /api/providers`): show "Connect {displayName}" button for each admin provider, manual URL field below
- When no admin providers configured (empty `GET /api/providers` response): show only the manual URL field — no "Connect" buttons

**Auth client additions:**
- `GET /api/providers` returns admin-configured OAuth provider instances (from Helm)
- `GET /auth/me` response gains `connectedProviders` array (see Data Model section)
- New methods: `getConnectionRepos(connectionId, query)`, `disconnectConnection(connectionId)`, `updateProjectIdentity(projectId, connectionId)`

### Session-agent

**Credential helper setup (Go code in session-agent binary):**

The entire credential setup and initial clone happen inside the Go session-agent binary, NOT in `entrypoint.sh`. The entrypoint (`mclaude-session-agent/entrypoint.sh`) remains minimal: SSH key setup, env vars, home directory, then `exec session-agent`. The existing git clone/init block in `entrypoint.sh` (bare repo clone, scratch project init via plumbing commands) moves entirely to Go. The Go binary handles: credential helper setup → initial clone (or scratch init if no `GIT_URL`) → NATS connection → session lifecycle. This ensures the same credential logic applies everywhere — no split between bash and Go.

At session start, the session-agent sets up `gh` and `glab` as git credential helpers using the tokens from the K8s Secret mount. The key insight: `gh auth setup-git` registers `gh` as a git credential helper for a given host. When git needs credentials, it calls `gh auth git-credential`, which returns the active account's token. No custom scripts.

**Session start sequence (all in Go binary):**

1. **Symlink PVC config:** Remove any pre-existing `~/.config/` directory (`rm -rf ~/.config/` — safe because `$HOME` is an emptyDir, fresh each boot). If `/data/.config/` exists (PVC-persisted from prior session), symlink via `ln -s /data/.config/ ~/.config/`. If not, create `/data/.config/` then symlink. This preserves manual `gh auth login` / `glab auth login` across pod restarts.

2. **Merge managed tokens into CLI configs:**
   - Read `gh-hosts.yml` from Secret mount (`/home/node/.user-secrets/gh-hosts.yml`)
   - Read existing `~/.config/gh/hosts.yml` (may have entries from manual `gh auth login`)
   - **Merge strategy:** For each host in the Secret's `gh-hosts.yml`, add/update the managed accounts in the existing file. Do NOT remove accounts that are only in the existing file (those are from manual `gh auth login`). If a managed account and a manual account have the same username on the same host, the managed token wins (overwrite).
   - Same merge for `glab-config.yml` → `~/.config/glab-cli/config.yml`

3. **Register credential helpers:**
   - Run `gh auth setup-git` — registers `gh` as git's credential helper for all hosts in `~/.config/gh/hosts.yml`
   - Run `glab auth setup-git` — same for GitLab hosts

4. **Switch to project identity:** If `GIT_IDENTITY_ID` env var is set (from the MCProject CRD):
   - Parse `~/.config/gh/hosts.yml` to find which host has a `users:` entry matching this connection's username. The mapping from connection ID → username is embedded in the `gh-hosts.yml` via a YAML comment or looked up from the `conn-{id}-username` key in the Secret (see below).
   - Run `gh auth switch --user {username} --hostname {host}` (GitHub)
   - This makes `gh auth git-credential` return this specific account's token for this host

**Connection metadata keys:** The `conn-{connection_id}-username` keys in `user-secrets` are written by the same code path that writes the token — the OAuth callback handler and the PAT add handler both write `conn-{id}-token` and `conn-{id}-username` to the Secret in a single patch. The `reconcileUserCLIConfig` function also ensures these keys exist (backfill on startup, cleanup on disconnect). The session-agent reads `conn-{GIT_IDENTITY_ID}-username` to resolve the connection UUID to a username, then looks up which host in `hosts.yml` has that username to construct the `gh auth switch` command.

5. **Proceed with normal session setup** (clone if needed, NATS connection, etc.)

**Before each git operation (clone, fetch, push, worktree add):**

1. Re-read `gh-hosts.yml` and `glab-config.yml` from Secret mount (K8s auto-syncs ~1 min)
2. If the set of managed tokens has changed since last check, re-merge into `~/.config/` and re-run `gh auth setup-git` / `glab auth setup-git`
3. If the project has `git_identity_id`, ensure the correct account is active (`gh auth switch` if needed)
4. Run the git command — git automatically calls `gh auth git-credential` for HTTPS URLs

**SSH → HTTPS normalization:** Before git operations, if the URL is SCP-style (`git@{host}:{path}`) and a credential helper is registered for that host, normalize to HTTPS (`https://{host}/{path}`). Only SCP-style shorthand is normalized — `ssh://` scheme URLs are left as-is (they use SSH key auth). This is the only URL manipulation the agent does.

**No GIT_ASKPASS, no custom credential provider interface, no hostname matching, no per-provider username mapping.** The `gh` and `glab` CLIs handle all of this natively.

**Manual auth within sessions:** If a user (or Claude) runs `gh auth login` manually within a session, that auth is written to `~/.config/gh/hosts.yml` on the PVC. It survives pod restarts (PVC persistence) and is not overwritten by the merge step (merge adds managed accounts, doesn't remove manual ones). The manual auth is usable for git operations if `gh auth setup-git` was already run for that host.

**Error handling:** If a git operation fails with auth error (exit code 128 + stderr matching any of: `"Authentication failed"`, `"HTTP Basic: Access denied"`, `"Invalid username or password"`, `"could not read Username"`), session-agent publishes a `session_failed` event with reason `provider_auth_failed`. The detection is provider-agnostic — it checks for common auth failure strings across GitHub and GitLab.

**Dockerfile changes (`mclaude-session-agent/Dockerfile`):** Add `gh` and `glab` to the session-agent image as system dependencies (same layer as `git`, `bash`, `openssh-client`). Not via Nix — the Nix store lives on a PVC (`/nix/` mount) which overlays the image layer at runtime.

```dockerfile
# gh — available in Alpine community repo (github-cli package, pinned to 2.40+ for multi-account support)
# glab — not in Alpine repos; download binary from GitLab releases
# Note: curl is already installed for Claude CLI setup. The glab download must happen
# BEFORE the existing `apk del curl` cleanup step. Reorder the Dockerfile accordingly.
ARG TARGETARCH=arm64
RUN apk add --no-cache github-cli && \
    GLAB_VERSION="1.92.1" && \
    curl -fsSL "https://gitlab.com/gitlab-org/cli/-/releases/v${GLAB_VERSION}/downloads/glab_${GLAB_VERSION}_linux_${TARGETARCH}.tar.gz" | \
    tar xz --strip-components=1 -C /usr/local/bin bin/glab && \
    chmod +x /usr/local/bin/glab
```

`gh` requires version 2.40+ for the multi-account `users:` map in `hosts.yml`. The Alpine `github-cli` package in node:22-alpine (Alpine 3.20) ships gh 2.49+, which satisfies this. If the Alpine version falls behind, pin via `apk add github-cli~2.40`.

### Helm chart

**New values:** See "Instance configuration" section above. Each provider instance is an entry in `controlPlane.providers[]` with its own K8s Secret for the client secret.

**New Secrets (one per provider instance):**
- `github-oauth-secret` → `client-secret` key
- `ghes-oauth-secret` → `client-secret` key (if GHES configured)
- `gitlab-oauth-secret` → `client-secret` key

## Data Model

### K8s Secret: `user-secrets` (per user namespace)

Existing keys:
- `nats-creds` — NATS credentials file

New keys:
- `gh-hosts.yml` — reconciler-managed `gh` CLI hosts config (all GitHub-type connections for this user)
- `glab-config.yml` — reconciler-managed `glab` CLI config (all GitLab-type connections for this user)
- `conn-{connection_id}-token` — OAuth access token or PAT (one per connection, UUID-based key)
- `conn-{connection_id}-refresh-token` — OAuth refresh token (GitLab connections only)
- `conn-{connection_id}-username` — provider username for this connection (plain text, e.g., `rsong-work`). Used by session-agent to resolve `GIT_IDENTITY_ID` → username for `gh auth switch`.

### Postgres: `oauth_connections` table

See schema above. One row per identity (provider account) per user. Multiple rows can share the same `base_url` (multi-identity). No tokens — only metadata.

### Postgres: `projects` table (updated)

New column: `git_identity_id TEXT REFERENCES oauth_connections(id) ON DELETE SET NULL`. Links a project to a specific identity for git operations.

**Project response change:** The `ProjectKVState` (published to NATS KV for the SPA) gains a `gitIdentityId` field (nullable string). The SPA uses this to render the "Git Identity" dropdown in project settings and to show which identity is bound in the project list. The control-plane populates it from the DB when writing project state to KV.

### `/auth/me` response (updated — backward-compatible additive field)

```json
{
  "userId": "abc123",
  "email": "user@example.com",
  "name": "User",
  "connectedProviders": [
    {
      "connectionId": "uuid-1",
      "providerId": "github",
      "providerType": "github",
      "authType": "oauth",
      "displayName": "GitHub",
      "baseUrl": "https://github.com",
      "username": "rsong-work",
      "connectedAt": "2026-04-14T00:00:00Z"
    },
    {
      "connectionId": "uuid-2",
      "providerId": "github",
      "providerType": "github",
      "authType": "oauth",
      "displayName": "GitHub",
      "baseUrl": "https://github.com",
      "username": "rsong-personal",
      "connectedAt": "2026-04-14T01:00:00Z"
    },
    {
      "connectionId": "uuid-3",
      "providerId": "gitlab",
      "providerType": "gitlab",
      "authType": "oauth",
      "displayName": "GitLab",
      "baseUrl": "https://gitlab.com",
      "username": "rsong",
      "connectedAt": "2026-04-14T02:00:00Z"
    },
    {
      "connectionId": "uuid-4",
      "providerId": "pat",
      "providerType": "github",
      "authType": "pat",
      "displayName": "ACME GitHub",
      "baseUrl": "https://github.acme.com",
      "username": "rsong",
      "connectedAt": "2026-04-14T03:00:00Z"
    }
  ]
}
```

Each entry now includes `connectionId` (the UUID) and `baseUrl`. The SPA uses `connectionId` for repo browsing and disconnect. Multiple entries can share the same `providerId` and `baseUrl` (different accounts on the same host).

### `GET /api/providers` response

Returns admin OAuth providers (from Helm config only). Does not include user PAT connections — those appear in `/auth/me`'s `connectedProviders`. The SPA cross-references both responses to build the Settings UI: providers from this endpoint show "Connect"/"Reconnect" buttons, connections from `/auth/me` show "Disconnect"/"Remove" buttons.

```json
{
  "providers": [
    {"id": "github", "type": "github", "displayName": "GitHub", "baseUrl": "https://github.com", "source": "admin"},
    {"id": "company-ghes", "type": "github", "displayName": "ACME GitHub", "baseUrl": "https://github.acme.com", "source": "admin"},
    {"id": "gitlab", "type": "gitlab", "displayName": "GitLab", "baseUrl": "https://gitlab.com", "source": "admin"}
  ]
}
```

PAT-based connections are not listed here — they appear only in `/auth/me`'s `connectedProviders`. The providers endpoint lists available OAuth providers the user can connect to.

## Error Handling

| Scenario | Detection | User-facing |
|----------|-----------|-------------|
| User denies OAuth | Callback receives `error=access_denied` | Control-plane modifies the `return_url` from state: replaces `connected=true` with `error=denied` (e.g., `/?provider=github&error=denied&goto=settings`). SPA reads query params and shows error toast. If no return_url in state (state missing/expired), redirects to `{externalUrl}/?error=denied`. |
| Token revoked on provider | `git clone`/`fetch` fails with 401 via credential helper | Session marked `failed`, reason `provider_auth_failed`. Banner: "{displayName} access denied — reconnect in Settings" |
| Disconnect while operation in-flight | Token revoked mid-transfer (e.g., large clone) | In-flight git operation fails with auth error. Same handling as "token revoked" above. Accepted limitation — disconnect is a destructive action confirmed by the user. |
| Provider API down during repo listing | Proxy returns 502 | SPA shows "Couldn't load repos — try again" with retry button |
| K8s Secret missing/deleted | Control-plane can't read token | Proxy returns 404. SPA shows provider as not connected |
| State param mismatch (CSRF) | Control-plane rejects callback | Redirect to `{externalUrl}/?error=csrf`. SPA reads query params and shows toast. |
| GitLab refresh token expired | Refresh call returns 401 | Control-plane deletes connection. Next `/auth/me` shows GitLab identity disconnected. SPA shows "GitLab session expired — reconnect" |
| GitLab refresh race (two refreshes) | Second refresh uses stale refresh token | GitLab rotates refresh tokens on use — second call fails. Control-plane retries once with latest token from Secret. |
| Token write to K8s Secret fails | K8s API error on Secret patch (RBAC, namespace missing, API unavailable) | Callback redirects to return_url with `error=storage` replacing `connected=true`. No `oauth_connections` row created. User sees toast: "Failed to save credentials — try again." |
| DB write fails after token stored | K8s Secret write succeeded but `oauth_connections` INSERT failed | Control-plane cleans up: removes the token key from `user-secrets` Secret. Redirects to return_url with `error=storage`. If cleanup also fails, the orphaned token key is harmless (not referenced by any DB row, won't appear in CLI configs). |
| Token exchange fails | Provider returns non-200 on code→token exchange | Redirects to return_url with `error=exchange_failed`. User sees toast: "Failed to connect — try again." No token obtained, no DB row created. |
| Profile fetch fails after token exchange | Provider user API returns error after successful token exchange | Control-plane discards the obtained token (does not store it). Redirects to return_url with `error=profile_failed`. User retries; next attempt fetches profile again. |
| OAuth state lost (pod restart) | State map is empty after restart | Callback can't find state, redirects to `{externalUrl}/?error=csrf`. User retries connect flow. Accepted limitation of single-replica control-plane. |
| `gh auth setup-git` fails | Non-zero exit code at session start | Session-agent logs the error, proceeds without credential helper for that host. Git operations fall back to SSH key auth. Not a session-fatal error. |
| `gh auth switch` fails | Username not found in hosts.yml | Session-agent logs warning, uses the default active account. Git operations proceed with whichever account is currently active. |
| Identity deleted mid-session | Project's `git_identity_id` connection removed while session running | Secret mount updates (~1 min), next merge detects the managed account is gone. Credential helper falls back to whatever account is active. Next git operation may fail if no valid credential exists for the host — same as "token revoked" handling. |

## Security

- **Tokens never reach the browser.** Control-plane handles OAuth exchange and all provider API calls. SPA communicates only with control-plane.
- **Tokens stored in K8s Secret only.** Not in Postgres, not in logs, not in NATS messages.
- **OAuth state parameter** prevents CSRF. Random, single-use, 10-minute TTL. Includes provider ID.
- **GitHub scopes match `gh auth login`** (`repo read:org gist workflow read:user user:email`). Broad but expected — same access as the CLI.
- **GitLab scopes**: `api` (matches `glab auth login`). Full API access — clone, push, MRs, CI triggers, snippets, issues. Same principle as matching `gh` scopes for GitHub.
- **Revocation on disconnect** for all providers — tokens don't linger.
- **Per-user namespace isolation** — each user's tokens are in their own K8s namespace (`mclaude-{userId}`).
- **Refresh tokens** (GitLab) are stored alongside access tokens in the K8s Secret. Rotated on each refresh (GitLab's policy).
- **CLI config on PVC** — `~/.config/` is on the project PVC, not ephemeral. Tokens in `hosts.yml` are derived from the K8s Secret (same security boundary). PVC is per-project, so different projects cannot read each other's manually-added auth.
- **Credential helpers** — `gh auth git-credential` returns tokens only for configured hosts. No token leakage to arbitrary URLs.

## Prerequisite: User Namespace at User Creation

Currently, the user namespace (`mclaude-{userId}`) and `user-secrets` K8s Secret are created by the project reconciler when the first project is provisioned. This plan requires them to exist **before any project** so that OAuth tokens can be written immediately on connect.

**Change:** Move namespace + `user-secrets` Secret + `user-config` ConfigMap creation from the project reconciler into the user creation handler. Currently users are created via the admin-only `POST /admin/users` endpoint (`:9090`) and the dev bootstrap in `main.go`. Both paths must create:
1. Namespace `mclaude-{userId}`
2. `user-secrets` Secret in that namespace with `nats-creds` key
3. `user-config` ConfigMap (unchanged — still created empty for config-sync sidecar)

These resources are **unowned** (no OwnerReference to any MCProject CR). They are user-level resources that outlive any individual project. The project reconciler becomes idempotent — it checks that the namespace exists (it always will) rather than creating it. The `user-config` ConfigMap creation in the reconciler (currently `ensureOwned()`) is replaced with `ensureExists()` (create-if-not-found, no owner reference). The `user-secrets` Secret creation (currently a direct `Create` call in the reconciler) moves to the user creation handler — the reconciler only patches existing keys.

**Namespace creation is synchronous** in the user creation HTTP handler. If K8s namespace creation fails, the user creation request returns `500` and the `users` DB row is not committed (single transaction). The user retries. This is acceptable because namespace creation is a fast, local K8s operation.

**Note on RBAC ownership:** The ServiceAccount, Role, and RoleBinding in the reconciler currently use `ensureOwned()` with MCProject OwnerReferences. This is a pre-existing issue (if one project is deleted, cascading deletes break other projects in the same namespace). This design does not change RBAC ownership — that's a separate fix. The prerequisite only moves namespace + Secret + ConfigMap creation.

**CLI config reconciliation:** The reconciler ensures the `gh-hosts.yml` and `glab-config.yml` keys in `user-secrets` are always up to date. It builds them from all `oauth_connections` rows for the user, organized by host and formatted for the respective CLI's config format.

**Trigger mechanism:** The HTTP handlers that modify `oauth_connections` (connect, disconnect, add PAT, remove PAT) call `reconcileUserCLIConfig(userId)` directly after the DB write succeeds. This is a synchronous function call within the same request — not a watch, not a queue, not polling. Additionally, the reconciler runs for all users on control-plane startup (handles token refresh and backfills existing users).

**Startup reconcile failure semantics:** Best-effort with timeout. Each user's reconcile gets a 10-second timeout. Failures (unreachable GitLab instance, K8s API error) are logged as warnings but do not block startup. The control-plane becomes ready regardless. Stale GitLab tokens are retried on the next 15-minute refresh cycle. GitHub tokens don't expire, so startup reconcile for GitHub users is just CLI config rebuilding (no external calls).

**Secret mount:** The existing `user-secrets` Secret is mounted as a full volume mount (no `items` projection) — all keys automatically appear as files in the mount path. New keys (`gh-hosts.yml`, `glab-config.yml`, `conn-*-token`) are automatically visible to pods without Helm chart changes to the volume mount spec.

For existing users (created before this feature), the startup reconcile adds the CLI config keys to their `user-secrets` Secret if missing.

**DB migration:** The `oauth_connections` table and `projects.git_identity_id` column are added to the `schema` constant in `db.go` as `CREATE TABLE IF NOT EXISTS` and `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` statements — the same pattern used for the `users` and `projects` tables. The schema is applied at startup via `Migrate()`. No external migration tool is used.

**`oauth_connections.id`:** Generated as a UUID (same pattern as `users.id` and `projects.id`).

## Scope

### v1 (this plan)
- GitHub OAuth App (github.com, GHES, GHEC) for repo access + CLI auth
- GitLab OAuth App (gitlab.com + self-hosted) with refresh token handling
- User PAT support for any GitHub/GitLab instance (runtime-addable, no Helm change needed)
- **Multi-identity:** multiple accounts per hostname, per-project identity binding
- Credential helpers via `gh auth setup-git` / `glab auth setup-git` (no custom GIT_ASKPASS)
- `gh` (apk, 2.40+ for multi-account) and `glab` (binary download) baked into session image — not Nix (PVC mount hides image-layer `/nix/`)
- PVC-persisted `~/.config/` — manual `gh auth login` survives pod restarts
- Merge-not-overwrite for CLI configs — managed tokens added alongside manual auth
- Provider-agnostic architecture: instance config, endpoints, credential helpers
- Connect/disconnect per identity in Settings
- "Add provider with PAT" in Settings
- Inline "Connect {provider}" prompt in New Project
- Repo picker with search and identity selector
- Identity selector in repo picker ("Browse as @username on host")
- Per-project identity binding via `git_identity_id`
- SSH → HTTPS normalization for SCP-style URLs
- `oauth_connections` DB table for metadata (OAuth + PAT, multi-identity)
- Token keys as `conn-{uuid}-token` in K8s Secret
- CLI config keys (`gh-hosts.yml`, `glab-config.yml`) in K8s Secret (reconciler-managed)
- Auth error surfacing (session status + banner)
- GitLab token refresh (background goroutine in control-plane)

### v2 (deferred)
- SSO login: "Sign in with GitHub/GitLab" on auth screen
- Sign up via provider (auto-create user from profile)
- Multi-provider account linking (user associates multiple providers, login with any)
- Bitbucket adapter
- Azure DevOps adapter (Entra ID, header-based auth)
- Dedicated secret storage (Vault or similar) replacing K8s Secrets
