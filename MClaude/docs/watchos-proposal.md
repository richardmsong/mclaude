# MClaude Watch — Proposal

## Overview

A watchOS companion app for MClaude that provides glanceable session status, haptic notifications for attention-needed events, and quick interactions (approve, deny, voice input, option selection) — all relayed through the iPhone app via WatchConnectivity.

## Why

Claude Code sessions run unattended but periodically need human input — permission approvals, question answers, or nudges. Today that requires pulling out your phone. A watch app turns these into 2-second wrist interactions.

## Architecture

```
┌─────────┐   WatchConnectivity   ┌──────────┐   HTTP/WS   ┌────────────────┐
│  Watch   │ ◄──────────────────► │  iPhone   │ ◄─────────► │ mclaude-server │
│  App     │                      │  App      │             │ (on Mac)       │
└─────────┘                       └──────────┘              └────────────────┘
```

**WatchConnectivity** is the only viable transport since the watch won't have Tailscale. The iPhone app already maintains a connection to mclaude-server and acts as a relay.

### Communication patterns

| Pattern | Use case | API |
|---------|----------|-----|
| **Application context** | Push latest session list + statuses to watch. Watch reads on launch or complication refresh. | `updateApplicationContext(_:)` |
| **User info transfer** | Queue session state updates (guaranteed delivery, FIFO). Used for complication updates. | `transferCurrentComplicationUserInfo(_:)` |
| **Interactive messaging** | Real-time actions: approve, deny, send input, voice transcript. Requires iPhone app reachable. | `sendMessage(_:replyHandler:)` |
| **Background transfer** | Not needed — no large payloads. | — |

### Data flow

**iPhone → Watch (state updates):**
- On every poll cycle (already 3s), iPhone packages session summaries into application context
- Session summary: `{id, projectName, status, statusDuration, lastMessage, prompt?}`
- On status change to `needsPermission` or `waitingForInput`, send via complication user info to trigger complication refresh

**Watch → iPhone (actions):**
- Interactive message with action payload: `{action: "approve"|"deny"|"sendInput"|"sendKey", sessionId, text?}`
- iPhone receives, forwards to mclaude-server, replies with success/failure
- Watch shows confirmation haptic or error

### Offline / unreachable handling

If the iPhone is unreachable (app not running, out of Bluetooth range):
- Watch shows last-known session state with a "stale" indicator
- Action buttons show "iPhone not connected" on tap
- No silent failures — always communicate state to the user

## Watch App

### Session List (root view)

Compact list showing all sessions. Sorted: needs-attention first, then working, then idle.

```
┌──────────────────────────┐
│ MClaude                  │
├──────────────────────────┤
│ 🟡 nutrition-tracker     │
│   Needs approval · 19h   │
├──────────────────────────┤
│ 🔵 mclaude               │
│   Working · 3m            │
├──────────────────────────┤
│ 🌙 work                  │
│   Idle · 143h             │
├──────────────────────────┤
│ 🌙 ai-interplay          │
│   Idle · 22h              │
└──────────────────────────┘
```

- Status dot color: yellow (needs attention), blue (working), gray (idle)
- Tap → session detail

### Session Detail

Shows the session's current state and available actions.

```
┌──────────────────────────┐
│ ◀ nutrition-tracker      │
│                          │
│ Needs permission         │
│                          │
│ "Do you want to allow    │
│  Write to file            │
│  src/sprite.swift?"      │
│                          │
│ ┌──────────┐ ┌─────────┐│
│ │ Approve  │ │  Deny   ││
│ └──────────┘ └─────────┘│
│                          │
│ 🎤 Voice input           │
└──────────────────────────┘
```

**Contextual actions based on status:**

| Status | Actions shown |
|--------|--------------|
| `needsPermission` | Approve / Deny buttons |
| `waitingForInput` / AskUserQuestion | Option buttons (tappable list) |
| `working` | Cancel button, last assistant message preview |
| `idle` | Voice input, quick replies |

### Voice Input

- Tap mic → watchOS dictation sheet appears
- Transcript sent as interactive message to iPhone → forwarded as `sendInput` to server
- Confirmation haptic on success

### Quick Replies

Pre-built responses for common interactions, shown as a scrollable list:

- "yes"
- "no"  
- "continue"
- "/exit"
- Custom (opens dictation)

### AskUserQuestion

When a session has a detected prompt with options:
- Options rendered as a tappable list
- Tap sends the number key via `sendKey`
- After selection, show confirmation

## Complications

### Circular (`CLKComplicationFamilyCircularSmall` / WidgetKit `.accessoryCircular`)

**Attention badge** — number of sessions needing input/permission.

- 0 needs attention: checkmark icon
- 1+: number in circle, tinted yellow

### Corner (`accessoryCorner`)

**Gauge ring** — working sessions / total sessions ratio.

- Ring fill = proportion of sessions currently working
- Inner text: attention count or checkmark
- Tapping opens the app

### Inline (`accessoryInline`)

Single line of text:

- `2 working · 1 needs approval`
- `All idle` (when nothing happening)
- `⚠ 3 need attention` (urgent)

### Rectangular (`accessoryRectangular`)

Mini session list — top 3 sessions by priority (attention > working > idle):

```
┌────────────────────────────┐
│ 🟡 nutrition  Needs input  │
│ 🔵 mclaude    Working 3m   │
│ 🌙 work       Idle 143h    │
└────────────────────────────┘
```

## Notifications

Delivered as local notifications from the iPhone app when session state changes.

| Event | Haptic | Notification |
|-------|--------|--------------|
| Session needs permission | `.notification` (prominent) | "nutrition-tracker needs approval: Write to file src/sprite.swift" |
| Session needs input (AskUserQuestion) | `.notification` | "mclaude is asking: What kind of character?" |
| Session finished turn | `.success` (subtle) | Silent — complication updates |
| Session error | `.failure` | "work session errored" |

Notification actions (actionable notifications):
- **Approve / Deny** inline buttons for permission requests
- Tapping notification body opens watch app to that session

## Implementation Plan

### Phase 1 — Foundation
1. Add watchOS target to the Xcode project (WatchKit App with SwiftUI lifecycle)
2. Create shared `WatchSessionRelay` on the iPhone side — observes `AppState` session changes, pushes application context to watch
3. Create `WatchConnectivityManager` on watch side — receives context, exposes `@Published` session state
4. Session list view on watch

### Phase 2 — Actions
5. Interactive messaging: approve, deny, cancel
6. Session detail view with contextual action buttons
7. Voice input via watchOS dictation
8. Quick replies

### Phase 3 — Complications
9. WidgetKit complications (circular, inline, rectangular, corner)
10. Complication user info transfers on status changes
11. Timeline refresh scheduling

### Phase 4 — Notifications
12. Actionable local notifications from iPhone when session needs attention
13. Notification category with approve/deny actions
14. Haptic patterns per event type

## Scope / Non-goals

- **No terminal output view** — screen too small to be useful
- **No conversation history** — defer to phone for that
- **No session creation** — use phone or desktop
- **No direct server communication** — always relay through iPhone
- **No file attachments** — phone only
- **No screenshot sending** — phone only

## Dependencies

- watchOS 10+ (WidgetKit complications, latest SwiftUI)
- Existing MClaude iOS app (relay layer)
- No new server endpoints needed — iPhone already has full API access
