# Spec: mclaude-web Dashboard

Dashboard screen plus its associated bottom sheets (New Session, Project Filter, New Project) for the mclaude-web SPA.

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
