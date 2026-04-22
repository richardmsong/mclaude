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

---

## Audit: 2026-04-22T04:00:00Z (post-commit re-audit)

**Document:** docs/adr-0032-line-level-lineage.md

Triggered by explicit `/design-audit` invocation after the ADR was promoted draft → accepted and co-committed with `spec-docs-mcp.md` + `spec-dashboard.md` (commit `ab0cc8d`). Fresh evaluator pass against the finalized state.

## Run: 2026-04-22T05:00:00Z

**Gaps found: 3**

1. **`mclaude-docs-mcp/package.json` is missing the `"./line-lineage"` subpath export** — The dashboard's `routes.ts` is specified (ADR Component Changes; `spec-dashboard.md` § HTTP API) to import `getLineLineage` from `"mclaude-docs-mcp/line-lineage"`. `spec-docs-mcp.md` § Package exports lists `"./line-lineage": "./src/line-lineage.ts"` as a required entry. The actual `mclaude-docs-mcp/package.json` `exports` map has six entries (`./parser`, `./db`, `./content-indexer`, `./lineage-scanner`, `./watcher`, `./tools`) and no `./line-lineage`. Under Bun's workspace resolver, the import `"mclaude-docs-mcp/line-lineage"` resolves through the `exports` map; without this entry, the import fails at runtime even after `line-lineage.ts` is created. A developer implementing `routes.ts` would write the import, run the server, and see a module-not-found error.
   - **Doc**: `spec-docs-mcp.md` § Package exports — `"./line-lineage": "./src/line-lineage.ts"` listed as a required export; ADR Impact § Code components — `mclaude-docs-mcp/package.json — add micromark deps` (implies the file is edited); ADR Component Changes § mclaude-docs-dashboard — `handleLineLineage imports getLineLineage directly from mclaude-docs-mcp/line-lineage`
   - **Code**: `mclaude-docs-mcp/package.json` lines 10–18 — exports map has no `"./line-lineage"` entry

2. **`micromark` and `micromark-extension-gfm-table` are absent from `mclaude-docs-mcp/package.json` dependencies** — Both the ADR (Impact § Code components: "add `micromark` + `micromark-extension-gfm-table` deps") and `spec-docs-mcp.md` § Dependencies explicitly require these packages. The actual `package.json` has only two runtime dependencies (`@modelcontextprotocol/sdk`, `zod`). A developer writing `line-lineage.ts` would add the imports and immediately fail `bun install` / runtime resolution. This is a missing prerequisite that blocks writing any line of the new module.
   - **Doc**: ADR Impact § Code components — `mclaude-docs-mcp/package.json — add micromark + micromark-extension-gfm-table deps`; `spec-docs-mcp.md` § Dependencies — `micromark + micromark-extension-gfm-table — markdown parsing for line-lineage block detection and HTML rendering (ADR-0032)`
   - **Code**: `mclaude-docs-mcp/package.json` lines 19–23 — `"dependencies"` contains only `@modelcontextprotocol/sdk` and `zod`

3. **`blocks` response shape is contradicted between the two specs: `Block[]` (array) vs `Record<string, Block>` (keyed map)** — `spec-docs-mcp.md` § Line-lineage module defines `LineLineageResponse.blocks` as `Block[]` (an array). `spec-dashboard.md` § MarkdownView line-lineage mode describes `blocks` as "a keyed map of `block_id → { adr_contributors, non_adr_commits }`" and says the hover handler "looks it up in the `blocks` map." A developer implementing `api.ts`'s `fetchLineLineage` return type and then the hover handler in `MarkdownView.tsx` faces a contradiction: the server produces an array but the client is told to index into it as an object. If the response is truly an array, the client must build the keyed map locally (O(n) on each hover event or once after fetch) — but this is not stated. If the response is a map, the MCP spec's `Block[]` type is wrong. Neither spec resolves this. A developer cannot determine the correct wire shape.
   - **Doc**: `spec-docs-mcp.md` § Line-lineage module — `interface LineLineageResponse { html: string; blocks: Block[]; }` (explicit array); `spec-dashboard.md` § MarkdownView — "`blocks` is a keyed map of `block_id → { adr_contributors, non_adr_commits }`" and "looks it up in the `blocks` map"
   - **Code**: No implementation yet; the contradiction is between the two co-committed specs

**Note on `Toast.tsx` vs `ReindexToast.tsx`:** The ADR's Impact section (line 141) lists `mclaude-docs-dashboard/ui/src/components/Toast.tsx (new or reused)` while `spec-dashboard.md` § Package layout names the file `ReindexToast.tsx`. The correct filename is `ReindexToast.tsx` per the spec (which is authoritative for implementation). A developer reading only the ADR implementation plan would create the wrong file. This is a minor inconsistency; the spec resolves it unambiguously, so a dev-harness that reads both will not be blocked — not reported as a blocking gap, but flagged here for the ADR to be corrected.
## Run: 2026-04-22T04:05:00Z

**Gaps found: 3**

1. **`./line-lineage` subpath export missing from `mclaude-docs-mcp/package.json`** — spec-docs-mcp.md § Package exports requires it; actual package.json does not yet list it. Dashboard's `import "mclaude-docs-mcp/line-lineage"` would fail at runtime.

2. **`micromark` + `micromark-extension-gfm-table` absent from `mclaude-docs-mcp/package.json` deps** — both the ADR Impact and spec-docs-mcp.md § Dependencies require them; package.json still has only `@modelcontextprotocol/sdk` and `zod`.

3. **`blocks` response shape contradicted between the two co-committed specs** — spec-docs-mcp.md defines `LineLineageResponse.blocks` as `Block[]` (array); spec-dashboard.md § MarkdownView treats `blocks` as "a keyed map of `block_id → { ... }`". Consumer code cannot determine wire shape.

**Minor (non-blocking):** ADR Impact references `Toast.tsx (new or reused)` while spec-dashboard.md § Package layout names it `ReindexToast.tsx`.

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | `./line-lineage` subpath export missing from package.json | **No doc change needed** — spec-docs-mcp.md § Package exports already lists `"./line-lineage": "./src/line-lineage.ts"`. This is pre-implementation state; dev-harness updates package.json to match the spec. Documented here for traceability. | non-gap |
| 2 | micromark deps missing from package.json | **No doc change needed** — spec-docs-mcp.md § Dependencies already lists `micromark` + `micromark-extension-gfm-table`. Pre-implementation state; dev-harness adds them. | non-gap |
| 3 | `blocks` shape contradiction (array vs map) | Clarified in `spec-dashboard.md` § MarkdownView line-lineage mode: wire shape is `Block[]` (matching `spec-docs-mcp.md`); client builds `Map<string, Block>` locally on fetch. Property names aligned to `contributors` and `non_adr_commits` (was `adr_contributors`). | factual |
| 4 | Minor: Toast.tsx vs ReindexToast.tsx | ADR Impact line 141 updated to `ReindexToast.tsx` (matching spec-dashboard.md § Package layout). | factual |


## Run: 2026-04-22T06:00:00Z (Round 5)

**Scope**: Post-round-4 re-audit. Round 4 gaps #1 and #2 (package.json) confirmed as pre-implementation state resolved by spec, not doc gaps. Round 4 gap #3 (`blocks` array vs map) and the minor `ReindexToast.tsx` issue confirmed as fixed. Fresh pass for remaining blocking gaps.

**Gaps found: 2**

1. **`head_sha` column described as "short hash" but the specified implementation stores the full 40-char SHA** — `spec-docs-mcp.md` § Tables describes the `head_sha` column as "Git HEAD **short hash** at which the cached response was computed." However, the module spec (§ Line-lineage module, export `getLineLineage`) specifies that HEAD is resolved via `getHeadCommit(repoRoot)`, which is already implemented in `lineage-scanner.ts` and runs `git rev-parse HEAD` — returning the full 40-char SHA, not a short hash. The cache invalidation functions (`pruneLineLineageCache`, `invalidateLineLineageCacheOnHeadAdvance`) also take values from `getHeadCommit` as their `head_sha` arguments. If a developer reads the column description and truncates to a short hash on INSERT, the DELETE/UPDATE comparisons sourced from `getHeadCommit` (full SHA) will never match, silently disabling cache invalidation on every HEAD advance. If the correct format is the full SHA, the column description must say so; if it is truly a short hash, the spec must state how `getHeadCommit`'s output is truncated before storage and must use the same truncated value in prune/invalidation SQL.
   - **Doc**: `spec-docs-mcp.md` line 65 — `"Git HEAD short hash at which the cached response was computed."`; line 139 — `getLineLineage` "Resolves HEAD via `getHeadCommit(repoRoot)`"; line 140 — `pruneLineLineageCache` — `DELETE FROM line_lineage_cache WHERE head_sha != :current_head_sha` with `current_head_sha` from `getHeadCommit`
   - **Code**: `lineage-scanner.ts:31-33` — `getHeadCommit` runs `git rev-parse HEAD` which returns the full 40-char SHA; no truncation step exists or is implied anywhere in the specified implementation

2. **`LineagePopover` props interface for line-lineage mode is not specified** — The ADR (Impact § Code components) says `LineagePopover.tsx` receives "a new rendering mode for line-lineage (contributors + muted non-ADR rows)." `spec-dashboard.md` § MarkdownView line-lineage mode (line 291) says the hover handler "opens `LineagePopover` in line-lineage mode anchored to that element, passing `blockMap.get(id)` as the payload." The payload is a `Block` object (defined in spec-docs-mcp.md). The existing `LineagePopover` component accepts `{ docPath: string, heading?: string | null, navigate: (href: string) => void }` — there is no prop for a `Block` payload, no discriminant for the new mode, and no specified prop name for the block data. A developer implementing `MarkdownView`'s hover handler cannot call `<LineagePopover ... />` without knowing the new prop(s). Possible implementations (e.g., `block?: Block`, `mode?: "section" | "line"`, or a separate component entirely) are all equally valid from the spec alone.
   - **Doc**: ADR line 140 — `LineagePopover.tsx — new rendering mode for line-lineage (contributors + muted non-ADR rows)`; `spec-dashboard.md` line 291 — "opens `LineagePopover` in line-lineage mode anchored to that element, passing `blockMap.get(id)` as the payload"
   - **Code**: `LineagePopover.tsx:9-14` — current interface is `{ docPath: string; heading?: string | null; navigate: (href: string) => void }`. No `Block` prop, no mode discriminant exists or is specified.

## Run: 2026-04-22T04:15:00Z

**Gaps found: 2**

1. **`head_sha` column: "short hash" contradicts full-SHA source** — spec-docs-mcp.md line 65 describes `head_sha` as "Git HEAD short hash," but `getHeadCommit` returns the full 40-char SHA (`git rev-parse HEAD`). Prune/invalidation comparisons would silently mismatch.

2. **`LineagePopover` props for line-lineage mode unspecified** — Existing component accepts `{docPath, heading, navigate}`. No prop name / discriminant for line-lineage mode is given in any of the three docs. Dev-harness cannot write the JSX call site.

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | `head_sha` short-hash vs full-SHA contradiction | spec-docs-mcp.md § line_lineage_cache column doc updated to "Full 40-char Git HEAD SHA … matches output of `getHeadCommit(repoRoot)` … no truncation." | factual |
| 2 | `LineagePopover` props for line mode unspecified | spec-dashboard.md § Detail views now declares `LineagePopoverProps` as a discriminated union: `{mode: "section", docPath, heading, navigate}` \| `{mode: "doc", docPath, navigate}` \| `{mode: "line", docPath, block, navigate}`. Line mode takes the `Block` payload directly (no additional fetch). | factual |


## Run: 2026-04-22T07:00:00Z (Round 6)

**Scope**: Post-round-5 re-audit. Both round-5 fixes verified as landed (`head_sha` column doc now says "Full 40-char Git HEAD SHA … no truncation"; `LineagePopoverProps` is now a discriminated union on `mode` with `"section" | "doc" | "line"`, `line` mode receiving `block: Block` directly). Fresh pass for any remaining blocking gaps.

**Gaps found: 1**

1. **Server-rendered HTML heading format for H2/H1 `≡` popovers is unspecified** — Both specs agree that when `MarkdownView` switches to server-rendered HTML (`dangerouslySetInnerHTML`), the existing H2 and H1 `≡` section-level and doc-level lineage popovers must continue to work. `spec-dashboard.md` line 298 says "H2 and H1 `≡` affordances (ADR-0031) are emitted by the server renderer as inline elements so the existing doc-level and section-level popover wiring keeps working unchanged." The existing wiring (`MarkdownView.tsx:80-99`) works by running `node.querySelectorAll('[data-lineage-heading]')` on the rendered container and calling `createRoot` on each matching span. `spec-docs-mcp.md` step 7 (line 151) states headings render "without `data-block-id`" but specifies nothing about what attribute or element structure the server must emit for headings so that `querySelectorAll('[data-lineage-heading]')` can find them. If the server emits plain `<h2>` elements (without a `data-lineage-heading` span inside), the existing popover injection finds nothing and all H2 `≡` icons silently disappear in line-lineage mode. If the server must emit `<span data-lineage-heading="..."></span>` inside each heading (mirroring the `marked` renderer output), neither spec says so. A developer implementing the server-side micromark renderer in `line-lineage.ts` cannot determine what heading HTML to produce.
   - **Doc**: `spec-dashboard.md` line 298 — "H2 and H1 `≡` affordances (ADR-0031) are emitted by the server renderer as inline elements so the existing doc-level and section-level popover wiring keeps working unchanged"; `spec-docs-mcp.md` line 151 step 7 — "Headings, blockquotes, thematic breaks, and HTML passthrough render without `data-block-id` (they are not interactive blocks)" — no heading element structure specified
   - **Code**: `MarkdownView.tsx:31,80-99` — `const POPOVER_PLACEHOLDER = "data-lineage-heading"` and the `containerRef` callback runs `node.querySelectorAll('[data-lineage-heading]')`, calling `createRoot` on each match. This attribute must be present in the server-produced HTML for the existing wiring to work. No spec states this requirement.

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | Server HTML heading format for H1/H2 popovers unspecified | spec-docs-mcp.md § Line-lineage module step 7 now explicitly declares the heading markup the server must emit: H1 → `<h1><text><span data-lineage-doc></span></h1>` (doc mode), H2 → `<h2><text><span data-lineage-heading="<url-encoded-text>"></span></h2>` (section mode, matches existing `MarkdownView.tsx:62`), H3+ → plain. spec-dashboard.md § MarkdownView line-lineage mode mirrors: `createRoot` injection queries `[data-lineage-heading]` (section mode) and `[data-lineage-doc]` (doc mode). | factual |

## Run: 2026-04-22T08:00:00Z (Round 7)

**Scope**: Post-round-6 re-audit. Round 6 fix verified as landed: spec-docs-mcp.md § Line-lineage module step 7 now explicitly declares `<span data-lineage-doc>` in H1 and `<span data-lineage-heading="<url-encoded>">` in H2; spec-dashboard.md § MarkdownView mirrors the injection pattern for both attributes. Fresh pass for any remaining blocking gaps.

**Gaps found: 2**

1. **`contributors` sort order contradicted across the three documents** — The ADR Decisions table (row "Block ADR aggregation") states contributors are "Ordered by most-recent commit date descending (the block's `last_commit` is the MAX across its lines)." ADR Component Changes step 6 says "sort by `last_commit` descending." spec-docs-mcp.md § Compute pipeline step 6 says "sort by `last_commit` DESC." But spec-dashboard.md § MarkdownView line-lineage mode (line 303) says "Sort: `contributors` rows by descending `commit_count` first." These are mutually exclusive: the server sorts contributors by `last_commit` DESC and stores that order in `response_json` (which the cache returns verbatim), but the client is told to display them ordered by `commit_count` DESC. A developer implementing `line-lineage.ts` step 6 cannot determine which field to sort on; a developer implementing the `LineagePopover` line mode cannot determine whether to display the server-provided order or re-sort by `commit_count`. The correct sort field is not inferable from context — both orderings are defensible design choices and neither spec says the other is wrong.
   - **Doc**: ADR Decisions table line 24 — "Ordered by most-recent commit date descending"; ADR Component Changes line 61 — "sort by `last_commit` descending"; spec-docs-mcp.md line 150 — "sort by `last_commit` DESC"; spec-dashboard.md line 303 — "Sort: `contributors` rows by descending `commit_count` first"
   - **Code**: No implementation yet; the contradiction is across all three co-committed documents

2. **H1 doc-level `LineagePopover` will be duplicated when `MarkdownView` injects `[data-lineage-doc]`** — spec-dashboard.md § MarkdownView line-lineage mode (line 298) says: "the injection loop queries `[data-lineage-heading]` → mounts `LineagePopover` in section mode, and `[data-lineage-doc]` → mounts in doc mode." This means `MarkdownView` will now call `createRoot` on the `<span data-lineage-doc>` placeholder inside the server-rendered H1 and mount a `LineagePopover` in doc mode there. However, the existing `AdrDetail.tsx` (line 64) and `SpecDetail.tsx` (line 56) each already mount a `<LineagePopover docPath={doc.doc_path} heading={null} navigate={navigate} />` directly in their JSX header — outside `MarkdownView`. The ADR states "No other changes to these two routes" (ADR Component Changes line 81; also spec-dashboard.md line 81). If both the JSX-placed popover and the injection-loop-placed popover survive, the reader sees two H1 `≡` icons — one from the route's JSX header and one from the injection into the H1 in the markdown body. A developer cannot determine which of the following is correct: (a) remove the `<LineagePopover heading={null}>` from `AdrDetail`/`SpecDetail` JSX and rely solely on the `[data-lineage-doc]` injection (contradicts "no other changes"); (b) do not inject on `[data-lineage-doc]` from within `MarkdownView` and keep the JSX-placed version (contradicts the spec's injection-loop description); (c) some other mechanism to prevent duplication. None of the three documents resolves this.
   - **Doc**: spec-dashboard.md line 298 — "the injection loop queries … `[data-lineage-doc]` → mounts in `doc` mode"; ADR line 81 and spec-dashboard.md line 81 — "`SpecDetail` and `AdrDetail` both render via the updated `MarkdownView`. No other changes to these two routes."
   - **Code**: `AdrDetail.tsx:64` — `<LineagePopover docPath={doc.doc_path} heading={null} navigate={navigate} />`; `SpecDetail.tsx:56` — same pattern; `MarkdownView.tsx:79-101` — the `containerRef` callback currently queries only `[data-lineage-heading]`, but the spec now requires it to also query `[data-lineage-doc]`, which would mount a second popover when the parent route already has one
## Run: 2026-04-22T04:25:00Z

**Gaps found: 2**

1. **`contributors` sort order contradicted** — ADR + spec-docs-mcp.md said `last_commit` DESC; spec-dashboard.md said `commit_count` DESC.
2. **H1 popover duplication** — Server-emitted `data-lineage-doc` placeholder + existing JSX-placed `<LineagePopover heading={null}>` in AdrDetail/SpecDetail would both render, producing two H1 ≡ icons.

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | contributors sort contradiction | spec-dashboard.md § line mode now says rows are server-sorted by `last_commit` DESC and rendered in received order. Both specs + ADR now agree. | factual |
| 2 | H1 popover duplication | spec-docs-mcp.md § step 7 updated: server renders body H1 as plain `<h1>` (no `data-lineage-doc`). spec-dashboard.md § MarkdownView updated: `createRoot` injection only scans `[data-lineage-heading]` (H2 section mode); H1 `≡` popover stays in `AdrDetail`/`SpecDetail` JSX header next to `doc.title`, its ADR-0031 position. | factual |


## Run: 2026-04-22T09:00:00Z (Round 8)

**Scope**: Post-round-7 re-audit. Both round-7 fixes verified as landed:
- `contributors` sort: ADR Decisions table, ADR Component Changes step 6, spec-docs-mcp.md step 6, and spec-dashboard.md § line mode all say `last_commit` DESC. Consistent.
- H1 popover duplication: spec-docs-mcp.md step 7 renders body H1 as plain `<h1>` with no placeholder; spec-dashboard.md § MarkdownView injection loop scans only `[data-lineage-heading]`; H1 `≡` stays in JSX header of `AdrDetail`/`SpecDetail`. Consistent.

**Gaps found: 1**

1. **`LineagePopoverProps` discriminated union requires `mode` prop, but `AdrDetail.tsx` and `SpecDetail.tsx` are declared as having "no other changes"** — spec-dashboard.md § Detail views declares `LineagePopoverProps` as a discriminated union keyed on `mode`: every call site must supply `mode="section"`, `mode="doc"`, or `mode="line"`. The existing `AdrDetail.tsx:64` calls `<LineagePopover docPath={doc.doc_path} heading={null} navigate={navigate} />` (no `mode`) and `SpecDetail.tsx:56` does the same. TypeScript will not compile either file after `LineagePopover.tsx` adopts the required `mode` discriminant — yet both the ADR (line 81) and spec-dashboard.md (line 81) state "No other changes to these two routes." A developer cannot implement the discriminated union without also updating `AdrDetail.tsx` and `SpecDetail.tsx` to add `mode="doc"`, which contradicts the "no other changes" constraint. One of the two must be revised: either the "no other changes" constraint must be removed (acknowledging the call-site update is required), or the discriminated union must be designed to preserve backward compatibility at the call site (e.g., `mode` defaults to `"doc"` when `heading` is absent and `block` is absent — but this is not stated).
   - **Doc**: spec-dashboard.md § Detail views — `type LineagePopoverProps = | { mode: "section"; ... } | { mode: "doc"; ... } | { mode: "line"; ... }` (all three arms require `mode`); ADR line 81 and spec-dashboard.md line 81 — "`SpecDetail` and `AdrDetail` both render via the updated `MarkdownView`. No other changes to these two routes."
   - **Code**: `AdrDetail.tsx:64` — `<LineagePopover docPath={doc.doc_path} heading={null} navigate={navigate} />`; `SpecDetail.tsx:56` — same pattern; `LineagePopover.tsx:9-14` — current interface is `{ docPath: string; heading?: string | null; navigate }` with no `mode` prop; adding a strict discriminated union breaks both existing call sites

## Run: 2026-04-22T04:35:00Z

**Gaps found: 1**

1. **`LineagePopoverProps` discriminated union + "no other changes to routes" contradiction** — The new union requires `mode` on every call site, but existing AdrDetail/SpecDetail JSX passes no mode. ADR claimed "no other changes to these two routes."

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | discriminated union requires route updates | ADR Component Changes line 81 now states the only change to `AdrDetail.tsx`/`SpecDetail.tsx` is adding `mode="doc"` to the existing H1 `<LineagePopover>` call. spec-dashboard.md § Detail views echoes this explicitly next to the discriminated-union type. | factual |

