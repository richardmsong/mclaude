# ADR: Fix adminGrantCluster copying cluster public_key to per-user hosts row

**Status**: accepted
**Status history**:
- 2026-05-03: accepted — Class A bug; INSERT fails with unique constraint violation when cluster has a public_key

## Overview

`POST /admin/clusters/{cslug}/grants` creates a per-user `hosts` row so a user can access a cluster. The handler fetches the cluster owner's `direct_nats_url` and `public_key` from the existing `hosts` row and inserts both into the new per-user row. The `public_key` column has a partial unique index (`UNIQUE WHERE public_key IS NOT NULL`) — so when the cluster owner row has a non-NULL `public_key`, the INSERT for a granted user fails with a unique constraint violation and returns HTTP 500.

## Motivation

Running `POST /admin/clusters/k3d-dev/grants` against a cluster that has a registered NKey (`public_key = UBMXIGPMM6VB...`) returns:

```
HTTP 500: failed to grant cluster access
```

The `k3d-dev` cluster's `public_key` is the controller's NKey identity, not a per-user credential. Copying it to every granted user's `hosts` row is semantically wrong and structurally broken (unique constraint).

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| `public_key` in per-user row | NULL — do not copy from owner row | `public_key` is the cluster controller's NKey identity; per-user rows authenticate via `user_jwt` (JWT challenge-response), not NKey challenge-response. The unique constraint enforces this: one NKey per physical host, not per access grant. |
| `direct_nats_url` in per-user row | Keep copying from owner row | Grants the user the cluster's external NATS WebSocket URL; needed for the SPA to connect to the worker NATS directly. Semantically shared across all users of that cluster. |
| Spec stale known-bugs | Remove `GetUserByEmail` and UUID-slug known bugs | Both are already fixed in the code: line 501 calls `GetUserBySlug`; line 509 calls `IssueHostJWTLegacy(user.Slug, ...)`. The spec entries are stale. |

## Component Changes

### `mclaude-control-plane/admin.go`

**`adminGrantCluster`** — remove `public_key` from both the SELECT and the INSERT:

```go
// Before: fetches public_key from owner row and inserts it
var directNATSURL, publicKey *string
err := s.db.pool.QueryRow(r.Context(), `
    SELECT direct_nats_url, public_key
    FROM hosts WHERE slug = $1 AND type = 'cluster' LIMIT 1`, cslug).
    Scan(&directNATSURL, &publicKey)
// ...
INSERT INTO hosts (id, user_id, slug, name, type, role, direct_nats_url, public_key, user_jwt)
VALUES ($1, $2, $3, $4, 'cluster', 'user', $5, $6, $7)
// $6 = publicKey

// After: only fetch direct_nats_url; public_key always NULL for per-user rows
var directNATSURL *string
err := s.db.pool.QueryRow(r.Context(), `
    SELECT direct_nats_url
    FROM hosts WHERE slug = $1 AND type = 'cluster' LIMIT 1`, cslug).
    Scan(&directNATSURL)
// ...
INSERT INTO hosts (id, user_id, slug, name, type, role, direct_nats_url, user_jwt)
VALUES ($1, $2, $3, $4, 'cluster', 'user', $5, $6)
// no public_key — NULL by default
```

## Error Handling

With `public_key` omitted, the INSERT can still conflict on `(user_id, slug)` — handled by `ON CONFLICT DO NOTHING` (idempotent re-grant). No other failure path is introduced.

## Security

Per-user cluster rows do not hold the cluster's NKey. User auth uses JWT challenge-response via `user_jwt`. No regression: the cluster controller row keeps its `public_key` for hub presence tracking.

## Impact

No spec updates — the spec already says "Creates a new `hosts` row for that user" without specifying `public_key`. Stale "Known bug" entries in the spec (GetUserByEmail, UUID-slug) are removed as part of this ADR since both are already fixed.

Components implementing the change:
- `mclaude-control-plane` (`admin.go`)

## Scope

**v1:**
- Remove `public_key` from the SELECT in `adminGrantCluster`
- Remove `public_key` from the INSERT in `adminGrantCluster`

## Integration Test Cases

Observable outcome: `POST /admin/clusters/k3d-dev/grants` returns HTTP 201 when the cluster has a registered `public_key`. Previously returned HTTP 500. Verified by the CLI integration test run (TestMain calls `grantClusterAccess`).

## Implementation Plan

| Component | New/changed lines (est.) | Notes |
|-----------|--------------------------|-------|
| `mclaude-control-plane/admin.go` | ~5 | Remove public_key from SELECT + INSERT |

**Total estimated tokens:** ~6k
