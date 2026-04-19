# BUG-004: "Agent down: mclaude -- heartbeat stale" despite running pod

**Severity**: Medium — misleading UI state, user thinks system is broken  
**Component**: spa (HeartbeatMonitor), session-agent (heartbeat publishing)  
**Reported**: 2026-04-16  

## Symptoms

- SPA dashboard shows red banner: "Agent down: mclaude -- heartbeat stale"
- Session-agent pod for the dev-seed project IS running (1/1 Ready)
- Session-agent logs confirm it started and created a session successfully
- User sees "agent down" even though sessions work

## Root Cause

Needs investigation. Possible causes:

1. **KV key mismatch**: Session-agent publishes heartbeat to `mclaude-heartbeats` KV with one key format, but HeartbeatMonitor in the SPA watches for a different key pattern. The spec says bucket is `mclaude-heartbeats` but code may use `mclaude-laptops` (spec audit noted: "Spec names bucket mclaude-locations; code uses mclaude-laptops").

2. **Missing TTL**: Spec says heartbeat KV entries should have 90s TTL (`plan-k8s-integration.md:199`). The spec audit flagged this as a GAP: "runHeartbeat writes to mclaude-heartbeats every 30s but no TTL is configured on the bucket or per-entry." If no TTL, old entries persist and new ones may not trigger KV watch events.

3. **Heartbeat source confusion**: The heartbeat may come from the laptop daemon (not the pod). If the daemon isn't running (no daemon process found on host), there's no heartbeat publisher. The pod's session-agent publishes session-level heartbeats, but the "Agent down" banner may be checking project-level or host-level heartbeats from the daemon.

4. **HeartbeatMonitor threshold**: Default threshold is 60s (`heartbeat-monitor.ts:21`). If the session-agent writes heartbeats every 30s but the first write is delayed (e.g., waiting for NATS connect), the monitor may immediately show stale.

## Evidence

- Pod running: `project-5bb60ebe-...-jhpch   1/1     Running`
- Pod logs: `session agent started`, `session created`
- SPA shows: "Agent down: mclaude -- heartbeat stale"
- Spec audit GAP: heartbeat KV has no TTL configured
- Spec audit PARTIAL: bucket name mismatch (mclaude-locations vs mclaude-laptops)

## Investigation Steps

1. Check what key the session-agent writes to `mclaude-heartbeats` KV
2. Check what key the SPA's HeartbeatMonitor watches
3. Verify the heartbeat KV bucket exists and has entries (via NATS CLI or control-plane admin)
4. Check if heartbeats come from the daemon (laptop) or the pod — the daemon may not be running

## Files

- `mclaude-session-agent/agent.go` — `runHeartbeat()` function
- `mclaude-session-agent/daemon.go:177-189` — laptop heartbeat (daemon mode)
- `mclaude-web/src/stores/heartbeat-monitor.ts` — SPA heartbeat watcher
- `docs/adr-2026-04-10-k8s-integration.md:199` — spec: 90s TTL on heartbeat entries
