---
name: spec-evaluator
description: Exhaustive spec compliance audit for a single component. Reads all spec docs and all production code, then lists every gap where spec says X but code doesn't implement X. Run after every dev-harness pass. Loop until CLEAN.
---

# Spec Evaluator

Audits one component against its spec. Produces an exhaustive gap list, or CLEAN.

## Usage

```
/spec-evaluator [component]
```

**component**: `control-plane` | `session-agent` | `spa` | `cli` | `helm`

<<<<<<< HEAD
Omit to audit **all** components in parallel. Each component runs as a separate subagent so they finish concurrently.

=======
>>>>>>> origin/main
---

## What it does

Reads every relevant spec doc in full, reads every production source file in the component, then for each spec statement asks: does the code implement this?

Reports only real gaps — spec says X, code doesn't do X. Does not report things the spec is silent about. Does not categorize anything as deferred or optional.

---

## Spec docs per component

| Component | Read these |
|-----------|-----------|
| `control-plane` | `docs/plan-k8s-integration.md` |
| `session-agent` | `docs/plan-k8s-integration.md` |
| `spa` | `docs/ui-spec.md`, `docs/plan-client-architecture.md` |
| `cli` | `docs/plan-k8s-integration.md` |
| `helm` | `docs/plan-k8s-integration.md`, `charts/mclaude/` |

---

<<<<<<< HEAD
## Algorithm — single component
=======
## Algorithm
>>>>>>> origin/main

```
1. Read all spec docs for the component in full
2. Read all production source files under the component root
3. For each statement in the spec that describes behavior/structure:
   - Does corresponding code exist?
   - Does it behave as described?
<<<<<<< HEAD
4. Save to .agent/audits/spec-<component>-<YYYY-MM-DD>.md (append if exists)
5. Output: CLEAN or one GAP: line per gap
```

## Algorithm — all components (no argument)

Spawn one subagent per component in parallel, each running the single-component algorithm above. Wait for all to finish, then print a combined summary:

```
### control-plane: N gaps
### session-agent: N gaps
### spa:           N gaps
### cli:           N gaps
### helm:          N gaps

See .agent/audits/ for full per-component reports.
=======
4. Output: CLEAN or one GAP: line per gap
>>>>>>> origin/main
```

---

## Output format

```
CLEAN
```
or:
```
GAP: "<spec quote>" → <what the code does or doesn't do>
GAP: "<spec quote>" → <what the code does or doesn't do>
...
```

Every gap line must:
- Quote the exact spec text
- Describe specifically what is missing or wrong in the code (file + rough location if possible)

---

## Rules

- **Never** mark a gap as deferred, optional, low priority, or future work
- **Never** report things the spec doesn't say (missing tests, style issues, etc.)
- **Only** report: spec says X, code does not do X
- If a gap cannot be implemented due to environment constraints, that must be noted in the spec itself — update the spec to reflect the constraint, then re-evaluate

---

<<<<<<< HEAD
## Saving results

**Always** write the full output to `.agent/audits/spec-<component>-<YYYY-MM-DD>.md` before doing anything else with the result. Append if the file exists (multiple runs per day).

Format:

```markdown
## Run: <ISO timestamp>

<CLEAN or all GAP: lines>
```

Create `.agent/audits/` if it doesn't exist. This is mandatory — auditing history must be preserved.

---

=======
>>>>>>> origin/main
## After running

If CLEAN: the component is spec-complete. Report to the calling skill.

If gaps found: pass each gap to `/dev-harness <component>` immediately. After dev-harness completes, re-run `/spec-evaluator <component>`. Loop until CLEAN.

---

## Reference

- Component roots: `mclaude-control-plane/`, `mclaude-web/`, `mclaude-session-agent/`, `mclaude-cli/`, `charts/mclaude/`
- Spec docs: `docs/plan-k8s-integration.md`, `docs/plan-client-architecture.md`, `docs/ui-spec.md`
