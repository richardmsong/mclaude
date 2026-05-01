## Run: 2026-05-01T00:00:00Z

**Component:** mclaude-cli
**Spec:** docs/mclaude-cli/spec-cli.md
**ADRs evaluated:** ADR-0005 (draft — skipped per status filter), ADR-0053 (draft — skipped), ADR-0054 (draft — skipped)
**Note:** All provided ADRs are `draft` status. Per evaluator rules, only `accepted` or `implemented` ADRs are authoritative. The spec file (no status field) is always authoritative.

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-cli:1-2 | "mclaude-cli is a terminal client for the mclaude platform" | main.go (entire file) | IMPLEMENTED | | Standalone Go binary with CLI subcommands |
| spec-cli:1-2 | "attaches to running session agents over unix sockets" | main.go:129-170, repl/repl.go | IMPLEMENTED | | `runAttach` dials unix socket, starts REPL |
| spec-cli:1-2 | "provides an interactive REPL for sending messages and approving tool-use permission requests" | repl/repl.go:55-140 | IMPLEMENTED | | REPL sends user messages and handles y/n permission responses |
| spec-cli:1-2 | "lists sessions for a given user/project" | cmd/session.go | IMPLEMENTED | | `RunSessionList` resolves slugs and prints KV key prefix |
| spec-cli:1-2 | "imports existing Claude Code session data into mclaude" | cmd/import.go | IMPLEMENTED | | `RunImport` discovers sessions, builds archive. NATS operations stubbed. |
| spec-cli:1-2 | "authenticates via a device-code flow" | cmd/login.go | IMPLEMENTED | | Full device-code flow implemented with HTTP calls |
| spec-cli:1-2 | "connects to NATS using JWT + NKey credentials for import operations" | cmd/import.go:250-285 | PARTIAL | CODE→FIX | NATS connection is stubbed (TODO comments). Auth credentials are loaded but NATS request/reply is not wired. |
| spec-cli:1-3 | "reads default slug values from a local context file (`~/.mclaude/context.json`)" | context/context.go | IMPLEMENTED | | Context file loaded with all fields |
| spec-cli:7 | "Installed as a standalone Go binary (`mclaude-cli`)" | main.go | IMPLEMENTED | | Single main.go entry point |
| spec-cli:9 | "No container, no daemon -- invoked directly from the shell" | main.go | IMPLEMENTED | | Direct CLI invocation |
| spec-cli:11 | "Requires a running session agent exposing a unix socket for the attach command" | main.go:129-170 | IMPLEMENTED | | Dials unix socket, fails if agent not running |
| spec-cli:13-17 | "`mclaude-cli attach <session-id>` — Connects to a session agent's unix socket and starts an interactive REPL" | main.go:120-170 | IMPLEMENTED | | |
| spec-cli:13-17 | "Events from the agent (streaming text, tool use, permission requests, progress, results, state changes, compaction boundaries) are rendered as human-readable terminal output" | renderer/renderer.go | IMPLEMENTED | | Renderer handles system, stream_event, assistant, control_request, tool_progress, result |
| spec-cli:13-17 | "User input is sent as messages; when a permission prompt is pending, `y`/`n` input is sent as an allow/deny control response instead" | repl/repl.go:96-120 | IMPLEMENTED | | Permission state tracked via atomic.Pointer; y/n sends control_response |
| spec-cli:19-22 | "attach --socket <path> — Override unix socket path, default /tmp/mclaude-session-{id}.sock" | main.go:123,136-140 | IMPLEMENTED | | socketPath defaults to `/tmp/mclaude-session-%s.sock`, --socket overrides |
| spec-cli:23 | "attach --log-machine — Emit structured JSON logs to stderr" | main.go:148-155 | IMPLEMENTED | | Sets zerolog JSON logger on os.Stderr |
| spec-cli:24 | "attach --log-level <level> — debug, info, warn, error; default info" | main.go:141-147,156-160 | IMPLEMENTED | | Parses level, defaults to info |
| spec-cli:26-34 | "`mclaude-cli session list` — Resolves user and project slugs (from flags or context file), validates them, and prints the NATS KV key prefix" | cmd/session.go | IMPLEMENTED | | Resolves slugs, validates, prints KV prefix |
| spec-cli:26-34 | "session list does not make network calls — outputs the resolved parameters only" | cmd/session.go | IMPLEMENTED | | No network calls; prints resolved params |
| spec-cli:30 | "session list -u flag for user slug" | main.go:213-216, cmd/session.go:59-66 | IMPLEMENTED | | |
| spec-cli:31 | "session list -p flag for project slug (accepts `@pslug` short form)" | main.go:218-221, cmd/session.go:69-76, context/context.go:101-106 | IMPLEMENTED | | @ prefix stripped by ParseProjectSlug |
| spec-cli:26-34 | "session list spec only lists -u and -p flags; code also requires --host" | cmd/session.go:79-86, main.go:223-226 | GAP | SPEC→FIX | Spec omits the --host flag from session list's flag table but the code requires a host slug (fails if missing). The code's behavior is correct — sessions are per-user per-host per-project, so host slug is needed. Spec should add --host to session list's flag table. |
| spec-cli:36-53 | "`mclaude login` — Device-code flow step 1: CLI generates an NKey pair locally" | cmd/login.go:88-99 | IMPLEMENTED | | Uses nkeys.CreateUser() |
| spec-cli:36-53 | "Step 2: CLI sends POST /api/auth/device-code to the control-plane with { publicKey }" | cmd/login.go:102-103 | IMPLEMENTED | | postDeviceCode sends publicKey |
| spec-cli:36-53 | "Step 3: Control-plane returns { deviceCode, userCode, verificationUrl, expiresIn, interval }" | cmd/login.go:43-50 | IMPLEMENTED | | deviceCodeResponse struct matches spec |
| spec-cli:36-53 | "Step 4: CLI displays the verification URL and user code" | cmd/login.go:106-112 | IMPLEMENTED | | Prints URL and code |
| spec-cli:36-53 | "Step 5: CLI polls POST /api/auth/device-code/poll with { deviceCode } at the specified interval until the user completes authentication or the code expires (15-minute TTL)" | cmd/login.go:114-138 | IMPLEMENTED | | Polls at interval, deadline defaults to 15 min |
| spec-cli:36-53 | "Step 6: On success, the poll response returns { jwt, userSlug }" | cmd/login.go:55-64 | IMPLEMENTED | | deviceCodePollResponse has JWT and UserSlug |
| spec-cli:36-53 | "Step 7: CLI writes credentials to ~/.mclaude/auth.json (mode 0600) in the format: { jwt, nkeySeed, userSlug }" | cmd/login.go:140-152, cmd/auth.go | IMPLEMENTED | | SaveAuth writes with 0600 mode, format matches spec |
| spec-cli:55 | "Re-running `mclaude login` regenerates the NKey pair and overwrites the file" | cmd/login.go:88-99 | IMPLEMENTED | | Each login generates new NKey pair; SaveAuth overwrites |
| spec-cli:57 | "JWT refresh: Before import operations, the CLI checks the JWT TTL and calls POST /auth/refresh if needed" | cmd/import.go | GAP | CODE→FIX | No JWT refresh logic exists in the import flow. The code loads credentials but never checks TTL or calls /auth/refresh. |
| spec-cli:59-61 | "login --server flag — Control-plane base URL, default from context.json or https://api.mclaude.internal" | cmd/login.go:80-84, context/context.go:56-62 | IMPLEMENTED | | ResolveServerURL handles priority: flag > context > default |
| spec-cli:63-90 | "`mclaude import` — Imports existing Claude Code session data from the local machine" | cmd/import.go | IMPLEMENTED | | RunImport implements the import flow |
| spec-cli:65 | "import prerequisites: User must be logged in" | cmd/import.go:241-246 | IMPLEMENTED | | LoadAuth errors if not logged in |
| spec-cli:66 | "import prerequisites: An active host must be registered and selected" | cmd/import.go:249-259 | IMPLEMENTED | | Resolves host slug from flag/symlink/context; errors if missing |
| spec-cli:68-69 | "CWD encoding algorithm: take absolute path, replace / with -, strip leading -" | cmd/import.go:75-82 | IMPLEMENTED | | EncodeCWD matches spec |
| spec-cli:69-70 | "CLI verifies at runtime that the derived path exists under ~/.claude/projects/; if not, lists available encoded directories and errors with a hint" | cmd/import.go:86-112 | IMPLEMENTED | | DiscoverSessions checks existence, lists available dirs on error |
| spec-cli:72-73 | "import flow step 4: Connects to NATS using stored JWT + NKey seed" | cmd/import.go:280-281 | PARTIAL | CODE→FIX | NATS connection is stubbed with TODO. Credentials are loaded but not used for NATS. |
| spec-cli:74-75 | "import flow step 5: Derives project name from last path component of CWD; checks slug availability via NATS request/reply" | cmd/import.go:272-284 | PARTIAL | CODE→FIX | Project name derivation works; slug check via NATS is stubbed |
| spec-cli:76 | "import flow step 6: If slug taken, prompts user for new name, re-checks" | cmd/import.go:286-287, cmd/import.go:225-234 | PARTIAL | CODE→FIX | promptProjectName exists but the slug collision loop is commented out (stub) |
| spec-cli:77 | "import flow step 7: Packages data into import-{slug}.tar.gz with metadata.json" | cmd/import.go:289-302 | IMPLEMENTED | | BuildArchive creates tar.gz with metadata.json |
| spec-cli:77 | "metadata.json containing { cwd, gitRemote, gitBranch, importedAt, sessionIds, claudeCodeVersion }" | cmd/import.go:64-72 | IMPLEMENTED | | ImportMetadata struct matches spec |
| spec-cli:78 | "import flow step 8: Requests pre-signed upload URL from CP via NATS request/reply" | cmd/import.go:305-308 | PARTIAL | CODE→FIX | Stubbed with TODO comments |
| spec-cli:79 | "import flow step 9: Uploads archive directly to S3 using the signed URL" | cmd/import.go:175-192, cmd/import.go:305-308 | PARTIAL | CODE→FIX | UploadToS3 function exists and is correct, but not called (NATS URL request is stubbed) |
| spec-cli:80 | "import flow step 10: Confirms upload via NATS request/reply with { importId }" | cmd/import.go:305-308 | PARTIAL | CODE→FIX | Stubbed with TODO |
| spec-cli:81 | "import flow step 11: Waits for provisioning acknowledgement, prints success" | cmd/import.go:305-308 | PARTIAL | CODE→FIX | Stubbed with TODO |
| spec-cli:83-84 | "import --host flag overrides active host" | cmd/import.go:249-256 | IMPLEMENTED | | Flag > active-host symlink > context |
| spec-cli:85 | "import --server flag" | cmd/import.go:280-281 | IMPLEMENTED | | ResolveServerURL used |
| spec-cli:87-98 | "`mclaude host register [--name <name>]` — Device-code registration flow, generates NKey pair, writes seed to ~/.mclaude/hosts/{hslug}/nkey.seed (mode 0600)" | cmd/host.go:93-160 | PARTIAL | CODE→FIX | NKey generation and seed writing implemented. HTTP device-code flow (POST /api/users/{uslug}/hosts/code, poll) is stubbed. |
| spec-cli:87-98 | "host register: Calls POST /api/users/{uslug}/hosts/code with {publicKey} to get a 6-character device code" | cmd/host.go:148-155 | PARTIAL | CODE→FIX | Stubbed — prints instructions but no HTTP call |
| spec-cli:87-98 | "host register: Polls GET /api/users/{uslug}/hosts/code/{code} until completion" | cmd/host.go:148-155 | PARTIAL | CODE→FIX | Stubbed |
| spec-cli:87-98 | "host register: On completion, writes ~/.mclaude/hosts/{hslug}/{nats.creds, config.json}" | cmd/host.go:281-310 | IMPLEMENTED | | WriteHostCredentials writes nats.creds and config.json |
| spec-cli:87-98 | "host register: Symlinks ~/.mclaude/active-host → {hslug}" | cmd/host.go:148-155 | PARTIAL | CODE→FIX | Symlink creation is not done in RunHostRegister (stubbed); it would happen after poll completion |
| spec-cli:87-98 | "host register --name flag: Display name, default hostname slugified" | cmd/host.go:102-118 | IMPLEMENTED | | Falls back to os.Hostname(), slugified |
| spec-cli:100-102 | "`mclaude host list` — Lists all hosts; calls GET /api/users/{uslug}/hosts; prints table of slug, name, type, role, online status" | cmd/host.go:175-225 | PARTIAL | CODE→FIX | Lists locally-registered hosts from ~/.mclaude/hosts/ directory. HTTP GET is stubbed. Does not print role or online status. |
| spec-cli:104-105 | "`mclaude host use <hslug>` — Sets active host by symlinking ~/.mclaude/active-host → ~/.mclaude/hosts/{hslug}/" | cmd/host.go:230-255 | IMPLEMENTED | | Creates symlink; validates slug; updates context |
| spec-cli:107-108 | "`mclaude host rm <hslug>` — Removes host registration; calls DELETE /api/users/{uslug}/hosts/{hslug}; removes local directory" | cmd/host.go:268-285 | PARTIAL | CODE→FIX | Local directory removed, DELETE HTTP call is stubbed |
| spec-cli:107-108 | "host rm: If removed host is active host, active-host symlink is also removed" | cmd/host.go:278-283 | IMPLEMENTED | | Checks and removes symlink + clears context |
| spec-cli:110-122 | "`mclaude cluster register` — Admin-only; calls POST /admin/clusters" | cmd/cluster.go | PARTIAL | CODE→FIX | Validates required flags (slug, jetstream-domain, leaf-url), but HTTP call is stubbed |
| spec-cli:110-122 | "cluster register --slug (required), --name, --jetstream-domain (required), --leaf-url (required), --direct-nats-url (optional)" | cmd/cluster.go:7-24, main.go:309-330 | IMPLEMENTED | | All flags parsed and validated |
| spec-cli:124 | "cluster register returns {slug, leafJwt, leafSeed, accountJwt, operatorJwt, jsDomain, directNatsUrl}" | cmd/cluster.go:33-40 | PARTIAL | CODE→FIX | ClusterRegisterResult struct has all fields but they're empty (HTTP call stubbed) |
| spec-cli:126-128 | "`mclaude cluster grant <cluster-slug> <uslug>` — Admin-only; calls POST /admin/clusters/{cluster-slug}/grants with {userSlug}" | cmd/cluster.go:82-117 | PARTIAL | CODE→FIX | Validates slugs, resolves context, but HTTP call is stubbed |
| spec-cli:130-135 | "`mclaude daemon --host <hslug>` — Starts BYOH local controller daemon" | cmd/daemon.go, main.go:345-365 | PARTIAL | CODE→FIX | Config resolution works. Actual daemon loop (NATS connection, subscription, session-agent subprocess management) is a TODO placeholder. |
| spec-cli:130-135 | "daemon reads --host from flag or active-host symlink" | cmd/daemon.go:52-60 | IMPLEMENTED | | Flag > active-host symlink > context |
| spec-cli:137-139 | "Context file: ~/.mclaude/context.json stores userSlug, projectSlug, hostSlug, and server defaults" | context/context.go:36-48 | IMPLEMENTED | | All four fields present |
| spec-cli:139 | "Default server URL: https://api.mclaude.internal" | context/context.go:52 | IMPLEMENTED | | DefaultServerURL constant matches |
| spec-cli:139 | "--server flags override context.Server" | context/context.go:56-62 | IMPLEMENTED | | ResolveServerURL priority: override > context > default |
| spec-cli:140 | "Context file path overridable via MCLAUDE_CONTEXT_FILE env var" | context/context.go:25-28 | IMPLEMENTED | | Checks env var first |
| spec-cli:140 | "If file does not exist, all fields default to empty" | context/context.go:69-72 | IMPLEMENTED | | Returns empty Context on os.IsNotExist |
| spec-cli:142-145 | "Wire protocol: JSONL on unix socket. Session agent publishes canonical SessionEvent types" | repl/repl.go:60-80, events/types.go | PARTIAL | SPEC→FIX | The code currently parses Claude Code's native stream-json types (system, stream_event, assistant, user, control_request, tool_progress, result), NOT the canonical SessionEvent types defined in ADR-0005. The spec says "the unix socket exposes these same events to the CLI" but the canonical event schema (ADR-0005) is draft. The code correctly handles the actual wire format. Spec should clarify that the current wire format is Claude Code's native stream-json, not the canonical schema. |
| spec-cli:147-159 | "Canonical SessionEvent types table (init, state_change, text_delta, thinking_delta, message, tool_call, tool_progress, tool_result, permission, turn_complete, error, backend_specific)" | events/types.go | PARTIAL | SPEC→FIX | The code handles Claude Code's native event types (system, stream_event, assistant, user, control_request, tool_progress, result) which map to the canonical types but are not identical. This is correct for the current implementation where the session agent exposes raw Claude Code events. The spec conflates the canonical schema (ADR-0005 draft) with the current wire format. |
| spec-cli:161-173 | "Claude Code stream-json mapping table" | events/parse.go, events/types.go | IMPLEMENTED | | The code correctly parses all Claude Code native types listed in the mapping table |
| spec-cli:175-178 | "Outbound messages: SessionInput typed commands — message, skill_invoke, permission_response" | repl/repl.go:132-171 | PARTIAL | SPEC→FIX | Code sends `user` type messages and `control_response` type messages. Does not implement `skill_invoke` input type. The current wire format uses Claude Code's native input types, not the canonical SessionInput schema. Spec should reflect the actual wire format (Claude Code native). |
| spec-cli:180-185 | "Accumulator: Events rendered — text_delta (streaming text), message (complete messages with content blocks), tool_call / tool_result, permission" | events/accumulator.go | IMPLEMENTED | | Accumulator renders stream_event (text_delta equivalent), assistant (message), tool_use/tool_result, control_request (permission). Maps to spec through Claude Code native types. |
| spec-cli:186 | "Events used for state tracking: state_change, turn_complete, init" | events/accumulator.go:88-94 | IMPLEMENTED | | handleSystem handles init and state_change; handleResult handles turn_complete equivalent |
| spec-cli:187 | "Events silently discarded: thinking_delta, backend_specific" | events/accumulator.go:83-95 | IMPLEMENTED | | No case for thinking or backend_specific types; they fall through the switch silently |
| spec-cli:189 | "When a message event carries a non-null parentToolUseId, the resulting turn is nested under the parent tool_call's agent turn" | events/types.go:29, events/accumulator.go | GAP | CODE→FIX | Event struct has ParentToolUseID field but the accumulator does not use it for nesting. All turns are appended linearly regardless of parentToolUseId. |
| spec-cli:191 | "On clear event: accumulator resets all turns to empty (no divider)" | events/accumulator.go | GAP | CODE→FIX | No handling for a `clear` event type. The accumulator handles `compact_boundary` but not `clear`. |
| spec-cli:193 | "On compact_boundary system event: accumulator resets all turns and renders a `--- context compacted ---` divider" | events/accumulator.go:92-94, renderer/renderer.go:61-62 | IMPLEMENTED | | Accumulator clears turns; renderer prints divider |
| spec-cli:195-196 | "No reconnection. If the unix socket drops, the CLI exits immediately" | repl/repl.go:103-115 | IMPLEMENTED | | When scanner fails (socket closed), goroutine exits; REPL returns error |
| spec-cli:198-200 | "NATS connection: JWT + NKey seed from ~/.mclaude/auth.json" | cmd/auth.go, cmd/import.go:241-246 | IMPLEMENTED | | AuthCredentials struct and LoadAuth function |
| spec-cli:201-204 | "NATS request/reply for slug checks, import.request, import.confirm" | cmd/import.go | PARTIAL | CODE→FIX | Subject strings are printed in output but NATS connection is stubbed |
| spec-cli:206-211 | "Dependencies: session agent unix socket, NATS, context.json, auth.json, mclaude-common (slug, subj), zerolog" | go.mod (implied), imports across files | IMPLEMENTED | | All dependencies used: net.Dial unix, context pkg, auth pkg, mclaude.io/common/pkg/slug, mclaude.io/common/pkg/subj, zerolog |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| main.go:1-35 | INFRA | Package doc, imports, constants |
| main.go:37-39 | INFRA | main() entry point |
| main.go:43-76 | INFRA | run() dispatch switch for subcommands |
| main.go:78-116 | INFRA | printUsage() help text |
| main.go:118-170 | INFRA | runAttach() — spec'd attach command implementation |
| main.go:172-186 | INFRA | runLogin() — spec'd login command delegation |
| main.go:188-205 | INFRA | runImport() — spec'd import command delegation |
| main.go:207-234 | INFRA | runSession() + runSessionList() — spec'd session command |
| main.go:236-291 | INFRA | runHost*() functions — spec'd host subcommands |
| main.go:293-342 | INFRA | runCluster*() functions — spec'd cluster subcommands |
| main.go:344-365 | INFRA | runDaemon() — spec'd daemon command |
| events/types.go | INFRA | Event types for spec'd event parsing |
| events/parse.go | INFRA | Parse + helper methods for spec'd event types |
| events/accumulator.go | INFRA | Accumulator for spec'd conversation model |
| repl/repl.go | INFRA | REPL loop for spec'd attach behavior |
| context/context.go | INFRA | Context file management for spec'd context.json |
| renderer/renderer.go | INFRA | Terminal rendering for spec'd event display |
| cmd/login.go | INFRA | Login command for spec'd device-code flow |
| cmd/auth.go | INFRA | Auth credential management for spec'd auth.json |
| cmd/session.go | INFRA | Session list command for spec'd session list |
| cmd/import.go | INFRA | Import command for spec'd import flow |
| cmd/host.go | INFRA | Host subcommands for spec'd host register/list/use/rm |
| cmd/host.go:272-310 | INFRA | WriteHostCredentials helper for host registration completion |
| cmd/cluster.go | INFRA | Cluster subcommands for spec'd cluster register/grant |
| cmd/daemon.go | INFRA | Daemon config resolution for spec'd daemon command |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| spec-cli:13-17 | attach: REPL sends messages and handles permissions | repl_test.go: TestReplSimpleMessage, TestReplToolUsePermissionApprove, TestReplToolUsePermissionDeny, TestReplUserMessageFormat | (none) | UNIT_ONLY | Component tests using mock unix socket server |
| spec-cli:13-17 | attach: streaming text rendered | repl_test.go: TestReplStreamingTextAccumulated | (none) | UNIT_ONLY | |
| spec-cli:19-22 | attach: --socket, --log-machine, --log-level flags | monitoring_test.go: TestLogMachineFlag, TestLogLevelFlag, TestExitCodeNoSocket | (none) | UNIT_ONLY | |
| spec-cli:26-34 | session list: resolves slugs, validates, prints KV prefix | cmd/session_test.go: TestSessionListContextDefaults, TestSessionListProjectFlagOverride, TestSessionListAtPrefixProjectFlag, TestSessionListInvalidProjectSlug, etc. | (none) | UNIT_ONLY | 10+ test cases covering context defaults, flag overrides, @pslug, validation, missing slugs |
| spec-cli:36-53 | login: device-code flow | cmd/login_test.go: TestLoginDeviceCodeFlow, TestLoginNKeyNotSentToServer, TestLoginAuthFileModeIs0600, TestLoginServerError, TestLoginContextUpdated, TestLoginDisplaysVerificationURL | (none) | UNIT_ONLY | Tests use httptest.NewServer to simulate control-plane |
| spec-cli:63-90 | import: CWD encoding | cmd/import_test.go: TestEncodeCWDBasic, TestEncodeCWDMatchesClaude | (none) | UNIT_ONLY | |
| spec-cli:63-90 | import: session discovery | cmd/import_test.go: TestDiscoverSessionsFindsJSONL, TestDiscoverSessionsMissingDir, TestDiscoverSessionsListsAvailableOnMissing | (none) | UNIT_ONLY | |
| spec-cli:63-90 | import: archive building | cmd/import_test.go: TestBuildArchiveCreatesValidTarGz, TestBuildArchiveMetadataJSON | (none) | UNIT_ONLY | |
| spec-cli:63-90 | import: auth requirement | cmd/import_test.go: TestRunImportMissingAuth, TestRunImportMissingHost, TestRunImportCWDEncoding | (none) | UNIT_ONLY | |
| spec-cli:137-140 | context.json: load/save, slug validation | context/context_test.go: 20+ tests covering Load, Save, Validate*, Parse*, DefaultPath, ResolveServerURL | (none) | UNIT_ONLY | |
| spec-cli:180-193 | accumulator behavior | events/accumulator_test.go: TestAccumulatorSystemInit, TestAccumulatorStateChanged, TestAccumulatorStreamingText, TestAccumulatorAssistantFinalises, TestAccumulatorToolUseAndResult, TestAccumulatorControlRequest, TestAccumulatorControlDeny, TestAccumulatorToolProgress, TestAccumulatorCompactBoundaryResets, TestAccumulatorResultUsageAccumulates | (none) | UNIT_ONLY | |
| spec-cli:142-145 | event parsing | events/parse_test.go: TestParseSystemInit, TestParseSystemStateChanged, TestParseStreamEvent, TestParseAssistantMessage, TestParseControlRequest, etc. | (none) | UNIT_ONLY | |
| spec-cli:13-17 | renderer output | renderer/renderer_test.go: 14 tests covering all event type rendering | (none) | UNIT_ONLY | |
| spec-cli:198-200 | auth.json save/load | cmd/auth_test.go: TestSaveAuthCreatesFile, TestSaveAuthWritesValidJSON, TestLoadAuthMissingFile, TestLoadAuthRoundtrip, TestLoadAuthMissingJWT | (none) | UNIT_ONLY | |
| spec-cli:57 | JWT refresh before import | (none) | (none) | UNTESTED | JWT refresh logic not implemented |
| spec-cli:189 | parentToolUseId nesting in accumulator | (none) | (none) | UNTESTED | Not implemented |
| spec-cli:191 | clear event handling | (none) | (none) | UNTESTED | Not implemented |
| spec-cli:87-98 | host register device-code HTTP flow | (none) | (none) | UNTESTED | Stubbed — no HTTP calls, no tests |
| spec-cli:100-102 | host list via HTTP API | (none) | (none) | UNTESTED | Stubbed |
| spec-cli:107-108 | host rm DELETE HTTP call | (none) | (none) | UNTESTED | Stubbed |
| spec-cli:110-122 | cluster register POST /admin/clusters | (none) | (none) | UNTESTED | Stubbed |
| spec-cli:126-128 | cluster grant POST /admin/clusters/{slug}/grants | (none) | (none) | UNTESTED | Stubbed |
| spec-cli:130-135 | daemon: NATS connection, subscription, session-agent management | (none) | (none) | UNTESTED | Stubbed |

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none for mclaude-cli) | — | — | No bugs in `.agent/bugs/` reference mclaude-cli as a component |

### Summary

- Implemented: 38
- Gap: 3
- Partial: 15
- Infra: 24
- Unspec'd: 0
- Dead: 0
- Tested (unit only): 14
- Unit only: 14
- E2E only: 0
- Untested: 8
- Bugs fixed: 0
- Bugs open: 0

**NOT CLEAN — 3 gaps, 15 partial implementations, 8 untested spec lines.**

Key findings:

**GAPs (CODE→FIX):**
1. `parentToolUseId` nesting: Accumulator ignores `parentToolUseId` field — all turns appended linearly instead of nesting under parent tool_call.
2. `clear` event: No handling for `clear` event type in accumulator. Only `compact_boundary` is handled.
3. Session list spec missing `--host` flag: Code requires host slug but spec doesn't list it → SPEC→FIX direction.

**PARTIAL (CODE→FIX) — Stubbed network operations:**
4. NATS connection for import operations — credentials loaded but NATS request/reply not wired.
5. Import flow steps 5-6 (slug check, collision prompt) — stub only.
6. Import flow steps 8-11 (pre-signed URL, S3 upload, confirm, ack) — stub only.
7. Host register HTTP device-code flow — NKey generation works, HTTP calls stubbed.
8. Host list HTTP API — lists local dirs instead of calling API.
9. Host rm DELETE call — local cleanup works, HTTP call stubbed.
10. Cluster register POST — validation works, HTTP call stubbed.
11. Cluster grant POST — validation works, HTTP call stubbed.
12. Daemon NATS loop — config resolution works, actual daemon loop is TODO.
13. JWT refresh before import — not implemented at all.

**PARTIAL (SPEC→FIX) — Wire protocol mismatch:**
14. Wire protocol: Code correctly handles Claude Code's native stream-json format, but spec describes the canonical SessionEvent schema (from draft ADR-0005). Spec should clarify the current wire format.
15. Outbound messages: Code sends Claude Code native types (`user`, `control_response`), not canonical `SessionInput` types. Spec should reflect actual format.
