# Spec: Docs Dashboard

## Role

`mclaude-docs-dashboard` is a local development dashboard for the mclaude `docs/` corpus. It lists every ADR and spec, renders them as formatted markdown, shows each ADR's status (`draft | accepted | implemented | superseded | withdrawn`) at a glance, exposes spec↔ADR lineage both inline (hover popover on H2 headings) and globally (force-directed graph), and live-updates as files change. It is dev-only: loopback bind, no auth, no deploy, read-only.

Established by ADR-0027. A sibling package to `mclaude-docs-mcp`, imported as a Bun workspace dependency. The dashboard reuses the parser, indexer, lineage scanner, watcher, and tool-layer query functions from `mclaude-docs-mcp/src/` — it does not reimplement any of them. Its only new logic is the HTTP/SSE surface, the two doc-level graph SQL queries (no helper exists in docs-mcp for this aggregation), and the React SPA.

## Runtime

- Bun. Native `Bun.serve` for HTTP + SSE. No framework.
- Vite + React 18 for the SPA (built into `ui/dist/`, served as static files).
- Single process. Binds to `127.0.0.1:<port>` (default 4567).
- Entrypoint: `mclaude-docs-dashboard/src/server.ts`. On boot: parses CLI flags (`--port`, `--db-path`), resolves repo root (walk up from cwd to first `.git` directory; error out if none found), opens the shared DB in WAL mode, runs `indexAllDocs` once, starts `startWatcher` with an `onReindex` callback wired to the SSE broker, and begins serving.

## Workspace setup

The repo root holds a Bun workspace `package.json`:

```json
{
  "name": "mclaude-workspace",
  "private": true,
  "workspaces": ["mclaude-docs-mcp", "mclaude-docs-dashboard"]
}
```

The dashboard's `package.json` declares `"mclaude-docs-mcp": "workspace:*"` as a dependency. Imports use the subpaths exported by docs-mcp (see `docs/mclaude-docs-mcp/spec-docs-mcp.md` § Package exports). Example:

```ts
import { parseMarkdown } from "mclaude-docs-mcp/parser";
import { openDb } from "mclaude-docs-mcp/db";
import { startWatcher } from "mclaude-docs-mcp/watcher";
import { searchDocs, getSection, getLineage, listDocs, readRawDoc } from "mclaude-docs-mcp/tools";
```

## CLI flags

| Flag         | Default                                      | Purpose                                         |
|--------------|----------------------------------------------|-------------------------------------------------|
| `--port <n>` | `4567`                                       | HTTP listen port. Fail fast if in use.          |
| `--db-path <p>` | `<repoRoot>/mclaude-docs-mcp/.docs-index.db` | Path to the shared SQLite index.                |

No other flags. Binding is always `127.0.0.1`.

## HTTP API

All endpoints return JSON with `Content-Type: application/json` unless noted. CORS is wide-open (`Access-Control-Allow-Origin: *`) — loopback-only bind means no security concern.

| Method | Path                                                      | Underlying                             | Response                                                                 |
|--------|-----------------------------------------------------------|-----------------------------------------|--------------------------------------------------------------------------|
| GET    | `/api/adrs?status=<s>`                                    | `listDocs({category: "adr", status})`   | `ListDoc[]` (shape from docs-mcp: includes `status`, `commit_count`, `last_status_change`) |
| GET    | `/api/specs`                                              | `listDocs({category: "spec"})`          | `ListDoc[]`                                                              |
| GET    | `/api/doc?path=<p>`                                       | `listDocs` (single-path filter) + `readRawDoc` | `DocResponse` (below) — sections come from the `ListDoc.sections` field; no per-section `getSection` call (that would be N+1). |
| GET    | `/api/lineage?doc=<p>&heading=<h>`                        | `getLineage`                            | `LineageResult[]` (includes `status`)                                    |
| GET    | `/api/search?q=<q>&limit=<n>&category=<c>&status=<s>`     | `searchDocs`                            | `SearchResult[]`                                                         |
| GET    | `/api/graph?focus=<p>`                                    | `graph-queries.ts` (direct SQL)         | `GraphResponse` (below)                                                  |
| GET    | `/events`                                                 | SSE broker                              | `text/event-stream` (see SSE section)                                    |
| GET    | `/` and `/assets/*`                                       | Bun static file serving                 | SPA bundle from `ui/dist/`                                               |

### `DocResponse`

```ts
interface DocResponse {
  doc_path: string;
  title: string | null;
  category: "adr" | "spec" | null;
  status: "draft" | "accepted" | "implemented" | "superseded" | "withdrawn" | null;
  commit_count: number;
  raw_markdown: string;      // full file bytes for client-side rendering via marked
  sections: { heading: string; line_start: number; line_end: number }[];
}
```

`raw_markdown` is sourced by calling `readRawDoc(repoRoot, doc_path)`. Sections come from the `ListDoc.sections` field of a `listDocs` call scoped to the single requested path. No per-section DB query is issued.

### `GraphResponse`

```ts
interface GraphResponse {
  nodes: {
    path: string;
    title: string | null;
    category: "adr" | "spec" | null;
    status: string | null;
    commit_count: number;
  }[];
  edges: {
    from: string;   // doc_path (canonicalized: from < to)
    to: string;
    count: number;  // aggregated commit_count across section-pair edges
  }[];
}
```

### Graph SQL

Implemented in `src/graph-queries.ts` (the one deliberate exception to the Logic-duplication rule — docs-mcp has no doc-level aggregation helper). Both queries canonicalize undirected edges via `MIN(a, b), MAX(a, b)`.

**Global mode** (no `focus` param):

```sql
-- Nodes
SELECT path, title, category, status, commit_count
FROM documents;

-- Edges
SELECT
  MIN(section_a_doc, section_b_doc) AS from_path,
  MAX(section_a_doc, section_b_doc) AS to_path,
  SUM(commit_count)                 AS count
FROM lineage
WHERE section_a_doc != section_b_doc
GROUP BY MIN(section_a_doc, section_b_doc), MAX(section_a_doc, section_b_doc);
```

The handler then applies sidebar filters (ADR↔spec default; optional ADR↔ADR and spec↔spec toggles) in JS by joining each edge against the nodes' categories.

**Local mode** (`?focus=<p>`):

```sql
-- Edges incident to focus
SELECT
  MIN(section_a_doc, section_b_doc) AS from_path,
  MAX(section_a_doc, section_b_doc) AS to_path,
  SUM(commit_count)                 AS count
FROM lineage
WHERE (section_a_doc = :focus OR section_b_doc = :focus)
  AND section_a_doc != section_b_doc
GROUP BY MIN(section_a_doc, section_b_doc), MAX(section_a_doc, section_b_doc);

-- Nodes: focus + neighbors from the edge result set
SELECT path, title, category, status, commit_count
FROM documents
WHERE path IN (:focus, :neighbor1, :neighbor2, ...);
```

The `IN` list is built in JS from the edge result set — 2-query pattern, not N+1. Local mode shows all edge categories (no sidebar filtering).

## SSE

Endpoint: `GET /events`. Two event shapes:

```ts
{ type: "hello" }                         // sent on connect
{ type: "reindex", changed: string[] }    // sent per watcher-sweep with deduped doc_paths
```

### Broker

Client registry is a module-level `Set<Writer>` where `Writer = { write: (chunk: string) => void; close: () => void }`.

**Connect path** (Bun.serve streaming body):

```ts
const encoder = new TextEncoder();
let writer: Writer | null = null;  // hoisted so cancel() closes over it

const stream = new ReadableStream<Uint8Array>({
  start(controller) {
    writer = {
      write: (chunk) => controller.enqueue(encoder.encode(chunk)),
      close: () => { try { controller.close(); } catch {} },
    };
    clients.add(writer);
    writer.write(`data: ${JSON.stringify({ type: "hello" })}\n\n`);
  },
  cancel() {
    if (writer) clients.delete(writer);
  },
});
return new Response(stream, {
  headers: {
    "Content-Type": "text/event-stream",
    "Cache-Control": "no-cache",
    "Connection": "keep-alive",
    "Access-Control-Allow-Origin": "*",
  },
});
```

Declaring `writer` in the enclosing function scope (not inside `start`) is load-bearing: `start` and `cancel` are sibling callbacks on the options object, and a `const writer` inside `start` is invisible to `cancel` (JavaScript scoping rule).

**Broadcast**: the watcher's `onReindex` callback invokes `broker.broadcast({type: "reindex", changed})`:

```ts
function broadcast(event: { type: string; changed?: string[] }): void {
  const payload = `data: ${JSON.stringify(event)}\n\n`;
  for (const writer of clients) {
    try {
      writer.write(payload);
    } catch {
      clients.delete(writer);  // dirty disconnect: stream already closed
    }
  }
}
```

Clean disconnects (browser tab close) fire `ReadableStream.cancel` → `clients.delete(writer)`. Dirty disconnects (network drop) raise on the next `write` and are caught + removed defensively. No heartbeat ping — `EventSource` auto-reconnects every ~5 s, bounding how long a stale entry could linger.

### Event flow end-to-end

1. Developer edits a `.md` file.
2. `fs.watch` event fires → watcher debounces 100 ms → single-file `indexFile` or full-scan `indexAllDocs` runs.
3. Paths where reindex actually happened are collected; watcher calls `onReindex(changed)`.
4. Handler calls `broker.broadcast({type: "reindex", changed})`.
5. Every connected client receives `data: {"type":"reindex","changed":[...]}\n\n`.
6. Browser `EventSource.onmessage` fires → client invalidates affected views → refetches.

## SPA

React 18 + Vite. Hash-based routing (no server-side route coupling; the Bun.serve static handler returns `index.html` for any unmatched path, and the client's hash router picks up the route).

### Routes

| Hash                                    | Component          | Purpose                                                             |
|-----------------------------------------|--------------------|---------------------------------------------------------------------|
| `#/`                                    | `Landing`          | ADRs by status + spec index.                                        |
| `#/adr/<slug>`                          | `AdrDetail`        | Rendered ADR + status history + H2 popovers.                        |
| `#/spec/<path>`                         | `SpecDetail`       | Rendered spec + H2 popovers.                                        |
| `#/search?q=<q>`                        | `SearchResults`    | FTS5 snippets, ranked.                                              |
| `#/graph` (global) or `#/graph?focus=<p>&section=<h>` (local) | `Graph` | Force-directed graph, two modes.                                    |

### Landing page

Two-column layout:

- **Left**: ADRs bucketed by status in the order `Drafts`, `Accepted`, `Implemented`, `Superseded`, `Withdrawn`, `Unspecified`. Drafts expanded by default; other buckets collapsed with counts. Each bucket sorted by `last_status_change` descending (most recent first). Each row shows title + slug + date.
- **Right**: specs grouped by directory — `docs/`, `docs/ui/`, then per-component subfolders (`docs/mclaude-docs-mcp/`, etc.). Each group is a collapsible list of titles.

### Detail views

Rendered via shared `MarkdownView` component using `marked` + `highlight.js`. The renderer extension rewrites relative markdown links:

- `docs/adr-*.md` → `#/adr/<slug>` (slug = filename minus `adr-` prefix and `.md` suffix)
- `docs/**/spec-*.md` → `#/spec/<path>` (path is the full repo-relative path)

So clicks inside the rendered markdown navigate within the dashboard.

Each H2 heading gets a small `≡` icon injected by a marked renderer extension. Hover opens a `LineagePopover`; click pins it (lets the cursor leave without dismissing). Esc or outside-click dismisses.

Popover content:

```
3× docs/spec-state-schema.md §Session KV
2× docs/mclaude-docs-mcp/spec-docs-mcp.md §Schema
Open graph centered here
```

- Rows: `<commit_count>× <doc_path> §<heading>`, sorted by `commit_count` desc. Each row clickable → navigates to `#/spec/<path>` or `#/adr/<slug>` scrolled to the heading.
- Status framing: rows whose linked doc is `superseded` or `withdrawn` render muted; `draft` rows render with a dashed outline.
- Final row: "Open graph centered here" → `#/graph?focus=<doc_path>&section=<heading>`.

Status badge renders next to the H1 title on ADR detail pages: colored pill with status text; the full `Status history` list is already part of the ADR body (markdown) so it renders inline immediately below the title — no hover popover is needed for it.

### Search bar

Top nav on every page. 150 ms debounce; on fire, `GET /api/search?q=<q>`. Results pane at `#/search?q=<q>` shows ranked list with FTS5 snippets (wrapped in `[...]`), doc path, heading; each row click → detail page scrolled to that section.

### Graph

`react-force-graph-2d` (canvas-based, ~150 KB).

**Nodes**:
- Fill color: category (ADR one hue, spec another).
- Border + fill tone for ADRs:
  - `accepted`, `implemented`: solid border, normal fill.
  - `draft`: dashed border.
  - `superseded`: dim/translucent fill.
  - `withdrawn`: grey fill.
- Radius: `∝ sqrt(commit_count)`. Square-root keeps one-commit vs ten-commit differences visible without letting a high outlier dominate.

**Edges**:
- Thickness: `∝ commit_count`.
- Hover tooltip: exact commit count + last-commit short hash.

**Global mode**: every doc as a node; default shows only ADR↔spec edges; sidebar toggles add ADR↔ADR and spec↔spec edges.

**Local mode** (`?focus=<p>`): focus doc centered; every doc with a lineage edge touching it rendered as a neighbor; only edges incident to the focus shown; all edge categories included (no sidebar filter — neighborhood is already small).

Clicking any node → detail page for that doc.

### SSE hook

`useEventSource("/events")` exposes reindex events to React. Route components read the most recent event and, if their own doc path appears in `changed`, re-run their fetch. The exact internal mechanism (ref counter vs. state-driven subscription) is an implementation detail.

## Error handling

| Failure                                                | Behavior                                                                                                   |
|--------------------------------------------------------|------------------------------------------------------------------------------------------------------------|
| `.git` not found walking up from cwd                   | Print error naming the cwd, exit non-zero.                                                                 |
| `.docs-index.db` missing or corrupt                    | `openDb` rebuilds per ADR-0015 contract; dashboard runs `indexAllDocs` during boot (before `Bun.serve` starts accepting connections), so by the time the browser can hit the server the index is ready. No UI overlay needed. |
| Port in use                                            | Fail fast: `Error: port <n> is in use. Use --port <m> or stop the other process.` Do not auto-increment.  |
| `fs.watch` dead                                        | Fall back to 5 s polling inside docs-mcp `startWatcher` (existing behavior). The polling path is transparent to the dashboard — `onReindex` still fires on changes. No UI indicator in v1. |
| `/api/doc` or `/api/lineage` unknown path              | HTTP 404, JSON `{error: "not found", path}`. UI renders inline error in that pane.                         |
| FTS5 syntax error                                      | HTTP 400 with error text. UI displays under search box.                                                    |
| SSE connection dropped                                 | Browser `EventSource` auto-reconnects. Server sends `{type: "hello"}` on reconnect; client triggers full refetch. |
| Markdown parse error                                   | `marked` is permissive; on catastrophic failure, render raw source in a `<pre>` with a one-line warning.   |

## Security

- Bind `127.0.0.1` only. No remote access.
- No authentication. Loopback is the sandbox.
- No write endpoints — browser cannot mutate filesystem or DB state.
- CORS wide-open (`*`) — acceptable because bind is loopback.

## Portability

No mclaude-specific branding or component names in the dashboard's UI, CLI, or server source. Repo root via walk-up-to-`.git`; DB path via `--db-path`. The `--db-path` default is `<repoRoot>/mclaude-docs-mcp/.docs-index.db` because that is the current location of the docs index (docs-mcp is the sibling package that owns the schema); when ADR-0026 plugin-wraps the dashboard, the plugin's bootstrap supplies `--db-path` pointing at the plugin's own index location, and no dashboard source changes. The `mclaude-docs-mcp` import identifier is a workspace-package name — it is not a filesystem assumption about the host repo.

## Package layout

```
mclaude-docs-dashboard/
├── package.json          # workspace dep on mclaude-docs-mcp; also marked, highlight.js
├── tsconfig.json
├── src/
│   ├── server.ts         # Bun.serve entry, CLI flag parsing, SSE broker
│   ├── routes.ts         # HTTP handlers (thin wrappers over docs-mcp functions)
│   ├── boot.ts           # repoRoot walk-up, DB open, initial index, watcher start
│   └── graph-queries.ts  # SQL for /api/graph (nodes + edges)
└── ui/
    ├── index.html
    ├── vite.config.ts
    ├── src/
    │   ├── main.tsx
    │   ├── App.tsx                       # hash router + SSE subscription
    │   ├── routes/
    │   │   ├── Landing.tsx
    │   │   ├── AdrDetail.tsx
    │   │   ├── SpecDetail.tsx
    │   │   ├── SearchResults.tsx
    │   │   └── Graph.tsx
    │   ├── components/
    │   │   ├── MarkdownView.tsx          # marked + highlight.js + link rewriting
    │   │   ├── LineagePopover.tsx
    │   │   ├── StatusBadge.tsx
    │   │   └── SearchBar.tsx
    │   └── api.ts                        # typed fetch wrappers for /api/*
    └── dist/                             # Vite build output, served by Bun
```

## Dependencies

- `mclaude-docs-mcp` (workspace) — shared parser, indexer, scanner, watcher, tools, `readRawDoc`.
- `marked` — markdown rendering.
- `highlight.js` — code block syntax highlighting.
- `react`, `react-dom` — SPA runtime.
- `react-force-graph-2d` — graph rendering.
- `vite` (dev) — SPA bundler.
- Bun standard library — `Bun.serve`, `fs`, `path`.
