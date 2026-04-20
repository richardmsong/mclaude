# Spec: UI Interaction Patterns

Shared interaction contracts for live state updates, event streaming, reconnect, and scroll behavior. Applies to every UI component.

## Interaction Patterns

### Session Status Transitions (live, no reload)

On WebSocket `sessions` message:
1. Update status dot color and label in session row (dashboard)
2. Update status dot + label in det-meta row (session detail)
3. Show/hide action bar (needs_permission / plan_mode → show; other states → hide)
4. Show/hide stop button in input bar (working → show; other → hide)
5. Update nav title badge count

### Event Streaming

On WebSocket `event` message:
1. Append new event to conversation list
2. Auto-scroll to bottom only if user is already at/near bottom (within 100px)
3. Never re-render the full list — append the new node

### Reconnect Behavior

On WebSocket disconnect:
1. Show connecting state in nav dot
2. Auto-reconnect after 3.5s
3. On reconnect: re-fetch sessions list; events continue from last received
4. No page reload

### Initial Scroll Position

**On fresh load (page refresh, direct URL, first navigation to a session)**:
- Continuously scroll to the bottom on every event/turn update until the user manually scrolls away.
- "User manually scrolled away" = the scroll container is more than 100px from the bottom (`scrollHeight - scrollTop - clientHeight > 100`). Once detected, stop auto-scrolling.
- On send, always scroll to bottom and reset the "user scrolled away" flag.

**On back-navigation (SPA: user navigated away then back within the same page session)**:
- Restore the saved scroll position immediately.
- Treat the restored position as if the user manually scrolled there — stop auto-scrolling.

**Implementation**: save scroll positions in a **module-level in-memory Map** (not `sessionStorage`). The Map is cleared on every page refresh (JS module re-evaluation), so page refreshes always trigger the continuous-scroll-to-bottom path. In-app navigation saves/restores from the Map.

### Scroll Persistence

Scroll position is saved into the module-level Map when the user navigates away from a session (component unmount / sessionId change). Restored from the Map on back-navigation. **Not** stored in `sessionStorage` — page refresh always starts from the bottom.

---

## What v1 Does That v2 Must Also Do

1. **Inline screenshot rendering** — user messages with `/tmp/mclaude-screenshots/*.png` paths show the image, not the path text
2. **Subagent nesting** — Agent tool calls group their sub-events under an expandable orange-bordered card; event count badge visible even when collapsed
3. **Bash syntax highlighting** — commands in tool cards use color syntax (blue for commands, orange for operators, purple for keywords, green for strings, cyan for flags, yellow for variables)
4. **Calibration** — token cost estimates can be multiplied by a user-supplied factor to match Anthropic Console actuals
5. **Compaction event** — renders as `— conversation compacted —` system line; events after compaction are still accumulated normally
6. **Laptop/host filtering** — all data is scoped to selected host; Settings lets user switch
7. **Live model switching** — three-dot menu → Model → select tier → sends `/model sonnet` as input
8. **Plan mode** — plan_mode status shows a collapsible purple plan card above the action bar, fetching the plan from the server on first open
9. **Scroll to bottom on send** — after user sends a message, scroll the conversation to bottom
10. **Tab memory** — switching sessions preserves which tab (Events/Terminal) was active
