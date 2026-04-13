---
name: spec-evaluator
description: Exhaustive spec compliance audit for a single component. Reads all spec docs and all production code, then lists every gap where spec says X but code doesn't implement X. Run after every dev-harness pass. Loop until CLEAN.
---

# Spec Evaluator

Audits one component against its spec. Produces an exhaustive gap list, or CLEAN.

## Usage

```
/spec-evaluator <component>
```

**component**: `control-plane` | `session-agent` | `spa` | `cli` | `helm`

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

## Algorithm

```
1. Read all spec docs for the component in full
2. Read all production source files under the component root
3. For each statement in the spec that describes behavior/structure:
   - Does corresponding code exist?
   - Does it behave as described?
4. Output: CLEAN or one GAP: line per gap
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

## After running

If CLEAN: the component is spec-complete. Report to the calling skill.

If gaps found: pass each gap to `/dev-harness <component>` immediately. After dev-harness completes, re-run `/spec-evaluator <component>`. Loop until CLEAN.

---

## Reference

- Component roots: `mclaude-control-plane/`, `mclaude-web/`, `mclaude-session-agent/`, `mclaude-cli/`, `charts/mclaude/`
- Spec docs: `docs/plan-k8s-integration.md`, `docs/plan-client-architecture.md`, `docs/ui-spec.md`
