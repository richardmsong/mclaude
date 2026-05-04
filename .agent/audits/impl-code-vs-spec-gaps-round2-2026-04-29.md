# Code vs Spec Audit — Round 2

**Date:** 2026-04-29
**Scope:** Exhaustive cross-reference of production code against all 6 spec files
**Prior round:** 26 gaps found and fixed in round 1

---

## Gaps Found

### GAP 1: `gen-leaf-creds` subcommand not documented in spec
- **Code location:** `mclaude-control-plane/main.go` lines 20–23 — routes `os.Args[1] == "gen-leaf-creds"` to `runGenLeafCreds()`
- **Spec location:** `docs/mclaude-control-plane/spec-control-plane.md` (Deployment / Internal Behavior)
- **Detail:** The spec documents `init-keys` as the only Helm pre-install subcommand. The code also has `gen-leaf-creds` which is used by the mclaude-worker chart's pre-install Job, but the spec does not mention this subcommand at all.

### GAP 2: `/readyz` does not check Postgres connectivity
- **Code location:** `mclaude-control-plane/server.go` — `/readyz` handler just returns `200 OK`
- **Spec location:** `docs/mclaude-control-plane/spec-control-plane.md` (HTTP Endpoints — Main Port)
- **Detail:** Spec says "`GET /readyz` — Kubernetes readiness probe (checks Postgres connectivity only — NATS outage must not mark pod unready)". The code does NOT check Postgres — it's identical to `/healthz` (unconditional 200).

### GAP 3: Admin routes served on loopback port, spec says main port
- **Code location:** `mclaude-control-plane/server.go` — `AdminMux()` bound to `127.0.0.1:adminPort`; `RegisterRoutes()` has no `/admin/` routes
- **Spec location:** `docs/mclaude-control-plane/spec-control-plane.md` (HTTP Endpoints sections)
- **Detail:** Spec explicitly says admin endpoints live on the main port with per-user bearer-token + `is_admin` check: "The previous `/admin/*` endpoints have moved to the main port (above)". The loopback port section says "The loopback-only port hosts only Prometheus metrics." Code has the opposite: admin is loopback-only, main port has no admin routes.

### GAP 4: `POST /admin/users/{uslug}/promote` not implemented
- **Code location:** `mclaude-control-plane/admin.go` `handleAdminRoutes` — no promote case
- **Spec location:** `docs/mclaude-control-plane/spec-control-plane.md` (Admin-only endpoints)
- **Detail:** Spec lists `POST /admin/users/{uslug}/promote` — "Sets `users.is_admin = true`". The admin route handler has no case for this path. `PromoteAdmin` only exists in `init_keys.go` (startup bootstrap), not as an HTTP endpoint.

### GAP 5: `DELETE /admin/clusters/{cslug}` not implemented
- **Code location:** `mclaude-control-plane/admin.go` `handleAdminRoutes` — no delete cluster case
- **Spec location:** `docs/mclaude-control-plane/spec-control-plane.md` (Admin-only endpoints)
- **Detail:** Spec lists `DELETE /admin/clusters/{cslug}` — "Removes the cluster — deletes all `hosts` rows where `slug=cslug AND type='cluster'`". No handler exists in the code.

### GAP 6: Cluster register response omits `accountJwt` and `operatorJwt`
- **Code location:** `mclaude-control-plane/admin.go` `adminRegisterCluster` — response fields left empty
- **Spec location:** `docs/mclaude-control-plane/spec-control-plane.md` (Admin-only endpoints, POST /admin/clusters)
- **Detail:** Spec says response includes `{slug, leafJwt, leafSeed, accountJwt, operatorJwt, jsDomain, directNatsUrl}`. The code defines the struct with `AccountJWT` and `OperatorJWT` fields but never populates them — they're always empty string. The admin needs these to deploy the worker cluster's NATS trust chain.

### GAP 7: `adminGrantCluster` looks up user by email instead of slug
- **Code location:** `mclaude-control-plane/admin.go` `adminGrantCluster` line: `user, err := s.db.GetUserByEmail(r.Context(), req.UserSlug)`
- **Spec location:** `docs/mclaude-control-plane/spec-control-plane.md` (Admin-only endpoints, POST /admin/clusters/{cslug}/grants)
- **Detail:** Spec says request body is `{userSlug}` and the handler should look up by slug. Code passes `req.UserSlug` to `GetUserByEmail()`, using the slug as if it were an email address. Should call `GetUserBySlug()`.

### GAP 8: `IssueHostJWT` in admin grant uses user.ID (UUID) instead of user.Slug
- **Code location:** `mclaude-control-plane/admin.go` `adminGrantCluster` — `IssueHostJWT(user.ID, cslug, s.accountKP)`
- **Spec location:** `docs/mclaude-control-plane/spec-control-plane.md` and `docs/spec-state-schema.md` (Per-host user JWT permissions)
- **Detail:** Spec says the per-user JWT for a cluster grant should be scoped to `mclaude.users.{userSlug}.hosts.{cslug}.>`. The code passes `user.ID` (UUID) as the uslug parameter, producing `mclaude.users.{UUID}.hosts.{cslug}.>` instead of slug-based scoping.

### GAP 9: HTTP project CRUD endpoints not implemented
- **Code location:** `mclaude-control-plane/server.go` + `hosts.go` — `handleHostRoutes` returns 404 for non-`hosts` paths; no project HTTP handlers exist
- **Spec location:** `docs/mclaude-control-plane/spec-control-plane.md` (Protected HTTP endpoints)
- **Detail:** Spec lists `POST /api/users/{uslug}/projects`, `GET /api/users/{uslug}/projects`, `GET /api/users/{uslug}/projects/{pslug}`, `DELETE /api/users/{uslug}/projects/{pslug}` as HTTP endpoints. None exist in the code — project creation only works via NATS `mclaude.users.*.api.projects.create`. Grep for `/api/users.*project` returns zero matches.

### GAP 10: SCIM 2.0 endpoints not implemented
- **Code location:** `mclaude-control-plane/server.go` `RegisterRoutes` — no `/scim/` routes
- **Spec location:** `docs/mclaude-control-plane/spec-control-plane.md` (SCIM 2.0 section)
- **Detail:** Spec documents `POST /scim/v2/Users`, `PUT /scim/v2/Users/{id}`, `DELETE /scim/v2/Users/{id}`, `GET /scim/v2/Users`. No SCIM routes are registered. The word "SCIM" only appears in a file-level comment.

### GAP 11: `/api/hosts/register` is unprotected, spec says protected
- **Code location:** `mclaude-control-plane/server.go` line: `mux.HandleFunc("/api/hosts/register", s.handleHostRegister)` — uses `HandleFunc` (no auth middleware)
- **Spec location:** `docs/mclaude-control-plane/spec-control-plane.md` (Protected HTTP endpoints)
- **Detail:** Spec lists `POST /api/hosts/register` under "Protected (per-host NATS JWT or admin bearer token)". The code registers it directly on the mux without `s.authMiddleware()`. The device-code redemption flow may intentionally skip auth (the code validates the device code), but it contradicts the spec's classification.

### GAP 12: `handleRefresh` returns internal NATS URL instead of external WebSocket URL
- **Code location:** `mclaude-control-plane/auth.go` `handleRefresh` — `NATSUrl: s.natsURL` (internal)
- **Spec location:** `docs/mclaude-control-plane/spec-control-plane.md` (Authentication / POST /auth/refresh)
- **Detail:** `handleLogin` correctly returns `s.natsWsURL` (external WebSocket URL for browser clients). `handleRefresh` returns `s.natsURL` (internal broker URL like `nats://localhost:4222`). The SPA would get an unusable URL on JWT refresh.

### GAP 13: Obsolete `mclaude.clusters.*` subjects in SPA code
- **Code location:** `mclaude-web/src/lib/subj.ts` — `subjClusterProvision()` and `subjClusterStatus()` produce `mclaude.clusters.{cslug}.api.projects.provision` and `mclaude.clusters.{cslug}.api.status`
- **Spec location:** `docs/spec-state-schema.md` (NATS Subjects)
- **Detail:** Spec explicitly states "There are **no** `mclaude.clusters.{cslug}.>` subjects. Cluster-scoped routing is achieved by user-level wildcards on host-scoped subjects." These two functions produce subjects the spec says don't exist.

### GAP 14: Obsolete `kvKeyUserClusters` for removed KV bucket
- **Code location:** `mclaude-web/src/lib/subj.ts` — `kvKeyUserClusters()` helper
- **Spec location:** `docs/spec-state-schema.md` (NATS KV Buckets)
- **Detail:** Spec says "The previous `mclaude-clusters` KV is removed." The `kvKeyUserClusters()` function still exists, referencing a bucket that the spec says is deleted.

### GAP 15: `reloadPlugins` control request not documented
- **Code location:** `mclaude-web/src/viewmodels/conversation-vm.ts` `reloadPlugins()` — sends `{subtype: 'reload_plugins'}`
- **Spec location:** `docs/mclaude-web/spec-web.md` (Model/Effort Switching)
- **Detail:** Spec's "Model/Effort Switching" section documents `set_model` and `set_max_thinking_tokens` control requests. `reload_plugins` is not mentioned anywhere in any spec.

### GAP 16: RBAC Role grants Secret access, spec only documents ConfigMap access
- **Code location:** `mclaude-controller-k8s/reconciler.go` `reconcileRBAC` — Role rules include `{Resources: ["secrets"], ResourceNames: ["user-secrets"], Verbs: ["get"]}`
- **Spec location:** `docs/spec-state-schema.md` (RBAC: ServiceAccount, Role, RoleBinding)
- **Detail:** Spec says "Role: `mclaude-role` — allows get/watch on ConfigMaps (for config reload)". Code additionally grants `get` on the `user-secrets` Secret. The Secret access is not documented.

### GAP 17: Controller spec says deployment name is `mclaude-session-agent-{projectId}`
- **Code location:** `mclaude-controller-k8s/reconciler.go` — `deployName := "project-" + projectID`
- **Spec location:** `docs/mclaude-controller/spec-controller.md` (Kubernetes resources summary)
- **Detail:** Controller spec summary lists "Per-project `mclaude-session-agent-{projectId}` Deployment". Code and state-schema both use `project-{projectId}`. The controller spec has a stale name.

### GAP 18: Desktop notifications not documented in web spec
- **Code location:** `mclaude-web/src/components/App.tsx` — requests `Notification.requestPermission()` and fires `new Notification(...)` on `requires_action` transitions
- **Spec location:** `docs/mclaude-web/spec-web.md`
- **Detail:** The SPA requests browser notification permission on first connect and fires desktop notifications when a session transitions to `requires_action` while the tab is not visible. This behavior is not documented in the web spec.

### GAP 19: Project filter with localStorage persistence not documented
- **Code location:** `mclaude-web/src/viewmodels/session-list-vm.ts` — `filterProjectId`, `setFilter()`, `resolveFilter()` using localStorage key `mclaude.filterProjectId`
- **Spec location:** `docs/mclaude-web/spec-web.md`
- **Detail:** The SPA supports filtering the dashboard by project ID, persisted in localStorage. This feature is not documented in the web spec.

---

## Summary

**19 gaps found** across 5 components:

| Component | Gaps | IDs |
|-----------|------|-----|
| Control-plane | 10 | 1–12 |
| Controller-k8s | 2 | 16, 17 |
| Session-agent | 0 | — |
| Web SPA | 5 | 13–15, 18, 19 |
| Helm charts | 0 | — |

**Severity breakdown:**
- **High** (code behavior contradicts spec): 3, 6, 7, 8, 12
- **Medium** (spec documents something code doesn't have): 2, 4, 5, 9, 10, 11
- **Low** (code has undocumented features): 1, 13, 14, 15, 16, 17, 18, 19

**Session-agent is clean** — all code behaviors match spec documentation.
**Helm charts are clean** — values and templates match spec-helm.md.
