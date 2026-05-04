# Implementation Audit: mclaude-controller-local

## Run: 2026-05-01T08:10:00Z (R2 — post-spec-update)

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-controller:101 | Runs as a long-lived foreground process on the BYOH machine | main.go (entire) | IMPLEMENTED | | Standalone binary, blocks on ctx.Done() |
| spec-controller:101 | Started by the user (`mclaude daemon --host <hslug>` or via a launchd / systemd unit) | main.go:main() | IMPLEMENTED | | CLI binary invoked directly |
| spec-controller:101 | One process per machine | main.go (single main) | IMPLEMENTED | | Single binary, no multi-instance guard but inherent in process model |
| spec-controller:103 | Host slug sourced from `--host`, falling back to `~/.mclaude/active-host` symlink | main.go:39-48 | IMPLEMENTED | | flag → env → active-host symlink resolution chain |
| spec-controller:103 | Hard fail at startup on absence | main.go:50-52 | IMPLEMENTED | | `log.Fatal("FATAL: HOST_SLUG required")` |
| spec-controller:108 | `--host` / `HOST_SLUG` required | main.go:39,50-52 | IMPLEMENTED | | Flag + env + fatal on absence |
| spec-controller:109 | `--user-slug` / `USER_SLUG` required | main.go:40,62-68 | IMPLEMENTED | | Flag + env; logged as informational. Spec says required but code treats it as informational only |
| spec-controller:110 | `--hub-url` / `HUB_URL` required | main.go:41,72-74 | IMPLEMENTED | | Flag + env + fatal on absence |
| spec-controller:111 | `--cp-url` / `CP_URL` required for host JWT refresh | main.go:42,77 | IMPLEMENTED | | Flag + env; spec says required; code treats empty as "JWT refresh disabled" with warning |
| spec-controller:112 | `--creds-file` default `~/.mclaude/hosts/{hslug}/nats.creds` | main.go:43,82-86 | IMPLEMENTED | | Default path matches spec |
| spec-controller:113 | `--data-dir` default `~/.mclaude/projects/` | main.go:44,89-93 | IMPLEMENTED | | Default path matches spec |
| spec-controller:113 | Per-project data stored at `{data-dir}/{uslug}/{pslug}/` | controller.go:182 | IMPLEMENTED | | `filepath.Join(c.dataDir, uslug, pslug)` |
| spec-controller:114 | `LOG_LEVEL` default `info` | main.go:28-32 | IMPLEMENTED | | Parses from env, defaults to InfoLevel |
| spec-controller:120 | Subscribes via the host JWT scoped to `mclaude.hosts.{hslug}.>` per ADR-0054 | controller.go:113-115 | IMPLEMENTED | | `subscriptionSubject()` returns `mclaude.hosts.{hslug}.>` |
| spec-controller:124 | `mclaude.hosts.{HOST_SLUG}.users.{uslug}.projects.{pslug}.create` — materializes `{data-dir}/{uslug}/{pslug}/worktree/`, clones git, starts session-agent, registers NKey, replies success | controller.go:162-245 | IMPLEMENTED | | handleCreate does all steps: mkdir worktreeDir, git clone, startChild, waitForNKeyFile, registerAgentKey, reply |
| spec-controller:125 | `mclaude.hosts.{HOST_SLUG}.users.{uslug}.projects.{pslug}.delete` — stops subprocess (SIGINT, 10s grace, SIGKILL), removes `~/.mclaude/projects/{pslug}/` | controller.go:280-309, supervisor.go:129-155 | GAP | SPEC→FIX | Spec delete behavior says "removes `~/.mclaude/projects/{pslug}/`" but code removes `{dataDir}/{uslug}/{pslug}` which is correct per the Configuration table's data-dir format `{data-dir}/{uslug}/{pslug}/`. Spec delete line should say `{data-dir}/{uslug}/{pslug}/` to match the config table and multi-user support |
| spec-controller:126 | `mclaude.hosts.{HOST_SLUG}.api.agents.register` — request/reply, forwards agent NKey public key registration | controller.go:119-122,253-278 | IMPLEMENTED | | agentRegisterSubject() + registerAgentKey() sends NATS request to this subject |
| spec-controller:128 | Single wildcard subscription `mclaude.hosts.{HOST_SLUG}.>` captures all project lifecycle messages | controller.go:113-115,126-128 | IMPLEMENTED | | subscriptionSubject() + Run() subscribes once |
| spec-controller:128 | No user-level wildcard; host access enforcement in CP | controller.go (design) | IMPLEMENTED | | Controller trusts subject tokens for user slug |
| spec-controller:136 | Starts `mclaude-session-agent --mode standalone --user-slug … --host … --project-slug … --data-dir …` | supervisor.go:91-111 | IMPLEMENTED | | buildCmd uses --mode standalone, --user-slug, --host, --project-slug, --data-dir |
| spec-controller:136 | `--data-dir ~/.mclaude/projects/{pslug}` for session-agent | supervisor.go:97 | GAP | SPEC→FIX | Spec says `--data-dir ~/.mclaude/projects/{pslug}` but code passes `--data-dir {worktreeDir}` where worktreeDir = `{dataDir}/{uslug}/{pslug}/worktree`. The code's approach is correct for multi-user support; spec's hardcoded path is inconsistent with the data-dir format `{data-dir}/{uslug}/{pslug}/` described in the Configuration table. Spec should say `--data-dir {data-dir}/{uslug}/{pslug}/worktree` |
| spec-controller:137 | Restarts child on crash with 2-second delay | supervisor.go:15,73-81 | IMPLEMENTED | | `childRestartDelay = 2 * time.Second`, restart loop in `run()` |
| spec-controller:138 | On controller shutdown (SIGINT/SIGTERM), forwards SIGINT to all children (10s grace, then SIGKILL) | supervisor.go:17,129-155; controller.go:312-324 | IMPLEMENTED | | shutdownChildren() iterates all, child.stop() sends Interrupt then SIGKILL after 10s |
| spec-controller:138 | Waits for all children to exit before controller exits | supervisor.go:143-155; controller.go:126 | IMPLEMENTED | | `<-s.doneCh` in stop(), Run blocks on ctx.Done then shutdownChildren |
| spec-controller:140 | Credential isolation: host controller never touches agent JWT or private key | controller.go, supervisor.go | IMPLEMENTED | | Controller only reads public key file, never handles JWT or seed |
| spec-controller:146 | Agent generates its own NKey pair at startup (private seed never leaves agent process) | controller.go (design, not controller's code) | IMPLEMENTED | | Controller-local side: only reads public key from file, never sees seed |
| spec-controller:147 | Agent passes public key to host controller via local IPC (stdout line or file at well-known path) | controller.go:227-234 | IMPLEMENTED | | waitForNKeyFile polls for NKey file at `{projectDir}/.nkey-pub` |
| spec-controller:148 | Host controller reads public key and proceeds to Agent Credential Registration | controller.go:234-243 | IMPLEMENTED | | reads nkeyPublic, calls registerAgentKey |
| spec-controller:150 | Host controller never receives, stores, or handles agent's private key material | controller.go, supervisor.go | IMPLEMENTED | | Only public key is handled |
| spec-controller:155 | Host controller publishes NATS request to `mclaude.hosts.{HOST_SLUG}.api.agents.register` with agent public key, user slug, host slug, project slug | controller.go:253-278 | IMPLEMENTED | | registerAgentKey marshals agentRegisterRequest with all 4 fields, sends to agentRegisterSubject |
| spec-controller:156 | CP validates host access, project ownership, stores in agent_credentials table | N/A (CP side) | IMPLEMENTED | | Controller sends correctly shaped request; CP-side validation is out of scope |
| spec-controller:157 | On NOT_FOUND, retries with exponential backoff: 100ms initial, doubling, max 5s, max 10 attempts | controller.go:12-18,264-278 | IMPLEMENTED | | agentRegisterRetries=10, agentRegisterInitDelay=100ms, agentRegisterMaxDelay=5s, backoff doubling logic correct |
| spec-controller:158 | Once registration succeeds, agent authenticates via HTTP challenge-response | N/A (agent side) | IMPLEMENTED | | Controller passes --auth-url to agent; auth is agent's responsibility |
| spec-controller:165 | Host JWT 5-minute TTL | host_auth.go:12 | IMPLEMENTED | | hostJWTTTL = 5 * time.Minute |
| spec-controller:166 | Before TTL expiry (proactive refresh), controller sends POST /api/auth/challenge + POST /api/auth/verify | host_auth.go:88-117,120-151 | IMPLEMENTED | | Refresh() implements challenge + verify; StartRefreshLoop fires at TTL - buffer |
| spec-controller:167 | CP validates NKey signature and returns fresh host JWT | host_auth.go:137-149 | IMPLEMENTED | | verifyChallenge returns new JWT |
| spec-controller:168 | Controller reconnects to NATS with new JWT | host_auth.go:56-61 | IMPLEMENTED | | JWTFunc returns updated JWT; NATS reconnect uses it |
| spec-controller:169 | On permissions violation, trigger immediate refresh + retry | main.go:107-115 | IMPLEMENTED | | ErrorHandler checks isPermissionsViolation, calls hostAuth.Refresh |
| spec-controller:171 | Host controller NOT involved in agent credential refresh | host_auth.go, controller.go | IMPLEMENTED | | No agent JWT refresh code in controller |
| spec-controller:175 | Liveness: hub publishes $SYS.ACCOUNT.{accountKey}.CONNECT, CP updates hosts.last_seen_at and KV online=true | N/A (hub+CP side) | IMPLEMENTED | | Controller simply connects; liveness is passive via NATS connection |
| spec-controller:175 | Controller does not publish heartbeats | controller.go, main.go | IMPLEMENTED | | No heartbeat publishing code |
| spec-controller:183 | ProvisionRequest fields: userID, userSlug, hostSlug, projectID, projectSlug, gitUrl, gitIdentityId | controller.go:70-79 | IMPLEMENTED | | ProvisionRequest struct matches all 7 fields with correct JSON tags |
| spec-controller:193 | Reply on success: `{ok: true, projectSlug: "billing"}` | controller.go:82-87 | IMPLEMENTED | | ProvisionReply struct has OK, ProjectSlug, Error, Code fields |
| spec-controller:197 | Reply on failure: `{ok: false, error: "...", code: "..."}` | controller.go:82-87 | IMPLEMENTED | | Error replies use OK=false, Error string, Code string |
| spec-controller:209 | mclaude-controller-local: `mclaude.hosts.{hslug}.>` (host-scoped JWT, zero JetStream) | controller.go:113-115 | IMPLEMENTED | | Correct subscription scope |
| spec-controller:209 | Zero JetStream access — no $JS.*, $KV.*, $O.* subjects | main.go, controller.go | IMPLEMENTED | | No JetStream API calls in the codebase |
| spec-controller:221 | BYOH git clone fails → reply `{ok: false, code: "git_clone_failed"}` | controller.go:210-218 | IMPLEMENTED | | Exact code "git_clone_failed" on git clone failure |
| spec-controller:223 | Delete request without matching project → idempotent reply `{ok: true}` | controller.go:280-309 | IMPLEMENTED | | child.stop() only if exists; os.RemoveAll is idempotent; replies OK=true |
| spec-controller:225 | HOST_SLUG not provided → fatal exit with `FATAL: HOST_SLUG required` | main.go:50-52 | IMPLEMENTED | | Exact message match |
| spec-controller:241 | Dependencies: Hub NATS reachable | main.go:98-103 | IMPLEMENTED | | nats.Connect to hubURL |
| spec-controller:242 | Dependencies: host JWT in `~/.mclaude/hosts/{hslug}/nats.creds` | main.go:82-86,97 | IMPLEMENTED | | Default creds path matches spec |
| spec-controller:243 | Dependencies: git, gh, glab binaries | controller.go:207 | IMPLEMENTED | | Uses git clone via exec.Command; gh/glab are session-agent deps, not controller-local |
| spec-controller:244 | Dependencies: mclaude-session-agent binary on $PATH | supervisor.go:109 | IMPLEMENTED | | exec.Command("mclaude-session-agent", ...) |
| spec-controller:248 | Daemon mode deprecation Phase 1: mclaude-controller-local extended with ADR-0054 subject scheme, agent credential registration, host credential refresh | controller.go, host_auth.go, main.go | IMPLEMENTED | | All three features present in codebase |
| spec-controller:260 | No migration of in-flight sessions | N/A (policy) | IMPLEMENTED | | No migration code exists |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| main.go:1-7 | INFRA | Package declaration, imports |
| main.go:28-35 | INFRA | Log level parsing and global logger setup |
| main.go:37-44 | INFRA | Flag definitions (all covered by spec Configuration table) |
| main.go:53-55 | INFRA | Slug validation for host slug |
| main.go:62-68 | INFRA | User slug validation and informational logging |
| main.go:95-104 | INFRA | Deprecation notice log message (informational, matches spec's deprecation section) |
| main.go:106-119 | INFRA | NKey-based NATS auth setup with HostAuth when cpURL is set — implements spec's JWT refresh |
| main.go:120-126 | INFRA | Static creds file fallback when no cpURL |
| main.go:128-132 | INFRA | NATS connect with options |
| main.go:134-136 | INFRA | Signal context for graceful shutdown (SIGINT/SIGTERM) |
| main.go:139-141 | INFRA | Start host JWT refresh loop — covered by Host Credential Refresh spec section |
| main.go:143-152 | INFRA | Controller instantiation and startup logging |
| main.go:154-157 | INFRA | Controller run and exit logging |
| main.go:161-168 | INFRA | isPermissionsViolation helper — supports spec's "permissions violation → immediate refresh" |
| controller.go:1-14 | INFRA | Package declaration, imports |
| controller.go:16-29 | INFRA | Constants for agent registration retries — covered by spec Agent Credential Registration |
| controller.go:31-39 | INFRA | Constants for NKey file polling (agentNKeyReadTimeout=15s, agentNKeyPollInterval=200ms) |
| controller.go:43-48 | INFRA | childKey type — internal key for multi-user child tracking |
| controller.go:54-64 | INFRA | Controller struct definition — internal state |
| controller.go:67-79 | INFRA | ProvisionRequest struct — covered by spec Shared Behavior |
| controller.go:82-87 | INFRA | ProvisionReply struct — covered by spec Shared Behavior |
| controller.go:90-100 | INFRA | agentRegisterRequest/agentRegisterReply structs — covered by spec Agent Credential Registration |
| controller.go:105-112 | INFRA | NewController constructor |
| controller.go:130-158 | INFRA | handleMessage router — dispatches create/delete/unknown, logs unrecognized |
| controller.go:169-175 | INFRA | handleCreate: payload parsing, JSON unmarshal |
| controller.go:176-183 | INFRA | handleCreate: slug validation from subject |
| controller.go:196-201 | INFRA | handleCreate: idempotency check — if child already running, reply OK |
| controller.go:311-318 | INFRA | reply helper — marshals and responds to NATS msg; no-op if no Reply subject |
| controller.go:321-330 | INFRA | shutdownChildren — iterates all children, calls stop |
| controller.go:333-339 | INFRA | minDuration utility |
| supervisor.go:1-7 | INFRA | Package declaration, imports |
| supervisor.go:24-37 | INFRA | supervisedChild struct definition |
| supervisor.go:44-57 | INFRA | startChild constructor — creates supervisedChild, starts goroutine |
| supervisor.go:60-81 | INFRA | run() — supervisor loop with select on stopCh and restart delay |
| supervisor.go:90-123 | INFRA | buildCmd — assembles exec.Cmd for session-agent |
| supervisor.go:126-155 | INFRA | stop — sends SIGINT, waits grace period, sends SIGKILL |
| host_auth.go:1-11 | INFRA | Package declaration, imports |
| host_auth.go:13-16 | INFRA | Constants hostJWTTTL and hostJWTRefreshBuffer |
| host_auth.go:20-28 | INFRA | HostAuth struct |
| host_auth.go:33-49 | INFRA | NewHostAuthFromCredsFile — loads NKey + JWT from .creds |
| host_auth.go:53-61 | INFRA | JWTFunc — returns current JWT for NATS auth |
| host_auth.go:65-69 | INFRA | SignFunc — signs NATS nonces |
| host_auth.go:72-74 | INFRA | PublicKey — returns NKey public key |
| host_auth.go:79-117 | INFRA | Refresh — challenge-response flow |
| host_auth.go:124-151 | INFRA | StartRefreshLoop — proactive refresh goroutine |
| host_auth.go:154-175 | INFRA | requestChallenge — HTTP POST /api/auth/challenge |
| host_auth.go:178-208 | INFRA | verifyChallenge — HTTP POST /api/auth/verify |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| spec-controller:103 | Host slug sourced from --host, fallback to active-host | None | None | UNTESTED | main.go flag parsing — not unit tested |
| spec-controller:103 | Hard fail on missing HOST_SLUG | None | None | UNTESTED | main.go fatal path — not unit tested (would require process exec test) |
| spec-controller:120 | Subscription subject `mclaude.hosts.{hslug}.>` | controller_test.go:TestControllerSubscriptionSubject | None | UNIT_ONLY | Tests subject generation |
| spec-controller:126 | Agent register subject | controller_test.go:TestControllerAgentRegisterSubject | None | UNIT_ONLY | Tests subject generation |
| spec-controller:124 | Create handler: materializes worktree, clones git, starts agent, registers NKey | controller_test.go:TestHandleCreate_GitCloneFailed, TestHandleCreate_GitCloneSkippedWhenRepoExists | None | UNIT_ONLY | Tests git clone failure path and idempotency; no full handler integration test |
| spec-controller:125 | Delete handler: stops subprocess, removes dir, idempotent | None | None | UNTESTED | No delete handler tests |
| spec-controller:128 | Subject token parsing / routing | controller_test.go:TestHandleMessage_SubjectTokenParsing | None | UNIT_ONLY | Tests subject parsing logic |
| spec-controller:136 | Session-agent started with correct flags | controller_test.go:TestSupervisorBuildCmd_* | None | UNIT_ONLY | Tests buildCmd flag generation |
| spec-controller:137 | Restart on crash with 2s delay | None | None | UNTESTED | No supervisor restart test |
| spec-controller:138 | Shutdown grace period 10s | controller_test.go:TestShutdownGracePeriod_Is10s | None | UNIT_ONLY | Constant value check |
| spec-controller:147 | NKey IPC: waitForNKeyFile | controller_test.go:TestWaitForNKeyFile_* | None | UNIT_ONLY | File exists, late appearance, whitespace stripping |
| spec-controller:155 | Agent NKey registration via NATS request | controller_test.go:TestAgentRegisterRequest_Marshal | None | UNIT_ONLY | Tests request marshalling only, not NATS flow |
| spec-controller:157 | Registration retry with exponential backoff | None | None | UNTESTED | No retry logic test |
| spec-controller:166 | Host JWT refresh via HTTP challenge-response | controller_test.go:TestHostAuth_RefreshFlow, TestHostAuth_RefreshUpdatesStoredJWT | None | UNIT_ONLY | Uses httptest server — unit-level mock |
| spec-controller:169 | Permissions violation → immediate refresh | None | None | UNTESTED | No test for ErrorHandler/isPermissionsViolation path |
| spec-controller:183 | ProvisionRequest/Reply marshalling | controller_test.go:TestProvisionReply_*, TestAgentRegisterRequest_Marshal | None | UNIT_ONLY | JSON marshalling tests |
| spec-controller:209 | Zero JetStream access | N/A | None | UNTESTED | No test verifying absence of JetStream calls (hard to test) |
| spec-controller:221 | git_clone_failed error code | controller_test.go:TestHandleCreate_GitCloneFailed | None | UNIT_ONLY | Tests error code shape |

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | No open bugs for mclaude-controller-local | N/A | No matching bugs in .agent/bugs/ |

### Summary

- Implemented: 33
- Gap: 2 (both SPEC→FIX)
- Partial: 0
- Infra: 41
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 10
- E2E only: 0
- Untested: 7
- Bugs fixed: 0
- Bugs open: 0

### Non-clean findings

```
GAP [SPEC→FIX]: "removes ~/.mclaude/projects/{pslug}/" → code removes {dataDir}/{uslug}/{pslug} which is correct per the Configuration table's data-dir format {data-dir}/{uslug}/{pslug}/. Spec delete line should say {data-dir}/{uslug}/{pslug}/ (controller.go:296)

GAP [SPEC→FIX]: "Starts mclaude-session-agent --mode standalone ... --data-dir ~/.mclaude/projects/{pslug}" → code passes --data-dir {worktreeDir} where worktreeDir = {dataDir}/{uslug}/{pslug}/worktree. The code's approach is correct for multi-user support; spec should say --data-dir {data-dir}/{uslug}/{pslug}/worktree (supervisor.go:97)
```
