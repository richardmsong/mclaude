# ADR: Fix Missing `gen-host-nkey` Subcommand in control-plane Binary

**Status**: implemented
**Status history**:
- 2026-05-02: accepted — no spec update required (Class A bug)
- 2026-05-02: implemented — all scope CLEAN

## Overview

The `control-plane` binary is missing the `gen-host-nkey` subcommand dispatch in `main.go`. The `mclaude-worker` Helm chart's pre-install/pre-upgrade Job runs `control-plane gen-host-nkey`, but `main.go` only handles `init-keys`. When `gen-host-nkey` is passed, the process falls through to the normal server startup path and fatals with "DATABASE_DSN required". This causes every worker Helm upgrade to fail.

## Motivation

CI deploy run 25261636993 succeeded on the control-plane upgrade (minio-bucket hook passed with `minio/mc:latest` after ADR-0072). The worker upgrade then failed immediately:

```
pre-upgrade hooks failed: resource not ready, name: mclaude-worker-gen-host-nkey, kind: Job, status: Failed
context deadline exceeded
```

Pod log:
```json
{"level":"fatal","component":"control-plane","time":"2026-05-02T20:56:04Z","message":"DATABASE_DSN required"}
```

The Job pod starts (`gen-host-nkey-lt26z`) and exits with error in ~4 seconds — `BackoffLimitExceeded` with `backoffLimit: 0`. This is not a timing issue; the binary is simply not dispatching the subcommand.

ADR-0063 specifies this Job and its behavior. `spec-helm.md` documents the Job as: "Generates NKey pair via `nkeys.CreateUser()` (U-prefix). Writes decorated seed string to Secret `{release}-host-creds` field `nkey_seed`. Prints public key to Job log and NOTES.txt. Idempotent — skips if Secret exists." The spec is correct; the code is wrong.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Where to dispatch | `main.go` early-exit block (same as `init-keys`) | Consistent with existing pattern; subcommand exits before any server init |
| Implementation file | New `gen_host_nkey.go` | Same pattern as `init_keys.go`; separates concern cleanly |
| NKey type | `nkeys.CreateUser()` | Per ADR-0063 and spec-helm.md — U-prefix keys are the correct type for host identities |
| Secret field name | `nkey_seed` | Per spec-helm.md and the controller's `HOST_NKEY_SEED_PATH` mount |
| Idempotency | Skip if Secret already exists (exit 0) | Per spec-helm.md: "Idempotent — skips if Secret exists." Pre-upgrade hook runs on every `helm upgrade`; must not overwrite an existing key |
| Seed format | Raw nkeys seed bytes (output of `kp.Seed()`) | Controller reads the file and passes to `nkeys.FromSeed()` — same pattern as init-keys writing `accountSeed` |

## Component Changes

### `mclaude-control-plane/main.go`

Add `gen-host-nkey` subcommand dispatch immediately after the `init-keys` check:

```go
if len(os.Args) > 1 && os.Args[1] == "gen-host-nkey" {
    runGenHostNkey()
    return
}
```

### `mclaude-control-plane/gen_host_nkey.go` (new file)

Implements `runGenHostNkey()`:

1. Read env: `NAMESPACE` (default `mclaude-system`), `HOST_CREDS_SECRET` (required — the Secret name, e.g. `mclaude-worker-host-creds`).
2. Build in-cluster K8s client via `rest.InClusterConfig()` + `kubernetes.NewForConfig()`.
3. GET the Secret. If it exists → log "already exists — skipping" and exit 0 (idempotent).
4. Generate NKey pair: `kp, err := nkeys.CreateUser()`.
5. Extract seed: `seed, err := kp.Seed()`.
6. Extract public key: `pub, err := kp.PublicKey()`.
7. Create K8s Secret with `Data: map[string][]byte{"nkey_seed": seed}`.
8. Log public key: `logger.Info().Str("nkeyPublic", pub).Msg("generated host NKey")` — this is what the operator reads from `kubectl logs job/...` to pass to `mclaude host register --nkey-public`.
9. Exit 0.

Error handling:
- `rest.InClusterConfig()` failure → fatal (must run inside K8s)
- `HOST_CREDS_SECRET` empty → fatal ("HOST_CREDS_SECRET env is required")
- Secret GET non-404 error → fatal
- `nkeys.CreateUser()` or `kp.Seed()` error → fatal
- Secret CREATE fails with AlreadyExists (race) → log and exit 0 (idempotent)
- Other CREATE error → fatal

## Impact

Specs updated in this commit:
- None — this is a class A bug fix. Spec already describes the correct behavior.

Components implementing the change:
- `mclaude-control-plane` (main.go + new gen_host_nkey.go)

## Scope

**In this change:** Add `gen-host-nkey` subcommand dispatch and implementation.

**Deferred:** Nothing — the fix is complete once the subcommand is implemented.

## Integration Test Cases

| Test case | What it verifies | Components exercised |
|-----------|------------------|----------------------|
| Worker Helm upgrade succeeds | `gen-host-nkey` pre-upgrade Job completes (Secret exists → idempotent skip); upgrade proceeds | control-plane binary, gen-host-nkey Job, deploy-main.yml |
| Fresh install (no prior Secret) | Job generates NKey pair, writes Secret, prints public key; exits 0 | control-plane binary, gen-host-nkey Job |
| Idempotent upgrade | Job sees existing Secret, logs skip message, exits 0 | control-plane binary, gen-host-nkey Job |
