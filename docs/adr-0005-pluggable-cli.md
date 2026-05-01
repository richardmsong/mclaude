# Pluggable CLI Backend Architecture

**Status**: draft
**Status history**:
- 2026-04-10: accepted
- 2026-04-19: reverted to draft — retroactive accepted tag incorrect; implementation not confirmed, needs per-ADR review


## Overview

Make the mclaude session agent support multiple CLI backends (Claude Code, Factory Droid, Devin CLI, Gemini CLI) through a driver/adapter pattern. Each CLI becomes a "driver" that owns its native protocol and translates it into a canonical internal event schema that flows through NATS to clients.

The three target CLIs use fundamentally different streaming protocols:

| Backend | Protocol | Transport | Spec |
|---------|----------|-----------|------|
| Claude Code | Custom NDJSON (`stream-json`) | Flat NDJSON lines on stdin/stdout | Anthropic proprietary |
| Factory Droid | JSON-RPC 2.0 (`stream-jsonrpc`) | JSON-RPC envelope, 22 notification types | Factory proprietary |
| Devin CLI | ACP (Agent Client Protocol) | JSON-RPC 2.0 over stdio | Open spec at agentclientprotocol.com |

These are not minor variations — they differ in message framing, session lifecycle, permission handling, and event semantics. The driver layer must absorb this complexity so everything downstream (NATS subjects, KV state, SPA rendering) stays protocol-agnostic.

## Protocol Comparison

### Message Framing

- **Claude Code**: Each stdout line is a JSON object with a `type` field (`system`, `assistant`, `user`, `stream`, `result`, `control_request`, `sdk_control_request`, `control_response`). No envelope. Subtypes on `subtype` field.
- **Droid**: JSON-RPC 2.0 envelope with `jsonrpc`, `factoryProtocolVersion`, `type` (`request`/`response`/`notification`). Session events arrive as notifications with typed payloads (`assistant_text_delta`, `tool_use`, `working_state_changed`, etc.).
- **Devin ACP**: JSON-RPC 2.0 envelope. Agent methods (`initialize`, `session/new`, `session/prompt`). Client notifications via `session/update` with nested update types (`content_chunk`, `tool_call`, `tool_call_update`, `plan`).

### Session Lifecycle

| | Claude Code | Droid | Devin ACP |
|---|---|---|---|
| **Create** | CLI flags: `--session-id {id}` | RPC: `droid.create_session` | RPC: `session/new` |
| **Resume** | CLI flag: `--resume {id}` | RPC: `droid.resume_session` | RPC: `session/load` |
| **Prompt** | Write `{"type":"user","message":...}` to stdin | RPC: `droid.send_message` | RPC: `session/prompt` |
| **Cancel** | Write `{"type":"control_request","request":{"subtype":"interrupt"}}` | RPC: interrupt method | Notification: `session/cancel` |
| **State** | `session_state_changed` events (`idle`/`running`/`requires_action`) | `working_state_changed` notifications (`Idle`/`Working`/`WaitingForPermission`) | Inferred from prompt response `stopReason` |
| **Turn end** | `result` event with cost/tokens | SDK-synthesized `turn_complete` on idle transition | `session/prompt` response with `stopReason` |

### Permission Handling

| | Claude Code | Droid | Devin ACP |
|---|---|---|---|
| **Request** | `sdk_control_request` with `subtype: "permission"` | Server→client RPC: `droid.request_permission` | Agent→client RPC: `session/request_permission` |
| **Response** | `control_response` with `behavior: allow/deny` | Client→server response with `selectedOption` enum | Client→agent response with `selectedOption` |
| **Auto-approve** | `--dangerously-skip-permissions` / `bypassPermissions` | `--skip-permissions-unsafe` / autonomy levels | `--permission-mode dangerous` / `bypass` mode |

### Streaming Granularity

| | Claude Code | Droid | Devin ACP |
|---|---|---|---|
| **Text deltas** | `stream` events with `event.type: "content_block_delta"` | `assistant_text_delta` notifications | `session/update` with `content_chunk` (role: `agent`) |
| **Thinking** | `thinking` content blocks in `assistant` messages | `thinking_text_delta` notifications | `session/update` with `content_chunk` (role: `thought`) |
| **Tool start** | `tool_use` block in `assistant` content array | `tool_use` notification (extracted from `create_message`) | `session/update` with `tool_call` |
| **Tool progress** | `tool_progress` event (elapsed time only, no stdout) | `tool_progress` notification with update object | `session/update` with `tool_call_update` |
| **Tool result** | `tool_result` content block in next `user` message | `tool_result` notification | `session/update` with `tool_call_update` (status: completed) |
| **Subagents** | `parent_tool_use_id` field on events | `parentId` on `create_message` | Not yet specified in ACP |

### Capabilities Discovery

- **Claude Code**: `system.init` event carries `tools`, `skills`, `agents`, `model`. Refresh via `reload_plugins` control request.
- **Droid**: Init notification carries tools, model, capabilities. `droid.list_skills` RPC. Settings update notifications.
- **Devin ACP**: `session/new` response carries `configOptions` (model picker, mode picker). `session/update` with `available_commands_update` for slash commands.

### Skills / Commands / Slash Commands

#### The Agent Skills Standard (`SKILL.md`)

An open cross-tool standard has emerged at [agentskills.io](https://agentskills.io/specification). A skill is a directory containing a `SKILL.md` file with YAML frontmatter (`name`, `description`) and markdown instructions, plus optional `scripts/`, `references/`, `assets/` subdirectories. Adopted by 25+ tools as of early 2026:

| Tool | Project skills path | Personal skills path | Legacy format |
|---|---|---|---|
| Claude Code | `.claude/skills/*/SKILL.md` | `~/.claude/skills/*/SKILL.md` | `.claude/commands/*.md` (flat files, still works) |
| Factory Droid | `.factory/skills/*/SKILL.md` | `~/.factory/skills/*/SKILL.md` | `.factory/commands/*.md` (still works) |
| OpenAI Codex | `.codex/skills/*/SKILL.md` | `~/.codex/skills/*/SKILL.md` | — |
| GitHub Copilot | `.github/skills/*/SKILL.md` | `~/.github/skills/*/SKILL.md` | `.github/copilot-instructions.md` |
| Gemini CLI | `.gemini/skills/*/SKILL.md` | `~/.gemini/skills/*/SKILL.md` | `GEMINI.md` |
| Cursor | `.cursor/skills/*/SKILL.md` | `~/.cursor/skills/*/SKILL.md` | `.cursor/rules/*.mdc` |
| Devin CLI | `.windsurf/skills/*/SKILL.md` | `~/.windsurf/skills/*/SKILL.md` | `.windsurfrules` |
| Generic (compat) | `.agent/skills/*/SKILL.md` | — | Vendor-neutral fallback |

The `SKILL.md` format is identical across all tools — only the discovery path differs. Frontmatter fields per the spec:

| Field | Required | Description |
|---|---|---|
| `name` | Yes | Lowercase letters, numbers, hyphens. Max 64 chars. Must match directory name. |
| `description` | Yes | What it does and when to use it. Max 1024 chars. Used for auto-discovery. |
| `license` | No | License name or reference. |
| `compatibility` | No | Environment requirements. |
| `metadata` | No | Arbitrary key-value map (author, version, etc.). |
| `allowed-tools` | No | Pre-approved tools (experimental). |

Some tools extend the spec with vendor-specific fields. Droid adds `user-invocable` and `disable-model-invocation`. These are ignored by tools that don't understand them.

#### Per-Backend Behavior

| | Claude Code | Factory Droid | Devin CLI |
|---|---|---|---|
| **Concept** | Skills (was "slash commands") | Skills | Slash commands |
| **Discovery** | `.claude/skills/*/SKILL.md` + legacy `.claude/commands/*.md` | `.factory/skills/*/SKILL.md` + legacy `.factory/commands/*.md` | Built-in (`/mode`, `/plan`, etc.) + ACP `available_commands_update` |
| **Model auto-invoke** | Yes (unless `disable-model-invocation: true`) | Yes (unless `disable-model-invocation: true`) | N/A (agent controls its own commands) |
| **Invoke syntax** | `/skill-name` | `/skill-name` | `/command` in interactive session |

#### Managing Skills

All skills live in `.agent/skills/` — the vendor-neutral path from the Agent Skills standard. Both Claude Code and Factory Droid scan this path natively:
- **Claude Code** follows the Agent Skills spec and discovers `.agent/skills/*/SKILL.md`.
- **Factory Droid** explicitly lists `/.agent/skills/` as a "Compatibility" scope.
- **Codex**, **Copilot**, **Gemini CLI** also scan `.agent/skills/` per the spec.

No symlinks, no vendor-prefixed duplicates. One directory, all tools pick it up.

**Adding a skill:**

```bash
mkdir -p .agent/skills/my-skill
cat > .agent/skills/my-skill/SKILL.md << 'EOF'
---
name: my-skill
description: What this skill does and when to use it. Be specific — the agent uses this to decide when to auto-invoke.
---

# My Skill

## Instructions

1. Step one
2. Step two
3. Verify the result
EOF
```

The skill is immediately available as `/my-skill` in any backend that scans `.agent/skills/`. No restart needed for Claude Code (live change detection); Droid requires a session restart to rescan.

**Editing a skill:** Edit `.agent/skills/my-skill/SKILL.md` directly. The YAML frontmatter controls behavior:

| Field | Effect |
|---|---|
| `description` | Drives auto-discovery — agent loads the skill when your task matches. Front-load keywords. Max 1024 chars, truncated at 250 in listings. |
| `disable-model-invocation: true` | Only you can invoke via `/name`. Use for deploy, destructive, or timing-sensitive workflows. |
| `user-invocable: false` | Only the agent can invoke. Use for background knowledge (e.g. legacy system context). |
| `allowed-tools` | Pre-approve tools when the skill is active (e.g. `Bash(git:*) Read Grep`). Experimental. |

**Supporting files:** Skills can include scripts, references, and assets alongside `SKILL.md`:

```
my-skill/
├── SKILL.md              # required — frontmatter + instructions
├── references/           # optional — detailed docs loaded on demand
│   └── api-patterns.md
├── scripts/              # optional — executable code
│   └── validate.sh
└── assets/               # optional — templates, schemas
    └── pr-template.md
```

Reference these from `SKILL.md` so the agent knows they exist. They load on demand, not at startup.

**Removing a skill:** Delete the directory. `rm -rf .agent/skills/my-skill`.

#### Skills in the Canonical Event Schema

The `InitEvent.Skills` field carries the union of discovered skills regardless of backend. The SPA populates the skills picker from this list. Skill invocation is sent as a plain text message prefixed with `/` — all backends handle this identically (the user types `/deploy-server`, the session agent sends it as a user message, the CLI expands it).

## Hooks

### Cross-Tool Landscape

No formal standard exists for hooks, but Claude Code's format has become the dominant pattern. Hooks are shell commands that fire on specific lifecycle events, with a stdin/stdout JSON protocol for context and control.

| | Claude Code | Factory Droid | Cursor | Codex | Gemini CLI | Devin CLI |
|---|---|---|---|---|---|---|
| **Config location** | `settings.json` (`hooks:`) | `settings.json` (`hooks:`) | `hooks.json` (standalone) | `codex.json` (`hooks:`) | `settings.json` (JS functions) | N/A |
| **Config format** | JSON array of `{matcher, hooks}` | JSON array of `{matcher, hooks}` | JSON array of `{event, command, ...}` | JSON object per event name | JavaScript function bodies | — |
| **Event naming** | `snake_case` (`pre_tool_use`, `post_tool_use`, `notification`) | `snake_case` (identical to Claude Code) | `camelCase` (`preToolUse`, `postToolUse`, `onNotification`) | `snake_case` (`on_agent_message`, `on_tool_start`) | `snake_case` (`pre_tool_use`, `post_tool_use`) | — |
| **Execution** | Shell command (any language) | Shell command (any language) | Shell command (any language) | Bash only | JavaScript function (in-process) | — |
| **Matchers** | `tool_name`, `tool_input` regex, `event` type | `tool_name`, `tool_input` regex, `event` type | `tool_name`, `tool_input` regex | Event name only (no matchers) | `tool_name` regex | — |
| **Context protocol** | JSON on stdin → JSON on stdout | JSON on stdin → JSON on stdout | JSON on stdin → JSON on stdout | Limited (env vars + args) | Function args (JS objects) | — |
| **Exit code semantics** | 0=proceed, non-0=block | 0=proceed, non-0=block | 0=proceed, non-0=block | 0=proceed, non-0=block | Return value (truthy/falsy) | — |
| **Output control** | `{"decision":"block","reason":"..."}` | `{"decision":"block","reason":"..."}` | `{"action":"block","message":"..."}` | No structured output | Return `{block: true, reason: "..."}` | — |

### Hook Events

| Event | Claude Code | Droid | Cursor | Codex | Gemini CLI |
|---|---|---|---|---|---|
| **Before tool execution** | `pre_tool_use` | `pre_tool_use` | `preToolUse` | `on_tool_start` | `pre_tool_use` |
| **After tool execution** | `post_tool_use` | `post_tool_use` | `postToolUse` | `on_tool_end` | `post_tool_use` |
| **Agent message** | `notification` | `notification` | `onNotification` | `on_agent_message` | — |
| **Session start** | — | — | `onSessionStart` | `on_start` | — |
| **Session end** | — | — | `onSessionEnd` | `on_finish` | — |
| **Stop signal** | `stop` (post-only) | `stop` (post-only) | — | — | — |
| **Subagent spawn** | `subagent` (post-only) | — | — | — | — |

### stdin/stdout Protocol (Claude Code / Droid)

Hooks receive context as JSON on stdin and can respond on stdout:

**Pre-tool-use stdin:**
```json
{
  "hook_type": "pre_tool_use",
  "tool_name": "Bash",
  "tool_input": {"command": "rm -rf /tmp/build"},
  "session_id": "abc123"
}
```

**Pre-tool-use stdout (to block):**
```json
{
  "decision": "block",
  "reason": "Destructive command detected — rm -rf is not allowed"
}
```

**Post-tool-use stdin:**
```json
{
  "hook_type": "post_tool_use",
  "tool_name": "Bash",
  "tool_input": {"command": "npm test"},
  "tool_output": "Tests passed: 42/42",
  "session_id": "abc123"
}
```

Exit code 0 with no stdout = allow. Exit code non-0 = block with stderr as reason.

### Key Observations

1. **Claude Code and Droid are identical** — same config format, same events, same stdin/stdout protocol, same exit code semantics. This is because Droid explicitly adopted Claude Code's hooks format.
2. **Cursor adopted the same wire protocol** but uses a separate `hooks.json` file and `camelCase` event names. Translation is trivial.
3. **Codex is the simplest** — only 5 events, Bash-only execution, no structured output on stdout.
4. **Gemini CLI uses JavaScript** instead of shell commands — hooks are functions in the settings file, not external processes. This is a fundamentally different execution model.
5. **Devin has no hooks system** at all.
6. **No vendor-neutral path exists** — hooks live in tool-specific settings files. There's no `.agent/hooks/` equivalent.

### Hooks in the Pluggable CLI Architecture

Hooks run locally (they're shell commands on the machine running the CLI). In the mclaude architecture:

- **Laptop mode**: Hooks fire inside the CLI process. The session agent doesn't need to know about them — the driver's underlying CLI handles hook execution natively.
- **K8s mode**: Hooks fire inside the session agent pod. The pod's filesystem has the project checkout, so hooks can read project state. Hook configs come from the project's settings file, which the session agent mounts.
- **Cross-backend**: Since Claude Code and Droid use identical formats, hooks written for one work on the other. For Cursor, a thin translation layer (camelCase ↔ snake_case, `decision` ↔ `action`) would enable shared hooks.

The canonical event schema doesn't need hook-specific events — hooks are transparent to the event stream. A `pre_tool_use` hook that blocks a tool call simply prevents the `tool_call` event from being emitted. A `post_tool_use` hook runs after the `tool_result` event.

## Subagents

### Cross-Tool Landscape

No formal standard exists for subagents, but there's tight convergence on markdown + YAML frontmatter format across the major tools. Unlike skills (which have the Agent Skills standard and `.agent/skills/` vendor-neutral path), subagents have no formalized specification and no vendor-neutral discovery path.

| | Claude Code | Factory Droid | Cursor | Codex | Devin CLI |
|---|---|---|---|---|---|
| **Config format** | Markdown + YAML frontmatter | Markdown + YAML frontmatter | Markdown + YAML frontmatter | TOML | N/A |
| **Project path** | `.claude/agents/*.md` | `.factory/droids/*.md` | `.cursor/agents/*.md` | `.codex/agents/*.toml` | — |
| **Personal path** | `~/.claude/agents/*.md` | `~/.factory/droids/*.md` | `~/.cursor/agents/*.md` | `~/.codex/agents/*.toml` | — |
| **Required fields** | `name`, `description` | `name` (description recommended) | `name` (description recommended) | `name`, `description`, `developer_instructions` | — |
| **System prompt** | Markdown body after frontmatter | Markdown body after frontmatter | Markdown body after frontmatter | `developer_instructions` TOML field | — |
| **Model control** | `model: sonnet/opus/haiku/inherit/ID` | `model: inherit` or model ID | `model: fast/inherit/ID` | `model` TOML field | — |
| **Tool restriction** | `tools: Read, Grep, Glob` or `disallowedTools` | `tools: read-only` (category) or array | `readonly: true` | Inherits from config | — |
| **Permission mode** | `permissionMode: acceptEdits/auto/plan/...` | N/A | N/A | `sandbox_mode` TOML field | — |
| **Hooks** | `hooks:` in frontmatter | N/A | N/A | N/A | — |
| **Skills preloading** | `skills: [api-conventions]` | N/A | N/A | `skills.config` TOML | — |
| **MCP servers** | `mcpServers:` in frontmatter | N/A | N/A | `mcp_servers` TOML | — |
| **Memory** | `memory: user/project/local` | N/A | N/A | N/A | — |
| **Background mode** | `background: true` | N/A | `is_background: true` | N/A | — |
| **Isolation** | `isolation: worktree` | N/A | N/A | N/A | — |
| **Cross-tool discovery** | Own path only | Own path + imports from `.claude/agents/` | `.cursor/agents/` + `.claude/agents/` + `.codex/agents/` | Own path only | — |
| **Built-in agents** | Explore, Plan, general-purpose | None | Explore, Bash, Browser | default, worker, explorer | — |
| **Nesting** | No (subagents cannot spawn subagents) | No | Yes (since 2.5, tree of subagents) | Yes (`agents.max_depth`) | — |

### Subagent Frontmatter Fields

The markdown + YAML frontmatter format is shared across Claude Code, Droid, and Cursor. Here's the union of all fields:

| Field | Claude Code | Droid | Cursor | Purpose |
|---|---|---|---|---|
| `name` | Required | Required | Required | Identifier. Lowercase + hyphens. |
| `description` | Required | Recommended | Recommended | When to spawn this agent. Used for auto-selection. |
| `model` | Optional | Optional | Optional | Model alias or ID. `inherit` = use parent's model. |
| `tools` | Optional | Optional | N/A | Allow-list of tools. Can be categories (`read-only`) or specific names. |
| `disallowedTools` | Optional | N/A | N/A | Deny-list of tools (inverse of `tools`). |
| `readonly` | N/A | N/A | Optional | Shorthand for read-only tool access. |
| `permissionMode` | Optional | N/A | N/A | `acceptEdits`, `auto`, `plan`, `bypassPermissions`. |
| `hooks` | Optional | N/A | N/A | Hook overrides specific to this agent. |
| `skills` | Optional | N/A | N/A | Skills to preload when this agent runs. |
| `mcpServers` | Optional | N/A | N/A | MCP servers scoped to this agent. |
| `memory` | Optional | N/A | N/A | Memory scope: `user`, `project`, or `local`. |
| `background` | Optional | N/A | Optional | Run without streaming output to the parent. |
| `isolation` | Optional | N/A | N/A | `worktree` = run in a separate git worktree. |
| `is_background` | N/A | N/A | Optional | Cursor's equivalent of `background`. |

### Key Observations

1. **Three of four use the same format** — Claude Code, Droid, and Cursor all use markdown + YAML frontmatter with the markdown body as the system prompt. Codex is the outlier with TOML.
2. **Claude Code is the richest** — it has hooks, skills preloading, MCP server scoping, persistent memory, worktree isolation, and permission modes per subagent. Droid and Cursor are much simpler (name + description + model + tools).
3. **Cursor does cross-tool discovery** — it scans `.cursor/agents/`, `.claude/agents/`, AND `.codex/agents/`. This is the most aggressive interop approach.
4. **Droid imports from Claude Code** — the `/droids` command can import from `.claude/agents/` with automatic tool/model mapping, acknowledging Claude Code as the upstream format.
5. **No vendor-neutral path** — unlike skills where `.agent/skills/` is the standard, there's no `.agent/agents/` path. Each tool looks in its own directory (with Cursor scanning others as a courtesy).
6. **Devin has no subagent system** — consistent with its lack of hooks.
7. **Nesting varies** — Claude Code and Droid prohibit subagent nesting (flat hierarchy). Cursor (since 2.5) and Codex allow tree-shaped subagent hierarchies with configurable depth limits.

### Subagents in the Pluggable CLI Architecture

Subagents are managed by the CLI backend, not the session agent. The driver doesn't need to understand subagent internals — it just needs to relay the events:

- **Claude Code**: Subagent events carry `parentToolUseId` — the canonical `MessageEvent` and `ToolCallEvent` already have this field. The SPA can render nested agent trees.
- **Droid**: Missions are Droid's equivalent of complex multi-agent work, but they're architecturally different (server-side orchestration, not client-side spawning). Mission events go through `backendSpecific`.
- **Cursor**: Tree-shaped subagent hierarchies would need `parentAgentId` tracking if we ever build a Cursor driver.
- **Devin/Codex**: No subagent streaming events to handle.

The `CLICapabilities.HasSubagents` flag tells the SPA whether to show subagent nesting in the conversation view. Only `claude_code` sets this to `true` currently.

### Cross-Tool Subagent Compatibility

For projects that want subagents to work across multiple tools, the pragmatic approach is:

1. **Write agents in Claude Code format** (`.claude/agents/*.md`) — it's the richest and most widely recognized.
2. **Cursor picks them up automatically** via cross-tool discovery.
3. **Droid imports them** via its `.claude/agents/` compatibility path.
4. **Codex requires manual conversion** to TOML — the format gap is too large for automatic translation.

If a vendor-neutral `.agent/agents/` path emerges (following the Agent Skills precedent), migration would be straightforward since the file format is already identical across Claude Code, Droid, and Cursor.

## Message Envelope

All NATS messages in the system — session events, user inputs, provisioning commands, credential requests, KV payloads — carry a standard envelope:

```go
type Envelope struct {
    ID string `json:"id"` // ULID: globally unique, time-ordered, lexicographically sortable
    TS int64  `json:"ts"` // Unix milliseconds, publisher wall clock
}
```

**ID format: ULID** (Universally Unique Lexicographically Sortable Identifier). 26 characters, base32-encoded. Time-ordered: `ORDER BY id` = `ORDER BY time`. 80 bits of randomness per millisecond — collision-safe without coordination. Generated by the publisher (SPA, agent, CP) at publish time.

Example: `"01JTRK8M4G3XQVN5P2WYZ7ABCD"`

| Design choice | Decision | Rationale |
|---------------|----------|-----------|
| ID format | ULID over UUIDv4 | Lexicographic sort = chronological. Postgres btree indexes on `id` are also time indexes. 26 chars vs 36. |
| ID generation | Publisher-side | No coordination needed. Each publisher generates independently. |
| ID scope | Per-message (not per-session, not per-user) | Every NATS message gets its own ID. A session event, a user input, a credential refresh response — each has a unique ULID. |
| Timestamp source | Publisher wall clock | Not NATS server time. The publisher knows when it produced the message. Clock skew between publishers is acceptable — `id` embeds time as well (ULID's time component), providing a consistent ordering within a single publisher. |

**Implementation phase:** Envelope generation is added in **Phase 3** (session agent refactor). The session agent wraps each `SessionEvent` with a ULID `id` and `ts` before publishing to NATS. During Phase 2 (ClaudeCodeDriver), the driver emits `SessionEvent` structs without IDs — the session agent's publish path assigns them. This keeps envelope logic in one place. **Backward compatibility:** The current code publishes raw Claude Code stream-json lines to NATS without envelopes. During migration (Phases 2-3 overlap), the SPA must handle both formats: if `id` is present, it's a canonical `SessionEvent`; if absent, it's a legacy raw stream-json line. The SPA's event accumulator checks for the `type` field shape to distinguish. Once all session agents are upgraded (Phase 3 complete), raw stream-json is no longer published.

**CQRS correlation:** When a query/search layer materializes stream messages to Postgres, `id` becomes the primary key. Search results return `{id, snippet, ...}` and the SPA can locate the exact message in the conversation. Without this, the only correlator is the NATS stream sequence number, which is stream-specific and doesn't survive stream recreation.

**Deduplication:** If a consumer replays messages (ordered push consumer reset), the `id` enables idempotent processing — skip messages with already-seen IDs.

**Libraries:** Go: `oklog/ulid`. TypeScript: `ulid`. Both are dependency-free and well-maintained.

## Canonical Event Schema

The driver translates native protocol events into a canonical schema that flows through NATS. This is NOT Claude Code's stream-json — it's a superset designed to represent all three protocols.

```go
// SessionEvent is the canonical event published to NATS.
// Drivers translate their native protocol into this schema.
// Embeds Envelope (id + ts on every message).
type SessionEvent struct {
    ID        string    `json:"id"`        // ULID
    TS        int64     `json:"ts"`        // Unix millis
    Type      EventType `json:"type"`
    SessionID string    `json:"sessionId"`

    // Populated based on Type:
    Init          *InitEvent          `json:"init,omitempty"`
    StateChange   *StateChangeEvent   `json:"stateChange,omitempty"`
    TextDelta     *TextDeltaEvent     `json:"textDelta,omitempty"`
    ThinkingDelta *ThinkingDeltaEvent `json:"thinkingDelta,omitempty"`
    Message       *MessageEvent       `json:"message,omitempty"`
    ToolCall      *ToolCallEvent      `json:"toolCall,omitempty"`
    ToolProgress  *ToolProgressEvent  `json:"toolProgress,omitempty"`
    ToolResult    *ToolResultEvent    `json:"toolResult,omitempty"`
    Permission       *PermissionEvent    `json:"permission,omitempty"`
    TurnComplete     *TurnCompleteEvent  `json:"turnComplete,omitempty"`
    Error            *ErrorEvent         `json:"error,omitempty"`
    BackendSpecific  json.RawMessage     `json:"backendSpecific,omitempty"` // opaque payload for backend-unique features (e.g. Droid missions)
}

type EventType string
const (
    EventInit          EventType = "init"
    EventStateChange   EventType = "state_change"
    EventTextDelta     EventType = "text_delta"
    EventThinkingDelta EventType = "thinking_delta"
    EventMessage       EventType = "message"       // complete assistant/user message
    EventToolCall      EventType = "tool_call"      // tool invocation started
    EventToolProgress  EventType = "tool_progress"
    EventToolResult    EventType = "tool_result"
    EventPermission    EventType = "permission"     // permission request or resolution
    EventTurnComplete     EventType = "turn_complete"
    EventError            EventType = "error"
    EventBackendSpecific  EventType = "backend_specific"  // opaque backend-unique events (e.g. Droid missions)
)

type InitEvent struct {
    Backend      CLIBackend         `json:"backend"`
    Model        string             `json:"model"`
    Tools        []string           `json:"tools"`
    Skills       []string           `json:"skills,omitempty"`
    Agents       []string           `json:"agents,omitempty"`
    Capabilities CLICapabilities `json:"capabilities"`
}

// SessionStatus enumerates all session lifecycle states.
// This enum aligns with the `status` field in `spec-nats-payload-schema.md`
// (KV_mclaude-sessions-{uslug} section) and ADR-0044 (which renames the JSON
// field from `state` to `status`).
//
// Quota-managed states (pending, paused, needs_spec_fix) per ADR-0044.
//
// Migration note: The current code (`state.go`) uses a `State string` field
// with values {idle, running, requires_action, updating, restarting, failed,
// plan_mode, waiting_for_input, unknown} per `spec-state-schema.md`. The
// rename from `state` to `status` and the extended value set below will be
// deployed as part of the ADR-0054 clean cut-over (all components simultaneously,
// no rolling upgrade). Until then, the current `state` values remain in code.
// `idle` maps to the between-turns state; `updating` and `restarting` are
// transient operational states not in the target enum (handled via lifecycle
// events instead).
type SessionStatus string
const (
    StatusPending        SessionStatus = "pending"         // process alive, awaiting prompt delivery (quota gate)
    StatusRunning        SessionStatus = "running"         // actively processing (includes idle between turns — see note)
    StatusPaused         SessionStatus = "paused"          // quota-paused (soft or hard), subprocess alive
    StatusRequiresAction SessionStatus = "requires_action" // blocked on permission or user input
    StatusCompleted      SessionStatus = "completed"       // finished successfully
    StatusStopped        SessionStatus = "stopped"         // stopped by user (not cancelled)
    StatusCancelled      SessionStatus = "cancelled"       // cancelled, subprocess reaped
    StatusNeedsSpecFix   SessionStatus = "needs_spec_fix"  // permission-denied → spec fix needed
    StatusFailed         SessionStatus = "failed"          // unrecoverable error
    StatusError          SessionStatus = "error"           // transient error, may recover
)
// Note on `idle` vs `running`: The current code distinguishes `idle` (waiting
// for user input between turns) from `running` (actively processing). In the
// target status enum, both map to `running` — the session is alive and the CLI
// process is active. The `state_change` *event* still carries fine-grained
// driver state (idle/running/requires_action) for real-time SPA updates; the
// KV `status` field tracks coarser lifecycle state.

// StateChangeEvent carries real-time driver state for SPA rendering.
// These are fine-grained states emitted by the driver as the CLI backend
// transitions. They are distinct from the coarser KV `status` field above.
type StateChangeEvent struct {
    State string `json:"state"` // "idle" | "running" | "requires_action"
}

type TextDeltaEvent struct {
    MessageID  string `json:"messageId"`
    BlockIndex int    `json:"blockIndex,omitempty"` // see "BlockIndex synthesis" below
    Text       string `json:"text"`
    Encoding   string `json:"encoding,omitempty"` // "plain" (default, UTF-8) or "base64" (GenericTerminalDriver PTY output). Omitted for structured backends.
}

type ThinkingDeltaEvent struct {
    MessageID  string `json:"messageId"`
    BlockIndex int    `json:"blockIndex,omitempty"` // see "BlockIndex synthesis" below
    Text       string `json:"text"`
}

// BlockIndex synthesis rules (per backend):
//
// - Claude Code: The stream-json protocol provides `block_index` natively on
//   content_block_delta events. The ClaudeCodeDriver passes it through.
//
// - Droid: `assistant_text_delta` and `thinking_text_delta` notifications do
//   not carry a block index. The DroidDriver maintains a per-message counter:
//   each new content block type transition (text→thinking, thinking→text, or
//   new tool_use) increments the counter. Within a contiguous run of the same
//   block type, `blockIndex` stays constant. Resets to 0 on each new messageId.
//
// - Devin ACP: `session/update` content_chunk events carry a `sequenceId`
//   per update but no block index. The DevinDriver uses the same per-message
//   counter strategy as DroidDriver.
//
// - GenericTerminalDriver: Always 0 (single continuous output stream).
//
// The SPA's event accumulator uses (messageId, blockIndex) as a composite key
// to append deltas to the correct content block. If blockIndex is 0 or omitted,
// the SPA appends to the first (and typically only) block for that messageId.

type MessageEvent struct {
    MessageID       string        `json:"messageId"`
    Role            string        `json:"role"` // assistant, user
    Content         []ContentBlock `json:"content"`
    ParentToolUseID string        `json:"parentToolUseId,omitempty"`
}

type ToolCallEvent struct {
    ToolUseID  string                 `json:"toolUseId"`
    ToolName   string                 `json:"toolName"`
    Input      map[string]interface{} `json:"input"`
    MessageID  string                 `json:"messageId,omitempty"`
}

type ToolProgressEvent struct {
    ToolUseID   string `json:"toolUseId"`
    ToolName    string `json:"toolName"`
    ElapsedSecs int    `json:"elapsedSecs,omitempty"`
    Content     string `json:"content,omitempty"`
}

type ToolResultEvent struct {
    ToolUseID string `json:"toolUseId"`
    ToolName  string `json:"toolName"`
    Content   string `json:"content"` // stringified
    IsError   bool   `json:"isError"`
}

type PermissionEvent struct {
    RequestID string `json:"requestId"`
    ToolName  string `json:"toolName"`
    ToolInput string `json:"toolInput,omitempty"` // summary
    Resolved  bool   `json:"resolved"`
    Allowed   bool   `json:"allowed,omitempty"`
}

type TurnCompleteEvent struct {
    InputTokens  int     `json:"inputTokens,omitempty"`
    OutputTokens int     `json:"outputTokens,omitempty"`
    CostUSD      float64 `json:"costUsd,omitempty"`
    DurationMs   int     `json:"durationMs,omitempty"`
}

type ErrorEvent struct {
    Message   string `json:"message"`
    Code      string `json:"code,omitempty"`
    Retryable bool   `json:"retryable,omitempty"` // Whether the agent will automatically retry this operation
}
```

### Mapping: Claude Code stream-json -> Canonical

| Claude Code event | Canonical event |
|---|---|
| `system.init` | `init` |
| `system.session_state_changed` | `state_change` |
| `stream` (content_block_delta, text) | `text_delta` |
| `stream` (content_block_delta, thinking) | `thinking_delta` |
| `assistant` (complete message) | `message` + extracted `tool_call` per tool_use block |
| `tool_progress` | `tool_progress` |
| user message with `tool_result` blocks | `tool_result` per block |
| `sdk_control_request.permission` | `permission` (resolved=false) |
| `control_response` to permission | `permission` (resolved=true) |
| `result` | `turn_complete` |

### Mapping: Droid stream-jsonrpc -> Canonical

| Droid notification | Canonical event |
|---|---|
| Session init notification | `init` |
| `working_state_changed` | `state_change` (map `Idle`->`idle`, `Working`->`running`, `WaitingForPermission`->`requires_action`) |
| `assistant_text_delta` | `text_delta` |
| `thinking_text_delta` | `thinking_delta` |
| `create_message` | `message` + extracted `tool_call` per tool_use block |
| `tool_use` (standalone) | `tool_call` |
| `tool_progress` | `tool_progress` |
| `tool_result` | `tool_result` |
| `droid.request_permission` (server->client) | `permission` (resolved=false) |
| `permission_resolved` | `permission` (resolved=true) |
| `turn_complete` (SDK-synthesized) | `turn_complete` |
| `token_usage_update` | fold into KV state, emit on turn_complete |

### Mapping: Devin ACP -> Canonical

| ACP message | Canonical event |
|---|---|
| `session/new` response | `init` |
| Inferred from update flow | `state_change` |
| `session/update` content_chunk (role=agent) | `text_delta` |
| `session/update` content_chunk (role=thought) | `thinking_delta` |
| `session/update` content (complete) | `message` |
| `session/update` tool_call | `tool_call` |
| `session/update` tool_call_update (running) | `tool_progress` |
| `session/update` tool_call_update (completed) | `tool_result` |
| `session/request_permission` | `permission` (resolved=false) |
| Client responds to request_permission | `permission` (resolved=true) |
| `session/prompt` response | `turn_complete` |

## CLIDriver Interface

```go
// CLIDriver owns the full lifecycle of a CLI backend process.
// It speaks the backend's native protocol and emits canonical events.
type CLIDriver interface {
    // Identity
    Backend() CLIBackend
    DisplayName() string
    Capabilities() CLICapabilities

    // Process lifecycle
    Launch(ctx context.Context, opts LaunchOptions) (*Process, error)
    Resume(ctx context.Context, sessionID string, opts LaunchOptions) (*Process, error)

    // Input (translated to native protocol)
    SendMessage(proc *Process, msg UserMessage) error
    SendPermissionResponse(proc *Process, requestID string, allow bool) error
    UpdateConfig(proc *Process, cfg SessionConfig) error
    Interrupt(proc *Process) error

    // Output (native protocol -> canonical events)
    // ReadEvents blocks, reading the process stdout and emitting
    // canonical SessionEvents on the channel until the process exits.
    ReadEvents(proc *Process, out chan<- SessionEvent) error
}

type LaunchOptions struct {
    CWD            string
    SessionID      string
    Model          string
    SystemPrompt   string
    PermissionMode string // auto, managed, allowlist
    EnvVars        map[string]string
}

type SessionConfig struct {
    Model          *string `json:"model,omitempty"`          // nil = no change
    PermissionMode *string `json:"permissionMode,omitempty"` // nil = no change
    SystemPrompt   *string `json:"systemPrompt,omitempty"`   // nil = no change
}

type Process struct {
    Cmd    *exec.Cmd
    Stdin  io.WriteCloser
    Stdout io.ReadCloser
    Stderr io.ReadCloser
    PID    int
}

type UserMessage struct {
    Text        string
    Attachments []ResolvedAttachment // binary data already downloaded from S3 by session agent
}

type ResolvedAttachment struct {
    Filename string
    MimeType string
    Data     []byte // raw bytes — agent resolved AttachmentRef via S3 pre-signed URL before forwarding to driver
}
```

## CLIBackend Enum and Capabilities

```go
type CLIBackend string
const (
    BackendClaudeCode       CLIBackend = "claude_code"
    BackendDroid            CLIBackend = "droid"
    BackendDevinACP         CLIBackend = "devin_acp"
    BackendGemini           CLIBackend = "gemini"
    BackendGenericTerminal  CLIBackend = "generic_terminal"
)

// CLICapabilities contains boolean feature flags for the backend.
// Note: The actual Skills, Tools, and Agents lists are NOT in CLICapabilities —
// they are populated in the `init` event's InitEvent struct (Tools, Skills, Agents fields).
// CLICapabilities is strictly about boolean feature flags and UI configuration hints.
// The init event carries the full inventory; CLICapabilities tells the SPA which
// UI features to enable/disable.
type CLICapabilities struct {
    HasThinking       bool              `json:"hasThinking"`
    HasSubagents      bool              `json:"hasSubagents"`
    HasSkills         bool              `json:"hasSkills"`
    HasPlanMode       bool              `json:"hasPlanMode"`
    HasMissions       bool              `json:"hasMissions"`
    HasEventStream    bool              `json:"hasEventStream"`
    HasSessionResume  bool              `json:"hasSessionResume"`
    ToolIcons         map[string]string `json:"toolIcons,omitempty"`
    ThinkingLabel     string            `json:"thinkingLabel"`
    ModelOptions      []string          `json:"modelOptions,omitempty"`
    PermissionModes   []string          `json:"permissionModes,omitempty"`
}
```

### Per-Backend Capabilities

| Capability | Claude Code | Droid | Devin ACP |
|---|---|---|---|
| `hasThinking` | true | true | true (role=thought) |
| `hasSubagents` | true (`parent_tool_use_id`) | false (missions are different) | false |
| `hasSkills` | true (`/commit`, `/review`) | true (skills) | true (slash commands) |
| `hasPlanMode` | false | true (spec mode) | true (`/plan` mode) |
| `hasMissions` | false | true | false |
| `hasEventStream` | true | true | true |
| `hasSessionResume` | true (`--resume`) | true | true (`session/load`) |
| `thinkingLabel` | "Thinking" | "Thinking" | "Thinking" |

## Driver Implementations

### ClaudeCodeDriver

Spawns: `claude --print --verbose --output-format stream-json --input-format stream-json --include-partial-messages --session-id {id}`

Resume: `claude --print --verbose --output-format stream-json --input-format stream-json --include-partial-messages --resume {id}`

Input: Write NDJSON lines to stdin (`{"type":"user","message":{...}}`, `{"type":"control_response",...}`)

Output: Parse NDJSON lines from stdout, translate each `type` to canonical `SessionEvent`.

Permission: Use `--permission-prompt-tool stdio` to route permissions through the control protocol. Map `sdk_control_request.permission` to canonical `permission` event, forward client response as `control_response`.

State: Direct mapping from `session_state_changed` events.

### DroidDriver

Spawns: `droid exec --input-format stream-jsonrpc --output-format stream-jsonrpc --auto low`

Input: JSON-RPC requests — `droid.create_session`, `droid.send_message`, `droid.interrupt`.

Output: Parse JSON-RPC notifications, translate 22 notification types to canonical events. Use `StreamStateTracker` pattern (from Droid SDK) to synthesize `turn_complete` from `working_state_changed` idle transitions.

Permission: Handle `droid.request_permission` server->client RPC. Respond with `selectedOption`.

State: Map `DroidWorkingState` enum (`Idle`/`Working`/`WaitingForPermission`/`WaitingForAskUser`) to canonical `idle`/`running`/`requires_action`.

Droid-specific: `mission_*` notifications (worker started/completed, progress, heartbeat) emit as pass-through events with `type: "backend_specific"` — the SPA handles these per-backend.

### DevinDriver

Spawns: `devin acp` (ACP mode over stdio)

Input: JSON-RPC requests — `initialize`, `session/new`, `session/prompt`, `session/cancel`.

Output: Parse `session/update` notifications. Map `content_chunk` to `text_delta`/`thinking_delta` based on role. Map `tool_call`/`tool_call_update` to `tool_call`/`tool_progress`/`tool_result`.

Permission: Implement `session/request_permission` as a client-side method handler. Map to canonical `permission` event. Route client response back via JSON-RPC response.

State: Devin ACP doesn't emit explicit state change events. Infer state:
- `running` when `session/prompt` is in-flight
- `requires_action` when `session/request_permission` is pending
- `idle` when `session/prompt` response is received

ACP extras: Devin exposes `fs/read_text_file`, `fs/write_text_file`, `terminal/create` as client-side capabilities. The session agent implements these (it has filesystem access), making Devin's tools work through the session agent's filesystem rather than Devin's own.

### GenericTerminalDriver

Fallback for unrecognized CLIs. No structured protocol — uses PTY heuristics.

- `hasEventStream: false` — SPA shows Terminal tab only, no conversation view
- State detection: idle = no stdout for N seconds, running = recent output
- No permission handling, no tool tracking
- Launch: spawn the CLI binary in a PTY via `creack/pty`

**PTY data flow:** The driver allocates a PTY pair via `creack/pty`. Raw bytes read from the PTY master are published as `text_delta` events. Each `text_delta` includes an `encoding` field:

- **`"plain"`** (default): UTF-8 text. Used when the PTY read chunk passes `utf8.Valid()`. ANSI escape sequences are valid UTF-8, so terminal control codes (colors, cursor movement) are sent as `"plain"`. The SPA's terminal emulator (e.g., xterm.js) handles ANSI parsing.
- **`"base64"`**: Base64-encoded binary. Used when the PTY read chunk fails `utf8.Valid()` — i.e., it contains byte sequences that are not valid UTF-8 (e.g., binary file output piped to stdout, corrupted stream). The SPA decodes from base64 before feeding to the terminal emulator.
- **Omitted / empty**: Consumers treat omitted `encoding` as `"plain"` (backward compat default). Structured backends (ClaudeCodeDriver, DroidDriver, DevinDriver) never set `encoding` — their text is always valid UTF-8.

The driver reads PTY output in chunks (up to 4KB per read). Each chunk is published as one `text_delta` event. The encoding decision is per-chunk: a chunk is either entirely `"plain"` or entirely `"base64"`.

User input arriving on `sessions.{sslug}.input` (type `message`) is written directly to the PTY master's stdin. Interrupts (type `interrupt`) send SIGINT to the PTY process group. No tool/permission/thinking events are emitted — the event stream is strictly raw I/O.

## Session Agent Changes

The session agent's core loop becomes driver-agnostic:

```go
func (sa *SessionAgent) runSession(driver CLIDriver, sessionID string, opts LaunchOptions) {
    proc, err := driver.Launch(ctx, opts)
    // ...

    events := make(chan SessionEvent, 256)
    go driver.ReadEvents(proc, events)

    // NATS input (.input subject) -> driver
    go func() {
        for msg := range inputSub.Chan() {
            switch parseInputType(msg.Data) {
            case "message":
                driver.SendMessage(proc, parseUserMessage(msg.Data))
            case "permission_response":
                driver.SendPermissionResponse(proc, ...)
            }
        }
    }()

    // NATS config (.config subject) -> driver
    go func() {
        for msg := range configSub.Chan() {
            driver.UpdateConfig(proc, parseSessionConfig(msg.Data))
        }
    }()

    // Driver events -> NATS
    for event := range events {
        raw, _ := json.Marshal(event)
        nats.Publish(eventsSubject, raw)

        // Side effects
        switch event.Type {
        case EventStateChange:
            updateKV(event)
        case EventPermission:
            if !event.Permission.Resolved {
                addPendingControl(event)
            } else {
                removePendingControl(event)
            }
        case EventTurnComplete:
            accumulateUsage(event)
        case EventInit:
            cacheCapabilities(event)
        }
    }
}
```

## NATS Subject Structure

Events on NATS are canonical `SessionEvent` JSON — not raw backend protocol. Subjects use the ADR-0024/ADR-0035 typed-slug format consolidated by ADR-0054:

```
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.create                     — create new session (SPA/CLI → agent)
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.events              — session event stream (agent → SPA)
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.input               — user input (SPA → agent)
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.delete              — delete session (SPA → agent)
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.config              — update session config (SPA → agent)
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.control.interrupt   — interrupt session (SPA → agent)
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.control.restart     — restart session (SPA → agent)
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.lifecycle.started   — session started (agent → SPA)
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.lifecycle.stopped   — session stopped (agent → SPA)
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.lifecycle.error     — session error (agent → SPA)
```

**JetStream resources (per ADR-0054):**

| Resource | Name | Scope | Key/Subject format |
|----------|------|-------|-------------------|
| Session stream | `MCLAUDE_SESSIONS_{uslug}` | Per-user | Captures `mclaude.users.{uslug}.hosts.*.projects.*.sessions.>` |
| Sessions KV bucket | `KV_mclaude-sessions-{uslug}` | Per-user | Key: `hosts.{hslug}.projects.{pslug}.sessions.{sslug}` |
| Projects KV bucket | `KV_mclaude-projects-{uslug}` | Per-user | Key: `hosts.{hslug}.projects.{pslug}` |
| Hosts KV bucket | `KV_mclaude-hosts` | Shared | Key: `{hslug}` |

Note: The user slug is encoded in the resource name (bucket/stream), not in the key. See `spec-nats-data-taxonomy.md` for the full naming derivation and `adr-0054-nats-jetstream-permission-tightening.md` for permission scoping.

**Current code uses a different scheme.** The current implementation uses shared KV buckets (`mclaude-sessions`, `mclaude-projects`) with key format `{uslug}.{hslug}.{pslug}.{sslug}` and shared streams (`MCLAUDE_EVENTS`, `MCLAUDE_API`, `MCLAUDE_LIFECYCLE`). The migration to per-user resources is tracked by ADR-0054 and will be deployed as a clean cut-over (all components simultaneously). The session agent's driver-agnostic loop (this ADR) should be implemented against the ADR-0054 target scheme from the start, using the subject helpers in `mclaude-common/pkg/subj/subj.go` (which will be updated to generate per-user resource names when ADR-0054 lands).

Clients parse canonical events. The `init` event tells the client which backend is active and what capabilities are available, enabling per-backend UI adaptation.

## Data Model Changes

**KV bucket and key format (per ADR-0054 and `spec-nats-data-taxonomy.md`):**

Session state is stored in the per-user KV bucket `KV_mclaude-sessions-{uslug}`. The user slug is in the bucket name, not the key. Key format:

```
hosts.{hslug}.projects.{pslug}.sessions.{sslug}
```

Example: `hosts.laptop-a.projects.myapp.sessions.sess-001` in bucket `KV_mclaude-sessions-alice`.

Note: The current code uses shared bucket `mclaude-sessions` with key format `{uslug}.{hslug}.{pslug}.{sslug}` (user slug in the key). The migration to per-user buckets is part of the ADR-0054 cut-over. Subject helpers in `subj.go` will be updated accordingly.

**New fields on session KV entry:**

- Add `CLIBackend` enum to shared models
- Add `backend: CLIBackend` field to session KV state (defaults to `claude_code`)
- Add `capabilities: CLICapabilities` to session KV state (populated from `init` event — boolean feature flags only; see below)
- Add `tools`, `skills`, `agents` as **top-level** fields on the session KV entry (migrated from the old nested `capabilities.{tools,skills,agents}` struct). The `InitEvent` carries these lists at init time; the agent copies them to KV for SPA consumption without requiring event replay.
- Session list includes `backend` field so UI can show backend badges
- Backward compat: `backend` field optional — old clients ignore it

**Capabilities struct migration:** The existing `Capabilities` struct in `state.go` (`{skills, tools, agents}`) is replaced by two things: (1) `CLICapabilities` (boolean feature flags — `hasThinking`, `hasSubagents`, etc.) stored under `capabilities` in the KV JSON, and (2) the `tools`, `skills`, `agents` arrays promoted to top-level fields in the KV JSON. This avoids breaking existing SPA code that reads `capabilities.skills` — the SPA migration path is: first check top-level `tools`/`skills`/`agents`, fall back to nested `capabilities.skills` during the transition. `CLICapabilities` is the Go struct; `capabilities` is its JSON key. The Go struct does NOT contain `Tools`/`Skills`/`Agents` — those are in `InitEvent` and in the top-level KV fields.

## Implementation Phases

### Phase 1: Canonical Event Schema + CLIDriver Interface
- Define `SessionEvent`, `EventType`, all event structs in `events.go`
- Define `CLIDriver` interface, `CLIBackend`, `CLICapabilities` in `driver.go`
- Define `DriverRegistry` that holds registered drivers
- **Size**: Small. No behavior change.

### Phase 2: ClaudeCodeDriver
- Implement `CLIDriver` for Claude Code
- Move all Claude-specific protocol knowledge into this driver:
  - NDJSON parsing (stream-json -> canonical events)
  - stdin message construction
  - Permission protocol (`sdk_control_request` <-> `control_response`)
  - State mapping (`session_state_changed` -> canonical `state_change`)
  - Launch/resume commands
- **Size**: Medium. This is the bulk of the refactoring — extracting existing logic from the session agent into a driver.

### Phase 3: Refactor Session Agent
- Session agent's core loop becomes the driver-agnostic pattern above
- Accept `CLIDriver` from registry based on project/session config
- `ReadEvents` channel replaces direct NDJSON scanning
- Permission routing goes through driver instead of raw stdin writes
- **Size**: Medium. Requires touching the core loop, but the logic moves into the driver — net code stays similar.

### Phase 4: DroidDriver
- Implement `CLIDriver` for Factory Droid
- JSON-RPC 2.0 framing (envelope construction, notification parsing)
- Map 22 notification types to canonical events
- `StreamStateTracker` for turn_complete synthesis
- Permission: `droid.request_permission` handler
- Pass-through for mission events
- **Size**: Medium. New code, but the canonical event mapping is well-defined.

### Phase 5: DevinDriver
- Implement `CLIDriver` for Devin ACP
- ACP JSON-RPC client (initialize, session/new, session/prompt)
- `session/update` notification parser (content_chunk, tool_call, tool_call_update, plan)
- `session/request_permission` handler
- State inference (no explicit state events in ACP)
- Implement client-side capabilities (fs/read_text_file, terminal/create) if Devin requests them
- **Size**: Medium-large. ACP is the most complex protocol to implement client-side.

### Phase 6: API + Client Adaptation
- `init` event carries `backend` and `capabilities`
- SPA reads capabilities to toggle UI features per backend
- Tool icons, thinking labels, feature toggles driven by capabilities
- Backend badge on session rows
- Permission UI works for all three backends (same canonical `permission` events)
- **Size**: Medium.

### Phase 7: GenericTerminalDriver
- PTY-based fallback for unrecognized CLIs
- Heuristic idle detection
- No event stream — Terminal tab only
- **Size**: Small.

## Dependency Graph

```
Phase 1 ──> Phase 2 ──> Phase 3 ──> Phase 4
                                 ├──> Phase 5
                                 ├──> Phase 6
                                 └──> Phase 7
```

Phases 1-3 are the critical path. Phase 2 (ClaudeCodeDriver) is the largest single piece — it's refactoring existing behavior, not writing new features. Once Phase 3 lands, Phases 4-7 are independent and can be done in parallel.

## Key Design Decisions

### Why a canonical schema instead of raw passthrough?

The K8s plan originally proposed raw Claude Code stream-json as the canonical event schema on NATS. This doesn't work when:
- Droid uses JSON-RPC 2.0 envelopes with different event names and semantics
- Devin ACP uses a completely different method/notification structure
- The SPA would need per-backend parsers for every event type

A canonical schema means: one parser in the SPA, one KV state updater in the session agent, one set of NATS subjects. The driver absorbs all protocol complexity.

### Why not just adopt ACP as the canonical protocol?

ACP is the closest thing to a standard (Zed, Cursor, Devin, Gemini CLI are adopting it). But:
- Claude Code doesn't speak ACP and is unlikely to any time soon (see anthropics/claude-code#6686)
- ACP lacks some features we need (subagent nesting, mission tracking)
- ACP is still evolving (draft RFDs for cancellation, MCP-over-ACP)
- The canonical schema is simpler than ACP — it's a flat event stream, not a bidirectional RPC protocol

If ACP stabilizes and Claude Code adopts it, the canonical schema could converge toward ACP. The driver layer makes this migration incremental.

### Backend-specific events

Some backends have unique features (Droid missions, Devin plans). These emit as:
```go
type SessionEvent struct {
    // ...
    BackendSpecific json.RawMessage `json:"backendSpecific,omitempty"`
}
```

The SPA checks `init.backend` and conditionally renders backend-specific UI. This avoids polluting the canonical schema with every backend's features.

### CLIs without event streams (GenericTerminalDriver)

Driver returns `HasEventStream: false`. The SPA shows only the Terminal tab (PTY stream via NATS). No conversation view, no tool tracking, no permission UI. This still useful — it means mclaude can manage any CLI process, even if it can't understand the conversation.

### Process discovery (laptop mode)

On a laptop, the session agent spawns CLI processes itself — no discovery needed. The driver's `Launch()`/`Resume()` methods handle the exact CLI invocation. The old TmuxMonitor process-scanning approach is replaced entirely by the session agent model (see K8s plan).

### Driver registration

```go
registry := NewDriverRegistry()
registry.Register(NewClaudeCodeDriver())
registry.Register(NewDroidDriver())
registry.Register(NewDevinDriver())
registry.Register(NewGenericTerminalDriver())
```

Session create requests specify the backend. The registry looks up the driver. If no backend specified, default to `claude_code` for backward compat.

**`sessions.create` payload for scheduled sessions:** When `sessions.create` includes quota fields, all quota fields are **top-level** in the JSON payload — not nested under a `quotaMonitor` object. This matches ADR-0044 which explicitly supersedes the nested `quotaMonitor: {...}` shape in `spec-nats-payload-schema.md`. The flat fields are: `prompt`, `branchSlug`, `softThreshold`, `hardHeadroomTokens`, `autoContinue`, `resumePrompt`, `permPolicy`, `allowedTools`, `resumeClaudeSessionID`. See ADR-0044 "Creating a Scheduled Session" for the canonical payload shape. Note: The current code's `QuotaMonitorConfig` struct in `state.go` uses the old nested shape with different field names (`Threshold`, `Priority`, `JobID`); this will be replaced when ADR-0044 is implemented.

## Session Input Schema

User inputs arrive on per-session subject `sessions.{sslug}.input` (full path: `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.input`) and are typed commands, not raw text. The driver translates each input type into the backend's native format.

**Current code vs target:** The current code uses a project-scoped multiplexed subject `…api.sessions.input` (see `subj.UserHostProjectAPISessionsInput()` in `subj.go`), where the session ID is carried inside the payload. The target architecture (this ADR + ADR-0054 + `spec-nats-data-taxonomy.md`) uses per-session subjects with `{sslug}` in the subject path. The migration happens as part of the ADR-0054 cut-over. The per-session subject design enables finer-grained NATS stream filtering (consumers can subscribe to a single session's input) and aligns with the per-session `.events`, `.delete`, `.config`, and `.control.*` subjects.

```go
type SessionInput struct {
    ID          string          `json:"id"`          // ULID
    TS          int64           `json:"ts"`          // Unix millis
    Type        InputType       `json:"type"`
    Text        string          `json:"text,omitempty"`
    Attachments []AttachmentRef `json:"attachments,omitempty"`
    SkillName   string          `json:"skillName,omitempty"`
    Args        json.RawMessage `json:"args,omitempty"`
    RequestID   string          `json:"requestId,omitempty"`
    Allowed     bool            `json:"allowed,omitempty"`
    Behavior    string          `json:"behavior,omitempty"` // allow_always, deny_always
}

type InputType string
const (
    InputMessage            InputType = "message"
    InputSkillInvoke        InputType = "skill_invoke"
    InputPermissionResponse InputType = "permission_response"
)
// Note: Interrupt, restart, and config are NOT InputTypes. They arrive on
// separate NATS subjects (sessions.{sslug}.control.interrupt,
// .control.restart, and .config respectively) with their own payload schemas
// (see "NATS Subject Structure"). The agent's input dispatcher only processes
// messages from sessions.{sslug}.input using the InputType enum above.
// Interrupt, restart, and config are handled by separate NATS subscriptions
// on their dedicated subjects.

type AttachmentRef struct {
    ID        string `json:"id"`        // opaque S3 reference
    Filename  string `json:"filename"`
    MimeType  string `json:"mimeType"`
    SizeBytes int64  `json:"sizeBytes"`
    S3Key     string `json:"s3Key,omitempty"` // S3 object key for agent-generated attachments
}
```

### Driver translation per input type

| Input type | Claude Code | Droid | Devin ACP |
|-----------|-------------|-------|-----------|
| `message` | Write `{"type":"user","message":{text, images}}` to stdin | RPC: `droid.send_message` | RPC: `session/prompt` |
| `message` with attachments | Agent downloads from S3 (pre-signed URL), passes as base64 images to CLI | Same — agent resolves refs before forwarding | Same |
| `skill_invoke` | Write user message `/skill-name` to stdin | Write user message `/skill-name` to stdin | Slash command in `session/prompt` |
| `permission_response` | Write `{"type":"control_response","behavior":"allow"}` to stdin | JSON-RPC response to pending `request_permission` | JSON-RPC response to pending `request_permission` |

### Attachment content block

Extension to ADR-0005's `ContentBlock` for binary data in session events.

**Note:** This `ContentBlock` is the **canonical NATS event schema** content block — it represents the protocol-agnostic format that flows through NATS after driver translation. It is distinct from the **CLI-layer** `ContentBlock` in `mclaude-cli/events/types.go`, which represents Claude Code's native stream-json format (with different field names: `ID` instead of `ToolCallID`, `Name` instead of `Tool`, `Content` string instead of `ContentBlocks`, `IsError` bool instead of `Status`). The `ClaudeCodeDriver` is responsible for mapping between the two: it reads the CLI-layer `ContentBlock` from Claude Code's stdout and translates to the canonical `ContentBlock` below.

```go
type ContentBlock struct {
    Type          string         `json:"type"` // "text", "tool_use", "tool_result", "image", "attachment_ref"
    Text          string         `json:"text,omitempty"`

    // tool_use fields
    ToolCallID    string                 `json:"toolCallId,omitempty"`
    Tool          string                 `json:"tool,omitempty"`
    Input         map[string]interface{} `json:"input,omitempty"`

    // tool_result fields (ToolCallID shared with tool_use)
    Status        string         `json:"status,omitempty"`        // "success", "error"
    ContentBlocks []ContentBlock `json:"contentBlocks,omitempty"` // nested content for tool_result

    // image fields
    MediaType     string `json:"mediaType,omitempty"` // e.g. "image/png"
    Base64        string `json:"base64,omitempty"`
    URL           string `json:"url,omitempty"`

    // attachment_ref fields
    AttachmentRef *AttachmentRef `json:"ref,omitempty"`
}
```

When an agent generates binary output (image, diagram, file), it uploads to S3 via pre-signed URL (ADR-0053) and publishes a session event with `attachment_ref` content blocks. The SPA resolves the ref to a download URL via CP. Binary data never flows through NATS.

## Critical Files

```
mclaude-session-agent/
  events.go              — canonical SessionEvent schema
  driver.go              — CLIDriver interface, CLIBackend, CLICapabilities
  registry.go            — DriverRegistry
  session.go             — driver-agnostic session loop (refactored)
  internal/drivers/      — driver implementations (Go internal package)
    claude_code.go       — ClaudeCodeDriver (stream-json protocol)
    droid.go             — DroidDriver (stream-jsonrpc protocol)
    devin.go             — DevinDriver (ACP protocol)
    generic.go           — GenericTerminalDriver (PTY heuristics)
```

**Package structure:** The `mclaude-session-agent` module is currently a single `package main`. The driver implementations live in `internal/drivers/` as a Go `internal` package — importable only by the session-agent binary, not by external modules. The `internal/` prefix is a Go convention that the compiler enforces. The main package imports `internal/drivers` and registers each driver at startup. The interface types (`CLIDriver`, `SessionEvent`, etc.) stay in the top-level package so they can be passed to the driver implementations without circular imports. If shared types are needed by other modules in the future, they move to `mclaude-common`.

### Skills layout (per-project)

```
.agent/skills/                           # vendor-neutral, all tools discover
  deploy-server/SKILL.md                 # Agent Skills standard format
  deploy-connector/SKILL.md
  deploy-relay/SKILL.md
  dev-harness/SKILL.md
  dev-harness/references/                # optional supporting files
```

See [Managing Skills](#managing-skills) for add/edit/remove procedures.

## Open Questions

1. **Droid missions**: Should mission events (worker started/completed, progress) map to canonical events, or always go through `backendSpecific`? Missions are a significant Droid feature that might warrant first-class support.

2. **ACP filesystem delegation**: When Devin ACP requests `fs/read_text_file`, should the session agent fulfill it from the pod filesystem? This makes Devin work in K8s (where Devin doesn't have direct filesystem access), but creates a different execution model than running Devin locally.

3. **Model switching mid-session**: ~~Claude Code supports `set_model` control request. Droid supports `updateSettings`. Devin supports `session/set_config_option`. Should model switching be part of the canonical `CLIDriver` interface, or handled per-backend?~~ **Resolved:** `sessions.{sslug}.config` subject with patch semantics. The `CLIDriver` interface gets an `UpdateConfig(proc *Process, cfg SessionConfig) error` method. Each driver translates to its native mechanism. See "NATS Subject Structure" (`sessions.{sslug}.config`).

4. **Gemini CLI**: Gemini CLI is adding `--output-format stream-json` (google-gemini/gemini-cli#8203) and has experimental ACP support (`--experimental-acp`). Wait for ACP to stabilize, or write a GeminiDriver against stream-json?

5. **Hook forwarding in K8s**: Hooks run inside the CLI process (or session agent pod). Should the session agent expose hook execution results as canonical events for observability? Currently hooks are transparent — a blocked tool call simply never emits a `tool_call` event. But operators may want audit logs of hook decisions.

6. **Subagent vendor-neutral path**: The Agent Skills standard established `.agent/skills/` as a vendor-neutral path. Should mclaude advocate for `.agent/agents/` as a vendor-neutral subagent path? Cursor's cross-tool discovery shows demand, but no tool scans `.agent/agents/` today.

7. **Cross-tool hook compatibility**: Claude Code and Droid hooks are identical, Cursor is trivially translatable (camelCase ↔ snake_case). Should the session agent normalize hook configs across backends, or leave hooks as backend-specific config?
