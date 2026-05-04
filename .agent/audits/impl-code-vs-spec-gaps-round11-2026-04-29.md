# Audit: Code vs Spec — Round 11

**Date:** 2026-04-29
**Auditor:** spec-evaluator subagent
**Confidence:** HIGH

## Specs Reviewed (6)

1. `docs/spec-state-schema.md`
2. `docs/mclaude-control-plane/spec-control-plane.md`
3. `docs/mclaude-controller/spec-controller.md`
4. `docs/mclaude-web/spec-web.md`
5. `docs/charts-mclaude/spec-helm.md`
6. `docs/mclaude-session-agent/spec-session-agent.md`

## Code Reviewed

- `mclaude-control-plane/*.go` (main, server, auth, db, hosts, admin, projects, sys_subscriber, nkeys, providers)
- `mclaude-controller-k8s/*.go` (main, reconciler, nats_subscriber, mcproject_types, nkeys)
- `mclaude-session-agent/*.go` (main, agent, session, state, events, quota_monitor, terminal, metrics, worktree, gitcreds, daemon, daemon_jobs)
- `mclaude-common/pkg/subj/subj.go`
- `mclaude-web/src/lib/subj.ts`, `slug.ts`
- `mclaude-web/src/stores/session-store.ts`, `event-store.ts`, `lifecycle-store.ts`, `heartbeat-monitor.ts`, `auth-store.ts`
- `mclaude-web/src/viewmodels/session-list-vm.ts`, `conversation-vm.ts`, `terminal-vm.ts`
- `mclaude-web/src/components/App.tsx`
- `charts/mclaude-cp/values.yaml`, `templates/control-plane-deployment.yaml`
- `charts/mclaude-worker/values.yaml`, `templates/controller-deployment.yaml`, `templates/session-agent-template.yaml`, `templates/clusterrole.yaml`

## New Gaps Found: 1

### GAP 1 — Session-agent NATS connection uses default reconnect limits (MaxReconnects=60) instead of unlimited

**Spec:** spec-session-agent.md § Error Handling: "NATS connection lost | NATS client auto-reconnects; state changes buffered in memory during outage and flushed on reconnect. Claude processes continue running. Reconnection counter incremented."

**Code:** `mclaude-session-agent/main.go` `natsConnect()` helper:
```go
func natsConnect(natsURL, credsFile string) (*nats.Conn, error) {
    opts := []nats.Option{}
    if credsFile != "" {
        opts = append(opts, nats.UserCredentials(credsFile))
    }
    return nats.Connect(natsURL, opts...)
}
```

No `RetryOnFailedConnect(true)` or `MaxReconnects(-1)` options. NATS Go client defaults: `MaxReconnects=60`, `RetryOnFailedConnect=false`.

**Comparison:** Both `mclaude-control-plane/main.go` and `mclaude-controller-k8s/main.go` explicitly set:
```go
nats.RetryOnFailedConnect(true),
nats.MaxReconnects(-1),
```

**Impact:** After 60 failed reconnect attempts, the session-agent permanently loses NATS connectivity. In K8s this may be mitigated by pod restart (liveness probe), but on BYOH machines in daemon mode the process would lose NATS connectivity and never recover — sessions would continue running locally but KV state, lifecycle events, and API commands would all stop flowing.

**Fix:** Add `nats.RetryOnFailedConnect(true)` and `nats.MaxReconnects(-1)` to `natsConnect()` options, matching the control-plane and controller-k8s patterns.

## Borderline Exclusions

1. **Project KV watcher DEL handling (fragile but functional):** `session-store.ts` project watcher does not explicitly check `entry.operation === 'DEL'` — it relies on `JSON.parse` throwing for empty DEL values, falling through to the catch block which extracts the project UUID from the key suffix. This works because (a) DEL entries have empty values that fail to parse, and (b) both the `_projects` map key and the KV key suffix use the same UUID. By contrast, the sessions watcher has an explicit `DEL`/`PURGE` check. Not a functional bug, but fragile code style — deferred as a code-quality item, not a spec gap.

2. **Missing `subj.go` restart helper:** TypeScript `subj.ts` has `subjSessionsRestart()` but Go `subj.go` has no `UserHostProjectAPISessionsRestart()` helper. Not a functional gap — the session-agent constructs the subject via prefix concatenation in `createJetStreamConsumers()` and never calls a standalone helper.

## Verification Summary

All pre-existing flagged items in all 6 specs were verified against code — no stale or incorrect annotations found. The exclusion keyword scan covered: Known bug, Not yet implemented, Not yet published, Dead code, Known gap, Known issue, Known mismatch, Not read by Go code, not yet injected, not yet read, deferred per, not yet migrated, ADR-0034, not yet enforced, not yet wired, phantom, non-functional, h-unknown, ghost sessions, spec target, target design, superseded, not yet computed, Known gaps, not yet created, not yet published.
