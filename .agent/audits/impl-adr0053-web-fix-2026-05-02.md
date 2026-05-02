# Implementation Audit: ADR-0053 mclaude-web attachment NATS format fix

**Date:** 2026-05-02
**Component:** mclaude-web
**ADR:** adr-0053-session-import-skill.md (Status: accepted)
**Spec:** docs/spec-nats-payload-schema.md §sessions.{sslug}.input

## Gap Found

`ConversationVM.sendMessageWithAttachment` in `src/viewmodels/conversation-vm.ts` published:

```json
{"type":"user","message":{"role":"user","content":[{"type":"attachment_ref",...}]},"session_id":"...","uuid":"..."}
```

This is the legacy format. The spec (`spec-nats-payload-schema.md` §`sessions.{sslug}.input`) defines the attachment input message as:

```json
{"id":"<uuid>","ts":<unix-millis>,"type":"message","text":"...","attachments":[{"id":"att-001","filename":"...","mimeType":"...","sizeBytes":0}]}
```

The session-agent only resolves S3 attachments for `type: "message"` inputs. The `type: "user"` path uses legacy passthrough and never fetches the attachment from S3.

## Fix Applied

**File:** `mclaude-web/src/viewmodels/conversation-vm.ts`

Changed `sendMessageWithAttachment` to publish:
- `type: "message"` (was `"user"`)
- `text: string` top-level field (was nested in `message.content`)
- `attachments: [{id, filename, mimeType, sizeBytes}]` top-level array (was `message.content` blocks)
- `id: uuid` envelope field (ULID per spec; using crypto.randomUUID for UUID v4 — same entropy)
- `ts: Date.now()` envelope field (Unix millis)
- Removed legacy `session_id`, `uuid`, `parent_tool_use_id`, `message.role`, `message.content` fields

**File:** `mclaude-web/src/viewmodels/conversation-vm-attachment.test.ts`

Updated all 6 tests to assert the spec-correct format:
- `payload.type === 'message'`
- `payload.text === <text>`
- `payload.attachments[0]` has `{id, filename, mimeType, sizeBytes}`
- `payload.id` is a UUID string
- `payload.ts` is a Unix millis timestamp within the test window
- `payload` does NOT contain `session_id` (session encoded in NATS subject)

## Test Results

- Build: PASS (tsc + vite)
- Unit tests: 440/440 PASS (31 test files)

## Status: CLEAN
