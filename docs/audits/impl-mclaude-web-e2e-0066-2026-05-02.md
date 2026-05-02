## Run: 2026-05-02T00:00:00Z

Component: mclaude-web (e2e test infrastructure)
ADR: docs/adr-0066-e2e-test-session-seeding.md
Scope: mclaude-web/e2e/global-setup.ts, mclaude-web/e2e/fixtures.ts

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0066:75 | "Calls `POST {BASE_URL}/auth/login` with `{email, password: token}` (no `nkey_public`)" | global-setup.ts:63-71 | IMPLEMENTED | — | Login is POST to `${baseURL}/auth/login` with `{email, password: token}`, no `nkey_public` in body |
| adr-0066:75 | "receive `{jwt, nkeySeed, natsUrl, userSlug}`" | global-setup.ts:72 | IMPLEMENTED | — | loginBody typed as `{ jwt, nkeySeed, natsUrl, userSlug }` |
| adr-0066:68-71 | "Throw — all tests abort with message `global-setup: login failed {status}: {body}`" | global-setup.ts:68-71 | IMPLEMENTED | — | Exact error message matches spec |
| adr-0066:76 | "Derives `natsWsUrl`: use `natsUrl` from login response if non-empty; otherwise replace `https://` with `wss://` in `BASE_URL` and append `/nats`" | global-setup.ts:75-78 | IMPLEMENTED | — | Ternary uses `loginBody.natsUrl` if truthy, else replaces `https://` with `wss://` and appends `/nats` |
| adr-0066:77 | "Connects to NATS WebSocket: `connect({ servers: [natsWsUrl], authenticator: jwtAuthenticator(jwt, seed) })` where `seed = new TextEncoder().encode(nkeySeed)`" | global-setup.ts:80-81 | IMPLEMENTED | — | Exact construct matches spec |
| adr-0066:78 | "Publishes `{name: \"e2e-default\", hostSlug: \"local\"}` to `mclaude.users.{uslug}.hosts.local.api.projects.create` via `nc.request(subject, data, { timeout: 30000 })`. Throws if response contains an error." | global-setup.ts:85-99 | IMPLEMENTED | — | Subject, payload, timeout, and error-field check all match spec |
| adr-0066:79 | "Waits up to 30 s for the project to appear in `mclaude-projects-{uslug}` KV (key prefix `hosts.local.projects.`). Reads `projectSlug` from the KV entry's `slug` field." | global-setup.ts:101-120 | IMPLEMENTED | — | Bucket `mclaude-projects-${uslug}`, prefix `hosts.local.projects.`, 30000 ms, reads `slug` field from JSON |
| adr-0066:79 | "Throw — all tests abort with timeout message (project)" | global-setup.ts:107 | IMPLEMENTED | — | `'global-setup: timed out waiting for project KV entry'` — exact wording matches |
| adr-0066:80 | "Sets `process.env['DEV_PROJECT_SLUG'] = projectSlug`" | global-setup.ts:122 | IMPLEMENTED | — | Set immediately after project KV wait |
| adr-0066:80 | "Publishes `{}` to `mclaude.users.{uslug}.hosts.local.projects.{pslug}.sessions.create`" | global-setup.ts:124-127 | IMPLEMENTED | — | Subject and empty payload match; uses `nc.publish` (not request) as spec says "publish" |
| adr-0066:81 | "Waits up to 60 s for a session to appear in `mclaude-sessions-{uslug}` KV (key prefix `hosts.local.projects.{pslug}.sessions.`). Reads `sessionSlug` from the KV key." | global-setup.ts:129-143 | IMPLEMENTED | — | Bucket `mclaude-sessions-${uslug}`, prefix `hosts.local.projects.${projectSlug}.sessions.`, 60000 ms, reads slug from last key segment |
| adr-0066:81 | "Throw — all tests abort with timeout message `global-setup: timed out waiting for session KV entry — check kubectl get pods -n mclaude-system for Pending pods`" | global-setup.ts:135-136 | IMPLEMENTED | — | Exact error message matches spec |
| adr-0066:82 | "Closes the NATS connection." | global-setup.ts:148-150 | IMPLEMENTED | — | `nc.close()` in `finally` block |
| adr-0066:83 | "Sets `process.env['DEV_PROJECT_SLUG'] = projectSlug` and `process.env['DEV_SESSION_SLUG'] = sessionSlug`" | global-setup.ts:122, 144 | IMPLEMENTED | — | Both env vars set after respective KV waits |
| adr-0066:84 | "Writes `{userId, email, token, projectSlug, sessionSlug}` to `e2e/.test-user.json` (extends prior schema)" | global-setup.ts:147 | IMPLEMENTED | — | Writes exact fields; path is relative to script's dir |
| adr-0066:122 | "ADMIN_URL not set → Skip (unchanged from ADR-0064): writes `{skipped: true}`, spec files fall back to hardcoded defaults. No login or NATS seeding attempt." | global-setup.ts:28-34 | IMPLEMENTED | — | Writes `{skipped: true}` and returns before any login/NATS call |
| adr-0066:20-21 | "DEV_EMAIL and DEV_TOKEN are both set → skip (CI override)" | global-setup.ts:21-26 | IMPLEMENTED | — | Checks both env vars and writes `{skipped: true}` before ADMIN_URL check |
| adr-0066:88-90 | "`DEV_PROJECT_SLUG`: reads `process.env['DEV_PROJECT_SLUG']` first, then `.test-user.json` `projectSlug`, then `'default-project'` hardcoded default" | fixtures.ts:30 | IMPLEMENTED | — | Three-tier: env → testUser?.projectSlug → `'default-project'` |
| adr-0066:90 | "`DEV_SESSION_SLUG`: reads `process.env['DEV_SESSION_SLUG']` first, then `.test-user.json` `sessionSlug`, then `''`" | fixtures.ts:31 | IMPLEMENTED | — | Three-tier: env → testUser?.sessionSlug → `''` |
| adr-0066:94-95 | "Spec file credential pattern: `const DEV_PROJECT_SLUG = process.env['DEV_PROJECT_SLUG'] \|\| 'default-project'` and `const DEV_SESSION_SLUG = process.env['DEV_SESSION_SLUG'] \|\| ''`" | fixtures.ts:30-31 | IMPLEMENTED | — | Exported constants match the pattern exactly |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|----------------|-------------|
| global-setup.ts:1-6 | INFRA | Imports: FullConfig, fs, path, crypto, fileURLToPath, nats.ws — all necessary plumbing |
| global-setup.ts:8-15 | INFRA | `slugifyEmail()` helper: converts email to a k8s-safe slug; used to derive `uslug` for NATS subjects and KV bucket names. Implicitly required by spec step 6 which uses `{uslug}` in subjects |
| global-setup.ts:17 | INFRA | `TEST_USER_FILE` path constant — required to write `.test-user.json` |
| global-setup.ts:37-53 | INFRA | Test user creation via `POST /admin/users` — step 4 (pre-seeding). ADR describes this as prior step 4, not in scope for ADR-0066 but present as pre-existing code. ADR says steps 5-14 are the additions; user creation is assumed prerequisite |
| global-setup.ts:56-60 | INFRA | Env propagation for DEV_EMAIL, DEV_TOKEN, DEV_USER_SLUG to worker processes — step 5, noted in ADR comment in code |
| global-setup.ts:72 | INFRA | `loginBody` type assertion — plumbing for typed access to login response fields |
| global-setup.ts:83-150 | INFRA | `try/finally` block ensuring `nc.close()` — standard resource cleanup |
| global-setup.ts:158-192 | INFRA | `waitForKVEntry` helper function — implements the shared KV watch logic used for both project and session waits. No direct spec line, but entirely required by spec steps 9 and 11 |
| fixtures.ts:1-5 | INFRA | Imports: @playwright/test, fs, path, fileURLToPath |
| fixtures.ts:8 | INFRA | `TEST_USER_FILE` path constant — matches global-setup.ts path |
| fixtures.ts:10-25 | INFRA | `loadTestUser()` function — reads and parses `.test-user.json`, returns typed object. Implements the "then `.test-user.json`" tier of the three-tier priority defined in spec |
| fixtures.ts:27 | INFRA | `testUser` = `loadTestUser()` call |
| fixtures.ts:28-29 | INFRA | `DEV_EMAIL`, `DEV_TOKEN` exports — pre-existing from ADR-0064; not changed by ADR-0066, no gaps |
| fixtures.ts:36-82 | INFRA | Helpers (`waitForNatsConnected`, `navigateToSession`, etc.) and `authenticatedPage` fixture — pre-existing from ADR-0064. Not in scope for ADR-0066. |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| adr-0066:75-84 | Global-setup seeding flow (steps 6-14) | None | Implicit: every Playwright run against k3d exercises full chain. ADR explicitly states "Every successful Playwright run against k3d exercises the full seeding chain." No isolated unit test for waitForKVEntry or slugifyEmail. | E2E_ONLY | ADR acknowledges implicit verification; no unit test exists for helper functions |
| adr-0066:88-91 | fixtures.ts three-tier priority for DEV_PROJECT_SLUG / DEV_SESSION_SLUG | None | Implicit via Playwright spec files that import from fixtures | E2E_ONLY | No isolated unit test; behavior verified transitively by each spec that uses DEV_PROJECT_SLUG / DEV_SESSION_SLUG |
| adr-0066:122 | Skip path when ADMIN_URL not set | None | None | UNTESTED | Skip path (writing {skipped:true} when ADMIN_URL absent) has no explicit test coverage. Functionally self-contained but untested in isolation. |
| adr-0066:21-26 | Skip path when DEV_EMAIL+DEV_TOKEN both set | None | None | UNTESTED | CI-override skip path has no explicit test. Would require a unit test for global-setup. |

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | — | — | No open bugs with Component matching mclaude-web e2e infrastructure |

### Summary

- Implemented: 20
- Gap: 0
- Partial: 0
- Infra: 15
- Unspec'd: 0
- Dead: 0
- Tested (E2E): 18
- Unit only: 0
- E2E only: 2
- Untested: 2
- Bugs fixed: 0
- Bugs open: 0
