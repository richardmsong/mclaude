# Peer messages (cross-session messages)

## Overview

Claude Code sessions on the same laptop can message each other with the `SendMessage` tool. Richard's sessions use this to coordinate shared-tree work (e.g. "About to archive TestFlight build 109 … reply ok or wait"). Today MClaude shows a received message as a raw user bubble full of `<cross-session-message …>` markup, and a sent message as a generic `SendMessage` tool row inside a work group.

This feature makes peer messages first class in the iOS app, backed by mclaude-server:
- Both directions render as compact **peer cards** in the transcript.
- Tapping a card opens a read-only **thread**: every message between the two sessions, in order, with a link to jump to the other session.
- The session **Info** screen lists the peers a session has talked to.

The scope is the iOS app (`MClaude/`) and mclaude-server only. The web SPA runs on the v2 NATS stack and isn't covered.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Identity key | The **Claude session UUID** of each side. Never the Claude Code name (`mclaude-v1-f1`) or pid. | Names are made up by Claude Code and get reused or changed; pids die with the process. An MClaude session keeps one Claude UUID for life (resume reuses it), and the UUID is also the transcript file name. |
| When identity is resolved | **At observation time**, from Claude Code's live session registry, then persisted. | Registry entries exist only while a process runs. Afterwards a name or socket path can't be mapped back to a session. |
| History horizon | **From now on** (Richard). Messages from before the server has this feature are never resolved. | No backfill scan and no text-matching heuristics. Old messages still render as cards, just without a thread link. |
| What is observed | Only sessions **managed by this mclaude-server**, from their live stream. | Every message in a thread A⇄B appears in A's own stream (its sends plus the messages it received), so the managed side alone is enough. Messages between two terminal-only sessions are out of scope. |
| Who parses the format | **The server** parses the undocumented tag and tool formats and attaches structured fields to events. The app never parses `<cross-session-message>`. | One place to fix when Claude Code changes the format. Older apps keep working because `text` is unchanged. |
| Entry points | Cards in the transcript plus a **per-session list** on the Info screen (Richard). No global inbox. | |
| Thread interaction | **Read-only** (Richard). It has an "Open <peer>" link; you type in the session itself. | Messages are written by Claude sessions, not by you, so a composer in the thread would be ambiguous. |
| Card style | **Collapsed cards in both directions** (Richard). Sent cards are pulled out of the work group. | Coordination messages matter and shouldn't hide in a folded tool group. Long messages (often a full screen) are collapsed. |
| In-process subagent messages | **Not** peer messages. `SendMessage` to a subagent or teammate (`recipient_kind: "agent"`) keeps the existing tool row. | They aren't cross-session. |

## Background: Claude Code's formats (observed, undocumented)

All of this comes from Claude Code 2.1.x on Richard's laptop. Any of it may change. Every rule below must fail safe: an unrecognized shape falls back to today's rendering.

### Session registry

- Each running Claude Code process writes `~/.claude/sessions/<pid>.json`. The file exists while the process runs and is removed when it exits. Files in that directory that aren't `<number>.json` (e.g. `<pid>.<hash>.key`) are ignored.
- Fields used here: `pid` (int), `sessionId` (Claude session UUID), `name` (the address other sessions use, e.g. `mclaude-v1-79`; may be missing), `messagingSocketPath` (e.g. `/tmp/cc-socks/<pid>.sock`; may be missing), `updatedAt` (epoch ms).
- MClaude-managed headless sessions register too (`entrypoint: "sdk-cli"`).

### Received message

The body is wrapped in a tag whose attributes can come in any order. Unknown attributes (e.g. `hop-chain`) are ignored:

```
<cross-session-message from="uds:/tmp/cc-socks/26108.sock" from-name="mclaude-v1-f1" from-mode="bypass">
…body…
</cross-session-message>
```

It reaches the receiving session in one of two forms:
1. **Idle delivery:** a `user` turn whose text is `Another Claude session sent a message:\n` followed by the tag, then a fixed trailer paragraph that starts `This came from another Claude session`. The leading line and the trailer are part of the wrapper, not the message.
2. **Mid-turn delivery:** Claude Code injects it into the running turn at the session's next step. The live stream carries it as a replayed `user` event whose text is the bare tag (`isSynthetic: true`, `origin.kind: "peer"`), and whose `timestamp` is the enqueue time. The transcript records a `queue-operation` (`operation: "enqueue"`, `content` = the bare tag) and an `attachment` of type `queued_command` (`prompt` = the bare tag).

Headless (`sdk-cli`) sessions also write a `queue-operation` enqueue for idle deliveries, so in history one idle delivery can appear both as an enqueue and as the wrapped user turn. It's shown once.

A single user turn can contain more than one tag. Text outside the tags (other than the wrapper line) is ordinary user text.

### Sent message

- An assistant `tool_use` with `name: "SendMessage"`. Input fields:
  - `to` (always present)
  - `message` (the body)
  - `summary` (optional)
  - `recipient_kind` (`"session"`, `"name"` or `"agent"`; missing in older versions)
  - `recipient`, `type`, `content`, `notify_when_idle`, which are ignored
- The matching `tool_result` content is a JSON string `{"success": true|false, "message": "<detail>"}`. Failure example: `No agent named 'mclaude-v1-55' is reachable. Did you mean: mclaude-v1-28?`

**Which sends count as peer messages:**
- `recipient_kind` is `"session"` or `"name"`.
- Or `recipient_kind` is missing **and** `to` either starts with `uds:` or doesn't look like an agent id. An agent id is `a` followed by exactly 16 lowercase hex characters.

## User Flow

1. Session A (say "Drawer/resume") sends B ("Gmail config") a message. In A's transcript a **To** card appears where the tool call was, showing "Sending…". When the tool result arrives it changes to ✓ (delivered) or "Not delivered" with the error.
2. In B's transcript a **From** card appears where the message arrived, whether B was idle or mid-turn.
3. On either card you see the peer's name, the summary when known, and the first 4 lines of the body.
4. Tap the card → the **thread sheet** opens: every message between A and B, oldest first, scrolled to the message you tapped.
5. **Open "Gmail config"** in the thread header closes the sheet and switches to B.
6. Later you open A's **Info** screen. The **Messages** card lists every session A has exchanged messages with. Tap a row → the same thread.

## Server (mclaude-server)

### Peer resolution

**Reading the registry:** the registry is read when a peer message is observed, not cached across observations. All `~/.claude/sessions/<n>.json` files are read. A file that's missing a field, can't be read, or isn't valid JSON is skipped. The observing session's own entry (same `sessionId`) never counts as a match.

**Resolving an address** (`from` of a received message, or a `to` starting with `uds:`):
- Strip the `uds:` prefix to get the socket path.
- Pick the entry whose `messagingSocketPath` equals that path.
- If no entry has that path, parse `<pid>` from a path ending in `/<pid>.sock` and use the entry with that `pid` if it has no `messagingSocketPath`.
- The result gives the peer's UUID and Claude Code name.

**Resolving a name** (a `to` that isn't an address, or `from-name` when address resolution failed):
- Match entries whose `name` equals the given name.
- If the name ends in ` [<hex>]`, the bracketed part is stripped before matching. It's used only to pick among several same-named entries: prefer the one whose `sessionId` starts with that hex. If none does, fall through to the next rule.
- If several entries still match, take the one with the largest `updatedAt`.

**Unresolved:** if neither rule finds an entry, the peer is **unresolved**. Its Claude Code name and address are still recorded.

**When resolution happens:**
- For received messages, when the message is observed.
- For sent messages, when the `tool_use` is observed. The result is held until its `tool_result` arrives and the record is written.

### Observation (live)

**Managed sessions:** observation covers every session this server runs. For each, the server watches:
- **User turns** on the session's stream: idle-delivery peer messages.
- **Mid-turn deliveries** on the same stream, as replayed user events (see Background). The card appears when the session takes the message at its next step, so it can lag by the length of a running tool call. The record's `at` is the event's `timestamp` (the enqueue time), not the time it was taken.
- **`SendMessage` tool_use and tool_result** pairs. A record is written when the result arrives. A tool_use with no result (the session was interrupted or crashed) is never recorded.

**Records:** each peer message observed creates one **peer record** (Data Model), appended to the store.

**Dedup key:**
- received: `recv:<observing UUID>:<from address or from-name>:<sha256 of the whitespace-trimmed body>`
- sent: `sent:<tool_use id>`

A record whose key already exists isn't written again. So a server restart that replays a session's history never duplicates or rewrites records.

**Cross-side dedup** (both sessions managed): A's sent record and B's received record for the same message are both stored. They're merged when a thread is assembled, not at write time.

### Event enrichment (live stream and history pages)

The event model gains three optional fields. Clients that don't know them ignore them, and `text`, `toolUse` and `toolResults` are unchanged.

**`peerMessages: [PeerMessage]`:**
- On a `user` event that holds one or more `<cross-session-message>` tags, from either delivery form: one entry per tag, in order.
- On a `tool_use` event for a peer `SendMessage` (as defined in Background): one entry with `direction: "sent"`.

**`peerOtherText: String`:**
- Only on a `user` event that has `peerMessages`.
- It's the event's text with every tag removed, the `Another Claude session sent a message:` line and the `This came from another Claude session…` trailer removed, and whitespace trimmed.
- It's omitted when empty. When present, the app shows it as a normal user bubble after the cards.

**`peerDelivery: {toolUseId, status: "delivered"|"failed", error?}`:**
- On the `tool_result` event of a peer `SendMessage`. `error` is the result's `message`, only when `success` is false.
- If the result content isn't the expected JSON, `status` is `delivered` when the result isn't marked as an error, otherwise `failed` with the raw content as `error`.

**How the peer is filled in:**
- **History:** events in history pages (`/sessions/:id/events`, older pages, and rebuilt history after resume) look up the peer record by the same dedup key. A send in history has no record yet when its result is still pending, so its peer is resolved against the live registry the same way as a live tool_use.
- **No record found:** the message predates this feature or was never observed. `peer` is unresolved: only `ccName`/`address` from the tag or `to`, and `displayName` from the rules below.

**When enrichment fails:** if a tag can't be parsed (no closing tag, no `from` and no `from-name`), that event gets no `peerMessages` and renders as today.

### API

All under mclaude-server's REST API. JSON keys are camelCase and dates are ISO 8601, as elsewhere.

**Authorization:** the caller must own session `:id` or be the laptop owner, else 404. A `PeerRef` includes `sessionId`, and uses the managed session's name, only when the caller can also see that peer session by the same rule. This applies to the endpoints and to event enrichment (per caller on the WebSocket and in history pages).

**`GET /sessions/:id/peers`** returns the session's peers, most recent first:

```json
[
  {
    "peer": PeerRef,
    "messageCount": 6,
    "lastAt": "2026-11-05T19:18:02.114Z",
    "lastDirection": "received",
    "lastPreview": "About to archive TestFlight build 109 from the shared tree…"
  }
]
```

- Built from every record where session `:id`'s UUID is either the observing side or the resolved peer, grouped by the other side's UUID. Records with an unresolved peer are left out.
- `messageCount` counts messages after cross-side merging.
- `lastPreview` is the first non-empty line of the latest message's body, cut to 120 characters plus `…`.
- A session with no records returns `[]`.

**`GET /sessions/:id/peers/:peerUuid/thread`** returns one thread:

```json
{
  "this": PeerRef,
  "peer": PeerRef,
  "messages": [PeerMessage]
}
```

- `messages` is every record between the two UUIDs, from both sides' records, merged, oldest first.
- **Merging:** a `sent` record (observed in X, peer Y, status `delivered`) and a `received` record (observed in Y, peer X) with the same body and timestamps within 120 s are one message. The result is `direction` relative to X, X's `summary`, and the earlier `at`. Its `id` is the key of the record observed in `this` (the session in the URL), so it matches the `id` on that session's card.
- `direction` in the response is relative to `this`: `sent` means `this` sent it.
- Returns 404 when `:peerUuid` has no records with this session.

**Sessions this server doesn't run:** for k8s sessions, or any id this server doesn't know, both endpoints return 404, the same as other session endpoints.

### Shapes

**PeerRef:**

```json
{
  "claudeSessionId": "a1d9…",
  "ccName": "mclaude-v1-f1",
  "address": "uds:/tmp/cc-socks/26108.sock",
  "sessionId": "s12",
  "displayName": "Drawer/resume",
  "open": true
}
```

- `claudeSessionId` is omitted when unresolved.
- `ccName` and `address` are each omitted when unknown.
- `sessionId` is present only for an **open** managed session the caller can see.
- `open` is true when `sessionId` is present.
- `displayName`, first match wins:
  1. the managed session's current name (open session the caller can see)
  2. its remembered ended name (`endedNames`; caller is the laptop owner or owned it)
  3. `ccName`
  4. `"Another session"`

**PeerMessage:**

```json
{
  "id": "sent:toolu_…",
  "direction": "sent",
  "peer": PeerRef,
  "summary": "Ask before restarting signed server",
  "text": "…full body…",
  "at": "…",
  "status": "delivered",
  "error": "…"
}
```

- `id` is the dedup key.
- `direction` is `sent` or `received`.
- `summary` is omitted when unknown. On a received message it's present only when the sender's record exists: after merging in a thread, and on an event when the sender's record was already stored.
- `status` is one of `sending`, `delivered`, `failed`, `received`. `sending` appears only in event enrichment, before the tool_result.
- `error` is present only when `status` is `failed`.

## Data Model

**Peer record store:** `~/.mclaude/peer-messages.jsonl`, or `$MCLAUDE_STATE_DIR/peer-messages.jsonl`. It's append-only with one JSON object per line, loaded into memory at server start. There's no pruning in v1; at a few KB per message, a year of coordination is a few MB.

```json
{
  "key": "recv:<uuid>:<addr>:<sha256>",
  "observedIn": "<observing Claude UUID>",
  "direction": "sent|received",
  "peerUuid": "<uuid>",
  "peerCcName": "mclaude-v1-f1",
  "peerAddress": "uds:/tmp/cc-socks/26108.sock",
  "summary": "…",
  "text": "…",
  "at": "<ISO 8601>",
  "status": "delivered|failed|received",
  "error": "…"
}
```

- `peerUuid`, `peerCcName`, `peerAddress`, `summary` and `error` are each omitted when absent.
- `at` is the event's own `timestamp` when it has one (the enqueue time for mid-turn deliveries), else the time the server observed it. For sends it's the tool_use's time.

**Loading:**
- A line that isn't valid JSON (e.g. a torn last line after a crash) is skipped and logged. The file is never rewritten to remove it.
- If the file doesn't exist, the store starts empty.

## iOS app

### Peer card (in the transcript)

Where it appears:
- One card per `peerMessages` entry. It goes where the user turn is (received) or where the tool call is (sent).
- **A sent card is standalone.** It isn't folded into a work group, and it splits the surrounding run of tool calls into two groups.
- `peerOtherText`, if present, follows as a normal user bubble.
- The `tool_result` of a peer `SendMessage` isn't shown as its own row. Its status is shown on the To card.

```
┌──────────────────────────────────────┐
│ ↙ From  Drawer/resume  mclaude-v1-f1 │
│ About to archive TestFlight build    │
│ 109 from the shared tree. Are you    │
│ editing anything in MClaude/ or      │
│ mclaude-server/ that isn't ready …   │
│                      View thread  ›  │
└──────────────────────────────────────┘
┌──────────────────────────────────────┐
│ ↗ To  Drawer/resume              ✓   │
│ Ack: holding restart until told      │
│ ok — nothing of mine is in flight…   │
│                      View thread  ›  │
└──────────────────────────────────────┘
```

**Container:** full-width raised card, 12pt radius, hairline border, like tool cards. It's not a right-aligned user bubble.

**Header row:**
- An icon: `arrow.down.left` for received, `arrow.up.right` for sent, coloured text-secondary.
- "From" or "To" in sans 13 muted.
- `peer.displayName` in sans semibold 13, text primary, one line.
- `peer.ccName` in muted mono 11, shown only when it's different from `displayName`.

**Status** (sent cards only, trailing in the header). It comes from the paired tool_result's `peerDelivery`; with no result yet it's `sending`.
- `sending`: a small spinner
- `delivered`: `checkmark` in muted
- `failed`: "Not delivered" in danger caption 12, and the `error` in danger caption under the body, up to 2 lines

**Body:**
- `summary`, when present, in sans semibold 14, one line.
- Then the message `text` in sans 14, text-secondary, limited to **4 lines** with a trailing ellipsis.

**Footer:**
- **"View thread ›"** in link colour, sans 13, shown when `peer.claudeSessionId` is present (the peer was resolved).
- When it's absent (pre-feature or unresolved) and the body was truncated, the footer is **"Show more"** instead, which expands the full text inline. The label then becomes "Show less".

**Taps:**
- Anywhere on a resolved card opens the thread sheet, scrolled to this message.
- On an unresolved card, a tap does the same as Show more or Show less.
- Long-press: a context menu with **Copy message**, which copies the full body.

**Live behaviour:** cards appear as events arrive, with no reload. A sent card's status changes in place when its result arrives.

### Thread sheet

**How it opens:** from a peer card, as a sheet over the session (large detent, title **"Messages"**, Done button). From the Info screen it's pushed inside the Info sheet's navigation instead, with the same content.

```
┌──────────────────────────────────────┐
│ Done           Messages              │
│ Gmail config  ⇄  Drawer/resume       │
│              [ Open Drawer/resume ]  │
│                                      │
│ ┌──────────────────────────┐         │
│ │ About to archive build   │         │
│ │ 109 …(full text)…        │         │
│ └──────────────────────────┘         │
│  Today 7:18 PM                       │
│         ┌──────────────────────────┐ │
│         │ Ack: holding restart     │ │
│         │ ok — nothing of mine …   │ │
│         └──────────────────────────┘ │
│                      Today 7:19 PM ✓ │
└──────────────────────────────────────┘
```

**Header:**
- `this.displayName ⇄ peer.displayName` in sans semibold 15, wrapping.
- Below it, when `peer.open`, a secondary capsule **"Open <peer.displayName>"**. Tapping it dismisses every sheet and switches to that session (same path as a push-notification tap).
- When the peer isn't open in MClaude, a muted caption replaces the button: "Not open in MClaude" plus `ccName` when known.

**Messages:**
- `this`'s messages (`direction: "sent"`) are right-aligned bubbles, sunken bg, about 18pt radius, like user bubbles.
- The peer's messages are left-aligned raised cards, 12pt radius, with a hairline border.
- Max width is 85% of the sheet.

**Message content:**
- `summary`, when present, in sans semibold 14.
- Then the full `text` in sans 15 through the shared selectable-text view. Bodies are plain text: no markdown rendering, but links are tappable.
- Under each, a muted caption 11 with the time: "Today 7:18 PM", "Yesterday 9:02 AM", else "Nov 3, 9:02 AM".
- A `delivered` sent message adds `✓`. A `failed` one adds "Not delivered: <error>" in danger and keeps its bubble at 60% opacity.

**Scrolling:**
- Opened from a card, it scrolls to that message (matched by `id`) and briefly highlights it with a clay border that fades over 1 s.
- Opened from the Info list, or when the message isn't found, it scrolls to the bottom.

**States:**
- A spinner while loading.
- An error message and **Retry** when the request fails.
- "No messages yet" if the thread is empty or the endpoint returns 404.

**Refresh:**
- It loads on open, and supports pull to refresh.
- While it's open over a session, a new peer event on that session's stream for the same peer UUID reloads the thread.

**Read-only:** there's no composer.

### Info screen: Messages card

**Placement:** directly after the Title card and before the usage cards. It's loaded from `GET /sessions/:id/peers` each time the Info screen opens.

**Content:**
- Title "Messages".
- One row per peer: `displayName` (sans 15, one line), then a muted caption line `<n> messages · <relative lastAt>`, then `lastPreview` in muted sans 13, one line, prefixed `You: ` when `lastDirection` is `sent`. A trailing chevron.
- Tapping a row pushes the thread.

**When it's hidden:**
- The response is `[]`, a 404 (an older server or a k8s session) or any error. The Info screen shows no error for it.
- It's also hidden while loading, so the usage cards don't jump.

## Error Handling

| Failure | Behaviour |
|---------|-----------|
| Registry unreadable or missing, or no matching entry | The peer is unresolved. The card shows the Claude Code name or `"Another session"`, with Show more instead of View thread. The message is left out of the Peers list and threads. |
| Peer process exited before the server observed the message (e.g. a message sent just before exit) | Same as unresolved. |
| Tag format changed so it can't be parsed | No `peerMessages`, so the raw user bubble shows as today. Nothing is lost. |
| `SendMessage` input changed (no `to`/`message`) | Not a peer message, so the generic tool row shows. |
| Tool result format changed | `peerDelivery` falls back to the result's error flag (see Event enrichment). |
| Send to a name nobody has (failed) | A sent card with "Not delivered" and the error. The record is stored with an unresolved peer, so there's no thread. |
| Peer-record store write fails (disk) | Logged. Event enrichment for the live event still happens, but the message won't appear in threads or the Peers list. |
| Store has a corrupt line | Skipped at load and logged. |
| Older server (no endpoints, no enrichment) | Events have no `peerMessages`, so the raw rendering shows as today. The Info card is hidden on 404. |
| Peer session was ended, or isn't visible to the caller | `PeerRef` has no `sessionId`, so the "Open" button is replaced by "Not open in MClaude". |

## Security

**What it exposes:**
- The endpoints show only the conversation between session `:id` and its peers.
- Every message in that conversation already appears in `:id`'s own transcript, except for merged-in `summary` lines and failed sends from the peer side.
- The usual session visibility check applies.
- Links to other sessions (`sessionId`) appear only when the caller can see them, so another user's session ids aren't revealed.

**Local access:** the registry is a local file read with the server's own user permissions. Nothing is written to it, and the server never connects to the peer sockets.

## Scope

**In v1:**
- Server observation of managed sessions, peer resolution, the record store, event enrichment, and the two endpoints.
- iOS peer cards, the thread sheet, and the Info Messages card.

**Deferred:**
- A global messages inbox.
- Replying or nudging from the thread.
- Backfill of messages from before the feature shipped.
- Messages between two sessions MClaude doesn't run.
- Push notifications for peer messages.
- Read receipts: Claude Code's delivery notices aren't in transcripts.
- Web SPA support.

## Implementation Plan

| Component | New/changed lines (est.) | Tokens (est.) | Notes |
|-----------|--------------------------|---------------|-------|
| mclaude-server: registry reader + resolution | ~150 | ~60k | Unit-tested against fixture registry files |
| mclaude-server: tag/tool parsing + event enrichment (live + history) | ~200 | ~80k | Both delivery forms; tests with real transcript samples |
| mclaude-server: record store + observation (incl. mid-turn watch) + endpoints | ~300 | ~100k | Mid-turn path must be verified against a live session |
| iOS: event model fields + API client | ~80 | ~30k | Shared package |
| iOS: peer card + work-group split | ~200 | ~70k | Transcript timeline |
| iOS: thread sheet + Info Messages card | ~250 | ~80k | Simulator validation against :8399 test server |

**Total estimated tokens:** ~420k
**Estimated budget:** ~420k of the ~15M tokens in the 5h budget (about 3%). Wall-clock is dominated by simulator validation and verifying mid-turn delivery against a live session.
