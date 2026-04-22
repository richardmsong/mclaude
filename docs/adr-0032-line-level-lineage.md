# ADR: Line-Level Spec Lineage

**Status**: accepted
**Status history**:
- 2026-04-22: draft
- 2026-04-22: accepted — paired with docs/mclaude-docs-mcp/spec-docs-mcp.md + docs/mclaude-docs-dashboard/spec-dashboard.md

## Overview

Every rendered line of a spec (and optionally of an ADR) exposes the commit(s) that last touched that exact line, and from each such commit the reader can see which ADR(s) co-committed with it. Today lineage is H2-section granular — "these ADRs shaped this section" — which collapses adjacent enum variants or adjacent fields into one bucket. Line-level lineage lets the reader ask, on a single `status` enum, "ADR-X gave us `requires_action`; ADR-Y gave us `waiting_for_input`."

## Motivation

Observed while reading a data-schema spec in the dashboard: the H2 "Status enum" section had six ADR contributors, but there was no way to see which enum variant came from which ADR. The reader had to either trust the coarse attribution or `git log -p` the file and scroll. Section-granular lineage is the right grain for "how was this concept born" — but not for "what drove line 47 specifically."

The scanner already has the hunk-level data (it computes `DiffHunk` per commit and maps hunks → sections by line-range intersection before throwing the line numbers away). All the raw information exists; we just stop short of surfacing it.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Storage model | Use `git blame` + `git log -L` at query time, no new DB table. Cache the computed block-level result per-(file, HEAD-sha) in memory; invalidate on HEAD advance. | Per-line history (`git log -L`) handles line movement automatically via `-M -w` and re-runs when HEAD changes. Pre-computing rows means re-scanning on every commit and mitigating line-number drift; the cache handles the repeat-query cost instead. |
| Granularity | Hybrid — per source line inside tables, fenced code blocks, and list items; collapsed to one entry per paragraph for continuous prose (consecutive non-blank lines not in a list/table/fence). Block boundaries computed from the markdown AST so soft wraps inside a paragraph stay in one block. | Preserves resolution where adjacent lines carry distinct semantic units (enum variants, schema rows) and reduces noise in narrative text. |
| Block ADR aggregation | A block's ADR list is the **union** of the full-authorship ADR sets of every source line in the block. Ordered by most-recent commit date descending (the block's `last_commit` is the MAX across its lines). | The block is a presentation unit; every ADR that ever touched any of its source lines is a legitimate contributor. Deduplication by ADR doc path keeps the list short. |
| Line "ADR contributor" definition | Full authorship chain — for each source line, take every commit that ever touched it via `git log -L<start>,<line>:<file> -M -w`, then union their co-committed ADRs. | The reader wants to see every ADR that has ever shaped a specific line, not just whichever commit happens to own it right now. A mechanical rename should not erase the ADR that originally drove the line. |
| Non-ADR commits | Show them muted. Each non-ADR commit renders as one greyed row: `<short_sha> · <first line of commit message, truncated to 60 chars>`. ADR contributors render first (coloured), non-ADR commits render below (muted). Both ordered by commit date descending within their group. | The reader wants to see whether a mechanical rename or typo fix is "hiding" the last ADR touch. Hiding non-ADR entirely would make a clean popover feel like blame-lying. Muting separates them visually without discarding them. |
| Symmetry (line-level on ADRs too) | Yes — both specs and ADRs get the per-block hover popover. On ADR pages the popover shows *which specs* this ADR block's source lines touched (inverse arrow), using the same `get_line_lineage` output but co-committed *spec* paths instead of *ADR* paths. | Readers land on ADR pages asking "which specs did this decision shape" just as often as the spec-side question. Symmetric surface area keeps the mental model simple. |
| Non-ADR bidirectional | The non-ADR muted rows apply to both directions. On an ADR page, a pure-refactor commit that touched a block also renders muted (same format). | Same rationale as non-ADR on spec pages: honesty about the authorship chain. |
| UI affordance | Hover popover on every rendered block. Hovering any paragraph, list item, table row, or code line reveals an inline popover listing that block's ADR contributors (deduped, ordered by most-recent `last_commit` descending). Zero visual weight at rest. Reuses the existing `LineagePopover` component and styling. | Matches the established `≡` popover UX. Keeps the page clean for readers who do not want attribution info. |
| API shape | `/api/line-lineage?doc=<p>` returns `{html: string, blocks: Block[]}` where each `Block` is `{block_id, start_line, end_line, kind: "prose" \| "list_item" \| "table_row" \| "code_line", contributors: {doc_path, status, commit_count, last_commit}[], non_adr_commits: {sha, subject, commit_date}[]}`. `html` is the server-rendered markdown with a `data-block-id="<id>"` attribute on each block's wrapping element. | One source of truth for block boundaries — the server runs micromark once, derives block spans and block_ids, and emits HTML with matching data attributes. The client has zero markdown-parsing responsibility in this mode; it just hydrates hover state by looking up `event.target.closest("[data-block-id]").dataset.blockId` in the `blocks` array. |
| Server-side HTML rendering | `mclaude-docs-mcp` produces the HTML by walking the same micromark token stream used for block detection, emitting minimal CommonMark-compliant HTML with `data-block-id` on each block wrapper (`<p>`, `<li>`, `<tr>`, `<code>` line span). Inline markdown (emphasis, links, code spans, etc.) is rendered normally. | Avoids shipping a second markdown parser to the UI. The MCP server is the canonical render. |
| Fetch strategy | One `git log -L<start>,<end>:<file> -M -w --format=%H` per block, executed serially on the main request path. Returns a commit set per block; each commit is then resolved to co-committed ADR/spec docs using existing `lineage` logic. No parallel fan-out, no SSE streaming for v1. | A 100-block spec takes ~1-2s on cold fetch; cached thereafter. Parallel and SSE add complexity the cache already eliminates. |
| Cache storage | SQLite table `line_lineage_cache(doc_path TEXT, head_sha TEXT, response_json TEXT, computed_at INTEGER, PRIMARY KEY(doc_path, head_sha))` in the existing docs DB. On HEAD advance the watcher deletes the rows for the `doc_path`s whose content changed. On process restart the cache is still warm. | Dashboard restarts are frequent during development; piggybacking on the existing SQLite DB is effectively free. |
| Cache pruning | On boot, run `DELETE FROM line_lineage_cache WHERE head_sha != :current_head_sha` once. Bounds steady-state cache to ≤1 row per `doc_path`. | Cheap (single SQL), avoids unbounded growth across many HEAD advances. |
| Markdown parser | `micromark` + `micromark-extension-gfm-table` inside `mclaude-docs-mcp`. Events are walked to extract block spans: `paragraph`, `listItemPrefix`, `tableHead`/`tableBody` rows (per source line), and `codeFenced` (per source line). | CommonMark-compliant, small (~10KB), GFM-capable. Hand-rolling a tokenizer would miss edge cases (setext headings, indented code blocks, HTML, nested lists). |
| Table row wrapping | Strict per-source-line inside tables. A wrapped table cell's continuation line becomes its own block. | Matches how `git blame` attributes source; keeps the block model uniform across tables/lists/code. Edits to a continuation line get correct attribution. |
| Non-ADR muted row display | `<short_sha> · <YYYY-MM-DD> · <subject-truncated-60>`. No click behaviour in v1. | Purely informational — there is nowhere meaningful to navigate. A `git show` modal is deferred. |
| Block ID derivation | `block_id = sha256("${start_line}:${end_line}:${content.trim()}").slice(0,16)` — 16 hex chars. Stable across identical block content; changes when the content or line range changes. | Gives the UI a stable anchor for popover state without depending on absolute line numbers alone. |
| Stale popover on live edit | On SSE `{type: "reindex", changed}` where `changed.includes(activeDocPath)`, the UI dismisses any open line-lineage popover, shows a brief "Doc updated" toast, and re-fetches `/api/line-lineage` to refresh the rendered HTML + blocks. Reader re-hovers to see fresh data. | Honest — no fuzzy id remapping that risks showing attribution for the wrong block. Reuses the existing reindex event; no new SSE event type. |
| HEAD-sha resolution | Server reads current HEAD via the watcher's in-memory state (populated by the existing HEAD-advance signal from ADR-0029). Falls back to `git rev-parse HEAD` if the watcher hasn't initialized yet. | Zero extra subprocess on the hot path once steady-state. |
| Relationship to existing lineage | Additive. Section-level popovers (ADR-0030) and doc-level (ADR-0031) remain unchanged. Line-level is a new overlay on the rendered markdown view. |  |

## User Flow

1. User opens a spec in the dashboard.
2. User hovers over any rendered block (paragraph, list item, table row, code line).
3. An inline `LineagePopover` opens listing the ADR(s) that have ever touched any source line in that block — deduplicated by ADR doc path, ordered by most-recent contribution.
4. User clicks an ADR row → navigates to that ADR's detail page. Other blocks remain interactive.

## Component Changes

### mclaude-docs-mcp

New tool `get_line_lineage({doc_path})`. Returns the block-level structure described in the API shape decision above. Internally:

1. Resolve `HEAD-sha` from the watcher's in-memory state (fallback `git rev-parse HEAD`).
2. Consult `line_lineage_cache(doc_path, head_sha)` — on hit, deserialize `response_json` and return.
3. On miss: parse the doc's current content with `micromark` (+`gfmTable` extension) and walk events to produce block spans. Block kinds: `prose` (one paragraph → one block, spanning its soft-wrapped source lines), `list_item` / `table_row` / `code_line` (one source line → one block).
4. For each block, run `git log -L<start>,<end>:<doc_path> -M -w --format='%H|%s|%cs'` once. This yields every commit that touched any source line in the block range, with sha, subject, and ISO commit date (`%cs`).
5. For each commit: resolve its co-committed doc paths by running `git show --name-only --format= <sha>` (cached per-sha for the request's lifetime) and filtering to tracked `docs/**/*.md` files. Partition into ADR docs (`docs/adr-*.md`), spec docs (`docs/**/spec-*.md`), and "other." On a spec page, `contributors` = union of ADR docs across the block's commits; on an ADR page, `contributors` = union of spec docs. `non_adr_commits` = every commit that produced no contributor doc match for the requested direction (pure refactors, typo fixes, or docs-of-the-wrong-category), rendered muted.
6. Dedupe `contributors` by `doc_path`; sort by `last_commit` descending. Sort `non_adr_commits` by `commit_date` descending. Compute each block's `block_id` per the Block ID derivation rule.
7. Serialize the result, INSERT OR REPLACE into `line_lineage_cache`, and return.

Cache pruning helper (new export from `line-lineage.ts`):

```ts
export function pruneLineLineageCache(db: Database, repoRoot: string): void {
  const head = getHeadCommit(repoRoot); // already exported from lineage-scanner.ts
  if (!head) return; // non-git context — nothing to prune
  db.run("DELETE FROM line_lineage_cache WHERE head_sha != ?", [head]);
}
```

On dashboard boot, `boot.ts` calls `pruneLineLineageCache(db, repoRoot)` — signature mirrors `runLineageScan(db, repoRoot)`, and `getHeadCommit` is already imported in the scanner.

### mclaude-docs-dashboard

- New HTTP endpoint `/api/line-lineage?doc=<p>` in `routes.ts`. `handleLineLineage` imports `getLineLineage` directly from `mclaude-docs-mcp/line-lineage` (the new module) — the same direct-module-import pattern `routes.ts` already uses for `getLineage` from `mclaude-docs-mcp/tools`. No IPC or MCP wire protocol on this path. Caller direction (spec-ward vs ADR-ward) is inferred server-side from the `doc`'s category.
- `MarkdownView.tsx`: when line-lineage is active for the viewed doc, replace the current `marked` render path with `dangerouslySetInnerHTML` of the server-returned `html`. Delegate a single `onMouseOver` / `onMouseOut` handler on the outer div — it walks up from `event.target` via `closest("[data-block-id]")` to find the hovered block, looks it up in the `blocks` array, and opens the `LineagePopover` variant for line-lineage. The existing per-H2 `≡` popover and per-H1 doc-level popover remain unchanged (the server HTML preserves the heading placeholders those affordances already use).
- SSE: `MarkdownView` subscribes to the existing `{type: "reindex", changed: string[]}` SSE event broadcast by `server.ts`. When `changed.includes(activeDocPath)` is true, the UI dismisses any open line-lineage popover and surfaces a brief toast ("Doc updated"). It then re-fetches `/api/line-lineage?doc=<p>` to refresh the HTML + blocks. No new SSE event type is introduced.
- `SpecDetail` and `AdrDetail` both render via the updated `MarkdownView`. No other changes to these two routes.
- Boot: `boot.ts` calls `pruneLineLineageCache(db, repoRoot)` after `runLineageScan` (ADR-0029) and before `startWatcher`. Failure is non-fatal (logged, continues).

## Data Model

New SQLite table in the docs DB:

```sql
CREATE TABLE line_lineage_cache (
  doc_path     TEXT NOT NULL,
  head_sha     TEXT NOT NULL,
  response_json TEXT NOT NULL,  -- full JSON response for this (doc, head), including html + blocks
  computed_at  INTEGER NOT NULL, -- unix seconds; for cache-age debugging
  PRIMARY KEY(doc_path, head_sha)
);

CREATE INDEX idx_line_lineage_cache_doc ON line_lineage_cache(doc_path);
```

Schema version: this ADR bumps `SCHEMA_VERSION` in `mclaude-docs-mcp/src/db.ts` from `"3"` to `"4"`. `openDb` will drop and rebuild the docs DB on first boot after deploy; the lineage table will be re-populated by the existing `runLineageScan` and the line-lineage cache will populate lazily on first access per doc.

Invalidation:

- **On HEAD advance** (watcher signal): identify the changed doc paths via `git diff --name-only <prev_head> <new_head>`. For changed docs, `DELETE FROM line_lineage_cache WHERE doc_path IN (<changed>)`. For unchanged docs, `UPDATE line_lineage_cache SET head_sha = :new_head WHERE head_sha = :prev_head AND doc_path NOT IN (<changed>)` — the computed answer is still valid, so we just re-stamp the key to the new HEAD. This keeps the steady-state cache warm across commits that do not touch the doc being read.
- **On boot**: `DELETE FROM line_lineage_cache WHERE head_sha != :current_head_sha` once. Bounds steady-state cache to ≤1 row per `doc_path` and cleans up any stale rows from crashes mid-advance.

## Error Handling

| Failure | Behavior |
|---------|----------|
| `git log -L` not available (non-git context) | Return empty array; UI renders normally without the line-level overlay. |
| Cold fetch exceeds 10s | Let it finish. There is no timeout in v1 — once complete, the result is cached and subsequent loads are instant. If this becomes a problem in practice, revisit with a parallel-fan-out or SSE streaming approach. |
| Doc is not in the repo (e.g. draft on disk only) | 404 from endpoint; UI skips overlay. |
| Markdown parse fails | Return empty array + log the parse error. UI skips overlay (no partial blocks — all-or-nothing for a given doc). |

## Security

Unchanged. Read-only. Blame output contains only commit hashes + author names already present in the repo history that the dashboard can already see via `docs/adr-*.md` content.

## Impact

Specs updated in this commit:

- `docs/mclaude-docs-mcp/spec-docs-mcp.md` — new `line_lineage_cache` table row under `### Tables`, schema version bump `"3"` → `"4"`, new `line-lineage.ts` module §, new `get_line_lineage` MCP tool §, and `pruneLineLineageCache` / `invalidateLineLineageCacheOnHeadAdvance` helpers.
- `docs/mclaude-docs-dashboard/spec-dashboard.md` — new `/api/line-lineage` endpoint under `## HTTP API`, `MarkdownView` per-block hover affordance, boot sequence addition (`pruneLineLineageCache` after `runLineageScan`), SSE `reindex`-driven popover dismissal + toast.

`docs/spec-state-schema.md` is not touched: per precedent (ADR-0027), the docs-mcp SQLite tables live under `docs/mclaude-docs-mcp/spec-docs-mcp.md` rather than the global state-schema spec.

Code components affected (implemented by `/feature-change`):

- `mclaude-docs-mcp/package.json` — add `micromark` + `micromark-extension-gfm-table` deps.
- `mclaude-docs-mcp/src/tools.ts` — `getLineLineage` implementation + zod schema.
- `mclaude-docs-mcp/src/index.ts` — MCP tool registration.
- `mclaude-docs-mcp/src/line-lineage.ts` — new module: block detection (micromark walker), `git log -L` runner, co-commit resolver reuse, cache read/write, `pruneLineLineageCache`.
- `mclaude-docs-mcp/src/db.ts` — `CREATE TABLE line_lineage_cache` DDL.
- `mclaude-docs-mcp/src/watcher.ts` — on HEAD advance, before updating the local `lastHead` variable, call the new `invalidateLineLineageCacheOnHeadAdvance(db, repoRoot, lastHead, currentHead)` helper exported from `line-lineage.ts`. That helper runs `git diff --name-only <prev> <new>` to get the changed paths, issues the `DELETE` for changed docs and the `UPDATE` for unchanged rows, and returns. The watcher then updates `lastHead = currentHead` as it does today. The watcher does not import SQL helpers directly — all DB mutations for line-lineage live in `line-lineage.ts`.
- `mclaude-docs-dashboard/src/routes.ts` — `/api/line-lineage` handler.
- `mclaude-docs-dashboard/src/boot.ts` — call `pruneLineLineageCache` after `runLineageScan`, before `startWatcher`.
- `mclaude-docs-dashboard/ui/src/components/MarkdownView.tsx` — wrap each block in a hover target; fetch + associate blocks by `block_id`.
- `mclaude-docs-dashboard/ui/src/components/LineagePopover.tsx` — new rendering mode for line-lineage (contributors + muted non-ADR rows).
- `mclaude-docs-dashboard/ui/src/components/Toast.tsx` (new or reused) — "Doc updated" toast.
- `mclaude-docs-dashboard/ui/src/api.ts` — `fetchLineLineage(doc_path)`.

## Scope

**In v1:**

- `/api/line-lineage?doc=<p>` endpoint.
- `get_line_lineage` MCP tool.
- `line_lineage_cache` SQLite table + invalidation + boot prune.
- Per-block hover `LineagePopover` on both spec pages and ADR pages.
- Muted non-ADR commit rows inside each popover.
- SSE-driven popover dismissal + toast on live edits.

**Deferred:**

- Diff-view modal for non-ADR commits (click a muted row → `git show`).
- Per-field drill-down deep-linking a spec line to the exact ADR paragraph that drove it (inverse of current direction; requires line-range attribution on both sides).
- Parallel fan-out or SSE streaming fetch — only needed if v1's serial fetch proves too slow on large specs.
- Client-side fuzzy block-id remap on live edits — dismissed in favour of dismiss + toast.

## Open questions

(None — all resolved.)

## Implementation Plan

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| `mclaude-docs-mcp` — `line-lineage.ts` module | ~300 prod + ~400 tests | ~100k | Block detection + git log -L batching + cache read/write. Unit tests need fixture repo. |
| `mclaude-docs-mcp` — `db.ts` + `tools.ts` + `index.ts` wiring | ~60 prod + ~80 tests | ~40k | DDL + schema + tool registration. |
| `mclaude-docs-mcp` — `watcher.ts` invalidation + UPDATE path | ~50 prod + ~120 tests | ~60k | HEAD-advance diff + cache re-stamp. Integration test against fixture repo. |
| `mclaude-docs-dashboard` — `routes.ts` handler + `boot.ts` prune | ~40 prod + ~100 tests | ~40k | Thin pass-through + boot hook. |
| `mclaude-docs-dashboard/ui` — `MarkdownView` block wrapping + SSE dismissal + toast | ~200 prod + ~200 tests | ~90k | Biggest UI change. Component tests for hover interactions + SSE. |
| `mclaude-docs-dashboard/ui` — `LineagePopover` line-mode rendering | ~80 prod + ~120 tests | ~50k | Contributors + muted rows. Snapshot tests. |
| `mclaude-docs-dashboard/ui` — `api.ts` fetcher | ~20 prod + ~40 tests | ~20k | Trivial. |

**Total estimated tokens:** ~400k
**Estimated wall-clock:** ~1.5h of 5h budget (~27%)

### How this was estimated

- `line-lineage.ts` is the only genuinely new subsystem. Block detection via micromark events + batched `git log -L` calls plus cache serialization → ~300 production lines, ~400 test lines covering block-kind classification, cache hit/miss/update, co-commit resolution, and error paths.
- UI changes reuse existing `LineagePopover` styling; the new work is block wrapping inside `MarkdownView` and the SSE-driven dismissal/toast. Biggest risk is hover interaction tests needing fine-grained user-event simulation.
- No new K8s, helm, or CLI work.
