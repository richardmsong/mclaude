---
name: spec-evaluator
description: Spec compliance audit for one or all components. Spawns the spec-evaluator agent (fresh context, no conversation history) per component. Saves results to .agent/audits/.
user_invocable: true
---

# Spec Evaluator

Spawns the `spec-evaluator` agent for one or all components. The agent has no conversation context — it reads only spec docs and code.

## Doc discovery

Agents glob ADRs at `docs/adr-*.md` (root only — ADRs never nest) and specs at `docs/**/spec-*.md` (recursive). Component-local specs live under per-component subfolders; components without a dedicated subfolder use only cross-cutting specs + relevant ADRs.

| Component          | Docs                                                                                                                  |
|--------------------|-----------------------------------------------------------------------------------------------------------------------|
| `control-plane`    | `docs/spec-state-schema.md` + any `docs/adr-*.md` referencing control-plane                                           |
| `session-agent`    | `docs/spec-state-schema.md` + any `docs/adr-*.md` referencing session-agent                                           |
| `spa`              | `docs/ui/spec-*.md` (shared) + `docs/ui/mclaude-web/spec-*.md` (web-local) + `docs/adr-0006-client-architecture.md`   |
| `cli`              | Cross-cutting specs + any `docs/adr-*.md` referencing cli                                                             |
| `helm`             | `docs/spec-state-schema.md` + any `docs/adr-*.md` referencing helm                                                    |
| `mclaude-docs-mcp` | `docs/mclaude-docs-mcp/spec-*.md` (lazy — folder doesn't exist yet; spec is created when the first ADR adds content)  |

## Usage

```
/spec-evaluator [component]
```

**component**: `control-plane` | `session-agent` | `spa` | `cli` | `helm`

Omit to audit **all** components in parallel.

---

## Single component

```
Agent({
  subagent_type: "spec-evaluator",
  description: "Spec evaluator: <component>",
  prompt: "Evaluate the <component> component. Component root: <root>. Read all spec docs listed in your instructions for this component."
})
```

The agent saves results to `.agent/audits/spec-<component>-<YYYY-MM-DD>.md` and returns CLEAN or a gap list.

---

## All components (no argument)

Spawn one agent per component **in parallel**:

```
Agent({ subagent_type: "spec-evaluator", prompt: "Evaluate control-plane...", run_in_background: true })
Agent({ subagent_type: "spec-evaluator", prompt: "Evaluate session-agent...", run_in_background: true })
Agent({ subagent_type: "spec-evaluator", prompt: "Evaluate spa...", run_in_background: true })
Agent({ subagent_type: "spec-evaluator", prompt: "Evaluate cli...", run_in_background: true })
Agent({ subagent_type: "spec-evaluator", prompt: "Evaluate helm...", run_in_background: true })
```

Wait for all to complete, then print combined summary:

```
### control-plane: N gaps
### session-agent: N gaps
### spa:           N gaps
### cli:           N gaps
### helm:          N gaps

See .agent/audits/ for full per-component reports.
```

---

## After running

If CLEAN: the component is spec-complete. Report to the calling skill.

If gaps found: the caller (typically `/feature-change`) passes gaps to `dev-harness`, then re-runs this evaluator. Loop until CLEAN.
