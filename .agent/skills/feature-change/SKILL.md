---
name: feature-change
description: Universal entry point for any change to the mclaude app — new features, bug fixes, refactors, config changes, anything. Always starts with the spec. If the spec already describes correct behavior, skip to the dev-harness agent. If not, run /spec-change first.
---

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

## The Loop

```
1. Read the relevant spec docs
2. Determine the spec relationship:
   A. Spec is correct, code is wrong (bug)      → skip to step 5
   B. Spec doesn't describe this yet (feature)  → run /spec-change (step 3)
   C. Spec needs updating (behavior change)     → run /spec-change (step 3)
   D. Spec and code will both change (refactor) → run /spec-change if behavior changes, otherwise skip to step 5
3. /spec-change <description> — updates spec docs and commits them
4. (handled by /spec-change)
5. Invoke the dev-harness agent for each affected component:
   Agent(subagent_type="dev-harness", prompt="<component> — <brief description>")
```

This order is mandatory. The spec always reflects intended behavior. If the code doesn't match the spec, the code is wrong — fix the code. If the spec doesn't describe the desired behavior, run `/spec-change` first, then invoke the dev-harness agent.

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

## Step 2 — Spec relationship

**A — Bug (spec correct, code wrong):**
The spec describes the desired behavior. The code doesn't match. Skip to step 5 — the spec doesn't need updating, just the code. Example: spec says `natsUrl` is omitted when empty, code was returning the internal cluster URL.

**B — New feature (spec missing):**
The spec doesn't mention this at all. You must add it before writing code. Example: `projects.create → control-plane` wasn't in the spec, it needed to be added.

**C — Behavior change (spec needs updating):**
The spec describes the old behavior. You're changing the behavior. Update the spec to describe the new behavior, then update the code. Example: changing JWT expiry from 8h to 24h needs a spec update if expiry is documented.

**D — Refactor (behavior unchanged):**
The spec describes the correct behavior already. The code is restructured but externally identical. Skip to step 5 if no behavior changes. If the refactor exposes a spec gap (something was undocumented), add it now.

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

## Step 5 — dev-harness agent per component

For each affected component, invoke the dev-harness agent:

```
Agent(subagent_type="dev-harness", prompt="<component> — <brief description of what was specced>")
```

The agent reads the spec, audits Phase 1 (spec → code) and Phase 2 (code → tests), implements gaps, runs tests, and commits. It runs to convergence independently.

---

## Step 5b — Spec evaluator loop (mandatory after every dev-harness pass)

After the dev-harness agent completes, run the spec-evaluator to exhaustively compare the spec against the actual code. Loop until the evaluator returns CLEAN.

```
Loop:
  1. /spec-evaluator <component>
     - Output: list of gaps (or "CLEAN" if none)
  2. If gaps found:
     → Agent(subagent_type="dev-harness", prompt="<component> — fix these gaps: <list>")
     → go to step 1
  3. If CLEAN: proceed to Step 6
```

**Rules:**
- Never report a task complete until the evaluator returns CLEAN
- One failing evaluator gap = one more dev-harness agent pass
- Evaluator runs after EVERY dev-harness pass, not just the first
- **Never deprioritize any gap** — every gap goes to dev-harness immediately
- If a gap cannot be implemented due to environment constraints, run `/spec-change` to update the spec, then re-evaluate

---

## Step 6 — Validate (SPA changes only)

After CI deploys the preview, use the **Playwright MCP** to validate the golden path directly in the browser. Do not stop at "build passes" — drive the browser through the actual user flow.

```
Validation checklist for spa changes:
1. Navigate to the preview URL (format: http://preview-{branch-slug}.{tailscale-ip}.sslip.io)
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

## Reference

- `docs/plan-k8s-integration.md` — backend architecture, NATS subjects, KV
- `docs/plan-client-architecture.md` — client architecture, stores, viewmodels
- `docs/ui-spec.md` — UI wireframes and behavior
- `docs/feature-list.md` — feature IDs
- Component roots: `mclaude-control-plane/`, `mclaude-web/`, `mclaude-session-agent/`, `mclaude-cli/`, `charts/mclaude/`
