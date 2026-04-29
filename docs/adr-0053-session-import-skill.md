# ADR: Session Import Skill

**Status**: draft
**Status history**:
- 2026-04-29: draft

## Overview
A CLI command (`mclaude import`) that imports existing Claude Code session data into mclaude. The import captures the current working directory context, session JSONL transcripts, and per-project memories, packages them into a tar archive, and uploads them into the mclaude platform which creates a new project from the imported data.

## Motivation
Users working with Claude Code locally accumulate valuable session history (JSONL transcripts with full conversation context, tool usage, and reasoning) and project memories. When they want to bring this work into mclaude — to continue sessions via the mclaude platform, share context with teammates, or benefit from mclaude's session management — there is currently no mechanism to do so. This skill bridges that gap.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Import surface | `mclaude` CLI subcommand | Portable, scriptable, doesn't require Factory session. A skill wrapper can be added later. |
| Upload target | NATS control-plane + Object Store | Full platform integration, works across hosts, uses existing provisioning pipeline |
| Session selection | All sessions for the CWD | Import is a bulk operation for all historical sessions. No per-session selection in v1. |
| Post-import behavior | Imported as read-only history; resume is a future enhancement | v1 focuses on getting data into mclaude. Resume requires session-agent `--resume` plumbing — deferred. |
| Archive format | tar.gz | Standard, widely supported, handles directory trees naturally |
| Project creation | Import always creates a NEW project | No merging into existing projects — merging risks conflicts. If name collides, user must rename. |
| Auth | User-level JWT via `mclaude login` (device-code flow) | Import acts on behalf of the user, not the host. Same NATS JWT mechanism as SPA login. |
| Storage placement | Controller-dependent (harness-aware) | BYOH/local: `~/.claude/projects/` native location. K8s: PVC mounted by session-agent. Controller decides. |
| K8s scope | In scope for v1 | Both BYOH/local and K8s deployment targets supported. |
| Upload security | Per-user Object Store bucket with scoped NATS permissions | Depends on ADR-0054 (NATS JetStream permission tightening). User JWT can only access their own bucket. |
| Session IDs | Preserve original Claude Code session IDs | Enables future `--resume`. Re-import of same session ID is rejected as a conflict error. |
| Name collision | Prompt user for new name | No auto-suffixing. User has full control over project naming. |
| UI distinction | None — imported sessions treated same as native | No "imported" badge. Simplifies UI. Metadata stored internally but not surfaced. |

## User Flow

1. User runs `mclaude login` (if not already authenticated) — device-code flow → user JWT + NATS credential stored locally
2. User runs `mclaude import [--host <hslug>]` from their project directory
3. CLI derives encoded CWD, discovers session data at `~/.claude/projects/{encoded-cwd}/`:
   - JSONL transcripts: `{sessionId}.jsonl`
   - Subagent data: `{sessionId}/subagents/`
   - Memories: `memory/` directory
   - Project CLAUDE.md (from CWD `.claude/CLAUDE.md` or `CLAUDE.md`)
4. CLI connects to NATS using stored JWT + NKey seed
5. CLI derives project name from directory name (last path component). Checks slug availability via NATS request/reply to control-plane (`mclaude.users.{uslug}.api.projects.check-slug`).
6. If slug taken → CLI prompts user for a new name → re-checks. Loop until available.
7. CLI publishes "initiate import" to control-plane. Control-plane creates per-user Object Store bucket (`mclaude-imports-{uslug}`) if needed, returns upload instructions (bucket name, object key).
8. CLI packages data into tar.gz with metadata.json
9. CLI uploads archive to per-user Object Store bucket
10. CLI publishes "complete import" to control-plane with confirmed slug + object reference
11. Control-plane creates new project (Postgres + KV) with `ImportObjectRef` in state, dispatches provisioning to controller
12. Controller creates pod/process; session-agent starts
13. Session-agent reads project state, sees import object ref, downloads archive from Object Store
14. Session-agent unpacks archive to PVC (K8s) or `~/.claude/projects/{encoded-cwd}/` (local)
15. Session-agent clears `ImportObjectRef` from project KV state, deletes object from Object Store
16. Session-agent's fsnotify watcher detects new JSONL files, creates SessionState KV entries
17. Sessions appear in web UI under the new project (read-only history; resume is a future enhancement)

**CLI bootstrapping:** The CLI needs the control-plane API URL to initiate login. This is configured via `~/.mclaude/config.json` (set by `mclaude config set api-url <url>`) with a default of `https://api.mclaude.internal`.

## Component Changes

### mclaude-cli
- New `mclaude login` subcommand:
  - Initiates device-code flow: `POST /auth/device-code` to control-plane → returns `{ deviceCode, userCode, verificationUrl, expiresIn, interval }`
  - Displays URL + code to user, polls `POST /auth/device-code/poll` until completion
  - Receives `{ jwt, nkeySeed, userId, userSlug, natsUrl }` (same as SPA login response)
  - Stores credentials at `~/.mclaude/auth.json`
  - JWT refresh: before import operations, check TTL → `POST /auth/refresh` if needed
- New `mclaude import` subcommand:
  - Loads auth from `~/.mclaude/auth.json` (error if not logged in)
  - Reads active host from context (`~/.mclaude/context.json`); `--host <hslug>` overrides
  - Derives encoded CWD from current directory (same encoding as Claude Code: `/` → `-`)
  - Discovers session data at `~/.claude/projects/{encoded-cwd}/`
  - Derives project name from last path component of CWD; checks slug uniqueness via control-plane
  - If slug collision: prompts user for new name
  - Packages data into `import-{slug}.tar.gz` with `metadata.json`
  - Connects to NATS using stored JWT + NKey seed
  - Uploads archive to per-user Object Store bucket `mclaude-imports-{uslug}`
  - Publishes import request to `mclaude.users.{uslug}.hosts.{hslug}.api.import`
  - Waits for acknowledgement, prints success message

### mclaude-control-plane
- New device-code auth endpoints:
  - `POST /auth/device-code` — generate device code, store pending auth in memory with TTL
  - `POST /auth/device-code/poll` — CLI polls with device code, returns credentials when user completes auth
  - `GET /auth/device-code/verify` — web UI endpoint where user enters the code and authenticates
- New NATS import handler:
  - Subject: `mclaude.users.{uslug}.hosts.{hslug}.api.import`
  - Validates user JWT, reads import metadata from request
  - Creates project in Postgres with `source: "import"` and `import_object_ref` column
  - Writes `ProjectKVState` with `ImportObjectRef` field
  - Dispatches provisioning request to controller (K8s or local based on host's cluster)
  - Returns success/error to CLI
- Per-user Object Store bucket creation: `mclaude-imports-{uslug}` created on first import for that user

### mclaude-session-agent
- New import consumer:
  - On startup, reads project KV state
  - If `ImportObjectRef` is set: downloads archive from Object Store, unpacks to session data directory
  - After successful unpack: clears `ImportObjectRef` from KV state, deletes object from Object Store
  - Reports import completion via lifecycle event
- New fsnotify watcher:
  - Watches session data directory for new `.jsonl` files
  - When new JSONL file detected: extracts session metadata (ID, timestamps, branch, model from first few lines)
  - Creates `SessionState` KV entry with state `"completed"` (imported sessions are historical)
  - Handles both import-triggered discovery and any future file-based session discovery

### mclaude-common
- New types: `ImportRequest` (user slug, host slug, project name, object ref), `ImportMetadata` (CWD, git remote, branch, session IDs, timestamp)
- Extended `ProjectKVState` / `ProjectState` with `ImportObjectRef` field
- Extended `ProjectState` Postgres model with `source` and `import_object_ref` columns

### mclaude-web (minimal)
- New device-code verification page: `/auth/device-code/verify` — user enters code, authenticates, redirected to success screen
- No changes to session list or project views (imported sessions treated identically to native)

## Data Model

**Import archive contents:**
```
import-{encoded-cwd}/
├── metadata.json          # CWD, git remote, branch, timestamp, session IDs
├── sessions/
│   ├── {sessionId}.jsonl  # Session transcripts
│   └── {sessionId}/
│       └── subagents/     # Subagent data if present
├── memory/                # Project memories
│   ├── MEMORY.md
│   └── *.md
└── claude.md              # Project-level CLAUDE.md if present
```

**metadata.json:**
```json
{
  "cwd": "/Users/rsong/work/mclaude",
  "gitRemote": "git@github.com:richardmsong/mclaude.git",
  "gitBranch": "main",
  "importedAt": "2026-04-29T...",
  "sessionIds": ["abc-123", "def-456"],
  "claudeCodeVersion": "1.x.x"
}
```

## Error Handling

| Error | Component | Behavior |
|-------|-----------|----------|
| Not logged in | CLI | `mclaude login` required first. Clear error message. |
| No session data found for CWD | CLI | Error listing expected path `~/.claude/projects/{encoded-cwd}/` |
| No active host in context | CLI | Error: `mclaude host register` and `mclaude host use` required first |
| Session JSONL files corrupted/truncated | CLI | Warn per file but include what's available in archive |
| Project slug collision | CLI + Control-plane | Control-plane returns 409. CLI prompts user for new name. |
| Session ID collision (re-import) | Session-agent | Skip the conflicting session with a warning, import remaining sessions |
| NATS connection failed | CLI | Retry with backoff, clear error with NATS URL and troubleshooting hints |
| Object Store upload failed mid-stream | CLI | Retry from beginning, delete partial object first |
| Archive too large | CLI | Pre-check archive size against configurable limit (default 500MB), warn before upload |
| Unpack fails (disk full, permissions) | Session-agent | Report error via lifecycle event, leave `ImportObjectRef` set for retry |
| Device-code expired | CLI + Control-plane | 15-minute TTL. CLI displays "code expired, try again" |
| Device-code not yet verified | CLI polling | Continue polling until verified or expired |

## Security

- Archive contains full conversation history including potentially sensitive tool outputs — NATS connections use TLS, encrypting transit
- Memories may contain user-specific context — ownership scoped to the importing user via NATS JWT identity
- Per-user Object Store buckets (`mclaude-imports-{uslug}`) with scoped NATS permissions prevent cross-user access (depends on ADR-0054)
- Session-agent validates archive integrity (metadata.json schema, JSONL line format) before unpacking
- CLI validates file sizes before packaging (guard against accidentally archiving node_modules etc.)
- Device-code auth: codes expire after 15 minutes, single-use, rate-limited

## Dependencies

- **ADR-0054 (NATS JetStream Permission Tightening)** — Required before implementation. Per-user Object Store bucket isolation depends on scoped JetStream permissions. Without this, all authenticated users/hosts have broad JetStream access.

## Impact

Specs updated in this commit:
- `docs/mclaude-cli/spec-cli.md` — new `login` and `import` commands
- `docs/mclaude-control-plane/spec-control-plane.md` — device-code auth endpoints, import handler, Object Store bucket management
- `docs/mclaude-session-agent/spec-session-agent.md` — import consumer, fsnotify watcher, session discovery
- `docs/mclaude-common/spec-common.md` — new shared types
- `docs/mclaude-web/spec-web.md` — device-code verification page (if spec exists)
- `docs/spec-state-schema.md` — new Object Store bucket, extended project KV state schema

## Scope

**v1:**
- `mclaude login` — device-code auth flow for CLI
- `mclaude import` — discover, package, upload session data from local `~/.claude/`
- Control-plane import handler — create project, dispatch provisioning
- Session-agent import consumer — download, unpack, advertise sessions
- Session-agent fsnotify watcher — live session discovery from JSONL files
- Web device-code verification page
- Both BYOH/local and K8s deployment targets

**Deferred:**
- Session resume (`--resume` from imported JSONL) — future enhancement
- Incremental sync (only new sessions since last import)
- Bidirectional sync
- Import from remote machines
- Import specific session subsets (date range, etc.)
- Import progress streaming (real-time progress bar in CLI)
- Archive compression options (different algorithms, size limits)

## Open questions

(none remaining)

## Integration Test Cases

| Test case | What it verifies | Setup/teardown | Components exercised |
|-----------|------------------|----------------|----------------------|
| Device-code login flow | CLI obtains valid NATS credentials via device-code | Create test user in Postgres. Teardown: delete user. | CLI, control-plane, web (verification page) |
| Import happy path (BYOH) | Full import: CLI packages → uploads → control-plane creates project → session-agent unpacks → sessions visible in KV | Create test user + host. Seed `~/.claude/projects/` with test JSONL + memories. Teardown: delete project, clean up Object Store. | CLI, control-plane, session-agent, NATS Object Store |
| Import happy path (K8s) | Same as above but targeting K8s deployment | Create test user + K8s cluster host. Teardown: delete namespace, PVC. | CLI, control-plane, K8s controller, session-agent |
| Slug collision prompts rename | CLI detects existing project with same slug, prompts for new name, import succeeds with new slug | Create test user + existing project with target slug. Teardown: delete both projects. | CLI, control-plane |
| Session ID collision on re-import | Re-importing same sessions skips duplicates | Import once, then import again. Teardown: delete project. | CLI, control-plane, session-agent |
| fsnotify discovers new JSONL | Session-agent creates KV entry when JSONL file appears in watched directory | Start session-agent for a project, then drop a JSONL file into the directory. Teardown: delete KV entry, remove file. | Session-agent |
| JWT refresh during long import | CLI refreshes expired JWT mid-upload | Create test user with short-TTL JWT. Teardown: delete user. | CLI, control-plane |
| Archive validation | Session-agent rejects malformed archive (bad metadata.json, non-JSONL content) | Upload crafted bad archive. Teardown: clean up Object Store. | Session-agent |

## Implementation Plan

Estimated effort to implement this design via dev-harness.

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| mclaude-cli (login) | ~300 | ~100k | Device-code flow, credential storage, JWT refresh. New command + auth package. |
| mclaude-cli (import) | ~400 | ~100k | Session discovery, tar packaging, Object Store upload, NATS publish. |
| mclaude-control-plane (device-code auth) | ~250 | ~80k | New HTTP endpoints + in-memory pending auth store + web verification handler |
| mclaude-control-plane (import handler) | ~200 | ~65k | NATS subscriber, project creation with import ref, Object Store bucket mgmt |
| mclaude-session-agent (import consumer) | ~200 | ~65k | Object Store download, archive unpack, import ref cleanup |
| mclaude-session-agent (fsnotify watcher) | ~250 | ~80k | File watching, JSONL metadata extraction, KV entry creation |
| mclaude-common (types) | ~80 | ~30k | ImportRequest, ImportMetadata, extended ProjectState |
| mclaude-web (device-code page) | ~150 | ~60k | Verification page UI + auth handler integration |

**Total estimated tokens:** ~580k
**Estimated wall-clock:** Depends on ADR-0054 completion first
