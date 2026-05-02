# ADR: Fix hostauth Challenge Signing — Sign Raw Nonce Bytes Not Base64 String

**Status**: implemented
**Status history**:
- 2026-05-02: accepted — paired with docs/mclaude-common/spec-common.md, docs/mclaude-control-plane/spec-control-plane.md
- 2026-05-02: implemented — all scope CLEAN

## Overview

The `hostauth.Refresh()` method signs the challenge nonce incorrectly. It calls `h.kp.Sign([]byte(challenge))` where `challenge` is the raw base64-encoded string returned by `POST /api/auth/challenge`. But `challenge.go` in the control-plane decodes the challenge from base64 to raw bytes before verifying: `nonce, _ := base64.StdEncoding.DecodeString(req.Challenge)` then `VerifyNKeySignature(req.NKeyPublic, nonce, sig)`. The client signs over the base64 string bytes; the server verifies over the raw decoded bytes. These are different byte sequences, so signature verification always fails with HTTP 401 `UNAUTHORIZED`.

## Motivation

CI deploy run after ADR-0073/0074 fixes: the `mclaude-worker-controller` pod starts successfully, loads the NKey seed, and begins the challenge-response loop. But every `POST /api/auth/verify` call returns HTTP 401 `{"ok":false,"error":"invalid signature","code":"UNAUTHORIZED"}`. Investigation confirmed the mismatch: `hostauth.go:162` signs `[]byte(challenge)` (a ~44-byte base64 string) while `challenge.go` verifies over the decoded 32-byte nonce. The controller loops forever retrying the challenge, unable to acquire a JWT or connect to NATS.

The specs for both components described "sign the challenge" and "verify the signature of the challenge nonce" but did not specify whether "challenge" means the base64 string or the decoded bytes. This ambiguity in the spec allowed the mismatch to go undetected.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| What data to sign | Raw nonce bytes (base64-decoded challenge) | NKey/Ed25519 convention: sign the raw payload, not its encoding. Control-plane already implements this correctly — it decodes then verifies. Fixing the client to match is the only correct interpretation. |
| Where to fix | `mclaude-common/pkg/hostauth/host_auth.go` | Control-plane side is correct. The `Refresh()` method in hostauth is the sole place the signature is computed. |
| Spec precision | Add explicit language: client decodes base64 challenge to raw bytes before signing | Prevents re-introduction of the mismatch in any future reimplementation. Both spec-common.md (client perspective) and spec-control-plane.md (server perspective) must be updated. |

## Component Changes

### `mclaude-common/pkg/hostauth/host_auth.go`

In `Refresh()`, replace:

```go
// OLD — signs the base64 string
sig, err := h.kp.Sign([]byte(challenge))
```

with:

```go
// NEW — decode base64 to raw nonce bytes, sign those
nonceBytes, err := base64.StdEncoding.DecodeString(challenge)
if err != nil {
    return "", fmt.Errorf("decode challenge nonce: %w", err)
}
sig, err := h.kp.Sign(nonceBytes)
```

Add `"encoding/base64"` to the import block.

## Impact

Specs updated in this commit:
- `docs/mclaude-common/spec-common.md` — `Refresh()` description: add explicit statement that the challenge is base64-decoded to raw bytes before signing.
- `docs/mclaude-control-plane/spec-control-plane.md` — `POST /api/auth/verify` description: clarify that the signature is verified over the raw nonce bytes obtained by base64-decoding the challenge field.
- `docs/spec-nats-payload-schema.md` — `signature` field description for `POST /api/auth/verify`: add explicit statement that the signature covers raw decoded bytes, not the base64 string.

Components implementing the change:
- `mclaude-common` (`pkg/hostauth/host_auth.go`)

## Scope

**In this change:** Fix signing in `hostauth.Refresh()`. Update both specs for precision.

**Deferred:** Nothing — the fix is complete once `hostauth.go` decodes before signing.

## Integration Test Cases

| Test case | What it verifies | Components exercised |
|-----------|------------------|----------------------|
| `TestRefresh_SignsRawNonceNotBase64` (unit) | `Refresh()` sends a signature over the raw decoded nonce bytes; mock CP verifies via `nkeys.FromPublicKey(pub).Verify(rawNonce, sig)` | `mclaude-common/pkg/hostauth` |
| K8s controller acquires JWT end-to-end | Controller pod starts, challenge-response succeeds (HTTP 200 from `/api/auth/verify`), JWT stored, NATS connection established | `mclaude-common`, `mclaude-control-plane`, `mclaude-controller-k8s` |
