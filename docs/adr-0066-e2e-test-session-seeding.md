# ADR: E2E Test Session Seeding and Session-Agent Memory Reduction

**Status**: accepted
**Status history**:
- 2026-05-02: accepted — paired with docs/mclaude-web/spec-web.md, docs/mclaude-controller-k8s/spec-k8s-architecture.md

## Overview

Two related changes to make Playwright e2e tests reliable in k3d:

1. **Session-agent memory request reduction**: The default session-agent pod memory request (256Mi) causes the k3d worker node to reach ~98% allocated memory when 30+ dev-user session-agent pods are running. New test-user pods land in `Pending` because no headroom remains. The request is reduced to 64Mi so test pods can be scheduled alongside dev pods.

2. **E2E global-setup session seeding**: Tests that open or interact with sessions (`openFirstSession()`, routing tests, conversation tests) fail because the freshly-created test user has zero sessions. The global setup script seeds a project and a session via NATS WebSocket immediately after user creation so every test worker starts with a usable session in the dashboard.

## Motivation

After ADR-0064 landed per-run test user isolation, 26 Playwright tests continue to fail. All 26 share the same root cause: `openFirstSession()` times out at 30 s waiting for a session status dot (`span[style*="border-radius: 50%"]`) that never appears because the test user has no sessions.

The failure has two coupled parts: (a) the test user has zero sessions in KV, and (b) even if we create a session, the session-agent pod is Pending due to memory pressure. Both must be fixed together.

The design philosophy (confirmed explicitly): e2e global-setup may use backend shortcuts (NATS WebSocket, HTTP APIs) for infrastructure seeding. Tests that explicitly test a user-facing flow (e.g., the "create session" button) must continue to drive the SPA UI via Playwright.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Memory request target | 64Mi in `values.yaml` | Reduces k3d footprint 4×. Actual session-agent RSS is ~40-80Mi; 64Mi request leaves buffer. AKS production keeps its own `values-aks.yaml` override (unchanged). |
| Reconciler hardcoded fallback | Match `values.yaml`: 64Mi | Fallback applies only when no ConfigMap exists — should stay consistent with the chart default. |
| Project creation mechanism | NATS subject `mclaude.users.{uslug}.hosts.local.api.projects.create` | The host-scoped NATS path fully provisions: Postgres row, KV write, provisioning request to controller. The HTTP `POST /api/users/{uslug}/projects` endpoint has CP-6 gap (skips KV write and provisioning). |
| Session creation mechanism | NATS subject `mclaude.users.{uslug}.hosts.local.projects.{pslug}.sessions.create` | Mirrors how the SPA creates sessions. The session-agent pod is created by the controller and writes to KV when it starts. |
| NATS auth in global-setup | Legacy login (no `nkey_public` in POST body) returns `nkeySeed` | Simpler than generating an NKey pair in Node.js. The SPA generates its own NKey pairs, but global-setup is a test utility that can use the legacy path. |
| natsUrl derivation | If login response `natsUrl` is empty, derive from `BASE_URL` by replacing `https://` with `wss://` and appending `/nats` | Matches how the SPA derives the NATS WebSocket URL from its own origin. |
| KV wait timeout for session | 60 s | Pod scheduling + container image pull + agent startup. Generous to handle slow k3d image imports. Throw if exceeded (not warn) — the test suite should not silently start with no session. |
| KV wait timeout for project | 30 s | Project KV write is synchronous in the CP handler; 30 s is generous. |
| Env propagation | Set `DEV_PROJECT_SLUG` and `DEV_SESSION_SLUG` in `process.env` after seeding | Spec files that need project/session slugs for URL construction read from these env vars. |
| Fallback in spec files | `process.env['DEV_PROJECT_SLUG'] \|\| 'default-project'` and `process.env['DEV_SESSION_SLUG'] \|\| ''` | Tests running without ADMIN_URL (local dev against Vite) fall back to the dev user's known defaults. |
| Teardown | Existing `DELETE /admin/users/{userId}` is sufficient | User deletion cascades to projects and sessions in Postgres. NATS KV entries are eventually cleaned up by the session-agent shutdown path or TTL. No explicit project/session teardown needed. |
| `.test-user.json` schema extension | Add `projectSlug` and `sessionSlug` fields | Allows spec files and fixtures to read the seeded slugs from the temp file if env vars are unavailable. |

## User Flow (developer)

No change to the developer workflow from ADR-0064. The existing command still applies:

```bash
kubectl port-forward -n mclaude-system svc/mclaude-cp-control-plane 9091:9091 &
BASE_URL=https://dev.mclaude.richardmcsong.com \
ADMIN_URL=http://localhost:9091 \
ADMIN_TOKEN=dev-admin-token \
npx playwright test --project=chromium
```

On a successful run:
1. global-setup creates test user, logs in, seeds project + session
2. All 154 tests run with a populated dashboard
3. global-teardown deletes the test user

## Component Changes

### `charts/mclaude-worker` — reduce session-agent memory request

`values.yaml`: `sessionAgent.resources.requests.memory` reduced from `256Mi` to `64Mi`.

`values-aks.yaml` is unchanged — AKS production retains its own resource configuration.

### `mclaude-controller-k8s` — reduce reconciler hardcoded fallback

`reconciler.go` `applyDefaultResources()`: memory request fallback reduced from `256Mi` to `64Mi`.

The limit (`2Gi`) is unchanged in both places.

### `mclaude-web` — global-setup session seeding

`e2e/global-setup.ts` — extended flow after user creation:

5. Calls `POST {BASE_URL}/auth/login` with `{email, password: token}` (no `nkey_public`) to receive `{jwt, nkeySeed, natsUrl, userSlug}`.
6. Derives `natsWsUrl`: use `natsUrl` from login response if non-empty; otherwise replace `https://` with `wss://` in `BASE_URL` and append `/nats`.
7. Connects to NATS WebSocket: `connect({ servers: [natsWsUrl], authenticator: jwtAuthenticator(jwt, seed) })` where `seed = new TextEncoder().encode(nkeySeed)`.
8. Publishes `{name: "e2e-default", hostSlug: "local"}` to `mclaude.users.{uslug}.hosts.local.api.projects.create` via `nc.request(subject, data, { timeout: 30000 })`. Throws if response contains an error.
9. Waits up to 30 s for the project to appear in `mclaude-projects-{uslug}` KV (key prefix `hosts.local.projects.`). Reads `projectSlug` from the KV entry's `slug` field.
10. Publishes `{}` to `mclaude.users.{uslug}.hosts.local.projects.{pslug}.sessions.create`.
11. Waits up to 60 s for a session to appear in `mclaude-sessions-{uslug}` KV (key prefix `hosts.local.projects.{pslug}.sessions.`). Reads `sessionSlug` from the KV key.
12. Closes the NATS connection.
13. Sets `process.env['DEV_PROJECT_SLUG'] = projectSlug` and `process.env['DEV_SESSION_SLUG'] = sessionSlug`.
14. Writes `{userId, email, token, projectSlug, sessionSlug}` to `e2e/.test-user.json` (extends prior schema).

`e2e/global-teardown.ts` — no changes required. User deletion cascades.

`e2e/fixtures.ts` — extended to expose `DEV_PROJECT_SLUG` and `DEV_SESSION_SLUG` alongside existing `DEV_EMAIL` / `DEV_TOKEN`:
- `DEV_PROJECT_SLUG`: reads `process.env['DEV_PROJECT_SLUG']` first, then `.test-user.json` `projectSlug`, then `'default-project'` hardcoded default.
- `DEV_SESSION_SLUG`: reads `process.env['DEV_SESSION_SLUG']` first, then `.test-user.json` `sessionSlug`, then `''` (empty — tests that need a session slug should skip if empty).

**Spec file credential pattern (extended):**
```ts
const DEV_PROJECT_SLUG = process.env['DEV_PROJECT_SLUG'] || 'default-project'
const DEV_SESSION_SLUG = process.env['DEV_SESSION_SLUG'] || ''
```

## Data Model

`e2e/.test-user.json` (extended schema):
```json
{
  "userId": "<uuid>",
  "email": "e2e-1746123456789@mclaude.local",
  "token": "a3f8c2d1e9b4f7a2",
  "projectSlug": "e2e-default",
  "sessionSlug": "new-session"
}
```

When env-var override is active: `{"skipped": true}` (unchanged).

## Error Handling

| Failure | Behavior |
|---------|----------|
| Login fails (401/5xx) | Throw — all tests abort with message `global-setup: login failed {status}: {body}` |
| NATS connect fails | Throw — all tests abort with NATS error |
| Project create NATS request times out or returns error | Throw — all tests abort |
| Project does not appear in KV within 30 s | Throw — all tests abort with timeout message |
| Session does not appear in KV within 60 s | Throw — all tests abort with timeout message (likely: pod Pending due to memory pressure — check `kubectl get pods -n mclaude-system`) |
| ADMIN_URL not set | Skip (unchanged from ADR-0064): writes `{skipped: true}`, spec files fall back to hardcoded defaults. No login or NATS seeding attempt. |

## Security

No new secrets. NATS credentials are derived from the test user's own login response and are discarded after global-setup completes. The NATS connection is closed before tests run.

## Impact

Specs updated in this commit:
- `docs/mclaude-web/spec-web.md` — §E2E Test Infrastructure extended with project/session seeding flow
- `docs/mclaude-controller-k8s/spec-k8s-architecture.md` — session-agent default memory request updated to 64Mi

Components implementing the change:
- `charts/mclaude-worker` (values.yaml)
- `mclaude-controller-k8s` (reconciler.go)
- `mclaude-web` (e2e/global-setup.ts, e2e/fixtures.ts)

## Scope

**In v1:**
- Memory request reduction in `values.yaml` and reconciler fallback
- NATS WebSocket seeding in `e2e/global-setup.ts`
- `fixtures.ts` extended for `DEV_PROJECT_SLUG` and `DEV_SESSION_SLUG`

**Deferred:**
- Per-test session isolation (each test creates its own session). Current tests share the seeded session — acceptable for v1.
- `values-aks.yaml` session-agent memory review (separate production tuning concern).

## Integration Test Cases

| Test case | What it verifies | Components exercised |
|-----------|------------------|----------------------|
| Global setup seeds project | Project appears in `mclaude-projects-{uslug}` KV within 30 s | global-setup, CP NATS handler, NATS KV |
| Global setup seeds session | Session appears in `mclaude-sessions-{uslug}` KV within 60 s | global-setup, session-agent startup, NATS KV |
| Test user pod is schedulable | Session-agent pod transitions from Pending to Running within the 60 s window | k3d node, Helm values, reconciler |
| `openFirstSession()` succeeds | Session status dot visible in dashboard — previously failing 26 tests now pass | mclaude-web SPA, NATS KV watcher |

**Implicit verification:** Every successful Playwright run against k3d exercises the full seeding chain. The 26 previously-failing tests are the acceptance criteria — if they pass, seeding works.
