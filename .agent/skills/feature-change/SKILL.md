---
name: feature-change
description: Universal entry point for any change to the mclaude app — new features, bug fixes, refactors, config changes, anything. Reads design docs as the spec. Runs dev-harness → spec-evaluator loop until CLEAN. Handles spec backpressure via /plan-feature rules.
user_invocable: true
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

## Design docs are the spec

Design docs (`docs/plan-*.md`) are the canonical spec for mclaude. There is no separate spec layer.

| What's changing | Read these design docs |
|----------------|-----------|
| Control-plane (auth, provisioning, NATS subjects, HTTP endpoints) | `docs/plan-k8s-integration.md` |
| Session-agent (session lifecycle, Claude process, KV, failure modes) | `docs/plan-k8s-integration.md` |
| SPA / client (screens, stores, viewmodels, NATS pub/sub) | `docs/ui-spec.md`, `docs/plan-client-architecture.md` |
| Cross-cutting (new NATS subject, new KV bucket, new shared API) | `docs/plan-k8s-integration.md` + `docs/plan-client-architecture.md` |
| Feature-specific subsystem | `docs/plan-<feature>.md` |

Also check `docs/feature-list.md` for feature IDs and platform support matrix.

---

## The Loop

```
1. Read the relevant design docs
2. Classify the change:
   A. Design doc correct, code wrong (bug)         → skip to step 4
   B. No design doc covers this (new feature)       → run /plan-feature first
   C. Design doc needs updating (change or gap)     → update design doc (step 3)
   D. Refactor (behavior unchanged)                 → skip to step 4
3. Update the design doc, commit spec separately
4. dev-harness → spec-evaluator loop per component (until CLEAN)
5. Validate (SPA changes only)
```

This order is mandatory. The design doc always reflects intended behavior. If the code doesn't match the design doc, the code is wrong — fix the code. If the design doc doesn't describe the desired behavior, fix the design doc first, then fix the code.

---

## Step 1 — Read the relevant design docs

Read the full section of every design doc that covers the area being changed. Context matters — don't just search for a keyword.

---

## Step 2 — Classify the change

**A — Bug (design doc correct, code wrong):**
The design doc describes the desired behavior. The code doesn't match. Skip to step 4. Example: design doc says `natsUrl` is omitted when empty, code was returning the internal cluster URL.

**B — New feature (no design doc):**
No design doc covers this feature. The user must run `/plan-feature` first to create the design doc. Do not write code without a spec. Tell the user:
```
No design doc covers this feature. Run /plan-feature <description> to create one, then re-run /feature-change.
```

**C — Behavior change or spec gap (design doc needs updating):**
Either the design doc describes old behavior you're changing, OR the design doc is silent on behavior that should be specified (e.g. error handling, edge cases, loading states). In both cases: update the design doc first to describe the intended behavior, commit separately, then proceed to step 4. For non-trivial changes, ask the user to confirm before committing.

A missing spec is NOT the same as "spec correct, code wrong" (A). If the spec doesn't say what should happen, classify as C and fill in the spec — don't skip to step 4 and leave the gap undocumented.

**D — Refactor (behavior unchanged):**
The design doc already describes the correct behavior. The code is restructured but externally identical. Skip to step 4. If the refactor reveals a spec gap (something undocumented), update the design doc first.

---

## Step 3 — Update the design doc

Follow the design doc editing rules defined in `/plan-feature`:

- Add to the right section with full payload/schema/behavior
- Remove entirely — no stale text
- Change in place — old text out, new text in
- Never leave design doc and code out of sync

Commit spec changes separately from code:
```bash
git add docs/
git commit -m "spec(<area>): <what changed and why>"
```

---

## Step 4 — dev-harness → spec-evaluator loop (exhaustive)

For each affected component, invoke the dev-harness agent **and keep re-invoking until all gaps are closed**:

```
Loop:
  1. Agent(subagent_type="dev-harness", prompt="<component> — <description>. Fix ALL spec gaps.")
  2. When the agent returns, run /spec-evaluator <component>
  3. If gaps remain:
     a. CODE gap (spec says X, code doesn't do X):
        → Agent(subagent_type="dev-harness", prompt="<component> — fix these gaps: <list>")
        → go to step 2
     b. SPEC gap (design doc is ambiguous/incomplete/wrong):
        → Handle backpressure (see below)
        → go to step 1
  4. If CLEAN: proceed to Step 5
```

The dev-harness agent has maxTurns=500 and is instructed to keep going until all gaps are closed. But if it hits context limits and returns with gaps remaining, **you must re-invoke it immediately** with the remaining gap list. Each re-invocation picks up from the last commit and continues.

### Handling backpressure

When dev-harness or spec-evaluator reports a gap that is actually a spec problem (ambiguity, missing detail, contradiction in the design doc), follow the backpressure rules from `/plan-feature`:

1. **Classify**: factual error → fix directly. Missing detail with obvious answer → fill in. Design decision needed → ask the user via AskUserQuestion.
2. **Edit** the design doc following `/plan-feature` editing rules.
3. **Commit** spec change separately from code.
4. **Re-invoke** dev-harness with the updated spec.

### Rules

- **Never report a task complete until the spec-evaluator returns CLEAN**
- One failing evaluator gap = one more dev-harness pass (or one spec update)
- Evaluator runs after EVERY dev-harness pass, not just the first
- **Never deprioritize any gap** — every gap gets handled immediately
- If a gap cannot be implemented due to environment constraints, update the design doc to reflect reality, then re-evaluate
- Running the dev-harness agent once and summarizing results is NOT acceptable — the loop must close

---

## Step 5 — Validate (SPA changes only)

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
- **Design docs** (`docs/plan-*.md`, `docs/ui-spec.md`, `docs/feature-list.md`) — spec updates in step 3
- **Skill files** (`.agent/skills/`) — process improvements
- **Memory files** — feedback, project context

The master session must **never** directly edit:
- `mclaude-control-plane/` — use dev-harness
- `mclaude-web/` — use dev-harness
- `mclaude-session-agent/` — use dev-harness
- `mclaude-cli/` — use dev-harness
- `charts/mclaude/` — use dev-harness

All implementation changes go through dev-harness subagents. The master session classifies, updates specs, orchestrates agents, and evaluates results — it does not write code, templates, or config.

---

## Reference

- `docs/plan-k8s-integration.md` — backend architecture, NATS subjects, KV
- `docs/plan-client-architecture.md` — client architecture, stores, viewmodels
- `docs/ui-spec.md` — UI wireframes and behavior
- `docs/plan-*.md` — feature-specific design docs (each is a spec)
- `docs/feature-list.md` — feature IDs
- Component roots: `mclaude-control-plane/`, `mclaude-web/`, `mclaude-session-agent/`, `mclaude-cli/`, `charts/mclaude/`
