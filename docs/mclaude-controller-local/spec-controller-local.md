# Spec: mclaude-controller-local

The local controller is a long-lived foreground process that runs on a BYOH machine (laptop, desktop, VM). It owns per-project runtime resources for all users who have provisioned projects on that machine.

Runs as `mclaude daemon --host <hslug>` or via a launchd / systemd unit. One process per machine.

## Configuration

| Flag / env | Required | Description |
|------------|----------|-------------|
| `--host` / `HOST_SLUG` | Yes | The local host's slug. |
| `--user-slug` / `USER_SLUG` | Yes | The owning user's slug. |
| `--hub-url` / `HUB_URL` | Yes | Hub NATS WebSocket URL (e.g. `wss://hub.mclaude.example/nats`). |
| `--cp-url` / `CP_URL` | Yes | Control-plane HTTP base URL (e.g. `https://api.mclaude.internal`). Required for host JWT refresh via HTTP challenge-response. |
| `--creds-file` | No | Path to host JWT credentials (default `~/.mclaude/hosts/{hslug}/nats.creds`). The ADR-0054 host-scoped JWT (zero JetStream). |
| `--data-dir` | No | Root for per-project worktrees (default `~/.mclaude/projects/`). Per-project data is stored at `{data-dir}/{uslug}/{pslug}/`. |
| `LOG_LEVEL` | No | Default `info`. |

## NATS Subscriptions

Subscribes via host JWT (scoped to `mclaude.hosts.{hslug}.>` per ADR-0054, zero JetStream):

| Subject | Behavior |
|---------|----------|
| `mclaude.hosts.{HOST_SLUG}.users.{uslug}.projects.{pslug}.create` | Fan-out from CP. Materializes `{data-dir}/{uslug}/{pslug}/worktree/`, clones git URL if provided, starts a session-agent subprocess, registers the agent's NKey public key with CP, and replies success. |
| `mclaude.hosts.{HOST_SLUG}.users.{uslug}.projects.{pslug}.delete` | Fan-out from CP. Stops the session-agent subprocess (SIGINT, 10s grace, SIGKILL), removes `{data-dir}/{uslug}/{pslug}/`. |
| `mclaude.hosts.{HOST_SLUG}.api.agents.register` | Request/reply. Forwards agent NKey public key registration requests to CP. |

The host controller subscribes to `mclaude.hosts.{HOST_SLUG}.>` — a single wildcard capturing all project lifecycle messages for any user with access to this host.

## Process Supervision

For each provisioned project:

- Starts `mclaude-session-agent --mode standalone --user-slug … --host … --project-slug … --data-dir {data-dir}/{uslug}/{pslug}/worktree` as a child process.
- Restarts child on crash with a 2-second delay.
- On controller shutdown (SIGINT / SIGTERM), forwards SIGINT to all children (10s grace per child, then SIGKILL) and waits for all children to exit.

**Credential isolation (ADR-0058):** The host controller never touches the agent's JWT or private key. The agent generates its own NKey pair and authenticates directly with CP via HTTP challenge-response.

## NKey IPC (ADR-0058)

When a per-project agent subprocess starts:

1. Agent generates its own NKey pair at startup (private seed never leaves the agent process).
2. Agent passes its **public key** to the host controller via local IPC (stdout line or file at a well-known path).
3. Host controller reads the public key and registers it with CP.

## Agent Credential Registration (ADR-0058)

After receiving the agent's public key via IPC:

1. Host controller publishes a NATS request to `mclaude.hosts.{HOST_SLUG}.api.agents.register` with payload `{nkey_public, userSlug, hostSlug, projectSlug}`.
2. CP validates host access, project ownership, and host assignment, then stores the agent's public key in `agent_credentials` (`UNIQUE(user_id, host_slug, project_slug)`).
3. On `NOT_FOUND` response (fan-out race condition), controller retries with exponential backoff: 100ms initial, doubling, max 5s interval, max 10 attempts.
4. Once registration succeeds, the agent authenticates directly via HTTP challenge-response to obtain its per-project JWT.

## Host Credential Refresh (ADR-0054/0058)

The host JWT has a 5-minute TTL. The host controller refreshes via HTTP challenge-response using `mclaude.io/common/pkg/hostauth`:

1. Before TTL expiry, sends `POST /api/auth/challenge {nkey_public}` then `POST /api/auth/verify {nkey_public, challenge, signature}`.
2. CP returns a fresh host JWT.
3. Controller reconnects to NATS with the new JWT.
4. On `permissions violation` error, triggers immediate refresh + retry.

The controller is **not involved** in agent credential refresh — each per-project agent manages its own JWT refresh independently.

## Liveness

When the controller connects to hub NATS, hub publishes `$SYS.ACCOUNT.{accountKey}.CONNECT` with `client.kind = "Client"`. Control-plane looks up the host by `public_key` (no type filter), updates `hosts.last_seen_at`, upserts `mclaude-hosts` KV `online=true`. On disconnect, `online=false`. No heartbeat publish.

## Provisioning Request Shape

```json
{
  "userID":      "uuid-v4",
  "userSlug":    "alice-gmail",
  "hostSlug":    "my-laptop",
  "projectID":   "uuid-v4",
  "projectSlug": "billing",
  "gitUrl":      "https://github.com/alice/billing.git",
  "gitIdentityId": "uuid"
}
```

Reply on success: `{ "ok": true, "projectSlug": "billing" }`
Reply on failure: `{ "ok": false, "error": "human-readable", "code": "git_clone_failed | rbac_failed | …" }`

## Error Handling

| Failure | Behavior |
|---------|----------|
| `HOST_SLUG` / `--host` not provided | Fatal exit at startup. |
| Hub NATS unreachable at boot | Retry with backoff. Controller not functional until connected. |
| Git clone fails | Reply `{ok: false, code: "git_clone_failed"}`; control-plane returns 503 to SPA. |
| Agent process crashes | Controller restarts it after 2s delay. |
| Delete for unknown project | Idempotent — reply `{ok: true}`. |

## Dependencies

- Hub NATS reachable from the BYOH machine.
- The host JWT credentials file (default `~/.mclaude/hosts/{hslug}/nats.creds`).
- `git`, `gh`, `glab` binaries (same as session-agent dependencies).
- The `mclaude-session-agent` binary on `$PATH`.
- `mclaude.io/common/pkg/hostauth` for shared NKey challenge-response logic.

## Daemon Mode Deprecation (ADR-0058)

The `mclaude-session-agent --daemon` mode is deprecated and replaced by this architecture. The `mclaude daemon` CLI command now launches `mclaude-controller-local` instead of `mclaude-session-agent --daemon`. The `--daemon` flag in session-agent will be removed in a future release.
