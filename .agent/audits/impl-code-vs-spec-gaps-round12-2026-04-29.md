# Audit Round 12: Code vs Spec Gaps

**Date:** 2026-04-29
**Scope:** All 6 spec files vs all production code files
**Confidence:** HIGH — every spec file and every listed code file read in full

## New Unflagged Gaps

### GAP-R12-1: Claude process per-session crash auto-restart not implemented

**Spec:** `spec-session-agent.md` Error Handling table: "Claude process crash (unexpected exit) → Session agent detects exit, publishes `session_failed` lifecycle event, auto-restarts with `--resume {sessionId}`. Increments `mclaude_claude_restarts_total` counter."

**Code:** When a Claude child process exits unexpectedly, `session.go`'s stdout scanner goroutine returns and `s.doneCh` closes. No code in `agent.go` watches individual session `doneCh` channels to trigger restart. The only consumer of `doneCh` is `QuotaMonitor.run()` (quota-monitoring only, not general crash recovery). There is no goroutine that detects unexpected exit and calls `sess.start(claudePath, true, ...)` with `--resume`.

**Impact:** A Claude process crash leaves the session dead in memory. The user must manually restart via `sessions.restart` or wait for the entire pod to restart (K8s restart policy).

**Files:** `mclaude-session-agent/session.go` (line ~60, `doneCh` closed after scanner loop), `mclaude-session-agent/agent.go` (no crash-watcher goroutine).

---

### GAP-R12-2: `session-agent-template` ConfigMap Helm template missing

**Spec:** `spec-state-schema.md` § Kubernetes Resources: "ConfigMap: `{release}-session-agent-template` (in `mclaude-system`) — Static Helm-templated ConfigMap containing session-agent pod spec values: image, imagePullPolicy, terminationGracePeriodSeconds, resourcesJson, projectPvcSize, projectPvcStorageClass, nixPvcSize, nixPvcStorageClass."

`spec-helm.md` § Chart: `mclaude-worker` lists it as a resource: "ConfigMap | `{release}-session-agent-template` | Static pod template values…"

**Code:** `charts/mclaude-worker/templates/` contains only `controller-deployment.yaml` and `mcproject-crd.yaml`. There is **no** `session-agent-template-cm.yaml` template. The `values.yaml` defines `sessionAgent.image.*`, `sessionAgent.resources`, `sessionAgent.terminationGracePeriodSeconds: 86400`, `sessionAgent.persistence.size: 50Gi`, `sessionAgent.nix.size: 20Gi`, `sessionAgent.corporateCA.*` — but these values are never rendered into the ConfigMap.

The controller's `loadTemplate()` in `reconciler.go` falls back to `defaultTemplate()` when the ConfigMap is not found: image `mclaude-session-agent:latest`, PVC sizes 10Gi, grace period 30s, no corporate CA. Helm values are silently ignored.

**Impact:** Session-agent pods get wrong image (`:latest` instead of the versioned tag), undersized PVCs (10Gi instead of 50Gi project / 20Gi nix), short grace period (30s instead of 86400s), and no corporate CA injection — regardless of what the operator sets in `values.yaml`.

**Files:** `charts/mclaude-worker/templates/` (file absent), `mclaude-controller-k8s/reconciler.go` `loadTemplate()`, `charts/mclaude-worker/values.yaml` `sessionAgent.*`.

---

### GAP-R12-3: `mclaude-job-queue` KV key uses UserID (UUID) instead of UserSlug

**Spec:** `spec-state-schema.md` § mclaude-job-queue: "Key format: `{uslug}.{jobId}` (dot-separated; `{jobId}` stays UUID v4)"

**Code:** `daemon_jobs.go` `writeJobEntry()` and `readJobEntry()` construct keys with:
```go
key := subj.JobQueueKVKey(slug.UserSlug(job.UserID), job.ID)
```
`job.UserID` is the user's UUID (set from `d.cfg.UserID` in `handleJobsRoute`). The type cast `slug.UserSlug(job.UserID)` wraps the UUID string as a slug type without converting it. The resulting key is `{UUID}.{jobId}`, not `{uslug}.{jobId}`.

**Impact:** The key format is internally consistent (daemon reads its own writes), so job dispatch works. But the key format deviates from the spec's slug-based convention, and any external consumer expecting slug-based keys would fail.

**Files:** `mclaude-session-agent/daemon_jobs.go` `writeJobEntry()`, `readJobEntry()`, `handleJobsRoute()`.

---

### GAP-R12-4: Daemon session KV lookups use UUID-based key components (extends flagged dispatchQueuedJob bug)

**Spec flags:** "Known bug: `dispatchQueuedJob()` casts `job.ProjectID` (UUID) to `slug.ProjectSlug` for NATS subject construction — dispatched messages use UUID-based subjects."

**Code:** The same UUID→slug casting pattern applies to **KV key lookups** (not just subjects), in three additional locations:

1. `dispatchQueuedJob()` polls session KV with:
   ```go
   kvKey := subj.SessionsKVKey(d.cfg.UserSlug, d.cfg.HostSlug, slug.ProjectSlug(job.ProjectID), slug.SessionSlug(sessionID))
   ```
   `job.ProjectID` (UUID) and `sessionID` (UUID from create response) are cast to slug types. Session-agents write keys with actual slugs.

2. `startupRecovery()` checks session existence with:
   ```go
   kvKey := subj.SessionsKVKey(slug.UserSlug(job.UserID), d.cfg.HostSlug, slug.ProjectSlug(job.ProjectID), slug.SessionSlug(job.SessionID))
   ```
   Uses UUID for all three variable components (user, project, session). None match slug-based keys.

**Impact:** `dispatchQueuedJob` can never confirm the session reached idle (poll always misses), triggering the retry loop. `startupRecovery` for running jobs always concludes the session is gone, resetting to queued.

**Files:** `mclaude-session-agent/daemon_jobs.go` `dispatchQueuedJob()` KV poll, `startupRecovery()` session check.

---

## Borderline Exclusions

| Item | Reason for exclusion |
|------|---------------------|
| `accumulateUsage()` dead code in `state.go` — cache tokens never accumulated | Already flagged as "Known bug" in spec-session-agent.md |
| `clearPendingControlsForResume` sets state to `"idle"` instead of `"restarting"` | Already flagged as "Known bug" in spec-session-agent.md |
| `natsConnect()` uses NATS defaults (MaxReconnects=60) instead of unlimited | Already flagged as "Known bug" in spec-session-agent.md |
| QuotaMonitor subscribes to UUID-based quota subject | Already flagged as "Known bug" in spec-session-agent.md |
| `dispatchQueuedJob` NATS subject uses ProjectID UUID | Already flagged as "Known bug" in spec-state-schema.md |
| `session_quota_interrupted` event name instead of `session_job_paused` | Already flagged as "Known bug" in spec-state-schema.md |
| Login JWT double expiry (16h instead of 8h) | Already flagged as "Known bug" in spec-control-plane.md |
| `/auth/refresh` returns `s.natsURL` instead of `s.natsWsURL` | Already flagged as "Known bug" in spec-control-plane.md |
| `AdminUserRequest` missing `IsAdmin` field | Already flagged as "Known gap" in spec-control-plane.md |
| `subj.go` missing `UserHostProjectAPISessionsRestart` helper | Code consistency issue, not a spec-code gap — restart subject is constructed correctly via prefix manipulation |
| `BOOTSTRAP_ADMIN_EMAIL` injected into Deployment but only read by init-keys Job | Wasted env var, not a behavioral gap — same image used for both Deployment and Job |
