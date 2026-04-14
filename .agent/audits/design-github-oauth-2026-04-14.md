## Audit: 2026-04-14T00:00:00Z

**Document:** docs/plan-github-oauth.md

### Round 1

**Gaps found: 7**

1. **git_identity_id delivery unspecified** — no mechanism for how the value reaches the session-agent pod (env var? API call? annotation?)
2. **glab not in Alpine repos** — `apk add glab` will fail; glab is distributed as a standalone binary
3. **glab multi-account not supported** — glab CLI has no `auth switch` command or `users:` map; multi-identity on same GitLab host is ambiguous
4. **gh minimum version not pinned** — multi-account `users:` map requires gh 2.40+; no version requirement stated
5. **MCProjectSpec CRD not updated** — `git_identity_id` has no path from Postgres to pod
6. **Hash fragment in OAuth redirect** — HTTP redirects discard fragments; mechanism to deliver `#?...` params unspecified (SPA uses hash routing)
7. **Startup reconcile failure semantics** — no timeout/concurrency/error-tolerance defined for GitLab refresh on startup

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | git_identity_id delivery | Added `GitIdentityID` to MCProjectSpec, `GIT_IDENTITY_ID` env var on Deployment, `conn-{id}-username` metadata key in Secret | factual |
| 2 | glab not in Alpine repos | Changed to binary download from GitLab releases with `--strip-components` for correct tar extraction | factual |
| 3 | glab multi-account | User chose: one identity per GitLab host. Enforced at API level, not DB constraint. | decision |
| 4 | gh minimum version | Added note: Alpine community repo gh 2.49+ on node:22-alpine (Alpine 3.20) satisfies 2.40+ requirement | factual |
| 5 | MCProjectSpec CRD | Specified CRD field addition + reconciler env var propagation | factual |
| 6 | Hash fragment redirect | Changed to query params (`/?provider=github&connected=true&goto=settings`). SPA reads query params on load, navigates to hash route, cleans via `history.replaceState` | factual |
| 7 | Startup reconcile failure | User chose: best-effort with 10s timeout per user. Failures logged, control-plane becomes ready regardless. | decision |

### Round 2

**Gaps found: 13**

1. **GET /api/providers contradicts description** — endpoint table says "admin + user connections" but response example and clarifying text show admin-only
2. **PAT provider_id + OAuth/PAT conflict** — unclear if user can have both OAuth and PAT for same base_url + provider_user_id
3. **Credential setup timing vs entrypoint.sh** — doc says "Go code" but current entrypoint.sh handles clone in bash
4. **glab binary architecture hardcoded** — `linux_arm64` hardcoded, no multi-arch support
5. **CreateProject missing gitIdentityId** — NATS message schema and DB function don't accept the new field
6. **ensureOwned reference inaccurate** — doc says `user-secrets` uses `ensureOwned()` but code uses direct `Create`
7. **User creation namespace error handling** — synchronous vs async, rollback on failure unspecified
8. **SPA JWT for HTTP endpoints** — different transport path than NATS-based APIs, not acknowledged
9. **Stale GIT_IDENTITY_ID in CRD after disconnect** — cascade deletes DB row but CRD still has old value
10. **returnUrl validation incomplete** — query params not validated, open to injection
11. **Connection repos authorization** — ownership check implied but not explicit
12. **PATCH /api/projects/{id} not in endpoint table** — referenced in user flow but not defined
13. **PAT duplicate handling inconsistency** — OAuth upserts, PAT rejects on conflict, no explanation

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | GET /api/providers | Fixed description to "admin OAuth providers only". Added clarifying text about cross-referencing with /auth/me | factual |
| 2 | OAuth/PAT coexistence | Added section: both can coexist if different provider_user_id. Same account = UNIQUE conflict. PAT update = remove + re-add. | factual |
| 3 | Credential setup timing | Clarified entire clone logic moves from entrypoint.sh to Go binary. Entrypoint is minimal: SSH + env + exec. | factual |
| 4 | glab architecture | Added `ARG TARGETARCH=arm64` to Dockerfile snippet | factual |
| 5 | CreateProject | Added NATS message schema change + validation on create (same as PATCH) | factual |
| 6 | ensureOwned reference | Fixed: `user-config` uses ensureOwned (→ ensureExists), `user-secrets` uses direct Create (→ moves to user handler). Noted RBAC ownership as pre-existing issue. | factual |
| 7 | Namespace error handling | Specified: synchronous, fail together (DB rollback if K8s fails), 500 on error | factual |
| 8 | SPA JWT | Added note: SPA stores JWT from login, sends as Bearer for HTTP. Provider endpoints are HTTP (not NATS) because redirects/proxies don't fit pub/sub. | factual |
| 9 | Stale GIT_IDENTITY_ID | Added: control-plane reconciles affected MCProject CRDs on disconnect. Session-agent checks for missing username key and falls back. | factual |
| 10 | returnUrl validation | Tightened: must start with `/`, no `://`, query params limited to allowlist (provider, connected, goto, error), others stripped | factual |
| 11 | Authorization check | Made explicit: "Verifies JWT user matches connection's user_id" on repos and disconnect endpoints | factual |
| 12 | PATCH endpoint | Added to endpoint table with full spec: body, validation, DB+CRD+KV update | factual |
| 13 | PAT duplicate handling | Added PAT update flow section: remove + re-add is intentional, no in-place update | factual |

### Round 3

**Gaps found: 11**

1. **Entrypoint git clone migration incomplete** — scratch project init (bare repo plumbing commands) not mentioned in Go migration
2. **EXTERNAL_URL enforcement mechanism** — required but no spec for how (Helm `required`, Go startup check, or both)
3. **Two Dockerfiles** — which one (`mclaude-session-agent/` vs `mclaude-session/`) gets modified
4. **curl deletion ordering** — glab download needs curl but existing Dockerfile deletes it
5. **providers.json ConfigMap** — no name, no Helm template spec, no volume mount spec
6. **Error reason mismatch** — decisions table says `github_auth_failed`, error handling says `provider_auth_failed`
7. **goto=new-project timing** — New Project sheet needs auth + data load before opening; query params read on page load
8. **PATCH null vs absent semantics** — does omitting `gitIdentityId` mean "don't change" or "clear"?
9. **Admin port number wrong** — doc says `:9091`, Helm values say `adminPort: 9090`
10. **reconcileUserCLIConfig concurrency** — concurrent OAuth callbacks could race on Secret read-modify-write
11. **OAuth callback doesn't mention username key** — callback flow steps only mention writing token, not username

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | Clone migration | Clarified: entire block (clone + scratch init) moves to Go. Go handles credential setup → clone/init → NATS → lifecycle | factual |
| 2 | EXTERNAL_URL enforcement | Specified: Go startup fatal exit + Helm template `required` function. Both enforce. | factual |
| 3 | Two Dockerfiles | Added: "Target Dockerfile: `mclaude-session-agent/Dockerfile`" | factual |
| 4 | curl ordering | Added note: glab download must happen before `apk del curl`. Reorder accordingly. | factual |
| 5 | providers.json ConfigMap | Specified: `{release}-provider-config` ConfigMap, mounted at `/etc/mclaude/providers.json`. Empty = starts normally, returns empty lists. | factual |
| 6 | Error reason mismatch | Fixed decisions table to `provider_auth_failed` (matches error handling and session-agent sections) | factual |
| 7 | goto=new-project timing | Specified: SPA stores params in memory, cleans query string, defers action until auth + initial data load complete | factual |
| 8 | PATCH null semantics | Specified: explicit `null` clears binding, omitting field is no-op | factual |
| 9 | Admin port | Fixed `:9091` → `:9090` | factual |
| 10 | Reconcile concurrency | Added: `resourceVersion`-based optimistic concurrency on Secret patch, retry once on 409 | factual |
| 11 | Callback username key | Added `conn-{id}-username` to callback step 11 (single patch with token + username + refresh token) | factual |

### Round 4

**Gaps found: 5**

1. **NATS KV not updated on PATCH** — PATCH handler updates DB + CRD but SPA reads from KV; ProjectKVState would be stale
2. **gitIdentityId validation on create** — PATCH validates but create doesn't mention validation
3. **GIT_IDENTITY_ID env var when NULL** — not specified whether omitted or set to empty string
4. **glab tarball extraction path wrong** — binary is at `bin/glab` in tarball, not at root
5. **~/.config/ symlink conflict** — pre-existing directory would cause EEXIST on symlink

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | NATS KV on PATCH | Added: PATCH handler writes updated ProjectKVState to NATS KV | factual |
| 2 | Create validation | Added: same validation as PATCH (connection belongs to user, hostname matches gitUrl) | factual |
| 3 | GIT_IDENTITY_ID when NULL | Specified: env var omitted from pod spec entirely when empty. Session-agent checks `os.Getenv != ""` | factual |
| 4 | glab tarball path | Fixed: `tar xz --strip-components=1 -C /usr/local/bin bin/glab` | factual |
| 5 | ~/.config/ symlink | Added: `rm -rf ~/.config/` first (safe — emptyDir, fresh each boot), then `ln -s` | factual |

### Round 5

CLEAN — no blocking gaps found.

### Result

**CLEAN** after 5 rounds, 36 total gaps resolved (34 factual fixes, 2 design decisions).

Design decisions made:
1. **GitLab multi-identity**: One identity per GitLab host (glab doesn't support multi-account switching)
2. **Startup reconcile failure**: Best-effort with 10s timeout per user (don't block platform on unreachable GitLab)
