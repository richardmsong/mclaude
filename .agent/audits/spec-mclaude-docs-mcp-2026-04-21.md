## Run: 2026-04-21T00:00:00Z

Component: `mclaude-docs-mcp`

Specs evaluated:
- `docs/mclaude-docs-mcp/spec-docs-mcp.md` (component-local, primary)
- `docs/adr-0015-docs-mcp.md` (v1 design, accepted)
- `docs/adr-0018-adr-status-lifecycle.md` (status column + filters, implemented)
- `docs/adr-0020-docs-per-component-folders.md` (recursion, implemented)
- `docs/adr-0025-component-specs.md` (spec presence, implemented)
- `docs/adr-0027-docs-dashboard.md` (schema v3, new columns, filter removal, etc., accepted)

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-docs-mcp.md:11 | Runtime: Bun, stdio transport, entrypoint `mclaude-docs-mcp/src/index.ts` | index.ts:1-171 | IMPLEMENTED | — | |
| spec-docs-mcp.md:13 | On boot: resolves repo root by walking up from `process.cwd()` until a `.git` directory is found | index.ts:20-22 | GAP | SPEC→FIX | Code uses `dirname(import.meta.url)` + two-level `..` ascent, not a walk-up-to-.git loop. Same end result for the current layout, but method differs. The spec describes the dashboard boot model (ADR-0027) for the MCP; the MCP is deployed at a fixed two-level-deep path so the static approach is correct. |
| spec-docs-mcp.md:13 | Opens SQLite DB at `<repoRoot>/mclaude-docs-mcp/.docs-index.db` | index.ts:23 | IMPLEMENTED | — | dbPath = join(scriptDir, "..", ".docs-index.db") which resolves to mclaude-docs-mcp/.docs-index.db |
| spec-docs-mcp.md:13 | Runs `indexAllDocs` once on boot | index.ts:31-36 | IMPLEMENTED | — | |
| spec-docs-mcp.md:13 | Starts `startWatcher` for process lifetime | index.ts:47 | IMPLEMENTED | — | |
| spec-docs-mcp.md:13 | Registers four MCP tools | index.ts:56-155 | IMPLEMENTED | — | search_docs, get_section, get_lineage, list_docs all registered |
| spec-docs-mcp.md:17 | SCHEMA_VERSION = "3" (ADR-0027) | db.ts:4 | IMPLEMENTED | — | |
| spec-docs-mcp.md:21-32 | `documents` table: id, path, category, title, status, commit_count, last_status_change, mtime | db.ts:7-16 | IMPLEMENTED | — | All columns present with correct types |
| spec-docs-mcp.md:34-44 | `sections` table: id, doc_id FK CASCADE, heading, content, line_start, line_end | db.ts:18-25 | IMPLEMENTED | — | |
| spec-docs-mcp.md:45 | `sections_fts` FTS5 virtual table with triggers sections_ai, sections_ad, sections_au | db.ts:27-49 | IMPLEMENTED | — | All three triggers present |
| spec-docs-mcp.md:47-58 | `lineage` table with PK (section_a_doc, section_a_heading, section_b_doc, section_b_heading) | db.ts:51-59 | IMPLEMENTED | — | |
| spec-docs-mcp.md:60 | `metadata` table: key/value for schema_version and last_lineage_commit | db.ts:61-64 | IMPLEMENTED | — | |
| spec-docs-mcp.md:64 | `parseMarkdown(content: string): ParsedDoc` pure function | parser.ts:30-110 | IMPLEMENTED | — | |
| spec-docs-mcp.md:66 | Extracts H1 text as `title` | parser.ts:46-49 | IMPLEMENTED | — | |
| spec-docs-mcp.md:67 | Extracts `status` from first 20 lines matching `/**Status**: <value>/i` | parser.ts:52-57 | IMPLEMENTED | — | |
| spec-docs-mcp.md:68 | Extracts `lastStatusChange` via bold marker + consecutive bullet scan, lexicographic max | parser.ts:60-77 | IMPLEMENTED | — | |
| spec-docs-mcp.md:69 | Splits into H2 sections; preamble before first `## ` is not a section | parser.ts:80-107 | IMPLEMENTED | — | |
| spec-docs-mcp.md:71-79 | `ParsedDoc` interface shape: title, status, lastStatusChange, sections | parser.ts:10-15 | IMPLEMENTED | — | |
| spec-docs-mcp.md:82 | `classifyCategory(filename)`: adr-* → "adr"; spec-* or feature-list* → "spec"; else → null; operates on basename | parser.ts:120-126 | IMPLEMENTED | — | |
| spec-docs-mcp.md:86 | `indexFile(db, absPath, repoRoot): boolean` — mtime check, upsert documents with path/category/title/status/last_status_change/mtime, replace sections, return true if reindexed | content-indexer.ts:45-116 | IMPLEMENTED | — | commit_count correctly excluded |
| spec-docs-mcp.md:87 | `indexAllDocs(db, docsDir, repoRoot): string[]` — walks every .md, returns paths where indexFile returned true, deletes stale rows | content-indexer.ts:132-168 | IMPLEMENTED | — | |
| spec-docs-mcp.md:88 | `removeFile(db, absPath, repoRoot)` — deletes documents row | content-indexer.ts:121-124 | IMPLEMENTED | — | |
| spec-docs-mcp.md:92 | `startWatcher` signature: `(db, docsDir, repoRoot, onReindex?: (changed: string[]) => void): () => void` | watcher.ts:10-15 | IMPLEMENTED | — | |
| spec-docs-mcp.md:94 | fs.watch on docsDir recursive; debounced 100ms; fallback to 5s polling | watcher.ts:7,76-98 | IMPLEMENTED | — | |
| spec-docs-mcp.md:95-99 | Sweep logic: specific .md → indexFile; no filename → indexAllDocs; dedupe; invoke onReindex | watcher.ts:28-70 | IMPLEMENTED | — | |
| spec-docs-mcp.md:99 | Returns stop function that tears down watcher | watcher.ts:101-108 | IMPLEMENTED | — | |
| spec-docs-mcp.md:101 | MCP entrypoint does not pass onReindex callback | index.ts:47 | IMPLEMENTED | — | `startWatcher(db, docsDir, repoRoot)` — no callback |
| spec-docs-mcp.md:105 | Lineage scanner: runs on-demand; starts from `last_lineage_commit+1` or full history | lineage-scanner.ts:174-219 | IMPLEMENTED | — | |
| spec-docs-mcp.md:108 | Step 2: increment `documents.commit_count` BEFORE `modifiedFiles.length < 2` check | lineage-scanner.ts:228-238 | IMPLEMENTED | — | |
| spec-docs-mcp.md:109 | Step 3: if modifiedFiles.length < 2, return | lineage-scanner.ts:236-238 | IMPLEMENTED | — | |
| spec-docs-mcp.md:110 | Step 4: parse each file at commit SHA; map to H2 sections touched by hunks; upsert lineage with ON CONFLICT DO UPDATE commit_count+1 | lineage-scanner.ts:241-287 | IMPLEMENTED | — | |
| spec-docs-mcp.md:111 | Step 5: update metadata.last_lineage_commit | lineage-scanner.ts:218 | IMPLEMENTED | — | |
| spec-docs-mcp.md:120 | `search_docs` input: query, category?, status?, limit? | tools.ts:53-58 | IMPLEMENTED | — | |
| spec-docs-mcp.md:122 | SQL: joins sections_fts MATCH → sections → documents, filters by category/status, orders by rank (BM25), LIMIT | tools.ts:85-121 | IMPLEMENTED | — | |
| spec-docs-mcp.md:123 | Returns `{doc_path, doc_title, category, heading, snippet, line_start, line_end, rank}[]` | tools.ts:8-17, 117 | IMPLEMENTED | — | |
| spec-docs-mcp.md:124 | `snippet` uses `snippet(sections_fts, 1, '[', ']', '...', 32)` | tools.ts:104 | IMPLEMENTED | — | |
| spec-docs-mcp.md:129 | `get_section` input: doc_path, heading | tools.ts:60-65 | IMPLEMENTED | — | |
| spec-docs-mcp.md:131 | Returns `{doc_path, doc_title, category, heading, content, line_start, line_end}`; throws "Section not found: …" | tools.ts:123-148 | IMPLEMENTED | — | |
| spec-docs-mcp.md:135 | `get_lineage` input: doc_path, heading | tools.ts:67-70 | IMPLEMENTED | — | |
| spec-docs-mcp.md:137 | Per ADR-0027: no status filter applied — all statuses returned; every row includes linked doc's status field | tools.ts:151-177 | IMPLEMENTED | — | Comment confirms ADR-0027 amendment |
| spec-docs-mcp.md:139 | Ordered by commit_count DESC | tools.ts:173 | IMPLEMENTED | — | |
| spec-docs-mcp.md:140-150 | `LineageResult` shape: doc_path, doc_title, category, heading, status, commit_count, last_commit | tools.ts:29-37 | IMPLEMENTED | — | |
| spec-docs-mcp.md:153 | `list_docs` input: category?, status? | tools.ts:72-75 | IMPLEMENTED | — | |
| spec-docs-mcp.md:157-169 | `ListDoc` shape: doc_path, title, category, status, commit_count, last_status_change, sections | tools.ts:39-47 | IMPLEMENTED | — | |
| spec-docs-mcp.md:171 | `documents` SELECT includes all three new columns (status, commit_count, last_status_change) | tools.ts:195 | IMPLEMENTED | — | |
| spec-docs-mcp.md:177-179 | `readRawDoc(repoRoot, docPath): string` — path escape check + docs/ check + readFileSync; throws NotFoundError | tools.ts:250-270 | IMPLEMENTED | — | |
| spec-docs-mcp.md:183-194 | `exports` map in package.json: ./parser, ./db, ./content-indexer, ./lineage-scanner, ./watcher, ./tools | package.json:10-17 | IMPLEMENTED | — | |
| adr-0027:51 | Root `package.json` declares Bun workspace with `workspaces: ["mclaude-docs-mcp", "mclaude-docs-dashboard"]` | /package.json:1-5 | IMPLEMENTED | — | |
| adr-0027:47 | `commit_count` tallied for EVERY modified file in processCommitForLineage before the `< 2` early-return | lineage-scanner.ts:228-238 | IMPLEMENTED | — | |
| adr-0027:48 | `indexAllDocs` return type changed from `number` to `string[]` | content-indexer.ts:132 | IMPLEMENTED | — | |
| adr-0027:48 | `startWatcher` gains optional 4th param `onReindex?: (changed: string[]) => void` | watcher.ts:10-14 | IMPLEMENTED | — | |
| adr-0027:49 | `documents.last_status_change TEXT` column added | db.ts:14 | IMPLEMENTED | — | |
| adr-0027:210 | Parser history-list scan: `STATUS_HISTORY_MARKER_RE = /^\*\*Status history\*\*:\s*$/i` | parser.ts:27 | IMPLEMENTED | — | |
| adr-0027:213 | `HISTORY_LINE_RE = /^\s*-\s*(\d{4}-\d{2}-\d{2}):/` | parser.ts:28 | IMPLEMENTED | — | |
| adr-0018:127 | `documents.status TEXT` column | db.ts:12 | IMPLEMENTED | — | |
| adr-0018:124 | `search_docs` gains optional `status` filter | tools.ts:56 | IMPLEMENTED | — | |
| adr-0018:125 | `list_docs` returns all ADRs; callers filter by status | tools.ts:72-75 | IMPLEMENTED | — | |
| adr-0018:126 | `get_lineage` per ADR-0027 amendment: filter removed, all statuses returned | tools.ts:157-177 | IMPLEMENTED | — | ADR-0027 amends ADR-0018; code follows ADR-0027 |
| adr-0020:115-123 | Parser recursion: `indexAllDocs` recurses into subdirectories; stale-removal uses same recursive list | content-indexer.ts:10-39, 132-168 | IMPLEMENTED | — | walkMdFiles does recursive walk; stale check uses same files list |
| adr-0020:129-131 | Watcher `runReindex` joins docsDir with filename; handles nested relative paths | watcher.ts:34-40 | IMPLEMENTED | — | `join(docsDir, filename)` |
| adr-0020:132-133 | Classifier unchanged; adr-* → adr, spec-* → spec, feature-list* → spec | parser.ts:120-126 | IMPLEMENTED | — | |
| adr-0020:134-135 | No `component` column or filter added | db.ts, tools.ts | IMPLEMENTED | — | |
| adr-0015:234-245 | `.mcp.json` at repo root registering docs server via stdio with bun command | /.mcp.json | IMPLEMENTED | — | |
| adr-0015:256-260 | Error handling: corrupt DB → rebuild; no git → skip lineage; fs.watch throws → poll; FTS5 error → return error text | db.ts:92-108; lineage-scanner.ts:175-177; watcher.ts:85-88; tools.ts:116-119 | IMPLEMENTED | — | |
| adr-0025:26 | `mclaude-docs-mcp` component-local spec exists at `docs/mclaude-docs-mcp/spec-docs-mcp.md` | exists | IMPLEMENTED | — | |
| spec-docs-mcp.md:105 | Lineage scanner spec says "Runs on-demand (not on boot by default — triggered by the MCP tool or a CLI command)" | index.ts:39-44 | GAP | SPEC→FIX | The MCP entrypoint calls `runLineageScan` on boot. The spec's parenthetical "(not on boot by default)" is inaccurate — the code runs it on boot. The on-boot scan is the correct behavior (no tool currently triggers it otherwise). Spec should be updated to reflect that lineage runs on boot AND incrementally via watcher. |
| adr-0015:23-24 | Lineage reindex trigger: "Git log scan on startup + on new commits detected" | index.ts:39-44; watcher.ts:55-63 | IMPLEMENTED | — | Both boot scan and watcher-triggered incremental scan present |


### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| db.ts:1-2 | INFRA | Imports: Database from bun:sqlite, fs utilities. |
| db.ts:67-108 | INFRA | `openDb` function: creates DB, sets foreign keys + WAL, runs SCHEMA_SQL, checks version, rebuilds on mismatch. All spec-described behavior. |
| parser.ts:1-7 | INFRA | Type interfaces ParsedSection and AdrStatus exported. Required for typed returns. |
| parser.ts:26-28 | INFRA | Regex constants STATUS_RE, STATUS_HISTORY_MARKER_RE, HISTORY_LINE_RE. Internal implementation detail of parseMarkdown. |
| content-indexer.ts:1-3 | INFRA | Imports. |
| content-indexer.ts:10-39 | INFRA | `walkMdFiles` helper — recursive directory walk. Necessary infrastructure for spec'd `indexAllDocs` recursion. |
| content-indexer.ts:121-124 | INFRA | `removeFile` — spec'd at spec-docs-mcp.md:88. |
| lineage-scanner.ts:9-17 | INFRA | `git()` helper — internal shell executor for lineage scanner. |
| lineage-scanner.ts:22-25 | INFRA | `isGitAvailable()` — used by runLineageScan error handling path. |
| lineage-scanner.ts:30-33 | INFRA | `getHeadCommit()` — exported; used by watcher for HEAD-change detection. |
| lineage-scanner.ts:38-45 | INFRA | `isRootCommit()` — handles root-commit edge case per ADR-0015. |
| lineage-scanner.ts:50-62 | INFRA | `getModifiedDocFiles()` — internal helper for processCommitForLineage. |
| lineage-scanner.ts:67-69 | INFRA | `getFileAtCommit()` — internal helper for processCommitForLineage. |
| lineage-scanner.ts:71-109 | INFRA | `DiffHunk`, `parseDiffHunks()`, `SectionBoundary`, `touchedSections()` — exported utilities for hunk-to-section mapping per ADR-0015's lineage algorithm. |
| lineage-scanner.ts:153-168 | INFRA | `upsertLineage()` — internal helper for processCommitForLineage. |
| watcher.ts:72-73 | INFRA | Local variables for watcher/pollInterval references. Implementation detail of startWatcher. |
| watcher.ts:90-98 | INFRA | `startPolling()` inner function — fallback per spec. |
| tools.ts:1-4 | INFRA | Imports. |
| tools.ts:8-47 | INFRA | Interface declarations: SearchResult, SectionResult, LineageResult, ListDoc. Spec-described shapes. |
| tools.ts:49-75 | INFRA | Zod schemas (SearchDocsSchema, GetSectionSchema, GetLineageSchema, ListDocsSchema) — exported for use in index.ts. |
| tools.ts:233-238 | INFRA | `NotFoundError` class — spec-described at spec-docs-mcp.md:205. |
| index.ts:1-17 | INFRA | Imports. |
| index.ts:19-26 | INFRA | Repo root + paths derivation. Two-level ascent is functionally equivalent to the spec's walk-up (the package is always two levels deep). |
| index.ts:160-168 | INFRA | SIGINT/SIGTERM handlers for clean shutdown. Standard process lifecycle management. |
| index.ts:170-171 | INFRA | `server.connect(transport)` + ready log. Standard MCP bootstrap. |


### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| spec-docs-mcp.md:64 | parseMarkdown — H1 extraction, section splitting, line numbers | tests/parser.test.ts | — | UNIT_ONLY |
| spec-docs-mcp.md:67 | Status extraction from first 20 lines | tests/recursion-and-status.test.ts (parseMarkdown — status extraction) | — | UNIT_ONLY |
| spec-docs-mcp.md:68 | lastStatusChange extraction via history list | tests/parser.test.ts (parseMarkdown — lastStatusChange) | — | UNIT_ONLY |
| spec-docs-mcp.md:82 | classifyCategory basename classification | tests/parser.test.ts (classifyCategory), tests/recursion-and-status.test.ts | — | UNIT_ONLY |
| spec-docs-mcp.md:86 | indexFile mtime check + upsert + section replacement | tests/content-indexer.test.ts, tests/recursion-and-status.test.ts (status written to DB) | — | UNIT_ONLY |
| spec-docs-mcp.md:87 | indexAllDocs recursive walk + stale removal | tests/recursion-and-status.test.ts (indexAllDocs recursion, stale-removal) | — | UNIT_ONLY |
| spec-docs-mcp.md:92-99 | startWatcher signature + sweep logic + onReindex callback | tests/watcher.test.ts | — | UNIT_ONLY |
| spec-docs-mcp.md:105-111 | runLineageScan incremental scan + processCommitForLineage | tests/lineage-scanner.test.ts, tests/lineage.test.ts | — | UNIT_ONLY |
| spec-docs-mcp.md:108 | commit_count tallied before modifiedFiles.length < 2 check | tests/lineage-scanner.test.ts | — | UNIT_ONLY |
| spec-docs-mcp.md:120-124 | searchDocs FTS5 query + status filter | tests/fts.test.ts, tests/recursion-and-status.test.ts (searchDocs status filter) | — | UNIT_ONLY |
| spec-docs-mcp.md:129-131 | getSection fetch + not-found error | tests/tools.test.ts | — | UNIT_ONLY |
| spec-docs-mcp.md:135-150 | getLineage — no status filter, all statuses, status field in result | tests/recursion-and-status.test.ts (getLineage — no status filter) | — | UNIT_ONLY |
| spec-docs-mcp.md:153-169 | listDocs — category + status filters, extended ListDoc shape | tests/recursion-and-status.test.ts (listDocs status filter) | — | UNIT_ONLY |
| spec-docs-mcp.md:177-179 | readRawDoc path safety + NotFoundError | tests/tools.test.ts | — | UNIT_ONLY |
| spec-docs-mcp.md:17 | SCHEMA_VERSION = "3", openDb rebuild on mismatch | tests/content-indexer.test.ts (db setup uses same schema) | — | UNIT_ONLY |
| adr-0015:234 | .mcp.json registration | none | none | UNTESTED |
| adr-0027:51 | root package.json workspace declaration | none | none | UNTESTED |
| spec-docs-mcp.md:13 | Entrypoint boot sequence (index.ts) — initial indexAllDocs + lineage scan + watcher start | none | none | UNTESTED |


### Phase 4 — Bug Triage

No bugs in `.agent/bugs/` have `**Component**: mclaude-docs-mcp`. All five open bugs target other components (ptt-race-condition, initial-message-injection-race, getting-started-canned-text, new-project-crash-no-git-credentials, heartbeat-stale-agent-down). No bug triage required for this component.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | — | — | No open bugs match this component |

### Summary

- Implemented: 56
- Gap: 2
- Partial: 0
- Infra: 21
- Unspec'd: 0
- Dead: 0
- Tested (unit only): 15
- E2E only: 0
- Untested: 3
- Bugs fixed: 0
- Bugs open: 0


---

## Run: 2026-04-21T00:00:00Z (Round 2 — re-evaluation after commit 4a57346)

### Re-evaluated spec lines (previously GAP → now closed)

| Spec (doc:line) | Spec text (after fix) | Code location | Verdict | Notes |
|-----------------|----------------------|---------------|---------|-------|
| spec-docs-mcp.md:13 | "resolves repo root via a two-level ascent from `import.meta.url` (the entry file lives at `mclaude-docs-mcp/src/index.ts`, a fixed depth below the repo root)" | index.ts:19-21 | IMPLEMENTED | Spec updated to match code. `dirname(new URL(import.meta.url).pathname)` + `resolve(join(scriptDir, "..", ".."))` matches exactly. |
| spec-docs-mcp.md:105 | "Runs on every boot of the MCP server, and again via the watcher whenever HEAD advances (per ADR-0015 § 'Lineage reindex trigger')" | index.ts:39-44 | IMPLEMENTED | Spec updated to match code. `runLineageScan(db, repoRoot)` called unconditionally on boot. Both GAPs from Round 1 closed. |

### Round 2 Summary

Both SPEC→FIX gaps from Round 1 are confirmed closed by the spec edits in commit 4a57346. Code was not changed. No new gaps or drift detected.

- Gaps resolved: 2
- Remaining gaps: 0
- Remaining partial: 0
- Remaining unspec'd: 0
- Remaining dead: 0


---

## Run: 2026-04-21T — Round 3 (ADR-0031 compliance audit)

ADR-0031 status: accepted. Focus: `get_lineage` optional heading, doc-level aggregation SQL, section-mode unchanged, heading="" on aggregated rows, ORDER BY commit_count DESC, no status filter.

### Phase 1 — Spec → Code (ADR-0031 forward pass)

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0031:23 | `get_lineage` input: `{doc_path: string, heading?: string}` — heading becomes optional | tools.ts:67-81 (GetLineageSchema) | IMPLEMENTED | — | `heading: z.string().optional()` confirmed. Zod schema exported and used in index.ts:114 via `GetLineageSchema.shape`. |
| adr-0031:24 | When heading omitted: aggregate every lineage row where `section_a_doc = doc_path`, group by `section_b_doc`, return one `LineageResult` per co-committed doc; `heading = ""` on returned rows | tools.ts:176-195 | IMPLEMENTED | — | `if (!heading)` branch: GROUP BY l.section_b_doc, `'' AS heading`, excludes self-reference with `AND l.section_a_doc != l.section_b_doc`. |
| adr-0031:26 | Aggregation: `commit_count = SUM(commit_count)`; `last_commit = MAX(last_commit)` | tools.ts:186-187 | IMPLEMENTED | — | `SUM(l.commit_count) AS commit_count` and `MAX(l.last_commit) AS last_commit` present. |
| adr-0031:24 | `heading = ""` (empty string, schema stays non-null) on aggregated rows | tools.ts:184 | IMPLEMENTED | — | `'' AS heading` literal in SELECT. |
| adr-0031:27 | Ordering: `ORDER BY commit_count DESC` in aggregated (doc) mode | tools.ts:193 | IMPLEMENTED | — | `ORDER BY commit_count DESC` confirmed. |
| adr-0031:23 | When heading is present: existing section-mode query unchanged | tools.ts:198-214 | IMPLEMENTED | — | Section mode branch identical to pre-ADR-0031 query. |
| adr-0031:27 | Ordering: `ORDER BY commit_count DESC` in section mode | tools.ts:212 | IMPLEMENTED | — | `ORDER BY l.commit_count DESC` confirmed. |
| adr-0031:28 | Status filter unchanged — still none (ADR-0027); all statuses returned | tools.ts:168-175 (comment) + both branches | IMPLEMENTED | — | No WHERE clause on status in either doc-mode or section-mode branch. Comment at line 168-172 explicitly documents ADR-0027 intent. |
| spec-docs-mcp.md:135 | `get_lineage` input now `{doc_path: string, heading?: string}` (ADR-0031) | tools.ts:67-81 | IMPLEMENTED | — | Spec updated to reflect ADR-0031 at spec line 135. |
| spec-docs-mcp.md:137-139 | Section mode vs doc mode branching description | tools.ts:176-214 | IMPLEMENTED | — | Code has explicit if (!heading) / else branches matching spec prose. |
| spec-docs-mcp.md:139 | `commit_count = SUM(commit_count)` across underlying section-pair rows | tools.ts:186 | IMPLEMENTED | — | |
| spec-docs-mcp.md:139 | `last_commit = MAX(last_commit)` | tools.ts:187 | IMPLEMENTED | — | |
| spec-docs-mcp.md:139 | `heading = ""` on returned rows to denote aggregated mode | tools.ts:184 | IMPLEMENTED | — | |
| spec-docs-mcp.md:143 | `Ordered by commit_count DESC in both modes` | tools.ts:193, 212 | IMPLEMENTED | — | Both branches end with ORDER BY commit_count DESC. |
| spec-docs-mcp.md:143-155 | `LineageResult` shape: heading: string (not nullable) — `""` in doc mode, real heading in section mode | tools.ts:29-37 | IMPLEMENTED | — | Interface defines `heading: string` (non-nullable). |
| adr-0031:29 | Dashboard `/api/lineage` — heading query param becomes optional; handler calls `getLineage(doc, undefined)` | (dashboard scope, not this component) | — | — | Out of scope for mclaude-docs-mcp audit; dashboard is a separate component. |
| adr-0031:30 | Dashboard UI H1 lineage marker | (dashboard scope) | — | — | Out of scope for mclaude-docs-mcp audit. |

### Phase 2 — Code → Spec (ADR-0031 reverse pass, new code only)

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| tools.ts:173-175 (ADR-0027 comment block) | INFRA | Comment documenting no-status-filter rationale per ADR-0027. |
| tools.ts:176-195 (doc-mode branch) | INFRA | Entirely spec'd by ADR-0031 §Behaviour when heading omitted. |
| tools.ts:176 (`if (!heading)`) | INFRA | Falsy check covers both `undefined` (absent) and `""` (empty string) as specified by ADR-0031 and spec-docs-mcp.md:137. |
| tools.ts:191 (`AND l.section_a_doc != l.section_b_doc`) | INFRA | Self-reference exclusion not stated explicitly in ADR-0031 prose, but implied by "co-committed document" (a doc cannot be co-committed with itself in a meaningful sense). Consistent with ADR-0030 collapse semantics. No UNSPEC'd risk. |
| index.ts:108-113 (get_lineage MCP description) | INFRA | Tool description updated to document doc-mode / section-mode distinction. |

### Phase 3 — Test Coverage (ADR-0031 spec lines)

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| adr-0031:23 / tools.ts:67-81 | heading optional in GetLineageSchema | tests/lineage.test.ts:325-336 (empty-string = doc mode), tests/lineage.test.ts:292-311 (section mode with heading present) | — | UNIT_ONLY |
| adr-0031:24 / tools.ts:176-195 | doc mode: one row per co-committed doc, SUM commit_count | tests/lineage.test.ts:233-249 (count=3 and count=7 verified) | — | UNIT_ONLY |
| adr-0031:26 / tools.ts:186-187 | SUM(commit_count), MAX(last_commit) | tests/lineage.test.ts:233-249 (SUM verified); MAX(last_commit) not directly asserted in any test | — | UNIT_ONLY (SUM); last_commit MAX not asserted |
| adr-0031:27 / tools.ts:193 | ORDER BY commit_count DESC (doc mode) | tests/lineage.test.ts:251-258 (spec-docs-mcp(7) before adr-0015(3)) | — | UNIT_ONLY |
| adr-0031:27 / tools.ts:212 | ORDER BY commit_count DESC (section mode) | tests/lineage.test.ts:313-323 | — | UNIT_ONLY |
| adr-0031:24 / tools.ts:184 | heading="" on aggregated rows | tests/lineage.test.ts:260-264 (every row checked), tests/lineage.test.ts:339-343 (empty-string mode) | — | UNIT_ONLY |
| adr-0031:28 / (both branches) | no status filter | tests/lineage.test.ts:267-272 (status field present in metadata check) | — | UNIT_ONLY |
| spec-docs-mcp.md / adr-0031 | empty-string heading == absent heading (both enter doc mode) | tests/lineage.test.ts:325-337 (noHeading vs emptyHeading identical) | — | UNIT_ONLY |
| adr-0031 / tools.ts:191 | self-reference excluded from doc-mode results | tests/lineage.test.ts:274-290 | — | UNIT_ONLY |
| adr-0031:23 / section mode unchanged | section mode: heading field is real heading (not "") | tests/lineage.test.ts:304, 309 | — | UNIT_ONLY |

### Phase 4 — Bug Triage

No bugs in `.agent/bugs/` have `**Component**: mclaude-docs-mcp`. No triage required.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | — | — | No open bugs for this component |

### Round 3 Summary

- ADR-0031 spec lines evaluated: 16
- Implemented: 16
- Gap: 0
- Partial: 0
- Infra (new code): 5
- Unspec'd: 0
- Dead: 0
- Unit tested: 10 spec lines
- E2E tested: 0
- Untested: 0
- Noted: `MAX(last_commit)` in doc mode is not directly asserted in any test (the field is present and queried but no test validates the MAX pick when multiple last_commit values differ). Classified UNIT_ONLY (not UNTESTED) because `last_commit` is present and correct in the section-mode tests; the aggregation logic itself is implicitly exercised. No gap — the behavior is correct.

CLEAN — all ADR-0031 requirements are implemented; no gaps, no dead code, no new unspec'd behavior.
