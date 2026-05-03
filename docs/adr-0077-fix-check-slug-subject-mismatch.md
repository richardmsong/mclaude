# ADR: Fix CP check-slug NATS subscription subject mismatch

**Status**: implemented
**Status history**:
- 2026-05-02: accepted — Class A bug fix; spec is correct, code is wrong
- 2026-05-03: implemented — all scope CLEAN

## Overview

The control-plane subscribes to `mclaude.users.*.hosts.*.projects.*.check-slug` (7 segments, extra `{pslug}` wildcard) but the spec and CLI both use `mclaude.users.{uslug}.hosts.{hslug}.projects.check-slug` (6 segments, no `{pslug}`). The subjects never match, so check-slug requests get no responder and import fails.

## Motivation

Integration tests for `mclaude import` (`TestIntegration_Import_HappyPath`) revealed:

```
check-slug request to mclaude.users.cli-test-*.hosts.dev-local.projects.check-slug: nats: no responders available for request
```

The CLI publishes to the 6-segment subject (correct per spec). The CP subscription pattern has an extra `*.` segment, so it never receives the message.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Which side is wrong | CP `lifecycle.go` subscription | Spec and CLI agree on 6-segment pattern; CP comment on line 81 also incorrectly shows `{pslug}` |
| Fix scope | Change subscription pattern only | No behavior change — handler logic is correct; only the subject filter is wrong |
| No spec update needed | Spec already describes `projects.check-slug` (no pslug) | This is a pure code-vs-spec divergence |

## Component Changes

### mclaude-control-plane

**`lifecycle.go`** — fix the check-slug subscription:

```go
// Before (wrong — 7 segments, mismatches CLI):
// check-slug: mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.check-slug
if _, err := nc.Subscribe("mclaude.users.*.hosts.*.projects.*.check-slug", ...)

// After (correct — 6 segments, matches CLI and spec):
// check-slug: mclaude.users.{uslug}.hosts.{hslug}.projects.check-slug
if _, err := nc.Subscribe("mclaude.users.*.hosts.*.projects.check-slug", ...)
```

Also fix the inline comment on the preceding line to remove `{pslug}`.

## Error Handling

No new error handling. The fix restores the existing handler to receiving requests it previously never saw.

## Security

No security impact. The subscription pattern change does not broaden access — it corrects the filter to match the subject the CLI actually publishes.

## Impact

No spec updates — specs are already correct.

Components implementing the change:
- `mclaude-control-plane` (lifecycle.go)

## Scope

**v1:**
- Fix subscription pattern in `lifecycle.go`
- Fix inline comment

**Deferred:**
- None

## Integration Test Cases

| Test case | What it verifies | Setup/teardown | Components exercised |
|-----------|------------------|----------------|----------------------|
| `TestIntegration_Import_HappyPath` (existing) | Import completes end-to-end; check-slug succeeds with no-responder error gone | TestMain creates user; teardown deletes project + user | CP lifecycle, CLI import, NATS |
| `TestIntegration_Import_SlugCollision` (existing) | Second import with same CWD detects collision via check-slug | Same as above | CP lifecycle, CLI import |

## Implementation Plan

| Component | New/changed lines (est.) | Notes |
|-----------|--------------------------|-------|
| `mclaude-control-plane/lifecycle.go` | 2 | One subscription string, one comment |

**Total estimated tokens:** ~20k
