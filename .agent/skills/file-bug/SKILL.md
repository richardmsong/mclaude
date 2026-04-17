# File Bug

Record a bug with spec context so it's actionable for a future `/feature-change` session. Does not fix the bug — just documents it.

Bugs live in `.agent/bugs/` as numbered markdown files. When a bug is fixed (by dev-harness), it moves to `.agent/bugs/fixed/`.

## Usage

```
/file-bug <description>
```

Examples:
- `/file-bug login response returns internal natsUrl when NATS_WS_URL is empty`
- `/file-bug session-agent KV key format doesn't include location segment`
- `/file-bug SPA heartbeat monitor doesn't unsubscribe on session switch`

---

## Algorithm

### Step 1 — Identify the spec

Determine which design doc covers the buggy behavior:

| Area | Design doc |
|------|-----------|
| Control-plane, auth, provisioning | `docs/plan-k8s-integration.md` |
| Session-agent, session lifecycle | `docs/plan-k8s-integration.md` |
| SPA, stores, viewmodels | `docs/ui-spec.md`, `docs/plan-client-architecture.md` |
| Feature-specific | `docs/plan-<feature>.md` |

Read the relevant section to confirm the spec describes the correct behavior.

### Step 2 — Locate the bug in code

Find the specific file(s) and line(s) where the behavior diverges from the spec. Use Grep/Glob/Read — don't guess.

### Step 3 — Check for existing findings

Check `.agent/bugs/` for duplicates and `.agent/audits/` for spec-evaluator results that already identified this gap:

```bash
ls .agent/bugs/
grep -r "<keyword>" .agent/audits/
```

If an audit already caught it, reference the finding. If a bug already exists, update it instead of creating a duplicate.

### Step 4 — Assign the next bug ID

Read existing files in `.agent/bugs/` to find the highest ID:

```bash
ls .agent/bugs/*.md | sort -V | tail -1
```

Increment by 1. Format: `NNN` (zero-padded to 3 digits).

### Step 5 — Write the bug file

Create `.agent/bugs/{NNN}-{slug}.md`:

```markdown
# BUG-{NNN}: {Short title}

**Severity**: Critical | High | Medium | Low
**Component**: {control-plane | session-agent | spa | cli | helm}
**Reported**: {YYYY-MM-DD}
**Status**: open

## Symptoms

{What the user sees or what fails. Observable behavior.}

## Root Cause

{Why it happens. Reference specific code paths. If unknown, say "Needs investigation" and list hypotheses.}

## Spec Reference

**Spec**: `{path-to-design-doc.md}`, section "{section name}"

{Quote or paraphrase the spec's description of correct behavior.}

## Evidence

{Logs, error messages, audit findings, code references with file:line.}

## Audit Reference

{Link to .agent/audits/ finding, or "none — found during manual review".}

## Fix

{Describe the fix approach. Reference specific files and what needs to change.}

## Files

{List of affected files with line numbers.}
```

### Step 6 — Optionally create a GitHub issue

If the bug is user-facing or needs tracking beyond the local file:

```bash
gh issue create \
  --title "bug({component}): {short description}" \
  --body "$(cat .agent/bugs/{NNN}-{slug}.md)"
```

### Step 7 — Report

Display the bug ID, file path, severity, and fix path.

---

## Rules

- **Don't fix the bug.** This skill documents only. Use `/feature-change` to fix.
- **Always reference the spec.** If no spec covers this behavior, note that — it may need `/plan-feature` first.
- **Always locate the code.** A bug report without file:line is not actionable.
- **Check for duplicates first.** Don't duplicate bugs that already exist in `.agent/bugs/`.
- **Severity guide:**
  - **Critical** — blocks core functionality, no workaround
  - **High** — core feature unreliable or degraded
  - **Medium** — misleading UX or incorrect but non-blocking behavior
  - **Low** — cosmetic, enhancement, or edge case
