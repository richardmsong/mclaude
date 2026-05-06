# ADR: Move NKey challenge nonce store from in-memory to NATS KV

**Status**: draft
**Status history**:
- 2026-05-06: draft

## Overview

Replace the in-memory `challengeStore` (`server/challenge.go`) with a NATS KV bucket (`mclaude-challenges`). The current implementation uses a `sync.Mutex`-guarded map, which means challenge-response auth fails when CP runs multiple replicas — the challenge POST hits pod A, the verify POST hits pod B, and the nonce doesn't exist on B. Moving to NATS KV makes the nonce store shared across all CP replicas.

## Motivation

1. **Multi-replica CP is broken.** The in-memory store is documented as single-replica only (`challenge.go:27`: "Assumes single-replica control-plane deployment"). With 2 replicas, challenge-response auth fails ~50% of the time with `"challenge not found"`. This blocks hosts, agents, and user-agents from authenticating.

2. **NATS KV is already in the dependency graph.** CP already creates and manages multiple KV buckets (`mclaude-hosts`, `mclaude-sessions-{uslug}`, `mclaude-projects-{uslug}`, `mclaude-user-info-{uslug}`). Adding one more is zero new infrastructure.

3. **TTL is built in.** NATS KV buckets support `MaxAge` (per-key TTL). Setting `MaxAge: 30s` on the `mclaude-challenges` bucket auto-expires nonces without a cleanup goroutine — replacing the manual `time.Now().After(entry.expiresAt)` check.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Store backend | NATS KV bucket `mclaude-challenges` | Shared across replicas, auto-TTL, already in dep graph |
| Bucket config | `MaxAge: 30s`, `History: 1`, `Storage: Memory` | Nonces are ephemeral, single-use, 30s TTL. Memory storage avoids disk I/O for high-frequency short-lived keys. |
| Key format | Base64-encoded nonce (same as current map key) | No change to the challenge/verify protocol |
| Value format | JSON `{nkey_public, created_at}` | Matches current `challengeEntry` struct fields (minus `expiresAt` — handled by bucket TTL) |
| Single-use enforcement | Delete key on successful verify (KV Delete = tombstone) | Same as current `delete(entries, challenge)` |
| Fallback | None — if NATS is unavailable, challenge-response fails | NATS unavailability already breaks all CP functionality. No special handling needed. |
| CP replicas | Restore to 2 (or more) after implementation | values-aks.yaml currently set to 1 as a workaround |
| `globalChallengeStore` | Remove entirely | No more in-memory state |

## Component Changes

### `mclaude-control-plane`

**`server/challenge.go`:**
- Replace `challengeStore` struct and `globalChallengeStore` var with a `kvChallengeStore` that holds a `nats.KeyValue` reference.
- `handleAuthChallenge`: instead of `globalChallengeStore.mu.Lock()` + map write, do `kv.Put(base64Nonce, json)`.
- `handleAuthVerify`: instead of `globalChallengeStore.mu.Lock()` + map lookup + delete, do `kv.Get(challenge)` + `kv.Delete(challenge)`.
- Remove the expiry check — bucket `MaxAge` handles it (expired keys return `nats.ErrKeyNotFound`).
- Create the bucket in `NewServer` (or `ensureSystemResources`): `js.CreateKeyValue(&nats.KeyValueConfig{Bucket: "mclaude-challenges", MaxAge: 30 * time.Second, History: 1, Storage: nats.MemoryStorage})`.

**`charts/mclaude-cp/values-aks.yaml`:**
- Restore `controlPlane.replicas: 2` after implementation.

## Impact

- `docs/mclaude-control-plane/spec-control-plane.md`: update challenge endpoint description to note NATS KV backing (replaces "stored in-memory" wording). Remove the "single-replica" caveat.

## Error Handling

- **NATS unavailable during challenge:** Returns 503 `{error: "challenge store unavailable"}`. Same failure mode as all other NATS-dependent operations.
- **Nonce not found on verify (expired or wrong replica in old code):** Returns 401 `{error: "challenge not found", code: "NOT_FOUND"}` — unchanged behavior, but now works correctly across replicas.

## Integration Test Cases

| Test case | What it verifies |
|-----------|------------------|
| Challenge-verify round trip succeeds | `POST /api/auth/challenge` → `POST /api/auth/verify` with valid signature succeeds |
| Expired challenge returns NOT_FOUND | Wait >30s after challenge, verify returns `{error: "challenge not found"}` |
| Challenge is single-use | Verify same challenge twice — second returns NOT_FOUND |
