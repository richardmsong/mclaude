# ADR: Remove IssueHostJWTLegacy — hosts must provide their own NKey

**Status**: implemented
**Status history**:
- 2026-05-06: accepted
- 2026-05-06: implemented — all scope CLEAN

## Overview

Remove `IssueHostJWTLegacy` from `auth/nkeys.go` and the legacy else branch in the host device-code registration handler (`server/hosts.go`). All hosts generate their own NKey pairs (via the `gen-host-nkey` Helm Job for K8s hosts) and provide `publicKey` during device-code registration. The server never generates NKey pairs for hosts — same principle as ADR-0100 (users) and ADR-0054 (all identities own their NKeys).

## Motivation

1. **Dead code.** Since ADR-0097, all hosts register with `--nkey-public` (required). The `gen-host-nkey` Job generates the NKey at Helm install time. No client path omits `publicKey`.

2. **Violates ADR-0054.** The legacy path generates an NKey server-side and returns the seed — the host can never use this seed because it's discarded. The JWT is functionally unusable.

3. **Consistency with ADR-0100.** User login already requires `nkey_public`. Host registration should have the same invariant.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| `IssueHostJWTLegacy` | Remove from `auth/nkeys.go` | Dead code, violates NKey ownership model |
| Legacy else branch | Remove from `server/hosts.go:441-448` | `publicKey` is always provided |
| `publicKey` validation | If `entry.PublicKey == ""`, return 400 `{error: "public_key is required"}` | Fail fast instead of silently generating unusable credentials |
| Also remove `NKeySeed` from `HostResponse` if present | Remove | Server never returns host seeds |

## Component Changes

### `mclaude-control-plane`

- `auth/nkeys.go`: delete `IssueHostJWTLegacy` function (lines 375-399).
- `server/hosts.go`: remove the `else` branch at lines 441-448. Add validation: if `entry.PublicKey == ""`, return 400 `{error: "public_key is required"}` before JWT issuance.
- `server/hosts.go`: remove `NKeySeed` field from `HostResponse` struct if present (stale field, never populated in production).

## Impact

- No spec changes needed — specs already state hosts own their NKeys (ADR-0054, ADR-0097).

## Integration Test Cases

| Test case | What it verifies |
|-----------|------------------|
| Host device-code registration without `publicKey` returns 400 | Device-code redeem with no `publicKey` stored → 400 `{error: "public_key is required"}` |
