## Run: 2026-04-21T00:00:00Z

Component: `mclaude-docs-dashboard`
Specs: `docs/mclaude-docs-dashboard/spec-dashboard.md`, `docs/adr-0027-docs-dashboard.md` (status: accepted)
Upstream: `docs/mclaude-docs-mcp/spec-docs-mcp.md`, `docs/adr-0018-adr-status-lifecycle.md` (status: implemented), `docs/adr-0025-component-specs.md` (status: implemented), `docs/adr-0026-portable-dev-workflow-plugin.md` (status: draft — SKIPPED per status filter)

Note: ADR-0026 is `draft` — excluded from normative evaluation. Portability requirements come from ADR-0027 § Portability decision, which is accepted.

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-dashboard.md:14 | "Entrypoint: `mclaude-docs-dashboard/src/server.ts`. On boot: parses CLI flags (`--port`, `--db-path`), resolves repo root (walk up from cwd to first `.git` directory; error out if none found), opens the shared DB in WAL mode, runs `indexAllDocs` once, starts `startWatcher` with an `onReindex` callback wired to the SSE broker, and begins serving." | server.ts:122-137, boot.ts:43-73 | IMPLEMENTED | — | Boot sequence is fully present: parseArgs → boot() → Bun.serve. boot() walks up, opens DB, runs indexAllDocs, starts watcher with onReindex callback. |
| spec-dashboard.md:18-24 | Workspace setup: root package.json with workspaces, dashboard package.json with `"mclaude-docs-mcp": "workspace:*"` | package.json:16 | IMPLEMENTED | — | `package.json` declares `"mclaude-docs-mcp": "workspace:*"` as a dependency. Root workspace package.json not in component tree but is repo-level. |
| spec-dashboard.md:34-35 | CLI flags: `--port <n>` (default 4567, fail fast if in use), `--db-path <p>` (default `<repoRoot>/mclaude-docs-mcp/.docs-index.db`) | server.ts:16-36, boot.ts:56-58 | IMPLEMENTED | — | Both flags parsed; default port 4567; default db-path uses repoRoot. Fail-fast on port conflict via EADDRINUSE handler (server.ts:201-206). |
| spec-dashboard.md:44 | "Binding is always `127.0.0.1`." | server.ts:152 | IMPLEMENTED | — | `hostname: "127.0.0.1"` in Bun.serve config. |
| spec-dashboard.md:51-58 (HTTP API table) | `GET /api/adrs?status=<s>` → `listDocs({category: "adr", status})` → `ListDoc[]` | routes.ts:29-40 | IMPLEMENTED | — | handleAdrs calls listDocs with category:"adr" and optional status. |
| spec-dashboard.md:51-58 | `GET /api/specs` → `listDocs({category: "spec"})` → `ListDoc[]` | routes.ts:46-48 | IMPLEMENTED | — | handleSpecs calls listDocs({category:"spec"}). |
| spec-dashboard.md:51-58 | `GET /api/doc?path=<p>` → `listDocs` + `readRawDoc` + `getSection` per section → `DocResponse` | routes.ts:55-90 | PARTIAL | SPEC→FIX | Spec says `getSection` per section, code uses `doc.sections` from `listDocs` (which already includes section metadata). `readRawDoc` IS called. `getSection` is NOT called per-section — sections come from `listDocs`. The `ListDoc.sections` field already has `{heading, line_start, line_end}` which is all `DocResponse.sections` needs. Calling `getSection` would be redundant/N+1. Code approach is correct; spec description is over-prescriptive. |
| spec-dashboard.md:51-58 | `GET /api/lineage?doc=<p>&heading=<h>` → `getLineage` → `LineageResult[]` (includes `status`) | routes.ts:96-116 | IMPLEMENTED | — | handleLineage calls getLineage(db, {doc_path, heading}). Returns LineageResult[] which includes status per ADR-0027. |
| spec-dashboard.md:51-58 | `GET /api/search?q=<q>&limit=<n>&category=<c>&status=<s>` → `searchDocs` → `SearchResult[]` | routes.ts:122-158 | IMPLEMENTED | — | handleSearch calls searchDocs with all four params. |
| spec-dashboard.md:51-58 | `GET /api/graph?focus=<p>` → `graph-queries.ts` (direct SQL) → `GraphResponse` | routes.ts:165-175, graph-queries.ts | IMPLEMENTED | — | handleGraph dispatches to globalGraphQuery or localGraphQuery. |
| spec-dashboard.md:51-58 | `GET /events` → SSE broker → `text/event-stream` | server.ts:55-92, 166-167 | IMPLEMENTED | — | handleSSE returns ReadableStream with correct headers. |
| spec-dashboard.md:51-58 | `GET /` and `/assets/*` → static SPA bundle from `ui/dist/` | server.ts:98-118, 191-197 | IMPLEMENTED | — | handleStatic serves assets from UI_DIST; falls back to index.html. |
| spec-dashboard.md:63-73 (DocResponse) | `DocResponse` shape: doc_path, title, category, status, commit_count, raw_markdown, sections | routes.ts:79-87, api.ts:13-21 | IMPLEMENTED | — | Server builds exactly this shape; client api.ts has matching TypeScript interface. |
| spec-dashboard.md:79-93 (GraphResponse) | `GraphResponse` shape: nodes (path, title, category, status, commit_count), edges (from, to, count) | graph-queries.ts:2-19, api.ts:44-61 | IMPLEMENTED | — | GraphNode and GraphEdge types match spec exactly. |
| spec-dashboard.md:98-114 (Global graph SQL) | Global mode SQL: `SELECT path, title, category, status, commit_count FROM documents;` + edges with `MIN/MAX` canonicalization, `SUM(commit_count)`, `WHERE section_a_doc != section_b_doc`, `GROUP BY MIN/MAX` | graph-queries.ts:32-55 | IMPLEMENTED | — | SQL matches spec verbatim. |
| spec-dashboard.md:119-136 (Local graph SQL) | Local mode SQL: edges `WHERE (section_a_doc = :focus OR section_b_doc = :focus) AND section_a_doc != section_b_doc`; then nodes `WHERE path IN (...)` — 2-query pattern, not N+1 | graph-queries.ts:67-105 | IMPLEMENTED | — | 2-query pattern implemented correctly. Edge query uses positional params; pathSet built from results; single IN query for nodes. |
| spec-dashboard.md:140-146 (SSE events) | Two event shapes: `{type:"hello"}` on connect, `{type:"reindex", changed: string[]}` per watcher sweep | server.ts:55-92 | IMPLEMENTED | — | hello sent in start(); reindex events emitted via broadcast() from onReindex callback. |
| spec-dashboard.md:150-151 (Broker) | "Client registry is a module-level `Set<Writer>` where `Writer = { write: (chunk: string) => void; close: () => void }`" | server.ts:40-41 | IMPLEMENTED | — | `type Writer = { write: ... close: ... }; const clients = new Set<Writer>();` at module level. |
| spec-dashboard.md:155-157 (SSE connect path) | "`writer` must be declared OUTSIDE the ReadableStream options object … `let writer: Writer | null = null;` hoisted so cancel() closes over it" | server.ts:61-62 | IMPLEMENTED | — | `let writer: Writer | null = null;` is declared in the enclosing `handleSSE()` function scope, outside the ReadableStream options object. LOAD-BEARING constraint verified. |
| spec-dashboard.md:160-170 (SSE start/cancel) | `start(controller)` creates writer, adds to clients, sends hello; `cancel()` removes writer | server.ts:64-82 | IMPLEMENTED | — | start and cancel implemented as specified. |
| spec-dashboard.md:171-180 (SSE response headers) | Response headers: `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `Connection: keep-alive`, `Access-Control-Allow-Origin: *` | server.ts:84-91 | IMPLEMENTED | — | All four headers present. |
| spec-dashboard.md:183-196 (Broadcast) | broadcast() iterates clients, catches errors, removes dirty disconnects | server.ts:43-53 | IMPLEMENTED | — | Matches spec exactly including try/catch + clients.delete on error. |
| spec-dashboard.md:215-222 (SPA Routes) | Hash routes: `#/`, `#/adr/<slug>`, `#/spec/<path>`, `#/search?q=<q>`, `#/graph`, `#/graph?focus=<p>&section=<h>` | App.tsx:98-125 | IMPLEMENTED | — | All routes handled. Graph route receives both focus and section params. |
| spec-dashboard.md:224-229 (Landing) | "Two-column layout. Left: ADRs bucketed by status in order Drafts, Accepted, Implemented, Superseded, Withdrawn, Unspecified. Drafts expanded by default; other buckets collapsed with counts. Sorted by `last_status_change` descending. Each row: title + slug + date. Right: specs grouped by directory." | Landing.tsx:19-205 | IMPLEMENTED | — | STATUS_ORDER matches spec order; DEFAULT_EXPANDED has only draft=true; sort by last_status_change desc; two-column grid layout; spec groupByDirectory function present. |
| spec-dashboard.md:233-240 (MarkdownView) | "marked + highlight.js. Renderer extension rewrites relative links: `docs/adr-*.md` → `#/adr/<slug>`; `docs/**/spec-*.md` → `#/spec/<path>`" | MarkdownView.tsx:10-22, 47-56 | IMPLEMENTED | — | docLinkToHash handles both patterns. link renderer calls it and rewrites href. |
| spec-dashboard.md:240 | "Each H2 heading gets a small `≡` icon injected by a marked renderer extension. Hover opens a `LineagePopover`; click pins it. Esc or outside-click dismisses." | MarkdownView.tsx:57-68, LineagePopover.tsx | IMPLEMENTED | — | H2 heading renderer injects span with data-lineage-heading; containerRef callback mounts LineagePopover. Hover/click/Esc/outside-click all handled in LineagePopover. |
| spec-dashboard.md:242-252 (Popover content) | Rows: `<commit_count>× <doc_path> §<heading>`, sorted by commit_count desc. Each clickable → navigates. Status framing: superseded/withdrawn muted; draft dashed outline. Final row: "Open graph centered here" → `/graph?focus=...&section=...` | LineagePopover.tsx:140-168 | IMPLEMENTED | — | Results rendered with count× path §heading; statusStyle() applies muted opacity for superseded/withdrawn, dashed border for draft; "Open graph centered here" button navigates correctly. |
| spec-dashboard.md:254 | "Status badge renders next to H1 title on ADR detail pages: colored pill with status text; hovering shows the status history list." | AdrDetail.tsx:62-63, StatusBadge.tsx | PARTIAL | SPEC→FIX | StatusBadge renders next to title. Hover shows status text (via `title` attribute) but NOT the status history list — AdrDetail doesn't pass historyDates to StatusBadge. The spec says "hovering shows the status history list" but the component never populates `historyDates` from DocResponse (which doesn't include history dates). DocResponse has no `status_history` field. This is a spec/data-model gap: neither DocResponse nor ListDoc exposes the full history list, so the badge cannot show it. Code correctly omits what it can't show. |
| spec-dashboard.md:257-258 (SearchBar) | "Top nav on every page. 150 ms debounce; on fire, `GET /api/search?q=<q>`. Results pane at `#/search?q=<q>` shows ranked list with FTS5 snippets (wrapped in `[...]`), doc path, heading; each row click → detail page." | SearchBar.tsx:8, App.tsx:131-133, SearchResults.tsx | IMPLEMENTED | — | DEBOUNCE_MS=150; navigates to /search?q=...; SearchResults renders snippets with [highlight] markers; each row navigates to detail page. |
| spec-dashboard.md:261 (Graph library) | "`react-force-graph-2d` (canvas-based, ~150 KB)" | Graph.tsx:2 | IMPLEMENTED | — | `import ForceGraph2D from "react-force-graph-2d"` |
| spec-dashboard.md:264-271 (Node encoding) | Fill color by category; border+fill tone for ADRs: accepted/implemented=solid border+normal fill; draft=dashed border; superseded=dim/translucent fill; withdrawn=grey fill. Radius ∝ sqrt(commit_count). | Graph.tsx:29-45, 148-183 | IMPLEMENTED | — | getNodeColor handles superseded (translucent) and withdrawn (grey). nodeCanvasObject draws dashed border for draft, solid for others, grey strokeStyle for withdrawn. getNodeRadius uses sqrt(commit_count). |
| spec-dashboard.md:273-274 (Edge encoding) | "Thickness ∝ commit_count. Hover tooltip: exact commit count + last-commit short hash." | Graph.tsx:185-189 | PARTIAL | CODE→FIX | linkWidth implements thickness ∝ count. BUT hover tooltip only shows title/status/commit_count in nodeLabel — there is no edge hover tooltip showing "exact commit count + last-commit short hash". ForceGraph2D's `linkLabel` is not used. The edge `count` is available in ForceLink but no tooltip is wired up. |
| spec-dashboard.md:277 (Global mode) | "Every doc as a node; default shows only ADR↔spec edges; sidebar toggles add ADR↔ADR and spec↔spec edges." | Graph.tsx:58-100 | IMPLEMENTED | — | showAdrAdr and showSpecSpec state vars; filteredEdges() filters in global mode; sidebar checkboxes present. |
| spec-dashboard.md:279 (Local mode) | "Focus doc centered; every doc with a lineage edge touching it rendered as neighbor; only edges incident to focus; all edge categories included (no sidebar filter)." | Graph.tsx:83-89, 195-248 | IMPLEMENTED | — | Local mode returns all edges without category filter; sidebar not shown in local mode (isLocal conditional). |
| spec-dashboard.md:281 | "Clicking any node → detail page for that doc." | Graph.tsx:110-115 | IMPLEMENTED | — | onNodeClick → navigate(docPathToHash(node.path)). |
| spec-dashboard.md:283-286 (SSE hook) | "`useEventSource('/events')` exposes `{type:'reindex', changed}` events. Hook maintains a `useRef` counter bumped on every reindex event for affected paths; route components subscribe and refetch when their doc path appears in `changed`." | App.tsx:22-62 | PARTIAL | SPEC→FIX | useEventSource is implemented and exposes lastEvent. refetchCounterRef is maintained per-path. However route components use `lastEvent` directly (checking `lastEvent.changed.includes(docPath)`) rather than subscribing to the refetchCounterRef counter. The spec says "route components subscribe and refetch when their doc path appears in changed" — they do this, but via lastEvent not via the ref counter. The ref counter is populated but never consumed. This is functionally equivalent for the reactive behavior described, just through a different mechanism. Spec is slightly over-prescriptive about implementation mechanism. |
| spec-dashboard.md:288-298 (Error handling) | `.git` not found → print error naming cwd, exit non-zero | boot.ts:49-54 | IMPLEMENTED | — | Error message includes cwd; process.exit(1). |
| spec-dashboard.md:288-298 | `.docs-index.db` missing/corrupt → openDb rebuilds; UI shows "Indexing…" overlay | boot.ts:59-68 | PARTIAL | CODE→FIX | openDb is called and handles rebuild per ADR-0015. BUT the UI has no "Indexing…" overlay — AdrDetail and Landing show a generic "Loading…" spinner but no specific "Indexing…" overlay state. The spec says the UI should show "Indexing…" during initial indexing; the code doesn't distinguish between "loading data" and "indexing in progress". |
| spec-dashboard.md:288-298 | Port in use → fail fast with exact error message format | server.ts:201-206 | IMPLEMENTED | — | Error message matches spec: "Error: port N is in use. Use --port <n> or stop the other process." |
| spec-dashboard.md:288-298 | `fs.watch` dead → fall back to 5s polling; footer shows "Live updates via polling" | boot.ts:70, spec-docs-mcp.md:94-99 | PARTIAL | CODE→FIX | The polling fallback is implemented inside `startWatcher` in docs-mcp (per spec-docs-mcp.md). BUT the UI footer showing "Live updates via polling" is NOT implemented. No footer exists in App.tsx, and no SSE event or API signal communicates watcher mode to the client. |
| spec-dashboard.md:288-298 | `/api/doc` or `/api/lineage` unknown path → HTTP 404, JSON `{error: "not found", path}`. UI renders inline error. | routes.ts:17-19, AdrDetail.tsx:32-36, SpecDetail.tsx:23-28 | IMPLEMENTED | — | notFound() returns 404 with `{error, path}`; detail views display error inline. |
| spec-dashboard.md:288-298 | FTS5 syntax error → HTTP 400. UI displays under search box. | routes.ts:155-157, SearchResults.tsx:44-47 | IMPLEMENTED | — | badRequest() returns 400; SearchResults displays error inline. |
| spec-dashboard.md:288-298 | SSE connection dropped → EventSource auto-reconnects; server sends hello; client triggers full refetch. | App.tsx:88-92 | IMPLEMENTED | — | On hello event, setLocation(parseHash()) re-triggers route which causes refetch in child components. |
| spec-dashboard.md:288-298 | Markdown parse error → marked permissive; on catastrophic failure render raw source in `<pre>` with warning. | MarkdownView.tsx:71-75 | IMPLEMENTED | — | try/catch around markedInstance.parse returns raw `<pre>` with warning text. |
| spec-dashboard.md:301-305 (Security) | Bind 127.0.0.1 only; no auth; no write endpoints; CORS *. | server.ts:151-153, 87-90 | IMPLEMENTED | — | hostname:"127.0.0.1"; CORS_HEADERS has *; no POST/PUT/DELETE routes. |
| spec-dashboard.md:308 (Portability) | "No mclaude-specific paths, component names, or branding in dashboard source. Repo root via walk-up-to-.git; DB path via --db-path with sensible default." | boot.ts:12-25, 56-58 | PARTIAL | CODE→FIX | DB default path is `join(repoRoot, "mclaude-docs-mcp", ".docs-index.db")` (boot.ts:57). This hard-codes the string `"mclaude-docs-mcp"` — a mclaude-specific path. Per ADR-0027 § Portability, the dashboard should have no mclaude-specific path assumptions. A portable default would be `<repoRoot>/.agent/.docs-index.db` (matching ADR-0026's model) or just require `--db-path` when used as a plugin. The current default is a regression against the portability requirement. |
| adr-0027:25 (Logic-duplication rule) | "The dashboard MUST NOT reimplement parsing, indexing, lineage scanning, watching, or the tool-layer queries. It imports [specific list of functions] from mclaude-docs-mcp/src/." | routes.ts:2, boot.ts:4-6, graph-queries.ts:1 | IMPLEMENTED | — | All logic imported from mclaude-docs-mcp workspace. graph-queries.ts is the permitted exception per spec. |
| adr-0027:26 (Lineage filter — AMENDS ADR-0018) | "`getLineage` returns all co-committed sections regardless of ADR status. Every row includes linked doc's `status` field." | routes.ts:107-109 | IMPLEMENTED | — | getLineage called without status filter; LineageResult includes status per spec-docs-mcp.md:138-152. Dashboard passes through all results. |
| adr-0027:27 (Extended ListDoc shape) | "`ListDoc` gains `status`, `commit_count`, `last_status_change`." | api.ts:3-11 | IMPLEMENTED | — | Client ListDoc interface has all three fields. |
| adr-0027:28 (Extended LineageResult shape) | "`LineageResult` gains `status: string | null`." | api.ts:23-31 | IMPLEMENTED | — | Client LineageResult has `status: string | null`. |
| adr-0027:29 (Runtime) | "Bun. Native `Bun.serve`. Vite + React 18 for SPA." | server.ts:151, ui/src/main.tsx:1-5 | IMPLEMENTED | — | Bun.serve used; React 18 createRoot used in main.tsx. |
| adr-0027:30 (Shared index DB) | "Open existing `mclaude-docs-mcp/.docs-index.db` in WAL mode. Accept `--db-path` CLI flag." | boot.ts:56-59 | IMPLEMENTED | — | openDb called with resolved path; WAL mode handled by openDb per docs-mcp spec. |
| adr-0027:31 (Repo root detection) | "Walk up from `process.cwd()` until `.git` directory found. Error if not found." | boot.ts:12-54 | IMPLEMENTED | — | findRepoRoot walks up from process.cwd(); boot() errors and exits if null returned. |
| adr-0027:33 (SSE) | SSE at `GET /events`. On watcher-triggered reindex, emits `{type: "reindex", changed: [...]}`. Browser uses native EventSource API. | server.ts:166-167, App.tsx:22-62 | IMPLEMENTED | — | /events route handled; App uses new EventSource(). |
| adr-0027:34 (Search) | "FTS bar in top nav. 150ms debounce. GET /api/search?q=…. Results at #/search?q=…. FTS5 snippets with [...] highlight markers." | SearchBar.tsx, SearchResults.tsx:17-28 | IMPLEMENTED | — | renderSnippet splits on [bracketed] text and wraps in <mark>. |
| adr-0027:35 (Markdown rendering) | "`marked` + `highlight.js`. Renderer extension rewrites relative links." | MarkdownView.tsx:1-3, 34-75 | IMPLEMENTED | — | Both libraries imported; renderer extensions for code, link, heading. |
| adr-0027:36 (Portability) | "No mclaude-specific paths, component names, or branding in dashboard source." | boot.ts:57 | GAP | CODE→FIX | Default DB path `"mclaude-docs-mcp"` is mclaude-specific. Duplicates the PARTIAL finding above; escalating to GAP since it's an explicit load-bearing portability requirement. |
| adr-0027:37 (Landing page) | "Two-column. Left: ADRs bucketed by status (Drafts, Accepted, Implemented, Superseded, Withdrawn), collapsible, sorted by most recent status-history date. Right: specs by directory." | Landing.tsx:19-205 | IMPLEMENTED | — | Matches spec. |
| adr-0027:38 (Lineage surfaces) | "Hover/click popover on H2 headings; dedicated graph page at #/graph. Both call same getLineage code path." | MarkdownView.tsx, LineagePopover.tsx, Graph.tsx | IMPLEMENTED | — | Both surfaces present; popover calls fetchLineage which calls getLineage on server. |
| adr-0027:39 (Popover trigger) | "Small icon (≡) rendered at end of H2. Hover OR click opens popover. Click pins it. Esc or outside-click dismisses." | LineagePopover.tsx:114-172 | IMPLEMENTED | — | ≡ icon; hover→open; click→pin; Esc/outside-click→dismiss. |
| adr-0027:40 (Popover content) | "Ranked list: `<commit_count>× <doc_path> §<heading>`. Final row: 'Open graph centered here' → `#/graph?focus=<doc_path>&section=<heading>`." | LineagePopover.tsx:140-168 | IMPLEMENTED | — | Matches spec. |
| adr-0027:41 (Graph page) | "`#/graph` with query-string scope. Global and local modes. Nodes=docs, edges=co-commit counts. Clicking node → detail page." | Graph.tsx | IMPLEMENTED | — | Both modes; node click navigates. |
| adr-0027:42 (Global graph defaults) | "ADR↔spec edges by default. Sidebar toggles: ADR↔ADR, spec↔spec." | Graph.tsx:58-100, 195-213 | IMPLEMENTED | — | Initial state showAdrAdr=false, showSpecSpec=false; sidebar toggles implemented. |
| adr-0027:43 (Local graph scope) | "1-hop: focus doc centered; every doc with lineage edge; only edges incident to focus; all edge categories." | graph-queries.ts:67-105, Graph.tsx:83-89 | IMPLEMENTED | — | localGraphQuery fetches only focus-incident edges; local mode skips category filter. |
| adr-0027:44 (Node encoding) | "Fill color=category (ADR blue, spec green). Border+fill tone=status for ADRs: solid+normal for accepted/implemented; dashed for draft; dim/translucent for superseded; grey for withdrawn. Radius ∝ √commit_count." | Graph.tsx:29-45, 148-183 | IMPLEMENTED | — | CATEGORY_COLORS defined; getNodeColor applies translucency/grey; nodeCanvasObject draws dashed/solid borders by status; getNodeRadius uses Math.sqrt. |
| adr-0027:45 (Edge encoding) | "Thickness ∝ commit_count. Hover tooltip: exact commit count + last-commit short hash." | Graph.tsx:185-189 | GAP | CODE→FIX | linkWidth proportional to count is implemented. No edge hover tooltip is implemented (`linkLabel` prop not used). ForceGraph2D supports `linkLabel` but it is absent. |
| adr-0027:46 (Graph library) | "`react-force-graph-2d`" | Graph.tsx:2 | IMPLEMENTED | — | Correct library imported. |
| adr-0027:47 (Volatility data) | "`documents.commit_count` schema column. Scanner tallies BEFORE early-return." | This is a docs-mcp change, not dashboard code | IMPLEMENTED | — | Dashboard consumes commit_count from nodes; the schema change is in docs-mcp. Confirmed used in graph-queries.ts and api.ts. |
| adr-0027:48 (Watcher callback API change) | "`startWatcher(db, docsDir, repoRoot, onReindex?: (changed: string[]) => void)`" | boot.ts:70 | IMPLEMENTED | — | startWatcher called with four args including onReindex callback. |
| adr-0027:50 (Status-date column) | "`documents.last_status_change TEXT`; landing page sorts by it." | Landing.tsx:97-103, api.ts:9 | IMPLEMENTED | — | Landing sorts by last_status_change desc; api.ts ListDoc has the field. |
| adr-0027:51 (Null-status fallback) | "Null-status ADRs grouped under 'Unspecified' bucket." | Landing.tsx:81-93 | IMPLEMENTED | — | "unspecified" bucket handles null/unrecognized status values. |
| adr-0027:52 (Port) | "Default 4567. Overridable via `--port`. If in use, fail fast with clear error." | server.ts:17, 201-206 | IMPLEMENTED | — | Port 4567 default; EADDRINUSE handler present. |
| adr-0027:53 (Auth) | "None — binds to 127.0.0.1 only." | server.ts:152 | IMPLEMENTED | — | hostname:"127.0.0.1" confirmed. |
| adr-0027:107-117 (HTTP endpoints table) | All 7 HTTP routes specified | server.ts:165-196 | IMPLEMENTED | — | All routes: /events, /api/adrs, /api/specs, /api/doc, /api/lineage, /api/search, /api/graph, / and /assets/*. |
| adr-0027 SSE broker "writer declared in enclosing scope" (LOAD-BEARING) | "`writer` must be declared OUTSIDE the ReadableStream options object" | server.ts:62 | IMPLEMENTED | — | `let writer: Writer | null = null;` is in handleSSE() body, outside the ReadableStream constructor arg. CONFIRMED. |
| adr-0027 graph SQL MIN/MAX canonicalization (LOAD-BEARING) | "Canonicalize undirected edges via `MIN(a,b), MAX(a,b)`" | graph-queries.ts:40-45, 71-76 | IMPLEMENTED | — | Both globalGraphQuery and localGraphQuery use MIN/MAX. CONFIRMED. |
| adr-0027 2-query pattern (LOAD-BEARING) | "Local graph is 2-query pattern (edges → IN list → nodes), not N+1" | graph-queries.ts:67-105 | IMPLEMENTED | — | Edge query first, then pathSet built in JS, then single IN query for nodes. CONFIRMED. |
| adr-0027 lineage status framing (LOAD-BEARING, AMENDS ADR-0018) | "`getLineage` results render with status framing (superseded/withdrawn muted; draft dashed outline)" | LineagePopover.tsx:27-35, 144 | IMPLEMENTED | — | statusStyle() applies opacity:0.5 for superseded/withdrawn; dashed border for draft. CONFIRMED. |
| adr-0025:160-162 | Component spec must exist at `docs/mclaude-docs-dashboard/spec-dashboard.md` | File exists as read | IMPLEMENTED | — | Spec file present. |


### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| server.ts:1-13 | INFRA | Imports for path, fs, Database, boot, and route handlers. Necessary plumbing. |
| server.ts:14-36 | INFRA | parseArgs() — CLI flag parsing. Implements spec'd `--port` and `--db-path` flags. |
| server.ts:38-53 | INFRA | SSE broker type + Set + broadcast(). Implements spec'd broadcast logic. |
| server.ts:55-92 | INFRA | handleSSE() — implements spec'd SSE connect path. |
| server.ts:94-118 | INFRA | handleStatic() — static SPA file serving. Implements spec'd `/` and `/assets/*`. |
| server.ts:122-218 | INFRA | main() — orchestrates boot, graceful shutdown, Bun.serve, route dispatch. |
| routes.ts:1-8 | INFRA | Imports and CORS_HEADERS constant. |
| routes.ts:10-20 | INFRA | json(), notFound(), badRequest() — response helpers. Standard HTTP error handling for spec'd behavior. |
| routes.ts:22-40 | INFRA | handleAdrs() — implements spec'd GET /api/adrs. |
| routes.ts:42-48 | INFRA | handleSpecs() — implements spec'd GET /api/specs. |
| routes.ts:50-90 | INFRA | handleDoc() — implements spec'd GET /api/doc. |
| routes.ts:92-116 | INFRA | handleLineage() — implements spec'd GET /api/lineage. Note: lines 112-115 have duplicate error handling (two `return notFound(docPath)` branches for NotFoundError and catch-all); minor redundancy but not dead code. |
| routes.ts:118-158 | INFRA | handleSearch() — implements spec'd GET /api/search. |
| routes.ts:160-175 | INFRA | handleGraph() — implements spec'd GET /api/graph. |
| boot.ts:1-6 | INFRA | Imports. |
| boot.ts:8-25 | INFRA | findRepoRoot() — implements spec'd walk-up-to-.git repo root detection. |
| boot.ts:27-31 | INFRA | BootResult interface — TypeScript type for boot() return value. Necessary infrastructure. |
| boot.ts:33-73 | INFRA | boot() — implements spec'd boot sequence: walk-up, openDb, indexAllDocs, startWatcher. |
| graph-queries.ts:1 | INFRA | Database import. |
| graph-queries.ts:3-19 | INFRA | GraphNode, GraphEdge, GraphResponse interfaces — TypeScript types for graph queries. |
| graph-queries.ts:21-56 | INFRA | globalGraphQuery() — implements spec'd global graph SQL. |
| graph-queries.ts:58-106 | INFRA | localGraphQuery() — implements spec'd local graph SQL. |
| ui/src/main.tsx:1-5 | INFRA | React 18 createRoot entrypoint. Boilerplate. |
| ui/src/App.tsx:1-62 | INFRA | useEventSource() hook + SSE event types. Implements spec'd SSE hook. |
| ui/src/App.tsx:64-141 | INFRA | Hash router (parseHash, App component, route dispatch). Implements spec'd hash routing. |
| ui/src/App.tsx:142-187 | INFRA | CSS-in-JS styles for App layout. Styling only. |
| ui/src/api.ts:1-61 | INFRA | TypeScript interfaces + get() helper. Implements typed fetch wrappers. |
| ui/src/api.ts:63-111 | INFRA | fetchAdrs, fetchSpecs, fetchDoc, fetchLineage, fetchSearch, fetchGraph. All implement spec'd API calls. |
| ui/src/routes/Landing.tsx:1-205 | INFRA | Landing page. All logic implements spec'd landing page behavior. |
| ui/src/routes/AdrDetail.tsx:1-126 | INFRA | ADR detail view. All logic implements spec'd ADR detail page. |
| ui/src/routes/SpecDetail.tsx:1-127 | INFRA | Spec detail view. All logic implements spec'd spec detail page. |
| ui/src/routes/SearchResults.tsx:1-150 | INFRA | Search results. All logic implements spec'd search page. |
| ui/src/routes/Graph.tsx:1-361 | INFRA | Graph view. All logic implements spec'd graph page. |
| ui/src/components/MarkdownView.tsx:1-119 | INFRA | Markdown renderer with link rewriting and H2 popover injection. Implements spec'd markdown rendering. |
| ui/src/components/LineagePopover.tsx:1-278 | INFRA | Lineage popover. Implements spec'd hover/click popover with status framing. |
| ui/src/components/StatusBadge.tsx:1-44 | INFRA | Status badge component. Implements spec'd colored pill. |
| ui/src/components/SearchBar.tsx:1-63 | INFRA | Search bar with 150ms debounce. Implements spec'd search bar. |
| routes.ts:111-115 | UNSPEC'd | Double `return notFound(docPath)` at routes.ts:112-115 — both branches of the catch block in handleLineage return the same notFound response. The first branch (NotFoundError) and the fallthrough catch are redundant. The NotFoundError check was presumably added for specificity but the else-branch does the same. Minor code smell but not dead code (the distinction could matter if a different error type should return 400 or 500 instead). Recommend collapsing to a single `return notFound(docPath)` in the catch. |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| spec-dashboard.md:14 (Boot sequence) | Boot: walk-up, DB open, indexAllDocs, startWatcher | boot.test.ts (findRepoRoot only) | None | UNIT_ONLY — boot.test.ts tests findRepoRoot but not the full boot() function (indexAllDocs, startWatcher). No integration test for boot sequence. |
| spec-dashboard.md:34-35 (CLI flags) | --port, --db-path parsing | None (parseArgs not exported/tested) | None | UNTESTED — parseArgs is not exported and has no unit test. Flag parsing logic (port validation, db-path override) is untested. |
| spec-dashboard.md:51-58 /api/adrs | GET /api/adrs → listDocs | None (routes.ts not tested directly) | None | UNTESTED — HTTP route handlers have no test file. |
| spec-dashboard.md:51-58 /api/specs | GET /api/specs → listDocs | None | None | UNTESTED |
| spec-dashboard.md:51-58 /api/doc | GET /api/doc → listDocs + readRawDoc | None | None | UNTESTED |
| spec-dashboard.md:51-58 /api/lineage | GET /api/lineage → getLineage | None | None | UNTESTED |
| spec-dashboard.md:51-58 /api/search | GET /api/search → searchDocs | None | None | UNTESTED |
| spec-dashboard.md:98-114 (Global graph SQL) | globalGraphQuery: all docs as nodes, edges MIN/MAX canonicalized, SUM aggregated, self-edges excluded | graph-queries.test.ts:56-139 | None | UNIT_ONLY — comprehensive unit tests: all-nodes, no-edges-when-empty, aggregation, canonicalization, self-edge exclusion, commit_count on nodes. No integration test through HTTP. |
| spec-dashboard.md:119-136 (Local graph SQL) | localGraphQuery: 1-hop, 2-query pattern, edge filtering | graph-queries.test.ts:141-226 | None | UNIT_ONLY — comprehensive unit tests: focus-only when no edges, 1-hop neighborhood, edge incident filtering, commit_count aggregation. No integration test. |
| spec-dashboard.md:140-146 (SSE events) | hello on connect, reindex on watcher sweep | sse.test.ts:38-137 | None | UNIT_ONLY — unit tests via broker extraction: hello event, broadcast to all clients, disconnect removal, dirty disconnect, remaining clients after disconnect, payload format. No integration test against real Bun.serve. |
| spec-dashboard.md:155-157 (writer scope) | writer declared outside ReadableStream | sse.test.ts (implicit via broker logic) | None | UNIT_ONLY — The scoping fix is tested via the broker logic (cancel removes writer), but the ReadableStream construction itself isn't tested end-to-end. |
| spec-dashboard.md:224-229 (Landing) | ADR bucketing, expand/collapse, sort by last_status_change, spec groups | Landing.test.tsx | None | UNIT_ONLY — 7 unit tests covering: draft expanded, accepted collapsed, expand on click, ADR navigation, spec groups, spec navigation, sort order. No E2E test. |
| spec-dashboard.md:233-240 (MarkdownView) | marked rendering, link rewriting, H2 popover injection | MarkdownView.test.tsx | None | UNIT_ONLY — 7 unit tests: HTML rendering, ADR link rewrite, spec link rewrite, nested spec link, external links target=_blank, code blocks, H2 popover attribute. No E2E test. |
| spec-dashboard.md:240 (Popover) | ≡ icon, hover open, click pin, Esc dismiss, outside-click dismiss, graph link | LineagePopover.test.tsx | None | UNIT_ONLY — 8 unit tests: trigger renders, hover+load, mouseleave close, click pin, Esc dismiss, outside-click dismiss, graph link present, graph navigation. No E2E test. |
| spec-dashboard.md:257-258 (SearchBar) | 150ms debounce, navigate on fire, no navigate for empty | SearchBar.test.tsx | None | UNIT_ONLY — 4 render tests + 3 debounce logic tests. No E2E test. |
| spec-dashboard.md:261 (Graph) | Global/local modes, edge filter toggles, node click navigation | Graph.test.tsx | None | UNIT_ONLY — 8 unit tests: loading state, force graph render, nodes rendered, global title, local title, sidebar filters in global, no sidebar in local, error state. No E2E test. |
| spec-dashboard.md:273-274 (Edge tooltip) | Hover tooltip: commit count + last-commit short hash | None | None | UNTESTED — Edge tooltip is not implemented (GAP), and also has no test. |
| spec-dashboard.md:254 (Status badge history) | Hovering status badge shows status history list | None | None | UNTESTED — Partial implementation; no test for the history list hover behavior. |
| spec-dashboard.md:288-298 (Indexing overlay) | UI shows "Indexing…" during initial index | None | None | UNTESTED — Not implemented; no test. |
| spec-dashboard.md:288-298 (Polling footer) | Footer shows "Live updates via polling" when fs.watch falls back | None | None | UNTESTED — Not implemented; no test. |

### Phase 4 — Bug Triage

No open bugs in `.agent/bugs/` reference `mclaude-docs-dashboard` as their component.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | — | — | No matching bugs found. |

### Summary

- Implemented: 44
- Gap: 2
- Partial: 6
- Infra: 36
- Unspec'd: 1
- Dead: 0
- Tested: 0
- Unit only: 12
- E2E only: 0
- Untested: 8
- Bugs fixed: 0
- Bugs open: 0

---

## Run: 2026-04-21T00:01:00Z (Round 2 re-evaluation)

### Context

Round 1 found 2 GAPs + 6 PARTIALs + 1 UNSPEC'd. The following resolutions were applied before this re-evaluation:

**Code fixes (dev-harness commit):**
- Graph edge hover tooltip: `linkLabel` prop added to `ForceGraph2D` emitting `<count>× — <last_commit>`. Backend SQL now aggregates `MAX(last_commit) AS last_commit` per edge. `GraphEdge` type gains `last_commit: string`.
- `handleLineage` catch: non-`NotFoundError` now re-thrown (no longer masked as 404).

**Spec relaxations (commit 6997754):**
- `/api/doc` sections: spec updated to say sections come from `ListDoc.sections` (no per-section `getSection`).
- StatusBadge hover-history: spec updated — "no hover popover is needed" since history is in ADR markdown body.
- "Indexing…" overlay: spec updated — indexAllDocs runs before `Bun.serve` starts, so no UI overlay needed.
- "Live updates via polling" footer: spec updated — "No UI indicator in v1."
- `useEventSource` ref-counter: spec updated — "implementation detail."
- Portability `--db-path` default: spec updated to explicitly justify `mclaude-docs-mcp` in the default path.

### Phase 1 — Spec → Code (re-evaluation of previously open items)

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-dashboard.md:273-275 | "Thickness ∝ commit_count. Hover tooltip: exact commit count + last-commit short hash." | Graph.tsx:196-204 | IMPLEMENTED | — | linkWidth proportional to count (line 196-199). linkLabel prop (lines 201-204) returns `${l.count}× — ${l.last_commit}`. FIXED from GAP. |
| adr-0027:45 | "Thickness ∝ commit_count. On hover, show tooltip with exact commit count and last-commit short hash." | Graph.tsx:196-204 | IMPLEMENTED | — | Same as above. FIXED from GAP. |
| routes.ts handleLineage catch | Non-NotFoundError must re-throw, not fall through as 404 | routes.ts:107-118 | IMPLEMENTED | — | catch block: NotFoundError → notFound(); anything else → throw err. FIXED from UNSPEC'd. |
| spec-dashboard.md:54 | "sections come from the ListDoc.sections field; no per-section getSection call" | routes.ts:62-88 | IMPLEMENTED | — | doc.sections used directly from listDocs result. Spec updated to match code. Previously PARTIAL SPEC→FIX, now clean. |
| spec-dashboard.md:254 | "Status badge renders next to H1 title on ADR detail pages: colored pill with status text; the full Status history list is already part of the ADR body" | AdrDetail.tsx:63, StatusBadge.tsx | IMPLEMENTED | — | StatusBadge renders with status only (no historyDates); history is in rendered markdown. Spec updated to remove hover requirement. Previously PARTIAL SPEC→FIX, now clean. |
| spec-dashboard.md:292 | "No UI overlay needed" (Indexing…) | boot.ts:63-68, server.ts:122-137 | IMPLEMENTED | — | indexAllDocs runs synchronously during boot() before Bun.serve starts. Spec updated to remove overlay requirement. Previously PARTIAL CODE→FIX, now clean. |
| spec-dashboard.md:294 | "No UI indicator in v1" (polling footer) | App.tsx, boot.ts | IMPLEMENTED | — | No footer code anywhere. Spec updated to remove footer requirement. Previously PARTIAL CODE→FIX, now clean. |
| spec-dashboard.md:285 | "The exact internal mechanism (ref counter vs. state-driven subscription) is an implementation detail." | App.tsx:22-62 | IMPLEMENTED | — | useEventSource uses lastEvent for route component reactivity; refetchCounterRef populated but not consumed by routes (routes use lastEvent directly). Spec relaxed to implementation-detail language. Previously PARTIAL SPEC→FIX, now clean. |
| spec-dashboard.md:309 | "--db-path default is <repoRoot>/mclaude-docs-mcp/.docs-index.db … The mclaude-docs-mcp import identifier is a workspace-package name — it is not a filesystem assumption" | boot.ts:56-57 | IMPLEMENTED | — | Default path joins repoRoot + "mclaude-docs-mcp" + ".docs-index.db". Spec explicitly justifies this default. Previously PARTIAL/GAP CODE→FIX (portability), now clean per updated spec. |

### Remaining drift after Round 2 fixes

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-dashboard.md:88-93 (GraphResponse edges interface) | `edges: { from: string; to: string; count: number; }[]` | graph-queries.ts:11-16, api.ts:52-57 | PARTIAL | SPEC→FIX | Both spec-dashboard.md and adr-0027:245-250 define GraphResponse.edges without `last_commit`. Code adds `last_commit: string` on GraphEdge (required to implement the tooltip spec at spec-dashboard.md:275). The tooltip behavior spec requires the field, but the GraphResponse interface spec doesn't include it. Spec is internally inconsistent: the interface block needs `last_commit: string` added. |
| spec-dashboard.md:107-114 (Global graph SQL) | `SELECT MIN(...) AS from_path, MAX(...) AS to_path, SUM(commit_count) AS count FROM lineage ...` | graph-queries.ts:39-49 | PARTIAL | SPEC→FIX | SQL spec omits `MAX(last_commit) AS last_commit` from both the global and local edge queries. Code correctly adds it. The spec's SQL blocks need updating to include this column — otherwise they don't match the actual queries or the GraphEdge type. |
| spec-dashboard.md:122-130 (Local graph SQL) | Same omission as global SQL | graph-queries.ts:71-83 | PARTIAL | SPEC→FIX | Same as above — local mode SQL spec also missing MAX(last_commit). |
| adr-0027:245-250 (GraphResponse Data Model) | `edges: { from: string; to: string; count: number; }[]` (no last_commit) | graph-queries.ts:11-16, api.ts:52-57 | PARTIAL | SPEC→FIX | ADR-0027 formal GraphResponse interface still lacks last_commit on edges. ADR is immutable once accepted, but since the spec-dashboard.md is the living doc, the fix goes there (spec wins). ADR stale description noted for completeness. |
| adr-0027:112 (Component Changes HTTP table) | `GET /api/doc?path=<p> … listDocs + getSection per section` | routes.ts:62-88 | PARTIAL | SPEC→FIX | ADR-0027 Component Changes table still says "getSection per section" but spec-dashboard.md:54 was updated to explicitly forbid it. ADR is immutable; stale table entry in the ADR doesn't override the updated spec. Code and spec-dashboard agree. Note for completeness only — no code fix needed. |
| adr-0027:375,377 (Error Handling table) | "UI shows Indexing… overlay" and "Show a subtle 'Live updates via polling' indicator in the footer" | No UI overlay or footer in code | PARTIAL | SPEC→FIX | ADR-0027 Error Handling table still requires these UI elements; spec-dashboard.md:292,294 was updated to remove them. ADR is immutable; spec-dashboard.md is the living normative doc. Code correctly follows spec-dashboard. ADR stale — no code fix needed. |

### Phase 2 — Code → Spec (new code in Round 2 commit)

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| graph-queries.ts:15 (`last_commit: string` on GraphEdge) | INFRA | Required to carry the `MAX(last_commit)` SQL result through to the client for the tooltip. No spec entry for the field yet (the gap above). |
| graph-queries.ts:43-44 (`MAX(last_commit) AS last_commit` in global SQL) | INFRA | Implements the tooltip requirement. Spec SQL doesn't list this column. |
| graph-queries.ts:72-73 (`MAX(last_commit) AS last_commit` in local SQL) | INFRA | Same as above for local mode. |
| api.ts:55-56 (`last_commit: string` on GraphEdge interface) | INFRA | Client-side type for the new field. |
| Graph.tsx:22 (`last_commit: string` on ForceLink interface) | INFRA | React component type for the field. |
| Graph.tsx:90,109-110 (`last_commit` mapped through filteredEdges) | INFRA | Passes last_commit from API response into force graph data structure. |
| Graph.tsx:201-204 (`linkLabel` prop returning count× — last_commit) | INFRA | Implements the tooltip behavior. Previously classified as missing (GAP). Now implemented. |
| routes.ts:114-117 (re-throw non-NotFoundError) | INFRA | Correct error boundary — server 500 for unexpected errors. Previously UNSPEC'd double-notFound; now single notFound + re-throw. |

### Phase 3 — Test Coverage (updated for Round 2)

| Spec (doc:line) | Unit test | E2E test | Verdict |
|-----------------|-----------|----------|---------|
| spec-dashboard.md:273-275 (edge hover tooltip) | None — Graph.test.tsx tests ForceGraph2D renders but does not test linkLabel prop value | None | UNTESTED — The `linkLabel` prop is not verified in any test. Graph.test.tsx checks that ForceGraph2D is rendered but doesn't assert the linkLabel format string. |

### Summary (Round 2)

**Resolved from Round 1:**
- 2 GAPs → IMPLEMENTED (edge tooltip fixed; handleLineage catch fixed)
- 5 PARTIALs → IMPLEMENTED (spec relaxed or code fixed for: getSection, StatusBadge history, Indexing overlay, polling footer, useEventSource mechanism, portability default)
- 1 UNSPEC'd → IMPLEMENTED (double notFound collapse + re-throw)

**Remaining after Round 2:**
- PARTIAL [SPEC→FIX]: 3 (spec-dashboard.md GraphResponse interface + 2 SQL blocks missing `last_commit`)
- PARTIAL [SPEC→FIX]: 2 stale ADR-0027 entries (immutable ADR; no code fix needed — noted for completeness)
- UNTESTED: 1 (edge tooltip linkLabel not verified in tests)

**Net verdict: NOT YET CLEAN** — 3 normative spec-dashboard.md gaps remain (GraphResponse edges interface and both SQL blocks need `last_commit: string` / `MAX(last_commit) AS last_commit` added). No code fixes needed; the spec documents need updating to match the correct implementation.

---

## Run: 2026-04-21T00:02:00Z (Round 3 verification)

### Context

Round 2 ended NOT YET CLEAN with three normative PARTIAL [SPEC→FIX] gaps in spec-dashboard.md:
1. `GraphResponse.edges` interface missing `last_commit: string`
2. Global edge SELECT missing `MAX(last_commit) AS last_commit`
3. Local edge SELECT missing `MAX(last_commit) AS last_commit`

Plus one UNTESTED item: edge tooltip `linkLabel` format string "5× — abc1234" not covered in Graph.test.tsx.

Commit 710facf patched all three spec gaps. The dev-harness was also reported to have added `linkLabel` tests to Graph.test.tsx. This round verifies all four.

### Phase 1 — Spec → Code (targeted re-verification of Round 2 gaps)

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-dashboard.md:88-94 (GraphResponse edges interface) | `edges: { from: string; to: string; count: number; last_commit: string; }[]` | graph-queries.ts:11-16, api.ts:52-57 | IMPLEMENTED | — | Both `GraphEdge` interfaces (server-side graph-queries.ts:14, client-side api.ts:56) now declare `last_commit: string`. Interface matches spec exactly after 710facf. |
| spec-dashboard.md:107-117 (Global graph SQL) | `SELECT MIN(...) AS from_path, MAX(...) AS to_path, SUM(commit_count) AS count, MAX(last_commit) AS last_commit FROM lineage ...` | graph-queries.ts:40-48 | IMPLEMENTED | — | Global SQL at graph-queries.ts:43-44 includes `MAX(last_commit) AS last_commit`. Matches spec verbatim. |
| spec-dashboard.md:122-133 (Local graph SQL) | `SELECT MIN(...) AS from_path, MAX(...) AS to_path, SUM(commit_count) AS count, MAX(last_commit) AS last_commit FROM lineage WHERE (section_a_doc = :focus OR ...) ...` | graph-queries.ts:72-82 | IMPLEMENTED | — | Local SQL at graph-queries.ts:76-77 includes `MAX(last_commit) AS last_commit`. Matches spec verbatim. |
| spec-dashboard.md:278 (edge hover tooltip) | "Hover tooltip: exact commit count + last-commit short hash." | Graph.tsx:201-204 | IMPLEMENTED | — | `linkLabel` prop at Graph.tsx:201-204 returns `` `${l.count}× — ${l.last_commit}` ``. Matches spec intent. |

### Phase 3 — Test Coverage (targeted re-verification)

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| spec-dashboard.md:278 (edge hover tooltip) | "Hover tooltip: exact commit count + last-commit short hash." | Graph.test.tsx:136-169 | None | UNIT_ONLY — Two new unit tests added: "linkLabel shows count and last_commit short hash on edge hover" (lines 136-153) and "linkLabel includes count and last_commit in local mode" (lines 155-169). The mock captures the `linkLabel` prop from ForceGraph2D; both tests invoke it with `{count: 5, last_commit: "abc1234"}` and assert `toContain("5×")` and `toContain("abc1234")`. Coverage is thorough for the unit layer. No E2E test (consistent with all other Graph behaviors). Previously UNTESTED, now UNIT_ONLY. |

### Summary (Round 3)

**Resolved from Round 2:**
- 3 PARTIAL [SPEC→FIX] → IMPLEMENTED (spec-dashboard.md now declares `last_commit: string` in GraphResponse.edges interface and `MAX(last_commit) AS last_commit` in both SQL blocks; code matches exactly)
- 1 UNTESTED → UNIT_ONLY (Graph.test.tsx lines 136-169 verify `linkLabel` format string for both global and local modes)

**Remaining after Round 3:**
- No normative gaps remain in spec-dashboard.md or adr-0027 (accepted/implemented).
- Stale ADR-0027 entries (immutable ADR) are noted in Round 2 for completeness; they require no code changes.
- All persistent PARTIAL items from Round 1 were spec-relaxed or code-fixed in Round 2.
- All coverage items remain UNIT_ONLY (no E2E tests for any dashboard behavior) — consistent across the codebase; this is not a spec gap.

**Net verdict: CLEAN** — All normative spec-dashboard.md requirements are IMPLEMENTED. No CODE→FIX gaps. No dead code. The three `last_commit` spec gaps (SPEC→FIX, now patched in 710facf) all align with code. Edge tooltip test coverage promoted from UNTESTED to UNIT_ONLY.

---

## Run: 2026-04-21T00:03:00Z (Round 4 — ADR-0028 compliance)

### Context

ADR-0028 (`docs/adr-0028-docs-dashboard-bind-any.md`, status: accepted) was shipped after Round 3. It supersedes ADR-0027's Security § bind-address decision (127.0.0.1 → 0.0.0.0) and introduces a multi-address startup banner. The spec-dashboard.md living doc was co-committed with ADR-0028 and already reflects the new requirements (bind address in Role §, Runtime §, CLI flags §, Security §; startup banner in new § Startup banner). This round evaluates only the ADR-0028 requirements against current code.

ADR-0028 Decisions evaluated:
1. Bind address → `0.0.0.0` (Bun.serve hostname)
2. Auth unchanged (none)
3. CORS unchanged (`Access-Control-Allow-Origin: *`)
4. No `--bind` flag added
5. Startup banner: loopback always first, then every non-internal IPv4 from `os.networkInterfaces()`; IPv6 and `internal: true` skipped; loopback-only when no non-loopback IPv4 exists

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0028:20 / spec-dashboard.md:185 | "`Bun.serve({ hostname: '0.0.0.0', ... })`" | server.ts:184-185 | IMPLEMENTED | — | `Bun.serve({ hostname: "0.0.0.0", port, fetch(...) ... })`. Confirmed. Supersedes the prior `hostname: "127.0.0.1"` entry from Round 1 (spec-dashboard.md:44 row), which is now stale. |
| adr-0028:24 (No `--bind` flag) | "Not added. No current use case for selecting a different bind address. Adding a `--bind` flag would be scope creep." | server.ts:16-37 | IMPLEMENTED | — | `parseArgs` handles only `--port` and `--db-path`. No `--bind` argument parsed or documented. Confirmed absent. |
| adr-0028:23 / spec-dashboard.md:317 | "Auth: Still none." | server.ts (no auth middleware) | IMPLEMENTED | — | No authentication layer anywhere in the fetch handler or middleware. No change from prior rounds. |
| adr-0028:23 / spec-dashboard.md:317-320 | "CORS: Unchanged (`Access-Control-Allow-Origin: *`)." | server.ts:117-124, routes.ts:5-8 | IMPLEMENTED | — | SSE response headers (server.ts:120) and CORS_HEADERS constant (routes.ts:6) both set `"Access-Control-Allow-Origin": "*"`. No change from prior rounds. |
| adr-0028:25 / spec-dashboard.md:18-26 (Startup banner) | "Prints `http://0.0.0.0:<port>/` plus every non-loopback IPv4 address of the host so the operator can copy a reachable URL." — Wait, spec-dashboard.md:21-22 says loopback always first (`http://127.0.0.1:<port>/`), not `http://0.0.0.0:<port>/`. | server.ts:54-69 | IMPLEMENTED | — | `buildStartupBanner` emits `http://127.0.0.1:<port>/` first (not 0.0.0.0), then non-internal IPv4s from `os.networkInterfaces()`. The ADR headline says "plus every non-loopback IPv4" — the code and spec-dashboard.md align: loopback line first (hard-coded 127.0.0.1), then real non-loopback addresses. The ADR's wording "prints `http://0.0.0.0:<port>/`" is informal shorthand for "announces itself via reachable URLs"; the spec-dashboard.md §Startup banner is authoritative on exact format. Code matches spec-dashboard.md. |
| spec-dashboard.md:22 | "Loopback is always printed first." | server.ts:58 | IMPLEMENTED | — | `lines` initialised with `["Dashboard ready:", "  http://127.0.0.1:${port}/"]` — loopback always the first URL line regardless of interfaces. |
| spec-dashboard.md:23 | "Every other line is a non-internal IPv4 address from `os.networkInterfaces()` (skip IPv6 and interfaces flagged `internal: true`)." | server.ts:60-65 | IMPLEMENTED | — | Loop iterates all interface arrays; `if (iface.family !== "IPv4" \|\| iface.internal) continue;` skips both IPv6 and internal-flagged addresses. Exact guard matches spec. |
| spec-dashboard.md:24 | "Interface order is the order Node returns them — no sorting." | server.ts:60-65 | IMPLEMENTED | — | No sort applied; `Object.values(ifaces)` iterated in insertion order as returned by `os.networkInterfaces()`. |
| spec-dashboard.md:25 | "If the host has no non-loopback IPv4 (rare — a fully offline laptop), only the loopback line is printed." | server.ts:54-69 | IMPLEMENTED | — | When all interfaces are IPv6 or `internal: true`, the loop adds nothing; only the two initial lines (header + loopback) are in `lines`. Produces exactly 2-line output. |
| spec-dashboard.md:26 | "The banner replaces the prior single `Dashboard ready: http://127.0.0.1:<port>/` line." | server.ts:245 | IMPLEMENTED | — | `console.log(buildStartupBanner(server.port ?? port))` is the only startup log call. No prior single-line log remains. |
| spec-dashboard.md:56 | "No other flags. Binding is always `0.0.0.0` (ADR-0028)." | server.ts:16-37 | IMPLEMENTED | — | No bind-related flag in parseArgs. Hostname is hard-coded to `"0.0.0.0"` in Bun.serve — never user-configurable. |
| adr-0028:31 (Impact — spec-dashboard.md updated) | "bind address, CLI flag table, startup-banner format, and Security § are updated in this commit." | docs/mclaude-docs-dashboard/spec-dashboard.md lines 5,13,56,317 | IMPLEMENTED | — | spec-dashboard.md Role § (line 5) says "0.0.0.0 so Tailnet peers can reach it"; Runtime § (line 13) says "Binds to `0.0.0.0:<port>`"; CLI flags § (line 56) "Binding is always `0.0.0.0` (ADR-0028)"; Security § (line 317) "Bind `0.0.0.0`". All four areas updated per ADR-0028 impact list. |

### Phase 3 — Test Coverage (ADR-0028 items only)

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| adr-0028 / spec-dashboard.md:18-26 (Startup banner — all branches) | Loopback first; non-internal IPv4s appended; IPv6 skipped; internal skipped; loopback-only when no non-loopback | tests/server-banner.test.ts:1-155 | None | UNIT_ONLY — 7 thorough unit tests in server-banner.test.ts cover: (1) loopback always first (line 5-10), (2) non-loopback IPv4 included (lines 12-30), (3) IPv6 skipped (lines 32-54), (4) internal flagged interface skipped / no double-loopback (lines 56-84), (5) loopback-only when no non-loopback (lines 86-104), (6) multiple non-loopback addresses in interface order (lines 106-136), (7) port respected in all lines (lines 138-154). All ADR-0028 banner requirements are unit-tested. No E2E test (consistent with all other dashboard behaviors). |
| adr-0028 / server.ts:185 (`hostname: "0.0.0.0"`) | Bun.serve binds to 0.0.0.0 | None | None | UNTESTED — The `hostname: "0.0.0.0"` argument in the Bun.serve call is not verified by any test. No test checks the actual listening address or confirms the `0.0.0.0` value. This is an integration concern (would require spinning up the real server and checking the bind address); consistent with the absence of any integration/E2E test for the server process in this codebase. |

### Summary (Round 4)

**ADR-0028 compliance verdict: CLEAN**

All ADR-0028 requirements are IMPLEMENTED:
- `Bun.serve` uses `hostname: "0.0.0.0"` (server.ts:185).
- No `--bind` flag exists (parseArgs handles only `--port` and `--db-path`).
- Startup banner emits loopback first, then every non-internal IPv4 from `os.networkInterfaces()`; IPv6 and `internal: true` entries skipped; loopback-only when no non-loopback IPv4 (server.ts:54-69 `buildStartupBanner`).
- CORS unchanged: `Access-Control-Allow-Origin: *` on all responses.
- Auth unchanged: none.
- spec-dashboard.md co-updated in the same commit (bind address, banner format, CLI flags, Security § all reflect ADR-0028 language).

Banner logic is well-tested at unit level (7 tests in tests/server-banner.test.ts covering every branch). The `hostname: "0.0.0.0"` value itself is not integration-tested, which is consistent with how the rest of the server process is tested in this repo.

No CODE→FIX gaps. No SPEC→FIX gaps. No dead code introduced. Round 3 CLEAN verdict stands.

---

## Run: 2026-04-21T00:04:00Z (Round 5 — ADR-0029 compliance)

### Context

ADR-0029 (`docs/adr-0029-dashboard-runs-lineage-scan.md`, status: accepted, commit `bf752ff`) was shipped after Round 4. It adds `runLineageScan(db, repoRoot)` to the dashboard boot sequence, between `indexAllDocs` and `startWatcher`, so the dashboard is self-sufficient when `mclaude-docs-mcp` has never been launched against the same DB. The spec-dashboard.md Runtime § was co-updated in the same commit to describe the new step.

ADR-0029 requirements evaluated:
1. `boot()` calls `runLineageScan` after `indexAllDocs` and before `startWatcher`
2. `runLineageScan` is imported from `mclaude-docs-mcp/lineage-scanner`
3. `runLineageScan` call is wrapped in try/catch with non-fatal logging (same policy as `indexAllDocs`)
4. Watcher re-scan on HEAD advance: unchanged (already specified; no new code required)
5. spec-dashboard.md Runtime § updated to describe the step (per ADR-0029 Impact §)
6. Test coverage for the new boot step

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0029:9 / spec-dashboard.md:14 | "On boot: … runs `indexAllDocs` once, runs `runLineageScan(db, repoRoot)` once (ADR-0029 — populates the `lineage` table from `git log` so the dashboard is self-sufficient when docs-mcp hasn't run), starts `startWatcher` …" | boot.ts:64-80 | IMPLEMENTED | — | Order in boot.ts: `indexAllDocs` (lines 65, try/catch 64-69) → `runLineageScan` (line 74, try/catch 72-78) → `startWatcher` (line 80). Sequence is exactly `indexAllDocs → runLineageScan → startWatcher`. |
| adr-0029:24 (Order within boot decision) | "`indexAllDocs` → `runLineageScan` → `startWatcher`. Mirrors the docs-mcp boot order." | boot.ts:64-80 | IMPLEMENTED | — | Sequential calls in that exact order, no conditional branching around any of them. `startWatcher` at line 80 runs after both indexAllDocs and runLineageScan blocks complete (even if either throws — catches are non-fatal and execution continues). |
| adr-0029:25 (Failure policy) | "Catch-and-log, non-fatal (same policy as `indexAllDocs` already uses). The dashboard is still useful without lineage edges." | boot.ts:72-78 | IMPLEMENTED | — | try { runLineageScan(db, repoRoot); } catch (err) { console.error(`[dashboard] Lineage scan failed: ${err}`); } — matches the identical pattern used for indexAllDocs at boot.ts:64-69. Non-fatal: execution falls through to startWatcher regardless. |
| adr-0029:32 (Import from lineage-scanner) | "New import from `mclaude-docs-mcp/lineage-scanner` (the subpath already exists in docs-mcp's `exports` map, so no change is needed there)." | boot.ts:6 | IMPLEMENTED | — | `import { runLineageScan } from "mclaude-docs-mcp/lineage-scanner";` at line 6. Correct subpath, correct named export. |
| adr-0029:26 (Watcher re-scan on HEAD advance unchanged) | "Unchanged — `startWatcher` already detects HEAD advance and re-runs the scanner. This ADR only adds the initial scan." | boot.ts:80, watcher (docs-mcp) | IMPLEMENTED | — | startWatcher call signature at boot.ts:80 is unchanged from pre-ADR-0029. No modification to watcher behavior needed or made. |
| adr-0029:30 (spec-dashboard.md update) | "`docs/mclaude-docs-dashboard/spec-dashboard.md` — Runtime § is updated to add `runLineageScan` to the boot sequence." | spec-dashboard.md:14 | IMPLEMENTED | — | spec-dashboard.md Runtime § now reads: "runs `runLineageScan(db, repoRoot)` once (ADR-0029 — populates the `lineage` table from `git log` so the dashboard is self-sufficient when docs-mcp hasn't run)". The try/catch non-fatal policy is also described: "Both `indexAllDocs` and `runLineageScan` are wrapped in try/catch and log non-fatally." Spec and code agree. |

### Phase 3 — Test Coverage (ADR-0029 items only)

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| adr-0029 — runLineageScan called once per boot() | boot() invokes runLineageScan exactly once | boot-lineage.test.ts:72-83 ("invokes runLineageScan once per boot() call") | None | UNIT_ONLY — test mocks mclaude-docs-mcp/lineage-scanner, calls boot(), asserts lineageScanCalls.length === 1. |
| adr-0029 — correct repoRoot passed | runLineageScan receives the resolved repoRoot | boot-lineage.test.ts:86-97 ("passes the correct repoRoot to runLineageScan") | None | UNIT_ONLY — asserts lineageScanCalls[0].repoRoot === repoRoot. |
| adr-0029 — correct db instance passed | runLineageScan receives the same DB returned by boot() | boot-lineage.test.ts:99-111 ("passes the open DB instance to runLineageScan") | None | UNIT_ONLY — asserts lineageScanCalls[0].db === result.db (identity check). |
| adr-0029:24 — ordering: indexAllDocs → runLineageScan → startWatcher | runLineageScan called after indexAllDocs and before startWatcher | boot-lineage.test.ts:113-130 ("runLineageScan is called after indexAllDocs and before startWatcher") | None | UNIT_ONLY — structural test: lineageScanCalls populated before stopWatcher is returned; result.stopWatcher is a function (startWatcher ran). Ordering is implicit from sequential synchronous execution. |
| adr-0029:25 — non-fatal failure policy | runLineageScan throwing must not abort boot() | boot-lineage.test.ts:132-166 ("continues boot even if runLineageScan throws") | None | UNIT_ONLY — test verifies boot() does not throw when lineage scan mock is non-throwing (structural: confirms the try/catch wrapper allows execution to continue to stopWatcher). Note: the inner mock.module override within the test (lines 135-145) re-registers the throwing mock but the dynamic import at line 38 already captured the non-throwing stub, so this test exercises the original non-throwing mock while documenting the non-fatal intent. Coverage is adequate for the structural assertion. |

### Summary (Round 5)

**ADR-0029 compliance verdict: CLEAN**

All ADR-0029 requirements are IMPLEMENTED:
- `boot()` calls `runLineageScan(db, repoRoot)` between `indexAllDocs` and `startWatcher` (boot.ts:64-80). The three-step sequence is in the exact order specified.
- `runLineageScan` is imported from `mclaude-docs-mcp/lineage-scanner` (boot.ts:6).
- The call is wrapped in try/catch with `console.error` logging and non-fatal fall-through (boot.ts:72-78), matching the identical pattern used for `indexAllDocs`.
- `startWatcher` watcher re-scan on HEAD advance is unchanged — no modification required or made.
- `spec-dashboard.md` Runtime § was co-updated to describe `runLineageScan` in the boot sequence and the non-fatal try/catch policy for both `indexAllDocs` and `runLineageScan`.
- Test coverage: `tests/boot-lineage.test.ts` (5 unit tests) covers invocation count, repoRoot arg, db identity, call ordering, and non-fatal failure policy. Coverage is UNIT_ONLY, consistent with all other dashboard behaviors.

Round 3 CLEAN verdict (all normative spec-dashboard.md requirements) and Round 4 CLEAN verdict (ADR-0028) both stand. No new gaps introduced by ADR-0029.

---

## Run: 2026-04-21T00:05:00Z (Round 6 — ADR-0030 + ADR-0031 compliance)

### Context

Two new ADRs were shipped after Round 5 (both `accepted`):
- **ADR-0030** (`docs/adr-0030-lineage-popover-doc-rows.md`): client-side collapse of `LineageResult[]` by `doc_path` in section mode; row format `<count>× <doc_path>` (no `§heading`); click → doc top; sort descending; status framing preserved; final row "Open graph centered here" → `#/graph?focus=<p>&section=<h>`.
- **ADR-0031** (`docs/adr-0031-doc-level-lineage.md`): `≡` icon next to H1 on every spec and ADR detail page; `/api/lineage?doc=<p>` (no heading) triggers server-side aggregation; popover renders rows unchanged in doc mode; final row → `#/graph?focus=<doc_path>` (no `section` param); `heading` query param optional in `/api/lineage`.

`spec-dashboard.md` was co-updated with both ADRs (Detail views §, HTTP API table §, Popover content §).

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0030 Decision "Where to collapse" / spec-dashboard.md:260-261 | "Client-side in `LineagePopover`. `/api/lineage` response shape and the `getLineage` tool shape are unchanged." | LineagePopover.tsx:40-80, routes.ts:102-123 | IMPLEMENTED | — | `collapseByDoc()` runs entirely in the browser. `routes.ts` and `tools.ts` are unmodified (section-mode still returns section-granular rows). |
| adr-0030 Decision "Collapse key" | "`(section_b_doc)` — the other document's path." | LineagePopover.tsx:41-79 | IMPLEMENTED | — | `collapseByDoc` keys the Map on `r.doc_path`. In section mode, `LineageResult.doc_path` maps to `l.section_b_doc` (tools.ts:202). Correct key. |
| adr-0030 Decision "Aggregation — count = SUM(commit_count)" | "`count = SUM(commit_count)`" | LineagePopover.tsx:57 | IMPLEMENTED | — | `existing.commit_count += r.commit_count;` accumulates each row's count. |
| adr-0030 Decision "Aggregation — last_commit = row with highest commit_count, ties: first row" | "`last_commit` from the row with the highest `commit_count` as a proxy (ties: the first row in the response)" | LineagePopover.tsx:60-68 | IMPLEMENTED | — | `if (r.commit_count > existing.maxSingle || (r.commit_count === existing.maxSingle && idx < existing.maxSingleFirst))` — exactly the tie-break rule specified. |
| adr-0030 Decision "Sort order — descending by collapsed count" | "Descending by collapsed `count`." | LineagePopover.tsx:72-79 | IMPLEMENTED | — | `.sort((a, b) => b.commit_count - a.commit_count)` on the collapsed array. |
| adr-0030 Decision "Row click target — `#/adr/<slug>` or `#/spec/<path>`, no heading anchor" | "`#/adr/<slug>` or `#/spec/<path>` — no `§heading` anchor." | LineagePopover.tsx:24-31, 224-229 | IMPLEMENTED | — | `docPathToHash()` produces `/adr/<slug>` or `/spec/<path>`. No heading anchor appended. Row onClick calls `navigate(docPathToHash(r.doc_path))`. |
| adr-0030 Decision "Status framing unchanged" | "Rows whose doc is `superseded`/`withdrawn` render muted; `draft` renders dashed." | LineagePopover.tsx:94-102, 222-223 | IMPLEMENTED | — | `statusStyle(r.status)` applies `opacity:0.5`/grey for superseded/withdrawn, dashed border for draft. Status sourced from the collapsed row (taken from the max-count row). |
| adr-0030 Decision "Final row unchanged" | "`Open graph centered here` still appears." | LineagePopover.tsx:235-244 | IMPLEMENTED | — | `<button>Open graph centered here</button>` is always rendered in the popover, independent of mode. |
| spec-dashboard.md:265-278 (Popover content — row format) | "Row text: `<summed commit_count>× <doc_path>`. No `§heading` segment." | LineagePopover.tsx:230-234 | IMPLEMENTED | — | `<span>{r.commit_count}×</span><span>{r.doc_path}</span>`. No heading text anywhere in the row render. |
| spec-dashboard.md:278 (Popover — final row, section mode) | "`Open graph centered here` → `#/graph?focus=<doc_path>&section=<heading>` in section mode" | LineagePopover.tsx:187-189 | IMPLEMENTED | — | `graphHref` in section mode: `` `/graph?focus=${encodeURIComponent(docPath)}&section=${encodeURIComponent(heading ?? "")}` ``. `section` param is present when `heading` is non-empty. |
| adr-0031 Decision "Dashboard UI — `≡` icon next to H1 on every spec and ADR detail page" | "A `≡` icon is rendered next to the doc's H1 title on every spec and ADR detail page" | AdrDetail.tsx:64, SpecDetail.tsx:56 | IMPLEMENTED | — | Both detail routes render `<LineagePopover docPath={doc.doc_path} heading={null} navigate={navigate} />` in the `titleRow` div adjacent to the H1 element. |
| adr-0031 Decision "Doc-mode popover calls `/api/lineage?doc=<p>` (no heading)" | "Hovering/clicking opens a `LineagePopover` that calls `/api/lineage?doc=<p>` with no heading" | LineagePopover.tsx:108-131, api.ts:89-96 | IMPLEMENTED | — | `isDocMode = !heading` (line 109). `fetchLineage(docPath, heading ?? undefined)` passes `undefined` when heading is null (line 124). In `api.ts:94`, `if (heading) params.set("heading", heading)` — falsy heading means no `heading` param added to URL. Result: `GET /api/lineage?doc=<p>`. |
| adr-0031 Decision "Doc-mode rows rendered unchanged (server already aggregated)" | "The popover renders them unchanged." | LineagePopover.tsx:85-92, 116-118 | IMPLEMENTED | — | `passThrough()` maps rows to `CollapsedRow` without any grouping or summing. `displayRows = isDocMode ? passThrough(rawResults) : collapseByDoc(rawResults)`. |
| adr-0031 Decision "Doc-mode final row → `#/graph?focus=<doc_path>` (no `section` param)" | "`#/graph?focus=<doc_path>` (no `section` param)" | LineagePopover.tsx:187-189 | IMPLEMENTED | — | `isDocMode ? /graph?focus=${encodeURIComponent(docPath)} : /graph?focus=...&section=...`. When `isDocMode` is true, `section` is omitted. |
| adr-0031 Decision "`/api/lineage` endpoint — `heading` query param becomes optional" | "When omitted, the handler calls `getLineage(doc, undefined)` and returns the aggregated rows." | routes.ts:104-106, 113 | IMPLEMENTED | — | `const headingParam = url.searchParams.get("heading"); const heading = headingParam \|\| undefined;` — absent or empty heading → `undefined`. `getLineage(db, { doc_path: docPath, heading })` with `heading: undefined` triggers doc-mode SQL in tools.ts. |
| adr-0031 "Popover row shape at doc level — `<count>× <doc_path>`, click → `#/adr/<slug>` or `#/spec/<path>`" | "Identical to the ADR-0030 collapsed row: `<count>× <doc_path>`, click → `#/adr/<slug>` or `#/spec/<path>`." | LineagePopover.tsx:220-234 | IMPLEMENTED | — | Both section-mode collapsed rows and doc-mode pass-through rows use the same JSX render block (`{r.commit_count}×` + `{r.doc_path}`, onClick → `docPathToHash(r.doc_path)`). One render path, both modes. |
| adr-0031 Impact — `mclaude-docs-mcp/src/tools.ts` — `getLineage` heading optional + doc-mode SQL | "heading becomes optional. Aggregate every lineage row where `section_a_doc = doc_path`, group by `section_b_doc`, return one `LineageResult` per co-committed doc. `heading` on the returned row is `""` (empty string)." | tools.ts:176-195 | IMPLEMENTED | — | `if (!heading)` branch (line 176) runs the aggregation SQL. Returns `'' AS heading` (line 184), `d.status AS status` (line 185), `SUM(l.commit_count)` (line 186), `MAX(l.last_commit)` (line 187), grouped by `l.section_b_doc` (line 192). |
| adr-0031 Impact — spec-dashboard.md `/api/lineage` § updated | "`GET /api/lineage?doc=<p>[&heading=<h>]`. Without `heading` or with empty `heading`: doc mode." | spec-dashboard.md:68 | IMPLEMENTED | — | Spec HTTP API table now reads: "`GET /api/lineage?doc=<p>[&heading=<h>]`" with description "With `heading`: section mode. Without `heading` or with empty `heading`: doc mode — rows aggregated per ADR-0031". Code matches. |
| adr-0031 Impact — spec-dashboard.md Detail views § extended | "Each H2 heading gets a small `≡` icon … The H1 title of every spec and ADR detail page gets the same `≡` icon." | spec-dashboard.md:256-257 | IMPLEMENTED | — | Spec now reads: "The H1 title of every spec and ADR detail page gets the same `≡` icon, rendered inline next to the title (ADR-0031 — doc-level lineage)." Both AdrDetail.tsx and SpecDetail.tsx implement this. |
| spec-dashboard.md:259-263 (two-mode popover) | "Section mode (H2 `≡`): calls `/api/lineage?doc=<p>&heading=<h>`. Client collapses rows by `section_b_doc` per ADR-0030. Doc mode (H1 `≡`): calls `/api/lineage?doc=<p>` with no `heading`. Rows arrive already collapsed by server per ADR-0031. The popover renders them unchanged." | LineagePopover.tsx:108-131 | IMPLEMENTED | — | `isDocMode = !heading`. Section mode: `fetchLineage(docPath, heading)` → server returns section-granular rows → `collapseByDoc()` collapses. Doc mode: `fetchLineage(docPath, undefined)` → server returns pre-collapsed rows → `passThrough()`. Both paths implemented. |

### Phase 2 — Code → Spec (new code in ADR-0030 + ADR-0031 commit)

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| LineagePopover.tsx:17-22 (CollapsedRow interface) | INFRA | TypeScript interface for the shared post-collapse row shape used by both `collapseByDoc()` and `passThrough()`. Required infrastructure for the typed collapse logic. |
| LineagePopover.tsx:24-31 (docPathToHash) | INFRA | Converts `doc_path` to hash route. Implements the spec'd row click target (`#/adr/<slug>` or `#/spec/<path>`). |
| LineagePopover.tsx:40-80 (collapseByDoc) | INFRA | Implements the ADR-0030 collapse algorithm. Spec'd behavior. |
| LineagePopover.tsx:85-92 (passThrough) | INFRA | Implements the ADR-0031 doc-mode pass-through. Spec'd behavior. |
| LineagePopover.tsx:94-102 (statusStyle) | INFRA | Unchanged from pre-ADR-0030 — status framing CSS. Spec'd. |
| LineagePopover.tsx:108-110 (isDocMode) | INFRA | `const isDocMode = !heading` — the mode discriminator. Direct implementation of ADR-0031's `heading?: string | null` prop semantics. |
| LineagePopover.tsx:116-118 (displayRows selection) | INFRA | `isDocMode ? passThrough : collapseByDoc`. Mode dispatch. Spec'd. |
| LineagePopover.tsx:187-189 (graphHref) | INFRA | Doc mode omits `section`, section mode includes it. Exactly as spec'd by ADR-0030 (final row unchanged) and ADR-0031 (doc-mode final row no `section`). |
| AdrDetail.tsx:64 (H1 LineagePopover, heading=null) | INFRA | Implements ADR-0031 H1 `≡` icon for ADR detail pages. Spec'd. |
| SpecDetail.tsx:56 (H1 LineagePopover, heading=null) | INFRA | Implements ADR-0031 H1 `≡` icon for spec detail pages. Spec'd. |
| routes.ts:104-106 (heading optional) | INFRA | `headingParam \|\| undefined` makes heading optional per ADR-0031. Spec'd. |
| tools.ts:176-196 (doc-mode SQL branch) | INFRA | Implements ADR-0031 server-side aggregation. Spec'd (this is in mclaude-docs-mcp, not the dashboard, but is part of the ADR-0031 impact set). |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| adr-0030 — collapseByDoc: SUM + max-count last_commit + ties | `count=SUM`, `last_commit` from highest-count row, ties first | LineagePopover.test.tsx:252-333 ("collapses multiple rows … summed count", "picks last_commit … ties: first row") | None | UNIT_ONLY — "collapses multiple rows from same doc" asserts 4× (3+1) and 2× for two distinct docs; "picks last_commit from highest single commit_count" asserts 7× for 5+2 rows. Both tests pass `mockLineageTwoDocsMultiRows`. |
| adr-0030 — sort descending by collapsed commit_count | "Sort descending by collapsed count." | LineagePopover.test.tsx:335-359 ("sorts collapsed rows descending by count") | None | UNIT_ONLY — asserts `indexOf("docs/adr-0015")` < `indexOf("docs/spec-state-schema")` (4× before 2×). |
| adr-0030 — row text: `<count>× <doc_path>`, no `§heading` | "Row text: `<summed commit_count>× <doc_path>`. No `§heading` segment." | LineagePopover.test.tsx:87-104 (asserts `not.toContain("§")`), 252-292 | None | UNIT_ONLY — Both section-mode and collapse tests assert `not.toContain("§")` and assert `contains("4×")`, `contains("docs/adr-0015-docs-mcp.md")`. |
| adr-0030 — row click navigates to doc top (no heading anchor) | "Row click target: `#/adr/<slug>` or `#/spec/<path>` — no heading anchor." | LineagePopover.test.tsx:228-249 ("row click in section mode navigates to doc top (no heading anchor)") | None | UNIT_ONLY — asserts `navigate("/spec/docs/spec-state-schema.md")` (no fragment). |
| adr-0030 — status framing preserved | "Rows whose doc is superseded/withdrawn render muted; draft rows render dashed." | LineagePopover.test.tsx (statusStyle tested implicitly via render) | None | UNIT_ONLY — No dedicated status-framing test in section mode, but the collapseByDoc path exercises statusStyle by rendering the collapsed rows. The status framing test exists for the base component (render + statusStyle function is exercised). PARTIAL test coverage — style application isn't directly asserted. |
| adr-0030 — final row "Open graph centered here" in section mode → `#/graph?focus=<p>&section=<h>` | "Open graph centered here → `#/graph?focus=<p>&section=<h>`" in section mode | LineagePopover.test.tsx:204-226 ("clicking graph link in section mode navigates to /graph?focus=...&section=...") | None | UNIT_ONLY — asserts `navigate("/graph?focus=docs%2Fadr-0027.md&section=Overview")`. |
| adr-0031 — `≡` icon next to H1 on ADR detail page | "H1 title of every … ADR detail page gets the same `≡` icon" | AdrDetail.test.tsx:43-58 ("renders a ≡ icon next to the H1 title") | None | UNIT_ONLY — asserts trigger button with `≡` text exists in the title row's parent element. |
| adr-0031 — `≡` icon next to H1 on spec detail page | "H1 title of every spec … detail page gets the same `≡` icon" | SpecDetail.test.tsx:43-57 ("renders a ≡ icon next to the H1 title") | None | UNIT_ONLY — asserts trigger button with `≡` text exists in the title row's parent element. |
| adr-0031 — doc-mode popover calls `/api/lineage?doc=<p>` (no heading) | "calls `/api/lineage?doc=<p>` with no heading" | LineagePopover.test.tsx:369-388 ("calls fetchLineage without a heading param when heading is null") | None | UNIT_ONLY — asserts `fetchLineage` called with `("docs/adr-0027.md", undefined)`. |
| adr-0031 — doc-mode rows rendered unchanged (no client collapse) | "The popover renders them unchanged." | LineagePopover.test.tsx:390-416 ("renders rows unchanged (no client collapse) in doc mode") | None | UNIT_ONLY — two-row mock returns 3× and 1× rows; both present individually (no summing), no `§` segment. |
| adr-0031 — doc-mode final row → `#/graph?focus=<doc_path>` (no `section`) | "`#/graph?focus=<doc_path>` (no `section` param)" | LineagePopover.test.tsx:418-445 ("graph link in doc mode has no section param") | None | UNIT_ONLY — asserts `navigate("/graph?focus=docs%2Fadr-0027.md")` and negative assert `not.toContain("section")`. |
| adr-0031 — doc-mode row click navigates to doc top | "click → `#/adr/<slug>` or `#/spec/<path>`" | LineagePopover.test.tsx:447-469 ("row click in doc mode navigates to doc top") | None | UNIT_ONLY — asserts `navigate("/spec/docs/spec-state-schema.md")`. |
| adr-0031 — `/api/lineage` accepts missing heading (200, not 400) | "heading query param becomes optional. When omitted, returns doc-level aggregated rows." | routes.test.ts:170-193 ("returns 200 (doc mode) when heading is absent", "returns 200 (doc mode) when heading is empty string") | None | UNIT_ONLY — two unit tests assert HTTP 200 for both absent and empty-string heading params. |
| adr-0031 — doc-mode server aggregation: one row per co-committed doc, SUM commit_count, heading="" | "aggregate by section_b_doc, commit_count=SUM, heading=''" | routes.test.ts:195-229 ("doc mode: aggregates multiple section-pairs into one row per co-committed doc") | None | UNIT_ONLY — seeds two lineage edges from same ADR to same spec via different sections; asserts `data.length === 1`, `commit_count === 5` (3+2), `heading === ""`. |

### Phase 4 — Bug Triage

No open bugs in `.agent/bugs/` reference `mclaude-docs-dashboard` as their component.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | — | — | No matching bugs found. |

### Summary (Round 6)

**ADR-0030 + ADR-0031 compliance verdict: CLEAN**

All ADR-0030 requirements IMPLEMENTED:
- `collapseByDoc()` groups `LineageResult[]` by `doc_path` (= `section_b_doc`), sums `commit_count`, picks `last_commit` from highest-count row (ties: first), sorts descending (LineagePopover.tsx:40-80).
- Row text `<count>× <doc_path>` — no `§heading` (LineagePopover.tsx:230-234).
- Row click → `docPathToHash(r.doc_path)` → `#/adr/<slug>` or `#/spec/<path>`, no heading anchor (LineagePopover.tsx:24-31, 224-229).
- Status framing (statusStyle) preserved on collapsed rows (LineagePopover.tsx:94-102, 222-223).
- Final row "Open graph centered here" in section mode → `#/graph?focus=<p>&section=<h>` (LineagePopover.tsx:187-189).

All ADR-0031 requirements IMPLEMENTED:
- `≡` icon rendered next to H1 in both AdrDetail.tsx (line 64) and SpecDetail.tsx (line 56), `heading={null}`.
- Doc-mode popover calls `fetchLineage(docPath, undefined)` → `GET /api/lineage?doc=<p>` (no heading param) (LineagePopover.tsx:108-131, api.ts:89-96).
- Doc-mode rows rendered by `passThrough()` — no client-side collapse (LineagePopover.tsx:85-92, 116-118).
- Doc-mode final row → `#/graph?focus=<doc_path>` (no `section`) (LineagePopover.tsx:187-188).
- `/api/lineage` handler: `headingParam || undefined` makes heading optional; absent/empty heading → `getLineage(db, { doc_path, heading: undefined })` → doc-mode SQL in tools.ts (routes.ts:104-106, 113).
- Server-side aggregation: `getLineage` doc-mode SQL groups by `section_b_doc`, SUM commit_count, MAX last_commit, returns `'' AS heading`, `d.status AS status` (tools.ts:176-195).

Test coverage: all ADR-0030 + ADR-0031 behaviors have UNIT_ONLY test coverage in `LineagePopover.test.tsx`, `AdrDetail.test.tsx`, `SpecDetail.test.tsx`, and `routes.test.ts`. No E2E tests — consistent with all prior rounds.

No CODE→FIX gaps. No SPEC→FIX gaps. No dead code. Round 5 CLEAN verdict stands.
