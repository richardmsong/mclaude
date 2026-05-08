# ADR: Invariant-Driven Development with Compiled Verification

**Status**: draft
**Status history**:
- 2026-05-07: draft

> **Pending split.** This draft currently mixes two concerns: (1) the *methodology* itself — what invariant-driven development is, its artifacts, its cycle, its own meta-invariants — and (2) mclaude's *adoption* of it — pilot order, coexistence routing, ADR-0099 withdrawal, cross-cutting-contract migration plan.
>
> Concern (1) belongs in the `agent-plugins` repo as a methodology ADR for the `spec-driven-dev` plugin (v2 evolution). Concern (2) is what this ADR-0100 should retain. Before promotion to `accepted`, this file will be reduced to the adoption-only form, citing the agent-plugins methodology ADR as the authoritative source for everything in concern (1).
>
> This draft is committed to preserve the design conversation across compaction; the split happens after the methodology ADR is authored in agent-plugins.

## Overview

Adopt a development methodology in which named **invariants** — not markdown spec prose — are the canonical contract surface, and verification is performed by deterministic CPU-bound mechanisms (Go test files, lint rules, schemas, type-system checks) rather than LLM-as-judge spec-evaluators. LLMs appear only at *authoring* time (compiling invariants into verifier code) and *audit* time (quarterly differential regeneration to detect spec gaps and dead code), never in the recurring CI validation path.

In v1: declare the methodology, define the artifact layout, and pilot it on `mclaude-control-plane`. The existing markdown-spec SDD methodology coexists for un-migrated components. Session-agent is the planned second pilot but is deferred to a follow-up ADR.

ADR-0099 (E2E state-snapshot testing scaffolding, draft) is **withdrawn** as part of this ADR — its scaffolding decisions (separate `mclaude-e2e/` package, free-form `cmp.Diff` snapshots, `//go:build e2e` tag) get reauthored inside the new methodology, where invariant-pinned tests co-located with the code they cover are the right shape.

## Motivation

The current SDD pipeline has a structural weakness: every gap between markdown spec and code is checked by an LLM evaluator doing fuzzy alignment. This carries recurring per-CI cost, non-deterministic outcomes, model-version sensitivity, and most importantly leaves dead/erroneous code in place because static spec-evaluator runs cannot detect "no spec line requires this code path."

Concrete pain in the codebase right now:
- `mclaude-control-plane` has accumulated drift between `docs/mclaude-control-plane/spec-control-plane.md` and the implementation that the spec-evaluator catches inconsistently and the implementation-evaluator catches only when it surfaces.
- Multiple recent draft ADRs (0095–0099) are spending design time on testing infrastructure (e2e snapshots, integration tests, per-user provisioning verification) without a coherent framework for which mechanism owns which invariant — leading to ad-hoc, partially overlapping verification surfaces.
- Token cost of running spec-evaluator + implementation-evaluator on every spec/code change has become a meaningful operational cost.

The shift: **specs are tests. ADRs carry rationale and decision history. The contract is executable.** This is not a rejection of SDD — it preserves the ADR-driven decision discipline — but replaces the markdown spec layer with a typed invariant registry and the LLM-as-judge validation with deterministic mechanism-per-invariant verification.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Spec form | Tests as spec: Go `*_test.go` files + `.arch/` rules + `proto/` schemas + `.lint/` analyzers, indexed by an invariant registry | Tests pin behavior deterministically; lint rules pin structure; schemas pin contracts. The full set is the executable spec. Markdown can't be deterministically verified at acceptable cost. |
| Decomposition unit | Named **invariants** with stable IDs (e.g. `topology.user_has_one_namespace`) | ADRs are too coarse (one ADR makes many decisions); unit tests are too granular (no semantic naming). Invariants give a registry that is coverage-checkable, roundtrippable, and evolvable deltawise. |
| Invariant lifecycle | ADRs declare deltas (add/modify/remove invariants) on a running registry; the registry is the source of truth for "what this system promises now" | Specs collapse history; the registry is timeless. Roundtripping the *current* invariants from the test suite is well-defined; roundtripping a historical ADR from the spec is not. |
| Verification taxonomy | Each invariant names exactly one primary verification mechanism: unit, table, property, architectural-rule, AST, type-system, schema, codegen-completeness, integration, journey | A requirement that can't name a mechanism is inadmissible — either move to ADR rationale or sharpen until it fits. Forces precision discipline at authoring. |
| LLM role | Authoring + audit only, never recurring validation | Token cost is paid once per invariant (compilation) and once per quarter (audit), not per CI run. CI is pure CPU, sub-minute, deterministic. |
| Language binding | Spec encoded in Go test files for Go components; the language is part of the contract | Goroutine semantics, `context.Context` propagation, error-wrapping conventions are load-bearing. "Recompile to Rust" would silently drop them. |
| Constraint scope | Constrain contracts (user-visible, cross-component, security-critical); leave internals liquid | Overconstraint causes refactor friction and premature crystallization. Differential regeneration tells us when the line is drawn wrong (divergence-with-correctness = appropriate freedom; divergence-with-incorrectness = real spec gap). |
| Pilot component | `mclaude-control-plane` first; `mclaude-session-agent` second (deferred to a follow-up ADR) | Control-plane is the most spec-heavy and has the most accumulated drift — biggest payoff. Session-agent has a tight, mostly-behavioral contract (stream-json over stdin/stdout) — the easiest second migration. |
| Coexistence | New methodology runs alongside markdown-spec SDD; per-component opt-in | (decision pending: how does master session route a change request to the right methodology — file-path-based, component-based, ADR-declared opt-in?) |
| Migration shape for control-plane | Greenfield: rm production code + regenerate from invariants × 5 | Confirmed in /plan-feature Q&A 2026-05-07. Only path that prunes dead code; treats existing implementation as one of N candidates. |
| ADR-0099 disposition | Withdrawn; replaced by this ADR's e2e/integration mechanism | Confirmed in /plan-feature Q&A: scaffolding decisions get reauthored inside the new methodology. |
| Vocabulary precision | Hybrid: types-as-glossary primary (entities → Go types, predicates → methods); explicit `spec/glossary.md` (or `internal/spec/glossary.go`) for cross-cutting concepts and ambiguous domain terms | Invariant statements use *terms*; un-pinned terms drift at the vocabulary layer. Types carry most of the load (compile-checked, refactor-safe); a small glossary covers what types can't express. CI gate: every term in an invariant statement resolves to a typed binding or glossary entry. |
| Registry framework approach | Custom thin Go layer (~200 LOC) + reuse of per-mechanism tools; no off-the-shelf "invariant registry framework" exists that fits | Closest adjacent frameworks (ArchUnit, fitness functions, TLA+, OPA/Rego, CUE, Concordion, JML, Cucumber) cover slices but none combines named registry + multi-mechanism verification + CI-gateable coverage. Building thin and reusing mature per-mechanism tools is the right shape. |
| Per-mechanism tool choices | Behavior: `go test` + table tests; Property: `rapid`; Architecture: `go-arch-lint` + `depguard`; AST: `semgrep` + `go/analysis`; Schema: `protobuf` + `buf`; Type system: `exhaustive`, `nilaway`, sealed interfaces; Codegen completeness: `go:generate` + reflection; Mutation: `go-mutesting` (audit-only, not per-CI) | Mature, Go-native, refactor-friendly. Avoids JVM/cross-language toolchains that would fight the "language is part of the contract" principle. |
| Invariant registry format | (decision pending: Go const slice, YAML file, separate file per invariant, or comment-extracted from test files) | |
| Verifier file layout | (decision pending: `*_test.go` co-located with code, `internal/spec/` subdirectory, `mclaude-e2e/`-style separate package, or hybrid by mechanism) | |
| Cross-cutting contracts during partial migration | (decision pending: move to `mclaude-common` with proto+buf now, or stay in markdown until both endpoints migrate) | |
| Differential regeneration cadence + owner | (decision pending: quarterly + manual trigger, nightly + scheduled, on-demand only; who reads the diff and decides whether divergence is gap or freedom) | |

## User Flow

The development cycle for a migrated component:

1. **Change request enters.** User asks for a feature, fix, refactor, etc.
2. **Master session authors an ADR draft.** Standard `/plan-feature` flow, with one addition: the ADR's "Component Changes" section enumerates which **invariants** are added, modified, or removed.
3. **Invariant decomposition.** Each new invariant is named, statemented in one line, and tagged with its primary verification mechanism. Invariants that can't name a mechanism are either inadmissible or need sharpening — this is the precision forcing function.
4. **LLM compiles invariants → verifier code.** A dedicated subagent reads the new/modified invariants from the ADR delta and generates the corresponding test files, lint rules, schemas. Output is committed alongside the ADR.
5. **Human reviews compiled verifiers.** Same review bar as hand-written code. The LLM's output is not blessed because an LLM wrote it; it's blessed because a human read it and understood what each verifier proves.
6. **Implementation follows.** dev-harness writes production code that makes the verifier suite pass. Same loop as today (dev-harness → implementation-evaluator) but the implementation-evaluator becomes redundant — `go test ./...` plus the lint/schema runs are the deterministic answer.
7. **CI runs the verifier suite on every commit.** Pure CPU, sub-minute, no model dependency. Failures are localized to specific invariants.
8. **Periodic differential regeneration audit.** Quarterly (or per major refactor checkpoint): `rm -rf` production code, regenerate from invariants × 5, diff results. Convergence-with-correctness validates spec precision. Divergence-with-correctness reveals appropriate underconstraint. Divergence-with-incorrectness localizes spec gaps to specific decisions. Output is a triaged list reviewed by a human; spec changes are authored as new ADRs.
9. **Periodic LLM audit run (advisory).** Reads the invariant registry plus recent ADRs, suggests missing journeys, missing invariants, drift between ADR rationale and registry contents. Output is a Markdown file that a human reads and acts on. Never blocks merges or deploys.

## Component Changes

### `mclaude-control-plane` (pilot)

The pilot. Concrete migration plan deferred to "Open questions" — both the migration shape (greenfield vs brownfield) and the verifier file layout are open decisions. The post-migration shape:

- `mclaude-control-plane/internal/spec/invariants.yaml` (or equivalent — format pending) — registry of named invariants with IDs, statements, mechanisms, verifier file pointers.
- `mclaude-control-plane/**/*_test.go` — verifier code, each test pinning ≥1 invariant by ID via comment annotation.
- `mclaude-control-plane/.arch/` — architectural rules (`depguard`, `go-arch-lint`, custom analyzers) for structural invariants.
- Existing `docs/mclaude-control-plane/spec-control-plane.md` — retained as a *read-only narrative* for now (TODO: decide whether to delete after migration completes, or keep as introductory documentation).

### `mclaude-common` (cross-cutting contracts)

(decision pending: whether NATS subject definitions and message schemas move here as proto+buf now, or stay in `docs/spec-nats-payload-schema.md` until both endpoints migrate)

### Other components

Continue under markdown-spec SDD until individually migrated via separate ADRs. No changes from this ADR.

### Tooling additions

- New subagent: `invariant-compiler` (LLM, one-shot per ADR delta) — reads invariant deltas, generates verifier code.
- New skill: `/audit-invariants` (LLM, advisory) — runs the quarterly differential regeneration ritual and produces a triaged output file.
- Existing `/feature-change` continues to apply, but for migrated components the implementation-evaluator step is replaced by the deterministic verifier suite.

## Data Model

### Invariant registry entry (format pending)

Conceptual shape:

```
- id: <stable_dot_separated_name>
  statement: <one-line natural language>
  mechanism: unit | table | property | arch | ast | type | schema | completeness | integration | journey
  verifier: <path/to/test_file.go::TestName | path/to/.arch/rule.yaml | ...>
  introduced_by: adr-NNNN
  status: active | superseded
```

Concrete format (YAML vs Go const slice vs per-invariant file vs comment-extracted) is open.

### ADR invariant delta block

Each ADR that affects invariants includes a structured block:

```
## Invariant Delta
### Added
- <id>: <statement> (mechanism: <m>, verifier: <path>)
### Modified
- <id>: <what changed and why>
### Removed
- <id>: <reason — typically "superseded by <new id>" or "no longer required because ...">
```

This block is itself coverage-checked: a deterministic script (CI gate) verifies that every entry in the registry has a corresponding `Added` line in some ADR; every `Removed` line corresponds to an entry no longer in the registry; modifications are traceable.

## Error Handling

- **Verifier compilation failure** (LLM produces invalid Go test code): build break, human authors fix or re-runs compilation with corrected ADR.
- **Differential regeneration produces incorrect implementations**: surfaces as a spec gap; the failing invariants identify which decisions weren't constrained enough. Author follow-up ADR with sharpened invariants.
- **Invariant registry drift** (registry entry has no live verifier file, or verifier file pins an unknown invariant): CI gate fails, must be resolved before merge.
- **Coexistence boundary violation** (a change request touches a migrated component but routes through markdown-spec SDD, or vice versa): TODO — depends on routing decision still open.

## Security

The methodology itself does not introduce new security boundaries, but it strengthens existing ones:
- Tenant isolation invariants (e.g. `topology.tenant_rbac_isolation`) become deterministic checks rather than markdown promises.
- JWT format, NATS subject permissions, and credential flows can be encoded as schemas (proto+buf) with breaking-change detection — currently they're prose.
- Architectural invariants like "no `net/http` server outside JWT issuer" become enforceable lint rules rather than spec aspirations.

The long-term security upside is significant; the short-term security risk is minimal because un-migrated components retain their existing protections.

## Impact

Specs updated in this commit:
- `docs/spec-invariant-driven-development.md` — **new**, cross-cutting methodology spec describing artifacts, cycle, and conventions. (TODO: confirm this is the right location vs. extending `docs/spec-doc-layout.md`.)
- `docs/spec-doc-layout.md` — extend to cover invariant registry location and verifier file conventions.
- `docs/mclaude-control-plane/spec-control-plane.md` — annotate as "pilot for invariant-driven development per ADR-0100" with pointer to the new registry. (TODO: confirm whether to retain the existing spec text or replace with a thin pointer.)
- `docs/adr-0099-e2e-state-snapshot-testing.md` — flip `Status: draft → withdrawn`, add note pointing to ADR-0100 as successor.

Components implementing the change:
- `mclaude-control-plane` (pilot, infrastructure work + invariant extraction).
- Tooling: new invariant-compiler subagent, new `/audit-invariants` skill, CI changes for registry-coverage checks.

Components explicitly **not** changed by this ADR:
- `mclaude-session-agent`, `mclaude-cli`, `mclaude-relay`, `mclaude-web`, `mclaude-controller-k8s`, `mclaude-user-agent`, `mclaude-common`, `mclaude-server`, `mclaude-mcp`, `mclaude-connector` — continue under markdown-spec SDD.

## Self-Application: Methodology as Zeroth Invariant Set

The methodology is self-applicable. The same registry, glossary, CI gates, and skill conventions that verify product invariants also verify *the methodology's own invariants*. This isn't a curiosity — it's evidence the framework is general, and it determines the implementation order.

Candidate methodology invariants (will be registered as `methodology.*` once the registry exists):

| ID | Statement | Mechanism |
|---|---|---|
| `methodology.registry.no_orphans` | Every registry entry has a live verifier file; every verifier file pins a live registry entry. | deterministic script (CI gate) |
| `methodology.glossary.complete` | Every term in an invariant statement resolves to a typed binding or a glossary entry. | deterministic script (CI gate) |
| `methodology.adr.delta_reconciles` | Sum of ADR delta blocks (Added − Removed) equals current registry contents. | deterministic script (CI gate) |
| `methodology.invariant.has_mechanism` | Every invariant names exactly one primary verification mechanism from the taxonomy. | schema check on registry |
| `methodology.invariant.statement_atomic` | Invariant statements contain no logical "AND" — atomicity is enforced. | lint on registry (LLM-assisted authoring; scriptable heuristic for CI) |
| `methodology.skills.context_isolation` | Each skill loads only its narrow slice of methodology context, not the whole spec. | advisory LLM audit + token-budget instrumentation per skill |
| `methodology.subagent.fresh_context` | Subagents run in fresh contexts with no inherited conversation history. | deterministic check on subagent invocation parameters |

**Where the recursion bottoms out** (trusted primitives — not invariant-checked):
- Go toolchain (`go build`, `go test`, type system, AST parser).
- Filesystem and `git`.
- Human judgment for the differential regeneration audit (triaging divergence-with-correctness vs spec gap).
- Day-0 bootstrap: this ADR (English) is the schema for the registry until the registry exists to hold its own invariants.

**Bootstrap order implied by self-application:**

1. **Day 0**: Registry empty. Methodology lives in this ADR + the new methodology spec doc (English).
2. **Day 1**: Register the ~7–10 methodology-own invariants. Build their deterministic CI gates (`/check-glossary`, `/check-registry-coverage`, ADR-delta reconciliation script). Verify the registry/glossary/CI-gate machinery works end-to-end on a small invariant set.
3. **Day N**: Register product invariants (starting with `mclaude-control-plane`). Every product invariant now benefits from methodology-invariant CI gates that are already proven.

This inverts the naive order ("build the methodology, then apply it to the product"). Instead: the methodology applies to itself first, which proves the infrastructure before betting the control-plane migration on it.

## Scope

In v1:
- Methodology declaration (artifacts, cycle, principles).
- Pilot plan for `mclaude-control-plane`.
- Withdrawal of ADR-0099.
- New cross-cutting methodology spec.
- New invariant-compiler subagent definition.
- New `/audit-invariants` skill definition.
- Coexistence routing rules between old and new methodology.
- **Day-1 bootstrap: the methodology's own ~7–10 invariants are the first registered, before any control-plane invariants. The infrastructure is proved on its own meta-level before being applied to the product.**

Explicitly deferred:
- `mclaude-session-agent` migration (follow-up ADR, same shape).
- Migration of any other component.
- Cross-cutting contracts (NATS subjects, JWT format, message schemas) moving to `mclaude-common` with proto+buf — deferred to a follow-up ADR unless the open question resolves to "do it now."
- Replacement of the existing `/feature-change` skill — this ADR layers on top; future ADR may consolidate.
- Automated language migration tooling — explicitly a non-goal (the language is part of the contract).

## Open questions

- **Cross-cutting contracts during partial migration**: move NATS subjects + JWT format + message schemas to `mclaude-common` with proto+buf now, or stay in markdown until both endpoints migrate?
- **Invariant registry format**: Go const slice, YAML file, separate file per invariant, or comment-extracted from test files via codegen?
- **Glossary form**: separate `spec/glossary.md` (Markdown), `internal/spec/glossary.go` (Go const map for type-checked term references), or generated from typed bindings (no separate glossary file, only a derived index)?
- **Verifier file layout**: `*_test.go` co-located with code, `internal/spec/` subdirectory, separate `mclaude-e2e/`-style package, or hybrid by mechanism (unit co-located, integration separate)?
- **ADR delta block structure**: where does the invariant delta live in the ADR template — its own section, embedded in Decisions, or in Component Changes?
- **Routing rule for coexistence**: how does the master session decide whether a change request goes through markdown-spec SDD or the new methodology — file-path-based, component-based, ADR-declared opt-in, or something else?
- **Differential regeneration cadence + owner**: quarterly + manual trigger, nightly + scheduled, on-demand only; who reads the diff output and decides whether divergence is appropriate freedom vs real spec gap?
- **Methodology spec location**: new `docs/spec-invariant-driven-development.md` (cross-cutting), or extend `docs/spec-doc-layout.md` to cover both?
- **Existing spec retention**: after `mclaude-control-plane` migrates, retain `docs/mclaude-control-plane/spec-control-plane.md` as introductory narrative, replace with a thin pointer to the registry, or delete entirely?
- **Invariant registry coverage CI gate**: scope (registry ↔ verifier integrity, ADR delta integrity, glossary coverage), failure semantics (block merge vs warn), where to run (per-PR, scheduled)?
- **LLM compilation step ergonomics**: invoked by master session inline during /plan-feature, by a dedicated /compile-invariants slash command after ADR is finalized, or as part of dev-harness?
- **Skill suite scope**: which skills exist as separate slash commands vs. fold into existing /plan-feature and /feature-change? Candidates: `/compile-invariants`, `/audit-invariants`, `/check-glossary`, `/check-registry-coverage`, `/route-change-request`. Trade-off: more skills = better context isolation per task; fewer skills = simpler mental model.
- **Context-overload mitigation strategy**: how do we keep the methodology's complexity from bloating every session — dense reference doc + skill-as-capsule + subagent narrowing + tool-driven determinism, or some other shape?

## Integration Test Cases

| Test case | What it verifies | Setup/teardown | Components exercised |
|-----------|------------------|----------------|----------------------|
| Full migrate-and-validate of a single `mclaude-control-plane` invariant | The full cycle works end-to-end: ADR delta → invariant-compiler subagent → generated verifier code → CI run → green | Manual: pick a single existing behavior in control-plane, author the ADR delta, run the cycle | mclaude-control-plane, invariant-compiler subagent, CI |
| Differential regeneration on a small subset of invariants | Regenerate-from-spec × 5 produces convergent implementations for sharply-stated invariants and reveals divergence for fuzzy ones | Manual: pick 3-5 invariants, run audit ritual | mclaude-control-plane, /audit-invariants skill |
| Registry-coverage CI gate | Adding an invariant to the registry without a verifier fails CI; adding a verifier without a registry entry fails CI | Synthetic test with bad and good registry+verifier pairs | CI |
| Coexistence smoke | A change to `mclaude-control-plane` correctly routes through the new methodology; a change to `mclaude-relay` continues to route through markdown-spec SDD | Manual: trigger one of each via /feature-change | master session routing logic |

## Implementation Plan

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| Methodology spec doc | 200-400 lines markdown | 30k | Cross-cutting spec; new file |
| Invariant-compiler subagent | 100-200 lines (subagent definition + prompts) | 50k | New subagent, modeled on dev-harness pattern |
| /audit-invariants skill | 200-400 lines | 60k | New skill, models the differential-regeneration ritual |
| Registry-coverage CI gate | ~100 lines Go or shell | 40k | Script + GH Actions wiring |
| Control-plane pilot — invariant extraction | depends on chosen migration shape: greenfield ~0 lines extracted (invariants from scratch), brownfield ~500-2000 lines test code translating existing spec/behavior | 150k-400k | Bulk of the work; depends on resolution of migration-shape question |
| Control-plane pilot — verifier authoring | proportional to invariant count; rough estimate 30-60 invariants × 50-150 lines = 1500-9000 lines | 200k-500k | LLM-compiled, human-reviewed |
| ADR-0099 withdrawal | ~10 lines edit | 5k | Mechanical |
| Coexistence routing logic | ~50-100 lines of CLAUDE.md or skill changes | 20k | Master-session-side rule |

**Total estimated tokens:** 555k–1.1M (high variance — pilot work dominates and depends on open questions)
**Estimated wall-clock:** depends heavily on migration shape; methodology + tooling alone could ship in 1-2 days, full control-plane pilot in 1-2 weeks.
