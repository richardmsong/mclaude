# ADR: Fix import.request reply field name mismatch

**Status**: accepted
**Status history**:
- 2026-05-03: accepted — Class A bug fix; spec is correct, code is wrong

## Overview

The CP's `handleNATSImportRequest` returns `{"importId": ..., "uploadUrl": ...}` but the spec (`spec-nats-activity.md` §import.request) and the CLI's `importRequestResponse` struct both expect `{"id": ..., "uploadUrl": ...}`. Because `json:"id"` ≠ `"importId"`, `resp.ID` is always empty in the CLI, causing the subsequent import.confirm to fail with "importId is required".

## Motivation

Integration tests `TestIntegration_Import_HappyPath` and `TestIntegration_Import_SlugCollision` fail:

```
RunImport: confirm import: import.confirm failed: importId is required
Output:
  Upload URL obtained (import ID: )   ← empty because resp.ID deserialized as ""
```

The spec at `docs/spec-nats-activity.md` line 929 shows the correct response:
```json
{"id":"imp-001","uploadUrl":"https://s3..."}
```

The CP at `attachments.go:503` sends:
```go
json.Marshal(map[string]string{"importId": importID, "uploadUrl": uploadURL})
```

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Which side is wrong | CP `attachments.go:503` | Spec and CLI both use `"id"`; the CP uses `"importId"` — one char change fixes it |
| Fix scope | Change the map key from `"importId"` to `"id"` in the marshal call | CLI struct and spec already correct |

## Component Changes

### mclaude-control-plane

**`attachments.go:503`** — change the reply map key:

```go
// Before (wrong):
reply, _ := json.Marshal(map[string]string{"importId": importID, "uploadUrl": uploadURL})

// After (correct — matches spec and CLI struct json tag):
reply, _ := json.Marshal(map[string]string{"id": importID, "uploadUrl": uploadURL})
```

## Error Handling

No new error handling. The fix restores the CLI's ability to read the importId from the response.

## Security

No security impact.

## Impact

No spec updates — spec already describes `"id"`.

Components implementing the change:
- `mclaude-control-plane` (attachments.go)

## Scope

**v1:**
- Fix the map key from `"importId"` to `"id"` in `handleNATSImportRequest`

## Integration Test Cases

| Test case | What it verifies | Setup/teardown | Components exercised |
|-----------|------------------|----------------|----------------------|
| `TestIntegration_Import_HappyPath` (existing) | Import completes end-to-end with non-empty importId | TestMain creates user; teardown deletes project + user | CP import.request, CLI import |
| `TestIntegration_Import_SlugCollision` (existing) | Second import collision also completes | Same | CP import.request, CLI import |

## Implementation Plan

| Component | New/changed lines (est.) | Notes |
|-----------|--------------------------|-------|
| `mclaude-control-plane/attachments.go` | 1 | Change `"importId"` to `"id"` in map literal |

**Total estimated tokens:** ~15k
