# Spec: Doc Layout

Canonical rules for where docs live in `docs/` and how they're named. This
is a living spec — `/plan-feature` and `/feature-change` reference it when
deciding where to author an ADR or spec edit. Changes to this file happen
via a new ADR that co-commits with the edits.

## Partitioning

Every doc under `docs/` is one of these:

| Kind | Location | Examples |
|------|----------|----------|
| ADR | `docs/adr-NNNN-<slug>.md` (always root) | `docs/adr-0001-telemetry.md` |
| Cross-cutting spec | `docs/spec-<concern>.md` | `docs/spec-state-schema.md`, `docs/spec-tls-certs.md` |
| UI-cluster shared spec | `docs/ui/spec-<topic>.md` | `docs/ui/spec-design-system.md` |
| UI-component-local spec | `docs/ui/<component>/spec-<topic>.md` | `docs/ui/mclaude-web/spec-screens.md` |
| Component-local spec | `docs/<component>/spec-<topic>.md` | `docs/mclaude-control-plane/spec-control-plane.md` |
| Inventory | `docs/feature-list.md` (unique) | `docs/feature-list.md` |

### Deciding where a new spec lives

Walk the checklist in order:

1. **Does it describe state, interfaces, or behavior that spans 2+ components?**
   → Cross-cutting. Root: `docs/spec-<concern>.md`.
2. **Is it UI, and does it describe a contract any UI component (web, iOS,
   future) would also implement?** → UI cluster shared:
   `docs/ui/spec-<topic>.md`.
3. **Is it UI, and specific to one UI component's screens / widgets /
   platform APIs?** → UI-component-local:
   `docs/ui/<component>/spec-<topic>.md`.
4. **Is it local to one non-UI component?** → Component-local:
   `docs/<component>/spec-<topic>.md`.
5. **Otherwise** → don't author a spec. Behavior stays in the ADR only,
   and may be promoted to a spec later when a second ADR needs to stay
   consistent with it.

### The UI shared/local test

Inside the UI cluster, a spec is **shared** if it describes a flow or
contract that another UI component would also have to implement. It is
**local** if it's a concrete widget implementation, screen layout, or
platform-API-specific detail.

| Type of content | Shared or local |
|-----------------|-----------------|
| Design system tokens, typography, color palette | Shared |
| Navigation model, screen transitions | Shared |
| Interaction patterns (gestures, keyboard shortcuts) | Shared |
| First-run flow | Shared (every UI implements the same flow) |
| Settings schema (what keys, what values, what defaults) | Local today (lives in `docs/ui/mclaude-web/spec-settings-web.md` alongside the web layout). Promote to shared (`docs/ui/spec-settings.md`) when a second UI component needs the same schema. |
| Push-to-talk behavior (the user-facing contract) | Shared |
| Connection indicator semantics | Shared |
| Prompt bar input contract | Shared |
| Inline diff view contract | Shared |
| Platform notes (cross-platform) | Shared |
| A specific `Screen: Dashboard` layout | Local |
| `Overlay: Event Detail` layout | Local |
| Web DOM / Web Audio implementation details | Local |
| Web-specific Settings layout | Local |

## Naming

### ADRs

`adr-NNNN-<slug>.md`, always at `docs/` root. Rules:

- **NNNN**: monotonic global counter, zero-padded to 4 digits. Computed at
  commit time as `max(existing) + 1`. If the chosen number is already
  taken at commit time (another draft committed first), bump and retry.
- **slug**: short kebab-case summary (2–5 words, ~30 chars max).
- **No date in filename.** Dates live in the ADR's `Status history`.

Example: `adr-0042-github-oauth.md`.

### Specs

`spec-<topic>.md`. Rules:

- `spec-` prefix is required regardless of folder depth — the docs MCP
  classifier relies on it.
- Keep files small. Prefer multiple topic files (`spec-design-system.md`,
  `spec-navigation.md`) over one monolith.
- Inside a component folder (`docs/<component>/`), topic usually starts
  broad (`spec-<component>.md`) and splits into topics as content grows.

### Folders

- Component folders: `docs/mclaude-<component>/` using the full package
  name. Mirrors `ls mclaude-*` in the repo root.
- UI cluster: `docs/ui/`.
- UI components: `docs/ui/mclaude-<component>/`.
- **Lazy creation.** A folder exists only when it holds at least one doc.

## ADR ↔ spec co-commit

The rule from `adr-0021-docs-plan-spec-refactor.md` still holds: an ADR
and the specs it modifies commit together in one `spec(<area>): …`
commit. This is the lineage edge the docs MCP indexes. The commit spans
whatever directories it needs — ADR at root, specs in subfolders, same
commit.

Draft ADRs may be committed alone (no spec edits, no status promotion)
for pause/resume convenience. Draft-only commits don't form lineage
edges.

## ADR immutability vs mechanical edits

Per `adr-0018-adr-status-lifecycle.md`, ADR bodies are immutable once
`accepted`. Allowed mechanical edits:

- Fixing a broken cross-reference when a file is renamed.
- Fixing a typo or restoring a broken link.
- Updating file path references across a scheme change (e.g., the
  2026-04-19 renumber from `adr-YYYY-MM-DD-*` to `adr-NNNN-*`).

Not allowed: changing what an ADR decided. Supersede with a new ADR instead.

## Docs MCP behavior

The docs MCP (provided by the `spec-driven-dev` plugin):

- Indexes `docs/**/*.md` recursively.
- Classifies by filename prefix only:
  - `adr-*` → `'adr'`
  - `spec-*` → `'spec'`
  - `feature-list*` → `'spec'`
  - anything else → `null`
- Exposes `category: 'adr' | 'spec'` as a filter on `list_docs` and
  `search_docs`.
- Does not expose a `component` filter. Callers who want component
  scoping filter results by `doc_path.startsWith("docs/<component>/")`.
- Computes lineage from git co-commits (same algorithm as before).
  A cross-directory co-commit (ADR at root, spec in subfolder) is still
  a single commit and produces the same lineage edges.

## Applying this spec to a new `/plan-feature` session

1. Pick the ADR number: `ls docs/adr-*.md | awk -F- '{print $2}' | sort -n | tail -1`, add 1.
2. Author the ADR as `docs/adr-NNNN-<slug>.md`.
3. For each impacted spec: walk the "Deciding where a new spec lives"
   checklist to find the right file. Create the file if the folder
   doesn't exist yet.
4. Commit ADR + specs together in one `spec(<area>): …` commit.
