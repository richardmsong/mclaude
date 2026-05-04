# Spec Alignment Audit: ADR-0053 (Session Import / Binary Data)

## Run: 2026-05-01T00:00:00Z (Round 3)

Round 3 — verifying all specs now reflect ADR-0053 decisions after Round 2 fixes.

**ADR:** `docs/adr-0053-session-import-skill.md` (status: draft)

**Specs evaluated:**
- `docs/spec-state-schema.md`
- `docs/spec-nats-payload-schema.md`
- `docs/spec-nats-activity.md`
- `docs/mclaude-common/spec-common.md`
- `docs/mclaude-cli/spec-cli.md`
- `docs/mclaude-control-plane/spec-control-plane.md`
- `docs/mclaude-web/spec-web.md`
- `docs/mclaude-session-agent/spec-session-agent.md`

### Phase 1 — ADR → Spec (forward pass)

| # | ADR (section) | ADR text | Spec location | Verdict | Direction | Notes |
|---|---------------|----------|---------------|---------|-----------|-------|
| 1 | Decisions | "Import surface: `mclaude` CLI subcommand" | spec-cli.md: `mclaude import [--host <hslug>]` command section | REFLECTED | — | Full import command with flow, flags, CWD encoding |
| 2 | Decisions | "Binary data storage: S3-compatible object storage (MinIO/S3)" | spec-control-plane.md: S3 env vars (`S3_ENDPOINT`, `S3_BUCKET`, etc.), S3 integration section | REFLECTED | — | |
| 3 | Decisions | "Upload/download mechanism: Pre-signed URLs (client ↔ S3 directly, CP only signs)" | spec-control-plane.md: attachment endpoints, import handlers; spec-nats-payload-schema.md: import/attachment subjects | REFLECTED | — | All endpoints return pre-signed URLs; CP never proxies binary data |
| 4 | Decisions | "NATS role for binary data: References only (`AttachmentRef` with opaque ID)" | spec-nats-payload-schema.md: "Binary data never travels through NATS." + AttachmentRef type definition | REFLECTED | — | |
| 5 | Decisions | "Session selection: All sessions for the CWD" | spec-cli.md: "Discovers session data at `~/.claude/projects/{encoded-cwd}/`" | REFLECTED | — | Bulk operation; no per-session selection |
| 6 | Decisions | "Post-import behavior: Imported as read-only history; resume deferred" | spec-session-agent.md: fsnotify watcher creates entries with `status: "completed"` (imported sessions are historical) | REFLECTED | — | |
| 7 | Decisions | "Archive format: tar.gz" | spec-cli.md: "Packages data into `import-{slug}.tar.gz`" | REFLECTED | — | |
| 8 | Decisions | "Project creation: Import always creates a NEW project" | spec-cli.md: "Creates a new project"; spec-control-plane.md: import.confirm creates project | REFLECTED | — | |
| 9 | Decisions | "Auth: User-level JWT via `mclaude login` (device-code flow)" | spec-cli.md: `mclaude login` with device-code flow; spec-control-plane.md: device-code endpoints | REFLECTED | — | |
| 10 | Decisions | "Storage placement: Controller-dependent" | spec-session-agent.md: "PVC `/data/projects/` on K8s, `~/.claude/projects/{encoded-cwd}/` on BYOH" | REFLECTED | — | |
| 11 | Decisions | "K8s scope: In scope for v1" | Multiple specs mention both K8s and BYOH deployment modes | REFLECTED | — | |
| 12 | Decisions | "Upload security: Pre-signed URLs with scoped S3 keys, 5-min TTL" | spec-nats-payload-schema.md: "Expires in 5 minutes"; spec-control-plane.md: "5-min TTL" | REFLECTED | — | |
| 13 | Decisions | "Attachment scoping: Per-project S3 key prefix `{uslug}/{hslug}/{pslug}/attachments/{id}`" | spec-state-schema.md: `attachments.s3_key` column with this format; spec-control-plane.md: S3 key prefix described | REFLECTED | — | |
| 14 | Decisions | "Session IDs: Preserve original Claude Code session IDs. Re-import: skip duplicate with warning" | spec-session-agent.md: Import Handler step 2e — skip conflicting sessions | REFLECTED | — | |
| 15 | Decisions | "Name collision: Prompt user for new name" | spec-cli.md: "If slug collision: prompts user for new name, re-checks. Loops until available." | REFLECTED | — | |
| 16 | Decisions | "UI distinction: None — imported sessions treated same as native" | spec-web.md: "Imported sessions...treated identically to native sessions. No 'imported' badge" | REFLECTED | — | |
| 17 | User Flow Import | "CLI derives encoded CWD: absolute path, `/`→`-`, strip leading `-`" | spec-cli.md: CWD encoding algorithm described in detail | REFLECTED | — | |
| 18 | User Flow Import | "CLI checks slug via `mclaude.users.{uslug}.hosts.{hslug}.projects.check-slug`" | spec-cli.md: step 5; spec-control-plane.md: subscribes to check-slug; spec-nats-payload-schema.md: check-slug defined; spec-nats-activity.md: section 11-pre | REFLECTED | — | |
| 19 | User Flow Import | "CLI requests pre-signed upload URL via `import.request`" | spec-cli.md: step 8; spec-nats-payload-schema.md: import.request with `{slug, sizeBytes}` | REFLECTED | — | |
| 20 | User Flow Import | "CLI uploads archive directly to S3 using signed URL" | spec-cli.md: step 9; spec-nats-activity.md: section 11a `CLI → S3: PUT <uploadUrl>` | REFLECTED | — | |
| 21 | User Flow Import | "CLI signals CP upload complete via `import.confirm` with `{importId}`" | spec-cli.md: step 10; spec-nats-payload-schema.md: import.confirm payload | REFLECTED | — | |
| 22 | User Flow Import | "CP creates project with `importRef` in KV state, dispatches provisioning" | spec-control-plane.md: import.confirm handler; spec-state-schema.md: importRef in ProjectState JSON | REFLECTED | — | |
| 23 | User Flow Import | "Session-agent reads project KV, sees importRef, requests download URL via import.download" | spec-session-agent.md: Import Handler steps 1-2a | REFLECTED | — | |
| 24 | User Flow Import | "Session-agent downloads from S3, unpacks to session data directory" | spec-session-agent.md: Import Handler steps 2b-2d | REFLECTED | — | |
| 25 | User Flow Import | "Session-agent clears importRef from KV, publishes import.complete with standard envelope `{id, ts}`" | spec-session-agent.md: step 2f; spec-nats-payload-schema.md: import.complete standard envelope | REFLECTED | — | |
| 26 | User Flow Import | "CP receives import.complete, deletes S3 object" | spec-control-plane.md: import.complete subscription; spec-nats-payload-schema.md: "CP receives signal, deletes S3 object" | REFLECTED | — | |
| 27 | User Flow Import | "fsnotify detects JSONL files, creates SessionState KV entries" | spec-session-agent.md: fsnotify Watcher section | REFLECTED | — | |
| 28 | User Flow Import | "CLI bootstrapping: reads server URL from `~/.mclaude/context.json`'s `server` key, default `https://api.mclaude.internal`" | spec-cli.md: Context file section describes `server` key and default | REFLECTED | — | |
| 29 | User Flow Attachments | "SPA requests upload URL: `POST /api/attachments/upload-url` with `{filename, mimeType, sizeBytes, projectSlug, hostSlug}`" | spec-control-plane.md: endpoint defined; spec-web.md: step 2; spec-nats-payload-schema.md: HTTP endpoint | REFLECTED | — | |
| 30 | User Flow Attachments | "SPA uploads directly to S3, confirms via `POST /api/attachments/{id}/confirm`" | spec-web.md: steps 4-5; spec-control-plane.md: confirm endpoint | REFLECTED | — | |
| 31 | User Flow Attachments | "SPA sends message with `AttachmentRef` in NATS (no binary data)" | spec-nats-payload-schema.md: sessions.input message type has `attachments` field; spec-web.md: step 6 | REFLECTED | — | |
| 32 | User Flow Attachments | "Agent requests download URL from CP via `attachments.download`" | spec-session-agent.md: Attachment Download section; spec-nats-payload-schema.md: attachments.download | REFLECTED | — | |
| 33 | User Flow Attachments | "Agent requests upload URL via `attachments.upload`, uploads to S3, confirms via `attachments.confirm`" | spec-session-agent.md: Attachment Upload section; spec-nats-payload-schema.md: both subjects defined | REFLECTED | — | |
| 34 | User Flow Attachments | "Agent includes `AttachmentRef` in session event content blocks" | spec-session-agent.md: Attachment Upload step 5; spec-nats-payload-schema.md: `attachment_ref` content block type | REFLECTED | — | |
| 35 | Component: CLI | "mclaude login: device-code flow → credentials at `~/.mclaude/auth.json` as `{jwt, nkeySeed, userSlug}`" | spec-cli.md: mclaude login section with full device-code flow and credential format | REFLECTED | — | |
| 36 | Component: CLI | "CLI generates NKey pair locally, sends only public key" | spec-cli.md: login step 1; spec-nats-activity.md: section 2f | REFLECTED | — | |
| 37 | Component: CLI | "mclaude import: full flow with CWD encoding, slug check, tar.gz, S3 upload, confirm" | spec-cli.md: mclaude import section with all 11 steps | REFLECTED | — | |
| 38 | Component: CP | "Device-code auth: POST /api/auth/device-code, device-code/poll, device-code/verify" | spec-control-plane.md: all three endpoints defined | REFLECTED | — | |
| 39 | Component: CP | "S3 integration: env vars S3_ENDPOINT, S3_BUCKET, S3_ACCESS_KEY_ID, S3_SECRET_ACCESS_KEY" | spec-control-plane.md: all four env vars in Environment Variables table | REFLECTED | — | |
| 40 | Component: CP | "Import NATS handlers: import.request, import.confirm, import.download, import.complete" | spec-control-plane.md: all four import subjects in Subscribes table | REFLECTED | — | |
| 41 | Component: CP | "Attachment HTTP endpoints: upload-url, confirm, download" | spec-control-plane.md: all three attachment HTTP endpoints | REFLECTED | — | |
| 42 | Component: CP | "Attachment NATS handlers: attachments.download, attachments.upload, attachments.confirm" | spec-control-plane.md: all three attachment NATS subjects in Subscribes table | REFLECTED | — | |
| 43 | Component: CP | "check-slug NATS handler: `mclaude.users.{uslug}.hosts.{hslug}.projects.check-slug`" | spec-control-plane.md: check-slug in Subscribes table | REFLECTED | — | |
| 44 | Component: CP | "S3 object deletion triggered by agent's import.complete publication" | spec-control-plane.md: import.complete handler; spec-control-plane.md: Project Deletion and S3 Cleanup | REFLECTED | — | |
| 45 | Component: Agent | "Import handler: on startup reads project KV, if importRef set → download from S3, unpack, clear importRef, publish import.complete" | spec-session-agent.md: Import Handler section (steps 1-2g) | REFLECTED | — | |
| 46 | Component: Agent | "Attachment download: request URL from CP via attachments.download, download from S3, pass to CLI driver" | spec-session-agent.md: Attachment Download section (steps 1-5) | REFLECTED | — | |
| 47 | Component: Agent | "Attachment upload: request URL from CP via attachments.upload, upload to S3, confirm via attachments.confirm, include AttachmentRef in event" | spec-session-agent.md: Attachment Upload section (steps 1-5) | REFLECTED | — | |
| 48 | Component: Agent | "fsnotify watcher: watches session data directory for new .jsonl files, creates SessionState KV entries with status 'completed'" | spec-session-agent.md: fsnotify Watcher section (steps 1-3) | REFLECTED | — | |
| 49 | Component: Agent | "Session ID collision on re-import: skip duplicate session with warning, import remaining" | spec-session-agent.md: Import Handler step 2e + Error Handling table | REFLECTED | — | |
| 50 | Component: Agent | "Unpack fails: leave importRef set for retry on next restart" | spec-session-agent.md: Import Handler step 3 + Error Handling table | REFLECTED | — | |
| 51 | Component: Common | "New types: ImportRequest, ImportMetadata, AttachmentRef (id, filename, mimeType, sizeBytes), AttachmentMeta" | spec-common.md: Import and attachment types table with all four types | REFLECTED | — | |
| 52 | Component: Common | "Extended ProjectState with importRef field (import ID, nullable)" | spec-common.md: "ProjectState (includes `importRef` field — import ID string, nullable)" | REFLECTED | — | |
| 53 | Component: Common | "AttachmentRef has no s3Key — agents resolve via attachments.download" | spec-common.md: AttachmentRef shows only id, filename, mimeType, sizeBytes — "No S3 key" | REFLECTED | — | |
| 54 | Component: Web | "Device-code verification page at `/auth/device-code/verify`" | spec-web.md: Device-Code Verification Page section | REFLECTED | — | |
| 55 | Component: Web | "Attachment upload in chat: file picker / paste → request upload URL → upload to S3 → confirm → attach AttachmentRef" | spec-web.md: File/Image Uploads section with full flow | REFLECTED | — | |
| 56 | Component: Web | "Attachment rendering: resolve AttachmentRef → request download URL → render image/file" | spec-web.md: Attachment Rendering section with full flow | REFLECTED | — | |
| 57 | Data Model | "Import archive structure: metadata.json with cwd, gitRemote, gitBranch, importedAt, sessionIds, claudeCodeVersion" | spec-common.md: ImportMetadata type matches all fields | REFLECTED | — | |
| 58 | State Schema | "projects table: `source` column (created/import)" | spec-state-schema.md: projects.source column | REFLECTED | — | |
| 59 | State Schema | "projects table: `import_ref` column (S3 key)" | spec-state-schema.md: projects.import_ref column | REFLECTED | — | |
| 60 | State Schema | "ProjectKVState: importRef field" | spec-state-schema.md: importRef in ProjectState JSON schema | REFLECTED | — | |
| 61 | State Schema | "attachments Postgres table with all columns and indexes" | spec-state-schema.md: full attachments table definition matching ADR | REFLECTED | — | |
| 62 | State Schema | "Agent JWT: No Object Store access — binary data uses S3" | spec-state-schema.md: "No Object Store access — binary data uses S3 (ADR-0053)" | REFLECTED | — | |
| 63 | NATS Activity | "Import flow walkthrough (sections 11-pre through 11d)" | spec-nats-activity.md: sections 11-pre, 11a, 11b, 11c, 11d | REFLECTED | — | |
| 64 | NATS Activity | "Subject tree includes import.*, check-slug, attachments.* subjects" | spec-nats-activity.md: Subject Namespace Map includes all import/attachment/check-slug subjects | REFLECTED | — | |
| 65 | NATS Activity | "No $O.* (Object Store) subjects" | spec-nats-activity.md: explicit note "No $O.* (Object Store) subjects — binary data uses S3 with pre-signed URLs (ADR-0053)" | REFLECTED | — | |
| 66 | Error Handling | "Archive too large: CP rejects if sizeBytes exceeds limit (default 500MB)" | spec-control-plane.md: import.request "Rejects if sizeBytes exceeds limit (default 500 MB)" | REFLECTED | — | |
| 67 | Error Handling | "Attachment too large: CP rejects if sizeBytes exceeds limit (default 50MB)" | spec-control-plane.md: "Rejects if sizeBytes exceeds configurable limit (default 50 MB)"; spec-web.md: "Max file size: 50MB" | REFLECTED | — | |
| 68 | Error Handling | "Device-code expired: 15-minute TTL" | spec-control-plane.md: "15-minute TTL" | REFLECTED | — | |
| 69 | Security | "CP validates ownership before signing any URL" | spec-control-plane.md: each handler mentions validation; spec-session-agent.md: Attachment Download notes CP validates | REFLECTED | — | |
| 70 | S3 Key Structure | "Cleanup: import archives deleted by CP on agent signal; attachments retained until project deleted" | spec-control-plane.md: import.complete deletes S3 object; Project Deletion and S3 Cleanup section | REFLECTED | — | |

### Summary

- Reflected: 70
- Gap: 0
- Partial: 0

**CLEAN — 70 ADR decisions reflected in specs, 0 gaps.**

All 9 gaps from Round 2 have been fixed:
- spec-session-agent.md: Import Handler, Attachment Support (download + upload), fsnotify Watcher all present with full detail
- spec-control-plane.md: check-slug NATS handler present in Subscribes table
- spec-nats-payload-schema.md: check-slug subject with request/response schema present
- spec-nats-activity.md: Section 11-pre (check-slug), Subject Namespace Map updated

Every concrete decision in ADR-0053 — spanning the Decisions table, User Flows (import + attachments), Component Changes (CLI, CP, session-agent, common, web), Data Model, Error Handling, S3 Key Structure, Security, and NATS Activity — is reflected in the corresponding specs with present-tense descriptions.
