## Run: 2026-05-01T00:00:00Z

Component: mclaude-control-plane
ADR: docs/adr-0063-k8s-architecture-spec.md (Component Changes — mclaude-control-plane section)
Spec: docs/mclaude-control-plane/spec-control-plane.md

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0063:135 | `"Client"` branch should look up the `hosts` row by `public_key` only with no type filter | sys_subscriber.go:83-121 | IMPLEMENTED | — | `case "Client"` does `WHERE public_key = $1` with no type filter, matches both machine and cluster |
| adr-0063:135 | updates `last_seen_at` and the `mclaude-hosts` KV `online` flag for both `type='machine'` and `type='cluster'` hosts | sys_subscriber.go:88-120 | IMPLEMENTED | — | On CONNECT: updates `last_seen_at` via SQL then writes KV with `online=true`; slug, name, type from DB row (no type filter) |
| adr-0063:135 | `"Leafnode"` branch should be removed; unexpected "Leafnode" events log a warning and are dropped | sys_subscriber.go:152-157 | IMPLEMENTED | — | `default` branch logs warning "unexpected client kind; leaf topology removed per ADR-0063 — dropping" |
| spec-control-plane.md:145 | `Client` → `SELECT * FROM hosts WHERE public_key = client.nkey LIMIT 1` (no type filter) | sys_subscriber.go:100-104 | IMPLEMENTED | — | Query is `WHERE h.public_key = $1` with no type filter |
| spec-control-plane.md:145 | The `Leafnode` branch is removed — all host controllers connect hub-direct as `"Client"` per ADR-0063 | sys_subscriber.go:152-157 | IMPLEMENTED | — | `"Leafnode"` is not a named case; falls into `default` with warning |
| adr-0063:217 | Drop CHECK constraint `(type = 'machine' OR (js_domain IS NOT NULL AND leaf_url IS NOT NULL AND account_jwt IS NOT NULL))` via migration | db.go:1009-1022 | IMPLEMENTED | — | DO block: finds constraint by `pg_get_constraintdef LIKE '%js_domain%'` and drops it |
| adr-0063:217 | Migration also drops `js_domain`, `leaf_url`, `account_jwt` columns if not already removed | db.go:1024-1034 | IMPLEMENTED | — | Three idempotent `IF EXISTS` drops for each column |
| spec-control-plane.md:205 | The `gen-leaf-creds` subcommand (which generated worker NATS leaf credentials) was removed by ADR-0063 | main.go:33-36 | IMPLEMENTED | — | Only `init-keys` subcommand exists; no `gen-leaf-creds` branch anywhere in main.go or any other file |
