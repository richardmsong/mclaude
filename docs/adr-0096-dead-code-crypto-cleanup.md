# ADR: Dead code removal, crypto stdlib migration, and control-plane package restructure

**Status**: accepted
**Status history**:
- 2026-05-05: draft
- 2026-05-05: accepted — no spec changes (class D refactor)

## Overview

Remove dead code paths across all components, migrate custom X25519/sealed-box crypto to Go stdlib (`crypto/ecdh`), and restructure all Go components from flat `package main` monoliths into idiomatic Go sub-packages (``, `cmd/`). This is a class D refactor — no behavior changes.

## Motivation

1. **Dead endpoints.** The SPA calls `/auth/sso/{provider}` and `/auth/logout` — neither has a backend handler. The frontend silently swallows failures, but the code is misleading.

2. **Dead reconcile functions.** `reconcileUserCLIConfig` is a no-op per ADR-0035 (K8s Secret management moved to controller-k8s). `ReconcileAllUserCLIConfigs` is never called from `main.go`. `buildGHHostsYAML` and `buildGlabConfigYAML` produce output that `reconcileUserCLIConfig` discards. All of these are dead weight.

3. **Hand-rolled crypto.** `mclaude-user-agent/x25519.go` manually clamps X25519 private keys (`priv[0] &= 248; priv[31] &= 127; priv[31] |= 64`) instead of using `crypto/ecdh.X25519()` which handles this internally. `mclaude-user-agent/sealedbox.go` reimplements the libsodium `crypto_box_seal` wire format using raw `x/crypto/nacl/box` + `x/crypto/blake2b`. The crypto is correct but unnecessarily low-level — Go 1.20+ provides `crypto/ecdh` for key generation, and the sealed box format is a thin wrapper over NaCl box that should be clearly isolated.

4. **Flat `package main` monoliths.** All Go components except `mclaude-cli` (already structured) are flat `package main` with no sub-packages. `mclaude-control-plane` has 23 files / 19.7k lines and `mclaude-session-agent` has 18 files / 18.5k lines — both well past the point where sub-packages improve navigability, testability, and dependency clarity. Even the smaller components (`mclaude-controller-k8s`, `mclaude-user-agent`, `mclaude-relay`) benefit from the `cmd/` + `` split for consistency.

5. **ADR-named test files.** `adr0065_integration_test.go`, `adr0065_unit_test.go`, `adr0091_integration_test.go` — tests named after the ADR that introduced them rather than the feature they test. Should be consolidated into topic-based test files.

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
- `sealedbox.go`: keep Blake2b nonce derivation + `box.Open` (no stdlib equivalent). Key parameter types stay as `*[32]byte` — `nacl/box.Open` requires `*[32]byte` anyway, so converting from `ecdh` types would add `.Bytes()` calls with no benefit. The `X25519Keypair` struct keeps `[32]byte` fields; `GenerateX25519Keypair` internally uses `crypto/ecdh` for generation then copies bytes out.

### `mclaude-cli`

**Audit `nacl/box` usage:**
- `cmd/anthropic.go:40`: uses `nacl/box` for sealing. Apply same treatment — use `crypto/ecdh` for key generation, keep `box.Seal` for the NaCl operation.

### Package restructure (all Go components)

**Target layout per component:**

```
mclaude-<component>/
  cmd/<component>/main.go        # entrypoint only — wires dependencies, starts server
  <domain>/                      # one package per domain area
  ...
```

**`mclaude-control-plane`** (23 files → ~8 packages):

| Package | Current files | Responsibility |
|---|---|---|
| `cmd/control-plane/` | `main.go` | Entrypoint, flag parsing, dependency wiring |
| `db/` | `db.go` | Postgres pool, migrations, queries |
| `auth/` | `auth.go`, `challenge.go`, `device_auth.go`, `nkeys.go` | Login, JWT, NKey challenge/verify, device-code |
| `admin/` | `admin.go`, `scim.go` | Admin routes, SCIM |
| `hosts/` | `hosts.go`, `lifecycle.go`, `sys_subscriber.go` | Host management, grants, NATS lifecycle |
| `projects/` | `projects.go`, `project_http.go`, `attachments.go`, `s3.go` | Project CRUD, provisioning, S3 attachments |
| `providers/` | `providers.go` | OAuth provider management |
| `server/` | `server.go`, `context.go`, `metrics.go`, `tracing.go`, `version.go` | HTTP server, middleware, observability |

**`mclaude-session-agent`** (18 files + existing `internal/drivers/` → ~5 packages):

| Package | Current files | Responsibility |
|---|---|---|
| `cmd/session-agent/` | `main.go` | Entrypoint, flag parsing, dependency wiring |
| `agent/` | `agent.go`, `daemon.go`, `daemon_jobs.go`, `events.go`, `quota_monitor.go`, `metrics.go`, `state.go` | Core agent logic, NATS subscriptions, daemon lifecycle |
| `session/` | `session.go`, `terminal.go`, `worktree.go`, `fswatch.go`, `shell_split.go`, `debug.go` | Session lifecycle, terminal management, worktree ops |
| `auth/` | `agent_auth.go` | NKey auth, challenge-response |
| `storage/` | `attachment.go`, `import.go`, `gitcreds.go` | S3 attachments, project import, git credential helpers |
| `drivers/` | existing `internal/drivers/` (6 files) | Moves from `internal/drivers/` to `drivers/` (public) |

**`mclaude-controller-k8s`** (4 files):

| Package | Responsibility |
|---|---|
| `cmd/controller-k8s/` | Entrypoint |
| `controller/` | NATS subscription, pod/namespace reconciliation |

**`mclaude-user-agent`** (6 files):

| Package | Responsibility |
|---|---|
| `cmd/user-agent/` | Entrypoint |
| `agent/` | Core agent logic, NATS subscriptions |
| `crypto/` | X25519 keypair, sealed box (post-stdlib migration) |
| `auth/` | NKey auth, challenge-response |

**`mclaude-relay`** (4 files):

| Package | Responsibility |
|---|---|
| `cmd/relay/` | Entrypoint |
| `relay/` | WebSocket tunnel, connection management |

Note: The relay Dockerfile uses a multi-stage build with `RUN go build ... -o relay .` inside the container. After restructuring, the build target changes from `.` to `./cmd/relay/`. The Dockerfile `RUN` command must be updated accordingly.

**`mclaude-user-agent`** — the user-agent binary is currently `COPY`'d from the build context root (no multi-stage Dockerfile build). CI (`deploy-main.yml`) has no `go build` step for user-agent. After restructuring:
- Add a new `build-user-agent-binaries` job to `deploy-main.yml` (same pattern as control-plane: `go build -o cmd/user-agent/user-agent-${TARGETARCH} ./cmd/user-agent/`, upload artifact `user-agent-binaries`).
- Add `build-user-agent-image` matrix job (arm64/self-hosted + amd64/ubuntu-latest) and `merge-user-agent` manifest job.
- Dockerfile updates `COPY` path to `cmd/user-agent/user-agent-${TARGETARCH}`.

**`mclaude-cli`** — already has sub-packages (`cmd/`, `context/`, `events/`, `renderer/`, `repl/`). Package structure stays. Changes needed:
- Update all `mclaude.io/common` imports to point to `mclaude-control-plane` packages (`slug`, `subjects`, `nkeys`).
- Update `go.mod`: drop `replace mclaude.io/common => ../mclaude-common`, add `require mclaude-control-plane` with `replace mclaude-control-plane => ../mclaude-control-plane`.

**Rules:**
- Packages are public (not `internal/`) — enables cross-module imports between mclaude components and external consumers. This also subsumes `mclaude-common`: shared packages migrate into the component that owns them. Other components import directly. `mclaude-common` is deleted once all consumers are migrated.

**`mclaude-common` package migration map:**

| Package | Current consumers | Destination | Rationale |
|---|---|---|---|
| `slug` | control-plane, session-agent, controller-k8s, cli | `mclaude-control-plane/slug` | CP defines the slug schema; all others are consumers |
| `subj` | control-plane, session-agent | `mclaude-control-plane/subjects` | CP defines the NATS subject hierarchy |
| `nats` (GenerateOperatorAccount) | control-plane, cli | `mclaude-control-plane/nkeys` | CP owns NKey/JWT generation |
| `hostauth` | controller-k8s | `mclaude-control-plane/hostauth` | CP defines the auth protocol; controller-k8s imports cross-module |

**Cross-module import mechanics:**
- Each consuming module's `go.mod` replaces `mclaude.io/common` with a direct dependency on `mclaude-control-plane`: `require mclaude-control-plane v0.0.0` + `replace mclaude-control-plane => ../mclaude-control-plane`.
- Import paths change from `mclaude.io/common/pkg/slug` → `mclaude-control-plane/slug`, `mclaude.io/common/pkg/subj` → `mclaude-control-plane/subjects`, etc.
- `go.work` drops `./mclaude-common` from the workspace.
- `mclaude-common/` directory is deleted after all consumers are migrated.
- `cmd/` contains only `main.go` — wires dependencies, no business logic.
- Test files follow their production files into the new packages.
- ADR-named test files are consolidated into topic-based files during the move.
- `Server` struct (currently in `server.go`) becomes the dependency root — constructed in `cmd/`, passed to sub-packages.

**Build output:** Binary is built into `cmd/<component>/` (e.g. `go build -o cmd/control-plane/control-plane-${TARGETARCH} ./cmd/control-plane/`). Dockerfiles update their `COPY` path accordingly (e.g. `COPY cmd/control-plane/control-plane-${TARGETARCH} /usr/local/bin/control-plane`). CI `go build` commands update their `-o` flag and working directory. The binary name and container entrypoint stay the same.

### `mclaude-web` (SPA)

**Remove dead endpoint calls:**
- `transport/auth-client.ts:26`: remove `loginSSO()` method (fetches `/auth/sso/{provider}` — no backend handler).
- `transport/auth-client.ts:45`: remove the backend fetch from `logout()`. Keep the method signature — it still clears client-side state (JWT, localStorage, NATS disconnect).
- `transport/types.ts`: remove `loginSSO()` from `IAuthClient` interface. Keep `logout(): Promise<void>` (method stays, just no backend call).
- `testutil/mock-auth.ts`: remove `loginSSO()` mock implementation.
- `stores/auth-store.ts:80`: remove `loginSSO()` call.
- `stores/auth-store.ts:85`: `logout()` continues to call `this.authClient.logout()` — the method now only does client-side cleanup.
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

**In v1 (continued):**
- Consolidate ADR-named test files into topic-based files: merge `adr0065_integration_test.go` → `device_auth_test.go` / `project_http_test.go`, `adr0065_unit_test.go` → same, `adr0091_integration_test.go` → `grants_test.go` / `integration_test.go`. Remove `ADR0091_` / `ADR0065_` prefixes from test function names. Tests are valid — just poorly organized by origin-ADR instead of feature area.

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
| `mclaude-control-plane` (restructure + dead code) | ~0 net (move + delete) | 200k | Split 23 files into ~8 internal packages, delete reconcile chain, consolidate test files |
| `mclaude-session-agent` (restructure) | ~0 net (move) | 150k | Split 18 files into ~5 internal packages |
| `mclaude-controller-k8s` (restructure) | ~0 net (move) | 50k | Split into cmd/ + controller/ |
| `mclaude-user-agent` (restructure + crypto) | ~-20 / +15 net | 80k | Split into cmd/ + , migrate X25519 to crypto/ecdh |
| `mclaude-relay` (restructure) | ~0 net (move) | 40k | Split into cmd/ + relay/ |
| `mclaude-cli` (crypto migration) | ~-5 / +5 lines | 30k | Update box.Seal key generation |
| `mclaude-web` (remove dead calls) | ~-30 lines | 30k | Remove loginSSO, logout fetch |

**Total estimated tokens:** 580k
**Estimated wall-clock:** 6-8h
