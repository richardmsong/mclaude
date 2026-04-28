# ADR: Core Platform Architecture

**Status**: implemented
**Status history**:
- 2026-04-28: draft
- 2026-04-28: accepted — design audit CLEAN (R4)
- 2026-04-28: implemented — spec-evaluator CLEAN (R4, 122 decisions reflected across 7 specs). Consolidation of existing architecture (ADR-0002, ADR-0003, ADR-0024).

> Supersedes:
> - `adr-0002-core-containers.md` — folded in: session image structure (entrypoint, config seeding, managed CLAUDE.md, guard hooks, Nix package management, registry mirrors), pod storage model, config-sync sidecar, CLAUDE.md three-tier system, auto-memory sharing across worktrees
> - `adr-0003-k8s-integration.md` — folded in: stream-json protocol integration, NATS subject layout, JetStream streams, KV bucket design, session-agent architecture, control-plane reconciliation controller, pod structure, SPA design, terminal access, health probes, reliability model, Claude Code upgrade skill
> - `adr-0024-typed-slugs.md` — folded in: typed-slug addressing scheme for all NATS subjects/HTTP URLs/KV keys, slug charset and `slugify()` algorithm, reserved-word blocklist, compile-time-safe subject construction via `mclaude-common` shared Go module, display-name/slug separation, Postgres slug columns
>
> The three ADRs above are marked `superseded` by this ADR in their status history.

## Overview

MClaude is a platform for running managed Claude Code sessions. Users interact via a web SPA. The platform manages Claude Code processes inside Kubernetes pods (or on laptops), streams events to the browser in real-time via NATS JetStream, and handles session lifecycle (create, restart, delete, idle teardown). A single Go binary — the session agent — spawns headless Claude Code processes using the stream-json protocol (the same protocol used by VS Code and JetBrains IDE extensions), eliminating tmux, JSONL tailing, and screen scraping entirely.

This ADR is the canonical architecture reference for the mclaude platform. It defines the component topology, data flow, addressing scheme, pod structure, storage model, and client architecture. It does not cover host-scoping (see `adr-0035-unified-host-architecture.md`) or NATS authentication/security (see `adr-0016-nats-security.md`), which are defined in their own ADRs.

## Motivation

The platform architecture evolved through three sequential ADRs that overlap on the same architectural surface:

1. **ADR-0002** defined the original container architecture (tmux-based sessions, Postgres LISTEN/NOTIFY, Swift server, JSONL tailing). Most of this was superseded by ADR-0003, but the session image structure, entrypoint design, managed config, guard hooks, Nix tooling, and registry mirror system survived.

2. **ADR-0003** replaced the core architecture with stream-json headless Claude Code, NATS JetStream for event routing, a Go session-agent, and a reconciliation-based control-plane. This became the canonical platform architecture but still referenced some ADR-0002 designs implicitly.

3. **ADR-0024** rewrote all NATS subjects, HTTP URLs, and KV keys from positional UUIDs to typed slugs, introduced the `mclaude-common` shared Go module, and defined the slug charset, `slugify()` algorithm, and compile-time-safe subject construction.

Reading the platform architecture requires synthesizing all three documents and mentally resolving conflicts. This consolidated ADR eliminates that burden — one document, one truth.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Claude Code integration | Headless via `--print --verbose --output-format stream-json --input-format stream-json --include-partial-messages` | Eliminates tmux, JSONL tailing, and screen scraping. Stream-json is the same protocol used by IDE extensions — stable, well-maintained. |
| Event bus | NATS JetStream | Append-only event streams, KV state store, subject-based routing, WebSocket proxy for browsers. Replaces Postgres LISTEN/NOTIFY. |
| Event schema | Raw Claude Code stream-json — no envelope, no protobuf translation | Subject encodes routing metadata (user/project/session). Events flow unchanged from Claude stdout to client. |
| State store | NATS KV (sessions, projects, hosts, job-queue) + Postgres (users, hosts, projects, oauth_connections) | KV gives real-time watches for client state. Postgres for durable identity, host management, and project records. |
| Session management | Single Go binary (`mclaude-session-agent`) per project pod | Same binary on K8s and laptop. Spawns Claude as child processes, routes stdin/stdout via NATS. No HTTP server. |
| Control plane | Go service (`mclaude-control-plane`), K8s-free per ADR-0035 | Owns user identity, host management, NATS JWT issuance, project provisioning via NATS request/reply to controllers (`mclaude-controller-k8s`, `mclaude-controller-local`). No kubebuilder, no K8s client. |
| Web client | React 18 SPA, mobile browser first | Enterprise constraint: must work in mobile browser (native apps can't reach VPN). Connects to NATS directly via WebSocket proxy. |
| Ingress | nginx reverse proxy — no auth logic, no routing decisions | `/nats` → NATS WebSocket, `/auth` `/api` `/scim` → control-plane, `/*` → SPA static files. |
| Addressing scheme | Typed literals between every slug: `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}` (host-scoped per ADR-0035) | Self-describing subjects. Every slug preceded by a word saying what it is. No positional ambiguity. |
| Slug charset | `[a-z0-9][a-z0-9-]{0,62}` — lowercase alphanumerics + hyphen, max 63 chars | Compatible with DNS labels, NATS tokens, URL segments, K8s names. No dot/slash/wildcard. |
| Reserved literals | Blocklist of 10 words: `users, hosts, projects, sessions, clusters, api, events, lifecycle, quota, terminal` | Prevents slug-literal collision. Leading `_` reserved for internal expansion. Append-only. |
| Slug ownership | All slugs system-computed from display names, never user-picked. Immutable after creation. | Users provide display names; system derives slugs silently. No rename API. |
| Display name vs slug | Separate fields. Display name: free-form UTF-8, max 128, mutable. Slug: auto-derived, immutable. | UI shows display name; subjects/URLs/keys use slug. |
| Subject construction | Typed Go wrappers (`type UserSlug string`, etc.) in `mclaude-common/pkg/subj` — raw strings are compile-time errors | Primary security benefit: user-sourced slugs cannot contain `.`, `*`, `>` that would be interpreted as NATS delimiters/wildcards. |
| Shared Go module | `mclaude-common/` at repo root, wired via `go.work` | `pkg/slug` (Slugify, Validate, ValidateOrFallback) + `pkg/subj` (typed subject builders). All Go components import it. |
| User slug derivation | `slugify(name-or-local-part)-{domain.split('.')[0]}` at creation. Collision → numeric suffix (`-2`, `-3`). | Deterministic. `richard@rbc.com` → `richard-rbc`, `richard@gmail.com` → `richard-gmail`. |
| Project/host/cluster slug | `slugify(display_name)` at creation; collision within scope → numeric suffix. Immutable. | Same no-user-prompt rule. |
| HTTP URL scheme | `/api/users/{uslug}/projects/{pslug}/...` for user-scoped. Auth + infra routes stay flat: `/auth/*`, `/health*`, `/metrics`. Admin: `/admin/users/{uslug}/...` | Logs read uniformly across NATS and HTTP. |
| KV key separator | Uniform `.` across all buckets | Matches NATS convention. Enables wildcard key matching. |
| Cross-user URL access | Hard 403 when JWT `sub` ≠ URL `{uslug}` | Simple, predictable, audit-friendly. Admin subtree bypasses with admin-role validation. |
| Git worktrees | One worktree per branch per project. `joinWorktree: true` to share. | Platform manages branch switching — `git checkout`/`git switch` blocked by guard hooks. |
| Tool installation | Nix single-user mode, per-project PVC at `/nix/`, `pkg` shim | Content-addressed deduplication within each project pod. |
| Claude CLI installation | Native binary via `claude install --version {pinned}` in Dockerfile. Image based on `node:22-alpine` (Claude Code runtime requires Node.js). | Version pinned. Updates go through `/upgrade-claude` skill. |
| Migration scope | Hard cutover for slug migration — no dual-path period | Pre-GA, all components deploy together via CI. No external users. |

---

## Component Topology

| Component | Language | Role |
|-----------|----------|------|
| `mclaude-session-agent` | Go | Spawns headless Claude Code processes, routes stream-json events to/from NATS. Same binary on laptop and in K8s pod. |
| `mclaude-control-plane` | Go | Platform control plane. Auth, SSO, SCIM, user/project/host management, NATS JWT issuance. K8s-free per ADR-0035 — all K8s mutation delegated to `mclaude-controller-k8s` via NATS request/reply. |
| `mclaude-cli` | Go | Debug attach tool. Thin text REPL over unix socket to session agent. |
| `mclaude-common` | Go | Shared module: slug/subject helpers, typed slug types. |
| `mclaude-web` | TypeScript (React 18) | Web SPA. Mobile browser first. |
| NATS JetStream | — | Event bus, state (KV), routing between all components and clients. |
| Postgres | — | Users, hosts, projects, oauth_connections tables. Lives in `mclaude-system`. |
| nginx ingress | — | Dumb reverse proxy. No routing logic, no auth. |

**Note**: the `mclaude-relay/` directory contains a standalone Go tunnel binary deployed to user VMs via the `deploy-relay` skill; only the in-cluster proxy role was replaced by nginx ingress. The binary is not published as a container image.

---

## Data Flow: User Input → Response

```
1. User types message in browser SPA
2. SPA publishes user message to NATS subject:
   mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.input
3. Session agent receives via NATS subscription
4. Session agent writes stream-json user message to Claude Code stdin pipe
5. Claude Code processes message, calls Anthropic API
6. Claude Code emits stream-json events on stdout (token-by-token streaming)
7. Session agent publishes each event to NATS JetStream:
   mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.events.{sslug}
8. SPA receives events via NATS subscription
9. SPA renders streaming response in real-time
```

---

## Claude Code Integration

The session agent spawns Claude Code headless:

```bash
claude --print --verbose \
  --output-format stream-json \
  --input-format stream-json \
  --include-partial-messages \
  --session-id {sessionId}
```

For session resume after pod restart:

```bash
claude --print --verbose \
  --output-format stream-json \
  --input-format stream-json \
  --include-partial-messages \
  --resume {sessionId}
```

`--print` disables the interactive TUI and trust dialog. Auth is handled via `ANTHROPIC_API_KEY` or `CLAUDE_CODE_OAUTH_TOKEN` env vars — no keychain needed. Hooks, LSP, auto-memory, and plugin discovery all run normally. `--include-partial-messages` enables token-by-token streaming.

Claude Code still writes JSONL internally (its own persistence for `--resume`). The session agent never reads JSONL.

### Stream-JSON Protocol

**Output (stdout)** — Claude Code emits NDJSON events:

```json
{"type": "system", "subtype": "init", "skills": [...], "tools": [...], "agents": [...], "model": "..."}
{"type": "system", "subtype": "session_state_changed", "state": "idle"}
{"type": "system", "subtype": "session_state_changed", "state": "running"}
{"type": "system", "subtype": "session_state_changed", "state": "requires_action"}
{"type": "assistant", "content": [...], "model": "...", "usage": {...}}
{"type": "stream_event", "event": {"type": "content_block_delta", ...}}
{"type": "user", "message": {...}}
{"type": "control_request", "request_id": "abc", "request": {"subtype": "can_use_tool", "tool_name": "Bash", ...}}
{"type": "tool_progress", "tool_use_id": "...", "tool_name": "Bash", "elapsed_time_seconds": 30}
{"type": "result", "subtype": "success", "usage": {...}, "duration_ms": 1234}
{"type": "clear"}
{"type": "compact_boundary"}
```

**Input (stdin)** — session agent writes:

```json
{"type": "user", "message": {"role": "user", "content": "fix the bug"}}
{"type": "user", "message": {"role": "user", "content": "/commit -m 'Fix bug'"}}
{"type": "user", "message": {"role": "user", "content": [{"type": "text", "text": "What's in this image?"}, {"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "iVBOR..."}}]}}
{"type": "control_response", "response": {"subtype": "success", "request_id": "abc", "response": {"behavior": "allow"}}}
{"type": "control_request", "request": {"subtype": "interrupt"}}
{"type": "control_request", "request": {"subtype": "reload_plugins"}}
{"type": "control_request", "request": {"subtype": "set_model", "model": "claude-opus-4-6"}}
{"type": "control_request", "request": {"subtype": "set_max_thinking_tokens", "max_thinking_tokens": 10000}}
```

Skills work via plain text `/commit` messages in user content. Images/files sent via standard Anthropic content arrays with base64-encoded data.

### Subagent Events

When Claude spawns a subagent, the subagent's events appear **flat** on the parent's stdout — not nested. Each event carries `parent_tool_use_id` linking it to the Agent tool_use that spawned it. The SPA uses `parent_tool_use_id` to render subagent events nested under the parent Agent block. Events with `parent_tool_use_id: null` are top-level. The session agent publishes all events verbatim regardless of nesting depth.

---

## NATS Subject Structure

All subjects use the typed-slug addressing scheme. Every slug token is preceded by a typed literal (`users`, `projects`, `sessions`, etc.), making subjects self-describing.

> **Host-scoping**: ADR-0035 extends this scheme by inserting `.hosts.{hslug}.` between user and project in all project-scoped subjects. See `adr-0035-unified-host-architecture.md` for the full mapping.

> **NATS security**: ADR-0016 defines the authentication model, signing-key hierarchy, JWT issuance, and per-identity permission grants. See `adr-0016-nats-security.md`.

### API — Session Commands (JetStream via MCLAUDE_API stream)

```
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.create     → captured by MCLAUDE_API, consumed by session-agent pull consumer
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.delete
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.input
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.control    → permission responses, interrupts
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.restart    → accepts optional extraFlags
```

### API — Project Management (request/reply via core NATS)

```
mclaude.users.{uslug}.hosts.{hslug}.api.projects.provision     → control-plane → controller (request/reply)
mclaude.users.{uslug}.hosts.{hslug}.api.projects.create        → control-plane → controller
mclaude.users.{uslug}.hosts.{hslug}.api.projects.update        → control-plane → controller
mclaude.users.{uslug}.hosts.{hslug}.api.projects.delete        → control-plane → controller
mclaude.users.{uslug}.api.projects.updated   → control-plane broadcasts (project state changed)
```

### Events (JetStream, append-only)

```
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.events.{sslug}       → Claude Code stream-json events
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.lifecycle.{sslug}     → session agent lifecycle events
```

`events` carries raw stream-json objects from Claude Code stdout — no envelope, the subject encodes the routing metadata. The session agent also publishes user input messages to the events stream. `lifecycle` carries session agent's own events (created, stopped, resumed, debug attached/detached).

Stream `MCLAUDE_EVENTS` captures `mclaude.users.*.hosts.*.projects.*.events.*`. Retained 30 days.
Stream `MCLAUDE_LIFECYCLE` captures `mclaude.users.*.hosts.*.projects.*.lifecycle.*`. Retained 30 days.

**NATS message size**: `max_payload: 8388608` (8MB) in NATS server config. If a single event exceeds 8MB, the session agent truncates the `content` field and sets `truncated: true`.

### Terminal I/O (core NATS, ephemeral)

```
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.terminal.{termId}.output    → raw PTY output bytes
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.terminal.{termId}.input     → raw keyboard input bytes
```

Not JetStream — raw terminal I/O is ephemeral, no replay needed. Use core NATS pub/sub for low latency.

### Terminal API

```
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.terminal.create    → spawn shell
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.terminal.delete     → kill terminal
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.terminal.resize     → resize PTY
```

### Quota

```
mclaude.users.{uslug}.quota    → broadcast signal, not request/reply
```

### JetStream Stream Filters

| Stream | Filter | Retention |
|--------|--------|-----------|
| `MCLAUDE_EVENTS` | `mclaude.users.*.hosts.*.projects.*.events.*` | 30 days |
| `MCLAUDE_LIFECYCLE` | `mclaude.users.*.hosts.*.projects.*.lifecycle.*` | 30 days |
| `MCLAUDE_API` | `mclaude.users.*.hosts.*.projects.*.api.sessions.>` | 1 hour (stale commands discarded) |

`MCLAUDE_API` captures session API commands (create, delete, input, restart, control) for at-least-once delivery. The session-agent consumes via durable pull consumers, not core NATS subscriptions.

---

## State (NATS KV)

```
KV bucket: mclaude-sessions
  key: {uslug}.{hslug}.{pslug}.{sslug}  → Session JSON

KV bucket: mclaude-projects
  key: {uslug}.{hslug}.{pslug}          → Project JSON

KV bucket: mclaude-hosts
  key: {uslug}.{hslug}                  → Host JSON (online status, type, role)

KV bucket: mclaude-job-queue
  key: {uslug}.{jobId}                  → Job JSON
```

**Bucket initialization**: control-plane creates all buckets idempotently on startup (`nats.KeyValueStoreOrCreate`). Session agents and launchers do not create buckets — they fail fast if a bucket doesn't exist.

**Entry lifetime**:
- `mclaude-sessions`: deleted by session agent on normal session delete. Orphaned entries swept by daily JSONL cleanup job.
- `mclaude-projects`: deleted by control-plane on project delete.

### Session State (KV value)

```json
{
  "id": "abc-123",
  "projectId": "proj-1",
  "branch": "feature/auth",
  "worktree": "feature-auth",
  "cwd": "/data/worktrees/feature-auth",
  "name": "Fix auth bug",
  "state": "idle",
  "stateSince": "2026-04-11T10:00:00Z",
  "createdAt": "2026-04-11T09:00:00Z",
  "model": "claude-sonnet-4-6",
  "extraFlags": "--disallowedTools \"Edit(src/**)\"",
  "userSlug": "alice-gmail",
  "hostSlug": "local",
  "projectSlug": "mclaude",
  "slug": "fix-auth-bug",
  "capabilities": {
    "skills": ["commit", "review-pr", "init"],
    "tools": ["Bash", "Read", "Edit", "Write", "Glob", "Grep"],
    "agents": ["general-purpose", "Explore", "Plan"]
  },
  "pendingControls": {},
  "usage": {
    "inputTokens": 12500,
    "outputTokens": 3200,
    "cacheReadTokens": 8000,
    "cacheWriteTokens": 4500,
    "costUsd": 0.042
  },
  "replayFromSeq": 1042
}
```

`state` maps directly from stream-json `session_state_changed` events: `"idle"`, `"running"`, `"requires_action"`.

`pendingControls` is a map of `request_id → control_request` — all unanswered permission prompts. The session agent adds an entry on `control_request` and removes it on `control_response`. No timeout — a prompt stays open until answered.

`capabilities` is populated from the `init` event on session start. Client reads from KV — one read, no stream replay needed for the skills picker.

`replayFromSeq` is the JetStream sequence number from which clients should start replaying events. Updated on `/clear` and compaction. Clients read this before subscribing.

### Project State (KV value)

```json
{
  "id": "proj-1",
  "slug": "mclaude",
  "userSlug": "alice-gmail",
  "hostSlug": "local",
  "name": "mclaude",
  "gitUrl": "git@github.com:org/mclaude.git",
  "gitIdentityId": null,
  "status": "running",
  "sessionCount": 2,
  "worktrees": ["main", "feature-auth"],
  "createdAt": "2026-04-01T00:00:00Z",
  "lastActiveAt": "2026-04-11T10:00:00Z"
}
```

### Session Agent Lifecycle Events

Published on `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.lifecycle.{sslug}`:

```json
{"type": "session_created", "sessionId": "abc-123", "ts": "..."}
{"type": "session_stopped", "sessionId": "abc-123", "exitCode": 0, "ts": "..."}
{"type": "session_restarting", "sessionId": "abc-123", "ts": "..."}
{"type": "session_resumed", "sessionId": "abc-123", "ts": "..."}
{"type": "session_failed", "sessionId": "abc-123", "error": "...", "ts": "..."}
{"type": "debug_attached", "sessionId": "abc-123", "ts": "..."}
{"type": "debug_detached", "sessionId": "abc-123", "ts": "..."}
```

---

## Slug System

### `slugify()` Algorithm

Lowercase → NFD Unicode decomposition → strip combining marks → replace runs of non-`[a-z0-9]` with `-` → trim leading/trailing `-` → truncate to 63 chars.

**Fallback**: if result is empty, matches a reserved word, or starts with `_`, emit `u-{6 base32 chars}` for users, `p-{6}` / `h-{6}` / `c-{6}` for projects/hosts/clusters. The 6 chars derive from the first 30 bits of the row's UUID for determinism.

### User Slug Derivation

`slugify(name-or-local-part)-{domain.split('.')[0]}` at user creation. Collision on same-(name,domain) pair → numeric suffix (`-2`, `-3`). Immutable after creation. Email changes do **not** rewrite the slug.

### Project / Host / Cluster Slug Derivation

`slugify(display_name)` at creation; collision within scope (per-user for project/host, global for cluster) → numeric suffix. Immutable.

### `mclaude-common` Shared Go Module

Layout:

```
mclaude-common/
├── go.mod                           (module mclaude.io/common)
└── pkg/
    ├── slug/                        (Slugify, Validate, ValidateOrFallback)
    └── subj/                        (typed subject-construction helpers)
```

- `pkg/slug`: `Slugify(displayName string) string`, `Validate(slug string) error`, `ValidateOrFallback(candidate string, kind Kind) string` where `kind ∈ {User, Project, Host, Cluster, Session}`. Reserved-word list is a typed constant.
- `pkg/subj`: typed subject-construction helpers keyed on named types (`type UserSlug string`, `type HostSlug string`, `type ProjectSlug string`, etc.). Helpers accept only typed wrappers — passing a raw string is a compile-time error. All project-scoped helpers include `HostSlug` per ADR-0035. Examples: `subj.UserHostProjectAPISessionsCreate(u UserSlug, h HostSlug, p ProjectSlug) string` returns `mclaude.users.{u}.hosts.{h}.projects.{p}.api.sessions.create`; `subj.UserHostProjectEvents(u UserSlug, h HostSlug, p ProjectSlug, s SessionSlug) string` returns `mclaude.users.{u}.hosts.{h}.projects.{p}.events.{s}`. KV key helpers: `subj.SessionsKVKey(u, h, p, s)` → `{u}.{h}.{p}.{s}`; `subj.ProjectsKVKey(u, h, p)` → `{u}.{h}.{p}`; `subj.HostsKVKey(u, h)` → `{u}.{h}`.

### TypeScript Mirrors

- `src/lib/slug.ts`: mirrors `pkg/slug` (Slugify + Validate + Fallback) for display consistency.
- `src/lib/subj.ts`: mirrors `pkg/subj`. Publishes via typed helpers only. Runtime assertion in dev builds.

### Postgres Slug Columns

```sql
-- users: slug is globally unique
ALTER TABLE users ADD COLUMN slug TEXT NOT NULL UNIQUE;

-- hosts: slug is unique per user (per ADR-0035)
-- For cluster hosts, all users granted to the same cluster share the same slug.
ALTER TABLE hosts ADD COLUMN slug TEXT NOT NULL;
-- UNIQUE (user_id, slug) enforced via table constraint

-- projects: slug is unique per user per host (per ADR-0035)
ALTER TABLE projects ADD COLUMN slug TEXT NOT NULL;
CREATE UNIQUE INDEX projects_user_id_host_id_slug_uniq ON projects (user_id, host_id, slug);
```

`users.id`, `projects.id`, `hosts.id` UUID PKs stay. All foreign keys reference `id`, not `slug`.

### Error Handling

- **Slug validation at ingress**: control-plane returns HTTP 400 with `{code:"invalid_slug", reason:"reserved_word|charset|length", field:"slug"}`.
- **Reserved-word match**: `slugify()` fallback kicks in automatically.
- **Unicode / empty / emoji-only display names**: slugify runs NFD + charset replacement; if result is empty, falls back.
- **Subject-construction guardrail**: `pkg/subj` and `src/lib/subj.ts` accept only typed slug structs.
- **Cross-user URL access**: middleware compares JWT `sub` claim's `uslug` with URL's `{uslug}`. Mismatch → 403.

---

## mclaude-session-agent

Single Go binary. Runs as a container inside each K8s project pod, or as a standalone daemon on a laptop. Identical code path — the only difference is how it connects to NATS.

### What It Does

- Subscribes to `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.>` — handles session CRUD, input, and control messages
- Spawns Claude Code as child processes with `--print --verbose --output-format stream-json --input-format stream-json`
- Routes stdout JSON events → NATS JetStream (raw, no envelope)
- Routes NATS input/control messages → Claude stdin
- Tracks session state from `session_state_changed` events, writes to NATS KV
- Caches capabilities from `init` event in NATS KV
- Spawns terminal (PTY) sessions via `creack/pty`, routes raw I/O through NATS
- Exposes unix socket for `mclaude-cli` debug attach
- On startup, reads NATS KV for existing sessions → relaunches with `--resume`

No tmux. No JSONL tailing. No screen scraping. No HTTP server.

### Core Loop

```go
cmd := exec.Command("claude",
    "--print", "--verbose",
    "--output-format", "stream-json",
    "--input-format", "stream-json",
    "--include-partial-messages",
    "--session-id", sessionID)

stdin, _ := cmd.StdinPipe()
stdout, _ := cmd.StdoutPipe()

// Stdin serialization — multiple NATS messages must not interleave JSON lines.
stdinCh := make(chan []byte, 64)
go func() {
    for msg := range stdinCh {
        stdin.Write(msg)
        stdin.Write([]byte("\n"))
    }
}()

// stdout → NATS (host-scoped subject per ADR-0035)
eventSubj := subj.UserHostProjectEvents(userSlug, hostSlug, projectSlug, sessionSlug)
go func() {
    scanner := bufio.NewScanner(stdout)
    scanner.Buffer(make([]byte, 0), 16*1024*1024) // 16MB buffer for large events
    for scanner.Scan() {
        line := scanner.Bytes()
        nats.Publish(eventSubj, line)
        if eventType := parseEventType(line); eventType != "" {
            switch eventType {
            case "session_state_changed": updateKV(line)
            case "control_request": updatePendingControl(line)
            case "result": accumulateUsage(line)
            case "clear", "compact_boundary": updateReplayFromSeq(line, jetStreamSeq)
            }
        }
    }
}()

// NATS → stdin via MCLAUDE_API JetStream pull consumer (at-least-once delivery)
// Session-agent creates durable pull consumers on MCLAUDE_API stream:
//   - Command consumer (sa-cmd-{uslug}-{pslug}): create, delete, input, restart
//   - Control consumer (sa-ctl-{uslug}-{pslug}): control responses
go func() {
    for {
        msgs, _ := cmdConsumer.Fetch(10, nats.MaxWait(30*time.Second))
        for _, msg := range msgs {
            stdinCh <- msg.Data()
            msg.Ack()
        }
    }
}()
```

### Session Operations

| NATS subject | Action |
|--------------|--------|
| `…api.sessions.create` | Spawn Claude with `--session-id` + shell-parsed `extraFlags` |
| `…api.sessions.delete` | Send interrupt control request → wait for exit → kill if timeout |
| `…api.sessions.input` | Write user message JSON to stdin pipe |
| `…api.sessions.control` | Write control_response JSON to stdin pipe |
| `…api.sessions.restart` | Kill process → if `extraFlags` in payload, update KV → relaunch with `--resume {sessionId}` |

### Permission Handling

`control_request` events are always emitted on stdout. The session agent publishes them to NATS. The client responds with a `control_response` via the `.api.sessions.control` subject.

For auto-approve workflows (CI, batch jobs):

```yaml
permissionPolicy: "auto"      # auto-approve all tools
# permissionPolicy: "managed"  # forward to client (default)
# permissionPolicy: "allowlist" # auto-approve listed tools, forward rest
# allowedTools: ["Bash", "Read", "Edit", "Write", "Glob", "Grep"]
```

### Startup / Recovery

```
1. Read NATS KV for all sessions with this projectId
2. Set all session KV entries to state: "restarting", clear pendingControls
3. Publish session_restarting lifecycle events
4. For each session: claude --resume {sessionId}
5. On init event: update KV with fresh state
6. Publish session_resumed lifecycle events
7. Sessions that fail to start within 30s: mark state: "failed", publish session_failed
```

No HTTP polling. No dependency on another service being up. Recovery is the same for graceful and ungraceful restarts — the agent always re-derives state from Claude Code.

### Graceful Shutdown (Zero-Downtime Upgrades)

On SIGTERM (pod termination), the shutdown sequence preserves in-progress work:

1. Write `state: "updating"` to KV for all sessions (SPA displays upgrade banner). Set `shutdownPending` flag to suppress further KV state flushes.
2. Cancel the command consumer (new commands queue in JetStream for the replacement pod).
3. Drain core NATS subscriptions (terminal API).
4. Keep the control consumer running (interrupts and permission responses still work).
5. Poll every 1 second: wait for all sessions to reach `idle` state with zero in-flight background agents. Auto-interrupt sessions stuck in `requires_action`.
6. Cancel the control consumer.
7. Publish `session_upgrading` lifecycle event per session.
8. Exit.

Set `terminationGracePeriodSeconds: 86400` (24h) in pod spec — the long window allows Claude to finish work naturally before the pod is replaced.

### Worktrees

Branch slugification: `feature/auth` → `feature-auth`.

Session create request payload:
```json
{
  "name": "Fix auth bug",
  "branch": "feature/auth",
  "cwd": "packages/api",
  "joinWorktree": false,
  "extraFlags": "--disallowedTools \"Edit(mclaude-web/src/**)\" --model claude-opus-4-7"
}
```

`branch` is optional — if omitted, derived from `name` via slugification. If both omitted, uses `session-{shortId}`. `extraFlags` is persisted in KV and re-applied on every pod restart.

| `joinWorktree` | Worktree exists? | Behaviour |
|----------------|-----------------|-----------|
| `false` (default) | No | Create worktree, spawn Claude |
| `false` (default) | Yes | Error: "branch already has an active session — set joinWorktree: true" |
| `true` | No | Create worktree, spawn Claude |
| `true` | Yes | Reuse existing worktree, spawn Claude |

Every project has a bare repo at `/data/repo` — initialized by the entrypoint (`git clone --bare` for git-backed, `git init --bare` for scratch projects).

### Debug Attach (mclaude-cli)

Session agent exposes a unix socket per session at `/tmp/mclaude-session-{id}.sock`. The `mclaude-cli` tool connects and provides a text REPL (~150 lines of Go).

For K8s: `kubectl exec -it pod -- mclaude-cli attach {sessionId}`

### Laptop Mode

On a laptop, **one session-agent per project** — same scoping as K8s. The `mclaude-controller-local` (BYOH process supervisor per ADR-0035) manages per-project agents:

- `mclaude-controller-local` runs as a launchd/systemd daemon with `--host {hslug}`
- Subscribes to `mclaude.users.{uslug}.hosts.{hslug}.api.projects.>` and manages session-agent processes
- Session-agent runs with `--daemon --host {hslug}` mode
- Each session-agent connects to NATS via `wss://mclaude.example.com/nats` (outbound, works behind NAT/firewall)
- NATS JWT stored in `~/.mclaude/hosts/{hslug}/nats.creds`
- Controller monitors child agents, restarts on crash
- JWT refresh: background goroutine checks `exp` every 60s, calls `POST /auth/refresh` when TTL < 15 minutes

---

## mclaude-control-plane

Single Go service in `mclaude-system`. Owns user identity, host management, NATS credential management (per-host JWT issuance), and project provisioning via NATS request/reply to controllers. K8s-free per ADR-0035 — no kubebuilder, no K8s client, no MCProject CRD reconciler. All K8s mutation is delegated to `mclaude-controller-k8s` via NATS. Owns Postgres (users, hosts, projects, oauth_connections). Issues per-host NATS JWTs signed by the deployment-level account key.

### Project Provisioning (NATS request/reply)

The control-plane does not touch K8s directly. On project creation, it publishes a NATS request to the host-scoped provisioning subject and waits for the controller's reply:

```
Subject: mclaude.users.{uslug}.hosts.{hslug}.api.projects.provision
Timeout: 10s (PROVISION_TIMEOUT_SECONDS)
```

The relevant controller handles the request:
- **`mclaude-controller-k8s`** (cluster hosts): subscribes to `mclaude.users.*.hosts.{cluster-slug}.api.projects.>` — reconciles `MCProject` CRs, provisions namespace/RBAC/PVCs/Secrets/Deployment.
- **`mclaude-controller-local`** (BYOH machines): subscribes to `mclaude.users.{uslug}.hosts.{hslug}.api.projects.>` — manages session-agent processes and local worktree directories.

If the controller times out or replies with an error, control-plane returns 503 with `{error: "host {hslug} unreachable"}`.

### Endpoints

**Auth**

| Method | Path | Action |
|--------|------|--------|
| `POST` | `/auth/login` | local credentials → NATS JWT + nkey seed |
| `POST` | `/auth/refresh` | refresh NATS JWT |
| `POST` | `/api/providers/{id}/connect` | initiate OAuth flow for a configured provider; returns `{redirectUrl}` |
| `GET` | `/auth/providers/{id}/callback` | OAuth callback — exchanges code for token, stores connection, redirects browser |

**Users (admin)**

| Method | Path | Action |
|--------|------|--------|
| `POST` | `/admin/users` | create user |
| `GET` | `/admin/users` | list users |
| `DELETE` | `/admin/users/{id}` | deprovision user + delete namespace |

**Projects (user-scoped)**

| Method | Path | Action |
|--------|------|--------|
| `POST` | `/api/users/{uslug}/projects` | create project (delegates K8s resources to controller via NATS) |
| `GET` | `/api/users/{uslug}/projects` | list projects (reads NATS KV) |
| `GET` | `/api/users/{uslug}/projects/{pslug}` | get project status |
| `DELETE` | `/api/users/{uslug}/projects/{pslug}` | delete project (PVC retained unless `?purge=true`) |

**Host management (user-scoped)**

| Method | Path | Action |
|--------|------|--------|
| `GET` | `/api/users/{uslug}/hosts` | list hosts owned by or granted to user |
| `POST` | `/api/users/{uslug}/hosts/code` | generate 6-char device code for BYOH host registration (accepts `{publicKey}`) |
| `GET` | `/api/users/{uslug}/hosts/code/{code}` | poll device-code status (`pending` → `completed`) |
| `POST` | `/api/hosts/register` | redeem device code from dashboard with `{code, name}` |
| `PUT` | `/api/users/{uslug}/hosts/{hslug}` | update host display name |
| `DELETE` | `/api/users/{uslug}/hosts/{hslug}` | remove a host (cascades to projects + sessions) |

**Cluster admin (admin-only, bearer token + `is_admin` check)**

| Method | Path | Action |
|--------|------|--------|
| `POST` | `/admin/clusters` | register a new cluster (`{slug, name, jsDomain, leafUrl, directNatsUrl?}`) |
| `GET` | `/admin/clusters` | list registered clusters |
| `POST` | `/admin/clusters/{cslug}/grants` | grant user access to cluster (`{userSlug}`) |
| `DELETE` | `/admin/clusters/{cslug}` | remove cluster (deletes all host rows for that cluster slug) |

**SCIM 2.0**

| Method | Path | Action |
|--------|------|--------|
| `POST` | `/scim/v2/Users` | IdP provisions user |
| `PUT` | `/scim/v2/Users/{id}` | IdP updates user |
| `DELETE` | `/scim/v2/Users/{id}` | IdP deprovisions user |
| `GET` | `/scim/v2/Users` | IdP syncs user list |

**Break-glass admin** (port `:9091`, loopback-only, never exposed via nginx)

```
GET    /metrics                  Prometheus metrics
```

The `/admin/*` routes (cluster register, grant, user management, session stop) are served on the **main** port (8080) and protected by per-user `Authorization: Bearer <token>` plus a server-side `users.is_admin` check — see "Users (admin)" and "Cluster admin" sections above.

### Postgres Schema

```sql
CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    slug          TEXT NOT NULL UNIQUE,
    email         TEXT UNIQUE NOT NULL,
    name          TEXT NOT NULL,
    password_hash TEXT NOT NULL DEFAULT '',
    oauth_id      TEXT,
    is_admin      BOOLEAN NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE hosts (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    slug            TEXT NOT NULL,
    name            TEXT NOT NULL,
    type            TEXT NOT NULL CHECK (type IN ('machine', 'cluster')),
    role            TEXT NOT NULL DEFAULT 'owner' CHECK (role IN ('owner', 'user')),
    js_domain       TEXT,
    leaf_url        TEXT,
    account_jwt     TEXT,
    direct_nats_url TEXT,
    public_key      TEXT,
    user_jwt        TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at    TIMESTAMPTZ,
    UNIQUE (user_id, slug),
    CHECK (type = 'machine' OR (js_domain IS NOT NULL AND leaf_url IS NOT NULL AND account_jwt IS NOT NULL))
);

CREATE TABLE projects (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    slug            TEXT NOT NULL DEFAULT '',
    git_url         TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'active',
    host_id         TEXT NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    git_identity_id TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX projects_user_id_host_id_slug_uniq ON projects (user_id, host_id, slug);

CREATE TABLE oauth_connections (
    id               TEXT PRIMARY KEY,
    user_id          TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider_id      TEXT NOT NULL,
    provider_type    TEXT NOT NULL,
    auth_type        TEXT NOT NULL DEFAULT 'oauth',
    base_url         TEXT NOT NULL,
    display_name     TEXT NOT NULL DEFAULT '',
    provider_user_id TEXT NOT NULL,
    username         TEXT NOT NULL,
    scopes           TEXT NOT NULL DEFAULT '',
    token_expires_at TIMESTAMPTZ,
    connected_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, base_url, provider_user_id)
);
```

Schema applied on startup via idempotent DDL (`CREATE TABLE IF NOT EXISTS`, `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`). NATS credentials are not stored in Postgres — per-host user JWTs are signed by the account key from the `mclaude-system/operator-keys` K8s Secret and stored in `hosts.user_jwt`; BYOH machine NKey seeds live on disk at `~/.mclaude/hosts/{hslug}/nkey.seed`.

### User Provisioning Flow

1. User created (local POST /admin/users, SSO first login, or SCIM push)
2. INSERT into Postgres users table (slug auto-derived from name/email)
3. Default `local` machine host created for the user in the `hosts` table
4. K8s namespace and RBAC created by `mclaude-controller-k8s` when first project is provisioned on a cluster host

### User Deprovision Flow

1. DELETE /users/{id} (or SCIM DELETE)
2. Revoke user's NATS JWT via account JWT revocations
3. NATS broker terminates all active connections immediately
4. DELETE from Postgres (cascades to hosts, projects, oauth_connections)
5. Publish NATS delete requests to affected controllers for resource cleanup
6. Controllers tear down owned resources (K8s namespaces for cluster hosts, local worktrees for machine hosts)

### Project Provisioning Flow

1. SPA calls `POST /api/users/{uslug}/projects` with `{name, hostSlug, gitUrl?}`
2. Control-plane writes Postgres `projects` row (with `host_id` resolved from `(user_id, hostSlug)`)
3. Control-plane writes Project JSON to NATS KV `mclaude-projects` at key `{uslug}.{hslug}.{pslug}`
4. Control-plane publishes NATS request to `mclaude.users.{uslug}.hosts.{hslug}.api.projects.provision`
5. Controller (k8s or local) provisions resources and replies with success/failure
6. Control-plane returns result to SPA

---

## Pod Structure (one per project)

```
Pod: project-{projectId}            namespace: mclaude-{userId}
├── container: session-agent
│   image: mclaude-session-agent:{version}
│   ├── project PVC      → /data/              (RW) repo, worktrees, shared-memory
│   ├── nix-store PVC    → /nix/               (RW) per-project Nix store
│   ├── claude-home      → ~/.claude/           (RW) emptyDir, ephemeral
│   ├── user-config      → ~/.claude-seed/      (RO) ConfigMap seed
│   └── user-secrets     → ~/.user-secrets/     (RO) Secret
├── container: config-sync
│   image: mclaude-config-sync:{version}
│   watches ~/.claude/ → patches user-config ConfigMap on change
└── container: dockerd-rootless     (optional — per-project flag)
    image: docker:dind-rootless
```

### `/data/` Layout

```
/data/
  repo/             bare git repo
  worktrees/
    main/
    feature-auth/
  shared-memory/    auto-memory symlinked across all worktrees
  projects/         symlinked to ~/.claude/projects/ (JSONL history — Claude's own, for --resume)
```

---

## 3-Tier Storage

| Tier | Scope | Storage | Contents |
|------|-------|---------|----------|
| **User** | Per namespace | ConfigMap + Secret | CLAUDE.md, settings.json, skills, commands, credentials |
| **Project** | Per project | PVC (RWO, managed-csi-premium) | Bare git repo, worktrees, JSONL (Claude's own persistence), shared memory |
| **Session** | Per session | NATS KV | id, branch, worktree, cwd, state, capabilities, pendingControls |
| **Home** | Per pod | emptyDir | Seeded from ConfigMap + Secret on boot. Ephemeral. |

---

## Home Directory + Config Sync

`$HOME` is an emptyDir — fresh on every pod start, writable, not persisted. Credentials belong in K8s Secrets.

**On boot**, entrypoint seeds `$HOME` from:
- K8s Secret: SSH keys, OAuth token, `.gitconfig`
- ConfigMap: `settings.json`, `CLAUDE.md`, commands, skills

**Git credential helper registration** is performed by the session-agent binary (via `CredentialManager.Setup`) on every pod start:
1. Symlink `/data/.config → ~/.config` (PVC-backed, survives pod restart)
2. Merge managed gh/glab token files from Secret mount into `~/.config/gh/hosts.yml` and `~/.config/glab-cli/config.yml`
3. Run `gh auth setup-git` — registers gh as a git HTTPS credential helper
4. Run `glab auth setup-git` (non-fatal if glab is not installed)

**config-sync sidecar** watches `~/.claude/settings.json` and `CLAUDE.md` for writes via inotify. On change, patches the `user-config` ConfigMap. `mclaude-config-sync` is a dedicated image with inotify-tools, kubectl, and jq pre-installed.

---

## Auto-Memory Sharing

Claude Code stores auto-memories per working directory at `~/.claude/projects/{encoded-cwd}/memory/`. Different worktrees have different cwds → separate memories by default.

Since memories (feedback, project context) should be shared across all branches of the same project, the entrypoint runs a background loop that symlinks each worktree's memory directory to a single shared location on the PVC (`/data/shared-memory/`).

---

## Managed Platform Config

### CLAUDE.md Three-Tier System

| Tier | Location | Controlled by | Can user override? |
|------|----------|--------------|-------------------|
| **Global (managed policy)** | `/etc/claude-code/CLAUDE.md` | Platform (baked into session image) | No |
| **User** | `~/.claude/CLAUDE.md` | User (synced via ConfigMap + config-sync sidecar) | Yes |
| **Project** | `{worktree}/CLAUDE.md` | Repo (committed to git) | Yes |

### /etc/claude-code/CLAUDE.md (managed policy)

```markdown
# MClaude Platform

## Environment
You are running in a Kubernetes pod.
- `/data/repo/` — bare git repo
- `/data/worktrees/{branch}/` — git worktrees
- `/data/shared-memory/` — auto-memory shared across worktrees
- `$HOME` is ephemeral — rebuilt on every pod restart

## Git
Branch switching is managed by the platform. Do not use `git checkout` or `git switch`.
The bare repo is at `/data/repo/`. Do not modify it directly.

## Tool Installation
Use `pkg install <package>`. Do not use `apt install` or `apt-get`.
Tools are cached in the shared Nix store and persist across pod restarts.

## Shell
- `~/.zshrc.local` for ephemeral shell additions (not synced)
- `~/.env.secrets` for credentials (sourced by .zshrc, written by entrypoint)
- Do not write secrets to `~/.zshrc` — it syncs to ConfigMap

## Docker
Docker is available via `DOCKER_HOST` if enabled for this project.
```

### Guard Hooks (/etc/claude-code/hooks/guard.sh)

Platform hooks enforce constraints at the Bash tool execution level — stricter than CLAUDE.md instructions:

```bash
#!/bin/bash
COMMAND=$(cat | jq -r '.input.command // empty')

# Block git branch switching (platform manages worktrees)
if echo "$COMMAND" | grep -qE '^\s*git\s+(checkout|switch)\s'; then
    echo "BLOCK: Branch switching is managed by the platform." >&2
    exit 2
fi
# Block real apt (use pkg shim)
if echo "$COMMAND" | grep -qE '(^|\s|/)(apt-get|apt)\s+install'; then
    echo "BLOCK: Use 'pkg install <package>' instead." >&2
    exit 2
fi
# Block modifying managed platform config
if echo "$COMMAND" | grep -qE '/etc/claude-code/'; then
    echo "BLOCK: Managed platform config cannot be modified." >&2
    exit 2
fi
# Block nuking critical paths
if echo "$COMMAND" | grep -qE 'rm\s+(-rf|-fr)\s+/(data/repo|nix|etc)\b'; then
    echo "BLOCK: Cannot delete platform-managed directories." >&2
    exit 2
fi
exit 0
```

---

## Tool Installation (Nix)

Nix store (`/nix/`) lives on a per-project PVC (`nix-{projectId}`).

```bash
# /usr/local/bin/pkg — shim
if [ "$1" = "install" ]; then
    shift; for p in "$@"; do nix profile install "nixpkgs#$p"; done
elif [ "$1" = "remove" ]; then
    shift; for p in "$@"; do nix profile remove "$p"; done
fi
```

`apt` and `brew` are shimmed to `pkg`. Content-addressed caching means packages are downloaded once and deduplicated.

---

## Registry Mirror System

Enterprise deployments need package managers configured to pull from internal mirrors. The session image includes platform hooks that read from a `mirrors.json` file (mounted from a ConfigMap). If the file doesn't exist (personal laptop), hooks skip — tools use public defaults.

**Mirror schema** (consumed by hook renderers):
```json
[
  {
    "origin": "https://registry.npmjs.org",
    "mirror": "https://npm.internal.example.com/",
    "type": "npm",
    "auth": { "secretRef": { "name": "registry-creds", "key": "token" } },
    "tls": { "caBundle": "corporate-ca" },
    "scopes": ["@myorg"]
  }
]
```

Supported types: `npm` (→ `.npmrc`), `pypi` (→ `pip.conf`), `go` (→ `GOPROXY`), `nix` (→ `nix.conf`).

---

## Entrypoint (session-agent container)

```bash
#!/bin/bash
set -e

# Consume secrets
[ -f "/home/node/.user-secrets/id_rsa" ] && {
    mkdir -p "$HOME/.ssh" && chmod 700 "$HOME/.ssh"
    cp /home/node/.user-secrets/id_rsa "$HOME/.ssh/id_rsa"
    chmod 600 "$HOME/.ssh/id_rsa"
    ssh-keyscan github.com >> "$HOME/.ssh/known_hosts" 2>/dev/null || true
}
[ -f "/home/node/.user-secrets/.gitconfig" ] && \
    cp /home/node/.user-secrets/.gitconfig "$HOME/.gitconfig"
[ -f "/home/node/.user-secrets/oauth-token" ] && \
    export CLAUDE_CODE_OAUTH_TOKEN=$(cat /home/node/.user-secrets/oauth-token)

# Seed user config (emptyDir is fresh each boot — always copy)
for f in CLAUDE.md settings.json; do
    [ -f "/home/node/.claude-seed/$f" ] && \
        cp "/home/node/.claude-seed/$f" "$HOME/.claude/$f"
done
for d in commands skills; do
    [ -d "/home/node/.claude-seed/$d" ] && \
        cp -r "/home/node/.claude-seed/$d" "$HOME/.claude/$d"
done

# Link JSONL history to PVC (Claude's own persistence for --resume)
mkdir -p /data/projects
ln -sf /data/projects "$HOME/.claude/projects"

# Skip onboarding
echo '{"hasCompletedOnboarding":true,"bypassPermissions":true}' > "$HOME/.claude.json"

# Git setup (bare repo — worktrees created by session agent)
if [ ! -d "/data/repo/HEAD" ]; then
    if [ -n "$GIT_URL" ]; then
        git clone --bare "$GIT_URL" /data/repo || {
            echo "[entrypoint] Git clone failed — exiting for restart"
            exit 1
        }
    else
        git init --bare /data/repo
        git -C /data/repo commit --allow-empty -m "init" \
            --author="mclaude <mclaude@local>" 2>/dev/null || true
    fi
else
    if [ -n "$GIT_URL" ]; then
        git -C /data/repo fetch --all --prune || true
    fi
fi
mkdir -p /data/worktrees

# Shared memory — symlink each worktree's memory dir to /data/shared-memory/
mkdir -p /data/shared-memory
(while true; do
    for dir in "$HOME/.claude/projects"/*/; do
        [ -d "$dir" ] && [ ! -L "${dir}memory" ] && {
            rm -rf "${dir}memory"
            ln -s /data/shared-memory "${dir}memory"
        }
    done
    sleep 5
done) &

# Wait for dockerd if enabled
[ "${DOCKER_ENABLED}" = "true" ] && \
    while [ ! -S /var/run/docker.sock ]; do sleep 0.5; done

# Platform hooks (registry mirrors, etc.)
if [ -d "/etc/mclaude/hooks.d" ]; then
    for hook in /etc/mclaude/hooks.d/*.sh; do
        [ -x "$hook" ] && source "$hook"
    done
fi

# Hand off to session agent
exec session-agent \
    --nats-url    "${NATS_URL}" \
    --nats-creds  "/home/node/.user-secrets/nats-creds" \
    --user-id     "${USER_ID}" \
    --user-slug   "${USER_SLUG}" \
    --host        "${HOST_SLUG}" \
    --project-id  "${PROJECT_ID}" \
    --project-slug "${PROJECT_SLUG}" \
    --data-dir    /data \
    --mode        k8s
```

### Environment Variables

| Var | Source | Description |
|-----|--------|-------------|
| `GIT_URL` | Deployment env (from project-config ConfigMap) | Git repo URL |
| `USER_ID` | Deployment env | User UUID |
| `USER_SLUG` | Deployment env | User typed slug |
| `HOST_SLUG` | Deployment env | Host typed slug (injected by mclaude-controller-k8s) |
| `PROJECT_ID` | Deployment env | Project UUID |
| `PROJECT_SLUG` | Deployment env | Project typed slug |
| `NATS_URL` | Deployment env | NATS connection URL |
| `DOCKER_ENABLED` | Deployment env (optional) | Enable dockerd sidecar |
| `CLAUDE_CODE_OAUTH_TOKEN` | Secret mount | Anthropic OAuth token |
| `HTTPS_PROXY` | Deployment env (optional) | HTTP proxy for Anthropic API |

---

## Terminal Access

The session agent manages two types of sessions:

1. **Claude sessions** — headless stream-json, JSON on stdin/stdout
2. **Terminal sessions** — interactive PTY, raw bytes on stdin/stdout

Terminal sessions are spawned via the same NATS API. The session agent spawns a shell using `creack/pty` (Go PTY library):

```go
cmd := exec.Command("/bin/zsh")
cmd.Dir = "/data/worktrees/" + branch
cmd.Env = append(os.Environ(), "TERM=xterm-256color")
ptmx, _ := pty.Start(cmd)

// PTY output → NATS (raw bytes, max 4KB per message, host-scoped subject)
termOutSubj := subj.UserHostProjectAPITerminal(userSlug, hostSlug, projectSlug, termId+".output")
go func() {
    buf := make([]byte, 4096)
    for {
        n, _ := ptmx.Read(buf)
        nats.Publish(termOutSubj, buf[:n])
    }
}()

// NATS → PTY input
termInSubj := subj.UserHostProjectAPITerminal(userSlug, hostSlug, projectSlug, termId+".input")
go func() {
    sub := nats.Subscribe(termInSubj)
    for msg := range sub.Chan() {
        ptmx.Write(msg.Data)
    }
}()
```

The web SPA renders terminal sessions with **xterm.js**. Terminal sessions share the same filesystem as Claude sessions.

### Real-time Claude Output

In headless stream-json mode, there is no terminal showing Claude's TUI:

| Event type | Latency | Content |
|-----------|---------|---------|
| `stream_event` (content_block_delta) | Token-by-token | Claude's text as it types |
| `tool_use` | Instant | Tool name + input |
| `tool_progress` | Periodic (~5s) | Elapsed time only — no stdout |
| `tool_result` | After completion | Full stdout/stderr |
| `control_request` | Instant | Permission prompt |

For long-running Bash commands, build output appears all at once (not streaming). The SPA shows animated elapsed-time indicators from `tool_progress` events. If Claude Code adds stdout streaming to `tool_progress` in the future, the architecture supports it immediately — no change needed.

---

## Web SPA

Mobile browser first — enterprise constraint.

- **iOS Safari, Android Chrome** — primary
- **Desktop browser** — same SPA
- **Framework**: React 18

**Real-time events**: client connects to NATS via `/nats` WebSocket proxy. Subscribes to `mclaude.users.{uslug}.hosts.*.projects.{pslug}.events.>` (wildcard on host). Events are raw stream-json.

**Rendering**: the SPA consumes stream-json event types:
- `stream_event` → live streaming text token-by-token
- `assistant` → complete message (replaces streamed deltas)
- `tool_use` → collapsible block
- `tool_progress` → elapsed time indicator
- `control_request` → approve/deny buttons
- `session_state_changed` → status indicator
- `init` → populate skills picker, tool list, model info
- `result` → usage/cost display
- Events with `parent_tool_use_id` → render nested under parent Agent block

**Model/effort switching**: SPA sends `set_model` and `set_max_thinking_tokens` control requests mid-session.

**Cost tracking**: `result` events include `usage`. Session agent accumulates in NATS KV. SPA shows per-session and per-project cost.

**File/image uploads**: base64 in user message content array. Max ~20MB per image.

**State**: client watches NATS KV buckets for live updates.

**Event replay**: on reconnect, re-subscribe from `max(lastSeenSeq + 1, replayFromSeq)`. Clients deduplicate by JetStream sequence number.

**Background reconnect** (mobile browser):
```js
document.addEventListener('visibilitychange', () => {
  if (document.visibilityState !== 'visible') return;
  nc.reconnect();
  kv.watch(`{uslug}/>`);
});
```

**Skills picker**: populated from `init` event's `skills` array, cached in KV.

**Routes**: `/api/users/{uslug}/projects/{pslug}/sessions/{sslug}`. Display names render in UI; slugs in the path. React Router v6 parametric segments.

---

## NATS Authentication

> Full security model defined in `adr-0016-nats-security.md`. Summary here for context.

**Login flow**:

```
1. Client POST /auth/login → control-plane
2. Validate credentials against Postgres
3. Issue NATS User JWT scoped to mclaude.users.{uslug}.>
4. Return { natsUrl, jwt, nkeySeed }
5. Client connects to wss://mclaude.example.com/nats using JWT
6. NATS broker validates JWT, enforces subject permissions
```

JWT expiry: 8h (configurable via `JWT_EXPIRY_SECONDS`). Refresh threshold: 15min (configurable via `JWT_REFRESH_THRESHOLD_SECONDS`). SPA checks TTL every 60s.

Session-agent receives per-project NATS credentials provisioned by control-plane (project-scoped publish/subscribe grants).

---

## nginx Ingress

```nginx
location /nats {
    proxy_pass         http://nats.mclaude-system:8080;
    proxy_http_version 1.1;
    proxy_set_header   Upgrade    $http_upgrade;
    proxy_set_header   Connection "upgrade";
    proxy_read_timeout 3600s;
}
location /auth { proxy_pass http://mclaude-control-plane:8080; }
location /api  { proxy_pass http://mclaude-control-plane:8080; }
location /scim { proxy_pass http://mclaude-control-plane:8080; }
location /     { proxy_pass http://mclaude-spa:80; }
```

No auth logic. No routing decisions.

---

## Image Build Pipeline

Push to `main` → CI builds changed components → pushes to `ghcr.io/richardmsong/<image>`. Two tags per build: `main-<7-char-sha>` (immutable) and `main` (moving). `:latest` is never published.

| Image | Repository | Contents |
|-------|-----------|----------|
| control-plane | `ghcr.io/richardmsong/mclaude-control-plane` | control-plane binary, kubectl, dbmate |
| SPA | `ghcr.io/richardmsong/mclaude-spa` | built SPA static files + nginx |
| session-agent | `ghcr.io/richardmsong/mclaude-session-agent` | session-agent binary, mclaude-cli binary, Claude CLI, git, Nix, zsh, pkg shim, guard hooks |

---

## Health Probes

**session-agent pod:**
```yaml
livenessProbe:
  exec:
    command: ["session-agent", "--health"]  # process alive + NATS connection
  initialDelaySeconds: 10
  periodSeconds: 30
readinessProbe:
  exec:
    command: ["session-agent", "--ready"]  # NATS connected + can spawn claude
  initialDelaySeconds: 5
  periodSeconds: 10
```

**control-plane pod:**
```yaml
livenessProbe:
  httpGet:
    path: /healthz    # never checks NATS — pod must stay alive for break-glass admin port
    port: 8080
  periodSeconds: 15
readinessProbe:
  httpGet:
    path: /readyz     # checks Postgres only — NATS outage must not mark pod unready
    port: 8080
  periodSeconds: 10
```

---

## Reliability

**Postgres unavailability** (control-plane): retry with exponential backoff. Login endpoints return 503. Already-issued NATS JWTs remain valid.

**NATS unavailability** (session-agent): buffer state changes in memory, flush on reconnect. Claude processes continue running. Delivery is at-least-once — clients deduplicate by JetStream sequence number.

**Pod restart — graceful**: graceful shutdown runs. On startup, read NATS KV, relaunch with `--resume`.

**Pod restart — ungraceful** (SIGKILL, node failure, OOM): stale KV entries cleaned up on startup (state reset to `restarting`, pendingControls cleared). JSONL on PVC is fine.

**Agent liveness**: detected via NATS `$SYS` presence events (connect/disconnect), not heartbeats.

**Claude process crash**: session-agent detects exit, publishes lifecycle event, auto-restarts with `--resume`.

**Git clone failure**: entrypoint exits non-zero. Deployment restart policy retries. Control-plane reflects `PROJECT_STATUS_FAILED` in KV.

**JSONL cleanup**: daily job deletes JSONL files older than 90 days, purges session files for sessions not in NATS KV.

---

## Security

- **Injection defense**: typed literals are hardcoded constants; user-sourced slugs constrained by charset. No `.`, `*`, or `>` in slugs.
- **Privilege boundaries**: NATS credentials grant by subject prefix (`mclaude.users.{uslug}.>`). Literal-string-equal boundary.
- **Enumeration resistance**: slugs are human-readable by design. Authorization checked per-subject, not by obscurity. UUIDs remain Postgres PKs.
- **Reserved-word blocklist**: append-only. Removals could shadow existing subjects.
- **Admin bypass**: `/admin/*` routes require admin role JWT claim. Actions logged by uslug + target uslug.

---

## `/upgrade-claude` Skill

Manages Claude Code version upgrades. Never update the pin manually.

```
/upgrade-claude [--version <target>]
```

1. Fetch changelog between current and target version
2. Analyze for breaking changes (stream-json events, CLI flags, control protocol, hooks)
3. If breaking changes: surface impact report, propose patches, require approval
4. If safe: update pinned version in Dockerfile, commit on `upgrade/claude-{version}` branch, open PR
5. Existing pods continue old version until redeployed

---

## Observability

**Metrics** (Prometheus/OTEL): per-user active sessions, events/sec, project count; NATS message rate, stream lag; control-plane auth/provisioning latency; session-agent process count, restart count, event throughput.

**Logging**: structured JSON to stdout. Labels: `userId`, `projectId`, `sessionId`.

**FinOps**: compute cost per user (CPU/memory × time), storage per user (PVC GiB), idle PVC alerts.

**Cost estimate per user** (2 active projects):

| Resource | Monthly cost |
|----------|-------------|
| Project pod ×2 (200m CPU, 512Mi → 4 CPU, 8Gi) | ~$12 |
| Project PVC ×2 (20Gi premium) | ~$6 |
| Nix store PVC (20Gi) | ~$1.20 |
| NATS share | ~$1 |
| Postgres share | ~$0.50 |
| **Total** | **~$21/month** |
