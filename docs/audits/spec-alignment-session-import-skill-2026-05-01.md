# Spec Alignment: ADR-0053 (Binary Data — Imports and Attachments)

## Run: 2026-05-01T03:50:00Z (Round 2 — post-fix verification)

**ADR:** `docs/adr-0053-session-import-skill.md` (status: draft)

**Specs checked:**
- `docs/spec-state-schema.md`
- `docs/spec-nats-payload-schema.md`
- `docs/spec-nats-activity.md`
- `docs/mclaude-common/spec-common.md`
- `docs/mclaude-cli/spec-cli.md`
- `docs/mclaude-control-plane/spec-control-plane.md`
- `docs/mclaude-web/spec-web.md`
- `docs/mclaude-session-agent/spec-session-agent.md`

### Phase 1 — ADR → Spec (forward pass)

#### Decisions Table

| # | ADR text | Spec location | Verdict | Direction | Notes |
|---|----------|---------------|---------|-----------|-------|
| D1 | "Import surface: `mclaude` CLI subcommand" | spec-cli.md `mclaude import [--host <hslug>]` | REFLECTED | — | Full flow described |
| D2 | "Binary data storage: S3-compatible object storage" | spec-control-plane.md S3 env vars (`S3_ENDPOINT`, `S3_BUCKET`, etc.) | REFLECTED | — | |
| D3 | "Upload/download mechanism: Pre-signed URLs (client ↔ S3 directly, CP only signs)" | spec-control-plane.md attachment + import endpoints; spec-nats-payload-schema.md import.request/download subjects | REFLECTED | — | |
| D4 | "NATS role for binary data: References only (AttachmentRef with opaque ID)" | spec-nats-payload-schema.md Common Types `AttachmentRef`; spec-common.md `AttachmentRef` type | REFLECTED | — | No S3 key in AttachmentRef ✓ |
| D5 | "Session selection: All sessions for the CWD" | spec-cli.md import flow step 3 | REFLECTED | — | Bulk import, no per-session selection |
| D6 | "Post-import behavior: Imported as read-only history" | spec-web.md "Imported Sessions" section | REFLECTED | — | "treated identically to native sessions" |
| D7 | "Archive format: tar.gz" | spec-cli.md step 7 `import-{slug}.tar.gz` | REFLECTED | — | |
| D8 | "Project creation: Import always creates a NEW project" | spec-cli.md step 6 slug collision prompt | REFLECTED | — | |
| D9 | "Auth: User-level JWT via `mclaude login` (device-code flow)" | spec-cli.md `mclaude login`; spec-control-plane.md device-code endpoints | REFLECTED | — | Full device-code flow described |
| D10 | "Upload security: Pre-signed URLs with scoped S3 keys, 5 min TTL" | spec-control-plane.md "signs upload URL (5-min TTL)" | REFLECTED | — | |
| D11 | "Attachment scoping: Per-project S3 key prefix `{uslug}/{hslug}/{pslug}/attachments/{id}`" | spec-state-schema.md `attachments` table `s3_key`; spec-control-plane.md S3 key generation | REFLECTED | — | |
| D12 | "Session IDs: Preserve original Claude Code session IDs" | spec-cli.md metadata.json includes `sessionIds` | REFLECTED | — | |
| D13 | "Name collision: Prompt user for new name" | spec-cli.md step 6 | REFLECTED | — | |
| D14 | "UI distinction: None — imported sessions treated same as native" | spec-web.md "No 'imported' badge, no visual distinction" | REFLECTED | — | |
| D15 | "K8s scope: In scope for v1" | spec-session-agent.md describes both K8s and BYOH modes | REFLECTED | — | |

#### User Flow — Import

| # | ADR text | Spec location | Verdict | Direction | Notes |
|---|----------|---------------|---------|-----------|-------|
| UF1 | "User runs `mclaude login` — device-code flow → credentials stored at `~/.mclaude/auth.json`" | spec-cli.md `mclaude login` full description | REFLECTED | — | `{jwt, nkeySeed, userSlug}` format ✓ |
| UF2 | "CLI derives encoded CWD…replace `/` with `-`, strip leading `-`" | spec-cli.md CWD encoding algorithm section | REFLECTED | — | Verbatim encoding described |
| UF3 | "CLI connects to NATS using stored JWT + NKey seed" | spec-cli.md step 4 + NATS connection section | REFLECTED | — | |
| UF4 | "Checks slug availability via NATS request/reply (`…projects.check-slug`)" | spec-cli.md step 5; spec-nats-payload-schema.md `check-slug` subject | REFLECTED | — | |
| UF5 | "CLI packages data into tar.gz with metadata.json" | spec-cli.md step 7 | REFLECTED | — | |
| UF6 | "CLI requests pre-signed upload URL from CP via NATS (`import.request`)" | spec-cli.md step 8; spec-nats-payload-schema.md `import.request` subject | REFLECTED | — | Full request/response schema |
| UF7 | "CLI uploads archive directly to S3" | spec-cli.md step 9 | REFLECTED | — | |
| UF8 | "CLI signals CP upload complete via NATS (`import.confirm`)" | spec-cli.md step 10; spec-nats-payload-schema.md `import.confirm` subject | REFLECTED | — | |
| UF9 | "CP creates project with `source: 'import'` and `import_ref`, writes ProjectKVState with `importRef`" | spec-state-schema.md `projects` table `source`/`import_ref` columns; ProjectKVState `importRef` field; spec-control-plane.md `import.confirm` subscriber | REFLECTED | — | |
| UF10 | "Session-agent reads project KV state, sees `importRef`; requests download URL via `import.download`" | spec-session-agent.md | GAP | SPEC→FIX | Session-agent spec mentions `importRef` only in passing (KV_projects write description). No dedicated import handler section describing the on-startup import flow. |
| UF11 | "Session-agent downloads archive from S3, unpacks to session data directory" | spec-session-agent.md | GAP | SPEC→FIX | No description of S3 download or archive unpacking behavior. |
| UF12 | "Session-agent clears `importRef` from project KV state, publishes `import.complete`" | spec-session-agent.md | GAP | SPEC→FIX | The import.complete signal and importRef clearing are not described. Only a parenthetical "e.g., clear importRef" in the KV bucket description. |
| UF13 | "Session-agent's fsnotify watcher detects new JSONL files, creates SessionState KV entries" | spec-session-agent.md | GAP | SPEC→FIX | No fsnotify watcher section exists. The spec describes JSONL cleanup but not session discovery from JSONL files. |
| UF14 | "CLI bootstrapping: reads control-plane server URL from `~/.mclaude/context.json`'s `server` key" | spec-cli.md context file section and `--server` flag | REFLECTED | — | |
| UF15 | "Standard message envelope payload `{id, ts}` for import.complete (no additional fields)" | spec-nats-payload-schema.md `import.complete` section | REFLECTED | — | |

#### User Flow — Attachments

| # | ADR text | Spec location | Verdict | Direction | Notes |
|---|----------|---------------|---------|-----------|-------|
| AF1 | "SPA requests pre-signed upload URL from CP: `POST /api/attachments/upload-url`" | spec-web.md File/Image Uploads section; spec-control-plane.md `POST /api/attachments/upload-url`; spec-nats-payload-schema.md HTTP endpoints | REFLECTED | — | |
| AF2 | "SPA uploads file directly to S3, confirms upload, sends chat message with `AttachmentRef`" | spec-web.md steps 4-6; spec-nats-payload-schema.md `sessions.{sslug}.input` with `attachments` array | REFLECTED | — | |
| AF3 | "Agent receives input with `AttachmentRef`, requests download URL from CP via NATS (`attachments.download`)" | spec-nats-payload-schema.md `attachments.download` subject; spec-nats-activity.md section 9e-input "message with attachments" | REFLECTED | — | But spec-session-agent.md: see AF5 |
| AF4 | "Agent generates binary output, requests upload URL, uploads to S3, confirms, includes AttachmentRef" | spec-nats-payload-schema.md `attachments.upload` + `attachments.confirm` subjects | REFLECTED | — | But spec-session-agent.md: see AF5 |
| AF5 | "Session-agent attachment support: download user attachments for CLI input, upload agent-generated attachments" | spec-session-agent.md | GAP | SPEC→FIX | Session-agent spec has zero mention of attachment handling. A developer implementing from this spec would not know the agent must resolve AttachmentRef via NATS request/reply to CP, download from S3, and pass to the CLI driver. |
| AF6 | "Attachment rendering in chat: resolve `AttachmentRef` → request download URL → render" | spec-web.md "Attachment Rendering" section | REFLECTED | — | |
| AF7 | "Max file size: 50MB enforced by CP" | spec-web.md "Max file size: 50MB"; spec-control-plane.md "Rejects if sizeBytes exceeds configurable limit (default 50 MB)" | REFLECTED | — | |

#### Component Changes — mclaude-control-plane

| # | ADR text | Spec location | Verdict | Direction | Notes |
|---|----------|---------------|---------|-----------|-------|
| CP1 | "New device-code auth endpoints: `POST /api/auth/device-code`, `POST /api/auth/device-code/poll`, `GET /api/auth/device-code/verify`" | spec-control-plane.md HTTP endpoints table | REFLECTED | — | |
| CP2 | "S3 client configured via environment (`S3_ENDPOINT`, `S3_BUCKET`, `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY`)" | spec-control-plane.md Environment Variables table | REFLECTED | — | |
| CP3 | "Import NATS handlers: `import.request`, `import.confirm`, `import.download`, `import.complete`" | spec-control-plane.md Subscribes section | REFLECTED | — | All four listed |
| CP4 | "Attachment HTTP endpoints: `POST /api/attachments/upload-url`, `POST /api/attachments/{id}/confirm`, `GET /api/attachments/{id}`" | spec-control-plane.md HTTP endpoints table | REFLECTED | — | |
| CP5 | "Attachment NATS handlers: `attachments.download`, `attachments.upload`, `attachments.confirm`" | spec-control-plane.md Subscribes section | REFLECTED | — | |
| CP6 | "Attachment metadata table in Postgres" | spec-state-schema.md `attachments` table; spec-control-plane.md Postgres section | REFLECTED | — | Full schema with indexes |
| CP7 | "`check-slug` NATS request/reply: CLI checks slug availability at `mclaude.users.{uslug}.hosts.{hslug}.projects.check-slug`" | spec-control-plane.md Subscribes section | GAP | SPEC→FIX | Control-plane spec does not list `check-slug` in its NATS Subscribes table. The subject is documented in spec-nats-payload-schema.md and spec-cli.md, but a developer implementing CP from its spec alone would miss this subscription. |
| CP8 | "Project deletion: CP deletes S3 prefix `{uslug}/{hslug}/{pslug}/`" | spec-control-plane.md "Project Deletion and S3 Cleanup" section | REFLECTED | — | |

#### Component Changes — mclaude-common

| # | ADR text | Spec location | Verdict | Direction | Notes |
|---|----------|---------------|---------|-----------|-------|
| CC1 | "New types: `ImportRequest`, `ImportMetadata`, `AttachmentRef`, `AttachmentMeta`" | spec-common.md types table | REFLECTED | — | All four types with correct fields |
| CC2 | "Extended `ProjectKVState` with `importRef` field" | spec-common.md `ProjectState` description | REFLECTED | — | "includes `importRef` field — import ID string, nullable" |
| CC3 | "Extended `ProjectState` Postgres model with `source` and `import_ref` columns" | spec-state-schema.md `projects` table | REFLECTED | — | |

#### Component Changes — mclaude-web

| # | ADR text | Spec location | Verdict | Direction | Notes |
|---|----------|---------------|---------|-----------|-------|
| WB1 | "New device-code verification page: `/auth/device-code/verify`" | spec-web.md "Device-Code Verification Page" section | REFLECTED | — | |
| WB2 | "Attachment upload in chat input: file picker / paste handler → S3 upload → confirm → AttachmentRef" | spec-web.md "File/Image Uploads (Attachments)" section | REFLECTED | — | |
| WB3 | "Attachment rendering: resolve AttachmentRef → download URL → render" | spec-web.md "Attachment Rendering" section | REFLECTED | — | |
| WB4 | "No changes to session list for imported sessions" | spec-web.md "Imported Sessions" section | REFLECTED | — | |

#### Component Changes — mclaude-cli

| # | ADR text | Spec location | Verdict | Direction | Notes |
|---|----------|---------------|---------|-----------|-------|
| CL1 | "Updated `mclaude login` with device-code flow" | spec-cli.md `mclaude login` section | REFLECTED | — | Full flow described |
| CL2 | "Credentials at `~/.mclaude/auth.json`: `{jwt, nkeySeed, userSlug}`" | spec-cli.md step 7 + auth.json description | REFLECTED | — | |
| CL3 | "New `mclaude import` subcommand" | spec-cli.md `mclaude import` section | REFLECTED | — | Full flow with all 11 steps |
| CL4 | "CWD encoding: replace `/` with `-`, strip leading `-`" | spec-cli.md CWD encoding algorithm section | REFLECTED | — | Exact algorithm described |
| CL5 | "NATS request/reply for slug check, import.request, import.confirm" | spec-cli.md NATS connection section | REFLECTED | — | All three listed |

#### Component Changes — mclaude-session-agent

| # | ADR text | Spec location | Verdict | Direction | Notes |
|---|----------|---------------|---------|-----------|-------|
| SA1 | "Import handler: on startup reads project KV, if `importRef` set → request download URL → download from S3 → unpack" | spec-session-agent.md | GAP | SPEC→FIX | No import handler section. Only a parenthetical "e.g., clear importRef" in the KV bucket description (line 178). The spec needs a dedicated section describing the import lifecycle on startup. |
| SA2 | "After unpack: clears `importRef` from KV, publishes `import.complete`" | spec-session-agent.md | GAP | SPEC→FIX | Same gap — import completion behavior not described. |
| SA3 | "Attachment support: download user attachments via `attachments.download` NATS request/reply, pass to CLI driver" | spec-session-agent.md | GAP | SPEC→FIX | Zero mention of attachment handling in the spec. |
| SA4 | "Attachment support: upload agent-generated attachments via `attachments.upload` + `attachments.confirm`" | spec-session-agent.md | GAP | SPEC→FIX | Zero mention of agent-generated attachment upload. |
| SA5 | "fsnotify watcher: watches session data directory for new `.jsonl` files, creates `SessionState` KV entries with state `completed`" | spec-session-agent.md | GAP | SPEC→FIX | No fsnotify watcher section. The spec describes JSONL cleanup (line 61-63) but not session discovery from new JSONL files. |

#### Error Handling

| # | ADR text | Spec location | Verdict | Direction | Notes |
|---|----------|---------------|---------|-----------|-------|
| EH1 | "Session ID collision (re-import): skip conflicting session with a warning, import remaining sessions" | spec-session-agent.md | GAP | SPEC→FIX | Session ID collision handling is not described in the spec. A developer would not know to skip duplicates on re-import. |
| EH2 | "Unpack fails (disk full, permissions): report error via lifecycle event, leave `importRef` set for retry" | spec-session-agent.md | GAP | SPEC→FIX | Unpack failure behavior not described. |
| EH3 | "Device-code expired: 15-minute TTL" | spec-control-plane.md device-code endpoints | REFLECTED | — | "15-minute TTL" in description |
| EH4 | "Archive too large: pre-check against configurable limit (default 500MB)" | spec-nats-payload-schema.md `import.request` "CP may reject if exceeds limit (default 500MB)"; spec-control-plane.md "Rejects if sizeBytes exceeds limit (default 500 MB)" | REFLECTED | — | |
| EH5 | "Attachment too large: CP rejects if sizeBytes exceeds limit (default 50MB)" | spec-control-plane.md; spec-web.md "Max file size: 50MB" | REFLECTED | — | |

#### Data Model

| # | ADR text | Spec location | Verdict | Direction | Notes |
|---|----------|---------------|---------|-----------|-------|
| DM1 | "metadata.json: `{cwd, gitRemote, gitBranch, importedAt, sessionIds, claudeCodeVersion}`" | spec-common.md `ImportMetadata` type | REFLECTED | — | All fields match |
| DM2 | "Import archive structure: metadata.json, sessions/, memory/, claude.md" | spec-cli.md import flow step 7 | REFLECTED | — | metadata.json described |

#### NATS Activity Spec

| # | ADR text | Spec location | Verdict | Direction | Notes |
|---|----------|---------------|---------|-----------|-------|
| NA1 | "Import flow sections 11-pre through 11d" | spec-nats-activity.md sections 11-pre, 11a, 11b, 11c, 11d | REFLECTED | — | Full walkthrough present |
| NA2 | "Subject tree includes import.* and check-slug and attachments.* subjects" | spec-nats-activity.md Subject Namespace Map | REFLECTED | — | All subjects in namespace tree |
| NA3 | "No $O.* (Object Store) subjects — binary data uses S3" | spec-nats-activity.md namespace map note | REFLECTED | — | Explicit note present |

### Phase 2 — Summary

- **Reflected:** 43
- **Gap:** 9
- **Partial:** 0

### Gap Details

All gaps are in the **session-agent spec** (`docs/mclaude-session-agent/spec-session-agent.md`) and the **control-plane spec** (`docs/mclaude-control-plane/spec-control-plane.md`):

**Session-agent spec gaps (7 gaps — all SPEC→FIX):**

1. **SA1 + SA2 (Import handler):** The spec needs a dedicated "Import Handler" section describing the startup behavior: check `importRef` in project KV → request pre-signed download URL from CP via `import.download` NATS request/reply → download archive from S3 → unpack to session data directory → clear `importRef` from KV → publish `import.complete` via NATS. Currently only a parenthetical mention.

2. **SA3 + SA4 (Attachment support):** The spec needs an "Attachment Support" section describing: (a) when processing user input with `AttachmentRef`: request download URL from CP via `attachments.download` NATS request/reply, download from S3, pass to CLI driver as image/file input; (b) when agent generates binary output: request upload URL from CP via `attachments.upload`, upload to S3, confirm via `attachments.confirm`, include `AttachmentRef` in session event.

3. **SA5 (fsnotify watcher):** The spec needs a "Session Discovery (fsnotify)" section describing: watch session data directory for new `.jsonl` files → extract session metadata (ID, timestamps, branch, model) → create `SessionState` KV entry with status `completed` (imported sessions are historical).

4. **EH1 (Session ID collision):** Error handling: session ID collision on re-import → skip conflicting session with warning, import remaining.

5. **EH2 (Unpack failure):** Error handling: unpack fails → report error via lifecycle event, leave `importRef` set for retry.

**Control-plane spec gap (1 gap — SPEC→FIX):**

6. **CP7 (check-slug subscription):** The control-plane subscribes to `mclaude.users.{uslug}.hosts.{hslug}.projects.check-slug` for slug availability checks. This subscription is not listed in the spec's NATS Subscribes table. The subject is documented in `spec-nats-payload-schema.md` and `spec-cli.md`, but the CP spec should also list it.
