## Run: 2026-05-01T04:10:00Z

**ADR:** `docs/adr-0044-scheduling-and-quota.md` (status: draft)
**Specs checked:**
- `docs/spec-state-schema.md`
- `docs/spec-nats-payload-schema.md`
- `docs/spec-nats-activity.md`
- `docs/mclaude-session-agent/spec-session-agent.md`

**Round:** 3 (verify Round 2 fixes + comprehensive re-check)

### Round 2 Fix Verification

All 5 Round 2 gaps (stale dispatcher/job references in `spec-nats-payload-schema.md`) are verified fixed:

| R2 # | R2 Finding | Status | Evidence |
|------|-----------|--------|----------|
| 45 | `sessions.create` publisher line referenced "or job dispatcher" | ✅ FIXED | Now reads: "**Publisher:** SPA/CLI \| **Subscriber:** Agent (via stream consumer)" |
| 46 | `sessions.create` Reply section referenced dispatcher | ✅ FIXED | Now reads: "The agent persists `claudeSessionID` to the session KV entry for degraded `--resume` fallback." |
| 47 | `session_job_paused` r5 description referenced "dispatcher" | ✅ FIXED | Now reads: "QuotaMonitor uses this for `resumeAt` when `autoContinue` is set" |
| 48 | `session_job_cancelled` referenced `DELETE /jobs/{id}` and Dispatcher | ✅ FIXED | Now reads: "User published `sessions.delete`. Agent interrupted the subprocess." |
| 49 | `session_permission_denied` referenced "Job transitions" | ✅ FIXED | Now reads: "Session transitions to `needs_spec_fix`." |

### Phase 1 — ADR → Spec Forward Pass (comprehensive)

| # | ADR (section) | ADR text | Spec location | Verdict | Direction | Notes |
|---|---------------|----------|---------------|---------|-----------|-------|
| 1 | Decisions: No separate job queue | "Eliminates `mclaude-job-queue-{uslug}` KV bucket, job dispatcher goroutine, `localhost:8378` HTTP API" | spec-state-schema.md § NATS KV Buckets ("Job queue KV eliminated (ADR-0044)"); spec-session-agent.md § HTTP (Removed), § Daemon: Job Dispatcher (Removed); spec-nats-payload-schema.md sessions.create (no dispatcher refs) | REFLECTED | — | All stale references eliminated in Round 2 fixes. |
| 2 | Decisions: Prompt shape | "Caller supplies a free-text `prompt` field on `sessions.create`; agent sends it verbatim as the initial `sessions.input` after CLI startup." | spec-nats-payload-schema.md § sessions.create (`prompt` field); spec-session-agent.md § QuotaMonitor Message Format | REFLECTED | — | |
| 3 | Decisions: Quota data source | "`api.anthropic.com/api/oauth/usage` via OAuth token from `~/.claude/.credentials.json`" | spec-state-schema.md § Quota Status; spec-nats-payload-schema.md § Quota Subject; spec-session-agent.md § Dependencies | REFLECTED | — | |
| 4 | Decisions: Quota publisher — initial designation | "CP assigns one agent per user on registration… delivered via a `quotaPublisher: true` field in the CP's response to `mclaude.hosts.{hslug}.api.agents.register`" | spec-nats-activity.md § 7b (shows `{"ok":true,"quotaPublisher":true}`); spec-session-agent.md § Quota Publisher Designation ("delivered via a `quotaPublisher: true` field in CP's response") | REFLECTED | — | Activity spec and session-agent spec both cover initial designation. |
| 5 | Decisions: Quota publisher — `quotaPublisher` in register response | "delivered via a `quotaPublisher: true` field in the CP's response to `mclaude.hosts.{hslug}.api.agents.register`" | spec-nats-payload-schema.md § Agent Public Key Registration — Response: `{"ok": true}` | GAP | SPEC→FIX | Payload schema's definitive response schema for `api.agents.register` omits the `quotaPublisher` boolean. The payload schema states it defines "the exact JSON payload for every NATS subject." A developer implementing this response using only the payload schema would not include `quotaPublisher`. Should add `quotaPublisher` to the response schema and field table. |
| 6 | Decisions: Quota publisher — re-designation | "If the designated agent goes offline (heartbeat timeout), CP re-designates another… notifies it via `manage.designate-quota-publisher`." | spec-nats-activity.md §§ 8f, 9i (full re-designation flow documented) | REFLECTED | — | Activity spec covers the full re-designation mechanism. |
| 7 | Decisions: Quota publisher — re-designation subject | "notifies it via `manage.designate-quota-publisher`" | spec-nats-payload-schema.md: NOT DOCUMENTED | GAP | SPEC→FIX | Payload schema does not document the `manage.designate-quota-publisher` subject. The payload schema claims to cover every NATS subject. Activity spec § 9i shows empty payload `{}`. Payload schema should add this subject with its (empty) payload. |
| 8 | Decisions: Quota publisher — re-designation subject | "notifies it via `manage.designate-quota-publisher`" | spec-state-schema.md NATS subjects tables: NOT LISTED | GAP | SPEC→FIX | State schema's NATS subjects tables do not list `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.manage.designate-quota-publisher`. Should be added to the "Session subjects" table (publisher: CP, subscriber: agent, payload: empty `{}`). |
| 9 | Component Changes: Quota publisher designation — agent subscription | "Non-designated agents only subscribe to the quota subject — they never poll the Anthropic API." + re-designation via `manage.designate-quota-publisher` | spec-session-agent.md § NATS Subjects (Subscribe): does not list `manage.designate-quota-publisher` | GAP | SPEC→FIX | Session-agent spec's NATS Subjects (Subscribe) section omits the `manage.designate-quota-publisher` core NATS subscription. Activity spec § 8f shows the agent subscribes to this. A developer implementing the agent's subscriptions from the session-agent spec alone would miss this. Should add a bullet: "**Quota publisher re-designation** via core NATS on `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.manage.designate-quota-publisher`." |
| 10 | Decisions: Soft threshold | "`softThreshold` (% 5h utilization) per session. On breach, agent injects `MCLAUDE_STOP: quota_soft`" | spec-session-agent.md § QuotaMonitor Goroutine; spec-nats-payload-schema.md § sessions.create | REFLECTED | — | |
| 11 | Decisions: Hard budget | "`hardHeadroomTokens` per session… 0 = immediate hard stop" | spec-session-agent.md § Token Counting; spec-nats-payload-schema.md § sessions.create | REFLECTED | — | |
| 12 | Decisions: Paused sessions stay alive | "Subprocess idles on stdin with in-memory conversation intact." | spec-session-agent.md § handleTurnEnd; spec-nats-activity.md § 9g | REFLECTED | — | |
| 13 | Decisions: Resume | "Agent sends a new user message via `sessions.input`. Uses caller-supplied `resumePrompt` if provided; otherwise platform defaults." | spec-session-agent.md § Message Format; spec-nats-activity.md §§ 9g, 9k | REFLECTED | — | |
| 14 | Decisions: Default resume nudges | "Soft-paused: `Resuming — continue where you left off.` Hard-paused: `Your previous turn was interrupted mid-response…`" | spec-session-agent.md § Message Format; spec-nats-activity.md § 9k | REFLECTED | — | |
| 15 | Decisions: Completion signalling | "No stdout-marker scanning. Session record persists." | spec-session-agent.md § handleTurnEnd; spec-nats-activity.md § 9g Completion | REFLECTED | — | |
| 16 | Decisions: Turn-end detection | "QuotaMonitor detects turn-end via stream-json `result` event" | spec-session-agent.md § Turn-End Detection ("turn_complete canonical event") | REFLECTED | — | Spec correctly abstracts to canonical event layer. |
| 17 | Decisions: Parallelism model | "All sessions start immediately. Each independently monitors `u5` against its own `softThreshold`." | spec-session-agent.md § QuotaMonitor; spec-nats-activity.md § 9g | REFLECTED | — | |
| 18 | Decisions: Permission policy | "`strict-allowlist` mode: auto-approve allowlisted tools, auto-deny everything else." | spec-session-agent.md § Permission Policy; spec-nats-payload-schema.md § sessions.create | REFLECTED | — | |
| 19 | Decisions: allowedTools validation | "If `allowedTools` is empty on a `strict-allowlist` session, the agent rejects" | spec-session-agent.md § Permission Policy; spec-nats-activity.md § 9l | REFLECTED | — | |
| 20 | Decisions: Worktree per session | "Branch `schedule/{branchSlug}`" | spec-session-agent.md § Deletion (quota-managed); spec-nats-activity.md § 9g | REFLECTED | — | |
| 21 | Decisions: Worktree cleanup | "Platform does not prune worktrees." | spec-session-agent.md § Deletion ("schedule/ branches skip removal") | REFLECTED | — | |
| 22 | Decisions: Auto-continuation | "`autoContinue` flag per session" | spec-nats-payload-schema.md; spec-state-schema.md; spec-session-agent.md § QuotaMonitor | REFLECTED | — | |
| 23 | Decisions: Cancel | "`sessions.delete` — emits `session_job_cancelled`" | spec-session-agent.md § Deletion; spec-nats-activity.md § 9g Cancellation | REFLECTED | — | |
| 24 | Decisions: Degraded fallback | "Agent persists `claudeSessionID` to session KV. On session loss, respawns with `claude --resume`" | spec-session-agent.md § Resumption; spec-nats-activity.md § 9n | REFLECTED | — | |
| 25 | Decisions: Permission-denied signaling | "In-process Go channel from `Session.onStrictDeny` to `QuotaMonitor.permDeniedCh`" | spec-session-agent.md § Session Callbacks | REFLECTED | — | |
| 26 | Component Changes: Extended sessions.create | 9 new fields (all top-level, not nested) | spec-nats-payload-schema.md § sessions.create (all 9 fields, top-level); spec-state-schema.md § sessions.create subject row | REFLECTED | — | |
| 27 | Component Changes: defaultDevHarnessAllowlist eliminated | "spec-session-agent.md must be updated to remove the 'default set is applied' paragraph" | spec-session-agent.md § Permission Policy ("The previous implicit `defaultDevHarnessAllowlist` is eliminated.") | REFLECTED | — | |
| 28 | Component Changes: QuotaMonitor goroutine | "Select-loop: 5 cases — stopCh, permDeniedCh, quotaCh, turnEndedCh (priority), doneCh" | spec-session-agent.md § QuotaMonitor Goroutine (all 5 cases, nested select priority) | REFLECTED | — | |
| 29 | Component Changes: Token counting | "Two-strategy: byte estimate `len(raw)/4`, authoritative `usage.output_tokens`" | spec-session-agent.md § Token Counting (both strategies) | REFLECTED | — | |
| 30 | Component Changes: handleTurnEnd | "3-row table: quota_soft → paused, quota_hard → paused, empty → completed" | spec-session-agent.md § Turn-End Detection (matching table) | REFLECTED | — | |
| 31 | Component Changes: handleSubprocessExit | "`terminalEventPublished` → no-op, otherwise → `session_job_failed`" | spec-session-agent.md § Subprocess Exit | REFLECTED | — | |
| 32 | Component Changes: Agent restart recovery (KV-based) | "pending → respawn; paused → check alive or --resume; running → --resume or failed" | spec-session-agent.md § Resumption; spec-nats-activity.md § 9n | REFLECTED | — | |
| 33 | Component Changes: handleDelete branching | "`SoftThreshold > 0` distinguishes interactive vs quota-managed" | spec-session-agent.md § Deletion (two branches) | REFLECTED | — | |
| 34 | Component Changes: Session KV extensions | 8 new fields: softThreshold, hardHeadroomTokens, autoContinue, pausedVia, claudeSessionID, branchSlug, failedTool, resumeAt | spec-state-schema.md § SessionState (all 8); spec-nats-payload-schema.md § KV Payloads (all 8) | REFLECTED | — | |
| 35 | Data Model: QuotaStatus | "U5, U7, R5, R7, HasData, TS" | spec-state-schema.md § Quota Status; spec-nats-payload-schema.md § Quota Subject | REFLECTED | — | |
| 36 | Data Model: 5 lifecycle events | "session_job_complete, session_job_paused, session_job_cancelled, session_permission_denied, session_job_failed" | spec-state-schema.md § Lifecycle Event Payloads (all 5); spec-nats-payload-schema.md § Job Lifecycle Events (all 5) | REFLECTED | — | |
| 37 | Component Changes: Session callbacks | "`onStrictDeny` and `onRawOutput`" | spec-session-agent.md § Session Callbacks (both) | REFLECTED | — | |
| 38 | Component Changes: sessions.input message format | "Three types: prompt, soft-stop marker, resume nudge" | spec-session-agent.md § Message Format (all 3); spec-nats-activity.md § 9g | REFLECTED | — | |
| 39 | Component Changes: hard-stop NOT published to NATS | "`control_request` interrupts are NOT published to NATS" | spec-session-agent.md § Message Format ("NOT published to NATS") | REFLECTED | — | |
| 40 | Component Changes: hasData==false behavior | "QuotaMonitor does NOT trigger stop or start" | spec-session-agent.md § QuotaMonitor; spec-nats-activity.md § 9j | REFLECTED | — | |
| 41 | Component Changes: status enum | "pending, running, paused, requires_action, completed, stopped, cancelled, needs_spec_fix, failed, error" | spec-state-schema.md § SessionState; spec-nats-payload-schema.md § KV Payloads | REFLECTED | — | Full 10-value enum matches. |
| 42 | User Flow: Cancel path — tombstone | "Agent tombstones the session KV entry (KV delete)" | spec-session-agent.md § Deletion; spec-nats-activity.md § 9g | REFLECTED | — | |
| 43 | Security: branchSlug regex | "`[a-z0-9-]+` prevents path-traversal" | spec-state-schema.md § SessionState (`^[a-z0-9][a-z0-9-]*$`) | REFLECTED | — | |
| 44 | Data Model: session_job_complete | "No `jobId`. No `prUrl`." | spec-state-schema.md § session_job_complete | REFLECTED | — | |

### Summary

- Reflected: 40
- Gap: 4
- Partial: 0

### Gap Details

**GAP 1 [SPEC→FIX]** (row 5): `spec-nats-payload-schema.md` — `api.agents.register` response missing `quotaPublisher` field.
- ADR: "delivered via a `quotaPublisher: true` field in the CP's response to `mclaude.hosts.{hslug}.api.agents.register`"
- Current payload schema response: `{"ok": true}` — no `quotaPublisher`.
- Fix: Add `quotaPublisher` boolean to the response schema and field table.

**GAP 2 [SPEC→FIX]** (row 7): `spec-nats-payload-schema.md` — missing `manage.designate-quota-publisher` subject.
- ADR: "CP re-designates the next online agent for that user and notifies it via `manage.designate-quota-publisher`"
- Payload schema claims "the exact JSON payload for every NATS subject" but omits this subject entirely.
- Fix: Add a section documenting `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.manage.designate-quota-publisher` with empty payload `{}`.

**GAP 3 [SPEC→FIX]** (row 8): `spec-state-schema.md` — `manage.designate-quota-publisher` subject missing from NATS subjects tables.
- The state schema's NATS subjects tables do not list this subject.
- Fix: Add to the "Session subjects" table: publisher=CP, subscriber=agent, payload=empty.

**GAP 4 [SPEC→FIX]** (row 9): `spec-session-agent.md` — NATS Subjects (Subscribe) missing `manage.designate-quota-publisher`.
- The session-agent spec's subscribe section lists session commands, terminal API, and quota status — but omits the re-designation subscription.
- The Quota Publisher Designation section only describes initial designation (via registration reply), not re-designation.
- Fix: (a) Add a subscribe bullet for `manage.designate-quota-publisher` core NATS subscription. (b) Add re-designation handling to the Quota Publisher Designation section: "If the designated agent disconnects, CP re-designates the next online agent by publishing to `manage.designate-quota-publisher`. On receipt, the agent starts `runQuotaPublisher`."
