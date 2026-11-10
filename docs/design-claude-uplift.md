# MClaude UI uplift: match the Claude mobile app

Goal (Richard, 2026-11-02): MClaude should look like it could be part of the Claude iOS app.

Sources:
- Design tokens were extracted from `/Applications/Claude.app/Contents/Resources/app.asar`. The desktop app uses the same design system as claude.ai.
- Layout comes from the App Store screenshots, saved in `/tmp/claude-design/shots/` (`strip.png` is the overview).

## Decisions (Richard)

| Topic | Decision |
|---|---|
| Theme | Follow system: light = ivory, dark = warm charcoal. Remove the hard-coded navy and black. |
| Fonts | Apple lookalikes, **not** Anthropic's proprietary fonts. Use New York (`.serif` design) for Claude's prose and SF Pro for the UI. Keep SF Mono for code and tool output. |
| Navigation | Claude-style drawer. No tab bar. |
| Tool calls | **Keep detailed.** Restyle only; don't collapse them. |
| Composer | **Restyle the current layout.** Keep today's buttons and the hold-to-talk mic, and apply the new colours and shapes. |
| App icon | New *original* mark in clay and ivory. **Not** Anthropic's spark/asterisk logo, so it can't pass as the official app. |

## Tokens

| Role | Light | Dark |
|---|---|---|
| bg page (`bg-100`) | `#FAF9F5` | `#262624` |
| bg raised: cards, composer, sheets (`bg-000`) | `#FFFFFF` | `#30302E` |
| bg sunken: user bubble, chips, secondary buttons (`bg-300`) | `#F0EEE6` | `#1F1E1D` |
| bg strong: pressed state, dividers block (`bg-400`) | `#E8E6DC` | `#141413` |
| text primary | `#141413` | `#FAF9F5` |
| text secondary | `#3D3D3A` | `#C2C0B6` |
| text muted: captions, timestamps | `#73726C` | `#9C9A92` |
| border | `#141413` at about 10–15% opacity | `#DEDCD1` at about 15% opacity |
| brand / clay: primary action, send, focus highlights | `#D97757`, pressed `#C6613F` | same |
| link / info | `#2C84DB` | `#5BA4E6` |
| danger | `#B53333` | `#DD5353` |
| success | `#3F8F1F` (approx.) | `#65BB30` (approx.) |

Rules:
- Every grey is **warm** (yellow undertone). No cool or blue greys, and no system grey fills for surfaces.
- Clay appears sparingly: the send button, the primary button and small highlights. Don't fill large areas with it.
- Radii: cards 12pt, composer 14–16pt, user bubble about 18pt, chips and pills capsule-shaped.
- Shadows are soft and low-contrast (black at about 6%, y 2, blur 8). In dark mode, rely on the surface steps instead of shadows.
- No gradients, glows or neon. Icons are SF Symbols in a regular or light weight, coloured with text-secondary.

## Components

- **Header:** a ☰ drawer button on the left; the session or project name in the centre (sans, semibold, small; tapping it, or the context gauge next to it, opens the Info screen); new-session and ••• buttons on the right. Tapping new-session opens the project picker sheet. On a session screen (the current session's cwd is known), long-pressing new-session (about 0.5s) creates a new session in that same project right away: no menu, no sheet. It fires a light haptic and selects the new session once the server returns it. Repeat long-presses are ignored while a create is still in flight. Where no current project is known, long-press does nothing beyond what a tap does. The background is the page bg, with no bar material and no hairline unless the content is scrolled.
- **Drawer:**
  - Opens by tapping ☰, a swipe from the left edge, or a **rightward swipe anywhere** (Richard, 2026-11-04). The swipe must be mostly horizontal (vertical travel under 0.6× horizontal) and at least 70pt, or a flick: predicted ≥220pt and at least 30pt actual. It's recognized simultaneously with scrolling, so vertical scrolling is untouched. It's ignored in two cases: when it starts on a text view that holds a selection (with 24pt of slack for the handles), because then it's a selection-handle drag; and when it starts on content that scrolls horizontally and is wider than its frame (tables, code blocks, image and attachment strips), because then it's scrolling that content. A horizontal scroller whose content fits doesn't block the swipe. The ☰ button and the left-edge swipe still open the drawer from anywhere. A swipe anywhere else opens the drawer and clears any leftover selection, so a stale selection can't quietly disable the gesture. The drawer slides over a dimmed page.
  - Top to bottom: the brand row, a "New session" row, a "Resume session" row (`clock.arrow.circlepath`, opens the Resume sheet), project filter chips, the session list, and Settings at the bottom.
  - **Session list** (redesigned 2026-11-04, Richard: grouping by project made it hard to find what's recent). It holds only open sessions, in **one list, not grouped by project**. There's no grouping toggle.
    - **Ordered by your last input only** (Richard, 2026-11-09: "don't reorder anything based on status, only use status circles to denote. Only order based on my last input time"). Sessions are sorted by `lastInputAt`, newest first (see Session order, read state and background work). Status never moves a row, and neither does a turn another session started by messaging this one. There are no Active or Recent sections and no header above the list. With an older server that sends no `lastInputAt`, the last status change is used instead.
    - Row: the name on line one, **semibold while the session is unread**, otherwise regular. Line two is muted: `<last input> · <project> · <status>`, e.g. `2m ago · mclaude-v1 · working`. The time is the same `lastInputAt` the list is sorted by, so the list always reads newest first.
    - **Status dot**, on the right (8pt). The first state that applies wins:
      1. **blue** (filled): needs you (waiting for input, a permission or a plan)
      2. **clay** (filled): working
      3. **clay ring** (hollow, 1.5pt stroke): idle, but background work Claude started is still running (background commands, agents, watchers)
      4. **green** (filled, the success colour): unread, i.e. a turn finished that you haven't seen
      5. none: idle and read
      - No dot while the process isn't running (starting, restarting, not running).
    - **Status word** (line two): `starting`, `restarting` or `not running` while the process is down. Otherwise **`replying to <peer>`** during a turn a peer message started (the peer's display name), **`<n> in background`** while idle with background work running, and the status label (`working`, `idle`, …) in every other case.
  - **Ops session** (Richard, 2026-11-08: "visually distinct", "pinned to the bottom of the MClaude project"). The session named **Ops** (case-insensitive) is the observability agent (docs/plan-observability.md → Ops session).
    - It sits in its own **Ops** section (with a small muted "Ops" header) at the very bottom of the drawer, after every other session, whatever its last input. Its status dot follows the same rules as any row.
    - Like any session, it's shown under `All` and under its project's chip (mclaude-v1), and search applies.
    - Its row shows a `stethoscope` icon before the name, and the name is in the Ops tint (violet: light `#6E56CF`, dark `#9E8CFC`).
    - On its session screen, the header shows the same icon before the name, in the same tint.
  - **Project chips:** a horizontally scrolling row of capsule chips: `All`, then one per project that has an open session, ordered by its sessions' most recent `lastInputAt`. Selecting one shows only that project's sessions; selecting it again goes back to `All`. The selection is saved (`@AppStorage`) and survives launches. If the saved project has no open sessions left, `All` is used. The chips are hidden when every open session is in one project.
  - **Search:** a search field sits at the top of the list, scrolled just out of view when the drawer opens, so pulling the list down reveals it (when the list is shorter than the drawer, it's simply visible). Case-insensitive match on the name and project. It applies on top of the chip filter. With no matches, a muted "No matching sessions" line shows.
  - **Swipe right on a row to end the session.** A red "End" action (`xmark.circle`) is revealed under the row. A full swipe ends it right away; a partial swipe leaves the button showing, and tapping it ends the session. Ending calls `DELETE /sessions/:id` (stops Claude and drops the session from the list; the transcript stays on disk, so it can be resumed). The row leaves the list, and a toast at the bottom of the drawer shows "Ended · Undo" for 4 seconds. Undo resumes it through the Resume endpoint and keeps the drawer open. If the ended session was the one on screen, the app switches to the most recent remaining session. A left drag still closes the drawer, wherever it starts.
  - **Long-press a row** for a context menu with **Rename** (an alert with a text field, prefilled with the name, saved with `PATCH /sessions/:id`) and **End** (destructive, same as the swipe).
  - Empty state: "No sessions yet" (or "Not connected") in muted caption.
  - Settings sits at the bottom.
- **Resume sheet:** opened from the drawer's "Resume session" row (the drawer closes first). A large-detent sheet titled "Resume session" with a Done button.
  - It lists **every Claude Code transcript on the server's laptop** that isn't an open session: MClaude sessions that were ended, and sessions started from a terminal. Newest first, by when the transcript was last written. Nothing expires, and **nothing can be deleted** from here.
  - Row: the name on line one (one line). Line two is muted: `<modified> · <project>`, plus an **In use** tag (clay text) when the transcript was written in the last 2 minutes. Line three, when there's a first prompt and it differs from the name, is the first line of it, muted, one line.
  - Name: the name MClaude last gave the session, else Claude Code's own title (`ai-title`), else the first prompt, else the project name.
  - A search field at the top (matching name, first prompt and project) and the same style of project chips (`All` plus every project that has a transcript, most recent first). The sheet's chip selection isn't saved.
  - Tapping a row resumes it: a spinner replaces the row's trailing area, and on success the sheet closes and the app opens the session. An **In use** row asks first: "This session may still be open in a terminal. Resuming it here too can mix up both copies." with Resume and Cancel. On failure, an alert shows the error and the sheet stays open.
  - States: a spinner while loading; "No other sessions on this laptop" when the list is empty; "No matching sessions" when search filters everything out; an error message and Retry when the request fails.
- **Server API for resume** (mclaude-server only, not the k8s backend). Both endpoints are for the laptop owner only (another signed-in user gets `[]` from the list and 403 from resume), because the transcripts are the owner's files.
  - `GET /transcripts` returns `[{id, cwd, projectName, name?, firstPrompt?, modifiedAt, inUse}]`, newest first.
    - `id` is the transcript's Claude session UUID (file name). Only `~/.claude/projects/*/<uuid>.jsonl` at the top level counts; subagent transcripts in subfolders don't.
    - It excludes transcripts of open sessions and transcripts with no real user prompt. A real prompt is a non-meta, non-sidechain `user` line whose text doesn't start with `<command-name>`, `<local-command`, `<command-message>` or `Caveat:`.
    - `cwd` comes from the first line that has a `cwd` field. `firstPrompt` is the first real prompt, whitespace-collapsed and capped at 200 characters. `name` is the name remembered for an ended MClaude session, else the last `ai-title` in the file, else omitted.
    - `inUse` means the file was modified less than 120 seconds ago. Only the first 256 KB and last 256 KB of each file are read, and results are cached by path, size and mtime.
  - `POST /transcripts/:id/resume` returns `{"status":"resumed","id":"s12"}`. If that transcript is already an open session, it returns `{"status":"open","id":...}` instead.
    - It registers a session with that Claude session id and cwd, and `hasConversation = true`. Its name is the transcript's `name` (else the first prompt capped at 60 characters, else the project name). It rebuilds the visible history from the transcript, sets last activity to the last event's time, and sets the owner.
    - Claude starts with `--resume` right away (see Session processes), except when the transcript was **In use**. Then it starts only on the first message, so a session that's live in a terminal doesn't get a second process until you send.
    - Errors: 404 for an unknown transcript; 422 `{"error":"directory missing"}` when its cwd no longer exists.
  - Ending a session (`DELETE /sessions/:id`, `/exit`, `/quit`) stays "stop and forget", but first saves the session's name under its Claude session id (`endedNames` in `~/.mclaude/managed-sessions.json`) so the Resume sheet can show it. A resume removes that entry.
  - On launch the app opens the session with the newest `lastInputAt`; with no sessions it shows an empty state.
- **User message:** right-aligned, sunken-bg bubble, sans 16, text primary, radius about 18. Attached photos sit as thumbnails above the bubble, matching the App Store "dog deck" screenshot. Attached videos sit in the same strip as poster tiles with a play glyph (see Video attachments).
- **Photo viewer:** tapping a photo (one the user sent, or a tool-result screenshot) opens it full screen, fitted, on the page bg, with a clay **Done** button in the nav bar. The image is laid out against the whole screen, under the nav bar and status bar, so showing or hiding the nav bar never moves it.
  - **Pinch** zooms around the point between the fingers, from 1× to 4×. Each pinch continues from the current zoom. Releasing past 4× springs back to 4×; releasing below 1× springs back to 1× and re-centres.
  - **Double-tap** zooms to 2.5× centred on the tapped point, or back to 1× if already zoomed.
  - **Drag while zoomed pans** the image, anywhere on screen, and never closes the viewer (Richard, 2026-11-08: "only allow swipe to exit when the screenshot isn't zoomed"). The image's edges stop at the screen's edges; dragging further does nothing. An axis where the zoomed image still fits the screen stays centred. Releasing mid-drag carries on in the direction of the flick and stops at the edge. To close a zoomed photo, double-tap back to 1× and swipe, or tap Done.
  - **Swipe to close at 1× only.** A drag that starts at 1× pulls the image 1:1 with the finger, in any direction. As it moves, the image shrinks slightly (up to 15%), the page behind fades (up to 70%) and the nav bar hides. Releasing more than 110pt from where it started, or flicking hard enough to carry 320pt, closes the viewer, and the image flies off in the flick's direction. Otherwise it snaps back. Whether a drag pans or swipes is decided when it starts (or when a pinch ends mid-drag), from the zoom at that moment.
  - VoiceOver: the hint reads "Swipe to close" at 1×; the zoom action steps the zoom up or down by 1× (between 1× and 4×).
- **Pending message (placeholder):** what the user sends shows at once at the bottom of the transcript as a placeholder bubble with a dashed outline. Its status reads "Uploading…" or "Sending…", then "Sent ✓" once the server has handed it to Claude. If the server queues the message instead, the placeholder goes away at once and the message shows in the queue.
  - **Queue group:** the server's queue for the session, always the last row of the transcript. Its header reads "Queued · N" (clock glyph, muted, tap to collapse or expand). Each queued message is a right-aligned bubble with a dashed outline, with **Edit** and **Delete** under it (also in its context menu). The footer reads "Claude reads these at its next step", with **Send now** (interrupts Claude's current step so it reads the queue at once).
  - **Delivered messages stay in the group until Claude reads them** (Richard, 2026-11-09: "when I switch between sessions, the queued messages disappear, even though they're still in queue"). At a step boundary or turn end the server hands the queue to Claude's stdin, but Claude only takes it, and its copy only reaches the transcript, at its next step: after a long tool or parallel tools, or at the end of the turn, seconds to minutes later. In between, the message moves from the queue to the session's **delivered** list. The app shows delivered messages in the same group, below the queued ones: the same bubble, muted, with the label "Delivered" in place of Edit and Delete, and no context menu, since it can't be changed any more. The header counts both: "Queued · 1 · Delivered · 1", or "Delivered · 1" when only delivered ones are left. Send now stays while anything is in the group, delivered messages included (Richard, 2026-11-10: "just because I can't edit it doesn't mean I don't want it to cut in"): a delivered message is already waiting in Claude's input, and interrupting the current step makes Claude read it at once as its next turn.
  - **When a delivered message leaves:** the server drops it, and the row disappears, when Claude's copy arrives as a top-level user message. It matches on the text, the way the app matches placeholders: whitespace collapsed, "[Image #N]" tags ignored, and a batch of messages delivered together matches one user message that contains each of them in turn. It's also dropped 10 s after the turn ends (a slash command, for example, isn't always echoed), when the Claude process exits or is restarted, and 10 minutes after delivery at the latest. Every change republishes the session list.
  - **Payload:** each session in `GET /sessions` and the WebSocket `sessions` frame carries `delivered`: `[{"id": "q12", "text": "…", "queuedAt": "…", "deliveredAt": "…"}]`, omitted when empty, next to `queue`. Delivered ids are the queue ids. Older apps ignore it. Each WebSocket `sessions` frame also carries a `seq`, and the app ignores a frame older than one it has already applied, so a late, older snapshot can't hide a queued or delivered message (docs/plan-observability.md → Queue delivery).
  - **Typed text while a question is pending is queued, not used as the answer.** While Claude waits on an AskUserQuestion, a message sent from the composer goes into the queue like any mid-turn message (`{"status":"queued"}`), and the question stays pending. The user answers on the question card (including Other for free text). Once the question is answered or dismissed and Claude finishes that step, the queue is delivered as usual. (Richard, 2026-11-08: "queued messages get submitted as answers if the AskUserQuestion tool is used. Instead, continue to queue the message, but allow the user to answer the question in the meantime.")
  - **Typed text that answers a plan or tool approval leaves no placeholder.** While Claude waits on a plan or tool approval, the server uses typed text as a rejection with "The user said: …" feedback. It never becomes a user message in the transcript, so no copy will arrive to clear the placeholder. `POST /sessions/:id/input` replies `{"status":"answered"}` in this case (otherwise `sent`, `queued` with a `queueId`, or `failed`). The placeholder goes away as soon as that reply arrives, as it does for a queued message. Older app builds read `answered` as `sent`. (Richard, 2026-11-07: old placeholders "floating around" in a session that kept asking him questions.)
  - **Removed, not hidden, when Claude's copy arrives.** Once the transcript has the user message Claude received, the placeholder is deleted and that message shows as a normal user bubble at its own position. (Richard, 2026-11-07: the long trip-planning session got "slow, especially when I'm just typing a message". Matched placeholders used to be hidden but kept for an hour, so a 5-second check redrew the whole transcript.)
  - **Only a newer message counts as the copy.** A transcript message matches a placeholder only if it's timestamped no earlier than 60 seconds before the send (allowing for clock differences between phone and Mac). Each transcript message clears at most one placeholder, oldest placeholder first. An earlier message with the same text ("yes", "ok") never clears a new placeholder.
  - **Matching:** a photo or video message matches the transcript message that contains its first uploaded path; one still uploading matches nothing. Text matches after removing the `[Image #N]` prefix and attachment paths and trimming whitespace, either exactly or on the first 30 characters (voice transcripts can differ slightly).
  - **Backstops:** a placeholder never handed to Claude is removed after 2 minutes; any placeholder is removed after an hour.
  - The check runs when new events arrive and every 5 seconds. When nothing needs removing it changes nothing, so it causes no redraw.
- **Text selection** (behavior from `ui-spec.md` → Conversation Events → Text Selection):
  - SwiftUI `Text.textSelection(.enabled)` only offers whole-block copy on iOS, which isn't enough.
  - User-bubble text, assistant prose and headings, code blocks, expanded thinking, and the text in the Event Detail sheet render through a shared `SelectableText` view: a non-editable, selectable, non-scrolling `UITextView` that sizes to fit its content.
  - It gets the same fonts, colours, line spacing, inline-code styling and links as the `Text` it replaces, built as an `NSAttributedString` with dynamic (light/dark) `UIColor`s.
  - Taps are handled inside `SelectableText`. A tap with no active selection runs the row's tap action, which opens the Event Detail sheet. A tap while a selection is active only clears it. A tap on a link opens the link.
  - Table cells keep `.textSelection(.enabled)` (copy per cell). So do Edit diff lines in the detail sheet (copy per line).
- **Assistant message:** **no bubble**. Full width, **serif (New York) about 17 with line height about 1.45**, text primary. Markdown headings use serif semibold. Inline code uses mono on a sunken bg.
- **Tool calls (keep detailed):**
  - Raised card, 12pt radius, hairline border.
  - The header row has a tool icon, the tool name in sans semibold 13, and the target in muted mono.
  - The body is mono 12 on sunken bg. Keep the existing expand and collapse behaviour.
  - Diff colours: muted green and red tints, not saturated.
- **Peer messages** (Claude Code `SendMessage` between sessions): collapsed From/To cards and a read-only thread sheet. Spec: `docs/plan-peer-messages.md`.
- **AskUserQuestion (inline):**
  - Raised card.
  - Options are rows with a radio (circle). For a question with `multiSelect: true`, they're checkboxes (rounded square) instead, and any number can be ticked. The selected row gets a clay ring and check.
  - **Other (free text)** (Richard, 2026-11-05: "I can't answer other using the current UI"):
    - Every question ends with an **Other** row, as in Claude Code, where the user can always type their own answer. A question with no options shows only the text field.
    - Tapping Other selects it and shows a multi-line text field under the row, focused, with the placeholder "Type your answer". In a single-select question, Other and the options are mutually exclusive: picking one clears the other. In a multi-select question, Other is one more checkbox alongside the options.
    - Tapping Other again deselects it and hides the field; the typed text is kept if it's reselected.
  - **Submit** is enabled only when every question has an answer: a ticked option, or Other with non-blank text. If Other is selected, its text must be non-blank. Until then the button reads "Answer every question".
  - **What's sent:** one answer per question. That's the option label; for multi-select, the ticked labels in option order with the Other text last, joined with ", "; or just the trimmed Other text. The app posts them to `POST /sessions/:id/answer` as `{"answers": {"<question text>": "<answer>"}}`. The server answers the pending AskUserQuestion with exactly those answers. It returns 409 if no question is pending and 400 if a question is missing an answer. On failure the card becomes interactive again, keeping the selections, and the error banner shows "Couldn't send your answer".
  - The submit button is a clay capsule, and cancel is text-secondary.
  - When answered, the card is read-only and the chosen rows are highlighted. A free-text answer, including the Other part of a multi-select answer, shows under the question as "Answer: …".
- **Composer (restyle current layout):**
  - Keep the existing icon row (camera, file, ⌘ skills, keyboard, stop) and the hold-to-talk mic.
  - **Camera button** (Richard, 2026-11-06: "default to the camera, which can then go into an image picker"):
    - The first icon is a camera glyph (`camera`), accessibility label "Take photo or attach from library". It's disabled while sending or once 5 attachments are pending or loading, as the photo button was.
    - Tap opens a full-screen in-app camera (photo only, back camera, flash off). Its controls, white on black:
      - ✕ (top-left): closes the camera, adding nothing.
      - Library (bottom-left, `photo.on.rectangle`): closes the camera and opens the photo picker (see Video attachments → Picker). The picker's selection limit is whatever's left of the 5.
      - Shutter (bottom-centre, white ring): takes the photo and shows it full-screen with **Retake** (bottom-left) and **Use Photo** (bottom-right). Retake returns to the live camera. Use Photo closes the camera and adds the photo to the pending strip, normalized like a picked photo (JPEG, long edge ≤ 2000px).
      - Flip (bottom-right, `arrow.triangle.2.circlepath.camera`): switches between back and front cameras. Hidden when the device has only one.
    - The photo is captured upright for however the phone is held, even though the UI stays portrait.
    - **No camera:** if the device has no camera (the simulator), or camera access is denied or restricted, the tap opens the photo picker directly. The first tap asks for camera access. If it's granted, the camera opens; if not, the picker does.
    - Videos come only from the library; the camera doesn't record.
  - The bar becomes a raised card with a 16pt radius and a hairline border. Icons use text-secondary.
  - Stop becomes a danger-tinted icon instead of a solid red circle.
  - The mic is a sunken-bg circle, turning clay while recording.
  - Replace the monospace "Hold to talk" with sans muted text.
  - **Hold-to-talk start** (build 124 crashed when recording couldn't start, 2026-11-08):
    - Each press tries to start recording **once**, when the finger first touches down. Moving the finger never retries.
    - Every start sets up and activates the audio session for recording, whether or not the keyboard-mode mic button was ever tapped, and whether or not the app has been in the background since.
    - **First press, permission not decided yet:** it asks for speech recognition and microphone access, and that press doesn't record. The next press does.
    - **Can't start** (permission denied, the recognizer is unavailable, no usable microphone input such as during a call, or the audio engine won't start): nothing records, the mic stays sunken, and the line where the transcript goes shows the reason in danger colour, e.g. "Microphone unavailable". It clears on the next press. A failed start leaves nothing behind, so the next press starts cleanly.
    - Releasing a press that never started recording sends nothing.
  - The send action, wherever text mode shows it, is a clay circle with ↑.
  - **Text field:** multi-line `TextField(axis: .vertical)` that grows to about 5 lines and then scrolls internally.
    - Autocorrect, spell-check and sentence-case autocapitalization stay **on** (system defaults; no `.autocorrectionDisabled()` and no `.never` autocapitalization).
    - On the software keyboard, Return inserts a newline and sending is via ↑ only.
    - With a hardware keyboard, Return sends and Shift+Return inserts a newline.
    - The icon row and ↑ stay bottom-aligned as the field grows.
    - **Sending clears the field at once** (on the main thread, in the same tap, before any network call), and the text being sent is passed along explicitly rather than re-read from the field. If an autocorrect, dictation or inline-prediction commit lands right after and puts back exactly the sent text, it's cleared again on the next run loop. If the send fails, the text is restored only when the field is still empty, so it never overwrites a newer draft. (Richard, 2026-11-04: "sometimes the composer bar doesn't clear".)
      - **Still-composing text at send.** iOS can hold text in the field that isn't committed yet ("marked" text), and SwiftUI won't push the cleared text into the field while it does, so the field used to stay full after a send (composer probe, build 124: "Give me an image" + grey " of"). The send therefore works on the text view directly:
        - **Inline prediction** (the grey ghost word iOS 17+ shows ahead of the cursor; marked text that starts at the cursor): it was never accepted, so it's **dropped** from the message (Richard, 2026-11-08: "drop the unaccepted word"). "Give me an image" + grey " of" sends "Give me an image". A message that would be only a prediction doesn't send.
        - **Any other marked text** (dictation still finishing, Chinese/Japanese composition; the cursor is at or inside its end): it's committed first and sent as part of the message.
        - The text view itself is then emptied (its marked text ended without committing a prediction), along with the app's copy, so the two can't disagree. The same applies to a photo caption.
- **Session list empty state:** centred serif line, e.g. "What should we work on?", plus a clay "New session" capsule.
- **Settings:** an inset grouped list on the page bg, with raised-bg rows and no system grey.
- **MCP servers:** the session ••• menu has an **MCP servers** item after Info, and Settings has an **MCP servers** row. Both are specified in `docs/plan-mcp-servers.md`.
- **ANSI/terminal colours** in tool output: remap to a warm palette that reads on both themes.

## Work groups and thinking

Added 2026-11-04 (Richard). Everything Claude does in a turn used to be spelled out in the transcript, which made it clunky. A finished turn now reads as its thinking, its work folded into summary rows, and its final reply. Thinking is never folded ("I care a lot about those thinking blocks").

- **Turns:** a turn is the run of top-level events after a user event (a message, task notification or interruption) up to the next one. It's used for live expansion.
- **Task-notification cards appear live** (Richard, 2026-11-05: "when the background agent comes back, shouldn't there be some chip that tells me"). In history, a task notification is a `<task-notification>` user line in the transcript file. Headless Claude doesn't put that line on stdout; it sends `{"type":"system","subtype":"task_notification", task_id, tool_use_id, status, output_file, summary}` instead. mclaude-server turns each of these into a `.user` event (uuid from the stdout line) whose text is a `<task-notification>` block shaped like the transcript's: `task-id`, `tool-use-id`, `output-file`, `status`, `summary`, plus `result` when there is one. The app's `TaskNotificationParser` renders that block, so a session shows the same card live and after a reload.
  - **Agents** (the `tool_use_id` is a known Agent/Task call): stdout's `summary` is the agent's final output. It becomes `<result>`, and the summary becomes `Agent "<description>" finished` (completed) or `Agent "<description>" <status>`, as in the transcript. Every other task keeps stdout's summary and gets no result.
  - `<` in field values is escaped as `&lt;`, so the agent's output can't close the block early.
  - **Foreground commands get no card.** stdout tracks a long foreground Bash command as a task too (`task_started` with `"is_backgrounded":false`). It sends a `task_notification` when the command ends, but Claude only gets the tool result, and the transcript has no notification. Those notifications are skipped, unless a `task_updated` later moves the task to the background.
  - The event doesn't start a turn on the server. The notification only says the task ended, and Claude picks it up at its next step.
  - **Mid-turn replay:** if Claude takes the notification during a running turn, stdout also replays it as a `<task-notification>` user line (with extra `note`/`usage` tags). The server counts the cards it has shown per task id. It removes one replayed block per counted card (an agent can notify more than once), and drops the replayed event if nothing else is left. The turn and status still update as for any user line. A notification taken while idle starts a new turn and isn't replayed.
- **Always visible, never folded:**
  - user messages and task-notification cards
  - thinking blocks
  - AskUserQuestion blocks
  - Blocked cards (auto mode denials, see Permissions (auto mode))
  - compaction markers, interruption notices, turn-duration rows
  - pending, working and queue rows
  - **all assistant text**, including narration between tool calls (Richard: intermediate output "should be a valid part of the transcript") and the final reply
- **Folded:** only tool calls (including Agent groups with their nested subagent events) and tool results. Each run of *consecutive* ones becomes one work group, and any other row ends the run. A turn that narrates between commands therefore reads as text, a small group, text, a small group, and so on.
  - Text never moves into or out of a group, so nothing changes identity when a turn ends. In an earlier draft the running turn's last text sat inside the group and popped out at the end, and that left the LazyVStack blank until scrolled.
- **Group id:** `wg-<first event uuid>`, stable while the run grows.
- **Summary row:**
  - Contents: a chevron, then "N steps" (one per tool call), then up to three kinds, most frequent first: commands (Bash, `!`), reads (Read), edits (Edit, Write, MultiEdit, NotebookEdit), searches (Grep, Glob), agents (Agent, Task), web (WebFetch, WebSearch), and the tool name for anything else. Then the elapsed time from the group's first to last event, when it's at least 1s.
  - An error (any tool result with `isError`) adds a red `xmark.circle`. While the group is the newest one in a running turn, a pulsing clay dot leads the row.
  - Sans 13, text-secondary, raised-card styling with a hairline, like a tool card.
  - Tapping toggles it. Expanded, the group's rows render as usual under the header, with a thin left rule.
- **Default expansion:**
  - Groups in the turn that's running (the last turn, while the session is working) start and stay expanded, so the work can be watched live.
  - When the turn ends, they collapse (animated over 0.25s, then the view re-pins to the bottom), unless the user isn't at the bottom. Then they stay expanded, so nothing jumps under the reader. When they collapse while the user is at the bottom, the view stays pinned to the bottom. (Builds 134–136 left them expanded as a workaround for blank screens; the transcript list below fixes the cause.)
  - Every other group (older turns, history pages, a session opened after the turn) starts collapsed.
  - A group the user has toggled keeps their choice for the life of the screen.
- **Pulsing dots** (the Working row, the live group's header) use `PhaseAnimator` on opacity only. A `repeatForever` animation started in `onAppear` also animated the dot's position, so it drifted toward where the row first appeared.
- **Thinking:** expanded by default and visually distinct.
  - Header: a brain icon and "Thinking" in muted caption, with a chevron.
  - Body: serif 14 *italic*, text-muted, line spacing 3, with a 2pt rule on the left and 10pt left padding.
  - Tapping the header collapses it; that choice also lasts for the life of the screen.
- **Exports:**
  - PDF and Full Page render work groups **collapsed** (summary rows only) and thinking **expanded**, which keeps screenshots short.
  - Markdown and plain text keep one line per tool call. They now include thinking: Markdown as a `> 💭 *Thinking*` quote with the text quoted line by line; plain text as `Thinking:` followed by the text.

## Transcript scrolling

Added 2026-11-04 (Richard: the conversation "sometimes left adjusts", and "sometimes the screen goes blank and I have to scroll up to find the last messages", on first load and mid-turn).

- **Leading anchors only:** `defaultScrollAnchor(.bottomLeading)`, and every `scrollTo` uses `.bottomLeading` or `.topLeading`. The centred anchors (`.bottom`, `.top`) split any excess content width, which shifted the whole column left by its 16pt padding until the next relayout.
- **Transcript list** (2026-11-10, Ops F-13–F-21): older history is a lazy stack; the newest 40 to 59 rows, and the bottom sentinel, are laid out normally below it. The boundary moves in steps of 20 rows, so a row moves from one part to the other (and is rebuilt) once per 20 new rows. Why: a lazy stack guesses the heights of rows it hasn't built, and transcript rows range from a one-line message to several screens (long replies, tool output, an expanded work group, a peer exchange). With guesses that far off, keeping the list pinned to the bottom put the screen where no rows were built: a blank transcript until the user dragged, after a group folded, a new turn started, the saved copy was replaced, a session was switched or the app resumed. Reproduced in a standalone app with no MClaude code: switching with a few very tall rows blanked 9 of 20 times, and 0 of 20 with the newest rows laid out normally.
- **Side padding inside each row** (2026-11-08): the transcript's 16pt side padding is applied to each row (and the history row at the top), not around the row stack. So every scroll target, including the bottom sentinel, starts at the column's left edge. With the padding around the stack, a leading-anchored `scrollTo` lined the target's left edge (16pt in) up with the screen's left edge and left the list scrolled 16pt sideways, until the user scrolled. Found by the alignment probe on Richard's phone (docs/plan-observability.md): horizontal offset 16 with the content exactly as wide as the screen, and no row wider than the screen.
- **Pin to bottom:** a jump to the bottom (new events while following, the user's own send, a message landing in the queue) is re-checked at about 120, 250 and 500ms. If the user isn't scrolling, is still following, and the bottom sentinel isn't on screen, it scrolls to the bottom again. A LazyVStack positions rows it hasn't built from estimated heights, so the first jump can land short (the conversation opening mid-way) or past the end. The first load (0 → N events) jumps without animation.
- **A tap that stops a scroll does nothing else (iOS 18+):** while the list is moving (scroll phase interacting, decelerating or animating), and for 0.35s after it stops, taps don't open the Event Detail sheet, toggle a work group or toggle a thinking block. Richard taps the screen to stop a fling, like anywhere in iOS, and it was opening the detail page. The state lives in a plain `ScrollMotion` object written from `onScrollPhaseChange`, so it never re-renders the list.
- **Overscroll recovery (iOS 18+):** if the offset ends up more than 48pt past the end of the content while the user isn't scrolling (`onScrollGeometryChange`), the view scrolls back to the bottom. This is the blank-screen case. It doesn't apply on iOS 17.
  - It only acts on a state that lasts about 250ms, and only once the list has scrolled (offset > 0). The LazyVStack reports one-frame content sizes while it swaps estimated row heights for real ones; scrolling on one of those left the list unanchored (the keyboard then covered the bottom) and could make it flip between two sizes every frame, pinning the CPU at 100% once the composer was used.

## Export (copy, share, Full Page screenshot)

Added 2026-11-04 (Richard). There is deliberately **no** per-turn copy button in the transcript (tried and removed at Richard's request); copying a whole message goes through the Event Detail sheet. Ways to get a conversation out of the app. Exports use the session's **complete** history: if older events haven't been loaded yet, they're fetched first (see History). Full Page does too.

- **Event Detail sheet → Copy:** a `doc.on.doc` menu in the sheet's toolbar, leading the Done button.
  - **Copy as Markdown** copies the event's raw text: user and assistant text, thinking text, tool input, and tool result content.
  - **Copy as plain text** copies the same text with inline markdown resolved (`**b**` → `b`, links → their text). Fenced code keeps its content without the fences.
- **Export conversation:** the ••• menu gains an **Export** submenu with three items: **Markdown**, **Plain text**, and **PDF**.
  - Each item builds a file named `<session display name> <yyyy-MM-dd>.md` / `.txt` / `.pdf` (filesystem-unsafe characters replaced with `-`) in the temporary directory, then presents the system share sheet (Save to Files, AirDrop, Mail, …).
  - A spinner replaces the menu label while the history loads and the file renders.
  - **Markdown layout:**
    - Header: `# <display name>`, then a line with `<cwd> · exported <date time>`.
    - Each user message: `**You:**` followed by its text. Photo paths become `[photo]`.
    - Each assistant text event: written verbatim.
    - Each tool call: one line, `` > 🔧 `<Tool>` <inputSummary> ``.
    - Each compaction: a line `--- Context compacted ---`.
    - Each thinking block: a `> 💭 *Thinking*` quote with the text quoted line by line.
    - Tool results, system/duration rows, the queue, and pending messages are omitted.
  - **Plain text:** the same structure with markdown resolved, as for "Copy as plain text". Labels are `You:` / `Claude:` / `Thinking:`, and a tool call is `[Tool] <inputSummary>`.
  - **PDF:** the transcript rendered the way it looks on screen (same rows, fonts, and light/dark colours as the current appearance). Page width is the screen width, with 16pt margins. The content is split into pages about 1.5 screen-heights tall (US-Letter aspect is not required). Interactive elements (AskUserQuestion buttons, Working indicator, queue, pending bubbles) are not rendered; answered questions render read-only.
- **Full Page screenshot:** the app adopts `UIScreenshotService`. While a session is open, the system screenshot editor shows **Screen | Full Page**. Full Page saves to Photos as a long image or to Files as a PDF; iOS handles both.
  - The app supplies a **single-page** PDF of the transcript, rendered like the PDF export (header and composer included).
  - It covers the **whole conversation**: older history is loaded first, like an export. The only limit is the 65,535px image-height ceiling. Past about 60,000pt (≈70 screens) the oldest events are dropped, and the page starts with the "Earlier messages omitted" note.
  - It's drawn in pieces of whole turns (a piece starts at a user message, about 60 events each), measured newest first until the height limit, then drawn top to bottom into the one page, with the main thread free between pieces. Laying out a long conversation in one go, and re-measuring it to trim, froze the app for a second or more (Ops F-23). Markdown and plain-text exports are built off the main thread for the same reason.
  - It's drawn at **1× PDF units** (1 unit per point). iOS flattens a shared or saved Full Page at about one pixel per unit. 3× (build 101) matched screenshot resolution, but Richard found it too heavy and chose 1× (2026-11-04). The editor still shows the page as sharp vectors. If the conversation is longer, the oldest events are dropped and the page starts with a muted line: "Earlier messages omitted. Use ••• → Export → PDF for the full conversation."
  - The visible rect points at the bottom of the page (the latest messages), so the editor opens there.
  - Outside a session screen (drawer, empty state, settings), the delegate returns no PDF, so only "Screen" is offered.
- **Screen chrome:** the PDF and Full Page renders frame the transcript like a screenshot.
  - **Top:** a static copy of the session header (☰ circle; status dot, session name and context gauge; new-session and ••• in a capsule).
  - **Bottom:** a static copy of the composer in its default hold-to-talk layout (camera, file, ⌘, keyboard, stop, "Hold to talk", mic circle).
  - In the paged PDF, the header is on the first page and the composer ends the last.
  - Neither is interactive or stateful, and the iOS status bar isn't drawn.
- **Rendering:** the PDF and Full Page render a non-lazy copy of the transcript with `ImageRenderer` into a PDF context. `ImageRenderer.scale` is 3, so the parts it has to rasterize (card shadows) are screen-resolution; at 1 they were blurry 1× bitmaps. Horizontal scroll views (code blocks, tables) are laid out in place in this mode, since `ImageRenderer` draws them empty; long code lines wrap at the page width. `SelectableText` (a UIKit view, which `ImageRenderer` can't draw) renders as a plain SwiftUI `Text` with the same attributed string in this mode, selected through an environment flag.

## History (load earlier messages)

Added 2026-11-04. Long sessions used to show only the last ~200 events: the server rebuilt only the last 200 transcript lines after a restart, kept at most 500 events in memory, and returned only the last 200, and the app kept only 200. Claude's transcript on disk (`~/.claude/projects/<cwd>/<claudeSessionId>.jsonl`) always has the full conversation.

- **Server:** `GET /sessions/:id/history?before=<uuid>&beforeTs=<iso8601>&limit=<n>` returns `{"events":[…],"hasMore":bool}`.
  - Events come from parsing the whole transcript, using the same parsing as the startup backfill (queued-message dedupe and repositioning). The parse is cached per file until its size or mtime changes.
  - The page holds the events strictly older than the cursor, oldest first. The cursor is matched by uuid; if that uuid isn't in the transcript, the server uses the first event with `timestamp >= beforeTs`. Live events can carry a server-made uuid and the server's receive time.
  - With no cursor, it returns the newest `limit` events.
  - `limit` defaults to 200 and is clamped to 1…1000. `hasMore` is true when older events remain.
  - Ownership checks match `/events`. `/events` is unchanged, so already-installed builds keep working.
  - Subagent transcripts (`<sessionId>/subagents/*.jsonl`) aren't paged. Older Agent tool cards show without their nested subagent events.
- **App:**
  - The per-session event cache is **not** capped any more. It used to trim to 200, which would throw loaded history away.
  - At the top of the transcript, while history remains, a row shows a small spinner and "Loading earlier messages…". When the user scrolls it into view, it fetches the next 100 older events with the oldest loaded event as the cursor, and prepends them.
  - It **never loads on its own** before the user has scrolled this conversation themselves, or while no events are loaded. Build 100 loaded on the first layout, when the list was still empty, then re-anchored to the old first row. That threw the reader away from the latest messages on open. After a user scroll ends with the row on screen, it loads; it doesn't depend on `onAppear`, which may have fired long before.
  - The scroll position is kept by re-anchoring, after layout, on the row that was first before the load (`.topLeading`). If the bottom sentinel is on screen too (a short conversation), it scrolls to the bottom instead. If the loader row is still on screen after a page lands (the reader is at the very top, or the page was short), it fetches the next page about 0.3s later, because it won't get another `onAppear` while visible.
  - Page events that duplicate loaded ones are dropped: either the same uuid, or the same type and text within 60s (the transcript copy of a live event). A page that was all duplicates advances the cursor and fetches again (up to 5 times).
  - When `hasMore` is false, the row becomes a muted "Beginning of conversation".
  - A session whose events load with none at all (a new chat) has no older history: it counts as complete, so the row shows nothing (no spinner, no marker). While that first load is still in flight, the spinner shows as before.
  - If a load fails, the row reads "Couldn't load earlier messages. Tap to retry".
- **Exports** (Markdown, plain text, PDF) and **Full Page** screenshots load the full history first, in pages of 500, into a copy of their own: the open transcript isn't changed, and keeps loading older pages only from the row at its top. (Loading hundreds of older events into the open transcript at once rebuilt it on screen and froze the app for a second or more: Ops F-23.)

## Saved transcripts (copy on the phone)

Added 2026-11-09 (Richard, Ops finding F-8). The event cache used to live only in memory. A session that hadn't been opened since the app last launched opened empty, and stayed empty until its first load returned. After the app wakes, the phone's network can take a second or more to come back, even when the server answers in milliseconds.

- **What's saved:** for each session, the newest 200 events in the cache, plus the session's Claude session id. One file per session, in the app's Caches folder (iOS may purge it; that only costs the copy), under a folder for the server's host and port, so another server's `s56` never shows up.
- **When it's saved:** after each successful events load for that session, and, for every session whose events changed since the last save, when the app goes to the background.
- **When it's used:** when a session opens with nothing in memory, its saved copy (if any) loads into the cache right away, before the events request is sent. The transcript shows it at once; the request then refreshes it as usual.
  - A copy whose Claude session id differs from the session's current one (the session was `/clear`ed while the app wasn't running) is deleted, not shown.
- **Refresh:** the fresh events merge into the shown copy by uuid, as with any load. If the fresh page holds the full 200 events and shares no event with the saved copy, the saved copy is too old to join up, so it's replaced by the fresh page; there's never a gap in the middle. Older history still loads from the "Loading earlier messages…" row.
- **Removed:** when the session is ended from the app, when its Claude session id changes, when a file hasn't been written for 14 days (checked at each background save), and all of them at sign-out.

## Compaction (`/compact`)

Added 2026-11-04 (Richard: "handle /compact gracefully"). Behaviour was checked against a real `claude --print --input-format stream-json` run (CLI 2.1.317).

- **What the CLI does:**
  - **Live stream:** `/compact [instructions]` runs as a command. The stream carries:
    - `system/status` with `"compacting"`, then `system/status` with `status:null, compact_result:"success"`
    - a `system/compact_boundary` with `compact_metadata {pre_tokens, post_tokens, duration_ms, trigger}`
    - an echo of the command as `<command-name>/compact</command-name>…<command-args>…</command-args>`
    - a `<local-command-stdout>Compacted </local-command-stdout>` user line
    - finally a `result`

    The summary itself is **not** on stdout.
  - **Transcript:** the boundary (`compactMetadata`, camelCase), then the summary as a user line with `isCompactSummary: true` whose `parentUuid` is the boundary, then the caveat, command and stdout lines. Same file, same sessionId.
- **Server:**
  - While `status == "compacting"` the session snapshot carries `compacting: true`. It's omitted otherwise, so old builds ignore it.
  - On `compact_boundary`, the context gauge moves straight to `post_tokens` (`contextTokens`). The compaction marker is emitted at the turn's `result`, once the summary is in the transcript: one `.compaction` event with the boundary's uuid, `text` = the summary (read from the transcript tail, matched by `parentUuid`), `compactPreTokens`/`compactPostTokens`, and `durationMs`.
  - The command echo becomes a normal user event with text `/compact` or `/compact <args>`, so the user's own message shows and the pending bubble clears.
  - `<local-command-stdout>` lines whose text (ANSI stripped) starts with "Compacted" are dropped.
  - **History parse:** `compact_boundary` → the same `.compaction` marker, and the following summary line (`isCompactSummary`, or the "This session is being continued…" prefix) merges into it, so there's one marker per compaction. A summary with no boundary (old transcripts) still renders as a marker.
  - **Queue:** a queued slash command (`/word…`, not a path like `/tmp/…`) is delivered **alone**: never joined with other queued messages and never at a mid-turn step boundary. Messages queued after it wait for the next turn end.
- **App:**
  - The Working row reads "Compacting conversation…" while `compacting` is true.
  - The marker is a divider: "Context compacted · 23.5k → 3.5k tokens", or just "Context compacted" without counts, with a small chevron. Tapping it opens the Event Detail sheet ("Context Compacted"), which shows "Context: 23.5k → 3.5k tokens" and then "Summary Claude kept" with the summary rendered as prose. With no summary it says "No summary was saved for this compaction."
  - The divider is rendered in PDF / Full Page exports (without the chevron) and written as `--- Context compacted ---` in text exports.
  - Token counts are formatted as `812`, `23.5k`, or `982k` (≥100k rounded to whole k).

## Local slash commands (`/reload-plugins`, `/cost` …)

Added 2026-11-07 (Richard: "Reload plugins doesn't get acknowledged as a response"). Behaviour was checked against a real headless run (CLI 2.1.317). It covers commands the CLI runs itself, without calling the model. `/compact` has its own section above.

- **What the CLI does:**
  - **Live stream:** there is no replayed user line for the command. Instead the CLI sends one `assistant` message with `model: "<synthetic>"`, `local_command_run {command, args}`, and the output as a text block. Then it sends a `result`.
  - **Transcript:** the enqueue/dequeue queue operations for the typed text, a caveat line (`isMeta`), a `<command-name>/x</command-name>…` user line, and the output as `{"type":"system","subtype":"local_command","content":"<local-command-stdout>…</local-command-stdout>"}`. Older CLIs write the output as a user line instead.
- **Server:**
  - **Live:** on a top-level assistant message that has `local_command_run`, the server first emits a user event with the command as typed: `/command`, or `/command args` when args aren't empty. That clears the app's pending bubble and keeps the command above its output. It's skipped when a user event with that text has already been emitted this turn. Then the output follows as a `.text` event (`model "<synthetic>"`). The `result` ends the turn as usual.
  - **History:** the typed command already shows, from its queue operation. The `system/local_command` line, and a `<local-command-stdout>` user line, become a `.text` event (`model "<synthetic>"`) with the tags and ANSI codes stripped, so the output reads the same live and after a reload. Output that's empty after trimming is dropped. Output starting with "Compacted" is dropped, as in the `/compact` section.
- **App:** no special handling. The command is a normal user bubble and the output is a normal assistant reply, followed by the turn-duration line.

## Clearing (`/clear`)

Added 2026-11-09 (Richard: the Ops session, `/clear`ed hours earlier, showed as "Not open in MClaude" in its peers' Messages sheets, and its conversation came back as the pre-clear one after a reload). Behaviour was checked against a real headless run (CLI 2.1.317).

- **What the CLI does:**
  - **Live stream:** `/clear` sends a top-level `{"type":"conversation_reset","trigger":"clear",…}` (still carrying the old `session_id`), then a `system/init` whose `session_id` is a **new Claude session id**. Every later message carries the new id: first the replayed command as a `<command-name>/clear</command-name>…` user line (the input's `uuid` comes back as its `uuid`, since the server always sends one), then the `result`.
  - **Transcript:** a new file, `<new id>.jsonl`, starting with the caveat line and the same `<command-name>/clear</command-name>…` user line. The old file stays on disk unchanged.
  - `--resume <id>` keeps the same id, so an id change is never a normal restart.
- **Server:**
  - **Detecting it:** a top-level `system/init` whose `session_id` differs from the session's Claude session id means the conversation was reset (by `/clear` or anything else that resets it).
  - **Following the new id:** the session's Claude session id becomes the new one and is persisted at once. Everything keyed by it follows: history pages and the startup rebuild read the new transcript, peer messages are observed as the new id, the Resume sheet counts the new transcript as in use, and a process restart resumes the new conversation.
  - **The old conversation** is remembered the way an ended session is: its name goes into `endedNames` under the old id (when it had a conversation), so the Resume sheet lists it by name and it can be resumed as its own session. The old id is also kept in the session's list of earlier ids, so peer messages from before the clear still name and open this session (`docs/plan-peer-messages.md`).
  - **The visible history restarts:** the in-memory events and output lines are dropped, and so are the turn's per-conversation leftovers (subagent info, task-notice bookkeeping, pending peer sends, a pending compaction). The context gauge goes to 0 until the next reply reports its size. Token and cost totals are kept, because they count the session's spending, not the conversation's.
  - The sessions list is published with the new `sessionId` right away.
  - **The `/clear` line:** the `<command-name>/clear</command-name>` user line becomes a user event with text `/clear`, both live (the replay that follows the init) and in history, so the user's command opens the new conversation, the pending bubble clears, and live and reloaded transcripts match.
- **App:** a `sessionId` change already drops the session's event cache, output cache and saved transcript copy (Saved transcripts, above), so the transcript restarts at the `/clear` line.

## Video attachments (screen recordings)

Added 2026-11-04 (Richard: attach screen recordings so Claude can analyze them). Claude can't read video, so the server turns each video into an **index**: a few overview frames Claude can `Read`, plus a motion timeline that says where the screen changed. The index is a starting point, not the only input. The original video stays untouched, and for an animation bug Claude extracts the relevant stretch at full frame rate itself (Richard, 2026-11-04: "treating it as an index rather than the only input").

- **Picker:** the camera's Library button (or the composer's camera button, when there's no usable camera) opens `PhotosPicker` with `.any(of: [.images, .videos])` and `preferredItemEncoding: .current`. Videos count toward the same 5-attachment limit as photos, in pick order.
  - A video is loaded as a **file representation** (`.movie`) and copied into the app's temp dir. It is never read into memory.
  - Videos over 500 MB are skipped, and the strip shows "Video over 500 MB skipped" until the next pick.
  - **Pending strip:** a video tile shows its first frame (`AVAssetImageGenerator`, preferred transform applied), a small play glyph in the centre, and a duration badge (`0:12`, `1:05:03`) in the bottom-left corner. The ✕ removes it like a photo. The footnote counts both kinds: "1 photo attached", "2 videos attached", "1 photo, 1 video attached".
- **Upload — `POST /videos`:**
  - The body is the raw file, uploaded from disk (`URLSession.upload(fromFile:)`) with a 10-minute timeout. The server streams it to `/tmp/mclaude-screenshots/video-<digits>.<mov|mp4>` and rejects anything over 500 MB with a 413. The name is claimed with an exclusive create, as `/screenshots` does. The extension comes from the ISO-BMFF brand: `qt  ` → `.mov`, anything else → `.mp4`. Anything without an `ftyp` box is rejected with a 415.
  - Then it builds the index (below) and answers `{"path": "<video path>", "input": "<video path> [video: …]", "poster": "<poster path>", "duration": 12.4, "frames": 14, "changes": 9}`. `changes` is the number of bursts in the timeline. Only `input` goes into the message.
  - If ffmpeg fails, it answers 422 and deletes the upload. The app shows "Video upload failed".
- **Frame extraction** (`VideoFrames.swift`, `/usr/local/bin/ffmpeg` + `ffprobe`):
  - `ffprobe` reads the duration and whether there's an audio stream. If AVFoundation reports a longer duration, that one wins. Screen recordings hold their last frame until recording stops: ffprobe's duration ends at the last frame, while AVFoundation's includes the hold and matches what Photos and the pending tile show (0:39, not 0:36).
  - **Analysis pass:** one decode of the video, with `scale` to a 640px long edge, then `select='gte(scene,0)'` and `metadata=print`. This gives every frame's index, time and scene score.
  - **Selection:**
    - Frames with score > 0.08 are change candidates. Candidates less than 0.5 s apart form one burst (an animation, fade or scroll). A burst then extends through adjacent low-level motion: frames scoring above 0.002 that are less than 0.25 s apart. A slow fade's tail changes too little per frame to be a candidate, but it is still moving. In the first real recording, the dark-mode switch crossed 0.08 only until 10.85 s but kept moving until 11.12 s. Without the tail, the timeline said 0.16 s and Claude reported that as the transition's length.
    - Each burst is represented by its **settled** frame: the last frame up to 0.7 s after the burst's last candidate, but before the next burst starts. Slow fades keep changing below the threshold after the last candidate, so the last candidate itself is often mid-transition.
    - The first and last frames are always kept.
    - Fallback minimum rate: between two kept frames more than `max(5 s, duration / 20)` apart, a frame is added near every such step. It's taken only from quiet frames: not a candidate, and not inside a burst (start to settled frame). Long static stretches still get frames this way. A variable-frame-rate screen recording has no frames in a static stretch, so nothing is added there, because the screen didn't change.
    - Cap 40 frames. If over, the threshold is raised (×1.25 per step) until the set fits. If even a threshold above 1 doesn't fit, the set is subsampled evenly, keeping the first and last frames.
  - **Extraction pass:** `select` by frame index, scaled so the long edge is ≤ 1600 px, `-vsync vfr`, JPEG quality 3. The frames go into `/tmp/mclaude-screenshots/video-<digits>/`, named by timestamp: `t012.4s.jpg` (`t%05.1fs`; a clash gets `-2`).
  - **Motion timeline:** `<dir>/changes.txt` lists **every** burst found at the base threshold, whether or not the cap kept a frame for it. The overview frames are the settled states, so the in-between frames of an animation are only reachable through this timeline.
    ```
    # Motion timeline for /tmp/mclaude-screenshots/video-123.mov
    # 38.5s, 912 frames decoded (23.7 fps average; screen recordings skip frames while the screen is still)
    # Each line is a stretch where the screen changed (scene score > 0.08; changes < 0.5s apart merged;
    # the end includes the motion's low-level tail, so it's when the screen stopped moving).
    # To find something: Read the overview frames first (each shows the screen once its stretch settled),
    # then extract the stretch you care about frame by frame (command at the end).
    # start-end  length  peak  overview frame
      4.19-4.30s     0.11s  0.48  t004.5s.jpg
      10.67-11.12s   0.45s  0.23  t011.8s.jpg
      27.90-28.02s   0.12s  0.09  -
    # These are overview frames only; the original video is untouched. To see a stretch frame by frame
    # (every real frame, named by its time in ms: t010667ms.jpg = 10.667s), set -ss/-to to that stretch
    # (from 0.2s before its start to 0.7s after its end). Example, for the longest stretch:
    #   mkdir -p /tmp/mclaude-screenshots/video-123/dense && ffmpeg -v error -ss 10.4 -to 12.1 -copyts -i /tmp/mclaude-screenshots/video-123.mov -fps_mode passthrough -frame_pts 1 -enc_time_base 1:1000 -vf "scale=…1600…" /tmp/mclaude-screenshots/video-123/dense/t%06dms.jpg
    # Screen recordings run at up to 60 fps during animations, so a 1s stretch can be 60 frames: Read selectively.
    ```
    The "how to use this" lines exist because Haiku, given only a ready-made command, ran it verbatim on the wrong stretch without ever opening the overview frames. The example command covers the longest burst, from 0.2 s before its start to `settle` (0.7 s) after its end. `-copyts` keeps absolute times, so the file names line up with the timeline. The server creates `<dir>/dense/` up front, because ffmpeg's image writer doesn't create directories. In the first real test, Claude's own ffmpeg call failed with an I/O error on the missing directory and it gave up.
    The overview frame is the kept frame between the burst's start and 0.7 s after its end, or `-` if the cap dropped it. At most 1000 burst lines are written, then `# … N more`. With no bursts at all, the file says `# No changes detected.`
  - **Poster:** a copy of the first frame, scaled so the long edge is ≤ 480 px, saved as `/tmp/mclaude-screenshots/video-<digits>-poster.jpg`. The app fetches it by basename from `GET /screenshots/`.
  - **Audio:** if there's an audio stream, it's extracted to `<dir>/audio.wav` (16 kHz mono PCM). If `volumedetect` reports a max volume at or below −80 dB, the track is silent: the wav is deleted and not mentioned. There's no transcription, because Whisper isn't installed. The note just tells Claude the audio exists.
- **Input sent to Claude:** each video contributes `<video path> [video: <duration>s, <N> overview frames in <dir>/ named by timestamp, motion timeline <dir>/changes.txt<, audio: <wav>>; the original is untouched, extract any stretch at full frame rate with ffmpeg (command in changes.txt)]`. The duration has one decimal. The note never contains `]`, so the app and titler can match it with `\[video:[^\]]*\]`. These fragments are joined with the photo paths, in pick order, and then the caption, into the one input the app already sends. Nothing becomes an image block automatically: `.mov`, `.wav` and the frame directory aren't image paths. Claude Reads the frames it needs, or re-runs ffmpeg on the original.
- **Session titles:** `SessionTitler.typedText` drops the `[video: …]` note along with the paths, so a video-only first message counts as attachment-only.
- **Transcript:**
  - The user-message parser matches `/tmp/mclaude-screenshots/video-<digits>.(mov|mp4)` plus its optional ` [video: …]` note, alongside photo paths. Both are removed from the bubble text.
  - In `SentPhotoStrip`, a video renders as its poster, with a play glyph and a duration badge parsed from the note.
  - Tapping a video opens a full-screen `VideoPlayer` (AVKit) that streams `GET /screenshots/<video name>` with the bearer token, via `AVURLAsset`'s HTTP header option. It has a Done button and swipe-down to dismiss.
  - Editing a queued message keeps each attachment's full fragment, note included.
  - Text exports write `[video]` where a video was, as they write `[photo]` for photos.
- **`GET /screenshots/:name`** streams the file from disk instead of reading it into memory. It honours a single `Range: bytes=a-b` request (206 + `Content-Range`, `Accept-Ranges: bytes`), which AVPlayer needs. It serves `.mov` as `video/quicktime`, `.mp4` as `video/mp4` and `.wav` as `audio/wav`.

## Session processes (always running)

Added 2026-11-05 (Richard: every open session should stay reachable by other sessions, which needs a running Claude process; "automatically restarted if it hasn't been ended intentionally").

- **Rule:** every open managed session on this server keeps a Claude process running. Only ending it stops it for good: `DELETE /sessions/:id` (drawer swipe or End), `/exit` or `/quit`. k8s sessions aren't covered.
- **Server startup:**
  - After orphaned processes from the previous run are stopped, every open session is started.
  - Sessions with a conversation start with `--resume`. Sessions created but never messaged start fresh with their session id.
  - Launches go one at a time, in session order. The next session starts once the previous one's MCP servers have finished connecting (none still `pending`, see `docs/plan-mcp-servers.md` → Startup pacing and recovery), or after 60 seconds, whichever comes first. Each session brings up about a dozen MCP servers, and starting them all at once starved the Mac until many timed out.
  - A session that isn't started at boot (In use), or whose launch fails, doesn't hold up the next one.
  - A session still waiting its turn shows as `starting`. Sending it a message starts it right away.
  - Sessions that were mid-turn still get the "Continue where you left off" message, as before.
  - A session whose launch fails is retried like a crash (below). It never blocks the server from coming up or other sessions from starting.
- **New sessions:** a session created with `POST /sessions` starts its process right away, without waiting for the first message.
- **Resumed sessions:** a session resumed from the Resume sheet also starts right away, unless its transcript was **In use**. An In use session stays without a process until its first message, even across server restarts, because it may still be live in a terminal. After that first message it follows the rule like any other session.
- **Unexpected exit (crash, killed):**
  - The session restarts with `--resume` after a backoff of 1, 2, 4, 8 and then 16 seconds. A turn that was running gets the "Continue where you left off" message, as today.
  - After more than 5 crashes within 5 minutes it stops backing off and retries **every 10 minutes**, indefinitely, instead of giving up.
  - Sending a message retries right away.
  - While it's down the session's status is `unknown`, and the transcript shows the existing "⚠︎ Claude exited" and "↻ Restarted Claude" lines.
- **No idle stop:** processes aren't stopped for being idle. The earlier 2-hour idle reap is removed. A typical idle Claude process uses about 80–210 MB.
- **Server stop (drain):** unchanged. Running turns finish, every process stops, and all of them start again at the next startup.
- **Server restarts are invisible.** After a restart, every session that was open is open and running again, with the same status words. Nothing about the restart shows in the transcript or the terminal view: no "↻ Server restarted" line, and the "The previous turn was interrupted by a server restart. Continue where you left off." message sent to a mid-turn session isn't shown as a user message, live or in history. Crash notices ("⚠︎ Claude exited", "↻ Restarted Claude") still show, because a crash is something the user should know about.
- **Process state (server → app):** each session in the sessions list gains an optional `process` field. It's omitted for k8s sessions. The existing `status` field and its values are unchanged, so older app builds keep decoding the list.
  - `starting`: a process was launched and hasn't reported ready yet. Ready means Claude's `system` `init` line, or its first output line of any kind.
  - `running`: the process is up. Only now does `status` describe the session (idle, working, …).
  - `restarting`: the process exited unexpectedly or failed to launch, and a restart is scheduled (backoff or the 10-minute retry).
  - `stopped`: deliberately not running, i.e. an In use resumed session waiting for its first message.
- **Status word (app):**
  - When `process` is present and not `running`, the drawer row's status word and the ••• menu's status label show **starting**, **restarting** or **not running** instead of the status label. Neither the drawer row nor the header shows a status dot.
  - When `process` is `running` or absent, the status label shows as today. So "idle" always means a running process that's ready for a message.

## Session order, read state and background work

Added 2026-11-09 (Richard: peer-message traffic "reorders my entire recently updated, since A2A counts as an update"; "have a status circle for finished but unread"; a status for "temporarily not working but active background agents or watchers"; "I also need it to know if the background agents were interrupted by a restart"). mclaude-server only; k8s sessions omit every field below.

**Sessions list fields (server → app).** Each session in `GET /sessions` and the WebSocket sessions update gains these optional fields. Older apps ignore them.
- `lastInputAt` (ISO 8601): the last time **you** gave the session input.
  - Counts: a message you send (when you send it, even if it's queued), an answer to a question, approving or denying a plan or permission, and an interrupt.
  - Doesn't count: peer messages, background-task notifications, the server's own messages (restart and crash resumes), automated input from the Ops dispatcher, rename, recap, restart and read.
  - A new session starts at its creation time. Resuming a transcript from the Resume sheet sets it to the moment of the resume.
  - It's persisted with the session. A session saved before this field existed gets the time of the last real prompt in its rebuilt history (a user line that isn't only peer messages, a task notification or a server message), else its creation time.
- `unread` (bool, omitted when false): a turn finished that you haven't seen. See Read state.
- `peerTurn` (string): during a **peer turn** (below), the display name of the peer whose message started it (its Claude Code name when it isn't resolved further). `peerTurnPeer` (string): that peer's Claude session id, when resolved. Both are omitted otherwise. The app shows the name of your open session with that Claude session id when there is one, else `peerTurn`.
- `backgroundTasks` (int, omitted when 0): how many background tasks Claude started are still running. See Background work.

**Peer turns.** A turn that a peer message started, with no input from you and not in answer to something you asked for, is a peer turn.
- The user line that starts the turn holds only peer messages: it has `peerMessages` and no `peerOtherText` (docs/plan-peer-messages.md → Event enrichment). A peer message delivered while a turn is already running never starts a peer turn.
- **Replies to your requests aren't peer turns** (Richard, 2026-11-09: "I was the one to intentionally tell you to test it, so I want the results"). For each peer, the server remembers whether this session's most recent message to it was sent from a peer turn or from any other turn. A turn started by a message from a peer is a peer turn only when this session has never messaged that peer, or its last message to it was sent from a peer turn. So when you ask a session to message another and the answer comes back as a new turn, that turn is a normal one: it isn't folded, it sets `unread` when it ends, and the status word stays `working`.
- Peers are matched by Claude session id, else socket address, else Claude Code name.
- The same rule is applied to history (after a restart, and to history pages), walking the events in order. Transcripts don't record turn ends, so a top-level user line that directly follows a tool result counts as delivered mid-turn (Claude takes messages only between a tool result and its next step), and any other top-level user line starts a turn. A turn-time event, where there is one, also ends a turn. On an older history page the pass starts fresh at the page, so a reply whose message was sent on an earlier page counts as a peer turn.
- It stops being one when anything else reaches Claude during it: your message delivered mid-turn, or a question, permission or plan prompt (you're needed, so it's no longer quiet).
- A peer turn never sets `unread`, and doesn't send the long-turn "finished" push.
- `lastInputAt` isn't touched by a peer turn, as by any non-input.

**Read state.** It's kept on the server, so a session read on one device is read on all of them (iPhone, iPad, Watch).
- **Set:** when a turn that isn't a peer turn ends (Claude's `result`), the session becomes *unread pending*.
- **Shown:** `unread` is true while the session is unread pending, idle (no turn, nothing waiting on you, process running) and has no background tasks running. So a turn that leaves background agents running isn't "finished" yet; it turns unread once that work and the turns it triggers are done.
- **Cleared:** by `POST /sessions/:id/read`, and by any input from you to the session.
- It's persisted with the session.
- `POST /sessions/:id/read` → `{"ok": true}`. Same access rule as the other session endpoints (owner or laptop owner, else 404). It publishes the sessions list when it changes something.
- **When the iPhone app marks a session read:** when the session's screen is shown with the app in the foreground (opening it, switching to it, the app coming back to the foreground on it), and whenever a sessions update says the session on screen is unread while the app is in the foreground. Opening a session's detail screen on the Watch, and any Watch action on it, also marks it read (docs/plan-watch-app.md → Read state).

**Background work.** Claude's background commands, background agents and watchers are tasks on its stdout: `task_started` with `"is_backgrounded": true` (or a foreground task that a later `task_updated` patch moves to the background), with a `task_type` (`local_bash`, `local_agent`, …) and a `description`. A task ends with a `task_updated` patch whose `status` is no longer running (`completed`, `failed`, `killed`, …), or with its `task_notification`, whichever comes first.
- The server keeps each session's running background tasks (id, type, description), persisted with the session. `backgroundTasks` is their count.
- **Interrupted by a restart:** background tasks live inside the Claude process, so they die whenever it does: a server restart, a crash, or a deliberate restart (Restart Claude, Restart all). A newly launched process never has tasks running, so every task still on the list when a process is launched was interrupted. When the server stops the process itself (a restart or the shutdown drain), Claude Code first reports its background tasks as `stopped` or `killed`; those endings don't remove a task, so it's still on the list for the note. A task that completes during the stop is removed as usual.
  - The list is cleared and Claude gets a note right after the launch: `A restart stopped your background tasks; they won't report back: Agent "Map server code", command "npm test". Restart any you still need, then continue.` Agents are listed as `Agent "<description>"`, background commands as `command "<description>"`, anything else as `<task_type> "<description>"`.
  - When the session also gets the "Continue where you left off" message (it was mid-turn), the note is appended to that message, so it's one turn. Otherwise the note is sent on its own and starts a turn, so Claude relaunches what it still needs without waiting for you.
  - The note is hidden from the transcript, live and in history, like the server-restart message. Interrupted tasks get no status of their own in the app.
- **App:** the drawer row and the session header show a hollow clay ring while the session is idle with `backgroundTasks` > 0, and the status word `<n> in background` (Drawer → Session list).

**Session header dot.** The dot before the name in the session header follows the drawer row's dot rules, without the green unread state (the session on screen is read).

## Permissions (auto mode)

Added 2026-11-08 (Richard). Sessions used to run with every permission check skipped, so a bad command or a prompt injection had full access to the Mac. They now run in Claude Code's **auto mode**. Routine actions (reading, editing, ordinary commands) run without asking. A classifier blocks risky ones, such as destructive git operations, sending secrets off the machine, or weakening security settings.

- **Launch:** every managed session's Claude process starts with `--permission-mode auto` and `--permission-prompt-tool stdio`. AskUserQuestion and ExitPlanMode still reach the server as approval requests, as before. So does any tool that still asks the host for permission; it shows as "Allow <tool>?" with Approve/Cancel.
- **A block is final.** In headless mode Claude Code never turns a block into a prompt. The call is denied, and Claude gets an error tool result: `Permission for this action was denied by the Claude Code auto mode classifier. Reason: [<rule>]. …` (or `The server-side auto mode classifier judged this action dangerous …`). Claude then tries a safer way, or stops and explains what it needs. Nothing on the server waits on a block, and the session's status doesn't change.
- **Unblocking:** reply in the chat, e.g. "yes, force-push it". Claude's next attempt is judged with that explicit request in the transcript.
- **Blocked card (app):** a tool result whose error text contains `auto mode classifier` shows as a **Blocked** card, not as a folded tool result.
  - Always visible, never folded. Like any other visible row, it ends the work-group run around it.
  - Contents: a `hand.raised` icon in the danger color with "Blocked" (caption semibold), then the rule name from `Reason: [<rule>]`, muted. With no rule name: "Auto mode blocked this action". Below that, the blocked call: the tool name and its input summary in mono 12, up to 3 lines, from the paired tool call. Raised-card styling with a hairline, like a tool card.
  - Tapping it opens the event detail sheet, like a tool result, which shows the full denial text.
  - A tool-result event that carries other, ordinary results too shows the Blocked card(s) and then those results as a normal tool-result block.
  - Live and in history alike, since the text is the same in the transcript file. PDF and Full Page exports show it the same way.
- **Blocked push (server):** the first block in a turn sends a push to the registered devices. Title: the session name. Body: `Blocked: <tool> · <rule>` (`Blocked: <tool>` with no rule name). The payload carries `sessionId`, like the other pushes. At most one per turn, so a Claude that retries several ways doesn't send a burst. Existing rules apply: the iPhone banner is suppressed while that session is on screen, and the push mirrors to the watch.

## Session titles and the Info screen

Added 2026-11-04 (Richard: a non-coding first message titled a session "This request isn't coding-related, so I can't create an appr…", and there was nowhere to see or change the full title).

- **Inferred title (server, `SessionTitler`):** on a session's first prompt, if the name is still the default (the cwd basename), a one-shot `claude -p --model haiku` call names it. Slash commands don't trigger it; attachment-only messages do.
  - **Emoji prefix** (Richard, 2026-11-09: "prefix each one with an appropriate emoji for easier tracking"): every inferred title starts with one emoji that fits the topic, then a space, e.g. `🐛 Fix Login Token Refresh`, so sessions are easy to tell apart at a glance in the drawer, header, pushes and watch.
  - **Prompt:** asks for one emoji that fits the topic, a space, then a 3–6 word Title Case title for a *conversation* that starts with the message. It says the conversation can be about anything (code, writing, questions, personal topics), and that the model must always reply with a title naming the topic: never a refusal, explanation, or question. No quotes, no other emoji, no trailing punctuation. It does **not** describe the session as a coding session (that framing caused the refusals).
  - **Sanitize:** takes the first non-empty line, strips a `Title:` prefix, and pulls out every emoji; the first one found becomes the prefix. The rest of the text has quotes, markdown emphasis and trailing punctuation stripped and whitespace collapsed. That text is **rejected** (the default name stays) if it's empty, longer than 8 words, or looks like a reply instead of a title. A reply is one that contains (case-insensitive) `i can't`, `i cannot`, `i can not`, `i'm unable`, `i am unable`, `i'm sorry`, `i apologize`, `i'd be happy`, `i need`, `i don't`, `this request`, `as an ai`, `please provide`, or `could you`. Apostrophes are matched as `'` or `’`. Accepted text over 60 characters is cut to 60 plus `…`. The final title is `<emoji> <text>`. If the model gave no emoji, `💬` is used.
  - Names set by hand (Rename, below) are stored as typed: no emoji is added or required.
- **Rename (server):** `PATCH /sessions/:id` with `{"name": "<title>"}`. The ownership check is the same as `/usage`.
  - The name is trimmed, and newlines and runs of whitespace collapse to one space. An empty result is a 400. It's capped at 500 characters, so a name you set isn't cut to the 60-character inferred-title limit.
  - The name is persisted and pushed to clients in the next sessions update. It answers `{"status":"ok","name":"<stored name>"}`, or 404 for an unknown session (including k8s sessions, which can't be renamed).
  - An inference still in flight never overwrites a name set this way: an inferred title is only applied while the name is still the default.
- **Info screen (app):** the sheet that used to be **Usage** is now **Info**: navigation title "Info", and the ••• menu item reads "Info" with an `info.circle` icon. It opens from the ••• menu, from the header's context gauge, and from tapping the session name in the header.
  - Its first card is **Title**. It always shows, even while usage is loading or unavailable.
    - The card shows the session's full display name in serif 20, text primary, wrapping with no line limit and selectable. Under it is the cwd in muted mono 11.
    - A pencil button (`pencil`, text-secondary) sits in the card's title row.
  - **Editing:** tapping the pencil swaps the name for a multi-line `TextField` (grows to about 4 lines) pre-filled with the current name and focused. Under it are Cancel (text-secondary) and Save (clay capsule).
    - Save is disabled while the trimmed text is empty or unchanged, and shows a spinner while the request runs.
    - A successful save updates the app's session list right away, so the header and drawer change without waiting for the next sessions update, and leaves edit mode.
    - A failure keeps edit mode and shows "Couldn't rename this session" in danger caption.
    - Return in the field submits, the same as Save.
  - A **Messages** card (the session's cross-session peers) follows the Title card when the session has any. See `docs/plan-peer-messages.md` → iOS app → Info screen.
  - The usage cards (Context, This conversation, By model) follow, unchanged. If usage isn't available, the "Usage isn't available for this session yet." note shows under the Title card.
- **Drawer and header** still show the name on one line, truncated. The full name lives on the Info screen.

## Usage page

Added 2026-11-04 (Richard: "a usage summary page that is basically an aggregate of all usage. How much have I used in the past day, week, month"). Same measures as the Info screen (tokens and list-price cost), in **calendar** windows. Richard pays per token; there's no subscription, so plan limits aren't shown.

- **Entry:** a "Usage" row (`chart.bar`) in the drawer, directly above Settings, below the divider. Tapping it closes the drawer and opens the Usage sheet (large detent, "Usage" title, Done button).
- **Windows:** a segmented control: **Today · This week · This month**. Default: Today. All three use the phone's calendar and time zone (`Calendar.current`): today is midnight to now; this week runs from the locale's first weekday through today; this month from the 1st through today.
- **Cards** (styled like the Info screen's):
  - **Totals:** total tokens (serif 30) and cost (serif 22, labelled "list price"), then the 2×2 grid of Input / Output / Cache write / Cache read.
  - **By day** (This week and This month only): a bar chart (Swift Charts) of cost per day for every day in the window, through today, with clay bars, muted axis labels and the day of the month on the x-axis. Tapping a bar shows that day's cost and tokens in a caption above the chart.
  - **By model:** one row per model (short name, tokens, cost), most expensive first.
  - **By project:** one row per project (the cwd's last path component), tokens and cost, most expensive first, top 8, then "N more" collapsed.
  - A muted footnote: "All Claude Code use on this laptop, including terminal sessions. Cost is estimated from tokens at each provider's list price (Bedrock or Anthropic)."
- **States:** a spinner while loading; "No usage in this period" with the cards hidden when the window is empty; an error message and Retry when the request fails. Pull to refresh. The sheet reloads each time it opens.
- **Server:** `GET /usage/summary?tz=<IANA zone>&from=<yyyy-MM-dd>`, owner only (like `/transcripts`; anyone else gets an empty `days`). The app sends `from` as the earlier of the start of the week and the start of the month.
  - Response: `{"days":[{"date":"2026-11-04","byModel":{"claude-opus-5-5":{"inputTokens":…,"outputTokens":…,"cacheReadTokens":…,"cacheWriteTokens":…,"costUSD":…}},"byProject":{"mclaude-v1":{…same fields…}}}],"unpricedModels":[…]}`. Days are in `tz`, run oldest first, and omit days with no usage. Models are canonical: the `us.`/`eu.`/`anthropic.` prefixes, date suffixes and `-v1:0` are stripped.
  - Source: every `assistant` line with `message.usage` in `~/.claude/projects/*/*.jsonl` **and** subagent transcripts (`*/<session>/subagents/*.jsonl`). It is deduped by `message.id` across all files, because Claude Code repeats a message's usage on each content-block line. Lines with model `<synthetic>` are skipped. The day comes from the line's `timestamp`. The project is the session's: the last path component of the transcript's first `cwd`, or for a subagent file, its parent transcript's first `cwd`. That way a session that `cd`s into a subfolder still counts toward the project it started in. A line's own `cwd` is used only when the session has none.
  - Tokens: `input_tokens`, `output_tokens`, `cache_read_input_tokens`, and `cache_creation_input_tokens` as cache write.
  - Cost per message (USD per million tokens):

    | Model | Input | Output | Cache read |
    |---|---|---|---|
    | claude-fable-5-1, claude-mythos-5-1 | 10 | 50 | 0.25 |
    | claude-fable-5, claude-mythos-5 | 10 | 50 | 1.00 |
    | claude-opus-5-5 | 4 | 20 | 0.20 |
    | claude-opus-5, claude-opus-4-8/4-7/4-6/4-5 | 5 | 25 | 0.50 |
    | claude-opus-4-1, claude-opus-4 | 15 | 75 | 1.50 |
    | claude-sonnet-5-5, claude-sonnet-5 | 2 | 10 | 0.20 |
    | claude-sonnet-4-6/4-5, claude-sonnet-4 | 3 | 15 | 0.30 |
    | claude-haiku-5-5 | 0.10 (0.50 when the message's prompt, meaning input plus cache read plus cache write, is over 100K tokens) | 0.50 (2.50 over 100K) | 0.01 (0.05 over 100K) |
    | claude-haiku-4-5 | 1 | 5 | 0.10 |

    That table is Anthropic's first-party list price. **Bedrock** usage is priced at Bedrock's own rates (Richard, 2026-11-05). The provider comes from the request's model id (`requestedModel`, else `message.model`): `global.anthropic.…` is Bedrock global; `us.`/`eu.`/`apac.`/`jp.anthropic.…` (cross-region profiles) and bare `anthropic.…` are Bedrock regional; anything else is Anthropic.
    - Bedrock global rates are the same as the table above, except that **claude-sonnet-5-5 cache reads cost $0.10** (Bedrock cut them 50% on 2026-10-07).
    - Bedrock regional rates (including `us.` profiles, which AWS bills as "Regional CRIS") are **1.1×** the global rates for every token type. The exceptions are claude-opus-4, claude-opus-4-1 and claude-sonnet-4, which have no regional premium.
    - Source: AWS's public price list `AmazonBedrockFoundationModels/current/us-east-1/index.json`, on-demand "Standard" SKUs, checked 2026-11-05. The response's `byProvider` map gives totals per provider: `anthropic`, `bedrock-global`, `bedrock-geo`, `bedrock-regional`.
    - The app's footnote says "Cost is estimated from tokens at each provider's list price (Bedrock or Anthropic)."
    Cache writes cost 1.25× input for the 5-minute TTL and 2× for the 1-hour TTL, using the line's `cache_creation.ephemeral_5m_input_tokens` / `ephemeral_1h_input_tokens` split, all 5-minute when the split is missing. A model not in the table counts tokens with cost 0 and is listed in `unpricedModels`. The app then adds "Some models have no price: …" to the footnote.
  - Cached per file by (path, size, mtime) as parsed message records, so a request only re-reads files that changed. The existing `/usage/timeline` is left as it is; the app doesn't use it.

## Icon

Make an original mark in the same family as the Claude spark without copying it, and don't use Anthropic's logo.
- Suggested: a clay (`#D97757`) rounded-square glyph, or a terminal-prompt-like shape (`>_` softened into one stroke), on an ivory `#FAF9F5` background.
- Also provide a dark variant: clay on `#262624`.
- 1024px with no alpha (TestFlight rejects alpha). Update the in-app logo that's next to the chat icon in the session header to match.

## Implementation notes

- Add `Theme.swift` holding semantic colour tokens as asset-catalog colours or `Color(light:dark:)`, plus `Font.claudeProse = .system(size: 17, design: .serif)`. Replace hard-coded colours across the views and grep for `.black`, `navy`, `Color(red:` and `.blue`.
- Remove the TabView. Use NavigationStack plus a custom drawer overlay.
- Don't change behaviour: inputs, key mapping, photo attach, voice and question handling all stay as they are.
- **Typing redraws only the composer.** The draft is bound inside a small host view around the composer, not in the session screen's body. When it was bound from the session screen, every keystroke redrew that screen and rebuilt the whole transcript: in a 1,121-event session, 4 full rebuilds for 5 keystrokes. Now 5 keystrokes cause 5 composer redraws and nothing else. (Richard, 2026-11-07: "slow, especially when I'm just typing a message".) Keep anything that changes on every keystroke out of the session screen and transcript.
