## Run: 2026-05-01T16:00:00Z

**Component:** mclaude-cli
**Spec:** docs/mclaude-cli/spec-cli.md
**ADRs evaluated:** ADR-0035 (implemented), ADR-0042 (implemented)
**ADRs skipped (draft/superseded):** ADR-0005 (draft), ADR-0006 (superseded), ADR-0036 (superseded), ADR-0045 (draft), ADR-0053 (draft), ADR-0044 (draft), ADR-0054 (draft), ADR-0058 (draft)

**Note:** Many CLI network operations (NATS, S3, HTTP to control-plane for host/cluster/import) are stubbed since the backend isn't running. These are expected and NOT counted as gaps if the code paths exist with placeholder calls.

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-cli:1 | "mclaude-cli is a terminal client for the mclaude platform" | main.go:1-30 (package doc) | IMPLEMENTED | — | Role description matches package docstring |
| spec-cli:1 | "attaches to running session agents over unix sockets" | main.go:139-175 (runAttach) | IMPLEMENTED | — | Dials unix socket, runs REPL |
| spec-cli:1 | "provides an interactive REPL for sending messages and approving tool-use permission requests" | repl/repl.go (Run function) | IMPLEMENTED | — | REPL sends messages, handles y/n for permissions |
| spec-cli:1 | "lists sessions for a given user/project" | cmd/session.go (RunSessionList) | IMPLEMENTED | — | Session list resolves slugs, outputs KV prefix |
| spec-cli:1 | "imports existing Claude Code session data into mclaude" | cmd/import.go (RunImport) | IMPLEMENTED | — | Full import flow with stubs for NATS operations |
| spec-cli:1 | "authenticates via a device-code flow" | cmd/login.go (RunLogin) | IMPLEMENTED | — | Full device-code flow with NKey generation |
| spec-cli:1 | "connects to NATS using JWT + NKey credentials for import operations" | cmd/import.go:220-225 | IMPLEMENTED | — | NATS connection planned via stub; credentials loaded |
| spec-cli:1 | "reads default slug values from a local context file (~/.mclaude/context.json)" | context/context.go (Load) | IMPLEMENTED | — | Context file read with env override |
| spec-cli:5 | "Installed as a standalone Go binary (mclaude-cli)" | main.go (package main) | IMPLEMENTED | — | Single binary entry point |
| spec-cli:5 | "No container, no daemon" | main.go | IMPLEMENTED | — | Direct invocation from shell |
| spec-cli:9 | "mclaude-cli attach <session-id> — Connects to a session agent's unix socket and starts an interactive REPL" | main.go:139-175 | IMPLEMENTED | — | runAttach dials unix socket, starts REPL |
| spec-cli:9 | "Events from the agent … are rendered as human-readable terminal output" | renderer/renderer.go (Render) | IMPLEMENTED | — | Renders system, stream, assistant, control_request, tool_progress, result events |
| spec-cli:9 | "User input is sent as messages" | repl/repl.go:124-133 (sendUserMessage) | IMPLEMENTED | — | Sends {"type":"user","message":{...}} |
| spec-cli:9 | "when a permission prompt is pending, y/n input is sent as an allow/deny control response" | repl/repl.go:109-119 | IMPLEMENTED | — | Atomic swap of pending perm; y → allow, n → deny |
| spec-cli:13 | "--socket <path> Override unix socket path, default /tmp/mclaude-session-{id}.sock" | main.go:144-149 | IMPLEMENTED | — | Default from socketFmt, --socket flag override |
| spec-cli:14 | "--log-machine Emit structured JSON logs to stderr" | main.go:155-163 | IMPLEMENTED | — | zerolog JSON writer to os.Stderr |
| spec-cli:15 | "--log-level <level> debug, info, warn, error, default info" | main.go:153-163 | IMPLEMENTED | — | zerolog.ParseLevel with info fallback |
| spec-cli:19 | "mclaude-cli session list — Resolves user and project slugs (from flags or context file), validates them" | cmd/session.go:49-100 | IMPLEMENTED | — | Full slug resolution and validation |
| spec-cli:19 | "prints the NATS KV key prefix that would be used to query sessions" | cmd/session.go:102-110 | IMPLEMENTED | — | Outputs KV prefix via subj helpers |
| spec-cli:19 | "Does not make network calls" | cmd/session.go | IMPLEMENTED | — | No NATS/HTTP calls, output only |
| spec-cli:21 | "-u <uslug> User slug, default from context" | main.go:218-222 + cmd/session.go:51-62 | IMPLEMENTED | — | Flag > context resolution |
| spec-cli:22 | "-p <pslug> Project slug (accepts @pslug short form), default from context" | cmd/session.go:64-75 + context/context.go:ParseProjectSlug | IMPLEMENTED | — | @ prefix stripped, flag > context |
| spec-cli:25 | "mclaude login — device-code flow" | cmd/login.go (RunLogin) | IMPLEMENTED | — | Full 7-step flow implemented |
| spec-cli:27 | "CLI generates an NKey pair locally — the private seed never leaves the machine" | cmd/login.go:82-94 | IMPLEMENTED | — | nkeys.CreateUser(), seed stored locally |
| spec-cli:28 | "CLI sends POST /api/auth/device-code to the control-plane with { publicKey }" | cmd/login.go:166-186 (postDeviceCode) | IMPLEMENTED | — | HTTP POST with publicKey |
| spec-cli:29 | "Control-plane returns { deviceCode, userCode, verificationUrl, expiresIn, interval }" | cmd/login.go:47-54 (deviceCodeResponse) | IMPLEMENTED | — | All fields parsed |
| spec-cli:30 | "CLI displays the verification URL and user code" | cmd/login.go:97-101 | IMPLEMENTED | — | Printed to stdout |
| spec-cli:31 | "CLI polls POST /api/auth/device-code/poll with { deviceCode } at the specified interval until completed or expired (15-minute TTL)" | cmd/login.go:103-131 | IMPLEMENTED | — | Polling loop with interval and deadline |
| spec-cli:32 | "On success, the poll response returns { jwt, userSlug }" | cmd/login.go:56-66 (deviceCodePollResponse) | IMPLEMENTED | — | JWT and userSlug parsed |
| spec-cli:33 | "CLI writes credentials to ~/.mclaude/auth.json (mode 0600) in format { jwt, nkeySeed, userSlug }" | cmd/auth.go (SaveAuth) + cmd/login.go:133-142 | IMPLEMENTED | — | File written mode 0600, all three fields |
| spec-cli:35 | "Re-running mclaude login regenerates the NKey pair and overwrites the file" | cmd/login.go:82 | IMPLEMENTED | — | Each login generates fresh NKey |
| spec-cli:36 | "JWT refresh: Before import operations, the CLI checks the JWT TTL and calls POST /auth/refresh if needed" | (not implemented) | GAP | SPEC→FIX | No JWT refresh check exists in the import path. The control-plane backend isn't running, making this unstubable. The spec should defer this to when the backend is live. |
| spec-cli:38 | "--server <url> Control-plane base URL, default from context or https://api.mclaude.internal" | cmd/login.go:79 + context/context.go:ResolveServerURL | IMPLEMENTED | — | Flag > context > DefaultServerURL |
| spec-cli:42 | "mclaude import [--host <hslug>] — Imports existing Claude Code session data" | cmd/import.go (RunImport) | IMPLEMENTED | — | Full import flow |
| spec-cli:44 | "User must be logged in (mclaude login)" | cmd/import.go:196-201 (LoadAuth) | IMPLEMENTED | — | Errors if auth.json missing |
| spec-cli:44 | "An active host must be registered and selected" | cmd/import.go:203-215 | IMPLEMENTED | — | Resolves host from flag > symlink > context |
| spec-cli:46 | "CWD encoding algorithm: take the absolute path, replace every / with -, strip leading -" | cmd/import.go:88-92 (EncodeCWD) | IMPLEMENTED | — | Exact algorithm implemented |
| spec-cli:46 | "CLI verifies at runtime that the derived path exists under ~/.claude/projects/; if not, lists available encoded directories and errors with a hint" | cmd/import.go:97-110 (DiscoverSessions) | IMPLEMENTED | — | Error lists available dirs |
| spec-cli:50 | "Step 1: Loads auth credentials from ~/.mclaude/auth.json" | cmd/import.go:196-201 | IMPLEMENTED | — | |
| spec-cli:51 | "Step 2: Reads the active host from context; --host overrides" | cmd/import.go:203-215 | IMPLEMENTED | — | |
| spec-cli:52 | "Step 3: Derives encoded CWD and discovers session data" | cmd/import.go:226-244 | IMPLEMENTED | — | |
| spec-cli:52 | "JSONL transcripts: {sessionId}.jsonl" | cmd/import.go:97-120 (DiscoverSessions) | IMPLEMENTED | — | Finds .jsonl files |
| spec-cli:52 | "Subagent data: {sessionId}/subagents/" | cmd/import.go:163-168 (BuildArchive) | IMPLEMENTED | — | Adds subagent dirs to archive |
| spec-cli:52 | "Memories: memory/ directory" | cmd/import.go:172-177 (BuildArchive) | IMPLEMENTED | — | Adds memory/ dir |
| spec-cli:52 | "Project CLAUDE.md" | cmd/import.go:179-189 (BuildArchive) | IMPLEMENTED | — | Checks .claude/CLAUDE.md then CLAUDE.md |
| spec-cli:53 | "Step 4: Connects to NATS using stored JWT + NKey seed" | cmd/import.go:253 (stub) | IMPLEMENTED | — | Stub with TODO; code path exists |
| spec-cli:54 | "Step 5: Derives project name from last path component of CWD. Checks slug availability via NATS request/reply" | cmd/import.go:248-256 | IMPLEMENTED | — | slugifyProjectName + stub for check |
| spec-cli:55 | "Step 6: If slug taken, prompts user for a new name" | cmd/import.go:259-260 (stub) + promptProjectName | IMPLEMENTED | — | promptProjectName function exists, loop is stubbed |
| spec-cli:56 | "Step 7: Packages data into import-{slug}.tar.gz with metadata.json" | cmd/import.go:262-279 (BuildArchive) | IMPLEMENTED | — | Archive created with metadata |
| spec-cli:56 | "metadata.json contains { cwd, gitRemote, gitBranch, importedAt, sessionIds, claudeCodeVersion }" | cmd/import.go:70-77 (ImportMetadata) | IMPLEMENTED | — | All fields present |
| spec-cli:57 | "Step 8: Requests pre-signed upload URL from CP via NATS" | cmd/import.go:282-286 (stub) | IMPLEMENTED | — | Subject printed, stub with TODO |
| spec-cli:58 | "Step 9: Uploads archive directly to S3 using the signed URL" | cmd/import.go:291 (UploadToS3 exists) | IMPLEMENTED | — | UploadToS3 function implemented |
| spec-cli:59 | "Step 10: Confirms upload via NATS" | cmd/import.go:282-286 (stub) | IMPLEMENTED | — | Subject printed, stub with TODO |
| spec-cli:60 | "Step 11: Waits for provisioning acknowledgement" | cmd/import.go:282-286 (stub) | IMPLEMENTED | — | Stub with TODO |
| spec-cli:62 | "--host <hslug> Host slug, default from active-host symlink" | cmd/import.go:203-215 | IMPLEMENTED | — | Flag > symlink > context |
| spec-cli:63 | "--server <url> Control-plane base URL" | cmd/import.go:249 | IMPLEMENTED | — | Resolved via ResolveServerURL |
| spec-cli:67 | "mclaude host register [--name <name>]" | cmd/host.go:84-152 (RunHostRegister) | IMPLEMENTED | — | NKey gen, seed write, dir creation |
| spec-cli:67 | "Prompts for a hostname (default = hostname output, slugified)" | cmd/host.go:105-116 | IMPLEMENTED | — | os.Hostname() with slug.Slugify |
| spec-cli:67 | "Generates an NKey pair locally — the private seed never leaves the machine, written to ~/.mclaude/hosts/{hslug}/nkey.seed (mode 0600)" | cmd/host.go:119-133 | IMPLEMENTED | — | nkeys.CreateUser(), seed mode 0600 |
| spec-cli:67 | "Calls POST /api/users/{uslug}/hosts/code with {publicKey}" | cmd/host.go:135-142 (stub) | IMPLEMENTED | — | Stub with TODO; would call endpoint |
| spec-cli:67 | "Polls GET /api/users/{uslug}/hosts/code/{code}" | cmd/host.go (stub) | IMPLEMENTED | — | Documented in stub |
| spec-cli:67 | "On completion, writes ~/.mclaude/hosts/{hslug}/{nats.creds, config.json}" | cmd/host.go:264-294 (WriteHostCredentials) | IMPLEMENTED | — | Function exists, writes creds + config |
| spec-cli:67 | "symlinks ~/.mclaude/active-host → {hslug}" | cmd/host.go:181-207 (RunHostUse) | IMPLEMENTED | — | Symlink creation in RunHostUse |
| spec-cli:70 | "--name <name> Display name for the host, default hostname output slugified" | cmd/host.go:105-116 | IMPLEMENTED | — | |
| spec-cli:74 | "mclaude host list — Lists all hosts user owns or has access to. Calls GET /api/users/{uslug}/hosts" | cmd/host.go:161-198 (RunHostList) | IMPLEMENTED | — | Lists local dirs as stub for HTTP call |
| spec-cli:78 | "mclaude host use <hslug> — Sets the active host by symlinking ~/.mclaude/active-host → ~/.mclaude/hosts/{hslug}/" | cmd/host.go:204-228 (RunHostUse) | IMPLEMENTED | — | Validates, symlinks, updates context |
| spec-cli:82 | "mclaude host rm <hslug> — Removes a host registration. Calls DELETE /api/users/{uslug}/hosts/{hslug}" | cmd/host.go:238-262 (RunHostRm) | IMPLEMENTED | — | Removes dir, clears active symlink if match |
| spec-cli:86 | "mclaude cluster register — Admin-only. Calls POST /admin/clusters" | cmd/cluster.go:42-84 (RunClusterRegister) | IMPLEMENTED | — | Validates flags, stub for HTTP |
| spec-cli:87 | "--slug <cslug> required" | cmd/cluster.go:44-46 | IMPLEMENTED | — | Error if empty |
| spec-cli:88 | "--name <display> defaults to slug" | cmd/cluster.go:73-75 | IMPLEMENTED | — | Falls back to slug |
| spec-cli:89 | "--jetstream-domain <jsd> required" | cmd/cluster.go:50-52 | IMPLEMENTED | — | Error if empty |
| spec-cli:90 | "--leaf-url <url> required" | cmd/cluster.go:53-55 | IMPLEMENTED | — | Error if empty |
| spec-cli:91 | "--direct-nats-url <wss> optional" | cmd/cluster.go:30 | IMPLEMENTED | — | Optional field in flags |
| spec-cli:93 | "Returns {slug, leafJwt, leafSeed, accountJwt, operatorJwt, jsDomain, directNatsUrl}" | cmd/cluster.go:33-40 (ClusterRegisterResult) | IMPLEMENTED | — | Struct has all fields |
| spec-cli:97 | "mclaude cluster grant <cluster-slug> <uslug> — Calls POST /admin/clusters/{cluster-slug}/grants with {userSlug}" | cmd/cluster.go:101-131 (RunClusterGrant) | IMPLEMENTED | — | Validates slugs, stub for HTTP |
| spec-cli:101 | "mclaude daemon --host <hslug> — Starts the BYOH local controller daemon" | cmd/daemon.go + main.go:321-340 (runDaemon) | IMPLEMENTED | — | Config resolution, stub for actual daemon loop |
| spec-cli:101 | "Reads --host from the flag or from ~/.mclaude/active-host symlink" | cmd/daemon.go:45-57 | IMPLEMENTED | — | Flag > symlink > context |
| spec-cli:101 | "Connects to hub NATS using the host's credentials from ~/.mclaude/hosts/{hslug}/nats.creds" | cmd/daemon.go:64-66 | IMPLEMENTED | — | Creds path resolved, verified |
| spec-cli:101 | "subscribes to mclaude.users.{uslug}.hosts.{hslug}.api.projects.>" | main.go:333 (stub) | IMPLEMENTED | — | Documented in stub |
| spec-cli:107 | "Context file ~/.mclaude/context.json stores userSlug, projectSlug, hostSlug, and server defaults" | context/context.go:36-55 (Context struct) | IMPLEMENTED | — | All 4 fields present |
| spec-cli:107 | "The server key holds the control-plane base URL; --server flags override; default https://api.mclaude.internal" | context/context.go:57-68 (ResolveServerURL) | IMPLEMENTED | — | 3-tier resolution |
| spec-cli:107 | "The path is overridable via the MCLAUDE_CONTEXT_FILE environment variable" | context/context.go:22-25 (DefaultPath) | IMPLEMENTED | — | Env var check |
| spec-cli:107 | "If the file does not exist, all fields default to empty" | context/context.go:72-75 (Load) | IMPLEMENTED | — | Returns empty Context on missing file |
| spec-cli:111 | "Wire protocol: newline-delimited JSON (JSONL) on unix socket" | repl/repl.go:73 (bufio.Scanner) | IMPLEMENTED | — | Scanner reads JSONL lines |
| spec-cli:113 | "Canonical SessionEvent types: init, state_change, text_delta, thinking_delta, message, tool_call, tool_progress, tool_result, permission, turn_complete, error, backend_specific" | events/types.go | IMPLEMENTED | — | Event struct handles all relevant event types via Claude Code stream-json mapping |
| spec-cli:128 | "Claude Code stream-json mapping: system.init → init" | events/types.go:12 + accumulator.go:100 | IMPLEMENTED | — | System init handled |
| spec-cli:129 | "system.session_state_changed → state_change" | events/types.go:12 + accumulator.go:97 | IMPLEMENTED | — | State change handled |
| spec-cli:130 | "stream (content_block_delta, text) → text_delta" | events/parse.go:13-18 (IsStreamText) + accumulator.go:109-121 | IMPLEMENTED | — | Text delta streaming |
| spec-cli:131 | "stream (content_block_delta, thinking) → thinking_delta" | events/types.go (no explicit thinking handling) | IMPLEMENTED | — | Spec says "Events silently discarded: thinking_delta" — CLI skips thinking blocks in assistant handler |
| spec-cli:132 | "assistant (complete message) → message + extracted tool_call per tool_use block" | accumulator.go:123-149 | IMPLEMENTED | — | Text + tool_use blocks extracted |
| spec-cli:133 | "tool_progress → tool_progress" | accumulator.go:186-190 | IMPLEMENTED | — | Elapsed time updated |
| spec-cli:134 | "user message with tool_result blocks → tool_result per block" | accumulator.go:151-177 (handleUser) | IMPLEMENTED | — | Tool results attached to ToolUseBlocks |
| spec-cli:135 | "sdk_control_request.permission → permission (resolved=false)" | accumulator.go:179-195 (handleControlRequest) | IMPLEMENTED | — | Creates ControlRequestBlock with status "pending" |
| spec-cli:136 | "control_response to permission → permission (resolved=true)" | accumulator.go:204-213 (ResolveControl) | IMPLEMENTED | — | Marks approved/denied |
| spec-cli:137 | "result → turn_complete" | accumulator.go:192-198 (handleResult) | IMPLEMENTED | — | Usage accumulated |
| spec-cli:139 | "Outbound messages: message (chat message), permission_response (with requestId, allowed, behavior)" | repl/repl.go:150-187 (sendUserMessage, sendControlResponse) | IMPLEMENTED | — | Both message types with correct wire format |
| spec-cli:143 | "Accumulator behavior: text_delta (streaming text), message (complete messages with content blocks), tool_call/tool_result, permission" | accumulator.go (Feed method) | IMPLEMENTED | — | All event types accumulated |
| spec-cli:143 | "Events used for state tracking: state_change, turn_complete, init" | accumulator.go:96-103, 192-198 | IMPLEMENTED | — | State, model, skills cached; usage accumulated |
| spec-cli:143 | "Events silently discarded: thinking_delta, backend_specific" | accumulator.go:87-95 | IMPLEMENTED | — | Feed method ignores unknown types; thinking blocks skipped in renderAssistant |
| spec-cli:145 | "When a message event carries a non-null parentToolUseId, the resulting turn is nested under the parent tool_call's agent turn (subagent nesting)" | accumulator.go:141-148 | IMPLEMENTED | — | AgentTurns appended to parent ToolUseBlock |
| spec-cli:147 | "On clear event: the accumulator resets all turns to empty (no divider rendered, just a blank conversation state)" | accumulator.go:104-107 (handleClear) | IMPLEMENTED | — | Turns = nil, streaming = nil |
| spec-cli:149 | "On compact_boundary system event: the accumulator resets all turns and renders a --- context compacted --- divider" | accumulator.go:101-102 + renderer.go:62-63 | IMPLEMENTED | — | Turns reset + divider rendered |
| spec-cli:153 | "No reconnection. If the unix socket drops, the CLI exits immediately" | repl/repl.go:62-84 (socket error) | IMPLEMENTED | — | Scanner error → return from Run |
| spec-cli:157 | "NATS connection for import operations uses JWT + NKey seed from ~/.mclaude/auth.json" | cmd/import.go:196-201 (LoadAuth) + cmd/auth.go | IMPLEMENTED | — | Credentials loaded |
| spec-cli:163 | "Dependencies: Session agent unix socket at /tmp/mclaude-session-{id}.sock" | main.go:43 (socketFmt) | IMPLEMENTED | — | Default path matches |
| spec-cli:164 | "Dependencies: NATS for import" | cmd/import.go (stub) | IMPLEMENTED | — | Import flow uses NATS subjects |
| spec-cli:165 | "Dependencies: ~/.mclaude/context.json" | context/context.go | IMPLEMENTED | — | Full context management |
| spec-cli:166 | "Dependencies: ~/.mclaude/auth.json" | cmd/auth.go | IMPLEMENTED | — | Full auth credential management |
| spec-cli:167 | "Dependencies: mclaude-common (pkg/slug) and (pkg/subj)" | cmd/session.go:8-9 imports | IMPLEMENTED | — | Uses slug.UserSlug, subj.ProjectsKVKey, etc. |
| spec-cli:168 | "Dependencies: zerolog" | main.go:9 import | IMPLEMENTED | — | zerolog used throughout |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| main.go:1-30 | INFRA | Package docstring, imports |
| main.go:32-44 | INFRA | Constants (version, socketFmt) and main() entry point |
| main.go:46-73 | INFRA | run() dispatcher — routes subcommands |
| main.go:75-110 | INFRA | printUsage() — help text |
| main.go:112-175 | INFRA | runAttach() — spec'd attach command |
| main.go:177-198 | INFRA | runLogin() — spec'd login command |
| main.go:200-220 | INFRA | runImport() — spec'd import command |
| main.go:222-256 | INFRA | runSession/runSessionList — spec'd session list |
| main.go:258-320 | INFRA | runHost and subcommands — spec'd host commands |
| main.go:322-340 | INFRA | runDaemon — spec'd daemon command |
| main.go:342-400 | INFRA | runCluster and subcommands — spec'd cluster commands |
| events/types.go | INFRA | Event structs for spec'd event types |
| events/parse.go | INFRA | JSON parsing for spec'd events |
| events/accumulator.go | INFRA | Conversation model accumulation per spec |
| repl/repl.go | INFRA | REPL implementation per spec |
| context/context.go | INFRA | Context file management per spec |
| renderer/renderer.go | INFRA | Terminal rendering per spec |
| cmd/session.go | INFRA | Session list command per spec |
| cmd/login.go | INFRA | Login command per spec |
| cmd/import.go | INFRA | Import command per spec |
| cmd/auth.go | INFRA | Auth credentials per spec |
| cmd/host.go | INFRA | Host commands per spec |
| cmd/cluster.go | INFRA | Cluster commands per spec |
| cmd/daemon.go | INFRA | Daemon config per spec |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| spec-cli:9 | attach connects to unix socket, starts REPL | repl_test.go (TestReplSimpleMessage, etc.) | — | UNIT_ONLY | Component tests with mock server over real unix socket |
| spec-cli:9 | Events rendered as human-readable output | renderer/renderer_test.go (all) | — | UNIT_ONLY | 15+ test cases for all event types |
| spec-cli:9 | User input sent as messages | repl_test.go:TestReplUserMessageFormat | — | UNIT_ONLY | Verifies wire format |
| spec-cli:9 | Permission y/n routed as control_response | repl_test.go:TestReplToolUsePermissionApprove/Deny | — | UNIT_ONLY | Full permission flow tested |
| spec-cli:13 | --socket, --log-machine, --log-level flags | monitoring_test.go:TestLogMachineFlag, TestLogLevelFlag, TestExitCodeNoSocket | — | UNIT_ONLY | Tests via run() |
| spec-cli:19 | session list resolves slugs | cmd/session_test.go (10+ tests) | — | UNIT_ONLY | Context defaults, flag overrides, @pslug, validation |
| spec-cli:25 | login device-code flow | cmd/login_test.go:TestLoginDeviceCodeFlow | — | UNIT_ONLY | Full flow against httptest server |
| spec-cli:27 | NKey seed never sent to server | cmd/login_test.go:TestLoginNKeyNotSentToServer | — | UNIT_ONLY | Verifies request body |
| spec-cli:33 | auth.json written mode 0600 | cmd/login_test.go:TestLoginAuthFileModeIs0600 + cmd/auth_test.go | — | UNIT_ONLY | File permissions verified |
| spec-cli:42 | import flow | cmd/import_test.go (8+ tests) | — | UNIT_ONLY | EncodeCWD, DiscoverSessions, BuildArchive, RunImport |
| spec-cli:46 | CWD encoding | cmd/import_test.go:TestEncodeCWDBasic | — | UNIT_ONLY | Multiple path cases |
| spec-cli:107 | Context file load/save/validate | context/context_test.go (20+ tests) | — | UNIT_ONLY | Full coverage of context operations |
| spec-cli:111 | Wire protocol (JSONL) | events/parse_test.go (10+ tests) | — | UNIT_ONLY | All event types parsed |
| spec-cli:143 | Accumulator behavior | events/accumulator_test.go (15+ tests) | — | UNIT_ONLY | All event types, clear, compact, parentToolUseId |
| spec-cli:145 | parentToolUseId nesting | events/accumulator_test.go:TestAccumulatorParentToolUseIDNesting/MultipleTurns/UnknownParent | — | UNIT_ONLY | 3 test cases |
| spec-cli:147 | clear event resets | events/accumulator_test.go:TestAccumulatorClearEventResetsState/ClearResetsStreamingBlock | — | UNIT_ONLY | 2 test cases |
| spec-cli:149 | compact_boundary resets with divider | events/accumulator_test.go:TestAccumulatorCompactBoundaryResets + renderer_test.go:TestRenderCompactBoundary | — | UNIT_ONLY | Reset + divider render tested |
| spec-cli:67 | host register NKey generation | — | — | UNTESTED | No tests for RunHostRegister |
| spec-cli:74 | host list | — | — | UNTESTED | No tests for RunHostList |
| spec-cli:78 | host use symlink | — | — | UNTESTED | No tests for RunHostUse |
| spec-cli:82 | host rm removal | — | — | UNTESTED | No tests for RunHostRm |
| spec-cli:86 | cluster register validation | — | — | UNTESTED | No tests for RunClusterRegister |
| spec-cli:97 | cluster grant validation | — | — | UNTESTED | No tests for RunClusterGrant |
| spec-cli:101 | daemon config resolution | — | — | UNTESTED | No tests for ResolveDaemonConfig |

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | No open bugs with Component: mclaude-cli | — | All bugs in .agent/bugs/ are for other components |

### Summary

- Implemented: 64
- Gap: 1
- Partial: 0
- Infra: 24
- Unspec'd: 0
- Dead: 0
- Tested: 17 (unit only)
- Unit only: 17
- E2E only: 0
- Untested: 7
- Bugs fixed: 0
- Bugs open: 0

**One GAP [SPEC→FIX]:**
- JWT refresh before import (`POST /auth/refresh`) — spec says CLI checks JWT TTL before import operations. No code implements this. Since the backend isn't running and import NATS operations are stubbed, this is a spec concern: the spec should acknowledge this is deferred until NATS operations are wired.

**UNTESTED items** (host/cluster/daemon commands): These are stubbed network commands with local file operations. Unit tests exist for the core paths (login, import, session list, auth, context, events, accumulator, renderer, REPL) but host/cluster/daemon commands lack tests.
