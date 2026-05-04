## Run: 2026-05-02T00:00:00Z

Component: mclaude-web (e2e test infrastructure)
ADR: docs/adr-0064-e2e-test-user-isolation.md
Status: accepted

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0064:65 | `global-setup.ts` reads `ADMIN_URL`, `ADMIN_TOKEN` (default `dev-admin-token`), and `BASE_URL` from env | global-setup.ts:19,25 | IMPLEMENTED | — | ADMIN_URL at line 19, ADMIN_TOKEN at line 25; BASE_URL is read from process.env implicitly via playwright.config.ts and not directly in global-setup.ts — acceptable since setup doesn't use it |
| adr-0064:66 | If `DEV_EMAIL` and `DEV_TOKEN` are both set, skips creation: writes `{skipped: true}` to `.test-user.json` and returns | global-setup.ts:11-16 | IMPLEMENTED | — | Lines 11–16 check both vars, write `{skipped:true}`, and return |
| adr-0064:67 | If `ADMIN_URL` is not set, skips creation: writes `{skipped: true}` and returns | global-setup.ts:19-23 | IMPLEMENTED | — | Lines 19–23 handle missing ADMIN_URL |
| adr-0064:68 | Calls `POST {ADMIN_URL}/admin/users` with `Authorization: Bearer {ADMIN_TOKEN}`, body `{email: "e2e-{Date.now()}@mclaude.local", name: "E2E Test User", password: "{random 16-char hex}"}` | global-setup.ts:26-37 | GAP | SPEC→FIX | Body also includes `id: userId` (pre-generated UUID) which is not mentioned in the ADR. The random token is 8 bytes → 16 hex chars (correct). The extra `id` field is reasonable (server may require it); spec should document it. |
| adr-0064:69 | Sets `process.env['DEV_EMAIL'] = email` and `process.env['DEV_TOKEN'] = token` | global-setup.ts:45-46 | IMPLEMENTED | — | Lines 45–46 set both env vars |
| adr-0064:70 | Writes `{userId, email, token}` to `e2e/.test-user.json` | global-setup.ts:48 | IMPLEMENTED | — | Line 48 writes the JSON file |
| adr-0064:71 | On failure, throws — all tests abort cleanly | global-setup.ts:39-42 | IMPLEMENTED | — | Lines 39–42 throw with status code and response body |
| adr-0064:73-78 | `global-teardown.ts` behavior: reads file, returns if missing or skipped, deletes file eagerly, calls DELETE endpoint, warns on failure | global-teardown.ts:1-39 | IMPLEMENTED | — | All five steps exactly match spec |
| adr-0064:82-84 | `fixtures.ts`: `DEV_EMAIL` reads `process.env['DEV_EMAIL']` first, then falls back to `.test-user.json` email; `DEV_TOKEN` reads `process.env['DEV_TOKEN']` first, then falls back to `.test-user.json` token | fixtures.ts:22-24 | IMPLEMENTED | — | Lines 22–24: priority order env var → JSON file → hardcoded default |
| adr-0064:84 | A helper `loadTestUser()` reads `.test-user.json` synchronously | fixtures.ts:10-20 | IMPLEMENTED | — | `loadTestUser()` at lines 10–20 reads file synchronously |
| adr-0064:87-88 | `playwright.config.ts` adds `globalSetup: './e2e/global-setup.ts'` and `globalTeardown: './e2e/global-teardown.ts'` | playwright.config.ts:8-9 | IMPLEMENTED | — | Both declarations present at lines 8–9 |
| adr-0064:90-91 | `.gitignore` adds `mclaude-web/e2e/.test-user.json` | .gitignore:62 | IMPLEMENTED | — | Entry confirmed at line 62 of root .gitignore |
| adr-0064:33 | Spec file credential pattern: `process.env['DEV_EMAIL'] \|\| 'dev@mclaude.local'` — all spec files that hardcode credentials should read from env var with hardcoded fallback | api.spec.ts:4, auth.spec.ts:5, dashboard.spec.ts:5, dashboard-create.spec.ts:5, session-flow.spec.ts:3, session-detail.spec.ts:3, settings.spec.ts:3, metrics.spec.ts:3, attachments.spec.ts:12, chat-events.spec.ts:8, device-code.spec.ts:10, routing-nats.spec.ts:7, quota-ui.spec.ts:16, terminal.spec.ts:3 | IMPLEMENTED | — | All 13 spec files use `process.env['DEV_EMAIL'] \|\| 'dev@mclaude.local'` and `process.env['DEV_TOKEN'] \|\| 'dev'` pattern |
| adr-0064:24 | User email pattern: `e2e-{timestamp}@mclaude.local` | global-setup.ts:27 | IMPLEMENTED | — | `e2e-${Date.now()}@mclaude.local` |
| adr-0064:25 | User password (login token): Random 16-char hex string generated at setup | global-setup.ts:28 | IMPLEMENTED | — | `crypto.randomBytes(8).toString('hex')` = 16 hex chars |
| adr-0064:26 | Credentials storage: `e2e/.test-user.json` (temp file, gitignored) | global-setup.ts:7, global-teardown.ts:6, fixtures.ts:8 | IMPLEMENTED | — | All three files resolve the same path via `import.meta.url` |
| adr-0064:27 | Teardown on failure: Always delete (global teardown runs even on test failure) | playwright.config.ts:9 | IMPLEMENTED | — | `globalTeardown` in playwright.config.ts runs after all tests regardless of result |
| adr-0064:28 | Admin API access: Separate `ADMIN_URL` env var | global-setup.ts:19, global-teardown.ts:20 | IMPLEMENTED | — | Both setup and teardown read `ADMIN_URL` |
| adr-0064:29 | Skip-on-no-ADMIN_URL: if `ADMIN_URL` is unset, global setup writes `{skipped:true}` | global-setup.ts:19-23 | IMPLEMENTED | — | |
| adr-0064:30 | ADMIN_TOKEN default: `dev-admin-token` | global-setup.ts:25, global-teardown.ts:21 | IMPLEMENTED | — | Both files default to `'dev-admin-token'` |
| adr-0064:113-118 | Error handling table: Admin API unreachable → throw; non-2xx → throw with body; `.test-user.json` missing in fixtures → fall back to env vars; deletion fails → warn not throw; DEV_EMAIL set without setup → skipped sentinel | global-setup.ts:39-42, global-teardown.ts:28-38, fixtures.ts:10-24 | IMPLEMENTED | — | All error cases handled per table |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| global-setup.ts:26 | UNSPEC'd | `const userId = crypto.randomUUID()` — the ADR body description (line 68) doesn't mention pre-generating a UUID client-side and sending it as `id` field. The code passes `id: userId` in the POST body. This is reasonable server integration but spec omits it. |
| global-teardown.ts:33 | INFRA | `res.status !== 404` guard — treats 404 as success on delete, which is a sensible idempotent-delete pattern not explicitly in the spec but consistent with spec intent ("user may have been deleted already"). |
| fixtures.ts:29-73 | INFRA | `waitForNatsConnected`, `navigateToSession`, `navigateToSettings`, `navigateToMetrics`, `navigateBack`, and the `authenticatedPage` fixture — these are test helper utilities not scoped by ADR-0064, but they are all consumed by spec files and are necessary fixture infrastructure. |
| api.spec.ts:6-7 | UNSPEC'd | `const DEV_USER_SLUG = 'dev-mclaude-local'` — hardcoded user slug that does NOT read from an env var. The ADR specifies that spec files use `process.env['DEV_EMAIL'] \|\| 'dev@mclaude.local'` pattern. `DEV_USER_SLUG` is derived from the email but there is no env-var fallback for it. When a test-user is created with email `e2e-{ts}@mclaude.local`, its slug will NOT be `dev-mclaude-local`. Used at api.spec.ts:631 in `API-ADMIN-04`. |
| routing-nats.spec.ts:8 | UNSPEC'd | Same issue — `const DEV_USER_SLUG = 'dev-mclaude-local'` hardcoded without env-var fallback. Not used directly in routing-nats.spec.ts body (declared but not referenced in any test), so dead for this file. |
| chat-events.spec.ts:327 | UNSPEC'd | `const bogusUrl = '/#/s/dev-mclaude-local/nonexistent-proj/nonexistent-session-xyz'` — hardcodes `dev-mclaude-local` slug directly in the URL (not via a constant). When a test user is `e2e-{ts}@mclaude.local`, the slug will differ. This particular test navigates to a nonexistent session URL to verify the app doesn't crash, so the wrong slug likely doesn't affect the test outcome (it is intentionally bogus), but it is still a hardcoded credential-derived value that bypasses the env-var pattern. |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| adr-0064:65-71 | global-setup.ts creates test user via POST /admin/users | None | api.spec.ts API-ADMIN-05 exercises POST /admin/users; no test exercises global-setup.ts itself | E2E_ONLY | No unit test for global-setup logic; the ADR Integration Test Cases table (line 154) lists "Global setup creates test user" as a required integration test but there is no dedicated test for it |
| adr-0064:73-78 | global-teardown.ts deletes test user via DELETE /admin/users/{userId} | None | api.spec.ts API-ADMIN-07 exercises DELETE /admin/users; no test exercises global-teardown.ts directly | E2E_ONLY | ADR Integration Test Cases (line 156) lists "Global teardown deletes test user" — no dedicated test exists |
| adr-0064:82-84 | fixtures.ts credential resolution priority | None | fixtures.ts is exercised by every test that uses `authenticatedPage`, but no test specifically verifies the priority fallback logic (env var > JSON file > hardcoded) | UNTESTED | ADR Integration Test Cases (line 155) lists "`authenticatedPage` fixture uses test user" — this is exercised implicitly by tests using the fixture, but not a dedicated test |
| adr-0064:87-88 | playwright.config.ts declares globalSetup and globalTeardown | None | Exercised implicitly on every `npx playwright test` invocation | E2E_ONLY | Config-level wiring verified by running the test suite |

### Phase 4 — Bug Triage

No open bugs in `.agent/bugs/` have `**Component**: mclaude-web` scoped to the e2e test infrastructure. The `002-droid-shield-false-positives.md` references `mclaude-web/e2e/api.spec.ts` but its component is "Droid-Shield pre-commit secret scanner", not mclaude-web.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| — | — | — | No bugs with Component: mclaude-web targeting e2e infrastructure |

### Summary

- Implemented: 18
- Gap: 1
- Partial: 0
- Infra: 2
- Unspec'd: 4
- Dead: 0
- Tested: 0
- Unit only: 0
- E2E only: 3
- Untested: 1
- Bugs fixed: 0
- Bugs open: 0
