# ADR: Stop Button Mid-Turn Interrupt Fix

**Status**: accepted
**Status history**:
- 2026-04-20: accepted

## Overview

The Stop button (✕) in the `mclaude-web` Session Detail input bar — specified to appear whenever Claude is actively working and to halt the turn when clicked — is not appearing to the user while Claude streams output. The plumbing behind the button (NATS `sessions.control` publish → session-agent `handleControl` → `{type:"control_request",request:{subtype:"interrupt"}}` into Claude's stdin) is already implemented and tested. The defect is in the visibility predicate that controls whether the button renders.

## Motivation

User report (2026-04-20): "i need an escape button to stop the turn midway in mclaude. [the button] doesn't appear." Feature C11 ("Interrupt — Stop Claude mid-turn") is in `docs/feature-list.md` and the Stop button is specified in `docs/ui/mclaude-web/spec-session-detail.md` Input Bar section with the visibility rule "only when working". The spec is correct; the visibility predicate in `SessionDetailScreen.tsx` is too narrow, or the upstream state signal that feeds it doesn't reach the `'running'` value when it should.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Classification | Class A (bug — spec correct, code wrong) | The spec unambiguously requires the Stop button to appear whenever Claude is working. No spec edit needed. |
| Scope of fix | Whatever makes the Stop button appear during every state in which Claude is actively running a turn (generating, using tools, waiting for a pending permission prompt, executing a tool call). | The user-facing contract is "I can always cancel an in-progress turn." Any state where the turn is not yet complete qualifies. |
| Root cause investigation | Dev-harness reproduces the bug in preview, traces whether (a) `sessionState` never reaches `'running'`, (b) `'running'` is reached but the predicate is too narrow for states like `requires_action`/`plan_mode`, or (c) KV/state-changed events aren't propagating to the SPA. | The plumbing has many layers (session-agent emits `session_state_changed` → KV → SPA `SessionStore` → `SessionDetailScreen` prop). The fix depends on which layer is the culprit. |
| Keyboard shortcut | Not in scope for this ADR. | The user's complaint is about the button not appearing, not about the lack of a key binding. If a keyboard shortcut is desired, file it separately. |

## Impact

- **Component**: `mclaude-web` (primary — visibility predicate and/or state wiring). Possibly `mclaude-session-agent` if state-changed events are not being emitted correctly during active turns.
- **Specs touched**: none. `docs/ui/mclaude-web/spec-session-detail.md` already describes the correct behavior.

## Scope

In v1:
- Stop button appears whenever the turn is not complete — including during tool use, streaming, and pending permission prompts. The existing interrupt NATS flow must reliably halt the turn.
- Dev-harness confirms the fix end-to-end in the preview deploy using Playwright (send a message, wait for the button to appear, click, verify the turn stops).

Deferred:
- Keyboard shortcut (Escape key) to trigger the same interrupt.
- `mclaude-cli` interrupt equivalent (Ctrl-C in CLI currently detaches the connection, not the turn).
- Confirmation dialog before interrupting.
