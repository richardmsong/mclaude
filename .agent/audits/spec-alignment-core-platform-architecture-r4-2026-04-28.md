## Run: 2026-04-28T12:00:00Z

ADR: `docs/adr-0042-core-platform-architecture.md` (Status: accepted)
Specs evaluated:
- `docs/spec-state-schema.md`
- `docs/mclaude-control-plane/spec-control-plane.md`
- `docs/mclaude-session-agent/spec-session-agent.md`
- `docs/mclaude-common/spec-common.md`
- `docs/mclaude-web/spec-web.md`
- `docs/mclaude-cli/spec-cli.md`
- `docs/charts-mclaude/spec-helm.md`

Round 4 — final verification after R1 (18+15), R2 (7+1), R3 (6+2) gaps all addressed.

| # | ADR Section | ADR Text | Spec Location | Verdict | Direction | Notes |
|---|-------------|----------|---------------|---------|-----------|-------|
| 1 | Decisions: Claude Code integration | "Headless via `--print --verbose --output-format stream-json --input-format stream-json --include-partial-messages`" | spec-session-agent §Session Lifecycle, Creation step 10 | REFLECTED | — | Spec has same flags plus `--replay-user-messages` (from a later ADR) |
| 2 | Decisions: Event bus | "NATS JetStream" | spec-state-schema §JetStream Streams | REFLECTED | — | All three streams defined |
| 3 | Decisions: Event schema | "Raw Claude Code stream-json — no envelope, no protobuf translation" | spec-session-agent §Event Routing | REFLECTED | — | "Publishes raw stream-json events" |
| 4 | Decisions: State store | "NATS KV (sessions, projects, hosts, job-queue) + Postgres (users, hosts, projects, oauth_connections)" | spec-state-schema §Postgres, §NATS KV Buckets | REFLECTED | — | All buckets and tables documented |
| 5 | Decisions: Session management | "Single Go binary (mclaude-session-agent) per project pod" | spec-session-agent §Role | REFLECTED | — | "per-project process supervisor" |
| 6 | Decisions: Control plane | "Go service, K8s-free per ADR-0035" | spec-control-plane §Role | REFLECTED | — | "K8s-free: no controller-runtime, no K8s client" |
| 7 | Decisions: Web client | "React 18 SPA, mobile browser first" | spec-web §Role | REFLECTED | — | "React 18…mobile browser first" |
| 8 | Decisions: Ingress | "nginx reverse proxy — no auth logic, no routing decisions" | spec-helm §Ingress | REFLECTED | — | Routes /auth,/api,/admin,/scim→CP; /→SPA |
| 9 | Decisions: Addressing scheme | "Typed literals between every slug: `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}`" | spec-common §subj, spec-state-schema §NATS Subjects | REFLECTED | — | Host-scoped typed-slug addressing throughout |
| 10 | Decisions: Slug charset | "`[a-z0-9][a-z0-9-]{0,62}`" | spec-common §Validation | REFLECTED | — | Exact match |
| 11 | Decisions: Reserved literals | "Blocklist of 10 words: users, hosts, projects, sessions, clusters, api, events, lifecycle, quota, terminal" | spec-common §Validation | REFLECTED | — | Same 10 words |
| 12 | Decisions: Slug ownership | "All slugs system-computed from display names, never user-picked. Immutable after creation." | spec-state-schema §users, §projects, §hosts | REFLECTED | — | "Derived…Immutable after creation" |
| 13 | Decisions: Display name vs slug | "Display name: free-form UTF-8, max 128, mutable. Slug: auto-derived, immutable." | spec-state-schema §users | REFLECTED | — | "Display name (free-form UTF-8, max 128 chars, mutable)" |
| 14 | Decisions: Subject construction | "Typed Go wrappers (`type UserSlug string`, etc.) in mclaude-common/pkg/subj — raw strings are compile-time errors" | spec-common §Role | REFLECTED | — | "typed slug wrappers…impossible to pass a raw string" |
| 15 | Decisions: Shared Go module | "`mclaude-common/` at repo root, wired via `go.work`" | spec-common §Role | REFLECTED | — | "module mclaude.io/common" |
| 16 | Decisions: User slug derivation | "`slugify(name-or-local-part)-{domain.split('.')[0]}` at creation. Collision → numeric suffix." | spec-common §DeriveUserSlug, spec-state-schema §users | REFLECTED | — | Both spec locations describe same derivation |
| 17 | Decisions: Project/host/cluster slug | "`slugify(display_name)` at creation; collision within scope → numeric suffix. Immutable." | spec-state-schema §projects, §hosts | REFLECTED | — | Scope rules match |
| 18 | Decisions: HTTP URL scheme | "`/api/users/{uslug}/projects/{pslug}/…`; Auth + infra routes flat; Admin: `/admin/users/{uslug}/…`" | spec-control-plane §HTTP Endpoints | REFLECTED | — | All routes present |
| 19 | Decisions: KV key separator | "Uniform `.` across all buckets" | spec-state-schema §NATS KV, spec-common §KV key helpers | REFLECTED | — | All keys use `.` |
| 20 | Decisions: Cross-user URL access | "Hard 403 when JWT `sub` ≠ URL `{uslug}`" | spec-control-plane §Authentication | REFLECTED | — | "rejects with 403 when JWT's sub user slug does not match URL's {uslug}" |
| 21 | Decisions: Git worktrees | "One worktree per branch per project. `joinWorktree: true` to share." | spec-session-agent §Worktree Management | REFLECTED | — | Collision + joinWorktree documented |
| 22 | Decisions: Tool installation | "Nix single-user mode, per-project PVC at `/nix/`, `pkg` shim" | spec-session-agent §Deployment | REFLECTED | — | "pkg shim translates apt/brew to nix" |
| 23 | Decisions: Claude CLI installation | "Native binary via `claude install --version {pinned}` in Dockerfile. Image based on `node:22-alpine`." | spec-session-agent §Deployment | REFLECTED | — | "Alpine-based with Node.js 22, Claude Code (native binary)" |
| 24 | Component Topology | "mclaude-session-agent: Go, spawns headless Claude Code processes" | spec-session-agent §Role | REFLECTED | — | |
| 25 | Component Topology | "mclaude-control-plane: Go, K8s-free per ADR-0035" | spec-control-plane §Role | REFLECTED | — | |
| 26 | Component Topology | "mclaude-cli: Go, debug attach tool, thin text REPL over unix socket" | spec-cli §Role | REFLECTED | — | "attaches to running session agents over unix sockets, provides an interactive REPL" |
| 27 | Component Topology | "mclaude-common: Go shared module, slug/subject helpers, typed slug types" | spec-common §Role | REFLECTED | — | |
| 28 | Component Topology | "mclaude-web: TypeScript (React 18), mobile browser first" | spec-web §Role | REFLECTED | — | |
| 29 | Component Topology | "nginx ingress: dumb reverse proxy, no routing logic, no auth" | spec-helm §Ingress | REFLECTED | — | |
| 30 | Data Flow | "SPA publishes user message to NATS…Session agent receives…routes stdin/stdout via NATS…SPA receives events via NATS" | spec-session-agent §Event Routing, spec-web §NATS Connection | REFLECTED | — | Full data flow covered across specs |
| 31 | Claude Code Integration | "`claude --print --verbose --output-format stream-json --input-format stream-json --include-partial-messages --session-id {sessionId}`" | spec-session-agent §Session Lifecycle, Creation step 10 | REFLECTED | — | |
| 32 | Claude Code Integration | "For session resume: `claude …--resume {sessionId}`" | spec-session-agent §Session Lifecycle, Resumption step 4 | REFLECTED | — | "resume the Claude process with `--resume {id}`" |
| 33 | Stream-JSON Protocol | Output events: system/init, session_state_changed, assistant, stream_event, user, control_request, tool_progress, result, clear, compact_boundary | spec-session-agent §Event Routing, §State Machine; spec-web §Event Rendering | REFLECTED | — | All event types covered |
| 34 | Stream-JSON Protocol | Input events: user message, control_response, interrupt, reload_plugins, set_model, set_max_thinking_tokens | spec-session-agent §Permission Policy; spec-web §Model/Effort Switching | REFLECTED | — | |
| 35 | Subagent Events | "subagent events appear flat on stdout…`parent_tool_use_id` linking to parent Agent block" | spec-web §Event Rendering: "Events with parent_tool_use_id → Render nested under parent Agent block" | REFLECTED | — | |
| 36 | NATS Subject Structure | API session commands: create, delete, input, control, restart (host-scoped) | spec-state-schema §NATS Subjects (Host-scoped) | REFLECTED | — | All 5 subjects listed |
| 37 | NATS Subject Structure | API project management: provision, create, update, delete (host-scoped), projects.updated (user-scoped) | spec-state-schema §NATS Subjects | REFLECTED | — | All subjects listed with correct scope |
| 38 | NATS Subject Structure | Events: `…events.{sslug}` (JetStream), lifecycle: `…lifecycle.{sslug}` (JetStream) | spec-state-schema §NATS Subjects, §JetStream Streams | REFLECTED | — | |
| 39 | NATS Subject Structure | "`max_payload: 8388608` (8MB)…truncates content field and sets `truncated: true`" | spec-session-agent §Event Routing; spec-state-schema §NATS Server Config | REFLECTED | — | |
| 40 | NATS Subject Structure | Terminal I/O: "Not JetStream — raw terminal I/O is ephemeral, no replay needed. Use core NATS pub/sub." | spec-state-schema §NATS Subjects: "Raw PTY output bytes (ephemeral, core NATS, max 4KB per message)" | REFLECTED | — | |
| 41 | NATS Subject Structure | Terminal API: create, delete, resize subjects | spec-state-schema §NATS Subjects | REFLECTED | — | |
| 42 | NATS Subject Structure | Quota: "`mclaude.users.{uslug}.quota` → broadcast signal, not request/reply" | spec-state-schema §NATS Subjects: "leaf under user scope…not under .api." | REFLECTED | — | |
| 43 | JetStream Streams | MCLAUDE_EVENTS (30 days), MCLAUDE_LIFECYCLE (30 days), MCLAUDE_API (1 hour) | spec-state-schema §JetStream Streams | REFLECTED | — | All three streams with matching retention |
| 44 | State: NATS KV | mclaude-sessions key: `{uslug}.{hslug}.{pslug}.{sslug}` | spec-state-schema §mclaude-sessions | REFLECTED | — | |
| 45 | State: NATS KV | mclaude-projects key: `{uslug}.{hslug}.{pslug}` | spec-state-schema §mclaude-projects | REFLECTED | — | |
| 46 | State: NATS KV | mclaude-hosts key: `{uslug}.{hslug}` | spec-state-schema §mclaude-hosts | REFLECTED | — | |
| 47 | State: NATS KV | mclaude-job-queue key: `{uslug}.{jobId}` | spec-state-schema §mclaude-job-queue | REFLECTED | — | |
| 48 | State: NATS KV | "control-plane creates all buckets idempotently on startup (`nats.KeyValueStoreOrCreate`)" | spec-control-plane §Startup Sequence step 7 | REFLECTED | — | "Ensures KV buckets exist" |
| 49 | Session State JSON | All fields: id, projectId, branch, worktree, cwd, name, state, stateSince, createdAt, model, extraFlags, userSlug, hostSlug, projectSlug, slug, capabilities, pendingControls, usage, replayFromSeq | spec-state-schema §mclaude-sessions Value | REFLECTED | — | Spec has all fields plus `joinWorktree` from later ADR |
| 50 | Session State | "`state` maps from stream-json `session_state_changed`: idle, running, requires_action" | spec-state-schema §mclaude-sessions; spec-session-agent §State Machine | REFLECTED | — | Spec has additional agent-managed states (updating, restarting, failed) |
| 51 | Session State | "`pendingControls`: map of request_id → control_request. No timeout." | spec-state-schema §mclaude-sessions | REFLECTED | — | |
| 52 | Session State | "`capabilities` populated from `init` event on session start" | spec-session-agent §Session Lifecycle; spec-state-schema | REFLECTED | — | |
| 53 | Session State | "`replayFromSeq`: JetStream sequence number…Updated on /clear and compaction" | spec-session-agent §Compact Boundary Tracking; spec-web §Event Replay | REFLECTED | — | |
| 54 | Project State JSON | All fields: id, slug, userSlug, hostSlug, name, gitUrl, gitIdentityId, status, sessionCount, worktrees, createdAt, lastActiveAt | spec-state-schema §mclaude-projects Value | REFLECTED | — | |
| 55 | Lifecycle Events | session_created, session_stopped, session_restarting, session_resumed, session_failed, debug_attached, debug_detached | spec-state-schema §Lifecycle Event Payloads | REFLECTED | — | Spec also has session_upgrading, session_permission_denied, job events from later ADRs |
| 56 | Slug System | "Lowercase → NFD → strip combining marks → replace non-[a-z0-9] with - → trim → truncate to 63" | spec-common §Slug algorithm | REFLECTED | — | Exact same steps |
| 57 | Slug System | "Fallback: emit `u-{6 base32}` for users, `p-{6}`/`h-{6}`/`c-{6}` for projects/hosts/clusters" | spec-common §Fallback | REFLECTED | — | Spec also has `s-` for sessions |
| 58 | Slug System | User slug: `slugify(name-or-local-part)-{domain.split('.')[0]}`. Collision → numeric suffix. Immutable. | spec-common §DeriveUserSlug; spec-state-schema §users.slug | REFLECTED | — | |
| 59 | Slug System | "Project/host/cluster slug: `slugify(display_name)` at creation; collision within scope → numeric suffix. Immutable." | spec-state-schema §projects, §hosts | REFLECTED | — | |
| 60 | mclaude-common layout | "pkg/slug (Slugify, Validate, ValidateOrFallback) + pkg/subj (typed subject builders)" | spec-common §Interfaces | REFLECTED | — | |
| 61 | mclaude-common subj | "subj.UserHostProjectAPISessionsCreate(u, h, p) → full subject; subj.SessionsKVKey(u, h, p, s) → key; subj.ProjectsKVKey(u, h, p); subj.HostsKVKey(u, h)" | spec-common §Package subj, §KV key helpers | REFLECTED | — | All helpers documented |
| 62 | TypeScript Mirrors | "src/lib/slug.ts mirrors pkg/slug; src/lib/subj.ts mirrors pkg/subj. Runtime assertion in dev builds." | spec-web §TypeScript Slug/Subject Mirrors | REFLECTED | — | Exact match |
| 63 | Postgres Slug Columns | "users.slug UNIQUE; hosts.slug UNIQUE(user_id, slug); projects.slug UNIQUE INDEX (user_id, host_id, slug)" | spec-state-schema §users, §hosts, §projects | REFLECTED | — | All constraints match |
| 64 | Error Handling | "Slug validation at ingress: HTTP 400 with `{code:'invalid_slug', reason:'reserved_word|charset|length', field:'slug'}`" | spec-control-plane §Error Handling | REFLECTED | — | |
| 65 | Error Handling | "Cross-user URL access: middleware compares JWT sub uslug with URL {uslug}. Mismatch → 403." | spec-control-plane §Authentication | REFLECTED | — | |
| 66 | Session Agent | "Subscribes to `…api.>`, handles CRUD, input, control messages" | spec-session-agent §NATS Subjects (Subscribe) | REFLECTED | — | |
| 67 | Session Agent Core Loop | "Stdin serialization via channel…stdout scanner with 16MB buffer…JetStream pull consumers (sa-cmd-{uslug}-{pslug}, sa-ctl-{uslug}-{pslug})" | spec-session-agent §Event Routing, §JetStream consumers | REFLECTED | — | "stdin serializer goroutine drains the stdin channel sequentially"; "16 MB scanner buffer"; consumer names match |
| 68 | Session Operations | "create: spawn with --session-id + extraFlags; delete: interrupt→wait→kill; input: write to stdin; control: write to stdin; restart: kill→update KV→relaunch --resume" | spec-session-agent §Session Lifecycle | REFLECTED | — | All operations covered |
| 69 | Permission Handling | "permissionPolicy: auto/managed/allowlist with allowedTools" | spec-session-agent §Permission Policy | REFLECTED | — | Spec has 4 policies (adds strict-allowlist from later ADR) |
| 70 | Startup/Recovery | "7-step: read KV → set restarting → publish lifecycle → resume → update KV → publish resumed → fail after 30s" | spec-session-agent §Resumption | REFLECTED | — | 8 steps in spec (adds updating state handling) |
| 71 | Graceful Shutdown | "8-step: write updating → cancel cmd consumer → drain core subs → keep ctl consumer → poll idle → cancel ctl → publish upgrading → exit. terminationGracePeriodSeconds: 86400" | spec-session-agent §Graceful Shutdown; spec-helm §sessionAgent.terminationGracePeriodSeconds | REFLECTED | — | Same 8 steps; 86400 in Helm values |
| 72 | Worktrees | "branch slugification: feature/auth → feature-auth; session create payload with name, branch, cwd, joinWorktree, extraFlags; behavior table for joinWorktree" | spec-session-agent §Worktree Management, §Session Lifecycle | REFLECTED | — | |
| 73 | Debug Attach | "unix socket per session at `/tmp/mclaude-session-{id}.sock`; mclaude-cli connects for text REPL" | spec-session-agent §Unix Sockets; spec-cli §attach command | REFLECTED | — | Both specs match |
| 74 | Laptop Mode | "One session-agent per project; mclaude-controller-local manages; --daemon --host {hslug}; wss:// NATS; ~/.mclaude/hosts/{hslug}/nats.creds; JWT refresh 60s/15min" | spec-session-agent §Daemon Mode, §JWT Refresh; spec-cli §mclaude daemon | REFLECTED | — | |
| 75 | Control Plane: Project Provisioning | "NATS request/reply to `…api.projects.provision`, timeout 10s (`PROVISION_TIMEOUT_SECONDS`)" | spec-control-plane §NATS Subjects (Publishes), §Environment Variables | REFLECTED | — | "PROVISION_TIMEOUT_SECONDS: 10" |
| 76 | Control Plane: Endpoints | Auth (login, refresh, OAuth), Users admin (CRUD), Projects (CRUD), Host management (list, code, register, update, delete), Cluster admin (register, list, grants, delete), SCIM, break-glass metrics | spec-control-plane §HTTP Endpoints | REFLECTED | — | All endpoints present |
| 77 | Control Plane: Postgres Schema | "users, hosts, projects, oauth_connections tables with full column definitions and constraints" | spec-state-schema §Postgres | REFLECTED | — | |
| 78 | User Provisioning Flow | "4 steps: user created → INSERT postgres → default local host → K8s namespace on first project" | spec-control-plane §Startup Sequence (dev seed), §Project Creation Flow | REFLECTED | — | Default local host at user creation documented |
| 79 | User Deprovision Flow | "6 steps: delete → revoke JWT → NATS terminates → DELETE postgres cascade → publish NATS delete → controllers tear down" | spec-control-plane §DELETE /admin/users/{id} | REFLECTED | — | |
| 80 | Project Provisioning Flow | "6 steps: SPA calls POST → postgres row → KV write → NATS request → controller provisions → return result" | spec-control-plane §Project Creation Flow | REFLECTED | — | Same 7-step flow |
| 81 | Pod Structure | "session-agent + config-sync sidecar + optional dockerd-rootless. Mounts: project PVC→/data, nix PVC→/nix, claude-home→~/.claude/ (emptyDir), user-config→~/.claude-seed/ (ConfigMap), user-secrets→~/.user-secrets/ (Secret)" | spec-session-agent §Pod Structure | REFLECTED | — | |
| 82 | /data/ Layout | "repo/, worktrees/, shared-memory/, projects/" | spec-session-agent §Deployment | REFLECTED | — | "bare repo, worktrees, JSONL persistence, shared memory" |
| 83 | 3-Tier Storage | "User (ConfigMap+Secret), Project (PVC), Session (NATS KV), Home (emptyDir)" | spec-session-agent §Managed Platform Config, §Deployment | REFLECTED | — | |
| 84 | Home Dir + Config Sync | "emptyDir seeded from Secret+ConfigMap; config-sync watches settings.json+CLAUDE.md, patches ConfigMap" | spec-session-agent §Pod Structure: config-sync sidecar | REFLECTED | — | |
| 85 | Git Credential Helper | "session-agent CredentialManager.Setup: symlink /data/.config→~/.config, merge tokens, gh/glab auth setup-git" | spec-session-agent §Credential Management (K8s Mode) | REFLECTED | — | All 7 steps match |
| 86 | Auto-Memory Sharing | "background loop symlinks worktree memory dirs to /data/shared-memory/" | spec-session-agent §Auto-Memory Sharing | REFLECTED | — | "every 5s…symlinks…/data/shared-memory/" |
| 87 | CLAUDE.md Three-Tier | "Global at /etc/claude-code/CLAUDE.md (platform, no override); User at ~/.claude/CLAUDE.md; Project at {worktree}/CLAUDE.md" | spec-session-agent §Managed Platform Config | REFLECTED | — | Same three-tier table |
| 88 | Guard Hooks | "/etc/claude-code/hooks/guard.sh blocks: git checkout/switch, apt install, /etc/claude-code/ mods, rm -rf on critical paths" | spec-session-agent §Managed Platform Config | REFLECTED | — | Same 4 blocks |
| 89 | Tool Installation | "Nix store on per-project PVC; /usr/local/bin/pkg shim; apt/brew shimmed to pkg" | spec-session-agent §Deployment | REFLECTED | — | |
| 90 | Registry Mirror System | "mirrors.json ConfigMap; types: npm→.npmrc, pypi→pip.conf, go→GOPROXY, nix→nix.conf; auth + tls" | spec-session-agent §Registry Mirror System | REFLECTED | — | |
| 91 | Entrypoint | "Detailed bash script: consume secrets, seed config, link JSONL, skip onboarding, git setup, shared memory, platform hooks, exec session-agent" | spec-session-agent §Standalone Mode (K8s) | REFLECTED | — | Entrypoint behavior described |
| 92 | Environment Variables | "GIT_URL, USER_ID, USER_SLUG, HOST_SLUG, PROJECT_ID, PROJECT_SLUG, NATS_URL, DOCKER_ENABLED, CLAUDE_CODE_OAUTH_TOKEN, HTTPS_PROXY" | spec-session-agent §Configuration | REFLECTED | — | All vars documented |
| 93 | Terminal Access | "creack/pty, /bin/zsh, raw I/O through NATS, max 4KB per message, core NATS" | spec-session-agent §Terminal Sessions | REFLECTED | — | "4 KB chunks…core NATS" |
| 94 | Terminal: xterm.js | "web SPA renders terminal sessions with xterm.js" | spec-web §Dependencies: "xterm.js: Terminal rendering" | REFLECTED | — | |
| 95 | Web SPA | "NATS via /nats WS proxy; stream-json rendering; model/effort switching; cost tracking; file/image uploads (base64, ~20MB); KV state; event replay; background reconnect; skills picker" | spec-web §(multiple sections) | REFLECTED | — | All features documented |
| 96 | NATS Auth: Login flow | "6 steps: client POST /auth/login → validate → issue JWT scoped to user.hosts.host.> → return {natsUrl, jwt, nkeySeed}" | spec-control-plane §Authentication and JWT Signing | REFLECTED | — | |
| 97 | NATS Auth: JWT expiry | "8h (`JWT_EXPIRY_SECONDS`). Refresh threshold: 15min. SPA checks TTL every 60s." | spec-control-plane §Env Vars; spec-web §JWT Refresh | REFLECTED | — | |
| 98 | nginx Ingress | "/nats→NATS WS; /auth,/api,/scim→control-plane; /→SPA" | spec-helm §Ingress | REFLECTED | — | |
| 99 | Image Build Pipeline | "3 images: control-plane, SPA, session-agent at ghcr.io/richardmsong. Tags: main-{sha} + main. Never :latest." | spec-helm §mclaude-cp SPA, §mclaude-worker session-agent template | REFLECTED | — | Image refs in helm; tag policy is CI behavior |
| 100 | Health Probes: session-agent | "liveness: exec session-agent --health (process + NATS); readiness: exec session-agent --ready (NATS + can spawn claude)" | spec-session-agent §Health and Readiness | REFLECTED | — | |
| 101 | Health Probes: control-plane | "liveness: GET /healthz (never checks NATS); readiness: GET /readyz (checks Postgres only)" | spec-control-plane §HTTP Endpoints (Public) | REFLECTED | — | |
| 102 | Reliability: Postgres down | "retry with exponential backoff. Login endpoints return 503." | spec-control-plane §Error Handling | REFLECTED | — | |
| 103 | Reliability: NATS down | "buffer state changes in memory, flush on reconnect. Claude processes continue. At-least-once delivery." | spec-session-agent §Error Handling: "state changes buffered in memory" | REFLECTED | — | |
| 104 | Reliability: Pod restart | "Graceful: shutdown runs. On startup, read KV, resume. Ungraceful: stale KV cleaned up on startup." | spec-session-agent §Session Lifecycle (Resumption) | REFLECTED | — | |
| 105 | Reliability: Agent liveness | "detected via NATS $SYS presence events (connect/disconnect), not heartbeats" | spec-state-schema §$SYS subjects; spec-session-agent §Daemon: Liveness | REFLECTED | — | "$SYS is the only liveness signal" |
| 106 | Reliability: Claude crash | "session-agent detects exit, publishes lifecycle event, auto-restarts with --resume" | spec-session-agent §Error Handling | REFLECTED | — | |
| 107 | Reliability: Git clone failure | "entrypoint exits non-zero. Deployment restart policy retries." | spec-session-agent §Error Handling | REFLECTED | — | |
| 108 | Reliability: JSONL cleanup | "daily job deletes JSONL >90 days, purges for sessions not in KV" | spec-session-agent §JSONL Cleanup Job | REFLECTED | — | |
| 109 | Security: Injection defense | "typed literals hardcoded; slugs constrained by charset — no `.`, `*`, `>` in slugs" | spec-common §Validation | REFLECTED | — | |
| 110 | Security: Privilege boundaries | "NATS credentials grant by subject prefix (`mclaude.users.{uslug}.>`)" | spec-state-schema §Per-host user JWT permissions | REFLECTED | — | |
| 111 | Security: Reserved-word blocklist | "append-only" | spec-common §Validation; spec-state-schema: "clusters stays defensively" | REFLECTED | — | |
| 112 | Security: Admin bypass | "/admin/* routes require admin role JWT claim. Actions logged by uslug." | spec-control-plane §Admin-only endpoints | REFLECTED | — | |
| 113 | /upgrade-claude Skill | "5-step: fetch changelog → analyze breaking changes → if breaking: impact report + patches → if safe: update pin, commit branch, open PR" | spec-session-agent §/upgrade-claude Skill | REFLECTED | — | |
| 114 | Observability: Metrics | "per-user sessions, events/sec, project count; NATS rate, lag; CP auth/provisioning latency; SA process count, restart count, throughput" | spec-session-agent §Prometheus Metrics; spec-control-plane §metrics port | REFLECTED | — | |
| 115 | Observability: Logging | "structured JSON to stdout. Labels: userId, projectId, sessionId." | spec-session-agent §Configuration (zerolog) | REFLECTED | — | |
| 116 | NATS Server: Hub config | "port 4222, ws 8080, max_payload 8MB, jetstream domain hub, operator JWT, resolver MEMORY, leafnodes listen 7422" | spec-state-schema §Hub NATS; spec-helm §Hub NATS | REFLECTED | — | |
| 117 | NATS Server: Worker config | "Same 3-tier trust, leafnode remote to hub, JetStream domain per worker" | spec-state-schema §Worker NATS; spec-helm §Worker NATS | REFLECTED | — | |
| 118 | NATS Server: Operator+account keys | "K8s Secret mclaude-system/operator-keys: operatorJwt, operatorSeed, accountJwt, accountSeed. Generated by Helm pre-install Job." | spec-state-schema §Operator+account NKeys; spec-helm §Pre-install Job | REFLECTED | — | |
| 119 | NATS Auth: Per-host JWT permissions | "publish/subscribe: mclaude.users.{uslug}.hosts.{hslug}.>, _INBOX.>, $JS.*.API.>, $SYS…" | spec-state-schema §Per-host user JWT permissions | REFLECTED | — | |
| 120 | NATS Auth: Per-cluster leaf JWT | "publish/subscribe wildcard at user level: mclaude.users.*.hosts.{cluster-slug}.>" | spec-state-schema §Per-cluster leaf / controller JWT | REFLECTED | — | |
| 121 | NATS Server: Single-cluster degenerate | "worker leaf URL localhost:7422 or in-cluster service DNS. All else identical." | spec-helm §Single-cluster degenerate install | REFLECTED | — | |
| 122 | Login Response | "JSON: user, jwt, nkeySeed, hubUrl, hosts[] (with jsDomain, directNatsUrl on cluster entries), projects[]" | spec-state-schema §Login Response Shape | REFLECTED | — | |

### Summary

- Reflected: 122
- Gap: 0
- Partial: 0
