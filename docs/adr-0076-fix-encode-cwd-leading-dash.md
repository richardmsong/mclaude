# ADR: Fix EncodeCWD Leading Dash and Remove Auto-Created Local Host Row

**Status**: draft
**Status history**:
- 2026-05-03: draft

## Overview

Two bugs prevent `mclaude import` from working correctly. First, `EncodeCWD` in `mclaude-cli/cmd/import.go` strips the leading dash from the encoded path, so session discovery never finds the correct directory. Second, `CreateUser` in `mclaude-control-plane/db.go` automatically inserts a `hosts` row with `slug='local'` for every new user (KNOWN-16) — a workaround that was never designed, causes the SPA to show an unattested host as "online," and contradicts the single code path principle. Both bugs are removed in this ADR.

## Motivation

**Bug 1 — EncodeCWD:** Running `mclaude import` printed "no Claude Code session data found at `~/.claude/projects/Users-rsong-work-mclaude`" but the actual directory is `~/.claude/projects/-Users-rsong-work-mclaude`. `EncodeCWD` in `cmd/import.go:148` calls `strings.TrimPrefix(encoded, "-")`, stripping the leading dash that Claude Code preserves. The spec (`docs/mclaude-cli/spec-cli.md`) previously documented stripping it too — both spec and code were wrong.

**Bug 2 — Auto-created `local` host row:** `CreateUser` (`db.go:207-218`) inserts a bare `hosts` row with `slug='local'`, `type='machine'`, `role='owner'`, and no `public_key` or `user_jwt` for every new user. A migration backfill at `db.go:905` does the same for existing users. `seedDev` (`main.go`) then finds this row and writes it to the `mclaude-hosts` NATS KV as "online," making the SPA display a host that has never been attested and cannot connect to NATS. The server should be agnostic about whether a user has any hosts registered — hosts are created only when a user explicitly runs `mclaude host register`.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Correct EncodeCWD encoding | Preserve leading dash: `/foo/bar` → `-foo-bar` | Matches Claude Code's actual directory structure, verified on disk |
| Spec update | Update `docs/mclaude-cli/spec-cli.md` to document the correct encoding (leading `-` preserved) | Spec previously documented stripping the leading dash, matching the wrong code behavior |
| Remove auto-created `local` host row | Delete the bare-row INSERT from `CreateUser` and remove the migration backfill; drop existing bare `local` rows (no `public_key`) via a new migration | Server should be agnostic — hosts exist only when explicitly registered via `mclaude host register` |
| Remove `seedDev` local-host NATS KV write | Delete the block in `seedDev` that queries for the `local` host and writes it to `mclaude-hosts` KV | No bare row → nothing to write; SPA only shows hosts that have been attested |

## Component Changes

### mclaude-cli

`cmd/import.go` — `EncodeCWD` function (line 138-149):

The docstring at lines 138-145 documents "Strip the leading `-`" — this comment must be updated to document the correct behavior (leading `-` preserved). The function body change is one line removed:

**Before:**
```go
// EncodeCWD derives the encoded CWD from an absolute path using Claude Code's
// path encoding algorithm:
//   - Take the absolute path (e.g. "/Users/rsong/work/mclaude").
//   - Replace every "/" with "-".
//   - Strip the leading "-".
//   - Result: "Users-rsong-work-mclaude".
//
// This matches the directory names under ~/.claude/projects/.
func EncodeCWD(absPath string) string {
    encoded := strings.ReplaceAll(absPath, "/", "-")
    return strings.TrimPrefix(encoded, "-")
}
```

**After:**
```go
// EncodeCWD derives the encoded CWD from an absolute path using Claude Code's
// path encoding algorithm:
//   - Take the absolute path (e.g. "/Users/rsong/work/mclaude").
//   - Replace every "/" with "-". The leading "-" is preserved.
//   - Result: "-Users-rsong-work-mclaude".
//
// This matches the directory names under ~/.claude/projects/.
func EncodeCWD(absPath string) string {
    return strings.ReplaceAll(absPath, "/", "-")
}
```

The existing unit tests for `EncodeCWD` must be updated. `TestEncodeCWDMatchesClaude` (`cmd/import_test.go:179-186`) has three lines to fix:
- Comment on line 181: `"Claude Code uses "Users-rsong-work-mclaude""` → `"-Users-rsong-work-mclaude"`
- Assertion on line 183: `"Users-rsong-work-mclaude"` → `"-Users-rsong-work-mclaude"`
- Error message on line 184: `"want Users-rsong-work-mclaude"` → `"want -Users-rsong-work-mclaude"`

`TestEncodeCWDBasic` has four cases to update (actual inputs from `cmd/import_test.go:166-170`):

| Input | Old `want` | New `want` |
|-------|-----------|-----------|
| `/Users/rsong/work/mclaude` | `Users-rsong-work-mclaude` | `-Users-rsong-work-mclaude` |
| `/home/alice/projects/my-app` | `home-alice-projects-my-app` | `-home-alice-projects-my-app` |
| `/` | `` | `-` |
| `/tmp` | `tmp` | `-tmp` |

`TestDiscoverSessions*` tests pass literal strings directly to `DiscoverSessions`, bypassing `EncodeCWD` — no changes needed.

`TestRunImport*` tests use `setupImportDir` which calls `cmd.EncodeCWD` at line 145 to create the project directory. After the fix both directory creation and discovery use `EncodeCWD` consistently — no changes needed.

### mclaude-control-plane

**`db.go` — remove auto-creation of `local` host in `CreateUser`:**

Delete the `INSERT INTO hosts` block at lines 207-218 that creates a bare `local` row for every new user.

**`db.go` — remove migration backfill:**

Delete or nullify the migration at line 905 that back-fills a `local` host row for existing users who don't have one.

**`db.go` — new migration to drop existing bare `local` rows:**

```sql
DELETE FROM hosts
WHERE slug = 'local'
  AND (public_key IS NULL OR public_key = '')
  AND (user_jwt   IS NULL OR user_jwt   = '');
```

This removes rows that were created by the auto-creation path (no credentials) and have never been attested. Rows where an operator has run `mclaude host register` (non-null `public_key` and `user_jwt`) are preserved.

**`main.go` — remove local-host NATS KV write from `seedDev`:**

Delete the block (approximately lines 255-295) that queries for `slug='local'`, marshals a `HostKVState`, and writes it to the `mclaude-hosts` JetStream KV bucket. With no bare row, there is nothing to write. Attested hosts are written to KV by the normal host-registration flow.

## Data Model

No schema changes. The cleanup is done via a `DELETE` migration — no columns added or removed.

## Error Handling

- `EncodeCWD` fix: no change to error paths. "No Claude Code session data found" is still correct when the directory doesn't exist; the fix only corrects the derived path so it matches the real directory.
- After removing the auto-created `local` row: `mclaude import` already returns a clear error if no host is registered ("no active host: run 'mclaude host register' and 'mclaude host use <hslug>' first"). No new error handling needed.

## Security

No change. Removing the bare unattested rows reduces the surface of hosts appearing "online" in the SPA without credentials.

## Impact

Specs updated in this commit:
- `docs/mclaude-cli/spec-cli.md` — corrected CWD encoding description: leading `-` is preserved, not stripped

ADRs not updated: ADR-0053 (`docs/adr-0053-session-import-skill.md`) documents the original (incorrect) encoding at lines 52 and 116. ADRs are immutable historical records — the incorrect encoding decision is superseded by this ADR, not edited out of ADR-0053.

## Scope

**v1 (this ADR):**
- Fix `EncodeCWD` to preserve the leading dash; update spec and existing tests.
- Remove auto-created bare `local` host rows from `CreateUser`, the migration backfill, and `seedDev`.
- Drop existing bare `local` rows via a new `DELETE` migration.

**Deferred:** None.

## Integration Test Cases

| Test case | What it verifies | Setup/teardown | Components exercised |
|-----------|------------------|----------------|----------------------|
| `TestIntegration_Import_HappyPath` (existing) | Full import path including session discovery via `EncodeCWD` | Seeds session dir at encoded CWD path; cleans up after | mclaude-cli |
| `TestIntegration_CreateUser_NoLocalHost` (new) | After `CreateUser`, the `hosts` table has no row with `slug='local'` for that user | Creates a user via DB helper; queries `hosts` table | mclaude-control-plane |

## Implementation Plan

| Component | New/changed lines (est.) | Notes |
|-----------|--------------------------|-------|
| mclaude-cli `cmd/import.go` | 1 line removed | Remove `strings.TrimPrefix` call |
| mclaude-cli `cmd/import_test.go` | ~8 | Update `TestEncodeCWDBasic` (4 cases) and `TestEncodeCWDMatchesClaude` (3 lines) |
| mclaude-control-plane `db.go` | ~20 | Remove INSERT block from `CreateUser`, remove backfill migration, add DELETE migration |
| mclaude-control-plane `main.go` | ~40 | Remove local-host NATS KV block from `seedDev` |

**Total estimated tokens:** ~40k
**Estimated wall-clock:** ~15 min
