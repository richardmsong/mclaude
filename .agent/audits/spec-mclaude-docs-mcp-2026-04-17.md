## Run: 2026-04-17T00:00:00Z

Component: mclaude-docs-mcp
Spec: docs/plan-docs-mcp.md
Code root: mclaude-docs-mcp/

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| plan-docs-mcp.md:11 | Language: TypeScript (bun). Separate package with its own tsconfig targeting Bun. | mclaude-docs-mcp/package.json, tsconfig.json | IMPLEMENTED | | package.json sets type=module, tsconfig targets Bun with bun-types |
| plan-docs-mcp.md:12 | Transport: stdio. Registered in .mcp.json. | index.ts:156 (StdioServerTransport) | PARTIAL | CODE→FIX | stdio transport is implemented; .mcp.json does not exist at repo root — see spec line 226 |
| plan-docs-mcp.md:13 | Search engine: SQLite FTS5 with BM25 ranking | db.ts:24-28, tools.ts:79-116 | IMPLEMENTED | | FTS5 virtual table with BM25 rank via sections_fts.rank |
| plan-docs-mcp.md:14 | Doc categorization: plan-*, design-* → design; spec-*, schema-*, ui-spec* → spec | parser.ts:77-83 | IMPLEMENTED | | classifyCategory matches all filename patterns |
| plan-docs-mcp.md:15 | Section granularity: ## (H2) level. Sub-headings (###, ####) included in parent section's content. | parser.ts:41-58 | IMPLEMENTED | | Splits on ^## , sub-headings folded into currentLines |
| plan-docs-mcp.md:16 | Content reindex trigger: fs.watch on docs/ | watcher.ts:58-70 | IMPLEMENTED | | fs.watch used with fallback to polling |
| plan-docs-mcp.md:17 | Lineage reindex trigger: on startup + new commits detected via fs.watch on .git/ or periodic check | watcher.ts:44-52, index.ts:39-44 | PARTIAL | SPEC→FIX | Lineage triggered on startup and when HEAD moves (detected in runReindex). Spec says "fs.watch on .git/" but code detects HEAD change via getHeadCommit comparison — equivalent behavior, no explicit .git/ watch. Acceptable approach. |
| plan-docs-mcp.md:18 | DB location: mclaude-docs-mcp/.docs-index.db. Gitignored in mclaude-docs-mcp/.gitignore (pattern .docs-index.db). Also add *.db to repo root .gitignore. | index.ts:23, mclaude-docs-mcp/.gitignore:1, .gitignore:62 | IMPLEMENTED | | dbPath set to ../  .docs-index.db relative to src/. Both .gitignore files correct. |
| plan-docs-mcp.md:26-77 | SQLite schema: documents, sections, sections_fts, triggers, lineage, PRIMARY KEY on lineage 4-tuple | db.ts:6-62 | IMPLEMENTED | | All tables, FTS5 virtual table, all three triggers, lineage with 4-part PK match spec exactly |
| plan-docs-mcp.md:84-91 | Category classification table including feature-list* → spec | parser.ts:77-83 | IMPLEMENTED | | /^(spec-\|schema-\|ui-spec\|feature-list)/ covers all spec categories |
| plan-docs-mcp.md:99-107 | search_docs: query (required), category (optional enum), limit (default 10). Returns array of {doc_path, doc_title, category, heading, snippet, line_start, line_end, rank} sorted by BM25 relevance | tools.ts:45-124, index.ts:56-78 | IMPLEMENTED | | Zod schema + SQL query match spec exactly including all return fields |
| plan-docs-mcp.md:109 | snippet is FTS5 snippet (~200 chars) highlighted match context | tools.ts:85,105 | PARTIAL | SPEC→FIX | Uses snippet(sections_fts, 1, '[', ']', '...', 32) — column index 1 = content, 32 tokens, not ~200 chars. 32 tokens is roughly 160-200 chars, close but not measured in chars. Spec says "~200 chars" which is approximate — this is reasonable. |
| plan-docs-mcp.md:115-119 | get_section: doc_path, heading params. Returns {doc_path, doc_title, category, heading, content, line_start, line_end} or error if not found | tools.ts:51-56, 126-152, index.ts:81-103 | IMPLEMENTED | | All fields returned, throws Error on not-found which is caught and returned as isError |
| plan-docs-mcp.md:124-132 | get_lineage: doc_path, heading params. Returns [{doc_path, doc_title, category, heading, commit_count, last_commit}] sorted by commit_count DESC. Joins documents to populate doc_title/category. If doc doesn't exist in index, entry omitted. | tools.ts:58-61, 154-178, index.ts:105-128 | IMPLEMENTED | | INNER JOIN with documents ensures missing docs are excluded. ORDER BY commit_count DESC. |
| plan-docs-mcp.md:138-144 | list_docs: optional category filter. Returns [{doc_path, title, category, sections: [{heading, line_start, line_end}]}] | tools.ts:63-65, 180-214, index.ts:130-153 | IMPLEMENTED | | All fields present. Note: return field is `title` not `doc_title` for the document-level field — spec says `title` so correct. |
| plan-docs-mcp.md:150-165 | Content indexing: stat mtime, skip if unchanged, read, parse, classify, BEGIN TRANSACTION, upsert doc, delete old sections, insert new sections, COMMIT | content-indexer.ts:10-79 | IMPLEMENTED | | Transaction wraps steps 6-9 with ROLLBACK on error |
| plan-docs-mcp.md:167 | Section parsing splits on ^## . Each section from ## line to line before next ## or EOF. H1/preamble not a section. | parser.ts:22-72 | IMPLEMENTED | | Matches exactly |
| plan-docs-mcp.md:169 | File deletions remove document and all sections (CASCADE) | content-indexer.ts:84-87, db.ts:14-17 (ON DELETE CASCADE) | IMPLEMENTED | | removeFile deletes from documents, CASCADE removes sections |
| plan-docs-mcp.md:172-195 | Lineage indexing algorithm: last_lineage_commit from metadata, git log range, per-commit modified files, skip < 2 files, git show for file at commit, parse sections, diff hunks, map to sections, upsert lineage pairs | lineage-scanner.ts:174-219 | IMPLEMENTED | | Full algorithm matches spec |
| plan-docs-mcp.md:196-197 | Section boundaries derived from file at commit version (git show <commit>:<path>), not current on-disk version | lineage-scanner.ts:245-253 | IMPLEMENTED | | getFileAtCommit used for every file in processCommitForLineage |
| plan-docs-mcp.md:199-200 | Root commit handling: detect by checking if commit~1 resolves; use git diff-tree --root | lineage-scanner.ts:38-45, 50-54, 114-118 | IMPLEMENTED | | isRootCommit uses rev-list --parents, root-specific args used in both getModifiedDocFiles and getCommitDiffHunks |
| plan-docs-mcp.md:201 | Single-doc commits produce no lineage edges | lineage-scanner.ts:227-229 | IMPLEMENTED | | modifiedFiles.length < 2 → return early |
| plan-docs-mcp.md:204-210 | Metadata table: key TEXT PRIMARY KEY, value TEXT NOT NULL. Stores last_lineage_commit, schema_version | db.ts:58-62, lineage-scanner.ts:186-208 | IMPLEMENTED | | metadata table created, both keys used |
| plan-docs-mcp.md:213-221 | File watcher: fs.watch with recursive:true on docs/. macOS FSEvents reliable. Linux fallback to polling (stat every 5s). Debounce 100ms. | watcher.ts:7-82 | IMPLEMENTED | | DEBOUNCE_MS=100, POLL_INTERVAL_MS=5000, recursive:true, polling fallback on catch |
| plan-docs-mcp.md:218-221 | On each debounced event: re-stat all docs, reindex changed mtime files, check HEAD moved → lineage scan | watcher.ts:23-52 | IMPLEMENTED | | runReindex does all three steps |
| plan-docs-mcp.md:223 | Watcher runs for lifetime of MCP server process (started on connect, stopped on disconnect) | index.ts:47, 158-166 | PARTIAL | CODE→FIX | Watcher started before server.connect(), stopped on SIGINT/SIGTERM. Spec says "stopped on disconnect" — there is no disconnect handler. SIGINT/SIGTERM is close but not exactly on MCP disconnect. |
| plan-docs-mcp.md:226-238 | Registration: .mcp.json at repo root with mcpServers.docs entry: type=stdio, command=bun, args=[run, mclaude-docs-mcp/src/index.ts] | (no .mcp.json exists) | GAP | CODE→FIX | .mcp.json does not exist at repo root |
| plan-docs-mcp.md:246-252 | Error handling table: docs/ missing → start with empty index, log warning, watch parent; DB corrupt → delete+rebuild; git unavailable → skip lineage; fs.watch unsupported → polling; FTS5 syntax error → return error to agent; file read error → skip+log+retry | content-indexer.ts:93-97, db.ts:89-105, lineage-scanner.ts:175-178, watcher.ts:68-70, tools.ts:119-123, content-indexer.ts:33-37 | PARTIAL | CODE→FIX | Most cases handled. "Watch parent for docs/ creation" is partially implemented via polling (startPolling detects when docs/ exists later) but no explicit "watch parent directory." File read error: logs warning, skips — no explicit retry noted (retry happens naturally on next watch event). FTS5 error: throws (caught in index.ts and returned as isError). Mostly correct. The "watch parent" is polling not watching; acceptable but slightly different from spec. |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| index.ts:1-17 | INFRA | Imports — standard plumbing |
| index.ts:19-24 | INFRA | Path resolution (repoRoot, docsDir, dbPath) — required infrastructure for spec'd behavior |
| index.ts:25 | INFRA | Startup log — standard operational logging |
| index.ts:28 | INFRA | openDb call — required to initialize DB per spec |
| index.ts:31-36 | INFRA | Initial content index call with error catch — implements spec "On startup" content indexing |
| index.ts:39-44 | INFRA | Initial lineage scan call — implements spec "on startup" lineage trigger |
| index.ts:47 | INFRA | startWatcher call — implements spec watcher startup |
| index.ts:50-53 | INFRA | McpServer construction with name/version — required MCP boilerplate |
| index.ts:55-78 | INFRA | search_docs tool registration — wraps spec'd searchDocs function |
| index.ts:80-103 | INFRA | get_section tool registration — wraps spec'd getSection function |
| index.ts:105-128 | INFRA | get_lineage tool registration — wraps spec'd getLineage function |
| index.ts:130-153 | INFRA | list_docs tool registration — wraps spec'd listDocs function |
| index.ts:155-166 | INFRA | StdioServerTransport + SIGINT/SIGTERM handlers — spec requires stdio transport; signal handling is operational necessity |
| index.ts:168-169 | INFRA | server.connect + ready log — required transport connection |
| db.ts:1-3 | INFRA | Imports + SCHEMA_VERSION constant — required for schema management per spec |
| db.ts:64-105 | INFRA | openDb function: opens DB, runs schema, checks version, rebuilds on corrupt/mismatch — directly implements spec's "SQLite DB corrupt or schema mismatch → Delete DB file, rebuild from scratch" |
| parser.ts:1-11 | INFRA | Interface declarations (ParsedSection, ParsedDoc) — TypeScript type definitions supporting spec'd data structures |
| parser.ts:22-72 | INFRA | parseMarkdown — directly implements spec's content parsing algorithm |
| parser.ts:77-83 | INFRA | classifyCategory — directly implements spec's category classification |
| content-indexer.ts:1-4 | INFRA | Imports |
| content-indexer.ts:10-79 | INFRA | indexFile — directly implements spec's content indexing algorithm |
| content-indexer.ts:84-87 | INFRA | removeFile — implements spec's file deletion handling |
| content-indexer.ts:93-130 | INFRA | indexAllDocs — implements spec's "index all docs on startup", also cleans up deleted files |
| lineage-scanner.ts:1-3 | INFRA | Imports |
| lineage-scanner.ts:9-17 | INFRA | git() helper — infrastructure for all git commands used in spec'd lineage algorithm |
| lineage-scanner.ts:22-25 | INFRA | isGitAvailable — implements spec error handling: "Git not available → skip lineage" |
| lineage-scanner.ts:30-33 | INFRA | getHeadCommit — used by watcher to detect new commits; exported and used by watcher.ts |
| lineage-scanner.ts:38-45 | INFRA | isRootCommit — implements spec's root commit detection |
| lineage-scanner.ts:50-62 | INFRA | getModifiedDocFiles — implements spec step 3a |
| lineage-scanner.ts:67-69 | INFRA | getFileAtCommit — implements spec step 3c |
| lineage-scanner.ts:71-109 | INFRA | DiffHunk interface + parseDiffHunks — implements spec step 3e/f |
| lineage-scanner.ts:113-123 | INFRA | getCommitDiffHunks — implements spec step 3e |
| lineage-scanner.ts:125-148 | INFRA | SectionBoundary interface + touchedSections — implements spec step 3f (hunk-to-section mapping) |
| lineage-scanner.ts:153-168 | INFRA | upsertLineage — implements spec step 3g upsert |
| lineage-scanner.ts:174-219 | INFRA | runLineageScan — implements spec's full lineage scan algorithm |
| lineage-scanner.ts:224-279 | INFRA | processCommitForLineage — implements spec step 3 per-commit processing |
| tools.ts:1-65 | INFRA | Zod schemas + type interfaces — supporting types for spec'd tool contracts |
| tools.ts:69-124 | INFRA | searchDocs — directly implements spec search_docs tool |
| tools.ts:126-152 | INFRA | getSection — directly implements spec get_section tool |
| tools.ts:154-178 | INFRA | getLineage — directly implements spec get_lineage tool |
| tools.ts:180-214 | INFRA | listDocs — directly implements spec list_docs tool |
| watcher.ts:1-7 | INFRA | Imports + constants — DEBOUNCE_MS and POLL_INTERVAL_MS match spec values |
| watcher.ts:10-92 | INFRA | startWatcher — directly implements spec's file watcher with debounce, polling fallback, and stop function |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| plan-docs-mcp.md:22-72 (parser) | parseMarkdown: H1 extraction, H2 splitting, sub-headings in parent, preamble not section, line numbers | parser.test.ts:4-82 (full coverage) | none | UNIT_ONLY |
| plan-docs-mcp.md:77-83 (category) | classifyCategory: all filename patterns | parser.test.ts:85-119 (full coverage) | none | UNIT_ONLY |
| plan-docs-mcp.md:99-107 (search_docs) | search_docs tool: FTS query, category filter, limit, BM25 rank, snippet | fts.test.ts:93-170 (keyword match, category filter, limit, rank, snippet, error) | none | UNIT_ONLY |
| plan-docs-mcp.md:115-119 (get_section) | get_section: return fields, error if not found | fts.test.ts (tests searchDocs only, no get_section test) | none | UNTESTED |
| plan-docs-mcp.md:124-132 (get_lineage) | get_lineage: returns edges, sorted, doc joins, omits missing docs | lineage.test.ts:88-183 (full coverage including omit-missing-doc case) | none | UNIT_ONLY |
| plan-docs-mcp.md:138-144 (list_docs) | list_docs: category filter, section list per doc | No test file covers listDocs | none | UNTESTED |
| plan-docs-mcp.md:150-165 (indexFile) | Content indexing: mtime check, transaction, upsert, delete+insert | No direct test for indexFile; covered indirectly via FTS tests | none | UNTESTED |
| plan-docs-mcp.md:172-195 (lineage scan) | Lineage scan algorithm: git log, per-commit processing, upsert | No unit test for runLineageScan or processCommitForLineage | none | UNTESTED |
| plan-docs-mcp.md:213-221 (watcher) | File watcher with debounce, polling fallback | No test for startWatcher | none | UNTESTED |
| plan-docs-mcp.md:246-252 (error handling) | Error cases: corrupt DB, git unavailable, FTS syntax error | fts.test.ts:159-161 (FTS error); db.ts error path not unit tested | none | UNIT_ONLY |

### Phase 4 — Bug Triage

No open bugs in .agent/bugs/ reference the mclaude-docs-mcp component.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | N/A | N/A | No bugs filed for this component |

### Summary

- Implemented: 22
- Gap: 1
- Partial: 5
- Infra: 46
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 5
- E2E only: 0
- Untested: 5
- Bugs fixed: 0
- Bugs open: 0
