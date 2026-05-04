# Spec Alignment Audit: ADR-0076

**ADR**: `docs/adr-0076-fix-encode-cwd-leading-dash.md`
**Status**: draft
**Run**: 2026-05-03T00:00:00Z

## Target Specs

- `docs/mclaude-cli/spec-cli.md` (identified in Impact section, line 122-123)

## Findings

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| 22, 51-62 | EncodeCWD encoding decision: "Preserve leading dash: `/foo/bar` → `-foo-bar`" and post-fix docstring: "Replace every `/` with `-`. The leading `-` is preserved." | spec-cli.md line 68 | REFLECTED | — | Spec says "The leading `-` is preserved — it is the artifact of the leading `/` being replaced" and example shows `-Users-rsong-work-mclaude`. This matches the ADR decision. |

| 23, 84-105 | Remove auto-created `local` host row: "Delete the bare-row INSERT from `CreateUser` and remove the migration backfill"; "Delete the block (approximately lines 255-295) that queries for `slug='local'`, marshals a `HostKVState`, and writes it to the `mclaude-hosts` JetStream KV bucket." | spec-control-plane.md lines 172, 218-219 | GAP | SPEC→FIX | Spec currently states "Dev-seed path: on `DEV_SEED=true`, `seedDev` writes the bootstrap user's `local` host entry with `online=true`" and "Optionally seeds... a default `local` machine host for that user... Writes the `local` host's `mclaude-hosts` KV entry with `online=true` (dev-only path...)". After this ADR, this behavior is removed entirely — the spec should be updated to remove or clarify these references to the auto-created `local` host. The ADR's Impact section does not list control-plane spec as needing update, but the spec will be inaccurate after implementation. |


### Summary

- Reflected: 1
- Gap: 1
- Partial: 0
