## Run: 2026-05-02T00:00:00Z

# Spec Alignment Audit: ADR-0075 Fix hostauth Challenge Signing

**ADR**: `docs/adr-0075-fix-hostauth-challenge-signing.md`
**Status**: accepted
**Specs evaluated**: `docs/mclaude-common/spec-common.md`, `docs/mclaude-control-plane/spec-control-plane.md`
**Cross-spec check**: all spec files grepped for challenge-signing concepts

---

## Phase 0b — Cross-Spec Consistency Check

Key concepts touched by this ADR:
- What data is signed during `Refresh()`: raw nonce bytes (base64-decoded) vs. base64 string
- `POST /api/auth/verify` server-side verification: over raw decoded bytes

Additional specs mentioning challenge-response signing:
- `docs/spec-nats-payload-schema.md` line 1443: `"signature" | Ed25519 signature of the challenge nonce, signed with the NKey seed. Base64-encoded.`
  - This description omits whether "challenge nonce" means the base64 string or the decoded raw bytes. Silent on the precision the ADR mandates.
- `docs/mclaude-controller-local/spec-controller-local.md` line 62: sends challenge/verify but does not specify what is signed.
- `docs/mclaude-controller-k8s/spec-k8s-architecture.md` line 15: calls `Refresh()` but does not describe the byte-level signing detail.
- `docs/mclaude-session-agent/spec-session-agent.md` line 431: mentions challenge-response but no byte-level detail.

None of these additional specs explicitly contradict the ADR's decision (they are silent, not wrong), and they all delegate the implementation to `pkg/hostauth` — the one place the ADR mandates the fix. The ADR's Impact section does not identify them as requiring updates, and since they describe behavior abstractly, they do not create a risk of re-implementing the bug independently. No cross-spec inconsistency requiring a `GAP` is introduced.

---

## Phase 1 — ADR → Spec (Forward Pass)

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| Line 22 (Decisions row 1) | "What data to sign: Raw nonce bytes (base64-decoded challenge)" | spec-common.md line 137: "Base64-decodes `challenge` to raw nonce bytes, then signs those bytes with the NKey private key: `sig = kp.Sign(base64.StdEncoding.DecodeString(challenge))`." | REFLECTED | — | spec-common.md `Refresh()` description explicitly states base64-decode before signing. |
| Line 23 (Decisions row 2) | "Where to fix: `mclaude-common/pkg/hostauth/host_auth.go`" | spec-common.md line 133: "`Refresh(ctx context.Context) (jwt string, err error)` — Performs HTTP challenge-response…" | REFLECTED | — | spec-common.md describes Refresh() as the client-side signing point; this is consistent. No spec change needed for the "where to fix" decision. |
| Line 23 (Decisions row 3) | "Spec precision: Add explicit language: client decodes base64 challenge to raw bytes before signing. Both spec-common.md (client perspective) and spec-control-plane.md (server perspective) must be updated." | spec-common.md line 137 (client), spec-control-plane.md line 55 (server) | REFLECTED | — | See rows below for each spec side. |
| Lines 136–138 (spec-common.md Refresh step 2) | "Base64-decodes `challenge` to raw nonce bytes, then signs those bytes with the NKey private key: `sig = kp.Sign(base64.StdEncoding.DecodeString(challenge))`." | spec-common.md line 137 (exact match) | REFLECTED | — | The spec contains this exact language. |
| Lines 29–45 (Component Changes go code block) | `nonceBytes, err := base64.StdDecoding.DecodeString(challenge)` … `sig, err := h.kp.Sign(nonceBytes)` | spec-common.md line 137 captures the semantics (not the code), which is appropriate for a spec | REFLECTED | — | Spec describes the contract; implementation detail in ADR is aligned. |
| Lines 47 (import block) | "Add `\"encoding/base64\"` to the import block." | Not mentioned in spec (import-level implementation detail) | REFLECTED | — | Import changes are implementation detail, not a spec-level decision. No spec entry needed. |
| Lines 52–53 (Impact: spec-common.md update) | "`docs/mclaude-common/spec-common.md` — `Refresh()` description: add explicit statement that the challenge is base64-decoded to raw bytes before signing." | spec-common.md line 137: present-tense explicit statement exists | REFLECTED | — | ADR's claimed spec update is present in the spec. |
| Lines 54–55 (Impact: spec-control-plane.md update) | "`docs/mclaude-control-plane/spec-control-plane.md` — `POST /api/auth/verify` description: clarify that the signature is verified over the raw nonce bytes obtained by base64-decoding the challenge field." | spec-control-plane.md line 55: "CP base64-decodes `challenge` to raw nonce bytes, then verifies the Ed25519 signature over those raw bytes against the stored public key (`nkeys.FromPublicKey(pub).Verify(rawNonce, sig)`)." | REFLECTED | — | The spec contains this exact language including the API call. |
| Line 54 (Impact: spec-nats-payload-schema.md update) | "`docs/spec-nats-payload-schema.md` — `signature` field description for `POST /api/auth/verify`: add explicit statement that the signature covers raw decoded bytes, not the base64 string." | spec-nats-payload-schema.md line 1443: "Ed25519 signature of the raw nonce bytes. Client must base64-decode the `challenge` string to its 32-byte nonce, then sign those bytes with the NKey private key — not the base64 string itself. Base64-encoded." | REFLECTED | — | The ADR's Impact claims this spec was updated, and the spec contains explicit unambiguous language matching that claim. |
| Line 141 (sentinel error + log message for 404) | "When constructed via `NewHostAuthFromSeed` and the public key is not yet registered (CP returns HTTP 404), `Refresh()` returns a sentinel retryable error and logs: `NKey <pubkey> not registered…`" | spec-common.md lines 141–146: identical behavior described | REFLECTED | — | Pre-existing behavior, confirmed still present in spec; ADR doesn't change this. |

---

### Summary

- Reflected: 10
- Gap: 0
- Partial: 0

## Run: 2026-05-02T00:01:00Z (re-evaluation with spec-nats-payload-schema.md included)

All three specs listed in the ADR's Impact section were evaluated. The prior run noted spec-nats-payload-schema.md line 1443 as potentially ambiguous based on a cross-spec grep that found an older version of the text. After reading the file directly, the spec at line 1443 contains the explicit, precise language the ADR mandated:

> "Ed25519 signature of the raw nonce bytes. Client must base64-decode the `challenge` string to its 32-byte nonce, then sign those bytes with the NKey private key — not the base64 string itself. Base64-encoded."

This resolves the prior run's GAP finding. All three specs are fully aligned. Result: **CLEAN**.
