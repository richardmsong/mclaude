---
name: plan-feature
description: Design a new feature through structured Q&A. Produces an ADR (and updates impacted specs) after resolving all ambiguities. Also owns spec maintenance — handles backpressure when dev-harness discovers spec/ADR gaps during implementation.
user_invocable: true
---

# Plan Feature

Structured design session for a new feature. Produces an **ADR** at `docs/adr-YYYY-MM-DD-<slug>.md` — and updates any impacted specs (`docs/spec-*.md`) in the same commit — after resolving all ambiguities with the user.

ADRs are dated, immutable records of individual decisions. Specs are living, present-tense descriptions of the current design. Git co-commits between an ADR and the specs it touches form the **lineage edge** that the `docs` MCP surfaces via `get_lineage`. This is load-bearing: without the co-commit, future agents cannot discover why a spec section looks the way it does.

## Usage

```
/plan-feature <description of the feature>
```

Examples:
- `/plan-feature GitHub OAuth for repo access`
- `/plan-feature multi-user support with per-user namespaces`
- `/plan-feature session sharing — let users share a session URL with a teammate`

---

## Algorithm

```
1. Research (read relevant specs + related ADRs via docs MCP)
2. Draft design + question list
3. Ask questions (AskUserQuestion)
4. Repeat steps 2-3 until no ambiguities remain
5. Write the ADR + update impacted specs
6. Design audit (/design-audit) until CLEAN
7. Commit ADR + spec edits together (single spec commit)
8. Hand off to /feature-change
```

---

## Step 1 — Research

Use the `docs` MCP instead of grepping the whole `docs/` tree:

- `list_docs category=spec` — see every living spec.
- `search_docs` — keyword search across ADRs and specs.
- `get_lineage` on a spec section — returns the ADRs that previously modified it. Read those first.
- `get_section` — targeted reads once you've identified a relevant section.

Also read:
- **Feature list**: `docs/feature-list.md` — feature IDs and platform support.
- **Existing code**: grep for related patterns, interfaces, types.

Use the Explore agent for broad codebase research. Use Grep/Glob for targeted lookups.

The goal is to understand:
- What exists today that this feature builds on or replaces
- Which specs will be touched (state-schema, ui, tailscale-dns, etc.)
- Which prior ADRs shaped the relevant spec sections — so this ADR extends the lineage rather than contradicting it silently
- What components are affected
- What constraints exist (NATS subjects, K8s resources, auth model, UI patterns)

---

## Step 2 — Draft design + question list

Write a short design sketch covering:

1. **User-facing flow**: What does the user see and do, step by step?
2. **Component responsibilities**: Which components change and how?
3. **Data model**: New tables, KV entries, NATS subjects, K8s resources?
4. **Integration points**: How does this connect to existing systems?
5. **Spec impact**: Which `docs/spec-*.md` files will be updated in the same commit as the ADR?

Then identify **every ambiguity** — places where you need a decision from the user. Categorize them:

- **Architecture**: fundamental choices that shape the whole design
- **Behavior**: what happens in specific scenarios
- **Scope**: what's in v1 vs deferred
- **UX**: how the user interacts with it

---

## Step 3 — Ask questions

Use `AskUserQuestion` to resolve ambiguities. Rules:

- **Batch questions by theme** — up to 4 questions per AskUserQuestion call
- **Provide concrete options** with descriptions explaining trade-offs
- **Use previews** for UI mockups or code snippets when comparing approaches
- **Put your recommended option first** with "(Recommended)" in the label
- **Don't ask yes/no questions** — offer real alternatives
- **Don't ask questions you can answer from the code** — only ask about decisions
- **Always include design ramifications in the question text** — explain the tradeoffs and consequences of each choice directly in the question, not just in the option descriptions. The user should be able to understand what each choice means for the system without having to ask "what are the ramifications?"

After the user answers, incorporate their decisions and check: are there new ambiguities revealed by their choices? If yes, draft follow-up questions and ask again (back to step 2).

**Keep going until there are zero unresolved ambiguities.** A design with open questions is not done.

**After each round of answers, explicitly audit for remaining ambiguity.** Walk through the entire design end-to-end — every data flow, every error path, every integration point — and ask yourself: "Could I implement this right now without guessing?" If the answer is no anywhere, formulate the ambiguity as a question and ask it. Keep doing this until you can honestly say there are zero open questions. Do not ask the user "is there anything else?" — it's your job to find the gaps, not theirs.

---

## Step 4 — Write the ADR + update impacted specs

**All output goes in a single working tree change** that will be committed together. The co-commit is the lineage edge.

### 4a. Write the ADR

Write the decision record to `docs/adr-YYYY-MM-DD-<slug>.md`. Use today's date (absolute, not relative) and a kebab-case slug.

```markdown
# ADR: <Feature Name>

## Overview
One paragraph: what this is, why it exists, what it enables.

## Motivation
Why this change is being made now. Incident, user request, scalability pressure, or other trigger.

## Decisions
Key decisions made during design, with rationale.

| Decision | Choice | Rationale |
|----------|--------|-----------|

## User Flow
Step-by-step from the user's perspective.

## Component Changes

### <Component 1>
What changes, new endpoints/subjects/types, behavior.

### <Component 2>
...

## Data Model
New tables, KV entries, NATS subjects, K8s resources. Full schemas.

## Error Handling
What can go wrong and how each failure is surfaced.

## Security
Auth, token storage, scope, revocation.

## Impact
Which specs are updated in this commit (`docs/spec-state-schema.md`, `docs/spec-ui.md`, etc.).
Which components implement the change.

## Scope
What's in v1. What's explicitly deferred.

## Implementation Plan

Estimated effort to implement this design via dev-harness.

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|

**Total estimated tokens:** N
**Estimated wall-clock:** Xh of Yh budget (Z%)

### How to estimate

Lines of code: count spec lines that describe concrete behavior (endpoints,
subjects, handlers, UI components, schemas). Each spec line typically produces
10-30 lines of production code + 15-40 lines of test code depending on
complexity.

Tokens: dev-harness consumes roughly 50-80k tokens per component category
(build/unit/integration/component/e2e). Multiply categories × 65k as a
baseline, then adjust:
- Simple categories (build, lint, config): ~30k tokens
- Medium categories (unit, mocks, views): ~60k tokens
- Complex categories (integration, component, e2e, failure): ~100k tokens
- First-time component setup: +50k tokens overhead

Budget: the 5h Anthropic API budget ≈ 15M tokens at Sonnet speed. Express
the estimate as a fraction of this budget.
```

### 4b. Update impacted specs

For every cross-cutting surface the feature touches, edit the matching `docs/spec-*.md` **in the same working tree**:

| Change surface | Spec to edit |
|---------------|--------------|
| Persistent state (DB, KV, NATS subjects, K8s resources) | `docs/spec-state-schema.md` |
| UI behavior, screens, design system, interactive element contracts | `docs/spec-ui.md` |
| DNS | `docs/spec-tailscale-dns.md` |
| A feature-local detail with no cross-cutting impact | None — ADR alone is enough |

Do not create new per-feature spec files in v1 unless a clear cross-component concern emerges (see ADR-2026-04-19 for the partitioning policy). Small, feature-local details belong in the ADR only.

When editing a spec, follow the **doc editing rules** below.

---

## Step 5 — Design audit

Run `/design-audit docs/adr-YYYY-MM-DD-<slug>.md` to verify the ADR is self-sufficient.

This calls the `design-evaluator` agent in a loop. The evaluator has no conversation context — it reads only the ADR (and referenced specs) plus the codebase. Between rounds, `/design-audit` classifies gaps (factual vs design decision), fixes factual ones, asks the user about decisions, and re-runs until CLEAN. All findings, fixes, and decisions are logged to `.agent/audits/`.

Do not commit or hand off until the audit passes.

---

## Step 6 — Commit (single spec commit)

Stage the new ADR together with any spec edits and commit once:

```bash
git add docs/
git commit -m "spec(<area>): <what changed and why>"
```

**This is the lineage edge.** The `docs` MCP reads co-commits to compute `get_lineage`. If the ADR is committed separately from the specs it modifies, lineage does not link them and future agents will have to re-read all ADRs to understand why a spec section exists.

Only `docs/` is staged in this commit. Code changes go through `/feature-change`'s dev-harness loop and commit separately.

---

## Step 7 — Hand off

After the ADR + spec edits are committed:

```
The design is complete at docs/adr-YYYY-MM-DD-<slug>.md
(and updates spec-<concern>.md). Run /feature-change to implement.
```

Do NOT write code yourself. The ADR + spec edits are the output. `/feature-change` implements the code.

---

## Doc editing rules

These rules apply whenever a doc is edited — during initial creation (step 4), during audit fixes (step 5), or during backpressure from dev-harness.

### ADRs are immutable

- ADR content is historical. Do not rewrite past decisions. If a later decision supersedes an earlier one, author a **new** ADR dated today that describes the supersession, and add a one-line `> Superseded by adr-YYYY-MM-DD-<slug>.md` note near the affected section (or at the top of the old ADR).
- Mechanical updates to an old ADR are allowed: fixing a broken cross-reference when a file is renamed, fixing a typo, restoring a broken link. These are not semantic changes.
- Never edit an ADR to change *what it decided* — author a new one instead.

### Specs are living

When editing a `docs/spec-*.md`:

**Adding something:**
- Add it to the right section with full payload/schema/wireframe
- Describe inputs, outputs, error cases, failure modes
- If it's a NATS subject: include subject pattern, publisher, subscriber, payload
- If it's a UI element: include wireframe and behavior bullets

**Removing something:**
- Delete it entirely — no stale text, no "deprecated" markers
- Remove every reference to it across the spec

**Changing something:**
- Update in place — old text out, new text in
- Update every place it appears in the spec

**Never:**
- Leave specs and implementation out of sync
- Write implementation details (function names, file paths) in a spec
- Describe future/intended behavior — only what is true now or will be true after this ADR lands

**UI-specific rules** (when editing `docs/spec-ui.md`):
- Wireframes: update ASCII art to match what will be rendered exactly
- Every interactive element has a behavior bullet: label, validation, default, on-submit behavior

### Commit rule

ADR + impacted specs are always committed together in a single spec-only commit, separate from any code changes:

```bash
git add docs/
git commit -m "spec(<area>): <what changed and why>"
```

---

## Backpressure from dev-harness

During implementation, `/feature-change` runs the dev-harness → spec-evaluator loop. Sometimes the dev-harness agent discovers that the ADR or a spec is ambiguous, incomplete, or wrong. This is **backpressure** — the implementation pushes back on the spec.

When `/feature-change` encounters backpressure, the doc update follows these rules:

### 1. Classify the gap

| Gap type | Action |
|----------|--------|
| **Factual error** (wrong endpoint, incorrect field name, stale reference) | Fix directly in the relevant doc. No user input needed. |
| **Missing detail** with obvious answer (from codebase/architecture) | Fill it in directly. |
| **Missing detail** requiring a design decision | Ask the user via AskUserQuestion. Batch related questions. |
| **Contradiction** (doc says X in one place, Y in another) | Determine correct answer from context. If genuinely ambiguous, ask the user. |

### 2. Decide which doc to edit

| What's wrong | Edit |
|--------------|------|
| Gap is in the ADR you just wrote | Edit the ADR (it hasn't been "historicized" by a later decision yet — still in the same workstream) |
| Gap is in a spec the ADR references | Edit the spec |
| Gap exposes a behavior that has no ADR (undocumented historical behavior) | Author a **new corrective ADR** dated today that describes the behavior and rationalizes it |
| A previously-decided ADR needs to be overridden | Author a **new superseding ADR** dated today — do not rewrite the old one |

### 3. Commit separately

```bash
git add docs/
git commit -m "spec(<area>): <what changed — backpressure from dev-harness>"
```

### 4. Return to `/feature-change`

Which re-invokes dev-harness with the updated spec. The loop continues until spec-evaluator returns CLEAN.

---

## Anti-patterns

- **Don't assume answers** — if you're not sure, ask
- **Don't ask one question at a time** — batch them, the user's time is valuable
- **Don't write code** — this skill produces an ADR and spec edits only (and edits docs during backpressure)
- **Don't skip research** — uninformed questions waste the user's time; the docs MCP makes research cheap
- **Don't present false choices** — if there's only one reasonable option, state it as your recommendation and ask if they agree
- **Don't ask about implementation details** — ask about behavior, scope, and architecture. Implementation is for the dev-harness.
- **Don't rewrite old ADRs** — supersede them with a new ADR instead
- **Don't split the ADR commit from the spec commit** — the co-commit is the lineage edge

---

## Skill authoring conventions (when the output is a SKILL.md)

These apply whenever `plan-feature` is designing a new skill (i.e. the ADR describes changes to `.agent/skills/<name>/SKILL.md`):

**External binaries**
- List every required binary in a `## Prerequisites` section with a one-line install command.
- Always invoke binaries by name only — never hardcode an absolute path (e.g. `nats`). Rely on PATH.
- Example:
  ```bash
  ## Prerequisites
  which nats   # install: brew install nats-io/nats-tools/nats
  which kubectl
  which helm
  ```

**Idempotency**
- All setup steps must be safe to re-run (`--dry-run=client -o yaml | kubectl apply -f -`, `helm upgrade --install`, etc.).

**No hardcoded user paths**
- No `/Users/<name>/...` paths anywhere in a skill. Use env vars (`$HOME`, `$KUBECONFIG`) or relative paths.
