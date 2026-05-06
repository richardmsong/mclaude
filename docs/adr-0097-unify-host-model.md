# ADR: Unify host model — remove type distinction and admin cluster endpoints

**Status**: draft
**Status history**:
- 2026-05-05: draft

## Overview

Remove the `type` column from `hosts` and the `role` column (always `owner`). A host is a host — machine or cluster, it registers the same way (`mclaude.users.{uslug}.hosts._.register`), authenticates the same way (NKey challenge-response), and runs projects the same way. The `POST /admin/clusters` HTTP endpoint and its associated `cluster register`, `cluster grant` CLI commands are removed — they duplicate the NATS registration path with stale fields (`leafUrl`, `jsDomain`) from the old leaf-node architecture.

## Motivation

1. **`type` is cosmetic.** CP never branches on `type` for authorization, scheduling, or behavior. The only consumer is the SPA, which shows a badge (machine/cluster). This distinction adds complexity (separate CLI commands, separate admin endpoints, separate registration flows) for zero functional value.

2. **`POST /admin/clusters` duplicates the NATS path.** Host registration already works via `mclaude.users.{uslug}.hosts._.register`. The HTTP admin endpoint adds a second path with stale fields (`leafUrl`, `jsDomain`, `leafJwt`, `leafSeed`) from the pre-hub-direct architecture. Two paths to do the same thing = two paths to maintain and two paths to have bugs in.

3. **`role` is always `owner`.** Every host row has `role = 'owner'`. The column was designed for shared hosts (grant-based access), but ADR-0091's `host_grants` table handles that at the grant level, not the host row level. `role` on `hosts` is dead.

4. **`--nkey-public` flag missing.** Cluster hosts generate their NKey in a pre-install Job (`gen-host-nkey`). The admin needs to pass the pre-generated public key when registering. The current `host register` CLI command generates a new NKey locally — it doesn't accept a pre-existing one. Adding `--nkey-public` to the existing command is the fix.

5. **`--default-platform` missing from NATS registration.** The `defaultPlatform` flag only existed on `POST /admin/clusters`. It needs to move to the NATS registration payload, with CP enforcing `is_admin` before allowing it.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| `type` column | Drop from `hosts` table via migration (`ALTER TABLE hosts DROP COLUMN type`) | Never used for authorization or behavior branching. SPA badge is not worth the complexity. |
| `role` column | Drop from `hosts` table via migration (`ALTER TABLE hosts DROP COLUMN role`) | Always `owner`. Grant-based access uses `host_grants` table (ADR-0091). |
| `POST /admin/clusters` | Remove endpoint | Duplicates NATS registration with stale fields. |
| `GET /admin/clusters` | Remove endpoint | Lists hosts — use `GET /api/users/{uslug}/hosts` instead. |
| `POST /admin/clusters/{cslug}/grants` | Remove endpoint | Use `host_grants` NATS flow (ADR-0091) instead. |
| `DELETE /admin/clusters/{cslug}` | Remove (fully implemented — deletes hosts by slug + type=cluster) | Use `host rm` CLI / `DELETE /api/users/{uslug}/hosts/{hslug}` instead. |
| `cluster register` CLI | Remove | Use `host register --nkey-public <key>` instead. |
| `cluster grant` CLI | Remove | Use host grants NATS flow instead. |
| `--nkey-public` flag | Add to `host register` CLI | Allows passing a pre-generated NKey (for cluster hosts where the worker chart generated the key). When omitted, CLI generates one locally (existing BYOH behavior). |
| `--default-platform` flag | Add to `host register` CLI + NATS registration payload | CP checks `is_admin` before allowing. At most one default platform globally (partial unique index). |
| `type` in NATS registration payload | Remove from `{name, type, nkey_public}` → `{name, nkey_public}` | No longer stored. |
| SPA host badge | Remove machine/cluster badge | Show host name only. If a visual distinction is needed later, derive it from capabilities, not a static type field. |
| Project routing | Always route through user-agent path for all hosts | Unify on `mclaude.users.{uslug}.hosts.{hslug}.agent.projects.provision` — no type-based branching. BYOH daemon implements the user-agent protocol (subscribes to the same subjects as the k8s user-agent). One path, one subscriber pattern. |

## User Flow

### Register a cluster host (admin)

```bash
# Admin logs in
mclaude login --server https://mclaude.example.com

# Get the NKey public key from the worker controller logs
kubectl logs -n mclaude-worker -l app.kubernetes.io/component=controller | grep pubkey

# Register with CP via NATS
mclaude host register --name "AKS Canada Central" --nkey-public UAKAK6UO7ZAJ... --default-platform
```

### Register a BYOH machine (any user)

```bash
# User logs in
mclaude login --server https://mclaude.example.com

# Register — generates NKey locally (no --nkey-public flag)
mclaude host register --name "My Laptop"

# Start daemon
mclaude daemon
```

Same `host register` command, same NATS subject, same CP handler. The only difference is whether the NKey is pre-generated (cluster) or generated on the spot (BYOH).

## Component Changes

### `mclaude-control-plane`

**Remove admin cluster endpoints:**
- `admin.go`: remove `handleAdminClusters`, `handleAdminClusterGrants`, and all cluster-related routes from `handleAdminRoutes`.
- Remove `POST /admin/clusters`, `GET /admin/clusters`, `POST /admin/clusters/{cslug}/grants`, `DELETE /admin/clusters/{cslug}` handlers.

**Update NATS registration handler:**
- `lifecycle.go` (or equivalent after ADR-0096 restructure): the `mclaude.users.*.hosts._.register` handler currently expects `{name, type, nkey_public}`. Change to `{name, nkey_public, defaultPlatform?}`.
- `NATSHostRegisterRequest` struct: add `DefaultPlatform bool` field, remove `Type` field.
- When `defaultPlatform` is true, check `is_admin` on the registering user. If not admin, return `{error: "admin_required"}`. Set `hosts.is_default_platform=true` on INSERT. Enforce partial unique index (at most one default platform — collisions return `{error: "default_platform_exists"}`).
- Setting `defaultPlatform` does NOT auto-grant existing users. Auto-grant is invoked per-user at JWT issuance time (`issueJWTForNKey` queries for the default platform) — existing behavior per ADR-0091.

**Schema changes (DDL block + migration):**
- In the `CREATE TABLE IF NOT EXISTS hosts` DDL: remove `type TEXT NOT NULL CHECK (type IN ('machine', 'cluster'))` column and `role TEXT NOT NULL DEFAULT 'owner'` column entirely.
- Add migration step after DDL: `ALTER TABLE hosts DROP COLUMN IF EXISTS type; ALTER TABLE hosts DROP COLUMN IF EXISTS role;` (for existing installations where the table already has these columns).

**Remove type/role from all queries (exhaustive list):**
- All INSERT INTO hosts: remove `type` and `role` from column lists and VALUES (lifecycle.go, hosts.go, admin.go — every call site).
- `GetHostBySlug`: remove `type` and `role` from SELECT, remove `AND role = 'owner'` filter.
- `GetHostsByUser` / `ListHosts`: remove `type` and `role` from SELECT.
- `Host` struct (db types): remove `Type` and `Role` fields.
- `HostKVState` struct (sys_subscriber.go): remove `Type` field. Existing KV entries with `type` are harmless — Go's JSON unmarshalling ignores unknown fields.
- All HTTP/NATS response payloads that include `type` or `role`: remove these fields.

**Remove admin cluster handlers:**
- `handleAdminClusters` (POST/GET), `handleAdminClusterGrants` (POST), admin cluster DELETE — all fully implemented code (not stubs). Remove handlers and routes.

**Unify project routing — remove type-based branching:**
- `projects.go`: remove `host.Type == "cluster"` branch. Always route provisioning to the user-agent path (`mclaude.users.{uslug}.hosts.{hslug}.agent.projects.provision`). Remove the old direct host-controller path (`mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.create`).
- `projects.go` (deletion): same — always route through user-agent, remove type branch.
- `admin.go`, `hosts.go`: remove `h.Type` from `publishProjectsDeleteToHost` calls. Use the user-agent path unconditionally.

**`sys_subscriber.go` — update `$SYS CONNECT` handler:**
- Remove `h.type` from the SQL query. Remove `Type` field from `HostKVState` struct. Existing KV entries with a `type` field are ignored by Go's JSON unmarshalling.

### `mclaude-cli`

**Remove cluster commands:**
- Remove `cluster register`, `cluster grant` subcommands entirely.

**Update `host register`:**
- Add `--nkey-public <key>` flag. When set, use this key instead of generating a new one. No local `nkey.seed` file is written (the seed lives in the k8s Secret on the worker cluster). No `nats.creds` file is written. A `config.json` with `hubUrl` IS written to `~/.mclaude/hosts/{hslug}/` so `host list` can display it. The `active-host` symlink is NOT set (the cluster isn't a local host to connect to). The CLI only needs the public key to tell CP "register this NKey."
- Add `--default-platform` flag. Included in the NATS registration payload.
- Remove `--type` flag if present.

### `mclaude-web` (SPA)

- Remove machine/cluster type badge from host list.
- Remove any `type` field references from host display components.
- Remove any admin cluster management UI if present.

### `mclaude-controller-k8s`

- No changes needed. The controller doesn't reference `type` or `role` — it authenticates via NKey and receives its host slug from the JWT.

### `mclaude-cli` (daemon)

- The BYOH daemon (`mclaude daemon`) must subscribe to the user-agent provisioning subject (`mclaude.users.{uslug}.hosts.{hslug}.agent.projects.provision`) instead of the old direct host-controller subject. This makes it behave identically to the k8s user-agent from CP's perspective — same subject, same payload, same response contract. The daemon already handles project provisioning; only the NATS subject changes.

## Data Model

**Removed columns:**
- `hosts.type` (TEXT) — was `machine` or `cluster`
- `hosts.role` (TEXT) — was always `owner`

**Added to NATS registration payload:**
- `defaultPlatform` (bool, optional, default false) — CP enforces admin-only

## Error Handling

- **Non-admin sets `defaultPlatform`:** NATS reply `{error: "admin_required", message: "Only admins can set default platform"}`.
- **Second `defaultPlatform` registration:** NATS reply `{error: "default_platform_exists"}` (existing partial unique index).
- **`--nkey-public` with invalid key format:** CLI validates NKey format before publishing. Error: `"invalid NKey public key format — expected 56-char base32 string starting with U or N"`.

## Security

No security impact. Admin-only `defaultPlatform` enforcement moves from an HTTP endpoint (which checked `isAdminMiddleware`) to the NATS handler (which checks `is_admin` on the user identified by the subject's `{uslug}`). Same authorization, different transport.

## Impact

- `docs/mclaude-control-plane/spec-control-plane.md`: remove admin cluster endpoints section; update NATS registration payload; remove `type`/`role` from host model description.
- `docs/spec-state-schema.md`: remove `type` and `role` columns from `hosts` table schema; remove `type` from `mclaude-hosts` KV `HostKVState` value description; remove `type` from NATS registration payload `{name, type, nkey_public}` → `{name, nkey_public, defaultPlatform?}`; remove `type`/`role` from login response target fields; remove `type?` from `manage.update` payload.
- `docs/mclaude-web/spec-web.md`: remove host type badge references.
- `docs/charts-mclaude/spec-helm.md`: remove any `type` references in worker chart description.

## Scope

**In v1:**
- Drop `type` and `role` columns
- Remove all admin cluster endpoints
- Remove `cluster register` and `cluster grant` CLI commands
- Add `--nkey-public` and `--default-platform` to `host register`
- Update NATS registration handler
- Remove SPA type badge

**Deferred:**
- Host capabilities/tags (if visual distinction is ever needed, derive from runtime capabilities, not a static field)
- Host groups/pools for scheduling

## Open questions

_(none — all resolved)_

## Integration Test Cases

| Test case | What it verifies | Setup/teardown | Components exercised |
|-----------|------------------|----------------|----------------------|
| Register host with pre-generated NKey | `host register --nkey-public <key>` creates host row with that public key; controller challenge-response succeeds | Generate NKey, register, verify DB row + challenge | mclaude-cli, mclaude-control-plane |
| Register host with default-platform via NATS (admin) | Publish to `mclaude.users.{uslug}.hosts._.register` with `{name, nkey_public, defaultPlatform: true}` as admin. Verify `is_default_platform=true` in DB. Second registration with `defaultPlatform: true` returns `{error: "default_platform_exists"}`. | Admin user, NATS publish | mclaude-control-plane |
| Register host with default-platform via NATS (non-admin) | Same NATS publish as non-admin user. Returns `{error: "admin_required"}`. | Non-admin user | mclaude-control-plane |
| Register host with default-platform via CLI (admin) | `host register --default-platform --nkey-public <key>` sets `is_default_platform=true`; second call returns `default_platform_exists` | Admin user, CLI | mclaude-cli, mclaude-control-plane |
| Migration drops type/role | After migration, `SELECT type FROM hosts` fails with column-not-found | Pre-seed hosts with type/role, run migration | mclaude-control-plane |
| Admin cluster endpoints removed | `POST /admin/clusters` returns 404 | Admin JWT, curl endpoint | mclaude-control-plane |

## Implementation Plan

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| `mclaude-control-plane` (remove admin clusters + migration + update registration) | ~-200 / +30 lines | 80k | Delete admin cluster handlers, update NATS handler, migration |
| `mclaude-cli` (remove cluster commands + update host register) | ~-80 / +20 lines | 40k | Delete cluster subcommands, add flags |
| `mclaude-web` (remove type badge) | ~-20 lines | 20k | Remove badge component |

**Total estimated tokens:** 140k
**Estimated wall-clock:** 2-3h
