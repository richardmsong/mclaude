## Run: 2026-05-01T00:00:00Z

**Component:** mclaude-controller-local
**Spec:** docs/mclaude-controller/spec-controller.md (Variant 2 sections)
**ADRs evaluated:** ADR-0035 (implemented). ADR-0054 and ADR-0058 are draft — skipped per status filter. Spec incorporates their decisions; evaluated via spec text.
**Production files:** controller.go, main.go, supervisor.go, host_auth.go
**Test files:** controller_test.go

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-controller:Variant2/Deployment:1 | "Runs as a long-lived foreground process on the BYOH machine." | main.go:1-148 | IMPLEMENTED | — | main() blocks on ctrl.Run(ctx) which blocks until ctx cancelled |
| spec-controller:Variant2/Deployment:2 | "Started by the user (mclaude daemon --host <hslug> or via a launchd / systemd unit they configure). One process per machine." | main.go (binary entry point) | IMPLEMENTED | — | Binary is mclaude-controller-local; launched per CLI invocation |
| spec-controller:Variant2/Deployment:3 | "The host slug is sourced from --host, falling back to the target of ~/.mclaude/active-host if unset." | main.go:42-51 | IMPLEMENTED | — | flag → env → active-host symlink resolution |
| spec-controller:Variant2/Deployment:4 | "Hard fail at startup on absence — there is no implicit default for BYOH controllers." | main.go:52-54 | IMPLEMENTED | — | log.Fatal with "FATAL: HOST_SLUG required" |
| spec-controller:Variant2/Config:--host | "--host / HOST_SLUG (required): The local host's slug." | main.go:42 | IMPLEMENTED | — | flag.String with env fallback |
| spec-controller:Variant2/Config:--user-slug | "--user-slug / USER_SLUG (required): The owning user's slug." | main.go:43 | IMPLEMENTED | — | flag.String with env fallback; informational only per code comments |
| spec-controller:Variant2/Config:--hub-url | "--hub-url / HUB_URL (required): Hub NATS WebSocket URL" | main.go:44 | IMPLEMENTED | — | flag.String with env fallback; fatal on empty |
| spec-controller:Variant2/Config:--creds-file | "--creds-file: Path to the host JWT credentials (default ~/.mclaude/hosts/{hslug}/nats.creds)" | main.go:46,72-76 | IMPLEMENTED | — | Defaults constructed from home dir + host slug |
| spec-controller:Variant2/Config:--data-dir | "--data-dir: Root for per-project worktrees (default ~/.mclaude/projects/)" | main.go:47,79-83 | IMPLEMENTED | — | Default ~/.mclaude/projects/ |
| spec-controller:Variant2/Config:LOG_LEVEL | "LOG_LEVEL: Default info." | main.go:28-33 | IMPLEMENTED | — | zerolog.ParseLevel from env, default InfoLevel |
| spec-controller:Variant2/Config:--cp-url | Not in spec table but present in code as --cp-url/CP_URL | main.go:45 | — | SPEC→FIX | Code has --cp-url flag for HTTP challenge-response. Spec Configuration table is missing this flag. Spec should add CP_URL to the config table. |
| spec-controller:Variant2/NATS:sub-subject | "Subscribes via the host JWT… mclaude.hosts.{HOST_SLUG}.>" | controller.go:111-113 | IMPLEMENTED | — | subscriptionSubject() returns "mclaude.hosts.{hslug}.>" |
| spec-controller:Variant2/NATS:create | "mclaude.hosts.{HOST_SLUG}.users.{uslug}.projects.{pslug}.create — Fan-out from CP. Materializes worktree, clones git URL if provided, registers credential helpers, starts session-agent subprocess, registers agent NKey, replies success." | controller.go:140-217 | PARTIAL | CODE→FIX | Worktree dir creation: ✓. Session-agent start: ✓. NKey registration: ✓. Reply: ✓. **Missing**: git clone (gitUrl from payload is parsed but never used for cloning). **Missing**: credential helper registration from ~/.mclaude/projects/{pslug}/.credentials/. **Missing**: --ready probe wait before reply. |
| spec-controller:Variant2/NATS:delete | "mclaude.hosts.{HOST_SLUG}.users.{uslug}.projects.{pslug}.delete — Stops session-agent subprocess (SIGINT, 10s grace, SIGKILL), removes project dir." | controller.go:265-295, supervisor.go:147-172 | PARTIAL | CODE→FIX | Stop: ✓ (SIGINT then SIGKILL). Remove dir: ✓. **Divergence**: Spec says "10s grace, SIGKILL" but code uses shutdownGracePeriod = 30s. |
| spec-controller:Variant2/NATS:agents-register | "mclaude.hosts.{HOST_SLUG}.api.agents.register — Request/reply. Forwards agent NKey public key registration requests to CP." | controller.go:118-121, 225-268 | IMPLEMENTED | — | agentRegisterSubject() + registerAgentKey() with retry logic |
| spec-controller:Variant2/Process:start-cmd | "Starts mclaude-session-agent --mode standalone --user-slug … --host … --project-slug … --data-dir ~/.mclaude/projects/{pslug}" | supervisor.go:103-125 | IMPLEMENTED | — | buildCmd with --mode standalone and all required flags |
| spec-controller:Variant2/Process:restart | "Restarts the child on crash with a 2-second delay." | supervisor.go:14,73-100 | IMPLEMENTED | — | childRestartDelay = 2s, restart loop in run() |
| spec-controller:Variant2/Process:shutdown | "On controller shutdown (SIGINT / SIGTERM), forwards SIGINT to all children and waits up to 30 seconds before exit." | main.go:127-129 (signal.NotifyContext), controller.go:305-316, supervisor.go:147-172 | IMPLEMENTED | — | NotifyContext(SIGINT, SIGTERM) + shutdownChildren → stop(SIGINT + 30s grace) |
| spec-controller:Variant2/CredIsolation | "The host controller never touches the agent's JWT or private key. The agent generates its own NKey pair…" | controller.go (no JWT handling for agents), supervisor.go:103-125 (passes --nkey-pub-file, --auth-url only) | IMPLEMENTED | — | Controller only reads the public key file; never has agent JWT or private key |
| spec-controller:Variant2/NKeyIPC:1 | "The agent generates its own NKey pair at startup (the private seed never leaves the agent process)." | — (agent-side, not controller code) | IMPLEMENTED | — | Controller side: expects file at nkeyPubFile path |
| spec-controller:Variant2/NKeyIPC:2 | "The agent passes its public key to the host controller via local IPC (stdout line or file at a well-known path)." | controller.go:200-206, 218-230 | IMPLEMENTED | — | waitForNKeyFile polls file at well-known path |
| spec-controller:Variant2/NKeyIPC:3 | "The host controller reads the public key and proceeds to Agent Credential Registration." | controller.go:206-217 | IMPLEMENTED | — | Reads key, calls registerAgentKey |
| spec-controller:Variant2/AgentReg:1 | "Host controller publishes a NATS request to mclaude.hosts.{HOST_SLUG}.api.agents.register with the agent's public key, user slug, host slug, and project slug." | controller.go:225-268 | PARTIAL | CODE→FIX | Sends user_slug, project_slug, nkey_public. **Missing**: host_slug not included in the agentRegisterRequest struct (spec says "user slug, host slug, and project slug"). |
| spec-controller:Variant2/AgentReg:2 | "On NOT_FOUND response, the controller retries with exponential backoff: 100ms initial delay, doubling, max 5s interval, max 10 attempts." | controller.go:24-30, 247-259 | IMPLEMENTED | — | Constants match: 100ms init, doubling, 5s max, 10 attempts |
| spec-controller:Variant2/HostCredRefresh:1 | "The host JWT has a 5-minute TTL. The host controller refreshes its own credential via the unified HTTP challenge-response protocol." | host_auth.go:15-17, 93-121 | IMPLEMENTED | — | hostJWTTTL=5min, Refresh() does challenge-response |
| spec-controller:Variant2/HostCredRefresh:2 | "Before TTL expiry (proactive refresh), the controller sends POST /api/auth/challenge followed by POST /api/auth/verify." | host_auth.go:128-152, 155-176, 179-218 | IMPLEMENTED | — | StartRefreshLoop fires at TTL-60s interval; requestChallenge + verifyChallenge |
| spec-controller:Variant2/HostCredRefresh:3 | "CP validates the host's NKey signature and returns a fresh host JWT." | host_auth.go:179-218 (verifyChallenge) | IMPLEMENTED | — | Parses JWT from verify response |
| spec-controller:Variant2/HostCredRefresh:4 | "The controller reconnects to NATS with the new JWT." | host_auth.go:59-64 (JWTFunc), main.go:100-104 (nats.UserJWT) | IMPLEMENTED | — | JWTFunc returns current JWT; NATS picks up on reconnect |
| spec-controller:Variant2/HostCredRefresh:5 | "On permissions violation error, the controller triggers an immediate refresh + retry." | main.go:108-117 | IMPLEMENTED | — | ErrorHandler checks isPermissionsViolation and calls Refresh |
| spec-controller:Variant2/Liveness | "When the local controller connects to hub NATS, hub publishes $SYS.ACCOUNT.{accountKey}.CONNECT; control-plane updates hosts.last_seen_at and mclaude-hosts KV online=true. On disconnect, online=false. The controller does not publish heartbeats." | — (CP-side, not controller code) | IMPLEMENTED | — | Controller has no heartbeat code; relies on NATS connection liveness |
| spec-controller:SharedBehavior/ProvReqShape | "Provisioning request shape: {userID, userSlug, hostSlug, projectID, projectSlug, gitUrl, gitIdentityId}" | controller.go:67-75 | IMPLEMENTED | — | ProvisionRequest struct matches all fields |
| spec-controller:SharedBehavior/ReplyOK | "Reply on success: {ok: true, projectSlug: …}" | controller.go:77-82 | IMPLEMENTED | — | ProvisionReply struct matches |
| spec-controller:SharedBehavior/ReplyFail | "Reply on failure: {ok: false, error: …, code: …}" | controller.go:77-82 | IMPLEMENTED | — | ProvisionReply struct matches |
| spec-controller:SharedBehavior/Auth | "mclaude-controller-local: mclaude.hosts.{hslug}.> (host-scoped JWT per ADR-0054). Zero JetStream access." | main.go:96-122 (creds/auth setup), controller.go:111-113 | IMPLEMENTED | — | Uses host JWT; no JS code in controller |
| spec-controller:ErrorHandling/HostSlugMissing | "HOST_SLUG / --host not provided (local): Fatal exit at startup with FATAL: HOST_SLUG required." | main.go:52-54 | IMPLEMENTED | — | Exact message matches |
| spec-controller:ErrorHandling/DeleteMissing | "Delete request without matching project: Idempotent: reply {ok: true} even if already gone." | controller.go:265-295 | IMPLEMENTED | — | Checks if child exists; removes dir with RemoveAll (idempotent) |
| spec-controller:ErrorHandling/ProvGitCloneFail | "Provision request: BYOH git clone fails → Reply {ok: false, code: 'git_clone_failed'}" | — | GAP | CODE→FIX | No git clone implementation exists in the controller; cannot fail with git_clone_failed |
| spec-controller:Dependencies | "Hub NATS reachable from the BYOH machine." | main.go:89-95 (nats.Connect) | IMPLEMENTED | — | Connects to hubURL |
| spec-controller:Dependencies | "The host JWT in ~/.mclaude/hosts/{hslug}/nats.creds" | main.go:72-76 | IMPLEMENTED | — | Default creds path matches |
| spec-controller:Dependencies | "The mclaude-session-agent binary on $PATH." | supervisor.go:113 | IMPLEMENTED | — | exec.Command("mclaude-session-agent", ...) |
| spec-controller:DaemonDeprecation | "mclaude daemon CLI command now launches mclaude-controller-local instead of mclaude-session-agent --daemon." | main.go:86-89 (deprecation notice log) | PARTIAL | SPEC→FIX | The controller logs a deprecation notice. The actual CLI command routing (mclaude daemon → controller-local) is in mclaude-cli, not this component. |
| spec-controller:Variant2/DataDir | "Project data at ~/.mclaude/projects/{pslug}/" | controller.go:170 | PARTIAL | SPEC→FIX | Code uses {dataDir}/{uslug}/{pslug}/ (user-scoped subdirectory). Spec says ~/.mclaude/projects/{pslug}/ without user prefix. Code's approach is correct for multi-user hosts — spec should be updated to include {uslug} prefix. |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| main.go:1-27 | INFRA | Package declaration, imports, logger setup |
| main.go:28-33 | INFRA | LOG_LEVEL env parsing (spec'd in config table) |
| main.go:35-48 | INFRA | Flag/env parsing for all config values (spec'd) |
| main.go:49-51 | INFRA | flag.Parse() |
| main.go:52-58 | INFRA | Host slug resolution chain (spec'd: --host → env → active-host) |
| main.go:59-67 | INFRA | Slug validation and owner user slug logging |
| main.go:68-70 | INFRA | Hub URL validation (spec'd) |
| main.go:72-83 | INFRA | Default creds-file and data-dir resolution (spec'd) |
| main.go:85-89 | UNSPEC'd | Deprecation notice log message for daemon mode. The spec mentions daemon deprecation phases but doesn't specify that the controller binary itself logs this notice. Harmless informational message. |
| main.go:91-122 | INFRA | NATS connection setup with HostAuth or static creds (spec'd: host JWT auth, permissions violation handling) |
| main.go:124-148 | INFRA | Signal handling, refresh loop, controller start, shutdown (all spec'd) |
| main.go:150-159 | INFRA | isPermissionsViolation helper (supports spec'd "on permissions violation, trigger immediate refresh") |
| controller.go:1-30 | INFRA | Package declaration, imports, constants for agent registration retries (all spec'd values) |
| controller.go:32-43 | INFRA | childKey type for supervisor map keying |
| controller.go:45-65 | INFRA | Controller struct definition with documented fields |
| controller.go:67-98 | INFRA | ProvisionRequest, ProvisionReply, agentRegisterRequest, agentRegisterReply structs (all spec'd payloads) |
| controller.go:100-121 | INFRA | NewController, subscriptionSubject, agentRegisterSubject (spec'd) |
| controller.go:123-139 | INFRA | Run method — subscribe + ctx.Done + shutdown (spec'd) |
| controller.go:140-165 | INFRA | handleMessage routing — subject parsing for create/delete/unknown (spec'd) |
| controller.go:166-228 | INFRA | handleCreate — worktree creation, child start, NKey IPC, agent registration (spec'd) |
| controller.go:230-243 | INFRA | waitForNKeyFile — poll for agent NKey public key file (spec'd NKey IPC) |
| controller.go:245-293 | INFRA | registerAgentKey — NATS request/reply with retry (spec'd agent credential registration) |
| controller.go:295-321 | INFRA | handleDelete — stop child, remove dir (spec'd) |
| controller.go:323-340 | INFRA | reply helper, shutdownChildren (spec'd behavior) |
| controller.go:342-349 | INFRA | minDuration utility |
| supervisor.go:1-16 | INFRA | Package, imports, constants (childRestartDelay=2s, shutdownGracePeriod=30s) |
| supervisor.go:18-45 | INFRA | supervisedChild struct definition |
| supervisor.go:47-66 | INFRA | startChild — creates and launches supervised child goroutine |
| supervisor.go:68-100 | INFRA | run — restart loop with crash detection (spec'd) |
| supervisor.go:102-138 | INFRA | buildCmd — constructs exec.Cmd with spec'd flags and env vars |
| supervisor.go:140-172 | INFRA | stop — SIGINT + grace period + SIGKILL (spec'd) |
| host_auth.go:1-17 | INFRA | Package, imports, constants (hostJWTTTL=5min, refreshBuffer=60s) |
| host_auth.go:19-35 | INFRA | HostAuth struct (spec'd host credential refresh) |
| host_auth.go:37-55 | INFRA | NewHostAuthFromCredsFile (spec'd creds file parsing) |
| host_auth.go:57-72 | INFRA | JWTFunc, SignFunc (NATS auth callbacks for spec'd JWT refresh) |
| host_auth.go:74-76 | INFRA | PublicKey helper |
| host_auth.go:78-121 | INFRA | Refresh — HTTP challenge-response (spec'd) |
| host_auth.go:123-153 | INFRA | StartRefreshLoop — proactive timer refresh (spec'd) |
| host_auth.go:155-218 | INFRA | requestChallenge, verifyChallenge HTTP helpers (spec'd auth flow) |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| Variant2/Deployment:3 | Host slug sourced from --host, falling back to active-host | None | None | UNTESTED | main.go slug resolution logic not unit tested |
| Variant2/Deployment:4 | Hard fail on missing HOST_SLUG | None | None | UNTESTED | Fatal path in main() not tested |
| Variant2/NATS:sub-subject | Subscribes to mclaude.hosts.{hslug}.> | TestControllerSubscriptionSubject | None | UNIT_ONLY | Subject string verified |
| Variant2/NATS:agents-register | mclaude.hosts.{hslug}.api.agents.register subject | TestControllerAgentRegisterSubject | None | UNIT_ONLY | Subject string verified |
| Variant2/NATS:create | Handles project create fan-out | TestHandleMessage_SubjectTokenParsing | None | UNIT_ONLY | Subject parsing tested; no full handleCreate test with real NATS |
| Variant2/NATS:delete | Handles project delete | TestHandleMessage_SubjectTokenParsing | None | UNIT_ONLY | Subject parsing tested; no full handleDelete test |
| Variant2/NKeyIPC | Agent writes NKey pub key to file; controller reads it | TestWaitForNKeyFile_* (3 tests) | None | UNIT_ONLY | File polling tested: exists, late, whitespace |
| Variant2/AgentReg | Register agent NKey with CP via NATS | TestAgentRegisterRequest_Marshal | None | UNIT_ONLY | JSON marshalling only; no NATS request/reply tested |
| Variant2/HostCredRefresh | HTTP challenge-response JWT refresh | TestHostAuth_RefreshFlow, TestHostAuth_RefreshUpdatesStoredJWT | None | UNIT_ONLY | Uses httptest server; challenge-response flow verified. No real CP integration. |
| Variant2/HostCredRefresh:permissions | Permissions violation triggers refresh | None | None | UNTESTED | ErrorHandler callback in main.go not tested |
| Variant2/Process:start-cmd | session-agent started with correct flags | TestSupervisorBuildCmd_* (4 tests) | None | UNIT_ONLY | Cmd args and env vars verified |
| Variant2/Process:restart | Restart on crash with 2s delay | None | None | UNTESTED | Restart loop in supervisor.run() not tested |
| Variant2/Process:shutdown | SIGINT + 30s grace + SIGKILL | None | None | UNTESTED | stop() method not tested |
| SharedBehavior/ProvReqShape | Request/reply payloads | TestProvisionReply_* (2 tests) | None | UNIT_ONLY | JSON marshalling verified |
| SharedBehavior/Auth | Host JWT auth, zero JetStream | TestHostAuth_* (7 tests) | None | UNIT_ONLY | NKey operations, JWT func, sign func, creds parsing all tested |
| ErrorHandling/DeleteMissing | Idempotent delete reply | None | None | UNTESTED | handleDelete idempotency not tested |
| CredIsolation | Controller never touches agent JWT/private key | TestSupervisorBuildCmd_NoNKeyFileWhenEmpty | None | UNIT_ONLY | Verifies no --nkey-pub-file when empty; indirect coverage |

### Phase 4 — Bug Triage

No bugs in .agent/bugs/ match component mclaude-controller-local.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | — | — | No bugs filed for this component |

### Summary

- Implemented: 28
- Gap: 1
- Partial: 4
- Infra: 32
- Unspec'd: 1
- Dead: 0
- Tested (unit+integration): 0
- Unit only: 10
- E2E only: 0
- Untested: 7
- Bugs fixed: 0
- Bugs open: 0

### Non-clean findings

```
GAP [CODE→FIX]: "Provision request: BYOH git clone fails → Reply {ok: false, code: 'git_clone_failed'}" → No git clone implementation exists. The controller creates the worktree directory but never clones the git URL from the provisioning payload. (controller.go:166-228)

PARTIAL [CODE→FIX]: "mclaude.hosts.{HOST_SLUG}.users.{uslug}.projects.{pslug}.create — Materializes worktree, clones git URL if provided, registers credential helpers, starts session-agent, registers agent NKey, replies success once --ready probe passes." → Worktree dir creation ✓, agent start ✓, NKey registration ✓, reply ✓. Missing: git clone, credential helper registration, --ready probe wait. (controller.go:166-228)

PARTIAL [CODE→FIX]: "Stops session-agent subprocess (SIGINT, 10s grace, SIGKILL)" → Code uses shutdownGracePeriod = 30 seconds, not 10s as spec states. (supervisor.go:15, supervisor.go:147-172)

PARTIAL [CODE→FIX]: "Host controller publishes NATS request with agent's public key, user slug, host slug, and project slug." → agentRegisterRequest struct is missing host_slug field. Sends user_slug, project_slug, nkey_public but not host_slug. (controller.go:85-90)

PARTIAL [SPEC→FIX]: "Project data at ~/.mclaude/projects/{pslug}/" → Code uses {dataDir}/{uslug}/{pslug}/ (user-scoped subdirectory). Code's approach is correct for multi-user hosts; spec should include {uslug} in the path. (controller.go:170)

PARTIAL [SPEC→FIX]: "Daemon deprecation Phase 1" → Controller logs deprecation notice. Actual CLI command routing is in mclaude-cli, not this component. (main.go:85-89)

UNSPEC'd: main.go:85-89 → Deprecation notice log message for daemon mode at startup. Harmless informational message not described in spec.
```
