## Run: 2026-05-01T04:30:00Z

**ADR:** `docs/adr-0044-scheduling-and-quota.md` (status: draft)
**Specs checked:**
- `docs/spec-state-schema.md`
- `docs/spec-nats-payload-schema.md`
- `docs/spec-nats-activity.md`
- `docs/mclaude-session-agent/spec-session-agent.md`

**Round:** 4 (verify Round 3 gap fixes, final comprehensive check)

### Round 3 Gap Verification

All 4 Round 3 gaps (quota publisher designation mechanism missing from 3 specs) verified fixed:

| R3 # | R3 Finding | Status | Evidence |
|------|-----------|--------|----------|
| 5 | `spec-nats-payload-schema.md` — `api.agents.register` response missing `quotaPublisher` field | ✅ FIXED | Response now: `{"ok": true, "quotaPublisher": false}`. Field table includes: "`quotaPublisher` — `true` if this agent is designated as the quota publisher for the user (ADR-0044)." |
| 7 | `spec-nats-payload-schema.md` — missing `manage.designate-quota-publisher` subject | ✅ FIXED | Full section added under "Agent Public Key Registration" with payload `{"id":"...","ts":...,"quotaPublisher": true}`, field table, and description of re-designation behavior. |
| 8 | `spec-state-schema.md` — `manage.designate-quota-publisher` missing from NATS subjects tables | ✅ FIXED | Added to "Host management subjects" table: publisher=CP, subscriber=session-agent, payload=`{quotaPublisher: bool}`, description references ADR-0044 re-designation. |
| 9 | `spec-session-agent.md` — NATS Subjects (Subscribe) missing `manage.designate-quota-publisher` | ✅ FIXED | Subscribe bullet added: "**Quota publisher re-designation** via core NATS on `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.manage.designate-quota-publisher`…". Quota Publisher Designation section now documents full re-designation lifecycle including `quotaPublisher: false` for de-designation. |

### Phase 1 — ADR → Spec Forward Pass (comprehensive re-check)

| # | ADR (section) | ADR text | Spec location | Verdict | Direction | Notes |
|---|---------------|----------|---------------|---------|-----------|-------|
| 1 | Decisions: No separate job queue | "Eliminates `mclaude-job-queue-{uslug}` KV bucket, job dispatcher goroutine, `localhost:8378` HTTP API" | spec-state-schema.md § "Job queue KV eliminated (ADR-0044)"; spec-session-agent.md § HTTP (Removed), § Daemon: Job Dispatcher (Removed); spec-nats-payload-schema.md sessions.create (no dispatcher refs) | REFLECTED | — | |
| 2 | Decisions: Prompt shape | "Caller supplies a free-text `prompt` field on `sessions.create`; agent sends it verbatim" | spec-nats-payload-schema.md § sessions.create; spec-session-agent.md § QuotaMonitor Message Format | REFLECTED | — | |
| 3 | Decisions: Quota data source | "`api.anthropic.com/api/oauth/usage` via OAuth token from `~/.claude/.credentials.json`" | spec-state-schema.md § Quota Status; spec-nats-payload-schema.md § Quota Subject; spec-session-agent.md § Dependencies | REFLECTED | — | |
| 4 | Decisions: Quota publisher — initial designation | "CP assigns one agent per user on registration… `quotaPublisher: true` in register response" | spec-nats-payload-schema.md § agents.register response (`quotaPublisher` field); spec-nats-activity.md § 7b; spec-session-agent.md § Quota Publisher Designation | REFLECTED | — | |
| 5 | Decisions: Quota publisher — re-designation | "CP re-designates another… via `manage.designate-quota-publisher`" | spec-nats-payload-schema.md § designate-quota-publisher; spec-state-schema.md NATS subjects; spec-nats-activity.md §§ 8f, 9i; spec-session-agent.md § Subscribe + § Quota Publisher Designation | REFLECTED | — | |
| 6 | Decisions: Soft threshold | "`softThreshold` (% 5h utilization). On breach, injects `MCLAUDE_STOP: quota_soft`" | spec-session-agent.md § QuotaMonitor; spec-nats-payload-schema.md § sessions.create | REFLECTED | — | |
| 7 | Decisions: Hard budget | "`hardHeadroomTokens` per session. 0 = immediate hard stop" | spec-session-agent.md § Token Counting; spec-nats-payload-schema.md § sessions.create | REFLECTED | — | |
| 8 | Decisions: Paused sessions stay alive | "Subprocess idles on stdin with in-memory conversation intact" | spec-session-agent.md § handleTurnEnd table; spec-nats-activity.md § 9g | REFLECTED | — | |
| 9 | Decisions: Resume from pause | "Agent sends a new user message via `sessions.input`. Uses `resumePrompt` if provided" | spec-session-agent.md § Message Format; spec-nats-activity.md §§ 9g, 9k | REFLECTED | — | |
| 10 | Decisions: Default resume nudges | "Soft: `Resuming — continue where you left off.` Hard: `Your previous turn was interrupted mid-response…`" | spec-session-agent.md § Message Format; spec-nats-activity.md § 9k | REFLECTED | — | |
| 11 | Decisions: Completion signalling | "No stdout-marker scanning. Session record persists." | spec-session-agent.md § Turn-End Detection; spec-nats-activity.md § 9g Completion | REFLECTED | — | |
| 12 | Decisions: Stop hook mechanism | "Claude Code's native `hooks.Stop`. Not a platform primitive." | spec-session-agent.md § Turn-End Detection (refers to Stop hook); ADR's Stop Hook Authoring Guide is caller-side, not spec material | REFLECTED | — | |
| 13 | Decisions: Turn-end detection | "via stream-json `result` event (`EventTypeResult`)" | spec-session-agent.md § Turn-End Detection ("turn_complete canonical event") | REFLECTED | — | Spec uses canonical event abstraction. |
| 14 | Decisions: Parallelism model | "All sessions start immediately. Each independently monitors `u5` against its own `softThreshold`" | spec-session-agent.md § QuotaMonitor; spec-nats-activity.md § 9g (sched-001 vs sched-002 example) | REFLECTED | — | |
| 15 | Decisions: Permission policy — strict-allowlist | "auto-approve allowlisted tools, auto-deny everything else" | spec-session-agent.md § Permission Policy; spec-nats-payload-schema.md § sessions.create | REFLECTED | — | |
| 16 | Decisions: allowedTools validation | "empty `allowedTools` with `strict-allowlist` is rejected" | spec-session-agent.md § Permission Policy; spec-nats-activity.md § 9l | REFLECTED | — | |
| 17 | Decisions: defaultDevHarnessAllowlist eliminated | "spec-session-agent.md must be updated to remove the 'default set is applied' paragraph" | spec-session-agent.md § Permission Policy ("The previous implicit `defaultDevHarnessAllowlist` is eliminated.") | REFLECTED | — | |
| 18 | Decisions: Worktree per session | "Branch `schedule/{branchSlug}`" | spec-session-agent.md § Deletion (schedule/ branch check); spec-nats-activity.md § 9g | REFLECTED | — | |
| 19 | Decisions: Worktree cleanup | "Platform does not prune worktrees" | spec-session-agent.md § Deletion ("skip worktree removal") | REFLECTED | — | |
| 20 | Decisions: AutoContinue | "`autoContinue` flag per session" | spec-nats-payload-schema.md; spec-state-schema.md; spec-session-agent.md § QuotaMonitor | REFLECTED | — | |
| 21 | Decisions: Cancel | "`sessions.delete`. Emits `session_job_cancelled`." | spec-session-agent.md § Deletion; spec-nats-activity.md § 9g Cancellation | REFLECTED | — | |
| 22 | Decisions: Degraded fallback — session loss | "persists `claudeSessionID`… `claude --resume`" | spec-session-agent.md § Resumption; spec-nats-activity.md § 9n | REFLECTED | — | |
| 23 | Decisions: Permission-denied signaling | "In-process Go channel `onStrictDeny` → `permDeniedCh`" | spec-session-agent.md § Session Callbacks | REFLECTED | — | |
| 24 | Component: Extended sessions.create payload | 9 new top-level fields | spec-nats-payload-schema.md § sessions.create (all 9 fields, field table); spec-state-schema.md § sessions.create subject row | REFLECTED | — | |
| 25 | Component: QuotaMonitor goroutine | "5 select-loop cases — stopCh, permDeniedCh, quotaCh, turnEndedCh (priority), doneCh" | spec-session-agent.md § QuotaMonitor Goroutine (all 5 cases, nested select priority) | REFLECTED | — | |
| 26 | Component: Token counting — two strategies | "byte estimate `len(raw)/4`, authoritative `usage.output_tokens`" | spec-session-agent.md § Token Counting (both strategies documented) | REFLECTED | — | |
| 27 | Component: handleTurnEnd | "3-row table: quota_soft → paused, quota_hard → paused, empty → completed" | spec-session-agent.md § Turn-End Detection (matching table) | REFLECTED | — | |
| 28 | Component: handleSubprocessExit | "`terminalEventPublished` → no-op; otherwise → `session_job_failed`" | spec-session-agent.md § Subprocess Exit | REFLECTED | — | |
| 29 | Component: Agent restart recovery (KV-based) | "pending → respawn; paused → check alive or --resume; running → --resume" | spec-session-agent.md § Resumption; spec-nats-activity.md § 9n | REFLECTED | — | |
| 30 | Component: handleDelete branching | "`SoftThreshold > 0` distinguishes interactive vs quota-managed" | spec-session-agent.md § Deletion (two branches documented) | REFLECTED | — | |
| 31 | Component: Session KV extensions | 8 new fields: softThreshold, hardHeadroomTokens, autoContinue, pausedVia, claudeSessionID, branchSlug, failedTool, resumeAt | spec-state-schema.md § SessionState (all 8 present); spec-nats-payload-schema.md § KV Payloads (all 8) | REFLECTED | — | |
| 32 | Data Model: QuotaStatus | "U5, U7, R5, R7, HasData, TS" | spec-state-schema.md § Quota Status; spec-nats-payload-schema.md § Quota Subject | REFLECTED | — | |
| 33 | Data Model: 5 lifecycle events | "session_job_complete, session_job_paused, session_job_cancelled, session_permission_denied, session_job_failed" | spec-state-schema.md § Lifecycle Event Payloads (all 5); spec-nats-payload-schema.md § Job Lifecycle Events (all 5) | REFLECTED | — | |
| 34 | Component: Session callbacks | "`onStrictDeny` and `onRawOutput`" | spec-session-agent.md § Session Callbacks (both documented) | REFLECTED | — | |
| 35 | Component: QuotaMonitor message format | "Three types: prompt, soft-stop marker, resume nudge — all via sessions.input" | spec-session-agent.md § Message Format (all 3); spec-nats-activity.md § 9g | REFLECTED | — | |
| 36 | Component: Hard-stop NOT published to NATS | "`control_request` interrupts are NOT published to NATS" | spec-session-agent.md § Message Format ("NOT published to NATS") | REFLECTED | — | |
| 37 | Component: hasData==false behavior | "do NOT trigger stop or start" | spec-session-agent.md § QuotaMonitor; spec-nats-activity.md § 9j | REFLECTED | — | |
| 38 | Component: Status enum (10 values) | "pending, running, paused, requires_action, completed, stopped, cancelled, needs_spec_fix, failed, error" | spec-state-schema.md § SessionState; spec-nats-payload-schema.md § KV Payloads | REFLECTED | — | |
| 39 | User Flow: Cancel — tombstone KV | "Agent tombstones the session KV entry (KV delete)" | spec-session-agent.md § Deletion; spec-nats-activity.md § 9g Cancellation | REFLECTED | — | |
| 40 | Security: branchSlug regex | "`[a-z0-9-]+` prevents path-traversal" | spec-state-schema.md § SessionState (`^[a-z0-9][a-z0-9-]*$`) | REFLECTED | — | |
| 41 | Data Model: session_job_complete | "No `jobId`. No `prUrl`." | spec-state-schema.md § session_job_complete (no jobId/prUrl fields) | REFLECTED | — | |
| 42 | Decisions: `/schedule-feature` skill location | "Lives in external plugin. Deleted from mclaude." | Not a spec concern (external plugin; ADR scope section confirms out of scope) | REFLECTED | — | No spec needed — explicitly deferred. |
| 43 | Decisions: Time-based scheduling deferred | "`--at` deferred" | N/A (out of scope) | REFLECTED | — | Deferred items don't need spec entries. |
| 44 | Component: Quota publisher — 60s poll interval | "Polls every 60 seconds" | spec-session-agent.md § Quota Publisher Designation ("polls…every 60 seconds"); spec-state-schema.md § Quota Status ("every 60s") | REFLECTED | — | |

### Summary

- Reflected: 44
- Gap: 0
- Partial: 0

**CLEAN — 44 ADR decisions reflected in specs, 0 gaps.**

All 4 Round 3 gaps have been verified as fixed. No new gaps discovered in Round 4 comprehensive re-check.
