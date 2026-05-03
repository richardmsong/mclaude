# ADR: Fix import.confirm success reply missing ok:true

**Status**: implemented
**Status history**:
- 2026-05-03: accepted — Class A bug fix; CLI contract expects ok:true, CP omits it
- 2026-05-03: implemented — all scope CLEAN

## Overview

`handleNATSImportConfirm` in the CP sends `{"projectId":..., "projectSlug":...}` on success but the CLI's `importConfirmResponse` struct checks `resp.OK` (maps to `json:"ok"`). Since `"ok"` is absent from the success reply, `resp.OK` deserializes to `false`, causing the CLI to report `"import.confirm failed: "` (with empty error) even when the project was created successfully.

## Motivation

Integration test `TestIntegration_Import_HappyPath` fails:

```
RunImport: confirm import: import.confirm failed: 
Output:
  Confirming import with control-plane...
--- FAIL: TestIntegration_Import_HappyPath
```

The CP side created the project and replies, but `ok` is missing from the reply. The CLI unconditionally returns an error when `ok != true`.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Fix location | CP `attachments.go:592` | CLI struct and contract are correct; CP success reply just needs `"ok":true` added |
| Reply shape | `{"ok":true,"projectId":"...","projectSlug":"..."}` | Matches NATS reply convention used everywhere else in the codebase |

## Component Changes

### mclaude-control-plane

**`attachments.go:592`** — add `"ok":true` to the success reply:

```go
// Before (missing ok):
reply, _ := json.Marshal(map[string]string{"projectId": projID, "projectSlug": proj.Slug})

// After (correct):
reply, _ := json.Marshal(map[string]any{"ok": true, "projectId": projID, "projectSlug": proj.Slug})
```

Note: change the map value type from `map[string]string` to `map[string]any` to accommodate the bool `ok` field alongside string fields.

## Error Handling

No new error paths. The fix adds `"ok":true` to the success branch only. Error replies already use `replyNATSError` which correctly sends `{"ok":false, "error":"..."}`.

## Security

No security impact.

## Impact

No spec updates needed — this is implementation catching up to the CLI's existing expectation.

Components implementing the change:
- `mclaude-control-plane` (attachments.go)

## Scope

**v1:**
- Add `"ok":true` to `handleNATSImportConfirm` success reply

## Integration Test Cases

| Test case | What it verifies | Setup/teardown | Components exercised |
|-----------|------------------|----------------|----------------------|
| `TestIntegration_Import_HappyPath` (existing) | Import confirms successfully (resp.OK = true) | TestMain creates user; teardown deletes project + user | CP import.confirm, CLI import |

## Implementation Plan

| Component | New/changed lines (est.) | Notes |
|-----------|--------------------------|-------|
| `mclaude-control-plane/attachments.go` | 1 | Add `"ok":true` to marshal call, change type to `map[string]any` |

**Total estimated tokens:** ~15k
