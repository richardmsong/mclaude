## Run: 2026-04-14T00:00:00Z

GAP: "All session-scoped subjects include a `{location}` segment" / "mclaude.{userId}.{location}.{projectId}.api.sessions.create" → Code omits the `{location}` segment entirely. All API subscription subjects in `agent.go:248–249` use `mclaude.{userId}.{projectId}.api.sessions.*` and `mclaude.{userId}.{projectId}.api.terminal.*` (two-segment, no location). Spec requires three segments: userId, location, projectId.

GAP: "mclaude.{userId}.{location}.{projectId}.events.{sessionId}" → `session.go:193` constructs event subject as `mclaude.%s.%s.events.%s` (userId, projectId, sessionId) — missing the `{location}` segment.

GAP: "mclaude.{userId}.{location}.{projectId}.lifecycle.{sessionId}" → `agent.go:710,723` constructs lifecycle subjects as `mclaude.%s.%s.lifecycle.%s` (userId, projectId, sessionId) — missing the `{location}` segment.

GAP: "mclaude.{userId}.{location}.{projectId}.terminal.{termId}.output / .input" → `terminal.go:69–70` constructs terminal subjects as `mclaude.%s.%s.terminal.%s.{output,input}` (userId, projectId, termId) — missing the `{location}` segment.

GAP: "Stream `MCLAUDE_EVENTS` captures all `mclaude.*.*.*.events.*` subjects" → `agent.go:85` registers `MCLAUDE_EVENTS` with subjects `["mclaude.*.*.events.*"]` (3 wildcards, not 4 as the spec requires for the location segment). This means events published with a location segment would not be captured by the stream.

GAP: "Stream `MCLAUDE_LIFECYCLE` captures all `mclaude.*.*.*.lifecycle.*` subjects. Retained 30 days." → `agent.go` does not create or ensure the `MCLAUDE_LIFECYCLE` stream at all (only `MCLAUDE_EVENTS` is created with `CreateOrUpdateStream`). Lifecycle events are published via core NATS publish only and are not retained in JetStream.

GAP: "KV bucket: mclaude-sessions / key: {userId}/{location}/{projectId}/{sessionId}" → `state.go:62–64` generates key as `{userId}.{projectId}.{sessionId}` (dots, no location). Spec requires slash-separated `{userId}/{location}/{projectId}/{sessionId}`.

GAP: "KV bucket: mclaude-heartbeats / key: {userId}/{location}/{projectId}" → `state.go:67–69` generates heartbeat key as `{userId}.{projectId}` (dots, no location). Spec requires slash-separated `{userId}/{location}/{projectId}`.

GAP: "KV bucket: mclaude-locations / key: {userId}/{location} → {\"type\": \"k8s\"|\"laptop\", \"machineId\": \"...\", \"ts\": \"...\"}" → Code uses a bucket named `mclaude-laptops` (`daemon.go:76,78`) instead of `mclaude-locations` as specified. The bucket name is wrong.

GAP: "Stored in the `mclaude-locations` KV bucket as `{userId}/{location}` → {...}" → `daemon.go:119` generates the KV key as `{userId}.{hostname}` (dot-separated) instead of the spec's slash-separated `{userId}/{location}`.

GAP: "launcher exits with an error: `location \"{location}\" is already registered to another machine — set a unique name with: mclaude config location <name>`" → `daemon.go:131–134` error message says `hostname %q is already registered to another machine ... set a unique hostname with: mclaude config hostname <name>` — uses "hostname" instead of "location" throughout, and the suggested command is `mclaude config hostname <name>` instead of `mclaude config location <name>`.

GAP: "On startup, reads NATS KV for existing sessions → relaunches with `--resume`" / "1. Read NATS KV for all sessions with this projectId" → `agent.go:136–201` recovery iterates via `WatchAll` but the KV key format used at write time (`{userId}.{projectId}.{sessionId}`) differs from the spec's slash-based format; recovery would only succeed if both read and write use the same format. Additionally, recovery does not filter by location (no location field in session state struct or KV key), which would cause incorrect recovery in multi-location deployments.

GAP: "New Permission Policy: `strict-allowlist`" / "`PermissionPolicyStrictAllowlist` — like `PermissionPolicyAllowlist` but auto-denies tools not in the allowlist instead of forwarding to the client" (plan-quota-aware-scheduling.md) → `state.go` defines only three permission policies: `managed`, `auto`, `allowlist`. `PermissionPolicyStrictAllowlist` is not defined and the auto-deny path is not implemented in `session.go:handleSideEffect`.

GAP: "New callbacks on `Session` struct: `onStrictDeny func(toolName string)` / `onRawOutput func(evType string, raw []byte)`" (plan-quota-aware-scheduling.md) → Neither `onStrictDeny` nor `onRawOutput` field exists on the `Session` struct in `session.go`.

GAP: "`QuotaMonitor` Goroutine (new file: `quota_monitor.go`)" (plan-quota-aware-scheduling.md) → The file `quota_monitor.go` does not exist in `mclaude-session-agent/`.

GAP: "New fields on `Daemon` struct: `sessKV`, `jobQueueKV`, `projectsKV`" (plan-quota-aware-scheduling.md) → `daemon.go` `Daemon` struct has only `laptopsKV jetstream.KeyValue`. The `sessKV`, `jobQueueKV`, and `projectsKV` fields are absent.

GAP: "New field in `DaemonConfig`: `CredentialsPath string`" (plan-quota-aware-scheduling.md) → `DaemonConfig` struct in `daemon.go` does not have a `CredentialsPath` field.

GAP: "`runQuotaPublisher(ctx context.Context)`" (plan-quota-aware-scheduling.md) → No `runQuotaPublisher` function exists in `daemon.go`. The daemon does not publish quota status to `mclaude.{userId}.quota`.

GAP: "`runLifecycleSubscriber(ctx context.Context)`" (plan-quota-aware-scheduling.md) → No `runLifecycleSubscriber` function exists in `daemon.go`. The daemon does not subscribe to lifecycle events or update `jobQueueKV`.

GAP: "`runJobDispatcher(ctx context.Context)`" (plan-quota-aware-scheduling.md) → No `runJobDispatcher` function exists in `daemon.go`. The daemon does not implement the job queue dispatch logic.

GAP: "Extended `sessions.create` Payload: `PermPolicy`, `AllowedTools`, `QuotaMonitor *QuotaMonitorConfig`" (plan-quota-aware-scheduling.md) → `agent.go:handleCreate` request struct does not include `permPolicy`, `allowedTools`, or `quotaMonitor` fields. These fields are not parsed or acted on.

GAP: "`a.publishLifecycleExtra(sessionID, eventType string, extra map[string]string)`" (plan-quota-aware-scheduling.md) → This method does not exist on `Agent` in `agent.go`.

GAP: "`a.publishPermDenied(sessionID, toolName, jobID string)`" (plan-quota-aware-scheduling.md) → This method does not exist on `Agent` in `agent.go`.

GAP: "Entrypoint changes — filesystem-first repo detection" (plan-scratch-to-git.md) → `entrypoint.sh` uses a bare repo at `/data/repo/` (checking `/data/repo/HEAD`) and the spec's new model uses a regular repo at `/data/` (checking `/data/.git/`). The entrypoint does not read `GIT_URL` from `/etc/mclaude/config/GIT_URL` (ConfigMap mount) — it only uses the `GIT_URL` env var directly.

GAP: "Session create changes — worktree logic based on filesystem: if /data/.git/ exists: create worktree: git -C /data worktree add /data/worktrees/{branchSlug} -b {branch} ... else: cwd = /data/" (plan-scratch-to-git.md) → `agent.go:handleCreate` always runs `git -C /data/repo worktree add /data/worktrees/{branchSlug} {branch}` using the bare repo at `/data/repo`, not the regular repo at `/data/`. No scratch-project path (`cwd = /data/`) is implemented.

GAP: "`/git-init` skill support — the session running the git-init skill operates on `/data/` directly" (plan-scratch-to-git.md) → No `/git-init` skill or entrypoint support for this path exists in the session-agent codebase.

GAP: "mclaude-heartbeats: TTL 90s on the KV entry itself (NATS KV native TTL). Expires automatically if agent stops writing." → `agent.go` creates the heartbeat KV bucket via `js.KeyValue(ctx, kvBucketHeartbeats)` (fail-fast if absent, no creation), and writes heartbeats with `hbKV.Put(ctx, key, val)` without setting any TTL on the entry. The spec requires the KV entry itself to have a 90s TTL.

GAP: "Session agent publishes all stdout events to NATS unfiltered — clients decide what to render" / `case "clear", "compact_boundary": updateReplayFromSeq(line, jetStreamSeq)` → `session.go:handleSideEffect` handles `compact_boundary` but does not handle the `clear` event type. The `updateReplayFromSeq` should also be called on `clear` events per the spec's core loop, but no `clear` event handling updates `replayFromSeq`.
