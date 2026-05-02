# ADR: E2E Test User Isolation and K3d Best Practices

**Status**: accepted
**Status history**:
- 2026-05-01: accepted

## Overview

Playwright e2e tests currently use a hardcoded `dev@mclaude.local` account shared across all test runs. This means tests can pollute each other's state (leftover projects, sessions, hosts), and the account must exist and be in a known state before any test can run. This ADR adopts per-run test user isolation: a global setup script creates a fresh user via the admin API before each Playwright invocation, and a global teardown script deletes it after. It also documents the canonical command for running tests against k3d.

## Motivation

Two problems:

1. **Shared mutable state.** Tests that create projects or sessions leave state behind in `dev@mclaude.local`. Tests that assume a clean account (e.g., "dashboard shows zero sessions") become flaky when prior tests leave data.

2. **Undocumented k3d workflow.** There is no documented procedure for running e2e tests against the local k3d cluster. Developers discover `BASE_URL` by inspection and have to know the admin token out-of-band.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Test user scope | One user per Playwright invocation (global setup/teardown) | Per-test user would require an admin call per test (slow); per-run is cheap and sufficient for isolation |
| User email pattern | `e2e-{timestamp}@mclaude.local` | Unique across runs; `.local` domain signals non-production |
| User password (login token) | Random 16-char hex string generated at setup | Can be used with `/auth/login email+token` |
| Credentials storage | `e2e/.test-user.json` (temp file, gitignored) | Shared between setup and all test workers; survives worker restarts |
| Teardown on failure | Always delete (global teardown runs even on test failure) | Prevents accumulation of orphaned test users |
| Admin API access | Separate `ADMIN_URL` env var (e.g. `http://localhost:9091` via kubectl port-forward) | Admin API binds to `127.0.0.1:9091` inside the pod — not exposed via public Ingress |
| Skip-on-no-ADMIN_URL | If `ADMIN_URL` is unset, global setup writes `{skipped:true}` and no-ops | Running without port-forward still works; spec files fall back to hardcoded dev defaults |
| ADMIN_TOKEN default | `dev-admin-token` | Matches the k3d dev cluster secret value; CI can override via env |
| BASE_URL for k3d | `https://dev.mclaude.richardmcsong.com` | The existing k3d Ingress URL |
| Credential propagation | Global setup sets `process.env['DEV_EMAIL']`/`['DEV_TOKEN']` in addition to writing `.test-user.json` | Spec files that read `process.env['DEV_EMAIL']` directly inherit the test user credentials |
| Spec file credential pattern | `process.env['DEV_EMAIL'] \|\| 'dev@mclaude.local'` | All spec files that hardcode credentials should read from env var with hardcoded fallback |
| Fixtures fallback | `DEV_EMAIL` / `DEV_TOKEN` env vars take priority; then `.test-user.json`; then hardcoded defaults | CI pipelines that pre-provision users can set env vars to bypass setup |

## User Flow (developer)

```bash
# Run against k3d (standard command):
# Step 1 — forward the admin port (separate terminal or background):
kubectl port-forward -n mclaude-system svc/mclaude-cp-control-plane 9091:9091 &

# Step 2 — run tests:
BASE_URL=https://dev.mclaude.richardmcsong.com \
ADMIN_URL=http://localhost:9091 \
ADMIN_TOKEN=dev-admin-token \
npx playwright test --project=chromium

# Run against local Vite dev server (unchanged — no ADMIN_URL needed, skips user creation):
npx playwright test

# Override to use a pre-existing account (opt-out of test user, no ADMIN_URL needed):
DEV_EMAIL=my@email.com DEV_TOKEN=mytoken \
BASE_URL=https://dev.mclaude.richardmcsong.com \
npx playwright test --project=chromium
```

## Component Changes

### `mclaude-web` — e2e test infrastructure

**New files:**

`e2e/global-setup.ts` — runs once before all tests:
1. Reads `ADMIN_URL` (e.g. `http://localhost:9091` via kubectl port-forward), `ADMIN_TOKEN` (default `dev-admin-token`), and `BASE_URL` from env.
2. If `DEV_EMAIL` and `DEV_TOKEN` are both set, skips creation: writes `{skipped: true}` to `.test-user.json` and returns.
3. If `ADMIN_URL` is not set, skips creation: writes `{skipped: true}` and returns — spec files fall back to their hardcoded defaults.
4. Otherwise, calls `POST {ADMIN_URL}/admin/users` with `Authorization: Bearer {ADMIN_TOKEN}`, body `{email: "e2e-{Date.now()}@mclaude.local", name: "E2E Test User", password: "{random 16-char hex}"}`.
5. Sets `process.env['DEV_EMAIL'] = email` and `process.env['DEV_TOKEN'] = token` so spec files that read from `process.env` inherit the test user credentials.
6. Writes `{userId, email, token}` to `e2e/.test-user.json`.
7. On failure, throws — all tests abort cleanly.

`e2e/global-teardown.ts` — runs once after all tests (including on failure):
1. Reads `e2e/.test-user.json`. If missing or `skipped: true`, returns immediately.
2. Deletes `e2e/.test-user.json` eagerly (before the API call, so it cannot be re-read on a subsequent crashed run).
3. Calls `DELETE {ADMIN_URL}/admin/users/{userId}` with `Authorization: Bearer {ADMIN_TOKEN}`.
4. Logs a warning (not a throw) if deletion fails — the user may have been deleted already.

**Modified files:**

`e2e/fixtures.ts`:
- `DEV_EMAIL`: reads `process.env['DEV_EMAIL']` first, then falls back to `e2e/.test-user.json` email.
- `DEV_TOKEN`: reads `process.env['DEV_TOKEN']` first, then falls back to `.test-user.json` token.
- A helper `loadTestUser()` reads `.test-user.json` synchronously (or returns the env-var values directly).

`playwright.config.ts`:
- Adds `globalSetup: './e2e/global-setup.ts'`
- Adds `globalTeardown: './e2e/global-teardown.ts'`

`.gitignore` (root):
- Adds `mclaude-web/e2e/.test-user.json`

## Data Model

`e2e/.test-user.json` (ephemeral, gitignored):
```json
{
  "userId": "<uuid>",
  "email": "e2e-1746123456789@mclaude.local",
  "token": "a3f8c2d1e9b4f7a2"
}
```

Or when env-var override is active:
```json
{
  "skipped": true
}
```

## Error Handling

| Failure | Behavior |
|---------|----------|
| Admin API unreachable in setup | Throw — all tests skip with clear message |
| Admin API returns non-2xx in setup | Throw with response body — helps debug wrong ADMIN_TOKEN |
| `.test-user.json` missing in fixtures | Fall back to `DEV_EMAIL`/`DEV_TOKEN` env vars; if both absent, test fails at auth |
| User deletion fails in teardown | Log warning, do not throw — test suite result is not affected |
| `DEV_EMAIL` env set, no setup call | `{skipped: true}` sentinel — teardown no-ops |

## Security

- `ADMIN_TOKEN` is a break-glass bearer token stored in the `mclaude-control-plane` K8s Secret (`admin-token` field). For k3d dev this is `dev-admin-token`.
- Test user passwords are random per run and discarded after teardown.
- `.test-user.json` is gitignored and ephemeral — it does not persist beyond a single `npx playwright test` invocation.

## Impact

Specs updated in this commit:
- `docs/mclaude-web/spec-web.md` — new §E2E Test Infrastructure section

Components implementing the change:
- `mclaude-web` (e2e test files only — no SPA production code changes)

**Exhaustive spec inventory:** No shared protocol concepts change (no KV buckets, NATS subjects, API contract changes). The admin API (`POST /admin/users`, `DELETE /admin/users/{id}`) is already specified in `docs/mclaude-control-plane/spec-control-plane.md:123,126` — no updates needed there.

## Scope

**In v1:**
- `e2e/global-setup.ts` and `e2e/global-teardown.ts`
- `fixtures.ts` updated to read from `.test-user.json`
- `playwright.config.ts` wired to global setup/teardown
- `.gitignore` entry for `.test-user.json`
- `docs/mclaude-web/spec-web.md` §E2E Test Infrastructure section

**Deferred:**
- Per-test project isolation (each test creates+deletes its own project). Current tests share the test user's projects — this is acceptable for v1.
- CI pipeline documentation (how to set ADMIN_TOKEN in GitHub Actions secrets).

## Integration Test Cases

| Test case | What it verifies | Components exercised |
|-----------|------------------|----------------------|
| Global setup creates test user | `POST /admin/users` returns 200 with userId; user can log in | mclaude-web (setup), mclaude-control-plane (admin API, auth) |
| `authenticatedPage` fixture uses test user | Login with dynamically-created email+token succeeds | mclaude-web (fixture, SPA), mclaude-control-plane (auth) |
| Global teardown deletes test user | `DELETE /admin/users/{userId}` returns 204; user cannot log in after | mclaude-web (teardown), mclaude-control-plane (admin API) |

### Cross-component interface tests

The global setup/teardown exercises the same `POST/DELETE /admin/users` endpoints used in `api.spec.ts` admin tests — verifying that the CP admin API contract is stable across both consumers.
