## Run: 2026-05-01T00:00:00Z

**Gaps found: 6**

1. **NKey seed storage location contradiction** — The ADR states in two places where the CLI's NKey seed is stored, giving conflicting answers. A developer cannot implement this without clarification.
   - **Doc**: Under `mclaude-cli` Component Changes, the login flow says: "the CLI generates its own NKey pair locally and stores the seed at `~/.mclaude/nkey`." Later in the same section: "Stores credentials at `~/.mclaude/auth.json` (same path as before; credential format changes from bearer token to NATS JWT + NKey seed)." One says the NKey seed goes to `~/.mclaude/nkey`, the other says it goes into `~/.mclaude/auth.json` alongside the JWT.
   - **Code**: The current `auth.json` stores a bearer token (per `spec-state-schema.md` "User-level credentials" section). The file at `~/.mclaude/nkey` does not exist in any current spec or code. The developer must know which path to use for the seed and which for the JWT.

2. **Session ID collision behavior contradiction** — The Decisions table and the Error Handling table give opposite instructions for what happens when a re-import contains a session ID that already exists.
   - **Doc (Decisions table)**: "Session IDs: Preserve original Claude Code session IDs — Enables future `--resume`. Re-import of same session ID is rejected as a conflict error."
   - **Doc (Error Handling table)**: "Session ID collision (re-import): Session-agent — Skip the conflicting session with a warning, import remaining sessions."
   - One says reject the whole import on collision; the other says skip the conflicting session and continue. A developer must choose one behavior.

3. **Claude Code CWD encoding algorithm underspecified** — The ADR describes the CWD-to-directory-name encoding used by Claude Code as "same encoding as Claude Code: `/` → `-`" but does not document the full algorithm. If the encoding handles any characters beyond `/` (e.g., spaces, special chars, Unicode), the simplified description will produce incorrect paths and the CLI will fail to locate session data.
   - **Doc**: User Flow step 3: "CLI derives encoded CWD, discovers session data at `~/.claude/projects/{encoded-cwd}/`" and Component Changes: "Derives encoded CWD from current directory (same encoding as Claude Code: `/` → `-`)."
   - **Code**: No Claude Code encoding logic exists in the mclaude codebase. The developer must reverse-engineer the encoding from Claude Code's source. The ADR should document the full algorithm (or cite the specific Claude Code source file) so a developer can implement it without guessing.

4. **import.confirm request payload ambiguous** — The payload for the `import.confirm` NATS request/reply is inconsistently described within the ADR, and differs again from the NATS activity spec.
   - **Doc (CLI Component Changes)**: "Confirms upload via NATS request/reply (`import.confirm` subject): `{hostSlug}`" — payload contains only `hostSlug`.
   - **Doc (NATS activity spec section 11a)**: `{"importId":"imp-001","hostSlug":"laptop-a"}` — payload contains both `importId` and `hostSlug`.
   - Both `hostSlug` and the project slug are already encoded in the NATS subject (`mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.confirm`), making `hostSlug` in the payload redundant. The `importId` (returned by `import.request`) is needed if the CP doesn't track pending imports by project slug alone. A developer would stop and ask what the payload should be.

5. **Postgres `attachments` table schema incomplete** — The ADR mentions "Attachment metadata table in Postgres" in the control-plane Component Changes section but does not provide the column definitions, constraints, or indexes.
   - **Doc**: The `AttachmentMeta` type in the mclaude-common section lists fields: `(id, s3Key, filename, mimeType, sizeBytes, userSlug, hostSlug, projectSlug, createdAt)`, but does not specify: PRIMARY KEY, NOT NULL constraints, foreign key relationships (e.g., does `userSlug` reference `users.slug`? Is there an FK to `projects`?), indexes for lookup patterns (e.g., by project for cleanup on project deletion), or uniqueness constraints.
   - **Code**: No `attachments` table exists in `mclaude-control-plane/db.go`. The existing tables (`users`, `projects`, `hosts`, `oauth_connections`) all have full DDL in `spec-state-schema.md`. The new table should follow the same convention.

6. **Cross-spec inconsistency: `s3Key` in NATS activity spec AttachmentRef** — The NATS activity spec includes `s3Key` in the `AttachmentRef` payload inside session input messages, but the ADR and the NATS payload schema define `AttachmentRef` without `s3Key`. This inconsistency exists in an already-updated spec referenced by the ADR.
   - **Doc (ADR-0053 Attachment Upload step 6)**: `"attachments":[{"id":"att-001","filename":"screenshot.png","mimeType":"image/png","sizeBytes":245000}]` — no `s3Key`.
   - **Doc (NATS payload schema Common Types)**: `AttachmentRef` has fields `{id, filename, mimeType, sizeBytes}` — no `s3Key`.
   - **Doc (NATS activity spec section 9e-input)**: `"attachments":[{"id":"att-001","filename":"screenshot.png","mimeType":"image/png","sizeBytes":245000,"s3Key":"alice/laptop-a/myapp/attachments/att-001.png"}]` — includes `s3Key`.
   - The `s3Key` should NOT be in NATS messages (it's an internal CP detail; the agent resolves it via the `attachments.download` request/reply). The NATS activity spec needs to be corrected. The ADR's Impact section lists `spec-nats-activity.md` for updates but does not call out this specific correction.
