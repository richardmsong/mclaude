# ADR: Fix User-Agent Image Repository Typo

**Status**: implemented
**Status history**:
- 2026-05-07: accepted
- 2026-05-07: implemented — charts-mclaude CLEAN

## Overview

Correct a one-character typo in the `mclaude-worker` Helm chart: `userAgent.image.repository` is set to `ghcr.io/richardmcsong/mclaude-user-agent` (extra `c`) instead of the correct `ghcr.io/richardmsong/mclaude-user-agent`. The typo causes the user-agent Deployment to reference a non-existent GHCR path, producing `ErrImagePull` on every pod start and causing the `aqua-scan-gate` controller to crash-loop on digest resolution failures for the same path.

## Motivation

CI publishes images to `ghcr.io/richardmsong/*` (the correct org). The typo `richardmcsong` means the user-agent image has never been pullable from the path stored in the chart, and the `aqua-scan-gate` controller (which must resolve image digests to gate pods) panics on `MANIFEST_UNKNOWN` for this path, contributing to its persistent `CrashLoopBackOff` (1700+ restarts over 42 days) and breaking scheduling-gate injection for all pods in the cluster.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Correct repository | `ghcr.io/richardmsong/mclaude-user-agent` | Matches the org where CI publishes (`GHCR: ghcr.io/richardmsong` in deploy-main.yml) |
| Default tag | `"main"` (unchanged) | CI publishes both `main-<sha>` and floating `main` tags; no change needed |
| Scope | `charts/mclaude-worker/values.yaml` only | Typo is isolated to this one field; no other chart or config references the wrong path |

## User Flow

No user-visible change. Operators running `helm upgrade mclaude-worker` will get a Deployment that pulls from the correct GHCR path.

## Component Changes

### charts-mclaude (mclaude-worker)

**File modified:**
- `charts/mclaude-worker/values.yaml` — `userAgent.image.repository`: change `ghcr.io/richardmcsong/mclaude-user-agent` → `ghcr.io/richardmsong/mclaude-user-agent`

## Data Model

No schema changes.

## Error Handling

No change. `ErrImagePull` will no longer occur for the user-agent container once the chart is corrected.

## Security

No change.

## Impact

**Specs updated in this commit:**
- `docs/charts-mclaude/spec-helm.md` — correct `[userAgent].[image].[repository]` default value

**Components implementing the change:**
- `charts-mclaude` (mclaude-worker Helm chart)

## Scope

**In scope:**
- Fix `userAgent.image.repository` in `charts/mclaude-worker/values.yaml`
- Update the matching spec entry in `docs/charts-mclaude/spec-helm.md`

**Explicitly deferred / out of scope:**
- Root-causing the `aqua-scan-gate` crash loop for other namespaces (separate issue)

## Integration Test Cases

No integration tests — change is config-only (Helm chart default value correction). The fix is validated by a successful `helm upgrade` and pod pull.

## Implementation Plan

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| charts-mclaude | 1 line changed | ~10k | Single value field correction |

**Total estimated tokens:** ~10k
**Estimated wall-clock:** 0.05h
