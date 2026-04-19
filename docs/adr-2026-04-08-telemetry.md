# Telemetry Proposal: OpenTelemetry + LGTM Stack

**Status**: accepted
**Status history**:
- 2026-04-08: accepted


## Current State

MClaude has a minimal telemetry endpoint (`POST /telemetry`) on the server. The iOS app queues errors (e.g. WebSocket disconnects) and retries until the server acknowledges. Errors are written to the server log as `[TELEMETRY]` lines.

Limitations:
- No structured storage or querying — grep through log files
- No metrics (latency, poll duration, WS message rate, ANSI parse time)
- No traces (request lifecycle across client -> server -> tmux)
- No dashboards or alerting
- Client-side telemetry limited to errors — no performance data

## Proposed Architecture

```
iOS App                    mclaude-server              LGTM Stack
--------                   ---------------              ----------
OTel SDK  ---(OTLP/HTTP)--> OTel Collector  ---> Loki    (logs)
                             (sidecar)      ---> Grafana  (dashboards)
                                            ---> Tempo   (traces)
                                            ---> Mimir   (metrics)
```

### Components

**OpenTelemetry Collector** — runs as a sidecar alongside mclaude-server (or as a separate launchd service). Receives OTLP from both client and server, exports to LGTM backends.

**Loki** — log aggregation. Replaces grepping `/tmp/mclaude-server.log`. Structured labels: `source=client|server`, `session_id`, `severity`.

**Tempo** — distributed traces. A single request from "user taps Approve" traces through: iOS app -> HTTP POST -> mclaude-server -> tmux send-keys -> pane capture -> WS broadcast -> iOS render. Useful for diagnosing latency.

**Mimir** — metrics. Time-series data for dashboards and alerting.

**Grafana** — dashboards and alerting. Accessible at `home-server:3000`.

### Instrumentation Plan

#### Server-side (Swift / Hummingbird)

Use [swift-otel](https://github.com/slashmo/swift-otel) for native OpenTelemetry support.

Metrics to emit:
- `mclaude.poll.duration_ms` — how long each tmux poll cycle takes
- `mclaude.poll.sessions_count` — number of active sessions per poll
- `mclaude.ws.clients` — gauge of connected WebSocket clients
- `mclaude.ws.broadcast_bytes` — bytes sent per broadcast
- `mclaude.ws.broadcast_duration_ms` — time to fan out a message
- `mclaude.tmux.capture_duration_ms` — per-pane capture latency
- `mclaude.http.request_duration_ms` — per-endpoint latency (histogram)

Traces:
- Span per HTTP request (automatic with Hummingbird OTel middleware)
- Span per poll cycle, child spans per tmux capture
- Span per WS broadcast

Logs:
- Status changes: `[session_id] idle -> working`
- WS connect/disconnect events
- Errors with full context

#### Client-side (iOS / Swift)

Use [opentelemetry-swift](https://github.com/open-telemetry/opentelemetry-swift). Export via OTLP/HTTP to the collector.

Metrics to emit:
- `mclaude.app.ansi_parse_duration_ms` — ANSI parsing time per update
- `mclaude.app.ws_latency_ms` — time from WS message received to UI update
- `mclaude.app.ws_reconnect_count` — reconnect frequency
- `mclaude.app.action_duration_ms` — time from tap to server response (send, approve, cancel)

Traces:
- Span per user action (send input, approve, cancel, voice send, screenshot upload)
- Span per WS reconnect cycle

Logs:
- All current telemetry events (WS disconnects, action failures)
- App lifecycle events (foreground/background)

### Deployment

All LGTM components run on the home server via Docker Compose:

```yaml
services:
  otel-collector:
    image: otel/opentelemetry-collector-contrib
    ports:
      - "4318:4318"  # OTLP HTTP
    volumes:
      - ./otel-config.yaml:/etc/otel/config.yaml

  loki:
    image: grafana/loki:latest
    ports:
      - "3100:3100"

  tempo:
    image: grafana/tempo:latest
    ports:
      - "3200:3200"

  mimir:
    image: grafana/mimir:latest
    ports:
      - "9009:9009"

  grafana:
    image: grafana/grafana:latest
    ports:
      - "3000:3000"
    environment:
      - GF_AUTH_ANONYMOUS_ENABLED=true
```

### Migration Path

1. Deploy LGTM stack via Docker Compose on the home server
2. Add OTel collector config pointing to Loki/Tempo/Mimir
3. Instrument mclaude-server with swift-otel (metrics + traces)
4. Add opentelemetry-swift to the iOS app (metrics + traces)
5. Replace the current `POST /telemetry` endpoint — client sends OTLP directly to the collector
6. Build Grafana dashboards: server health, session activity, client performance
7. Add alerting: server down, high reconnect rate, poll latency spikes

### Estimated Effort

- LGTM stack setup: ~1 session (Docker Compose + config)
- Server instrumentation: ~1 session (swift-otel + middleware)
- Client instrumentation: ~1 session (opentelemetry-swift + spans)
- Dashboards: ~1 session (Grafana JSON models)

### Open Questions

- Should the OTel collector run as a launchd service or Docker container?
- Retention policy for logs/traces/metrics? (Home server storage is finite)
- Should the iOS app buffer telemetry when offline and flush on reconnect, or drop it?
- Do we need Mimir, or is Prometheus sufficient for this scale?
