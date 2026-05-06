## Run: 2026-05-06T00:00:00Z

### ADR: docs/adr-0101-remove-admin-http-routes.md
### Specs checked: docs/mclaude-control-plane/spec-control-plane.md, docs/spec-state-schema.md
### Cross-spec check: all docs/**/spec-*.md

---

## Phase 1 — Forward Pass (ADR → Spec)

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| Decisions table, row 1 | "Remove /admin/* routes — Yes — all of them" | spec-control-plane.md:327 — "Requests to `/admin/*` or `/scim/v2/*`: return 404 (routes not registered, ADR-0101)" | REFLECTED | — | Error handling section clearly states 404 for admin routes |
| Decisions table, row 2 | "Remove /scim/v2/* stubs — Yes — delete scim.go entirely" | spec-control-plane.md:327 — same line covers scim/v2/* → 404 | REFLECTED | — | Covered alongside admin route removal |
| Decisions table, row 3 | "Remove `isAdminMiddleware` — Yes, no routes remain behind it" | spec-control-plane.md — no mention of isAdminMiddleware remaining; the HTTP routes table has no admin section; error handling confirms 404. No present-tense statement of isAdminMiddleware existing or being retained. | REFLECTED | — | Silence is correct here; spec does not describe deleted internals |
| Decisions table, row 4 | "`is_admin` DB column — Retain — still gates `defaultPlatform=true` in host registration (ADR-0097) and admin-as-operator bypass in NATS lifecycle handlers" | spec-state-schema.md:24 — "`is_admin` BOOLEAN … Gates `defaultPlatform=true` flag on host registration (ADR-0097) and admin-as-operator bypass in NATS lifecycle handlers (`manage.deregister`, `manage.revoke`, `manage.update`). No HTTP admin routes exist (ADR-0101). Set TRUE by the `init-keys` Helm Job for `BOOTSTRAP_ADMIN_EMAIL`; no HTTP promotion endpoint." | REFLECTED | — | Fully matches the ADR decision |
| Decisions table, row 5 | "Admin bypass in auth middleware slug-match (auth.go:381) — Remove — vestigial with no admin routes" | spec-control-plane.md:262 — "No admin bypass — `is_admin=true` users are subject to the same slug-match check as any other user (ADR-0101)." | REFLECTED | — | Present-tense statement in Internal Behavior / Authentication section |
| Decisions table, row 6 | "`requireSelfOrAdmin` in hosts.go (grants/accept/pubkey endpoints) — Remove — self-only access enforcement" | spec-control-plane.md:94-96 — `GET /api/users/{uslug}/grants`, `POST /api/users/{uslug}/hosts/{hslug}/grant/accept`, `GET /api/users/{uslug}/hosts/{hslug}/user-agent/pubkey` all say "Auth: self." No mention of admin bypass. | REFLECTED | — | All three endpoints already say self-only auth |
| Decisions table, row 7 | "`is_admin` gates in NATS lifecycle handlers (manage.deregister, manage.revoke, manage.update) — Retain — platform operator tools" | spec-control-plane.md:144 — "Authorization: host owner or platform operator" for manage.deregister; spec-state-schema.md:24 lists the three NATS handlers explicitly as the retained is_admin gates | REFLECTED | — | Both specs correctly reflect the retain decision |
| Decisions table, row 8 | "Integration tests for removed routes — Delete test cases; add regression tests asserting 404" | spec-control-plane.md:347-353 — Admin/SCIM surface removed section lists `TestAdminRoutes_AllReturn404`, `TestAuthMiddleware_AdminBypassRemoved`, and `TestRequireSelfOrAdmin_AdminCannotAccessOtherUserGrants` | REFLECTED | — | All three regression tests from the ADR Integration Test Cases table (ADR lines 106-108) are present in the spec |
| Component Changes — server/server.go | "remove `mux.Handle('/admin/', ...)`, `mux.Handle('/scim/v2/', ...')`, and `isAdminMiddleware` method" | spec-control-plane.md:327 — 404 for admin/scim routes; no admin section in HTTP endpoints table | REFLECTED | — | Spec does not describe implementation file internals; behavioral outcome is captured |
| Component Changes — server/auth.go | "remove admin bypass block (~line 381): the `if !user.IsAdmin { return }` check inside the `/api/users/{uslug}/...` slug-match enforcement" | spec-control-plane.md:262 — "No admin bypass — `is_admin=true` users are subject to the same slug-match check as any other user (ADR-0101)" | REFLECTED | — | |
| Component Changes — server/hosts.go | "remove `requireSelfOrAdmin` function … update its three callers … to use self-only access enforcement" | spec-control-plane.md:94-96 — grants, grant/accept, pubkey endpoints say "Auth: self" | REFLECTED | — | |
| Error Handling | "Requests to `/admin/*` or `/scim/v2/*` fall through to the default mux handler and return 404." | spec-control-plane.md:327 — exact match | REFLECTED | — | |
| Impact — spec-state-schema.md `is_admin` | "update `is_admin` column description (no longer gates HTTP admin routes; gates NATS lifecycle admin bypass and defaultPlatform registration)" | spec-state-schema.md:24 — "No HTTP admin routes exist (ADR-0101)" and lists the two retained gates | REFLECTED | — | |
| Impact — spec-state-schema.md `nkey_public` | "update `nkey_public` column description (remove reference to `POST /admin/users`)" | spec-state-schema.md:23 — nkey_public description: "Required in `POST /auth/login` request body (ADR-0100) — stored on first login…" — no mention of `POST /admin/users`. | REFLECTED | — | The referenced stale text is already absent |
| Impact — spec-control-plane.md Phase 1 "What works" | spec-control-plane.md:23 — "Additional users must be created via `BOOTSTRAP_ADMIN_EMAIL`/`BOOTSTRAP_ADMIN_PASSWORD` in Helm values (ADR-0101)." | REFLECTED | — | Correctly attributes user creation to bootstrap env vars, not HTTP admin route |

---

## Phase 0b — Cross-spec consistency check

Shared concepts touched by this ADR: `POST /admin/users`, `DELETE /admin/users/{userId}`, `/admin/*` routes, `/scim/v2/*` routes, `AdminUserRequest`, `requireSelfOrAdmin`, ingress routing rules for `/admin` and `/scim`.

**spec-web.md** (lines 258, 269): Now correctly states "No ephemeral user is created — `POST /admin/users` is removed (ADR-0101)" and "No user deletion is performed — `DELETE /admin/users/{userId}` is removed (ADR-0101)". CLEAN.

**spec-cli.md** (lines 232, 244): Now correctly states "No ephemeral user is created — `POST /admin/users` is removed (ADR-0101)" and "No user deletion — the bootstrap admin is a permanent account and `DELETE /admin/users/{userId}` is removed (ADR-0101)". CLEAN.

**spec-helm.md** (line 93): Ingress description still routes `/admin` and `/scim` to the control-plane — "Routes `/auth`, `/api`, `/admin`, `/scim` to control-plane". These routes no longer exist in the CP after ADR-0101. The Ingress rule routing `/admin/*` and `/scim/*` to the CP is dead routing. This is a GAP — a developer reading spec-helm.md would include `/admin` and `/scim` path rules in the Ingress template when those routes should be removed.

---

## Findings Table (cross-spec)

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| Decisions row 1 + row 2 (remove /admin/* and /scim/v2/*) | All `/admin/*` and `/scim/v2/*` routes removed | spec-helm.md:93 — "Routes `/auth`, `/api`, `/admin`, `/scim` to control-plane" | GAP | SPEC→FIX | The Helm Ingress spec still lists `/admin` and `/scim` as routed paths to the control-plane. These path rules should be removed from the Ingress description. The ADR's Impact section does not list spec-helm.md but the route removal is a shared concept that propagates to Ingress configuration. |

---

### Summary

- Reflected: 15
- Gap: 1
- Partial: 0

---

## Run: 2026-05-06T12:00:00Z (re-evaluation after spec updates)

**Note:** The prior run (00:00:00Z) found 2 GAPs in spec-web.md and spec-cli.md plus 1 PARTIAL in spec-control-plane.md. The specs have since been updated: spec-web.md and spec-cli.md now correctly reflect the route removals (ADR-0101 cited inline), and spec-control-plane.md now lists all three regression tests including `TestRequireSelfOrAdmin_AdminCannotAccessOtherUserGrants`. Those findings are now CLEAN. One new GAP was found in spec-helm.md (not listed in the ADR's Impact section) during cross-spec review.

**Current status: 1 GAP remaining.**

GAP [SPEC→FIX]: "Remove /admin/* routes — Yes — all of them" + "Remove /scim/v2/* stubs" → spec-helm.md:93 still lists `/admin` and `/scim` as Ingress routes to the control-plane. The spec should remove these two path prefixes from the Ingress rule description. (spec-helm.md is not listed in the ADR Impact section but is a downstream consumer of the route removal.)

## Run: 2026-05-06T20:00:00Z (third evaluation)

### ADR: docs/adr-0101-remove-admin-http-routes.md
### Specs checked: docs/mclaude-control-plane/spec-control-plane.md, docs/spec-state-schema.md, docs/mclaude-web/spec-web.md, docs/mclaude-cli/spec-cli.md, docs/charts-mclaude/spec-helm.md
### Cross-spec check: all docs/**/spec-*.md

---

The GAP from the prior run (spec-helm.md:93 routing /admin and /scim to CP) has been fixed. spec-helm.md:93 now reads: "Routes `/auth`, `/api` to control-plane; `/` to SPA. Created when `ingress.enabled`. `/admin` and `/scim` paths are removed (ADR-0101)."

## Phase 1 — Forward Pass (ADR → Spec)

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| Decisions row 1 | "Remove /admin/* routes — Yes — all of them" | spec-control-plane.md:327 — "Requests to `/admin/*` or `/scim/v2/*`: return 404 (routes not registered, ADR-0101)" | REFLECTED | — | |
| Decisions row 2 | "Remove /scim/v2/* stubs — Yes — delete scim.go entirely" | spec-control-plane.md:327 — same error handling entry covers scim/v2 | REFLECTED | — | |
| Decisions row 3 | "Remove `isAdminMiddleware` — Yes, no routes remain behind it" | spec-control-plane.md — no present-tense isAdminMiddleware described; spec correctly omits deleted internals | REFLECTED | — | |
| Decisions row 4 | "`is_admin` DB column — Retain — gates `defaultPlatform=true` and NATS lifecycle handlers" | spec-state-schema.md:24 — "Gates `defaultPlatform=true` flag on host registration (ADR-0097) and admin-as-operator bypass in NATS lifecycle handlers (`manage.deregister`, `manage.revoke`, `manage.update`). No HTTP admin routes exist (ADR-0101)." | REFLECTED | — | Fully matches ADR decision |
| Decisions row 5 | "Admin bypass in auth middleware slug-match — Remove" | spec-control-plane.md:262 — "No admin bypass — `is_admin=true` users are subject to the same slug-match check as any other user (ADR-0101)." | REFLECTED | — | |
| Decisions row 6 | "`requireSelfOrAdmin` in hosts.go — Remove, self-only enforcement" | spec-control-plane.md:94-96 — grants, grant/accept, pubkey endpoints all say "Auth: self." | REFLECTED | — | |
| Decisions row 7 | "`is_admin` gates in NATS lifecycle handlers — Retain" | spec-control-plane.md:144 — "Authorization: host owner or platform operator"; spec-state-schema.md:24 lists the three handlers | REFLECTED | — | |
| Decisions row 8 | "Integration tests — delete old, add 404 regression tests" | spec-control-plane.md:347-353 — Admin/SCIM surface removed section lists all three required regression tests | REFLECTED | — | |
| Error Handling | "Requests to `/admin/*` or `/scim/v2/*` return 404" | spec-control-plane.md:327 — exact match | REFLECTED | — | |
| Impact — spec-state-schema.md `is_admin` | "update `is_admin` description — no longer gates HTTP admin routes" | spec-state-schema.md:24 — matches | REFLECTED | — | |
| Impact — spec-state-schema.md `nkey_public` | "remove reference to `POST /admin/users`" | spec-state-schema.md:23 — no such reference present | REFLECTED | — | |
| Impact — spec-control-plane.md Phase 1 | Bootstrap users via env vars, not HTTP admin route | spec-control-plane.md:23 — "Additional users must be created via `BOOTSTRAP_ADMIN_EMAIL`/`BOOTSTRAP_ADMIN_PASSWORD` in Helm values (ADR-0101)" | REFLECTED | — | |
| Ingress routing (cross-spec) | Remove /admin and /scim Ingress paths | spec-helm.md:93 — now says "/admin and /scim paths are removed (ADR-0101)" | REFLECTED | — | Previously a GAP; now fixed |

## Phase 0b — Cross-spec consistency check (new findings)

Checking shared concepts that remain stale after the admin route removal:

**spec-control-plane.md:80** — Section header for protected routes reads "Protected (per-host NATS JWT or **admin bearer token** in Authorization header)". With admin routes and isAdminMiddleware removed, there is no "admin bearer token" authentication mode. This is stale terminology — a developer reading this header would expect an admin bearer token path to exist.

**spec-control-plane.md:393** — Error handling entry: "Duplicate user email: **admin create** returns 409 Conflict." `adminCreateUser` is deleted with admin.go. This references a handler that no longer exists.

**spec-web.md:239** — "Admin routes are served on the main port and are accessible at the same public URL as the CP — no port-forward needed." Admin routes no longer exist. The real reason no port-forward is needed (all routes on main port) is accurate, but the stated reason ("admin routes") is stale.

**spec-web.md:249** — "then uses that JWT for all admin API calls." There are no HTTP admin API calls anymore — the bootstrap admin JWT is used only for `POST /auth/login` and the device-code flow, both public/user routes.

**spec-cli.md:227** — "No port-forward needed — admin routes are served on the main port." Same stale rationale as spec-web.md:239.

## Findings Table

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| Decisions row 1+2 (remove /admin/*, /scim/v2/*) | All admin and SCIM routes removed | spec-control-plane.md:80 — "Protected (per-host NATS JWT or admin bearer token in Authorization header)" | GAP | SPEC→FIX | The section header still says "admin bearer token" as an auth mechanism. With isAdminMiddleware deleted and all /admin/* routes gone, there is no admin bearer token auth path. Should read "Protected (per-host NATS JWT in Authorization header)" or similar. |
| Decisions row 1 (remove /admin/* routes) | All admin routes removed including adminCreateUser | spec-control-plane.md:393 — "Duplicate user email: admin create returns 409 Conflict." | GAP | SPEC→FIX | References `adminCreateUser` handler which is deleted with admin.go. This error handling entry is dead — no admin create path exists. Should be removed from the Error Handling section. |
| Decisions row 1 (remove /admin/* routes) | Admin routes removed | spec-web.md:239 — "Admin routes are served on the main port and are accessible at the same public URL as the CP — no port-forward needed" | GAP | SPEC→FIX | Stale rationale. Admin routes no longer exist. The statement about no port-forward is still true (all routes use main port), but the reason ("Admin routes are served on the main port") is misleading. Should be rephrased to not mention admin routes. |
| Decisions row 1 (remove /admin/* routes) | All HTTP admin API calls removed | spec-web.md:249 — "then uses that JWT for all admin API calls" | GAP | SPEC→FIX | There are no HTTP admin API calls anymore. The bootstrap admin JWT is used for `POST /auth/login` (public route). The sentence implies HTTP admin routes still exist. |
| Decisions row 1 (remove /admin/* routes) | Admin routes removed | spec-cli.md:227 — "No port-forward needed — admin routes are served on the main port." | GAP | SPEC→FIX | Same stale rationale as spec-web.md:239. Admin routes no longer exist; the claim about no port-forward is still correct but the stated reason is wrong. |

---

### Summary

- Reflected: 13
- Gap: 5
- Partial: 0

---

## Run: 2026-05-06T21:00:00Z (fourth evaluation — full spec set)

### ADR: docs/adr-0101-remove-admin-http-routes.md
### Specs checked: docs/mclaude-control-plane/spec-control-plane.md, docs/spec-state-schema.md, docs/mclaude-web/spec-web.md, docs/mclaude-cli/spec-cli.md, docs/charts-mclaude/spec-helm.md

All five GAPs reported in the prior run (20:00:00Z) have been resolved in the current spec files:

1. spec-control-plane.md:80 — "Protected (per-host NATS JWT in Authorization header):" — "admin bearer token" text removed. CLEAN.
2. spec-control-plane.md:~393 — "Duplicate user email: admin create returns 409" entry — no longer present. CLEAN.
3. spec-web.md:~239 — "Admin routes are served on the main port" — no longer present. CLEAN.
4. spec-web.md:~249 — "then uses that JWT for all admin API calls" — no longer present. CLEAN.
5. spec-cli.md:227 — "No port-forward needed — admin routes are served on the main port" — now reads "No port-forward needed — the CP is accessible at the same public URL as the SPA." CLEAN.

### Phase 1 — Forward Pass

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| Decisions row 1 | "Remove /admin/* routes — Yes, all of them" | spec-control-plane.md:327 — "Requests to `/admin/*` or `/scim/v2/*`: return 404 (routes not registered, ADR-0101)"; HTTP endpoints table has no /admin/* entries | REFLECTED | — | |
| Decisions row 2 | "Remove /scim/v2/* stubs — Yes, delete scim.go entirely" | spec-control-plane.md:327 — same 404 error entry covers /scim/v2/*; no SCIM section in HTTP table | REFLECTED | — | |
| Decisions row 3 | "Remove isAdminMiddleware — Yes, no routes remain behind it" | spec-control-plane.md — no isAdminMiddleware described; protected routes header correctly says "per-host NATS JWT" only | REFLECTED | — | |
| Decisions row 4 | "is_admin DB column — Retain — gates defaultPlatform=true and NATS lifecycle handlers" | spec-state-schema.md:24 — "Gates defaultPlatform=true … and admin-as-operator bypass in NATS lifecycle handlers (manage.deregister, manage.revoke, manage.update). No HTTP admin routes exist (ADR-0101)." | REFLECTED | — | |
| Decisions row 5 | "Admin bypass in auth middleware slug-match — Remove" | spec-control-plane.md:262 — "No admin bypass — is_admin=true users are subject to the same slug-match check as any other user (ADR-0101)." | REFLECTED | — | |
| Decisions row 6 | "requireSelfOrAdmin — Remove, self-only access enforcement" | spec-control-plane.md:94-96 — all three endpoints (grants, grant/accept, pubkey) say "Auth: self." | REFLECTED | — | |
| Decisions row 7 | "is_admin gates in NATS lifecycle handlers — Retain" | spec-control-plane.md:144 — "Authorization: host owner or platform operator" for manage.deregister; spec-state-schema.md:24 lists all three handlers | REFLECTED | — | |
| Decisions row 8 | "Integration tests — delete old, add regression tests asserting 404" | spec-control-plane.md:347-353 — Admin/SCIM surface removed section with all three tests (TestAdminRoutes_AllReturn404, TestAuthMiddleware_AdminBypassRemoved, TestRequireSelfOrAdmin_AdminCannotAccessOtherUserGrants) | REFLECTED | — | |
| Error Handling | "Requests to /admin/* or /scim/v2/* return 404" | spec-control-plane.md:327 — exact match | REFLECTED | — | |
| Impact — is_admin description | "no longer gates HTTP admin routes; gates NATS lifecycle admin bypass and defaultPlatform registration" | spec-state-schema.md:24 — matches ADR decision exactly | REFLECTED | — | |
| Impact — nkey_public description | "remove reference to POST /admin/users" | spec-state-schema.md:23 — no such reference present | REFLECTED | — | |
| User Flow | "Additional users must use BOOTSTRAP_ADMIN_EMAIL/BOOTSTRAP_ADMIN_PASSWORD" | spec-control-plane.md:23 — "Additional users must be created via BOOTSTRAP_ADMIN_EMAIL/BOOTSTRAP_ADMIN_PASSWORD in Helm values (ADR-0101)." | REFLECTED | — | |
| Ingress (cross-spec) | Remove /admin and /scim Ingress paths | spec-helm.md:93 — "/admin and /scim paths are removed (ADR-0101)" | REFLECTED | — | |
| E2E setup (cross-spec) | No ephemeral user creation via POST /admin/users | spec-web.md:258 — "No ephemeral user is created — POST /admin/users is removed (ADR-0101)" | REFLECTED | — | |
| CLI test teardown (cross-spec) | No DELETE /admin/users | spec-cli.md:244 — "DELETE /admin/users/{userId} is removed (ADR-0101)" | REFLECTED | — | |

### Phase 0b — Cross-spec consistency check

Concepts checked across all spec-*.md files:
- No spec-*.md file references admin bearer token auth, admin create user endpoint, or /admin/* routes as operational. The relay spec (docs/mclaude-relay/spec-relay.md) has its own /admin/* routes — these are relay-specific and unrelated to the control-plane admin surface removed by ADR-0101. No inconsistency.
- spec-control-plane.md, spec-state-schema.md, spec-web.md, spec-cli.md, spec-helm.md all consistently describe a world without HTTP admin routes.

### Summary

- Reflected: 15
- Gap: 0
- Partial: 0

CLEAN — 15 ADR decisions reflected in specs, 0 gaps.
