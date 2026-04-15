## Audit: 2026-04-15T01:50:00Z

**Document:** docs/plan-replay-user-messages.md

### Round 1

**Gaps found: 6**

1. **ConversationVMState not updated to expose pendingMessages** — doc referenced eventStore directly from UI
2. **system_message block type undefined** — no SystemMessageBlock in Block union
3. **tool_result early-return skips uuid matching** — ordering ambiguity in case 'user' handler
4. **uuid/isReplay/isSynthetic missing from UserEvent type** — doc didn't specify where to add them
5. **--replay-user-messages stdout shape unspecified** — no exact JSON documented
6. **sending guard becomes incorrect** — doc didn't address removing setSending

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | pendingMessages not in ConversationVMState | Added pendingMessages field to ConversationVMState, threaded via ConversationVM.state getter | factual |
| 2 | system_message block undefined | Defined SystemMessageBlock type, added to Block union | factual |
| 3 | tool_result early-return ordering | Clarified tool_result return stays first (no uuid on tool results), uuid matching is step 2 | factual |
| 4 | UserEvent missing fields | Added uuid?, isReplay?, isSynthetic? directly to UserEvent interface | factual |
| 5 | stdout shape unspecified | Documented exact NDJSON shape for all three replay variants | factual |
| 6 | sending guard incorrect | Added explicit instruction to remove sending state and setSending calls | factual |

### Round 2

**Gaps found: 2**

1. **Error Handling contradicts Scope on text-matching dedup** — error handling said "fall back to text-matching", scope said "remove text-matching dedup"
2. **Spawn args show `-w {cwd}` which doesn't exist** — actual code uses `cmd.Dir`; examples also omitted `--include-partial-messages`

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | dedup contradiction | Removed text-matching fallback from error handling; no duplicate risk since addUserTurn() is also removed | factual |
| 2 | spawn args mismatch | Replaced illustrative examples with actual args slice format matching session.go | factual |

### Round 3

**Gaps found: 1**

1. **`_pendingMessages` not cleared on `clear` or `compact_boundary` events** — after a clear or compaction, no replay will arrive for in-flight pending messages (session context truncated), so they'd remain stuck as pending indefinitely.

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | _pendingMessages not cleared | Added clearing instruction to EventStore section for clear and compact_boundary handlers | factual |

### Round 4

CLEAN — no blocking gaps found.

### Result

**CLEAN** after 4 rounds, 9 total gaps resolved (9 factual fixes, 0 design decisions).

## Run: 2026-04-15T00:00:00Z

**Gaps found: 6**

1. **`ConversationVMState` not updated to expose `pendingMessages`** — The doc says `SessionDetailScreen` should render `eventStore.pendingMessages` at the bottom of the chat. But `ConversationVM` is the intermediary the screen actually consumes via `conversationVM.state` (a `ConversationVMState`). The doc says to add a getter `get pendingMessages(): PendingMessage[]` to `EventStore`, but does not specify whether `ConversationVMState` needs a `pendingMessages` field, and does not specify how `SessionDetailScreen` obtains the list — directly from `EventStore` (bypassing `ConversationVM`) or through a new field on `ConversationVMState`. The `ConversationVM` exposes no `pendingMessages` and `ConversationVMState` has no such field today. The document must state which surface delivers the pending list to the component layer.
   - **Doc**: "Render `eventStore.pendingMessages` at the bottom of the message list, after all conversation turns."
   - **Code**: `ConversationVM.state` returns `ConversationVMState` (turns, state, model, skills, isStreaming) — no pending messages field. `SessionDetailScreen` consumes only `conversationVM.state`. (`mclaude-web/src/viewmodels/conversation-vm.ts:32-45`, `mclaude-web/src/components/SessionDetailScreen.tsx:95`)

2. **`system_message` block type is not defined in the type system** — The doc specifies that synthetic replays (`isSynthetic: true`) are rendered as a "system turn with a system-message block." No `system_message` block type exists in `types.ts`; the `Block` union has: `text`, `streaming_text`, `tool_use`, `tool_result`, `thinking`, `control_request`, `compaction`. The existing `system` turn type only carries `CompactionBlock` today. A developer cannot implement this without knowing the block type name, its fields, and how `EventList` renders it.
   - **Doc**: "create a system turn with a system-message block" / "Render as gray, smaller text — similar to compaction summary blocks."
   - **Code**: `Block` union (`mclaude-web/src/types.ts:284-297`) has no `system_message` variant. `EventList` only dispatches `block.type === 'compaction'` for system turns (`mclaude-web/src/components/events/EventList.tsx:85-87`).

3. **`tool_result` early-return in `_applyEvent` will skip uuid-matched pending removal** — The doc says the modified `case 'user'` handler should: (1) if uuid matches, remove from `_pendingMessages`, then (2) if tool_result, attach to ToolUseBlock and return early. But the current `user` case has a bare `return` (not `break`) at the tool_result branch (line 391 of event-store.ts), which exits `_applyEvent` entirely before reaching the pending-removal step. The doc does not specify the ordering of these two steps when the event is a tool_result — does a tool_result user event ever carry a `uuid` that needs to be matched? If yes, the early return must happen after uuid matching; if no, the doc should say so explicitly.
   - **Doc**: "If event has `uuid` and a matching pending message exists: remove from `_pendingMessages`" listed as step 1, then "If event content is a tool_result: attach to matching ToolUseBlock" as step 2.
   - **Code**: `event-store.ts:376-391` — `return` on tool_result exits before any uuid matching. The current early return would skip step 1 for any tool_result that somehow has a uuid.

4. **`uuid` field missing from `UserEvent` TypeScript type** — The doc adds `uuid?`, `isReplay?`, and `isSynthetic?` to user events, but `UserEvent` in `types.ts` has no such fields. A developer implementing the `case 'user'` handler in `_applyEvent` would write `event.uuid` — TypeScript would reject this because `UserEvent` only has `type`, `parent_tool_use_id`, and `message`. The doc says "Add to `StreamJsonEvent` union (or the user event type)" but does not specify whether to augment the existing `UserEvent` interface or create a new `UserReplayEvent` variant in the union.
   - **Doc**: "Add to `StreamJsonEvent` union (or the user event type): `uuid?: string`, `isReplay?: boolean`, `isSynthetic?: boolean`"
   - **Code**: `mclaude-web/src/types.ts:181-187` — `UserEvent` has only `type`, `parent_tool_use_id`, and `message`. The `StreamJsonEvent` union (`types.ts:222-234`) uses a closed discriminated union.

5. **`--replay-user-messages` flag validity not verified** — The doc instructs adding `--replay-user-messages` to both new-session and resume invocations of Claude Code. The current `session.go` `start()` builds args from a fixed list and has no such flag. Whether `--replay-user-messages` is a real flag accepted by the installed `claude` binary, what it does to Claude's stdout format (does it emit a new event type? does it echo the user message verbatim or wrap it differently?), and whether it is available in the current Claude Code version are all unspecified. The doc describes the expected stdout shape for replayed user events but does not reference a Claude Code changelog, flag documentation, or example output. A developer cannot implement the stdout scanner changes without knowing the exact JSON shape Claude emits for these replays.
   - **Doc**: "Add `--replay-user-messages` to the Claude Code exec.Command" / events stream payload shows `"isReplay": true`, `"uuid": "..."` — asserting Claude echoes the uuid back.
   - **Code**: `mclaude-session-agent/session.go:132-147` — current args list has no such flag; no other part of the repo references it.

6. **`sendMessage` / `sendMessageWithImage` `setSending` guard becomes incorrect** — `SessionDetailScreen` sets `setSending(true)` before calling `conversationVM.sendMessage()` and `setSending(false)` in `finally`. Today this disables the send button until the call returns (synchronously). After this change, the send button is meant to be re-enabled immediately (the user can send another message while one is pending). The doc does not say whether the `sending` guard and the `setSending` calls should be removed, or whether a new condition (e.g., based on the pending messages list) should replace them. A developer touching `handleSend` will encounter this ambiguity.
   - **Doc**: "Message appears immediately at bottom of chat, dimmed (pending state)" — implies send should not be locked.
   - **Code**: `mclaude-web/src/components/SessionDetailScreen.tsx:184-207` — `sending` state gates the send button; it is set true before the call and cleared in `finally`.

## Run: 2026-04-15T02:45:00Z

**Gaps found: 2**

1. **Error Handling table contradicts Scope section on text-matching dedup** — The Error Handling table says "Fall back to text-matching dedup against pending messages" when `uuid` is missing from a replay. The Scope section says "Remove old text-matching dedup and `addUserTurn()`." Both are in v1. A developer cannot know whether to keep text-matching as a fallback or remove it entirely. If text-matching is removed (per Scope) and a uuid-less replay arrives, it would create a duplicate inline turn alongside the still-pending pending message. If it is kept (per Error Handling), `addUserTurn` and the dedup logic in `_applyEvent` must not be removed.
   - **Doc**: Error Handling: "uuid missing from replay (older Claude version) — Fall back to text-matching dedup against pending messages." Scope In v1: "Remove old text-matching dedup and `addUserTurn()`."
   - **Code**: `mclaude-web/src/stores/event-store.ts:396-413` — current text-matching dedup block exists and would need to be either kept or removed.

2. **Spawn args examples show `-w {cwd}` flag that does not exist in the actual code** — The doc's spawn arg examples include `-w {cwd}` as the working directory argument. The actual `session.go` `start()` function sets the working directory via `cmd.Dir = s.state.CWD`, not via a CLI flag. A developer following the example literally would add both `--replay-user-messages` and `-w {cwd}` to the args slice, introducing an unknown flag that would cause Claude to reject the invocation. The doc should specify only what to add to the existing args list, not rewrite the full command with an incorrect flag.
   - **Doc**: "claude --print --verbose --output-format stream-json --input-format stream-json --replay-user-messages --session-id {id} -w {cwd}" (line 71)
   - **Code**: `mclaude-session-agent/session.go:149-151` — `cmd.Dir = s.state.CWD` sets the working directory; no `-w` or `--cwd` flag in the args slice. The args slice also includes `--include-partial-messages` which is absent from the doc's example.

## Run: 2026-04-15T04:00:00Z

**Gaps found: 1**

1. **`_pendingMessages` not cleared on `clear` or `compact_boundary` events** — The document specifies `_pendingMessages` as new state in `EventStore` and describes how it is populated and drained on replay. But the `clear` and `compact_boundary` event handlers in `_applyEvent` both reset `_conversation.turns`. If pending messages exist when one of these events arrives, there will be no subsequent replay to match their uuids — the session context has been truncated. The document does not specify whether `_pendingMessages` should also be cleared in those two cases. A developer implementing the `clear` and `compact_boundary` branches of `_applyEvent` cannot know whether to also call `_pendingMessages = []` without a decision from the spec.
   - **Doc**: "Modified `case 'user'` handler in `_applyEvent()`" — only describes the `user` case. No mention of `clear` or `compact_boundary` affecting `_pendingMessages`.
   - **Code**: `mclaude-web/src/stores/event-store.ts:222-228` (`clear` resets `_conversation`), `event-store.ts:232-242` (`compact_boundary` replaces `_conversation.turns`). Neither touches any pending state.

## Run: 2026-04-15T05:30:00Z

CLEAN — no blocking gaps found.

All previously identified gaps have been addressed in the current document:
- `ConversationVMState.pendingMessages` and the `ConversationVM.state` getter are fully specified.
- `SystemMessageBlock`, its fields, and its placement in the `Block` union are defined.
- `tool_result` early-return ordering is clarified (tool results never carry a uuid; early return in step 1 is safe).
- `UserEvent` interface additions (`uuid?`, `isReplay?`, `isSynthetic?`) are specified.
- Exact Claude stdout NDJSON shapes for all three replay variants are documented.
- `sending` guard removal and `setSending` call removal from `handleSend` are explicitly specified.
- Text-matching dedup contradiction is resolved (dedup removed; no fallback).
- Spawn args match the actual `session.go` args slice (no `-w` flag; `--include-partial-messages` present).
- `_pendingMessages` is cleared in both `clear` and `compact_boundary` handlers.
