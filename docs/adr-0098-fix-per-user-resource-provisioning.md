# ADR: Fix per-user resource provisioning — wire missing user-info KV + sessions stream

**Status**: accepted
**Status history**:
- 2026-05-06: draft
- 2026-05-06: accepted — paired with docs/mclaude-control-plane/spec-control-plane.md

## Overview

`mclaude-control-plane`'s `ensureUserResources()` is supposed to create four per-user JetStream resources on user registration / first login: `KV_mclaude-sessions-{uslug}`, `KV_mclaude-projects-{uslug}`, `KV_mclaude-user-info-{uslug}` (ADR-0091), and `MCLAUDE_SESSIONS_{uslug}` stream (ADR-0054). Empirical evidence on a fresh deploy shows only the first two get created — the user-info KV bucket and the per-user sessions stream are missing for `dev-mclaude-local`. The `ensureUserResources` function appears to call all four in sequence, so the most likely root cause is either an early-return on a partial error, an error swallow, or a path that bypasses the function entirely.

This ADR is a class-A bug fix: the existing accepted ADRs (0054, 0091) and the existing specs (`spec-control-plane.md`, `spec-state-schema.md`, `spec-nats-data-taxonomy.md`) already say what the behavior should be. The code diverges. The fix is mechanical: identify the divergence, wire the missing creation calls, and verify all four resources land on every login (idempotent — re-running is a no-op when resources already exist).

## Motivation

Verified on the fresh deploy from CI run 25407993052 (commit b6e01d7, deployed 2026-05-05 23:35Z):

```
Account: mclaude-account
  KV_mclaude-hosts                          (shared, present)
  KV_mclaude-sessions-dev-mclaude-local     ✓ present
  KV_mclaude-projects-dev-mclaude-local     ✓ present
  KV_mclaude-user-info-dev-mclaude-local    ✗ MISSING (ADR-0091)
  MCLAUDE_SESSIONS_dev-mclaude-local        ✗ MISSING (ADR-0054)
```

Downstream consequences:
- ADR-0091's CLI flow (`mclaude auth anthropic` → `mclaude-user-info-{uslug}`) cannot complete: the bucket the CLI tries to PUT into doesn't exist, so the OAuth-token write returns "no such stream". Every Anthropic-token configure attempt fails for dev-mclaude-local until the bucket exists.
- ADR-0054's per-user sessions stream is the JetStream substrate behind every session lifecycle event. Without it, session creation, replay, and dashboard notifications fail or fall through to legacy code paths.

The function `ensureUserResources()` at `mclaude-control-plane/projects.go:456` reads correctly: it calls all four ensure-functions in sequence, each is idempotent on existence-check. The fact that the first two succeed and the next two don't, on the same call, indicates the function is returning early after the second call (likely a swallowed/transient error) — the dev-harness will pinpoint exactly which call errors and fix it.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Strategy | **Both/and**: fix the root cause AND make the chain resilient | The chain shouldn't error in the first place — the function as written should produce all four resources successfully on a healthy NATS connection. Identify and fix the specific cause of today's failure (most likely `ensurePerUserInfoKV` or `ensurePerUserSessionsStream` — see investigation list below). Separately, harden the umbrella function so a future transient error in step N doesn't wedge steps N+1..4. |
| Idempotency | `ensureUserResources` is idempotent on every call (each ensure-function existence-checks first) | Calling on every login backfills missing resources for partially-provisioned users (like dev-mclaude-local today) without disturbing already-created ones. No separate migration job needed. |
| Where the call lives | `auth.go:180` (handleLogin) and `challenge.go:249` (handleAuthVerify) — both call sites already exist | No new wiring; both call sites are the right places. |
| Error handling on every step | Each ensure-step's error is **logged but does not abort the chain**; chain returns the first observed error after all four steps were attempted | Resilience layer in case any single step regresses in the future. Loud (per-step WARN logs) but recoverable (next login retries the missing one). |
| Backfill for `dev-mclaude-local` | Happens automatically on next login for that user. No one-shot migration. | The user logs in again → `ensureUserResources` runs → existing buckets are no-ops → missing buckets/stream get created. Same code path covers fresh users and partially-provisioned legacy users. |
| Test coverage | Add an integration test `TestIntegration_EnsureUserResources_AllFour` that runs `ensureUserResources` against an in-process NATS server, then asserts all four named resources exist. Specifically prevents future regressions of the same shape. | Existing `TestIntegration_ADR0091_UserInfoKVBucket` only checks the user-info bucket in isolation, not the full set in one call. |

## User Flow

(No user-visible flow changes. This is a back-end provisioning fix.)

1. User logs in (SPA → `POST /auth/login`) or completes challenge-response (`POST /api/auth/verify`).
2. CP calls `ensureUserResources(nc, user.Slug)`.
3. The function ensures, in order:
   - `KV_mclaude-sessions-{uslug}` — create if missing.
   - `KV_mclaude-projects-{uslug}` — create if missing.
   - `KV_mclaude-user-info-{uslug}` — create if missing.
   - `MCLAUDE_SESSIONS_{uslug}` JetStream stream — create if missing.
4. Errors at any step are logged with full context (bucket name, error string) but **do not short-circuit** the remaining steps. The function returns the first observed error after all four have been attempted.
5. Login response proceeds normally; missing resources cause downstream NATS calls to fail with their own error paths (already handled).

After this fix, all four resources are present after any login by any user. Re-logins are no-ops on the existence-check.

## Component Changes

### `mclaude-control-plane/projects.go`

`ensureUserResources(nc, uslug)` is restructured to **not short-circuit** on errors. The dev-harness identifies the actual root cause (which step errors in production) and either fixes that step's error or upgrades the chain to error-aggregation.

Root cause confirmed (design-audit round 1, 2026-05-06): the legacy password-only login path in `auth.go:159–196` calls `IssueUserJWTLegacy` and exits **without** calling `ensureUserResources`. The function is only invoked inside the `if req.NKeyPublic != ""` branch. The bootstrap admin's first login went through the legacy branch (no NKey supplied), provisioning nothing — except `mclaude-sessions-{uslug}` and `mclaude-projects-{uslug}` materialized lazily via separate code paths, leaving info KV and the sessions stream unprovisioned.

**Primary fix:** move the `ensureUserResources` call to a location that runs on **every** successful login, regardless of NKey presence — ideally just before composing the login response. The call is idempotent on existence-check, so running it on every login (NKey or legacy) backfills any missing resources for users that previously logged in via the legacy path.

**Secondary investigations** (lower priority, only if the above doesn't fully resolve):
1. **`ensurePerUserInfoKV` config diff** — once it runs, verify `js.CreateKeyValue` with `History: 1` succeeds against the running NATS. Diff config against `ensurePerUserSessionsKV` / `ensurePerUserProjectsKV` if needed.
2. **`ensurePerUserSessionsStream` subject filter** — verify `mclaude.users.{uslug}.hosts.*.projects.*.sessions.>` doesn't overlap with a default stream and that slugs containing `-` interpolate cleanly.
3. **JWT permissions** — CP should have full account permissions; unlikely to be the issue but check if any new bucket subjects are missing from the CP's account JWT permission list.

After identifying the cause, the fix:
- **If a specific ensure-function errors**: fix that function (correct config, retry on transient error, etc.).
- **If a call site bypasses the umbrella function**: replace with `ensureUserResources(nc, uslug)`.
- **Always**: switch the umbrella function from "fail-fast on first error" to "log-and-continue, return first error after all four". This makes future regressions backfill on next login rather than wedge all four on one bad step.

### Integration tests

Add tests in `mclaude-control-plane/integration_test.go` (the existing file with the `httptest` server fixture). The ADR-0091 integration test file uses raw NATS without the HTTP server, so it cannot host the handleLogin-via-httptest case. New cases:

- `TestIntegration_EnsureUserResources_AllFour` — given a fresh NATS connection, call `ensureUserResources(nc, "test-user")` and assert all four resources exist.
- `TestIntegration_HandleLogin_ProvisionsAllFour` — go through the full login HTTP path (legacy + NKey both) and assert all four per-user resources exist after each.
- `TestIntegration_EnsureUserResources_Backfill` — pre-create only sessions+projects (simulating dev-mclaude-local), call `ensureUserResources`, assert all four exist with the existing two unchanged.
- `TestIntegration_EnsureUserResources_StepErrorContinues` — mock the third ensure to error; verify the fourth ensure was still attempted.

## Data Model

No schema changes. Resources defined in:
- `KV_mclaude-sessions-{uslug}` — already in `spec-state-schema.md` and `spec-nats-data-taxonomy.md`.
- `KV_mclaude-projects-{uslug}` — same.
- `KV_mclaude-user-info-{uslug}` — already in `spec-state-schema.md` and `spec-nats-data-taxonomy.md` (ADR-0091).
- `MCLAUDE_SESSIONS_{uslug}` stream — already in `spec-state-schema.md` (ADR-0054).

## Error Handling

- **Any single ensure-step errors**: logged at WARN with `bucket`/`stream` and error string. The chain continues to the next step. The function returns the first observed error to the caller after attempting all four.
- **Login handler receives an error from `ensureUserResources`**: error is logged at WARN with the user slug + error string, then **the login response proceeds normally** (provisioning is best-effort; missing resources retry on the next login). The current `auth.go:180` does `_ = resErr` (silently drops); this ADR adds an explicit `log.Warn().Err(resErr).Str("uslug", ...).Msg("ensureUserResources partial failure")` call. The `challenge.go:249` discard is similarly converted to a logged warning.
- **NATS unreachable at provisioning time**: the very first ensure call fails. All four are skipped. Login still proceeds (other code paths handle NATS reconnection separately). Resources backfill on the next successful login when NATS is reachable again.

## Security

No security changes. The CP's NATS connection (which executes `ensureUserResources`) already has account-wide permissions. The new bucket has the same client-only access model as the existing two.

## Impact

This is a class-A bug fix. **No spec changes** — the behavior described in the existing specs is correct; the code diverges and is being brought into compliance.

- `docs/mclaude-control-plane/spec-control-plane.md`: no changes (the "Per-user resource provisioning" section already lists all four resources).
- `docs/spec-state-schema.md`: no changes.
- `docs/spec-nats-data-taxonomy.md`: no changes.
- ADR-0054, ADR-0091: no changes (their decisions stand; this ADR records that the implementation is being corrected to match).

## Scope

**In v1:**
- Identify why `ensureUserResources` doesn't reach steps 3-4 in production.
- Fix the root cause.
- Convert the umbrella function to log-and-continue error semantics so future partial failures don't wedge all subsequent steps.
- Add a regression test that asserts all four resources exist after one `ensureUserResources` call.

**Deferred:**
- One-shot migration job that iterates all existing users and ensures resources. Not needed because `dev-mclaude-local` is the only existing user, and they'll backfill on next login.
- Telemetry / metrics on per-user resource creation. Out of scope.

## Open questions

_(none — all resolved.)_

## Integration Test Cases

| Test case | What it verifies | Setup/teardown | Components exercised |
|-----------|------------------|----------------|----------------------|
| Fresh user — all four resources created | Call `ensureUserResources(nc, "fresh-user")` against an empty NATS server. Assert KV `mclaude-sessions-fresh-user`, KV `mclaude-projects-fresh-user`, KV `mclaude-user-info-fresh-user`, and stream `MCLAUDE_SESSIONS_fresh-user` all exist. | testcontainers NATS; fresh JS state per test | mclaude-control-plane projects.go ensure-functions |
| Existing user with partial resources — backfill | Pre-create only sessions + projects KVs (simulating dev-mclaude-local's current state). Call `ensureUserResources(nc, "partial-user")`. Assert all four exist after the call, with the existing two unchanged (same stream sequence numbers / KV revisions as before). | Pre-populate two of four; call once; assert | projects.go ensure-functions, idempotency check |
| Login backfills missing resources | Simulate a login for a user whose row exists in Postgres but whose NATS resources are partial (the dev-mclaude-local case). Verify after `handleLogin` returns, all four resources exist. | Insert `users` row, manually create only sessions+projects KVs, call handleLogin via httptest, assert. | auth.go handleLogin → ensureUserResources |
| Single ensure-step transient error doesn't skip subsequent steps | Mock the `js.CreateKeyValue` for the user-info bucket to return an error. Call `ensureUserResources`. Assert: function returns the error, BUT the sessions stream (step 4) was still attempted and created. | Mock JetStream interface; intercept the third call; verify the fourth call still ran. | projects.go umbrella function error semantics |

## Implementation Plan

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| `mclaude-control-plane/projects.go` | ~30 lines (refactor `ensureUserResources` to log-and-continue + diff individual ensure-funcs) | 50k | Identify root cause first via runtime logs / unit test, then fix. |
| `mclaude-control-plane/auth.go` or `challenge.go` (if root cause is wrong call site) | ~5 lines | 10k | Only if the issue is a bypassed call site. |
| `mclaude-control-plane/integration_test.go` | ~80 lines (4 new test cases above) | 30k | testcontainers NATS already wired; just add cases. |

**Total estimated tokens:** 90k
**Estimated wall-clock:** 1h
