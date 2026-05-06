## Run: 2026-05-04T00:00:00Z

ADR: `docs/adr-0098-fix-per-user-resource-provisioning.md`
Specs checked:
- `docs/mclaude-control-plane/spec-control-plane.md`
- `docs/spec-state-schema.md`
- `docs/spec-nats-data-taxonomy.md`

---

### Phase 1 — ADR → Spec (forward pass)

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| 36 | "Both/and: fix the root cause AND make the chain resilient" | spec-control-plane.md:197 "If a per-user bucket or stream is missing at runtime (e.g., after a restore), the control-plane creates it on demand." | REFLECTED | — | The spec already expresses idempotent on-demand creation; "resilient chain" is an implementation detail of how that guarantee is achieved. |
| 37 | "ensureUserResources is idempotent on every call (each ensure-function existence-checks first)" | spec-control-plane.md:197 same line; spec-control-plane.md:275 "These resources are created once per user and persist." | REFLECTED | — | Idempotency is expressed in both specs. |
| 38 | "auth.go:180 (handleLogin) and challenge.go:249 (handleAuthVerify) — both call sites already exist" | spec-control-plane.md:275 "On user registration (first login), CP creates per-user KV buckets…" | REFLECTED | — | The spec says first login triggers creation; call-site selection is an implementation detail, not a spec decision. |
| 39 | "Each ensure-step's error is logged but does not abort the chain; chain returns the first observed error after all four steps were attempted" | spec-control-plane.md: no statement about per-step error semantics within ensureUserResources; spec-state-schema.md: no statement | GAP | SPEC→FIX | The specs are silent on the error-handling contract of the provisioning chain. A developer reading only the spec would not know that a failure in step N must not abort steps N+1..4, nor that the function returns the first observed error after all four are attempted. spec-control-plane.md's "Per-user resource provisioning" paragraph (line 275) should add this constraint. |
| 40 | "Happens automatically on next login for that user. No one-shot migration." | spec-control-plane.md:197 "creates it on demand" and :275 "first login" implies retry on next login | REFLECTED | — | The on-demand + idempotency statements together express the backfill behavior. |
| 41 | "Add integration test TestIntegration_EnsureUserResources_AllFour" | Not a spec statement — test coverage is an implementation plan detail | REFLECTED | — | Integration test specs are in the ADR's Integration Test Cases table; no spec file is expected to enumerate this. |
| 50-54 (User Flow step 3) | "The function ensures, in order: KV_mclaude-sessions-{uslug}, KV_mclaude-projects-{uslug}, KV_mclaude-user-info-{uslug}, MCLAUDE_SESSIONS_{uslug}" | spec-control-plane.md:190-195 lists all four under "Per-user (created on user registration / first login)"; spec-control-plane.md:275 names all four explicitly | REFLECTED | — | All four resources are named in the spec. |
| 52 | "KV_mclaude-user-info-{uslug} — create if missing" | spec-control-plane.md:194 "Created by CP on user registration"; spec-state-schema.md:362-364 "Created by control-plane on user creation alongside mclaude-sessions-{uslug} and mclaude-projects-{uslug}"; spec-nats-data-taxonomy.md:81 lists the bucket | REFLECTED | — | All three specs name this bucket and attribute creation to CP. |
| 53 | "MCLAUDE_SESSIONS_{uslug} JetStream stream — create if missing" | spec-control-plane.md:195 "Created by CP on user registration"; spec-state-schema.md:385 "Created by control-plane on user registration"; spec-nats-data-taxonomy.md:93 lists the stream | REFLECTED | — | All three specs name this stream and attribute creation to CP. |
| 54 | "Errors at any step are logged with full context (bucket name, error string) but do not short-circuit the remaining steps." | spec-control-plane.md: silent; spec-state-schema.md: silent; spec-nats-data-taxonomy.md: silent | GAP | SPEC→FIX | Duplicate of row for line 39 — same gap. The per-step WARN log + non-short-circuit behavior is a concrete runtime contract not expressed in any spec. |
| 90-91 (Error Handling) | "Any single ensure-step errors: logged at WARN with bucket/stream and error string. The chain continues to the next step. The function returns the first observed error to the caller after attempting all four." | All three specs: silent on this | GAP | SPEC→FIX | Same gap as above — consolidated here as the Error Handling section restates it. One spec fix covers all three occurrences. |
| 91 | "Login handler receives an error from ensureUserResources: existing behavior preserved (currently auth.go:180 logs and continues; challenge.go:249 discards). No change to the login response." | spec-control-plane.md: silent on what happens when ensureUserResources returns an error to the login handler | PARTIAL | SPEC→FIX | The spec says resources are created on first login but does not state that a provisioning error during login does not abort the login response. A developer could implement a blocking login failure instead. The error-disposition contract (log and continue, login proceeds) should be stated in the "Per-user resource provisioning" paragraph. |
| 92 | "NATS unreachable at provisioning time: the very first ensure call fails. All four are skipped. Login still proceeds." | spec-control-plane.md:197 "creates it on demand" implies retry, but the spec does not say login proceeds despite NATS failure | PARTIAL | SPEC→FIX | The spec's "creates it on demand" implies eventual backfill but is silent on whether NATS unavailability during login is a hard login failure or a soft error. The ADR decides it is soft (login proceeds). The spec should say so. |

---

### Summary

- Reflected: 8
- Gap: 3 (lines 39, 54, 90-91 — all the same underlying gap, one fix covers them)
- Partial: 2 (lines 91 and 92)

The three gaps and two partials all stem from the same missing spec text: the error-handling contract of `ensureUserResources`. A single addition to the "Per-user resource provisioning" paragraph in `spec-control-plane.md` (line 275) would cover all of them:

> Each ensure step is attempted regardless of earlier failures. A step that errors logs at WARN with the resource name and error string and does not abort subsequent steps. The function returns the first observed error to the caller after all four steps have been attempted. If `ensureUserResources` returns an error, the login response proceeds normally — the missing resource will be retried on the next login.

The four-resource list and "created on first login" claim are fully reflected in all three specs. The ADR's claim that "no spec changes are needed" is **partially correct**: the existence and naming of the four resources is already in the specs. However, the error-handling contract introduced by this ADR — log-and-continue, non-aborting chain, login proceeds on provisioning error — is new behavioral specification that is not present in any of the three specs.
