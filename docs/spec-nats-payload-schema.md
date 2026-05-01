# NATS Payload Schema Specification

Defines the exact JSON payload for every NATS subject in the system. Each subject has one payload schema. No ambiguity — a developer implementing a publisher or subscriber can look up the subject and know exactly what JSON to produce or expect.

Depends on: `adr-0054-nats-jetstream-permission-tightening.md` (subjects, permissions), `adr-0005-pluggable-cli.md` (canonical event schema, driver interface), `spec-nats-data-taxonomy.md` (naming conventions), `spec-nats-activity.md` (runtime walkthrough).

---

## Design Principles

1. **Every message has a standard envelope** (`id` + `ts`). See ADR-0005 "Message Envelope" for the decision rationale (ULID format, publisher-side generation, CQRS correlation, deduplication).

2. **Session events use the canonical `SessionEvent` schema from ADR-0005.** The driver layer translates backend-native protocols into this schema. Everything downstream is backend-agnostic.

3. **Binary data never travels through NATS.** Attachments use S3 with pre-signed URLs (ADR-0053). NATS messages carry `AttachmentRef` references. See ADR-0005 "Session Input Schema" and "Attachment content block" for the type definitions.

4. **Input messages are structured commands, not raw text.** See ADR-0005 `SessionInput` for the type definitions (`message`, `skill_invoke`, `permission_response`).

---

## Common Types

Defined in ADR-0005, referenced here for payload examples:

```go
// AttachmentRef — S3 object reference carried in NATS messages (never raw bytes)
type AttachmentRef struct {
    ID       string `json:"id"`        // opaque reference (e.g., "att-001")
    Filename string `json:"filename"`  // original filename
    MimeType string `json:"mimeType"`  // e.g., "image/png", "application/pdf"
    SizeBytes int64  `json:"sizeBytes"` // file size
}
```

Attachment lifecycle:
1. Client requests pre-signed upload URL from CP: `POST /api/attachments/upload-url`
2. Client uploads directly to S3 using the signed URL
3. Client confirms upload: `POST /api/attachments/{id}/confirm`
4. NATS messages reference the attachment by `id`
5. Consumers request pre-signed download URL from CP: `GET /api/attachments/{id}`
6. Consumer downloads directly from S3

---

## Session Stream Subjects

All captured by `MCLAUDE_SESSIONS_{uslug}`.
Full subject prefix: `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.`

### `sessions.create`

**Publisher:** SPA/CLI | **Subscriber:** Agent (via stream consumer)

Interactive sessions (SPA/CLI):
```json
{
    "id": "01JTRK8M4G3XQVN5P2WYZ7ABCD",
    "ts": 1714470060000,
    "sessionSlug": "sess-001",
    "backend": "claude_code",
    "model": "sonnet",
    "systemPrompt": "...",
    "permissionMode": "managed"
}
```

Scheduled sessions (per ADR-0044 — all quota fields are top-level, not nested):
```json
{
    "id": "01JTRK8M4G3XQVN5P2WYZ7ABCD",
    "ts": 1714470060000,
    "sessionSlug": "sched-001",
    "backend": "claude_code",
    "prompt": "<free text>",
    "branchSlug": "refactor-auth-middleware",
    "softThreshold": 75,
    "hardHeadroomTokens": 50000,
    "autoContinue": true,
    "permPolicy": "strict-allowlist",
    "allowedTools": ["Read", "Write", "Edit", "Glob", "Grep", "Bash"],
    "resumePrompt": "",
    "resumeClaudeSessionID": ""
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `sessionSlug` | yes | Slug for the new session |
| `backend` | no | CLI backend (default: `claude_code`). See ADR-0005 `CLIBackend` enum. |
| `model` | no | Model override. If omitted, uses project/backend default. |
| `systemPrompt` | no | System prompt override. |
| `permissionMode` | no | `auto`, `managed`, `allowlist`. Default: `managed`. For scheduled sessions, use `permPolicy` + `allowedTools` instead. |
| `prompt` | no | Free-text prompt for scheduled sessions. Agent sends it verbatim as the initial user message after CLI startup (gated by quota). |
| `branchSlug` | no | Worktree branch (`schedule/{branchSlug}`). Agent creates/attaches worktree on startup. |
| `softThreshold` | no | Anthropic 5h utilization % threshold. When `u5 >= softThreshold`, agent injects cooperative stop marker. If > 0, agent starts a `QuotaMonitor` goroutine. |
| `hardHeadroomTokens` | no | Output token budget past soft mark. 0 = immediate hard stop after soft marker. |
| `autoContinue` | no | Whether the session auto-resumes when quota recovers. |
| `permPolicy` | no | `strict-allowlist` — auto-approve allowlisted tools, auto-deny all others. Used by scheduled sessions (ADR-0044). |
| `allowedTools` | no | Tool names the session may use. Required when `permPolicy` is `strict-allowlist`. |
| `resumePrompt` | no | Custom resume message sent when quota recovers. If empty, platform default is used. |
| `resumeClaudeSessionID` | no | Claude Code session ID for `--resume` fallback after session loss. Empty for new sessions. |

Reply:
```json
{
    "id": "sess-001",
    "claudeSessionID": "cs-abc123"
}
```
The agent persists `claudeSessionID` to the session KV entry for degraded `--resume` fallback.

### `sessions.{sslug}.input`

**Publisher:** SPA/CLI | **Subscriber:** Agent (via stream consumer)

```json
{
    "id": "01JTRK8M5H7YRMZ6Q3WXA8EFGH",
    "ts": 1714470061000,
    "type": "message",
    "text": "what's wrong with this error?",
    "attachments": [
        {"id": "att-001", "filename": "screenshot.png", "mimeType": "image/png", "sizeBytes": 245000}
    ]
}
```

The `type` field distinguishes input kinds. Common fields (`id`, `ts`, `type`) are present on all inputs.

**`message` fields:**

| Field | Required | Description |
|-------|----------|-------------|
| `text` | yes | User's chat message text |
| `attachments` | no | Array of `AttachmentRef` objects. SPA uploads to S3 first, then includes references here. Agent resolves to pre-signed download URLs before passing to the CLI backend. Empty array or omitted if no attachments. |

**`skill_invoke` fields:**

| Field | Required | Description |
|-------|----------|-------------|
| `skillName` | yes | Skill/command name (e.g., `deploy-server`). Driver translates to backend-native format (Claude Code: `/skillName` prefix, Devin: slash command). |
| `args` | no | Skill-specific arguments as a key-value map. Schema depends on the skill. |

**`permission_response` fields:**

| Field | Required | Description |
|-------|----------|-------------|
| `requestId` | yes | ID of the permission request being answered (from a prior `permission_request` event) |
| `allowed` | yes | `true` to approve, `false` to deny |
| `behavior` | no | Persistence hint: `allow_always` (auto-approve this tool for the rest of the session), `deny_always` (auto-deny). Omitted for one-time decisions. |

**`message` input:**
```json
{
    "id": "01JTRK8M6J...",
    "ts": 1714470062000,
    "type": "message",
    "text": "help me refactor this function",
    "attachments": []
}
```

**`skill_invoke` input:**
```json
{
    "id": "01JTRK8M7K...",
    "ts": 1714470063000,
    "type": "skill_invoke",
    "skillName": "deploy-server",
    "args": {}
}
```

The driver translates this to the backend's native skill invocation format. For Claude Code and Droid, this becomes a user message prefixed with `/deploy-server`. For Devin ACP, this becomes a slash command.

**`permission_response` input:**
```json
{
    "type": "permission_response",
    "requestId": "perm-001",
    "allowed": true,
    "behavior": "allow_always"
}
```

| `behavior` | Meaning |
|-------------|---------|
| (omitted) | One-time allow/deny |
| `allow_always` | Auto-approve this tool for the rest of the session |
| `deny_always` | Auto-deny this tool for the rest of the session |

### `sessions.{sslug}.events`

**Publisher:** Agent | **Subscriber:** SPA/CLI (via stream consumer or core pub/sub)

Every event is a `SessionEvent` — the canonical schema that all CLI backend drivers translate into. The driver has already converted from the backend-native format (Claude Code stream-json, Droid stream-jsonrpc, Devin ACP, etc.).

**Envelope (present on every event):**

| Field | Description |
|-------|-------------|
| `id` | ULID — unique event identifier |
| `ts` | Unix millis — publisher-side timestamp |
| `type` | Event type (see enum below) |
| `sessionId` | Session slug this event belongs to |

**Event types:**

Only one of the type-specific fields is populated per event, matching the `type` value.

| `type` | Field | Description |
|--------|-------|-------------|
| `init` | `init` | Backend initialized — capabilities, model, tools |
| `state_change` | `stateChange` | Session state changed (idle, running, requires_action) |
| `text_delta` | `textDelta` | Streaming text fragment from assistant |
| `thinking_delta` | `thinkingDelta` | Streaming thinking/reasoning fragment |
| `message` | `message` | Complete message (assistant or user) |
| `tool_call` | `toolCall` | Tool invocation started |
| `tool_progress` | `toolProgress` | Tool execution progress update |
| `tool_result` | `toolResult` | Tool execution completed |
| `permission` | `permission` | Permission request or resolution |
| `turn_complete` | `turnComplete` | Assistant turn finished, token usage |
| `error` | `error` | Error from the CLI backend |
| `backend_specific` | `backendSpecific` | Opaque backend-unique events (e.g., Droid missions) |

---

**`init`** — emitted once when the CLI process starts:

```json
{
    "id": "01JTRK...", "ts": 1714470060000, "type": "init", "sessionId": "sess-001",
    "init": {
        "backend": "claude_code",
        "model": "sonnet",
        "tools": ["Read", "Write", "Bash", "Grep"],
        "skills": ["deploy-server"],
        "agents": ["worker"],
        "capabilities": {
            "hasThinking": true,
            "hasSubagents": true,
            "hasSkills": true,
            "hasPlanMode": false,
            "hasMissions": false,
            "hasEventStream": true,
            "hasSessionResume": true,
            "toolIcons": {"Read": "file", "Write": "pencil", "Bash": "terminal"},
            "thinkingLabel": "Thinking",
            "modelOptions": ["sonnet", "opus", "haiku"],
            "permissionModes": ["auto", "managed"]
        }
    }
}
```

| Field | Description |
|-------|-------------|
| `backend` | CLI backend that was launched |
| `model` | Active model |
| `tools` | Available tool names |
| `skills` | Available skill names (may be empty) |
| `agents` | Available sub-agent names (may be empty) |
| `capabilities` | Feature flags the SPA uses to adapt the UI (e.g., hide plan mode toggle, show thinking panel) |

---

**`state_change`**:

```json
{
    "id": "01JTRK...", "ts": 1714470061000, "type": "state_change", "sessionId": "sess-001",
    "stateChange": {"state": "running"}
}
```

| Field | Description |
|-------|-------------|
| `state` | `idle` (waiting for input), `running` (processing), `requires_action` (permission prompt pending) |

---

**`text_delta`** — streaming text fragment:

```json
{
    "id": "01JTRK...", "ts": 1714470062000, "type": "text_delta", "sessionId": "sess-001",
    "textDelta": {"messageId": "msg-001", "blockIndex": 0, "text": "Sure, let me help ", "encoding": "plain"}
}
```

| Field | Description |
|-------|-------------|
| `messageId` | ID of the message this delta belongs to |
| `blockIndex` | Index within the message's content blocks (for multi-block messages) |
| `text` | Text fragment to append |
| `encoding` | `"plain"` (default, UTF-8) or `"base64"` (GenericTerminalDriver PTY output). Omitted or empty for structured backends (ClaudeCodeDriver, DroidDriver, DevinDriver) — consumers treat omitted as `"plain"`. |

---

**`thinking_delta`** — streaming thinking/reasoning fragment (same structure as `text_delta`):

```json
{
    "id": "01JTRK...", "ts": 1714470062500, "type": "thinking_delta", "sessionId": "sess-001",
    "thinkingDelta": {"messageId": "msg-001", "blockIndex": 1, "text": "I should check the error..."}
}
```

---

**`message`** — complete message (emitted after all deltas for a message):

```json
{
    "id": "01JTRK...", "ts": 1714470063000, "type": "message", "sessionId": "sess-001",
    "message": {
        "messageId": "msg-001",
        "role": "assistant",
        "content": [
            {"type": "text", "text": "Here's the diagram:"},
            {"type": "attachment_ref", "ref": {"id": "att-002", "filename": "diagram.png", "mimeType": "image/png", "sizeBytes": 180000}}
        ],
        "parentToolUseId": ""
    }
}
```

| Field | Description |
|-------|-------------|
| `messageId` | Unique message ID |
| `role` | `assistant` or `user` |
| `content` | Array of content blocks (see below) |
| `parentToolUseId` | If this message was produced by a sub-agent, the tool_use ID that spawned it. Empty string for top-level messages. |

**Content block types:**

| `type` | Fields | Description |
|--------|--------|-------------|
| `text` | `text` | Plain text |
| `tool_use` | `toolUseId`, `toolName`, `input` | Tool invocation (inline in message) |
| `tool_result` | `toolUseId`, `content`, `isError` | Tool result (inline in message) |
| `image` | `source` | Base64-encoded image (small images only; large images use `attachment_ref`) |
| `attachment_ref` | `ref` (AttachmentRef) | Reference to binary data in S3. SPA resolves to a pre-signed download URL via CP. |

---

**`tool_call`** — tool invocation started:

```json
{
    "id": "01JTRK...", "ts": 1714470064000, "type": "tool_call", "sessionId": "sess-001",
    "toolCall": {"toolUseId": "tu-001", "toolName": "Read", "input": {"file_path": "/src/main.go"}, "messageId": "msg-001"}
}
```

| Field | Description |
|-------|-------------|
| `toolUseId` | Unique tool use ID (for correlating with `tool_progress` and `tool_result`) |
| `toolName` | Tool being invoked |
| `input` | Tool input parameters (schema varies per tool) |
| `messageId` | Message that triggered this tool call |

---

**`tool_progress`** — tool execution progress:

```json
{
    "id": "01JTRK...", "ts": 1714470065000, "type": "tool_progress", "sessionId": "sess-001",
    "toolProgress": {"toolUseId": "tu-001", "toolName": "Bash", "elapsedSecs": 5, "content": "Running tests..."}
}
```

| Field | Description |
|-------|-------------|
| `toolUseId` | Tool use being tracked |
| `toolName` | Tool name |
| `elapsedSecs` | Seconds since tool_call was emitted |
| `content` | Progress text (e.g., partial stdout for long-running commands) |

---

**`tool_result`** — tool execution completed:

```json
{
    "id": "01JTRK...", "ts": 1714470066000, "type": "tool_result", "sessionId": "sess-001",
    "toolResult": {"toolUseId": "tu-001", "toolName": "Read", "content": "package main\n...", "isError": false}
}
```

| Field | Description |
|-------|-------------|
| `toolUseId` | Tool use that completed |
| `toolName` | Tool name |
| `content` | Stringified result |
| `isError` | `true` if the tool execution failed |

---

**`permission`** — permission request or resolution:

```json
{
    "id": "01JTRK...", "ts": 1714470067000, "type": "permission", "sessionId": "sess-001",
    "permission": {"requestId": "perm-001", "toolName": "Bash", "toolInput": "rm -rf /tmp/old", "resolved": false, "allowed": false}
}
```

| Field | Description |
|-------|-------------|
| `requestId` | Unique request ID (user responds with this in `permission_response` input) |
| `toolName` | Tool requesting permission |
| `toolInput` | Summary of what the tool wants to do (for display to user) |
| `resolved` | `false` = request pending (SPA shows prompt), `true` = resolved (SPA shows outcome) |
| `allowed` | Only meaningful when `resolved: true`. Whether the user approved or denied. |

---

**`turn_complete`** — assistant turn finished:

```json
{
    "id": "01JTRK...", "ts": 1714470068000, "type": "turn_complete", "sessionId": "sess-001",
    "turnComplete": {"inputTokens": 1500, "outputTokens": 800, "costUsd": 0.012, "durationMs": 4500}
}
```

| Field | Description |
|-------|-------------|
| `inputTokens` | Tokens consumed as input this turn |
| `outputTokens` | Tokens generated as output this turn |
| `costUsd` | Estimated cost for this turn |
| `durationMs` | Wall-clock duration of the turn |

---

**`error`** — error from the CLI backend:

```json
{
    "id": "01JTRK...", "ts": 1714470069000, "type": "error", "sessionId": "sess-001",
    "error": {"message": "rate limit exceeded", "code": "rate_limit", "retryable": true}
}
```

| Field | Description |
|-------|-------------|
| `message` | Human-readable error message |
| `code` | Machine-readable error code (backend-specific, e.g., `rate_limit`, `context_overflow`, `auth_failed`) |
| `retryable` | Whether the agent will automatically retry |

---

**`backend_specific`** — opaque backend-unique events (e.g., Droid missions):

```json
{
    "id": "01JTRK...", "ts": 1714470070000, "type": "backend_specific", "sessionId": "sess-001",
    "backendSpecific": {"kind": "mission_progress", "missionId": "m-001", "workerCount": 3}
}
```

| Field | Description |
|-------|-------------|
| (opaque) | `json.RawMessage` — the SPA interprets the payload per-backend. Used for backend-unique features that don't map to canonical event types (e.g., Droid mission notifications, worker started/completed events). |

### `sessions.{sslug}.delete`

**Publisher:** SPA/CLI | **Subscriber:** Agent

```json
{}
```

Empty payload. The session slug is in the subject. Agent stops the CLI process, emits lifecycle.stopped, and tombstones the KV entry.

### `sessions.{sslug}.control.interrupt`

**Publisher:** SPA/CLI | **Subscriber:** Agent

```json
{}
```

Agent sends SIGINT to the CLI subprocess. Driver translates to native interrupt (Claude Code: `control_request.interrupt`, Droid: interrupt RPC, Devin: `session/cancel`).

### `sessions.{sslug}.control.restart`

**Publisher:** SPA/CLI | **Subscriber:** Agent

```json
{
    "preserveHistory": true
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `preserveHistory` | no | If true, restart with `--resume` (default: true) |

### `sessions.{sslug}.config`

**Publisher:** SPA/CLI | **Subscriber:** Agent

Patch semantics — only non-null fields are applied. The agent translates to the backend's native config mechanism.

```json
{
    "id": "01JTRK8M9N...",
    "ts": 1714470070000,
    "model": "opus",
    "permissionMode": null,
    "systemPrompt": null
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `model` | no | Switch model. Driver translates: Claude Code `set_model`, Droid `updateSettings`, Devin `session/set_config_option`. |
| `permissionMode` | no | `auto`, `managed`, `allowlist`. Some backends may require restart to apply. |
| `systemPrompt` | no | Replace system prompt. May require restart depending on backend. |

If the backend doesn't support live config changes (e.g., GenericTerminalDriver), the agent responds with a lifecycle error event and the SPA shows an appropriate message.

### `sessions.{sslug}.lifecycle.started`

**Publisher:** Agent | **Subscriber:** SPA/CLI

```json
{
    "id": "01JTRK8MA1...",
    "ts": 1714470060000,
    "sessionSlug": "sess-001",
    "backend": "claude_code",
    "startedAt": "2026-04-30T12:01:00Z",
    "capabilities": {
        "hasThinking": true,
        "hasSubagents": true,
        "hasSkills": true,
        "hasPlanMode": false,
        "hasMissions": false,
        "hasEventStream": true,
        "hasSessionResume": true,
        "toolIcons": {"Read": "file", "Write": "pencil", "Bash": "terminal"},
        "thinkingLabel": "Thinking",
        "modelOptions": ["sonnet", "opus", "haiku"],
        "permissionModes": ["auto", "managed"]
    }
}
```

| Field | Description |
|-------|-------------|
| `sessionSlug` | Slug of the started session |
| `backend` | CLI backend that was launched (see ADR-0005 `CLIBackend` enum) |
| `startedAt` | ISO 8601 timestamp when the CLI process became ready |
| `capabilities` | Driver-reported feature flags from the backend's `init` event. SPA uses these to adapt the UI per-backend (e.g., hide "Plan Mode" toggle if `hasPlanMode: false`). See ADR-0005 for the full `Capabilities` struct. |

### `sessions.{sslug}.lifecycle.stopped`

**Publisher:** Agent | **Subscriber:** SPA/CLI

```json
{
    "id": "01JTRK8MA2...",
    "ts": 1714470120000,
    "sessionSlug": "sess-001",
    "reason": "user_deleted",
    "stoppedAt": "2026-04-30T14:00:00Z",
    "exitCode": 0
}
```

| Field | Description |
|-------|-------------|
| `sessionSlug` | Slug of the stopped session |
| `reason` | Why the session stopped (see enum below) |
| `stoppedAt` | ISO 8601 timestamp when the CLI process exited |
| `exitCode` | Process exit code. `0` = clean exit, non-zero = crash or error. |

| `reason` | Description |
|-----------|-------------|
| `user_deleted` | User requested deletion via `sessions.{sslug}.delete` |
| `completed` | CLI exited normally (conversation ended) |
| `crashed` | CLI exited with non-zero code |
| `evicted` | Agent JWT expired and HTTP re-authentication failed |
| `host_offline` | Host disconnected (controller process died) |

### `sessions.{sslug}.lifecycle.error`

**Publisher:** Agent | **Subscriber:** SPA/CLI

```json
{
    "id": "01JTRK8MA3...",
    "ts": 1714470090000,
    "sessionSlug": "sess-001",
    "error": "CLI process exited with code 1: out of memory",
    "recoverable": true,
    "occurredAt": "2026-04-30T13:30:00Z"
}
```

| Field | Description |
|-------|-------------|
| `sessionSlug` | Slug of the affected session |
| `error` | Human-readable error message |
| `recoverable` | If `true`, the agent will attempt to restart the CLI process. If `false`, the session is dead (manual intervention needed). |
| `occurredAt` | ISO 8601 timestamp when the error was detected |

---

## Quota Subject

### `mclaude.users.{uslug}.quota` (core NATS, no JetStream retention)

**Publisher:** Daemon quota publisher (every 60s) | **Subscriber:** Per-session QuotaMonitors

```json
{
    "u5": 76,
    "u7": 42,
    "r5": "2026-04-28T08:00:00Z",
    "r7": "2026-05-01T00:00:00Z",
    "hasData": true,
    "ts": "2026-04-30T12:00:00Z"
}
```

| Field | Description |
|-------|-------------|
| `u5` | Anthropic 5-hour rolling window utilization (percentage) |
| `u7` | Anthropic 7-day rolling window utilization (percentage) |
| `r5` | ISO 8601 — 5-hour window reset time |
| `r7` | ISO 8601 — 7-day window reset time |
| `hasData` | `false` when the Anthropic API call failed (credentials missing, network error). QuotaMonitor does not act on stale data. |
| `ts` | ISO 8601 — when this status was fetched |

Data sourced from `GET https://api.anthropic.com/api/oauth/usage` using OAuth token from `~/.claude/.credentials.json`.

---

## Job Lifecycle Events

Published on `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.lifecycle.*` (captured by session stream).

### `session_job_paused`

```json
{
    "id": "01JTRK...", "ts": 1714470090000,
    "type": "session_job_paused",
    "sessionId": "sess-001",
    "pausedVia": "quota_soft",
    "u5": 76,
    "r5": "2026-04-28T08:00:00Z",
    "outputTokensSinceSoftMark": 0
}
```

| Field | Description |
|-------|-------------|
| `pausedVia` | `quota_soft` (cooperative stop after marker injection) or `quota_hard` (hard interrupt after token budget exceeded) |
| `u5` | Utilization at time of pause |
| `r5` | 5-hour window reset time (QuotaMonitor uses this for `resumeAt` when `autoContinue` is set) |
| `outputTokensSinceSoftMark` | Tokens generated after soft marker was injected. 0 for soft pause, non-zero for hard pause. |

### `session_job_complete`

```json
{
    "id": "01JTRK...", "ts": 1714470100000,
    "type": "session_job_complete",
    "sessionId": "sess-001",
    "branch": "schedule/refactor-auth"
}
```

Claude ended its turn naturally and the Stop hook allowed the stop. CLI subprocess exits. Session record persists for user review — `sessions.delete` only happens on explicit user action.

### `session_job_cancelled`

```json
{
    "id": "01JTRK...", "ts": 1714470110000,
    "type": "session_job_cancelled",
    "sessionId": "sess-001"
}
```

User published `sessions.delete`. Agent interrupted the subprocess.

### `session_permission_denied`

```json
{
    "id": "01JTRK...", "ts": 1714470120000,
    "type": "session_permission_denied",
    "sessionId": "sess-001",
    "tool": "mcp__gmail__send_email"
}
```

| Field | Description |
|-------|-------------|
| `tool` | Tool name that was auto-denied by the `strict-allowlist` policy |

Agent sends graceful stop. Session transitions to `needs_spec_fix`.

### `session_job_failed`

```json
{
    "id": "01JTRK...", "ts": 1714470130000,
    "type": "session_job_failed",
    "sessionId": "sess-001",
    "error": "subprocess exited without turn-end signal"
}
```

---

## KV Payloads

### `KV_mclaude-sessions-{uslug}`

Key: `hosts.{hslug}.projects.{pslug}.sessions.{sslug}`

Interactive session:
```json
{
    "slug": "sess-001",
    "hostSlug": "laptop-a",
    "projectSlug": "myapp",
    "status": "running",
    "backend": "claude_code",
    "model": "sonnet",
    "title": "Refactor auth module",
    "createdAt": "2026-04-30T12:00:00Z",
    "startedAt": "2026-04-30T12:01:00Z",
    "lastActivityAt": "2026-04-30T13:45:00Z",
    "inputTokens": 15000,
    "outputTokens": 8000,
    "costUsd": 0.12,
    "tools": ["Read", "Write", "Bash", "Grep"],
    "skills": ["deploy-server"],
    "agents": ["worker"],
    "capabilities": {
        "hasThinking": true,
        "hasSubagents": true,
        "hasSkills": true,
        "hasPlanMode": false,
        "hasMissions": false,
        "hasEventStream": true,
        "hasSessionResume": true
    }
}
```

Quota-managed session (additional fields):
```json
{
    "slug": "sched-001",
    "hostSlug": "laptop-a",
    "projectSlug": "myapp",
    "status": "paused",
    "backend": "claude_code",
    "model": "sonnet",
    "title": "refactor-auth-middleware",
    "createdAt": "2026-04-30T12:00:00Z",
    "startedAt": "2026-04-30T12:01:00Z",
    "lastActivityAt": "2026-04-30T13:45:00Z",
    "inputTokens": 15000,
    "outputTokens": 8000,
    "costUsd": 0.12,
    "tools": ["Read", "Write", "Edit", "Glob", "Grep", "Bash"],
    "skills": [],
    "agents": [],
    "capabilities": {
        "hasThinking": true,
        "hasSubagents": true,
        "hasSkills": true,
        "hasPlanMode": false,
        "hasMissions": false,
        "hasEventStream": true,
        "hasSessionResume": true
    },
    "softThreshold": 75,
    "hardHeadroomTokens": 50000,
    "autoContinue": true,
    "pausedVia": "quota_soft",
    "claudeSessionID": "cs-abc123",
    "branchSlug": "refactor-auth-middleware",
    "failedTool": "",
    "resumeAt": "2026-04-30T17:00:00Z"
}
```

| Field | Description |
|-------|-------------|
| `slug` | Session slug (matches KV key suffix and NATS subject token) |
| `hostSlug` | Host the session runs on |
| `projectSlug` | Project the session belongs to |
| `status` | Current lifecycle state (see enum below) |
| `backend` | CLI backend (`claude_code`, `droid`, `devin_acp`, `gemini`, `generic_terminal`) |
| `model` | Active model (may differ from project default if changed via `config`) |
| `title` | Auto-generated or user-set session title |
| `createdAt` | ISO 8601 — when the session was requested |
| `startedAt` | ISO 8601 — when the CLI process became ready (`null` if still pending) |
| `lastActivityAt` | ISO 8601 — last message or event timestamp. Updated by agent on every event. |
| `inputTokens` | Cumulative input tokens consumed |
| `outputTokens` | Cumulative output tokens consumed |
| `costUsd` | Estimated cost in USD (computed by agent from token counts and model pricing) |
| `tools` | Available tool names reported by the backend's `init` event. Top-level array (not nested under `capabilities`). |
| `skills` | Available skill names reported by the backend's `init` event. Top-level array (may be empty). |
| `agents` | Available sub-agent names reported by the backend's `init` event. Top-level array (may be empty). |
| `capabilities` | `CLICapabilities` — boolean feature flags from the backend's `init` event. SPA uses these to adapt the UI per-backend (e.g., hide plan mode toggle if `hasPlanMode: false`). Fields: `hasThinking`, `hasSubagents`, `hasSkills`, `hasPlanMode`, `hasMissions`, `hasEventStream`, `hasSessionResume`. See ADR-0005 for the full struct. |

**Quota-managed session fields** (omitted for interactive sessions, zero values omitted):

| Field | Description |
|-------|-------------|
| `softThreshold` | Anthropic 5h utilization % threshold. Encodes urgency: higher = aggressive (runs until quota nearly exhausted), lower = conservative (pauses early). SPA can display "pauses at 75%." |
| `hardHeadroomTokens` | Output token budget past soft mark. |
| `autoContinue` | Whether the session auto-resumes when quota recovers. |
| `pausedVia` | `quota_soft`, `quota_hard`, or empty. **SPA uses this to filter/dim paused sessions** (e.g., collapse "3 paused (quota)" group, show pause icon). |
| `claudeSessionID` | Claude Code's session ID for `--resume` fallback. |
| `branchSlug` | Worktree branch (`schedule/{branchSlug}`). SPA can link to the branch. |
| `failedTool` | Tool name that triggered `needs_spec_fix`. SPA shows "blocked on: {tool}." |
| `resumeAt` | ISO 8601 — when `autoContinue` will trigger resume. SPA can show countdown. |

| `status` | Description |
|-----------|-------------|
| `pending` | CLI process spawned but waiting for quota before sending prompt |
| `running` | CLI process active, session idle or working |
| `paused` | Stopped on quota pressure (see `pausedVia`). CLI subprocess alive. |
| `requires_action` | Permission prompt pending |
| `completed` | Claude finished naturally (Stop hook allowed) |
| `stopped` | CLI process exited normally (interactive session ended) |
| `cancelled` | User cancelled via `sessions.delete` |
| `needs_spec_fix` | Out-of-allowlist tool denied. Manual intervention needed. |
| `failed` | Unrecoverable error (crash, `--resume` failure) |
| `error` | CLI process crashed or agent evicted |

### `KV_mclaude-projects-{uslug}`

Key: `hosts.{hslug}.projects.{pslug}`

```json
{
    "slug": "myapp",
    "hostSlug": "laptop-a",
    "name": "My App",
    "path": "/home/alice/projects/myapp",
    "backend": "claude_code",
    "defaultModel": "sonnet",
    "defaultPermissionMode": "managed",
    "createdAt": "2026-04-30T11:00:00Z",
}
```

| Field | Description |
|-------|-------------|
| `slug` | Project slug (matches KV key suffix and NATS subject token) |
| `hostSlug` | Host the project is provisioned on |
| `name` | Display name |
| `path` | Filesystem path on the host machine |
| `backend` | Default CLI backend for new sessions |
| `defaultModel` | Default model for new sessions (overridable per-session via `config`) |
| `defaultPermissionMode` | Default permission mode for new sessions |
| `createdAt` | ISO 8601 — when the project was provisioned |

### `KV_mclaude-hosts`

Key: `{hslug}`

```json
{
    "slug": "laptop-a",
    "name": "My MacBook",
    "type": "machine",
    "online": true,
    "lastSeenAt": "2026-04-30T12:00:00Z",
    "version": "0.1.0",
    "os": "darwin",
    "arch": "arm64"
}
```

| Field | Description |
|-------|-------------|
| `slug` | Host slug (matches KV key and NATS subject token). Globally unique, `[a-z0-9-]+`. |
| `name` | Display name set by owner at registration or via `manage.update` |
| `type` | Host type (see enum below) |
| `online` | `true` if host controller is currently connected to NATS. Updated by CP on `$SYS.CONNECT/DISCONNECT` events. |
| `lastSeenAt` | ISO 8601 — last `$SYS.CONNECT` or heartbeat timestamp |
| `version` | Host controller software version |
| `os` | Operating system (e.g., `darwin`, `linux`) |
| `arch` | CPU architecture (e.g., `arm64`, `amd64`) |

| `type` | Description |
|--------|-------------|
| `machine` | BYOH laptop/desktop |
| `cluster` | K8s-managed cluster |

---

## Host Management Subjects

### `mclaude.users.{uslug}.hosts._.register` (request/reply)

**Publisher:** CLI | **Subscriber:** CP

Request:
```json
{
    "id": "01JTRK8MA...",
    "ts": 1714470080000,
    "name": "My MacBook",
    "type": "machine",
    "nkeyPublic": "UABC..."
}
```

Response:
```json
{
    "ok": true,
    "slug": "laptop-a"
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `name` | yes | Display name for the host |
| `type` | yes | `machine` (BYOH) or `cluster` (K8s cluster) |
| `nkeyPublic` | yes | Ed25519 public key generated by the host controller at startup. Starts with `U`. CP stores it for future HTTP challenge-response authentication. |

CP validates the user's identity, creates the host in Postgres (slug, name, type, owner_id, nkey_public). No JWT in the response — the host controller authenticates itself via HTTP challenge-response (see "Unified HTTP Credential Protocol"). `_` is a sentinel token that can never collide with real slugs (slugs are `[a-z0-9-]+`).

### `mclaude.users.{uslug}.hosts.{hslug}.manage.update` (request/reply)

**Publisher:** CLI | **Subscriber:** CP

Request (patch semantics — only included fields are applied):
```json
{
    "id": "01JTRK8MB...",
    "ts": 1714470081000,
    "name": "My MacBook Pro",
    "type": "machine"
}
```

Response:
```json
{"ok": true}
```

Owner-only. CP updates the host row in Postgres and publishes a KV update to `$KV.mclaude-hosts.{hslug}` with the new metadata.

### `mclaude.users.{uslug}.hosts.{hslug}.manage.grant` (request/reply)

**Publisher:** CLI (host owner) | **Subscriber:** CP

Request:
```json
{
    "id": "01JTRK8MC...",
    "ts": 1714470082000,
    "userSlug": "bob"
}
```

Response:
```json
{"ok": true}
```

Owner-only. CP inserts `(host_id, user_id)` into `host_access`. Revokes the grantee's NATS JWT — their SPA reconnects and gets a new JWT with the host in its host list.

### `mclaude.users.{uslug}.hosts.{hslug}.manage.revoke-access` (request/reply)

**Publisher:** CLI (host owner) | **Subscriber:** CP

Request:
```json
{
    "id": "01JTRK8MC...",
    "ts": 1714470082000,
    "userSlug": "bob"
}
```

Response:
```json
{"ok": true}
```

Owner-only. CP deletes `(host_id, user_id)` from `host_access`. Revokes the grantee's NATS JWT and all agent JWTs for the grantee's projects on this host. Active sessions are terminated.

### `mclaude.users.{uslug}.hosts.{hslug}.manage.rekey` (request/reply)

**Publisher:** CLI (host owner) | **Subscriber:** CP

Request:
```json
{
    "id": "01JTRK8MC2...",
    "ts": 1714470082500,
    "nkeyPublic": "UNEW..."
}
```

Response:
```json
{"ok": true}
```

Owner-only. Updates `hosts.nkey_public` in Postgres in place. SSH `known_hosts` model — the owner explicitly attests to the new key after a host reinstall or disk wipe. The old JWT becomes useless (NATS nonce challenge will fail against the new key on next connection). The host controller must re-authenticate via HTTP with the new key.

### `mclaude.users.{uslug}.hosts.{hslug}.manage.deregister` (request/reply)

**Publisher:** CLI | **Subscriber:** CP

Request:
```json
{
    "id": "01JTRK8MD...",
    "ts": 1714470083000
}
```

Response:
```json
{"ok": true}
```

Owner-only. CP drains active sessions (sends `projects.delete` to host controller for each active project), revokes the host's JWT, deletes the stored NKey public key, removes the host KV entry (tombstone on `$KV.mclaude-hosts.{hslug}`), and marks the host as deregistered in Postgres. See ADR-0054 "Deregistration" for the full flow.

### `mclaude.users.{uslug}.hosts.{hslug}.manage.revoke` (request/reply)

**Publisher:** CLI | **Subscriber:** CP

Request:
```json
{
    "id": "01JTRK8ME...",
    "ts": 1714470084000
}
```

Response:
```json
{"ok": true}
```

Owner-only. Emergency revocation — CP adds the host JWT to the NATS revocation list (immediate disconnect), deletes stored NKey public key, marks host as revoked in Postgres, updates KV with `"status":"revoked"`. All agent connections on the host also drop. Host must re-register to reconnect (new NKey, new attestation).

All management subjects return the standard error format on failure (see "Error Responses").

---

## Project Subjects (fan-out)

Control-plane publishes after HTTP validation. Both CP and host controller receive via NATS fan-out. No relay or interceptor.

### `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.create`

**Publisher:** control-plane (fan-out after HTTP validation) | **Subscribers:** CP + host controller

```json
{
    "id": "01JTRK8MF...",
    "ts": 1714470085000,
    "projectPath": "/home/alice/projects/myapp",
    "backend": "claude_code",
    "importS3Key": null
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `projectPath` | yes | Filesystem path on the host. Controller verifies it exists and is accessible. |
| `backend` | yes | CLI backend to use (see ADR-0005 `CLIBackend` enum) |
| `importS3Key` | no | S3 key for import archive. If non-null, agent downloads and extracts before starting session. |

No `userSlug` or `projectSlug` in the payload — both are encoded in the subject. Session creation is a separate operation (see `sessions.create`).

**CP** validates alice has access to laptop-a, creates Postgres records, writes project KV entry.
**Host controller** creates the project directory, starts the agent subprocess. Agent generates NKey, host controller registers it via `api.agents.register` (with retry-backoff if CP hasn't finished processing yet), agent authenticates via HTTP.

### `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.delete`

**Publisher:** control-plane (fan-out after HTTP validation) | **Subscribers:** CP + host controller

```json
{
    "id": "01JTRK8MG...",
    "ts": 1714470086000
}
```

No additional fields — the subject encodes user, host, and project. Host controller stops the agent subprocess (graceful drain — agent publishes `lifecycle.stopped` for active sessions), cleans up local state. CP removes Postgres records, tombstones project KV entry.

### `mclaude.users.{uslug}.hosts.{hslug}.projects.check-slug` (request/reply)

**Publisher:** CLI | **Subscriber:** CP

Request:
```json
{
    "slug": "myapp"
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `slug` | yes | Proposed project slug to check for availability. Uniqueness is per-user-per-host (host token is in the subject). |

Response (available):
```json
{
    "available": true
}
```

Response (taken):
```json
{
    "available": false,
    "suggestion": "myapp-2"
}
```

| Field | Description |
|-------|-------------|
| `available` | `true` if the slug is available for use, `false` if already taken |
| `suggestion` | Only present when `available` is `false`. A suggested alternative slug (original with numeric suffix). |

### `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.complete`

**Publisher:** Agent | **Subscriber:** CP

```json
{
    "id": "01JTRK8MH...",
    "ts": 1714470087000,
}
```

Standard envelope only — no additional fields. The subject encodes user/host/project, and CP resolves the import ID from the project's KV state (`importRef`). Agent signals that the S3 import archive has been downloaded and extracted. CP receives the signal, deletes the S3 object. The agent (which sent this message) handles clearing `importRef` from project KV state.

### `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.request` (request/reply)

**Publisher:** CLI | **Subscriber:** CP

Request:
```json
{
    "id": "01JTRK8MH1...",
    "ts": 1714470085000,
    "slug": "myapp",
    "sizeBytes": 5242880
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `slug` | yes | Project slug for the import |
| `sizeBytes` | yes | Archive size in bytes. CP may reject if it exceeds the configurable limit (default 500MB). |

Response:
```json
{
    "id": "imp-001",
    "uploadUrl": "https://s3.../alice/laptop-a/myapp/imports/imp-001.tar.gz?X-Amz-Signature=..."
}
```

| Field | Description |
|-------|-------------|
| `id` | Import ID (opaque, e.g. `imp-001`). Used in `import.confirm` and stored as `importRef` in project KV. |
| `uploadUrl` | Pre-signed S3 PUT URL. Expires in 5 minutes. CLI uploads the archive directly to S3. |

### `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.confirm` (request/reply)

**Publisher:** CLI | **Subscriber:** CP

Request:
```json
{
    "id": "01JTRK8MH2...",
    "ts": 1714470086000,
    "importId": "imp-001"
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `importId` | yes | Import ID returned by `import.request` |

Response:
```json
{"ok": true}
```

CP validates the upload exists in S3, creates the project in Postgres (with `source: "import"` and `import_ref` set to the S3 key), writes `ProjectKVState` with `importRef` (import ID), and dispatches provisioning to the host controller.

### `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.download` (request/reply)

**Publisher:** Agent | **Subscriber:** CP

Request:
```json
{
    "id": "01JTRK8MH3...",
    "ts": 1714470088000,
    "importId": "imp-001"
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `importId` | yes | Import ID from `importRef` in project KV state |

Response:
```json
{
    "downloadUrl": "https://s3.../alice/laptop-a/myapp/imports/imp-001.tar.gz?X-Amz-Signature=..."
}
```

| Field | Description |
|-------|-------------|
| `downloadUrl` | Pre-signed S3 GET URL. Expires in 5 minutes. Agent downloads the archive directly from S3. |

CP validates the requesting agent owns the project (agent JWT contains uslug/hslug/pslug).

### `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.attachments.download` (request/reply)

**Publisher:** Agent | **Subscriber:** CP

Request:
```json
{
    "id": "01JTRK8MH4...",
    "ts": 1714470089000,
    "attachmentId": "att-001"
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `attachmentId` | yes | Attachment ID from `AttachmentRef` in the session input message |

Response:
```json
{
    "downloadUrl": "https://s3.../alice/laptop-a/myapp/attachments/att-001?X-Amz-Signature=..."
}
```

| Field | Description |
|-------|-------------|
| `downloadUrl` | Pre-signed S3 GET URL. Expires in 5 minutes. Agent downloads directly from S3. |

CP validates the agent's project ownership (agent JWT contains uslug/hslug/pslug; attachment S3 key matches).

### `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.attachments.upload` (request/reply)

**Publisher:** Agent | **Subscriber:** CP

Request:
```json
{
    "id": "01JTRK8MH5...",
    "ts": 1714470090000,
    "filename": "diagram.png",
    "mimeType": "image/png",
    "sizeBytes": 180000
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `filename` | yes | Original filename (preserved for download) |
| `mimeType` | yes | MIME type (e.g. `image/png`, `application/pdf`) |
| `sizeBytes` | yes | File size in bytes. CP enforces a per-file limit (default 50MB). |

Response:
```json
{
    "id": "att-002",
    "uploadUrl": "https://s3.../alice/laptop-a/myapp/attachments/att-002?X-Amz-Signature=..."
}
```

| Field | Description |
|-------|-------------|
| `id` | Opaque attachment ID. Agent includes this in `AttachmentRef` when publishing session events. |
| `uploadUrl` | Pre-signed S3 PUT URL. Expires in 5 minutes. Agent uploads directly to S3. |

### `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.attachments.confirm` (request/reply)

**Publisher:** Agent | **Subscriber:** CP

Request:
```json
{
    "id": "01JTRK8MH6...",
    "ts": 1714470091000,
    "attachmentId": "att-002"
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `attachmentId` | yes | Attachment ID returned by `attachments.upload` |

Response:
```json
{"ok": true}
```

CP records attachment metadata in Postgres (`confirmed = true`). Until confirmed, the attachment ID is not resolvable by consumers.

---

## Agent Public Key Registration (NATS)

### `mclaude.hosts.{hslug}.api.agents.register` (request/reply)

**Publisher:** Host controller | **Subscriber:** CP

Request:
```json
{
    "id": "01JTRK8MI...",
    "ts": 1714470088000,
    "userSlug": "alice",
    "projectSlug": "myapp",
    "nkeyPublic": "UABC..."
}
```

Response:
```json
{
    "ok": true,
    "quotaPublisher": false
}
```

| Field | Description |
|-------|-------------|
| `ok` | `true` on success |
| `quotaPublisher` | `true` if this agent is designated as the quota publisher for the user (ADR-0044). Only the designated agent runs `runQuotaPublisher` — all others only subscribe to `mclaude.users.{uslug}.quota`. |

| Field | Required | Description |
|-------|----------|-------------|
| `userSlug` | yes | Owner of the project the agent serves |
| `projectSlug` | yes | Project the agent is scoped to |
| `nkeyPublic` | yes | Ed25519 public key generated by the agent at startup. Starts with `U`. |

CP validates host access + project ownership + host assignment, stores the `(uslug, hslug, pslug) → nkey_public` mapping. The agent then authenticates via HTTP (see below).

If CP returns `NOT_FOUND` (project create not yet processed — fan-out race), the host controller retries with exponential backoff (100ms, 200ms, 400ms, ..., max 5s, max 10 attempts).

### `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.manage.designate-quota-publisher`

**Publisher:** CP | **Subscriber:** Session-agent

Published by the control-plane when re-designating the quota publisher for a user (ADR-0044). Triggered when the previously designated agent goes offline (detected via `$SYS.ACCOUNT.*.DISCONNECT`). CP selects the next online agent for the user and publishes to that agent's project-scoped subject.

```json
{
    "id": "01JTRK8MI2...",
    "ts": 1714470089000,
    "quotaPublisher": true
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `quotaPublisher` | yes | `true` to designate this agent as the quota publisher; `false` to de-designate (e.g., when another agent takes over). |

On receipt of `quotaPublisher: true`, the agent starts `runQuotaPublisher` (polling Anthropic OAuth usage endpoint every 60s, publishing `QuotaStatus` to `mclaude.users.{uslug}.quota`). On receipt of `quotaPublisher: false`, the agent stops `runQuotaPublisher` if running. Fire-and-forget (no reply).

---

## Unified HTTP Credential Protocol

All identity types (user, host, agent) authenticate and refresh via the same HTTP endpoints. Bootstrap and refresh are the same code path.

### `POST /api/auth/challenge`

Request:
```json
{
    "nkeyPublic": "UABC..."
}
```

Response:
```json
{
    "challenge": "<random-nonce>"
}
```

| Field | Description |
|-------|-------------|
| `nkeyPublic` | NKey public key (starts with `U`). CP looks up this key in Postgres to determine identity type. |
| `challenge` | Random nonce (base64-encoded, 32 bytes). Single-use, expires after 30 seconds. |

CP looks up the public key in Postgres. If not found, returns `{"ok":false,"error":"unknown public key","code":"NOT_FOUND"}`.

### `POST /api/auth/verify`

Request:
```json
{
    "nkeyPublic": "UABC...",
    "challenge": "<nonce>",
    "signature": "<ed25519-signature-of-nonce>"
}
```

Response:
```json
{
    "ok": true,
    "jwt": "<signed-jwt>"
}
```

| Field | Description |
|-------|-------------|
| `nkeyPublic` | Same public key from the challenge step |
| `challenge` | The nonce returned by `/api/auth/challenge` |
| `signature` | Ed25519 signature of the challenge nonce, signed with the NKey seed. Base64-encoded. |
| `jwt` | NATS user JWT signed by CP's account key. Contains identity-specific Pub/Sub.Allow (see ADR-0054 permission specs). |

CP verifies the signature against the stored public key, determines the identity type (user, host, or agent), resolves current permissions, signs and returns a JWT. Error responses:

```json
{"ok": false, "error": "invalid signature", "code": "UNAUTHORIZED"}
{"ok": false, "error": "host revoked", "code": "FORBIDDEN"}
{"ok": false, "error": "challenge expired", "code": "EXPIRED"}
```

---

## HTTP Endpoints (attachment lifecycle)

These are not NATS subjects but are referenced by NATS payloads.

### `POST /api/attachments/upload-url`

Request:
```json
{
    "filename": "screenshot.png",
    "mimeType": "image/png",
    "sizeBytes": 245000,
    "projectSlug": "myapp",
    "hostSlug": "laptop-a"
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `filename` | yes | Original filename (preserved for download) |
| `mimeType` | yes | MIME type. CP validates against an allowlist (images, PDFs, archives, text). |
| `sizeBytes` | yes | Exact file size. CP enforces a per-file limit (e.g., 50MB) and per-project quota. |
| `projectSlug` | yes | Project the attachment belongs to (for S3 key scoping and access control) |
| `hostSlug` | yes | Host the project runs on (for access control validation) |

Response:
```json
{
    "id": "att-001",
    "uploadUrl": "https://s3.../att-001?X-Amz-Expires=300&X-Amz-Signature=..."
}
```

| Field | Description |
|-------|-------------|
| `id` | Opaque attachment ID. Used in all subsequent references (`AttachmentRef.id`, confirm, download). |
| `uploadUrl` | Pre-signed S3 PUT URL. Expires in 5 minutes. Client uploads the file directly to S3 using this URL. |

### `POST /api/attachments/{id}/confirm`

Request:
```json
{}
```

CP verifies the object exists in S3 (HEAD request), records metadata (size, content-type, upload timestamp) in Postgres. Until confirmed, the attachment ID is not resolvable by consumers.

### `GET /api/attachments/{id}`

Response:
```json
{
    "id": "att-001",
    "filename": "screenshot.png",
    "mimeType": "image/png",
    "sizeBytes": 245000,
    "downloadUrl": "https://s3.../att-001?X-Amz-Expires=300&X-Amz-Signature=..."
}
```

| Field | Description |
|-------|-------------|
| `id` | Attachment ID |
| `filename` | Original filename |
| `mimeType` | MIME type |
| `sizeBytes` | File size in bytes |
| `downloadUrl` | Pre-signed S3 GET URL. Expires in 5 minutes. Consumer downloads directly from S3. |

CP validates the requester has access to the project the attachment belongs to, then generates a pre-signed download URL.

---

## Error Responses

All request/reply subjects (provisioning, agent registration, host management) and HTTP auth endpoints use a consistent error format:

```json
{
    "ok": false,
    "error": "access denied: alice does not have access to laptop-a",
    "code": "UNAUTHORIZED"
}
```

| Field | Description |
|-------|-------------|
| `ok` | Always `false` for errors |
| `error` | Human-readable error message. Safe to log. Never contains secrets or stack traces. |
| `code` | Machine-readable error code (see enum below). Clients switch on this, not the message. |

| `code` | Description |
|--------|-------------|
| `UNAUTHORIZED` | Identity not authorized for this operation (e.g., not host owner, invalid signature) |
| `FORBIDDEN` | Identity recognized but action denied (e.g., host revoked, user lacks host access) |
| `NOT_FOUND` | Resource doesn't exist (e.g., unknown public key, host slug not found) |
| `EXPIRED` | Challenge nonce or JWT expired |
| `INVALID` | Request payload validation failed (e.g., missing required field, invalid slug format) |
| `CONFLICT` | Resource already exists or state conflict (e.g., slug taken, host already registered) |
| `INTERNAL` | CP internal error (database failure, signing error). Client should retry with backoff. |
