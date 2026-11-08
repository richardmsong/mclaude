# mclaude-obs

MClaude's observability stack. The design is in `docs/plan-observability.md`.

## Stack

Docker Compose project `mclaude-obs`:

| Service | What | Ports |
|---|---|---|
| `clickhouse` | ClickHouse 25.8. Database `otel`, user and password `otel`, standard OTel tables, 30-day TTL | 8123, 9000 (localhost only) |
| `otel-collector` | OTel Collector contrib 0.152.1. OTLP in, ClickHouse out, persistent queue | 4317 gRPC, 4318 HTTP (all interfaces) |
| `obs-api` | Read-only JSON API for the iOS system map, plus the Ops dispatcher and findings API (`api/server.py`) | 4320 (all interfaces) |

- Start or update: `./up.sh`. This rebuilds `obs-api` when `api/` changed. Use `./up.sh obs-api` to touch only the API. It first runs `./write-server-token.sh` (see Ops dispatcher).
- Stop: `docker compose down`. This keeps the data volumes; never add `-v` unless you mean to delete the data.
- Logs: `docker compose logs -f obs-api` (or `otel-collector`, `clickhouse`).
- Query: `./query.sh "SELECT ... FROM otel_logs ..."` (read-only).

## Obs API

Python 3.12, standard library only. It runs fixed SQL against ClickHouse (`http://clickhouse:8123`), and request values reach ClickHouse only as typed query parameters. Each request has a 2 s budget covering all its queries, and returns 504 when it runs out. ClickHouse unreachable gives 503, bad parameters give 400, and an unknown path gives 404.

```sh
H=http://home-server.taild44daa.ts.net:4320
curl "$H/api/health"                       # {"ok":true,"ingest_lag_s":4.0}
curl "$H/api/map"                          # now, 60 s window; ?at=<unix>&window=<s>
curl "$H/api/timeline?bins=96"             # last 24 h; ?from=<unix>&to=<unix>
curl "$H/api/finding?trace_id=<32 hex>"    # one rule break plus its screenshot path
```

Node and edge states follow the "State rules" table in the plan. Notes on how they're implemented:

- **Precedence.** Failing beats degraded, and degraded beats no data. A rule break or a stall still shows when the vitals are missing, and so does a 5xx when the heartbeat is missing.
- **Presence.** Heartbeats and current values (phone vitals, the server `up` beat, the Mac metrics) look back `max(window, 45 s)`, so a short window doesn't flap to "no data" between beats. Event-based rules use the window as given.
- **Server, slow requests.** These routes are excluded from the 5 s rule: `/input`, `/answer` and `/approve` (user input), `/ws`, and the uploads `/screenshots`, `/videos` and `/files`.
- **Bedrock.** Model requests come from `claude_code.llm_request` spans, or from the `claude_code.api_request` events when there are no spans. Model errors are errored spans plus `claude_code.api_error` events. With no requests in the window, the state is `ok` with value `idle`. When Claude itself has no data, the state is `nodata`.
- **Edges that aren't in the table.**
  - server–claude: `nodata` if the server has no data, and `degraded` if a `claude.turn` span failed.
  - claude–bedrock: mirrors the Bedrock node.
- **Mac.** `cpu_pct` is 1 − mean idle utilization across cores. Memory is the `used` share of `system.memory.utilization`. Per-process CPU is a percentage of the whole Mac, across all cores.
- **Obs.** Ingest lag is `at` minus the newest record of any service in the last 24 h, at or before `at`.
- **Timeline.** Each bin's state is simplified and computed from aggregates:
  - failing: a rule break, a stall of 1 s or more, a process restart, a model error, a server 5xx, or a server heartbeat gap (heartbeats were seen earlier in the range and other services are reporting)
  - degraded: a stall of 250 ms or more
  - no data: nothing from any known service in the bin
  - It ignores latency, the WebSocket state, Mac load and ingest lag.
- **Pins** are `mclaude.rule.broken` records.

Service names default to `mclaude-ios`, `mclaude-server`, `claude-code` and `mac-host`. They can be overridden with comma-separated lists in `OBS_PHONE_SERVICES`, `OBS_SERVER_SERVICES`, `OBS_CLAUDE_SERVICES` and `OBS_MAC_SERVICES`, for example to point a throwaway instance at synthetic test services:

```sh
docker run --rm -d --name obs-api-test --network mclaude-obs_default -p 127.0.0.1:4321:4320 \
  -e OBS_PHONE_SERVICES=obs-api-test-phone -e OBS_SERVER_SERVICES=obs-api-test-server \
  -e OBS_CLAUDE_SERVICES=obs-api-test-claude -e OBS_MAC_SERVICES=obs-api-test-mac mclaude-obs-api:local
```

## Ops dispatcher

The Ops session (plan → "Ops session (phase 3)") runs inside `obs-api`: a background thread turns trigger records into findings and delivers them to the MClaude session named "Ops", and the findings API lets Ops record its proof.

**It's off by default.** It needs a live mclaude-server with `"automated": true` input and `POST /push/notify`. Until the live server runs that build, the server would ignore `automated` and could take a finding as the answer to a pending prompt. While off, obs-api makes **no calls to mclaude-server at all**: no scans, no session lookup, no delivery, no push. The findings API still answers.

To turn it on after the live server restart, create `mclaude-obs/.env` (Compose reads it) and recreate the API:

```sh
echo OPS_DISPATCH_ENABLED=true > ~/work/mclaude-v1/mclaude-obs/.env
~/work/mclaude-v1/mclaude-obs/up.sh obs-api
docker compose logs obs-api | grep '\[ops\]'    # "dispatcher on: server https://…:8377 …"
```

| Variable | Default | What |
|---|---|---|
| `OPS_DISPATCH_ENABLED` | `false` | `true` starts the dispatcher and allows pushes. Set in `.env`; the compose file passes it through. |
| `OPS_SERVER_URL` | `MCLAUDE_SERVER_URL`, else `https://home-server.taild44daa.ts.net:8377` | mclaude-server base URL. A test instance points this at a :8399 server. |
| `OPS_SESSION_ID` | — | Pin the session to deliver to. Skips the name lookup and never creates one. |
| `OPS_SESSION_NAME`, `OPS_SESSION_CWD` | `Ops`, `/Users/rsong/work/mclaude-v1` | The session looked up by name, and the folder one is created in. |
| `OPS_CREATE_SESSION` | `true` | `false` never creates a session; delivery waits until one named "Ops" exists. |
| `OPS_MAX_DELIVERIES_PER_HOUR` | `10` | Rate limit. |
| `OPS_SCAN_INTERVAL_S` | `30` | Scan interval. |
| `OPS_LATE_WINDOW_S` | `86400` | How far behind the watermark each scan re-reads, for records that arrive late. |
| `OPS_DB`, `OPS_TOKEN_FILE`, `OPS_SERVER_TOKEN_FILE` | `/data/findings.db`, `/secrets/ops-token`, `/secrets/server-token` | Store and secrets. |

**Secrets.** `~/.mclaude/obs` is mounted read-only at `/secrets`.
- `ops-token` (random, mode 600) authorizes `POST /api/findings/<id>`.
- `server-token` is the mclaude-server owner token: the key in `~/mclaude-session-tokens.json` whose value is `ownerUserId` in `~/mclaude-config.json`. `./write-server-token.sh` writes it once, mode 600, without printing it. `--force` rewrites it, e.g. after the token changes.
- The container runs as `nobody`. That works because OrbStack presents bind-mounted files as owned by whichever uid reads them, so mode 600 files are readable.

**Store.** SQLite at `/data/findings.db`, in the named volume `mclaude-obs_obs-api-data`. `/data` is owned by `nobody` in the image, so a new volume gets that ownership. Tables: `findings`, `seen` (processed record keys), `deliveries` (for the rate limit) and `meta` (watermark, Ops session id, last waiting card).

**Triggers.** Live data only (`LIVE_COND`: no simulator, server port 8377 or none), and never a record whose `mclaude.session_id` (record attribute, else resource attribute) is the Ops session's.

| Kind | Records (service) |
|---|---|
| `rule` | `mclaude.rule.broken` (phone); the finding's rule is `LogAttributes['rule']` |
| `stall` | `mclaude.app.stall` with `duration_ms` ≥ 1000 (phone) |
| `crash` | `mclaude.app.exception`, `mclaude.app.crash`, `mclaude.app.unclean_exit` (phone); `mclaude.process.exited` with `mclaude.expected` = `false`, and `mclaude.process.restart_scheduled` (server); server spans with `SpanKind = 'Server'` and `http.response.status_code` ≥ 500 (server) |
| `model` | `claude_code.api_error` events, with the name taken from `EventName` or `LogAttributes['event.name']` like the map does; `claude_code.llm_request` spans that errored (`StatusCode = 'Error'`, `success = 'false'`, an `error`, or `status_code` ≥ 400) |

**How a scan works.**
- Every 30 s it reads trigger records from `watermark − OPS_LATE_WINDOW_S` (never before the dispatcher's first run) up to now, skips records already in `seen`, and moves the watermark to now. A crash record is sent at the app's next launch with its original timestamp, so a plain watermark would skip it. A restart neither repeats nor skips records.
- The first run starts at that moment and doesn't backfill.
- Signature: `<kind>|<rule or record name>|<session id>`. A record matching a finding opened in the last hour adds to it (count, last seen, trace IDs). Otherwise it opens `F-<n>` with status `new`.
- New findings are delivered right away, at most 10 per rolling hour. Past that they're `held` (recorded, not sent). One plain-text "N more findings waiting" message goes out at most once an hour, only when something new was held. A finding that can't be delivered (server down) is retried each scan for an hour, then held.
- Delivery is `POST /sessions/<ops>/input` with `{"text": <card>, "automated": true}`. The card is the spec's two lines. `rule=` is used for rule breaks and `name=` for the other kinds, as the app's card parser reads them. `trace=` is included only for rule breaks, because the card's Open button loads `/api/finding`, which serves rule breaks.
- The Ops session is looked up by name in `GET /sessions` at delivery time. If there's none, it's created with `POST /sessions {"cwd", "name": "Ops"}` and then renamed with `PATCH /sessions/<id> {"name": "Ops"}`. Its id is cached, and refreshed every 5 min for the self-exclusion. A 404 on input looks it up again.

**Findings API.**

```sh
curl -s "http://127.0.0.1:4320/api/findings?status=new"     # newest first; &limit=<n, default 100>
curl -s http://127.0.0.1:4320/api/findings/F-12               # one finding: trace_ids, first/last seen, proof fields
curl -s -X POST http://127.0.0.1:4320/api/findings/F-12 -H "X-Ops-Token: $(cat ~/.mclaude/obs/ops-token)" \
  -H 'Content-Type: application/json' -d '{"status":"root_cause","summary":"…","root_cause":"…"}'
```

- POST takes any subset of `status`, `summary`, `root_cause`, `code_pointer`, `reasoning`, `reproduction`, `proposed_fix` and `question`, as strings.
- A missing or wrong token gives 403, an unknown field or status gives 400, and an unknown id gives 404.
- When the status changes to `root_cause`, `fix_proposed` or `needs_you`, it calls the server's `POST /push/notify` with `{"title": "Ops · F-12 <rule, or the kind>", "body": <the question for needs_you, else the summary>, "sessionId": <ops id>}`. The response's `push` field says `sent`, `failed: …` or `skipped: OPS_DISPATCH_ENABLED is off`.

**Testing against a throwaway server.** Never point a dispatch-enabled instance at :8377 for a test. Start a :8399 test server (`/deploy-server` step 2), give an existing test session the name "Ops" (`PATCH /sessions/<id>`; don't create sessions, because that writes the shared owners file), then:

```sh
docker run -d --name obs-api-opstest --network mclaude-obs_default -p 127.0.0.1:4322:4320 \
  -v obs-api-opstest-data:/data -v $HOME/.mclaude/obs:/secrets:ro -e CLICKHOUSE_URL=http://clickhouse:8123 \
  -e OPS_DISPATCH_ENABLED=true -e OPS_SERVER_URL=https://home-server.taild44daa.ts.net:8399 \
  -e OPS_CREATE_SESSION=false -e OPS_MAX_DELIVERIES_PER_HOUR=2 -e OPS_SCAN_INTERVAL_S=10 mclaude-obs-api:local
```

Trigger it with synthetic OTLP logs posted to `http://127.0.0.1:4318/v1/logs`, using `service.name=mclaude-ios`, `mclaude.simulator=false`, an obviously fake rule (`ops_test_rule`), and a body that says it's test data. Afterwards, `docker rm -f obs-api-opstest && docker volume rm obs-api-opstest-data`, and stop the test server.

## Mac host agent

The native OTel Collector contrib 0.152.1 (the official darwin arm64 release, SHA-256 checked against the release's checksums file) runs as the launchd agent `com.mclaude.otel-host`. Every 10 s it reports these metrics as `service.name=mac-host` to the collector on `127.0.0.1:4318`:

- host CPU utilization, memory usage and utilization, and load
- per-process CPU and memory for `mclaude-server` and the Claude Code processes

Claude Code's native binary is named after its version (e.g. `2.1.317`), so it's matched by a version-number regex. Both kinds of process are tagged with `mclaude.process` (`mclaude-server` or `claude`).

- Binary and config: `~/.mclaude/otel-host/otelcol-contrib`, `~/.mclaude/otel-host/config.yaml`
- Plist: `~/Library/LaunchAgents/com.mclaude.otel-host.plist` (`KeepAlive`, `RunAtLoad`)
- Start: `launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.mclaude.otel-host.plist`
- Stop: `launchctl bootout gui/$(id -u)/com.mclaude.otel-host`
- Restart (e.g. after a config change): `launchctl kickstart -k gui/$(id -u)/com.mclaude.otel-host`
- Status: `launchctl print gui/$(id -u)/com.mclaude.otel-host | grep -E 'state|pid'`
- Logs: `/tmp/mclaude-otel-host.log`
- Check the config: `~/.mclaude/otel-host/otelcol-contrib validate --config=$HOME/.mclaude/otel-host/config.yaml`
- Check the data: `./query.sh "SELECT MetricName, count(), max(TimeUnix) FROM otel_metrics_gauge WHERE ServiceName='mac-host' GROUP BY 1"`

Don't set `ProcessType` to `Background` in the plist. With it, launchd throttled the collector so hard that it never finished starting.
