# ADR: Dead code removal and crypto stdlib migration

**Status**: draft
**Status history**:
- 2026-05-05: draft

## Overview

Remove dead code paths across all components and migrate custom X25519/sealed-box crypto to Go stdlib (`crypto/ecdh`). This is a class D refactor — no behavior changes, just removing code that isn't called and replacing hand-rolled crypto with standard library equivalents.

## Motivation

1. **Dead endpoints.** The SPA calls `/auth/sso/{provider}` and `/auth/logout` — neither has a backend handler. The frontend silently swallows failures, but the code is misleading.

2. **Dead reconcile functions.** `reconcileUserCLIConfig` is a no-op per ADR-0035 (K8s Secret management moved to controller-k8s). `ReconcileAllUserCLIConfigs` is never called from `main.go`. `buildGHHostsYAML` and `buildGlabConfigYAML` produce output that `reconcileUserCLIConfig` discards. All of these are dead weight.

3. **Hand-rolled crypto.** `mclaude-user-agent/x25519.go` manually clamps X25519 private keys (`priv[0] &= 248; priv[31] &= 127; priv[31] |= 64`) instead of using `crypto/ecdh.X25519()` which handles this internally. `mclaude-user-agent/sealedbox.go` reimplements the libsodium `crypto_box_seal` wire format using raw `x/crypto/nacl/box` + `x/crypto/blake2b`. The crypto is correct but unnecessarily low-level — Go 1.20+ provides `crypto/ecdh` for key generation, and the sealed box format is a thin wrapper over NaCl box that should be clearly isolated.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Dead SPA endpoints | Remove frontend calls to `/auth/sso/{provider}` and `/auth/logout` | No backend handler exists. SSO will be re-added via OAuth providers (already implemented). Logout is client-side only (clear JWT from local storage). |
| Dead reconcile chain | Remove `ReconcileAllUserCLIConfigs`, `reconcileUserCLIConfig` (no-op), `buildGHHostsYAML`, `buildGlabConfigYAML`, and all call sites | ADR-0035 moved this responsibility to controller-k8s. The no-op function and its callers are dead weight. |
| X25519 key generation | Replace manual clamping + `x/crypto/curve25519` with `crypto/ecdh.X25519().GenerateKey(rand.Reader)` | stdlib handles clamping internally. Eliminates hand-rolled bit manipulation on crypto keys. |
| Sealed box format | Keep `sealedbox.go` but refactor to use `crypto/ecdh` for key types. The NaCl sealed box wire format (Blake2b nonce derivation + box.Open) has no stdlib equivalent, so the glue code stays but uses stdlib key types. | `x/crypto/nacl/box` is still needed for the actual encryption/decryption (NaCl box is not in stdlib). `x/crypto/blake2b` is still needed for the nonce derivation. The improvement is in key generation and type safety, not in removing the sealed box format code. |
| bcrypt | Keep `x/crypto/bcrypt` | No stdlib equivalent exists. This is the standard Go bcrypt implementation. |

## Component Changes

### `mclaude-control-plane`

**Remove dead reconcile chain:**
- `providers.go`: delete `reconcileUserCLIConfig` (no-op function, ~line 1176), `buildGHHostsYAML` (~line 1181), `buildGlabConfigYAML` (~line 1246), `ReconcileAllUserCLIConfigs` (~line 1632).
- `providers.go`: remove all call sites of `reconcileUserCLIConfig` (~lines 406, 505, 602, 1560, 1585).

### `mclaude-user-agent`

**Migrate X25519 to stdlib:**
- `x25519.go`: replace `GenerateX25519Keypair` implementation:
  - Remove manual `rand.Read` + clamping + `curve25519.X25519`.
  - Use `crypto/ecdh`: `key, _ := ecdh.X25519().GenerateKey(rand.Reader)`.
  - `X25519Keypair.Public` derived from `key.PublicKey().Bytes()`.
  - `X25519Keypair.Private` from `key.Bytes()`.
- `x25519.go`: replace `curve25519.X25519(kp.Private[:], curve25519.Basepoint)` in `LoadOrGenerateX25519Keypair` with `ecdh.X25519().NewPrivateKey(decoded)` → `.PublicKey().Bytes()`.
- `sealedbox.go`: keep Blake2b nonce derivation + `box.Open` (no stdlib equivalent). Consider updating key parameter types from `*[32]byte` to `*ecdh.PrivateKey` / `*ecdh.PublicKey` if it simplifies callers.

### `mclaude-cli`

**Audit `nacl/box` usage:**
- `cmd/anthropic.go:40`: uses `nacl/box` for sealing. Apply same treatment — use `crypto/ecdh` for key generation, keep `box.Seal` for the NaCl operation.

### `mclaude-web` (SPA)

**Remove dead endpoint calls:**
- `transport/auth-client.ts:26`: remove `loginSSO()` method (fetches `/auth/sso/{provider}` — no backend handler).
- `transport/auth-client.ts:45`: remove `logout()` fetch to `/auth/logout` (no backend handler). Keep the client-side JWT clearing logic.
- `stores/auth-store.ts:80`: remove `loginSSO()` call.
- `stores/auth-store.ts:85`: update `logout()` to only clear local state, not call the backend.
- `components/App.tsx`: remove SSO login UI flow if present.

## Data Model

No changes.

## Error Handling

No changes — removed code was either dead or a no-op.

## Security

**Improved.** Replacing manual key clamping with `crypto/ecdh` eliminates a class of potential implementation bugs. The sealed box wire format code remains but uses stdlib key types for generation. bcrypt usage unchanged.

## Impact

- No spec changes — this is dead code removal and internal refactoring with no behavior change.

## Scope

**In v1:**
- Remove dead reconcile chain from control-plane
- Remove dead SPA endpoint calls
- Migrate X25519 key generation to `crypto/ecdh` in user-agent and CLI
- Remove `x/crypto/curve25519` dependency

**Deferred:**
- Replacing `x/crypto/nacl/box` with a pure-stdlib alternative (none exists today)
- Replacing `x/crypto/blake2b` (needed for libsodium sealed box nonce derivation)
- Implementing `/auth/logout` as a real endpoint (JWT revocation — needs design)

## Open questions

_(none — all resolved)_

## Integration Test Cases

No integration tests — change is refactoring/dead-code-removal only. Existing tests verify behavior is preserved.

## Implementation Plan

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| `mclaude-control-plane` (remove reconcile chain) | ~-120 lines | 40k | Delete 4 functions + 5 call sites |
| `mclaude-user-agent` (crypto migration) | ~-20 / +15 lines | 50k | Replace keygen, update sealedbox types |
| `mclaude-cli` (crypto migration) | ~-5 / +5 lines | 30k | Update box.Seal key generation |
| `mclaude-web` (remove dead calls) | ~-30 lines | 30k | Remove loginSSO, logout fetch |

**Total estimated tokens:** 150k
**Estimated wall-clock:** 2-3h
