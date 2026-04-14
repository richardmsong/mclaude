---
name: plan-feature
description: Design a new feature through structured Q&A. Produces a design doc (which IS the spec) after resolving all ambiguities. Also owns spec maintenance — handles backpressure when dev-harness discovers spec gaps during implementation.
user_invocable: true
---

# Plan Feature

Structured design session for a new feature. Produces a design document — which IS the spec — after resolving all ambiguities with the user.

Design docs (`docs/plan-*.md`) are the canonical spec. There is no separate spec layer. What the design doc says is what gets built. What gets built must match the design doc.

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
1. Research
2. Draft design + question list
3. Ask questions (AskUserQuestion)
4. Repeat steps 2-3 until no ambiguities remain
5. Write design document
6. Design audit (/design-audit) until CLEAN
7. Hand off to /feature-change
```

---

## Step 1 — Research

Read everything relevant before forming opinions:

- **Existing design docs**: `docs/plan-*.md` — these are the spec
- **UI spec**: `docs/ui-spec.md`
- **Feature list**: `docs/feature-list.md`
- **Existing code**: grep for related patterns, interfaces, types
- **Current state**: what's already built that this feature touches?

Use the Explore agent for broad codebase research. Use Grep/Glob for targeted lookups.

The goal is to understand:
- What exists today that this feature builds on or replaces
- What components are affected
- What patterns the codebase already uses for similar things
- What constraints exist (NATS subjects, K8s resources, auth model, UI patterns)

---

## Step 2 — Draft design + question list

Write a short design sketch covering:

1. **User-facing flow**: What does the user see and do, step by step?
2. **Component responsibilities**: Which components change and how?
3. **Data model**: New tables, KV entries, NATS subjects, K8s resources?
4. **Integration points**: How does this connect to existing systems?

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

After the user answers, incorporate their decisions and check: are there new ambiguities revealed by their choices? If yes, draft follow-up questions and ask again (back to step 2).

**Keep going until there are zero unresolved ambiguities.** A design with open questions is not done.

**After each round of answers, explicitly audit for remaining ambiguity.** Walk through the entire design end-to-end — every data flow, every error path, every integration point — and ask yourself: "Could I implement this right now without guessing?" If the answer is no anywhere, formulate the ambiguity as a question and ask it. Keep doing this until you can honestly say there are zero open questions. Do not ask the user "is there anything else?" — it's your job to find the gaps, not theirs.

---

## Step 4 — Write design document

Once all questions are answered, write the design to `docs/plan-<feature-slug>.md`:

```markdown
# <Feature Name>

## Overview
One paragraph: what this is, why it exists, what it enables.

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

## Scope
What's in v1. What's explicitly deferred.
```

---

## Step 5 — Design audit

Run `/design-audit docs/plan-<feature-slug>.md` to verify the design document is self-sufficient.

This calls the `design-evaluator` agent in a loop. The evaluator has no conversation context — it reads only the design document and the codebase. Between rounds, `/design-audit` classifies gaps (factual vs design decision), fixes factual ones, asks the user about decisions, and re-runs until CLEAN. All findings, fixes, and decisions are logged to `.agent/audits/`.

Do not hand off to `/feature-change` until the audit passes.

---

## Step 6 — Hand off

After the design document is written, audited, and committed:

```
The design is complete at docs/plan-<feature-slug>.md.
Run /feature-change to implement.
```

Do NOT write code yourself. The design document is the output. `/feature-change` implements it.

---

## Design doc editing rules

These rules apply whenever a design doc is edited — during initial creation (step 4), during audit fixes (step 5), or during backpressure from dev-harness.

**Adding something:**
- Add it to the right section with full payload/schema/wireframe
- Describe inputs, outputs, error cases, failure modes
- If it's a NATS subject: include subject pattern, publisher, subscriber, payload
- If it's a UI element: include wireframe and behavior bullets

**Removing something:**
- Delete it entirely — no stale text, no "deprecated" markers
- Remove every reference to it across the design doc

**Changing something:**
- Update in place — old text out, new text in
- Update every place it appears in the doc

**Never:**
- Leave design doc and implementation out of sync
- Write implementation details (function names, file paths) in the design doc
- Describe future/intended behavior — only what will be built now

**UI-specific rules** (when editing UI sections):
- Wireframes: update ASCII art to match what will be rendered exactly
- Every interactive element has a behavior bullet: label, validation, default, on-submit behavior

**Commit rule:** Design doc changes are always committed separately from code changes:
```bash
git add docs/
git commit -m "spec(<area>): <what changed and why>"
```

---

## Backpressure from dev-harness

During implementation, `/feature-change` runs the dev-harness → spec-evaluator loop. Sometimes the dev-harness agent discovers that the design doc is ambiguous, incomplete, or wrong. This is **backpressure** — the implementation pushes back on the spec.

When `/feature-change` encounters backpressure, the spec update follows these rules:

### 1. Classify the gap

| Gap type | Action |
|----------|--------|
| **Factual error** (wrong endpoint, incorrect field name, stale reference) | Fix directly in the design doc. No user input needed. |
| **Missing detail** with obvious answer (from codebase/architecture) | Fill it in directly. |
| **Missing detail** requiring a design decision | Ask the user via AskUserQuestion. Batch related questions. |
| **Contradiction** (doc says X in one place, Y in another) | Determine correct answer from context. If genuinely ambiguous, ask the user. |

### 2. Edit the design doc

Follow the editing rules above.

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
- **Don't write code** — this skill produces a design document only (and edits design docs during backpressure)
- **Don't skip research** — uninformed questions waste the user's time
- **Don't present false choices** — if there's only one reasonable option, state it as your recommendation and ask if they agree
- **Don't ask about implementation details** — ask about behavior, scope, and architecture. Implementation is for the dev-harness.
