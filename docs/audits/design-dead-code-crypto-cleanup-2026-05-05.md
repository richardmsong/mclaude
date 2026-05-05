## Run: 2026-05-05T00:00:00Z

**Gaps found: 5**

1. **`sealedbox.go` key type change is undecided** — The doc says "Consider updating key parameter types from `*[32]byte` to `*ecdh.PrivateKey` / `*ecdh.PublicKey` if it simplifies callers" but does not decide. This is a blocking implementation choice: `SealedBoxOpen` currently takes `*[32]byte` for both pub and priv (sealedbox.go:24), and `tokens.go:75` calls it with `&tm.keypair.Public` / `&tm.keypair.Private` — both `[32]byte` fields on `X25519Keypair`. If the dev migrates the signature to `*ecdh.PrivateKey`/`*ecdh.PublicKey`, they must also update `tokens.go` and `sealedbox_test.go`; if they leave it as `*[32]byte`, they must extract `.Bytes()` in `GenerateX25519Keypair` and `LoadOrGenerateX25519Keypair`. The document does not specify which path to take, leaving the implementer to make a design decision.
   - **Doc**: "Consider updating key parameter types from `*[32]byte` to `*ecdh.PrivateKey` / `*ecdh.PublicKey` if it simplifies callers."
   - **Code**: `sealedbox.go:24` — `func SealedBoxOpen(sealed []byte, recipientPub, recipientPriv *[32]byte)`. `tokens.go:75` calls with `&tm.keypair.Public, &tm.keypair.Private` (both `[32]byte`).

2. **CI build commands for user-agent not specified** — The doc states "CI `go build` commands update their `-o` flag and working directory" but `deploy-main.yml` has no `go build` step for `mclaude-user-agent` (only control-plane, session-agent, and controller-k8s are built there). The user-agent Dockerfile (`mclaude-user-agent/Dockerfile`) does a bare `COPY user-agent /usr/local/bin/user-agent` with no multi-stage build, meaning the binary is built externally before `docker build` is run. The doc does not explain where that pre-build step lives or how it changes after restructuring.
   - **Doc**: "CI `go build` commands update their `-o` flag and working directory."
   - **Code**: `deploy-main.yml:41-42` builds `control-plane-{arm64,amd64}`, lines 189-190 build `session-agent-{arm64,amd64}`, lines 277-278 build `controller-k8s-{arm64,amd64}`. No `user-agent` build step exists in `deploy-main.yml`. `mclaude-user-agent/Dockerfile:4` — `COPY --chmod=0755 user-agent /usr/local/bin/user-agent`.

3. **Relay Dockerfile build path not addressed** — The relay Dockerfile is a multi-stage build that runs `go build -o relay .` inside the container (mclaude-relay/Dockerfile:7), not a pre-built binary COPY like other components. The doc's restructure changes the package from `package main` at root to `cmd/relay/main.go`, which breaks `go build -o relay .` (the `.` would now need to be `./cmd/relay/`). The doc says Dockerfiles update their `COPY` path but does not mention the relay's build stage `RUN` command which also needs updating.
   - **Doc**: "Dockerfiles update their `COPY` path accordingly."
   - **Code**: `mclaude-relay/Dockerfile:7` — `RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o relay .` (builds from package root, not `./cmd/relay/`).

4. **`IAuthClient` interface not updated for `loginSSO` removal** — The doc lists removing `loginSSO()` from `transport/auth-client.ts` and `stores/auth-store.ts`, but `loginSSO(provider: string): Promise<string>` is also declared in the `IAuthClient` interface (`types.ts:88`) and implemented in `testutil/mock-auth.ts:29`. The doc does not mention removing it from either location. A developer implementing the SPA changes would encounter TypeScript compilation errors if they delete the `AuthClient.loginSSO` method without also updating `IAuthClient` and the mock.
   - **Doc**: "remove `loginSSO()` method" — references only `transport/auth-client.ts:26` and `stores/auth-store.ts:80`.
   - **Code**: `types.ts:88` — `loginSSO(provider: string): Promise<string>`. `testutil/mock-auth.ts:29` — `async loginSSO(_provider: string): Promise<string>`.

5. **`logout()` keep-or-remove decision is ambiguous for the `IAuthClient` interface** — The doc says remove the `logout()` fetch to `/auth/logout` in `auth-client.ts` and "update `logout()` to only clear local state." But `logout(): Promise<void>` is part of `IAuthClient` (`types.ts:90`) and the method body in `auth-store.ts:83-89` calls `this.authClient.logout()` which chains NATS close and state reset. The doc does not state whether `logout()` stays in `IAuthClient` (with a simplified no-fetch implementation) or is removed. If it stays, the mock needs updating; if it's removed from the interface, every caller (`App.tsx:541`) must be updated directly. Neither path is specified.
   - **Doc**: "remove `logout()` fetch to `/auth/logout`... Keep the client-side JWT clearing logic."
   - **Code**: `types.ts:90` — `logout(): Promise<void>` in `IAuthClient`. `auth-store.ts:85` — `await this.authClient.logout()`. `App.tsx:541` — `await authStore.logout()`.

## Run: 2026-05-05T09:00:00Z

**Gaps found: 2**

1. **CI build for `mclaude-user-agent` not specified** — The doc says "the CI must add a `go build` step (same pattern as control-plane/session-agent/controller-k8s)" but does not say which workflow file gets the new job, what artifact name is used, or how the pre-built binary reaches the Dockerfile `COPY` step. `deploy-main.yml` has zero references to `user-agent` today. A developer implementing this has no specification for where to add the new CI job.
   - **Doc**: "If no multi-stage build exists, the CI must add a `go build` step (same pattern as control-plane/session-agent/controller-k8s)." (Component Changes section, `mclaude-user-agent` note)
   - **Code**: `deploy-main.yml` — no `user-agent` build job exists. `mclaude-user-agent/Dockerfile:4` — `COPY --chmod=0755 user-agent /usr/local/bin/user-agent` (expects pre-built binary, no multi-stage build).

2. **`mclaude-common` package migration destinations incomplete** — The doc says `mclaude-common` is deleted and its packages migrate to the owning component, giving two examples (`subj` helpers → `mclaude-control-plane/subjects`, KV types → `mclaude-control-plane/kvstate`). But four packages are actively imported across components: `slug`, `subj`, `nats`, and `hostauth`. The doc does not specify where `slug`, `nats`, or `hostauth` land. `hostauth` is imported only by `mclaude-controller-k8s`, so if it moves into `mclaude-control-plane`, controller-k8s gains a cross-component import — a non-trivial architectural decision the doc leaves open.
   - **Doc**: "shared types currently in `mclaude.io/common` migrate into the component that owns them (e.g. NATS subject helpers → `mclaude-control-plane/subjects`, shared KV types → `mclaude-control-plane/kvstate`). Other components import directly. `mclaude-common` is deleted once all consumers are migrated."
   - **Code**: `slug` — imported by control-plane, session-agent, controller-k8s, cli. `subj` — imported by control-plane, session-agent. `nats` — imported by control-plane (`init_keys.go`), cli. `hostauth` — imported by controller-k8s (`main.go:35`) only.

## Run: 2026-05-05T14:00:00Z

**Gaps found: 3**

1. **`mclaude-session-agent` file-to-package mapping not specified** — The doc gives only responsibility labels for 5 session-agent packages but no file assignments. There are 18 source files to redistribute, and many have ambiguous ownership: `attachment.go`, `debug.go`, `events.go`, `fswatch.go`, `gitcreds.go`, `metrics.go`, `quota_monitor.go`, `shell_split.go`, `state.go`, `worktree.go`, `daemon_jobs.go` do not obviously map to `agent/`, `session/`, `auth/`, or `sandbox/` by name alone. Additionally, `internal/drivers/` (6 files, currently a sub-package) is not mentioned — a developer must decide whether it stays as `internal/drivers/` inside the new layout or migrates into one of the listed packages.
   - **Doc**: Table with 5 packages for session-agent, responsibility labels only — no file list.
   - **Code**: 18 non-test Go files at `/Users/270840341/work/mclaude/mclaude-session-agent/*.go`; sub-package at `internal/drivers/` imported by `session.go:18`, `agent.go:22`, `state.go:10`.

2. **Cross-module import paths after `mclaude-common` deletion not specified** — The doc says packages from `mclaude-common` (module name `mclaude.io/common`) move into `mclaude-control-plane` (module name `mclaude-control-plane`), and other components import them directly. But it does not specify: (a) the new import path (e.g., is it `mclaude-control-plane/slug` or something else?), (b) whether `go.work` loses `./mclaude-common` and gains nothing or a new entry, (c) whether consuming `go.mod` files (`mclaude-session-agent`, `mclaude-controller-k8s`, `mclaude-cli`) drop their `mclaude.io/common v0.0.0` require lines and add a new require for `mclaude-control-plane`, (d) what `replace` directives are needed for the workspace. Without this, a developer cannot update the import statements in `session-agent/agent.go:19-20`, `controller-k8s/main.go:35`, or `cli/cmd/cluster.go:8`.
   - **Doc**: "Other components import directly. `mclaude-common` is deleted once all consumers are migrated." (Package restructure section, migration map table)
   - **Code**: `go.work:4-11` — workspace lists `./mclaude-common`. Module names: `mclaude-common` = `mclaude.io/common` (`mclaude-common/go.mod:1`); `mclaude-control-plane` = `mclaude-control-plane` (`mclaude-control-plane/go.mod:1`). Session-agent imports `"mclaude.io/common/pkg/slug"` at `agent.go:19`; controller-k8s imports `"mclaude.io/common/pkg/hostauth"` at `main.go:35`; CLI imports `"mclaude.io/common/pkg/slug"` at `cmd/cluster.go:8`.

3. **`mclaude-cli` restructure scope unspecified** — The doc states `mclaude-cli` is "already structured, no changes needed" and its implementation plan row says only "Update box.Seal key generation." However, the CLI also imports `mclaude.io/common/pkg/slug`, `mclaude.io/common/pkg/subj`, and `mclaude.io/common/pkg/nats` (via `go.mod:23 replace mclaude.io/common => ../mclaude-common`). If `mclaude-common` is deleted, the CLI's `go.mod` must be updated and import paths changed — but the doc's CLI section only discusses the crypto migration and says no restructure is needed, leaving the `mclaude-common` migration for CLI implicit and unaddressed in the per-component breakdown.
   - **Doc**: "`mclaude-cli` — already structured, no changes needed." (Package restructure section)
   - **Code**: `mclaude-cli/go.mod:23` — `replace mclaude.io/common => ../mclaude-common`. `cmd/cluster.go:8`, `cmd/daemon.go:10`, `cmd/host.go:12-13`, `cmd/session.go:17-18` — all import from `mclaude.io/common`.

## Run: 2026-05-05T16:00:00Z

CLEAN — no blocking gaps found.

All gaps from prior runs are resolved in the current document version:
- Sealedbox key type decision: resolved (`*[32]byte` stays, `crypto/ecdh` used internally then bytes copied out).
- IAuthClient / mock-auth.ts loginSSO removal: explicitly listed in Component Changes.
- logout() interface fate: explicitly stated ("Keep `logout(): Promise<void>` — method stays, just no backend call").
- User-agent CI build specification: full job spec added (build-user-agent-binaries, matrix image job, manifest merge).
- mclaude-common migration map: complete table with all four packages (slug, subj, nats, hostauth) and all consumers.
- Session-agent file-to-package mapping: explicit file lists now provided per package.
- Cross-module import paths: exact import paths, go.mod mechanics, and go.work changes all specified.
- CLI mclaude-common migration: explicitly addressed ("Update all mclaude.io/common imports to point to mclaude-control-plane packages").
- Relay Dockerfile RUN command: "build target changes from . to ./cmd/relay/. The Dockerfile RUN command must be updated accordingly."
