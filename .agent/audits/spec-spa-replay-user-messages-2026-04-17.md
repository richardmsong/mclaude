## Run: 2026-04-17T00:00:00Z

Component: spa (mclaude-web)
Spec: docs/plan-replay-user-messages.md
Focus: event-store.ts — addPendingMessage, case 'user', _insertUserTurn

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| plan-replay-user-messages.md:5 | "addPendingMessage immediately inserts an optimistic user turn into _conversation.turns (dimmed, pendingUuid set)" | event-store.ts:172-193 | IMPLEMENTED | — | addPendingMessage pushes pending to _pendingMessages AND pushes a Turn with pendingUuid to _conversation.turns |
| plan-replay-user-messages.md:11 | "User sees message instantly (dimmed, bottom of chat)" | event-store.ts:188-189 | IMPLEMENTED | — | Turn is pushed to end of turns[] (bottom); dimming is UI responsibility via pendingUuid field |
| plan-replay-user-messages.md:11 | "When Claude echoes it back, message moves inline to where Claude accepted it" | event-store.ts:536-548 | IMPLEMENTED | — | case 'user': on uuid match, splices the optimistic turn out, clears pendingUuid, calls _insertUserTurn which repositions it before the fresh streaming asst turn |
| plan-replay-user-messages.md:12 | "Mid-turn user messages appear at the exact position in the conversation where Claude drained them from its queue — between tool results" | event-store.ts:239-302 | IMPLEMENTED | — | _insertUserTurn finds fresh streaming asst turn under wantedParent, reorders confirmed turn before it; pending turns re-appended at end |
| plan-replay-user-messages.md:41 | "Pending message disappears from bottom; inline user turn appears at the correct position in the conversation (between the tool results where Claude accepted it)" | event-store.ts:528-548 | IMPLEMENTED | — | splice at optimisticIdx, delete pendingUuid, _insertUserTurn with event.parent_tool_use_id |
| plan-replay-user-messages.md:164 | "_pendingMessages: PendingMessage[] — messages sent but not yet echoed" | event-store.ts:41 | IMPLEMENTED | — | private _pendingMessages: PendingMessage[] = [] |
| plan-replay-user-messages.md:164-165 | "addPendingMessage(uuid, content): Adds to _pendingMessages AND immediately inserts an optimistic user turn into _conversation.turns (pendingUuid set, styled as dimmed/pending). Notifies listeners." | event-store.ts:172-193 | IMPLEMENTED | — | All three actions: push to _pendingMessages, push Turn with pendingUuid, call _notify() |
| plan-replay-user-messages.md:167-172 | "Modified case 'user' handler: Step 1: tool_result array → attach to ToolUseBlock and return early" | event-store.ts:474-501 | IMPLEMENTED | — | hasToolResult check, attaches resultBlock to toolUseBlock, returns early |
| plan-replay-user-messages.md:168-169 | "Step 2: uuid match → remove from _pendingMessages and clear pendingUuid on matching optimistic turn — turn stays at its position, just un-dimmed" | event-store.ts:504-549 | PARTIAL | CODE→FIX | Spec says turn stays at its position, just un-dimmed. Code instead MOVES the turn: splices it out and calls _insertUserTurn which repositions it. The inline reposition is the actual desired behavior per lines 11, 41 and the User Flow sections — but this Step 2 description specifically says "stays at its position." This is an internal spec inconsistency. See notes. |
| plan-replay-user-messages.md:170 | "Step 3: If isSynthetic: true → create system turn with SystemMessageBlock" | event-store.ts:553-569 | IMPLEMENTED | — | isSynthetic check creates system turn with SystemMessageBlock |
| plan-replay-user-messages.md:171 | "Step 4: If no matching pending turn was found (step 2 returned -1): create a new user turn inline at current position" | event-store.ts:619-649 | IMPLEMENTED | — | Falls through to Step 4c which calls _insertUserTurn |
| plan-replay-user-messages.md:172 | "Text-matching fallback (step 2): retained as defensive measure; matches by exact string comparison; only fires when event.uuid is absent" | event-store.ts:519-526 | IMPLEMENTED | — | pendingIdx = -1 and !event.uuid and incomingText check for text match |
| plan-replay-user-messages.md:174-176 | "clear and compact_boundary handlers: Both reset _conversation.turns. They must also clear _pendingMessages = []" | event-store.ts:317-318, 343-344 | IMPLEMENTED | — | Both handlers set _pendingMessages = [] |
| plan-replay-user-messages.md:176 | "get pendingMessages(): PendingMessage[] — exposes pending list for the UI" | event-store.ts:195-197 | IMPLEMENTED | — | Public getter present |
| plan-replay-user-messages.md:183-193 | "ConversationVMState: pendingMessages: PendingMessage[] added; ConversationVM.state getter reads this.eventStore.pendingMessages" | conversation-vm.ts:6-13, 41 | IMPLEMENTED | — | Interface field present; state getter includes pendingMessages |
| plan-replay-user-messages.md:195-199 | "sendMessage: 1. Generate uuid (crypto.randomUUID()); 2. Call eventStore.addPendingMessage; 3. Publish to NATS with uuid; 4. No sending guard" | conversation-vm.ts:49-62 | IMPLEMENTED | — | All four steps present |
| plan-replay-user-messages.md:200-202 | "sendMessageWithImage: same pattern — uuid, addPendingMessage, publish with uuid" | conversation-vm.ts:64-83 | IMPLEMENTED | — | All steps present for image path |
| plan-replay-user-messages.md:44-49 | "Multiple messages while busy: three dimmed pending messages stack at bottom; each replay removes matching pending and inserts inline; all three arrive roughly together" | event-store.ts:276-301 (Step 5 re-appends remaining pending turns) | IMPLEMENTED | — | _insertUserTurn step 2 collects ALL pending turns, removes them, then re-appends remaining after the confirmed+streaming pair |
| plan-replay-user-messages.md:53-55 | "Synthetic messages: isSynthetic:true → gray system message inline" | event-store.ts:553-569 | IMPLEMENTED | — | Creates system turn with SystemMessageBlock |
| plan-replay-user-messages.md:117-128 | "UserEvent interface: uuid?, isReplay?, isSynthetic? added" | types.ts:232-241 | IMPLEMENTED | — | All three optional fields present on UserEvent |
| plan-replay-user-messages.md:130-148 | "SystemMessageBlock added to Block union; PendingMessage type added" | types.ts:333-365 | IMPLEMENTED | — | SystemMessageBlock, PendingMessage, Turn.pendingUuid all present |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| event-store.ts:1-20 | INFRA | Imports |
| event-store.ts:22-43 | INFRA | EventStoreOptions interface, EventStoreListener type, class fields including _pendingMessages |
| event-store.ts:45-68 | INFRA | Getters: conversation, lastSequence, replayFromSeq, sessionState, model, capabilities — infrastructure for the event model |
| event-store.ts:69-146 | INFRA | start() / stop() — JetStream subscription setup; spec describes EventStore subscribing to event stream but does not detail the implementation |
| event-store.ts:155-170 | INFRA | applyEventForTest() — test-only hook; not production behavior |
| event-store.ts:199-209 | INFRA | _nextTurnId(), _currentAssistantTurn() — helpers for turn management, necessary infrastructure |
| event-store.ts:211-219 | INFRA | _findToolUseBlock() — helper for tool_result attachment |
| event-store.ts:539-549 | UNSPEC'd | Edge case: when pendingIdx != -1 but optimisticIdx == -1 (pending message with no text, so no optimistic turn was inserted), the code removes the pending entry then falls through to create a new user turn via Step 4c. Spec does not describe this case. Behavior is reasonable (create a normal inline turn) but unspecced. |
| event-store.ts:571-617 | UNSPEC'd | Steps 4a and 4b: [SYSTEM NOTIFICATION] filter and SkillInvocationBlock parsing — these are not described in plan-replay-user-messages.md. They are described in other spa design docs/specs (outside scope of this audit). Not dead code — they handle real input prefixes. |
| event-store.ts:653-701 | INFRA | case 'control_request', 'tool_progress', 'result', 'keep_alive' handlers — spec says these event types exist but does not detail their handling in the replay spec |
| event-store.ts:704-707 | INFRA | _notify() — listener notification helper |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| plan-replay-user-messages.md:5 | addPendingMessage inserts optimistic turn (pendingUuid set) | event-store.test.ts:470-476, 537-542 | None | UNIT_ONLY |
| plan-replay-user-messages.md:11 | User sees message at bottom, dimmed | event-store.test.ts:861-881 (idle-state test) | None | UNIT_ONLY |
| plan-replay-user-messages.md:11 | When Claude echoes, turn moves inline | event-store.test.ts:883-913 (mid-turn test), 915-995 (batched test) | None | UNIT_ONLY |
| plan-replay-user-messages.md:12 | Mid-turn messages at exact position between tool results | event-store.test.ts:883-913 | None | UNIT_ONLY |
| plan-replay-user-messages.md:41 | Pending disappears from bottom; inline at correct position | event-store.test.ts:883-913 | None | UNIT_ONLY |
| plan-replay-user-messages.md:168-169 | Step 2: uuid match removes pending and repositions turn | event-store.test.ts:222-236, 496-519, 522-535, 544-565 | None | UNIT_ONLY |
| plan-replay-user-messages.md:170 | isSynthetic creates system turn | event-store.test.ts:259-278 | None | UNIT_ONLY |
| plan-replay-user-messages.md:172 | Text-matching fallback | None found (no test explicitly tests uuid-absent text-fallback path with a pending) | None | UNTESTED |
| plan-replay-user-messages.md:174-176 | clear/compact_boundary clear _pendingMessages | event-store.test.ts:183-202 | None | UNIT_ONLY |
| plan-replay-user-messages.md:195-199 | sendMessage: uuid, addPendingMessage, publish | conversation-vm.test.ts:44-69 | None | UNIT_ONLY |
| plan-replay-user-messages.md:44-49 | Batched 3-message send, each echo promotes inline | event-store.test.ts:915-995 | None | UNIT_ONLY |

### Phase 4 — Bug Triage

No open bugs in .agent/bugs/ have Component: spa. The five open bugs (001-005) target other components (project creation, PTT, initial message injection, heartbeat, getting-started copy). None are relevant to this audit.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| 001 | new-project-crash-no-git-credentials | N/A | Component: control-plane |
| 002 | ptt-race-condition | N/A | Component: session-agent |
| 003 | initial-message-injection-race | N/A | Component: session-agent |
| 004 | heartbeat-stale-agent-down | N/A | Component: session-agent |
| 005 | getting-started-canned-text | N/A | Component: spa — but unrelated to replay user messages |

### Summary

- Implemented: 19
- Gap: 0
- Partial: 1
- Infra: 7
- Unspec'd: 2
- Dead: 0
- Tested (unit): 10
- Unit only: 10
- E2E only: 0
- Untested: 1
- Bugs fixed: 0
- Bugs open: 0
