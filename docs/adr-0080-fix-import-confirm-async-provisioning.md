# ADR: Fix import.confirm async provisioning to avoid CLI timeout

**Status**: implemented
**Status history**:
- 2026-05-03: accepted — Class A bug fix; confirm handler must reply before provisioning
- 2026-05-03: implemented — all scope CLEAN

## Overview

`handleNATSImportConfirm` in the CP calls `nc.Request(provSubject, provData, 30*time.Second)` to trigger controller provisioning — blocking the handler for up to 30 seconds — before sending the reply to the CLI. The CLI's NATS request timeout is 10 seconds, so the CLI times out while the CP is waiting for the controller's provisioning reply. The import fails with "nats: timeout" even though the project was created successfully.

## Motivation

Integration test `TestIntegration_Import_HappyPath` fails with `k3d-dev` as host slug:

```
RunImport: confirm import: import.confirm to .../import.confirm: nats: timeout
```

The controller (`k3d-dev`) subscribes to the provisioning subject and processes the request, but the CP blocks waiting for the controller's reply (up to 30s) before replying to the CLI. The CLI's 10s timeout fires first.

The provisioning step is already marked non-fatal (`log.Warn`) — the CP doesn't use the provisioning reply for anything. It is logically fire-and-forget.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Fix approach | Reply to CLI immediately; trigger provisioning in a goroutine | Provisioning is non-fatal and the CLI doesn't need to wait for it; decouples import confirmation from session-agent creation latency |
| Provisioning still uses Request | Keep `nc.Request` (not `nc.Publish`) in the goroutine | Preserves error logging; fire-and-forget semantics achieved via goroutine |
| HTTP project creation unchanged | Leave `project_http.go` as-is | HTTP handler has its own timeout and benefits from knowing provisioning succeeded |

## Component Changes

### mclaude-control-plane

**`attachments.go`** — reorder: reply to CLI first, then trigger provisioning in a goroutine.

```go
// Before (wrong — blocks CLI for up to 30s):
// Dispatch provisioning request to controller.
if s.nc != nil {
    provReq := ProvisionRequest{...}
    provData, _ := json.Marshal(provReq)
    provSubject := "mclaude.hosts." + hslug + "..."
    if _, reqErr := s.nc.Request(provSubject, provData, 30*time.Second); reqErr != nil {
        log.Warn()...
    }
}

reply, _ := json.Marshal(map[string]any{"ok": true, "projectId": projID, "projectSlug": proj.Slug})
if msg.Reply != "" {
    _ = msg.Respond(reply)
}

// After (correct — reply immediately, provision asynchronously):
reply, _ := json.Marshal(map[string]any{"ok": true, "projectId": projID, "projectSlug": proj.Slug})
if msg.Reply != "" {
    _ = msg.Respond(reply)
}

// Dispatch provisioning in background (non-fatal, CLI already replied).
if s.nc != nil {
    provReq := ProvisionRequest{...}
    provData, _ := json.Marshal(provReq)
    provSubject := "mclaude.hosts." + hslug + "..."
    go func() {
        if _, reqErr := s.nc.Request(provSubject, provData, 30*time.Second); reqErr != nil {
            log.Warn()...
        }
    }()
}
```

## Error Handling

Provisioning errors continue to be logged as warnings. The CLI is decoupled from provisioning latency — if provisioning fails, the import archive remains in S3 and the `importRef` stays set, which is the correct state for retry.

## Security

No security impact.

## Impact

No spec updates — this is an implementation-only fix.

Components implementing the change:
- `mclaude-control-plane` (attachments.go)

## Scope

**v1:**
- Reorder confirm handler: reply first, provision in goroutine

## Integration Test Cases

| Test case | What it verifies | Setup/teardown | Components exercised |
|-----------|------------------|----------------|----------------------|
| `TestIntegration_Import_HappyPath` (existing) | import.confirm returns before controller responds; CLI doesn't time out | TestMain creates user; teardown deletes project + user | CP import.confirm, CLI import |

## Implementation Plan

| Component | New/changed lines (est.) | Notes |
|-----------|--------------------------|-------|
| `mclaude-control-plane/attachments.go` | ~10 | Move reply before provisioning block; wrap provisioning in `go func()` |

**Total estimated tokens:** ~15k
