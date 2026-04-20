## Run: 2026-04-20T00:00:00Z (round 2)

Component: `charts/mclaude`
Prior audit: `.agent/audits/spec-charts-mclaude-2026-04-20.md`
Focus: Re-verify the two SPEC→FIX gaps identified in round 1, confirm resolution, check for regressions.

Authoritative docs:
- `docs/adr-0024-typed-slugs.md` (accepted) — subject strings, KV scopes, NATS permission grant inventory
- `docs/spec-state-schema.md` — paired spec (KV bucket keys, stream subjects, NATS permissions)
- `docs/adr-0016-nats-security.md` (accepted, partial-supersession notice added by commit 5c17771)

---

### Gap resolution check — prior round 1 GAPs

**Gap 1 (ADR-0016:44-45): Controller KV grant cluster-scoped vs broad**

Round 1 finding:
> ADR-0016 specifies `$KV.mclaude-projects.{clusterId}.>` (cluster-scoped). ADR-0024 explicitly supersedes this: controller gets broad `$KV.mclaude-projects.>`. ADR-0024 §KV scope note says this is intentional. Code follows ADR-0024. ADR-0016's cluster-scoped KV grant text is superseded but not formally marked as such.

Resolution applied: Commit `5c17771` added a partial-supersession notice to ADR-0016 lines 7-8:
> "ADR-0024 (typed-slugs, 2026-04-20) supersedes every subject and KV-scope string here with the typed-literal shape... When the two ADRs disagree on a subject string, ADR-0024 wins."

**Verification:** The notice is present and unambiguous. The conflict between the old ADR-0016:44-45 text (`$KV.mclaude-projects.{clusterId}.>`) and the code (`$KV.mclaude-projects.>`) is now resolved by the tie-breaking rule in the notice: ADR-0024 wins. The gap is **CLOSED**.

---

**Gap 2 (ADR-0016:46-47): Session-agent signing key ceiling**

Round 1 finding:
> ADR-0016 specifies `mclaude.*.sessions.{clusterId}.*.>` as the signing key ceiling. Code has `mclaude.users.*.projects.*.>`. ADR-0016 text is superseded by ADR-0024.

Resolution applied: Same commit `5c17771` supersession notice explicitly names `session-agent signing-key ceiling mclaude.users.*.projects.*.>` as the new authoritative value. The notice also names the old ceiling string (`mclaude.{userId}.sessions.{clusterId}.{sessionId}.>`) as legacy.

**Verification:** The notice is present and names this exact string. The gap is **CLOSED**.

---

### Phase 1 — Spec → Code (full re-verification pass)

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0016:7-8 | Partial supersession notice: "When the two ADRs disagree on a subject string, ADR-0024 wins" | docs/adr-0016-nats-security.md:7 | IMPLEMENTED | — | Notice is present; written by commit 5c17771; resolves both prior GAPs |
| adr-0024:226-239 | SPA pub allow: `mclaude.users.{uslug}.>`, `_INBOX.>` | nats-permissions-configmap.yaml:39-41 | IMPLEMENTED | — | Exact match |
| adr-0024:226-239 | SPA sub allow: `mclaude.users.{uslug}.>`, `$KV.mclaude-sessions.>`, `$KV.mclaude-projects.>`, `$JS.API.DIRECT.GET.>`, `_INBOX.>` | nats-permissions-configmap.yaml:42-47 | IMPLEMENTED | — | Exact match |
| adr-0024:226-239 | SPA pub deny: `$KV.>`, `$JS.>`, `mclaude.system.>` | nats-permissions-configmap.yaml:48-51 | IMPLEMENTED | — | Exact match |
| adr-0024:241-243 | Control-plane: unchanged full grants `mclaude.>`, `$KV.>`, `$JS.>`, `_INBOX.>`, `$SYS.ACCOUNT.>` for both pub and sub | nats-permissions-configmap.yaml:60-71 | IMPLEMENTED | — | Both pub.allow and sub.allow present with all five entries |
| adr-0024:244-257 | Controller pub allow: `$KV.mclaude-projects.>`, `mclaude.clusters.{cslug}.>`, `_INBOX.>` | nats-permissions-configmap.yaml:89-92 | IMPLEMENTED | — | Exact match; KV scope note confirms broad grant correct |
| adr-0024:244-257 | Controller sub allow: `mclaude.clusters.{cslug}.api.>` | nats-permissions-configmap.yaml:93-94 | IMPLEMENTED | — | Exact match |
| adr-0024:244-257 | Controller pub deny: `mclaude.users.*.>`, `$KV.mclaude-sessions.>`, `$JS.>` | nats-permissions-configmap.yaml:95-98 | IMPLEMENTED | — | Exact match |
| adr-0024:260-261 | Session-agent signing key ceiling: `mclaude.users.*.projects.*.>` (replaces ADR-0016 ceiling) | nats-permissions-configmap.yaml:105-106 | IMPLEMENTED | — | Exact match; comment on line 104 explicitly references supersession of ADR-0016 |
| adr-0024:262-270 | Session-agent pub allow: `mclaude.users.{uslug}.projects.{pslug}.events.>`, `mclaude.users.{uslug}.projects.{pslug}.lifecycle.>`, `_INBOX.>` | nats-permissions-configmap.yaml:120-123 | IMPLEMENTED | — | Exact match |
| adr-0024:262-270 | Session-agent sub allow: `mclaude.users.{uslug}.projects.{pslug}.api.sessions.>`, `mclaude.users.{uslug}.projects.{pslug}.api.terminal.>` | nats-permissions-configmap.yaml:124-126 | IMPLEMENTED | — | Exact match |
| adr-0024:126-128 | charts/mclaude: NATS permission templates + backfill migration Job | nats-permissions-configmap.yaml (present), slug-backfill-job.yaml (present) | IMPLEMENTED | — | Both templates present |
| adr-0016:44-45 | OLD: Controller KV grant `$KV.mclaude-projects.{clusterId}.>` | nats-permissions-configmap.yaml:90 | GAP→RESOLVED | SPEC→FIX (closed) | ADR-0016:7 supersession notice: "ADR-0024 wins". Code follows ADR-0024. Conflict is now documented. No longer a gap. |
| adr-0016:46-47 | OLD: Session-agent ceiling `mclaude.*.sessions.{clusterId}.*.>` | nats-permissions-configmap.yaml:105-106 | GAP→RESOLVED | SPEC→FIX (closed) | ADR-0016:7 supersession notice: "ADR-0024 wins". Code follows ADR-0024. Conflict is now documented. No longer a gap. |

---

### Phase 2 — Code → Spec (regression check)

No new files were added to `charts/mclaude/templates/` since round 1. Confirming template list matches prior audit.

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| nats-permissions-configmap.yaml:1-127 | INFRA | NATS permission grant template. All grant strings match ADR-0024 exactly. No regressions found. |
| slug-backfill-job.yaml:1-end | INFRA | Backfill Job. Unchanged from round 1. Pre-install/pre-upgrade hook, correct binary path, correct env vars. |
| All other templates | INFRA | Same classification as round 1. No new templates added; no template content regressed. |

---

### Phase 3 — Test Coverage

Same as round 1 — no new tests added. The gaps/changes from `5c17771` are documentation-only (spec fix), so no new test coverage is required.

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| adr-0024:226-239 | SPA NATS permission grants | None | Smoke test only | UNTESTED |
| adr-0024:260-261 | Session-agent signing key ceiling | None | None | UNTESTED |
| adr-0024:244-257 | Controller grants | None | None | UNTESTED |
| adr-0024:126-128 | Backfill Job hook | None | None | UNTESTED |

No regression in test coverage — status unchanged from round 1.

---

### Phase 4 — Bug Triage

No open bugs in `.agent/bugs/` reference `charts/mclaude`. No change from round 1.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | — | — | No helm-related bugs open |

---

### Summary

- Implemented: 11 (core grant rows re-verified)
- Gap: 0 (both prior SPEC→FIX gaps are now RESOLVED — supersession notice closes them)
- Partial: 0
- Infra: 20 (unchanged from round 1)
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 0
- E2E only: 0
- Untested: 4 (unchanged — no helm-unittest framework)
- Bugs fixed: 0
- Bugs open: 0

### Gap resolution verdict

| Prior gap | File | Resolution |
|-----------|------|------------|
| ADR-0016:44-45 — Controller KV grant cluster-scoped vs broad | docs/adr-0016-nats-security.md:7 | CLOSED — supersession notice added; ADR-0024 wins on all subject/KV strings |
| ADR-0016:46-47 — Session-agent ceiling old positional shape | docs/adr-0016-nats-security.md:7 | CLOSED — supersession notice added; ADR-0024 wins on all subject/KV strings |
