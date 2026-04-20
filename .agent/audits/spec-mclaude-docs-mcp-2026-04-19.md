## Run: 2026-04-19T00:00:00Z

Component: `mclaude-docs-mcp`
ADRs evaluated: adr-0015 (accepted), adr-0018 (accepted), adr-0020 (accepted), adr-0021 (accepted)
Specs evaluated: spec-doc-layout.md

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0020:112-123 | `indexAllDocs` in `content-indexer.ts` must recurse — file enumeration switches to a recursive walk that returns every `**/*.md` under `docs/` | `content-indexer.ts:10-39` (`walkMdFiles`) + `content-indexer.ts:138` | IMPLEMENTED | — | `walkMdFiles` recursively descends, skips symlinks |
| adr-0020:118-120 | Skip symlinked directories to avoid loops | `content-indexer.ts:26-29` | IMPLEMENTED | — | `lstatSync` + `isSymbolicLink()` guard |
| adr-0020:119-123 | Stale-removal reference list (`docPaths`) must be built from the same recursive `files` list | `content-indexer.ts:149-159` | IMPLEMENTED | — | `docPaths = files.map(...)` where `files` is the result of `walkMdFiles(docsDir)` |
| adr-0020:126-128 | File watcher `runReindex` joins `docsDir` with filename; on macOS FSEvents nested filenames are relative paths (`ui/spec-design-system.md`) — `join(docsDir, filename)` resolves correctly | `watcher.ts:28-30` | IMPLEMENTED | — | `const fullPath = join(docsDir, filename)` — correct for both flat and nested relative paths |
| adr-0020:131-135 | Classifier unchanged: `adr-*` → `'adr'`, `spec-*` → `'spec'`, `feature-list*` → `'spec'` | `parser.ts:96-102` | IMPLEMENTED | — | `classifyCategory` uses basename prefix matching; directory depth irrelevant |
| adr-0020:134-135 | No new `component` column or filter on `list_docs` or `search_docs` | `tools.ts:47-69` | IMPLEMENTED | — | No `component` param in `SearchDocsSchema` or `ListDocsSchema` |
| adr-0018:122-125 | `search_docs` gains optional `status` filter | `tools.ts:50-51`, `searchDocs:86-89` | IMPLEMENTED | — | `status: AdrStatusEnum.optional()` and applied as `WHERE d.status = ?` |
| adr-0018:122-125 | `list_docs` accepts optional `status` filter | `tools.ts:68-69`, `listDocs:181-189` | IMPLEMENTED | — | `status: AdrStatusEnum.optional()` and applied as `WHERE status = ?` |
| adr-0018:125 | `get_lineage` only traverses edges where connected doc has `accepted` or `implemented` status; draft/superseded/withdrawn ADRs excluded | `tools.ts:154-173` | IMPLEMENTED | — | Query: `d.category != 'adr' OR d.status IS NULL OR d.status IN ('accepted', 'implemented')` |
| adr-0018:126 | Parser reads `**Status**:` line from ADR body and stores it on `documents.status TEXT` column | `parser.ts:25`, `parser.ts:47-52`; `db.ts:13`; `content-indexer.ts:82-89` | IMPLEMENTED | — | `STATUS_RE` regex, scanned only in first 20 lines; `documents.status TEXT` column in schema |
| adr-0018:126 | Spec says: "Index is re-derived from the filesystem; no data migration needed" | `db.ts:84-88` (schema version check → rebuild) | IMPLEMENTED | — | Schema version `"2"` triggers fresh rebuild on mismatch |
| adr-0015:33-85 | SQLite schema: `documents(id, path, category, title, mtime)`, `sections`, `sections_fts`, lineage, metadata tables | `db.ts:6-63` | IMPLEMENTED | — | Schema v2 adds `status TEXT` column; all other columns present |
| adr-0015:89-99 | Category classification: `adr-*` → `'adr'`, `spec-*` → `'spec'`, `feature-list*` → `'spec'` | `parser.ts:96-102` | IMPLEMENTED | — | Superseded `'design'` category per adr-0021 |
| adr-0021:82-95 | Classifier: `adr-*` → `'adr'`, `spec-*` → `'spec'`, `feature-list*` → `'spec'`, everything else → `null`; `'design'` removed | `parser.ts:96-102` | IMPLEMENTED | — | `classifyCategory` matches exactly |
| adr-0021:93-94 | Tool parameter enum for `search_docs` and `list_docs`: `category: 'adr' \| 'spec'` | `tools.ts:49`, `tools.ts:67` | IMPLEMENTED | — | `z.enum(["adr", "spec"])` |
| adr-0015:103-115 | `search_docs` tool: query, category (optional), limit (default 10). Returns `{doc_path, doc_title, category, heading, snippet, line_start, line_end, rank}` | `tools.ts:47-115` | IMPLEMENTED | — | |
| adr-0015:117-128 | `get_section` tool: doc_path, heading. Returns section or error if not found | `tools.ts:117-143` | IMPLEMENTED | — | |
| adr-0015:129-139 | `get_lineage` tool: returns co-modified sections sorted by commit_count desc; excludes entries where doc no longer in index | `tools.ts:145-174` | IMPLEMENTED | — | JOIN on `documents` table filters deleted docs |
| adr-0015:141-152 | `list_docs` tool: category filter optional; returns `{doc_path, title, category, sections: [{heading, line_start, line_end}]}` | `tools.ts:176-212` | IMPLEMENTED | — | |
| adr-0015:155-177 | Content indexing: stat file → mtime check → read → parse → classify → BEGIN TX → upsert doc → delete old sections → insert new (FTS triggers) → COMMIT | `content-indexer.ts:45-115` | IMPLEMENTED | — | |
| adr-0015:179-208 | Lineage indexing: git log → for each commit get modified files → diff hunks → map to sections → upsert lineage rows | `lineage-scanner.ts:174-219`, `processCommitForLineage:224-278` | IMPLEMENTED | — | |
| adr-0015:206-208 | Root commit detection: `git rev-list --parents -1 <commit>` | `lineage-scanner.ts:38-45` | IMPLEMENTED | — | |
| adr-0015:210-229 | File watcher: `fs.watch` with `recursive: true`, debounce 100ms, fallback to polling every 5s | `watcher.ts:7,65-66,73-80` | IMPLEMENTED | — | |
| adr-0015:233-247 | `.mcp.json` registration with `bun run mclaude-docs-mcp/src/index.ts` | Not in scope of this file audit — `.mcp.json` at repo root | IMPLEMENTED | — | (Confirmed present per prior audit; not re-read) |
| adr-0015:251-261 | Error handling: corrupt DB → delete and rebuild; FTS5 error → return error message; file read error → skip and continue | `db.ts:90-103`; `tools.ts:110-114`; `content-indexer.ts:67-72` | IMPLEMENTED | — | |
| adr-0015:222-229 | Watcher: if `docs/` doesn't exist, watch parent for creation (polling fallback) | `watcher.ts:59-81` | IMPLEMENTED | — | Polling fallback handles missing `docs/` |
| spec-doc-layout.md:122-133 | Docs MCP indexes `docs/**/*.md` recursively; classifies by filename prefix only | `content-indexer.ts:10-39`; `parser.ts:96-102` | IMPLEMENTED | — | |
| spec-doc-layout.md:133-136 | `list_docs` and `search_docs` expose `category: 'adr' \| 'spec'` filter; no `component` filter | `tools.ts:47-69` | IMPLEMENTED | — | |
| adr-0020:299 | Error: parser encounters symlink loop → ignore symlinks during walk | `content-indexer.ts:26-29` | IMPLEMENTED | — | Both symlinked files and dirs are skipped via `lstatSync` + `isSymbolicLink()` |
| adr-0020:113-114 | `indexAllDocs` two sites change together: file enumeration AND docPaths list both use same recursive walk | `content-indexer.ts:138,149` | IMPLEMENTED | — | Both `files = walkMdFiles(docsDir)` result |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| `content-indexer.ts:1-4` | INFRA | Imports |
| `content-indexer.ts:120-123` | INFRA | `removeFile` exported helper — called by `indexFile` when file is gone |
| `parser.ts:1-14` | INFRA | Exported interfaces and type aliases |
| `parser.ts:25` | INFRA | `STATUS_RE` compiled regex — implementation detail for status extraction |
| `db.ts:4` | INFRA | `SCHEMA_VERSION = "2"` constant — schema migration control; not spec'd explicitly but required by the "rebuild on mismatch" behavior |
| `lineage-scanner.ts:1-17` | INFRA | Imports and `git()` helper |
| `lineage-scanner.ts:22-25` | INFRA | `isGitAvailable` — used by `runLineageScan` as a guard per spec |
| `lineage-scanner.ts:71-109` | INFRA | `parseDiffHunks` — internal parser for git diff output, necessary for lineage algorithm |
| `lineage-scanner.ts:113-123` | INFRA | `getCommitDiffHunks` — internal helper, composes diff commands per spec |
| `lineage-scanner.ts:125-148` | INFRA | `touchedSections` — internal section mapping, part of lineage algorithm |
| `lineage-scanner.ts:153-168` | INFRA | `upsertLineage` — internal DB write for lineage edges |
| `watcher.ts:55-57` | INFRA | `watcher` and `pollInterval` typed variables — watcher lifecycle management |
| `index.ts:19-23` | INFRA | Path computation for repo root, docs dir, db path |
| `index.ts:157-168` | INFRA | `SIGINT`/`SIGTERM` handlers for clean shutdown |
| `tools.ts:1-43` | INFRA | Imports, result interfaces, Zod schemas |
| `content-indexer.ts:129-163` | INFRA | `indexAllDocs` function — outer shell, error logging, counts |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| adr-0020: recursive walk | `indexAllDocs` recurses into subdirs | `recursion-and-status.test.ts:158-303` | — | UNIT_ONLY |
| adr-0020: symlink avoidance | Skip symlinked directories | `recursion-and-status.test.ts:234-260` | — | UNIT_ONLY |
| adr-0020: stale-removal correctness | Stale-removal uses same recursive files list | `recursion-and-status.test.ts:262-303` | — | UNIT_ONLY |
| adr-0020: watcher nested filename | `join(docsDir, filename)` resolves nested relative path | `recursion-and-status.test.ts:325-380` | — | UNIT_ONLY |
| adr-0020: classifier unchanged | `adr-*` → `'adr'`, `spec-*` → `'spec'` for nested paths | `recursion-and-status.test.ts:369-379`; `parser.test.ts:85-117` | — | UNIT_ONLY |
| adr-0018: status extraction (all 5 values) | Parser reads `**Status**:` | `recursion-and-status.test.ts:387-475` | — | UNIT_ONLY |
| adr-0018: status stored to DB | `documents.status TEXT` populated on indexFile | `recursion-and-status.test.ts:437-475` | — | UNIT_ONLY |
| adr-0018: status=accepted/draft/implemented/superseded filter on list_docs | `listDocs` status filter | `recursion-and-status.test.ts:508-547` | — | UNIT_ONLY |
| adr-0018: status filter on search_docs | `searchDocs` status filter | `recursion-and-status.test.ts:569-598` | — | UNIT_ONLY |
| adr-0018: get_lineage skips draft/superseded/withdrawn | Status filtering in lineage traversal | `recursion-and-status.test.ts:649-712` | — | UNIT_ONLY |
| adr-0015: search_docs FTS5 | Full-text search, category filter, limit, snippet, rank | `fts.test.ts:94-171` | — | UNIT_ONLY |
| adr-0015: get_section | Section retrieval by doc_path + heading | `tools.test.ts:92-153` | — | UNIT_ONLY |
| adr-0015: list_docs | Category filter, sections shape | `tools.test.ts:156-233` | — | UNIT_ONLY |
| adr-0015: get_lineage | Sorted by commit_count, excludes deleted docs | `lineage.test.ts:89-184` | — | UNIT_ONLY |
| adr-0015: content indexing | mtime check, transaction, category, FTS | `content-indexer.test.ts:89-215` | — | UNIT_ONLY |
| adr-0015: lineage scanner | git commit processing, symmetric edges, incremental scan | `lineage-scanner.test.ts:335-472` | — | UNIT_ONLY |
| adr-0015: file watcher | debounce, HEAD detection, stop function | `watcher.test.ts:126-310` | — | UNIT_ONLY |

### Phase 4 — Bug Triage

No open bugs with `**Component**: mclaude-docs-mcp` found in `.agent/bugs/`. All five open bugs reference other components (spa, session-agent, control-plane).

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| — | — | N/A | No docs-mcp bugs in .agent/bugs/ |

### Summary

- Implemented: 28
- Gap: 0
- Partial: 0
- Infra: 17
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 17
- E2E only: 0
- Untested: 0
- Bugs fixed: 0
- Bugs open: 0
