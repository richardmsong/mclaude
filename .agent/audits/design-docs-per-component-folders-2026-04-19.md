## Audit: 2026-04-19T00:00:00Z

**Document:** docs/adr-2026-04-19-docs-per-component-folders.md

#### Fixes applied after Round 1

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | Renumber non-determinism | Replaced git-log-based rule with explicit 21-row NNNN→filename mapping derived from date-in-filename + alphabetical slug. | factual |
| 2 | `indexAllDocs` stale-removal under recursion | Spec'd that both `readdirSync` walk (line 102) AND `docPaths` (line 115) must recurse together; noted that decoupling them deletes subfolder docs on every scan. | factual |
| 3 | Watcher nested-filename test missing | Implementation Plan test row expanded to require a test for watcher single-file reindex receiving a nested relative filename. | factual |
| 4 | Conversation Events destination ambiguous | Routed to shared `docs/ui/spec-conversation-events.md` — event types are cross-platform rendering contracts. Rationale added to migration section. | decision (applied under the user's existing shared/local rule) |
| 5 | Screen: Auth / Login unmapped | Routed to shared `docs/ui/spec-auth.md` — login flow and error rules are cross-platform. | decision (same rule) |
| 6 | Token Usage sections unmapped | Both routed to shared `docs/ui/spec-token-usage.md` — calibration and budget-bar semantics are cross-platform. | decision (same rule) |
| 7 | Sheet: Edit Session unmapped | Routed to web-local `docs/ui/mclaude-web/spec-session-detail.md` — concrete in-session sheet widget. | decision (same rule) |
| 8 | plan-feature Step 4b table not enumerated | Added full 8-row replacement table to the component-changes section. | factual |
| 9 | spec-doc-layout.md cross-references | Rewrote the two old-style refs in `spec-doc-layout.md` to use new-style filenames (`adr-0018-...`, `adr-0021-...`) directly, so no find/replace dependency. Also updated two old-style refs in the ADR body. | factual |

#### Fixes applied after Round 2

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | Target Layout tree vs H2 table drift | Rewrote Target Layout tree's `docs/ui/` and `docs/ui/mclaude-web/` listings to enumerate exactly the 17 files produced by the H2 table. | factual |
| 2 | Target Layout missing spec-auth/conv-events/token-usage | Added all three to the `docs/ui/` listing in the tree. | factual |
| 3 | Settings split boundary undefined | Collapsed the split — whole `Screen: Settings` H2 routes to `docs/ui/mclaude-web/spec-settings-web.md`. Added rationale to the less-obvious-calls list. Added a deferred item for extracting a shared settings-keys contract when a second UI actually needs it. | factual |
| 4 | 20 vs 21 ADR count | Global replace of `~20 existing ADRs` → `21 existing ADRs`, `20 existing ADR files` → `21 existing ADR files (including this one)`. Also corrected Impact's "21+ new UI spec files" to the accurate count (17 = 12 shared + 5 web-local). | factual |

#### Fixes applied after Round 3

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | spec-doc-layout says Settings schema = Shared, ADR routes all Settings to web-local | Updated the Settings-schema row in spec-doc-layout.md's shared/local table to say "Local today; promote to shared when a second UI needs the same schema." Matches the ADR's deferred-extraction stance. | factual |

### Result

**CLEAN** after 4 rounds, 14 total gaps resolved (11 factual fixes, 3 design calls applied under the user's existing shared/local rule).

## Run: 2026-04-19T00:01:00Z

**Gaps found: 9**

1. **Renumber algorithm is non-deterministic: all ADRs share the same first-commit date** — The ADR states that NNNN is "determined by ascending first-commit-date per file (`git log --reverse --format=%ad --date=short -- <path>`). Ties broken by ascending slug alphabetical." However, every single one of the 20 existing ADRs has first-commit-date 2026-04-19 (they were all landed in the same bulk-rename commit). This means the entire sort is a tie and the result depends entirely on the alphabetical slug tiebreaker. The document does not enumerate what the resulting NNNN assignments actually are — a developer must independently re-derive them. Without an explicit enumeration (or a guaranteed-reproducible script invocation against a specific commit), two developers running the algorithm independently could produce different numbering if, for example, they run `git log` against different tree states (before vs after staging). The ADR must either enumerate the final NNNN-to-slug mapping explicitly, or specify the exact `git log` command + tree state to use.
   - **Doc**: "NNNN determined by ascending first-commit-date per file (`git log --reverse --format=%ad --date=short -- <path>`). Ties (same first-commit-date across multiple files) broken by ascending slug alphabetical. Sequence starts at `0001`."
   - **Code**: `git log --reverse --format='%ad' --date=short -- docs/adr-*.md` returns 2026-04-19 for all 20 existing ADRs; the tiebreaker slug sort determines the entire ordering but is not enumerated in the doc.

2. **`indexAllDocs` stale-removal query does not cover nested paths** — The ADR says the docs MCP parser will be changed to recurse into `docs/**/*.md`. However, the existing stale-entry-removal query in `content-indexer.ts` (line 118) is `SELECT path FROM documents WHERE path LIKE 'docs/%.md'`. This query already matches nested paths correctly (the `%` wildcard matches slashes in SQLite LIKE). But the current `indexAllDocs` function uses `readdirSync(docsDir)` (flat, non-recursive) to build the `docPaths` list it compares against, so any subfolder files it indexes would never appear in `docPaths` and would be deleted on every full reindex. The ADR says "change directory walk from `docs/*.md` (flat) to `docs/**/*.md` (recursive)" but does not specify that `indexAllDocs`'s stale-removal logic must also build its reference list recursively. Without fixing both the walk and the stale-removal list, the recursive index would delete every subfolder-indexed file on every startup scan.
   - **Doc**: "Parser: change directory walk from `docs/*.md` (flat) to `docs/**/*.md` (recursive) for content indexing on startup and on watcher events."
   - **Code**: `content-indexer.ts:102` uses `readdirSync(docsDir).filter(f => f.endsWith(".md"))` (flat); stale-removal at line 115 builds `docPaths` from the same flat list; any recursively-indexed file would be absent from `docPaths` and unconditionally deleted.

3. **Watcher `filename` path-join is incorrect for nested files** — The watcher's `runReindex` function (watcher.ts:27) joins `join(docsDir, filename)` where `filename` comes from the `fs.watch` callback. On macOS FSEvents with `recursive: true`, `filename` for a nested file is the relative path from the watched directory (e.g., `ui/spec-design-system.md`). The join produces the correct full path. However, there is no test coverage specified for this case, and the ADR says "Parser/classifier tests for nested paths: ~60 [lines]" but does not specify that the watcher's single-file reindex path for nested filenames must be tested. A developer would not know whether to test `indexFile(db, join(docsDir, "ui/spec-design-system.md"), repoRoot)` or a flat filename. This is a gap in the watcher test spec.
   - **Doc**: "Parser/classifier tests for nested paths — Cover `docs/ui/spec-*.md`, `docs/mclaude-*/spec-*.md`, symlink avoidance." No mention of watcher single-file reindex for nested filename events.
   - **Code**: `watcher.ts:29` — `const fullPath = join(docsDir, filename)` — works for nested paths but is unspecified in the test plan.

4. **spec-ui.md split: "Conversation Events" section has no destination** — The section-by-section mapping in Migration Step 3 lists specific H2 headings and their target files, but the H2 heading "Conversation Events" (line 455 of `docs/spec-ui.md`) is not listed. The mapping mentions "Conversation Events, Tab: Terminal → `docs/ui/mclaude-web/spec-screens.md` (part of Session Detail)" only in passing as part of a combined line. However "Conversation Events" is its own H2 heading, distinct from "Screen: Session Detail" and "Tab: Terminal". A developer must guess whether the entire "Conversation Events" section (which includes sub-sections for User Message, Skill Invocation Chip, Assistant Text, Thinking, Tool Use, AskUserQuestion, Tool Result, System Event, Subagent Group — approximately 200 lines) goes to `spec-screens.md`, a new `spec-events.md`, or somewhere else.
   - **Doc**: "Conversation Events, Tab: Terminal → `docs/ui/mclaude-web/spec-screens.md` (part of Session Detail)" — ambiguous: is "Conversation Events" its own destination or is it meant to be grouped under Session Detail?
   - **Code**: `docs/spec-ui.md:454` — `## Conversation Events` is a top-level H2 section with 9 sub-types; it is not an H3 under "Screen: Session Detail".

5. **spec-ui.md split: "Screen: Auth / Login" has no destination** — The section-by-section mapping lists `Screen: *` sections as going to `docs/ui/mclaude-web/spec-screens.md`. But "Screen: Auth / Login" is a `Screen:` heading (line 85 of `docs/spec-ui.md`). It is not explicitly named in the mapping. The mapping says "All `Screen: *`, `Sheet: *` → `docs/ui/mclaude-web/spec-screens.md`" which implies it is covered, but then immediately makes exceptions ("Conversation Events, Tab: Terminal → `docs/ui/mclaude-web/spec-screens.md` (part of Session Detail)" and "Screen: Settings → split"). A developer must determine whether "Screen: Auth / Login" is covered by the general rule or is an exception. Given that the Login screen contains UI elements (error message rules, network error text) that may be considered cross-platform contracts, it's unclear whether it belongs in `spec-screens.md` or a shared file.
   - **Doc**: "All `Screen: *`, `Sheet: *` → `docs/ui/mclaude-web/spec-screens.md`" — does not explicitly list "Screen: Auth / Login" or clarify whether auth/login is web-local or shared.
   - **Code**: `docs/spec-ui.md:84` — `## Screen: Auth / Login` — full H2 section; spec-ui.md describes the Login screen with login error rules that a future iOS client would also need to implement.

6. **spec-ui.md split: "Screen: Token Usage (Global)" and "Overlay: Token Usage (Session)" destinations unspecified** — The mapping does not list these two sections. "All `Screen: *`" would cover "Screen: Token Usage (Global)" → `spec-screens.md`, and "All `Overlay: *`" would cover "Overlay: Token Usage (Session)" → `spec-overlays.md`. However, the Token Usage overlay is opened from the three-dot menu within a session, which is session-specific behavior, while the Token Usage screen is a global navigational screen. Given the mapping already makes exceptions (Settings is split), a developer cannot be sure whether Token Usage sections are covered by the general rules or require their own split decision.
   - **Doc**: Migration Step 3 mapping does not mention "Screen: Token Usage (Global)" or "Overlay: Token Usage (Session)".
   - **Code**: `docs/spec-ui.md:744` and `docs/spec-ui.md:712` — both are H2 sections not referenced in the mapping.

7. **"Sheet: Edit Session" and "Overlay: Three-dot Menu" destinations unspecified in mapping** — The migration mapping lists "All `Overlay: *`, Raw Output Overlay → `docs/ui/mclaude-web/spec-overlays.md`". This general rule would cover "Overlay: Three-dot Menu" (line 667) and "Overlay: Event Detail Modal" (line 637). But "Sheet: Edit Session" (line 688) is a `Sheet:` — the general rule says "All `Screen: *`, `Sheet: *` → `spec-screens.md`" which would place it there. A developer must decide whether a sheet that appears inside a session's overlay context should be grouped with screens or overlays. This is a judgment call not made explicit in the document.
   - **Doc**: "All `Screen: *`, `Sheet: *` → `docs/ui/mclaude-web/spec-screens.md`" vs "All `Overlay: *`, Raw Output Overlay → `docs/ui/mclaude-web/spec-overlays.md`" — "Sheet: Edit Session" is technically a Sheet but contextually an in-session overlay. Not listed explicitly.
   - **Code**: `docs/spec-ui.md:688` — `## Sheet: Edit Session` — H2 section, not explicitly mapped.

8. **`plan-feature/SKILL.md` and `feature-change/SKILL.md` still reference old spec paths** — The ADR's skills/agents table says these files must be updated. The current `plan-feature/SKILL.md` (Step 4b "Update impacted specs" table) still references `docs/spec-ui.md` directly as the target for "UI behavior, screens, design system, interactive element contracts." After the split, there is no longer a single `docs/spec-ui.md` — the correct targets are `docs/ui/spec-*.md` and `docs/ui/mclaude-web/spec-*.md`. The ADR says "Step 4b 'Update impacted specs' table adds per-component and UI-cluster rows" but does not enumerate what those rows are. A developer implementing this step does not know what the updated table should look like — which rows to add, which to remove, and how to describe the UI cluster vs web-local split.
   - **Doc**: "`.agent/skills/plan-feature/SKILL.md` | Step 4b 'Update impacted specs' table adds per-component and UI-cluster rows. Filename template updated to `adr-NNNN-<slug>.md`. Next-number computation: `max(existing) + 1`. Collision policy: bump-and-retry at commit time." Does not enumerate the new table rows.
   - **Code**: `plan-feature/SKILL.md:248-256` — current table references `docs/spec-ui.md` for UI behavior; this path will be deleted after the migration.

9. **ADR's own filename under the new scheme is unspecified** — The ADR introduces the `adr-NNNN-<slug>.md` naming convention and states that this ADR itself is in the same renaming commit. The document is currently named `adr-2026-04-19-docs-per-component-folders.md`. Under the new scheme it will be assigned a number (NNNN), but the ADR does not state what that number will be, nor does it self-reference its new filename. The document references its superseded ADR by old-style path (`adr-2026-04-19-docs-plan-spec-refactor.md`) and the companion spec references `adr-2026-04-19-adr-status-lifecycle.md` and `adr-2026-04-19-docs-plan-spec-refactor.md` by old path. After the rename commit, all cross-references in `spec-doc-layout.md` (which says "per `adr-2026-04-19-docs-plan-spec-refactor.md`" and "per `adr-2026-04-19-adr-status-lifecycle.md`") will be stale unless updated. The ADR's cross-reference update policy ("Mechanical find/replace") covers this, but the ADR itself does not verify that `spec-doc-layout.md`'s internal references to old-style filenames are in scope for the find/replace pass.
   - **Doc**: "Mechanical find/replace across `docs/`, `.agent/`, `.claude/`, top-level `*.md` replacing every occurrence of each old filename with its new name." `spec-doc-layout.md` contains two references to old-style ADR filenames (`adr-2026-04-19-docs-plan-spec-refactor.md` at line 98, `adr-2026-04-19-adr-status-lifecycle.md` at line 116) that are inside a `docs/` file and should be in scope, but this is the file being co-committed — a developer must confirm that the find/replace runs after (not before) the co-commit draft of `spec-doc-layout.md` is written.
   - **Code**: `docs/spec-doc-layout.md:98` — references `adr-2026-04-19-docs-plan-spec-refactor.md` by old-style name; `spec-doc-layout.md:116` — references `adr-2026-04-19-adr-status-lifecycle.md` by old-style name. Both will be stale post-rename unless explicitly included in the find/replace pass.

## Run: 2026-04-20T00:00:00Z

**Round 2 evaluation — post-fix re-read**

**Gaps found: 4**

1. **Target Layout tree contradicts the H2 mapping table for mclaude-web files** — The Target Layout tree at `docs/ui/mclaude-web/` shows three files: `spec-screens.md` (described as "all Screen: / Sheet: sections"), `spec-overlays.md`, and `spec-settings-web.md`. But the H2-to-destination mapping table routes screen/sheet sections to individual per-screen files: `spec-dashboard.md`, `spec-session-detail.md`, `spec-user-management.md` — with no row mapping to `spec-screens.md` at all. `spec-screens.md` does not appear anywhere in the H2 table. A developer following the Target Layout tree would create `spec-screens.md` and put all screen content there; a developer following the H2 table would create `spec-dashboard.md`, `spec-session-detail.md`, and `spec-user-management.md` instead. These two sections are directly contradictory and cannot both be followed.
   - **Doc**: Target Layout tree: `├── spec-screens.md  # all Screen: / Sheet: sections`. H2 table: routes Screen: Dashboard, Sheet: New Session/Project/Filter → `docs/ui/mclaude-web/spec-dashboard.md`; Screen: Session Detail, Tab: Terminal, Sheet: Edit Session → `docs/ui/mclaude-web/spec-session-detail.md`; Screen: User Management → `docs/ui/mclaude-web/spec-user-management.md`. No row maps to `spec-screens.md`.
   - **Code**: `docs/spec-ui.md` — confirms H2 headings: `## Screen: Dashboard` (line 143), `## Screen: Session Detail` (line 379), `## Screen: User Management (admin only)` (line 840), etc.

2. **Target Layout tree omits three shared UI spec files that the H2 mapping table creates** — The H2 mapping table routes three sections to shared `docs/ui/` files that do not appear in the Target Layout tree: `docs/ui/spec-auth.md` (Screen: Auth / Login), `docs/ui/spec-conversation-events.md` (Conversation Events), and `docs/ui/spec-token-usage.md` (Overlay: Token Usage + Screen: Token Usage). A developer building the directory structure from the Target Layout tree would not create these three files. A developer using only the H2 table would create them. The authoritative file list is ambiguous.
   - **Doc**: Target Layout tree lists 10 files under `docs/ui/`: spec-design-system.md, spec-navigation.md, spec-interaction-patterns.md, spec-first-run-flow.md, spec-ptt.md, spec-platform-notes.md, spec-connection-indicator.md, spec-prompt-bar.md, spec-diff-view.md, spec-settings.md. None of spec-auth.md, spec-conversation-events.md, spec-token-usage.md appears. H2 table rows: "Screen: Auth / Login → `docs/ui/spec-auth.md`"; "Conversation Events → `docs/ui/spec-conversation-events.md`"; "Overlay: Token Usage (Session) → `docs/ui/spec-token-usage.md`"; "Screen: Token Usage (Global) → `docs/ui/spec-token-usage.md`".
   - **Code**: `docs/spec-ui.md` — confirmed H2 headings at lines 85, 453, 710, 744.

3. **"Screen: Settings" split row does not specify how to divide the section content** — The H2 mapping table has a single row for "Screen: Settings" with destination `split: shared schema → docs/ui/spec-settings.md; web layout → docs/ui/mclaude-web/spec-settings-web.md`. This is the only row in the entire table that maps one H2 to two destination files. The ADR states that "shared schema" goes to the shared file and "web layout" goes to the web-local file, but the actual Settings H2 section in spec-ui.md (lines 782–838) contains wireframe ASCII art, widget descriptions, error-handling rules, and git-provider loading failure text — all of which appear to be web-local layout, not a settings schema. There is no subsection header in the Settings H2 marking where "schema" ends and "layout" begins. A developer cannot determine which lines are "shared schema" vs "web layout" without a subsection-level specification.
   - **Doc**: "Screen: Settings | split: shared schema → `docs/ui/spec-settings.md`; web layout → `docs/ui/mclaude-web/spec-settings-web.md`". No subsection boundary specified.
   - **Code**: `docs/spec-ui.md:782–838` — the Settings H2 contains no `###` subsection headers that mark a schema/layout boundary; the section is entirely widget-level layout and error-handling rules.

4. **Scope and Impact say "20 existing ADRs" but the mapping table and Migration Step 1 both say 21** — The Scope section says "Rename all 20 existing ADRs to `adr-NNNN-<slug>.md`." The Impact section says "All 20 existing ADR files — mechanical rename." But Migration Step 1 explicitly says "Rename 21 existing ADR files (including this ADR itself)" and the mapping table has 21 rows (0001–0021). There are 21 files currently on disk. The "20" in Scope and Impact is wrong. A developer using those sections as a checklist would stop after renaming 20 files and miss the 21st.
   - **Doc**: Scope: "Rename all 20 existing ADRs". Impact: "All 20 existing ADR files — mechanical rename." Migration Step 1: "Rename 21 existing ADR files (including this ADR itself)". Mapping table: 21 rows.
   - **Code**: `ls docs/adr-*.md | wc -l` → 21 files currently on disk.

## Run: 2026-04-19T03:00:00Z (Round 3)

**Gaps found: 1**

1. **`spec-doc-layout.md` classifies "Settings schema" as Shared, but the ADR migrates all Settings content to the web-local file with no shared file created** — The co-committed living spec `docs/spec-doc-layout.md` (line 52) contains this row in the UI shared/local test table: `| Settings schema (what keys, what values, what defaults) | Shared |`. Under this rule a developer implementing future specs would route any settings-schema content to a shared `docs/ui/spec-settings.md`. But this ADR's migration routes the entire `Screen: Settings` H2 to `docs/ui/mclaude-web/spec-settings-web.md` (whole section, no split), and no `docs/ui/spec-settings.md` is created. The Target Layout tree does not include a `docs/ui/spec-settings.md`. The Scope section defers "Extracting a shared settings-keys contract (`docs/ui/spec-settings.md`)." The result is that the living spec says settings schema is shared, but no such file exists after the migration, and the ADR explicitly does not create one. A developer implementing the spec split would see the spec-doc-layout.md rule and ask: should I create `docs/ui/spec-settings.md` as part of this migration, or not? The ADR says no; spec-doc-layout.md says yes for any future feature that adds settings schema content. The contradiction is between the two documents that are co-committed in the same commit — the living spec's classification table needs to acknowledge the deferred state (e.g., by noting that no shared settings file exists yet, or by omitting the "Settings schema" row until the file is created).
   - **Doc (spec-doc-layout.md:52)**: `| Settings schema (what keys, what values, what defaults) | Shared |`
   - **Doc (ADR Migration, Step 3 note)**: "Screen: Settings → web-local (whole section, no split)… Extracting a shared settings-keys contract is deferred (see Scope) until a second UI component forces it."
   - **Doc (ADR Scope, Deferred)**: "Extracting a shared settings-keys contract (`docs/ui/spec-settings.md`) out of the web-local `spec-settings-web.md` — do when a second UI component actually needs to consume the same settings schema."
   - **Code**: `docs/spec-ui.md:782–838` — `## Screen: Settings` contains widget-level layout and error-handling rules; no schema-vs-layout subsection boundary exists in the current content.

## Run: 2026-04-19T04:00:00Z (Round 4)

CLEAN — no blocking gaps found.

Verified:
- `spec-doc-layout.md` line 52: Settings-schema row now reads "Local today (lives in `docs/ui/mclaude-web/spec-settings-web.md` alongside the web layout). Promote to shared (`docs/ui/spec-settings.md`) when a second UI component needs the same schema." Consistent with ADR's deferred stance.
- All 26 H2 sections of `docs/spec-ui.md` have explicit rows in the H2-to-destination mapping table; count matches.
- Target Layout tree enumerates all 17 destination files (12 shared under `docs/ui/`, 5 web-local under `docs/ui/mclaude-web/`), consistent with Impact section.
- `content-indexer.ts` line numbers (102, 115, 122) match the ADR's code references exactly.
- `watcher.ts` lines 27–31 reference is accurate; nested-filename test is explicitly required in the Implementation Plan test row.
- 21-row NNNN→filename mapping is explicit and authoritative; git-log non-determinism is eliminated.
- ADR self-assigns number 0020 consistently with the mapping table.
- `spec-doc-layout.md` cross-references use `adr-0018-*` and `adr-0021-*` (new-style), not old date-style filenames.
- Scope and Impact both say "21 existing ADRs"; consistent with mapping table and disk count.
- plan-feature Step 4b replacement table (8 rows) is fully enumerated in Component Changes section.
