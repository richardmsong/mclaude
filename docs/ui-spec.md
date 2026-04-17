# MClaude UI Specification

Cross-platform wireframe and interaction spec. All platforms (web SPA, iOS, future) must produce identical screens, navigation flows, and visual states. This document is the single source of truth.

The canonical reference implementation is `mclaude-relay/static/index.html` (v1 webapp). Every design decision here is derived from it. When this spec is ambiguous, defer to the v1 reference.

---

## Design System

### Color Palette (dark theme — all platforms)

| Token | Hex | Usage |
|-------|-----|-------|
| `--bg` | `#111111` | Page background |
| `--surf` | `#1c1c1e` | Card / sheet surface |
| `--surf2` | `#2c2c2e` | Secondary surface (tool bodies, code blocks) |
| `--surf3` | `#3a3a3c` | Tertiary surface (progress bars, hover) |
| `--border` | `#38383a` | Dividers, card borders |
| `--text` | `#ffffff` | Primary text |
| `--text2` | `#8e8e93` | Secondary text (labels, metadata) |
| `--text3` | `#48484a` | Tertiary text (timestamps, placeholder) |
| `--blue` | `#0a84ff` | User messages, links, active states |
| `--green` | `#30d158` | Success, idle sessions, approve actions |
| `--orange` | `#ff9f0a` | Working/active status, tool events, warnings |
| `--red` | `#ff453a` | Errors, needs-permission status, cancel actions |
| `--purple` | `#bf5af2` | Thinking events, plan mode, model switch |

All platforms use this palette verbatim. Do not substitute platform system colors.

### Typography

- **Body**: SF Pro (iOS) / Inter (web) / system-ui — 14–15px
- **Monospace**: SF Mono (iOS) / Menlo / 'Courier New' — 12–13px — used for tool bodies, code, terminal
- **Nav title**: 17px, weight 600
- **Section labels**: 12px, weight 600, uppercase, letter-spacing 0.5px, color `--text2`

### Spacing

- Screen edge padding: 16px
- Card border-radius: 12px
- Small element border-radius: 8px
- List item height: ~52px with 12px vertical padding
- Separator: 1px `--border` color

### Viewport

The SPA is a full-screen app — no zoom is permitted. The viewport is locked to device width at 1:1 scale.

- **Meta tag**: `<meta name="viewport" content="width=device-width, initial-scale=1.0, maximum-scale=1.0, user-scalable=no" />`
- **CSS**: `touch-action: manipulation` on `html, body, #root` — prevents double-tap zoom on Safari (which sometimes ignores `user-scalable=no` for accessibility)

### Status Dots

A filled circle (8–10px) indicating session state, animated where noted.

| State | Color | Animation |
|-------|-------|-----------|
| `working` | `--orange` | Pulsing opacity: 1.0 → 0.4 → 1.0, 1.2s loop |
| `needs_permission` | `--red` | None |
| `plan_mode` | `--purple` | None |
| `idle` | `--green` | None |
| `unknown` / `waiting_for_input` | `--text3` (dark gray) | None |

The pulse animation applies only to `working` state and uses a CSS keyframe that scales opacity, not size.

---

## Navigation Model

Hash-based routing (web) / stack navigation (iOS):

| Route | Screen |
|-------|--------|
| `#` (default) | Dashboard |
| `#s/{sessionId}` | Session Detail |
| `#settings` | Settings |
| `#usage` | Token Usage |
| `#users` | User Management (admin only) |

Navigation bar is always visible (fixed top). Back navigation uses a `‹ Back` button on the left side. The nav title is always centered.

---

## Screen: Auth / Login

Shown when no access token is stored.

```
┌─────────────────────────────────┐
│                                 │
│              ⚡                  │  (large icon, centered)
│           MClaude               │  (title, 28px bold)
│  Sign in to your account        │  (subtitle, --text2)
│                                 │
│  ┌─────────────────────────┐    │
│  │  email@example.com      │    │  (email field)
│  └─────────────────────────┘    │
│  ┌─────────────────────────┐    │
│  │  •••••••••••••          │    │  (password field)
│  └─────────────────────────┘    │
│  ┌─────────────────────────┐    │
│  │         Connect         │    │  (primary button, --blue fill)
│  └─────────────────────────┘    │
│                                 │
└─────────────────────────────────┘
```

**Behavior:**
- Pressing Return / Enter in the token field submits
- On success: token persisted in local storage; navigate to Dashboard
- On failure: error state shown inline (red text below field). Error message rules:
  - Server returned an error body (non-2xx with text): show the server's response text verbatim
  - Server returned non-2xx with no body: show `Login failed: {status}`
  - Network error (`Load failed` on Safari, `Failed to fetch` on Chrome): show `Network error — if using HTTPS with a self-signed certificate, ensure it is trusted in your system keychain`
  - Login succeeded but NATS connection failed: show `Login succeeded but could not connect to messaging ({natsUrl}): {error}`

---

## First-Run Flow

Triggered when the user logs in and no sessions exist in any project in the KV store (checked after ~1s watch-settle delay). This handles the case where the server has seeded a project (e.g. "Default Project") with no sessions yet.

1. If no projects exist, client calls `projects.create` with `{ name: "Default" }` to create one first.
2. Client calls `sessions.create` in the first available project with `{ name: "Getting Started" }`
3. Client navigates directly to the new session
4. Client sends the pre-seeded onboarding message as the first user turn:

> Hi! I'm Claude. You're in MClaude — a real-time coding environment powered by Claude Code.
>
> Here's what you can do here:
> - Write and edit files across your project
> - Run shell commands (git, npm, make, etc.)
> - Search and read your codebase
> - Create more sessions for different tasks or branches
>
> Ask me anything to get started — like "what's in this project?" or "help me fix this bug". What would you like to work on?

The flow runs once. After the default project is created it appears in the KV store on subsequent logins.

---

## Screen: Dashboard

```
┌─────────────────────────────────┐
│ MClaude  [2]    📊  ⚙  ⋯   ●  │  nav: title + badge + usage + settings + menu + conn dot
├─────────────────────────────────┤
│  Showing: mclaude        [✕]   │  filter banner (only when filter active)
├─────────────────────────────────┤
│                                 │
│  MCLAUDE                        │  project header (only when >1 project visible)
│  ●  working-session             │  session row
│     Working · ~/work/mclaude    │
│                               › │
│                                 │
│  OTHER-PROJECT                  │
│  ●  demo                        │
│     Idle · ~/work/other         │
│                               › │
│                                 │
│         (empty state)           │
│      No Sessions                │
│   Tap + to start a session      │
│                                 │
└─────────────────────────┬───────┘
                          │ +     │  (FAB, bottom right)
                          └───────┘
```

### Nav Bar

- **Title**: "MClaude" + optional badge (red circle with count) when sessions need attention
- **Badge** appears when any session has `needs_permission` or `plan_mode` status
- **📊 button**: navigates to Token Usage
- **⚙ button**: navigates to Settings
- **⋯ button**: opens dashboard overflow menu (see below)
- **Connection dot**: 8px circle — green (connected), gray (disconnected), red (error), animated pulse when connecting

### Dashboard Overflow Menu (⋯)

Dropdown anchored to the ⋯ button, dismisses on outside tap.

```
  ┌───────────────────────────┐
  │ 📁 New Project            │
  │ 🔍 Filter by Project      │  only shown when >1 project exists
  └───────────────────────────┘
```

- **New Project**: opens the New Project sheet.
- **Filter by Project**: opens the Project Filter Sheet. Hidden when there is ≤1 project (nothing to filter).

### Session List Grouping

When more than one project is visible, sessions are grouped by project, with a project header above each group. When only one project is visible — either because the user has a single project, or because a project filter is active — project headers are hidden and session rows render flat.

- Projects sorted alphabetically by name.
- Within a project, sessions sort by descending last-updated time.
- Project header: 12px, weight 600, uppercase, `--text2`, 8px top padding, 4px bottom padding, not tappable.

### Project Filter

The dashboard can be filtered to sessions from a single project. The filter is opened via the overflow menu (⋯ → "Filter by Project") and persists across reloads.

**State**: `localStorage.mclaude.filterProjectId` holds the selected project ID. Unset or empty string means "no filter" (show all projects).

**Filter banner**: when a filter is active, a banner renders above the session list:

```
┌─────────────────────────────────┐
│  Showing: mclaude        [✕]   │  --surf2 background, 13px/500/--text, 10px padding
└─────────────────────────────────┘
```

- Banner text: `Showing: {project name}`.
- `✕` button on the right clears the filter (removes `mclaude.filterProjectId` from localStorage, re-renders dashboard).

**Stale filter**: if the filtered project no longer exists in the KV store (was deleted), the filter is cleared automatically on the next render and the banner is hidden.

### Session Rows (`.srow`)

```
[status dot]  [project name]                [›]
              [status label · short/path]
```

- Full-width tap target, no explicit separator (use padding)
- Status dot left-aligned, 12px, color matches state
- Project name: `--text`, 15px, weight 500
- Metadata line: `--text2`, 13px — e.g. "Working · ~/work/myproject"
- Chevron `›` right-aligned, `--text3`
- On tap: navigate to `#s/{id}`

Status labels:
| State | Label |
|-------|-------|
| `working` | "Working" |
| `needs_permission` | "Needs permission" |
| `plan_mode` | "Plan mode" |
| `idle` | "Idle" |
| `unknown` | "Unknown" |
| `waiting_for_input` | "Waiting for input" |

### Empty State

When there are no sessions but there ARE projects:

```
┌─────────────────────────────────┐
│                                 │
│  Your Projects                  │  section label, 12px caps
│  📁 Default Project          ›  │  project row
│  📁 Other Project            ›  │
│                                 │
│  Tap + to start a session       │  body text, --text2
│                                 │
└─────────────────────────────────┘
```

- Section label "Your Projects": 12px, weight 600, uppercase, `--text2`
- Project rows: full-width tap target, `--text` color, chevron `›` on right
- On tap: starts a new session in that project immediately
- Body line below project list: "Tap + to start a session"

When there are no sessions AND no projects:
- Heading: "No Sessions"
- Body: "Tap + to start a Claude session"

When a project filter is active and the filtered project has no sessions:
- Heading: "No Sessions"
- Body: "No sessions in this project"

### FAB

Circular button, 52px, `--blue` background, white `+` glyph. Fixed bottom-right, 20px inset.

- **1 project**: creates a new session in that project immediately (no sheet)
- **Multiple projects**: opens the New Session sheet with the last-used project pre-selected

---

## Sheet: New Session

Bottom sheet modal with scrim overlay (tapping scrim closes it).

```
┌─────────────────────────────────┐
│         New Session         [✕] │
├─────────────────────────────────┤
│  📁 project-alpha               │  project row
│     ~/work/alpha                │
│  📁 project-beta                │
│     ~/work/beta                 │
│  (loading…)                     │  while fetching
│  (No projects found)            │  if empty
├─────────────────────────────────┤
│  ▶ Advanced                     │  collapsible, collapsed by default
└─────────────────────────────────┘
```

Expanded Advanced section:

```
├─────────────────────────────────┤
│  ▼ Advanced                     │
│  Extra flags                    │  label
│  ┌─────────────────────────┐    │
│  │ --disallowedTools "Edit │    │  monospace textarea, 3 rows
│  │ (src/**)"               │    │
│  └─────────────────────────┘    │
└─────────────────────────────────┘
```

Project rows sorted: last-used project first (tracked in `localStorage` by `mclaude.lastProjectId`), then alphabetical by name.

On tap: create session in that project, persist its ID to `mclaude.lastProjectId`, include `extraFlags` string if non-empty, dismiss sheet.

**Advanced section**: collapsible `<details>`/`<summary>` element, collapsed by default. Contains a monospace `<textarea>` (3 rows, full width) labeled "Extra flags". The user types raw Claude Code CLI flags (e.g. `--disallowedTools "Edit(src/**)" --model claude-opus-4-7`). The raw string is sent as `extraFlags` in the session create payload — no client-side parsing. Empty or whitespace-only = omit the field.

---

## Sheet: Project Filter

Bottom sheet modal with scrim overlay (tapping scrim closes it). Opened from the dashboard overflow menu → "Filter by Project".

```
┌─────────────────────────────────┐
│     Filter by Project       [✕] │
├─────────────────────────────────┤
│  ○ All Projects                 │  always first; selecting clears the filter
│  ● mclaude                      │  current active filter (filled radio)
│  ○ other-project                │
└─────────────────────────────────┘
```

- "All Projects" is always the first row; selecting it clears the filter (`mclaude.filterProjectId` removed from localStorage).
- Project rows below, sorted alphabetically by name.
- The row matching the current `mclaude.filterProjectId` has a filled radio; all others empty. If no filter is active, "All Projects" is filled.
- On tap: write `mclaude.filterProjectId` to localStorage (or remove it if "All Projects" tapped), dismiss sheet, re-render dashboard.

---

## Sheet: New Project

Bottom sheet modal with scrim overlay (tapping scrim closes it).

```
┌─────────────────────────────────┐
│         New Project         [✕] │
├─────────────────────────────────┤
│  Name                           │  section label
│  ┌─────────────────────────┐    │
│  │  My Project             │    │  text input (required)
│  └─────────────────────────┘    │
│                                 │
│  Git Repository  (optional)     │  section label
│  ┌─────────────────────────┐    │
│  │  https://github.com/…   │    │  text input (optional)
│  └─────────────────────────┘    │
│  Clone a repo, or leave blank   │  helper text, --text2
│  to start from scratch.         │
│                                 │
│  ┌─────────────────────────┐    │
│  │       Create Project    │    │  primary button, --blue
│  └─────────────────────────┘    │
└─────────────────────────────────┘
```

**Behavior:**
- Name is required; Create button disabled when empty
- Git URL is optional; if provided the server will clone it
- On submit: calls `projects.create`, shows spinner on button, dismisses on success
- On error: shows inline error text below the form in `--red`
- After creation: always navigates to the new project by starting a session in it immediately

---

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
  /skill-name   Brief description       [source]
  /other-skill  Another description     [source]
```
Each row tappable; fills the input field with `/skill-name `.

---

## Conversation Events

Events render in a vertically scrolling list, newest at bottom. Each event type has a distinct visual treatment.

### User Message (`.ev-user`)

Right-aligned bubble (`--blue` background, white text, border-radius 18px, tighter on the send side). Text content renders in the bubble. Image content blocks (from the Attach button or clipboard paste) render as thumbnails inside the bubble — max 240px wide, rounded corners 8px — directly below the text. Tapping a thumbnail opens it full-size in a lightbox overlay (dimmed backdrop, tap outside to close); on web, clicking also works. When a message has both text and images, text appears first, images below. Pending messages (opacity 0.5) show thumbnails at the same opacity.

```
                       ┌──────────────────┐
                       │  fix the bug     │
                       │ ┌──────────────┐ │
                       │ │  [thumbnail] │ │
                       │ └──────────────┘ │
                       └──────────────────┘
```

### Skill Invocation Chip (`.ev-skill`)

Rendered in place of a user message when the event is a skill expansion (text starts with `"Base directory for this skill:"`).

```
┌────────────────────────────────┐
│ 🔧 feature-change  [Skill]   ▶│  header row (collapsed)
└────────────────────────────────┘

┌────────────────────────────────┐
│ 🔧 feature-change  [Skill]   ▼│  header row (expanded)
│                                │
│   Fix two event-store bugs…    │  args text (full, --text2)
│                                │
│   ‹ raw skill text ›           │  full expansion in --surf2 block, monospace, scrollable
└────────────────────────────────┘
```

- Blue left border (user-originated), `--surf` background, border-radius 12px
- Header: `🔧` icon + skill name (bold) + `[Skill]` badge (blue pill, same style as Agent type badge) + count of args chars or blank + expand chevron
- Collapsed by default; tapping header toggles expansion
- Expanded body shows args text first, then full raw expansion in a `--surf2` monospace block

### Assistant Text (`.ev-text`)

Full-width, no background, `--text` color. Content is markdown-rendered:
- Headings (h1–h4): progressively larger, bold
- Bold `**text**`, italic `*text*`
- Inline code: `--surf2` background pill, monospace
- Code blocks: `--surf2` background, 8px radius, monospace, horizontally scrollable
- Unordered / ordered lists: indented bullets
- Tables: horizontally scrollable, `--border` separators
- Paragraphs: separated by spacing, not `<br>`

Tappable to open detail modal.

### Thinking (`.ev-think`)

Collapsible row. Collapsed: `▶ Claude's thinking…` in `--purple` color. Expanded: shows raw thinking text in monospace, `--surf2` background.

```
▶ Claude's thinking…

▼ Claude's thinking…
  ┌────────────────────────────┐
  │ Let me analyze the code…   │
  └────────────────────────────┘
```

### Tool Use + Result (`.ev-tool`, paired)

A unified card showing the tool invocation and its result inline:

```
┌────────────────────────────────┐
│ 💻 Bash                        │  tool header
│ git status                     │  tool body (command/path)
├────────────────────────────────┤
│ On branch main                 │  result body
│ nothing to commit              │
└────────────────────────────────┘
```

Tool icons by name:
| Tool | Icon |
|------|------|
| `Bash` / `!` | 💻 / ⚡ |
| `Edit` | ✏️ |
| `Write` | 📝 |
| `Read` | 📄 |
| `Grep` | 🔍 |
| `Glob` | 🔍 |
| `Agent` | 🤖 |
| `/skill-name` | 🔧 |
| (other) | 🛠 |

Tool-specific display:
- **Bash**: shows `highlightCommand(command)` — syntax-colored (bash/python/js/go/swift/ruby detected)
- **Edit**: shows filename (last 2 path segments); detail modal shows unified diff
- **Write**: shows filename + first 500 chars of content
- **Read**: shows filename + optional line range (`L12-L60`)
- **Grep / Glob**: shows pattern + last 2 path segments of search location

Result section:
- Normal result: `--surf2` background, monospace, truncated with "show more"
- Error result: `--red` text, error indicator in header

Both halves are individually tappable to open detail modal.

### AskUserQuestion (`.ev-ask`)

Rich interactive block for structured questions:

```
┌────────────────────────────────┐
│ ❓ Question                    │
│                                │
│ Should we use TypeScript?      │  question text
│                                │
│ ○  Yes                         │  option (unselected)
│    TypeScript adds type safety │  description
│ ●  No                          │  option (selected, --blue dot)
│    Plain JS is simpler         │
│                                │
│ [        Submit       ]        │  submit button (--blue)
│                              [Cancel] │
└────────────────────────────────┘
```

- Options are radio buttons, single-select per question
- Submit: enabled only when all questions answered; sends formatted answer to Claude
- Cancel: sends cancel signal, `--red` tint
- After submit: button shows "✓ Submitted", disabled; options locked

### Tool Result (standalone `.ev-result`)

Only shown when not paired with a tool_use. Monospace card, `--surf2` background, green left border. Error results: red left border.

### System Event (`.ev-sys`)

Centered, `--text3` color, small text. Examples:
- "Turn completed in 3.2s"
- "— conversation compacted —"

### Subagent Group (`.ev-agent-group`)

Collapsible group for Agent tool calls with nested sub-events:

```
┌────────────────────────────────┐
│ 🤖 Agent  [Explore]  desc…  5 ▼│  header row (orange accent pill for type)
│                                │
│   [nested event 1]             │  expanded body: indented sub-events
│   [nested event 2]             │
│   …                            │
└────────────────────────────────┘
```

Orange left border on the whole card. Sub-events render using the same event rendering rules.

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

---

## Overlay: Token Usage (Session)

Full-screen overlay with back button. Shows breakdown for a single session:

```
┌─────────────────────────────────┐
│ ‹ Back   Token Usage            │
├─────────────────────────────────┤
│  sonnet-4-6 · 5 turns           │  model + turn count
│                                 │
│  ┌────────┐  ┌────────┐         │
│  │ Input  │  │ Output │         │  2-column grid
│  │ 12.3K  │  │ 4.1K   │         │
│  │ $0.012 │  │ $0.041 │         │
│  └────────┘  └────────┘         │
│  ┌────────┐  ┌────────┐         │
│  │Cache W │  │Cache R │         │
│  │ 2.1K   │  │ 45.2K  │         │
│  │ $0.003 │  │ $0.005 │         │
│  └────────┘  └────────┘         │
│                                 │
│  ┌─────────────────────────┐    │
│  │  Estimated Cost         │    │
│  │  $0.061                 │    │
│  │  63.7K total tokens     │    │
│  └─────────────────────────┘    │
│  Prices: input $3/M · output …  │
└─────────────────────────────────┘
```

Token tiles: 2×2 grid, each showing label, token count (formatted as K/M), cost estimate. Colors match the design palette (input=blue, output=green, cache-write=orange, cache-read=purple).

---

## Screen: Token Usage (Global)

```
┌─────────────────────────────────┐
│ ‹ Back   Token Usage            │
├─────────────────────────────────┤
│ 1H  6H  24H  7D  30D            │  time range chips
├─────────────────────────────────┤
│ $4.23 / $140 this month  Calib  │  monthly budget bar
│ [████░░░░░░░░░░░░░░░░░░░] $140  │
│ 30% used · 12/30 days           │
│ Projected month-end: $9.20      │
├─────────────────────────────────┤
│ ┌──────┐ ┌──────┐ ┌──────┐     │
│ │Tokens│ │ Cost │ │Tok/m │     │  stat tiles
│ │ 1.2M │ │$4.23 │ │ 845  │     │
│ └──────┘ └──────┘ └──────┘     │
├─────────────────────────────────┤
│ [stacked bar chart SVG]         │  tokens over time
│  ■ Input ■ Output ■ Cache R ■ W │
├─────────────────────────────────┤
│ ● Input      1.2M    $3.60      │  token breakdown list
│ ● Output     89K     $1.34      │
│ ● Cache Read 320K    $0.10      │
│ ● Cache Write 12K    $0.015     │
├─────────────────────────────────┤
│ sonnet-4-6 ×12 · 89 API calls   │  footer
└─────────────────────────────────┘
```

Budget bar: two-layer progress bar — solid for actual spend (color: green/orange/red based on %, threshold 60%/85%), semi-transparent for projected overage.

Chart: stacked bar chart, time-bucketed. Buckets: 5min (1H), 30min (6H), 2h (24H), 6h (7D), 1d (30D). X-axis labels at 4 evenly-spaced positions.

Calibration: link to adjust cost estimates against Anthropic Console actuals. When calibrated, shows badge with multiplier.

---

## Screen: Settings

```
┌─────────────────────────────────┐
│ ‹ Back      Settings            │
├─────────────────────────────────┤
│                                 │
│  HOST                           │  section label
│  ┌─────────────────────────┐    │
│  │  Active Host  [select▾] │    │
│  └─────────────────────────┘    │
│                                 │
│  CONNECTED HOSTS                │
│  ┌─────────────────────────┐    │
│  │ ● macbook-pro   3 sess  │    │
│  │ ● macbook-air   1 sess  │    │
│  └─────────────────────────┘    │
│                                 │
│  CONNECTION                     │
│  ┌─────────────────────────┐    │
│  │  Status    ● Connected  │    │
│  │  Sessions  4            │    │
│  └─────────────────────────┘    │
│                                 │
│  ADMINISTRATION (admin only)    │
│  ┌─────────────────────────┐    │
│  │  User Management      › │    │
│  └─────────────────────────┘    │
│                                 │
│  ACCOUNT                        │
│  ┌─────────────────────────┐    │
│  │  Name       Richard     │    │
│  │  Role       admin       │    │
│  └─────────────────────────┘    │
│                                 │
│  ┌─────────────────────────┐    │
│  │        Sign Out         │    │  red text
│  └─────────────────────────┘    │
└─────────────────────────────────┘
```

Settings rows use a grouped card style (iOS Settings aesthetic):
- `--surf` card background
- `--border` dividers between rows within a card
- Row: label left, value/control right
- Status dot: 8px circle, green/gray/red

"Active Host" dropdown: selecting a host reconnects the WebSocket filtered to that host. "All Hosts" option shows sessions from all connected hosts.

**Error handling — general rule:**
Every section that loads data from the server must surface failures visibly. Silent catches that swallow errors and show an empty/default state are not acceptable — the user cannot distinguish "no data" from "failed to load."

Specific rules:
- **Git Providers section**: if `getMe()` or `getAdminProviders()` fails, show a red error line in the section (e.g. "Failed to load providers") instead of "No providers configured." Always `console.error` the underlying error for dev-tools debugging.
- **Any async load**: on failure, log to `console.error` with the error object. Show an inline error in the relevant section. Never silently fall back to an empty state.

---

## Screen: User Management (admin only)

```
┌─────────────────────────────────┐
│ ‹ Back      Users               │
├─────────────────────────────────┤
│ ┌─────────────────────────────┐ │
│ │ [+] Invite User             │ │
│ └─────────────────────────────┘ │
│                                 │
│  richard@example.com            │
│  Richard · admin                │
│                                 │
│  alice@example.com              │
│  Alice · user                   │
└─────────────────────────────────┘
```

---

## Component: Connection Indicator

A small colored dot (`.cdot`) in the nav bar.

| State | Color |
|-------|-------|
| Connected | `--green` |
| Connecting | gray, pulsing |
| Error | `--red` |
| Off / disconnected | `--text3` (dark gray) |

---

## Component: Inline Diff View (`.diff-view`)

GitHub-style unified diff with char-level highlighting.

```
┌─────────────────────────────────┐
│ 📄 src/main.go                  │  filename header (optional)
├─────────────────────────────────┤
│   package main                  │  context line (--text3 background)
│ − import "fmt"                  │  removed line (--red bg: rgba(255,69,58,.12))
│ + import "log"                  │  added line (--green bg: rgba(48,209,88,.12))
│   func main() {                 │  context line
└─────────────────────────────────┘
```

- Gutter column: `−` for removed, `+` for added, space for context
- Char-level highlights: `<span class="diff-hl">` — darker background within the line (rgba(255,255,255,.25) for additions, rgba(255,69,58,.35) for removals)
- Monospace font, 12px, `--surf2` base background
- Horizontally scrollable for long lines
- Every element (container, line div, gutter span, content span) must explicitly set `fontSize: 12` and `fontFamily: monospace` — never rely on inheritance; add `-webkit-text-size-adjust: 100%` to prevent iOS scaling

---

## Interaction: Prompt Bar

Shown when session has a pending question (distinct from AskUserQuestion tool — this is a Claude Code `/ask` prompt):

```
┌─────────────────────────────────┐
│ What would you like to do?      │  question text
│ [1. Continue]  [2. Stop]        │  option buttons
└─────────────────────────────────┘
```

Option buttons: pill shape, `--surf2` background, tapping sends the option number as input.

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

---

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

## Push-to-Talk (PTT)

The microphone button in the input bar:
- **Hold**: starts recording via Web Speech API
- **Release**: stops recording, transcribed text is sent immediately (not placed in field)
- **Visual state**: button turns red with pulse animation while recording
- **Fallback**: if Speech API unavailable or on HTTP, button is dimmed (40% opacity); tapping shows alert explaining why

### Voice-first mode

Configurable in **Settings → Input → Default input method** (stored in `localStorage` as `mclaude.inputMode: 'text' | 'voice'`; default `'text'`).

**Text mode** (default): PTT button is small (32px) and sits between Attach and Send. Layout: `[Stop] [📷] [textarea] [🎙] [Send]`.

**Voice mode**: the Send button is replaced by a large microphone button (56×56px). The textarea shrinks but remains visible (users can still type and press Enter to send). Layout: `[Stop] [📷] [textarea…] [large 🎙]`.

In voice mode the large button uses the same hold-to-record / release-to-send semantics. A keyboard icon (⌨) appears in the top-right corner of the input area; tapping it temporarily focuses the textarea and collapses the button back to small size until focus is lost.

**Settings screen** adds an "Input" section:
```
Input
  Default method:  ○ Text  ● Voice
```
The preference is persisted in `localStorage` under `mclaude.inputMode` and applied on every render of the input bar.

---

## Platform Notes

### Web SPA
- Use React functional components + hooks
- Routing via `window.location.hash` + `hashchange` event (no React Router)
- WebSocket connection to NATS via `nats.ws`
- Terminal via `@xterm/xterm` + `@xterm/addon-fit`
- No server-side rendering; pure client app

### iOS (SwiftUI)
- Navigation: `NavigationStack` with programmatic push
- Dark color scheme enforced — do not follow system light mode
- Status dot animation: `withAnimation(.easeInOut(duration:1.2).repeatForever())`
- Terminal: `WKWebView` with xterm.js, or native `UITextView` in text mode
- PTT: `AVAudioSession` + `SFSpeechRecognizer`
- Colors: use `Color(hex:)` extension mapping `--blue` → `Color(0x0a84ff)` etc.
- Font: `.fontDesign(.monospaced)` for tool bodies

### Future Platforms
- Match the color tokens exactly — do not substitute platform system accent colors
- Implement all event types including AskUserQuestion interactive block
- Implement the full diff view including char-level highlighting
- The terminal tab is required

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

