## Run: 2026-05-01T00:00:00Z

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-controller:101 | Runs as a long-lived foreground process on the BYOH machine. Started by the user (`mclaude daemon --host <hslug>` or via a launchd / systemd unit they configure). One process per machine. | main.go:1-148 | IMPLEMENTED | — | main() runs foreground, blocks on ctx.Done() via ctrl.Run(ctx) |
| spec-controller:103 | The host slug is sourced from `--host`, falling back to the target of `~/.mclaude/active-host` if unset. Hard fail at startup on absence. | main.go:40-52 | IMPLEMENTED | — | flag.String("host", os.Getenv("HOST_SLUG"), ...), then checks active-host symlink, then log.Fatal |
| spec-controller:108 | `--host` / `HOST_SLUG` Yes — The local host's slug. | main.go:40,46-52 | IMPLEMENTED | — | --host flag with HOST_SLUG env fallback, fatal on empty |
| spec-controller:109 | `--user-slug` / `USER_SLUG` Yes — The owning user's slug. | main.go:41 | IMPLEMENTED | — | --user-slug flag with USER_SLUG env fallback. Note: not fatal on absence (informational per ADR-0054) |
| spec-controller:110 | `--hub-url` / `HUB_URL` Yes — Hub NATS WebSocket URL. | main.go:42,67-69 | IMPLEMENTED | — | --hub-url flag with HUB_URL env fallback, fatal on empty |
| spec-controller:111 | `--cp-url` / `CP_URL` Yes — Control-plane HTTP base URL. Required for host JWT refresh via HTTP challenge-response. | main.go:43,71-72 | IMPLEMENTED | — | --cp-url flag with CP_URL env fallback; if empty, warns but continues (JWT refresh disabled) |
| spec-controller:112 | `--creds-file` No — Path to the host JWT credentials (default `~/.mclaude/hosts/{hslug}/nats.creds`). | main.go:44,77-80 | IMPLEMENTED | — | --creds-file with NATS_CREDS_FILE env, default to ~/.mclaude/hosts/{hslug}/nats.creds |
| spec-controller:113 | `--data-dir` No — Root for per-project worktrees (default `~/.mclaude/projects/`). Per-project data is stored at `{data-dir}/{uslug}/{pslug}/` for multi-user host support. | main.go:45,83-87; controller.go:175 | IMPLEMENTED | — | default ~/.mclaude/projects/, project dir = filepath.Join(dataDir, uslug, pslug) |
| spec-controller:114 | `LOG_LEVEL` No — Default `info`. | main.go:28-32 | IMPLEMENTED | — | zerolog.ParseLevel(os.Getenv("LOG_LEVEL")), defaults to InfoLevel |
| spec-controller:120 | Subscribes via the host JWT scoped to `mclaude.hosts.{hslug}.>` per ADR-0054. | controller.go:114-117; main.go:126,131 | IMPLEMENTED | — | subscriptionSubject() returns "mclaude.hosts."+hostSlug+".>" |
| spec-controller:124 | `mclaude.hosts.{HOST_SLUG}.users.{uslug}.projects.{pslug}.create` — Fan-out from CP. Materializes worktree, clones git URL if provided, starts session-agent subprocess, registers agent NKey with CP, and replies success. | controller.go:140-244 | IMPLEMENTED | — | handleCreate: creates worktree dir, git clone if URL provided, starts child, waits for NKey, registers with CP, replies |
| spec-controller:124 | create: registers credential helpers from `~/.mclaude/projects/{pslug}/.credentials/` | controller.go:140-244 | GAP | SPEC→FIX | Spec mentions registering credential helpers from .credentials/ directory but code doesn't do this. No credential helper registration logic exists. The spec may be aspirational — git credential helpers are managed by session-agent, not the controller. |
| spec-controller:124 | create: replies success once the session-agent's `--ready` probe passes. | controller.go:243 | GAP | SPEC→FIX | Code replies success immediately after NKey registration attempt, without waiting for a `--ready` probe from the session-agent. No ready probe mechanism exists. |
| spec-controller:125 | `mclaude.hosts.{HOST_SLUG}.users.{uslug}.projects.{pslug}.delete` — Stops session-agent (SIGINT, 10s grace, SIGKILL), removes project dir. | controller.go:285-311; supervisor.go:136-162 | IMPLEMENTED | — | handleDelete stops child (SIGINT + 10s grace + SIGKILL), RemoveAll on project dir, idempotent reply |
| spec-controller:126 | `mclaude.hosts.{HOST_SLUG}.api.agents.register` — Request/reply. Forwards agent NKey public key registration requests to CP. | controller.go:120-123,247-293 | IMPLEMENTED | — | agentRegisterSubject() is correct; registerAgentKey publishes to this subject via NATS request/reply |
| spec-controller:128 | The host controller subscribes to `mclaude.hosts.{HOST_SLUG}.>` — a single wildcard that captures all project lifecycle messages. | controller.go:114-117,126 | IMPLEMENTED | — | subscriptionSubject() = "mclaude.hosts.{hslug}.>" |
| spec-controller:136 | Starts `mclaude-session-agent --mode standalone --user-slug … --host … --project-slug … --data-dir ~/.mclaude/projects/{pslug}` as a child process. | supervisor.go:95-123 | IMPLEMENTED | — | buildCmd() constructs exec.Command with all required flags |
| spec-controller:137 | Restarts the child on crash with a 2-second delay. | supervisor.go:66-98; supervisor.go:15 | IMPLEMENTED | — | childRestartDelay = 2*time.Second; run() loops with restart after crash |
| spec-controller:138 | On controller shutdown (SIGINT / SIGTERM), forwards SIGINT to all children and waits up to 30 seconds before exit. | controller.go:295-310; supervisor.go:136-162; main.go:122 | GAP | CODE→FIX | Spec says 30s shutdown grace period for all children. Code has shutdownGracePeriod = 10s in supervisor.go:16 (per-child SIGINT→SIGKILL). The controller-level shutdownChildren() calls child.stop() serially but each child gets only 10s, not 30s. The spec says "waits up to 30 seconds before exit" for the entire shutdown. |
| spec-controller:140 | Credential isolation (ADR-0058): The host controller never touches the agent's JWT or private key. | controller.go, supervisor.go, host_auth.go | IMPLEMENTED | — | Controller only handles NKey public key (via file IPC), never JWT or private seed |
| spec-controller:144-146 | NKey IPC: Agent generates NKey pair, passes public key via file at well-known path. Controller reads the public key. | controller.go:211-226; supervisor.go:110-112 | IMPLEMENTED | — | nkeyPubFile path at {projectDir}/.nkey-pub; waitForNKeyFile polls for it |
| spec-controller:148 | The host controller never receives, stores, or handles the agent's private key material. | controller.go, supervisor.go | IMPLEMENTED | — | Only public key string is read and forwarded |
| spec-controller:152 | Agent Credential Registration: Host controller publishes NATS request to `mclaude.hosts.{HOST_SLUG}.api.agents.register` with agent's public key, user slug, host slug, and project slug. | controller.go:247-293 | IMPLEMENTED | — | registerAgentKey marshals agentRegisterRequest with all 4 fields, publishes to agentRegisterSubject() |
| spec-controller:154 | On `NOT_FOUND` response, retries with exponential backoff: 100ms initial delay, doubling, max 5s interval, max 10 attempts. | controller.go:17-22,262-291 | IMPLEMENTED | — | agentRegisterRetries=10, agentRegisterInitDelay=100ms, agentRegisterMaxDelay=5s, delay*2 doubling |
| spec-controller:160 | Host JWT has a 5-minute TTL. The host controller refreshes via HTTP challenge-response protocol. | host_auth.go:13-15,95-133 | IMPLEMENTED | — | hostJWTTTL=5*time.Minute, Refresh() does POST /api/auth/challenge then POST /api/auth/verify |
| spec-controller:162 | Before TTL expiry (proactive refresh), the controller sends challenge + verify. | host_auth.go:139-163 | IMPLEMENTED | — | StartRefreshLoop ticker at hostJWTTTL-hostJWTRefreshBuffer (5min - 60s = 4min) |
| spec-controller:164 | CP validates signature and returns fresh host JWT. Controller reconnects to NATS with new JWT. | host_auth.go:95-133; main.go:102-103 | IMPLEMENTED | — | Refresh updates currentJWT; JWTFunc returns it on next NATS auth challenge |
| spec-controller:165 | On `permissions violation` error, the controller triggers an immediate refresh + retry. | main.go:107-114 | IMPLEMENTED | — | nats.ErrorHandler checks isPermissionsViolation, calls hostAuth.Refresh() |
| spec-controller:167 | The host controller is not involved in agent credential refresh. | controller.go, supervisor.go | IMPLEMENTED | — | No agent JWT refresh code in controller; only public key registration |
| spec-controller:171 | When the local controller connects to hub NATS, hub publishes $SYS event; CP updates hosts.last_seen_at and KV online=true. On disconnect, online=false. The controller does not publish heartbeats. | main.go (entire file) | IMPLEMENTED | — | Controller connects to NATS; no heartbeat publishing code. Liveness is server-side via $SYS events handled by CP. |
| spec-controller:196-207 | Provisioning request shape: `{userID, userSlug, hostSlug, projectID, projectSlug, gitUrl, gitIdentityId}`. Reply on success: `{ok: true, projectSlug}`. Reply on failure: `{ok: false, error, code}`. | controller.go:66-95 | IMPLEMENTED | — | ProvisionRequest and ProvisionReply structs match spec exactly |
| spec-controller:219 | Authentication: mclaude-controller-local uses `mclaude.hosts.{hslug}.>` (host-scoped JWT per ADR-0054). Zero JetStream access. | main.go:98-118; controller.go:114-117 | IMPLEMENTED | — | Host JWT used for NATS connection; subscription only to mclaude.hosts.{hslug}.>; no JS operations |
| spec-controller:225 | Error: Provision request: BYOH git clone fails → Reply `{ok: false, code: "git_clone_failed"}` | controller.go:196-207 | IMPLEMENTED | — | handleCreate returns git_clone_failed code on clone error |
| spec-controller:227 | Error: Delete request without matching project → Idempotent: reply `{ok: true}` even if already gone. | controller.go:285-311 | IMPLEMENTED | — | handleDelete: if child not in map, still proceeds and replies OK after RemoveAll |
| spec-controller:229 | Error: `HOST_SLUG` / `--host` not provided (local) → Fatal exit at startup with `FATAL: HOST_SLUG required`. | main.go:49-51 | IMPLEMENTED | — | log.Fatal with exact message "FATAL: HOST_SLUG required..." |
| spec-controller:237 | Dependencies: Hub NATS reachable from BYOH machine. | main.go:120-124 | IMPLEMENTED | — | nats.Connect with fatal on failure |
| spec-controller:238 | Dependencies: The host JWT in `~/.mclaude/hosts/{hslug}/nats.creds`. | main.go:77-80,95-103 | IMPLEMENTED | — | Default creds path; read and parsed for NKey+JWT |
| spec-controller:239 | Dependencies: `git`, `gh`, `glab` binaries (same as session-agent dependencies). | controller.go:191 | IMPLEMENTED | — | exec.Command("git", "clone", ...) for git; gh/glab are session-agent deps, not controller deps |
| spec-controller:240 | Dependencies: The `mclaude-session-agent` binary on `$PATH`. | supervisor.go:107 | IMPLEMENTED | — | exec.Command("mclaude-session-agent", args...) |
| spec-controller:244-245 | Daemon Mode Deprecation: `mclaude daemon` CLI command now launches `mclaude-controller-local` instead of `mclaude-session-agent --daemon`. | main.go:89-92 | IMPLEMENTED | — | Deprecation notice log message at startup |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| main.go:1-6 | INFRA | Package declaration and imports |
| main.go:7-18 | INFRA | Import block |
| main.go:20-32 | INFRA | Log level setup (spec says LOG_LEVEL, default info — covered in Phase 1) |
| main.go:34-45 | INFRA | Flag definitions — all covered by spec Configuration table |
| main.go:46-57 | INFRA | Host slug resolution logic — covered by spec |
| main.go:58-65 | INFRA | User slug validation and logging (informational) — spec says USER_SLUG is required but code treats it as informational; covered in Phase 1 |
| main.go:67-72 | INFRA | Hub URL and CP URL parsing — covered by spec |
| main.go:74-87 | INFRA | Creds file and data dir default path resolution — covered by spec |
| main.go:89-92 | INFRA | Deprecation notice log message — covered by spec Daemon Mode Deprecation |
| main.go:94-118 | INFRA | NATS connection options: HostAuth setup with JWT refresh or static creds file — covered by spec Host Credential Refresh |
| main.go:120-124 | INFRA | NATS connection establishment with fatal on error — covered by spec |
| main.go:126-128 | INFRA | Signal context for SIGINT/SIGTERM — supports spec shutdown behavior |
| main.go:131-132 | INFRA | Start host JWT refresh loop — covered by spec Host Credential Refresh |
| main.go:134-146 | INFRA | Controller creation and Run() — main flow, covered by spec |
| main.go:149-157 | INFRA | isPermissionsViolation helper — supports spec permissions violation handling |
| controller.go:1-15 | INFRA | Package, imports |
| controller.go:17-29 | INFRA | Constants for agent registration backoff — covered by spec |
| controller.go:31-44 | INFRA | childKey struct and String() — internal key type for child map |
| controller.go:46-64 | INFRA | Controller struct — aligns with spec role description |
| controller.go:66-80 | INFRA | ProvisionRequest struct — matches spec request shape |
| controller.go:82-88 | INFRA | ProvisionReply struct — matches spec reply shape |
| controller.go:90-102 | INFRA | agentRegisterRequest/Reply structs — matches spec agent credential registration |
| controller.go:104-117 | INFRA | NewController constructor and subscriptionSubject() — factory, subject per spec |
| controller.go:119-123 | INFRA | agentRegisterSubject() — subject per spec |
| controller.go:126-136 | INFRA | Run() method — subscribe + block on ctx — spec Deployment section |
| controller.go:138-168 | INFRA | handleMessage routing — routes create/delete per spec NATS subscriptions |
| controller.go:170-244 | INFRA | handleCreate — spec create behavior covered in Phase 1 |
| controller.go:246-293 | INFRA | registerAgentKey — spec Agent Credential Registration covered in Phase 1 |
| controller.go:295-311 | INFRA | handleDelete — spec delete behavior covered in Phase 1 |
| controller.go:313-327 | INFRA | reply() helper — NATS reply with no-op when no reply subject (fan-out) |
| controller.go:329-340 | INFRA | shutdownChildren — spec shutdown behavior |
| controller.go:342-348 | INFRA | minDuration helper — utility for backoff |
| supervisor.go:1-11 | INFRA | Package, imports |
| supervisor.go:13-16 | INFRA | Constants childRestartDelay=2s, shutdownGracePeriod=10s — covered in Phase 1 (note: 10s vs 30s spec gap) |
| supervisor.go:18-33 | INFRA | supervisedChild struct — internal process tracking |
| supervisor.go:35-55 | INFRA | startChild() — creates supervised child, launches goroutine |
| supervisor.go:57-98 | INFRA | run() — restart loop with crash detection and delay |
| supervisor.go:100-123 | INFRA | buildCmd() — constructs session-agent command with all flags/env |
| supervisor.go:126-162 | INFRA | stop() — SIGINT + grace period + SIGKILL |
| host_auth.go:1-14 | INFRA | Package, imports, constants |
| host_auth.go:16-26 | INFRA | HostAuth struct |
| host_auth.go:28-49 | INFRA | NewHostAuthFromCredsFile — loads NKey + JWT from creds file |
| host_auth.go:51-68 | INFRA | JWTFunc() and SignFunc() — NATS auth callbacks |
| host_auth.go:70-74 | INFRA | PublicKey() accessor |
| host_auth.go:76-107 | INFRA | Refresh() — HTTP challenge-response flow per spec |
| host_auth.go:109-133 | INFRA | StartRefreshLoop — proactive refresh per spec |
| host_auth.go:135-179 | INFRA | requestChallenge, verifyChallenge HTTP helpers — per spec

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| spec-controller:101 | Runs as long-lived foreground process | — | — | UNTESTED | main() flow not directly tested (acceptable — integration concern) |
| spec-controller:103 | Host slug from --host, fallback to active-host, hard fail | — | — | UNTESTED | Flag resolution logic in main() not tested |
| spec-controller:108-114 | Configuration flags/env vars | — | — | UNTESTED | Flag parsing in main() not tested |
| spec-controller:120 | Subscribes to mclaude.hosts.{hslug}.> | TestControllerSubscriptionSubject | — | UNIT_ONLY | Subject string verified |
| spec-controller:124 | create: materializes worktree, clones git, starts agent, registers NKey | TestHandleCreate_GitCloneFailed, TestHandleCreate_GitCloneSkippedWhenRepoExists | — | UNIT_ONLY | Git clone failure and idempotency tested; full handleCreate flow not tested (requires NATS) |
| spec-controller:125 | delete: SIGINT, 10s grace, SIGKILL, removes dir | TestShutdownGracePeriod_Is10s | — | UNIT_ONLY | Grace period constant verified; full delete flow not tested |
| spec-controller:126 | api.agents.register subject | TestControllerAgentRegisterSubject | — | UNIT_ONLY | Subject string verified |
| spec-controller:136 | Starts mclaude-session-agent --mode standalone with flags | TestSupervisorBuildCmd_BaseFlags, TestSupervisorBuildCmd_NKeyPubFile, TestSupervisorBuildCmd_AuthURL, TestSupervisorBuildCmd_NoNKeyFileWhenEmpty, TestSupervisorBuildCmd_EnvVars | — | UNIT_ONLY | Command construction fully tested |
| spec-controller:137 | Restarts on crash with 2s delay | — | — | UNTESTED | Restart loop not tested |
| spec-controller:144-146 | NKey IPC: reads public key from file | TestWaitForNKeyFile_FileExists, TestWaitForNKeyFile_FileAppearsLate, TestWaitForNKeyFile_WhitespaceStripping | — | UNIT_ONLY | File polling well-tested |
| spec-controller:152-154 | Agent credential registration with retry | TestAgentRegisterRequest_Marshal | — | UNIT_ONLY | Marshalling tested; retry logic not tested |
| spec-controller:160-165 | Host JWT refresh via HTTP challenge-response | TestHostAuth_RefreshFlow, TestHostAuth_RefreshUpdatesStoredJWT, TestHostAuth_RefreshErrorWhenNoCPURL | — | UNIT_ONLY | Full refresh flow tested with httptest server |
| spec-controller:165 | Permissions violation triggers immediate refresh | — | — | UNTESTED | Error handler logic in main() not tested |
| spec-controller:196-207 | Provisioning request/reply shape | TestProvisionReply_OKMarshal, TestProvisionReply_ErrorMarshal | — | UNIT_ONLY | Reply struct marshalling tested |
| spec-controller:229 | HOST_SLUG not provided → fatal exit | — | — | UNTESTED | main() fatal path not tested |
| spec-controller:219 | Authentication: host-scoped JWT, zero JetStream | TestHostAuth_JWTFuncReturnsCurrent, TestHostAuth_PublicKeyFormat, TestHostAuth_SignFuncProducesSignature, TestHostAuth_VerifySignatureCorrectness, TestNewHostAuthFromCredsFile_Valid, TestNewHostAuthFromCredsFile_InvalidSeed | — | UNIT_ONLY | Auth primitives thoroughly tested |

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| 002 | PTT race condition | N/A | Component: spa — not mclaude-controller-local |
| 003 | Initial message injection race | N/A | Component: spa — not mclaude-controller-local |
| 005 | Getting Started canned text | N/A | Component: spa — not mclaude-controller-local |

No open bugs for mclaude-controller-local.

### Summary

- Implemented: 30
- Gap: 3 (2 SPEC→FIX, 1 CODE→FIX)
- Partial: 0
- Infra: 38
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 11
- E2E only: 0
- Untested: 6
- Bugs fixed: 0
- Bugs open: 0

**Non-clean findings:**

```
GAP [SPEC→FIX]: "create: registers credential helpers from ~/.mclaude/projects/{pslug}/.credentials/" → Code does not implement credential helper registration; this is managed by session-agent, not the controller. Spec should remove this from the controller's create behavior. (controller.go:140-244)

GAP [SPEC→FIX]: "create: replies success once the session-agent's --ready probe passes" → Code replies success immediately after NKey registration attempt without waiting for a ready probe. No ready probe mechanism exists. Spec should describe the actual behavior (reply after NKey registration or immediately after child start). (controller.go:243)

GAP [CODE→FIX]: "On controller shutdown (SIGINT / SIGTERM), forwards SIGINT to all children and waits up to 30 seconds before exit" → Code uses shutdownGracePeriod = 10s per child (supervisor.go:16), not 30s as specified. Either the code should be updated to 30s or the spec should document 10s. (supervisor.go:16; controller.go:295-310)
```
