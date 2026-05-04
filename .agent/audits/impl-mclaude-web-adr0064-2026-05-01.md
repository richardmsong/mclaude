## Run: 2026-05-01T00:00:00Z

Component: mclaude-web (e2e test infrastructure — ADR-0064)
Spec files: docs/adr-0064-e2e-test-user-isolation.md, docs/mclaude-web/spec-web.md §E2E Test Infrastructure

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0064:57 | `POST {BASE_URL}/admin/users` with `Authorization: Bearer {ADMIN_TOKEN}`, body `{email: "e2e-{Date.now()}@mclaude.local", name: "E2E Test User", password: "{random 16-char hex}"}` | global-setup.ts:22-29 | IMPLEMENTED | — | email pattern, name, password all match; 16-char hex = randomBytes(8).toString('hex') |
| adr-0064:55-56 | `e2e/global-setup.ts` reads `BASE_URL` and `ADMIN_TOKEN` (default `dev-admin-token`) from env | global-setup.ts:17-18 | IMPLEMENTED | — | BASE_URL falls back via config.projects[0] then localhost; ADMIN_TOKEN defaults to dev-admin-token |
| adr-0064:58 | If `DEV_EMAIL` / `DEV_TOKEN` are both set, skips creation and writes sentinel `{skipped: true}` | global-setup.ts:10-15 | IMPLEMENTED | — | Exact sentinel written; returns early |
| adr-0064:59 | Writes `{userId, email, token}` to `e2e/.test-user.json` | global-setup.ts:37 | IMPLEMENTED | — | Writes `{userId: data.id, email, token}`; data.id maps to userId field |
| adr-0064:60 | On failure, throws — all tests abort cleanly | global-setup.ts:31-33 | IMPLEMENTED | — | Throws with status code and response body on non-ok |
| adr-0064:62-63 | `e2e/global-teardown.ts` reads `e2e/.test-user.json`; if missing or `skipped: true`, returns immediately | global-teardown.ts:8-14 | IMPLEMENTED | — | Missing check on line 8; skipped check on line 14; file deleted BEFORE skipped check (see Notes) | 
| adr-0064:64 | `DELETE {BASE_URL}/admin/users/{userId}` with `Authorization: Bearer {ADMIN_TOKEN}` | global-teardown.ts:21-24 | IMPLEMENTED | — | Correct method, path, and auth header |
| adr-0064:65 | Deletes `e2e/.test-user.json` | global-teardown.ts:12 | PARTIAL | SPEC→FIX | File is deleted BEFORE the DELETE API call, not after. Spec implies delete-file happens after successful delete. Code deletes eagerly on line 12 then calls API. Practically fine (cleanup still happens); spec is ambiguous about order. |
| adr-0064:66 | Logs a warning (not a throw) if deletion fails | global-teardown.ts:26-29 | IMPLEMENTED | — | console.warn on non-ok status and on catch; no throw |
| adr-0064:75-76 | `playwright.config.ts` adds `globalSetup: './e2e/global-setup.ts'` and `globalTeardown: './e2e/global-teardown.ts'` | playwright.config.ts:8-9 | IMPLEMENTED | — | Exact paths present |
| adr-0064:71 | `DEV_EMAIL`: reads `process.env['DEV_EMAIL']` first, then falls back to `e2e/.test-user.json` email | fixtures.ts:22 | IMPLEMENTED | — | env var first, then testUser?.email, then hardcoded fallback |
| adr-0064:72 | `DEV_TOKEN`: reads `process.env['DEV_TOKEN']` first, then falls back to `.test-user.json` token | fixtures.ts:23 | IMPLEMENTED | — | env var first, then testUser?.token, then hardcoded fallback |
| adr-0064:73 | A helper `loadTestUser()` reads `.test-user.json` synchronously (or returns env-var values directly) | fixtures.ts:9-19 | IMPLEMENTED | — | Reads synchronously; handles missing file and skipped sentinel |
| adr-0064:79 | `.gitignore` (root): adds `mclaude-web/e2e/.test-user.json` | .gitignore:62 | IMPLEMENTED | — | Exact line present with comment |
| spec-web.md:215 | If `DEV_EMAIL` and `DEV_TOKEN` env vars are both set, skips creation and writes `{skipped: true}` | global-setup.ts:10-15 | IMPLEMENTED | — | Matches ADR row above |
| spec-web.md:216-218 | Calls `POST {BASE_URL}/admin/users` with body `{email, name, password}` | global-setup.ts:22-29 | IMPLEMENTED | — | Matches ADR row above |
| spec-web.md:226 | `playwright.config.ts` declares `globalSetup` and `globalTeardown` | playwright.config.ts:8-9 | IMPLEMENTED | — | Matches ADR row above |
| spec-web.md:230-231 | Fixtures resolve credentials: env vars first, then `.test-user.json` | fixtures.ts:22-23 | IMPLEMENTED | — | Matches ADR row above |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| global-setup.ts:1-5 | INFRA | Imports (FullConfig, fs, path, crypto) — required plumbing |
| global-setup.ts:6 | INFRA | TEST_USER_FILE path constant — shared with teardown and fixtures |
| global-setup.ts:17 | INFRA | Fallback chain for BASE_URL (config.projects[0] → localhost) — practical env handling not fully spelled out in spec but reasonable |
| global-teardown.ts:1-5 | INFRA | Imports — required plumbing |
| global-teardown.ts:25 | INFRA | `res.status !== 404` guard in teardown warning — treats 404 as success (user already gone); reasonable defensive code, not contradicted by spec |
| fixtures.ts:1-3 | INFRA | Imports |
| fixtures.ts:22-23 | INFRA | Hardcoded fallback `'dev@mclaude.local'` / `'dev'` after env+json miss | 
| fixtures.ts:27-74 | INFRA | Helper functions (waitForNatsConnected, navigateToSession, navigateToSettings, navigateToMetrics, navigateBack) and authenticatedPage fixture — test infrastructure supporting spec'd credential flow |
| playwright.config.ts:1-46 | INFRA | Project config, browser projects, webServer config — all supporting spec'd test infrastructure |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| adr-0064:143 | Global setup creates test user (POST /admin/users, user can log in) | None | None | UNTESTED | No test exercises global-setup.ts directly. Integration test case listed in ADR is not implemented. |
| adr-0064:144 | authenticatedPage fixture uses test user credentials from .test-user.json | None | None | UNTESTED | Spec files that use `authenticatedPage` (from fixtures.ts) all hardcode `dev@mclaude.local` — they import from fixtures.ts but override DEV_EMAIL/DEV_TOKEN locally. See api.spec.ts:4, auth.spec.ts:5, dashboard.spec.ts:5, etc. |
| adr-0064:145 | Global teardown deletes test user (DELETE /admin/users/{userId}, user cannot log in after) | None | None | UNTESTED | No test exercises global-teardown.ts directly. Integration test case listed in ADR is not implemented. |

### Phase 4 — Bug Triage

No open bugs with `Component: mclaude-web` (e2e test infrastructure scope). Bugs 002-ptt, 003-initial-message, 005-getting-started are SPA runtime bugs, not e2e infrastructure bugs — out of scope for this audit.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| — | — | N/A | No open bugs scoped to e2e test infrastructure |

### Summary

- Implemented: 16
- Gap: 0
- Partial: 1
- Infra: 10
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 0
- E2E only: 0
- Untested: 3
- Bugs fixed: 0
- Bugs open: 0
