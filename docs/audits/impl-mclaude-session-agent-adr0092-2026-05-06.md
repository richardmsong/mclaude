# Implementation Evaluator: mclaude-session-agent (ADR-0092 scope)

**Component**: mclaude-session-agent
**Component root**: mclaude-session-agent/
**Scope**: ADR-0092 — `token_expired` status reporting on 401/403, Secret mount watch for recovery, transition back to running
**Run**: 2026-05-06T00:00:00Z

---

## Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0092:292 | "Report `status: "token_expired"` to session KV when the mounted token fails (Anthropic API returns 401/403). This is a new session status value surfaced to the SPA." | session.go:775-786, events.go:46 | IMPLEMENTED | — | `handleSideEffect` detects `r.IsError && isAnthropicAuthError(r.Error)` on `EventTypeResult`, sets `s.state.State = StatusTokenExpired`, flushes KV. `StatusTokenExpired = "token_expired"` defined in events.go:46. |
| adr-0092:293 | "session-agent watches the mounted Secret directory (`/secrets/tokens/`) via `fsnotify` (or polls every 30s)." | token_watch.go:39-62 | IMPLEMENTED | — | `watchTokenSecrets` tries `fsnotify.NewWatcher()` + `watcher.Add("/secrets/tokens")`, falls back to polling via `runTokenWatcherPoll` with `tokenPollInterval = 30s`. |
| adr-0092:293 | "When the token file is updated (user-agent writes new token after re-delivery), session-agent reads the new token" | token_watch.go:204-212, 256-282 | IMPLEMENTED | — | `restartTokenExpiredSession` calls `readTokenEnv()` → `readTokenEnvFromDir("/secrets/tokens")` which re-reads the file; injects as `newSess.extraEnv`. |
| adr-0092:293 | "retries the failed Anthropic API call, and transitions back to `running` status. No user action or session restart required" | token_watch.go:172-250 | PARTIAL | SPEC→FIX | The session is stopped and resumed via `driver.Resume()` (a full process restart using `--resume`), not a simple retry of the failed API call. The spec says "retries the failed API call" and "No session restart required." The implementation uses process restart, which achieves functional equivalence (session resumes from JSONL history) but is technically a restart, not an in-process retry. The spec should be updated to say "session-agent restarts the session process (driver.Resume) with the new token, transitioning back to idle/running." |
| spec-session-agent.md:29 | "Recovery: session-agent watches the mounted Secret directory (`/secrets/tokens/`) via `fsnotify` (or polls every 30s). When the token file is updated (user-agent writes new token after re-delivery), session-agent reads the new token, retries the failed API call, and transitions back to `running`." | token_watch.go:39-250, agent.go:218-221 | PARTIAL | SPEC→FIX | Same as adr-0092:293 — "retries the failed API call" and "transitions back to `running`" are imprecise. The implementation restarts the session process via `driver.Resume`, which transitions to `idle` (on init event), not directly to `running`. The spec should say "restarts the session process with the new token (driver.Resume); session transitions to idle on init, then running on the next user message." |
| spec-state-schema.md:289 | "`token_expired` — model-provider token invalid (401/403 from Anthropic); session-agent sets this, SPA renders reconnect banner (ADR-0092)" | events.go:46, session.go:782 | IMPLEMENTED | — | `StatusTokenExpired = "token_expired"` defined and used. The `SessionState.State` field (JSON key `status`) is updated to this value and flushed to KV. |
| adr-0092 (Component Changes, session-agent section) | "~15 lines" estimate with `token_expired` status reporting on 401/403 | session.go:771-787, events.go:46 | IMPLEMENTED | — | Token expired detection and status set is concise (~15 lines as estimated). |

---

## Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| token_watch.go:1-31 | INFRA | Package declaration, imports, constants (`tokenSecretsDir`, `tokenPollInterval`) — all directly spec'd by ADR-0092. |
| token_watch.go:33-62 | INFRA | `watchTokenSecrets` dispatcher — routes to fsnotify or polling path per spec. |
| token_watch.go:64-92 | INFRA | `runTokenWatcherFsnotify` — fsnotify event loop per spec. |
| token_watch.go:94-116 | INFRA | `runTokenWatcherPoll` — 30s poll fallback per spec. |
| token_watch.go:119-139 | INFRA | `latestMtime` helper — necessary infrastructure for poll-based detection. No independent spec requirement, but directly supports the spec'd polling behavior. |
| token_watch.go:141-165 | INFRA | `recoverTokenExpiredSessions` — iterates sessions, filters by `StatusTokenExpired`, delegates to `restartTokenExpiredSession`. Directly implements spec. |
| token_watch.go:167-250 | INFRA | `restartTokenExpiredSession` — implements recovery sequence: stop → read new token → rebuild session → start with resume → publish lifecycle events. Implements spec, with the implementation detail noted in Phase 1 (restart vs retry). |
| token_watch.go:252-282 | INFRA | `readTokenEnv` / `readTokenEnvFromDir` — reads `/secrets/tokens/${TOKEN_PROVIDER}`, returns `CLAUDE_CODE_OAUTH_TOKEN=<value>` env string. Required infrastructure for recovery path. |
| session.go:770-787 | INFRA | `EventTypeResult` case in `handleSideEffect`: accumulates usage, then checks `isAnthropicAuthError` if `r.IsError`, sets `StatusTokenExpired`. Directly spec'd. |
| session.go:796-822 | INFRA | `isAnthropicAuthError` — pattern-matching function for 401/403 error strings. Necessary implementation detail for the spec'd behavior. |
| agent.go:218-221 | INFRA | `go a.watchTokenSecrets(ctx)` goroutine launch in `Run()`. Directly spec'd by ADR-0092. |
| events.go:46 | INFRA | `StatusTokenExpired = "token_expired"` constant. Directly spec'd by spec-state-schema.md and ADR-0092. |

No UNSPEC'd or DEAD code found in the ADR-0092 scope files.

---

## Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| adr-0092:292 | Report `status: "token_expired"` on 401/403 | token_watch_test.go:21-58 (`TestIsAnthropicAuthError_*`), token_watch_test.go:67-120 (`TestTokenExpiredStatusOnAuthError`) | None found | UNIT_ONLY | `TestTokenExpiredStatusOnAuthError` uses real mock-claude binary and real session lifecycle (process spawn + NATS KV writes), making it a strong unit test. No integration test exercises this via a real cluster with real NATS JWT enforcement. |
| adr-0092:293 | Watch `/secrets/tokens/` via fsnotify or 30s poll | token_watch_test.go:128-208 (`TestReadTokenEnv_*`, `TestLatestMtime_*`) | None found | UNIT_ONLY | Tests cover `readTokenEnvFromDir` and `latestMtime` helpers. No test covers `watchTokenSecrets` or `recoverTokenExpiredSessions` end-to-end (watcher triggering recovery). |
| adr-0092:293 | Read new token, restart session, transition back to running | No test for end-to-end recovery (watchTokenSecrets → recoverTokenExpiredSessions → restartTokenExpiredSession → KV status returns to idle) | None found | UNTESTED | The individual helpers (readTokenEnv, latestMtime) are tested. The full recovery flow — token file updated → watcher fires → session restarts → KV transitions out of token_expired — has no test. |
| spec-state-schema.md:289 | `token_expired` in `SessionState.status` enum | token_watch_test.go:67-120 via kvCapture | None found | UNIT_ONLY | The unit test verifies the KV write using an in-process `kvCapture`. No integration test verifies the field appears in a real NATS KV entry. |

---

## Phase 4 — Bug Triage

No open bugs with `**Component**: mclaude-session-agent` found in `.agent/bugs/`.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| — | — | — | No open bugs matching this component. |

---

## Summary

- Implemented: 5
- Gap: 0
- Partial: 2
- Infra: 12
- Unspec'd: 0
- Dead: 0
- Tested (unit+integration): 0
- Unit only: 3
- E2E only: 0
- Untested: 1
- Bugs fixed: 0
- Bugs open: 0
