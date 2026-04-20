# Spec: mclaude-web Session Detail

Session Detail screen, the Terminal tab, and the Edit Session sheet for the mclaude-web SPA.

## Screen: Session Detail

```
┌─────────────────────────────────┐
│ ‹ Back   my-project   ↻  ●  ⋯  │  nav
├─────────────────────────────────┤
│ ●  Working  · #window-id        │  det-meta row
├─────────────────────────────────┤
│ [📋 View Plan  ▶]               │  plan card (plan_mode only, collapsible)
│ [✓ Approve]  [✕ Cancel]         │  action bar (needs_permission or plan_mode only)
├─────────────────────────────────┤
│  Events  │  Terminal            │  tab bar
├─────────────────────────────────┤
│                                 │
│  (conversation events list)     │  scrollable content
│                                 │
│                                 │
├─────────────────────────────────┤
│ [✕]  [📷]  [___message___] [🎙][↑]│  input bar (Events tab only)
└─────────────────────────────────┘
```

### Nav Bar

- `‹ Back`: returns to Dashboard
- Title: project name (truncated)
- `↻`: refresh (re-fetch events from server)
- Connection dot
- `⋯` (three-dot menu): opens model picker, token usage, raw output overlays

### Det-Meta Row

`[status dot] [status label] · #[session-id]`

Updates live as status changes from WebSocket. When status transitions to `needs_permission` or `plan_mode`, action bar appears below without full re-render.

### Plan Card (plan_mode only)

Collapsible card with purple accent. Header: "📋 View Plan [filename] ▶/▼". Body shows markdown-rendered plan content. Fetched from server on first expand.

### Action Bar (needs_permission or plan_mode)

Two buttons side by side:
- **✓ Approve**: green, `--green` color scheme
- **✕ Cancel**: red, `--red` color scheme

### Tab Bar

Two tabs: Events | Terminal. Active tab has `--text` color + bottom border in `--blue`. Inactive tab `--text2`.

Switching to Terminal tab: hides input bar, shows PTY.

### Input Bar (Events tab)

Left to right:
1. **✕ Stop button** (only when `working`): round, `--red` tint — sends Escape to halt Claude
2. **📷 Attach**: camera icon, opens image file picker; paste from clipboard also works
3. **Textarea**: auto-grows up to ~120px, placeholder "Message… or / for skills", Enter sends (Shift+Enter newline)
4. **🎙 PTT button**: hold to record (Web Speech API), releases to submit transcription; grayed if unsupported
5. **↑ Send button**: round, `--blue` background

When a screenshot is staged, a preview strip appears above the input bar showing the thumbnail + "Screenshot ready" + ✕ to remove.

### Skills Autocomplete

When input starts with `/`, a popup appears above the input bar listing matching skills:
```
  /skill-name   [built-in]
  /other-skill  [built-in]
```
Each row tappable; fills the input field with `/skill-name `. Skills data is a flat name list — no description field is available in the data model.

---

## Tab: Terminal

Full-screen terminal emulator (xterm.js on web, WKWebView / native terminal on iOS).

```
┌─────────────────────────────────┐
│                                 │
│  (terminal canvas / text area)  │  black background (#000000)
│                                 │
├─────────────────────────────────┤
│ Esc  Ctrl  Tab  ▲ ▼ ◀ ▶  ⌅ Paste  ⎘ Text │  keyboard toolbar
└─────────────────────────────────┘
```

Keyboard toolbar: fixed bottom, dark background (`#111`), monospace buttons. Buttons: Esc, Ctrl (toggles — blue when active), Tab, arrow keys, Paste, Text mode toggle.

**Text mode** (⎘ Text): switches xterm canvas to a selectable `<pre>` element for copy-paste on mobile. Live mode (⌨ Live) switches back.

Terminal input bar (the message composer) is hidden on the Terminal tab.

---

## Sheet: Edit Session

Bottom sheet modal with scrim overlay. Shows current session settings and restarts with the updated values.

```
┌─────────────────────────────────┐
│      Edit Session           [✕] │
├─────────────────────────────────┤
│  Extra flags                    │  label
│  ┌─────────────────────────┐    │
│  │ --disallowedTools "Edit │    │  monospace textarea, 4 rows,
│  │ (src/**)"               │    │  pre-filled with current value
│  └─────────────────────────┘    │
│                                 │
│  [     Restart Session    ]     │  --blue button
└─────────────────────────────────┘
```

- Pre-fills `extraFlags` textarea with the value from the current session's KV entry (read from `sessionVm.extraFlags`).
- **Restart Session** button: sends `sessions.restart` with `{ sessionId, extraFlags }` (extraFlags is the trimmed textarea value; omit if blank/whitespace-only), then closes the sheet. The session will briefly show "restarting" state as the agent kills and relaunches the Claude process.
