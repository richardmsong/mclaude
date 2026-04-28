# ADR: Fix mclaude-controller-k8s Boot

**Status**: implemented
**Status history**:
- 2026-04-27: accepted
- 2026-04-27: implemented — all pods Running 1/1, zero restarts

## Overview

Fix two boot failures in the mclaude-controller-k8s binary that prevent the worker controller pod from starting.

## Motivation

The controller pod crash-loops with `log.SetLogger(...) was never called` from controller-runtime, and would also fail to access JetStream (same unauthenticated NATS connection bug fixed for control-plane in ADR-0038).

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Controller-runtime logger | Call `ctrl.SetLogger(zerologr)` before `ctrl.NewManager()`. Use a `zerologr` adapter or the `logr/zerologr` bridge. If no zerologr adapter is available, use `zap` via `zap.New()` from controller-runtime's zap package, which is the standard controller-runtime logger. | controller-runtime v0.23+ panics if SetLogger is never called. |
| NATS JWT auth | Same pattern as control-plane (ADR-0038): generate a user JWT from the account seed, connect with `nats.UserJWT()`. | Worker NATS runs JWT auth; unauthenticated connections can't access JetStream. |
| NATS_ACCOUNT_SEED env | Add to the controller Deployment template from the operator-keys Secret, same as the control-plane fix. | The controller needs the account seed to generate user JWTs for NATS auth. |

## Impact

No specs updated — bug fix.

**Components:**
- `mclaude-controller-k8s/main.go` — add logger setup + NATS JWT auth
- `charts/mclaude-worker/templates/controller-deployment.yaml` — add NATS_ACCOUNT_SEED env from operator-keys Secret

## Scope

Bug fix only.
