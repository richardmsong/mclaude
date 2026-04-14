---
name: spec-change
description: Update the mclaude spec for any change — new features, bug fixes, behavior changes. Writes spec docs and commits them. Does NOT implement code. After this, invoke the dev-harness agent to implement.
---

# Spec Change

Updates the spec for any change to the mclaude app. Features, bug fixes, refactors, config changes, UI tweaks — anything that requires a spec update goes through this skill first.

For pure bug fixes where the spec already correctly describes the intended behavior, skip straight to invoking the dev-harness agent.

## Usage

```
/spec-change <description of the change>
```

Examples:
- `/spec-change add project creation to control-plane`
- `/spec-change login returns wrong natsUrl to browser clients`
- `/spec-change remove server URL field from login screen`
- `/spec-change increase JWT expiry to 24h`

---

## The Loop

```
1. Read the relevant spec docs
2. Determine the spec relationship:
   A. Spec is correct, code is wrong (bug)      → spec needs no update; go straight to dev-harness
   B. Spec doesn't describe this yet (feature)  → update spec (step 3)
   C. Spec needs updating (behavior change)     → update spec (step 3)
   D. Refactor (behavior unchanged)             → update spec only if behavior changes; otherwise go to dev-harness
3. Update the spec doc (see below)
4. Commit: spec only, no code
   git add docs/
   git commit -m "spec(<area>): <what changed and why>"
5. Invoke the dev-harness agent for each affected component:
   Agent(subagent_type="dev-harness", prompt="<component> — <brief description of what was specced>")
```

---

## Step 1 — Read the relevant spec

| What's changing | Read these |
|----------------|-----------|
| Control-plane (auth, provisioning, NATS subjects, HTTP endpoints) | `docs/plan-k8s-integration.md` |
| Session-agent (session lifecycle, Claude process, KV, failure modes) | `docs/plan-k8s-integration.md` |
| SPA / client (screens, stores, viewmodels, NATS pub/sub) | `docs/ui-spec.md`, `docs/plan-client-architecture.md` |
| Cross-cutting (new NATS subject, new KV bucket, new shared API) | both `plan-k8s-integration.md` and `plan-client-architecture.md` |
| New subsystem with no existing doc | Create `docs/spec-<name>.md` (see below) |

Always read the full section, not just the specific line. Context matters.

---

## Step 3 — Update or create the spec

**Which doc to update:**

| Doc | Owns |
|-----|------|
| `docs/plan-k8s-integration.md` | NATS subjects, KV schema, session lifecycle, provisioning, failure modes, HTTP endpoints |
| `docs/plan-client-architecture.md` | Stores, viewmodels, protocol contract, accumulation algorithm, NATS pub/sub from client |
| `docs/ui-spec.md` | Screens, wireframes, fields, labels, interactions, visual states |
| `docs/feature-list.md` | Feature IDs and platform support matrix |

**If the change doesn't fit any existing doc** (new subsystem, new protocol, new integration), create `docs/spec-<name>.md`:

```markdown
# <Feature Name>

## Overview
One paragraph: what this is, why it exists, what problem it solves.

## Spec

[Subjects, endpoints, payloads, schemas, behavior, failure modes.
 Write exactly what will be built. No future work.]

## Component Responsibilities

| Component | Responsibility |
|-----------|---------------|
```

Then add a one-line entry to `docs/feature-list.md`.

**Rules for editing any spec doc:**

Adding something:
- Add it to the right section with full payload/schema/wireframe
- Describe inputs, outputs, error cases, failure modes
- If it's a NATS subject: add to the subjects table with the owning component
- If it's a UI element: add to the wireframe and add a behavior bullet

Removing something:
- Delete it entirely — no stale text, no "deprecated" markers
- Remove every reference to it across all spec docs

Changing something:
- Update in place — old text out, new text in
- Update every place it appears

Never:
- Leave spec and implementation out of sync
- Write implementation details (function names, file paths) in the spec
- Describe future/intended behavior — only what will be built now

**UI-specific rules** (when editing `docs/ui-spec.md`):
- Wireframes: update ASCII art to match what will be rendered exactly
- Every interactive element has a behavior bullet: label, validation, default, on-submit behavior
- Removing a field: delete from wireframe, delete behavior bullet, note in commit if any store value it drove is still used elsewhere
- Adding a field: add to wireframe, add behavior bullet, update `plan-client-architecture.md` if it needs a new store value

---

## Step 4 — Commit spec only

```bash
git add docs/
git commit -m "spec(<area>): <what changed and why>"
```

Never bundle spec and code in the same commit. The commit message must say what changed and why — not what file was edited.

---

## Step 5 — Invoke dev-harness agent

For each affected component, invoke the dev-harness agent:

```
Agent(subagent_type="dev-harness", prompt="<component> — <brief description of what was specced>")
```

The agent reads the spec, audits what's implemented vs what the spec requires, implements the gaps, runs tests, and commits. It runs to convergence independently — no hand-holding needed.

After the agent completes, check the summary it returns. If there are remaining gaps or it hit a spec ambiguity, run `/spec-change` again to resolve, then re-invoke the agent.

---

## Reference

- `docs/plan-k8s-integration.md` — backend architecture, NATS subjects, KV
- `docs/plan-client-architecture.md` — client architecture, stores, viewmodels
- `docs/ui-spec.md` — UI wireframes and behavior
- `docs/feature-list.md` — feature IDs
- Component roots: `mclaude-control-plane/`, `mclaude-web/`, `mclaude-session-agent/`, `mclaude-cli/`, `charts/mclaude/`
