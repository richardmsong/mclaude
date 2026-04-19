---
name: feature-change
description: Universal entry point for any change to the mclaude app — new features, bug fixes, refactors, config changes, anything. Authors an ADR for the request, updates impacted specs, and runs dev-harness → spec-evaluator loop until CLEAN. Handles spec backpressure via /plan-feature rules.
user_invocable: true
---

Base directory for this skill: /Users/rsong/work/mclaude/.claude/skills/feature-change

# Feature Change

The entry point for **any change to any part of the mclaude app**. Features, bug fixes, refactors, config changes, UI tweaks, backend changes, helm values — everything goes through this loop.

## Usage

```
/feature-change <description of the change>
```

Examples:
- `/feature-change add project creation to control-plane`
- `/feature-change login returns wrong natsUrl to browser clients`
- `/feature-change refactor auth middleware to use context helper`
- `/feature-change remove server URL field from login screen`
- `/feature-change increase JWT expiry to 24h`
- `/feature-change helm chart missing resource limits on session-agent`

---

## ADRs and Specs

Two kinds of docs live in `docs/`:

- **ADRs** (`docs/adr-YYYY-MM-DD-<slug>.md`): dated, immutable records of individual decisions. One per feature request / change. This is where the *why* is recorded.
- **Specs** (`docs/spec-<concern>.md`): living, present-tense references that describe the current design (data schema, UI contract, DNS, etc.). Updated in place as ADRs introduce changes.

Every change goes through this skill, which **always authors a new ADR** and — when the change has cross-cutting impact — updates the relevant spec(s) **in the same commit**. The co-commit is what the `docs` MCP reads as the lineage edge between an ADR and the spec sections it shaped.

| What's changing | Read these specs + ADRs |
|----------------|-------------------------|
| Persistent state (DB, KV, NATS subjects, K8s resources) | `docs/spec-state-schema.md` + related ADRs |
| UI / SPA behavior | `docs/spec-ui.md` + `docs/adr-2026-04-11-client-architecture.md` |
| DNS | `docs/spec-tailscale-dns.md` |
| Feature-specific subsystem | the relevant `docs/adr-*-<feature>.md` |

Use `search_docs` / `get_lineage` from the `docs` MCP to discover which ADRs previously shaped a spec section before touching it. Avoid re-reading every ADR — let lineage surface the relevant history.

Also check `docs/feature-list.md` for feature IDs and platform support matrix.

---

## The Loop

```
1. Read the relevant specs + related ADRs (via docs MCP)
   — only ADRs in `accepted` or `implemented` status. Drafts, superseded,
     withdrawn ADRs are skipped. (See adr-2026-04-19-adr-status-lifecycle.md.)
2. Classify the change (A/B/C/D)
3. Author a new ADR: docs/adr-YYYY-MM-DD-<slug>.md (status: accepted from the start)
4. Update impacted specs (if any) — same working tree
5. Commit ADR + spec edits together (single spec commit)
6. dev-harness → spec-evaluator loop per component (until CLEAN)
7. Flip ADR status from `accepted` → `implemented` (append history line).
   Commit the status-only update.
8. Validate (SPA changes only)
```

**Every request produces at minimum an ADR.** This is load-bearing — without a per-request ADR, the docs MCP cannot build the lineage edges that let future agents discover *why* a spec section looks the way it does.

If Step 5's co-commit is skipped (ADR committed separately from specs), the lineage edge does not form. Always co-commit.

**Why `accepted` not `draft`:** `/feature-change` ADRs are authored *because a decision has already been made* — the user is asking for the change to happen. There's no pause point before implementation. If you need a drafting pause, the user should use `/plan-feature` instead (which starts in `draft`).

---

## Step 1 — Read the relevant specs + related ADRs

Use the `docs` MCP rather than grepping the whole `docs/` tree:

- `search_docs` for keywords related to the change.
- `get_lineage` on the spec section you expect to touch — returns the ADRs that previously modified it. Read those first to understand prior decisions.
- `list_docs category=spec` to see all living specs.
- `get_section` for targeted reads of sections you've identified.

Context matters — don't skim a keyword match. Read the full spec section and the full body of any prior ADR that shaped it.

---

## Step 2 — Classify the change

| Class | Meaning | ADR needed? | Spec update? |
|-------|---------|-------------|--------------|
| A — bug | Spec correct, code wrong | Yes — records the bug and fix rationale | No |
| B — new feature | No spec/ADR covers it | Yes — route via `/plan-feature` first | Yes, if cross-cutting |
| C — behavior change / spec gap | Spec describes old behavior OR is silent | Yes | Yes |
| D — refactor | Behavior unchanged | Yes — records *why* the refactor is worth doing | Usually no |

**A — Bug:** The spec describes the desired behavior in enough detail that someone reading only the spec could implement the fix. The code simply diverges. Example: spec says `natsUrl` is omitted when empty, code returns the internal URL.

**Litmus test for A:** Can you point to a specific sentence in the spec that the fix restores compliance with? If yes → A. If the fix requires adding behavior, configuration, setup steps, or environmental prerequisites that the spec doesn't mention → C.

**B — New feature:** Tell the user:
```
No ADR or spec covers this. Run /plan-feature <description> to produce an ADR, then re-run /feature-change.
```

**C — Behavior change or spec gap:** Either the spec describes old behavior you're changing, OR the spec is silent on something that should be specified. Author a new ADR describing the change AND update the impacted spec section(s) in the same commit.

**D — Refactor:** Behavior unchanged. Still author an ADR — it records the motivation (debt, readability, test coverage, etc.) so the next person sees why the refactor happened. Usually no spec update.

**Default to C when in doubt.** An undocumented behavior is cumulative cost; an extra ADR is near-zero cost. If the spec doesn't say what should happen, classify C and fill in the spec.

---

## Step 3 — Author the ADR

Create `docs/adr-YYYY-MM-DD-<slug>.md`. Use today's date (absolute, not relative) and a kebab-case slug.

Minimum content:

```markdown
# ADR: <Title>

**Status**: accepted
**Status history**:
- YYYY-MM-DD: accepted

## Overview
What this change is and what it enables. One paragraph.

## Motivation
Why this change is being made. Include the incident, user report, scalability pressure, or other trigger.

## Decisions
| Decision | Choice | Rationale |
|----------|--------|-----------|

## Impact
Which specs are updated in this commit. Which components implement the change.

## Scope
What's in v1. What's explicitly deferred.
```

For bug fixes (class A), the ADR is short — an Overview + Motivation + a one-line Impact is often enough. For features and spec changes (B/C), follow the fuller structure in `/plan-feature`.

---

## Step 4 — Update impacted specs

If the change is cross-cutting (state schema, UI contract, DNS, NATS subjects, etc.), edit the relevant `docs/spec-*.md` **in the same working tree**, to be committed together with the ADR.

Follow the spec editing rules in `/plan-feature` (add with full payload/schema, remove entirely, change in place — no stale text).

If only one spec is impacted, update it. If several are, update all in the same commit.

**Skip this step if no spec is impacted** — e.g., class A bugs, class D refactors. The ADR alone is the commit.

---

## Step 5 — Commit (single spec commit)

Stage both the new ADR and any spec edits, then commit once:

```bash
git add docs/
git commit -m "spec(<area>): <what changed and why>"
```

The co-commit is the lineage edge. If you commit the ADR separately from spec edits (two commits), `get_lineage` will not link them.

Only `docs/` is staged in this commit. Code changes go through the dev-harness in Step 6 and commit separately.

---

## Step 6 — dev-harness → spec-evaluator loop (exhaustive)

For each affected component, invoke the dev-harness agent **and keep re-invoking until all gaps are closed**:

```
Loop:
  1. Agent(subagent_type="dev-harness", prompt="<component> — <description>. Fix ALL spec gaps.")
  2. When the agent returns, run /spec-evaluator <component>
  3. If gaps remain:
     a. CODE gap (spec says X, code doesn't do X):
        → Agent(subagent_type="dev-harness", prompt="<component> — fix these gaps: <list>")
        → go to step 2
     b. SPEC gap (ADR/spec is ambiguous/incomplete/wrong):
        → Handle backpressure (see below)
        → go to step 1
  4. If CLEAN: proceed to Step 7
```

The dev-harness agent has `maxTurns=500` and is instructed to keep going until all gaps are closed. If it hits context limits and returns with gaps remaining, **re-invoke it immediately** with the remaining gap list. Each re-invocation picks up from the last commit and continues.

### Handling backpressure

When dev-harness or spec-evaluator reports a gap that is actually a spec or ADR problem (ambiguity, missing detail, contradiction), follow the rules from `/plan-feature`:

1. **Classify**: factual error → fix directly. Missing detail with obvious answer → fill in. Design decision needed → ask the user via `AskUserQuestion`.
2. **Edit** the relevant doc(s):
   - If the gap is in the ADR you just wrote → edit the ADR.
   - If the gap is in a spec the ADR references → edit the spec.
   - If the gap exposes a missing prior ADR (the current behavior is undocumented historically) → author a new corrective ADR dated today.
3. **Commit** the doc update(s) separately from code.
4. **Re-invoke** dev-harness with the updated spec.

### Rules

- **Never report a task complete until the spec-evaluator returns CLEAN**
- One failing evaluator gap = one more dev-harness pass (or one spec/ADR update)
- Evaluator runs after EVERY dev-harness pass, not just the first
- **Never deprioritize any gap** — every gap gets handled immediately
- If a gap cannot be implemented due to environment constraints, update the ADR/spec to reflect reality, then re-evaluate
- Running the dev-harness agent once and summarizing results is NOT acceptable — the loop must close

---

## Step 7 — Promote ADR to `implemented`

Once every affected component's spec-evaluator returns CLEAN for the scope of this ADR:

1. Edit the ADR's header:
   - Change `**Status**: accepted` → `**Status**: implemented`.
   - Append a new line to `**Status history**` with today's date: `- YYYY-MM-DD: implemented — all scope CLEAN`.
2. Commit **only** the ADR (status header change). No spec edits, no code changes. This is the signal that the decision has landed in code.

```bash
git add docs/adr-YYYY-MM-DD-<slug>.md
git commit -m "spec(<slug>): promote ADR to implemented"
```

The status flip is intentionally a separate commit so lineage lookup distinguishes "shape the spec" (the `draft → accepted` co-commit) from "lands in code" (the `accepted → implemented` ADR-only commit).

Skip this step for ADRs that describe meta-process changes where there is no runtime code to evaluate — those stay in `accepted` (e.g. a `/feature-change` skill rewrite).

---

## Step 8 — Validate (SPA changes only)

After CI deploys the preview, use the **Playwright MCP** to validate the golden path directly in the browser. Do not stop at "build passes" — drive the browser through the actual user flow.

```
Validation checklist for spa changes:
1. Navigate to the preview URL
2. Log in as dev@mclaude.local / dev
3. Assert the changed screen/behavior matches the spec
4. Assert the previous state (before the fix/feature) is gone
5. Test the specific acceptance criteria stated in the original request
```

**Tools**: `mcp__playwright__browser_navigate`, `mcp__playwright__browser_snapshot`,
`mcp__playwright__browser_fill_form`, `mcp__playwright__browser_click`,
`mcp__playwright__browser_wait_for`, `mcp__playwright__browser_evaluate`,
`mcp__playwright__browser_console_messages`

**Diagnostic tips** when something looks wrong:
- `browser_console_messages` — check for JS errors
- `browser_evaluate` — inspect live state (e.g. `() => window._captured`)
- Check NATS JetStream consumer state via port-forwarded NATS monitoring (`kubectl port-forward ... 8222:8222`)
- Check pod logs (`kubectl logs`) to confirm control-plane or session-agent received the request
- Check KV bucket contents via `curl localhost:8222/jsz?streams=1`

Do not report the task complete until Playwright confirms the acceptance criteria are met in the running preview.

---

## Master session write restrictions

The master session (where `/feature-change` runs) may only write to:
- **ADRs** (`docs/adr-*.md`) — authored in Step 3
- **Specs** (`docs/spec-*.md`, `docs/feature-list.md`) — edited in Step 4
- **Skill files** (`.agent/skills/`) — process improvements
- **Agent files** (`.agent/agents/`) — agent instructions
- **Memory files** — feedback, project context

The master session must **never** directly edit:
- `mclaude-control-plane/` — use dev-harness
- `mclaude-web/` — use dev-harness
- `mclaude-session-agent/` — use dev-harness
- `mclaude-cli/` — use dev-harness
- `mclaude-docs-mcp/` — use dev-harness
- `charts/mclaude/` — use dev-harness

All implementation changes go through dev-harness subagents. The master session classifies, authors ADRs, updates specs, orchestrates agents, and evaluates results — it does not write code, templates, or config.

---

## Reference

- `docs/spec-state-schema.md` — canonical persistent state (DB, KV, NATS subjects, K8s resources)
- `docs/spec-ui.md` — UI wireframes and behavior
- `docs/spec-tailscale-dns.md` — DNS conventions
- `docs/adr-*.md` — one per past decision; use `docs` MCP to search
- `docs/feature-list.md` — feature IDs
- `docs/adr-2026-04-19-docs-plan-spec-refactor.md` — ADR #1, establishes the layout this skill operates under
- Component roots: `mclaude-control-plane/`, `mclaude-web/`, `mclaude-session-agent/`, `mclaude-cli/`, `mclaude-docs-mcp/`, `charts/mclaude/`
