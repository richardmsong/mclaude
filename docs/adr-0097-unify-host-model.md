# ADR: Unify host model — remove type distinction and admin cluster endpoints

**Status**: draft
**Status history**:
- 2026-05-05: draft

## Overview

Remove the `type` column from `hosts` and the `role` column (always `owner`). A host is a host — it registers the same way (`mclaude.users.{uslug}.hosts._.register`), authenticates the same way (NKey challenge-response), and runs projects the same way. The `POST /admin/clusters` HTTP endpoint and its associated `cluster register`, `cluster grant` CLI commands are removed — they duplicate the NATS registration path with stale fields (`leafUrl`, `jsDomain`) from the old leaf-node architecture. The BYOH daemon (`mclaude daemon`) is dead code and removed from scope — all hosts are cluster hosts running the k8s user-agent.

## Motivation

1. **`type` is dead.** CP uses `host.Type == "cluster"` for project provisioning routing, but BYOH is dead — all hosts run the k8s user-agent. The routing branch is removed; all provisioning goes through the user-agent subject unconditionally.

2. **`POST /admin/clusters` duplicates the NATS path.** Host registration already works via `mclaude.users.{uslug}.hosts._.register`. The HTTP admin endpoint adds a second path with stale fields from the pre-hub-direct architecture.

3. **`role` is always `owner`.** Grant-based access uses `host_grants` table (ADR-0091), not a column on `hosts`.

4. **CLI never generates host NKeys.** Hosts generate their own NKeys (worker chart's `gen-host-nkey` Job). The CLI only passes the public key via `--nkey-public`. There is no "generate locally" path.

5. **`--default-platform` missing from NATS registration.** Needs to move from the removed admin endpoint to the NATS handler.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| `type` column | Drop from DDL + migration | Dead — BYOH removed, all hosts are k8s user-agent hosts. |
| `role` column | Drop from DDL + migration | Always `owner`. Grants use `host_grants` table. |
| BYOH daemon | Remove `mclaude daemon` subcommand and all BYOH code paths | Dead code. All hosts are k8s clusters with user-agents. Will redesign if needed later. |
| Project routing | Always user-agent: `mclaude.users.{uslug}.hosts.{hslug}.agent.projects.provision` | No type-based branching. Single routing path. |
| `POST /admin/clusters` | Remove all 4 admin cluster endpoints | Duplicates NATS registration. |
| `cluster register` / `cluster grant` CLI | Remove | Use `host register --nkey-public` instead. |
| `host register` | `--nkey-public <key>` required. CLI never generates host NKeys. Publishes to `mclaude.users.{uslug}.hosts._.register` via user's NATS connection. | Hosts generate their own keys. CLI is just the messenger. |
| `--default-platform` | Flag on `host register`, sent in NATS payload. CP checks `is_admin`. | Admin-only, at most one globally (partial unique index). |
| SPA host badge | Remove machine/cluster badge | No `type` to display. |
| `manage.update` payload | Remove `type` field from `ManageUpdateRequest` and validation logic | Column doesn't exist anymore. |

## User Flow

### Register a host (admin)

```bash
# Admin logs in
mclaude login --server https://mclaude.example.com

# Get the NKey public key from the worker controller logs
kubectl logs -n mclaude-worker -l app.kubernetes.io/component=controller | grep pubkey

# Register with CP via NATS (--nkey-public is required)
mclaude host register --name "AKS Canada Central" --nkey-public UAKAK6UO7ZAJ... --default-platform
```

The CLI connects to NATS using the user's JWT (from `mclaude login`), publishes to `mclaude.users.{uslug}.hosts._.register` with `{name: "AKS Canada Central", nkey_public: "UAKAK6...", defaultPlatform: true}`, and prints the resulting host slug.

No local files are written except `~/.mclaude/hosts/{hslug}/config.json` (for `host list` display). No `nkey.seed`, no `nats.creds` — the CLI doesn't own the host's credentials.

## Component Changes

### `mclaude-control-plane`

**Remove admin cluster endpoints:**
- `admin.go` (or `server/admin.go` post-restructure): remove `handleAdminClusters` (POST/GET), `handleAdminClusterGrants` (POST), `adminDeleteCluster` (DELETE). All fully implemented — not stubs. Remove routes from `handleAdminRoutes`.
- `adminListClusters` uses `WHERE type = 'cluster'` — must be removed before column drop.
- `adminDeleteCluster` uses `DELETE FROM hosts WHERE slug = $1 AND type = 'cluster'` — same.

**Schema changes (DDL only — no migration, prerelease):**
- In the `CREATE TABLE IF NOT EXISTS hosts` DDL: remove `type TEXT NOT NULL CHECK (type IN ('machine', 'cluster'))` and `role TEXT NOT NULL DEFAULT 'owner'` columns entirely. No migration DO block — prerelease, wipe and reinstall.

**Remove type/role from all queries (exhaustive):**
- `scanHost` helper (`db/db.go:414`): remove `type` and `role` from the scan list. Update `Host` struct to remove `Type` and `Role` fields.
- `GetHostBySlug` (`db/db.go:464-466`): remove `type, role` from SELECT and remove `AND role = 'owner'` filter.
- `GetHostByPublicKey` (`db/db.go:479`): remove `type, role` from SELECT.
- `GetHostsByUser` (`db/db.go:429`): remove `type, role` from SELECT.
- `GetHostsByUser` / `ListHosts` (`hosts.go:201-212`): remove `type` and `role` from SELECT and scan.
- `HostResponse` struct (`server/hosts.go:24-35`): remove `Type string json:"type"` and `Role string json:"role"` fields. This is the HTTP response serialization struct — separate from the `Host` DB struct. Without removal, `GET /api/users/{uslug}/hosts` would serialize empty `type`/`role` fields.
- All INSERT INTO hosts (`lifecycle.go`, `hosts.go`, `admin.go`): remove `type` and `role` from column lists and VALUES. `POST /api/users/{uslug}/hosts` HTTP endpoint (`server/hosts.go:270-274`): remove entirely — BYOH is dead, all registration goes through NATS. Remove the route from `handleHostRoutes`.
- `HostKVState` struct (`sys_subscriber.go:16-22`): remove `Type` field. Remove `h.type` from the SQL query in the `$SYS CONNECT` handler (`sys_subscriber.go:100-104`). The DISCONNECT read-modify-write path (`sys_subscriber.go:133-148`) will silently strip `type` from existing KV entries on first DISCONNECT — this is the intended migration path (prerelease, no KV data preservation needed).
- `ManageUpdateRequest` (`lifecycle.go:713-716`): remove `Type` field. Update validation at `lifecycle.go:736` — check `req.Name == ""` only (remove `req.Type` from the condition).

**Update NATS registration handler:**
- `NATSHostRegisterRequest` (`lifecycle.go:253-257`): add `DefaultPlatform bool` field, remove `Type string` field. Remove the type defaulting logic at `lifecycle.go:285-287`.
- INSERT (`lifecycle.go:306-312`): remove `type` and `role` from columns and VALUES. Add `is_default_platform` when `defaultPlatform` is true. Preserve the existing `ON CONFLICT (user_id, slug) DO UPDATE SET name = EXCLUDED.name, public_key = EXCLUDED.public_key` upsert — re-registration updates name and key.
- Check `is_admin` before allowing `defaultPlatform`: the handler resolves `uslug` from the NATS subject, looks up the user in Postgres via `GetUserBySlug`, checks `user.IsAdmin`. This is the same trust model as all NATS handlers — the NATS server enforces that only the user who owns the JWT can publish to `mclaude.users.{uslug}.*` subjects. The slug-to-user lookup + `is_admin` DB check is equivalent to HTTP's `isAdminMiddleware` (which also resolves user from JWT → DB lookup → check `is_admin`). Return `{error: "admin_required"}` if not admin.
- Partial unique index collision returns `{error: "default_platform_exists"}`.
- Setting `defaultPlatform` does NOT auto-grant existing users. Auto-grant fires per-user at JWT issuance time (`issueJWTForNKey`). Operators should re-login existing users or restart CP to trigger re-issuance.

**Unify project routing — remove type-based branching:**
- `projects.go:287`: remove `host.Type == "cluster"` branch. Always route to `mclaude.users.{uslug}.hosts.{hslug}.agent.projects.provision`.
- `projects.go:573-579`: remove `hostType[0] == "cluster"` branch for deletion. Always route to user-agent deletion subject.
- `admin.go`, `hosts.go`: remove `h.Type` from `publishProjectsDeleteToHost`. Use user-agent path unconditionally.

### `mclaude-cli`

**Remove:**
- `cluster register`, `cluster grant` subcommands entirely.
- `daemon` subcommand (BYOH is dead).
- `host register` local NKey generation path — `--nkey-public` is now required, not optional.

**Update `host register`:**
- `--nkey-public <key>` — required flag. CLI validates NKey format (56-char base32 starting with `U` — user-level NKey only; `N`-prefix server keys are rejected).
- `--default-platform` — optional flag, included in NATS registration payload.
- `--name <name>` — required display name.
- CLI publishes to `mclaude.users.{uslug}.hosts._.register` via the user's NATS JWT (obtained during `mclaude login`). No HTTP endpoint involved.
- On success, writes `~/.mclaude/hosts/{hslug}/config.json` with `hubUrl`. No `nkey.seed`, no `nats.creds`, no `active-host` symlink.

### `mclaude-web` (SPA)

- Remove machine/cluster type badge from host list and all display components.
- Remove `type` field from TypeScript host types (including any `HostKVState` type definition).
- Remove any admin cluster management UI.

### `mclaude-controller-k8s`

- No changes needed. Doesn't reference `type` or `role`.

## Data Model

**Removed columns:**
- `hosts.type` (TEXT) — was `machine` or `cluster`
- `hosts.role` (TEXT) — was always `owner`

**No migration needed** — prerelease, wipe and reinstall. No cleanup of `role = 'user'` grant rows.

**Added to NATS registration payload:**
- `defaultPlatform` (bool, optional, default false) — CP enforces admin-only

## Error Handling

- **Non-admin sets `defaultPlatform`:** NATS reply `{error: "admin_required", message: "Only admins can set default platform"}`.
- **Second `defaultPlatform` registration:** NATS reply `{error: "default_platform_exists"}` (partial unique index collision).
- **`--nkey-public` with invalid key format:** CLI validates before publishing. Error: `"invalid NKey public key format"`.
- **`--nkey-public` omitted:** CLI errors with `"--nkey-public is required"`.
- **Admin cluster endpoint called after removal:** Returns 404 (route no longer registered).

## Security

No security impact. Admin-only `defaultPlatform` enforcement moves from HTTP `isAdminMiddleware` to the NATS handler (checks `is_admin` on the user from the subject's `{uslug}`). Same authorization, different transport.

## Impact

- `docs/mclaude-control-plane/spec-control-plane.md`: remove admin cluster endpoints section; update NATS registration payload `{name, nkey_public, defaultPlatform?}`; remove `type`/`role` from host model; remove type-based routing description; remove BYOH daemon references.
- `docs/spec-state-schema.md`: remove `type` and `role` columns from hosts table; remove `type` from `mclaude-hosts` KV value; update registration payload; remove `type?` from `manage.update` payload; remove `type`/`role` from login response target fields.
- `docs/mclaude-web/spec-web.md`: remove host type badge references; remove `type` from TypeScript types.
- `docs/charts-mclaude/spec-helm.md`: remove any `type` references.

## Scope

**In v1:**
- Drop `type` and `role` columns (DDL only — prerelease wipe)
- Remove all admin cluster endpoints
- Remove `cluster register`, `cluster grant`, `daemon` CLI commands
- Make `--nkey-public` required on `host register`
- Add `--default-platform` flag
- CLI registers via NATS publish
- Unify project routing to user-agent only
- Remove `ManageUpdateRequest.Type`
- Remove `POST /api/users/{uslug}/hosts` HTTP endpoint (BYOH dead)
- Remove SPA type badge + TypeScript type fields

**Deferred:**
- BYOH redesign (if/when needed — separate ADR)
- Host capabilities/tags
- Host groups/pools for scheduling

## Open questions

_(none — all resolved)_

## Integration Test Cases

| Test case | What it verifies | Setup/teardown | Components exercised |
|-----------|------------------|----------------|----------------------|
| Register host via NATS with nkey_public | Publish to `mclaude.users.{uslug}.hosts._.register` with `{name, nkey_public}`. Host row created, challenge-response succeeds. | Admin user, NATS publish | mclaude-control-plane |
| Register host with default-platform (admin) | Same NATS publish with `defaultPlatform: true`. `is_default_platform=true` in DB. Second registration returns `default_platform_exists`. | Admin user | mclaude-control-plane |
| Register host with default-platform (non-admin) | NATS publish with `defaultPlatform: true` as non-admin. Returns `admin_required`. | Non-admin user | mclaude-control-plane |
| Schema has no type/role columns | Fresh `Migrate()` on empty DB: `hosts` table has no `type` or `role` columns. | Empty DB | mclaude-control-plane |
| Admin cluster endpoints return 404 | `POST /admin/clusters` returns 404 after removal. | Admin JWT | mclaude-control-plane |
| Existing admin cluster tests rewritten | `TestIntegration_ADR0091_AdminCluster_DefaultPlatform` and `_Conflict` (grants_integration_test.go:768,817) — rewrite to use NATS registration with `defaultPlatform: true` instead of `POST /admin/clusters`. `TestIntegration_ADR0091_DefaultPlatform_AutoGrant` (line 880) — rewrite raw INSERT to remove `type`/`role` columns. |  | mclaude-control-plane |
| Project provisioning routes to user-agent | Create project, verify NATS publish goes to `mclaude.users.{uslug}.hosts.{hslug}.agent.projects.provision` (no type branch). | Registered host + user | mclaude-control-plane |

## Implementation Plan

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| `mclaude-control-plane` (remove admin clusters + migration + routing + registration) | ~-300 / +50 lines | 100k | Delete admin handlers, update NATS handler, migration DO block, remove routing branch, update all queries |
| `mclaude-cli` (remove cluster/daemon commands + update host register) | ~-150 / +30 lines | 50k | Delete cluster/daemon subcommands, add NATS publish, make --nkey-public required |
| `mclaude-web` (remove type badge + TS types) | ~-30 lines | 20k | Remove badge, type fields |

**Total estimated tokens:** 170k
**Estimated wall-clock:** 3-4h
