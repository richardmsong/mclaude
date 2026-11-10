# MClaude for Apple Watch

Added 2026-11-06 (Richard: "Scaffold out a simple watch app that I can see updates for mclaude"). Reworked 2026-11-07 (Richard: the flat list "seems overwhelming"; "whenever I'm working on a session, the user needs to be somewhat reminded of [the] train of thought"; "My goal with the watch app is to not have to take my phone out of my pocket"; "Ship it all at once").

## Overview

The watch handles the whole check-in loop without the iPhone leaving your pocket:
1. **Know when to look:** a push when a session needs you (mirrored to the watch).
2. **See what matters:** one list in the order you last worked in each session, with a dot for what each is doing (needs you, working, background work, unread) and read sessions folded away.
3. **Get back into a session:** each session has a short recap (Goal / Where it is / Next), shown on its detail screen.
4. **Act on it:** answer a question, approve or deny, tell Claude to continue, or talk to it.

## Decisions (Richard)

| Topic | Decision |
|---|---|
| Purpose | Never having to take the phone out of your pocket (2026-11-07). |
| List | One list ordered by your last input, the same order as the iPhone drawer; status shows only as dots, never as sections or order (2026-11-09, replacing the 2026-11-07 Needs you / Working / Unread sections). Read sessions stay behind one row (2026-11-07). |
| Read vs unread | The split that matters is read vs unread, not idle vs active (2026-11-07). A session that just finished is unread until you look at it. Read state lives on the server and is shared by every device (2026-11-09). |
| Recap | Not in the list; only on the detail screen (2026-11-07). |
| Rows | Two lines (2026-11-07). |
| Scope | Ship everything at once (2026-11-07). |
| Complication | Not yet (2026-11-06). |
| Look | Black background with Claude accents (2026-11-06). |
| Data path | The watch never talks to the network or holds the server token; the iPhone app relays (2026-11-06). |
| Full message | A button on the session screen shows Claude's last answer in full, rendered (2026-11-07). |
| Talk | Talk to the session on its screen, transcribed on the watch with Apple's dictation; tapping Done in dictation is fine (2026-11-07). |
| Live view | While a session works, follow its train of thought: Claude's thinking and text as they're written, not its commands. As little delay as possible, pushed rather than polled (2026-11-10). |
| Course correction | What you say while Claude works goes through the same queue as on the phone, no cut-in. One button stops Claude; with something queued it reads Send now, because stopping delivers the queue anyway (2026-11-10). |

## Server

### Recap

Every session with a conversation carries a **recap**: three short lines that remind you where it stands.

- `goal`: what you're trying to get done in this session overall (≤ 80 characters).
- `progress`: where it is now, what's done or what just happened (≤ 120 characters).
- `next`: what happens next (≤ 120 characters), and `nextBy`: `"you"` when it's waiting on you (a question, an approval, or Claude asked you something), `"claude"` when Claude proposed a next step it can take itself, `"none"` when the work is finished.

**When it's made:** whenever a turn ends, and whenever a session starts waiting on a question, plan approval or tool permission. One recap runs per session at a time; a trigger that arrives while one is running causes one more run after it finishes. A failed or timed-out run (60 s) keeps the previous recap.

**How it's made:** a one-shot call to the Haiku model outside the session (like session titles), so the session's own context isn't touched. The input is the session's first real prompt (up to 1,000 characters), the most recent user and assistant messages (newest kept, up to 6,000 characters, tool calls and thinking left out; answers to Claude's questions and plan approvals count as user messages, so the model knows a follow-up is based on real answers), and the pending question or request if there is one. The model returns JSON `{"goal","progress","next","nextBy"}`; anything that doesn't parse, or is missing a field, counts as a failed run. Fields over their limits are cut at a word boundary with "…".

**Stored:** with the session, so it survives server restarts. Ended sessions drop it.

**In the sessions list:** each session gains an optional `recap`:

```
"recap": {"goal": "...", "progress": "...", "next": "...", "nextBy": "you" | "claude" | "none", "at": "<ISO date>"}
```

Omitted until the first recap exists, and for k8s sessions. Older app builds ignore it.

### Pending prompt detail

The session's `prompt` object gains two optional fields so the watch can answer every question:

- `kind`: `"question"`, `"plan"` or `"tool"`.
- `questions`: for `kind: "question"`, every question of the pending AskUserQuestion as `{"question", "options": [labels], "multiSelect": bool}`.

`question` and `options` keep their current meaning (the first question), so older builds are unaffected.

### Live stream

The server passes on Claude's main-thread thinking and text while a turn runs, from Claude Code's partial-message stream (subagent output is left out).

- **Live turn:** per session, the server keeps the current turn's blocks in order. A block is `thinking` (its text grows as Claude thinks; redacted thinking has none), `text` (Claude's reply as it's written) or `tool` (any tool call: no text, only when it started). A block is `done` when the next block starts or the turn ends; a tool block counts as running until then, which covers the tool's run time. A new turn (the session starts working again) replaces the previous turn's blocks; after a turn ends its blocks stay until the next one starts.
- **WebSocket `live` frames**, to the same clients that receive the session's events (same ownership rule), at most one per session every 200 ms (the first change after a quiet spell goes out at once), carrying everything new since the last frame:

```
{"type": "live", "data": "<the JSON below, as a string, like every other frame's data>"}

{"id": "s12", "turn": 7, "ended": false,
 "blocks": [{"index": 3, "kind": "thinking", "append": "…new text…", "new": false},
            {"index": 4, "kind": "tool", "append": "", "new": true, "startedAt": "<ISO date>"}]}
```

  `turn` counts up from 1 per session; `index` counts blocks within the turn from 0; `new` marks a block's first appearance (with `startedAt`); `append` is text added since the last frame. When the turn ends, one more frame carries `"ended": true`.
- **`GET /sessions/:id/live`** returns the live turn as it stands: `{"turn": 7, "active": true|false, "blocks": [{"index", "kind", "text", "startedAt", "done"}]}`. It keeps the last 30 blocks, each with at most 4,000 characters of text (a cut block is "…" and its last 3,999 characters). `active` is false once the turn has ended. Before the session's first turn: `{"turn": 0, "active": false, "blocks": []}`. 404 for an unknown session; same ownership rule as the session's other endpoints.

### "Needs you" push

When a session starts waiting on a question, a plan approval or a tool permission, the server sends a push to the registered devices: title = the session name, body = the question text (first question), "Approve the plan?", or "Allow <tool>?". The payload carries `sessionId`, like the long-turn push. One push per pending request. The existing long-turn push is unchanged. An auto-mode block sends a separate "Blocked: …" push (docs/design-claude-uplift.md → Permissions (auto mode)); it doesn't make the session wait, so it isn't a "needs you" push. On the iPhone, the banner is suppressed when that session is on screen (existing rule). The push mirrors to the watch.

## iPhone app

### Read state

Read state is kept on the server and shared by every device (docs/design-claude-uplift.md → Session order, read state and background work). The server's `unread` flag is the only source; the iPhone app keeps no seen times of its own.

- The iPhone app marks a session read (`POST /sessions/:id/read`) when its screen is shown with the app in the foreground, and when a sessions update says the session on screen is unread while the app is in the foreground.
- Opening a session's detail screen on the watch, and any watch action on a session, marks it read too (the watch asks the iPhone app, which calls the server).
- A session is **unread** when the server says so: a turn that wasn't started by a peer message finished, and the session is now idle with no background work running.

### Relay

The watch sends requests; the iPhone app (woken in the background if needed) handles them and replies.

- **Refresh** (`{"type":"refresh"}`): the iPhone app fetches `GET /sessions`, plus each session's last assistant message from `GET /sessions/:id/history?limit=40` (reused when the session isn't working and its last activity hasn't changed), and replies with a snapshot. It also stores the snapshot as the watch's "last known state".
- **Mark read** (`{"type":"markRead","id":"s12"}`): calls `POST /sessions/:id/read` and replies `{"ok":true|false}` plus a fresh snapshot.
- **Action** (`{"type":"action","id":"s12","action":...}`), replying `{"ok":true|false}` plus a fresh snapshot:
  - `"answer"` with `"answers": {"<question>": "<answer>"}` → `POST /sessions/:id/answer`.
  - `"approve"` → `POST /sessions/:id/approve`.
  - `"deny"` → `POST /sessions/:id/cancel`.
  - `"continue"` → sends the message "Continue." (`POST /sessions/:id/input`).
  - `"reply"` with `"text"` → sends that text as a message.
  - `"stop"` → `POST /sessions/:id/cancel` (interrupts the running turn; the turn ends and anything queued is delivered, as usual).
  - `"sendNow"` → `POST /sessions/:id/queue/flush` (interrupts Claude's current step so it reads the queue now, like the phone's Send now).
  - An action also marks the session read.
- **Full message** (`{"type":"message","id":"s12"}`): fetches `GET /sessions/:id/history?limit=40` and replies `{"text": "<the last assistant message, Markdown as written>"}` (capped at 20,000 characters, cut with "…"), or `{"text": null}` when there is none, or `{"error": "server"}`.
- **Live** (`{"type":"live","id":"s12"}`): replies `{"live": <GET /sessions/:id/live, as JSON data>}` (or `{"error":"server"}`), and subscribes the watch to that session for 10 seconds from this request. While subscribed, the iPhone app keeps its own WebSocket to the server open and forwards each `live` frame for that session to the watch the moment it arrives, as `{"type":"liveFrame","frame":<the frame's data>}`. The watch repeats the request every 3 seconds while a session's screen is open, which renews the subscription; when it lapses, the iPhone app closes that WebSocket.
- If the iPhone app isn't signed in, every request replies with `error: "signedOut"`.

### Snapshot

```
{
  "fetchedAt": <date>,
  "error": "signedOut" | "server" | null,
  "sessions": [
    {
      "id": "s12",
      "name": "<display name>",
      "project": "<project name>",
      "state": "waiting" | "working" | "idle" | "starting" | "restarting" | "notRunning",
      "since": <date>,                      // when the state began (last status change)
      "inputAt": <date>,                    // your last input (the server's lastInputAt; else `since`)
      "unread": true | false,               // the server's unread flag
      "background": <n>,                    // background tasks still running (0 when none)
      "kind": "question" | "plan" | "tool" | null,
      "question": "<first question / request>" | null,
      "questions": [{"question", "options", "multiSelect"}] | null,
      "recap": {"goal", "progress", "next", "nextBy", "at"} | null,
      "lastText": "<last assistant message, Markdown, ≤ 600 chars>" | null,
      "queue": [{"id": "q3", "text": "<queued message>"}] | null   // the server's queue, oldest first
    }
  ]
}
```

- `state` follows the drawer's rules: `starting` / `restarting` / `notRunning` while the process isn't running; otherwise `working`, `waiting` (waiting for input, needs approval, plan ready) or `idle` (idle, unknown).
- `sessions` is ordered like the iPhone drawer: newest `inputAt` first, with the Ops session last.
- `lastText` keeps the message's Markdown. Past 600 characters it's cut at the last line break (or else word boundary) before the limit and ends with "…"; a code block left open by the cut is closed.
- `error: "server"`: the session list couldn't be fetched; `sessions` is the last good list.

## Watch app

### Session list

```
┌──────────────────────┐
│ MClaude         9:41 │
│ 1 needs you · 2 working · 1 unread │
│ ● Fix deploy         │  ← blue dot
│   2m                 │
│ ● Weekly meals       │  ← green dot
│   5m                 │
│ ○ mclaude-v1 watch   │  ← clay ring
│   20m                │
│ ● Drawer/resume      │  ← clay dot
│   1h                 │
│ Read · 14          › │
│ Updated 9:41         │
└──────────────────────┘
```

- **Summary line** under the title: the non-zero counts of `needs you`, `working` (working or with background work running) and `unread`, joined with ` · `. When all three are zero: "All caught up".
- **One list, no sections:** every session that needs you, is working, has background work running or is unread, in the snapshot's order (newest `inputAt` first, Ops last). Status never changes the order.
- **Row** (two lines): a dot and the name (one line, semibold); then the age of your last input (`inputAt`), muted (`now`, `14s`, `2m`, `3h`, `2d`). Tapping a row opens the session.
- **Dot**, the first that applies: blue (filled) = needs you; clay (filled) = working; clay ring (hollow) = background work running; green (filled, `#65BB30`) = unread.
- **Read row:** `Read · <count>` with a chevron, shown when any read session exists; it opens the Read list.
- **Footer:** `Updated <time>`, or `iPhone not reachable · updated <time>`, or `Can't reach the Mac · updated <time>`.
- **States:** no snapshot yet → spinner and "Connecting to iPhone…" (or "iPhone not reachable" once a refresh failed); `signedOut` → "Sign in to MClaude on your iPhone."; no sessions at all → "No sessions".

### Read list

Titled "Read". Every other session (idle, read, no background work, or its process isn't running), in the snapshot's order, with the same two-line rows (no dot). Tapping opens the session.

### Session detail

```
┌──────────────────────┐
│ ‹ Fix deploy         │
│ ● Needs you · 2m     │
│ mclaude-v1           │
│ GOAL                 │
│ Ship the retry fix   │
│ WHERE IT IS          │
│ Tests pass; ready to │
│ run the migration    │
│ NEXT                 │
│ Run it now or back   │
│ up first?            │
│ [ ▤ Full message ]   │
│ ┌──────────────────┐ │
│ │ Run now or back… │ │
│ │ ○ Run now        │ │
│ │ ● Back up first  │ │
│ │ ○ Other…         │ │
│ └──────────────────┘ │
│ [      Send      ]   │
└──────────────────────┘
```

When nothing is pending, the same screen ends with `[ ▤ Full message ]`, `[ Continue ]` (if Claude proposed a next step), `[ 🎤 Talk ]` and a small `Type…` field.

- **Header:** dot, state label (`Needs you`, `Working`, `Idle`, `Starting`, `Restarting`, `Not running`) · age; the project name, muted.
- **Recap:** three blocks, each a small muted uppercase label (`GOAL`, `WHERE IT IS`, `NEXT`) and its text. Without a recap yet: the last message under a `LAST MESSAGE` label (if any), rendered like the Last answer screen.
- **Full message** (secondary capsule, `doc.text` icon): shown when the session has a last message. Opens the Last answer screen.
- **Actions**, by situation:
  - **Question** (`kind: "question"`): a card per question with its text and its options as rows (radio for single-select, checkbox for multi-select), plus **Other…**, which opens text input (dictation, Scribble or keyboard) and selects Other with that text. **Send** (clay capsule) is enabled once every question has an answer; it sends `answer`. Single-select: the option label (or the Other text). Multi-select: ticked labels in option order, Other text last, joined with ", ".
  - **Plan or tool permission** (`kind: "plan"` / `"tool"`): the request text, then **Approve** (clay capsule) and **Deny** (secondary) buttons.
  - **Continue** (secondary capsule): when the session isn't working or waiting and its recap's `nextBy` is `"claude"`.
  - **Talk** (clay mic capsule, "Talk"): always, except while a question is pending. See Talk below.
  - **Type** (secondary, small): a text field that opens the watch's text input (keyboard, Scribble or dictation). Hidden while a question is pending. A non-blank result is sent as `reply`.
  - While an action is in flight its buttons are disabled and show a spinner. Success plays the watch's success haptic and refreshes; failure plays the failure haptic and shows "Couldn't send. Try again." under the buttons, keeping the selections.
- **While Claude works** (state `working`, or the live turn is still active), the recap, Full message, Continue and the question/approval area give way to the live view (below); the header stays.
- **Read:** opening the screen marks the session seen (and again when an update arrives while it's open).
- The screen scrolls with the Digital Crown, updates in place, and goes back to the list if the session ends.

### Live view

```
┌──────────────────────┐
│ ‹ Fix deploy         │
│ ● Working · 2m       │
│ The retry fires      │  ← thinking: muted, streaming
│ before the token     │
│ refresh, so 401s…▌   │
│ ── working ⋯ 12s ──  │  ← a run of tool calls
│ Found it: the retry  │  ← Claude's text: full brightness
│ ignores 401s…        │
│ QUEUED               │
│  ┆ use the new token ┆│  ← your queued message
│ (■)    (⌨)     (🎤)  │  ← bottom toolbar, always in view
└──────────────────────┘
```

- **Feed:** the live turn's blocks in order, growing as Claude writes.
  - **Thinking** in the muted text colour with inline Markdown; **text** in the primary colour with its full formatting (headings, lists, bold, code), rendered like the Last answer screen. A block that's still growing ends with a clay cursor.
  - **Tools:** each run of consecutive tool blocks is one quiet centred line: `working ⋯ <seconds>` counting up while it runs, then `worked <duration>` (e.g. `worked 41s`, `worked 2m 5s`). Commands and file names never show.
  - Long thinking blocks show their last 1,500 characters, starting with "…".
  - Before the first block arrives: "Starting…".
- **Following:** the feed stays scrolled to the newest line, just above the toolbar. On watchOS 11 and later, scrolling it yourself (a drag or the Digital Crown) stops following, and coming to rest at the bottom follows again; on watchOS 10 it always follows.
- **Queued:** under the feed, when the session's queue isn't empty: a small muted `QUEUED` label, each queued message (oldest first) as a muted bubble with a dashed outline, and "Claude reads these at its next step".
- **Bottom toolbar** (the watch's bottom bar, so the controls stay in view while the feed follows the newest line), three round buttons:
  - **Stop / Send now** (leading): `stop.fill` in the danger colour, labelled Stop, sending `stop` when nothing is queued; `paperplane.fill` in clay, labelled Send now, sending `sendNow` when something is queued. Either way Claude's current work is interrupted; with a queue, Claude reads the queued messages next.
  - **Type** (centre, `keyboard`): opens the watch's text input (keyboard, Scribble or dictation); a non-blank result is sent as `reply`.
  - **Talk** (trailing, `mic.fill`, clay): as on the rest of the session screen.
  - What you send with Type or Talk joins the queue while Claude works. A button shows a spinner while its action is in flight, and all three are disabled meanwhile; a failure shows "Couldn't send. Try again." under the feed.
- **Push, then poll:** while a session's screen is open (live view or not, so a turn that starts while you're looking switches to the live view as soon as Claude writes) the watch asks the iPhone for the live turn (`live` request) when it opens and every 3 seconds, and applies each pushed `liveFrame` as it arrives. A frame applies only when its `turn` matches and it continues from the blocks the watch has (no gap in `index`); otherwise the watch re-requests the live turn. If no frame has arrived for 1.5 seconds while the turn is active, the watch requests the live turn every second until frames resume.
- **Turn end:** when the live turn ends (or the session stops working), the screen goes back to the normal layout; the new recap arrives with the next refresh.

### Last answer

Titled "Last answer". Shows a spinner while it loads, then Claude's last answer in full, rendered for the watch:
- Headings (`#`…) as bold lines; paragraphs with inline bold, italic, inline code and links; bulleted and numbered list items with their marker and a hanging indent; quotes muted with a bar; code blocks in a small monospaced font on a raised card (wrapping, not scrolling sideways); tables as their rows in a small monospaced font; horizontal rules as a thin divider.
- Scrolls with the Digital Crown. "No message yet" when there's none; "Couldn't load the message" with a Retry button when the request fails or the iPhone isn't reachable.

### Talk

- Tapping **Talk** opens Apple's dictation on the watch directly: the text appears as you speak, and you tap **Done** when you're finished. The transcription happens on the watch; the iPhone only relays the message.
- **Done** with non-blank text sends it right away as `reply` (while Claude is working, the server queues it as usual). The Talk button shows a spinner while sending; success plays the success haptic; failure plays the failure haptic, shows "Couldn't send. Try again." and puts the text in the Type field so it can be sent again.
- **Cancel**, or Done with nothing said, sends nothing.
- If dictation can't be opened, the Type field's text input opens instead.

### Notifications

Tapping a MClaude notification on the watch opens the app on that session's detail screen.

### Refreshing

While the app is on screen it refreshes when it appears and every 15 seconds. A request with no reply within 10 seconds counts as "iPhone not reachable" (a late reply still applies). Refreshing stops while the app isn't on screen.

### Formatting

Claude's text keeps its Markdown everywhere on the watch, never shown as raw `**`, `#` or backticks:
- **Block formatting** (as on the Last answer screen): the last message on the session screen and the Last answer screen.
- **Inline formatting** (bold, italic, inline code, links): recap fields, question text, option labels, and plan/tool request text.

## Look

Black background. Text `#FAF9F5` / `#C2C0B6` / `#9C9A92`; raised cards `#30302E` with a 12pt radius; clay `#D97757` (working dot, primary buttons, tint); link blue `#5BA4E6` (needs-you dot); green `#65BB30` unread dot. System fonts (SF Pro).

## Packaging

A watchOS 10+ app embedded in the iPhone app (bundle id + `.watchkitapp`), same version and build number, shipped in the same TestFlight build.

## Scope

Not in this version: a complication, talking to the hub from the list, starting or ending sessions from the watch, and a recap card on the iPhone.
