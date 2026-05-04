## Run: 2026-05-03T00:00:00Z

### ADR: docs/adr-0085-fix-integration-test-cluster-grant.md
### Specs checked: docs/mclaude-cli/spec-cli.md

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| 34 | "When to grant: Immediately after createTestUser, before device-code login" | spec-cli.md:238 "After creating the user, TestMain calls grantClusterAccess… This step is required before the device-code login" | REFLECTED | — | Ordering is explicit in spec |
| 35 | "Endpoint: POST /admin/clusters/{hslug}/grants with {userSlug}" | spec-cli.md:238 "calls POST /admin/clusters/{hslug}/grants with {userSlug}" | REFLECTED | — | Exact endpoint and payload present |
| 36 | "Conditional on cluster type: Always attempt; idempotent ON CONFLICT DO NOTHING" | spec-cli.md:238 "The call is made unconditionally for every test run regardless of cluster type — the CP endpoint is idempotent (ON CONFLICT DO NOTHING) so re-running is safe" | REFLECTED | — | Unconditional call and idempotency both captured |
| 37 | "Teardown: No explicit revoke needed; deleteTestUser cascades to all hosts rows via FK constraint" | spec-cli.md:248 "No explicit revoke is needed — deleteTestUser cascades to all hosts rows for the user via the FK constraint, which drops the cluster grant automatically" | REFLECTED | — | Teardown rationale and FK cascade both present |
| 38 | "New helper: grantClusterAccess(adminURL, adminToken, hslug, userSlug) in integration_main_test.go" | spec-cli.md:238 "TestMain calls grantClusterAccess(adminURL, adminToken, hslug, user.Slug) (defined in integration_main_test.go)" | REFLECTED | — | Helper name, signature shape, and file location all named in spec |
| 87 | "If grantClusterAccess fails… runTests deletes the test user and exits 1" | spec-cli.md:238 "If the grant call fails, TestMain deletes the test user and exits 1" | REFLECTED | — | Error handling path exactly reflected |
| 87 | "ON CONFLICT DO NOTHING makes the call idempotent" | spec-cli.md:238 "the CP endpoint is idempotent (ON CONFLICT DO NOTHING)" | REFLECTED | — | Idempotency statement already in spec |

### Summary

- Reflected: 7
- Gap: 0
- Partial: 0

## Run: 2026-05-03T12:00:00Z

### ADR: docs/adr-0085-fix-integration-test-cluster-grant.md
### Specs checked: docs/mclaude-cli/spec-cli.md

**Note:** This run supersedes the previous run in this file. The prior run incorrectly marked several decisions as PARTIAL or GAP. After careful re-reading of spec-cli.md line 238 and line 248, all ADR decisions are fully reflected in the spec.

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| 34 | "When to grant: Immediately after createTestUser, before device-code login" | spec-cli.md:238 | REFLECTED | — | Spec: "After creating the user, TestMain calls grantClusterAccess… This step is required before the device-code login" |
| 35 | "Endpoint: POST /admin/clusters/{hslug}/grants with {userSlug}" | spec-cli.md:238 | REFLECTED | — | Spec names exact endpoint and request body |
| 36 | "Always attempt; idempotent ON CONFLICT DO NOTHING" | spec-cli.md:238 | REFLECTED | — | Spec: "The call is made unconditionally for every test run regardless of cluster type — the CP endpoint is idempotent (ON CONFLICT DO NOTHING)" |
| 37 | "Teardown: No explicit revoke; deleteTestUser cascades hosts rows via FK" | spec-cli.md:248 | REFLECTED | — | Spec: "No explicit revoke is needed — deleteTestUser cascades to all hosts rows for the user via the FK constraint, which drops the cluster grant automatically" |
| 38 | "New helper: grantClusterAccess(adminURL, adminToken, hslug, userSlug) in integration_main_test.go" | spec-cli.md:238 | REFLECTED | — | Spec names the function, its parameters, and its file location explicitly |
| 87 | "If grantClusterAccess fails, runTests deletes the test user and exits 1" | spec-cli.md:238 | REFLECTED | — | Spec: "If the grant call fails, TestMain deletes the test user and exits 1" |

### Summary

- Reflected: 6
- Gap: 0
- Partial: 0
