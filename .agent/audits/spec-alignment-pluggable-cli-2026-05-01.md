## Run: 2026-05-01T00:00:00Z (Round 3)

ADR: `docs/adr-0005-pluggable-cli.md` (Status: draft)

Specs checked:
- `docs/spec-state-schema.md`
- `docs/spec-nats-payload-schema.md`
- `docs/spec-nats-activity.md`
- `docs/mclaude-session-agent/spec-session-agent.md`
- `docs/mclaude-cli/spec-cli.md`

Note: `docs/spec-session-agent.md` (root-level) does not exist; the correct path is `docs/mclaude-session-agent/spec-session-agent.md`.

### Phase 1 — ADR → Spec (forward pass)

| # | ADR (section) | ADR text | Spec location | Verdict | Direction | Notes |
|---|---------------|----------|---------------|---------|-----------|-------|
| 1 | CLIBackend enum | `claude_code`, `droid`, `devin_acp`, `gemini`, `generic_terminal` | spec-state-schema.md: `"backend": "claude_code \| droid \| devin_acp \| gemini \| generic_terminal (default: claude_code)"` | REFLECTED | — | Also in payload schema KV section and session-agent spec |
| 2 | CLICapabilities struct | Boolean feature flags: hasThinking, hasSubagents, hasSkills, hasPlanMode, hasMissions, hasEventStream, hasSessionResume, toolIcons, thinkingLabel, modelOptions, permissionModes | spec-state-schema.md: `capabilities` object lists all fields | REFLECTED | — | Payload schema and session-agent spec also have complete struct |
| 3 | InitEvent fields | `backend`, `model`, `tools[]`, `skills[]`, `agents[]`, `capabilities` | spec-nats-payload-schema.md: init event example shows all six fields | REFLECTED | — | spec-nats-activity.md also shows complete init event |
| 4 | Canonical SessionEvent — 12 event types | init, state_change, text_delta, thinking_delta, message, tool_call, tool_progress, tool_result, permission, turn_complete, error, backend_specific | spec-nats-payload-schema.md: events section lists all 12 types with field descriptions | REFLECTED | — | spec-cli.md lists all 12 types; session-agent spec references canonical schema |
| 5 | SessionStatus enum | pending, running, paused, requires_action, completed, stopped, cancelled, needs_spec_fix, failed, error | spec-state-schema.md: Status enum lists all 10 values | REFLECTED | — | Payload schema KV section also lists all values |
| 6 | StateChangeEvent | `state: "idle" \| "running" \| "requires_action"` | spec-nats-payload-schema.md: state_change event with three states | REFLECTED | — | |
| 7 | TextDeltaEvent | messageId, blockIndex, text, encoding ("plain"/"base64") | spec-nats-payload-schema.md: text_delta with all four fields documented | REFLECTED | — | encoding semantics explained |
| 8 | ThinkingDeltaEvent | messageId, blockIndex, text | spec-nats-payload-schema.md: thinking_delta section | REFLECTED | — | |
| 9 | MessageEvent | messageId, role, content[], parentToolUseId | spec-nats-payload-schema.md: message event with all fields | REFLECTED | — | |
| 10 | ToolCallEvent | toolUseId, toolName, input, messageId | spec-nats-payload-schema.md: tool_call event | REFLECTED | — | |
| 11 | ToolProgressEvent | toolUseId, toolName, elapsedSecs, content | spec-nats-payload-schema.md: tool_progress event | REFLECTED | — | |
| 12 | ToolResultEvent | toolUseId, toolName, content, isError | spec-nats-payload-schema.md: tool_result event | REFLECTED | — | |
| 13 | PermissionEvent | requestId, toolName, toolInput, resolved, allowed | spec-nats-payload-schema.md: permission event | REFLECTED | — | |
| 14 | TurnCompleteEvent | inputTokens, outputTokens, costUsd, durationMs | spec-nats-payload-schema.md: turn_complete event | REFLECTED | — | |
| 15 | ErrorEvent | message, code, retryable | spec-nats-payload-schema.md: error event | REFLECTED | — | |
| 16 | BackendSpecific | `json.RawMessage` opaque payload | spec-nats-payload-schema.md: backend_specific event | REFLECTED | — | |
| 17 | Message Envelope | Every NATS message has `id` (ULID) + `ts` (Unix millis) | spec-nats-payload-schema.md: Design Principle #1 documents envelope | REFLECTED | — | |
| 18 | CLIDriver interface | Backend(), DisplayName(), Capabilities(), Launch(), Resume(), SendMessage(), SendPermissionResponse(), UpdateConfig(), Interrupt(), ReadEvents() | spec-session-agent.md: CLIDriver Interface section lists all methods | REFLECTED | — | |
| 19 | DriverRegistry | Maps CLIBackend enum to driver instances; default `claude_code` | spec-session-agent.md: DriverRegistry section with code example | REFLECTED | — | |
| 20 | Driver implementations | ClaudeCodeDriver, DroidDriver, DevinACPDriver, GenericTerminalDriver in `internal/drivers/` | spec-session-agent.md: Driver Implementations table lists all four | REFLECTED | — | |
| 21 | SessionInput types | message, skill_invoke, permission_response | spec-nats-payload-schema.md: sessions.{sslug}.input with all three types | REFLECTED | — | |
| 22 | AttachmentRef | id, filename, mimeType, sizeBytes | spec-nats-payload-schema.md: Common Types section | REFLECTED | — | |
| 23 | NATS subjects | sessions.create, .events, .input, .delete, .config, .control.interrupt, .control.restart, lifecycle.* | spec-state-schema.md and spec-nats-payload-schema.md: all subjects listed | REFLECTED | — | spec-nats-activity.md walkthrough covers all |
| 24 | KV session state — backend field | `backend: CLIBackend` defaults to `claude_code` | spec-state-schema.md: `"backend"` field documented with all enum values | REFLECTED | — | |
| 25 | KV session state — capabilities struct | `CLICapabilities` boolean flags under `capabilities` key | spec-state-schema.md: `capabilities` object with all boolean fields | REFLECTED | — | |
| 26 | KV session state — tools/skills/agents top-level | Promoted from nested `capabilities.{tools,skills,agents}` to top-level | spec-state-schema.md: `"tools": ["string"], "skills": ["string"], "agents": ["string"]` as top-level | REFLECTED | — | Payload schema KV section also shows them as top-level |
| 27 | SessionConfig struct | model, permissionMode, systemPrompt (nullable patch semantics) | spec-nats-payload-schema.md: sessions.{sslug}.config with all three fields | REFLECTED | — | |
| 28 | ContentBlock types | text, tool_use, tool_result, image, attachment_ref | spec-nats-payload-schema.md: Content block types table | REFLECTED | — | |
| 29 | GenericTerminalDriver PTY | hasEventStream: false, heuristic idle detection, PTY via creack/pty, encoding plain/base64 | spec-session-agent.md: GenericTerminalDriver row describes all behaviors | REFLECTED | — | |
| 30 | Driver-agnostic core loop | `CLIDriver.ReadEvents(proc) → chan SessionEvent → NATS publish + side effects` | spec-session-agent.md: Event Routing section describes driver-agnostic pattern | REFLECTED | — | |
| 31 | sessions.create backend field | Optional, default `claude_code` | spec-nats-payload-schema.md: sessions.create table shows `backend` optional, default `claude_code` | REFLECTED | — | |
| 32 | lifecycle.started payload | Includes backend, capabilities from init event | spec-nats-payload-schema.md: lifecycle.started shows backend + capabilities fields | REFLECTED | — | |
| 33 | Claude Code stream-json → canonical mapping | 10-row mapping table | spec-cli.md: Claude Code stream-json mapping table reproduced | REFLECTED | — | |
| 34 | Skill invocation via /prefix | All backends handle `/skill-name` as user message | spec-nats-payload-schema.md: skill_invoke translation described | REFLECTED | — | Session-agent spec also covers input routing |

### Round 2 Gap Verification

| Gap | Status | Evidence |
|-----|--------|----------|
| Missing `tools`, `skills`, `agents` in payload schema KV section | **FIXED** | spec-nats-payload-schema.md KV_mclaude-sessions-{uslug} now shows `tools`, `skills`, `agents` as top-level fields with descriptions. Both interactive and quota-managed examples include them. |

### Summary

- Reflected: 34
- Gap: 0
- Partial: 0

**CLEAN — 34 ADR decisions reflected in specs, 0 gaps**
