# Observability: later phases

> **Not what's being built now.** This is the full target design agreed with Richard on 2026-11-07/08, kept for reference. The spec of what's being built is `docs/plan-observability.md` (the MVP).

## Overview

MClaude has almost no telemetry today. The app reports WebSocket disconnects and the server prints them. When Richard reports a bug that Claude can't reproduce, there's nothing to go on, so the bug waits until it happens again. Two recent bugs were found only by reading code: the build-120 typing lag and the stuck placeholders after typed answers.

This feature makes **"I can't reproduce it" a dead end that no longer exists.** Evidence from every layer (phone app, network, Mac, mclaude-server, Claude processes) is captured all the time, as one story on one timeline. When something goes wrong, the evidence from that moment is already stored. A dedicated **Ops session** investigates from that evidence. It proves the root cause (code pointer, reasoning and a reproduction) and proposes a fix. A native **live system map** in the iOS app shows the same story to the human.

It's also Richard's SRE playground: standard OpenTelemetry underneath, and new ideas in how the data is analyzed and presented. It is not a Grafana dashboard.

## Decisions

Decided with Richard on 2026-11-07.

| # | Decision | Why |
|---|---|---|
| 1 | The top goal is to end "can't reproduce" (Richard: "when I report a bug and you say you can't replicate it, then what do I do?"). | Every other choice serves this one. |
| 2 | Standard OpenTelemetry (OTLP, the OTel Collector, OTel semantic conventions) underneath. The new part is analysis and presentation. | Standard practice and tooling; the invention goes where dashboards fall short (root cause). |
| 3 | **One story, not layers.** All layers share one timeline, and each user action is one OpenTelemetry trace across every layer. Layers must agree, and a disagreement is itself a finding. | Separate views per layer hide causes that cross layers. |
| 4 | Storage: the OTel Collector writes into **ClickHouse**, all running in Docker on the Mac. | One store and one query language for every signal, so cross-layer joins are a single query. |
| 5 | **Full content** is captured (message text, transcript lines, request bodies), unless volume gets unwieldy. Then the summaries carry the signal (Richard: "I'm not afraid of data capture, but too much data means slow and clunky"). | Reproductions need the real inputs. |
| 6 | **Send continuously.** The phone streams all the time and keeps a local outbox only while it can't reach the Mac. | Single user; the Mac has the disk. |
| 7 | Retention: **30 days of raw data, summaries kept forever.** Reports, findings and screenshots are kept forever. | Enough to compare against last week's build. |
| 8 | Reports: **shake anywhere, plus a bug button** on session screens. Each report includes a screenshot plus the app state behind it, and an optional note. | Visual bugs need the state behind the pixels. |
| 9 | Reports, and automatic detections (crashes, broken app rules, stalls, layer disagreements), all go to **one dedicated Ops session**. | Working sessions stay uninterrupted. |
| 10 | **Autonomy up to a proposed fix:** a root cause with proof, plus a fix validated in the simulator. Shipping waits for Richard's OK. | |
| 11 | **Proof of a root cause** = code pointer + reasoning + a reproduction. | |
| 12 | Claude reads the data through **MCP tools plus a read-only SQL tool**. | A fast common path, with no ceiling. |
| 13 | The human's view is a **live system map** (topology plus time scrubber) on a native iOS screen, opened from a drawer entry next to Usage. Claude doesn't use the map. | "Human interfaces are for humans." Phone-first. |
| 14 | Ops tells Richard through an **iOS push notification** (the server's existing APNs sender), with the full write-up in the Ops session. | Works when the app is closed. Richard picked "notify outside the app"; the Signal notifier is not configured, while APNs push is live. |
| 15 | The first version includes the evidence pipeline, Ops investigations **and the map**. | |
| 16 | SLOs, error budgets and burn-rate alerting are a **separate feature**, built around the key user stories. This feature provides the end-to-end traces those SLOs will measure. | |
| 17 | **Claude Code's own OpenTelemetry is a first-class part of the trace** (Richard: "you cannot ignore that Claude Code has its own OTel"). Its spans join ours by trace context, with a mapping where that's impossible. | Claude's own view of the turn (model calls, tools, hooks, subagents) is where much of the time and cost goes. |

## How evidence is collected

Every bug shows up as one of five kinds of data. Each layer records all five. **Each user action is one trace**: the app starts it at the tap or send, and its trace ID follows the action from the phone through the server to Claude and back. Every record caused by the action, in any layer, is a span or a log record in that trace.

1. **Inputs**: everything that comes in. Taps, typing, sends, every network request and response, every WebSocket frame in and out, what the server writes to Claude and every line Claude prints, and OS events.
2. **State changes**: each layer's internal model, recorded as structured changes. For example: "placeholder +1 (id, text)", "connection: connected → reconnecting". A full snapshot is recorded at launch, on returning to the foreground, and in every report.
3. **Decisions, with reasons**: wherever code takes a branch that changes what is shown or sent, it records which branch and why. For example: "typed text sent as the answer to a pending question, not as a message", or "placeholder abc: no copy within the window, still waiting".
4. **What's on screen**: the screen's structure rather than an image: which screen is open, and the visible items with their kind, ID, a text summary and state flags. Recorded whenever the screen's content changes. An image is captured only for reports.
5. **Cost and health**: rebuild counts and durations, main-thread stalls, frame hitches, CPU, memory, temperature, battery, network timings, and Claude's tokens and cost.

**Completeness test.** A recording is complete for a bug when feeding its recorded inputs into the code (the replay mode in the simulator, or a test) ends in the same recorded state. Where a replay diverges from the original, the divergence names the missing signal.

**Rule for fixes.** Every fix the Ops session proposes adds the signal, rule check or detector that would have caught the bug, unless one already exists. Over time, each class of bug gets caught automatically.

## How data points are correlated

Two records happening at the same time is not, on its own, a link between them. OTel links spans to each other and logs to spans. It leaves host metrics and sampled values loosely connected, joined by shared labels and a time window. This section defines how every data point joins every other one. **Standard OTel structures are used wherever OTel has one**, so the raw data is plain OTel any tool can follow. Our own additions live in a small derived index next to it.

### The three join keys

1. **When:** a timestamp on the Mac's clock. Phone records are corrected by the clock offset and carry its uncertainty (half the export round trip, in ms). A join by time never claims more precision than the two records' combined uncertainty plus the sampling interval.
2. **Where:** the entity the record belongs to. Entities follow the shape of OTel's Entities model (in development in spec 1.62): a type, identifying attributes and descriptive attributes, carried in the resource.

   | Entity | Identified by |
   |---|---|
   | phone device | install ID |
   | app launch | install ID, launch start time |
   | Mac host | `host.id` |
   | mclaude-server run | `process.pid`, process start time |
   | MClaude session | session ID |
   | Claude process run | `process.pid`, process start time |
   | tool subprocess | `process.pid`, process start time, `process.parent_pid` |
   | network path | install ID, Mac host |
   | observability stack | the stack's own service instances |

   Process identity is pid plus start time, so a reused pid is never confused with an old process. Starts and exits, and links such as "this Claude process run serves session s44", are sent as OTel **entity events**. From them, the obs service keeps an **entity graph** that is valid at any moment. The map's boxes are these entities.
3. **Why:** the cause, using OTel's own structures:
   - **Spans** nest by trace context. Each user action is one trace.
   - **Log records** (decisions, inputs) are emitted inside the span they belong to, so they carry its trace and span ID.
   - **State changes** are short spans, children of the span that caused them: an input frame, a tap or a timer firing. Each carries the store's version number (`mclaude.state.version`), which goes up on every write, and whether the write changed anything.
   - **Rebuilds** are spans with **span links** to the state-change spans that triggered them, each link marked `mclaude.link.type=trigger`. A rebuild with no trigger links had no cause.
   - **Screen records** are spans with span links to the state changes they show (`mclaude.link.type=shows`).
   - **WebSocket frames:** a received frame's span is a child of the sender's span, because every frame carries `traceparent`.
   - **One input serving several actions:** a queue flush that sends several queued messages in one turn starts its own trace, with a span link to each message's trace.

### Numbers

- **Counts and durations** (rebuilds per second, request latency, stalls, error rates) are computed by the obs service from the stored spans and log records, not sent as separate counters. Every number opens into the records it counts.
- **Measured samples** (CPU, memory, temperature, battery, host metrics, Tailscale path) join by entity and time interval. The obs service attributes each sample to the spans active on the same entity during its interval: to one span, shared between several in proportion to overlap, or **unattributed** (work with no known cause, itself a signal). For the samples that matter most the CPU is measured inside the span, so nothing is inferred:
  - the app's main-thread spans (rebuilds, input handling, event processing) record the main-thread CPU time they used
  - the server's turn spans record the Claude process's CPU time over the turn
- **Cardinality rule:** no metric carries a session, trace, message or other unbounded ID as an attribute. Those stay on spans and log records, and per-entity numbers are computed from event rows in ClickHouse. The host agent's per-process metrics are the only per-entity metrics, and they're limited to mclaude-server, the Claude processes and their tool subprocesses.

### Join table

| From ↔ to | Joined by |
|---|---|
| span ↔ span, any layer | trace context, or span links |
| log record ↔ span | trace and span ID |
| state change ↔ its cause | parent span |
| rebuild or screen ↔ state change | span links (`trigger`, `shows`) |
| frame sent ↔ frame received | parent span (`traceparent` on the frame) |
| server turn span ↔ Claude Code's interaction span | `claude.session_id` on the turn = Claude Code's `session.id`, and the turn's time window. Claude Code 2.1.317 ignores a parent passed on the input message outside its cloud remote-session mode (verified 2026-11-08) |
| Claude Code span ↔ Claude Code span | Claude Code's own trace context |
| Claude Code log event ↔ its span | trace and span ID, or `prompt.id` → interaction span |
| Claude Code session ↔ MClaude session | `session.id` (the server starts Claude with the MClaude session ID) |
| tool subprocess ↔ tool span | trace context propagated into the subprocess |
| main-thread CPU ↔ span | CPU time measured in the span |
| CPU or memory sample ↔ span | entity and interval overlap (the attribution index) |
| host process metrics ↔ session | entity graph: process → Claude process run → session |
| network path sample ↔ request | path entity and time |
| anything else | time only |

### Investigation rules

- **No proof from time alone.** A root cause rests on the structural joins above: trace context, span links, the entity graph, attribution. Two things that merely happened at the same time are a lead. If the lead matters, the missing link becomes a "needs signal" item.
- **Silence is checked, not assumed.** Every source sends a heartbeat (see Heartbeats and dead man's switch), so a source that goes quiet, whether crashed, partitioned or asleep, is known to be quiet.
- **Absence counts as evidence only when the pipeline was healthy.** Before concluding that something didn't happen ("the server never sent the echo"), Ops checks the telemetry pipeline's health for that source and time. If the source's export queue overflowed, the outbox dropped data or the collector refused data, the absence proves nothing.

### Agreement checks

"Layers must agree" is checked continuously. Each check opens a finding when it fails.

- **Main-thread time:** CPU measured inside main-thread spans vs the app's sampled main-thread CPU. Unattributed main-thread time over 20% for 10 s means work with no known cause.
- **Frames:** frames sent per channel type (server counters) vs frames received (phone counters). A mismatch beyond in-flight frames, while the pipeline is healthy, is a finding.
- **Request time:** the phone's client span duration minus the server's span duration should equal network plus queueing time. A result below zero, beyond the clock uncertainty, is a finding about the clock or the instrumentation.
- **Claude CPU:** the host agent's per-process CPU for a Claude process vs the sum over its turn spans.
- **Tokens:** Claude Code's reported tokens per turn vs the usage in that turn's transcript lines.
- **Claude trace join:** every turn span has exactly one Claude Code interaction span with the same Claude session ID inside the turn's time window. None or more than one is a finding. (Claude Code 2.1.317 doesn't accept a parent from the input message outside its cloud remote-session mode, so this is a session-and-time join; if a later version starts reporting `parent.source="event"`, the join becomes parent-child.)

### Where joins are made

1. **At the source.** The code that knows the truth records it: trace context, parent spans, span links, entity events and CPU inside spans. Rule: **a link that can be known at the source is recorded at the source**, never reconstructed later.
2. **At ingest, in the collector: per-record work only.** It stamps the receive time, routes records missing required attributes to a `malformed` table rather than dropping them, and applies tail sampling (see Sampling). It never joins records with each other; it sees batches in arrival order, and phone outbox data can arrive hours late.
3. **In the obs service: a derived index.** Two joins can't be recorded at the source, and the obs service builds them: the **entity graph** (from entity events) and the **attribution** of measured samples to spans. They're stored next to the raw data, rebuilt by re-running when late data arrives, and can always be rebuilt from the raw data alone.

**Analysis** (Claude, the map, the story and the detectors) reads the raw OTel data plus this index. Nothing downstream invents a join of its own.

### Sampling

Today every source keeps everything. Sampling is a setting per source and record kind, so it can be turned down as the system grows, with no change to code or data model.

- **All or nothing per trace:** OTel's consistent probability sampling, with the decision carried in the trace context, so the phone, server and Claude Code keep or drop the same trace. A story is never half there.
- **Interesting traces always kept:** tail sampling in the collector keeps, in full, every trace with an error, a stall, a broken app rule, a layer disagreement or a report.
- **Estimates:** each kept record carries its sampling probability in the standard way, so counts can be scaled up statistically.
- **Full detail around reports when sampled:** the phone and server keep the last 15 minutes at full detail locally. When a report or detector fires, that window is uploaded. At today's setting of 100% this does nothing.

### Where we depart from OTel

- **Custom attributes** where OTel has none: `mclaude.state.version`, `mclaude.link.type`, the install ID and the clock uncertainty, all named in OTel's style.
- **Entities ahead of the standard:** entities and entity events follow a spec still in development. When the spec's entity relationships are finished, the format changes and the model doesn't.
- **The derived index** (entity graph, attribution) is ours. Nothing in OTel builds it.

## User flow

### Something looks wrong (manual report)

1. Richard shakes the phone anywhere in the app, or taps the bug button in a session screen's header.
2. The app takes a screenshot of the current screen before showing anything. It then opens the **Report** sheet with a thumbnail of the screenshot, an optional note field (typing or keyboard dictation), and **Send**.
3. On Send, the report is uploaded. It contains the screenshot, the note, a full state snapshot, the current screen structure, the time and the current session. The sheet closes with a toast: "Reported. Ops is looking into it." When offline, the toast says "Saved. Will send when connected."
4. The Ops session receives the finding and investigates (see Ops investigation).
5. When Ops has a result, a push notification arrives, for example "Ops: stuck placeholder after answering a question. Root cause found, fix proposed". Tapping it opens the Ops session at the write-up.
6. Richard replies in the Ops session: approve the fix ("ship it"), ask questions, or reject.

### Something goes wrong without a report (automatic detection)

The same flow, starting at step 4. The finding comes from a detector instead of a report, and an automatic report (screenshot plus state) is captured when the app is in the foreground.

### Looking at the system

Richard opens the drawer → **System**. The map shows the system live. He drags the scrubber back to "when it felt slow", and each box shows its state at that moment. Pins mark reports and findings; tapping one shows what Ops found.

## Components

### Observability stack (Docker on the Mac)

One Docker Compose project, separate from the k3d cluster, with three containers:

- **Collector**: the OpenTelemetry Collector (contrib distribution).
  - Receives OTLP over HTTP (port 4318) and gRPC (port 4317), published on the Mac's Tailscale IP and on localhost. Reached as `otel.mclaude.internal:4318` through the existing wildcard DNS.
  - Exports to ClickHouse with the standard ClickHouse exporter and the OTel schema. Raw records are stored as received.
  - Does per-record work and tail sampling only (see Where joins are made). It never joins records.
  - Uses a file-backed persistent queue, so a ClickHouse restart doesn't lose data.
  - The existing scratch `otel-collector` container (a May 2026 debug-only experiment on the same ports) is stopped and removed when this stack is first deployed.
- **ClickHouse**: stores everything. Reachable on localhost only, never on the Tailscale IP. Raw OTel tables have a 30-day TTL. The observability service's own tables (summaries, story lines, findings, reports) have no TTL.
- **Observability service** (new, called "obs"). One process on port 4320, published on the Tailscale IP and localhost and reached as `obs.mclaude.internal:4320`. It provides:
  - the report upload endpoint
  - the map's query API
  - the MCP server for Claude
  - the story builder, the detectors, and dispatch of findings to the Ops session

  Screenshots are stored as files in `~/.mclaude/obs/screenshots/`, bind-mounted into the container, so Claude can open them directly as images on the Mac.

**Transport.** Everything inside the tailnet is plain HTTP; Tailscale (WireGuard) already encrypts end to end. The iOS app has an App Transport Security exception scoped to the `mclaude.internal` domain only.

### Mac host agent (native, launchd)

Docker on the Mac runs inside a Linux VM, so a container can't see the Mac itself. A second, native OpenTelemetry Collector runs as launchd service `com.mclaude.otel-host`. It reports, every 10 s, to the Docker collector on localhost:

- host metrics: CPU, memory and memory pressure, disk, network, load
- per-process CPU, memory and thread count for mclaude-server and every Claude process, matched by executable name and pid. The obs service maps each pid to a Claude process run through the server's start and exit records.

### mclaude-server

Instrumented with the OpenTelemetry Swift SDK. It exports OTLP to the local collector.

- **Traces:**
  - A span for every HTTP request and every WebSocket message received. It continues the app's trace from the `traceparent` header (HTTP) or the `traceparent` field (WebSocket messages).
  - A **turn span** per Claude turn, in the trace of the input that started it. It begins at the stdin write and ends at Claude's `result` line, and the server's records of Claude's output lines go inside it. Every user message the server writes to Claude's stdin carries the turn span's trace context as the message's `parent.traceparent` (ignored by Claude Code 2.1.317 outside its cloud remote-session mode, so Claude's spans join the turn by session ID and time window). A queue flush that sends several queued messages in one turn starts its own trace, with span links to each message's trace.
- **Inputs:** the full body of every request, every WebSocket frame sent to clients, every line written to a Claude process's stdin, and every line read from its stdout. Binary uploads (screenshots, videos, files) are recorded as size, type and hash only.
- **State changes:** each session's status, process state, queue contents, and pending question, plan or approval. Also the client list: which WebSocket clients are connected, and which sessions they follow.
- **Decisions:** at every branch that routes or transforms input or output. Examples:
  - typed text routed as a message, as a question's answer, or as plan or tool feedback
  - message queued or sent now
  - an event deduped or dropped
  - a mid-turn replay removed
  - a restart scheduled, with its backoff

  Each decision is a log record in the trace of the input that caused it.
- **Trace context on the way back:** an event the server pushes to clients carries the `traceparent` of the input that caused it, when there was one. For example, the user-message echo carries its send's trace, so the app's handling of the echo joins the same trace.
- **Work not caused by a user action** (timers, reconnects, restarts, polling) starts its own trace.
- **Tailscale path:** every 30 s the server records the path to each peer device (direct or relayed, and round-trip time), taken from the Tailscale CLI's status output.
- **Claude processes:** each Claude process is started with Claude Code's built-in OpenTelemetry export pointed at the local collector, with all three signals on:
  - **Traces** (Claude Code's enhanced telemetry, beta in 2.1.317): an interaction span per prompt, a span per model request, spans for each tool call and its execution, time blocked waiting for the user, hooks, MCP calls, subagent spawns, compaction and Bash subprocesses.
  - **Log events:** prompts, model requests and errors, tool decisions and results, all carrying `session.id` and `prompt.id`.
  - **Metrics:** tokens, cost, sessions, active time, lines changed, commits and pull requests.
  - **Content:** user prompts, assistant responses and tool content are all logged, per the full-content decision.
  - **Trace propagation into tools:** turned on, so a subprocess a tool starts (for example a build run from Bash) carries the tool span's trace context.
  - **Resource attributes:** the MClaude session ID, the Claude process run's entity ID and the Claude Code version.

  Claude Code's own trace data is kept as Claude Code emits it, not translated. The join to MClaude's traces is by Claude's session ID and the turn's time window (see How data points are correlated). Each input message also carries `parent.traceparent`, which 2.1.317 ignores outside its cloud remote-session mode. The Claude Code version is recorded on every Claude process run, because these telemetry features are beta and can change between versions.
- **Old telemetry endpoint:** `POST /telemetry` stays for older app builds. Each report it receives is written as a log record instead of printed.

### iOS app

Instrumented with the OpenTelemetry Swift SDK. It exports OTLP over HTTP to `otel.mclaude.internal:4318`. Every record carries resource attributes: app version and build, device model, OS version, simulator or device, and a stable install ID.

- **Sending:**
  - Batches go out every 5 s in the foreground. On going to the background, the app flushes using its background task time.
  - Records that can't be sent go to an on-disk **outbox**, capped at 200 MB. It survives relaunch and is sent oldest-first once the collector is reachable.
  - When the cap is hit, the oldest records are dropped, and a "dropped N records from T1 to T2" record is sent in their place, so the gap is explicit.
- **Clock alignment:** every export records the phone's send time. The obs service compares it with the receive time and the round trip, keeps a per-device clock offset, and corrects phone timestamps onto the Mac's clock.
- **Inputs:**
  - Taps on interactive elements: the element's accessibility identifier or label, and the screen.
  - The composer's text: recorded after 1 s without a keystroke, plus a keystroke count.
  - **Text inputs, both sides.** A text field has two copies of its text: the app's model and the UIKit text view that displays it. Writes to either copy are recorded with their source: the app (and which code path, such as a send's clear), SwiftUI pushing the model into the view, or the keyboard (typing, autocorrect or prediction commits, dictation). Each records from → to, and whether the view held marked (still-composing) text. Writes are normally debounced to 1 s. In the 1 s after a user action on that field, such as a send, they're recorded individually. Each user action on a text field also records the view's actual text, marked text, selection and active keyboard at that moment.
  - Every send, answer, approval and cancel.
  - Every HTTP request and response, with full bodies (binary uploads as size and hash) and per-phase timings: DNS, connect, TLS, first byte, total.
  - Every WebSocket frame in and out, with full content.
  - OS events: foreground and background, memory warnings, thermal state, low power mode, network path changes (interface, constrained, expensive).
- **State changes:** every piece of state a screen depends on, recorded as changes:
  - the session list
  - connection state
  - the open session and its event count
  - pending placeholders (each added or removed, with the reason)
  - queued messages
  - pending question, plan or approval
  - composer mode
- **Decisions:** at every branch that changes what's shown or sent. Examples:
  - a placeholder matched or still waiting, and why
  - reconnect and backoff
  - an event cache hit or miss
  - events deduped or dropped
  - a send retried or marked failed
- **What's on screen:** whenever a screen's content changes, debounced to 1 s. It records the screen's name and its on-screen elements (kind, ID, a text summary of up to 80 characters, and state flags such as "failed" or "pending"), read from the accessibility tree of the visible window.
- **Cost and health:**
  - Each instrumented screen records every rebuild: the screen, its duration, the main-thread CPU time it used, and its trigger versions (see How data points are correlated).
  - A watchdog thread detects main-thread stalls of 100 ms or more. For stalls of 250 ms or more it samples the main thread's call stack.
  - Frame hitches are summed per second.
  - App CPU and memory footprint every second; battery level and charging state every minute.
  - MetricKit crash, hang and CPU-exception diagnostics, forwarded when iOS delivers them, which is usually the next launch.
  - The instrumentation records its own CPU time and bytes sent, so its overhead is visible.
- **Overhead rule:** capture work on the main thread is limited to reading values. Encoding, batching and sending happen off the main thread. Instrumentation failures are swallowed and counted, and never change app behavior.
- **App rule checks:** evaluated after every state change, plus every 5 s. A broken rule records a "rule broken" record. In the foreground it also captures an automatic report (screenshot and state), at most once per rule per 10 minutes. The first rules:
  - A placeholder is older than 2 minutes and isn't marked failed.
  - A placeholder is still shown while the user event it represents is already in the session.
  - The WebSocket has been disconnected for over 30 s while the app is in the foreground.
  - A session's event count went down without a reload.
  - **Screen and state disagree:** the screen shows something that isn't in the state behind it, or misses something that is. For example, a bubble with no event behind it, or a text field showing different text from its model. Besides the regular checks, it runs at 0.1 s, 0.5 s and 1 s after every user action, so a screen that fails to settle is caught even when the next keystroke would hide it.
- **Rules only observe.** A broken rule records and reports. It never changes what's on screen, so the user sees the bug exactly as it happens.
- **Report capture:** shake anywhere, or the bug button in a session screen's header. Shake-to-undo is turned off app-wide so the shake always means "report".
- **Replay mode** (simulator builds only): the app can be launched with a recording exported from the obs service. Its network layer is then replaced by a player that feeds the recorded HTTP responses and WebSocket frames, and the recorded user actions (open session, send, answer, approve), in their original order and relative timing. There's an option to run as fast as possible. A replay sends its own telemetry tagged with the finding it replays, so the two recordings can be compared.
- **Replaced:** the existing telemetry client is removed. The disconnects it reported are covered by the inputs and decisions above.

### Report sheet (iOS)

```
┌─────────────────────────────────┐
│ Report a problem          Cancel│
│ ┌──────┐                        │
│ │ shot │  Session · 14:02:31    │
│ └──────┘                        │
│ ┌─────────────────────────────┐ │
│ │ What's wrong? (optional)    │ │
│ └─────────────────────────────┘ │
│            [ Send ]             │
└─────────────────────────────────┘
```

- **Screenshot thumbnail:** taken before the sheet appeared. Tapping it shows it full screen.
- **Session line:** the current session's name (or the screen's name outside a session) and the time.
- **Note field:** optional and multi-line. Keyboard dictation works as usual.
- **Send:** uploads the report, closes the sheet and shows a toast: "Reported. Ops is looking into it." When the obs service can't be reached, the report goes into the outbox and the toast says "Saved. Will send when connected."
- **Cancel:** discards the screenshot and the report.
- **Bug button:** a `ladybug` icon in the session header, just left of •••. Tapping it opens the sheet, the same as a shake. Its accessibility label is "Report a problem".

### Live system map (iOS)

Opened from a **System** row (`point.3.connected.trianglepath.dotted`) in the drawer, directly above Usage. The row shows a small red dot when a finding needs Richard: a fix is proposed, or Ops is asking a question. Tapping the row closes the drawer and opens the map full screen, with a Done button.

```
┌─────────────────────────────────┐
│ Done        System       ● Live │
│                                 │
│   ┌───────┐  ws/http  ┌──────┐  │
│   │ Phone │━━━━━━━━━━━│Server│  │
│   │ stall │  direct   │  ok  │  │
│   └───────┘   18 ms   └──┬───┘  │
│                          │stdio │
│   ┌───────┐           ┌──┴───┐  │
│   │  Mac  │           │Claude│  │
│   │cpu 41%│           │ 6 up │  │
│   └───────┘           └──┬───┘  │
│                          │api   │
│   ┌───────┐           ┌──┴───┐  │
│   │  Obs  │           │Bedrock│ │
│   │lag 2s │           │ 1.2s │  │
│   └───────┘           └──────┘  │
│                                 │
│ ── Story at 14:02 ───────────── │
│ Conversation rebuilt 12× in 10s │
│ with no new data · CPU 80%      │
│                                 │
│ ▁▂▁▁▅█▂▁▁▁🐞▁▁▁▁▂▁▁▁▁▁▁▁▁▁▁▁│▲  │
│ 12:00        13:00       14:00  │
└─────────────────────────────────┘
```

- **Boxes:**
  - **Phone**: this app.
  - **Server**: mclaude-server.
  - **Claude**: all Claude processes. The count running is shown, and the box expands per session.
  - **Bedrock**: the model API Claude calls.
  - **Mac**: the host.
  - **Obs**: the observability system itself.

  Each box shows a one-word state ("ok", "stall", "down", "no data") and one key number at the selected moment. The state word is always shown, so the meaning never depends on color alone. Colors: green ok, amber degraded, red failing, hatched grey **no data**. Missing data is never drawn as healthy.
- **Lines:** Phone–Server (HTTP and WebSocket; label: Tailscale path and round-trip time), Server–Claude (stdio), Claude–Bedrock (model latency, time to first token and errors, from Claude Code's model request spans). Colored like the boxes.
- **State rules at the selected moment:**
  - **Phone:** red on a crash or a stall of 1 s or more within ±30 s. Amber on a stall of 250 ms or more, or a broken rule.
  - **Server:** red if not answering health checks. Amber if any request took over 5 s, not counting Claude turn time.
  - **Claude:** red if a process is restarting. Amber on API errors.
  - **Phone–Server line:** red while the WebSocket is disconnected in the foreground. Amber when the path is relayed or the round trip is over 300 ms.
  - **Mac:** amber when CPU is over 85% or memory pressure is high.
  - **Obs:** amber when ingest lag is over 30 s, sampling is active, or any part of the telemetry pipeline is unhealthy (export queues overflowing, outbox drops, data refused).
- **Story strip:** the story lines for the selected moment (±1 minute), newest first. It scrolls.
- **Scrubber:** a density bar of activity over the last 24 hours. Drag it to pick a moment and every box, line and story line updates. **● Live** returns to now; while live, the map updates every 2 s. Pinch the bar to zoom from 24 h down to 1 minute.
- **Pins:** 🐞 for a report, ▲ for an automatic finding. Tapping a pin opens the **finding sheet**: title, status, screenshot (if any), the Ops summary and proof, and **Open in Ops session**, which switches to the Ops session.
- **Tapping a box or line** opens a detail sheet with its key values at the moment, its story lines for ±2 minutes, and a list of actions in progress, each with **Follow**. Follow shows that action's path across layers as a vertical list of steps with times.
- **States:** a spinner while loading. When the obs service can't be reached: "Can't reach observability (obs.mclaude.internal)" with Retry. The phone keeps recording into its outbox meanwhile.

### Ops session

- **One open managed session named "Ops"**, working in the mclaude-v1 repository. The obs service stores its session ID. If it's ended, the obs service creates a new one with the same name when the next finding arrives.
- **Dispatch:** each new finding is sent to the Ops session as a message through mclaude-server's input API: "Investigate finding F-<n>: <one line>", plus how to investigate. The server's existing queue handles messages that arrive while Ops is busy.
- **Dedupe and limits:**
  - A detection with the same detector and signature within 1 hour is added to the open finding instead of opening a new one.
  - At most 10 findings are dispatched per hour. Beyond that, findings are recorded and shown on the map, and one message says "N more findings waiting".
  - Manual reports are always dispatched.
- **How Ops investigates:**
  1. Open the finding and its report.
  2. Read the story around it.
  3. Inspect the moment across layers.
  4. Follow the actions involved.
  5. Form a hypothesis, and check that every layer agrees with it. A layer that disagrees means the hypothesis is wrong or a signal is missing. Follow the investigation rules: no proof from time alone, and absence counts only when the pipeline was healthy.
  6. Find the code pointer.
  7. Build a reproduction. Either replay the recording in the simulator against a throwaway test server on port 8399, or write a test that feeds in the recorded inputs.
  8. Compare the reproduction's recording with the original.
  9. Write the proof (root cause, code pointer, reasoning, reproduction and how it was compared) into the finding.
- **Proposed fix:** through `/feature-change`, in an isolated git worktree, never in the shared working tree. Validated in the simulator. It includes the signal, rule or detector that would have caught the bug. Ops then marks the finding "fix proposed" and sends the push. It never ships on its own. When Richard approves in the Ops session, shipping follows the normal `/feature-change` lane, including coordinating with other sessions.
- **When the evidence can't explain the bug:** the finding becomes "needs signal". Ops proposes the instrumentation change, with the same autonomy, and the finding stays open until the next occurrence is captured.
- **Never** test against the live server (port 8377) or change the user's working tree without approval.

### Observability service

- **Report upload:** `POST /reports` (multipart):
  - `meta`: JSON with `reportId`, `time`, `installId`, `build`, `sessionId?`, `screen`, `note?`, `snapshot` (the full app state), `screenStructure`, `trigger` (`shake`, `button` or `rule:<name>`)
  - `screenshot`: a PNG

  The reply is `{"findingId":"F-123"}`. Upload is idempotent on `reportId`.
- **Story builder:** every minute it writes:
  - per-component minute summaries: counts, p50/p95/max latencies, stalls, rebuilds, CPU and memory, errors, bytes
  - **story lines**: rule-based sentences for notable moments, each linked to the records behind it. Examples:
    - "Conversation rebuilt 12× in 10 s with no new data"
    - "Placeholder for 'ok' waiting 3 min; server routed its text as a question answer"
    - "Phone WebSocket reconnecting; path relayed, 420 ms"
- **Detectors:** each one opens a finding.
  - **Crash:** an app crash diagnostic, or an unexpected exit of the server or a Claude process.
  - **Broken app rule.**
  - **Stall:** a main-thread stall of 500 ms or more on the phone, or a server request over 5 s, not counting Claude turn time.
  - **Layer disagreement:**
    - Every user send must reach a final outcome in every layer within 2 minutes. In the app, the placeholder is removed or marked failed. On the server, the text is delivered as a message, used as an answer or feedback, or queued. When the layers' outcomes don't match, for example the app is still waiting for an echo the server will never send, it's a disagreement.
    - The phone shows the WebSocket as connected but the server has no matching client, or the reverse, for over 30 s.
    - "Rebuild without cause": a screen rebuilt 5 or more times within 10 s where the trigger is `none`, or where the triggering state changes changed nothing.
    - Any failed agreement check (see How data points are correlated).
    - The phone's clock offset jumped by more than 1 s, which means the timeline alignment is suspect.
- **Volume guard:**
  - Each source has a daily byte budget: phone 500 MB, server 1 GB, Claude processes 1 GB, host 100 MB.
  - Past the budget, high-frequency kinds (rebuilds, frame hitches, per-second samples) are sampled at 10%, and a "sampling active" record is written. Inputs, decisions and state changes are never sampled.
  - The Obs box on the map shows sampling and ClickHouse disk use.
- **Heartbeats and dead man's switch:**
  - Every source sends a heartbeat metric every 30 s: the app while in the foreground, mclaude-server, the Mac host agent, the collector, ClickHouse (through the collector) and the obs service itself. The app also records going to the background and coming back, so its silence in the background is expected, not an outage.
  - The obs service marks a source **silent** after 2 missed heartbeats while it's expected to be active. A silent source is drawn as **no data** on the map, and its silence is a finding once it has lasted 5 minutes. When a silent phone comes back and its outbox delivers, the records fill the gap and the finding is updated with what really happened. For example, "off the tailnet 14:02–14:20", or "network partition: phone was sending, collector unreachable".
  - **The switch outside the system:** the obs stack can't report its own death, so mclaude-server checks the obs service's health every minute. After 5 minutes without an answer, it sends a push notification directly: "Observability is down since 14:02". It sends one more when the stack is back.
- **Self-observability:** the obs service records ingest lag, dropped records and detector run times. A source that stops sending is shown as **no data** on the map, never as healthy.
- **Push:** when Ops marks a finding "fix proposed" or "needs you", the obs service calls mclaude-server's new owner-only `POST /push/notify` with `{title, body, sessionId}`. The server delivers it with its existing APNs sender, and tapping the notification opens that session.

### MCP server (for Claude)

Served by the obs service over streamable HTTP at `obs.mclaude.internal:4320/mcp`. It's registered as a user-level MCP server, so every Claude session on the Mac can use it, not only Ops. Times are ISO 8601 in the Mac's clock. Results are JSON, capped at 2,000 rows or 200 KB, with a `truncated` flag.

| Tool | Input | Returns |
|---|---|---|
| `findings_list` | `status?`, `since?` | findings: id, title, status, detector, first and last seen, count |
| `finding_get` | `id` | the finding, its report (note, snapshot, screen structure, screenshot file path), linked records, proof fields |
| `finding_update` | `id`, `status` (`investigating`, `needs signal`, `fix proposed`, `needs you`, `resolved`, `not a bug`), `rootCause?`, `codePointer?`, `reasoning?`, `reproduction?`, `proposedFix?`, `question?` | the updated finding. A status change to `fix proposed` or `needs you` sends the push. |
| `story` | `from`, `to`, `components?` | story lines and minute summaries |
| `moment` | `time`, `windowSeconds` (default 10) | the entity graph at that time, and each entity's state: app state (last snapshot plus changes), screen structure, CPU and memory with attribution, network in flight, server state, Claude processes. Plus all records in the window and how they join |
| `attribute` | `entity`, `from`, `to` | CPU and memory for the entity over the range, split into attributed (per span), shared and unattributed |
| `follow` | `traceId` | every span and record in that trace across all layers, including Claude Code's own spans and events and the tool subprocesses' host metrics, in time order |
| `compare` | `recordingA`, `recordingB` (finding IDs, replay IDs or time ranges with an install ID) | the two recordings aligned by input sequence, the first divergence (input, state or screen), and every later difference |
| `replay_export` | `findingId` or (`installId`, `from`, `to`) | the path of a recording file for the app's replay mode |
| `pipeline_health` | `source?`, `from`, `to` | for each source, its telemetry pipeline over the range: export failures and drops, outbox size and drops, collector queue and refusals, outages |
| `query` | `sql` | read-only results (SELECT only; 30 s timeout) over the raw tables and the derived index |

## Data model

- **Signals:** standard OTel traces, logs and metrics in ClickHouse's standard OTel tables.
  - Inputs and decisions are log records inside their span, with `event.name` set to `mclaude.input.<type>` or `mclaude.decision.<type>`. Rule breaks use `mclaude.rule.broken`; stalls and crashes are log records too, so they keep their context.
  - State changes, rebuilds and screen records are spans named `mclaude.state.<store>`, `mclaude.rebuild.<screen>` and `mclaude.screen.<screen>`, with span links as described in How data points are correlated.
  - Measured samples (CPU, memory, temperature, battery, host metrics, pipeline health) are metrics, cumulative where they're counts.
- **Common attributes:**
  - `service.name`: `mclaude-ios`, `mclaude-server`, `claude-code`, `mac-host` or `obs`
  - `mclaude.install_id`, `mclaude.build`, `mclaude.session_id` (when there is one)
  - `mclaude.replay_of` (on replay telemetry)
  - entity identity in the resource, per the entity table in How data points are correlated
  - `mclaude.time_uncertainty_ms`: on phone records
  - `mclaude.state.version` and `mclaude.state.changed`: on state-change spans
  - `mclaude.link.type` (`trigger` or `shows`): on span links from rebuilds and screen records
- **Traces:** one trace per user action, started by the app. Context travels as the W3C `traceparent` header on HTTP and as a `traceparent` field on WebSocket messages in both directions. Log records use OTel's own trace and span ID fields, so no custom ID attribute is needed.
- **Obs tables:**
  - `findings`: id, title, status, detector, signature, first and last seen, count, report id, proof fields, ops message time
  - `reports`: report id, finding id, meta JSON, screenshot path
  - `summaries_minute`: component, minute, metrics JSON
  - `story_lines`: time, component, text, linked trace and span IDs
  - `clock_offsets`: install id, time, offset in ms, round trip in ms
  - `entities`: entity id, kind, attributes (pid, session id, install id, …), start, end
  - `entity_links`: parent entity, child entity, kind, start, end
  - `attributions`: metric sample (name, resource, timestamp), span (trace and span ID), share
  - `malformed`: the raw record, and why it was rejected
- **Retention:** raw OTel tables 30 days. Attributions follow their raw records' 30 days. Other obs tables and screenshots are kept forever.

## Error handling

- **The Mac is unreachable from the phone** (off the tailnet, Mac asleep, stack down): the phone fills its outbox and sends it later, with original timestamps. Reports are saved and sent later. The map shows the error state.
- **Collector up, ClickHouse down:** the collector's persistent queue holds data until ClickHouse returns.
- **The server can't reach the collector:** an in-memory buffer of 10,000 records, then oldest-first drops with a dropped-count record.
- **A Claude Code update changes its telemetry** (span names, the input `parent` field, attributes): the Claude trace join check fires, and the obs service falls back to joining by `session.id`, `prompt.id` and the turn's time window. Joins made that way are marked as fallback joins in tool results.
- **Clock skew:** phone timestamps are corrected by the latest offset. A large jump fires the clock detector.
- **Instrumentation errors:** never change app or server behavior. They're counted and visible on the Obs box.
- **The Ops session is down or busy:** messages wait in the server's queue. If the session was ended, it's recreated.
- **Push fails:** the finding still shows on the map with the drawer dot.
- **Full content:** stays inside the tailnet. ClickHouse is bound to localhost only. The collector and obs ports are bound only to the Tailscale IP and localhost.

## Worked examples

**Typing lag (fixed in build 120).** Placeholders were hidden but never removed, and a 5 s timer reassigned the list, rebuilding the whole conversation for a 650k-token session.

- **Signals, with their links:**
  - Every 5 s, a rebuild span of the conversation screen has a trigger link to a placeholder state change.
  - That state change is a child of the 5 s timer's span and has `changed=false`.
  - Each rebuild span measured hundreds of milliseconds of main-thread CPU, and the stalls while typing are attributed to those spans.
  - No inbound frame spans exist in those windows, and the pipeline was healthy, so the absence is real.
- **Detectors:** "rebuild without cause" and the stall detector fire.
- **Reproduction:** replay with that session's recorded events in the simulator.

**Stuck placeholders after typed answers (fixed in build 121).** While a question, plan or approval was pending, typed text was used as the answer, so no user message ever came back. The placeholder waited until the 1-hour cleanup.

- **Signals:** the server's decision record ("text routed as question answer", in the send's trace) sits next to the app's "placeholder waiting" state.
- **Detectors:** the send-outcome disagreement detector fires after 2 minutes. The rule check "placeholder older than 2 minutes" captures a report with the stuck bubbles on screen.
- **Reproduction:** the same send replayed against a test server with a pending question.

**Composer still shows the sent message (seen twice on 2026-11-08, from session s49).** After tapping ↑, the message is sent ("Sent ✓"), but the composer still shows its text while the send arrow is grey. The arrow only goes grey when the model's text is empty, so the model holds "" while the UIKit text view still displays the old text, and the next keystroke would bring it back into the draft. Screenshots can't tell apart the two hypotheses:

- **A:** the text view held marked (still-composing) text at the send, and SwiftUI didn't push the cleared text into UIKit while composition was active.
- **B:** after the clear, the keyboard committed text back into the view (autocorrect or prediction), and the follow-up re-clear set the model from "" to "", which SwiftUI saw as no change. UIKit kept the re-inserted text.

It uses no special mechanism, only the five kinds of data on one text field:
- **Input:** the tap on ↑, and any keyboard commits (view-side writes from the keyboard).
- **State:** the model's text going from the message to "", then the re-clear's "" → "", which bumps the version but changes nothing.
- **Decisions:** "clear after send", and "re-clear because the text still equals the message".
- **Screen:** what the text view actually shows.
- **Rule:** screen and state disagree. It fires on its own at the 0.1 s check, with no screenshot or noticing needed, so an intermittent bug is caught every time.

All of it sits in the send's trace: the tap starts the trace, and the clear and re-clear are its children. A keyboard commit is its own input, linked to the same text view by entity and by the state version it overwrote.

The two hypotheses leave different evidence:
- **A** shows marked text at the send, and no keyboard write afterwards.
- **B** shows a keyboard write after the clear, then the re-clear, all before the 0.1 s check.

**Reproduction:** replay the recorded keystrokes, keyboard and send timing in the simulator.

## Scope

**In:**
- the observability stack, the Mac host agent, and server and Claude-process instrumentation
- iOS instrumentation with the outbox, rule checks, reports (shake and bug button) and replay mode
- the obs service: reports, story, detectors, Ops dispatch, push, MCP server
- the live system map
- the Ops session workflow

**Out (separate features):**
- SLOs, error budgets and burn-rate alerting, to be built around the key user stories
- the k8s (v2) components and the web SPA
- the Watch app
- multiple users
