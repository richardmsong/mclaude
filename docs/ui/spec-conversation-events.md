# Spec: UI Conversation Events

Event types and rendering contracts for the session conversation stream. Shared across all UI components — every UI must render these event types consistently.

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

Centered, `--text3` color, small text. Two subtypes:

**CompactionBlock** (from `compact_boundary` system event): renders `"— conversation compacted —"` as a centered divider. The conversation model resets all prior turns; only events after the compaction boundary are displayed.

**Clear event** (from `clear` event): resets the conversation model to empty — no turns, no divider. The UI shows a blank conversation state. Unlike compaction, no summary or divider is rendered.

Other system events (turn completion, etc.): `"Turn completed in 3.2s"`.

### System Message (`.ev-sys-msg`)

Rendered from user events with the `isSynthetic` flag set. These are system notifications injected by the platform (not typed by the user). Centered, `--text3` color, small text — visually identical to `.ev-sys` but semantically distinct (user-turn origin, not system-event origin). Not shown as a user bubble.

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

## User-Message Parsing Rules

When a `user` event arrives with text content (not a tool_result):

1. **Synthetic**: if `isSynthetic` flag is set, render as a System Message (`.ev-sys-msg`). Do not show as a user bubble.
2. **Skill invocation**: if text starts with `"Base directory for this skill:"`, render as a Skill Invocation Chip (`.ev-skill`).
3. **System notification discard**: if text starts with `"[SYSTEM NOTIFICATION"`, discard entirely — do not create any turn or visual element.
4. **Normal message**: dedup against optimistic pending messages — match by `event.uuid` (primary) or exact text content (fallback). On match, clear the `pendingUuid` on the existing optimistic turn (no new turn created). No match: create a new user Turn with TextBlock(s).

For user messages containing image content blocks: render as User Message (`.ev-user`) with image thumbnails per the image rendering rules above.
