## Run: 2026-05-01T00:00:00Z

**ADR**: `docs/adr-0005-pluggable-cli.md` (Status: draft)
**Specs evaluated**:
- `docs/spec-state-schema.md`
- `docs/spec-nats-payload-schema.md`
- `docs/spec-nats-activity.md`
- `docs/spec-nats-data-taxonomy.md`
- `docs/mclaude-session-agent/spec-session-agent.md`
- `docs/mclaude-cli/spec-cli.md`

### Phase 1 — ADR → Spec (forward pass)

| # | ADR (section) | ADR text | Spec location | Verdict | Direction | Notes |
|---|---------------|----------|---------------|---------|-----------|-------|
| 1 | CLIBackend enum | `CLIBackend` enum: `claude_code`, `droid`, `devin_acp`, `gemini`, `generic_terminal` | spec-nats-payload-schema: `sessions.create` payload has `backend` field with enum `claude_code`, `droid`, `devin_acp`, `generic_terminal`; KV session has `backend` field | PARTIAL | SPEC→FIX | `spec-nats-payload-schema.md` KV session entry lists `claude_code`, `droid`, `devin_acp`, `generic_terminal` but omits `gemini` from ADR-0005 enum. `spec-state-schema.md` has no `backend` field on session KV at all (uses old schema). |
| 2 | Canonical SessionEvent schema | `SessionEvent` struct with `id`, `ts`, `type`, `sessionId`, and 12 event type variants (init, state_change, text_delta, thinking_delta, message, tool_call, tool_progress, tool_result, permission, turn_complete, error, backend_specific) | spec-nats-payload-schema: `sessions.{sslug}.events` section lists all 10 event types (init through error) | PARTIAL | SPEC→FIX | `spec-nats-payload-schema.md` lists event types but omits `backend_specific` from the event type table. The ADR defines 12 EventType constants; the payload spec table shows only 10. |
| 3 | Message Envelope | Every NATS message carries `id` (ULID) + `ts` (Unix millis) envelope | spec-nats-payload-schema: "Every message has a standard envelope (id + ts). See ADR-0005." All example payloads include `id` and `ts`. | REFLECTED | — | Spec correctly references ADR-0005 and shows envelope in all examples. |
| 4 | SessionStatus enum | 10 status values: pending, running, paused, requires_action, completed, stopped, cancelled, needs_spec_fix, failed, error | spec-nats-payload-schema: KV session `status` enum lists all 10 values | REFLECTED | — | Payload schema matches. |
| 5 | SessionStatus enum vs spec-state-schema | Same 10 status values | spec-state-schema: `state` field (not `status`) with values `idle, running, requires_action, updating, restarting, failed, plan_mode, waiting_for_input, unknown` | GAP | SPEC→FIX | `spec-state-schema.md` still uses the old `state` field with old values. ADR-0005 renames to `status` with a new enum. The state-schema has not absorbed this change (acknowledged by spec-nats-activity divergence notice). |
| 6 | StateChangeEvent | `StateChangeEvent` carries `state`: `idle`, `running`, `requires_action` (fine-grained driver state for SPA) | spec-nats-payload-schema: `state_change` event shows `{"state": "running"}` with description "idle, running, requires_action" | REFLECTED | — | |
| 7 | TextDeltaEvent encoding field | `TextDeltaEvent.Encoding`: `"plain"` (default) or `"base64"` for GenericTerminalDriver PTY output | spec-nats-payload-schema: `text_delta` example shows `messageId`, `blockIndex`, `text` but no `encoding` field | GAP | SPEC→FIX | ADR-0005 defines the `encoding` field on `TextDeltaEvent` for GenericTerminalDriver PTY binary output. `spec-nats-payload-schema.md` does not mention `encoding` in the `text_delta` schema or examples. |
| 8 | InitEvent struct | `InitEvent` with `backend`, `model`, `tools`, `skills`, `agents`, `capabilities` (CLICapabilities) | spec-nats-payload-schema: `init` event example includes all fields | REFLECTED | — | |
| 9 | CLICapabilities struct | Boolean feature flags: `hasThinking`, `hasSubagents`, `hasSkills`, `hasPlanMode`, `hasMissions`, `hasEventStream`, `hasSessionResume`, plus `toolIcons`, `thinkingLabel`, `modelOptions`, `permissionModes` | spec-nats-payload-schema: `init` and `lifecycle.started` examples show capabilities struct with all boolean flags + `thinkingLabel`, `modelOptions`, `permissionModes` | PARTIAL | SPEC→FIX | `spec-nats-payload-schema.md` capabilities examples omit `toolIcons` map from ADR-0005's `CLICapabilities`. |
| 10 | ToolCallEvent fields | `ToolCallEvent` with `toolUseId`, `toolName`, `input`, `messageId` | spec-nats-payload-schema: `tool_call` shows `toolUseId`, `toolName`, `input`, `messageId` | REFLECTED | — | |
| 11 | ToolProgressEvent fields | `ToolProgressEvent` with `toolUseId`, `toolName`, `elapsedSecs`, `content` | spec-nats-payload-schema: `tool_progress` shows same 4 fields | REFLECTED | — | |
| 12 | ToolResultEvent fields | `ToolResultEvent` with `toolUseId`, `toolName`, `content`, `isError` | spec-nats-payload-schema: `tool_result` shows same 4 fields | REFLECTED | — | |
| 13 | PermissionEvent fields | `PermissionEvent` with `requestId`, `toolName`, `toolInput`, `resolved`, `allowed` | spec-nats-payload-schema: `permission` shows same 5 fields | REFLECTED | — | |
| 14 | TurnCompleteEvent fields | `TurnCompleteEvent` with `inputTokens`, `outputTokens`, `costUsd`, `durationMs` | spec-nats-payload-schema: `turn_complete` shows same 4 fields | REFLECTED | — | |
| 15 | ErrorEvent fields | `ErrorEvent` with `message`, `code`, `retryable` | spec-nats-payload-schema: `error` shows same 3 fields | REFLECTED | — | |
| 16 | SessionInput schema | `SessionInput` with types `message`, `skill_invoke`, `permission_response` and fields `id`, `ts`, `type`, `text`, `attachments`, `skillName`, `args`, `requestId`, `allowed`, `behavior` | spec-nats-payload-schema: `sessions.{sslug}.input` defines all 3 input types with matching fields | REFLECTED | — | |
| 17 | AttachmentRef type | `AttachmentRef` with `id`, `filename`, `mimeType`, `sizeBytes`, `s3Key` | spec-nats-payload-schema: Common Types shows `AttachmentRef` with `id`, `filename`, `mimeType`, `sizeBytes`; `sessions.input` message example includes same | PARTIAL | SPEC→FIX | ADR-0005 `AttachmentRef` has an `s3Key` field for agent-generated attachments. `spec-nats-payload-schema.md` omits `s3Key` from the Common Types definition. |
| 18 | ContentBlock type | Canonical `ContentBlock` with types `text`, `tool_use`, `tool_result`, `image`, `attachment_ref` and distinct fields from CLI-layer ContentBlock | spec-nats-payload-schema: `message` event shows content block types `text`, `tool_use`, `tool_result`, `image`, `attachment_ref` | REFLECTED | — | Types listed in payload spec message event. |
| 19 | NATS Subject Structure | Per-session subjects: `sessions.{sslug}.events`, `.input`, `.delete`, `.config`, `.control.interrupt`, `.control.restart`, `.lifecycle.started`, `.lifecycle.stopped`, `.lifecycle.error` | spec-nats-payload-schema: All these subjects documented; spec-nats-data-taxonomy: Resource hierarchy shows all session suffixes | REFLECTED | — | |
| 20 | sessions.create payload — backend field | `sessions.create` includes `backend` field (defaults to `claude_code`) | spec-nats-payload-schema: `sessions.create` shows `backend` field with description "CLI backend (default: claude_code)" | REFLECTED | — | |
| 21 | sessions.{sslug}.config subject | Patch semantics config update: `model`, `permissionMode`, `systemPrompt` (nullable) + `CLIDriver.UpdateConfig()` | spec-nats-payload-schema: `sessions.{sslug}.config` shows patch semantics with `model`, `permissionMode`, `systemPrompt` | REFLECTED | — | |
| 22 | lifecycle.started payload | Includes `backend` and `capabilities` (CLICapabilities) | spec-nats-payload-schema: `sessions.{sslug}.lifecycle.started` shows `backend`, `capabilities` | REFLECTED | — | |
| 23 | lifecycle.stopped payload | Includes `reason` enum and `exitCode` | spec-nats-payload-schema: `sessions.{sslug}.lifecycle.stopped` shows `reason` enum (user_deleted, completed, crashed, evicted, host_offline) and `exitCode` | REFLECTED | — | |
| 24 | KV session entry — backend field | Add `backend: CLIBackend` to session KV state | spec-nats-payload-schema: KV session shows `backend` field | REFLECTED | — | Present in payload spec. |
| 25 | KV session entry — backend field in state-schema | Same: `backend` field on session KV | spec-state-schema: `mclaude-sessions` KV value schema | GAP | SPEC→FIX | `spec-state-schema.md` session KV JSON schema has no `backend` field. |
| 26 | KV session entry — capabilities migration | `capabilities` becomes `CLICapabilities` (boolean flags only); `tools`, `skills`, `agents` promoted to top-level | spec-state-schema: `capabilities: {skills, tools, agents}` (old nested shape) | GAP | SPEC→FIX | `spec-state-schema.md` still has the old `capabilities` struct with nested `skills/tools/agents`. ADR-0005 replaces this with boolean `CLICapabilities` and top-level `tools`/`skills`/`agents`. |
| 27 | KV session — per-user bucket | KV bucket `KV_mclaude-sessions-{uslug}` with key `hosts.{hslug}.projects.{pslug}.sessions.{sslug}` | spec-nats-payload-schema: Uses `KV_mclaude-sessions-{uslug}` with per-user key format; spec-nats-data-taxonomy: same; spec-nats-activity: same | REFLECTED | — | ADR-0005 notes migration; payload spec, taxonomy, and activity specs use the target format. |
| 28 | KV session — per-user bucket in state-schema | Same per-user bucket | spec-state-schema: Uses shared `mclaude-sessions` bucket with key `{uslug}.{hslug}.{pslug}.{sslug}` | GAP | SPEC→FIX | `spec-state-schema.md` documents the old shared bucket and old key format (acknowledged in spec-nats-activity divergence notice). This is an ADR-0054 decision, not strictly ADR-0005, but ADR-0005 references it. |
| 29 | JetStream — per-user session stream | `MCLAUDE_SESSIONS_{uslug}` captures `mclaude.users.{uslug}.hosts.*.projects.*.sessions.>` | spec-nats-data-taxonomy: `MCLAUDE_SESSIONS_{uslug}` stream documented; spec-nats-activity: same | REFLECTED | — | Present in taxonomy and activity specs. |
| 30 | JetStream — per-user stream in state-schema | Same per-user stream | spec-state-schema: Documents shared streams `MCLAUDE_EVENTS`, `MCLAUDE_API`, `MCLAUDE_LIFECYCLE` | GAP | SPEC→FIX | `spec-state-schema.md` still documents the old shared stream architecture. ADR-0005 + ADR-0054 target is per-user `MCLAUDE_SESSIONS_{uslug}`. |
| 31 | CLIDriver interface | `CLIDriver` with methods: `Backend()`, `DisplayName()`, `Capabilities()`, `Launch()`, `Resume()`, `SendMessage()`, `SendPermissionResponse()`, `UpdateConfig()`, `Interrupt()`, `ReadEvents()` | spec-session-agent: Describes session agent as Claude Code process supervisor; no mention of CLIDriver interface or driver pattern | GAP | SPEC→FIX | `spec-session-agent.md` describes the agent as tightly coupled to Claude Code (e.g., "spawns Claude Code child processes", "bridges stream-json I/O"). It does not describe the driver/adapter pattern, `CLIDriver` interface, or multi-backend support from ADR-0005. |
| 32 | Driver-agnostic session loop | Session agent loop reads from `CLIDriver.ReadEvents()` channel, translates to NATS publishes | spec-session-agent: Event routing section describes NDJSON stdout parsing specific to Claude Code | GAP | SPEC→FIX | `spec-session-agent.md` "Event Routing" section describes reading NDJSON from Claude Code stdout directly. ADR-0005 replaces this with a driver-agnostic `ReadEvents()` channel pattern. |
| 33 | GenericTerminalDriver | Fallback PTY-based driver: `hasEventStream: false`, heuristic idle detection, `creack/pty`, encoding field on text_delta | spec-session-agent: "Terminal Sessions" describes PTY terminal feature, but not as a CLIDriver impl; not as a backend type | GAP | SPEC→FIX | `spec-session-agent.md` has terminal sessions but as a separate feature (shell PTY for users), not as a GenericTerminalDriver backend type. The ADR's GenericTerminalDriver is a different concept. |
| 34 | sessions.create — scheduled session quota fields top-level | Quota fields (`prompt`, `branchSlug`, `softThreshold`, etc.) are top-level in sessions.create, not nested under `quotaMonitor` | spec-nats-payload-schema: sessions.create shows flat quota fields | REFLECTED | — | Payload spec matches ADR-0005 + ADR-0044 flat format. |
| 35 | Session agent — driver registration | `DriverRegistry` for registering/looking up drivers by backend type | spec-session-agent: No mention of DriverRegistry | GAP | SPEC→FIX | `spec-session-agent.md` has no concept of a driver registry. Follows from gap #31. |
| 36 | Critical files — package structure | `internal/drivers/` package for driver implementations; interface types in top-level package | spec-session-agent: No mention of `internal/drivers/` or package restructuring | GAP | SPEC→FIX | `spec-session-agent.md` does not describe the `internal/drivers/` package layout from ADR-0005. |
| 37 | BackendSpecific events | `type: "backend_specific"` with `json.RawMessage` for backend-unique features (e.g. Droid missions) | spec-nats-payload-schema: Does not list `backend_specific` in the event type table | GAP | SPEC→FIX | ADR-0005 defines `EventBackendSpecific` as one of 12 event types. `spec-nats-payload-schema.md` event table lists only 10 types, omitting `backend_specific`. |
| 38 | BlockIndex synthesis rules | Per-backend rules for `blockIndex`: Claude Code passes through natively, Droid/Devin synthesize via per-message counter, GenericTerminal always 0 | spec-nats-payload-schema: `text_delta` and `thinking_delta` show `blockIndex` field but no synthesis rules | PARTIAL | SPEC→FIX | `spec-nats-payload-schema.md` shows the field but does not document the per-backend synthesis semantics from ADR-0005. |
| 39 | MessageEvent.ParentToolUseID | `parentToolUseId` on `MessageEvent` for subagent nesting | spec-nats-payload-schema: `message` event shows `parentToolUseId` field | REFLECTED | — | |
| 40 | Process struct | `Process` struct with `Cmd`, `Stdin`, `Stdout`, `Stderr`, `PID` | spec-session-agent: No `Process` struct described | GAP | SPEC→FIX | Follows from gap #31 — the session-agent spec doesn't describe the driver abstraction layer including `Process`, `LaunchOptions`, etc. |
| 41 | UserMessage + ResolvedAttachment | `UserMessage` with `Text` + `Attachments []ResolvedAttachment`; agent resolves S3 refs before forwarding to driver | spec-nats-payload-schema: Input message shows `attachments` as `AttachmentRef[]`; notes "Agent resolves to pre-signed download URLs before passing to CLI backend" | REFLECTED | — | The resolution step is documented. |
| 42 | SessionConfig patch semantics | `SessionConfig` with nullable `Model`, `PermissionMode`, `SystemPrompt` for live config updates | spec-nats-payload-schema: `sessions.{sslug}.config` describes patch semantics with nullable fields | REFLECTED | — | |
| 43 | CLI spec — multi-backend awareness | CLI should support multi-backend (currently describes only Claude Code unix socket) | spec-cli: "attaches to running session agents over unix sockets" — describes Claude Code-specific stream-json wire protocol | GAP | SPEC→FIX | `spec-cli.md` describes the attach REPL wire protocol as Claude Code stream-json types (`system`, `stream_event`, `assistant`, `user`, `control_request`, etc.). ADR-0005's canonical event schema replaces this. The CLI would need to understand canonical `SessionEvent` types instead. |
| 44 | spec-nats-activity — tool_call field names | ADR-0005 uses `toolUseId`, `toolName`, `input` | spec-nats-activity: section 9e uses `toolCallId`, `tool`, `input` for tool_call; `toolCallId`, `text` for tool_progress; `toolCallId`, `status`, `contentBlocks` for tool_result | GAP | SPEC→FIX | `spec-nats-activity.md` section 9e uses field names that diverge from ADR-0005's canonical schema: `toolCallId` vs `toolUseId`, `tool` vs `toolName`, `status`+`contentBlocks` vs `content`+`isError`. |
| 45 | spec-nats-activity — permission field names | ADR-0005 uses `requestId`, `resolved`, `allowed` | spec-nats-activity: section 9e permission uses `permissionId`, `resolved`, `granted` | GAP | SPEC→FIX | `spec-nats-activity.md` uses `permissionId` instead of `requestId`, and `granted` instead of `allowed`. These diverge from ADR-0005's `PermissionEvent` struct. |
| 46 | spec-nats-activity — permission_response input field names | ADR-0005 `SessionInput`: `requestId`, `allowed`, `behavior` | spec-nats-activity: section 9e-input permission_response uses `permissionId`, `granted` (no `behavior`) | GAP | SPEC→FIX | `spec-nats-activity.md` permission_response input uses `permissionId`/`granted` instead of ADR-0005's `requestId`/`allowed`/`behavior`. |
| 47 | KV projects — per-user bucket with backend field | `KV_mclaude-projects-{uslug}` with `backend` field for default CLI backend | spec-nats-payload-schema: `KV_mclaude-projects-{uslug}` includes `backend`, `defaultModel`, `defaultPermissionMode` | REFLECTED | — | |
| 48 | KV projects — per-user bucket in state-schema | Same per-user projects bucket | spec-state-schema: Uses shared `mclaude-projects` bucket with UUID key format | GAP | SPEC→FIX | `spec-state-schema.md` documents shared `mclaude-projects` with `{userId}.{projectId}` key format (noted as deferred per ADR-0050). Same class of gap as #28 — state-schema not updated. |
| 49 | ULID libraries | Go: `oklog/ulid`, TypeScript: `ulid` | All specs | N/A | — | Implementation detail, not a spec-level decision. Skipped. |
| 50 | Interrupt/restart/config on separate subjects | "Interrupt, restart, and config are NOT InputTypes. They arrive on separate NATS subjects" | spec-nats-payload-schema: Separate sections for `control.interrupt`, `control.restart`, `config` | REFLECTED | — | |
| 51 | sessions.create reply | Reply includes `{id, claudeSessionID}` for dispatcher fallback | spec-nats-payload-schema: sessions.create reply shows `{id, claudeSessionID}` | REFLECTED | — | |
| 52 | Session agent spec — multi-backend description | ADR-0005 redesigns agent to support multiple CLI backends through driver/adapter | spec-session-agent: Role section says "manages multiple concurrent Claude Code sessions" — no mention of multi-backend | GAP | SPEC→FIX | `spec-session-agent.md` Role/Overview is Claude Code-specific. ADR-0005 makes it backend-agnostic. The spec should describe the driver pattern and mention support for multiple backends. |

### Summary

- **Reflected**: 27
- **Gap**: 16
- **Partial**: 4
- **Skipped (N/A)**: 1

### Gap Analysis

The gaps fall into three clusters:

**Cluster 1: `spec-state-schema.md` not updated (6 gaps — #5, #25, #26, #28, #30, #48)**

`spec-state-schema.md` still describes the pre-ADR-0005/ADR-0054 architecture: shared KV buckets (`mclaude-sessions`, `mclaude-projects`), old key format (`{uslug}.{hslug}.{pslug}.{sslug}`), shared JetStream streams (`MCLAUDE_EVENTS`, `MCLAUDE_API`, `MCLAUDE_LIFECYCLE`), the old `state` field (not `status`), old state enum, old nested `capabilities` struct, and no `backend` field. The `spec-nats-activity.md` divergence notice acknowledges this explicitly. These are all SPEC→FIX: the state schema must absorb the ADR-0005 + ADR-0054 decisions.

**Cluster 2: `spec-session-agent.md` not updated (6 gaps — #31, #32, #33, #35, #36, #52)**

The session-agent spec describes a Claude Code-specific process supervisor. ADR-0005 introduces the `CLIDriver` interface, `DriverRegistry`, `internal/drivers/` package structure, driver-agnostic event loop, and `GenericTerminalDriver`. None of this is in the spec. SPEC→FIX: the session-agent spec needs a major update to describe the pluggable driver architecture.

**Cluster 3: `spec-nats-activity.md` field name divergences (3 gaps — #44, #45, #46)**

`spec-nats-activity.md` section 9e uses field names (`toolCallId`, `tool`, `status`, `contentBlocks`, `permissionId`, `granted`) that do not match ADR-0005's canonical event schema (`toolUseId`, `toolName`, `content`, `isError`, `requestId`, `allowed`). SPEC→FIX: the activity spec must use the canonical field names.

**Cluster 4: Minor schema gaps in `spec-nats-payload-schema.md` (3 gaps + 2 partials — #2/#37, #7, #1, #9, #17, #38)**

- `backend_specific` event type missing from event table (#2, #37)
- `encoding` field missing from `text_delta` (#7)
- `gemini` missing from `CLIBackend` enum in KV examples (#1)
- `toolIcons` missing from `CLICapabilities` examples (#9)
- `s3Key` missing from `AttachmentRef` common type (#17)
- `blockIndex` synthesis rules not documented (#38)

**Cluster 5: `spec-cli.md` not updated (#43)**

The CLI spec describes a Claude Code-specific stream-json wire protocol. ADR-0005 replaces this with the canonical `SessionEvent` schema. SPEC→FIX: if the CLI will consume canonical events (vs raw stream-json), the spec needs updating.
