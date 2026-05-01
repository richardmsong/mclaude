## Run: 2026-05-01T20:15:00Z (Round 3)

Previous gap (round 2, 1 gap) — **resolved.** Fix verified:
- `outputTokensAtSoftMark` field added to QuotaMonitor struct definition (line 237) ✅
- Soft-stop path step 3 now explicitly says "captures `outputTokensAtSoftMark = <current cumulative output tokens>`" (line 96) ✅
- Select-loop soft threshold breached trigger also references "capture `outputTokensAtSoftMark = <current cumulative output tokens>`" (line 250) ✅
- Authoritative token counting formula `outputTokensSinceSoftMark = resultUsage.OutputTokens - outputTokensAtSoftMark` now has both fields defined in the struct ✅

Cross-checked against:
- `spec-state-schema.md` — QuotaStatus, lifecycle events, session KV schema consistent
- `spec-nats-payload-schema.md` — sessions.create, quota subject, lifecycle events consistent (spec still has `jobId` in lifecycle events per pre-ADR-0044 schema; ADR-0044 explicitly removes it — spec update deferred to implementation)
- `spec-session-agent.md` — QuotaMonitor behavior, permission policy, recovery mechanism consistent
- `adr-0054-nats-jetstream-permission-tightening.md` — per-user KV buckets, session stream, state→status rename, co-requisite deployment all consistent
- `spec-nats-activity.md` — soft-stop, hard-stop, resume flows match ADR-0044 descriptions
- Codebase: `quota_monitor.go`, `state.go`, `agent.go`, `session.go` — existing code structures match ADR descriptions where applicable; code divergences are expected (ADR describes target state, code is pre-implementation)

CLEAN — no blocking gaps found.

---

## Run: 2026-05-01T19:30:00Z (Round 2)

Previous gaps (runs 1-3, 12 total) — **all resolved.** Fixes verified:
- U5 conversion formula specified ✅
- ADR-0054 co-requisite explicitly called out ✅
- handleDelete branching condition (`SoftThreshold > 0`) specified ✅
- allowedTools breaking change acknowledged with migration ✅
- Two-strategy token counting (byte estimate + authoritative result event) ✅
- KV-based recovery (not stream replay) ✅
- Lifecycle subject pattern divergence note ✅
- sessions.input message format with JSON examples ✅
- `jobId` removed from lifecycle event payloads ✅
- `prompt` field added to QuotaMonitor struct ✅
- `pending → running` trigger added to select-loop ✅
- Intermediate turn-end + `turnEndedCh`/`doneCh` race — clarified with Stop hook suppression + nested select priority ✅
- `State`→`Status` rename — clean cut-over, no dual-read ✅

**Gaps found: 1**

1. **`outputTokensAtSoftMark` referenced in authoritative token counting formula but not defined in the QuotaMonitor struct** — The ADR's `QuotaMonitor` struct definition (§ QuotaMonitor Goroutine) lists `outputTokensSinceSoftMark int` but does NOT list `outputTokensAtSoftMark`. However, the authoritative (result event) counting strategy explicitly references it: `"outputTokensSinceSoftMark = resultUsage.OutputTokens - outputTokensAtSoftMark"`. This formula requires knowing the cumulative output token count at the moment the soft marker was injected. The soft-stop path says "resets `outputTokensSinceSoftMark = 0`" but does not say "captures `outputTokensAtSoftMark = <current cumulative output tokens>`." A developer implementing the struct verbatim would have `outputTokensSinceSoftMark` (for the byte estimate, starting at 0) but no field to store the authoritative snapshot needed for the correction formula at turn-end. They would need to add a field and infer when to populate it from context. The existing code (`quota_monitor.go:36`) has `outputTokensAtSoftMark int` on the struct — the ADR's struct definition dropped it.
   - **Doc**: QuotaMonitor struct (§ QuotaMonitor Goroutine) — `outputTokensSinceSoftMark int` is listed; no `outputTokensAtSoftMark` field.
   - **Doc**: Token counting, Authoritative strategy — `"outputTokensSinceSoftMark = resultUsage.OutputTokens - outputTokensAtSoftMark"` — references undefined field.
   - **Code**: `quota_monitor.go:36` — `outputTokensAtSoftMark int` exists in current code but was omitted from the ADR's struct definition.

---

## Run: 2026-05-01T12:00:00Z

**Gaps found: 4**

1. **`jobId` in lifecycle events with no source** — All five lifecycle event payloads (`session_job_complete`, `session_job_paused`, `session_job_cancelled`, `session_permission_denied`, `session_job_failed`) include a `"jobId": "..."` field, but the job queue is explicitly eliminated ("No separate job queue"), the `sessions.create` payload has no `jobId` field, the Session KV extensions do not include `jobId`, and the `QuotaMonitor` struct has no `jobId` field. A developer implementing these events cannot determine what value to populate. The activity spec (`spec-nats-activity.md` section 9g) has already dropped `jobId` from these events, making this an internal ADR inconsistency.
   - **Doc**: Data Model → Lifecycle Event Payloads — each event contains `"jobId": "..."`; User Flow → Creating a Scheduled Session — `sessions.create` has no `jobId`; Decisions → "No separate job queue"
   - **Code**: The existing `QuotaMonitorConfig` in `state.go:128` has `JobID string` from the pre-ADR-0044 job-queue model. The ADR's new `QuotaMonitor` struct and `sessions.create` payload do not include it, yet the event payloads still reference it.

2. **Missing `prompt` field in QuotaMonitor struct — deferred prompt delivery impossible** — The "Session Startup" flow describes gating prompt delivery on quota: when `u5 >= softThreshold`, the QuotaMonitor holds the prompt until quota recovers. However, the explicitly defined `QuotaMonitor` struct has no `prompt` field. The struct has `resumePrompt` (for resume nudges) but not the initial prompt. A developer implementing the struct verbatim has nowhere to store the deferred prompt.
   - **Doc**: Session Startup step 3: "`u5 >= softThreshold`: hold the prompt. CLI process is warm and idle on stdin — no tokens consumed. When a quota update arrives with `u5 < softThreshold`, send the prompt."
   - **Code**: The struct definition lists `sessionSlug`, `userSlug`, `hostSlug`, `projectSlug`, `softThreshold`, `hardHeadroomTokens`, `autoContinue`, `resumePrompt`, etc. — no `prompt` field.

3. **`pending → running` transition missing from QuotaMonitor select-loop** — The select-loop's quota-update handler defines three triggers: (a) "Soft threshold breached" (pauses a running session), (b) "Quota recovered" (resumes a **paused** session), (c) "No data" (no action). No trigger covers "session is `pending` AND `u5 < softThreshold` → send initial prompt, transition to `running`." The "No data" case mentions "do not start pending sessions," implying pending sessions are normally started somewhere, but none of the defined triggers handle it. A `pending` session is not `paused`, so trigger (b) explicitly excludes it.
   - **Doc**: QuotaMonitor "Goroutine select-loop cases" → `msg := <-m.quotaCh` lists only three triggers; "Quota recovered" says "session is paused, `u5 < softThreshold`" — excludes `pending`.
   - **Code**: N/A — new behavior not yet implemented.

4. **`handleTurnEnd()` fires premature `session_job_complete` on intermediate turn-ends** — `turnEndedCh` fires on every `result` event (the ADR calls `result` "Claude Code's canonical end-of-turn marker"). When `stopReason == ""` (no quota stop in progress), `handleTurnEnd()` publishes `session_job_complete` and sets session KV → `completed`. In multi-turn sessions where a Stop hook blocks an intermediate turn (Claude continues working), `result` fires for each completed turn — not just the final one. The first intermediate `result` would prematurely signal completion. The ADR provides no mechanism to distinguish the final turn-end from intermediate ones. The existing code avoids this by using `doneCh` (subprocess exit) for completion detection rather than per-turn events.
   - **Doc**: `handleTurnEnd()` table: `stopReason == ""` → `session_job_complete`, "CLI subprocess exits naturally." But `turnEndedCh` fires per-turn, not per-session-exit.
   - **Code**: Current `quota_monitor.go:181` (`run()`) uses `<-m.session.doneCh` (subprocess exit) for lifecycle event dispatch, not per-turn detection. The ADR adds `turnEndedCh` without clarifying its interaction with multi-turn scenarios.

## Run: 2026-05-01T18:45:00Z

**Gaps found: 3**

1. **`handleTurnEnd()` fires premature `session_job_complete` on intermediate turn-ends** — The ADR states that `onRawOutput` unconditionally sends on `turnEndedCh` for every `EventTypeResult` event ("Turn-end detection: if `evType == EventTypeResult`, send non-blocking on `turnEndedCh`"). When `stopReason == ""`, `handleTurnEnd()` publishes `session_job_complete` and updates session KV → `completed`. However, the ADR also states: "Multi-turn sessions (where Claude requests more input) emit `result` events between turns but the Stop hook blocks them, so `handleTurnEnd` is not called for intermediate turns." These are contradictory — `onRawOutput` fires on EVERY `result` event unconditionally, so `handleTurnEnd` WOULD be called for intermediate turns, prematurely publishing `session_job_complete`. The codebase confirms `result` events fire at each turn-end (tests in `session_test.go` count multiple `result` events across turns). The existing `QuotaMonitor` (`quota_monitor.go:181`) avoids this by using `<-m.session.doneCh` (subprocess exit) for lifecycle dispatch, not per-turn events. A developer cannot implement the specified `turnEndedCh`/`handleTurnEnd` mechanism correctly without knowing how to distinguish final turn-ends from intermediate ones.
   - **Doc**: "`onRawOutput(evType, raw)` handles: Turn-end detection: if `evType == EventTypeResult`, send non-blocking on `turnEndedCh`" AND "`handleTurnEnd()` fires on every stream-json `result` event" — unconditional per-`result` firing.
   - **Doc**: "Multi-turn sessions (where Claude requests more input) emit `result` events between turns but the Stop hook blocks them, so `handleTurnEnd` is not called for intermediate turns" — contradicts the unconditional firing.
   - **Code**: `session_test.go:TestCompactBoundaryUpdatesReplayFromSeq` verifies multiple `result` events across turns in a single session; `events.go:44` defines `EventTypeResult = "result"`.

2. **`doneCh`/`turnEndedCh` race in select loop — false `session_job_failed` on normal completion** — The ADR's select loop includes both `<-m.turnEndedCh` and `<-session.doneCh`. On natural completion, the sequence is: (1) Claude emits final `result` event → `onRawOutput` sends on `turnEndedCh`, (2) stdout reaches EOF → `doneCh` closes. Both happen in the same goroutine (`session.go` stdout router: `onRawOutput` is called inline, then the scanner loop exits and `close(s.doneCh)` fires at `session.go` deferred close). Because the select loop runs in a separate goroutine, both `turnEndedCh` and `doneCh` can become simultaneously ready. Go's `select` picks non-deterministically. If `doneCh` fires first, `handleSubprocessExit` sees `terminalEventPublished == false` (because `handleTurnEnd` hasn't run yet) and publishes `session_job_failed` with "subprocess exited without turn-end signal" — a false failure for a successful completion. The ADR does not specify priority ordering or draining of `turnEndedCh` before acting on `doneCh`.
   - **Doc**: Select-loop cases: "`<-m.turnEndedCh`: dispatch to `handleTurnEnd()`" and "`<-session.doneCh`: dispatch to `handleSubprocessExit()`" — no priority, no mutual exclusion.
   - **Doc**: `handleSubprocessExit`: "If `terminalEventPublished` → no-op. Otherwise → publish `session_job_failed`."
   - **Code**: `session.go` stdout router: `defer close(s.doneCh)` runs immediately after the scanner loop exits, which is immediately after processing the last stdout line (the `result` event). The `onRawOutput` callback (which sends on `turnEndedCh`) runs synchronously during that last line's processing, before `doneCh` closes — but the QuotaMonitor's select loop may not have consumed `turnEndedCh` before both become ready.

3. **Session KV `State` → `Status` field rename — no backward-compatibility transition specified** — The ADR renames the session KV JSON field from `"state"` to `"status"` (`json:"status"`) and extends the enum with `pending`, `paused`, `completed`, `cancelled`, `needs_spec_fix`. The codebase uses `State string \`json:"state"\`` in both `mclaude-session-agent/state.go:35` and `mclaude-common/pkg/types/types.go:53` (authoritative shared type). The `spec-state-schema.md` (which says "this document is updated first, in the same commit as the ADR") uses `"state"` with the old enum (`idle | running | requires_action | updating | restarting | failed | plan_mode | waiting_for_input | unknown`). A rolling deployment would have old agents writing `"state"` and new agents writing `"status"`. Old SPAs reading `"state"` would not see the value from new agents, and vice versa. The ADR does not specify: (a) whether a dual-read transition period is needed (e.g., read `status` first, fall back to `state`), (b) how the spec-state-schema.md update should be coordinated, or (c) whether a KV migration is required for existing entries.
   - **Doc**: Session KV Extensions: "The existing `State` field is **renamed to `Status`** (`json:\"status\"`)."
   - **Code**: `mclaude-common/pkg/types/types.go:53` — `State string \`json:"state"\``.
   - **Code**: `mclaude-session-agent/state.go:35` — `State string \`json:"state"\``.
