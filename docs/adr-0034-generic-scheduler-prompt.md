# ADR: Generic Scheduler Primitive — Decouple Scheduling from SDD

**Status**: accepted
**Status history**:
- 2026-04-24: draft
- 2026-04-26: accepted — paired with `docs/spec-state-schema.md` updates (JobEntry schema, lifecycle event payloads) and supersession note prepended to `docs/adr-0009-quota-aware-scheduling.md`

> Supersedes the `scheduledSessionPrompt` template, the `specPath → component`
> mapping, the `SESSION_JOB_COMPLETE:` completion marker, the
> same-prompt-on-redispatch continuation model, the single-threshold quota
> model, and the `/schedule-feature` skill described in
> `adr-0009-quota-aware-scheduling.md`. The quota publisher, lifecycle
> subscriber, HTTP API surface, strict-allowlist mechanics, and
> `mclaude-job-queue` KV bucket from ADR-0009 remain in force; only the
> layers listed above are replaced.

## Overview

Turn the mclaude scheduler into a methodology-agnostic long-running-interruptible
job runner. The caller POSTs a free-text `prompt` and the platform spawns an
unattended Claude session to execute it. The platform never interprets the
prompt's content: it enforces quota, preserves session state across quota
pauses, and hands termination decisions to the caller's Stop hook. Paused
sessions stay alive inside the always-running per-project session-agent pod,
so "resume" is just another user message in the same conversation — no
restart, no `--resume` dance, no lost context. Quota pressure is handled in
two tiers: a soft threshold that injects a cooperative stop marker and waits
for Claude to end its turn, and a hard token budget that aborts the current
turn via `control_request` interrupt.

## Motivation

ADR-0009 baked spec-driven development into the platform: `/schedule-feature`
took a `specPath`, the dispatcher derived a `component`, and
`scheduledSessionPrompt` emitted a template hardcoded to
`/dev-harness <component>`. After the plugin extraction (ADR-0026),
`/dev-harness` no longer exists in this project — every scheduled session
would fail at step 1 of its own prompt. More fundamentally, the SDD coupling
prevented mclaude from being used as a general unattended-Claude platform:
anyone who wants scheduled refactors, arbitrary code generation, doc
updates, or any non-SDD work cannot queue a job without the system
pretending the work is a spec implementation.

Two other coupling points sit alongside the prompt template: the
completion-via-stdout-marker mechanism (`SESSION_JOB_COMPLETE:{prUrl}`) — a
platform scan for a specific string — and the same-prompt-on-redispatch
continuation model, which forces every paused session to restart from a
fresh conversation and relies on the caller's prompt being idempotent. Both
are replaced: completion is driven by Claude Code's native Stop hook, and
continuation keeps the session alive across pauses so resumption is just a
new user message.

The platform primitive is "queue a Claude session on a worktree, enforce
quota, let the caller steer stop decisions via Claude Code's Stop hook."
That primitive is generic. The SDD-flavored prompt composition and Stop
hook configuration belong in the plugin that owns the SDD vocabulary.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Prompt shape | Caller supplies a free-text `prompt` field; platform passes it verbatim as the initial user message. | Methodology-agnostic. Platform never parses, rewrites, or prepends content. |
| `specPath` field on `JobEntry` | Removed entirely. Replaced by `prompt` (required), `title` (display label for `/job-queue`), and `branchSlug` (required — used to compute the worktree branch). | Predictable top-level fields. Caller-provided slug keeps branch names readable without the platform parsing prompt content. |
| `hostSlug` field on `JobEntry` | Retained from `spec-state-schema.md`; required on POST. Identifies which host the scheduled session runs on. Used by the dispatcher to construct the 4-part session KV key (`{userSlug}.{hostSlug}.{projectSlug}.{sessionSlug}`) and host-scoped NATS subjects per ADR-0004. | Multi-laptop setups (ADR-0004) can have multiple hosts per project; v1 makes the caller pick explicitly rather than implicitly defaulting. Subject routing and session KV lookups fail without it. |
| Completion signalling | No stdout-marker scanning. Completion happens when Claude ends a turn naturally (no platform-injected marker) and the Stop hook allows the stop. Platform then calls `sessions.delete`. | Platform stays out of content. Stop hook is the authoritative "is this job done?" decider; it's already in the caller's toolkit as a Claude Code native mechanism. |
| Stop hook mechanism | Claude Code's native `hooks.Stop` (in the caller's `settings.json` or equivalent) — not a new platform primitive. | Reuses Claude Code's hook infrastructure. Hook fires on Claude's natural turn-end; can block with `{"decision": "block", "reason": "..."}` to force another turn. |
| Stop-reason signalling (platform → hook) | Platform injects `MCLAUDE_STOP: quota_soft` as a user message before waiting for turn-end on soft quota. Caller's hook reads `transcript_path` (native hook input field), scans the last user message for the marker, and allows the stop when present. No marker for hard-stop or cancel (both bypass the hook by design). | Claude Code's hook input schema is closed. Transcript is the one surface both sides can see. `quota_soft` is the only cooperative stop; hard-stop and cancel are forceful by definition. |
| Quota — soft threshold | `softThreshold` (% 5h utilization) per job. On breach, platform injects `MCLAUDE_STOP: quota_soft` and waits for Claude to end its turn. | Same shape as existing threshold, renamed for the two-tier model. |
| Quota — hard budget | `hardHeadroomTokens` per job. After the soft marker is injected, the session-agent counts output tokens emitted by Claude (from stream-json `usage` events); at `hardHeadroomTokens`, platform sends a `control_request` interrupt. Claude's current turn ends mid-response; the subprocess stays alive (the interrupt is not a kill). | Token counter lives in session-agent (already reads stream-json). More precise than a percentage — percentages can be derived from tokens, not the other way around. Hook bypass is guaranteed by Claude Code (interrupts don't fire Stop hooks). |
| Paused sessions stay alive | Soft-stop and hard-stop both leave the Claude Code subprocess running inside the per-project session-agent pod. Subprocess sits on stdin, in-memory conversation intact. Dispatcher does not terminate. | Claude Code on stream-json idles on stdin indefinitely. Keeping the session alive across quota pauses eliminates all restart complexity (no `--resume`, no prompt idempotency, no context loss). |
| Resume from pause | Dispatcher sends a new user message via the existing `sessions.input` subject. Caller-supplied `resumePrompt` if provided; otherwise a platform default (see below). | Resume is just another turn in the same conversation. |
| Default resume nudge | `"Resuming — continue where you left off."` for soft-paused sessions. `"Your previous turn was interrupted mid-response. Check `git status` and recover state before continuing."` for hard-paused sessions. | Hard-stopped sessions may have inconsistent state (uncommitted edits, partial tool calls); a different nudge prompts recovery. |
| Cancel | Single verb: `DELETE /jobs/{id}`. Behavior: `control_request` interrupt + `sessions.delete` + mark `cancelled`. No cooperative variant, no `?force=true` flag. | If the user pulled the job from the queue, they want it gone. Cooperative cancel was ceremony. Stop hook doesn't fire because interrupts bypass it. |
| `--resume` (Claude Code native) | Degraded fallback for actual session loss: subprocess crash, node reboot, pod eviction, daemon crash mid-pause. Dispatcher persists the Claude Code session ID (`ClaudeSessionID`) to `JobEntry` on first dispatch; on fallback, spawns a new session with `--resume <ClaudeSessionID>` and injects the resume nudge. If `--resume` fails, job is marked `failed`. | Normal path never uses this. Only covers actual infra failure. |
| Statuses on `JobEntry` | `queued`, `starting`, `running`, `paused`, `completed`, `cancelled`, `failed`, `needs_spec_fix`. | Status = what the platform did. Observability of how it got there is captured in separate fields. |
| Pause observability | `pausedVia: "quota_soft" \| "quota_hard"` field on `JobEntry`. Set when status transitions to `paused`; cleared on resume. | Lineage of a paused state without multiplying statuses. Dispatcher uses this to pick the appropriate resume nudge. |
| Worktree handling | Always create a worktree at `schedule/{branchSlug}` (no short-ID suffix). If one already exists, the session attaches to it — two sessions with the same `branchSlug` share a worktree. | Worktrees are a session-management concern, not SDD-specific. Same slug = shared worktree lets scheduled sessions collaborate or hand off. Dropping the short-ID makes shared-slug attach unambiguous. |
| Worktree cleanup | Platform does not prune worktrees. The caller's PR/merge workflow owns branch lifecycle. | Avoids cross-job race conditions; platform stays out of git state beyond creation/attach. |
| Default permission policy | None. Caller must pass `permPolicy` and `allowedTools` on POST. Missing or empty → 400. | Explicit. Forces every caller to think about what tools their unattended session needs. |
| `scheduledSessionPrompt`, `specPathToComponent`, `specPathToSlug` | Deleted. | Prompt composition moves out of the platform. |
| `SESSION_JOB_COMPLETE:` scanning in `onRawOutput` | Deleted. `onRawOutput` callback itself stays for token counting. | No output-marker scanning in the platform. |
| `/schedule-feature` skill location | Moves from mclaude to the `spec-driven-dev` plugin; deleted from mclaude. | Skill composes an SDD-flavored prompt + configures a Stop hook; both are SDD concerns. |
| `/job-queue` skill | Stays in mclaude. Display changes: `SPEC` column → `TITLE`; status detail shows first 200 chars of `Prompt` + `SoftThreshold`/`HardHeadroomTokens` + `PausedVia`. | Pure platform UX; no SDD vocabulary. |
| ADR-0009 treatment | Keep accepted; prepend a supersession note listing the replaced sections. | Per ADR-0018, cross-reference note is the allowed mechanical edit on an accepted ADR. |

## User Flow

### POST a job

1. Caller assembles a free-text prompt. The prompt may optionally include instructions for Claude on how to respond to `MCLAUDE_STOP: quota_soft` (e.g. "commit in-progress work, output a one-line status, stop"). If the prompt is silent on this, Claude will typically wrap up on its own; if it doesn't, the hard-token budget catches it.
2. Caller (optionally) configures a Stop hook. The hook can block turn-ends to keep Claude running (course-correction). To avoid fighting with the platform on quota stops, the hook should check whether the last user message in `transcript_path` contains `MCLAUDE_STOP: quota_soft` and NOT block when it does. Without a hook, every natural turn-end results in `completed`.
3. Caller POSTs to `http://localhost:8378/jobs`:
   ```json
   {
     "prompt": "<free text>",
     "title": "refactor-auth-middleware",
     "branchSlug": "refactor-auth-middleware",
     "projectId": "...",
     "hostSlug": "laptop-1",
     "priority": 5,
     "softThreshold": 75,
     "hardHeadroomTokens": 50000,
     "autoContinue": true,
     "permPolicy": "strict-allowlist",
     "allowedTools": ["Read", "Write", "Edit", "Glob", "Grep", "Bash"],
     "resumePrompt": ""
   }
   ```
4. Platform writes a `JobEntry` with status `queued`. Dispatcher picks it up when quota allows.

### First dispatch

1. Dispatcher reads `job.BranchSlug` and `job.HostSlug`, ensures the worktree `schedule/{branchSlug}` exists on the target host (creates if absent; attaches if present).
2. Dispatcher calls `sessions.create` with `permPolicy`, `allowedTools`, and the worktree branch — sending to `subj.UserHostProjectAPISessionsCreate(userSlug, hostSlug, projectSlug)`.
3. Session-agent spawns the Claude Code subprocess and routes its stream-json stdout. The first event is Claude Code's `system`/`init` event: `{"type":"system","subtype":"init","session_id":"...","cwd":"...","model":"..."}`. Session-agent extracts `session_id` from this event and includes it in the `sessions.create` NATS reply as `claudeSessionID`.
4. Dispatcher persists the returned `claudeSessionID` to `JobEntry.ClaudeSessionID`.
5. Dispatcher sends `job.Prompt` as the initial user message via `sessions.input`. Status → `running`.

### Soft-stop path

1. Quota publisher reports `u5 >= job.SoftThreshold`. Both the dispatcher (via `quotaCh`) and every per-session QuotaMonitor (via its own subscription to `mclaude.{userSlug}.quota`) observe this simultaneously.
2. Dispatcher (sole marker injector): publishes `sessions.input` on `subj.UserHostProjectAPISessionsInput(userSlug, hostSlug, projectSlug)` with the content `"MCLAUDE_STOP: quota_soft"`. QuotaMonitor does NOT inject the marker — the dispatcher is the single source of truth for quota-level decisions (priority ordering, etc.), and dual-injection would duplicate the marker in Claude's context.
3. QuotaMonitor (on the same quota-threshold observation) sets its local `stopReason = "quota_soft"` and resets `outputTokensSinceSoftMark = 0`. This is a STATE update only — no stdin write. Token counting begins now.
4. Claude processes the marker. Caller's prompt (or default Claude behavior) wraps up the task — commits, summarizes, whatever.
5. Claude ends its turn, emitting a stream-json `result` event. QuotaMonitor's `onRawOutput` sees it, dispatches to `handleTurnEnd()`, which publishes `session_job_paused` with `pausedVia: "quota_soft"` and `r5` from the cached `QuotaStatus`. `stopReason` resets to `""`.
6. `runLifecycleSubscriber` receives the event and writes `JobEntry.Status = paused`, `PausedVia = "quota_soft"`. If `AutoContinue` is true and the event's `r5` is set, `ResumeAt = r5`. The dispatcher does NOT write the status — the lifecycle subscriber is the single writer (matches ADR-0009's pattern and the cancel path).

### Hard-stop path

1. While `stopReason == "quota_soft"`, QuotaMonitor counts output tokens on every `onRawOutput` event.
2. When `outputTokensSinceSoftMark >= job.HardHeadroomTokens`, QuotaMonitor sets `stopReason = "quota_hard"` and queues a `control_request` interrupt on `s.stdinCh` directly (bypassing the NATS/sessions.input path, because the interrupt is monitor-local and must fire immediately).
3. Claude's current turn ends mid-response (interrupted). Stop hook is NOT fired (interrupts bypass it per Claude Code). Claude emits a final `result` event reflecting the interrupted turn.
4. QuotaMonitor's `onRawOutput` sees the `result`, dispatches to `handleTurnEnd()`, which publishes `session_job_paused` with `pausedVia: "quota_hard"`, `r5`, and `outputTokensSinceSoftMark`. `stopReason` resets to `""`.
5. `runLifecycleSubscriber` writes `JobEntry.Status = paused`, `PausedVia = "quota_hard"`. Same `AutoContinue`/`ResumeAt` logic as soft-stop. Session stays alive awaiting stdin.

### Resume path

1. Quota publisher reports `u5` below all paused jobs' `SoftThreshold`. Dispatcher sorts paused jobs by `Priority` descending, resumes one at a time. If a job has `ResumeAt` set and the current time is before that, the job is skipped until `ResumeAt` passes.
2. For each resumable job: dispatcher first verifies the session is still alive by doing a `sessKV.Get` at `subj.SessionsKVKey(userSlug, hostSlug, projectSlug, sessionSlug)`. If the key is missing → session lost, fall through to the degraded-fallback path below. If present → proceed.
3. Dispatcher picks the resume nudge —
   - `job.ResumePrompt` if non-empty.
   - Platform default for `quota_soft`: `"Resuming — continue where you left off."`.
   - Platform default for `quota_hard`: `"Your previous turn was interrupted mid-response. Check git status and recover state before continuing."`.
4. Dispatcher sends the nudge via `sessions.input` (same session ID; same live subprocess; same conversation).
5. Dispatcher updates `JobEntry`: `Status = running`, `PausedVia = ""`, `ResumeAt = nil`.

### Natural completion path

1. Claude ends a turn with no platform-injected marker preceding it. Stream-json `result` event fires.
2. Stop hook fires. Hook does not block (either because none is configured, or because the caller's hook judged this a genuine completion).
3. QuotaMonitor's `onRawOutput` sees the `result` event and routes to `handleTurnEnd()`. `stopReason == ""`, so `handleTurnEnd()` publishes `session_job_complete` on `subj.UserHostProjectLifecycle(...)`.
4. `runLifecycleSubscriber` receives `session_job_complete`. Subscriber writes `JobEntry.Status = completed` AND publishes `sessions.delete` on `subj.UserHostProjectAPISessionsDelete(...)`. The subscriber is the single point that (a) writes terminal status and (b) issues session cleanup for completion.
5. Session-agent's `handleDelete` terminates the subprocess via `stopAndWait`; the `schedule/` branch-prefix carve-out skips worktree removal; `session_stopped` is published (ignored by the subscriber — terminal status already written).

### How the platform distinguishes paused-vs-completed on turn-end

The platform does **not** scan Claude's output for domain content. But it DOES observe Claude Code's stream-json `result` event (the canonical turn-end signal — see `EventTypeResult` / `resultEvent` struct in `events.go`) because that's the only reliable way to detect that a turn has ended while the subprocess is still alive. Every turn in a stream-json session ends with exactly one `result` event before Claude goes back to awaiting stdin; `session.doneCh` only closes on subprocess exit, which — under ADR-0034 — happens only on `sessions.delete` (completion, cancel) or subprocess crash, never on quota-driven stops.

The QuotaMonitor carries a local state variable `stopReason` tracking the most recent platform-initiated action:

- On soft-stop: `stopReason = "quota_soft"` and QuotaMonitor records the timestamp when it injected `MCLAUDE_STOP: quota_soft`.
- On hard-stop: `stopReason = "quota_hard"` (set when the token counter fires the `control_request` interrupt).
- On natural completion: `stopReason = ""` — QuotaMonitor never took platform action.

When `onRawOutput` sees a `result` event, the monitor's select-loop case for `turnEndedCh` fires `handleTurnEnd()`, which inspects `stopReason`:

| `stopReason` | Event published | Next step |
|--------------|-----------------|-----------|
| `"quota_soft"` | `session_job_paused` with `pausedVia: "quota_soft"` + `r5` | `stopReason` reset to `""`; subprocess stays alive awaiting stdin. Dispatcher sets `Status = paused`. |
| `"quota_hard"` | `session_job_paused` with `pausedVia: "quota_hard"` + `r5` + `outputTokensSinceSoftMark` | Same as above, but `pausedVia` captures the mid-turn-cut lineage. |
| `""` | `session_job_complete` | Lifecycle subscriber writes `Status = completed` AND publishes `sessions.delete`. `handleDelete`'s `stopAndWait` terminates the subprocess. |

This keeps the decision entirely platform-side. Claude's output content never factors in (the monitor treats a `result` event as a turn-end marker without reading its body). The Stop hook's decision to allow or block the turn is still the gate upstream — but the platform's interpretation of an allowed stop depends only on what the platform itself did in the lead-up.

### Cancel path

1. User calls `DELETE /jobs/{id}`.
2. Dispatcher publishes `sessions.delete` on `subj.UserHostProjectAPISessionsDelete(userSlug, hostSlug, projectSlug)` with `{"sessionId": "..."}`. Session-agent's existing `handleDelete` calls `sess.stopAndWait(sessionDeleteTimeout)`, which internally sends a `control_request` interrupt to Claude's stdin and waits for the subprocess to exit. `handleDelete` then cleans up the session KV entry and publishes its own `session_stopped` event. This ADR does NOT modify `handleDelete`.
3. Dispatcher directly publishes `session_job_cancelled` on `subj.UserHostProjectLifecycle(userSlug, hostSlug, projectSlug, sessionSlug)` — see `spec-state-schema.md` § Lifecycle Event Payloads for the payload. The dispatcher owns this publication (not the session-agent) because `handleDelete`'s `session_stopped` is a generic non-job event and has no knowledge of whether the session corresponds to a job queue entry. Modifying `handleDelete` to emit job-specific events would couple it to job-queue logic, breaking the separation that ADR-0009 established between scheduled and ad-hoc sessions.
4. `runLifecycleSubscriber` receives the `session_job_cancelled` event and updates `JobEntry.Status = cancelled`.

The dispatcher does NOT send a separate `control_request` interrupt on `sessions.input` before the delete — `handleDelete`'s internal `stopAndWait` already handles the interrupt. Sending a second one would be redundant and could race with the delete path. No transient `cancelling` status is recorded — the dispatcher publishes `sessions.delete` and `session_job_cancelled`; the subscriber (in-daemon) does the final status write.

### Degraded fallback: session loss

If a paused session's KV entry is missing when the dispatcher tries to resume (because the subprocess died in pod eviction, node reboot, daemon crash, etc.), the resume path falls through to a session-recreate via Claude Code's native `--resume`:

1. Dispatcher constructs a `sessions.create` payload with a new field `resumeClaudeSessionID = job.ClaudeSessionID` and publishes it on `subj.UserHostProjectAPISessionsCreate(userSlug, hostSlug, projectSlug)`.
2. Session-agent's `handleCreate` sees `resumeClaudeSessionID` non-empty and spawns `claude --resume <claudeSessionID>` instead of a fresh session. Claude Code restores the conversation from its own on-disk JSONL transcript under `~/.claude/projects/`.
3. The new session emits a `system`/`init` event whose `session_id` matches the resumed one. Session-agent captures it and replies on `sessions.create`.
4. Dispatcher updates `JobEntry.ClaudeSessionID` (in case the value differs on resume) and `JobEntry.SessionID` to the new mclaude session ID, then sends the resume nudge via `sessions.input`.
5. If step 1–2 fails (`handleCreate` returns an error, `--resume` aborts, transcript missing), dispatcher marks `Status = failed` with an error describing that both in-memory resume and `--resume` fallback failed.

### `/job-queue` display

`list` columns: `ID, TITLE, PRI, STATUS, SESSION`. `status <jobId>` renders the full `JobEntry` with `Prompt` truncated to the first 200 chars, `SoftThreshold`/`HardHeadroomTokens` broken out, and `PausedVia` when non-empty.

## Stop Hook Authoring Guide

Callers are responsible for two things to make scheduled sessions work well: (1) the **prompt** must teach Claude how to respond to `MCLAUDE_STOP: quota_soft`, and (2) the **Stop hook** (optional but recommended for anything long-running) must distinguish a platform-initiated stop from a Claude-initiated one and respond appropriately.

### Prompt obligations

The prompt should include explicit handling for `MCLAUDE_STOP: quota_soft`. Minimum viable guidance:

```
If you receive a user message starting with "MCLAUDE_STOP: quota_soft":
  1. Save any in-progress work (git add + commit, or write to a scratchpad file).
  2. Output a one-line summary of where you are.
  3. End your turn. Do not start new tasks.
```

This can live in the `prompt` field itself, or in the project's `CLAUDE.md` that the session loads on first turn, or in a skill the caller installs in the worktree. The platform doesn't care where — as long as Claude responds to the marker, the soft-stop path works cleanly.

If the prompt is silent on this and Claude keeps working past the marker, the hard-token budget (`HardHeadroomTokens`) catches it and the session ends up `paused` with `PausedVia: "quota_hard"`. That's the safety net, not the intended path.

### Stop hook responsibilities

Two scenarios the hook must distinguish:

**Scenario A — platform-initiated stop (quota_soft).** The last user message in the transcript is `MCLAUDE_STOP: quota_soft`. Claude has saved state and ended its turn. The hook should **allow the stop** (return with no decision, or exit 0). The platform will mark the job `paused` and resume it later.

**Scenario B — Claude-initiated stop (natural turn-end).** No `MCLAUDE_STOP:` marker in the recent user messages. Claude thinks it's done. The hook should **debate**: is the work actually complete? If the caller has work-done criteria (all spec gaps closed, all tests passing, PR created, whatever), the hook checks them. If satisfied, allow the stop (the platform will mark `completed` and delete the session). If not, block with steering:

```json
{ "decision": "block", "reason": "Spec gap X is still open — investigate and close before stopping." }
```

### Reference hook skeleton

A bash Stop hook reads JSON on stdin and writes a JSON decision on stdout. The hook input includes `transcript_path`; the hook reads the transcript to find the last user message.

```bash
#!/usr/bin/env bash
# ~/.claude/hooks/stop.sh (or wherever settings.json points)
# Fired by Claude Code when Claude is about to end a turn.

INPUT=$(cat)
TRANSCRIPT=$(jq -r .transcript_path <<< "$INPUT")

# Grab the last user message — jsonl; each line is an event.
LAST_USER=$(tac "$TRANSCRIPT" | jq -r 'select(.type=="user") | .message.content' | head -1)

# Scenario A: platform asked us to stop. Allow.
if [[ "$LAST_USER" == MCLAUDE_STOP:* ]]; then
  exit 0
fi

# Scenario B: Claude-initiated. Check project-specific "am I done?" criteria.
# This is where your caller-side logic lives. Example: grep for any open spec gaps.
if unfinished_work_exists; then
  jq -n '{decision: "block", reason: "There are still open spec gaps. Continue working."}'
  exit 0
fi

# Genuinely done. Allow the stop.
exit 0
```

### Anti-patterns

- **Hook blocks even when `MCLAUDE_STOP: quota_soft` is present.** The session keeps running past the marker; the hard-token budget eventually interrupts; job ends in `paused` with `PausedVia: "quota_hard"`. The `PausedVia: quota_hard` on a soft-threshold breach is a strong signal that the hook is miscoded.
- **Hook has no Scenario-A path and only handles Scenario-B logic.** Platform stops still work (the hook allowing by default lets them through), but the hook's work-done logic may misfire on `MCLAUDE_STOP:` messages by thinking "the user just said to stop, that's a blocker for work-done criteria." Always short-circuit on the marker before running the debate logic.
- **No hook at all, and the prompt has no work-done criteria.** Every natural turn-end becomes `completed` — fine if the session is expected to be a single-shot task, problematic if it's meant to be a long-running sprint. Consider at least a minimal hook that blocks once to ask Claude "are you truly done?" before allowing.

## Component Changes

### Session-Agent struct definitions and KV-key wrappers (`mclaude-session-agent/state.go`)

Both the struct definitions and the local KV-key wrappers in `state.go` need updating; `state.go` must be updated **before** any dispatcher or monitor code changes, otherwise the rest of this ADR will not compile:

- **`JobEntry`** (currently at `state.go:141–163`): replace with the full struct defined in Data Model § `JobEntry`. Remove `SpecPath`, `Threshold`, `PRUrl`. Add `Prompt`, `Title`, `BranchSlug`, `ResumePrompt`, `HostSlug`, `SoftThreshold`, `HardHeadroomTokens`, `PermPolicy`, `AllowedTools`, `ClaudeSessionID`, `PausedVia`. Adjust JSON tags to match `spec-state-schema.md`.
- **`QuotaMonitorConfig`** (currently at `state.go:132–137`): remove `Threshold int` and `Priority int`; add `SoftThreshold int` and `HardHeadroomTokens int`. Keep `JobID string` and `AutoContinue bool`. Priority is not carried on this struct — dispatcher reads it from `JobEntry` directly.
- **`sessionKVKey(userSlug, projectSlug, sessionSlug)`** (currently at `state.go:69`): update to take `hostSlug` and forward to the canonical 4-arg `subj.SessionsKVKey(u, h, p, s)`. New signature: `sessionKVKey(userSlug slug.UserSlug, hostSlug slug.HostSlug, projectSlug slug.ProjectSlug, sessionSlug slug.SessionSlug) string`.
- **`heartbeatKVKey(userSlug, projectSlug)`** (currently at `state.go:75`): update to take `hostSlug` and forward to the 3-arg `subj.ProjectsKVKey(u, h, p)`. New signature: `heartbeatKVKey(userSlug slug.UserSlug, hostSlug slug.HostSlug, projectSlug slug.ProjectSlug) string`.

Every call site of these wrappers (across `agent.go`, `daemon.go`, `daemon_jobs.go`, `session.go`) must be updated to pass the extra host slug — sourced from `job.HostSlug` for scheduled flows, from `req.HostSlug` (in `handleCreate`/`handleInput`/`handleDelete`) for ad-hoc session API flows, and from the session-agent's own config for heartbeat emission.

### Daemon (`mclaude-session-agent/daemon_jobs.go`)

- **Delete** `specPathToComponent`, `specPathToSlug`, `scheduledSessionPrompt`.
- **Modify** `dispatchQueuedJob`:
  - Branch name: `schedule/{job.BranchSlug}` (no short-ID suffix). If a worktree for the branch already exists, session-agent's `handleCreate` attaches rather than creating.
  - Initial user message: `job.Prompt` verbatim.
  - `sessions.create` payload now includes `permPolicy`, `allowedTools` from `JobEntry`, and `quotaMonitor: {softThreshold, hardHeadroomTokens, jobId, autoContinue}`.
  - After `sessions.create` returns, capture the Claude Code session ID from the reply and persist to `JobEntry.ClaudeSessionID`.
  - Status transitions: `queued → starting → running`.
- **New** `resumePausedJob(job *JobEntry)`:
  - Selects the resume nudge based on `job.ResumePrompt` and `job.PausedVia`.
  - Sends the nudge via `sessions.input` on the existing session.
  - Clears `job.PausedVia`; sets `job.Status = running`.
- **New** fallback logic in `dispatchQueuedJob` when the session is not found in `sessKV` (e.g. after daemon restart + pod eviction): construct `sessions.create` with a `resumeClaudeSessionID` field set to `job.ClaudeSessionID`.
- **Modify** all subject/KV key construction. `daemon_jobs.go` currently calls helper names that no longer exist in `mclaude-common/pkg/subj` (the existing file references `UserProjectAPISessionsCreate`, `UserProjectAPISessionsInput`, `UserProjectAPISessionsDelete`, `UserProjectLifecycle`, and uses 3 args for `SessionsKVKey`). The canonical `subj` package exports only the host-scoped forms: `UserHostProjectAPISessionsCreate(u, h, p)`, `UserHostProjectAPISessionsInput(u, h, p)`, `UserHostProjectAPISessionsDelete(u, h, p)`, `UserHostProjectLifecycle(u, h, p, s)`, and `SessionsKVKey(u, h, p, s)` taking 4 args. Replace every call site in `daemon_jobs.go` — specifically: `dispatchQueuedJob` (session creation, input sending, SessionsKVKey lookup), `startupRecovery` (SessionsKVKey lookup during daemon restart recovery), the quota-exceeded path in `processDispatch` (input/lifecycle publish), the cancel path in `handleJobByID` (session delete + lifecycle publish), and any other NATS/KV operations that reference a session. Source the host slug from `job.HostSlug`; source the user/project/session slugs from `job.UserID`/`job.ProjectID`/`job.SessionID` via the existing `slug` package. This aligns with ADR-0004 and also unblocks compilation (the existing daemon file does not currently build against the canonical `subj` package).
- **Fix** `runLifecycleSubscriber` subscription subject: the existing code subscribes to `mclaude.users.{uslug}.projects.*.lifecycle.*`, which is missing the `.hosts.*.` segment required by ADR-0004 and the `MCLAUDE_LIFECYCLE` stream pattern (`mclaude.users.*.hosts.*.projects.*.lifecycle.*`). Without the fix, the subscriber receives zero lifecycle events and every job-queue status transition silently fails. Change the subscription subject to `"mclaude.users." + string(d.cfg.UserSlug) + ".hosts.*.projects.*.lifecycle.*"`. This is a pre-existing bug from ADR-0009 that this ADR must resolve because the new event types (`session_job_paused` with `pausedVia`/`r5`, `session_job_cancelled`) flow through this subscriber — leaving the bug means none of ADR-0034's lifecycle logic functions.
- **Modify** quota-exceeded path in `processDispatch`:
  - Rename `job.Threshold` → `job.SoftThreshold`.
  - Content of the injected message: `"MCLAUDE_STOP: quota_soft"` (exact). Platform does not add human-readable explanation text; the caller's prompt owns that.
- **Modify** `DELETE /jobs/{id}`:
  - Removes the cooperative path and the `?force=true` flag handling — no such flag exists anymore.
  - Single behavior: dispatcher publishes `sessions.delete` (which causes `handleDelete` to interrupt + reap the subprocess internally via `stopAndWait`), then directly publishes the `session_job_cancelled` lifecycle event on `subj.UserHostProjectLifecycle(...)`. Does not write `Status = cancelled` directly; waits for `runLifecycleSubscriber` to receive the `session_job_cancelled` and write the terminal status (single write path).
  - Dispatcher does NOT send a separate `sessions.input` `control_request` interrupt — `handleDelete`'s internal interrupt is sufficient and sending a second one would be redundant.
  - Session-agent's `handleDelete` is NOT modified. It continues to emit `session_stopped` for any session deletion (job-queued or ad-hoc). The dispatcher's extra `session_job_cancelled` publish is what marks this as a job cancellation.
- **Modify** lifecycle subscriber (`runLifecycleSubscriber`) event → status map:
  - `session_job_paused` → `Status = paused`, `PausedVia = ev["pausedVia"]`. If `job.AutoContinue` is true, read `ev["r5"]` as the 5h reset time and set `ResumeAt = r5`. If `r5` is absent or unparseable, leave `ResumeAt` nil (dispatcher will resume whenever quota recovers). The event MUST carry `r5` for this path to function — see data-model § lifecycle event payloads.
  - `session_job_complete` → `Status = completed` AND publish `sessions.delete` on `subj.UserHostProjectAPISessionsDelete(...)` to reap the subprocess. Subscriber is the single caller of `sessions.delete` for the completion path (dispatcher calls it for cancel, subscriber for completion).
  - `session_job_cancelled` → `Status = cancelled`.
  - `session_job_failed` → `Status = failed`, `Error = ev["error"]`.
  - `session_permission_denied` → unchanged from ADR-0009 (`Status = needs_spec_fix`).

### Daemon HTTP server (`handleJobsRoute` POST)

- Request body struct:
  - Remove `SpecPath`, `Threshold`.
  - Add required: `Prompt string`, `BranchSlug string`, `HostSlug string`, `PermPolicy string`, `AllowedTools []string`, `SoftThreshold int`, `HardHeadroomTokens int`.
  - Add optional: `Title string` (falls back to `BranchSlug`), `ResumePrompt string` (falls back to platform default).
- Validation: 400 if any required field is empty/zero. `BranchSlug` and `HostSlug` must match `^[a-z0-9][a-z0-9-]*$`. `AllowedTools` must be non-empty.
- `JobEntry` construction: populate the new fields; `ClaudeSessionID` starts empty.

### Session-Agent (`session.go`, `quota_monitor.go`, `agent.go`)

- **`Session.onRawOutput`** callback: remove `SESSION_JOB_COMPLETE:` marker scanning. Keep the callback for token counting.
- **New field** on the `sessions.create` request: `ResumeClaudeSessionID string` (optional). When set, session-agent spawns the subprocess with `claude --resume <ResumeClaudeSessionID>` instead of a fresh session.
- **`sessions.create` reply** — the existing `id` field stays as-is (it carries the mclaude-internal session UUID and is used by the dispatcher to route subsequent `sessions.input` and `sessions.delete` calls). This ADR **adds** a new separate field `claudeSessionID` alongside `id`. The two fields have distinct values and distinct consumers:
  - `id` (unchanged): mclaude UUID generated by the session-agent. Used by the dispatcher to populate `JobEntry.SessionID` and for all subject-level routing (`sessions.input` NATS payloads reference this as `session_id`, session KV keys derive `sessionSlug` from it).
  - `claudeSessionID` (new): Claude Code's own session ID, extracted by the session-agent from the first stream-json event emitted by the Claude Code subprocess — `{"type":"system","subtype":"init","session_id":"...","cwd":"...","model":"..."}`. Used by the dispatcher to populate `JobEntry.ClaudeSessionID` for the degraded `--resume` fallback path. Not used in any NATS subject routing.
  - Session-agent reads the first stdout line synchronously (blocking on the first event) before sending the `sessions.create` reply, so the dispatcher always gets both fields populated on success.
  - Concretely, the existing `a.reply(msg, map[string]string{"id": sessionID}, "")` at `agent.go` becomes `a.reply(msg, map[string]string{"id": sessionID, "claudeSessionID": claudeSessionID}, "")` where `claudeSessionID` is the parsed value from the `system`/`init` event.
- **QuotaMonitor** changes:
  - Config: `SoftThreshold int`, `HardHeadroomTokens int` replaces `Threshold int`. `Priority` is NOT carried on `QuotaMonitorConfig` — priority lives only on `JobEntry` and is used exclusively by the dispatcher for preemption ordering; the monitor never needs it.
  - Monitor holds the most recent `QuotaStatus` received from its `mclaude.{userSlug}.quota` subscription (fields `u5, u7, r5, r7, hasData, ts`). `r5` from this latest snapshot is included in the `session_job_paused` event payload.
  - New state: `outputTokensSinceSoftMark int`, `stopReason string` (empty | "quota_soft" | "quota_hard"), `turnEndedCh chan struct{}` (1-buffered; fires once per turn-end detection).
  - `onRawOutput(evType, raw)` callback handles two jobs:
    - **Token counting:** parses the stream-json event; if `evType == EventTypeAssistant` OR `EventTypeResult` and `usage.output_tokens` is present, increments `outputTokensSinceSoftMark` (only while `stopReason != ""`). Fallback to `len(raw) / 4` estimate when `usage` is absent.
    - **Turn-end detection:** if `evType == EventTypeResult`, send non-blocking on `turnEndedCh`. The `result` event is Claude Code's canonical end-of-turn marker (see `events.go:EventTypeResult` + `resultEvent` struct — has `duration_ms`, `num_turns`, `usage`, `stop_reason`). This is the signal the monitor uses to detect turn-end while the subprocess is still alive — `session.doneCh` only fires on subprocess exit, so `result` events are the only reliable turn-end signal for soft-stop / hard-stop flows where the subprocess stays alive.
  - Select-loop has these cases:
    - `<-m.stopCh`: monitor shutdown signal, exit cleanly.
    - `toolName := <-m.permDeniedCh`: existing strict-allowlist handling (unchanged from ADR-0009).
    - `msg := <-m.quotaCh`: update cached `QuotaStatus`. If `u5 >= SoftThreshold` and `stopReason == ""`: set `stopReason = "quota_soft"` and reset `outputTokensSinceSoftMark = 0`. Do NOT write to `s.stdinCh` — the dispatcher is the sole marker injector (both are subscribed to the same quota topic; both observe the same threshold). This case is state-only on the monitor side.
    - `<-m.turnEndedCh` (NEW): fires whenever Claude emits a `result` event. Dispatch to `handleTurnEnd()` — see below.
    - Token-budget check (tick on `onRawOutput` increments, not the select): when `outputTokensSinceSoftMark >= HardHeadroomTokens` and `stopReason == "quota_soft"`, set `stopReason = "quota_hard"` and publish a `control_request` interrupt on `s.stdinCh`. The interrupt will cause Claude to end the current turn, which triggers a `result` event, which routes to `handleTurnEnd()` via `turnEndedCh`.
    - `<-session.doneCh`: subprocess actually exited. Call `handleSubprocessExit()`.
  - `handleTurnEnd()` inspects `stopReason` and emits the correct lifecycle event:
    - `stopReason == "quota_soft"` → publish `session_job_paused` with `pausedVia: "quota_soft"` and `r5` from cached `QuotaStatus`. Reset `stopReason = ""`. Subprocess stays alive.
    - `stopReason == "quota_hard"` → publish `session_job_paused` with `pausedVia: "quota_hard"`, `r5`, and `outputTokensSinceSoftMark`. Reset `stopReason = ""`. Subprocess stays alive.
    - `stopReason == ""` → publish `session_job_complete`. This signals the dispatcher to issue `sessions.delete` and transition `JobEntry.Status = completed`. The subprocess terminates shortly afterward via the cancel/delete path, which closes `doneCh`. The monitor's `handleSubprocessExit()` will then see `stopReason == ""` AND a lifecycle event has already been published, so it no-ops.
  - `handleSubprocessExit()` (on `doneCh` close) inspects state to distinguish expected termination (after `session_job_complete` or `session_job_cancelled` was already published by another path) from unexpected crashes:
    - If `session_job_complete` or `session_job_cancelled` has been published for this session (tracked via `m.terminalEventPublished bool`) → no-op (expected cleanup).
    - Otherwise → publish `session_job_failed` with `error: "subprocess exited without turn-end signal"`.
  - (Contrast with ADR-0009: the old monitor relied on `doneCh` for all lifecycle emits because soft-stop killed the subprocess after 30 min. In ADR-0034 the subprocess stays alive on quota events, so `result` events are the primary signal and `doneCh` is the cleanup signal.)
- **`Session.onRawOutput`** callback stays. Its direct call sites (the stdout router goroutine in `session.start()`) are unchanged — they invoke the callback under `s.mu.Lock()` for every raw event. QuotaMonitor's new turn-end + token-counting logic all lives inside the callback it registers.
- **`Agent` struct and `NewAgent`** (`agent.go`): add a `hostSlug slug.HostSlug` field to `Agent`. Populate it in `NewAgent(...)` from the incoming config (same way `userSlug`/`projectSlug` are populated today). Every `publishLifecycle*` method and event-subject construction must use the 4-arg host-scoped form:
  - `subj.UserHostProjectLifecycle(a.userSlug, a.hostSlug, a.projectSlug, sessionSlug)` replaces any `UserProjectLifecycle(...)` call.
  - `subj.UserHostProjectEvents(a.userSlug, a.hostSlug, a.projectSlug, sessionSlug)` for `session.go`'s event-publish subject (currently built without `.hosts.{hslug}.`).
  - This is another pre-existing compilation/routing bug that ADR-0034 must close because all the new lifecycle events flow through these methods.
- **`SessionState` struct** (`state.go`): add a field `ClaudeSessionID string `json:"claudeSessionID"`` alongside the existing mclaude `ID`. Populated by `handleCreate` from the parsed `system`/`init` event (same extraction as the `sessions.create` reply's `claudeSessionID`). Used by `session.start()` when resume is requested.
- **`session.start()` resume mechanism**: when `resume=true`, `start()` currently passes `--resume s.state.ID` (the mclaude UUID) to Claude Code. Change to `--resume s.state.ClaudeSessionID` when `ClaudeSessionID` is non-empty; fall back to the previous behavior only when it's empty (for compatibility with non-scheduled sessions that never captured a Claude Code ID). The scheduled fallback path (`handleCreate` with `resumeClaudeSessionID` set) populates `s.state.ClaudeSessionID` before `start()` is invoked, so resume consistently uses Claude Code's native ID.
- **`handleDelete` worktree-prune exclusion** (`agent.go:850–864`): `handleDelete` currently removes the worktree when the session is the last user. For scheduled sessions this conflicts with ADR-0034's "platform does not prune worktrees" decision — multiple scheduled sessions can share a worktree via `branchSlug`, and the worktree must survive across PR review. Change: if the branch name matches the `schedule/` prefix, skip the worktree removal step. Specifically: add `if strings.HasPrefix(sessionBranch, "schedule/") { return }` before the worktree-removal block. This is the only change to `handleDelete`; its lifecycle-event emission (`session_stopped`) and subprocess termination via `stopAndWait` are unchanged.
- **`onStrictDeny`** path: unchanged from ADR-0009.

### Tests (`mclaude-session-agent/quota_test.go`, `daemon_test.go`, etc.)

- **Delete** tests for `scheduledSessionPrompt`, `specPathToComponent`, `specPathToSlug`, `SESSION_JOB_COMPLETE:` marker detection.
- **New** tests:
  - `dispatchQueuedJob` reads `job.Prompt` verbatim and forwards it as `sessions.input` content.
  - `dispatchQueuedJob` uses `job.BranchSlug` for the branch name; no short-ID suffix.
  - `dispatchQueuedJob` attaches to an existing worktree when one matches `schedule/{branchSlug}`.
  - `dispatchQueuedJob` captures `ClaudeSessionID` from `sessions.create` reply and persists to KV.
  - `resumePausedJob` sends the platform default nudge when `ResumePrompt` is empty, with different text for `quota_soft` vs `quota_hard`.
  - `resumePausedJob` sends the caller's `ResumePrompt` when set (regardless of `PausedVia`).
  - `HandleJobsRoute` POST 400s on missing `Prompt`, `BranchSlug`, `PermPolicy`, empty `AllowedTools`, `BranchSlug` regex failure.
  - `QuotaMonitor` counts output tokens only after soft-mark injection; fires hard interrupt at `HardHeadroomTokens`.
  - `QuotaMonitor` detects turn-end via stream-json `result` event (not `doneCh`): synthetic `onRawOutput("result", ...)` call triggers `handleTurnEnd()` while subprocess stays alive.
  - `handleTurnEnd()` with `stopReason == ""` publishes `session_job_complete`; with `stopReason == "quota_soft"` publishes `session_job_paused` + `pausedVia: "quota_soft"`; with `stopReason == "quota_hard"` publishes `session_job_paused` + `pausedVia: "quota_hard"` + `outputTokensSinceSoftMark`.
  - Hard-stop path leaves the subprocess alive; `session_job_paused` lifecycle event is published with `pausedVia: "quota_hard"` after the `result` event arrives (not after `doneCh`).
  - `handleSubprocessExit()` after a terminal event was already published → no-op.
  - `handleSubprocessExit()` with no prior terminal event → publishes `session_job_failed`.
  - Cancel path: `DELETE /jobs/{id}` sends `control_request` interrupt + `sessions.delete`; no marker injection.
  - Fallback `--resume` path: dispatcher finds missing session in `sessKV`, creates new session with `ResumeClaudeSessionID`, sends resume nudge.

### mclaude skills

- **Delete** `.agent/skills/schedule-feature/` (moves to plugin).
- **Update** `.agent/skills/job-queue/`:
  - `list` header: `SPEC` → `TITLE`.
  - `status <jobId>` detail: show `Prompt` (truncated to 200 chars), `SoftThreshold`/`HardHeadroomTokens`, `PausedVia` when non-empty.

### spec-driven-dev plugin — `/schedule-feature` (separate repo, out of scope for this ADR's implementation)

Covered here for completeness. The plugin's skill:
1. Parses `<spec-path> [--priority N] [--soft-threshold N] [--hard-headroom-tokens N] [--auto-continue]`.
2. Derives component + branch slug from spec path (plugin-internal convention).
3. Composes an SDD-flavored prompt (instructs Claude to run `/feature-change <component>`, commit, and respond to `MCLAUDE_STOP: quota_soft`).
4. Configures a Stop hook in the plugin's settings (reads `transcript_path`, inspects recent output for spec-gap signals; blocks turn-end if gaps remain AND the last user message is not `MCLAUDE_STOP: quota_soft`).
5. Calls `GET http://localhost:8378/jobs/projects` to resolve `projectId`.
6. POSTs to `http://localhost:8378/jobs` with the full body.

### ADR-0009 (`docs/adr-0009-quota-aware-scheduling.md`)

Mechanical edit: prepend a supersession note near the top.

```markdown
> **Note:** The `scheduledSessionPrompt` template, the `specPath → component`
> mapping, the `SESSION_JOB_COMPLETE:{prUrl}` completion marker, the
> single-threshold quota model, the same-prompt-on-redispatch continuation
> model, and the `/schedule-feature` skill described below are superseded
> by `adr-0034-generic-scheduler-prompt.md`. The scheduler primitive is
> now content-agnostic — callers supply a free-text prompt and a branch
> slug directly; completion is signalled by Claude Code's Stop hook, not
> by stdout marker; quota is two-tier (soft percentage + hard output-token
> budget); paused sessions stay alive in the session-agent pod so resume
> is just a new user message rather than a restart. The `specPath` and
> `threshold` fields on `JobEntry` are removed; `PRUrl` is removed. New
> fields: `prompt`, `title`, `branchSlug`, `softThreshold`,
> `hardHeadroomTokens`, `permPolicy`, `allowedTools`, `resumePrompt`,
> `claudeSessionID`, `pausedVia`.
```

## Data Model

### `JobEntry` (in `mclaude-job-queue` KV)

```go
type JobEntry struct {
    ID                  string     `json:"id"`
    UserID              string     `json:"userId"`
    UserSlug            string     `json:"userSlug"`             // Retained from spec-state-schema.md. Typed slug for KV keys / NATS subjects.
    ProjectID           string     `json:"projectId"`
    ProjectSlug         string     `json:"projectSlug"`          // Retained from spec-state-schema.md.
    SessionID           string     `json:"sessionId"`            // mclaude session UUID from sessions.create
    SessionSlug         string     `json:"sessionSlug"`          // Retained from spec-state-schema.md. Derived from SessionID for KV key.
    Prompt              string     `json:"prompt"`               // NEW — free-text initial user message
    Title               string     `json:"title"`                // NEW — display label; falls back to BranchSlug
    BranchSlug          string     `json:"branchSlug"`           // NEW — worktree branch = "schedule/{branchSlug}"
    ResumePrompt        string     `json:"resumePrompt"`         // NEW — caller-supplied nudge on resume; empty = platform default
    HostSlug            string     `json:"hostSlug"`             // Retained from spec-state-schema.md. Required on POST. Feeds host-scoped NATS subjects and 4-part session KV key.
    Priority            int        `json:"priority"`
    SoftThreshold       int        `json:"softThreshold"`        // RENAMED from Threshold — % u5
    HardHeadroomTokens  int        `json:"hardHeadroomTokens"`   // NEW — output tokens past soft mark before hard interrupt
    AutoContinue        bool       `json:"autoContinue"`
    PermPolicy          string     `json:"permPolicy"`           // NEW — required on POST
    AllowedTools        []string   `json:"allowedTools"`         // NEW — required on POST
    Status              string     `json:"status"`               // queued | starting | running | paused | completed | cancelled | failed | needs_spec_fix
    PausedVia           string     `json:"pausedVia"`            // NEW — "quota_soft" | "quota_hard" | "" when not paused
    ClaudeSessionID     string     `json:"claudeSessionID"`      // NEW — Claude Code's own session ID for --resume fallback
    Branch              string     `json:"branch"`               // "schedule/{branchSlug}"
    FailedTool          string     `json:"failedTool"`
    Error               string     `json:"error"`
    RetryCount          int        `json:"retryCount"`
    ResumeAt            *time.Time `json:"resumeAt"`             // set if autoContinue + paused on quota (5h reset time)
    CreatedAt           time.Time  `json:"createdAt"`
    StartedAt           *time.Time `json:"startedAt"`
    CompletedAt         *time.Time `json:"completedAt"`
    // Removed: SpecPath, Threshold, PRUrl
}
```

### HTTP `POST /jobs` request body

```json
{
  "prompt": "...",
  "title": "refactor-auth-middleware",
  "branchSlug": "refactor-auth-middleware",
  "resumePrompt": "",
  "projectId": "...",
  "hostSlug": "laptop-1",
  "priority": 5,
  "softThreshold": 75,
  "hardHeadroomTokens": 50000,
  "autoContinue": true,
  "permPolicy": "strict-allowlist",
  "allowedTools": ["Read", "Write", "Edit", "Glob", "Grep", "Bash"]
}
```

Required: `prompt`, `branchSlug`, `projectId`, `hostSlug`, `softThreshold`, `hardHeadroomTokens`, `permPolicy`, `allowedTools`.
Optional: `title` (defaults to `branchSlug`), `resumePrompt` (defaults per `pausedVia`), `priority` (default 5), `autoContinue` (default false).

### HTTP `DELETE /jobs/{id}`

Single behavior: `control_request` interrupt + `sessions.delete` + mark `cancelled`. No flags.

### NATS subjects, KV buckets

All session-scoped subjects and KV keys use the host-slug-inclusive form from ADR-0004:
- Subjects: `mclaude.users.{userSlug}.hosts.{hostSlug}.projects.{projectSlug}.api.sessions.*` and `...lifecycle.{sessionSlug}`.
- `mclaude-sessions` KV key: `{userSlug}.{hostSlug}.{projectSlug}.{sessionSlug}` (per `subj.SessionsKVKey`).
- `mclaude-job-queue` KV key: `{userSlug}.{jobId}` (dot-separated, per `subj.JobQueueKVKey` and `spec-state-schema.md`). Unchanged from ADR-0009.

Lifecycle event set changes:
- `session_job_complete`: no `prUrl` field. Fields: `sessionId`, `jobId`, `branch`, `ts`.
- `session_job_paused`: carries `pausedVia` ("quota_soft" | "quota_hard"), `u5` (where available from the most recent `QuotaStatus`), `r5` (RFC3339 UTC; same source as `u5`), `outputTokensSinceSoftMark` (for hard-paused), `jobId`, `sessionId`, `ts`. Supersedes ADR-0009's `session_job_paused` + `session_quota_interrupted` (consolidated). `r5` is load-bearing for `AutoContinue` — subscriber cannot set `ResumeAt` without it.
- `session_job_cancelled` (new): `sessionId`, `jobId`, `ts`. Added to the canonical payload list in `spec-state-schema.md`.
- `session_permission_denied`: unchanged.
- `session_job_failed`: unchanged.

## Error Handling

| Failure | Handling |
|---------|----------|
| POST `/jobs` missing required field | 400 Bad Request with `{"error": "<field> required"}`. |
| `branchSlug` or `hostSlug` regex mismatch | 400 with `{"error": "<field> must match ^[a-z0-9][a-z0-9-]*$"}`. |
| `allowedTools` empty array | 400 with `{"error": "allowedTools must not be empty"}`. |
| `sessions.create` reply missing `claudeSessionID` (session-agent couldn't parse `system`/`init` event) | Dispatcher logs a warning and persists `ClaudeSessionID = ""`. Degraded-fallback `--resume` will mark the job `failed` because it cannot construct a valid `claude --resume` command without the ID. Normal resume still works (the session is alive). |
| Session-lost detection timing | Dispatcher always checks `sessKV.Get` on the session key BEFORE sending the resume nudge. If absent → degraded-fallback. This is synchronous; there is no reliance on NATS publish reply because `sessions.input` is fire-and-forget. |
| Prompt ignores `MCLAUDE_STOP: quota_soft` | Session keeps producing output. Token counter fires hard interrupt at `HardHeadroomTokens`. Session paused with `pausedVia: "quota_hard"`. Existing mechanism. |
| Stop hook blocks turn-end even when `MCLAUDE_STOP: quota_soft` is present (caller bug) | Session keeps running. Hard-stop fires at `HardHeadroomTokens`. Same outcome as above. The caller's hook is broken; `pausedVia: "quota_hard"` surfaces the issue. |
| Shared worktree: session B attaches while session A is still running | Both sessions use the same worktree. Both can write concurrently. Platform does not lock files. Caller is expected to have designed the prompts for collaboration. |
| Worktree exists but at diverged branch history | `git worktree add` errors at `handleCreate`. Session creation returns error. Dispatcher marks job `failed`. |
| Existing in-flight jobs under the old schema | Clean break — user must cancel any queued/running jobs and re-queue via the new API. Dispatcher adds a guard: entries with empty `Prompt` or empty `BranchSlug` are logged and skipped. |
| Session lost while paused (pod eviction, node reboot, daemon crash) | Dispatcher's resume path fails with `sessions.input` error. Falls back to `sessions.create` with `ResumeClaudeSessionID`. If that also fails, `Status = failed` with an error message. |
| `claude --resume <ClaudeSessionID>` fails | Session-agent's `handleCreate` returns the error; dispatcher marks job `failed` with `Error = "session-resume failed: <underlying>"`. |
| `usage.output_tokens` missing from a stream-json event | QuotaMonitor falls back to byte-count estimate: `outputTokensSinceSoftMark += len(raw) / 4`. Not precise but keeps the mechanism functional. |
| Subprocess hangs after `control_request` interrupt (refuses to end turn) | Not addressed in v1 — the session-agent's existing liveness handling applies. Noted as a follow-up if observed in practice. |

## Security

- Caller-supplied `prompt` is passed verbatim to Claude as a user message. Platform does not interpret, sanitize, or escape it.
- `MCLAUDE_STOP:` is a platform-owned marker prefix. Platform never reads this marker back; only the caller's Stop hook does. A malicious prompt containing the prefix in its body does not confuse the platform.
- `BranchSlug` regex prevents path-traversal branch names.
- Required `permPolicy` + `allowedTools` on POST forces every caller to explicitly declare what tools their unattended session may use.
- `ClaudeSessionID` persistence in `mclaude-job-queue` KV: the ID is not a secret on its own but points to a Claude Code transcript that contains the session's work. Same trust boundary as the rest of `mclaude-job-queue` (user-scoped keys, NATS auth).

## Impact

**Specs updated in this commit:**
- `docs/adr-0009-quota-aware-scheduling.md` — supersession note prepended.
- `docs/spec-state-schema.md` — canonical source for `JobEntry` schema and lifecycle event payloads. Must reflect: new fields (`prompt`, `title`, `branchSlug`, `resumePrompt`, `hostSlug` retained with callout, `softThreshold`, `hardHeadroomTokens`, `permPolicy`, `allowedTools`, `claudeSessionID`, `pausedVia`); removed fields (`specPath`, `threshold`, `prUrl`); updated status enum (no `cancelling`; `needs_spec_fix` retained); new lifecycle event `session_job_cancelled`; updated `session_job_paused` payload (adds `pausedVia`, `r5`, `outputTokensSinceSoftMark`; removes `threshold`); removed `session_quota_interrupted`; removed `session_job_complete.prUrl`.
- `docs/mclaude-session-agent/spec-*.md` — if present, update documented `sessions.create` payload shape (new `resumeClaudeSessionID` request field, new `claudeSessionID` reply field), `QuotaMonitorConfig` (`softThreshold`/`hardHeadroomTokens` replacing `threshold`; `priority` removed), token counting behavior in `onRawOutput`, and the new `MCLAUDE_STOP:` marker as the quota-stop mechanism (replacing `QUOTA_THRESHOLD_REACHED:` text). Research pass during design audit confirmed the exact file is `docs/mclaude-session-agent/spec-session-agent.md` — update that.
- `docs/mclaude-control-plane/spec-*.md` — if documented there, update `mclaude-job-queue` KV schema. The session-agent spec above is the primary owner for `JobEntry` since the daemon lives there; control-plane spec only needs to reflect the bucket name and any ownership notes.

**Components implementing the change:**
- `mclaude-session-agent` — `state.go` (JobEntry + QuotaMonitorConfig struct rewrites — do this first), `daemon.go`, `daemon_jobs.go`, `session.go`, `quota_monitor.go`, `agent.go`, tests.
- `.agent/skills/schedule-feature/` — deleted.
- `.agent/skills/job-queue/` — display updates.
- spec-driven-dev plugin (separate repo) — new `/schedule-feature` skill + Stop hook. Not implemented in this loop.

## Scope

**In scope:**
- Rewrite `JobEntry` and `QuotaMonitorConfig` structs in `mclaude-session-agent/state.go` to match the new shape (removes `SpecPath`, `Threshold`, `PRUrl`, `QuotaMonitorConfig.Priority`, `QuotaMonitorConfig.Threshold`; adds all new fields per Data Model). State.go rewrite must precede any dispatcher/monitor code changes.
- Rename `SpecPath` → `Prompt`/`Title`/`BranchSlug` on `JobEntry`, POST body, validation.
- Replace `Threshold` with `SoftThreshold` + `HardHeadroomTokens`.
- Require `PermPolicy` + `AllowedTools` on POST.
- Add `ResumePrompt`, `ClaudeSessionID`, `PausedVia`, `HostSlug` to `JobEntry`.
- Remove `PRUrl` from `JobEntry`.
- Update all daemon NATS subject + session KV key construction to use host-scoped 4-part helpers (`UserHostProject*`, `SessionsKVKey(u, h, p, s)`) sourcing host slug from `job.HostSlug`.
- Delete `specPathToComponent`, `specPathToSlug`, `scheduledSessionPrompt`.
- Delete `SESSION_JOB_COMPLETE:` scanning from `onRawOutput` (keep callback for token counting).
- Update `dispatchQueuedJob`: new field plumbing, shared-worktree attach, ClaudeSessionID capture.
- Add `resumePausedJob` function + dispatcher integration with `PausedVia`-based nudge selection.
- Add fallback path: dispatcher falls through to `--resume` via `ResumeClaudeSessionID` on session-lost.
- Session-agent accepts `ResumeClaudeSessionID` in `sessions.create`; spawns subprocess with `claude --resume`.
- Session-agent `sessions.create` reply carries `claudeSessionID`.
- Add token counter in QuotaMonitor; hard-interrupt at `HardHeadroomTokens`.
- Collapse old `session_quota_interrupted` + `session_job_paused` into single `session_job_paused` with `pausedVia`.
- Add `session_job_cancelled` lifecycle event.
- Update `runLifecycleSubscriber` event → status mapping.
- Simplify `DELETE /jobs/{id}` to single-verb behavior (no `?force=true`).
- Delete `.agent/skills/schedule-feature/`.
- Update `.agent/skills/job-queue/` display.
- Prepend supersession note to ADR-0009.
- Update `docs/spec-state-schema.md` with new `JobEntry` schema, status enum (no `cancelling`), and new/changed lifecycle event payloads.
- Update `docs/mclaude-session-agent/spec-session-agent.md` with `sessions.create` request/reply shape changes, `QuotaMonitorConfig` changes, `onRawOutput` behavior, and `MCLAUDE_STOP:` marker mechanism.
- Update `docs/mclaude-control-plane/spec-*.md` if they document `mclaude-job-queue` ownership.

**Out of scope:**
- Plugin's `/schedule-feature` skill (lives in plugin repo).
- Plugin's SDD Stop hook (lives in plugin repo).
- Migration for in-flight jobs (manual cancel + re-queue).
- Subprocess liveness probe / stuck-session handling beyond hard interrupt (follow-up).
- BYOM proxy chain for `/jobs` (still deferred, same as ADR-0009).

## Open questions

(None remaining from the planning Q&A. Design audit may surface more.)

## Implementation Plan

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| `daemon_jobs.go` field rename + function deletions | ~130 | 70k | Struct changes; dispatcher rewrites; DELETE simplification. |
| `daemon_jobs.go` resume-path logic (`resumePausedJob`, fallback) | ~80 | 50k | Resume nudge selection; session-lost fallback to `--resume`. |
| `quota_monitor.go` token counter + hard interrupt | ~90 | 60k | New state; usage event parsing; interrupt dispatch. |
| `session.go` / `agent.go` `ResumeClaudeSessionID` plumbing | ~40 | 30k | Session creation with `claude --resume`; capture Claude Code session ID in reply. |
| Lifecycle event consolidation + `session_job_cancelled` | ~50 | 40k | Event type changes; subscriber switch updates. |
| Test updates (`quota_test.go`, `daemon_test.go`, etc.) | ~250 | 140k | Delete ~6 old tests; add ~15 new tests; update ~15. |
| `.agent/skills/schedule-feature/` deletion | ~5 | 5k | `rm` + commit. |
| `.agent/skills/job-queue/` display edits | ~30 | 20k | SKILL.md edits. |
| ADR-0009 supersession note | ~15 | 10k | Mechanical prepend. |
| Spec file updates (session-agent / control-plane) | ~60 | 40k | Reflect field renames + new events + new QuotaMonitor behavior. |

**Total estimated tokens:** ~465k
**Estimated wall-clock:** ~75–90m of a 5h budget (~30%).
