# ADR: Fix integration TestMain missing cluster-grant step

**Status**: accepted
**Status history**:
- 2026-05-03: accepted — Class C spec gap; TestMain fixture setup omits cluster-grant, causing reconciler to fail with "user does not have access to host"

## Overview

The CLI integration test `TestMain` creates an ephemeral test user but does not grant that user access to the cluster host (`MCLAUDE_TEST_HOST_SLUG`). Without a `hosts` row for the cluster, the controller reconciler fails with `agents.register failed: user does not have access to host` and the import's `importRef` never clears. The fix: add a `grantClusterAccess` step in TestMain immediately after `createTestUser`, before the device-code login.

## Motivation

Running `TestIntegration_Import_HappyPath` against the k3d dev cluster produces:

```
integration: TestMain setup complete; userSlug=cli-test-... hslug=k3d-dev
integration_import_test.go:151: importRef not yet null, polling...
...
integration_import_test.go:154: timed out waiting for importRef to become null — session-agent did not unpack within 60s
```

Controller logs show the root cause:

```json
{"error":"agents.register failed: user does not have access to host","message":"ensure agent nkey"}
```

`POST /admin/clusters/{cslug}/grants` creates a `hosts` row for the user with the cluster's shared fields. Without it, `getUserHostSlugs` returns an empty list for the test user, and the controller's `agents.register` call to the CP fails the access check.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| When to grant | Immediately after `createTestUser`, before device-code login | JWT is issued during login; the `hosts` row must exist before JWT issuance so the user's JWT includes the cluster host slug |
| Endpoint | `POST /admin/clusters/{hslug}/grants` with `{userSlug}` | Existing admin endpoint; grants the user a per-user hosts row for the cluster |
| Conditional on cluster type | Always attempt; idempotent `ON CONFLICT DO NOTHING` in CP means it's safe even if user already has access | No type check needed in test code; safe to call unconditionally |
| Teardown | No explicit revoke needed | `deleteTestUser` cascades to all `hosts` rows for the user via FK constraint |
| New helper | `grantClusterAccess(adminURL, adminToken, hslug, userSlug)` in `integration_main_test.go` | Mirrors the `createTestUser` / `deleteTestUser` pattern |

## Component Changes

### `mclaude-cli/cmd/integration_main_test.go`

**Add `grantClusterAccess` helper** and call it in `runTests` immediately after `createTestUser`:

```go
// grantClusterAccess grants the test user access to the named cluster host.
// Called before device-code login so the user's JWT includes the cluster slug.
func grantClusterAccess(adminURL, adminToken, hslug, userSlug string) error {
    body, _ := json.Marshal(map[string]string{"userSlug": userSlug})
    reqURL := fmt.Sprintf("%s/admin/clusters/%s/grants", adminURL, hslug)
    httpReq, _ := http.NewRequest(http.MethodPost, reqURL, bytes.NewReader(body))
    httpReq.Header.Set("Content-Type", "application/json")
    if adminToken != "" {
        httpReq.Header.Set("Authorization", "Bearer "+adminToken)
    }
    resp, err := http.DefaultClient.Do(httpReq)
    if err != nil {
        return fmt.Errorf("POST /admin/clusters/%s/grants: %w", hslug, err)
    }
    defer resp.Body.Close()
    respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
    if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
        return fmt.Errorf("POST /admin/clusters/%s/grants returned %d: %s", hslug, resp.StatusCode, respBody)
    }
    return nil
}
```

**Call in `runTests`** after `createTestUser` returns:

```go
user, err := createTestUser(adminURL, adminToken, ...)
if err != nil { ... return 1 }
fmt.Fprintf(os.Stderr, "integration: created test user slug=%s\n", user.Slug)

// Grant access to the test cluster host so reconciler can provision.
if err := grantClusterAccess(adminURL, adminToken, hslug, user.Slug); err != nil {
    fmt.Fprintf(os.Stderr, "integration: grant cluster access: %v\n", err)
    _ = deleteTestUser(adminURL, adminToken, user.ID)
    return 1
}
```

## Error Handling

If `grantClusterAccess` fails (cluster not found, user not found, or network error), `runTests` deletes the test user and exits 1, which fails the test run loudly. The admin API's `ON CONFLICT DO NOTHING` makes the call idempotent.

## Security

No new surface. Uses the existing admin token-authenticated endpoint.

## Impact

Spec update: `docs/mclaude-cli/spec-cli.md` §Test user setup — add cluster grant step after user creation.

Components implementing the change:
- `mclaude-cli` (`cmd/integration_main_test.go`)

## Scope

**v1:**
- Add `grantClusterAccess` helper to `integration_main_test.go`
- Call `grantClusterAccess(adminURL, adminToken, hslug, user.Slug)` in `runTests` after `createTestUser`

## Integration Test Cases

This ADR is itself a test-infrastructure fix. The observable outcome is `TestIntegration_Import_HappyPath` passes: `importRef` becomes null within 60s, and NATS KV shows at least 1 session entry.

## Implementation Plan

| Component | New/changed lines (est.) | Notes |
|-----------|--------------------------|-------|
| `mclaude-cli/cmd/integration_main_test.go` | ~25 | New helper + one call site in runTests |

**Total estimated tokens:** ~8k
