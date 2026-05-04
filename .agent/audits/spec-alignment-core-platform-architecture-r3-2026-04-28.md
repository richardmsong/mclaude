## Run: 2026-04-28T12:00:00Z

ADR: `docs/adr-0042-core-platform-architecture.md` (status: accepted)
Round: 3 (R1: 18 GAP + 15 PARTIAL → fixed; R2: 7 GAP + 1 PARTIAL → fixed)

Specs evaluated:
- `docs/spec-state-schema.md`
- `docs/mclaude-control-plane/spec-control-plane.md`
- `docs/mclaude-session-agent/spec-session-agent.md`
- `docs/mclaude-common/spec-common.md`
- `docs/mclaude-web/spec-web.md`
- `docs/mclaude-cli/spec-cli.md`
- `docs/charts-mclaude/spec-helm.md`

### Phase 1 — ADR → Spec (forward pass)

| # | ADR section | ADR text | Spec location | Verdict | Direction | Notes |
|---|------------|----------|---------------|---------|-----------|-------|
| 1 | Decisions table | "Headless via `--print --verbose --output-format stream-json --input-format stream-json --include-partial-messages`" | spec-session-agent §Session Lifecycle > Creation step 10 | REFLECTED | — | Spec adds `--replay-user-messages` (spec evolution); ADR flags all present |
| 2 | Decisions table | "Event bus: NATS JetStream" | spec-state-schema §NATS JetStream Streams | REFLECTED | — | |
| 3 | Decisions table | "Raw Claude Code stream-json — no envelope, no protobuf translation" | spec-state-schema §MCLAUDE_EVENTS, spec-session-agent §Event Routing | REFLECTED | — | |
| 4 | Decisions table | "NATS KV (sessions, projects, hosts, job-queue) + Postgres (users, hosts, projects, oauth_connections)" | spec-state-schema §Postgres + §NATS KV Buckets | REFLECTED | — | All 4 KV buckets and 4 Postgres tables documented |
| 5 | Decisions table | "Single Go binary (`mclaude-session-agent`) per project pod" | spec-session-agent §Role | REFLECTED | — | |
| 6 | Decisions table | "Go service (`mclaude-control-plane`), K8s-free per ADR-0035" | spec-control-plane §Role | REFLECTED | — | |
| 7 | Decisions table | "React 18 SPA, mobile browser first" | spec-web §Role | REFLECTED | — | |
| 8 | Decisions table | "nginx reverse proxy — no auth logic, no routing decisions" | spec-helm §Ingress | REFLECTED | — | |
| 9 | Decisions table | "Typed literals between every slug: `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}`" | spec-common §subj, spec-state-schema §NATS Subjects | REFLECTED | — | |
| 10 | Decisions table | "Slug charset: `[a-z0-9][a-z0-9-]{0,62}`" | spec-common §Validation | REFLECTED | — | |
| 11 | Decisions table | "Blocklist of 10 words: users, hosts, projects, sessions, clusters, api, events, lifecycle, quota, terminal" | spec-common §Validation | REFLECTED | — | Exact 10-word list matches |
| 12 | Decisions table | "All slugs system-computed from display names, never user-picked. Immutable after creation." | spec-state-schema §users, spec-common §slug | REFLECTED | — | |
| 13 | Decisions table | "Display name: free-form UTF-8, max 128, mutable. Slug: auto-derived, immutable." | spec-state-schema §users (name + slug columns) | REFLECTED | — | |
| 14 | Decisions table | "Typed Go wrappers (`type UserSlug string`, etc.) in `mclaude-common/pkg/subj` — raw strings are compile-time errors" | spec-common §subj | REFLECTED | — | |
| 15 | Decisions table | "`mclaude-common/` at repo root, wired via `go.work`" | spec-common §Role | REFLECTED | — | |
| 16 | Decisions table | "User slug: `slugify(name-or-local-part)-{domain.split('.')[0]}` at creation. Collision → numeric suffix" | spec-common §DeriveUserSlug, spec-state-schema §users.slug | REFLECTED | — | |
| 17 | Decisions table | "Project/host/cluster slug: `slugify(display_name)` at creation; collision within scope → numeric suffix. Immutable." | spec-state-schema §projects, §hosts | REFLECTED | — | |
| 18 | Decisions table | "HTTP URL scheme: `/api/users/{uslug}/projects/{pslug}/...`" | spec-control-plane §HTTP Endpoints | REFLECTED | — | All routes match pattern |
| 19 | Decisions table | "KV key separator: Uniform `.` across all buckets" | spec-state-schema §KV Buckets, spec-common §KV key helpers | REFLECTED | — | |
| 20 | Decisions table | "Cross-user URL access: Hard 403 when JWT `sub` ≠ URL `{uslug}`" | spec-control-plane §Authentication and JWT Signing | REFLECTED | — | |
| 21 | Decisions table | "Git worktrees: One worktree per branch per project. `joinWorktree: true` to share." | spec-session-agent §Worktree Management | REFLECTED | — | |
| 22 | Decisions table | "Tool installation: Nix single-user mode, per-project PVC at `/nix/`, `pkg` shim" | spec-session-agent §Deployment (Standalone Mode) | REFLECTED | — | |
| 23 | Decisions table | "Claude CLI installation: Native binary via `claude install --version {pinned}` in Dockerfile. Image based on `node:22-alpine`." | spec-session-agent §Deployment (Standalone Mode) | REFLECTED | — | "Alpine-based with Node.js 22, Claude Code (native binary)" |
| 24 | Component Topology | "mclaude-cli: Debug attach tool. Thin text REPL over unix socket" | spec-cli §Role | REFLECTED | — | |
| 25 | Component Topology | "nginx ingress: Dumb reverse proxy. No routing logic, no auth." | spec-helm §Ingress | REFLECTED | — | |
| 26 | Data Flow | Steps 1-9 (user input → SPA → NATS → session agent → Claude → NATS → SPA) | spec-state-schema §NATS Subjects, spec-session-agent §Event Routing, spec-web §Event Rendering | REFLECTED | — | All subjects and data paths documented |
| 27 | Claude Code Integration | "`--resume {sessionId}` for session resume after pod restart" | spec-session-agent §Session Lifecycle > Resumption | REFLECTED | — | |
| 28 | Stream-JSON Protocol | Output event types: system/init, session_state_changed, assistant, stream_event, control_request, tool_progress, result, clear, compact_boundary | spec-web §Event Rendering, spec-session-agent §Event Routing | REFLECTED | — | All event types referenced where consumed |
| 29 | Stream-JSON Protocol | Input types: user messages, control_response, control_request (interrupt, set_model, set_max_thinking_tokens) | spec-web §Model/Effort Switching, spec-session-agent §Permission Policy | REFLECTED | — | |
| 30 | Subagent Events | "each event carries `parent_tool_use_id` linking it to the Agent tool_use" | spec-web §Event Rendering, spec-cli §Accumulator behavior | REFLECTED | — | |
| 31 | NATS Subjects | API session commands: create, delete, input, control, restart | spec-state-schema §NATS Subjects | PARTIAL | SPEC→FIX | `sessions.restart` is listed in MCLAUDE_API filter but has no dedicated row in the NATS Subjects table — payload (optional `extraFlags`, `sessionSlug`) is undocumented |
| 32 | NATS Subjects | API project management: provision, create, update, delete, projects.updated | spec-state-schema §NATS Subjects | REFLECTED | — | All 5 subjects have rows |
| 33 | NATS Subjects | "Events: `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.events.{sslug}`" | spec-state-schema §NATS Subjects | REFLECTED | — | |
| 34 | NATS Subjects | "Lifecycle: `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.lifecycle.{sslug}`" | spec-state-schema §NATS Subjects | REFLECTED | — | |
| 35 | Terminal I/O | "`mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.terminal.{termId}.output` → raw PTY output bytes / `…terminal.{termId}.input` → raw keyboard input bytes" | spec-state-schema §NATS Subjects, spec-session-agent §Terminal Sessions | GAP | SPEC→FIX | spec-state-schema collapses all terminal subjects into a single `api.terminal.*` wildcard row. The per-terminal I/O subject pattern (`terminal.{termId}.output`, `terminal.{termId}.input`) is not documented in any spec. spec-session-agent says "bridge I/O through core NATS subjects" but omits the subject format. |
| 36 | Terminal I/O | "Not JetStream — raw terminal I/O is ephemeral, no replay needed. Use core NATS pub/sub for low latency." | spec-session-agent §Terminal Sessions ("core NATS") | REFLECTED | — | |
| 37 | Terminal API | create, delete, resize subjects | spec-session-agent §NATS Subjects (Subscribe) | REFLECTED | — | Exact subjects listed |
| 38 | Quota | "`mclaude.users.{uslug}.quota` → broadcast signal, not request/reply" | spec-state-schema §NATS Subjects, spec-session-agent §NATS Subjects (Publish) | REFLECTED | — | |
| 39 | JetStream Streams | MCLAUDE_EVENTS: filter `*.events.*`, 30d; MCLAUDE_LIFECYCLE: filter `*.lifecycle.*`, 30d; MCLAUDE_API: filter `*.api.sessions.>`, 1h | spec-state-schema §JetStream Streams | REFLECTED | — | All 3 streams with matching filters and retention |
| 40 | NATS message size | "`max_payload: 8388608` (8MB). If event exceeds 8MB, agent truncates `content` field and sets `truncated: true`." | spec-session-agent §Event Routing, spec-state-schema §NATS Server Configuration | REFLECTED | — | |
| 41 | Session State KV | Full JSON schema: id, projectId, branch, worktree, cwd, name, state, stateSince, model, extraFlags, capabilities, pendingControls, usage, replayFromSeq | spec-state-schema §mclaude-sessions | REFLECTED | — | Spec adds `joinWorktree` (evolution) |
| 42 | Session State KV | "`state` maps directly from stream-json `session_state_changed` events: idle, running, requires_action" | spec-state-schema §mclaude-sessions | REFLECTED | — | Spec extends with agent-managed states (updating, restarting, failed) — consistent with ADR's other sections |
| 43 | Session State KV | "`pendingControls` is a map of request_id → control_request. No timeout." | spec-state-schema §mclaude-sessions, spec-session-agent §Permission Policy | REFLECTED | — | |
| 44 | Session State KV | "`capabilities` populated from `init` event. Client reads from KV." | spec-session-agent §Session Lifecycle > Creation | REFLECTED | — | |
| 45 | Session State KV | "`replayFromSeq` updated on `/clear` and compaction" | spec-web §Event Replay, spec-session-agent §Compact Boundary Tracking | REFLECTED | — | |
| 46 | Project State KV | Full JSON schema: id, slug, userSlug, hostSlug, name, gitUrl, gitIdentityId, status, sessionCount, worktrees, createdAt, lastActiveAt | spec-state-schema §mclaude-projects | REFLECTED | — | |
| 47 | Hosts KV | "`mclaude-hosts` bucket, key `{uslug}.{hslug}` → Host JSON (online status, type, role)" | spec-state-schema §mclaude-hosts | REFLECTED | — | |
| 48 | Bucket initialization | "control-plane creates all buckets idempotently on startup" | spec-control-plane §NATS KV Buckets | REFLECTED | — | Lists all 4 buckets |
| 49 | Lifecycle Events | "session_created, session_stopped (exitCode), session_restarting, session_resumed, session_failed (error), debug_attached, debug_detached" | spec-state-schema §Lifecycle Event Payloads | REFLECTED | — | All 7 ADR lifecycle event types have payloads |
| 50 | Graceful Shutdown | "Publish `session_upgrading` lifecycle event per session" (step 7) | spec-session-agent §Graceful Shutdown step 7 | GAP | SPEC→FIX | `session_upgrading` is referenced in both ADR shutdown step 7 and spec-session-agent shutdown step 7, but spec-state-schema §Lifecycle Event Payloads has no entry for it. A developer reading spec-state-schema as the canonical payload reference would not know this event exists. |
| 51 | Slug System | "slugify(): Lowercase → NFD → strip combining marks → replace non-[a-z0-9] with `-` → trim → truncate to 63" | spec-common §slug algorithm | REFLECTED | — | Steps match exactly |
| 52 | Slug System | "Fallback: emit `u-{6 base32 chars}` / `p-{6}` / `h-{6}` / `c-{6}` from first 30 bits of UUID" | spec-common §ValidateOrFallback | REFLECTED | — | Spec says "first 4 bytes" (≈30 usable bits for 6 base32 chars); functionally equivalent |
| 53 | Slug System | TypeScript mirrors: `src/lib/slug.ts`, `src/lib/subj.ts` | spec-web §TypeScript Slug/Subject Mirrors | REFLECTED | — | |
| 54 | Slug System | Postgres slug columns: users.slug UNIQUE, hosts (user_id, slug) UNIQUE, projects (user_id, host_id, slug) UNIQUE | spec-state-schema §Postgres tables | REFLECTED | — | |
| 55 | Slug System - Error Handling | "Slug validation at ingress: control-plane returns HTTP 400 with `{code:\"invalid_slug\", reason:\"reserved_word|charset|length\", field:\"slug\"}`" | spec-control-plane §Error Handling | GAP | SPEC→FIX | No spec documents this response format. Relevant for `POST /admin/clusters` where slug is user-provided. Spec lists "Duplicate cluster slug → 409" but not invalid slug → 400. |
| 56 | Session Agent — Core Loop | "stdinCh channel for serialization, 16MB stdout scanner buffer, pull consumers sa-cmd-{uslug}-{pslug} and sa-ctl-{uslug}-{pslug}" | spec-session-agent §Event Routing, §NATS JetStream Streams | REFLECTED | — | |
| 57 | Session Agent — Session Operations | "restart: Kill process → if `extraFlags` in payload, update KV → relaunch with `--resume`" | spec-session-agent §Session Lifecycle > Restart | REFLECTED | — | |
| 58 | Session Agent — Permission Handling | "Three policies: auto, managed, allowlist" | spec-session-agent §Permission Policy | REFLECTED | — | Spec adds strict-allowlist (evolution) |
| 59 | Session Agent — Startup/Recovery | "Step 2: Set all session KV entries to state: 'restarting', clear pendingControls. Step 3: Publish session_restarting lifecycle events." | spec-session-agent §Session Lifecycle > Resumption | GAP | SPEC→FIX | Resumption section says "clear pending controls, resume" but does NOT mention setting KV state to 'restarting' or publishing `session_restarting` lifecycle events. A developer would resume without notifying clients of the recovery state. |
| 60 | Session Agent — Startup/Recovery | "Step 7: Sessions that fail to start within 30s: mark state: 'failed', publish session_failed" | spec-session-agent §Session Lifecycle > Resumption | GAP | SPEC→FIX | Resumption section has no timeout for failed resume. Developer would not mark stuck sessions as failed during recovery. |
| 61 | Session Agent — Graceful Shutdown | 8-step shutdown sequence (updating KV → cancel cmd consumer → drain NATS → keep ctl consumer → poll idle → cancel ctl → publish session_upgrading → exit) | spec-session-agent §Graceful Shutdown | REFLECTED | — | All 8 steps match |
| 62 | Session Agent — Worktrees | Branch slugification, joinWorktree table, bare repo at /data/repo | spec-session-agent §Worktree Management | REFLECTED | — | |
| 63 | Session Agent — Debug Attach | "unix socket at `/tmp/mclaude-session-{id}.sock`" | spec-session-agent §Unix Sockets, spec-cli §Commands > attach | REFLECTED | — | |
| 64 | Session Agent — Laptop Mode | "Session-agent runs with `--daemon --host {hslug}`, JWT refresh every 60s, refresh when TTL < 15min" | spec-session-agent §Daemon Mode, §Daemon: JWT Refresh | REFLECTED | — | |
| 65 | Control Plane — Project Provisioning | "NATS request to `…api.projects.provision`, timeout 10s" | spec-control-plane §NATS Subjects (Publishes), §Project Creation Flow | REFLECTED | — | |
| 66 | Control Plane — All HTTP endpoints | Auth, Users, Projects, Hosts, Clusters, SCIM, Metrics endpoints | spec-control-plane §HTTP Endpoints | REFLECTED | — | All ADR endpoints have matching spec entries |
| 67 | Control Plane — Postgres schema | 4 tables: users, hosts, projects, oauth_connections | spec-state-schema §Postgres | REFLECTED | — | |
| 68 | Control Plane — User provisioning flow | "INSERT → default local host → K8s namespace on first cluster project" | spec-state-schema §Default machine host, spec-control-plane §Startup Sequence | REFLECTED | — | |
| 69 | Control Plane — User deprovision flow | "Revoke JWT → NATS terminates connections → DELETE cascade → publish to controllers" | spec-control-plane §HTTP Endpoints (DELETE /admin/users/{id}) | REFLECTED | — | |
| 70 | Control Plane — Project provisioning flow | "Postgres row → KV write → NATS request to controller → reply" | spec-control-plane §Project Creation Flow | REFLECTED | — | 7-step flow matches |
| 71 | Pod Structure | "session-agent, config-sync, dockerd-rootless containers" | spec-session-agent §Pod Structure | REFLECTED | — | |
| 72 | 3-Tier Storage | "User (ConfigMap+Secret), Project (PVC), Session (KV), Home (emptyDir)" | spec-session-agent §Deployment, spec-state-schema §K8s Resources | REFLECTED | — | |
| 73 | Home Directory + Config Sync | "emptyDir seeded from Secret+ConfigMap, config-sync sidecar watches inotify" | spec-session-agent §Pod Structure (config-sync container) | REFLECTED | — | |
| 74 | Git credential helper setup | "CredentialManager.Setup: symlink, merge configs, gh/glab auth setup-git" | spec-session-agent §Credential Management | REFLECTED | — | Full 7-step setup described |
| 75 | Auto-Memory Sharing | "Background loop symlinks worktree memory dirs to /data/shared-memory/" | spec-session-agent §Auto-Memory Sharing | REFLECTED | — | "every 5s" loop documented |
| 76 | CLAUDE.md Three-Tier | "Global (/etc/claude-code/), User (~/.claude/), Project ({worktree}/)" | spec-session-agent §Managed Platform Config | REFLECTED | — | Matching 3-tier table |
| 77 | Guard Hooks | "Block git checkout/switch, apt install, /etc/claude-code/ modification, rm -rf critical paths" | spec-session-agent §Managed Platform Config (Guard hooks) | REFLECTED | — | All 4 guard rules listed |
| 78 | Registry Mirror System | "mirrors.json, supported types: npm, pypi, go, nix" | spec-session-agent §Registry Mirror System | REFLECTED | — | |
| 79 | Entrypoint | Environment variables: GIT_URL, USER_ID, USER_SLUG, HOST_SLUG, PROJECT_ID, PROJECT_SLUG, NATS_URL, DOCKER_ENABLED, CLAUDE_CODE_OAUTH_TOKEN, HTTPS_PROXY | spec-session-agent §Configuration | REFLECTED | — | All env vars mapped to config knobs |
| 80 | Terminal Access | "Two session types: Claude sessions (JSON) and terminal sessions (raw PTY bytes)" | spec-session-agent §Terminal Sessions | REFLECTED | — | |
| 81 | Terminal Access | "4KB chunks, xterm.js rendering" | spec-session-agent §Terminal Sessions ("4 KB chunks"), spec-web §Dependencies (xterm.js) | REFLECTED | — | |
| 82 | Web SPA | "NATS KV watches for live updates" | spec-web §State Management | REFLECTED | — | |
| 83 | Web SPA | "Event replay from max(lastSeenSeq+1, replayFromSeq), deduplicate by JetStream seq" | spec-web §Event Replay | REFLECTED | — | |
| 84 | Web SPA | "Background reconnect on visibilitychange" | spec-web §Background Reconnect | REFLECTED | — | Code snippet matches |
| 85 | Web SPA | "Model/effort switching via set_model and set_max_thinking_tokens control requests" | spec-web §Model/Effort Switching | REFLECTED | — | |
| 86 | Web SPA | "Cost tracking from result events, accumulated in KV" | spec-web §Cost Tracking | REFLECTED | — | |
| 87 | Web SPA | "File/image uploads: base64 in user message content array, max ~20MB" | spec-web §File/Image Uploads | REFLECTED | — | |
| 88 | Web SPA | "Routes: `/api/users/{uslug}/projects/{pslug}/sessions/{sslug}`. React Router v6." | spec-web §Routing | REFLECTED | — | |
| 89 | NATS Authentication | "Login: validate → issue NATS User JWT scoped to user.> → return {natsUrl, jwt, nkeySeed}" | spec-control-plane §Authentication and JWT Signing | REFLECTED | — | |
| 90 | NATS Authentication | "JWT expiry: 8h. Refresh threshold: 15min. SPA checks TTL every 60s." | spec-web §JWT Refresh, spec-control-plane §env JWT_EXPIRY_SECONDS | REFLECTED | — | |
| 91 | nginx Ingress | "/nats → NATS WS, /auth /api /scim → control-plane, / → SPA" | spec-helm §Ingress | REFLECTED | — | |
| 92 | Health Probes | "session-agent: exec `--health` (liveness), exec `--ready` (readiness)" | spec-session-agent §Health and Readiness | REFLECTED | — | |
| 93 | Health Probes | "control-plane readiness: `path: /ready`, checks Postgres only" | spec-control-plane §HTTP Endpoints | GAP | ADR→FIX | ADR says `path: /ready`, spec says `/readyz`. `/readyz` is the K8s convention and likely correct. The spec also adds `/healthz` as a liveness alias. |
| 94 | Reliability | "Postgres unavailability: retry with backoff. Login returns 503." | spec-control-plane §Error Handling | REFLECTED | — | |
| 95 | Reliability | "NATS unavailability: buffer state changes in memory, flush on reconnect" | spec-session-agent §Error Handling | REFLECTED | — | |
| 96 | Reliability | "Claude process crash: detect exit, publish lifecycle, auto-restart with --resume" | spec-session-agent §Error Handling | REFLECTED | — | |
| 97 | Reliability | "Agent liveness via NATS $SYS presence events, not heartbeats" | spec-state-schema §$SYS events, spec-session-agent §Daemon: Liveness | REFLECTED | — | |
| 98 | Reliability | "JSONL cleanup: daily job deletes files older than 90 days" | spec-session-agent §JSONL Cleanup Job | REFLECTED | — | |
| 99 | Security | "Injection defense: typed literals hardcoded, slugs constrained by charset" | spec-common §Validation | REFLECTED | — | |
| 100 | /upgrade-claude Skill | "5-step process: fetch changelog → analyze breaking changes → propose patches → update Dockerfile → open PR" | spec-session-agent §/upgrade-claude Skill | REFLECTED | — | |
| 101 | Login Response | Full JSON shape with user, jwt, nkeySeed, hubUrl, hosts[], projects[] | spec-state-schema §Login Response Shape | REFLECTED | — | JSON structures match |
| 102 | NATS Server Config | "Hub NATS: port 4222, ws 8080, leafnodes 7422, JetStream domain hub, 3-tier trust chain" | spec-state-schema §NATS Server Configuration | REFLECTED | — | |
| 103 | NATS Server Config | "Worker NATS: same trust chain, leaf remote to hub, unique JS domain" | spec-state-schema §NATS Server Configuration | REFLECTED | — | |
| 104 | K8s Resources | MCProject CRD, namespace, Secret, ConfigMap, PVC, Deployment, RBAC per project | spec-state-schema §Kubernetes Resources | REFLECTED | — | |
| 105 | Helm | "mclaude-cp: hub NATS, Postgres, control-plane, SPA, init-keys Job" | spec-helm §Chart: mclaude-cp | REFLECTED | — | |
| 106 | Helm | "mclaude-worker: worker NATS, controller, session-agent template" | spec-helm §Chart: mclaude-worker | REFLECTED | — | |
| 107 | Helm | "`terminationGracePeriodSeconds: 86400` (24h)" | spec-helm §mclaude-worker config knobs | REFLECTED | — | |
| 108 | sessions.create payload | "Session create request payload: `{name, branch, cwd, joinWorktree, extraFlags}`" | spec-state-schema §NATS Subjects (sessions.create row) | PARTIAL | SPEC→FIX | spec-state-schema lists `{branch, permPolicy, quotaMonitor, requestId}` as the create payload — missing `name`, `cwd`, `joinWorktree`, `extraFlags` from the ADR. The session-agent spec's creation steps reference these fields but the canonical subjects table doesn't list them. |

### Summary

- Reflected: 101
- Gap: 6
- Partial: 2

### Gap and Partial Details

**GAP [SPEC→FIX] #35**: Terminal I/O subject patterns
- ADR: `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.terminal.{termId}.output` / `…terminal.{termId}.input`
- Fix: spec-state-schema §NATS Subjects table should add a dedicated row for per-terminal I/O subjects with the `{termId}.output` / `{termId}.input` pattern. spec-session-agent §Terminal Sessions should state the subject format explicitly.

**GAP [SPEC→FIX] #50**: `session_upgrading` lifecycle event payload missing
- ADR (Graceful Shutdown step 7): "Publish `session_upgrading` lifecycle event per session."
- Fix: spec-state-schema §Lifecycle Event Payloads should add: `{"type": "session_upgrading", "sessionId": "...", "ts": "RFC3339"}`

**GAP [SPEC→FIX] #55**: Slug validation error response
- ADR (Error Handling): `HTTP 400 with {code:"invalid_slug", reason:"reserved_word|charset|length", field:"slug"}`
- Fix: spec-control-plane §Error Handling should add: "Invalid slug in admin-provided fields (e.g. cluster registration): returns 400 with `{code: \"invalid_slug\", reason: …, field: \"slug\"}`."

**GAP [SPEC→FIX] #59**: Recovery missing restarting state and lifecycle events
- ADR (Startup/Recovery steps 2-3): "Set all session KV entries to state: 'restarting', clear pendingControls. Publish session_restarting lifecycle events."
- Fix: spec-session-agent §Session Lifecycle > Resumption should add between current steps 1 and 2: "Set all session KV entries to state `restarting` and clear `pendingControls`. Publish `session_restarting` lifecycle event for each session."

**GAP [SPEC→FIX] #60**: Recovery missing 30s timeout for failed resume
- ADR (Startup/Recovery step 7): "Sessions that fail to start within 30s: mark state: 'failed', publish session_failed"
- Fix: spec-session-agent §Session Lifecycle > Resumption should add: "Sessions that do not emit an `init` event within 30 seconds of `--resume` are marked `state: failed` and a `session_failed` lifecycle event is published."

**GAP [ADR→FIX] #93**: Health probe readiness path `/ready` vs `/readyz`
- ADR (Health Probes): `readinessProbe: httpGet: path: /ready`
- Spec (spec-control-plane): `/readyz` — K8s convention. The spec also adds `/healthz` as a liveness alias.
- Fix: ADR health probes YAML should use `path: /readyz` to match the spec and K8s convention.

**PARTIAL [SPEC→FIX] #31**: `sessions.restart` subject row missing
- ADR: `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.restart → accepts optional extraFlags`
- The subject appears in the MCLAUDE_API filter pattern (`{create|input|restart|delete|control}`) but has no dedicated row in the NATS Subjects table — its payload is undocumented.
- Fix: spec-state-schema §NATS Subjects table should add: `| …api.sessions.restart | SPA | session-agent (JetStream, cmd consumer) | {sessionSlug, extraFlags?} |`

**PARTIAL [SPEC→FIX] #108**: `sessions.create` payload incomplete
- ADR payload: `{name, branch, cwd, joinWorktree, extraFlags}`
- spec-state-schema payload: `{branch, permPolicy, quotaMonitor, requestId}`
- Fix: spec-state-schema should document the full payload union: `{name?, branch?, cwd?, joinWorktree?, extraFlags?, permPolicy?, quotaMonitor?, requestId?}`.
