## Audit: 2026-04-22T00:00:00Z

**Document:** docs/adr-0032-line-level-lineage.md

## Run: 2026-04-22T00:00:01Z

**Gaps found: 5**

1. **Schema version not bumped** — The ADR adds a new SQLite table `line_lineage_cache` to the existing docs DB, but does not specify what the new `SCHEMA_VERSION` string should be. The current version is `"3"` (`db.ts:4`). A mismatch between the declared version and whatever a developer picks causes `openDb` to delete and rebuild the DB on every process start, destroying the new cache and the existing lineage table. The ADR must state the new version value.
   - **Doc**: "mclaude-docs-mcp/src/db.ts — `CREATE TABLE line_lineage_cache` DDL"
   - **Code**: `db.ts:4` — `const SCHEMA_VERSION = "3"`. No mention of incrementing this in the ADR.

2. **`pruneLineLineageCache` caller mismatch** — The Component Changes section says `boot.ts` calls `pruneLineLineageCache(db, currentHeadSha)` after `runLineageScan` and before `startWatcher`. But `boot.ts` (the real file) does not pass `repoRoot` to a prune function, and the ADR does not specify how `currentHeadSha` is obtained inside `boot.ts`. The existing `boot.ts` calls `runLineageScan(db, repoRoot)` — `repoRoot` is already available. But `pruneLineLineageCache` must call `getHeadCommit(repoRoot)` or accept it as a parameter; neither the signature nor the resolution path is specified in the ADR. A developer must guess the function signature.
   - **Doc**: "Boot: `boot.ts` calls `pruneLineLineageCache(db, currentHeadSha)` after `runLineageScan`"
   - **Code**: `boot.ts:73-78` — current boot sequence has no mechanism to retrieve or thread HEAD sha.

3. **Watcher HEAD-advance invalidation: `prev_head_sha` source undefined** — The Component Changes section says `watcher.ts` should "track `prev_head_sha` across HEAD advances." The existing watcher (`watcher.ts:17`) already tracks `lastHead` locally. The ADR does not say whether `prev_head_sha` is the same variable renamed, a new export, or stored in-memory differently — specifically, it does not specify how the invalidation function in `line-lineage.ts` gets access to the previous HEAD value, which lives inside the watcher's closure. A developer cannot determine the call boundary: does the watcher call a function in `line-lineage.ts`, or vice versa, or does the watcher itself issue the DELETE/UPDATE directly?
   - **Doc**: "`mclaude-docs-mcp/src/watcher.ts` — track `prev_head_sha` across HEAD advances; on advance: `git diff --name-only <prev> <new>`, DELETE changed rows, UPDATE unchanged rows' `head_sha`"
   - **Code**: `watcher.ts:17` — `let lastHead: string | null = getHeadCommit(repoRoot)` exists already, but no interface between watcher and the cache invalidation is specified.

4. **`MarkdownView` block-wrapping mechanism not specified** — The ADR says `MarkdownView` wraps "every rendered block (paragraph, list item, table row, fenced code line) in a hover target." The existing `MarkdownView.tsx` uses `marked` to produce HTML and injects React roots via `createRoot` into placeholder `<span>` elements. The ADR does not specify how the server-computed `block_id` values (which come from the micromark parse in the MCP server) are associated with the rendered DOM elements produced by `marked` on the client. The block detection happens server-side (micromark), but `MarkdownView` renders client-side via `marked`. There is no described mechanism by which the client's rendered paragraphs/rows/list items get their `block_id` attributes so hover events can look up the right entry in the fetched `/api/line-lineage` response. This is the largest implementation gap.
   - **Doc**: "`MarkdownView` wraps every rendered block … hovering opens an inline `LineagePopover` variant … The UI can associate popover state with a block across re-fetches without relying on absolute line numbers" (block_id decision)
   - **Code**: `MarkdownView.tsx:34-110` — current approach uses `marked` renderer to embed placeholder attributes in H2 headings only. No mechanism for injecting `block_id` into paragraphs, list items, table rows, or code lines is described.

5. **SSE event name mismatch** — The ADR says on `doc_changed` SSE event the UI dismisses open line-lineage popovers. But the existing SSE spec (`spec-dashboard.md`) and the broadcast function (`server.ts`) emit `{type: "reindex", changed: string[]}` — there is no `doc_changed` event type anywhere in the codebase. A developer implementing the dismissal logic would need to decide whether to handle `reindex` (the actual event) or introduce a new `doc_changed` event type, and whether the client filter is `changed.includes(docPath)`.
   - **Doc**: "On SSE `doc_changed` for the currently viewed doc, the UI dismisses any open line-lineage popover"
   - **Code**: `spec-dashboard.md` SSE section — only two event shapes: `{type: "hello"}` and `{type: "reindex", changed: string[]}`. No `doc_changed` exists.

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | Schema version not bumped | ADR now states `SCHEMA_VERSION` bumps from `"3"` to `"4"` and calls out that `openDb` will drop/rebuild on first boot | factual |
| 2 | `pruneLineLineageCache` signature + HEAD resolution | ADR now spells out the TS signature `pruneLineLineageCache(db, repoRoot)` using `getHeadCommit(repoRoot)` from `lineage-scanner.ts` | factual |
| 3 | Watcher invalidation call boundary | ADR now names the helper `invalidateLineLineageCacheOnHeadAdvance(db, repoRoot, prev, new)` exported from `line-lineage.ts`; watcher imports and calls it | factual |
| 4 | Client-side block_id-to-DOM association | User chose server-returns-HTML approach; `/api/line-lineage` now returns `{html, blocks}`; `MarkdownView` swaps to `dangerouslySetInnerHTML` and delegates hover via `closest("[data-block-id]")` | decision |
| 5 | SSE event name mismatch | ADR now references the existing `{type: "reindex", changed: string[]}` event, filter by `changed.includes(activeDocPath)`; no new event type introduced | factual |

### Round 2

## Run: 2026-04-22T00:00:02Z

**Gaps found: 2**

1. **Column name inconsistency: `blocks_json` vs `response_json`** — The Decisions table (Cache storage row) and Component Changes step 2 both call the cache column `blocks_json`. The DDL in the Data Model section defines it as `response_json`. A developer writing the INSERT and SELECT queries in `line-lineage.ts` would see two different column names for the same field and cannot determine which is correct.
   - **Doc**: Decisions table row 32: `line_lineage_cache(... blocks_json TEXT ...)` and Component Changes step 2: `deserialize blocks_json`; Data Model DDL line 91: `response_json TEXT NOT NULL`
   - **Code**: No implementation yet; the inconsistency is entirely within the ADR.

2. **`/api/line-lineage` import path not specified** — The ADR says `routes.ts` is "a thin wrapper over the MCP tool." All existing routes call directly-imported functions from `mclaude-docs-mcp` modules (e.g. `routes.ts:2` imports `getLineage` from `mclaude-docs-mcp/tools`). The ADR does not state whether `handleLineLineage` in `routes.ts` imports a function directly from `mclaude-docs-mcp/line-lineage` (the new module) or invokes the registered MCP tool via some other mechanism. "Thin wrapper over the MCP tool" implies subprocess/IPC, but the established pattern is a direct module import. Without a clear statement, a developer cannot determine the import path or call boundary.
   - **Doc**: "New HTTP endpoint `/api/line-lineage?doc=<p>` → thin wrapper over the MCP tool"
   - **Code**: `routes.ts:2` — `import { ..., getLineage, ... } from "mclaude-docs-mcp/tools"` — direct module import pattern; no IPC/subprocess used anywhere.

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | Column name inconsistency | Renamed `blocks_json` → `response_json` throughout the ADR (Decisions table, Component Changes step 2, DDL) | factual |
| 2 | `/api/line-lineage` import path | ADR now states `handleLineLineage` imports `getLineLineage` directly from `mclaude-docs-mcp/line-lineage`, matching the existing `getLineage` direct-import pattern | factual |

### Round 3

## Run: 2026-04-22T03:00:00Z

CLEAN — no blocking gaps found.

Both Round 2 fixes verified as landed:

- `response_json` is consistent in all three locations: Decisions table (line 32), Component Changes step 2 (line 56), and DDL (line 91). No remaining `blocks_json` reference.
- Import path is explicit: `handleLineLineage` imports `getLineLineage` directly from `mclaude-docs-mcp/line-lineage`, matching the established `getLineage` pattern from `mclaude-docs-mcp/tools`.

All Round 1 fixes remain intact: schema version `"3"` → `"4"` called out; `pruneLineLineageCache(db, repoRoot)` signature specified with `getHeadCommit` resolution; `invalidateLineLineageCacheOnHeadAdvance` call boundary defined in watcher; server-returns-HTML approach with `dangerouslySetInnerHTML` and `closest("[data-block-id]")` specified; SSE uses existing `{type: "reindex", changed}` event. No new state introduced that contradicts `spec-state-schema.md`.

### Result

**CLEAN** after 3 rounds, 7 total gaps resolved (6 factual fixes, 1 design decision).
