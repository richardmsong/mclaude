## Run: 2026-05-01T12:00:00Z

Design document: `docs/adr-0053-session-import-skill.md`
Round: 3 (verifying round 2 fixes + checking for new issues)

### Round 2 Fix Verification

All 4 gaps from round 2 are confirmed fixed:

| # | Gap | Status | Verification |
|---|-----|--------|--------------|
| 1 | Stale `ImportObjectRef` in ADR-0054 | ✅ Fixed | ADR-0054 line 393 now reads `clear importRef` (was `clear ImportObjectRef`) |
| 2 | spec-state-schema missing fields | ✅ Fixed | Projects table has `source` (line 43) and `import_ref` (line 44). ProjectKVState has `importRef` (line 213). `attachments` Postgres table added (line 119+). |
| 3 | spec-nats-activity missing subjects | ✅ Fixed | Sections 11-pre through 11d added with full import flow. Subject tree includes `import.request`, `import.confirm`, `import.download`, `import.complete`, `check-slug`. |
| 4 | spec-nats-payload-schema missing definitions | ✅ Fixed | `AttachmentRef` type defined (lines 26-31). Import subjects fully specified: `import.request`, `import.confirm`, `import.download`, `import.complete`. Attachment NATS subjects specified: `attachments.download`, `attachments.upload`, `attachments.confirm`. HTTP endpoints for attachments specified. |

### New Gaps Found

**Gaps found: 2**

1. **`import.complete` behavior inconsistency in spec-nats-payload-schema.md** — The payload schema says CP "updates the project state in KV (`importStatus: 'complete'`)" upon receiving `import.complete`. This contradicts both ADR-0053 and spec-nats-activity.md in two ways: (a) ADR-0053 step 15 and spec-nats-activity.md section 11d say the **agent** clears `importRef` from project KV state, not CP; (b) the field name `importStatus` does not exist anywhere — not in ADR-0053, not in spec-state-schema.md (which only defines `importRef` in ProjectKVState). A developer implementing the CP `import.complete` handler would not know whether CP should write `importStatus: "complete"` to KV (as the payload schema says) or whether the agent handles KV cleanup (as the ADR and activity spec say), and would find no `importStatus` field defined in any schema.
   - **Doc**: spec-nats-payload-schema.md `import.complete` section: "CP receives the signal, deletes the S3 object, and updates the project state in KV (`importStatus: \"complete\"`)"
   - **Code**: ADR-0053 step 15: "Session-agent clears `importRef` from project KV state"; spec-nats-activity.md section 11d: "Agent: Clears importRef from project KV state / CP: Receives signal, resolves import ID from project KV, deletes S3 object"

2. **`check-slug` subject missing from spec-nats-payload-schema.md** — ADR-0053 step 5 defines a NATS request/reply subject `mclaude.users.{uslug}.hosts.{hslug}.projects.check-slug` for slug availability checking. spec-nats-activity.md section 11-pre provides example payloads (`{"slug":"myapp"}` → `{"available":true}` or `{"available":false,"suggestion":"myapp-2"}`). However, spec-nats-payload-schema.md — which claims to define "the exact JSON payload for every NATS subject in the system" — has no section for this subject. A developer implementing the CP handler would find the payload only in the activity spec's informal example, not in the authoritative payload schema with field descriptions and required/optional annotations.
   - **Doc**: ADR-0053 step 5: "Checks slug availability via NATS request/reply to control-plane (`mclaude.users.{uslug}.hosts.{hslug}.projects.check-slug`)"; spec-nats-activity.md section 11-pre shows request/response examples
   - **Code**: spec-nats-payload-schema.md has no `check-slug` section (searched entire file)
