## Run: 2026-05-02T00:00:00Z

Component: mclaude-web (e2e test infrastructure)
ADR: docs/adr-0064-e2e-test-user-isolation.md
Spec: docs/mclaude-web/spec-web.md §E2E Test Infrastructure

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0064:24 | User email pattern: `e2e-{timestamp}@mclaude.local` | global-setup.ts:36 `const email = \`e2e-${Date.now()}@mclaude.local\`` | IMPLEMENTED | — | Exact match |
| adr-0064:25 | User password (login token): Random 16-char hex string | global-setup.ts:37 `crypto.randomBytes(8).toString('hex')` | IMPLEMENTED | — | 8 bytes = 16 hex chars |
| adr-0064:26 | Credentials storage: `e2e/.test-user.json` | global-setup.ts:58, global-teardown.ts:6, fixtures.ts:8 | IMPLEMENTED | — | All three files use same path |
| adr-0064:27 | Teardown on failure: Always delete (global teardown runs even on test failure) | global-teardown.ts (entire file), playwright.config.ts:9 | IMPLEMENTED | — | `globalTeardown` runs unconditionally |
| adr-0064:28 | Admin API access: Separate `ADMIN_URL` env var | global-setup.ts:28 `const adminURL = process.env['ADMIN_URL']` | IMPLEMENTED | — | |
| adr-0064:29 | Skip-on-no-ADMIN_URL: writes `{skipped:true}` and no-ops | global-setup.ts:29-32 | IMPLEMENTED | — | |
| adr-0064:30 | ADMIN_TOKEN default: `dev-admin-token` | global-setup.ts:34, global-teardown.ts:21 | IMPLEMENTED | — | Both files default to `dev-admin-token` |
| adr-0064:31 | BASE_URL for k3d: `https://dev.mclaude.richardmcsong.com` | playwright.config.ts:34 | IMPLEMENTED | — | In `live` project config |
| adr-0064:32 | Credential propagation: sets `DEV_EMAIL`, `DEV_TOKEN`, `DEV_USER_SLUG` | global-setup.ts:54-56 | IMPLEMENTED | — | All three env vars set |
| adr-0064:33 | Spec file credential pattern: `process.env['DEV_EMAIL'] \|\| 'dev@mclaude.local'` | api.spec.ts:4-6, routing-nats.spec.ts:6-8 | IMPLEMENTED | — | Both use env-var-with-fallback pattern |
| adr-0064:34 | User slug propagation: `process.env['DEV_USER_SLUG'] \|\| 'dev-mclaude-local'` | api.spec.ts:6 `process.env['DEV_USER_SLUG'] \|\| 'dev-mclaude-local'` | IMPLEMENTED | — | Reads from env with correct fallback |
| adr-0064:69 | POST body includes `id: userId` (required by CP AdminUserRequest struct) | global-setup.ts:45 `body: JSON.stringify({ id: userId, email, name: 'E2E Test User', password: token })` | IMPLEMENTED | — | `id` field present |
| adr-0064:70 | Computes `DEV_USER_SLUG = slugify(email)` — lowercase, replace non-`[a-z0-9]` with `-`, trim, truncate to 63 chars | global-setup.ts:7-14 `slugifyEmail()` function | IMPLEMENTED | — | Algorithm matches spec exactly |
| adr-0064:74-77 | global-teardown: reads `.test-user.json`, deletes file eagerly, calls `DELETE /admin/users/{userId}`, logs warning on failure | global-teardown.ts:9-38 | IMPLEMENTED | — | Exact sequence: read → delete file → API call → warn on error |
| adr-0064:83-85 | fixtures.ts: DEV_EMAIL reads process.env first, falls back to .test-user.json; DEV_TOKEN same | fixtures.ts:23-24 | IMPLEMENTED | — | Priority: env → file → hardcoded default |
| adr-0064:88-89 | playwright.config.ts: adds globalSetup and globalTeardown | playwright.config.ts:8-9 | IMPLEMENTED | — | Both declared |
| adr-0064:91 | .gitignore: adds `mclaude-web/e2e/.test-user.json` | .gitignore:62 | IMPLEMENTED | — | Entry present at line 62 |
| spec-web.md:225-231 | Global setup steps 1-7 match ADR steps 1-7 (spec mirrors ADR) | global-setup.ts (entire file) | IMPLEMENTED | — | Spec and code aligned |
| spec-web.md:240 | playwright.config.ts declares globalSetup and globalTeardown | playwright.config.ts:8-9 | IMPLEMENTED | — | |
| spec-web.md:244-248 | Fixture credentials priority: env vars → .test-user.json → hardcoded defaults | fixtures.ts:23-24 `process.env['DEV_EMAIL'] \|\| testUser?.email \|\| 'dev@mclaude.local'` | IMPLEMENTED | — | Three-level fallback |
| spec-web.md:249-253 | Spec files must use `process.env['DEV_USER_SLUG'] \|\| 'dev-mclaude-local'` | api.spec.ts:6, routing-nats.spec.ts (no DEV_USER_SLUG — not needed) | IMPLEMENTED | — | Only api.spec.ts uses DEV_USER_SLUG; routing-nats.spec.ts doesn't use it at all (no user-scoped API URLs) |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| global-setup.ts:1-6 | INFRA | Imports: FullConfig, fs, path, crypto, fileURLToPath — all used by spec'd behavior |
| global-setup.ts:7-14 | INFRA | `slugifyEmail()` helper — implements the spec'd slug computation (ADR-0064:70) |
| global-setup.ts:16 | INFRA | `TEST_USER_FILE` constant — file path used throughout |
| global-teardown.ts:1-6 | INFRA | Imports and TEST_USER_FILE constant |
| global-teardown.ts:23-26 | INFRA | Guard: if `!adminURL` in teardown, warn and return — necessary error handling for spec'd behavior |
| fixtures.ts:1-8 | INFRA | Imports and TEST_USER_FILE constant |
| fixtures.ts:10-20 | INFRA | `loadTestUser()` helper — implements the spec'd file-fallback credential loading |
| fixtures.ts:29-51 | INFRA | `waitForNatsConnected()`, `navigateToSession()`, `navigateToSettings()`, `navigateToMetrics()`, `navigateBack()` — test helper utilities; not spec'd by ADR-0064 but are pre-existing test infrastructure |
| fixtures.ts:55-73 | INFRA | `authenticatedPage` fixture implementation — uses DEV_EMAIL/DEV_TOKEN as spec'd |
| playwright.config.ts:1-46 | INFRA | Full config file including base URL, browser projects, webServer config — all spec'd or necessary infrastructure |
| api.spec.ts:1-711 | INFRA | All API test cases — not in scope of ADR-0064 (which only governs test infrastructure). The credential/slug reading pattern at lines 3-6 is the scope item, which is IMPLEMENTED. |
| routing-nats.spec.ts:1-484 | INFRA | All routing/NATS test cases — not in scope of ADR-0064. The credential pattern at lines 5-8 is the scope item, which is IMPLEMENTED. |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| adr-0064:155-159 | Integration test cases: setup creates user → user can log in | None (no standalone test file for setup/teardown scripts) | Implicitly verified: every `npx playwright test` run with ADMIN_URL set creates user and tests log in | E2E_ONLY | ADR §Note on implicit verification explicitly documents this — every test run proves setup/teardown correctness |
| adr-0064:155-159 | `authenticatedPage` fixture uses test user credentials | None | fixtures.ts used by all auth-requiring tests — login with dynamic user succeeds | E2E_ONLY | Implicit per ADR |
| adr-0064:155-159 | Teardown deletes test user | None | implicit — teardown runs after all tests | E2E_ONLY | Implicit per ADR |

### Phase 4 — Bug Triage

No open bugs in `.agent/bugs/` have `**Component**: mclaude-web` — no bug triage required.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| — | — | — | No mclaude-web bugs open |

### Summary

- Implemented: 21
- Gap: 0
- Partial: 0
- Infra: 12 (code blocks)
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 0
- E2E only: 3 (all explicitly documented as implicit by ADR)
- Untested: 0
- Bugs fixed: 0
- Bugs open: 0
