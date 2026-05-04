## Run: 2026-04-28T00:00:00Z (Round 2)

ADR: `docs/adr-0042-core-platform-architecture.md` (Status: accepted)

Specs evaluated:
- `docs/spec-state-schema.md`
- `docs/mclaude-control-plane/spec-control-plane.md`
- `docs/mclaude-session-agent/spec-session-agent.md`
- `docs/mclaude-common/spec-common.md`
- `docs/mclaude-web/spec-web.md`
- `docs/mclaude-cli/spec-cli.md`
- `docs/charts-mclaude/spec-helm.md`

### Phase 1 — ADR → Spec (forward pass)

| # | ADR (section) | ADR text | Spec location | Verdict | Direction | Notes |
|---|---------------|----------|---------------|---------|-----------|-------|
| 1 | Decisions table | "Headless via `--print --verbose --output-format stream-json --input-format stream-json --include-partial-messages`" | spec-session-agent: Role, Creation step 10 | REFLECTED | — | Spec lists matching flags (plus `--replay-user-messages` addition) |
| 2 | Decisions table | "Event bus: NATS JetStream" | spec-state-schema: MCLAUDE_EVENTS, MCLAUDE_API, MCLAUDE_LIFECYCLE | REFLECTED | — | All three streams documented |
| 3 | Decisions table | "Raw Claude Code stream-json — no envelope, no protobuf translation" | spec-session-agent: "Publishes raw stream-json events from Claude Code stdout" | REFLECTED | — | |
| 4 | Decisions table | "State store: NATS KV (sessions, projects, hosts, job-queue) + Postgres (users, hosts, projects, oauth_connections)" | spec-state-schema: all KV buckets + Postgres tables | REFLECTED | — | |
| 5 | Decisions table | "Single Go binary (`mclaude-session-agent`) per project pod" | spec-session-agent: "per-project process supervisor" | REFLECTED | — | |
| 6 | Decisions table | "Go service (`mclaude-control-plane`), K8s-free per ADR-0035" | spec-control-plane: "K8s-free: no controller-runtime, no K8s client" | REFLECTED | — | |
| 7 | Decisions table | "React 18 SPA, mobile browser first" | spec-web: "React 18 single-page application designed mobile browser first" | REFLECTED | — | |
| 8 | Decisions table | "nginx reverse proxy — no auth logic, no routing decisions" | spec-helm: Ingress resources; spec-control-plane mentions nginx | REFLECTED | — | |
| 9 | Decisions table | "Typed literals between every slug: `mclaude.users.{uslug}.hosts.{hslug}...`" | spec-common: typed subject helpers; spec-state-schema: all subjects | REFLECTED | — | |
| 10 | Decisions table | "Slug charset: `[a-z0-9][a-z0-9-]{0,62}`" | spec-common: "Charset: `[a-z0-9][a-z0-9-]{0,62}`. Max length 63." | REFLECTED | — | |
| 11 | Decisions table | "Blocklist of 10 words: users, hosts, projects, sessions, clusters, api, events, lifecycle, quota, terminal" | spec-common: Validate — identical 10-word blocklist | REFLECTED | — | |
| 12 | Decisions table | "All slugs system-computed from display names, never user-picked. Immutable after creation." | spec-state-schema: "Derived at user creation... Immutable after creation" | REFLECTED | — | |
| 13 | Decisions table | "Display name: free-form UTF-8, max 128, mutable. Slug: auto-derived, immutable." | spec-state-schema: users/projects/hosts tables separate name + slug | REFLECTED | — | |
| 14 | Decisions table | "Typed Go wrappers (`type UserSlug string`, etc.) in `mclaude-common/pkg/subj` — raw strings are compile-time errors" | spec-common: typed wrappers (UserSlug, ProjectSlug, etc.) documented | REFLECTED | — | |
| 15 | Decisions table | "Shared Go module `mclaude-common/` at repo root, wired via `go.work`" | spec-common: "module `mclaude.io/common`" | REFLECTED | — | |
| 16 | Decisions table | "User slug: `slugify(name-or-local-part)-{domain.split('.')[0]}`" | spec-common: DeriveUserSlug — "{slugify(displayName)}-{first domain segment}" | REFLECTED | — | |
| 17 | Decisions table | "Project/host/cluster slug: `slugify(display_name)` at creation; collision → numeric suffix" | spec-state-schema: hosts/projects slug derivation matches | REFLECTED | — | |
| 18 | Decisions table | "HTTP URL scheme: `/api/users/{uslug}/projects/{pslug}/...`" | spec-control-plane: all endpoints documented with slug URLs | REFLECTED | — | |
| 19 | Decisions table | "KV key separator: uniform `.`" | spec-state-schema: all KV keys use `.` separator | REFLECTED | — | |
| 20 | Decisions table | "Hard 403 when JWT `sub` ≠ URL `{uslug}`. Admin subtree bypasses with admin-role validation." | spec-control-plane: "extracts the user slug + host slug, and rejects requests targeting a different host" | PARTIAL | SPEC→FIX | Spec explicitly states host-scope rejection but only implicitly covers user-scope (uslug) rejection. Should state: JWT sub uslug ≠ URL {uslug} → 403 |
| 21 | Decisions table | "One worktree per branch per project. `joinWorktree: true` to share." | spec-session-agent: Worktree Management section | REFLECTED | — | |
| 22 | Decisions table | "Nix single-user mode, per-project PVC at `/nix/`, `pkg` shim" | spec-session-agent: "Nix PVC at `/nix`"; spec-helm: nix-{projectId} PVC | REFLECTED | — | |
| 23 | Decisions table | "Image based on `node:22-alpine` (Claude Code runtime requires Node.js)" | spec-session-agent: "Alpine-based with Node.js 22" | REFLECTED | — | |
| 24 | Decisions table | "Subject construction: typed Go wrappers in `mclaude-common/pkg/subj`" | spec-common: pkg/subj with typed helpers | REFLECTED | — | |
| 25 | Data Flow | "SPA publishes user message to NATS subject: `…api.sessions.input`" | spec-state-schema: subject documented; spec-web: NATS connection | REFLECTED | — | |
| 26 | Data Flow | "Session agent publishes each event to NATS JetStream: `…events.{sslug}`" | spec-session-agent: "Publishes raw stream-json events" to events subject | REFLECTED | — | |
| 27 | Data Flow | "The session agent also publishes user input messages to the events stream" | spec-session-agent: "User input messages are also published to the events stream" | REFLECTED | — | |
| 28 | Stream-JSON | "Subagent events appear flat on parent stdout with `parent_tool_use_id`" | spec-web: "Events with `parent_tool_use_id` → Render nested under parent Agent block"; spec-cli: nesting by parent_tool_use_id | REFLECTED | — | |
| 29 | NATS Subjects | "`MCLAUDE_EVENTS` captures `…events.*`. Retained 30 days." | spec-state-schema: MCLAUDE_EVENTS — MaxAge: 30 days | REFLECTED | — | |
| 30 | NATS Subjects | "`MCLAUDE_LIFECYCLE` captures `…lifecycle.*`. Retained 30 days." | spec-state-schema: MCLAUDE_LIFECYCLE — MaxAge: 30 days | REFLECTED | — | |
| 31 | NATS Subjects | "`MCLAUDE_API` captures `…api.sessions.>`. Retained 1 hour." | spec-state-schema: MCLAUDE_API — MaxAge: 1 hour | REFLECTED | — | |
| 32 | NATS Subjects | "`max_payload: 8388608` (8MB). If event exceeds 8MB, session agent truncates content, sets `truncated: true`." | spec-session-agent: "Truncates events exceeding the 8 MB NATS payload limit (removes `content` field, adds `truncated: true`)"; spec-state-schema: "max_payload: 8MB" | REFLECTED | — | |
| 33 | NATS Subjects | "Terminal I/O: Not JetStream — raw terminal I/O is ephemeral, no replay needed." | spec-state-schema: terminal subjects in core NATS subjects table; spec-session-agent: "core NATS subjects" | REFLECTED | — | |
| 34 | NATS Subjects | "Quota: `mclaude.users.{uslug}.quota` → broadcast signal" | spec-state-schema: user-level subject, "core NATS, not JetStream" | REFLECTED | — | |
| 35 | NATS Subjects | "MCLAUDE_API: session-agent consumes via durable pull consumers, not core NATS subscriptions" | spec-session-agent: "durable pull consumers on MCLAUDE_API" — command + control consumers | REFLECTED | — | |
| 36 | NATS Subjects | "API — Project Management: `mclaude.users.{uslug}.api.projects.updated` → control-plane broadcasts" | spec-state-schema: NATS Subjects (User-level) | GAP | SPEC→FIX | spec-state-schema's user-level subjects table only lists `…quota`. Missing the `…api.projects.updated` broadcast subject. spec-common has `UserAPIProjectsUpdated` helper but the canonical subject table omits it. |
| 37 | State (KV) | "mclaude-sessions key: `{uslug}.{hslug}.{pslug}.{sslug}`" | spec-state-schema: matching key format | REFLECTED | — | |
| 38 | State (KV) | "mclaude-projects key: `{uslug}.{hslug}.{pslug}`" | spec-state-schema: matching key format | REFLECTED | — | |
| 39 | State (KV) | "mclaude-hosts key: `{uslug}.{hslug}`" | spec-state-schema: matching key format | REFLECTED | — | |
| 40 | State (KV) | "Bucket initialization: control-plane creates all buckets idempotently (`nats.KeyValueStoreOrCreate`)" | spec-control-plane: "Ensures KV buckets exist" | REFLECTED | — | |
| 41 | State (KV) | "Session state JSON: id, projectId, branch, worktree, cwd, name, state, stateSince, model, extraFlags, capabilities, pendingControls, usage, replayFromSeq" | spec-state-schema: SessionState JSON has all fields | REFLECTED | — | |
| 42 | State (KV) | "Project state JSON: id, slug, userSlug, hostSlug, name, gitUrl, gitIdentityId, status, sessionCount, worktrees, createdAt, lastActiveAt" | spec-state-schema: ProjectState JSON matches incl. gitIdentityId | REFLECTED | — | |
| 43 | Lifecycle Events | "session_created: `{type, sessionId, ts}`" | spec-state-schema: session_created payload documented (has extra `branch` field — spec is superset) | REFLECTED | — | |
| 44 | Lifecycle Events | "session_stopped: `{type, sessionId, exitCode, ts}`" | spec-state-schema: session_stopped payload — missing `exitCode` | GAP | SPEC→FIX | ADR specifies `exitCode: 0` in session_stopped; spec-state-schema omits it |
| 45 | Lifecycle Events | "session_restarting: `{type, sessionId, ts}`" | spec-state-schema: Lifecycle Event Payloads | GAP | SPEC→FIX | Payload not documented in spec-state-schema |
| 46 | Lifecycle Events | "session_resumed: `{type, sessionId, ts}`" | spec-state-schema: Lifecycle Event Payloads | GAP | SPEC→FIX | Payload not documented in spec-state-schema |
| 47 | Lifecycle Events | "session_failed: `{type, sessionId, error, ts}`" | spec-state-schema: has session_job_failed but not standalone session_failed | GAP | SPEC→FIX | Base session_failed (without jobId) payload missing from spec-state-schema |
| 48 | Lifecycle Events | "debug_attached: `{type, sessionId, ts}`; debug_detached: `{type, sessionId, ts}`" | spec-state-schema: Lifecycle Event Payloads | GAP | SPEC→FIX | Neither debug_attached nor debug_detached payloads are documented |
| 49 | Slug System | "slugify() algorithm: Lowercase → NFD → strip combining → replace non-[a-z0-9] → trim → truncate 63" | spec-common: Slug algorithm — matching steps | REFLECTED | — | |
| 50 | Slug System | "Fallback: `u-{6 base32}` for users, `p-{6}` / `h-{6}` / `c-{6}`" | spec-common: ValidateOrFallback — matching prefix + base32 | REFLECTED | — | |
| 51 | Slug System | "mclaude-common module layout: pkg/slug + pkg/subj" | spec-common: matching layout | REFLECTED | — | |
| 52 | Slug System | "TypeScript mirrors: src/lib/slug.ts + src/lib/subj.ts" | spec-web: TypeScript Slug/Subject Mirrors section | REFLECTED | — | |
| 53 | Slug System | "Postgres: users.slug UNIQUE; hosts UNIQUE(user_id, slug); projects UNIQUE(user_id, host_id, slug)" | spec-state-schema: matching constraints | REFLECTED | — | |
| 54 | Slug System | "Slug validation at ingress: HTTP 400 with `{code:'invalid_slug', reason:'reserved_word|charset|length', field:'slug'}`" | spec-control-plane: error handling section | REFLECTED | — | Spec implies 400 on validation failure though doesn't prescribe the exact JSON shape; close enough for implementation |
| 55 | Session Agent | "16MB scanner buffer for large events" | spec-session-agent: "16 MB scanner buffer to handle large events" | REFLECTED | — | |
| 56 | Session Agent | "Pull consumers: command consumer (`sa-cmd-{uslug}-{pslug}`) and control consumer (`sa-ctl-{uslug}-{pslug}`)" | spec-session-agent: both consumers documented with matching names | REFLECTED | — | |
| 57 | Session Agent | "Permission policies: auto, managed, allowlist" | spec-session-agent: 4 policies (managed, auto, allowlist, strict-allowlist) | REFLECTED | — | Spec adds strict-allowlist from ADR-0034 |
| 58 | Session Agent | "Startup/Recovery: Read NATS KV → set state to 'restarting' → resume with `--resume`" | spec-session-agent: Resumption section matches | REFLECTED | — | |
| 59 | Session Agent | "Graceful Shutdown: 1. Write 'updating' to KV. 2. Cancel command consumer. 3. Drain core NATS. 4. Keep control consumer. 5. Poll idle + zero agents. 6. Cancel control consumer. 7. Publish session_upgrading. 8. Exit." | spec-session-agent: Graceful Shutdown section — 8 steps match exactly | REFLECTED | — | |
| 60 | Session Agent | "terminationGracePeriodSeconds: 86400 (24h)" | spec-helm: `sessionAgent.terminationGracePeriodSeconds: 86400` | REFLECTED | — | |
| 61 | Session Agent | "Worktree: branch optional — derived from name via slugification. If both omitted, `session-{shortId}`" | spec-session-agent: Creation step 3: "Derive branch name (from request, slugified session name, or `session-{shortId}`)" | REFLECTED | — | |
| 62 | Session Agent | "Debug unix socket per session at `/tmp/mclaude-session-{id}.sock`" | spec-session-agent: "debug socket at `/tmp/mclaude-session-{sessionId}.sock`"; spec-cli: attach command | REFLECTED | — | |
| 63 | Session Agent | "JWT refresh: background goroutine checks `exp` every 60s, calls `POST /auth/refresh` when TTL < 15 minutes" | spec-session-agent: Daemon JWT Refresh — "checks its host JWT TTL every 60 seconds. When remaining TTL falls below 15 minutes" | REFLECTED | — | |
| 64 | Reliability | "Claude process crash: session-agent detects exit, publishes lifecycle event, auto-restarts with `--resume`" | spec-session-agent: error handling table | GAP | SPEC→FIX | Spec lists "Claude process fails to start → session_failed" but doesn't document mid-session crash detection + auto-restart with --resume |
| 65 | Reliability | "NATS unavailability: buffer state changes in memory, flush on reconnect. Claude processes continue running." | spec-session-agent: "state changes buffered in memory during outage and flushed on reconnect. Claude processes continue running." | REFLECTED | — | |
| 66 | Reliability | "Agent liveness: detected via NATS `$SYS` presence events, not heartbeats" | spec-state-schema: "$SYS is the only liveness signal"; spec-session-agent: "no periodic heartbeat publish" | REFLECTED | — | |
| 67 | Reliability | "JSONL cleanup: daily job deletes files >90 days, purges for sessions not in KV" | spec-session-agent: "deletes JSONL files older than 90 days... purges session files for sessions not present in mclaude-sessions KV" | REFLECTED | — | |
| 68 | Control Plane | "Auth endpoints: POST /auth/login, POST /auth/refresh" | spec-control-plane: both endpoints documented | REFLECTED | — | |
| 69 | Control Plane | "SSO endpoints: POST /api/providers/{id}/connect → {redirectUrl}; GET /auth/providers/{id}/callback" | spec-control-plane: both documented | REFLECTED | — | |
| 70 | Control Plane | "SCIM 2.0: POST/PUT/DELETE/GET /scim/v2/Users" | spec-control-plane: all 4 SCIM endpoints documented | REFLECTED | — | |
| 71 | Control Plane | "Admin endpoints: POST /admin/users, GET /admin/users, DELETE /admin/users/{id}" | spec-control-plane: all admin endpoints documented | REFLECTED | — | |
| 72 | Control Plane | "Projects: POST/GET/GET/{pslug}/DELETE with ?purge=true" | spec-control-plane: all project endpoints documented | REFLECTED | — | |
| 73 | Control Plane | "Host management: GET hosts, POST hosts/code, GET hosts/code/{code}, POST hosts/register, PUT/DELETE hosts/{hslug}" | spec-control-plane: all host endpoints documented | REFLECTED | — | |
| 74 | Control Plane | "Break-glass admin: port 9091, loopback-only, GET /metrics only" | spec-control-plane: "Loopback Port (9091) — hosts only Prometheus metrics" | REFLECTED | — | |
| 75 | Control Plane | "User deprovision: Revoke NATS JWT → DELETE Postgres cascade → publish NATS delete to controllers" | spec-control-plane: DELETE /admin/users/{id} matches the flow | REFLECTED | — | |
| 76 | Control Plane | "Project provisioning: 6-step flow via NATS request/reply, PROVISION_TIMEOUT_SECONDS=10" | spec-control-plane: Project Creation Flow (7 steps); PROVISION_TIMEOUT_SECONDS=10 | REFLECTED | — | |
| 77 | Control Plane | "Controller timeout → 503 with `{error: 'host {hslug} unreachable'}`" | spec-control-plane: "returns 503 with `{error: 'host {hslug} unreachable'}`" | REFLECTED | — | |
| 78 | Control Plane | "Postgres schema: users, hosts, projects, oauth_connections + idempotent DDL" | spec-state-schema: all 4 tables; spec-control-plane: "idempotent DDL" | REFLECTED | — | |
| 79 | Pod Structure | "Pod: session-agent, config-sync, optional dockerd-rootless" | spec-session-agent: Pod Structure (Multi-Container) — 3 containers match | REFLECTED | — | |
| 80 | Pod Structure | "/data/ layout: repo/, worktrees/, shared-memory/, projects/" | spec-session-agent: data layout documented | REFLECTED | — | |
| 81 | Pod Structure | "3-tier storage: User ConfigMap+Secret, Project PVC, Session KV, Home emptyDir" | spec-session-agent + spec-state-schema: all tiers documented | REFLECTED | — | |
| 82 | Config | "CLAUDE.md three-tier: Global /etc/claude-code/, User ~/.claude/, Project {worktree}/" | spec-session-agent: CLAUDE.md three-tier table matches | REFLECTED | — | |
| 83 | Config | "Guard hooks at /etc/claude-code/hooks/guard.sh — block git checkout/switch, apt install, /etc/claude-code/ modification, rm -rf critical paths" | spec-session-agent: Guard hooks — matching blocks | REFLECTED | — | |
| 84 | Config | "Registry mirrors: mirrors.json, types npm/pypi/go/nix" | spec-session-agent: Registry Mirror System — matching types | REFLECTED | — | |
| 85 | Terminal | "PTY sessions via `creack/pty`, raw bytes, 4KB chunks" | spec-session-agent: "publishes terminal output in 4 KB chunks" | REFLECTED | — | |
| 86 | Web SPA | "Event rendering: stream_event, assistant, tool_use, tool_progress, control_request, session_state_changed, init, result, parent_tool_use_id nesting" | spec-web: Event Rendering table — all types match | REFLECTED | — | |
| 87 | Web SPA | "Event replay: re-subscribe from `max(lastSeenSeq + 1, replayFromSeq)`. Deduplicate by JetStream seq." | spec-web: Event Replay section matches | REFLECTED | — | |
| 88 | Web SPA | "Background reconnect: `document.addEventListener('visibilitychange', ...)` on mobile" | spec-web: Background Reconnect section — matching code | REFLECTED | — | |
| 89 | Web SPA | "Model/effort switching: `set_model` and `set_max_thinking_tokens` control requests mid-session" | spec-web: Model/Effort Switching section | REFLECTED | — | |
| 90 | Web SPA | "File/image uploads: base64 in content array. Max ~20MB." | spec-web: "base64 in the user message content array... Max ~20MB" | REFLECTED | — | |
| 91 | Web SPA | "Routes: React Router v6 parametric segments. Display names in UI; slugs in path." | spec-web: "React Router v6 with parametric segments" | REFLECTED | — | |
| 92 | NATS Auth | "JWT expiry: 8h. Refresh threshold: 15min. SPA checks TTL every 60s." | spec-control-plane: JWT_EXPIRY_SECONDS=28800 (8h); spec-web: (no mention of TTL check/refresh) | GAP | SPEC→FIX | spec-web does not document SPA-side JWT expiry handling (check TTL every 60s, refresh when < 15min). A developer implementing the SPA from spec-web alone would miss JWT lifecycle management. |
| 93 | Health Probes | "session-agent: `--health` (process + NATS); `--ready` (NATS + can spawn claude)" | spec-session-agent: "--health" and "--ready" documented | REFLECTED | — | |
| 94 | Health Probes | "control-plane: /health never checks NATS; /ready checks Postgres only" | spec-control-plane: "/health — never checks NATS"; "/readyz — checks Postgres only — NATS outage must not mark pod unready" | REFLECTED | — | |
| 95 | Helm | "mclaude-cp + mclaude-worker chart split per ADR-0035" | spec-helm: two charts documented | REFLECTED | — | |
| 96 | Helm | "init-keys Job: generates operator + account NKeys, idempotent" | spec-helm: init-keys Job documented | REFLECTED | — | |
| 97 | Upgrade | "/upgrade-claude skill: fetch changelog, analyze breaking changes, update Dockerfile pin, PR on upgrade/claude-{version} branch" | spec-session-agent: /upgrade-claude Skill section | REFLECTED | — | |

### Summary

- Reflected: 89
- Gap: 7
- Partial: 1

### GAP and PARTIAL details

**GAP [SPEC→FIX] #1** — spec-state-schema: `mclaude.users.{uslug}.api.projects.updated` broadcast subject missing from NATS Subjects user-level table

ADR (NATS Subject Structure / API — Project Management):
> `mclaude.users.{uslug}.api.projects.updated   → control-plane broadcasts (project state changed)`

The user-level subjects table in spec-state-schema only lists `mclaude.users.{uslug}.quota`. The `…api.projects.updated` broadcast subject (used by control-plane to notify the SPA of project state changes) is absent. Note: `spec-common` has a `UserAPIProjectsUpdated` helper, confirming this subject is real — but the canonical subject reference (spec-state-schema) omits it.

**Fix**: Add a row to spec-state-schema's "User-level subjects" table:
```
| `mclaude.users.{uslug}.api.projects.updated` | control-plane | SPA | Project state changed broadcast |
```

---

**GAP [SPEC→FIX] #2** — spec-state-schema: `session_stopped` lifecycle event payload missing `exitCode` field

ADR (Session Agent Lifecycle Events):
> `{"type": "session_stopped", "sessionId": "abc-123", "exitCode": 0, "ts": "..."}`

spec-state-schema documents:
> `{ "type": "session_stopped", "sessionId": "...", "ts": "RFC3339" }`

The `exitCode` field is absent from the spec payload.

**Fix**: Add `"exitCode": 0` to the session_stopped payload in spec-state-schema.

---

**GAP [SPEC→FIX] #3** — spec-state-schema: missing lifecycle event payloads for session_restarting, session_resumed, session_failed (standalone), debug_attached, debug_detached

ADR (Session Agent Lifecycle Events) defines these 5 payloads:
> `{"type": "session_restarting", "sessionId": "abc-123", "ts": "..."}`
> `{"type": "session_resumed", "sessionId": "abc-123", "ts": "..."}`
> `{"type": "session_failed", "sessionId": "abc-123", "error": "...", "ts": "..."}`
> `{"type": "debug_attached", "sessionId": "abc-123", "ts": "..."}`
> `{"type": "debug_detached", "sessionId": "abc-123", "ts": "..."}`

spec-state-schema's Lifecycle Event Payloads section documents session_created, session_stopped, session_permission_denied, session_job_complete, session_job_paused, session_job_cancelled, session_job_failed — but not these 5 base lifecycle events from ADR-0042.

Note: spec-session-agent references them in prose ("Published on session creation, stop, resume, restart, upgrade, failure...") but without payload schemas. The canonical payload reference is spec-state-schema, which is incomplete.

**Fix**: Add payload schemas for all 5 events to spec-state-schema's Lifecycle Event Payloads section.

---

**GAP [SPEC→FIX] #4** — spec-web: missing JWT refresh/expiry handling

ADR (NATS Authentication):
> `JWT expiry: 8h (configurable via JWT_EXPIRY_SECONDS). Refresh threshold: 15min (configurable via JWT_REFRESH_THRESHOLD_SECONDS). SPA checks TTL every 60s.`

spec-web documents initial NATS connection using JWT from login but says nothing about JWT lifecycle: no TTL check interval, no refresh threshold, no call to `POST /auth/refresh`. A developer implementing the SPA from spec-web alone would not know to add JWT refresh.

**Fix**: Add a "JWT Lifecycle" or "Authentication Refresh" section to spec-web documenting: check JWT TTL every 60s, refresh via `POST /auth/refresh` when remaining TTL < 15 minutes, reconnect NATS with new credentials.

---

**GAP [SPEC→FIX] #5** — spec-session-agent: missing Claude process crash → auto-restart with `--resume`

ADR (Reliability):
> `Claude process crash: session-agent detects exit, publishes lifecycle event, auto-restarts with --resume.`

spec-session-agent's error handling table lists "Session create — Claude process fails to start: `session_failed` lifecycle event" (initial start failure) but does not document mid-session crash detection and automatic restart with `--resume`. This is a distinct runtime behavior from startup failure.

**Fix**: Add an error handling row: "Claude process exits unexpectedly mid-session: detect exit, publish `session_failed` lifecycle event, auto-restart with `--resume {sessionId}`. If restart fails after N attempts, mark session as `failed`."

---

**PARTIAL [SPEC→FIX] #1** — spec-control-plane: cross-user HTTP access control not explicitly stated

ADR (Decisions table + Error Handling):
> `Cross-user URL access: Hard 403 when JWT sub ≠ URL {uslug}`
> `middleware compares JWT sub claim's uslug with URL's {uslug}. Mismatch → 403.`

spec-control-plane (Internal Behavior / Authentication):
> `The auth middleware on protected routes decodes and validates the JWT against the account public key, extracts the user slug + host slug, and rejects requests targeting a different host than the JWT was issued for.`

The spec explicitly mentions host-scope rejection but only implicitly covers user-scope rejection (user slug is extracted but the explicit user-slug-vs-URL comparison and 403 is not stated). Since the JWT's NATS permissions include the user slug, a developer might rely on NATS-level enforcement and skip the HTTP-level user-slug check, which would leave HTTP-only endpoints (like project CRUD) without user-scope validation.

**Fix**: Change "rejects requests targeting a different host" to "rejects requests targeting a different user or host" (i.e., JWT user slug ≠ URL `{uslug}` → 403, JWT host slug ≠ request host → 403).
