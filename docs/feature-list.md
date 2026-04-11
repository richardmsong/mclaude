# mclaude Client Feature List

Canonical list of client-side features every platform implements. Platform-specific docs and implementation tasks should reference features by ID (e.g., C3, T1, X1) rather than re-describing behavior.

Future: Figma designs linked per feature.

---

## Auth

| # | Feature | Description |
|---|---------|-------------|
| A1 | Login | Email/password or SSO login flow |
| A2 | Session persistence | Secure JWT + nkeySeed storage, auto-reconnect on app reopen |
| A3 | Token refresh | Background JWT refresh before expiry, graceful re-auth on failure |
| A4 | Logout | Clear credentials, disconnect NATS |

## Project & Session Management

| # | Feature | Description |
|---|---------|-------------|
| P1 | Project list | List projects with health status (from heartbeat monitor) |
| P2 | Session list | List sessions per project with state, model, branch, cost |
| P3 | Create session | Specify project, branch, optional name |
| P4 | Delete session | Stop Claude process, clean up worktree |
| P5 | Session state indicator | Live idle/running/requires_action/restarting/failed badge |
| P6 | Agent health banner | Show warning when heartbeat is stale (agent down) |

## Conversation

| # | Feature | Description |
|---|---------|-------------|
| C1 | Send message | Text input, submit to session |
| C2 | Streaming text | Live token-by-token rendering from `stream_event` deltas |
| C3 | Markdown rendering | Render assistant text as formatted markdown |
| C4 | Syntax highlighting | Code blocks in assistant text and tool results |
| C5 | Tool use display | Show tool name, input summary, elapsed time, result |
| C6 | Tool result display | Render tool output, collapsed for large results, expand-on-click |
| C7 | Thinking blocks | Show/hide extended thinking content |
| C8 | Subagent nesting | Nest subagent turns under parent Agent tool block, default collapsed |
| C9 | Compaction indicator | Visual divider when context is compacted |
| C10 | Image upload | Send images (base64) in user messages, clipboard paste |
| C11 | Interrupt | Stop Claude mid-turn |

## Permissions

| # | Feature | Description |
|---|---------|-------------|
| R1 | Permission prompt | Show tool name, input, approve/deny buttons on `control_request` |
| R2 | Permission notification | System-level notification when permission is needed (mobile push, desktop notification) |

## Skills

| # | Feature | Description |
|---|---------|-------------|
| S1 | Skills picker | List available skills from capabilities, invoke by name |
| S2 | Skills refresh | `reload_plugins` to pick up mid-session changes |

## Model & Cost

| # | Feature | Description |
|---|---------|-------------|
| M1 | Model switcher | Change model mid-session via `set_model` control request |
| M2 | Effort switcher | Change thinking budget via `set_max_thinking_tokens` |
| M3 | Cost display | Per-session token usage and cost (input, output, cache read/write) |
| M4 | Context meter | Context window utilization from `get_context_usage` |

## Terminal

| # | Feature | Description |
|---|---------|-------------|
| T1 | Terminal session | Open interactive PTY shell alongside Claude sessions |
| T2 | Terminal resize | Sync terminal dimensions on window resize |
| T3 | Multiple terminals | Manage multiple terminal tabs per project |

## Voice

| # | Feature | Description |
|---|---------|-------------|
| V1 | Push-to-talk | Voice input via platform speech recognition API |

## System

| # | Feature | Description |
|---|---------|-------------|
| X1 | Cache reset | Button to clear all client-side caches (ConversationModel, capabilities, session state) and re-subscribe from scratch |
| X2 | Reconnection | Auto-reconnect on network loss, JWT expiry, tab foreground — gap-free event replay |
| X3 | Background reconnect | Mobile: reconnect on `visibilitychange`, re-watch KV |
| X4 | Forced update | On connect, check client version against `minClientVersion` from control plane. If below minimum, block UI entirely and prompt user to update. No graceful degradation — breaking changes are hard cuts. |

---

## Platform Support Matrix

|   | Web SPA | mclaude-cli | iOS (future) |
|---|---------|-------------|--------------|
| A1–A4 | All | All | All |
| P1–P6 | All | P2, P5 only (single-session focus) | All |
| C1–C11 | All | C1, C5, C6, C11 (text-only) | All |
| R1 | Inline | `y/n` prompt | Inline + push notification (R2) |
| R2 | Desktop notification | — | Push notification |
| S1–S2 | All | S1 (numbered list) | All |
| M1–M4 | All | M1 only | All |
| T1–T3 | All (xterm.js) | — (`kubectl exec`) | All (SwiftTerm) |
| V1 | WebSpeech API | — | SFSpeechRecognizer |
| X1–X4 | All | X2, X4 (print error + exit) | All |

### X4 platform behaviour

| Platform | Below `minClientVersion` |
|----------|--------------------------|
| Web SPA | Force `location.reload()` — new content-hashed assets load automatically. If version still below minimum after reload (shouldn't happen), show blocking "server is updating, please wait" screen. |
| iOS | Blocking screen: "Update required — open the App Store to update mclaude." No partial functionality. |
| mclaude-cli | Print `mclaude: server requires client version ≥ {min}, you have {current}. Run: brew upgrade mclaude` and exit 1. |

`minClientVersion` is set in control-plane config and returned on the auth login response and on a `GET /version` endpoint (no auth required). Clients check it on startup and on every reconnect. When a breaking mclaude version ships, bump `minClientVersion` in the control-plane deployment.
