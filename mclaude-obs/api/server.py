"""MClaude Obs API: read-only JSON over the observability store (docs/plan-observability.md, "Obs API").

Standard library only. Every endpoint runs fixed SQL against ClickHouse over HTTP; request values
reach ClickHouse only as typed query parameters ({name:Type}), never inside the SQL text.

    GET /api/map?at=<unix>&window=<s, default 60>
    GET /api/timeline?from=<unix>&to=<unix>&bins=<n, default 96>
    GET /api/finding?trace_id=<32 hex>
    GET /api/health

Ops session (phase 3): a dispatcher thread turns trigger records into findings and delivers them to
the Ops session, and the Ops API reads and updates them (SQLite, not ClickHouse):

    GET  /api/findings?status=<s>&limit=<n, default 100>
    GET  /api/findings/<F-n>
    POST /api/findings/<F-n>      (header X-Ops-Token)
"""

import base64
import hashlib
import hmac
import json
import math
import os
import re
import sqlite3
import sys
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

CH_URL = os.environ.get("CLICKHOUSE_URL", "http://clickhouse:8123")
CH_USER = os.environ.get("CLICKHOUSE_USER", "otel")
CH_PASSWORD = os.environ.get("CLICKHOUSE_PASSWORD", "otel")
CH_DB = os.environ.get("CLICKHOUSE_DB", "otel")
PORT = int(os.environ.get("PORT", "4320"))
REQUEST_BUDGET_S = 2.0  # every API request, all its queries included, finishes within this


def _services(var, default):
    return [s.strip() for s in os.environ.get(var, default).split(",") if s.strip()]


# Service names per node. Overridable so a test instance can watch synthetic services only.
PHONE = _services("OBS_PHONE_SERVICES", "mclaude-ios")
SERVER = _services("OBS_SERVER_SERVICES", "mclaude-server")
CLAUDE = _services("OBS_CLAUDE_SERVICES", "claude-code")
MAC = _services("OBS_MAC_SERVICES", "mac-host")
ALL_SERVICES = PHONE + SERVER + CLAUDE + MAC

# Thresholds (State rules table).
STALL_DEGRADED_MS = 250
STALL_FAILING_MS = 1000
PHONE_HTTP_SLOW_MS = 2000
SERVER_SLOW_MS = 5000
# Long generations aren't failures (Richard's long Opus turns run 15-30 s per request), so only a
# median that suggests requests are stuck counts as degraded; errors stay the main signal.
MODEL_SLOW_MS = 120000
MAC_CPU_PCT = 85
MAC_MEM_PCT = 90
LAG_DEGRADED_S = 30
LAG_FAILING_S = 120
# Heartbeats arrive every 10 s (phone vitals, Mac) or 15 s (server). Presence checks and current
# values look back at least this far so a short window doesn't flap to "no data" between beats.
PRESENCE_MIN_S = 45
LIVE_TOLERANCE_S = 15
# Server routes that don't count toward "a request >= 5 s": user input (can wait on Claude's
# process), the long-lived WebSocket, and uploads.
SLOW_EXEMPT_ROUTE_RE = r"(/input|/answer|/approve)$|^/ws$|^/(screenshots|videos|files)$"

STATE_ORDER = {"ok": 0, "nodata": 1, "degraded": 2, "failing": 3}

EV_RULE = "mclaude.rule.broken"
EV_SCREENSHOT = "mclaude.rule.screenshot"
EV_STALL = "mclaude.app.stall"
EV_EXITED = "mclaude.process.exited"
EV_RESTART = "mclaude.process.restart_scheduled"
EV_API_ERROR = "claude_code.api_error"
EV_API_REQUEST = "claude_code.api_request"
MAP_EVENTS = [EV_RULE, EV_STALL, EV_EXITED, EV_RESTART, EV_API_ERROR]

# Claude Code may put its event name in EventName, in LogAttributes['event.name'] (with or without
# the "claude_code." prefix) or only in Body. Normalise to "claude_code.<name>".
EV_EXPR = """multiIf(
    EventName != '', EventName,
    LogAttributes['event.name'] != '' AND position(LogAttributes['event.name'], '.') = 0,
        concat('claude_code.', LogAttributes['event.name']),
    LogAttributes['event.name'] != '', LogAttributes['event.name'],
    startsWith(Body, 'claude_code.'), Body,
    '')"""


def window_cond(col, lo="t0", hi="t1"):
    return f"{col} >= fromUnixTimestamp64Milli({{{lo}:Int64}}) AND {col} <= fromUnixTimestamp64Milli({{{hi}:Int64}})"


# ---------------------------------------------------------------- ClickHouse access


class ApiError(Exception):
    def __init__(self, status, message):
        super().__init__(message)
        self.status = status
        self.message = message


class Deadline:
    def __init__(self, seconds):
        self.end = time.monotonic() + seconds

    def remaining(self):
        left = self.end - time.monotonic()
        if left <= 0.05:
            raise ApiError(504, "query timeout (2 s)")
        return left


def _param_value(v):
    if isinstance(v, (list, tuple)):
        return "[" + ",".join("'" + str(x).replace("\\", "\\\\").replace("'", "\\'") + "'" for x in v) + "]"
    return str(v)


def query(sql, params, deadline):
    """Runs one fixed SELECT. Returns a list of row dicts. A missing table counts as no rows."""
    left = deadline.remaining()
    qs = {
        "database": CH_DB,
        "default_format": "JSON",
        "readonly": "2",
        "max_execution_time": f"{max(left - 0.1, 0.1):.2f}",
        "timeout_overflow_mode": "throw",
        "output_format_json_quote_64bit_integers": "0",
        "output_format_json_quote_denormals": "0",
    }
    for k, v in params.items():
        qs["param_" + k] = _param_value(v)
    req = urllib.request.Request(
        CH_URL + "/?" + urllib.parse.urlencode(qs),
        data=sql.encode(),
        method="POST",
        headers={"Authorization": "Basic " + base64.b64encode(f"{CH_USER}:{CH_PASSWORD}".encode()).decode()},
    )
    try:
        with urllib.request.urlopen(req, timeout=left) as resp:
            return json.loads(resp.read())["data"]
    except urllib.error.HTTPError as e:
        text = e.read().decode(errors="replace")
        if "UNKNOWN_TABLE" in text or "Code: 60" in text:
            return []
        if "TIMEOUT_EXCEEDED" in text or "Code: 159" in text:
            raise ApiError(504, "query timeout (2 s)")
        log(f"clickhouse error {e.code}: {text[:400]}")
        raise ApiError(502, "observability store query failed")
    except (TimeoutError, OSError) as e:
        if isinstance(e, TimeoutError) or "timed out" in str(e):
            raise ApiError(504, "query timeout (2 s)")
        raise ApiError(503, f"observability store unreachable: {e}")


POOL = ThreadPoolExecutor(max_workers=32)


def run_all(jobs, deadline):
    """jobs: {name: (sql, params)} -> {name: rows}, run concurrently under one deadline."""
    futures = {name: POOL.submit(query, sql, params, deadline) for name, (sql, params) in jobs.items()}
    out = {}
    for name, fut in futures.items():
        out[name] = fut.result(timeout=deadline.remaining() + 0.5)
    return out


# ---------------------------------------------------------------- fixed queries

# Latest value of each metric series (gauges and sums alike, so a sender's choice of instrument
# doesn't matter), keyed by the attributes the rules use.
# Only the real system: no simulator records, and only the live mclaude-server (port 8377) and
# the Claude processes it runs. A test server on another port, and its Claude processes, are
# left out. Records without the attribute (the Mac, older builds) count as live.
LIVE_PORT = os.environ.get("OBS_LIVE_SERVER_PORT", "8377")
LIVE_COND = ("ResourceAttributes['mclaude.simulator'] != 'true' AND "
             "ResourceAttributes['mclaude.test_data'] != 'true' AND "
             f"ResourceAttributes['mclaude.server.port'] IN ('', '{LIVE_PORT}')")

Q_METRICS = f"""
SELECT svc, m, proc, state, cpu, role, pid, argMax(v, t) AS v, max(t) AS ts FROM (
    SELECT ServiceName AS svc, MetricName AS m, Attributes['process'] AS proc, Attributes['state'] AS state,
           Attributes['cpu'] AS cpu, ResourceAttributes['mclaude.process'] AS role,
           ResourceAttributes['process.pid'] AS pid, Value AS v, toUnixTimestamp64Milli(TimeUnix) AS t
    FROM otel_metrics_gauge
    WHERE {window_cond('TimeUnix', 'p0')} AND ServiceName IN {{svcs:Array(String)}} AND {LIVE_COND} AND MetricName IN {{metrics:Array(String)}}
    UNION ALL
    SELECT ServiceName, MetricName, Attributes['process'], Attributes['state'], Attributes['cpu'],
           ResourceAttributes['mclaude.process'], ResourceAttributes['process.pid'], Value,
           toUnixTimestamp64Milli(TimeUnix)
    FROM otel_metrics_sum
    WHERE {window_cond('TimeUnix', 'p0')} AND ServiceName IN {{svcs:Array(String)}} AND {LIVE_COND} AND MetricName IN {{metrics:Array(String)}}
)
GROUP BY svc, m, proc, state, cpu, role, pid
"""

MAP_METRICS = [
    "mclaude.app.cpu", "mclaude.app.memory", "mclaude.app.connected",
    "mclaude.server.up", "mclaude.server.ws_clients", "mclaude.server.sessions", "mclaude.server.turns_running",
    "system.cpu.utilization", "system.memory.utilization", "system.memory.usage", "system.cpu.load_average.1m",
    "process.cpu.utilization", "process.memory.usage",
]

Q_EVENTS = f"""
SELECT t, svc, ev, trace_id, attrs FROM (
    SELECT toUnixTimestamp64Milli(Timestamp) AS t, ServiceName AS svc, {EV_EXPR} AS ev,
           TraceId AS trace_id, LogAttributes AS attrs
    FROM otel_logs
    WHERE {window_cond('Timestamp')} AND ServiceName IN {{svcs:Array(String)}} AND {LIVE_COND}
)
WHERE ev IN {{events:Array(String)}}
ORDER BY t DESC
LIMIT 500
"""

# Phone HTTP client spans ("HTTP <method>").
Q_PHONE_HTTP = f"""
SELECT count() AS n,
       quantileExact(0.5)(Duration) / 1e6 AS median_ms,
       max(Duration) / 1e6 AS max_ms,
       countIf(Duration >= {{slow_ns:UInt64}}) AS slow,
       countIf(status >= 400 OR StatusCode = 'Error') AS errors,
       argMax(path, Duration) AS slowest_path,
       anyIf(concat(SpanName, ' ', path, ' -> ', toString(status)), status >= 400 OR StatusCode = 'Error') AS error_example
FROM (
    SELECT Duration, StatusCode, SpanName,
           toInt32OrZero(SpanAttributes['http.response.status_code']) AS status,
           coalesce(nullIf(SpanAttributes['url.template'], ''), nullIf(SpanAttributes['http.route'], ''),
                    nullIf(SpanAttributes['url.path'], ''), '') AS path
    FROM otel_traces
    WHERE {window_cond('Timestamp')} AND ServiceName IN {{svcs:Array(String)}} AND {LIVE_COND} AND startsWith(SpanName, 'HTTP ')
)
"""

# mclaude-server request spans (server kind).
Q_SERVER_REQ = f"""
SELECT count() AS n,
       countIf(status >= 500) AS errors_5xx,
       anyIf(concat(route, ' -> ', toString(status)), status >= 500) AS error_example,
       countIf(Duration >= {{slow_ns:UInt64}} AND NOT match(route, {{exempt:String}})) AS slow,
       argMaxIf(route, Duration, NOT match(route, {{exempt:String}})) AS slowest_route,
       maxIf(Duration, NOT match(route, {{exempt:String}})) / 1e6 AS slowest_ms,
       quantileExact(0.5)(Duration) / 1e6 AS median_ms
FROM (
    SELECT Duration, toInt32OrZero(SpanAttributes['http.response.status_code']) AS status,
           coalesce(nullIf(SpanAttributes['http.route'], ''), SpanName) AS route
    FROM otel_traces
    WHERE {window_cond('Timestamp')} AND ServiceName IN {{svcs:Array(String)}} AND {LIVE_COND} AND SpanKind = 'Server'
)
"""

Q_TURNS = f"""
SELECT count() AS n,
       countIf(StatusCode = 'Error' OR match(lower(SpanAttributes['outcome']), 'error|fail|crash')) AS errors,
       anyIf(SpanAttributes['outcome'], StatusCode = 'Error' OR match(lower(SpanAttributes['outcome']), 'error|fail|crash')) AS error_outcome,
       quantileExact(0.5)(Duration) / 1e6 AS median_ms
FROM otel_traces
WHERE {window_cond('Timestamp')} AND ServiceName IN {{svcs:Array(String)}} AND {LIVE_COND} AND SpanName = 'claude.turn'
"""

# Model requests: Claude Code's llm_request spans and, when traces are off, its api_request /
# api_error log events.
Q_MODEL = f"""
SELECT 'span' AS src, count() AS n, countIf(err) AS errors, quantileExact(0.5)(ms) AS median_ms FROM (
    SELECT Duration / 1e6 AS ms,
           StatusCode = 'Error' OR SpanAttributes['success'] = 'false' OR SpanAttributes['error'] != ''
               OR toInt32OrZero(SpanAttributes['status_code']) >= 400 AS err
    FROM otel_traces
    WHERE {window_cond('Timestamp')} AND ServiceName IN {{svcs:Array(String)}} AND {LIVE_COND} AND SpanName = 'claude_code.llm_request'
)
UNION ALL
SELECT 'event', countIf(ev = '{EV_API_REQUEST}'), countIf(ev = '{EV_API_ERROR}'),
       quantileExactIf(0.5)(ms, ev = '{EV_API_REQUEST}') FROM (
    SELECT {EV_EXPR} AS ev, toFloat64OrZero(LogAttributes['duration_ms']) AS ms
    FROM otel_logs
    WHERE {window_cond('Timestamp')} AND ServiceName IN {{svcs:Array(String)}} AND {LIVE_COND}
)
"""

# Tokens in the window from claude_code.token.usage: delta points are summed; cumulative series
# contribute their growth inside the window.
Q_TOKENS = f"""
SELECT sum(d) AS tokens FROM (
    SELECT if(any(AggregationTemporality) = 1, sum(Value),
              if(min(StartTimeUnix) >= fromUnixTimestamp64Milli({{t0:Int64}}), max(Value), max(Value) - min(Value))) AS d
    FROM otel_metrics_sum
    WHERE {window_cond('TimeUnix')} AND ServiceName IN {{svcs:Array(String)}} AND {LIVE_COND} AND MetricName = 'claude_code.token.usage'
    GROUP BY ResourceAttributes, Attributes, StartTimeUnix
)
"""

# Newest record of any service at or before `hi` (looking back a day).
Q_NEWEST = f"""
SELECT max(t) AS newest FROM (
    SELECT toUnixTimestamp64Milli(max(Timestamp)) AS t FROM otel_logs WHERE {window_cond('Timestamp', 'lo', 'hi')}
    UNION ALL SELECT toUnixTimestamp64Milli(max(Timestamp)) FROM otel_traces WHERE {window_cond('Timestamp', 'lo', 'hi')}
    UNION ALL SELECT toUnixTimestamp64Milli(max(TimeUnix)) FROM otel_metrics_gauge WHERE {window_cond('TimeUnix', 'lo', 'hi')}
    UNION ALL SELECT toUnixTimestamp64Milli(max(TimeUnix)) FROM otel_metrics_sum WHERE {window_cond('TimeUnix', 'lo', 'hi')}
)
"""

# ---- timeline (per-bin aggregates; bin = floor((t - from) / width))

BIN = "toInt64(floor((toUnixTimestamp64Milli({col}) - {{t0:Int64}}) / {{width_ms:Float64}}))"

Q_TL_EVENTS = f"""
SELECT bin, ev, count() AS n, max(dur) AS max_dur FROM (
    SELECT {BIN.format(col='Timestamp')} AS bin, {EV_EXPR} AS ev, toFloat64OrZero(LogAttributes['duration_ms']) AS dur
    FROM otel_logs
    WHERE {window_cond('Timestamp')} AND ServiceName IN {{svcs:Array(String)}} AND {LIVE_COND}
)
WHERE ev IN {{events:Array(String)}}
GROUP BY bin, ev
"""

Q_TL_SPANS = f"""
SELECT {BIN.format(col='Timestamp')} AS bin,
       countIf(ServiceName IN {{server:Array(String)}} AND SpanKind = 'Server'
               AND toInt32OrZero(SpanAttributes['http.response.status_code']) >= 500) AS server_5xx,
       countIf(ServiceName IN {{claude:Array(String)}} AND SpanName = 'claude_code.llm_request'
               AND (StatusCode = 'Error' OR SpanAttributes['success'] = 'false' OR SpanAttributes['error'] != ''
                    OR toInt32OrZero(SpanAttributes['status_code']) >= 400)) AS model_errors
FROM otel_traces
WHERE {window_cond('Timestamp')} AND ServiceName IN {{svcs:Array(String)}} AND {LIVE_COND}
GROUP BY bin
"""

# Records per bin (any signal, known services), and server heartbeats per bin.
Q_TL_ACTIVITY = f"""
SELECT bin, sum(n) AS n, sum(up) AS up FROM (
    SELECT {BIN.format(col='Timestamp')} AS bin, count() AS n, 0 AS up FROM otel_logs
        WHERE {window_cond('Timestamp')} AND ServiceName IN {{svcs:Array(String)}} AND {LIVE_COND} GROUP BY bin
    UNION ALL SELECT {BIN.format(col='Timestamp')}, count(), 0 FROM otel_traces
        WHERE {window_cond('Timestamp')} AND ServiceName IN {{svcs:Array(String)}} AND {LIVE_COND} GROUP BY 1
    UNION ALL SELECT {BIN.format(col='TimeUnix')}, count(),
                     countIf(ServiceName IN {{server:Array(String)}} AND MetricName = 'mclaude.server.up')
        FROM otel_metrics_gauge WHERE {window_cond('TimeUnix')} AND ServiceName IN {{svcs:Array(String)}} AND {LIVE_COND} GROUP BY 1
    UNION ALL SELECT {BIN.format(col='TimeUnix')}, count(),
                     countIf(ServiceName IN {{server:Array(String)}} AND MetricName = 'mclaude.server.up')
        FROM otel_metrics_sum WHERE {window_cond('TimeUnix')} AND ServiceName IN {{svcs:Array(String)}} AND {LIVE_COND} GROUP BY 1
)
GROUP BY bin
"""

Q_PINS = f"""
SELECT toUnixTimestamp64Milli(Timestamp) AS t, LogAttributes['rule'] AS rule, TraceId AS trace_id
FROM otel_logs
WHERE {window_cond('Timestamp')} AND ServiceName IN {{svcs:Array(String)}} AND {LIVE_COND} AND EventName = '{EV_RULE}'
ORDER BY t
LIMIT 1000
"""

Q_FINDING = f"""
SELECT toUnixTimestamp64Milli(Timestamp) AS t, EventName AS ev, ServiceName AS svc, SpanId AS span_id,
       LogAttributes AS attrs, Body AS body
FROM otel_logs
WHERE TraceId = {{trace_id:String}} AND ServiceName IN {{svcs:Array(String)}} AND {LIVE_COND}
  AND EventName IN ('{EV_RULE}', '{EV_SCREENSHOT}')
ORDER BY Timestamp
LIMIT 20
"""

# ---------------------------------------------------------------- helpers


def log(msg):
    print(f"{datetime.now(timezone.utc).isoformat(timespec='seconds')} {msg}", file=sys.stderr, flush=True)


def worst(*states):
    return max(states, key=lambda s: STATE_ORDER[s])


def num(v):
    if v is None:
        return None
    try:
        f = float(v)
    except (TypeError, ValueError):
        return None
    return None if math.isnan(f) or math.isinf(f) else f


def rnd(v, nd=1):
    v = num(v)
    return None if v is None else round(v, nd)


def fmt_ms(ms):
    if ms is None:
        return None
    return f"{ms / 1000:.1f} s" if ms >= 1000 else f"{ms:.0f} ms"


def node(id_, label, state, value, reason, details):
    return {"id": id_, "label": label, "state": state, "value": value,
            "reason": None if state == "ok" else (reason or "no data"), "details": details}


def edge(frm, to, state, label, reason=None):
    return {"from": frm, "to": to, "state": state, "label": label,
            "reason": None if state == "ok" else (reason or "no data")}


class Metrics:
    """Latest metric values, keeping only each metric's newest batch (series that stopped
    reporting, like an exited process, drop out)."""

    def __init__(self, rows):
        self.by = {}
        for r in rows:
            self.by.setdefault((r["svc"], r["m"]), []).append(r)
        for key, rs in self.by.items():
            newest = max(r["ts"] for r in rs)
            self.by[key] = [r for r in rs if r["ts"] >= newest - 5000]

    def rows(self, services, metric):
        out = []
        for s in services:
            out.extend(self.by.get((s, metric), []))
        return out

    def latest(self, services, metric):
        rs = self.rows(services, metric)
        return num(max(rs, key=lambda r: r["ts"])["v"]) if rs else None

    def has(self, services, metrics):
        return any(self.rows(services, m) for m in metrics)


def parse_body(body):
    try:
        return json.loads(body)
    except (TypeError, ValueError):
        return body


# ---------------------------------------------------------------- /api/map


def api_map(qs):
    now = time.time()
    at = qnum(qs, "at", now, lo=0, hi=now + 86400)
    window = qnum(qs, "window", 60, lo=1, hi=86400)
    live = abs(now - at) <= LIVE_TOLERANCE_S
    t1 = int(at * 1000)
    t0 = int((at - window) * 1000)
    p0 = int((at - max(window, PRESENCE_MIN_S)) * 1000)
    w = {"t0": t0, "t1": t1}
    deadline = Deadline(REQUEST_BUDGET_S)

    r = run_all({
        "metrics": (Q_METRICS, {"p0": p0, "t1": t1, "svcs": ALL_SERVICES, "metrics": MAP_METRICS}),
        "events": (Q_EVENTS, {**w, "svcs": ALL_SERVICES, "events": MAP_EVENTS}),
        "http": (Q_PHONE_HTTP, {**w, "svcs": PHONE, "slow_ns": PHONE_HTTP_SLOW_MS * 1_000_000}),
        "req": (Q_SERVER_REQ, {**w, "svcs": SERVER, "slow_ns": SERVER_SLOW_MS * 1_000_000,
                               "exempt": SLOW_EXEMPT_ROUTE_RE}),
        "turns": (Q_TURNS, {**w, "svcs": SERVER}),
        "model": (Q_MODEL, {**w, "svcs": CLAUDE}),
        "tokens": (Q_TOKENS, {**w, "svcs": CLAUDE}),
        "newest": (Q_NEWEST, {"lo": t1 - 86_400_000, "hi": t1 + 5000}),
    }, deadline)

    m = Metrics(r["metrics"])
    events = r["events"]

    def evs(names, services=None):
        return [e for e in events if e["ev"] in names and (services is None or e["svc"] in services)]

    nodes, edges = [], []

    # phone
    stalls = evs({EV_STALL}, PHONE)
    stall_max = max((num(e["attrs"].get("duration_ms")) or 0 for e in stalls), default=0)
    rules = evs({EV_RULE}, PHONE)
    has_vitals = m.has(PHONE, ["mclaude.app.cpu", "mclaude.app.memory", "mclaude.app.connected"])
    mem = m.latest(PHONE, "mclaude.app.memory")
    cpu = m.latest(PHONE, "mclaude.app.cpu")
    connected = m.latest(PHONE, "mclaude.app.connected")
    if rules:
        state, reason = "failing", f"rule broken: {rules[0]['attrs'].get('rule') or '?'}"
    elif stall_max >= STALL_FAILING_MS:
        state, reason = "failing", f"main thread stalled {stall_max / 1000:.1f} s"
    elif stall_max >= STALL_DEGRADED_MS:
        state, reason = "degraded", f"main thread stalled {stall_max:.0f} ms"
    elif not has_vitals:
        state, reason = "nodata", "no vitals (app in background or offline)"
    else:
        state, reason = "ok", None
    mem_mb = None if mem is None else round(mem / 1048576)
    nodes.append(node("phone", "Phone", state, f"mem {mem_mb} MB" if mem_mb is not None else None, reason, {
        "cpu_pct": rnd(cpu), "memory_mb": mem_mb, "stalls": len(stalls), "max_stall_ms": rnd(stall_max, 0) if stalls else None,
        "rule_breaks": len(rules), "ws_connected": None if connected is None else connected >= 0.5}))

    # phone - server
    h = r["http"][0] if r["http"] else {}
    hn = int(h.get("n") or 0)
    h_median = num(h.get("median_ms")) if hn else None
    if not has_vitals:
        es, ereason, elabel = "nodata", "phone has no data", "no data"
    else:
        ws = "ws" if connected is None or connected >= 0.5 else "ws down"
        elabel = ws + (f" · {fmt_ms(h_median)}" if h_median is not None else "")
        if connected is not None and connected < 0.5:
            es, ereason = "failing", "phone WebSocket disconnected"
        elif int(h.get("errors") or 0):
            es, ereason = "degraded", f"{h['errors']} HTTP error(s), e.g. {h.get('error_example')}"
        elif int(h.get("slow") or 0):
            es, ereason = "degraded", f"HTTP request {fmt_ms(num(h.get('max_ms')))} ({h.get('slowest_path') or '?'})"
        else:
            es, ereason = "ok", None
    edges.append(edge("phone", "server", es, elabel, ereason))

    # server
    q = r["req"][0] if r["req"] else {}
    server_up = m.has(SERVER, ["mclaude.server.up"])
    sessions = {}
    for row in m.rows(SERVER, "mclaude.server.sessions"):
        sessions[row["proc"] or "total"] = sessions.get(row["proc"] or "total", 0) + int(num(row["v"]) or 0)
    ws_clients = m.latest(SERVER, "mclaude.server.ws_clients")
    turns_running = m.latest(SERVER, "mclaude.server.turns_running")
    n_sessions = sum(sessions.values()) if sessions else None
    if int(q.get("errors_5xx") or 0):
        state, reason = "failing", f"{q['errors_5xx']} 5xx response(s), e.g. {q.get('error_example')}"
    elif not server_up:
        state, reason = "nodata", "no heartbeat"
    elif int(q.get("slow") or 0):
        state, reason = "degraded", f"request took {fmt_ms(num(q.get('slowest_ms')))} ({q.get('slowest_route')})"
    else:
        state, reason = "ok", None
    value = None
    if server_up:
        value = f"{n_sessions if n_sessions is not None else '?'} sessions · {int(ws_clients) if ws_clients is not None else '?'} ws"
    nodes.append(node("server", "Server", state, value, reason, {
        "sessions": n_sessions, "sessions_by_process": sessions or None,
        "ws_clients": None if ws_clients is None else int(ws_clients),
        "requests": int(q.get("n") or 0), "median_request_ms": rnd(q.get("median_ms")) if q.get("n") else None,
        "errors_5xx": int(q.get("errors_5xx") or 0), "slow_requests": int(q.get("slow") or 0)}))

    # server - claude, and claude
    t = r["turns"][0] if r["turns"] else {}
    tn = int(t.get("n") or 0)
    restarts = evs({EV_RESTART}, SERVER)
    exits = evs({EV_EXITED}, SERVER)
    api_errors = evs({EV_API_ERROR}, CLAUDE)
    restarting = sessions.get("restarting", 0)
    running = sessions.get("running")
    if not server_up:
        es, ereason, elabel = "nodata", "server has no data", "no data"
    else:
        elabel = f"{tn} turns · {fmt_ms(num(t.get('median_ms')))}" if tn else "idle"
        if int(t.get("errors") or 0):
            es, ereason = "degraded", f"{t['errors']} turn(s) failed ({t.get('error_outcome') or 'error'})"
        else:
            es, ereason = "ok", None
    edges.append(edge("server", "claude", es, elabel, ereason))

    if not server_up:
        cstate, creason = "nodata", "server has no data"
    elif restarts or restarting:
        cstate = "failing"
        creason = (f"{len(restarts)} process restart(s) scheduled" if restarts else f"{restarting} process(es) restarting")
    elif api_errors:
        a = api_errors[0]["attrs"]
        cstate = "degraded"
        creason = f"{len(api_errors)} API error(s): {(a.get('status_code') or '')} {(a.get('error') or '')[:80]}".strip()
    else:
        cstate, creason = "ok", None
    cvalue = None
    if server_up:
        cvalue = f"{running if running is not None else '?'} up · {int(turns_running) if turns_running is not None else '?'} turns"
    nodes.append(node("claude", "Claude", cstate, cvalue, creason, {
        "processes_running": running, "processes_restarting": restarting,
        "turns_running": None if turns_running is None else int(turns_running),
        "turns_finished": tn, "median_turn_ms": rnd(t.get("median_ms")) if tn else None,
        "restarts": len(restarts), "exits": len(exits),
        "unexpected_exits": sum(1 for e in exits
                                if (e["attrs"].get("mclaude.expected") or e["attrs"].get("expected")) == "false"),
        "api_errors": len(api_errors)}))

    # bedrock (and claude - bedrock)
    model = {row["src"]: row for row in r["model"]}
    span, ev = model.get("span", {}), model.get("event", {})
    src = span if int(span.get("n") or 0) else ev
    mn = int(src.get("n") or 0)
    m_median = num(src.get("median_ms")) if mn else None
    m_errors = int(span.get("errors") or 0) + int(ev.get("errors") or 0)
    tokens = num(r["tokens"][0]["tokens"]) if r["tokens"] else None
    tpm = round(tokens * 60 / window) if tokens else 0
    if m_errors:
        bstate, breason = "failing", f"{m_errors} model request error(s)"
    elif cstate == "nodata":
        bstate, breason = "nodata", "Claude has no data"
    elif mn == 0:
        bstate, breason = "ok", None  # idle: no model request in the window
    elif m_median is not None and m_median >= MODEL_SLOW_MS:
        bstate, breason = "degraded", f"median model request {fmt_ms(m_median)}"
    else:
        bstate, breason = "ok", None
    bvalue = "idle" if mn == 0 and bstate != "nodata" else fmt_ms(m_median)
    nodes.append(node("bedrock", "Bedrock", bstate, bvalue, breason, {
        "idle": mn == 0, "model_requests": mn, "median_model_ms": rnd(m_median), "model_errors": m_errors,
        "tokens_per_min": tpm}))
    edges.append(edge("claude", "bedrock", bstate, bvalue or "no data", breason))

    # mac
    cpu_rows = m.rows(MAC, "system.cpu.utilization")
    mac_cpu = None
    if cpu_rows:
        idle = [num(x["v"]) for x in cpu_rows if x["state"] == "idle"]
        ncpu = len({x["cpu"] for x in cpu_rows}) or 1
        busy = (1 - sum(idle) / len(idle)) if idle else sum(num(x["v"]) or 0 for x in cpu_rows) / ncpu
        mac_cpu = max(0.0, min(100.0, busy * 100))
    mem_rows = m.rows(MAC, "system.memory.utilization")
    mac_mem = sum(num(x["v"]) or 0 for x in mem_rows if x["state"] == "used") * 100 if mem_rows else None
    usage = m.rows(MAC, "system.memory.usage")
    used_b = sum(num(x["v"]) or 0 for x in usage if x["state"] == "used") if usage else None
    total_b = sum(num(x["v"]) or 0 for x in usage) if usage else None
    procs = {}
    for metric, key in (("process.cpu.utilization", "cpu"), ("process.memory.usage", "mem")):
        for x in m.rows(MAC, metric):
            p = procs.setdefault(x["role"] or "other", {"pids": set(), "cpu": 0.0, "mem": 0.0})
            p["pids"].add(x["pid"])
            p[key] += num(x["v"]) or 0
    has_mac = bool(cpu_rows or mem_rows or m.has(MAC, ["system.cpu.load_average.1m"]))
    if not has_mac:
        state, reason = "nodata", "no host metrics"
    elif mac_cpu is not None and mac_cpu >= MAC_CPU_PCT:
        state, reason = "degraded", f"CPU {mac_cpu:.0f}%"
    elif mac_mem is not None and mac_mem >= MAC_MEM_PCT:
        state, reason = "degraded", f"memory {mac_mem:.0f}%"
    else:
        state, reason = "ok", None
    nodes.append(node("mac", "Mac", state, f"cpu {mac_cpu:.0f}%" if mac_cpu is not None else None, reason, {
        "cpu_pct": rnd(mac_cpu), "memory_pct": rnd(mac_mem),
        "memory_used_gb": rnd(used_b / 1e9 if used_b is not None else None, 2),
        "memory_total_gb": rnd(total_b / 1e9 if total_b else None, 2),
        "load_1m": rnd(m.latest(MAC, "system.cpu.load_average.1m"), 2),
        "processes": {role: {"count": len(p["pids"]), "cpu_pct": round(p["cpu"] * 100, 1),
                             "memory_mb": round(p["mem"] / 1048576)} for role, p in sorted(procs.items())} or None}))

    # obs
    newest = num(r["newest"][0]["newest"]) if r["newest"] else None
    lag = max(0.0, at - newest / 1000) if newest else None
    if lag is None:
        state, reason = "failing", "no records in the last 24 h"
    elif lag >= LAG_FAILING_S:
        state, reason = "failing", f"ingest lag {lag:.0f} s"
    elif lag >= LAG_DEGRADED_S:
        state, reason = "degraded", f"ingest lag {lag:.0f} s"
    else:
        state, reason = "ok", None
    nodes.append(node("obs", "Obs", state, f"lag {lag:.0f} s" if lag is not None else None, reason,
                      {"ingest_lag_s": rnd(lag)}))

    return {"at": int(at), "live": live, "window": int(window), "nodes": nodes, "edges": edges,
            "events": map_events(events)}


def map_events(rows):
    out = []
    for e in rows:  # already newest first
        a, ev = e["attrs"], e["ev"]
        item = {"t": round(e["t"] / 1000, 3), "trace_id": e["trace_id"] or None}
        if ev == EV_RULE:
            item.update(kind="rule", rule=a.get("rule"), node="phone")
        elif ev == EV_STALL:
            item.update(kind="stall", node="phone", label=f"stall {fmt_ms(num(a.get('duration_ms')) or 0)}")
        elif ev == EV_RESTART:
            item.update(kind="restart", node="claude",
                        label=f"restart scheduled {a.get('session') or a.get('session_id') or ''}".strip())
        elif ev == EV_EXITED:
            item.update(kind="exit", node="claude", label=("expected " if (a.get("mclaude.expected") or a.get("expected")) == "true" else "") +
                        f"exit {a.get('session') or a.get('session_id') or ''}".strip())
        elif ev == EV_API_ERROR:
            item.update(kind="api_error", node="claude",
                        label=f"API error {a.get('status_code') or ''} {(a.get('error') or '')[:60]}".strip())
        else:
            continue
        out.append(item)
        if len(out) >= 100:
            break
    return out


# ---------------------------------------------------------------- /api/timeline

# Per-bin state (simplified, from aggregates):
#   failing  = rule break, stall >= 1 s, process restart scheduled, model request error
#              (llm_request span error or claude_code.api_error), server 5xx, or a server up-gap
#              (heartbeats seen earlier in the range, other services reporting, none in this bin)
#   degraded = stall >= 250 ms
#   nodata   = no record from any known service in the bin, or the bin is in the future
#   ok       = otherwise
TL_EVENTS = [EV_RULE, EV_STALL, EV_RESTART, EV_API_ERROR]


def api_timeline(qs):
    now = time.time()
    to = qnum(qs, "to", now, lo=0, hi=now + 86400)
    frm = qnum(qs, "from", to - 86400, lo=0, hi=to)
    bins = int(qnum(qs, "bins", 96, lo=1, hi=1000))
    if to - frm < bins:
        raise ApiError(400, "range must be at least one second per bin")
    if to - frm > 31 * 86400:
        raise ApiError(400, "range longer than 31 days")
    t0, t1 = int(frm * 1000), int(to * 1000)
    width_ms = (t1 - t0) / bins
    p = {"t0": t0, "t1": t1, "width_ms": width_ms, "svcs": ALL_SERVICES}
    deadline = Deadline(REQUEST_BUDGET_S)
    r = run_all({
        "events": (Q_TL_EVENTS, {**p, "events": TL_EVENTS}),
        "spans": (Q_TL_SPANS, {**p, "server": SERVER, "claude": CLAUDE}),
        "activity": (Q_TL_ACTIVITY, {**p, "server": SERVER}),
        "pins": (Q_PINS, {"t0": t0, "t1": t1, "svcs": PHONE}),
    }, deadline)

    state = ["ok"] * bins
    reason = [None] * bins

    def mark(b, s, why):
        if 0 <= b < bins and STATE_ORDER[s] > STATE_ORDER[state[b]]:
            state[b], reason[b] = s, why

    activity = {int(x["bin"]): x for x in r["activity"]}
    for b in range(bins):
        if b not in activity or t0 + b * width_ms > now * 1000:
            mark(b, "nodata", "no data")
    for x in r["events"]:
        b, ev, n = int(x["bin"]), x["ev"], int(x["n"])
        if ev == EV_RULE:
            mark(b, "failing", f"{n} rule break(s)")
        elif ev == EV_STALL:
            d = num(x["max_dur"]) or 0
            if d >= STALL_FAILING_MS:
                mark(b, "failing", f"stall {d / 1000:.1f} s")
            elif d >= STALL_DEGRADED_MS:
                mark(b, "degraded", f"stall {d:.0f} ms")
        elif ev == EV_RESTART:
            mark(b, "failing", f"{n} process restart(s)")
        elif ev == EV_API_ERROR:
            mark(b, "failing", f"{n} model API error(s)")
    for x in r["spans"]:
        b = int(x["bin"])
        if int(x["model_errors"]):
            mark(b, "failing", f"{x['model_errors']} model request error(s)")
        if int(x["server_5xx"]):
            mark(b, "failing", f"{x['server_5xx']} server 5xx")
    up_bins = sorted(b for b, x in activity.items() if int(x["up"]))
    if up_bins and width_ms >= 30_000:
        for b in range(up_bins[0], bins):
            if b in activity and not int(activity[b]["up"]) and t0 + (b + 1) * width_ms <= now * 1000:
                mark(b, "failing", "server heartbeat missing")

    return {
        "from": int(frm), "to": int(to), "bin_s": round(width_ms / 1000, 3),
        "bins": [{"t": int((t0 + b * width_ms) / 1000), "state": state[b], **({"reason": reason[b]} if reason[b] and state[b] != "ok" else {})}
                 for b in range(bins)],
        "pins": [{"t": round(x["t"] / 1000, 3), "kind": "rule", "rule": x["rule"], "trace_id": x["trace_id"]}
                 for x in r["pins"]],
    }


# ---------------------------------------------------------------- /api/finding

TRACE_ID_RE = re.compile(r"^[0-9a-f]{32}$")


def api_finding(qs):
    trace_id = (qs.get("trace_id") or [""])[0].strip().lower()
    if not TRACE_ID_RE.match(trace_id):
        raise ApiError(400, "trace_id must be 32 hex characters")
    rows = query(Q_FINDING, {"trace_id": trace_id, "svcs": PHONE}, Deadline(REQUEST_BUDGET_S))
    rule = next((x for x in rows if x["ev"] == EV_RULE), None)
    if rule is None:
        raise ApiError(404, "no rule break in that trace")
    shot = next((x for x in rows if x["ev"] == EV_SCREENSHOT), None)
    path = (shot["attrs"].get("path") or None) if shot else None
    return {
        "trace_id": trace_id,
        "rule": rule["attrs"].get("rule"),
        "t": round(rule["t"] / 1000, 3),
        "time": datetime.fromtimestamp(rule["t"] / 1000, timezone.utc).isoformat(timespec="milliseconds"),
        "node": "phone",
        "service": rule["svc"],
        "attributes": rule["attrs"],
        "body": parse_body(rule["body"]),
        "screenshot_path": path,
        "screenshot_uploaded": None if shot is None else shot["attrs"].get("uploaded") == "true",
    }


# ---------------------------------------------------------------- /api/health


def api_health(qs):
    now = time.time()
    t1 = int(now * 1000)
    rows = query(Q_NEWEST, {"lo": t1 - 86_400_000, "hi": t1 + 5000}, Deadline(REQUEST_BUDGET_S))
    newest = num(rows[0]["newest"]) if rows else None
    return {"ok": True, "ingest_lag_s": round(max(0.0, now - newest / 1000), 1) if newest else None}


# ---------------------------------------------------------------- Ops dispatcher (phase 3)
#
# docs/plan-observability.md -> "Ops session (phase 3)". A background thread scans ClickHouse for
# trigger records every 30 s, groups them into findings (SQLite at /data), and delivers new findings
# to the "Ops" session on mclaude-server as automated input. Off unless OPS_DISPATCH_ENABLED=true;
# while off, obs-api makes no calls to mclaude-server at all (no scans, no delivery, no push).


def _env_bool(name, default="false"):
    return os.environ.get(name, default).strip().lower() in ("1", "true", "yes", "on")


OPS_ENABLED = _env_bool("OPS_DISPATCH_ENABLED")
OPS_SERVER_URL = (os.environ.get("OPS_SERVER_URL") or os.environ.get("MCLAUDE_SERVER_URL")
                  or "https://home-server.taild44daa.ts.net:8377").rstrip("/")
OPS_SESSION_ID = os.environ.get("OPS_SESSION_ID", "").strip()        # pin the session (skips lookup and creation)
OPS_SESSION_NAME = os.environ.get("OPS_SESSION_NAME", "Ops")
OPS_SESSION_CWD = os.environ.get("OPS_SESSION_CWD", "/Users/rsong/work/mclaude-v1")
OPS_CREATE_SESSION = _env_bool("OPS_CREATE_SESSION", "true")
OPS_SCAN_S = float(os.environ.get("OPS_SCAN_INTERVAL_S", "30"))
OPS_MAX_PER_HOUR = int(os.environ.get("OPS_MAX_DELIVERIES_PER_HOUR", "10"))
OPS_DEDUPE_S = 3600
OPS_WAITING_CARD_S = 3600
# Records can arrive long after their timestamp (a crash record is sent at the next launch, MetricKit
# a launch or more later). Every scan re-reads this far behind the watermark and skips records it
# has already processed, so late records are neither lost nor counted twice.
OPS_LATE_S = float(os.environ.get("OPS_LATE_WINDOW_S", "86400"))
OPS_SCAN_LIMIT = 5000
OPS_SESSION_REFRESH_S = 300
OPS_DB = os.environ.get("OPS_DB", "/data/findings.db")
OPS_TOKEN_FILE = os.environ.get("OPS_TOKEN_FILE", "/secrets/ops-token")
SERVER_TOKEN_FILE = os.environ.get("OPS_SERVER_TOKEN_FILE", "/secrets/server-token")

FINDING_STATUSES = ("new", "investigating", "root_cause", "fix_proposed", "needs_you", "resolved", "not_a_bug")
PUSH_STATUSES = {"root_cause", "fix_proposed", "needs_you"}
PROOF_FIELDS = ("summary", "root_cause", "code_pointer", "reasoning", "reproduction", "proposed_fix", "question")
FINDING_ID_RE = re.compile(r"^F-(\d{1,9})$")

EV_EXCEPTION = "mclaude.app.exception"
EV_CRASH = "mclaude.app.crash"
EV_UNCLEAN = "mclaude.app.unclean_exit"
SPAN_LLM = "claude_code.llm_request"

# The session a record belongs to: the record's own attribute, else its resource's (Claude Code).
SID_LOG = "coalesce(nullIf(LogAttributes['mclaude.session_id'], ''), ResourceAttributes['mclaude.session_id'])"
SID_SPAN = "coalesce(nullIf(SpanAttributes['mclaude.session_id'], ''), ResourceAttributes['mclaude.session_id'])"
# Records of the Ops session itself never trigger ({ops} is '' while the Ops session is unknown).
NOT_OPS = "({ops:String} = '' OR sid != {ops:String})"

# Triggers table: rule, stall, crash (app and Claude process), model (api_error events).
Q_TRIGGER_LOGS = f"""
SELECT tns, svc, name, trace_id, span_id, sid, attrs, body FROM (
    SELECT toUnixTimestamp64Nano(Timestamp) AS tns, ServiceName AS svc, {EV_EXPR} AS name, TraceId AS trace_id,
           SpanId AS span_id, {SID_LOG} AS sid, LogAttributes AS attrs, substring(Body, 1, 300) AS body
    FROM otel_logs
    WHERE {window_cond('Timestamp')} AND ServiceName IN {{svcs:Array(String)}} AND {LIVE_COND}
)
WHERE {NOT_OPS} AND (
       (svc IN {{phone:Array(String)}} AND (
            name = '{EV_RULE}'
            OR (name = '{EV_STALL}' AND toFloat64OrZero(attrs['duration_ms']) >= {{stall_ms:Float64}})
            OR name IN ('{EV_EXCEPTION}', '{EV_CRASH}', '{EV_UNCLEAN}')))
    OR (svc IN {{server:Array(String)}} AND (
            (name = '{EV_EXITED}' AND (attrs['mclaude.expected'] = 'false' OR attrs['expected'] = 'false'))
            OR name = '{EV_RESTART}'))
    OR (svc IN {{claude:Array(String)}} AND name = '{EV_API_ERROR}'))
ORDER BY tns
LIMIT {OPS_SCAN_LIMIT}
"""

# Server request spans with a 5xx status (crash), and errored Claude Code model requests (model).
Q_TRIGGER_SPANS = f"""
SELECT tns, svc, name, trace_id, span_id, sid, attrs, body FROM (
    SELECT toUnixTimestamp64Nano(Timestamp) AS tns, ServiceName AS svc, SpanName AS name, TraceId AS trace_id,
           SpanId AS span_id, {SID_SPAN} AS sid, SpanAttributes AS attrs, StatusMessage AS body,
           SpanKind AS span_kind, StatusCode AS status_code
    FROM otel_traces
    WHERE {window_cond('Timestamp')} AND ServiceName IN {{svcs:Array(String)}} AND {LIVE_COND}
)
WHERE {NOT_OPS} AND (
       (svc IN {{server:Array(String)}} AND span_kind = 'Server'
            AND toInt32OrZero(attrs['http.response.status_code']) >= 500)
    OR (svc IN {{claude:Array(String)}} AND name = '{SPAN_LLM}'
            AND (status_code = 'Error' OR attrs['success'] = 'false' OR attrs['error'] != ''
                 OR toInt32OrZero(attrs['status_code']) >= 400)))
ORDER BY tns
LIMIT {OPS_SCAN_LIMIT}
"""


def iso(t):
    return None if t is None else datetime.fromtimestamp(t, timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def one_line(s, n=200):
    s = " ".join(str(s or "").split())
    return s if len(s) <= n else s[: n - 1] + "…"


def classify(src, r):
    """A trigger row -> (kind, name, summary). name is the rule for rule breaks, else the record name."""
    a, name, sid = r["attrs"], r["name"], r["sid"] or ""
    where = f" (session {sid})" if sid else ""
    if src == "span":
        if name == SPAN_LLM:
            why = a.get("error") or (f"status {a['status_code']}" if a.get("status_code") else "") or r["body"] or "error"
            return "model", name, one_line(f"Model request failed: {why}{where}.")
        status = a.get("http.response.status_code") or "5xx"
        return "crash", name, one_line(f"Server returned {status} on {name}{where}.")
    if name == EV_RULE:
        rule = a.get("rule") or "unknown"
        return "rule", rule, one_line(f"Rule {rule} broke on the phone{where}.")
    if name == EV_STALL:
        ms = num(a.get("duration_ms")) or 0
        return "stall", name, one_line(f"Main thread stalled {fmt_ms(ms)} on the phone{where}.")
    if name == EV_EXCEPTION:
        what = ": ".join(x for x in (a.get("exception.type"), a.get("exception.message")) if x) or "uncaught exception"
        return "crash", name, one_line(f"App crashed with an uncaught exception, {what}{where}.")
    if name == EV_CRASH:
        what = ", ".join(x for x in (a.get("exception.type"), a.get("signal") and f"signal {a['signal']}",
                                     a.get("termination_reason")) if x) or "crash report"
        return "crash", name, one_line(f"MetricKit crash report: {what}.")
    if name == EV_UNCLEAN:
        return "crash", name, one_line("The app's previous launch ended in the foreground "
                                       f"(build {a.get('previous.build') or '?'}, pid {a.get('previous.pid') or '?'}).")
    if name == EV_EXITED:
        return "crash", name, one_line(f"Claude process exited unexpectedly, code {a.get('process.exit.code') or '?'}{where}.")
    if name == EV_RESTART:
        return "crash", name, one_line(f"Claude process restart scheduled after {a.get('mclaude.crashes') or '?'} "
                                       f"crash(es), backoff {a.get('mclaude.backoff_s') or '?'} s{where}.")
    if name == EV_API_ERROR:
        why = " ".join(x for x in (a.get("status_code"), a.get("error")) if x) or "error"
        return "model", name, one_line(f"Claude Code API error: {why}{where}.")
    return "crash", name, one_line(f"{name}{where}.")


def subject(kind, name):
    return name if kind == "rule" else f"{kind} · {name}"


def xml_escape(s):
    return str(s).replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;").replace('"', "&quot;")


class FindingsStore:
    """SQLite store: findings, processed record keys, delivery log, and dispatcher state."""

    def __init__(self, path):
        d = os.path.dirname(path)
        if d:
            os.makedirs(d, exist_ok=True)
        self.lock = threading.Lock()
        self.db = sqlite3.connect(path, timeout=10, check_same_thread=False)
        self.db.row_factory = sqlite3.Row
        with self.lock, self.db:
            self.db.execute("PRAGMA journal_mode=WAL")
            self.db.executescript("""
                CREATE TABLE IF NOT EXISTS findings (
                    n INTEGER PRIMARY KEY AUTOINCREMENT,
                    signature TEXT NOT NULL, kind TEXT NOT NULL, name TEXT NOT NULL, session_id TEXT NOT NULL,
                    service TEXT, title TEXT NOT NULL,
                    opened_at REAL NOT NULL, first_seen REAL NOT NULL, last_seen REAL NOT NULL,
                    count INTEGER NOT NULL DEFAULT 1, trace_ids TEXT NOT NULL DEFAULT '[]',
                    status TEXT NOT NULL DEFAULT 'new',
                    summary TEXT, root_cause TEXT, code_pointer TEXT, reasoning TEXT, reproduction TEXT,
                    proposed_fix TEXT, question TEXT,
                    delivery TEXT NOT NULL DEFAULT 'pending', delivered_at REAL, held_at REAL, delivery_error TEXT,
                    updated_at REAL NOT NULL);
                CREATE INDEX IF NOT EXISTS findings_sig ON findings (signature, opened_at);
                CREATE TABLE IF NOT EXISTS seen (key TEXT PRIMARY KEY, t REAL NOT NULL);
                CREATE TABLE IF NOT EXISTS deliveries (t REAL NOT NULL, what TEXT NOT NULL, finding INTEGER);
                CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT);
            """)

    def get_meta(self, key, default=None):
        with self.lock:
            row = self.db.execute("SELECT value FROM meta WHERE key = ?", (key,)).fetchone()
        return row[0] if row else default

    def set_meta(self, key, value):
        with self.lock, self.db:
            self.db.execute("INSERT OR REPLACE INTO meta (key, value) VALUES (?, ?)", (key, str(value)))

    def record(self, rec_key, t, signature, kind, name, sid, service, summary, trace_id, now):
        """Adds one trigger record. Returns 'seen', 'added' (to an open finding) or 'opened'."""
        with self.lock, self.db:
            if self.db.execute("INSERT OR IGNORE INTO seen (key, t) VALUES (?, ?)", (rec_key, t)).rowcount == 0:
                return "seen"
            f = self.db.execute("SELECT n, trace_ids, first_seen, last_seen FROM findings WHERE signature = ? "
                                "AND opened_at >= ? ORDER BY n DESC LIMIT 1", (signature, now - OPS_DEDUPE_S)).fetchone()
            if f:
                traces = json.loads(f["trace_ids"])
                if trace_id and trace_id not in traces and len(traces) < 100:
                    traces.append(trace_id)
                self.db.execute("UPDATE findings SET count = count + 1, first_seen = ?, last_seen = ?, trace_ids = ?, "
                                "updated_at = ? WHERE n = ?",
                                (min(f["first_seen"], t), max(f["last_seen"], t), json.dumps(traces), now, f["n"]))
                return "added"
            self.db.execute(
                "INSERT INTO findings (signature, kind, name, session_id, service, title, opened_at, first_seen, last_seen,"
                " trace_ids, summary, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
                (signature, kind, name, sid, service, subject(kind, name), now, t, t,
                 json.dumps([trace_id] if trace_id else []), summary, now))
            return "opened"

    def prune_seen(self, before):
        with self.lock, self.db:
            self.db.execute("DELETE FROM seen WHERE t < ?", (before,))
            self.db.execute("DELETE FROM deliveries WHERE t < ?", (time.time() - 7 * 86400,))

    def pending(self):
        with self.lock:
            return self.db.execute("SELECT * FROM findings WHERE delivery = 'pending' ORDER BY n").fetchall()

    def deliveries_since(self, t, what="finding"):
        with self.lock:
            return self.db.execute("SELECT count(*) FROM deliveries WHERE t >= ? AND what = ?", (t, what)).fetchone()[0]

    def set_delivery(self, n, state, now, error=None):
        with self.lock, self.db:
            if state == "delivered":
                self.db.execute("UPDATE findings SET delivery = 'delivered', delivered_at = ?, delivery_error = NULL "
                                "WHERE n = ?", (now, n))
                self.db.execute("INSERT INTO deliveries (t, what, finding) VALUES (?, 'finding', ?)", (now, n))
            elif state == "held":
                self.db.execute("UPDATE findings SET delivery = 'held', held_at = ?, delivery_error = ? WHERE n = ?",
                                (now, error, n))
            else:
                self.db.execute("UPDATE findings SET delivery_error = ? WHERE n = ?", (error, n))

    def held_waiting(self, since):
        """(held findings still 'new', of which held after `since`)."""
        with self.lock:
            row = self.db.execute("SELECT count(*), coalesce(sum(held_at > ?), 0) FROM findings "
                                  "WHERE delivery = 'held' AND status = 'new'", (since,)).fetchone()
        return int(row[0]), int(row[1])

    def log_delivery(self, what, now):
        with self.lock, self.db:
            self.db.execute("INSERT INTO deliveries (t, what, finding) VALUES (?, ?, NULL)", (now, what))

    def list(self, status=None, limit=100):
        with self.lock:
            if status:
                rows = self.db.execute("SELECT * FROM findings WHERE status = ? ORDER BY n DESC LIMIT ?", (status, limit))
            else:
                rows = self.db.execute("SELECT * FROM findings ORDER BY n DESC LIMIT ?", (limit,))
            return rows.fetchall()

    def get(self, n):
        with self.lock:
            return self.db.execute("SELECT * FROM findings WHERE n = ?", (n,)).fetchone()

    def update(self, n, fields, now):
        """Applies fields to finding n. Returns (old row, new row), or (None, None) if there's none."""
        with self.lock, self.db:
            old = self.db.execute("SELECT * FROM findings WHERE n = ?", (n,)).fetchone()
            if old is None:
                return None, None
            if fields:
                cols = ", ".join(f"{k} = ?" for k in fields)
                self.db.execute(f"UPDATE findings SET {cols}, updated_at = ? WHERE n = ?", (*fields.values(), now, n))
            return old, self.db.execute("SELECT * FROM findings WHERE n = ?", (n,)).fetchone()


def finding_json(row):
    f = dict(row)
    out = {
        "id": f"F-{f['n']}", "kind": f["kind"], "rule": f["name"] if f["kind"] == "rule" else None, "name": f["name"],
        "session_id": f["session_id"] or None, "service": f["service"], "title": f["title"], "signature": f["signature"],
        "status": f["status"], "count": f["count"], "trace_ids": json.loads(f["trace_ids"]),
        "first_seen": iso(f["first_seen"]), "last_seen": iso(f["last_seen"]),
        "first_seen_t": round(f["first_seen"], 3), "last_seen_t": round(f["last_seen"], 3),
        "opened_at": iso(f["opened_at"]), "updated_at": iso(f["updated_at"]),
        "delivery": f["delivery"], "delivered_at": iso(f["delivered_at"]),
    }
    for k in PROOF_FIELDS:
        out[k] = f[k]
    return out


# ---- mclaude-server client


class ServerError(Exception):
    pass


def server_call(method, path, body=None, timeout=10):
    """One authenticated call to mclaude-server. Returns (status, parsed JSON or text)."""
    try:
        with open(SERVER_TOKEN_FILE) as fh:
            token = fh.read().strip()
    except OSError as e:
        raise ServerError(f"no server token ({SERVER_TOKEN_FILE}: {e.strerror})")
    if not token:
        raise ServerError(f"server token file {SERVER_TOKEN_FILE} is empty")
    req = urllib.request.Request(OPS_SERVER_URL + path, method=method,
                                 data=None if body is None else json.dumps(body).encode(),
                                 headers={"Authorization": f"Bearer {token}", "Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            status, raw = resp.status, resp.read()
    except urllib.error.HTTPError as e:
        status, raw = e.code, e.read()
    except (OSError, TimeoutError) as e:
        raise ServerError(f"{method} {path}: {e}")
    text = raw.decode(errors="replace")
    try:
        return status, json.loads(text) if text else None
    except ValueError:
        return status, text


class Dispatcher:
    def __init__(self, store):
        self.store = store
        self.session_checked = 0.0

    # -- the Ops session

    def find_ops_session(self):
        """Ops session id by name, or None when there's none. Raises ServerError if the list can't be read."""
        status, sessions = server_call("GET", "/sessions")
        if status != 200 or not isinstance(sessions, list):
            raise ServerError(f"GET /sessions -> {status}")
        named = [s for s in sessions if isinstance(s, dict) and s.get("name") == OPS_SESSION_NAME]
        named.sort(key=lambda s: (s.get("cwd") != OPS_SESSION_CWD, s.get("startedAt") or ""))
        return named[0]["id"] if named else None

    def known_ops_session(self):
        """Last known Ops session id, refreshed every few minutes. Used to exclude Ops' own records."""
        if OPS_SESSION_ID:
            return OPS_SESSION_ID
        now = time.time()
        if now - self.session_checked >= OPS_SESSION_REFRESH_S:
            self.session_checked = now
            try:
                sid = self.find_ops_session()
                if sid:
                    self.store.set_meta("ops_session_id", sid)
            except ServerError as e:
                log(f"[ops] session lookup failed: {e}")
        return self.store.get_meta("ops_session_id", "")

    def ensure_ops_session(self):
        """The Ops session to deliver to: looked up by name, created (and named) when there's none."""
        if OPS_SESSION_ID:
            return OPS_SESSION_ID
        sid = self.find_ops_session()
        if sid is None:
            if not OPS_CREATE_SESSION:
                raise ServerError(f'no session named "{OPS_SESSION_NAME}" and OPS_CREATE_SESSION is off')
            status, resp = server_call("POST", "/sessions", {"cwd": OPS_SESSION_CWD, "name": OPS_SESSION_NAME}, timeout=60)
            if status != 200 or not isinstance(resp, dict) or not resp.get("id"):
                raise ServerError(f"POST /sessions -> {status} {str(resp)[:200]}")
            sid = resp["id"]
            status, resp = server_call("PATCH", f"/sessions/{urllib.parse.quote(sid)}", {"name": OPS_SESSION_NAME})
            log(f"[ops] created Ops session {sid} in {OPS_SESSION_CWD}; rename -> {status}")
        self.store.set_meta("ops_session_id", sid)
        self.session_checked = time.time()
        return sid

    def send_input(self, sid, text):
        status, resp = server_call("POST", f"/sessions/{urllib.parse.quote(sid)}/input",
                                   {"text": text, "automated": True}, timeout=30)
        ok = status == 200 and isinstance(resp, dict) and resp.get("status") in ("sent", "queued")
        return ok, status, resp

    def deliver(self, text):
        """Sends text to the Ops session. Re-resolves the session once if it's gone (ended)."""
        sid = self.ensure_ops_session()
        ok, status, resp = self.send_input(sid, text)
        if not ok and status == 404 and not OPS_SESSION_ID:
            self.store.set_meta("ops_session_id", "")
            sid = self.ensure_ops_session()
            ok, status, resp = self.send_input(sid, text)
        if not ok:
            raise ServerError(f"POST /sessions/{sid}/input -> {status} {str(resp)[:200]}")
        return sid, resp.get("status")

    # -- scanning

    def scan(self):
        now = time.time()
        start = float(self.store.get_meta("start") or 0)
        if not start:
            start = now
            self.store.set_meta("start", start)
            self.store.set_meta("watermark", start)
            log(f"[ops] first run: watching records from {iso(start)}")
        watermark = float(self.store.get_meta("watermark") or start)
        catch_up = self.store.get_meta("catch_up") == "1"
        lo = max(start, watermark if catch_up else watermark - OPS_LATE_S)
        ops = self.known_ops_session() or ""
        p = {"t0": int(lo * 1000), "t1": int(now * 1000), "ops": ops, "svcs": ALL_SERVICES,
             "phone": PHONE, "server": SERVER, "claude": CLAUDE}
        deadline = Deadline(20)
        logs = query(Q_TRIGGER_LOGS, {**p, "stall_ms": float(STALL_FAILING_MS)}, deadline)
        spans = query(Q_TRIGGER_SPANS, p, deadline)
        rows = [("log", r) for r in logs] + [("span", r) for r in spans]
        rows.sort(key=lambda x: x[1]["tns"])
        counts = {"opened": 0, "added": 0, "seen": 0}
        for src, r in rows:
            kind, name, summary = classify(src, r)
            sid = r["sid"] or ""
            t = r["tns"] / 1e9
            key = hashlib.sha1("|".join([src, str(r["tns"]), r["svc"], r["name"], r["trace_id"], r["span_id"],
                                         json.dumps(r["attrs"], sort_keys=True)]).encode()).hexdigest()
            outcome = self.store.record(key, t, f"{kind}|{name}|{sid}", kind, name, sid, r["svc"], summary,
                                        r["trace_id"], now)
            counts[outcome] += 1
        full = len(logs) >= OPS_SCAN_LIMIT or len(spans) >= OPS_SCAN_LIMIT
        # A full page: continue from its last record next time, without the late look-back, so a
        # backlog can't keep re-reading the same records.
        new_wm = rows[-1][1]["tns"] / 1e9 if full and rows else now
        self.store.set_meta("watermark", new_wm)
        self.store.set_meta("catch_up", "1" if full else "0")
        self.store.prune_seen(min(lo, new_wm - OPS_LATE_S) - 3600)
        if counts["opened"] or counts["added"]:
            log(f"[ops] scan {iso(lo)}..{iso(now)}: {len(rows)} trigger records, "
                f"{counts['opened']} new finding(s), {counts['added']} added to open findings")

    # -- delivery

    def card(self, f):
        attrs = {"id": f"F-{f['n']}", "kind": f["kind"]}
        attrs["rule" if f["kind"] == "rule" else "name"] = f["name"]
        attrs["at"] = iso(f["first_seen"])
        traces = json.loads(f["trace_ids"])
        if f["kind"] == "rule" and traces:  # the card's Open button loads /api/finding, which takes rule breaks
            attrs["trace"] = traces[0]
        attrs["count"] = str(f["count"])
        a = " ".join(f'{k}="{xml_escape(v)}"' for k, v in attrs.items())
        return f"<mclaude-finding {a}>{xml_escape(f['summary'] or f['title'])}</mclaude-finding>\n" \
               "Investigate this finding with the ops skill."

    def deliver_pending(self):
        now = time.time()
        for f in self.store.pending():
            fid = f"F-{f['n']}"
            if f["opened_at"] < now - OPS_DEDUPE_S:
                self.store.set_delivery(f["n"], "held", now, f["delivery_error"] or "not delivered within an hour")
                log(f"[ops] {fid} held: not delivered within an hour ({f['delivery_error']})")
                continue
            if self.store.deliveries_since(now - 3600) >= OPS_MAX_PER_HOUR:
                self.store.set_delivery(f["n"], "held", now, f"rate limit ({OPS_MAX_PER_HOUR} per hour)")
                log(f"[ops] {fid} held: rate limit ({OPS_MAX_PER_HOUR} per hour)")
                continue
            f = self.store.get(f["n"])  # current count
            try:
                sid, how = self.deliver(self.card(f))
            except ServerError as e:
                self.store.set_delivery(f["n"], "pending", now, str(e))
                log(f"[ops] {fid} delivery failed, will retry: {e}")
                return
            self.store.set_delivery(f["n"], "delivered", now)
            log(f"[ops] {fid} ({f['title']}, x{f['count']}) delivered to {sid}: {how}")
        held, held_new = self.store.held_waiting(float(self.store.get_meta("waiting_card_at") or 0))
        last_card = float(self.store.get_meta("waiting_card_at") or 0)
        if held_new and now - last_card >= OPS_WAITING_CARD_S:
            text = (f"{held} more finding{'s' if held != 1 else ''} waiting: past the limit of {OPS_MAX_PER_HOUR} "
                    "delivered per hour, they were recorded but not sent. List them with "
                    "`curl -s 'http://127.0.0.1:4320/api/findings?status=new'`.\n"
                    "Triage them with the ops skill.")
            try:
                sid, how = self.deliver(text)
            except ServerError as e:
                log(f"[ops] waiting card failed, will retry: {e}")
                return
            self.store.set_meta("waiting_card_at", now)
            self.store.log_delivery("waiting_card", now)
            log(f"[ops] waiting card ({held} held) delivered to {sid}: {how}")

    def run(self):
        log(f"[ops] dispatcher on: server {OPS_SERVER_URL}, session "
            f"{OPS_SESSION_ID or repr(OPS_SESSION_NAME)}, every {OPS_SCAN_S:.0f} s, max {OPS_MAX_PER_HOUR}/h")
        while True:
            started = time.monotonic()
            try:
                self.scan()
                self.deliver_pending()
            except ApiError as e:
                log(f"[ops] scan failed: {e.message}")
            except Exception as e:  # noqa: BLE001 - the dispatcher must keep running
                log(f"[ops] dispatcher error: {e!r}")
            time.sleep(max(1.0, OPS_SCAN_S - (time.monotonic() - started)))


STORE = None
DISPATCHER = None


def ops_store():
    if STORE is None:
        raise ApiError(503, "findings store unavailable")
    return STORE


# ---- Ops API


def api_findings(qs):
    status = (qs.get("status") or [""])[0].strip()
    if status and status not in FINDING_STATUSES:
        raise ApiError(400, f"status must be one of {', '.join(FINDING_STATUSES)}")
    limit = int(qnum(qs, "limit", 100, lo=1, hi=1000))
    return {"findings": [finding_json(r) for r in ops_store().list(status or None, limit)]}


def finding_number(fid):
    m = FINDING_ID_RE.match(fid)
    if not m:
        raise ApiError(404, "no such finding")
    return int(m.group(1))


def api_findings_get(fid):
    row = ops_store().get(finding_number(fid))
    if row is None:
        raise ApiError(404, "no such finding")
    return finding_json(row)


def ops_token_ok(given):
    try:
        with open(OPS_TOKEN_FILE) as fh:
            want = fh.read().strip()
    except OSError as e:
        log(f"[ops] can't read the ops token ({OPS_TOKEN_FILE}: {e.strerror}); refusing updates")
        return False
    return bool(want) and hmac.compare_digest((given or "").strip().encode(), want.encode())


def api_findings_post(fid, headers, raw):
    if not ops_token_ok(headers.get("X-Ops-Token")):
        raise ApiError(403, "X-Ops-Token missing or wrong")
    n = finding_number(fid)
    try:
        body = json.loads(raw or b"{}")
    except ValueError:
        raise ApiError(400, "body must be JSON")
    if not isinstance(body, dict):
        raise ApiError(400, "body must be a JSON object")
    unknown = set(body) - {"status", *PROOF_FIELDS}
    if unknown:
        raise ApiError(400, f"unknown field(s): {', '.join(sorted(unknown))}")
    fields = {}
    for k, v in body.items():
        if v is not None and not isinstance(v, str):
            raise ApiError(400, f"{k} must be a string")
        if v is not None and len(v) > 50_000:
            raise ApiError(400, f"{k} is too long")
        fields[k] = v
    if "status" in fields and fields["status"] not in FINDING_STATUSES:
        raise ApiError(400, f"status must be one of {', '.join(FINDING_STATUSES)}")
    old, new = ops_store().update(n, fields, time.time())
    if new is None:
        raise ApiError(404, "no such finding")
    out = finding_json(new)
    if new["status"] != old["status"] and new["status"] in PUSH_STATUSES:
        out["push"] = push_finding(new)
    return out


def push_finding(f):
    """Status changed to root_cause / fix_proposed / needs_you: push to Richard via mclaude-server."""
    fid = f"F-{f['n']}"
    if not OPS_ENABLED:
        log(f"[ops] {fid} -> {f['status']}: push skipped (OPS_DISPATCH_ENABLED is off)")
        return "skipped: OPS_DISPATCH_ENABLED is off"
    title = f"Ops · {fid} {f['name'] if f['kind'] == 'rule' else f['kind']}"
    text = (f["question"] if f["status"] == "needs_you" and f["question"] else None) or f["summary"] or f["question"] \
        or f["title"]
    sid = OPS_SESSION_ID or (STORE.get_meta("ops_session_id", "") if STORE else "")
    body = {"title": title, "body": one_line(text, 400)}
    if sid:
        body["sessionId"] = sid
    try:
        status, resp = server_call("POST", "/push/notify", body)
    except ServerError as e:
        log(f"[ops] {fid} -> {f['status']}: push failed: {e}")
        return f"failed: {e}"
    if status == 200:
        log(f"[ops] {fid} -> {f['status']}: push sent ({title})")
        return "sent"
    err = resp.get("error") if isinstance(resp, dict) else str(resp)[:200]
    log(f"[ops] {fid} -> {f['status']}: push failed: {status} {err}")
    return f"failed: {status} {err}"


def start_ops():
    global STORE, DISPATCHER
    try:
        STORE = FindingsStore(OPS_DB)
    except (OSError, sqlite3.Error) as e:
        log(f"[ops] findings store {OPS_DB} unavailable: {e}; Ops API and dispatcher off")
        return
    if not OPS_ENABLED:
        log("[ops] dispatcher off (OPS_DISPATCH_ENABLED is not true); Ops API serves the store read/write, no server calls")
        return
    DISPATCHER = Dispatcher(STORE)
    threading.Thread(target=DISPATCHER.run, name="ops-dispatcher", daemon=True).start()


# ---------------------------------------------------------------- HTTP


def qnum(qs, name, default, lo, hi):
    vals = qs.get(name)
    if not vals or vals[0] == "":
        return default
    try:
        v = float(vals[0])
    except ValueError:
        raise ApiError(400, f"{name} must be a number")
    if math.isnan(v) or not lo <= v <= hi:
        raise ApiError(400, f"{name} out of range")
    return v


ROUTES = {"/api/map": api_map, "/api/timeline": api_timeline, "/api/finding": api_finding, "/api/health": api_health,
          "/api/findings": api_findings}
FINDINGS_PREFIX = "/api/findings/"


class Handler(BaseHTTPRequestHandler):
    server_version = "mclaude-obs-api"
    protocol_version = "HTTP/1.1"

    def do_GET(self):
        started = time.monotonic()
        url = urllib.parse.urlsplit(self.path)
        path = url.path.rstrip("/") or "/"
        fn = ROUTES.get(path)
        try:
            if path.startswith(FINDINGS_PREFIX):
                status, body = 200, api_findings_get(path[len(FINDINGS_PREFIX):])
            elif fn is None:
                raise ApiError(404, "not found")
            else:
                status, body = 200, fn(urllib.parse.parse_qs(url.query))
        except ApiError as e:
            status, body = e.status, {"error": e.message}
            if url.path == "/api/health":
                body["ok"] = False
        except Exception as e:  # noqa: BLE001 - never leak a traceback to the client
            log(f"internal error on {url.path}: {e!r}")
            status, body = 500, {"error": "internal error"}
        self.send_json(status, body)
        log(f"{self.client_address[0]} GET {url.path} {status} {(time.monotonic() - started) * 1000:.0f}ms")

    def do_HEAD(self):
        self.send_json(405, {"error": "method not allowed"})

    def do_POST(self):
        started = time.monotonic()
        path = urllib.parse.urlsplit(self.path).path.rstrip("/")
        if not path.startswith(FINDINGS_PREFIX):
            return self.send_json(405, {"error": "method not allowed"})
        try:
            length = int(self.headers.get("Content-Length") or 0)
            if length > 1_048_576:
                raise ApiError(413, "body too large")
            raw = self.rfile.read(length) if length > 0 else b""
            status, body = 200, api_findings_post(path[len(FINDINGS_PREFIX):], self.headers, raw)
        except ApiError as e:
            status, body = e.status, {"error": e.message}
        except ValueError:
            status, body = 400, {"error": "bad Content-Length"}
        except Exception as e:  # noqa: BLE001
            log(f"internal error on POST {path}: {e!r}")
            status, body = 500, {"error": "internal error"}
        self.send_json(status, body)
        log(f"{self.client_address[0]} POST {path} {status} {(time.monotonic() - started) * 1000:.0f}ms")

    def do_PUT(self):
        self.send_json(405, {"error": "method not allowed"})

    do_DELETE = do_PATCH = do_PUT

    def send_json(self, status, body):
        data = json.dumps(body, separators=(",", ":")).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.send_header("Cache-Control", "no-store")
        if status == 405:
            self.send_header("Allow", "GET, POST" if self.path.startswith(FINDINGS_PREFIX) else "GET")
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(data)

    def log_message(self, fmt, *args):  # replaced by the one-line log in do_GET
        pass


def main():
    httpd = ThreadingHTTPServer(("0.0.0.0", PORT), Handler)
    httpd.daemon_threads = True
    log(f"obs-api on :{PORT}, clickhouse {CH_URL}, services phone={PHONE} server={SERVER} claude={CLAUDE} mac={MAC}")
    start_ops()
    httpd.serve_forever()


if __name__ == "__main__":
    main()
