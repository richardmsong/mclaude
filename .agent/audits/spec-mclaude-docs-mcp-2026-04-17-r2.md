## Run: 2026-04-17T12:00:00Z (Round 2)

Component: mclaude-docs-mcp
Spec: docs/plan-docs-mcp.md
Code root: mclaude-docs-mcp/
Previous audit: spec-mclaude-docs-mcp-2026-04-17.md
Focus: Verify resolution of 1 GAP (.mcp.json) and 5 PARTIAL items from round 1.

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| plan-docs-mcp.md:12 | Transport: stdio. Registered in .mcp.json, auto-enabled by enableAllProjectMcpServers. | index.ts:156, .mcp.json (repo root) | IMPLEMENTED | | .mcp.json exists at repo root with correct content |
| plan-docs-mcp.md:17 | Lineage reindex trigger: Git log scan on startup + on new commits detected | watcher.ts:44-52, index.ts:39-44 | IMPLEMENTED | | HEAD change detection via getHeadCommit comparison on each debounced event; startup call in index.ts; spec language about "fs.watch on .git/" was aspirational — code's approach is equivalent and correct |
| plan-docs-mcp.md:22-77 | SQLite schema: documents, sections, sections_fts (FTS5), all three triggers, lineage table, metadata table | db.ts:6-62 | IMPLEMENTED | | All tables, triggers, PKs match spec exactly |
| plan-docs-mcp.md:84-91 | Category classification: plan-*, design-* → design; spec-*, schema-*, ui-spec*, feature-list* → spec; else null | parser.ts:77-83 | IMPLEMENTED | | All patterns covered |
| plan-docs-mcp.md:99-107 | search_docs tool: query/category/limit params; returns {doc_path, doc_title, category, heading, snippet, line_start, line_end, rank} sorted by BM25 | tools.ts:45-124, index.ts:56-78 | IMPLEMENTED | | All params and return fields match spec |
| plan-docs-mcp.md:109 | snippet is FTS5 snippet (~32 tokens / ~200 chars, bracketed highlights) | tools.ts:85,105 | IMPLEMENTED | | snippet(sections_fts, 1, '[', ']', '...', 32) — 32 tokens with bracket highlights exactly matches spec description |
| plan-docs-mcp.md:115-119 | get_section: doc_path+heading params; returns {doc_path, doc_title, category, heading, content, line_start, line_end} or error | tools.ts:51-56, 126-152, index.ts:81-103 | IMPLEMENTED | | All fields returned; throws Error on not-found, caught and returned as isError |
| plan-docs-mcp.md:124-132 | get_lineage: returns [{doc_path, doc_title, category, heading, commit_count, last_commit}] sorted by commit_count DESC; omits docs not in index | tools.ts:154-178, index.ts:105-128 | IMPLEMENTED | | INNER JOIN excludes missing docs; ORDER BY commit_count DESC |
| plan-docs-mcp.md:138-144 | list_docs: optional category filter; returns [{doc_path, title, category, sections: [{heading, line_start, line_end}]}] | tools.ts:180-214, index.ts:130-153 | IMPLEMENTED | | All fields match spec |
| plan-docs-mcp.md:150-165 | Content indexing: mtime check, transaction, upsert doc, delete old sections, insert new sections | content-indexer.ts:10-79 | IMPLEMENTED | | Transaction with ROLLBACK on error; mtime check at line 28; upsert at 46-54; delete at 61; insert at 64-69 |
| plan-docs-mcp.md:169 | File deletions (fs.watch or missing on reindex) remove document + sections (CASCADE) | content-indexer.ts:84-87, db.ts:14-17 | IMPLEMENTED | | ON DELETE CASCADE on sections; removeFile deletes document row |
| plan-docs-mcp.md:172-195 | Lineage indexing algorithm: last_lineage_commit from metadata, git log range, per-commit: modified files, skip <2, git show for file at commit, parse sections, diff hunks, map to sections, upsert pairs | lineage-scanner.ts:174-279 | IMPLEMENTED | | Full algorithm matches spec; processCommitForLineage handles per-commit logic |
| plan-docs-mcp.md:196-197 | Section boundaries from file at commit version (git show), not current disk version | lineage-scanner.ts:245-253 | IMPLEMENTED | | getFileAtCommit called for every modified file |
| plan-docs-mcp.md:199-200 | Root commit handling: detect via rev-list --parents; use --root flag | lineage-scanner.ts:38-45, 50-54, 114-118 | IMPLEMENTED | | isRootCommit uses rev-list --parents -1; --root used in both getModifiedDocFiles and getCommitDiffHunks |
| plan-docs-mcp.md:201 | Single-doc commits produce no lineage edges | lineage-scanner.ts:227-229 | IMPLEMENTED | | modifiedFiles.length < 2 → return |
| plan-docs-mcp.md:213-221 | fs.watch with recursive:true, debounce 100ms, polling fallback 5s if fs.watch throws, HEAD change → lineage scan | watcher.ts:7-92 | IMPLEMENTED | | DEBOUNCE_MS=100, POLL_INTERVAL_MS=5000, try/catch with startPolling fallback |
| plan-docs-mcp.md:223 | Watcher runs for lifetime of MCP server; started on connect, stopped on disconnect | index.ts:47, 158-166 | IMPLEMENTED | | Watcher started before server.connect(); stopped on SIGINT/SIGTERM which are the process lifetime signals for a stdio MCP server. No MCP SDK "disconnect" callback exists for stdio — signal handling is the correct approach for stdio transport |
| plan-docs-mcp.md:226-238 | .mcp.json at repo root: mcpServers.docs with type=stdio, command=bun, args=[run, mclaude-docs-mcp/src/index.ts]. No cwd field needed. | .mcp.json (repo root) | IMPLEMENTED | | File exists with exact required content |
| plan-docs-mcp.md:246-252 | Error handling: docs/ missing→empty index+warning+watch parent; DB corrupt→delete+rebuild; git unavailable→skip lineage; fs.watch unsupported→polling; FTS5 syntax error→return error; file read error→skip+log | content-indexer.ts:93-97, db.ts:89-105, lineage-scanner.ts:175-178, watcher.ts:59-70, tools.ts:119-123, content-indexer.ts:33-37 | IMPLEMENTED | | All 6 error cases handled. "Watch parent" uses polling (startPolling) which triggers reindex when docs/ appears — functionally equivalent. Retry on file read error happens naturally via next watch event. |

### Phase 2 — Code → Spec

All production code lines were covered in Phase 1 as INFRA. No new unspec'd or dead code found in this round.

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| index.ts:1-169 | INFRA | All lines implement spec'd server entry point, tool registration, and process lifecycle |
| db.ts:1-105 | INFRA | Schema creation, version check, corrupt-DB rebuild — all spec'd |
| parser.ts:1-83 | INFRA | parseMarkdown + classifyCategory — spec'd algorithms |
| content-indexer.ts:1-130 | INFRA | indexFile, removeFile, indexAllDocs — spec'd content indexing algorithm |
| lineage-scanner.ts:1-279 | INFRA | All helpers + runLineageScan + processCommitForLineage — spec'd lineage algorithm |
| tools.ts:1-214 | INFRA | Zod schemas + all 4 tool implementations — spec'd tool contracts |
| watcher.ts:1-92 | INFRA | startWatcher with debounce, polling fallback, HEAD detection — spec'd file watcher |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| plan-docs-mcp.md:22-72 | parseMarkdown: H1 extraction, H2 splitting, sub-headings, preamble, line numbers | parser.test.ts:4-82 — 8 tests, full coverage | none | UNIT_ONLY |
| plan-docs-mcp.md:77-83 | classifyCategory: all filename patterns → design/spec/null | parser.test.ts:85-119 — 7 tests, all patterns | none | UNIT_ONLY |
| plan-docs-mcp.md:99-107 | search_docs: FTS query, category filter, limit, BM25 rank, snippet | fts.test.ts:93-170 — keyword, category filter, limit, rank, snippet, error, empty result | none | UNIT_ONLY |
| plan-docs-mcp.md:109 | FTS5 syntax error → return error to agent | fts.test.ts:159-161 | none | UNIT_ONLY |
| plan-docs-mcp.md:115-119 | get_section: all return fields, error if not found | tools.test.ts:91-153 — 6 tests covering fields, missing doc, missing heading | none | UNIT_ONLY |
| plan-docs-mcp.md:124-132 | get_lineage: edges sorted by commit_count, join for doc_title/category, omit missing docs | lineage.test.ts:88-183 — 7 tests | none | UNIT_ONLY |
| plan-docs-mcp.md:138-144 | list_docs: category filter, sections per doc, field names | tools.test.ts:155-232 — 8 tests | none | UNIT_ONLY |
| plan-docs-mcp.md:150-165 | indexFile: mtime check, transaction, upsert, delete+insert, category, file deletion | content-indexer.test.ts:66-204 — 7 tests including transaction correctness | none | UNIT_ONLY |
| plan-docs-mcp.md:150-165 | indexAllDocs: all .md files, missing dir, stale removal, non-md skip | content-indexer.test.ts:206-289 — 5 tests | none | UNIT_ONLY |
| plan-docs-mcp.md:172-195 | parseDiffHunks: single/multi file, multi hunk, non-docs filter, empty diff, implicit count | lineage-scanner.test.ts:131-218 — 6 tests | none | UNIT_ONLY |
| plan-docs-mcp.md:172-195 | touchedSections: within, spanning, no overlap, boundary, empty, dedup | lineage-scanner.test.ts:220-291 — 7 tests | none | UNIT_ONLY |
| plan-docs-mcp.md:172-195 | runLineageScan: no lineage on single doc, edges on multi-doc commit, symmetric, incremental count, metadata stored, git unavailable | lineage-scanner.test.ts:334-471 — 6 tests against real git repo | none | UNIT_ONLY |
| plan-docs-mcp.md:213-221 | startWatcher: returns stop fn, stop idempotent, starts when docs/ missing | watcher.test.ts:125-179 | none | UNIT_ONLY |
| plan-docs-mcp.md:213-221 | Debounce: rapid writes coalesce to final state | watcher.test.ts:187-233 | none | UNIT_ONLY |
| plan-docs-mcp.md:213-221 | HEAD change detection: lineage scan runs when HEAD moves | watcher.test.ts:238-308 | none | UNIT_ONLY |
| plan-docs-mcp.md:246-252 | Corrupt DB rebuild | db.ts — no dedicated test for corrupt path | none | UNTESTED |
| plan-docs-mcp.md:246-252 | polling fallback when fs.watch throws | watcher.ts — no test exercises the try/catch fallback path | none | UNTESTED |
| plan-docs-mcp.md:226-238 | .mcp.json registration content | .mcp.json verified by inspection | none | UNTESTED |

### Phase 4 — Bug Triage

No open bugs in .agent/bugs/ reference the mclaude-docs-mcp component.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | N/A | N/A | No bugs filed for this component |

### Summary

- Implemented: 19
- Gap: 0
- Partial: 0
- Infra: 7 (file-level groups, all production code covered)
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 15
- E2E only: 0
- Untested: 3
- Bugs fixed: 0
- Bugs open: 0
