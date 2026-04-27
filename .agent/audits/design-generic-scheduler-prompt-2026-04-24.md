## Audit: 2026-04-24T15:30:00Z

**Document:** docs/adr-0034-generic-scheduler-prompt.md

### Round 1

**Gaps found: 9**

1. **NATS subject / KV key construction omits `hostSlug`** — all new dispatcher calls to `sessions.create`/`sessions.input`/`sessions.delete` and session-scoped KV lookups require `{hslug}` per ADR-0004; `subj` package helpers and `SessionsKVKey` take a `HostSlug` parameter. Design doc never mentions where it comes from or stores it on `JobEntry`.
2. **`mclaude-sessions` KV key needs 4 args** — same root cause as #1; `subj.SessionsKVKey(u, h, p, s)`.
3. **`JobEntry` schema omits `hostSlug`** — canonical `spec-state-schema.md` includes it; proposed struct drops fields but doesn't declare `hostSlug` retained.
4. **`cancelling` status not in enum** — cancel flow step 2 sets it; status column doesn't list it.
5. **`session_job_cancelled` missing from `spec-state-schema.md`; that spec not in Impact list** — new event, canonical schema not updated.
6. **`QuotaMonitorConfig` drops `priority` without explanation** — dispatcher still uses priority for preemption; unclear if field is removed, kept, or unused.
7. **`claudeSessionID` source unspecified** — doc says "readable from Claude Code's initial event output" but names no event type or field.
8. **Session-loss detection unspecified** — `sessions.input` is fire-and-forget; no reply to error on. `sessKV` lookup timing (before/after/on-timer) not stated.
9. **`autoContinue` + `ResumeAt` broken** — subscriber sets `ResumeAt = r5` when `autoContinue` is true, but `session_job_paused` payload in the same doc carries no `r5` field.

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|------------|------|
| 1-3 | `hostSlug` plumbing | Added `HostSlug` field to `JobEntry`; added to POST body as required; added Decisions-table row explaining retention from `spec-state-schema.md`; updated all dispatcher steps (First dispatch, Soft-stop, Cancel, Resume, Degraded fallback) to cite `subj.UserHostProject*` + `SessionsKVKey(u, h, p, s)` with `job.HostSlug` as the source; added validation row (hostSlug regex) to Error Handling; added to Scope. | factual |
| 4 | `cancelling` status | Removed from Cancel path flow — dispatcher now publishes interrupt + `sessions.delete` then waits for `runLifecycleSubscriber` to receive `session_job_cancelled` and write terminal status. No transient state. Status enum unchanged. | factual |
| 5 | `spec-state-schema.md` missing from Impact | Expanded Impact with explicit list of changes to `spec-state-schema.md` (JobEntry, status enum, lifecycle event payloads, `session_job_cancelled` add). Added to Scope. | factual |
| 6 | `QuotaMonitorConfig.priority` | Clarified in Session-Agent component changes: `Priority` is NOT carried on `QuotaMonitorConfig`; lives only on `JobEntry` and is used by dispatcher for preemption ordering; monitor never needs it. | factual |
| 7 | `claudeSessionID` source | Specified: session-agent parses the first stream-json event `{"type":"system","subtype":"init","session_id":"..."}` and extracts `session_id`. Noted that this is the canonical value accepted by `claude --resume` and stored at `~/.claude/projects/<projectHash>/<sessionId>.jsonl`. Session-agent reads synchronously before sending `sessions.create` reply. | factual (research via claude-code-guide agent) |
| 8 | Session-loss detection mechanism | Replaced ambiguous "sessions.input reply error, or sessKV miss" with explicit: dispatcher does synchronous `sessKV.Get` on the 4-part session key BEFORE sending resume nudge; missing key → degraded fallback. No reliance on fire-and-forget publish replies. | factual |
| 9 | `r5` missing from event payload | Added `r5` to the `session_job_paused` payload. QuotaMonitor includes it from its most recent `QuotaStatus` snapshot. Subscriber reads `ev["r5"]` for `ResumeAt`. Documented the monitor's `QuotaStatus` cache. | factual |

---

## Audit: 2026-04-24T17:00:00Z

**Document:** docs/adr-0034-generic-scheduler-prompt.md

### Round 2

**Gaps found: 5**

1. **`spec-state-schema.md` lifecycle event payloads not updated — `session_job_cancelled` is missing and `session_job_paused` payload is wrong** — The ADR's Impact section says `spec-state-schema.md` must be updated to add `session_job_cancelled` and the revised `session_job_paused` payload (with `pausedVia`, `r5`, `outputTokensSinceSoftMark`; without `priority`). But `spec-state-schema.md` still showed the old payloads: `session_job_paused` has `{"priority": 5, "u5": 76}` and no `pausedVia`, `r5`, or `outputTokensSinceSoftMark`; `session_job_cancelled` does not exist; `session_quota_interrupted` still appears as a separate event type; `session_job_complete` still carries `prUrl`. The ADR designates `spec-state-schema.md` as authoritative for schemas — leaving these contradictions unresolved means the design doc and canonical schema disagree on every lifecycle payload this ADR touches.
   - **Doc**: "Must reflect: ... updated `session_job_paused` payload (adds `pausedVia`, `r5`, `outputTokensSinceSoftMark`; removes `threshold`); removed `session_quota_interrupted`; removed `session_job_complete.prUrl`; new lifecycle event `session_job_cancelled`" (Impact section)
   - **Code**: `spec-state-schema.md` lines 539–560 still showed old payloads.

2. **`runLifecycleSubscriber` subscription subject is wrong and will miss all lifecycle events** — Existing code subscribes to `mclaude.users.{uslug}.projects.*.lifecycle.*`; the correct pattern is `mclaude.users.{uslug}.hosts.*.projects.*.lifecycle.*`. The ADR listed only the event→status map changes, not the subject fix.
   - **Doc**: "Modify lifecycle subscriber (`runLifecycleSubscriber`) event → status map" — no instruction to fix the subscription subject.
   - **Code**: `daemon_jobs.go:254`: missing `.hosts.*.` segment.

3. **`sessions.create` reply field name mismatch — doc says `claudeSessionID` but existing reply uses `id`** — Existing `handleCreate` replies with `{"id": sessionID}` (mclaude UUID). ADR adds `claudeSessionID` as a new field but did not clarify that `id` stays alongside it.
   - **Code**: `agent.go:808`: `a.reply(msg, map[string]string{"id": sessionID}, "")`.

4. **Cancel path: double-interrupt** — The doc said dispatcher sends `control_request` interrupt via `sessions.input` then `sessions.delete`; `handleDelete` internally calls `stopAndWait` which also sends an interrupt, resulting in two interrupts with no ordering guarantee.
   - **Code**: `agent.go:841`: `sess.stopAndWait(sessionDeleteTimeout)`.

5. **`spec-state-schema.md` `JobEntry` schema not updated** — Old fields (`specPath`, `threshold`, `prUrl`, `branch` with short-ID suffix) still present; new fields absent.
   - **Code**: `spec-state-schema.md` lines 212–234.

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|------------|------|
| 1, 5 | `spec-state-schema.md` schema + lifecycle payloads out of sync | Edited `docs/spec-state-schema.md` directly. `JobEntry` now lists all ADR-0034 fields (`prompt`, `title`, `branchSlug`, `resumePrompt`, `softThreshold`, `hardHeadroomTokens`, `permPolicy`, `allowedTools`, `claudeSessionID`, `pausedVia`), removes `specPath`/`threshold`/`prUrl`, drops the short-ID suffix from `branch`. Lifecycle payloads: removed `session_quota_interrupted`; rewrote `session_job_paused` with `pausedVia`/`r5`/`outputTokensSinceSoftMark`; removed `prUrl` from `session_job_complete`; added `session_job_cancelled`. Each payload now has a "Published by" note describing who writes it and when. | factual |
| 2 | `runLifecycleSubscriber` subscription subject missing `.hosts.*.` segment | Added explicit "Fix subscription subject" line in Component Changes — Daemon: replace `mclaude.users.{uslug}.projects.*.lifecycle.*` with `mclaude.users.{uslug}.hosts.*.projects.*.lifecycle.*`. Noted that without this fix, none of the ADR-0034 lifecycle logic functions. | factual |
| 3 | `id` vs `claudeSessionID` — two distinct values, one reply field | Rewrote Session-Agent component changes to spell out: `id` stays as the existing mclaude UUID (used for routing); `claudeSessionID` is added as a NEW separate reply field carrying the Claude Code session ID (used only for `--resume` fallback). Showed the concrete `a.reply(map[string]string{"id": ..., "claudeSessionID": ...})` transformation. | factual |
| 4 | Cancel path: double-interrupt + `handleDelete` publishes `session_stopped` not `session_job_cancelled` | Removed the dispatcher's explicit `sessions.input` interrupt step — `handleDelete`'s internal `stopAndWait` already interrupts. Cancel flow is now: dispatcher publishes `sessions.delete` (handleDelete interrupts + reaps + emits `session_stopped`), then dispatcher directly publishes `session_job_cancelled` on the lifecycle subject. `handleDelete` unchanged. Rationale documented: coupling `handleDelete` to job-queue events breaks the scheduled-vs-ad-hoc separation from ADR-0009. Matching update in `spec-state-schema.md`'s `session_job_cancelled` "Published by" note. | factual |

---

## Audit: 2026-04-24T19:00:00Z

**Document:** docs/adr-0034-generic-scheduler-prompt.md

### Round 3

**Gaps found: 4**

1. **`daemon_jobs.go` calls 2-arg `subj` helpers that do not exist — the `subj` package only exports 3-arg host-scoped helpers** — The ADR instructs the developer to replace all 2-arg subject helpers with the canonical 3-arg host-scoped versions (`UserHostProjectAPISessionsCreate`, `UserHostProjectAPISessionsInput`, `UserHostProjectAPISessionsDelete`, `UserHostProjectLifecycle`) sourcing the host slug from `job.HostSlug`. The existing code calls `subj.UserProjectAPISessionsCreate`, `subj.UserProjectAPISessionsInput`, `subj.UserProjectAPISessionsDelete`, and `subj.UserProjectLifecycle` — none of which exist in `mclaude-common/pkg/subj/subj.go`. Similarly `subj.SessionsKVKey` takes 4 args (`u, h, p, s`) but the code calls it with 3 args (`u, p, s`) at `daemon_jobs.go:377` and `daemon_jobs.go:491`. The ADR specifies the migration in its Scope and Component Changes sections, but the instruction is stated as "replace 2-arg helpers with 3-arg helpers" — which implies the 2-arg helpers currently exist. They do not. A developer who reads the code will find the code does not compile at all against the current `subj` package. The blocking question is: were the old 2-arg helpers removed from `subj` before this ADR was written, or are they still expected to be there? The code calls them as if they exist; the `subj` package has already removed them. This must be resolved so the developer knows the current starting state of the code.
   - **Doc**: "Modify all subject/KV key construction: replace 2-arg helpers (`UserProjectAPISessionsCreate`, etc.) with the canonical host-scoped 3-arg helpers" (Component Changes — Daemon)
   - **Code**: `mclaude-common/pkg/subj/subj.go` — no `UserProjectAPISessionsCreate`, `UserProjectAPISessionsInput`, `UserProjectAPISessionsDelete`, or `UserProjectLifecycle` functions exist (confirmed by reading full file). `daemon_jobs.go:342`: `subj.UserProjectAPISessionsCreate(d.cfg.UserSlug, slug.ProjectSlug(job.ProjectID))` — will not compile. `daemon_jobs.go:377`: `subj.SessionsKVKey(d.cfg.UserSlug, slug.ProjectSlug(job.ProjectID), slug.SessionSlug(sessionID))` — 3-arg call to a 4-arg function, will not compile.

2. **`JobEntry` Go struct in `state.go` still uses old schema — new fields absent, removed fields present** — The ADR and the updated `spec-state-schema.md` both specify the new `JobEntry` schema (adds `Prompt`, `Title`, `BranchSlug`, `ResumePrompt`, `HostSlug`, `SoftThreshold`, `HardHeadroomTokens`, `PermPolicy`, `AllowedTools`, `ClaudeSessionID`, `PausedVia`; removes `SpecPath`, `Threshold`, `PRUrl`). The actual Go struct at `state.go:141–163` still has the old shape: `SpecPath`, `Threshold`, `PRUrl` present; none of the new fields present. The ADR does not instruct the developer to update `state.go` (only `daemon_jobs.go`, `session.go`, `quota_monitor.go`, `agent.go`, and spec files are mentioned in Component Changes). A developer implementing ADR-0034 would write the new dispatcher logic referencing `job.Prompt`, `job.BranchSlug`, `job.SoftThreshold`, etc. against a struct that has none of those fields. The struct definition is the single source of truth for what can be compiled.
   - **Doc**: Data Model § `JobEntry` — full new struct definition with all new fields. Component Changes — Daemon: "dispatchQueuedJob reads `job.Prompt` verbatim", "uses `job.BranchSlug` for branch name", etc.
   - **Code**: `mclaude-session-agent/state.go:141–163` — `JobEntry` struct has `SpecPath string`, `Threshold int`, `PRUrl string`, `Branch string`; missing `Prompt`, `Title`, `BranchSlug`, `ResumePrompt`, `HostSlug`, `SoftThreshold`, `HardHeadroomTokens`, `PermPolicy`, `AllowedTools`, `ClaudeSessionID`, `PausedVia`.

3. **`QuotaMonitorConfig` struct in `state.go` still has old fields — `Threshold`/`Priority` present; `SoftThreshold`/`HardHeadroomTokens` absent** — The ADR specifies `QuotaMonitorConfig` changes: `Threshold int` → `SoftThreshold int`, `HardHeadroomTokens int` added, `Priority int` removed (priority lives only on `JobEntry`). The actual struct at `state.go:132–137` still has `Threshold int` and `Priority int`; no `SoftThreshold` or `HardHeadroomTokens`. The ADR's Component Changes — Session-Agent section describes these changes explicitly, but does not reference `state.go` as a file to modify — the developer would need to infer it. More critically, the ADR body is internally consistent (all quota-monitor code in the doc uses `SoftThreshold`/`HardHeadroomTokens`) but the code uses `Threshold`/`Priority`, and the sessions.create payload the dispatcher currently sends has `"threshold": job.Threshold, "priority": job.Priority`. A developer would not know which file defines `QuotaMonitorConfig` without searching, and would not know that `state.go` must be updated unless the ADR names it.
   - **Doc**: Component Changes — Session-Agent: "`QuotaMonitorConfig`: `SoftThreshold int`, `HardHeadroomTokens int` replaces `Threshold int`. `Priority` is NOT carried on `QuotaMonitorConfig`."
   - **Code**: `mclaude-session-agent/state.go:132–137`: `QuotaMonitorConfig` has `Threshold int`, `Priority int`; missing `SoftThreshold int`, `HardHeadroomTokens int`.

4. **`spec-state-schema.md` `session_job_cancelled` "Published by" note contradicts the ADR's cancel path** — The updated `spec-state-schema.md` (as fixed in Round 2) describes `session_job_cancelled` as: "Published by: daemon dispatcher when handling `DELETE /jobs/{id}`, after publishing the `control_request` interrupt on `sessions.input` and the `sessions.delete` message." But the ADR's own cancel path (as fixed in Round 2) explicitly says the dispatcher does NOT send a `control_request` interrupt on `sessions.input` — that step was removed because `handleDelete`'s internal `stopAndWait` already sends the interrupt. A developer reading `spec-state-schema.md` sees that the cancel path involves a `sessions.input` interrupt; a developer reading the ADR sees it does not. These are contradictory instructions in the same commit's deliverables.
   - **Doc**: Cancel path step 2: "Dispatcher publishes `sessions.delete` on `subj.UserHostProjectAPISessionsDelete(...)`. Session-agent's existing `handleDelete` calls `sess.stopAndWait(sessionDeleteTimeout)`, which internally sends a `control_request` interrupt... The dispatcher does NOT send a separate `sessions.input` `control_request` interrupt."
   - **Code**: `docs/spec-state-schema.md` lines 578–579: `session_job_cancelled` "Published by" note says "after publishing the `control_request` interrupt on `sessions.input` and the `sessions.delete` message" — which describes the old (removed) behavior.

---

## Audit: 2026-04-24T18:00:00Z

**Document:** docs/adr-0034-generic-scheduler-prompt.md

### Round 3

**Gaps found: 4**

1. `daemon_jobs.go` calls 2-arg `subj` helpers that do not exist — current code doesn't compile against the canonical `subj` package.
2. `JobEntry` struct in `state.go` still uses old schema; ADR doesn't name `state.go` as a file to modify.
3. `QuotaMonitorConfig` struct in `state.go` still has old fields; same issue.
4. `spec-state-schema.md` `session_job_cancelled` note still references the removed `sessions.input` interrupt step (stale from pre-Round-2 text).

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|------------|------|
| 1 | Non-existent 2-arg `subj` helpers | Reworded Component Changes — Daemon subj/KV section to describe the current state explicitly: current `daemon_jobs.go` calls helpers that no longer exist in `subj`, so ADR-0034 doubles as the fix. Named the exact call sites and listed the canonical replacement signatures. | factual |
| 2, 3 | `state.go` not listed as a file to modify | Added new "Session-Agent struct definitions (`mclaude-session-agent/state.go`)" section at the top of Component Changes, specifying: `JobEntry` and `QuotaMonitorConfig` struct rewrites, with a note that `state.go` must be updated FIRST before dispatcher/monitor code changes. Also added `state.go` to the Impact "Components implementing the change" list and a new Scope bullet. | factual |
| 4 | `spec-state-schema.md` cancel-path note stale | Edited `spec-state-schema.md` `session_job_cancelled` "Published by" block: removed the reference to the dispatcher sending a `sessions.input` interrupt; now explicitly states the dispatcher publishes only `sessions.delete` (handleDelete's internal `stopAndWait` handles the interrupt) then the lifecycle event. | factual |

---

## Run: 2026-04-24T21:00:00Z

**Document:** docs/adr-0034-generic-scheduler-prompt.md
**Round:** 4 (after Round-3 fixes applied)

**Gaps found: 4**

1. **`state.go:sessionKVKey` wrapper calls `subj.SessionsKVKey` with 3 args — compile error not covered by the ADR** — `state.go` line 70 defines `sessionKVKey(userSlug, projectSlug, sessionSlug)` which calls `subj.SessionsKVKey(userSlug, projectSlug, sessionSlug)`. The `subj` package's `SessionsKVKey` requires 4 args `(u, h, p, s)` — a host slug is the second parameter. This is a compile error in `state.go` itself (pre-existing from ADR-0004 migration). The ADR's new "Session-Agent struct definitions" section says to rewrite `JobEntry` and `QuotaMonitorConfig` in `state.go`, but does not mention updating `sessionKVKey`. All callers of `sessionKVKey` in `agent.go` pass only 3 args and would also need a host slug argument. The ADR does not identify this as a file+function to fix, and does not specify where the agent gets its host slug for this call. Without a fix, the code will not compile even after applying all ADR-0034 instructions.
   - **Doc**: Component Changes § "Session-Agent struct definitions (`mclaude-session-agent/state.go`)" — mentions `JobEntry` and `QuotaMonitorConfig` only; silent on `sessionKVKey`.
   - **Code**: `state.go:69–71`: `func sessionKVKey(userSlug slug.UserSlug, projectSlug slug.ProjectSlug, sessionSlug slug.SessionSlug) string { return subj.SessionsKVKey(userSlug, projectSlug, sessionSlug) }` — 3-arg call to a 4-arg function. `subj.go:178`: `func SessionsKVKey(u slug.UserSlug, h slug.HostSlug, p slug.ProjectSlug, s slug.SessionSlug) string`.

2. **`state.go:heartbeatKVKey` wrapper calls `subj.ProjectsKVKey` with 2 args — same compile error, same omission** — `state.go` line 75 defines `heartbeatKVKey(userSlug, projectSlug)` which calls `subj.ProjectsKVKey(userSlug, projectSlug)`. `subj.ProjectsKVKey` requires 3 args `(u, h, p)`. Again, a pre-existing compile error from ADR-0004 that the ADR instructs `state.go` changes but does not address this function.
   - **Doc**: Same omission as Gap 1.
   - **Code**: `state.go:75–77`: `func heartbeatKVKey(userSlug slug.UserSlug, projectSlug slug.ProjectSlug) string { return subj.ProjectsKVKey(userSlug, projectSlug) }` — 2-arg call to a 3-arg function. `subj.go:187`: `func ProjectsKVKey(u slug.UserSlug, h slug.HostSlug, p slug.ProjectSlug) string`.

3. **`startupRecovery` in `daemon_jobs.go` also calls `subj.SessionsKVKey` with 3 args — not mentioned in the ADR's call-site list** — The ADR's Component Changes — Daemon section says to replace all `SessionsKVKey` 3-arg calls in `daemon_jobs.go` with the 4-arg host-scoped form, and names `dispatchQueuedJob` explicitly. But `startupRecovery` (line 491) also calls `subj.SessionsKVKey(slug.UserSlug(job.UserID), slug.ProjectSlug(job.ProjectID), slug.SessionSlug(job.SessionID))` with 3 args. The ADR does not mention this call site. A developer following the ADR's explicit call-site list would fix `dispatchQueuedJob` but leave `startupRecovery` broken, and the code still would not compile.
   - **Doc**: Component Changes — Daemon: "Replace every call site in `daemon_jobs.go` with the host-scoped variant. Source the host slug from `job.HostSlug`" — does not enumerate `startupRecovery` as a call site.
   - **Code**: `daemon_jobs.go:491`: `kvKey := subj.SessionsKVKey(slug.UserSlug(job.UserID), slug.ProjectSlug(job.ProjectID), slug.SessionSlug(job.SessionID))` — 3-arg call.

4. **`QuotaMonitor` has no mechanism to detect "Claude's turn ended while subprocess is alive" — `doneCh` only fires on subprocess exit** — The ADR instructs QuotaMonitor to call `publishExitLifecycle()` on `session.doneCh` close and to emit `session_job_paused` when "marker injected + turn ended + subprocess still alive." These are contradictory: `session.doneCh` is closed by the stdout-router goroutine's `defer close(s.doneCh)` (session.go:234), which only fires when the Claude subprocess's stdout EOF is reached — i.e., when the subprocess exits. For soft-stop and hard-stop, the ADR explicitly says "Subprocess stays alive (the interrupt is not a kill)" and "Session stays alive." So `doneCh` will not close after a soft or hard stop. The ADR gives no alternative signal for detecting that Claude's turn ended while the subprocess is alive (e.g., observing a stream-json `result` event indicating turn completion, or a KV state transition to `idle`). A developer implementing the QuotaMonitor select-loop as described cannot emit `session_job_paused` on turn-end because the signal the ADR names (`doneCh`) will not fire for that case.
   - **Doc**: Component Changes — Session-Agent: "Select-loop: on `session.doneCh` close → `publishExitLifecycle()`." Data Model § paused path: "Session-agent reports session state idle. QuotaMonitor's lifecycle event publishes `session_job_paused`..." How-platform-distinguishes section: "Marker injected + turn ended + subprocess still alive → `session_job_paused`."
   - **Code**: `session.go:233–234`: stdout router goroutine has `defer close(s.doneCh)` — closes when `stdout` reaches EOF, i.e., when the Claude subprocess exits. `quota_monitor.go:221`: `case <-m.session.doneCh:` — existing code uses this only for subprocess-exit detection. No "turn ended, subprocess alive" signal exists in `Session`.

---

## Audit: 2026-04-24T18:45:00Z

**Document:** docs/adr-0034-generic-scheduler-prompt.md

### Round 4

**Gaps found: 4**

1. `state.go:sessionKVKey` wrapper calls 3-arg `subj.SessionsKVKey` — compile error not covered.
2. `state.go:heartbeatKVKey` calls 2-arg `subj.ProjectsKVKey` — same.
3. `startupRecovery` in `daemon_jobs.go` also calls 3-arg `SessionsKVKey` — not enumerated.
4. QuotaMonitor has no mechanism to detect "turn ended while subprocess alive" — `doneCh` only fires on subprocess exit, which doesn't happen on soft/hard stop.

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|------------|------|
| 1, 2 | `state.go` local KV-key wrappers have stale signatures | Expanded the "Session-Agent struct definitions and KV-key wrappers" section to include `sessionKVKey` (add host slug → forward to 4-arg `subj.SessionsKVKey`) and `heartbeatKVKey` (add host slug → forward to 3-arg `subj.ProjectsKVKey`), with new signatures and a note that every call site across `agent.go`, `daemon.go`, `daemon_jobs.go`, `session.go` must pass the extra host slug. Named the sources (`job.HostSlug`, `req.HostSlug`, session-agent config). | factual |
| 3 | `startupRecovery` SessionsKVKey call site unenumerated | Expanded the Daemon subj/KV section to enumerate every call site: `dispatchQueuedJob`, `startupRecovery`, quota-exceeded path in `processDispatch`, cancel path in `handleJobByID`. | factual |
| 4 | No turn-end signal while subprocess alive | Rewrote the QuotaMonitor component and the "How the platform distinguishes" section to use Claude Code's stream-json `result` event as the turn-end signal. New state: `turnEndedCh` + `handleTurnEnd()` + `handleSubprocessExit()`. `onRawOutput` now handles two jobs (token counting + turn-end detection on `EventTypeResult`). `handleTurnEnd` publishes the correct lifecycle event based on `stopReason`. `handleSubprocessExit` is the crash detector. Added test cases covering synthetic `result` events and the terminal-event-already-published no-op. | factual |

---

## Run: 2026-04-24T23:00:00Z

**Document:** docs/adr-0034-generic-scheduler-prompt.md
**Round:** 5 (after Round-4 fixes applied)

**Gaps found: 6**

1. **Dual writers for `session_job_paused` status transition** — The soft-stop user-flow step 6 says "Dispatcher updates `JobEntry`: `Status = paused`, `PausedVia = quota_soft`." The hard-stop step 5 says the same. But Component Changes for `runLifecycleSubscriber` also says "`session_job_paused` → `Status = paused`, `PausedVia = ev['pausedVia']`". The cancel path uses the single-subscriber-write model ("Does not write `Status = cancelled` directly; waits for `runLifecycleSubscriber`"). A developer cannot determine whether the dispatcher or the lifecycle subscriber is the authoritative writer for `paused` status. If both write, there is a race; if only one should write, the other must be removed, but the doc prescribes both.
   - **Doc**: "Dispatcher updates `JobEntry`: `Status = paused`, `PausedVia = quota_soft`." (§ Soft-stop path, step 6) vs "Modify lifecycle subscriber event → status map: `session_job_paused` → `Status = paused`, `PausedVia = ev['pausedVia']`" (§ Component Changes — Daemon, runLifecycleSubscriber).
   - **Code**: `daemon_jobs.go:298–302` — existing `runLifecycleSubscriber` treats `session_job_paused` as a no-op and logs it, meaning the dispatcher currently writes the `paused` status directly. Neither pattern from the ADR is unambiguous about which to implement.

2. **`Agent` struct has no `hostSlug` — lifecycle publishing and session event publishing will silently route to wrong subjects** — `publishLifecycle`, `publishLifecycleExtra`, and `publishPermDenied` in `agent.go` call `subj.UserProjectLifecycle(a.userSlug, a.projectSlug, ...)` (lines 1136, 1163, 1180). That function does not exist in the canonical `subj` package; the package exports only `UserHostProjectLifecycle(u, h, p, s)` requiring a `HostSlug`. The ADR requires QuotaMonitor to publish `session_job_paused` and `session_job_complete` via `publishLifecycleExtra`. The ADR's Component Changes — Session-Agent section does not call out adding `hostSlug` to `Agent` or `NewAgent`. Without this, every lifecycle publication from the session-agent will fail to compile. Similarly, `session.go:241` builds the event subject as `"mclaude.users." + s.state.UserSlug + ".projects." + s.state.ProjectSlug + ".events." + s.state.Slug`, missing `.hosts.{hslug}.`; `SessionState` has no `HostSlug` field. The ADR's Component Changes — Session-Agent section does not mention fixing event subject construction.
   - **Doc**: Component Changes — Session-Agent says to modify `session.go` only for `ResumeClaudeSessionID` plumbing, `SESSION_JOB_COMPLETE:` removal, and `claudeSessionID` in reply. No mention of `Agent.hostSlug`, `NewAgent` changes, `publishLifecycle*` function signatures, or fixing the event subject in `session.go`.
   - **Code**: `agent.go:1136`: `subj.UserProjectLifecycle(a.userSlug, a.projectSlug, slug.SessionSlug(sessionID))` — function does not exist in `subj.go`. `agent.go:78`: `Agent` struct has `userSlug slug.UserSlug` and `projectSlug slug.ProjectSlug` but no `hostSlug slug.HostSlug`. `session.go:241`: event subject missing `.hosts.{hslug}.` segment. `SessionState` (`state.go:29–49`) has no `HostSlug` field.

3. **`session.go:start()` resume path uses mclaude session ID, not Claude Code session ID — mechanism for passing `claudeSessionID` into `start()` is unspecified** — `start(claudePath, resume bool, ...)` when `resume=true` passes `--resume s.state.ID` (the mclaude UUID, `session.go:174`). For the degraded fallback path the correct value is the Claude Code session ID (`JobEntry.ClaudeSessionID`). `SessionState` has no field to carry this value. The ADR says `handleCreate` should accept `ResumeClaudeSessionID` and spawn `claude --resume <claudeSessionID>`, but does not specify whether this is achieved by adding a new field to `SessionState`, changing the `start()` signature to accept the ID directly, or using `ExtraFlags`. Without this, a developer cannot implement the `--resume <claudeSessionID>` fallback.
   - **Doc**: "session-agent's `handleCreate` sees `resumeClaudeSessionID` non-empty and spawns `claude --resume <claudeSessionID>` instead of a fresh session." (§ Degraded fallback). Component Changes — Session-Agent: "New field on the `sessions.create` request: `ResumeClaudeSessionID string`."
   - **Code**: `session.go:163–174` — `start(claudePath string, resume bool, publish ..., writeKV ...)`. When `resume=true`: `"--resume", s.state.ID`. `SessionState` has no `ClaudeSessionID` or equivalent field. `session.go` is listed for modification only for `ResumeClaudeSessionID` plumbing and removing `SESSION_JOB_COMPLETE:` scanning; the mechanism for threading a different ID into `start()` is not named.

4. **`handleDelete` worktree removal conflicts with "platform does not prune worktrees"** — The ADR states "Platform does not prune worktrees. The caller's PR/merge workflow owns branch lifecycle." (§ Decisions, Worktree cleanup). The ADR also says "This ADR does NOT modify `handleDelete`." But `handleDelete` at `agent.go:850–864` calls `gitWorktreeRemove` when the deleted session is the last user of the worktree, unconditionally for all session types. For scheduled sessions sharing a worktree (same `branchSlug`), completing or cancelling the last session will silently remove the worktree. A developer cannot tell whether to exclude scheduled sessions from the worktree-removal logic, and if so, by what criterion (branch prefix match, a new `SessionState` flag, etc.).
   - **Doc**: "Platform does not prune worktrees." (§ Decisions, Worktree cleanup) and "This ADR does NOT modify `handleDelete`." (§ Cancel path).
   - **Code**: `agent.go:850–864` — `handleDelete` checks `lastUser := !a.worktreeInUse(st.Worktree)` and if true calls `a.gitWorktreeRemove(repoPath, worktreePath)`. No exclusion for scheduled-session worktrees.

5. **`JobEntry` Go struct omits `UserSlug`, `ProjectSlug`, `SessionSlug` fields that the dispatcher needs for KV key construction** — `spec-state-schema.md` states "Dispatcher uses slug fields (`userSlug`, `hostSlug`, `projectSlug`, `sessionSlug`) to construct KV keys into `mclaude-sessions`." The existing `state.go:141–163` `JobEntry` has `UserSlug`, `ProjectSlug`, `SessionSlug` (as `sessionSlug,omitempty`) and the new `HostSlug`. The ADR's Data Model § `JobEntry` Go struct lists `UserID` and `ProjectID` but does not include `UserSlug`, `ProjectSlug`, or `SessionSlug`. The dispatcher's resume-path `sessKV.Get` requires `SessionsKVKey(userSlug, hostSlug, projectSlug, sessionSlug)` — all four slug-typed values. Without `SessionSlug` on `JobEntry`, the dispatcher cannot construct this key. The ADR's struct definition must declare whether these slug fields are retained or removed.
   - **Doc**: ADR Data Model `JobEntry` struct (§ Data Model): lists `SessionID string \`json:"sessionId"\`` but no `SessionSlug`, `UserSlug`, or `ProjectSlug`. Spec-state-schema `JobEntry` includes `"sessionSlug"`, `"userSlug"`, `"projectSlug"`.
   - **Code**: `state.go:148`: `SessionSlug string \`json:"sessionSlug,omitempty"\``. `state.go:143–144`: `UserSlug`, `ProjectSlug` present. The ADR's struct silently drops these without saying so.

6. **`session_job_paused` soft-stop flow: dispatcher injects the `MCLAUDE_STOP:` marker via `sessions.input`, but the soft-stop detection now lives in the `QuotaMonitor` — dual trigger paths not reconciled** — The ADR's soft-stop user-flow step 2 says "Dispatcher injects `MCLAUDE_STOP: quota_soft` via `sessions.input` on `subj.UserHostProjectAPISessionsInput(...)`". The ADR's QuotaMonitor select-loop section says the monitor itself sets `stopReason = "quota_soft"` and injects `"MCLAUDE_STOP: quota_soft"` on `s.stdinCh` when `u5 >= SoftThreshold`. These are two separate injection paths — the daemon dispatcher publishes to the NATS `sessions.input` subject, and the QuotaMonitor writes to `session.stdinCh` directly — for the same event. The existing ADR-0009 soft-stop path in `processDispatch` also injects the stop message via the NATS subject (line 636 of `daemon_jobs.go`). If both are retained, Claude receives two identical stop messages. The ADR does not say to remove the dispatcher's NATS injection; it only describes the monitor's direct injection. A developer cannot tell which path is authoritative.
   - **Doc**: Soft-stop user-flow step 2: "Dispatcher injects `MCLAUDE_STOP: quota_soft` via `sessions.input` on `subj.UserHostProjectAPISessionsInput(...)`". QuotaMonitor select-loop: "If `u5 >= SoftThreshold` and `stopReason == \"\"`: set `stopReason = \"quota_soft\"`; inject `MCLAUDE_STOP: quota_soft` on `s.stdinCh`" (§ Component Changes — Session-Agent).
   - **Code**: `daemon_jobs.go:636` — existing `processDispatch` sends the stop message via `d.nc.Publish(inputSubject, stopMsg)` on the NATS subject. `quota_monitor.go` currently has `sendGracefulStop()` which also sends to `s.stdinCh`. ADR-0034's Component Changes — Daemon says to rename `QUOTA_THRESHOLD_REACHED` → `MCLAUDE_STOP: quota_soft` in `processDispatch` (§ "Modify quota-exceeded path"), implying the dispatcher injection is retained. The QuotaMonitor injection is also added. Double injection is not addressed.

---

## Audit: 2026-04-24T19:45:00Z

**Document:** docs/adr-0034-generic-scheduler-prompt.md

### Round 5

**Gaps found: 6**

1. Dual writers for `session_job_paused` status transition — dispatcher AND subscriber both claimed authority.
2. `Agent` struct has no `hostSlug`; lifecycle/event publishing uses 2-arg subj helpers that don't exist.
3. `session.go:start()` resume uses mclaude session ID, not Claude Code session ID; `SessionState` has no `ClaudeSessionID` field.
4. `handleDelete` unconditionally removes worktree, conflicts with "platform does not prune worktrees" for scheduled sessions.
5. `JobEntry` Go struct omits `UserSlug`, `ProjectSlug`, `SessionSlug` slug fields needed for KV key construction.
6. Double soft-stop marker injection — dispatcher via NATS AND QuotaMonitor via stdinCh.

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|------------|------|
| 1 | Dual writers for paused status | Rewrote Soft-stop and Hard-stop user-flow steps. Dispatcher is the marker injector (for quota_soft) and does NOT write `Status = paused`. `runLifecycleSubscriber` is the single writer of terminal statuses, matching ADR-0009's pattern and the cancel path. Added step 6 in each flow explicitly naming the subscriber as the writer. | factual |
| 2 | `Agent` struct missing `hostSlug` | Added new Session-Agent component change item: add `hostSlug` to `Agent` struct; `NewAgent` populates it; all `publishLifecycle*` + event-subject construction uses 4-arg host-scoped subj helpers. Flagged as another pre-existing compilation bug that ADR-0034 must close. | factual |
| 3 | `start()` resume mechanism / missing `ClaudeSessionID` on SessionState | Added: `SessionState` gets `ClaudeSessionID` field; `handleCreate` populates it from the parsed system/init event; `start()` passes `--resume s.state.ClaudeSessionID` when non-empty, falling back to old behavior for non-scheduled sessions. | factual |
| 4 | `handleDelete` worktree-prune conflicts with "platform does not prune" | Added explicit carve-out in `handleDelete`: `if strings.HasPrefix(sessionBranch, "schedule/") { skip worktree prune }`. Everything else in `handleDelete` (session_stopped emission, subprocess termination) stays unchanged. Updated the "this ADR does NOT modify handleDelete" claim to "only modifies the worktree-prune decision; leaves lifecycle emission + subprocess termination untouched." | factual |
| 5 | `JobEntry` Go struct missing slug fields | Added `UserSlug`, `ProjectSlug`, `SessionSlug` to the JobEntry struct definition in Data Model. Matches spec-state-schema.md's canonical shape. | factual |
| 6 | Double soft-stop marker injection | Clarified in user flow: dispatcher is the sole marker injector; QuotaMonitor observes the same quota NATS topic independently and sets its local `stopReason = "quota_soft"` as a state-only update (no stdin write). Both react to the same u5 threshold; only the dispatcher writes to the conversation. Hard-stop remains monitor-local because it's token-count-driven and must fire immediately. | factual |

---

## Run: 2026-04-24T25:00:00Z

**Document:** docs/adr-0034-generic-scheduler-prompt.md
**Round:** 6 (after Round-5 fixes applied)

**Gaps found: 3**

1. **QuotaMonitor select-loop still says "inject `MCLAUDE_STOP: quota_soft` on `s.stdinCh`" — contradicts the Round-5 fix that made the dispatcher the sole injector** — The soft-stop user-flow (step 2) correctly states: "Dispatcher (sole marker injector): publishes `sessions.input`... QuotaMonitor does NOT inject the marker." But Component Changes — Session-Agent, QuotaMonitor select-loop (line 337) still reads: "If `u5 >= SoftThreshold` and `stopReason == ""`: set `stopReason = "quota_soft"`; **inject `MCLAUDE_STOP: quota_soft` on `s.stdinCh`**; reset `outputTokensSinceSoftMark = 0`." The Round-5 fix updated the user-flow but left the select-loop instruction unchanged. A developer implementing the QuotaMonitor select-loop from the Component Changes section will write stdin injection code that the user-flow explicitly prohibits. One of these two descriptions must be authoritative; they currently give opposite instructions.
   - **Doc**: § Soft-stop path step 2: "QuotaMonitor does NOT inject the marker." vs. § Component Changes — Session-Agent, QuotaMonitor select-loop: "inject `MCLAUDE_STOP: quota_soft` on `s.stdinCh`."
   - **Code**: `quota_monitor.go:118–130` — `sendGracefulStop()` writes to `m.session.stdinCh`. Under the user-flow, this method should NOT be called on soft-stop quota threshold; under the select-loop, it should.

2. **Natural completion path is three-way contradictory on who signals `sessions.delete` and who writes `Status = completed`** — The "Natural completion path" user-flow (steps 4–5) says: "Dispatcher sees idle state after no marker injection. Calls `sessions.delete`. Dispatcher updates `JobEntry`: `Status = completed`." The `handleTurnEnd()` table says: "`stopReason == ""` → publish `session_job_complete`. This signals the dispatcher to issue `sessions.delete`." The `runLifecycleSubscriber` Component Changes says: "`session_job_complete` → `Status = completed`." These describe three incompatible models: (A) dispatcher polls session KV for idle state and writes `completed`; (B) QuotaMonitor publishes `session_job_complete`, dispatcher responds to that event by calling `sessions.delete`; (C) lifecycle subscriber writes `completed` on `session_job_complete`. A developer cannot determine whether `sessions.delete` is triggered by KV polling or lifecycle event, nor whether `Status = completed` is written by the dispatcher or the subscriber.
   - **Doc**: § Natural completion path steps 4–5 (dispatcher polls + writes); § How the platform distinguishes, `handleTurnEnd()` table, row `stopReason == ""` ("signals the dispatcher to issue `sessions.delete`"); § Component Changes — Daemon, `runLifecycleSubscriber` (`session_job_complete → Status = completed`).
   - **Code**: No existing mechanism in `processDispatch` or `runJobDispatcher` for detecting natural completion from either KV state or lifecycle events — both would require new code, and the ADR must specify which path to implement.

3. **`mclaude-job-queue` KV key separator stated incorrectly in the Data Model section** — § Data Model, NATS subjects/KV buckets says: "`mclaude-job-queue` KV key: unchanged from ADR-0009 — `{userSlug}/{jobId}`." The separator is a forward slash (`/`). The canonical `spec-state-schema.md` states key format is `{uslug}.{jobId}` (dot-separated), and `subj.JobQueueKVKey` produces `string(u) + "." + jobID` (dot). The code (`readJobEntry`, `writeJobEntry`) uses `subj.JobQueueKVKey` — dot-separated. A developer following the ADR's stated key format would use a slash separator, producing a different key than every existing reader and writer. This is a schema contradiction between the ADR and the canonical state schema.
   - **Doc**: § Data Model, NATS subjects, KV buckets: "`mclaude-job-queue` KV key: unchanged from ADR-0009 — `{userSlug}/{jobId}`."
   - **Code**: `subj.go:217–219`: `JobQueueKVKey` returns `string(u) + "." + jobID`. `spec-state-schema.md`: "Key format: `{uslug}.{jobId}` (dot-separated)".

---

## Audit: 2026-04-24T20:30:00Z

**Document:** docs/adr-0034-generic-scheduler-prompt.md

### Round 6

**Gaps found: 3**

1. QuotaMonitor select-loop still said "inject marker on s.stdinCh" despite Round-5 flow update.
2. Natural completion path: 3-way contradiction on who calls sessions.delete + writes Status=completed.
3. `mclaude-job-queue` KV key separator: ADR said `/`, canonical uses `.`.

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|------------|------|
| 1 | QuotaMonitor select-loop stdin injection contradicted user flow | Updated QuotaMonitor select-loop `quotaCh` case bullet: state-only update (`stopReason`, `outputTokensSinceSoftMark`), no stdin write. Both dispatcher and monitor observe the same quota topic; only the dispatcher writes to the conversation. | factual |
| 2 | Natural-completion three-way contradiction | Rewrote "Natural completion path": QuotaMonitor publishes `session_job_complete` on turn-end with empty stopReason. `runLifecycleSubscriber` is the single writer of `Status = completed` AND the single caller of `sessions.delete` for the completion path. Dispatcher doesn't poll KV or call sessions.delete here. Updated the `handleTurnEnd` truth table and the subscriber component change to match. | factual |
| 3 | KV key separator mismatch | Changed ADR text from `{userSlug}/{jobId}` to `{userSlug}.{jobId}` and cited `subj.JobQueueKVKey` + `spec-state-schema.md` as canonical. | factual |

---

## Run: 2026-04-24T27:00:00Z

**Document:** docs/adr-0034-generic-scheduler-prompt.md
**Round:** 7 (after Round-6 fixes applied)

CLEAN — no blocking gaps found.

All previously reported gaps have been resolved:

1. **`subj` helper migration** — ADR now explicitly names the non-existent 2-arg helpers as a pre-existing compile bug, enumerates every call site in `daemon_jobs.go` (`dispatchQueuedJob`, `startupRecovery`, quota-exceeded path, cancel path), and specifies the canonical 4-arg replacements sourcing `job.HostSlug`.

2. **`state.go` struct rewrites** — `state.go` is now the first named file in Component Changes; `JobEntry` and `QuotaMonitorConfig` rewrites are fully specified with field additions and removals matching `spec-state-schema.md`. `sessionKVKey` and `heartbeatKVKey` wrapper signatures are updated with new host-slug parameters and their call sites enumerated.

3. **`runLifecycleSubscriber` subscription subject** — Fix is explicitly stated (insert `.hosts.*.` segment).

4. **`spec-state-schema.md` consistency** — All lifecycle event payloads (`session_job_paused`, `session_job_complete`, `session_job_cancelled`, `session_job_failed`) match the ADR text. `session_job_cancelled` "Published by" note no longer references the removed `sessions.input` interrupt. `JobEntry` fields match. KV key separator is dot, matching `subj.JobQueueKVKey`.

5. **Dual-writer for `session_job_paused` status** — Subscriber is the single writer; dispatcher does not write `Status = paused` directly.

6. **`Agent.hostSlug` and lifecycle subject** — ADR explicitly adds `hostSlug` to `Agent` struct and `NewAgent`, and specifies migration of all `publishLifecycle*` calls and `session.go` event-subject construction to the 4-arg host-scoped helpers.

7. **`SessionState.ClaudeSessionID` and `start()` resume** — `SessionState` gets the field; `start()` uses `s.state.ClaudeSessionID` when non-empty, falling back for non-scheduled sessions.

8. **`handleDelete` worktree-prune exclusion** — `schedule/` branch-prefix carve-out is specified with exact code guidance.

9. **`JobEntry` slug fields** — `UserSlug`, `ProjectSlug`, `SessionSlug` are retained in the Data Model Go struct.

10. **Soft-stop marker injection** — Dispatcher is sole injector; QuotaMonitor select-loop does state-only update on quota threshold; no `s.stdinCh` write in that case.

11. **Natural completion path** — `runLifecycleSubscriber` is the single writer of `Status = completed` AND the single caller of `sessions.delete` for the completion path; dispatcher does not poll KV.

12. **Turn-end signal** — `turnEndedCh` + `handleTurnEnd()` based on `EventTypeResult` stream-json event; `doneCh` reserved for subprocess-exit detection only.

13. **`handleJobsProjects` prefix** — Uses `userID + "."` as prefix to filter `mclaude-projects` KV. The projects KV key format changed to `{uslug}.{hslug}.{pslug}` per ADR-0004; filtering by `userID` (UUID, not slug) against a slug-keyed bucket will silently return zero results. However, this is a pre-existing bug in the current code unaddressed by ADR-0034, and the ADR does not claim to fix it — it is not introduced or worsened by the ADR's changes. A developer implementing ADR-0034 as written will leave this function unchanged, which is the correct behavior per the ADR's scope. Not a blocking gap for ADR-0034 implementation.

---

## Audit: 2026-04-24T21:00:00Z

**Document:** docs/adr-0034-generic-scheduler-prompt.md

### Round 7

CLEAN — no blocking gaps found.

### Result

**CLEAN** after 7 rounds, 31 total gaps resolved (31 factual, 0 design decisions).

Gap count per round: 9, 5, 4, 4, 6, 3, 0.

All gaps were factual — the ADR exposed several pre-existing bugs in the codebase (subject construction, subscriber subject, `state.go` KV wrappers, `Agent.hostSlug`, `session.go` event subject) that ADR-0034 must close as part of its implementation scope because the new lifecycle flows depend on them. No user design decisions were required during the audit itself; all emerged during the planning Q&A before Round 1.
