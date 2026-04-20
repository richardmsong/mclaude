# Spec: mclaude-web Overlays

Full-screen and dropdown overlays for the mclaude-web SPA: Event Detail Modal, Three-dot Menu, and Raw Output.

## Overlay: Event Detail Modal

Bottom sheet sliding up over the session detail screen. Shows structured data for any tapped event.

```
┌─────────────────────────────────┐
│  🛠 Bash          12:34:56   ✕  │  header: icon + type + timestamp + close
├─────────────────────────────────┤
│  Command                        │  field label
│  ┌─────────────────────────┐    │
│  │ git status              │    │  syntax-highlighted monospace
│  └─────────────────────────┘    │
│  Result                         │
│  ┌─────────────────────────┐    │
│  │ On branch main          │    │  monospace pre
│  └─────────────────────────┘    │
└─────────────────────────────────┘
```

- Tapping the scrim closes the modal
- **No raw JSON "Input" dump** — never show raw `JSON.stringify` of tool input
- Tool-specific rendering (no raw JSON Input section appended after):
  - **Bash / `!`**: syntax-highlighted command only
  - **Edit**: full file path prominently, then unified diff (DiffView)
  - **Write**: full file path prominently, then content in scrollable monospace block
  - **Read**: full file path + optional line range (`L12-L60`)
  - **Grep / Glob**: pattern and path in separate labeled fields
  - **All others**: pretty-printed, syntax-highlighted JSON — keys in `--blue`, string values in `--green`, numbers in `--orange`, booleans/null in `--purple`, punctuation in `--text3`
- Result section: monospace pre block, `--red` text on error

---

## Overlay: Three-dot Menu

Dropdown anchored to the `⋯` button in the session nav bar.

```
  ┌───────────────────────────┐
  │ 🧠 Model                  │
  │    sonnet-4-6             │
  │ 📊 Token Usage            │
  │ 📜 Raw Output             │
  │ ⚙ Edit Session            │
  └───────────────────────────┘
```

- **Model**: opens sub-menu to switch between Opus / Sonnet / Haiku
- **Token Usage**: opens Token Usage overlay (same data as Token Usage screen but session-scoped)
- **Raw Output**: opens Raw Output overlay (live-polling terminal text)
- **Edit Session**: opens the Edit Session bottom sheet

---

## Raw Output Overlay

Full-screen overlay showing the raw terminal output of the session, live-refreshing at 500ms:

```
┌─────────────────────────────────┐
│ ‹ Back      Raw Output          │
├─────────────────────────────────┤
│                                 │
│  > claude code                  │  ANSI-colored terminal text
│  Claude 4.6.1                   │
│  ✓ 3 tool calls                 │
│  ...                            │
│                                 │
└─────────────────────────────────┘
```

ANSI escape codes are rendered as colored text (not stripped). Scrollable, always-bottom behavior when user hasn't scrolled up.
