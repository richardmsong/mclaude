# Spec: Docs MCP Server

## Role

`mclaude-docs-mcp` is a local-only MCP server (stdio transport) that gives agents structured access to the `docs/` corpus — ADRs and specs. It parses markdown files into H2 sections, indexes them in a SQLite database with FTS5 full-text search, scans git history to derive lineage edges between co-committed sections, and watches the filesystem to keep the index live. Four MCP tools — `search_docs`, `get_section`, `get_lineage`, `list_docs` — expose the index. Additional library functions in `src/tools.ts` are exported for sibling packages (notably `mclaude-docs-dashboard`) that consume the same data without duplicating logic.

Established by ADR-0015 (the v1 design), extended by ADR-0018 (status column + status filters), extended by ADR-0027 (`commit_count` + `last_status_change` columns, lineage filter removal, `readRawDoc` helper).

## Runtime

- Bun (loads `.ts` files natively; no build step).
- Single-process; stdio transport for MCP. No HTTP, no auth, no network.
- Entrypoint: `mclaude-docs-mcp/src/index.ts`. On boot: resolves repo root via a two-level ascent from `import.meta.url` (the entry file lives at `mclaude-docs-mcp/src/index.ts`, a fixed depth below the repo root); opens the SQLite DB at `<repoRoot>/mclaude-docs-mcp/.docs-index.db`; runs `indexAllDocs`; runs `runLineageScan` (initial lineage derivation from `git log`); starts `startWatcher` for the process lifetime; registers the four MCP tools.

## Data store

SQLite file at `<repoRoot>/mclaude-docs-mcp/.docs-index.db`, opened in WAL mode with foreign keys on. Schema lives in the `SCHEMA_SQL` constant in `src/db.ts`. Schema version is tracked in the `metadata` table under key `schema_version`; on mismatch or corruption, `openDb` deletes the file and rebuilds from scratch. Current version: `"4"` (ADR-0032).

### Tables

**`documents`** — one row per indexed markdown file.

| Column               | Type     | Notes                                                                      |
|----------------------|----------|----------------------------------------------------------------------------|
| `id`                 | INTEGER  | Primary key.                                                               |
| `path`               | TEXT     | Unique. Repo-root-relative POSIX path, e.g. `docs/adr-0015-docs-mcp.md`.   |
| `category`           | TEXT     | `adr`, `spec`, or `null`. Classified from the basename.                    |
| `title`              | TEXT     | H1 text (without the `# ` prefix). `null` if no H1.                        |
| `status`             | TEXT     | ADR status per ADR-0018: `draft|accepted|implemented|superseded|withdrawn`. `null` for specs or ADRs without a `**Status**:` line. |
| `commit_count`       | INTEGER  | Total git commits that touched this file (per ADR-0027). Default 0. Maintained by the lineage scanner. |
| `last_status_change` | TEXT     | ISO date (`YYYY-MM-DD`) of the most recent `**Status history**` line (per ADR-0027). `null` for specs or ADRs without a history list. |
| `mtime`              | REAL     | File mtime as a Unix-epoch float seconds. Used to skip reparse on unchanged files. |

**`sections`** — one row per H2 section within a document.

| Column       | Type     | Notes                                                                |
|--------------|----------|----------------------------------------------------------------------|
| `id`         | INTEGER  | Primary key.                                                         |
| `doc_id`     | INTEGER  | FK → `documents.id`, `ON DELETE CASCADE`.                            |
| `heading`    | TEXT     | H2 text without the `## ` prefix.                                    |
| `content`    | TEXT     | Full section body including the `## ` heading line, trailing whitespace trimmed. |
| `line_start` | INTEGER  | 1-based line number of the `## ` line.                               |
| `line_end`   | INTEGER  | 1-based inclusive line number of the last line in the section.       |

**`sections_fts`** — FTS5 virtual table shadowing `sections(heading, content)`. Maintained via triggers (`sections_ai`, `sections_ad`, `sections_au`). BM25 ranking via `sections_fts.rank`.

**`lineage`** — one row per ordered (section_a, section_b) pair observed co-committed at least once.

| Column             | Type    | Notes                                                            |
|--------------------|---------|------------------------------------------------------------------|
| `section_a_doc`    | TEXT    | doc_path of the first section of the pair.                       |
| `section_a_heading`| TEXT    | Heading of the first section.                                    |
| `section_b_doc`    | TEXT    | doc_path of the second section.                                  |
| `section_b_heading`| TEXT    | Heading of the second section.                                   |
| `commit_count`     | INTEGER | Number of commits that touched both sections together. Default 1. |
| `last_commit`      | TEXT    | Short hash of the most recent co-committing commit.              |

Primary key: `(section_a_doc, section_a_heading, section_b_doc, section_b_heading)`. The scanner writes each observed pair exactly once per (A, B) ordering; `get_lineage` queries only the `section_a = doc/heading` position, so lookups are symmetric only when the scanner chose to write both orderings.

**`line_lineage_cache`** — one row per `(doc_path, head_sha)` pair, caching the full per-block line-lineage response for a document at a specific HEAD (ADR-0032).

| Column          | Type    | Notes                                                                    |
|-----------------|---------|--------------------------------------------------------------------------|
| `doc_path`      | TEXT    | Repo-root-relative POSIX path of the document.                           |
| `head_sha`      | TEXT    | Full 40-char Git HEAD SHA at which the cached response was computed (matches the output of `getHeadCommit(repoRoot)` from `lineage-scanner.ts`, which runs `git rev-parse HEAD` — no truncation). |
| `response_json` | TEXT    | Serialized `{html, blocks}` response, exactly as returned by the tool.   |
| `computed_at`   | INTEGER | Unix-epoch seconds the row was written. For cache-age debugging.         |

Primary key: `(doc_path, head_sha)`. Index `idx_line_lineage_cache_doc` on `doc_path` for the batch DELETE path.

Invalidation:

- On HEAD advance (watcher signal): `git diff --name-only <prev> <new>` yields the changed paths. `DELETE FROM line_lineage_cache WHERE doc_path IN (<changed>)`. For unchanged docs, `UPDATE line_lineage_cache SET head_sha = :new_head WHERE head_sha = :prev_head AND doc_path NOT IN (<changed>)` — the computed response is still valid, so the key is re-stamped to the new HEAD.
- On boot: `DELETE FROM line_lineage_cache WHERE head_sha != :current_head_sha` once. Bounds steady-state cache to ≤1 row per `doc_path`.

**`metadata`** — key/value store. Currently holds `schema_version` and `last_lineage_commit` (short hash of the most recent commit the lineage scanner has processed).

## Parser (`src/parser.ts`)

Pure function: `parseMarkdown(content: string): ParsedDoc`.

- Extracts the H1 text as `title` (first `# ` line).
- Extracts `status` from the first line within the first 20 that matches `/^\*\*Status\*\*:\s*(draft|accepted|implemented|superseded|withdrawn)\s*$/i`.
- Extracts `lastStatusChange` (per ADR-0027) by locating the bold marker line matching `/^\*\*Status history\*\*:\s*$/i`, then collecting each consecutive bullet line matching `/^\s*-\s*(\d{4}-\d{2}-\d{2}):/`, and returning the lexicographically maximum date. `null` if the marker is absent, the list empty, or no matched dates.
- Splits the remainder of the document into H2 sections. Everything before the first `## ` (including the H1 and preamble) is not a section. Each H2 section runs to the line before the next `## ` or EOF. Sub-headings (`###`, `####`) stay inside the parent H2 section's content.

`ParsedDoc` shape:

```ts
interface ParsedDoc {
  title: string | null;
  status: AdrStatus | null;
  lastStatusChange: string | null;
  sections: ParsedSection[];
}
```

`classifyCategory(filename)`: `adr-*` → `"adr"`; `spec-*` or `feature-list*` → `"spec"`; anything else → `null`. Operates on the basename; directory path is stripped.

## Content indexer (`src/content-indexer.ts`)

- `indexFile(db, absPath, repoRoot): boolean` — compares file mtime against the stored row's `mtime`; if identical, returns `false` without reparsing. Otherwise reads the file, calls `parseMarkdown`, upserts the `documents` row (writing `path`, `category`, `title`, `status`, `last_status_change`, `mtime`), replaces all rows in `sections` for that doc, and returns `true`. `commit_count` is never written by this function — it is owned by the lineage scanner.
- `indexAllDocs(db, docsDir, repoRoot): string[]` — walks every `.md` file under `docsDir`, calls `indexFile` on each, and returns the repo-root-relative POSIX paths of files where `indexFile` returned `true`. After the walk, deletes `documents` rows whose `path` is no longer present on disk (cascade drops their sections).
- `removeFile(db, absPath, repoRoot)` — deletes the `documents` row for a file that has been removed from disk.

## Watcher (`src/watcher.ts`)

Signature: `startWatcher(db, docsDir, repoRoot, onReindex?: (changed: string[]) => void): () => void`.

Uses `fs.watch` on `docsDir` (recursive). Events are debounced 100 ms per change, then grouped into a single sweep. If `fs.watch` throws (unsupported filesystem), falls back to a 5 s polling loop. On each sweep:

- If the event carries a specific `.md` filename: call `indexFile` on that path. If `indexFile` returned `true`, collect the doc_path.
- Otherwise (no filename, or non-`.md`): call `indexAllDocs(db, docsDir, repoRoot)` and take its `string[]` return value as the set of reindexed paths.
- Dedupe the collected paths; if non-empty, invoke `onReindex(changed)` once per sweep.
- Returns a stop function that tears down the watcher.

The watcher also polls `getHeadCommit(repoRoot)` and keeps a local `lastHead` variable. On each HEAD advance (`currentHead !== lastHead`), before updating `lastHead`, it calls `invalidateLineLineageCacheOnHeadAdvance(db, repoRoot, lastHead, currentHead)` from `line-lineage.ts` (ADR-0032). Failures from the invalidation helper are caught and logged; the watcher continues. It also triggers the existing `runLineageScan` on HEAD advance (ADR-0015).

The MCP entrypoint does not pass an `onReindex` callback — the MCP server is a pure consumer of the index. The dashboard (`mclaude-docs-dashboard`) passes a callback that feeds its SSE broker.

## Lineage scanner (`src/lineage-scanner.ts`)

Runs on every boot of the MCP server, and again via the watcher whenever HEAD advances (per ADR-0015 § "Lineage reindex trigger"). Iterates every commit in the repo whose diff touches `docs/*.md`, starting from `metadata.last_lineage_commit + 1` if present, else from the repo's first commit. For each commit:

1. Compute the list of modified `.md` files under `docs/`.
2. For each modified file, increment `documents.commit_count` by 1 (per ADR-0027 — this runs **before** the `modifiedFiles.length < 2` check so solo commits are counted for volatility).
3. If `modifiedFiles.length < 2`, return (no pairs to emit).
4. Otherwise, parse each file at this commit's SHA, expand each file to its list of H2 sections modified in the diff (via line-range intersection), and for every ordered pair (section_a, section_b) across distinct files upsert a `lineage` row: `INSERT … ON CONFLICT DO UPDATE SET commit_count = commit_count + 1, last_commit = <short hash>`.
5. Update `metadata.last_lineage_commit` to this commit's short hash.

Incremental rescan: subsequent runs start where the previous run left off.

## Line-lineage module (`src/line-lineage.ts`)

Added by ADR-0032. Owns the block-level lineage computation, HTML rendering, and `line_lineage_cache` management.

Exports:

- `getLineLineage(db: Database, repoRoot: string, docPath: string): Promise<LineLineageResponse>` — the hot read path. Resolves HEAD via `getHeadCommit(repoRoot)` (falling back if the watcher's in-memory HEAD is unset), SELECTs from `line_lineage_cache` keyed on `(docPath, head_sha)` and returns the cached response on hit. On miss, runs the compute pipeline below, INSERTs the result, and returns.
- `pruneLineLineageCache(db: Database, repoRoot: string): void` — `DELETE FROM line_lineage_cache WHERE head_sha != :current_head_sha`. Called from the dashboard's `boot.ts` after `runLineageScan` and before `startWatcher`. No-op when `getHeadCommit` returns `null` (non-git context).
- `invalidateLineLineageCacheOnHeadAdvance(db: Database, repoRoot: string, prevHead: string, newHead: string): void` — runs `git diff --name-only <prevHead> <newHead>`. Issues `DELETE FROM line_lineage_cache WHERE doc_path IN (<changed>)` for changed docs and `UPDATE line_lineage_cache SET head_sha = :newHead WHERE head_sha = :prevHead AND doc_path NOT IN (<changed>)` for unchanged docs. Called from `watcher.ts` on every HEAD advance, before the local `lastHead` variable is updated.

Compute pipeline (cache miss):

1. Read the doc's current content via `readRawDoc(repoRoot, docPath)`.
2. Parse with `micromark` + `micromark-extension-gfm-table`; walk events to extract block spans of kind `prose` (one paragraph → one block spanning its soft-wrapped source lines), `list_item` / `table_row` / `code_line` (one source line each). Compute `block_id = sha256("${start_line}:${end_line}:${content.trim()}").slice(0, 16)` for each block.
3. For each block, run `git log -L<start>,<end>:<docPath> -M -w --format='%H|%s|%cs'` once; collect every commit (`sha`, `subject`, `commit_date`) that touched any source line in the block range.
4. Resolve each commit's co-committed doc paths via `git show --name-only --format= <sha>` (cached per-sha for the request's lifetime), filtering to tracked `docs/**/*.md` files. Partition into ADR docs (`docs/adr-*.md`), spec docs (`docs/**/spec-*.md`), and "other."
5. Direction: if the requested doc's category is `spec`, `contributors` are the ADR docs; if `adr`, `contributors` are the spec docs. `non_adr_commits` are the commits that produced no contributor-category match for this direction.
6. Dedupe `contributors` by `doc_path`, compute per-contributor `commit_count` (count of commits contributing) and `last_commit` (MAX commit sha by date), sort by `last_commit` DESC. Sort `non_adr_commits` by `commit_date` DESC.
7. Render the doc's content to HTML by walking the same micromark events: wrap each block in its native element (`<p>`, `<li>`, `<tr>` containing `<td>`s, `<span class="code-line">`) annotated with `data-block-id="<id>"`. Inline markdown (emphasis, links, code spans) rendered in the usual CommonMark way. Blockquotes, thematic breaks, and HTML passthrough render without `data-block-id` (they are not interactive blocks).
   - Headings receive no `data-block-id` but **H2 headings must carry the same `data-lineage-heading` placeholder span emitted by the client-side `marked` renderer** so the existing H2 popover injection in `MarkdownView` keeps working unchanged when the view flips from client-rendered to server-rendered HTML:
     - H1: plain `<h1 id="<slug>"><text></h1>` — no placeholder. The H1 doc-level `≡` affordance (ADR-0031) is rendered by `AdrDetail.tsx` / `SpecDetail.tsx` in the route's JSX header next to `doc.title`, **outside** `MarkdownView`; the body H1 must not emit a second placeholder or the page would show two doc-mode popovers.
     - H2: `<h2 id="<slug>"><text><span data-lineage-heading="<url-encoded-text>"></span></h2>` (attribute value is the URL-encoded heading text, matching `MarkdownView.tsx:62`; signals section-mode popover, ADR-0030).
     - H3 and deeper: plain `<hN id="<slug>"><text></hN>` — no placeholder, no popover.
   - Relative markdown links inside any block are rewritten to in-dashboard hashes (`docs/adr-*.md` → `#/adr/<slug>`, `docs/**/spec-*.md` → `#/spec/<path>`) so the server HTML behaves identically to the client-rendered HTML for navigation.
8. Assemble `{html, blocks}` and write to `line_lineage_cache`.

Response shape:

```ts
interface LineLineageResponse {
  html: string;                           // server-rendered markdown with data-block-id on each block
  blocks: Block[];
}
interface Block {
  block_id: string;                       // 16 hex chars
  start_line: number;                     // 1-based inclusive
  end_line: number;                       // 1-based inclusive
  kind: "prose" | "list_item" | "table_row" | "code_line";
  contributors: {
    doc_path: string;
    status: string | null;
    commit_count: number;
    last_commit: string;
  }[];
  non_adr_commits: {
    sha: string;
    subject: string;                      // truncated to 60 chars
    commit_date: string;                  // ISO YYYY-MM-DD
  }[];
}
```

## MCP tools (`src/tools.ts`)

All five tools take a `Database` handle and a validated args object (Zod schemas in the same module). Returns are plain JS values JSON-serialized by the MCP SDK.

### `search_docs`

Input: `{query: string, category?: "adr"|"spec", status?: AdrStatus, limit?: number}`.

SQL: joins `sections_fts MATCH ?` → `sections` → `documents`, filters by category/status if provided, orders by `sections_fts.rank` (BM25), applies `LIMIT`. Returns `{doc_path, doc_title, category, heading, snippet, line_start, line_end, rank}[]`. `snippet` uses FTS5's `snippet(sections_fts, 1, '[', ']', '...', 32)` — the match is wrapped in `[...]`.

FTS5 query syntax is exposed directly — callers can use phrases (`"..."`), `AND`/`OR`/`NOT`, prefix (`foo*`), and column filters.

### `get_section`

Input: `{doc_path: string, heading: string}`.

Returns `{doc_path, doc_title, category, heading, content, line_start, line_end}`. Throws `"Section not found: <doc_path> / <heading>"` if no row matches.

### `get_lineage`

Input: `{doc_path: string, heading?: string}` (ADR-0031).

**Section mode** — when `heading` is a non-empty string: returns `LineageResult[]` of sections co-committed at least once with the requested section.

**Doc mode** — when `heading` is absent or empty: returns `LineageResult[]` aggregated across every section of the requested doc, grouped by the co-committed document. One row per co-committed doc; `commit_count = SUM(commit_count)` across the underlying section-pair rows, `last_commit = MAX(last_commit)`; `heading = ""` on the returned rows to denote aggregated mode while keeping the schema non-null. Matches the collapse rule used by the dashboard's LineagePopover (ADR-0030), applied server-side so a single call answers "which docs shaped this whole spec."

Per ADR-0027, **no status filter is applied** in either mode — draft, superseded, and withdrawn ADRs all appear. Every row includes the linked doc's `status` so callers can frame historical rows appropriately (`superseded`/`withdrawn` = "tried but not current"; `draft` = "in-progress design thinking"). The tool's MCP description instructs agents to use `status` for this framing.

Ordered by `commit_count DESC` in both modes. `LineageResult` shape:

```ts
interface LineageResult {
  doc_path: string;
  doc_title: string | null;
  category: string | null;
  heading: string;           // "" in doc mode; real heading in section mode
  status: string | null;     // per ADR-0027
  commit_count: number;
  last_commit: string;
}
```

### `get_line_lineage`

Input: `{doc_path: string}` (ADR-0032).

Returns `LineLineageResponse` (shape defined in § "Line-lineage module" above). Direction is inferred from the requested doc's category: for `spec` docs, `contributors[]` contains ADRs; for `adr` docs, `contributors[]` contains specs. `non_adr_commits[]` carries pure-refactor or typo-fix commits that produced no contributor match for this direction, rendered as muted rows in the UI.

The tool delegates to `getLineLineage(db, repoRoot, doc_path)` from `line-lineage.ts`. Cache-hit requests complete in well under a request frame; cache-miss requests take ~1-2s on a typical spec (100 blocks × one `git log -L` each). There is no request timeout in v1; a slow first fetch simply blocks until the cache populates, after which subsequent fetches are instant.

### `list_docs`

Input: `{category?: "adr"|"spec", status?: AdrStatus}`.

Returns `ListDoc[]`:

```ts
interface ListDoc {
  doc_path: string;
  title: string | null;
  category: string | null;
  status: string | null;              // per ADR-0018
  commit_count: number;               // per ADR-0027
  last_status_change: string | null;  // per ADR-0027
  sections: { heading: string; line_start: number; line_end: number }[];
}
```

The `documents` SELECT includes all three new columns. Section arrays are fetched per doc in a second statement (ordered by `line_start`).

## Shared helpers (not MCP tools)

Exported from `src/tools.ts` for workspace consumers (the dashboard, per ADR-0027 § Logic-duplication rule).

### `readRawDoc(repoRoot, docPath): string`

Joins `repoRoot + docPath`, verifies the resolved path remains inside `repoRoot` (rejects `..` escape), verifies the resolved path is inside `<repoRoot>/docs/`, calls `fs.readFileSync(path, "utf8")`, returns the file contents. Throws `NotFoundError` if the file is missing. Not exposed as an MCP tool.

## Package exports

`mclaude-docs-mcp/package.json` declares a subpath-per-module `exports` map so the dashboard (and future workspace consumers) can import individual modules directly:

```json
"exports": {
  "./parser": "./src/parser.ts",
  "./db": "./src/db.ts",
  "./content-indexer": "./src/content-indexer.ts",
  "./lineage-scanner": "./src/lineage-scanner.ts",
  "./line-lineage": "./src/line-lineage.ts",
  "./watcher": "./src/watcher.ts",
  "./tools": "./src/tools.ts"
}
```

TypeScript source is published directly; both publisher and consumer run under Bun.

## Error handling

- Missing or corrupt `.docs-index.db` → deleted and rebuilt on next `openDb`.
- Schema version mismatch → same rebuild path.
- `fs.watch` throws on startup → poll every 5 s instead.
- FTS5 syntax error in `search_docs` query → thrown as `"FTS5 query error: <detail>"`, caller surface TBD (MCP returns the error text to the agent).
- `get_section` miss → throws `"Section not found"`.
- `readRawDoc` path escape or missing file → throws `NotFoundError`.

## Dependencies

- `@modelcontextprotocol/sdk` — MCP server runtime.
- `zod` — input validation.
- `bun:sqlite` — SQLite bindings (WAL, FTS5).
- `micromark` + `micromark-extension-gfm-table` — markdown parsing for line-lineage block detection and HTML rendering (ADR-0032).
- Bun standard library — `fs`, `path`, `crypto` (sha256 for `block_id`), `child_process` (lineage scanner + line-lineage shell out to `git`).
