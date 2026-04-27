# Spec Evaluator Audit — ADR-0034 Generic Scheduler Prompt
# Component: session-agent (daemon_jobs.go, quota_monitor.go, state.go, agent.go)

## Run: 2026-04-24T00:00:00Z

**ADR Status check:** ADR-0034 has `Status: draft`. Per evaluator rules, only `accepted` or `implemented` ADRs are authoritative. However, the user explicitly requested evaluation against this ADR, so we evaluate as requested — noting the draft status means these are aspirational specs that have not yet been merged into accepted guidance.

**Note on compilation:** Several calls in `daemon_jobs.go` and `agent.go` reference functions that do not exist in the current `mclaude-common/pkg/subj` package (`subj.UserProjectAPISessionsCreate`, `subj.UserProjectAPISessionsInput`, `subj.UserProjectAPISessionsDelete`, `subj.UserProjectLifecycle`, `subj.UserProjectAPITerminal`, and 3-arg `subj.SessionsKVKey`). The code does not compile against the current `subj` package. This is the pre-existing compilation bug that ADR-0034 identifies and requires to be fixed.

---

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0034:62 | "Remove `SpecPath`, `Threshold`, `PRUrl`. Add `Prompt`, `Title`, `BranchSlug`, `ResumePrompt`, `HostSlug`, `SoftThreshold`, `HardHeadroomTokens`, `PermPolicy`, `AllowedTools`, `ClaudeSessionID`, `PausedVia`." | state.go:141–163 | GAP | CODE→FIX | `JobEntry` still has `SpecPath`, `Threshold`, `PRUrl`. Missing: `Prompt`, `Title`, `BranchSlug`, `ResumePrompt`, `HostSlug`, `SoftThreshold`, `HardHeadroomTokens`, `PermPolicy`, `AllowedTools`, `ClaudeSessionID`, `PausedVia`. |
| adr-0034:272 | "`QuotaMonitorConfig`: remove `Threshold int` and `Priority int`; add `SoftThreshold int` and `HardHeadroomTokens int`. Keep `JobID string` and `AutoContinue bool`." | state.go:131–137 | GAP | CODE→FIX | `QuotaMonitorConfig` still has `Threshold int` and `Priority int`. Missing `SoftThreshold` and `HardHeadroomTokens`. |
| adr-0034:273 | "`sessionKVKey(...)`: update to take `hostSlug` and forward to canonical 4-arg `subj.SessionsKVKey(u, h, p, s)`." | state.go:67–71 | GAP | CODE→FIX | `sessionKVKey` still has 3-arg signature and calls 3-arg `subj.SessionsKVKey` which no longer exists. |
| adr-0034:274 | "`heartbeatKVKey(...)`: update to take `hostSlug` and forward to 3-arg `subj.ProjectsKVKey(u, h, p)`." | state.go:73–77 | GAP | CODE→FIX | `heartbeatKVKey` still has 2-arg signature and calls 2-arg `subj.ProjectsKVKey` which no longer exists. |
| adr-0034:280 | "Delete `specPathToComponent`, `specPathToSlug`, `scheduledSessionPrompt`." | daemon_jobs.go:163–222 | GAP | CODE→FIX | All three functions still present: `specPathToComponent` (line 164), `specPathToSlug` (line 183), `scheduledSessionPrompt` (line 191). |
| adr-0034:281–286 | "Modify `dispatchQueuedJob`: Branch name `schedule/{job.BranchSlug}` (no short-ID suffix). Initial user message: `job.Prompt` verbatim. `sessions.create` payload includes `permPolicy`, `allowedTools`, `quotaMonitor: {softThreshold, hardHeadroomTokens, jobId, autoContinue}`. After create, capture `claudeSessionID` from reply and persist to `JobEntry.ClaudeSessionID`. Status → `queued → starting → running`." | daemon_jobs.go:323–454 | GAP | CODE→FIX | `dispatchQueuedJob` still derives branch using `specPathToSlug(job.SpecPath)` with short-ID suffix. Sends old `scheduledSessionPrompt` as prompt. `sessions.create` payload uses `threshold`/`priority` not `softThreshold`/`hardHeadroomTokens`. No `permPolicy` or `allowedTools`. No `claudeSessionID` capture. Calls non-existent `subj.UserProjectAPISessionsCreate` (3-arg) and `subj.UserProjectAPISessionsInput` (3-arg). |
| adr-0034:287–290 | "New `resumePausedJob(job *JobEntry)`: selects nudge from `job.ResumePrompt` and `job.PausedVia`; sends via `sessions.input`; clears `PausedVia`; sets `Status = running`." | daemon_jobs.go (entire file) | GAP | CODE→FIX | No `resumePausedJob` function exists. |
| adr-0034:291 | "New fallback logic in `dispatchQueuedJob` when session not found in `sessKV`: construct `sessions.create` with `resumeClaudeSessionID = job.ClaudeSessionID`." | daemon_jobs.go:457–510 (startupRecovery) | GAP | CODE→FIX | No fallback path; startup recovery just resets `running` jobs to `queued` when session gone. No `--resume` fallback via `resumeClaudeSessionID`. |
| adr-0034:292–293 | "Replace every call site in `daemon_jobs.go` with host-scoped forms: `UserHostProjectAPISessionsCreate(u,h,p)`, `UserHostProjectAPISessionsInput(u,h,p)`, `UserHostProjectAPISessionsDelete(u,h,p)`, `UserHostProjectLifecycle(u,h,p,s)`, `SessionsKVKey(u,h,p,s)` taking 4 args." | daemon_jobs.go:342,377,438,491,635,647,848 | GAP | CODE→FIX | All call sites still use non-existent 3-arg forms: `subj.UserProjectAPISessionsCreate`, `subj.UserProjectAPISessionsInput`, `subj.UserProjectAPISessionsDelete`, `subj.UserProjectLifecycle`, and 3-arg `subj.SessionsKVKey`. Code does not compile. |
| adr-0034:293 | "Fix `runLifecycleSubscriber` subscription subject: change to `'mclaude.users.' + string(d.cfg.UserSlug) + '.hosts.*.projects.*.lifecycle.*'`." | daemon_jobs.go:254 | GAP | CODE→FIX | Subscription subject is still `"mclaude.users." + string(d.cfg.UserSlug) + ".projects.*.lifecycle.*"` — missing `.hosts.*.` segment. Receives zero lifecycle events. |
| adr-0034:294–296 | "Modify quota-exceeded path: rename `job.Threshold` → `job.SoftThreshold`. Injected message content: `'MCLAUDE_STOP: quota_soft'` (exact). Dispatcher is sole marker injector." | daemon_jobs.go:612–660 | GAP | CODE→FIX | Threshold field still named `job.Threshold`. Message content is still `"QUOTA_THRESHOLD_REACHED: ..."` not `"MCLAUDE_STOP: quota_soft"`. Calls non-existent `subj.UserProjectAPISessionsInput` and `subj.UserProjectLifecycle`. |
| adr-0034:297–301 | "Modify `DELETE /jobs/{id}`: single behavior: dispatcher publishes `sessions.delete`, then publishes `session_job_cancelled` lifecycle event. Does NOT write `Status = cancelled` directly; waits for `runLifecycleSubscriber`." | daemon_jobs.go:840–858 | GAP | CODE→FIX | Cancel handler writes `job.Status = "cancelled"` directly instead of publishing `session_job_cancelled` lifecycle event. Does not publish `session_job_cancelled`. Does not publish `sessions.delete` for the paused-jobs case (only publishes delete when `job.Status == "running"`). Calls non-existent `subj.UserProjectAPISessionsDelete`. |
| adr-0034:302–307 | "Lifecycle subscriber event→status map: `session_job_paused` → `Status=paused, PausedVia=ev['pausedVia']`, AutoContinue reads `r5`. `session_job_complete` → `Status=completed` AND publish `sessions.delete`. `session_job_cancelled` → `Status=cancelled`. `session_job_failed` → `Status=failed`. `session_permission_denied` → `needs_spec_fix`." | daemon_jobs.go:270–309 | GAP | CODE→FIX | Lifecycle subscriber handles `session_quota_interrupted` (old name) instead of `session_job_paused`. Does not handle `session_job_cancelled`. `session_job_paused` case is a no-op with a log. `session_job_complete` does not publish `sessions.delete`. |
| adr-0034:309–316 | "POST /jobs request body: remove `SpecPath`, `Threshold`; add required `Prompt`, `BranchSlug`, `HostSlug`, `PermPolicy`, `AllowedTools`, `SoftThreshold`, `HardHeadroomTokens`. Validation: 400 on missing required fields; slug regex check." | daemon_jobs.go:743–778 | GAP | CODE→FIX | POST body struct still has `SpecPath`, `Threshold`; missing all new fields. No slug regex validation. `JobEntry` construction populates old fields. |
| adr-0034:320 | "`Session.onRawOutput` callback: remove `SESSION_JOB_COMPLETE:` marker scanning. Keep the callback for token counting." | quota_monitor.go:94–113 | GAP | CODE→FIX | `onRawOutput` still scans for `SESSION_JOB_COMPLETE:` marker (lines 98–113). Token counting not implemented. |
| adr-0034:321–326 | "New field on `sessions.create` request: `ResumeClaudeSessionID string`. When set, spawns `claude --resume <ResumeClaudeSessionID>`. `sessions.create` reply adds `claudeSessionID` alongside existing `id`." | agent.go (handleCreate area ~590–800) | GAP | CODE→FIX | No `ResumeClaudeSessionID` field in create request. No `claudeSessionID` in reply. Reply only sends `id`. |
| adr-0034:327–349 | "QuotaMonitor: `SoftThreshold`/`HardHeadroomTokens` replace `Threshold`. `stopReason` state var (empty/'quota_soft'/'quota_hard'). `outputTokensSinceSoftMark`. `turnEndedCh`. `handleTurnEnd()`. Monitor observes `result` event for turn-end; does NOT write to stdinCh for quota marker (dispatcher-only). Hard-stop: fires `control_request` interrupt when `outputTokensSinceSoftMark >= HardHeadroomTokens`. On `doneCh` close: `handleSubprocessExit()`." | quota_monitor.go:1–228 | GAP | CODE→FIX | QuotaMonitor uses old model: single `Threshold`, 30-min timer for hard interrupt, `sendGracefulStop()` sends `QUOTA_THRESHOLD_REACHED` to stdinCh, `publishExitLifecycle` on `doneCh` close scans `completionPR`. None of the new ADR-0034 state variables (`stopReason`, `outputTokensSinceSoftMark`, `turnEndedCh`) exist. |
| adr-0034:350–353 | "`Agent` struct: add `hostSlug slug.HostSlug` field. All `publishLifecycle*` and event-subject construction must use 4-arg host-scoped forms." | agent.go:33–77 | GAP | CODE→FIX | No `hostSlug` field on `Agent`. All `publishLifecycle*` methods (lines 1136, 1149, 1163, 1179) call non-existent `subj.UserProjectLifecycle`. Session event publish calls non-existent `subj.UserProjectAPISessionsCreate`, `subj.UserProjectAPITerminal`. |
| adr-0034:354 | "`SessionState` struct: add `ClaudeSessionID string json:'claudeSessionID'`." | state.go:28–49 | GAP | CODE→FIX | `SessionState` has no `ClaudeSessionID` field. |
| adr-0034:355 | "`session.start()` resume: when `resume=true`, use `--resume s.state.ClaudeSessionID` when non-empty; fall back to previous behavior." | session.go (start function) | GAP | CODE→FIX | Needs verification — see below. |
| adr-0034:356 | "`handleDelete` worktree-prune exclusion: if branch name matches `schedule/` prefix, skip worktree removal." | agent.go:845–868 | GAP | CODE→FIX | `handleDelete` removes worktree unconditionally when last user; no `schedule/` prefix check. |
| spec-state-schema.md:245 | "Field origins: `specPath`, `threshold`, and `prUrl` from ADR-0009 are removed (see ADR-0034). `prompt`, `title`, `branchSlug`, `resumePrompt`, `softThreshold`, `hardHeadroomTokens`, `permPolicy`, `allowedTools`, `claudeSessionID`, `pausedVia` are introduced by ADR-0034." | state.go:141–163 | GAP | CODE→FIX | Spec-state-schema declares these as the canonical `JobEntry` fields, but `state.go` still has the old fields. |
| spec-state-schema.md:246 | "Key format: `{uslug}.{jobId}`" | daemon_jobs.go:244 | IMPLEMENTED | — | `subj.JobQueueKVKey(slug.UserSlug(job.UserID), job.ID)` matches. |
| spec-state-schema.md:251 | "Dispatcher uses slug fields (`userSlug`, `hostSlug`, `projectSlug`, `sessionSlug`) to construct KV keys." | daemon_jobs.go:377, 491 | GAP | CODE→FIX | Dispatcher uses UUID-based slug construction (`slug.ProjectSlug(job.ProjectID)`) rather than the slug fields. No `hostSlug` used. |
| spec-state-schema.md:558–579 | "`session_job_paused` event payload: `pausedVia: 'quota_soft' \| 'quota_hard'`, `u5`, `r5`, `outputTokensSinceSoftMark` (hard only)." | daemon_jobs.go:648–655, quota_monitor.go:144–168 | GAP | CODE→FIX | Old code publishes `session_quota_interrupted` (daemon) and has no `pausedVia`, no two-tier format. QuotaMonitor also uses old `session_quota_interrupted`. |
| spec-state-schema.md:577–579 | "`session_job_cancelled` payload: `type, sessionId, jobId, ts`." | daemon_jobs.go (handleJobByID DELETE) | GAP | CODE→FIX | `session_job_cancelled` lifecycle event never published; cancel writes status directly. |
| spec-session-agent.md:240–244 | "QuotaMonitor: sends `QUOTA_THRESHOLD_REACHED` message to Claude's stdin, starts 30-min hard-interrupt timer, scans for `SESSION_JOB_COMPLETE:{prUrl}` marker." | quota_monitor.go:118–169 | IMPLEMENTED | — | This matches the OLD behavior described in spec-session-agent.md §Quota Monitoring. The spec-session-agent has NOT been updated to reflect ADR-0034 changes yet. The code matches spec-session-agent's current text. |
| spec-session-agent.md:267–273 | "Dispatch: sends `sessions.create` with `strict-allowlist` policy and quota monitor config, waits for session to reach `idle` (30s poll), then sends the dev-harness prompt." | daemon_jobs.go:323–454 | PARTIAL | CODE→FIX | Sends `sessions.create` and waits for idle. But sends old `scheduledSessionPrompt` (not job.Prompt). `sessions.create` lacks `permPolicy`/`allowedTools`. Uses non-existent subject functions. |
| spec-session-agent.md:272 | "Quota preemption: when any running job's threshold is exceeded, sends graceful stop messages to exceeded jobs in ascending priority order." | daemon_jobs.go:612–660 | PARTIAL | CODE→FIX | Priority ordering implemented. But stop message is old `QUOTA_THRESHOLD_REACHED` not `MCLAUDE_STOP: quota_soft`. Subject calls non-existent functions. |
| spec-session-agent.md:273 | "Job status transitions: lifecycle subscriber updates job status on `session_job_complete`, `session_quota_interrupted`, `session_permission_denied`, `session_job_failed` events." | daemon_jobs.go:270–309 | PARTIAL | SPEC→FIX | Handles all listed events. But spec-session-agent.md's list is from ADR-0009; ADR-0034 replaces `session_quota_interrupted` with `session_job_paused` and adds `session_job_cancelled`. Spec-session-agent.md needs updating to reflect ADR-0034. |

---

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| daemon_jobs.go:163–179 | UNSPEC'd (should be DEAD per ADR-0034) | `specPathToComponent` — ADR-0034 requires deletion; it still exists and is called. |
| daemon_jobs.go:181–188 | UNSPEC'd (should be DEAD per ADR-0034) | `specPathToSlug` — ADR-0034 requires deletion; still exists and is called. |
| daemon_jobs.go:190–222 | UNSPEC'd (should be DEAD per ADR-0034) | `scheduledSessionPrompt` — ADR-0034 requires deletion; still exists and is called. |
| daemon_jobs.go:254 (subscription subject) | DEAD | Wrong subject `"mclaude.users.{uslug}.projects.*.lifecycle.*"` — missing `.hosts.*.` causes zero events received. Effectively dead for MCLAUDE_LIFECYCLE routing. |
| daemon_jobs.go:272–277 | UNSPEC'd (stale) | `session_job_complete` case reads `ev["prUrl"]` and stores to `job.PRUrl` — field removed from `JobEntry` per ADR-0034. |
| daemon_jobs.go:278–291 | UNSPEC'd (stale) | `session_quota_interrupted` case — event type replaced by `session_job_paused` per ADR-0034. This case never fires (lifecycle subscriber receives zero events due to missing hosts.* segment). |
| daemon_jobs.go:298–303 | UNSPEC'd (placeholder) | `session_job_paused` case — no-op log only. ADR-0034 requires this to write `Status=paused`, `PausedVia`, and optionally `ResumeAt`. |
| daemon_jobs.go:415–434 | UNSPEC'd | Collects `otherSpecs` from other running jobs for context in `scheduledSessionPrompt` — entire block becomes dead when `scheduledSessionPrompt` is deleted. |
| quota_monitor.go:31 | DEAD | `completionPR string` — used only for `SESSION_JOB_COMPLETE:` marker scanning, which ADR-0034 requires to be deleted. |
| quota_monitor.go:92–113 | DEAD (per ADR-0034) | `onRawOutput` scans for `SESSION_JOB_COMPLETE:` marker. ADR-0034 requires removal of this scanning; the callback stays for token counting only. |
| quota_monitor.go:118–130 | UNSPEC'd (stale) | `sendGracefulStop` — sends `QUOTA_THRESHOLD_REACHED` message to session stdin. ADR-0034 replaces with dispatcher-injected `MCLAUDE_STOP: quota_soft`; monitor no longer writes to stdinCh for quota threshold. |
| quota_monitor.go:175–214 | UNSPEC'd (stale) | `run()` select loop — monitors quota and sends `sendGracefulStop` + 30-min timer for `sendHardInterrupt`. ADR-0034 replaces with: dispatcher injects marker, monitor tracks `stopReason`, fires `handleTurnEnd()` on `result` event, hard budget is token count not timer. |
| quota_monitor.go:144–168 | UNSPEC'd (stale) | `publishExitLifecycle` — publishes `session_job_complete` (only on `SESSION_JOB_COMPLETE:` marker), `session_quota_interrupted` (old event name), `session_job_failed`. ADR-0034 replaces all three with `handleTurnEnd()` logic. |
| state.go:149–163 | UNSPEC'd (stale) | `JobEntry` fields `SpecPath`, `Threshold`, `PRUrl` — removed by ADR-0034 / spec-state-schema.md. All spec-current fields missing. |
| state.go:132–137 | UNSPEC'd (stale) | `QuotaMonitorConfig.Threshold` and `.Priority` — removed by ADR-0034. New fields `SoftThreshold` / `HardHeadroomTokens` missing. |

---

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| adr-0034 §Tests | "`dispatchQueuedJob` reads `job.Prompt` verbatim and forwards it as `sessions.input` content." | None found | None found | UNTESTED |
| adr-0034 §Tests | "`dispatchQueuedJob` uses `job.BranchSlug` for branch; no short-ID suffix." | None found | None found | UNTESTED |
| adr-0034 §Tests | "`dispatchQueuedJob` captures `ClaudeSessionID` from reply and persists to KV." | None found | None found | UNTESTED |
| adr-0034 §Tests | "`resumePausedJob` sends platform default nudge (soft vs hard variants)." | None found | None found | UNTESTED |
| adr-0034 §Tests | "`HandleJobsRoute` POST 400s on missing required fields." | None found | None found | UNTESTED |
| adr-0034 §Tests | "`QuotaMonitor` counts tokens only after soft-mark; fires hard interrupt at `HardHeadroomTokens`." | None found | None found | UNTESTED |
| adr-0034 §Tests | "`handleTurnEnd()` publishes correct event based on `stopReason`." | None found | None found | UNTESTED |
| adr-0034 §Tests | "Hard-stop path leaves subprocess alive; `session_job_paused` published with `pausedVia: 'quota_hard'` after `result` event." | None found | None found | UNTESTED |
| adr-0034 §Tests | "`handleSubprocessExit()` after terminal event → no-op; without prior event → publishes `session_job_failed`." | None found | None found | UNTESTED |
| adr-0034 §Tests | "Cancel path: `DELETE /jobs/{id}` sends `sessions.delete`; publishes `session_job_cancelled`; no marker injection." | None found | None found | UNTESTED |
| adr-0034 §Tests | "Fallback `--resume` path: dispatcher finds missing session, creates with `ResumeClaudeSessionID`." | None found | None found | UNTESTED |
| adr-0034 §Tests | "Delete stale tests for `scheduledSessionPrompt`, `specPathToComponent`, `specPathToSlug`, `SESSION_JOB_COMPLETE:` detection." | Old tests may still exist | N/A | UNTESTED |

---

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (no bugs in .agent/bugs/ matching this component) | — | — | No bug files found for session-agent related to ADR-0034. |

---

### Summary

- Implemented: 2 (JobQueueKVKey format, spec-session-agent old QuotaMonitor behavior — but that spec section itself is stale)
- Gap: 22
- Partial: 2
- Infra: 0
- Unspec'd: 10
- Dead: 3
- Tested: 0
- Unit only: 0
- E2E only: 0
- Untested: 12
- Bugs fixed: 0
- Bugs open: 0
