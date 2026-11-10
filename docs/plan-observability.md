# Observability MVP: evidence for two live bugs

## Overview

The MVP tests one bet: **if the evidence is captured when a bug happens, Claude can find the root cause without reproducing it.**

The first data collection targets two live bugs:

- **Composer keeps the sent text.** After tapping ↑, the composer sometimes still shows the sent message while the send arrow is grey. The app's model holds "" but the UIKit text view still shows the old text, so the next keystroke would bring it back into the draft.
- **Transcript shifted left.** The transcript sometimes sits against the left edge of the screen, having lost its 16pt left margin.

**Success** means each bug gets a root cause and a fix from the captured data, with no new lag from the instrumentation.

The full target design (server and Claude Code tracing, Ops session, live map, correlation model, sampling and so on) is in `docs/plan-observability-later.md`. None of it is built yet.

## Decisions

Decided with Richard on 2026-11-08.

- **MVP first, no overbuilding.** Only what these two bugs need gets built. Later phases follow from what turns out to be missing.
- **First collection = these two bugs.** Both happen entirely on the phone, so nothing on the server is instrumented yet.
- **The phone sends to the same Mac host name it already uses for mclaude-server** (Tailscale MagicDNS), on the collector's port. `*.mclaude.internal` doesn't resolve today: there's no CoreDNS container, and port 53 is held by the k3d cluster.
- **The old collector was removed.** The May 2026 experiment container `otel-collector` was stopped and removed, so the new collector uses the standard OTLP ports.
- **Rules only observe.** A broken rule records evidence. It never changes the screen, so the bug shows exactly as it happens.
- **Standard OTLP on the wire.**
- **Full content is captured**, including the composer's text.

## Pipeline

- **Docker Compose project `mclaude-obs`**, in the repo's `mclaude-obs/` folder. `./up.sh` starts or updates it. `./query.sh "<sql>"` runs a read-only query.
- **OTel Collector** (contrib 0.152.1):
  - Receives OTLP over gRPC (4317) and HTTP (4318) on all interfaces. OrbStack can't bind a single host IP because containers run in its VM, so this matches how mclaude-server listens on :8377. The phone reaches it over Tailscale.
  - Memory limiter and batching.
  - Exports to ClickHouse with a file-backed persistent queue and unlimited retry, so a ClickHouse restart loses nothing.
- **ClickHouse** 25.8:
  - Localhost only (8123, 9000).
  - Database `otel`, with the standard OTel tables created by the exporter.
  - Raw data is kept 30 days.
- **Transport:** phone traffic is plain HTTP inside the tailnet; Tailscale (WireGuard) already encrypts it. The app's App Transport Security settings get one exception: insecure HTTP is allowed to `ts.net` and its subdomains (Tailscale names) only. The existing "allow arbitrary loads" setting doesn't cover this, because iOS ignores it while "allow local networking" is also set.

## App telemetry

- **Exporter:** a small built-in OTLP/HTTP JSON exporter, with no OpenTelemetry SDK dependency. It sends logs and spans to `http://<server host>:4318`, where the server host is the one the app is configured with.
- **Resource attributes** on every record:
  - `service.name` = `mclaude-ios`
  - `service.version` and `mclaude.build`
  - `mclaude.install_id`: a stable UUID per install
  - `mclaude.launch_id`: one per app launch
  - device model, OS version, and whether it's the simulator
- **Outbox:**
  - Records are written to an on-disk outbox right away, so they survive the app being killed.
  - The outbox is sent every 5 s in the foreground, and flushed when the app goes to the background.
  - Past 20 MB, the oldest records are dropped, and one "dropped N records" record is sent in their place.
- **Failures:** export failures never affect the app. They're retried on the next flush.
- **Launch record:** each launch sends one `mclaude.app.launch` record with the server host, so every launch proves the pipeline end to end. It also carries the process ID (`pid`).

## Crashes

Added 2026-11-08: build 124 crashed in the foreground (an Objective-C exception from the audio engine when hold-to-talk started) and ClickHouse had no record of it, only vitals that stopped. Three records cover a crash, from fastest to fullest. All are severity ERROR, except `unclean_exit`, which is WARN.

- **`mclaude.app.exception`**, written as the app dies, for an uncaught Objective-C exception:
  - `exception.type` (the exception's name), `exception.message` (its reason) and `exception.stacktrace` (the symbol names the runtime gives, one frame per line), plus the open session ID.
  - It's written straight to the outbox file on the crashing thread before the app aborts, and it's sent by the next launch. Swift runtime traps and signals don't go through this path; the MetricKit record covers them.
- **`mclaude.app.unclean_exit`**, at the next launch, when the previous launch ended while in the foreground:
  - The app keeps a small marker file: the launch ID, process ID, build and app version, whether it's in the foreground, and when that last changed. It's rewritten on every change between foreground and background.
  - At launch, a marker that says foreground means the previous launch died on screen: a crash, a watchdog kill, or iOS ending it for memory. The record carries `previous.launch_id`, `previous.pid`, `previous.build`, `previous.version` and `previous.foreground_since`.
  - A launch that ended in the background (swiped away, or ended by iOS while suspended) records nothing, because that's normal.
- **`mclaude.app.crash`**, from MetricKit, Apple's diagnostics for the app, delivered by iOS at a later launch (usually the next one):
  - `exception.type` (Mach exception type), `exception.code`, `signal`, `termination_reason`, the Objective-C exception's name and message when there is one, and the crashed build and app version.
  - `crashed.pid`, and `crashed.launch_id` when that process ID matches one of the last 20 launches the app has recorded.
  - The body is the full diagnostic as JSON: the call stack tree with each frame's binary name, binary UUID and offset. Frames aren't symbolicated on the phone (see Reading the data).
  - **Hangs, CPU exceptions and disk write exceptions** from the same payload are `mclaude.app.diagnostic` records (WARN), with `kind` = `hang`, `cpu_exception` or `disk_write_exception`, the duration or totals Apple reports, and the same JSON body.
- **Telemetry errors never change app behavior**, here too: a crash record that can't be written is lost, and the app goes on dying exactly as it would have.
- **Unchanged:** the existing error-report client and the server's `POST /telemetry` stay as they are.

## Composer probe

- **Each send is a trace.** Its root span, `composer.send`, lasts the 1 s observation window after the send.
- **At send time, before clearing**, it records:
  - the UIKit text view's actual text
  - whether the view holds marked (still-composing) text
  - the selected range
  - the active keyboard language
  - the model's text
- **For 1 s after the send**, it records each write individually:
  - **Model-side writes:** from → to (text and length), whether the new text equals the sent message, and the source. Sources are the send's clear, the follow-up re-clear (executed or skipped), or other.
  - **Keyboard-side writes to the text view** (typing, autocorrect or prediction commits, dictation): the text, its length, whether it equals the sent message, and whether marked text is present.
  - **Programmatic writes to the text view** (SwiftUI pushing the model's text into UIKit): the view's text is sampled every frame and before every other entry, so a change with no keyboard behind it is recorded with the time it was first seen, at most one frame late.
  - **Marked text starting or ending.**
- **Checks** at 0.1, 0.5 and 1.0 s compare the model's text with the text view's text.
- **On a mismatch:**
  - One log record with event `mclaude.rule.broken` and rule `composer_desync`, carrying the full timeline as JSON.
  - The screenshot is captured at the first failed check and uploaded through the server's existing screenshot upload. Its server path arrives as a `mclaude.rule.screenshot` record in the same trace (`uploaded` false if the upload failed).
  - At most one `composer_desync` per minute.
- **Sampling (Richard, 2026-11-08: "maybe not 0% but low"):** the outcome is known when the 1 s window ends, so the app decides then what to export (tail sampling at the source).
  - **Desync:** everything is exported: the `composer.send` span, the send-time snapshot (`mclaude.composer.send`), the timeline (`mclaude.composer.send.settled`), the rule break and the screenshot.
  - **In sync:** the same records for a random 5% of sends, and nothing for the rest. The sampled ones are the comparison set for bad sends.
  - Every exported record carries `mclaude.sampling.rate` (1.0 or 0.05), so counts can be scaled up.
- **Send counter:** every send, sampled or not, adds 1 to the counter `mclaude.composer.sends`, split by `outcome` (`in_sync` or `desync`). It's a standard OTLP cumulative sum, counted from app launch, and written once a minute and when the app goes to the background, whenever it changed. It carries no content. It tells "no breaks" apart from "the probe or the pipeline isn't working".
- **It never changes the text field.**

## Transcript alignment probe

- **What's measured:** the transcript column (the stack of rows; each row carries its own 16pt side padding) against the scroll view's visible frame, whenever either changes.
- **Aligned** means the column's left edge equals the viewport's left edge, and its width equals the viewport's width, both within ±0.5pt.
- **Kept in memory only**, and exported only when a break happens. Ring buffers of the last 30 entries each for:
  - rows wider than the viewport (row ID, kind, width)
  - drawer events: open, close, and the end of every page drag (translation, start point, whether the drawer was open)
  - keyboard show and hide
  - every programmatic scroll, with its source: `pin`, `repin` (with its delay), `stream_follow`, `overscroll_recovery`, `history_bottom`, `history_anchor`, `question`, `question_pill`
  - every change of the horizontal scroll geometry (offset, content width, container width), with its time
  - viewport size changes
- **Baseline:** once per opened session, a `mclaude.transcript.baseline` record with the first full measurement (viewport, column and scroll geometry, and whether it's aligned), so normal values are on record for each device.
- **Scroll geometry** (iOS 18 and later): the scroll view's horizontal content offset, content width and container width also count. Aligned additionally requires no sideways offset and content no wider than the container.
- **A state must last 300 ms** before it counts as misaligned, because layout passes report one-frame sizes.
- **On becoming misaligned:**
  - One `mclaude.rule.broken` record with rule `transcript_misaligned`. It carries the viewport frame, the column frame, the scroll geometry, the left shift, the widest rows, the ring buffers, and the session ID and event count. The screenshot is captured and uploaded as for the composer probe, with its path in a `mclaude.rule.screenshot` record in the same trace.
  - Each misaligned stretch is its own trace (span `transcript.misaligned`), and the screenshot record sits in it.
  - At most one report per 2 minutes.
- **On becoming aligned again:** a `transcript.realigned` record with how long the misalignment lasted.
- **It never changes the layout.**

## Blank screen probe

Richard, 2026-11-08: "catch whenever the screen is blank. I'm seeing it quite often, especially if the server takes a long time loading."

- **What's measured:** which transcript rows are on screen. Each row view (each instance, with its own token) reports when its visibility in the scroll view changes, and when it appears or disappears. Only these changes run any code.
  - A row is **on screen** when its view has appeared and not disappeared, and its last measured visibility is on. The visibility is kept across a disappear: the lazy stack can fire disappear and then appear again on a view that never moved, and the visibility callback only fires on a change, so it won't report "visible" again (Ops F-9: the rows in the screenshot had last reported "disappeared").
  - Opening another session clears nothing; the old session's row views disappear on their own. Clearing on open wiped rows of the new session that had already reported (Ops F-11: a row reported visible in the same millisecond as the switch, then stayed put).
- **Blank** means a session is open and no row is on screen, for at least 1 s (row swaps during layout leave one-frame gaps), while the app is active. Before the session's events have loaded, that's blank too. A session whose events have loaded and are empty (a new chat) is not blank: there's nothing to show. It becomes blank-eligible again once it has events.
- **Context kept in memory** (same ring buffers as the alignment probe, shared), plus:
  - the session's event loads: start, end, duration, whether they succeeded, and how many events came back
  - WebSocket connection changes, with the reason
- **On a blank:** one `mclaude.rule.broken` record with rule `transcript_blank`, in its own trace (span `transcript.blank`), carrying:
  - the session ID, event count and time since the session was opened
  - the transcript scroll view's state, read only at that moment: vertical and horizontal offset, content size, visible height, insets, and whether it's being dragged or decelerating
  - all the ring buffers
  - a screenshot, as for the other probes
  - a `kind`, also as a record attribute: `rows_offscreen` when the session has events (rows exist and none is on screen), `not_loaded` when it has none yet (its first load is in flight or failed)
  - at most one report per 2 minutes **for each kind**, so a slow first load can't use up the report for a rendering blank right after it (F-8: a send-time blank with 200 events went unreported 27 s after a `not_loaded` one)
- **When rows come back:** a `transcript.unblanked` record with how long the screen was blank, its `kind`, and whether it was reported. Every episode, reported or not, carries a body: the scroll view's state read when the blank was confirmed (same fields as the report) and again when rows came back, and all the ring buffers as of that moment.
- **Scroll reasons:** each `pin` entry in the scrolls ring says why it ran: `send` (the user sent a message), `queue` (a sent message landed in the server queue), `events` (the event count changed), or `first_load` (0 → N). The scrolls ring also records each time the overscroll recovery sees the list go past the end of its content or come back (`past_end`, with whether the app was active), and each recovery it skips (`overscroll_skipped`, with why).
- **Saved-copy restores** (docs/design-claude-uplift.md → Saved transcripts) go in the loads ring as `disk_restore`, with the event count and how long the read took. **Older-history pages** go there too, as `history_start` (with the page size) and `history_end` (events added, duration).
- **Diagnostics** (added 2026-11-09, Ops findings F-2/F-5: 14 of 20 reports were false alarms, with the rows on screen, and 6 were real blanks; the earlier data couldn't tell them apart). They only observe; what counts as blank is unchanged.
  - The report carries `visible_instances` (instances whose last visibility is on, appeared or not) and `live_instances` (appeared and not disappeared), with up to 10 examples each. No live instances mean the list built nothing; live instances that are all off mean rows are built and off screen.
  - A `rows` ring of every change: id, instance, visible, live, source (`geometry`, `appear` or `gone`), and the number of distinct row ids on screen after it. It keeps the last 80 changes.
  - A `context` ring: each `context()` call, with whether it opened a session and how many row ids were on screen.
  - A `scene` ring: app active, inactive, background and foreground changes.
  - Whether the bottom sentinel is on screen (`bottom_sentinel_visible`).
  - `mclaude.rule.screenshot` carries `captured_at`, the phone's capture time. The screenshot is drawn at the report; the server's file name is the upload time.
- **It never changes the screen.**

## Queue delivery

Richard, 2026-11-09: queued messages vanished when he switched sessions. The server had handed them to Claude, so they'd left the queue, but Claude hadn't read them yet, so they weren't in the transcript either (docs/design-claude-uplift.md → Queue group). These records show each queued message's whole trip, and what the phone was shown at each step.

- **Server** log records, each with `mclaude.session_id` and `mclaude.queue_id`:
  - `mclaude.queue.queued`: a message went into the queue (`mclaude.queue.length` after it).
  - `mclaude.queue.delivered`: it was written to Claude's stdin, with `mclaude.queue.reason` (`step boundary`, `turn ended`, `send now`, `restarted`), `mclaude.queue.batch_size`, and `mclaude.queue.waited_ms` since it was queued.
  - `mclaude.queue.read`: Claude's copy arrived as a user message, with `mclaude.queue.delivered_ms` since delivery.
  - `mclaude.queue.cleared` (WARN): it left the delivered list without a match, with `mclaude.queue.reason` (`turn_end`, `process_exit`, `restart`, `cap`) and `mclaude.queue.delivered_ms`.
- **`seq` on every WebSocket `sessions` frame:** a number that goes up by one per frame, numbered when the snapshot is taken, so a frame that arrives after a newer one shows up as a lower `seq`. It starts again at 1 when the server restarts. Older apps ignore it.
  - The server sends each frame from its own task, so frames can arrive out of order. The first test run caught it: the frame that added a queued message (`seq` 62) arrived just before an older one without it (`seq` 61), and the queue group vanished until the next frame.
  - **The app drops a stale frame:** one whose `seq` is lower than the highest it has applied on the current connection. That highest `seq` resets on every new connection, since a restarted server counts from 1 again. Frames without `seq` (older servers) are always applied.
- **Phone:** for the open session, a `mclaude.queue.frame` record each time the frame's queue or delivered ids change (switching to another session counts as a change). It carries the session ID, `seq`, `queue_ids` and `delivered_ids` (comma-separated), and `previous_seq`, the highest `seq` applied before it. A dropped stale frame also records, with `stale` = true, when its ids differ. Frames that change nothing record nothing.

## System map (phase 2)

Richard, 2026-11-08: "implement the system map/visual obs layer", covering phone, Mac, server and Claude. It's the human's live view of the whole system. Claude keeps reading the data with SQL.

### What feeds it

- **Phone** (already sending), plus:
  - **Vitals:** every 10 s while in the foreground, gauges `mclaude.app.cpu` (percent of one core), `mclaude.app.memory` (footprint, bytes), and `mclaude.app.connected` (1 or 0, the WebSocket).
  - **Main-thread stalls:** a watchdog thread pings the main thread every 50 ms. A stall of 250 ms or more becomes a `mclaude.app.stall` log record (duration, screen, session), sampled at 100%. Its body carries when the stall started and the transcript probe's ring buffers, read on the main thread right after it (so they show the work that ran before and during it, such as history pages, loads and scrolls; Ops F-10).
  - **HTTP client spans** for every request to mclaude-server: `HTTP <method>`, with method, path template, status and duration. Each request carries a W3C `traceparent` header, so the server's span joins the same trace.
- **mclaude-server** (`service.name` = `mclaude-server`), with the same kind of built-in OTLP/JSON exporter as the app, sending to the local collector:
  - **Request spans** (server kind) for every HTTP request: method, route, status, duration, session ID. They continue the phone's trace from `traceparent`.
  - **Turn spans** `claude.turn`: from the stdin write that starts a turn to Claude's `result` line, with session ID and outcome. A turn started by a phone request is a child of that request's span. Each turn span records Claude's own session ID (`claude.session_id`).
  - **Joining Claude Code's spans:** Claude Code 2.1.317 does **not** take a parent trace from stream-json input outside its cloud remote-session mode (verified 2026-11-08: its interaction span reports `parent.source="none"`). So its spans sit in their own traces, joined to the turn by Claude's `session.id` and the turn's time window. The user message still carries `parent.traceparent`, which costs nothing and would start working if a later version reads it.
  - **Decision and lifecycle records** (log records):
    - `mclaude.decision.input_routed` (`message`, `answer`, `queued`, `feedback`)
    - `mclaude.process.started` (session, pid)
    - `mclaude.process.exited` (session, pid, expected or not)
    - `mclaude.process.restart_scheduled` (session, backoff)
  - **Heartbeat gauges** every 15 s:
    - `mclaude.server.up` (1)
    - `mclaude.server.ws_clients`
    - `mclaude.server.sessions`, by `process` state: starting, running, restarting, stopped
    - `mclaude.server.turns_running`
- **Claude Code** (`service.name` as Claude Code sets it): its built-in OpenTelemetry is turned on for every Claude process the server starts, exporting all three signals to the local collector:
  - traces (enhanced telemetry, beta)
  - log events and metrics
  - full content: prompts, responses and tool content
  - resource attribute `mclaude.session_id`
  - trace propagation into tool subprocesses
- **Mac** (`service.name` = `mac-host`): a native OpenTelemetry Collector (contrib, the official release binary, checksum-verified), run as launchd service `com.mclaude.otel-host`. Every 10 s it reports host CPU utilization, memory usage and utilization, load, free space on the data volume (`/System/Volumes/Data`), plus per-process CPU and memory for mclaude-server and the Claude processes, to the Docker collector on localhost.

### Obs API

A small read-only service in the `mclaude-obs` stack (port 4320, all interfaces, reached as `http://<server host>:4320`). It runs fixed queries against ClickHouse and returns JSON. The phone never sends SQL.

- `GET /api/map?at=<unix seconds>&window=<seconds, default 60>` returns the state at that moment, looking back `window` seconds:
  ```json
  {"at": 1794150000, "live": true,
   "nodes": [{"id": "phone", "label": "Phone", "state": "ok", "value": "mem 182 MB",
              "reason": null, "details": {"cpu_pct": 4.1, "memory_mb": 182, "stalls": 0}}],
   "edges": [{"from": "phone", "to": "server", "state": "ok", "label": "ws · 38 ms"}],
   "events": [{"t": 1794149990, "kind": "rule", "rule": "composer_desync", "trace_id": "…", "node": "phone"}]}
  ```
  - Nodes: `phone`, `server`, `claude`, `bedrock`, `mac`, `obs`.
  - Edges: `phone`–`server`, `server`–`claude`, `claude`–`bedrock`.
  - `state` is `ok`, `degraded`, `failing` or `nodata`. `reason` says why when it's not `ok`.
- `GET /api/timeline?from=<unix>&to=<unix>&bins=<n, default 96>` returns, for each bin, the worst state of any node, plus pins:
  ```json
  {"bins": [{"t": 1794146400, "state": "ok"}],
   "pins": [{"t": 1794149990, "kind": "rule", "rule": "transcript_misaligned", "trace_id": "…"}]}
  ```
- `GET /api/finding?trace_id=<id>` returns a rule break: rule, time, node, the record's JSON body, and the screenshot path (loaded through mclaude-server's existing screenshot endpoint).
- `GET /api/health` returns `{"ok": true, "ingest_lag_s": 2.1}`.

### State rules

Box colors come **only from reliability signals**: errors, latency, stalls, down or no data. **Usage numbers** (tokens, cost, sends, turns) are shown as values and never color a box. The exception is usage that indicates breakage, such as token use over 5× the session's usual rate, which counts as a reliability signal and names itself in `reason` (Richard, 2026-11-08: "reliability metrics and usage metrics begin to blur the lines").

| Node | No data | Failing | Degraded | Value shown |
|---|---|---|---|---|
| phone | no vitals in the window (backgrounded or offline) | a stall ≥1 s, or a rule break | a stall ≥250 ms | memory |
| phone–server | phone has no data | phone's WebSocket disconnected | an HTTP span ≥2 s or an HTTP error | WebSocket state and median request time |
| server | no `up` heartbeat in the window | a 5xx response | a request ≥5 s, not counting input requests | sessions and WebSocket clients |
| claude | server has no data | a process restarting | an API error event | processes running, and turns running |
| bedrock | no model request in the window (shown as "idle", not grey) | model request errors | median model request ≥2 min (long generations are normal; only a median that suggests stuck requests counts) | median model request time, tokens per minute |
| mac | no host metrics in the window | data-volume free space < 5 GB | free space < 15 GB, CPU ≥85% or memory ≥90% | CPU % and GB free |
| obs | — (the API answered) | ingest lag ≥120 s | ingest lag ≥30 s | ingest lag |

Missing data is never drawn as healthy: `nodata` is grey with the words "no data".

### Map screen (iOS)

- **Entry:** a **System** row (`point.3.connected.trianglepath.dotted`) in the drawer, directly above Usage. Tapping it closes the drawer and opens the map full screen, with a Done button. The row has a small red dot when any node is `failing` in the latest Live poll.
- **Layout:**
  ```
  ┌─────────────────────────────────┐
  │ Done        System       ● Live │
  │   ┌───────┐   ws·38ms ┌──────┐  │
  │   │ Phone │━━━━━━━━━━━│Server│  │
  │   │  ok   │           │  ok  │  │
  │   └───────┘           └──┬───┘  │
  │   ┌───────┐           ┌──┴───┐  │
  │   │  Mac  │           │Claude│  │
  │   │cpu 41%│           │ 6 up │  │
  │   └───────┘           └──┬───┘  │
  │   ┌───────┐           ┌──┴────┐ │
  │   │  Obs  │           │Bedrock│ │
  │   │lag 2s │           │ 1.2 s │ │
  │   └───────┘           └───────┘ │
  │ ── At 14:02 ─────────────────── │
  │ ▲ transcript_misaligned · Phone │
  │ ▁▂▁▁▅█▂▁▁▁▲▁▁▁▁▂▁▁▁▁▁▁▁▁▁▁▁│    │
  │ 12:00        13:00       14:00  │
  └─────────────────────────────────┘
  ```
- **Boxes** show the label, a state word ("ok", "degraded", "failing", "no data") and the value. Colors: green ok, amber degraded, red failing, grey no data. The word is always shown, so meaning never depends on color alone.
- **Lines** are colored the same way, labelled with the edge label. The Mac and Obs boxes have no lines.
- **Events strip:** the `events` of the selected moment, newest first. Tapping a rule event opens its finding sheet.
- **Scrubber:** the timeline bins for the last 24 hours, each colored by its worst state, with ▲ pins for rule breaks.
  - Dragging selects a moment, and the map and events update to it. **● Live** returns to now.
  - While Live, the map polls every 3 s, and the timeline every 60 s.
- **Tapping a box** opens a detail sheet with the node's `details` as label–value rows, plus its `reason`.
- **Finding sheet:** rule, time and node, the screenshot (if any), and the record's JSON summarized as label–value rows, with the raw JSON in a collapsed section.
  - **Show in Ops** (Richard, 2026-11-10: "I still want the card for details of finding … just create a button to take me to the ops session"): a button under the header, shown once the record's trace ID is found in the trace IDs of a delivered finding (`GET /api/findings`, fetched when the map opens and with each timeline poll, and fetched again when the sheet opens if the trace isn't in it yet) and there's an Ops session in the session list. Held findings, rule breaks with no finding, and no Ops session show no button.
  - Tapping it closes the sheet and the map and opens the Ops session: the one named "Ops", preferring one working in `~/work/mclaude-v1`, then the oldest, the same pick the dispatcher makes. The conversation scrolls so the card that delivered the finding is at the top, rings it in clay for about a second, and stops following the bottom. If the card isn't loaded yet, older history loads page by page until it's found or the start is reached. If it's not in the transcript at all (still waiting in the queue, or from before the transcript was cleared), the conversation stays at the bottom, where the queue group is.
  - The sheet opened from a Finding card's **Open** (already in Ops) has no Show in Ops button.
- **States:**
  - a spinner on first load
  - "Can't reach observability (\<host\>:4320)" with Retry when the API fails
  - a failed Live poll keeps the last map and shows "Last updated hh:mm:ss"

## Ops session (phase 3)

Richard, 2026-11-08: "continue with the ops agent". Agreed design: one dedicated Ops session investigates findings from the evidence, proves the root cause (code pointer, reasoning and a reproduction), proposes a validated fix, tells Richard by push, and never ships on its own.

### The session

- **One managed MClaude session named "Ops"**, working in `~/work/mclaude-v1`. It's the same project as the other mclaude-v1 sessions, so it shares their memory and CLAUDE.md.
- The dispatcher looks it up by name in the live server's session list. If there's none (never created, or ended), it creates one in that folder and renames it "Ops" when the next finding arrives.
- **How Ops works** is a project skill, `ops`. Every Finding card ends by pointing at it.

### Triggers

Decided by Richard, 2026-11-08: all four. Only live data counts, using the same filter as the map (no simulator, only the live server).

| Kind | Records |
|---|---|
| `rule` | `mclaude.rule.broken` (composer_desync, transcript_misaligned, transcript_blank, any future rule) |
| `stall` | `mclaude.app.stall` with `duration_ms` ≥ 1000 |
| `crash` | `mclaude.app.exception`, `mclaude.app.crash`, `mclaude.app.unclean_exit`; `mclaude.process.exited` with `mclaude.expected` = false; `mclaude.process.restart_scheduled`; server request spans with a 5xx status |
| `model` | Claude Code `api_error` events, and errored `claude_code.llm_request` spans |

Plus one metric-based trigger, kind `host`: the Mac's data volume has under 5 GB free (`system.filesystem.usage`, state `free`), at most one finding per hour. On 2026-11-08 the disk filled up and took OrbStack and the whole stack down with no warning.

The Ops session's own process restarts and model errors never trigger, so a problem in Ops can't loop into more Ops work.

### Dispatcher

Part of the obs-api service.

- **Scanning:** every 30 s it reads trigger records newer than its watermark. The watermark is persisted, so a restart neither repeats nor skips records.
- **Signature:** each record gets one, made of the kind, the rule or record name, and the session ID if there is one.
- **Dedupe:** a record whose signature matches a finding opened within the last hour is added to that finding (count + 1, last seen, plus its trace ID) instead of opening a new one.
- **New findings:** a new signature opens finding `F-<n>` with status `new`.
- **Rate limit:** at most 10 findings are delivered to Ops per hour. Past that, findings are recorded but not delivered, and one card per hour says "N more findings waiting".
- **Findings store:** a SQLite file in the obs-api volume. Each finding has an id, signature, kind, title, first and last seen, count, trace IDs, status (`new`, `investigating`, `root_cause`, `fix_proposed`, `needs_you`, `resolved` or `not_a_bug`), the proof fields, and updated at.

### Delivery: the Finding card

- The dispatcher posts to the live server's session input endpoint with `"automated": true`. The text is:
  ```
  <mclaude-finding id="F-12" kind="rule" rule="composer_desync" at="2026-11-08T15:02:11Z" trace="5b8e…" count="1">One-line summary.</mclaude-finding>
  Investigate this finding with the ops skill.
  ```
- **Server:** an automated input is always delivered as a message: queued while the session is busy, and never used as the answer to a pending question, plan or tool prompt. It raises no "needs you" push.
- **App:** a user event whose text holds a `<mclaude-finding>` block renders as a **Finding card**, never as a user bubble:
  - ▲ "Finding F-12 · composer_desync"
  - the time, the summary, and "×N" when count > 1
  - an **Open** button that opens the System map's finding sheet for that trace
  - the card is where the finding sheet's **Show in Ops** lands (see Map screen → Finding sheet)
  - the trailing instruction line is not shown

### Ops API

Endpoints on obs-api, for the Ops session.

- `GET /api/findings?status=<s>` lists findings, newest first.
- `GET /api/findings/<id>` returns one finding with its trace IDs and time range.
- `POST /api/findings/<id>` updates a finding. Body: `{"status", "summary", "root_cause", "code_pointer", "reasoning", "reproduction", "proposed_fix", "question"}`, any subset. It needs the header `X-Ops-Token`, holding the contents of `~/.mclaude/obs/ops-token` (random, mode 600, mounted into the container). Without the token it returns 403.
- **Push:** when a finding's status changes to `root_cause`, `fix_proposed` or `needs_you`, obs-api calls the live server's new owner-only `POST /push/notify` with `{"title", "body", "sessionId"}`. The server sends it with its existing APNs sender, and tapping it opens the Ops session. Title: "Ops · F-12 composer_desync". Body: the summary or the question.

### How Ops investigates

This lives in the `ops` skill.

1. Mark the finding `investigating`.
2. Pull the evidence with `mclaude-obs/query.sh`: the trace's records, the surrounding minutes across the phone, server, Claude and Mac, and the screenshot (a file under `/tmp/mclaude-screenshots/`, opened as an image).
3. Read the code involved. Form a hypothesis and check that every layer agrees with it.
   - **No proof from time alone.**
   - **Absence counts only when the pipeline was healthy.**
4. **Proof** = a code pointer, the reasoning, and a reproduction (a simulator run against a :8399 test server, or a test). If the evidence can't separate the hypotheses, set `needs_you` with the instrumentation that would, and propose that as the fix.
5. Record the proof fields and set `root_cause`.
6. Propose a fix, validated in the simulator, and set `fix_proposed`. **Where the fix is built and tested is still open** (it depends on the git decision); until that's decided, Ops stops at `root_cause` with a written fix plan.
7. Never ship, never restart the live server, never test against :8377, and never edit the shared tree without Richard's OK. Shipping after Richard says "ship it" follows the normal `/feature-change` lane, including coordinating with other sessions.

## Reading the data

Claude queries ClickHouse with SQL through `mclaude-obs/query.sh`, which is read-only. Examples:

- **Latest rule breaks:**
  ```sql
  SELECT Timestamp, LogAttributes['rule'], Body
  FROM otel_logs
  WHERE EventName = 'mclaude.rule.broken'
  ORDER BY Timestamp DESC LIMIT 20
  ```
- **Recent crashes and unclean exits:**
  ```sql
  SELECT Timestamp, EventName, ResourceAttributes['mclaude.build'], LogAttributes
  FROM otel_logs
  WHERE EventName IN ('mclaude.app.exception', 'mclaude.app.unclean_exit', 'mclaude.app.crash')
  ORDER BY Timestamp DESC LIMIT 20
  ```
- **Symbolicating a MetricKit stack:** each frame has a binary UUID and an offset. The dSYM for build N is in `/tmp/MClaude-N.xcarchive/dSYMs/` (from `/deploy-ios`). Check its UUID with `dwarfdump --uuid`, then run `atos -arch arm64 -o <dSYM>/Contents/Resources/DWARF/MClaude -l 0x100000000 <0x100000000 + offset>`.
- **Everything in one send's trace:**
  ```sql
  SELECT Timestamp, EventName, Body, LogAttributes
  FROM otel_logs
  WHERE TraceId = '<trace id>'
  ORDER BY Timestamp
  ```
- **Sends per outcome and build** (from the counter; each launch counts from zero, so take each launch's latest value):
  ```sql
  SELECT build, outcome, sum(v) FROM (
    SELECT ResourceAttributes['mclaude.build'] AS build, ResourceAttributes['mclaude.launch_id'] AS launch,
           Attributes['outcome'] AS outcome, argMax(Value, TimeUnix) AS v
    FROM otel_metrics_sum WHERE MetricName = 'mclaude.composer.sends'
    GROUP BY build, launch, outcome)
  GROUP BY build, outcome ORDER BY build, outcome
  ```

## Error handling

- **Mac unreachable** (off the tailnet, Mac asleep): the outbox holds the data and sends it later.
- **Collector down:** the outbox holds the data.
- **ClickHouse down:** the collector's persistent queue holds the data until ClickHouse returns.
- **Screenshot upload fails:** the screenshot record says `uploaded` false, with no path.
- **Telemetry errors never change app behavior.**

## Scope

**In:** everything above: the pipeline, the app exporter and outbox, crash records, the composer probe, the transcript alignment probe, the blank screen probe, the system map with its feeds (phone vitals and stalls, server and Claude Code instrumentation, Mac host metrics) and the Obs API, and the Ops session (dispatcher, Finding cards, Ops API, push).

**Out** (see `docs/plan-observability-later.md`):
- the obs service's story builder and cross-layer detectors
- reports via shake or the bug button
- agreement checks, the entity graph and CPU attribution
- sampling beyond the composer, heartbeats with a dead man's switch
- replay mode
