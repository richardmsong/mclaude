## Audit: 2026-04-21T00:00:00Z

**Document:** docs/adr-0027-docs-dashboard.md

## Run: 2026-04-21T00:01:00Z

**Gaps found: 10**

1. **`listDocs` return type missing `status`, `commit_count`, `last_status_change` — landing page sort key unimplementable** — The landing page sorts ADR buckets by `last_status_change` descending. The `/api/adrs` handler is specified to call `listDocs`. But `listDocs` returns `ListDoc[]` = `{ doc_path, title, category, sections[] }` — no `status`, no `commit_count`, no `last_status_change`. A developer cannot build the landing page sort, the status-bucket grouping, or the graph node encoding from this return type. The ADR's logic-duplication rule forbids reimplementing the query. The required extension to `listDocs` (or a supplementary query) is unspecified.
   - **Doc**: "List ADRs, optional status filter | `listDocs({category: "adr", status: s})`" and "Logic-duplication rule: must not reimplement … imports `listDocs`"
   - **Code**: `ListDoc` interface = `{ doc_path, title, category, sections[] }` — no status, commit_count, or last_status_change (tools.ts:36–42)

2. **`getLineage` hard-filters draft/superseded ADRs — conflicts with graph showing draft nodes** — `getLineage` in tools.ts (lines 165–170) excludes ADR rows with status `draft`, `superseded`, or `withdrawn` from results. The graph explicitly shows draft ADRs (dashed border) and their edges. The popover on a spec section would omit co-committed draft ADRs entirely. The ADR says the dashboard must call `getLineage` without reimplementing it. Whether the dashboard should bypass this filter for graph/popover use is unresolved.
   - **Doc**: "Ranked list of related sections … Each row clickable" and Node encoding shows dashed-border draft nodes with edges. "must not reimplement … imports `getLineage`"
   - **Code**: `getLineage` WHERE: `AND (d.category != 'adr' OR d.status IS NULL OR d.status IN ('accepted', 'implemented'))` — draft ADRs excluded (tools.ts:165–170)

3. **`startWatcher` provides no callback — SSE `changed` array is unimplementable as specified** — The SSE event shape requires `{type: "reindex", changed: string[]}` listing which doc paths were reindexed. `startWatcher` returns only a stop function `() => void`; it has no callback or event emitter to signal which files were touched. The dashboard cannot observe what the watcher reindexed without modifying `startWatcher` or reimplementing the watcher. Neither option is specified. The required API change to `startWatcher` is absent.
   - **Doc**: "On every watcher-triggered reindex, the server emits `{type: "reindex", changed: [<doc_path>, ...]}`. … must not reimplement … imports `startWatcher`"
   - **Code**: `startWatcher(db, docsDir, repoRoot): () => void` — no callback, no event channel (watcher.ts:10)

4. **Schema-version bump does not cover `SCHEMA_SQL` update or `indexFile` upsert column list** — The ADR says bump `SCHEMA_VERSION` `"2"` → `"3"` to trigger auto-rebuild. But the rebuild creates the DB from `SCHEMA_SQL` (db.ts:74), which is a constant that must also be updated with the two new columns. More critically, the `indexFile` SQL upsert in content-indexer.ts (lines 82–89) has a hardcoded column list `(path, category, title, status, mtime)` and will not write `last_status_change` unless that file is also changed. `ParsedDoc` also has no `lastStatusChange` field. The ADR describes the parser extension in isolation but does not explicitly call out that `content-indexer.ts`'s upsert SQL must be updated — a gap that a developer reading only the ADR would miss.
   - **Doc**: "Bump `SCHEMA_VERSION` from `'2'` to `'3'`; the existing corrupt/mismatch handler in `db.ts` triggers a full rebuild. No migration code required."
   - **Code**: `SCHEMA_SQL` constant must be edited in db.ts; `indexFile` upsert: `INSERT INTO documents(path, category, title, status, mtime) VALUES (?, ?, ?, ?, ?) ON CONFLICT ... DO UPDATE SET category=…, title=…, status=…, mtime=…` — `last_status_change` absent (content-indexer.ts:82–89); `ParsedDoc` has no `lastStatusChange` field (parser.ts:10–14)

5. **`commit_count` on `documents`: solo-commit semantics unspecified vs. existing early-return** — The ADR says the lineage scanner "also increments `commit_count` on every doc-row touched by each commit." But `processCommitForLineage` returns early at line 227–229 when `modifiedFiles.length < 2` — solo-file commits are dropped before any per-doc logic. If `commit_count` is meant to be "total commits ever touching this doc," solo commits must be counted before the early return, requiring a structural change to the scanner. If only multi-file commits count, the semantics differ from the description and node radius encoding will misrepresent lightly co-committed but frequently solo-edited docs. The ADR does not resolve this.
   - **Doc**: "The lineage scanner already iterates every commit touching `docs/*.md` — it now also increments `commit_count` on every doc-row touched by each commit."
   - **Code**: `processCommitForLineage` line 227: `if (modifiedFiles.length < 2) { return; }` — solo commits never reach per-doc logic (lineage-scanner.ts:227–229)

6. **Root `package.json` (Bun workspace) does not exist and `mclaude-docs-mcp/package.json` has no `exports` field** — The workspace model requires a new root `package.json` with `workspaces: ["mclaude-docs-mcp", "mclaude-docs-dashboard"]`. No root `package.json` exists today (the repo root has `go.work`, not `package.json`). Additionally `mclaude-docs-mcp/package.json` has no `exports` map. The ADR acknowledges the missing exports but only says "add an `exports` field so the dashboard can import cleanly" — the exact subpath export map (which modules, `.js` extension convention, whether to expose individual files or a single barrel) is unspecified.
   - **Doc**: "Convert the repo root to a Bun workspace … `workspaces: ['mclaude-docs-mcp', 'mclaude-docs-dashboard']` in a new root `package.json` … If `mclaude-docs-mcp/package.json` does not yet declare these exports, add an `exports` field"
   - **Code**: No root `package.json` exists; `mclaude-docs-mcp/package.json` has no `exports` field (mclaude-docs-mcp/package.json:1–14)

7. **`/api/doc` requires `raw_markdown` but no function or column provides full file content** — `DocResponse.raw_markdown` is specified as the full raw markdown string for client-side rendering. The route handler is specified to call `listDocs` + `getSection` per section. Neither function returns raw file bytes — `listDocs` returns metadata+section headings; `getSection` returns parsed section content. The `documents` table has no raw content column. Obtaining `raw_markdown` requires either a `readFileSync` in the route handler or a new DB column, neither of which is specified. The logic-duplication rule does not address this case.
   - **Doc**: "`/api/doc`: … `listDocs` + `getSection` per section" and `DocResponse { … raw_markdown: string // for client-side rendering via marked … }`
   - **Code**: No function in tools.ts returns file-level raw content; `documents` table schema has no content column (db.ts SCHEMA_SQL; tools.ts)

8. **SSE broker design is absent — no specification of client-set management or Bun.serve streaming body API** — The implementation plan explicitly flags "SSE broker needs care (broadcast to all connected clients)" but the ADR provides no design. Missing: how the `Bun.serve` handler holds connections open (the streaming body API details), what data structure holds active client writers, how disconnects are detected and clients removed, and how the watcher signals the broker (compounded by gap 3). A developer has two entirely unspecified components to wire together.
   - **Doc**: "GET `/events` | SSE; emits `{type: "reindex", changed: [...]}` on watcher fires | Bun.serve streaming body" and "SSE broker needs care (broadcast to all connected clients)"
   - **Code**: No existing SSE infrastructure in the codebase

9. **`/api/graph` SQL is entirely unspecified** — `graph-queries.ts` is described as issuing "direct SQL" to aggregate doc-level edges from the `lineage` table, but no SQL is given. The ADR acknowledges this is a deliberate exception to the logic-duplication rule. The global query (all nodes + aggregated edges) and the 1-hop local query (neighbors of a focus doc) must both be written from scratch. The aggregation semantics for `edges.count` (sum of all `lineage.commit_count` for `(from_doc, to_doc)` pairs across all heading combinations) is described in prose but not as SQL, and a naive approach would be an N+1 query. The developer must invent both queries.
   - **Doc**: "`/api/graph` … `graph-queries.ts` (direct SQL)" and "groups the section-level `lineage` rows into doc-level edges by summing `commit_count` for all `(from_doc, to_doc)` pairs. Local mode … filters to rows where `from_doc = <p>` or `to_doc = <p>`"
   - **Code**: `graph-queries.ts` does not exist; no SQL provided in the ADR

10. **`last_status_change` parser: real ADRs use `**Status history**:` (bold markdown) not `Status history:`** — The ADR specifies parsing "the `Status history:` list." Every real ADR in the corpus (confirmed in adr-0015-docs-mcp.md line 5) uses `**Status history**:` with bold asterisks. The current `parser.ts` correctly handles `**Status**:` via a regex, but has zero history-scanning code. If the new parser scans for the plain string `Status history:` it will match nothing in real ADRs, returning `null` for all documents. The exact detection pattern for the history block is unspecified.
    - **Doc**: "scan the `Status history:` list (bullet lines of the form `- YYYY-MM-DD: <status> …`)"
    - **Code**: Real ADRs have `**Status history**:` at line 5 of adr-0015-docs-mcp.md; `parser.ts` has no history-list scanning code (parser.ts:1–102)

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | `listDocs` return type missing status/commit_count/last_status_change | Added "Extended `ListDoc` shape" decision row: `listDocs` SQL extended to SELECT the new columns; shape gains `status`, `commit_count`, `last_status_change`. | factual |
| 2 | `getLineage` hard-filters draft/superseded/withdrawn | Added "Lineage filter semantics (AMENDS ADR-0018)" decision row: remove the status filter globally; every row returns the linked doc's `status`; MCP tool description updated to instruct agents to frame superseded/withdrawn as "tried but not current." | decision |
| 3 | `startWatcher` has no callback | Added "Watcher callback (API CHANGE)" decision row: extend signature with optional `onReindex?: (changed: string[]) => void`; watcher collects paths where `indexFile` returns `true` during a debounced sweep and invokes once per sweep. | factual |
| 4 | Schema-version bump scope (SCHEMA_SQL, content-indexer upsert, parser field) | Rewrote Data Model § Schema change: new `documents` table shape shown verbatim; explicit 3-file table naming `db.ts`, `parser.ts`, `content-indexer.ts` and the required edit in each. | factual |
| 5 | `commit_count` solo-commit semantics | Rewrote Volatility data decision: tally `commit_count` for every modified file BEFORE the `modifiedFiles.length < 2` early-return; early-return moves below the tally loop. | factual |
| 6 | No root `package.json`; no `exports` in docs-mcp | Added Component Changes § Workspace setup: full root `package.json` contents; full `exports` map for `mclaude-docs-mcp/package.json`; explicit subpath-per-module convention with `.ts` extension (Bun-native). | factual |
| 7 | `/api/doc` raw_markdown has no source function | Added `readRawDoc(repoRoot, docPath): string` helper in docs-mcp `tools.ts`; path-escape guarded; included in Logic-duplication rule import list. | factual |
| 8 | SSE broker design absent | Added Data Model § SSE broker design: `Set<Writer>` client registry; full Bun.serve `ReadableStream` start/cancel pattern; broadcast loop with defensive catch-and-remove for dirty disconnects; end-to-end flow description. | factual |
| 9 | `/api/graph` SQL unspecified | Added Data Model § Graph SQL: two complete SQL queries (global aggregate + local 1-hop) using `MIN`/`MAX` scalar canonicalization over the undirected edge; 2-query pattern for local mode (edges then nodes IN list). | factual |
| 10 | Parser must match `**Status history**:` (bold) | Rewrote Data Model § Parser addition: explicit `STATUS_HISTORY_MARKER_RE = /^\*\*Status history\*\*:\s*$/i` plus `HISTORY_LINE_RE`; note that plain-text variant is unsupported. | factual |

## Run: 2026-04-21T10:00:00Z

**Gaps found: 2**

1. **SSE broker `cancel` handler cannot access `writer` — disconnect cleanup is broken as specified** — The ADR's SSE broker code snippet defines `writer` inside the `start(controller)` function, then references it inside `cancel()`, which is a sibling function in the `ReadableStream` constructor options object. These are separate lexical scopes: `cancel` has no access to `writer`. A developer implementing this verbatim gets a `ReferenceError` at runtime whenever a client disconnects cleanly. The disconnect detection path (`clients.delete(writer)` in `cancel`) silently never fires, leaking client entries indefinitely.
   - **Doc**: "cancel() { // Fires when the client disconnects … clients.delete(writer); }" — `writer` is defined in `start(controller)`, not accessible in `cancel` (SSE broker design section)
   - **Code**: `writer` is a local variable inside `start(controller)`. `cancel` is a sibling callback in the same options object, not a nested function — it does not close over `start`'s locals. JavaScript scoping rule: functions only close over the scope in which they are *defined*, not sibling function scopes.

2. **Watcher callback: `indexAllDocs` returns `number`, not `string[]` — full-scan path cannot populate `changed` list** — The "Watcher callback (API CHANGE)" decision specifies the watcher collects paths where `indexFile` returned `true` during a debounced sweep and invokes `onReindex(changed: string[])`. When the watcher triggers a full reindex (no specific filename, or filename without `.md` extension), the current code calls `indexAllDocs(db, docsDir, repoRoot)`, which returns a `number` (count of reindexed files), not a `string[]` of paths. The ADR specifies the new watcher signature and callback semantics but does not specify: (a) changing `indexAllDocs` to return `string[]` instead of `number`, or (b) replacing the `indexAllDocs` call in `runReindex` with an inline loop over all doc files calling `indexFile` individually and collecting truthy results. A developer cannot implement the `onReindex` callback for the full-scan code path without resolving this.
   - **Doc**: "the watcher collects those paths during a debounced sweep and invokes `onReindex` once per sweep with the deduped list" (Watcher callback decision)
   - **Code**: `indexAllDocs` signature: `indexAllDocs(db, docsDir, repoRoot): number` — returns a count (content-indexer.ts:129). Watcher full-scan path: `indexAllDocs(db, docsDir, repoRoot)` — return value unused (watcher.ts:37). No specification in the ADR of changing `indexAllDocs` or the call site to produce a path list.

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | SSE broker `writer` scope bug (`cancel` can't see it) | Rewrote SSE broker `Connect path` snippet: `let writer = null` hoisted to outer scope (enclosing the `ReadableStream` constructor) so both `start` and `cancel` capture the same binding. `cancel` guards with `if (writer)`. Added a load-bearing comment explaining sibling-callback scoping so a reader does not re-introduce the bug. | factual |
| 2 | `indexAllDocs` returns `number`, watcher callback can't get paths | Updated Watcher callback decision: `indexAllDocs` signature changes `(db, docsDir, repoRoot): number → string[]` (returns reindexed doc_paths). Watcher single-file path uses `indexFile` truthy return; full-rescan path uses the new `indexAllDocs` return value. Both concatenated and deduped per sweep. Schema change table expanded to 4 files (db.ts, parser.ts, content-indexer.ts, watcher.ts) with explicit edits including the `indexAllDocs` return-type change. `indexAllDocs`'s existing callers that read the count read `.length` — backwards compatible. | factual |

## Run: 2026-04-21T11:00:00Z

CLEAN — no blocking gaps found.

Round 3 re-evaluation against docs/adr-0027-docs-dashboard.md (Round 2 fixes applied).

**What was verified:**

- SSE broker `writer` hoisted to outer `let writer = null` scope (ADR lines 319–320): both `start` and `cancel` now close over the same binding. Fix confirmed in ADR text.
- `indexAllDocs` return type change from `number` to `string[]` specified in the four-file schema-change table (ADR content-indexer.ts row); watcher callback decision prose describes how both single-file and full-rescan paths feed the `changed` list. Fix confirmed in ADR text.
- `getLineage` filter removal: "Lineage filter semantics (AMENDS ADR-0018)" decision row specifies removal of the WHERE clause filter and addition of `status` to `LineageResult`. The codebase still has the filter (tools.ts:165–170) but this is the specified change, not a gap.
- `listDocs` extended shape: decision row specifies `status`, `commit_count`, `last_status_change` added to `ListDoc`; SQL extension implied by column names and known table. Implementable.
- `readRawDoc` helper: path-escape guard, docs/ containment check, `readFileSync`, and `NotFoundError` on missing file — behavior is fully specified; class name is conventional enough not to block.
- `processCommitForLineage` solo-commit tally: ADR specifies tally loop runs before the `modifiedFiles.length < 2` early-return (Volatility data decision). Clear.
- `mtime REAL` vs `INTEGER` discrepancy between ADR schema and codebase (`db.ts:13`): SQLite does not enforce type affinity; existing float comparison logic is unchanged. Does not block implementation.
- Graph SQL: two queries (global aggregate + 1-hop local) with `MIN`/`MAX` canonicalization specified in full. Implementable.
- Parser `STATUS_HISTORY_MARKER_RE` regex specified with bold asterisks matching real corpus format. Implementable.
- Workspace setup (`package.json` + `exports` map) fully specified with literal JSON in the ADR.
- All cross-spec checks against spec-state-schema.md: dashboard introduces no NATS/KV/K8s/Postgres state — no conflicts.
- ADR-0018 lineage filter conflict: explicitly superseded by "Lineage filter semantics (AMENDS ADR-0018)" decision row.

### Result

**CLEAN** after 3 rounds, 12 total gaps resolved (11 factual fixes, 1 design decision).
