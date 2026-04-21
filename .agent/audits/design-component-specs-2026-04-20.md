## Run: 2026-04-20T17:21:50Z

**Gaps found: 6**

1. **"9 in parallel" contradicts "10 components" throughout** — The Ordering decision (line 30) says "All 9 in parallel" but the Decisions cell for "Which components get specs" says "All 10 backend components" and then names only 9 (session-agent, control-plane, server, cli, connector, relay, mcp, common, charts/mclaude — mclaude-docs-mcp is absent from that list). The Scope section says "Author 10 component-local specs … all in parallel." The Deferred section says "Running spec-evaluator against all 9 new specs." The Implementation Plan table has "10 component specs." A developer cannot determine how many specs to author in the initial batch (is docs-mcp included or not?) and whether parallelism count is 9 or 10.
   - **Doc**: `| Ordering | All 9 in parallel …` (line 30) vs `| Which components get specs | All 10 backend components: session-agent … charts/mclaude |` (line 25, which lists 9 names and calls it 10) vs `Author 10 component-local specs from code + ADR cross-reference, all in parallel.` (line 125) vs `Running spec-evaluator against all 9 new specs` (line 133)
   - **Code**: N/A (no specs authored yet)

2. **Helm spec folder path is inconsistent** — The Component Inventory table lists `charts/mclaude` with folder `charts/mclaude/` (the code root). The Spec-Evaluator Update table maps the `helm` component to `docs/charts-mclaude/spec-helm.md`. The naming convention in `docs/spec-doc-layout.md` says component folders use `docs/mclaude-<component>/` (mirroring `ls mclaude-*`). The charts folder is at `charts/mclaude/` in the codebase — it doesn't follow the `mclaude-<component>` pattern. ADR-0020 (`docs/adr-0020-docs-per-component-folders.md`) references `docs/charts/` as the intended lazy folder. A developer implementing this has three different candidate paths (`docs/charts-mclaude/`, `docs/charts/`, `docs/charts/mclaude/`) with no canonical answer.
   - **Doc**: `| \`charts/mclaude\` | \`charts/mclaude/\` | …` (line 49) vs `| \`helm\` | \`docs/charts-mclaude/spec-helm.md\` …` (line 105)
   - **Code**: `docs/adr-0020-docs-per-component-folders.md` line 108 uses `docs/charts/`; `docs/spec-doc-layout.md` naming rule says `docs/mclaude-<component>/`. The actual chart code root is `charts/mclaude/`.

3. **Spec-evaluator component roots table not addressed** — The ADR says to update `.agent/agents/spec-evaluator/AGENT.md` component table to reference the new component-local specs (the "Read these" table). The `spec-evaluator` AGENT.md also has a separate "Component roots" table (lines 55–60 of AGENT.md) that maps component names to code root paths. This table currently lists only 5 components: control-plane, session-agent, spa, cli, helm. The 6 new components (server, connector, relay, mcp, common, mclaude-docs-mcp) have no entry. A spec-evaluator run on `server` or `connector` after this ADR would have no code root to scan. The ADR does not mention this table at all.
   - **Doc**: `The component table in \`.agent/agents/spec-evaluator/AGENT.md\` is updated to reference the new specs` (line 93) — refers only to the "Read these" table
   - **Code**: `.agent/agents/spec-evaluator/AGENT.md` lines 55–60 contain the "Component roots" table with only 5 entries; 6 new components would be unreachable.

4. **Spec-evaluator SKILL.md component list not specified** — The Impact section lists `.agent/skills/spec-evaluator/SKILL.md — component table update` (line 118) as an item to update, and the Implementation Plan budgets 10k tokens for it. The SKILL.md currently enumerates 5 components (`control-plane | session-agent | spa | cli | helm`) in its `## Usage` line and its "All components" parallel-spawn block. The ADR does not say what the new full list should be, what the new usage line should say, or what new `Agent(...)` calls to add to the parallel block. A developer cannot implement this without deciding which components the skill should cover and what prompts to use for the 6 new ones.
   - **Doc**: `- \`.agent/skills/spec-evaluator/SKILL.md\` — component table update` (line 118) with no further specification
   - **Code**: `.agent/skills/spec-evaluator/SKILL.md` line 30 `**component**: \`control-plane\` | \`session-agent\` | \`spa\` | \`cli\` | \`helm\``; lines 53–59 spawn exactly 5 agents. No new component roots, prompts, or summary entries are specified for the 6 new components.

5. **Dev-harness AGENT.md per-component category tables not addressed** — The dev-harness AGENT.md has per-component category tables (e.g. `### control-plane`, `### session-agent`, `### spa`, `### cli`, `### helm`) that define the implementation categories, production code targets, and test requirements for each component. After this ADR, six new components (server, connector, relay, mcp, common, mclaude-docs-mcp) will have specs, but the dev-harness has no category tables for them. The ADR Impact section says "`.agent/agents/dev-harness/AGENT.md` — discovery table update (if needed)" — the conditional "if needed" leaves this unresolved. If spec-evaluator finds gaps against the new specs, dev-harness would be invoked on these components but has no guidance on what to build or test.
   - **Doc**: `- \`.agent/agents/dev-harness/AGENT.md\` — discovery table update (if needed).` (line 117)
   - **Code**: `.agent/agents/dev-harness/AGENT.md` has category tables only for control-plane, session-agent, spa, cli, helm (lines 148–206). No entries for server, connector, relay, mcp, common, mclaude-docs-mcp.

6. **Spec-gap detection trigger condition in plan-feature is ambiguous** — The Workflow Change section (lines 136–145) describes adding spec-gap detection to plan-feature Step 4b: glob `docs/<component>/spec-*.md` for each component in the ADR's "Component Changes" section; if no spec exists, draft one. The ADR does not define what "each impacted component" maps to when "Component Changes" lists a non-UI component that already has a spec folder but no spec for the specific sub-topic touched (e.g., `mclaude-docs-mcp` might get `spec-docs-mcp.md` from this ADR, but a later ADR adds a new topic). The check is `glob docs/<component>/spec-*.md` — if any spec exists, the check passes even if the new behavior is entirely uncovered. A developer implementing this rule cannot determine whether the check is "does any spec file exist for this component" (the glob check described) or "does this specific behavior have spec coverage" (what the motivation implies).
   - **Doc**: `1. For each component in the ADR's "Component Changes" section, glob \`docs/<component>/spec-*.md\`. 2. If no spec exists, draft one from code` (lines 140–142)
   - **Code**: N/A (this is a behavioral spec for the SKILL.md update). The stated check would pass for `mclaude-docs-mcp` if `spec-docs-mcp.md` exists, regardless of whether the new ADR's behavior is documented there.

## Run: 2026-04-20T23:00:00Z

Re-audit after 6-gap fix round. Checking each claimed fix and scanning for new gaps.

### Prior gap verification

| # | Prior gap | Resolved? | Evidence |
|---|-----------|-----------|---------|
| 1 | Count inconsistency (9 vs 10) | Yes | ADR now uses "9" throughout: Ordering decision, Scope v1, Deferred, Implementation Plan all say 9 |
| 2 | Helm spec folder path | Yes | New "Helm Spec Folder Path" section (lines 176-184) canonicalizes `docs/charts-mclaude/spec-helm.md` with rationale |
| 3 | Spec-evaluator Component roots table | Yes | New subsection "AGENT.md — 'Component roots' table" (lines 115-130) shows all 10 entries explicitly |
| 4 | Spec-evaluator SKILL.md changes | Yes | New subsections specify component enum (line 137), parallel block additions (lines 140-148), doc discovery table additions (lines 154-162) |
| 5 | Dev-harness category tables | Yes | New paragraph (lines 164-174) explicitly defers category tables with rationale: specs don't exist yet at authoring time |
| 6 | Spec-gap detection scope | Yes | Lines 220-225 explicitly separate the two concerns (file-exists check vs. behavior-described check) |

CLEAN — no blocking gaps found.

All 6 prior gaps are resolved. No new blocking gaps were introduced.

**Notes on fixes reviewed:**
- The Helm Spec Folder Path section references "ADR-0020's convention at line 108 (`docs/charts/`)" — the line number is inaccurate (ADR-0020 line 108 shows `<other mclaude-*>/` in a diagram, not `docs/charts/`; the `docs/charts/` mention is in the Scope/Deferred section). However, this is a prose reference error, not an ambiguity that would cause a developer to create the folder at the wrong path. The canonical path `docs/charts-mclaude/spec-helm.md` is unambiguous.
- The SKILL.md parallel block addition says "adds 5 new agents" and "The summary block adds matching lines." The summary format is not shown, but the existing 5-line pattern (`### <component>: N gaps`) is sufficient for a developer to derive the 5 new entries without stopping to ask.
- The Impact section entry "`feature-change/SKILL.md` — spec location table update (if needed)" — the actual feature-change SKILL.md already has an up-to-date component-local row in its spec-location table; no update is required. The conditional is correctly qualified.
