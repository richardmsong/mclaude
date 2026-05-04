# Audit Round 7: Code vs Spec Gaps

**Date:** 2026-04-29
**Auditor:** Subagent (worker)
**Scope:** All 6 spec files × all production code files listed in task

## Summary

Found **11 unflagged gaps** between production code and spec files. All previously-flagged items (Known bug, Not yet implemented, Dead code, etc.) were excluded.

---

## GAP 1: `POST /admin/users` missing optional `isAdmin` field

- **Code:** `mclaude-control-plane/admin.go` — `AdminUserRequest` struct (line ~30)
- **Spec:** `spec-control-plane.md` — Admin endpoints table: "Creates a user (id, email, name, optional password, optional `isAdmin`)"
- **Detail:** The `AdminUserRequest` struct has `ID`, `Email`, `Name`, `Password` but no `IsAdmin` field. The spec says the admin create-user endpoint accepts an optional `isAdmin` boolean to set `users.is_admin` directly at creation time. The current code requires a separate `POST /admin/users/{uslug}/promote` call (which itself is flagged as not yet wired), meaning there is no working path to create an admin user via the admin API except the bootstrap flow.

## GAP 2: `DELETE /admin/users/{id}` — missing NATS JWT revocation and controller notification

- **Code:** `mclaude-control-plane/admin.go` — `adminDeleteUser()` (line ~140)
- **Spec:** `spec-control-plane.md` — Admin endpoints table: "Deletes a user: revokes NATS JWT, DELETEs from Postgres (cascades), publishes NATS delete requests to controllers."
- **Detail:** The code only calls `db.DeleteUser(ctx, userID)`. It does not revoke the user's NATS JWT (the deleted user's JWT remains valid until expiry) and does not publish NATS delete requests to controllers (`mclaude.users.{uslug}.hosts.{hslug}.api.projects.delete`). Orphaned K8s resources (namespace, PVCs, deployments) for the deleted user's projects will persist until manually cleaned up.

## GAP 3: `POST /admin/sessions/stop` references nonexistent `sessions` table

- **Code:** `mclaude-control-plane/admin.go` — `adminStopSession()` (line ~155)
- **Spec:** `spec-state-schema.md` — Postgres section lists only `users`, `hosts`, `projects`, `oauth_connections` tables
- **Detail:** The handler executes `UPDATE sessions SET status = 'stopped', stopped_at = NOW() WHERE ...` against a `sessions` table that does not exist in the schema DDL (`db.go` `schema` constant). The query silently fails (returns an error that is caught and a warning response is returned), making this endpoint effectively non-functional. Session state lives in NATS KV (`mclaude-sessions`), not Postgres.

## GAP 4: `operator-keys` Secret has undocumented keys

- **Code:** `mclaude-control-plane/init_keys.go` — `runInitKeys()` Secret data map (line ~85)
- **Spec:** `spec-state-schema.md` — "Secret: mclaude-system/operator-keys" section
- **Detail:** The code writes 6 keys: `operatorJwt`, `operatorSeed`, `accountJwt`, `accountSeed`, `accountPublicKey`, `resolverPreload`. The spec documents only 4: `operatorJwt`, `operatorSeed`, `accountJwt`, `accountSeed`. The additional `accountPublicKey` (account NKey public key) and `resolverPreload` (formatted resolver_preload config for NATS) are used by the NATS config templates and the Helm chart but are not documented in the state schema spec.

## GAP 5: `IssueUserJWT` double-expiry — JWT lives 2× configured lifetime

- **Code:** `mclaude-control-plane/auth.go` — `handleLogin()` and `handleRefresh()` (lines ~100, ~140)
- **Spec:** `spec-control-plane.md` — "JWT lifetime is JWT_EXPIRY_SECONDS (default 8h)"
- **Detail:** Both handlers compute:
  ```go
  expirySecs := int64(s.jwtExpiry.Seconds())        // 28800
  expiresAt := time.Now().Add(s.jwtExpiry).Unix()    // Unix timestamp = now + 8h
  jwt, seed, err := IssueUserJWT(..., expiresAt+expirySecs)  // now + 16h!
  ```
  `IssueUserJWT` sets `claims.Expires = expirySecs` which is an **absolute Unix timestamp** in the NATS JWT library, not a relative duration. The result is `claims.Expires = (now + 8h) + 28800s = now + 16h`. The JWT actually expires at **twice** the configured lifetime. `LoginResponse.ExpiresAt` is set correctly (`expiresAt = now + 8h`) so the SPA refreshes at the right time, but the JWT itself is valid for 16h — twice what the spec documents.

## GAP 6: `handleSideEffect` does not accumulate cache token usage

- **Code:** `mclaude-session-agent/session.go` — `handleSideEffect()` EventTypeResult case (line ~340)
- **Spec:** `spec-state-schema.md` — SessionState `usage` object: `{inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, costUsd}`
- **Detail:** The EventTypeResult handler manually adds only 3 of 5 usage fields:
  ```go
  s.state.Usage.InputTokens += r.Usage.InputTokens
  s.state.Usage.OutputTokens += r.Usage.OutputTokens
  s.state.Usage.CostUSD += r.TotalCostUSD
  ```
  It does NOT accumulate `CacheReadTokens` (from `r.Usage.CacheReadInputTokens`) or `CacheWriteTokens` (from `r.Usage.CacheCreationInputTokens`). The `accumulateUsage()` helper in `state.go` correctly includes all 5 fields but is **never called** — it is dead code. The SPA will always show `cacheReadTokens: 0` and `cacheWriteTokens: 0` for every session.

## GAP 7: `clearPendingControlsForResume` sets state "idle" instead of "restarting"

- **Code:** `mclaude-session-agent/state.go` — `clearPendingControlsForResume()` (line ~95)
- **Spec:** `spec-session-agent.md` — "Resumption (on pod restart): 2. Set all matching session KV entries to state: 'restarting'"
- **Detail:** The function sets `st.State = StateIdle` ("idle"), but the spec says sessions should be set to `"restarting"` state during recovery. The SPA's `STATE_LABELS` map includes a `restarting` label, and the spec explicitly defines this as a transient state visible to users during pod restart. By writing `"idle"` instead, the SPA never shows the "Restarting..." state indicator during recovery — sessions jump directly from their old state to "idle" or "running".

## GAP 8: `start()` does not enforce 30-second startup timeout

- **Code:** `mclaude-session-agent/session.go` — `start()` method (line ~130)
- **Spec:** `spec-session-agent.md` — "Resumption: 8. Sessions that fail to start within 30s: mark state: 'failed', publish session_failed lifecycle event."
- **Detail:** The code spawns a background goroutine that monitors `initCh`, `doneCh`, and a 30s timer, but on timeout it does **nothing** (no state change, no lifecycle event, no process kill):
  ```go
  case <-time.After(startupTimeout):
      // No init after timeout — likely the user never sent a message.
      // Don't kill the process; it's idle but valid.
  ```
  The spec says sessions that fail to start within 30s during resumption should be marked `state: "failed"` with a `session_failed` lifecycle event. The code silently ignores the timeout, leaving the session in its current state indefinitely. Note: for initial creation (non-resume), the spec doesn't require this timeout, but for resume it's explicitly required.

## GAP 9: Session state constants incomplete — only 4 of 9 defined

- **Code:** `mclaude-session-agent/events.go` — State constants (line ~25)
- **Spec:** `spec-state-schema.md` — SessionState.state union: `"idle | running | requires_action | updating | restarting | failed | plan_mode | waiting_for_input | unknown"`
- **Detail:** The code defines only 4 constants: `StateIdle`, `StateRunning`, `StateRequiresAction`, `StateUpdating`. Missing constants: `"restarting"`, `"failed"`, `"plan_mode"`, `"waiting_for_input"`, `"unknown"`. While the `handleSideEffect` handler accepts any string from Claude's `session_state_changed` events (so the missing states work at runtime), the absence of constants means:
  - The graceful shutdown drain predicate only checks `StateIdle` and `StateRequiresAction` — it cannot specifically identify `"restarting"`, `"failed"`, `"plan_mode"`, or `"waiting_for_input"` sessions for special handling.
  - Code that writes state (e.g. `clearPendingControlsForResume`) cannot use a `StateRestarting` constant and defaults to `StateIdle` (see GAP 7).

## GAP 10: `METRICS_PORT` env var injected by Helm but not consumed by Go code

- **Code:** `charts/mclaude-cp/templates/control-plane-deployment.yaml` — env section (line ~75)
- **Spec:** `spec-control-plane.md` — Configuration table
- **Detail:** The Helm template injects `METRICS_PORT` as an environment variable:
  ```yaml
  - name: METRICS_PORT
    value: {{ .Values.controlPlane.service.metricsPort | quote }}
  ```
  The Go code does not read `METRICS_PORT` — `/metrics` is served on the admin port (`ADMIN_PORT`, default 9091). The spec's env var table does not list `METRICS_PORT` at all. The Helm template also declares a `metrics` container port (9091) that has no listener (the spec's `metricsPort` "Known issue" flag covers the port, but not the injected env var).

## GAP 11: `gen-leaf-creds` JWT has no explicit permissions

- **Code:** `mclaude-control-plane/gen_leaf_creds.go` — `runGenLeafCreds()` (line ~55)
- **Spec:** `spec-state-schema.md` — "Per-cluster leaf / controller JWT permissions" section
- **Detail:** The `gen-leaf-creds` subcommand creates a NATS user JWT with no pub/sub permissions:
  ```go
  claims := natsjwt.NewUserClaims(userPub)
  claims.Name = "leaf-node"
  claims.IssuerAccount, _ = accountKP.PublicKey()
  // No claims.Permissions set — unrestricted within account
  ```
  The spec states: "Per-cluster leaf / controller JWT permissions... doubles as the worker NATS leaf-node JWT and the cluster controller's NATS auth" with specific scoped pub/sub rules (`mclaude.users.*.hosts.{cluster-slug}.>, _INBOX.>, $JS.*.API.>, $SYS.ACCOUNT.*.CONNECT, $SYS.ACCOUNT.*.DISCONNECT`). The `adminRegisterCluster` handler in `admin.go` does issue a properly-scoped JWT via `IssueHostJWT("*", req.Slug, s.accountKP)`, but the pre-install Job credential (used by the worker NATS StatefulSet's leaf-node config) is unrestricted. These are separate credentials despite the spec describing them as one.

---

## Borderline items considered but excluded

1. **`subjProjectsKVKey` in `subj.go`** constructs `{uslug}.{hslug}.{pslug}` but code uses `{userId}.{projectId}` — the spec already documents this as "deferred per ADR-0050".
2. **`accumulateUsage()` function** in state.go — dead code (never called), but the gap is covered by GAP 6 above.
3. **`kvKeyProjectsForUser` TypeScript typed as `UserSlug`** but the spec says the SPA should pass `userId` (UUID) for project KV watching — this is a type annotation discrepancy, not a behavioral gap (the function is called with the right value at runtime per the spec's App.tsx description).
4. **Session-agent `HOSTNAME` and `MACHINE_ID` dead code** — already flagged in spec as "Dead code".
