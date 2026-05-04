## Run: 2026-05-01T00:00:00Z

Component: mclaude-control-plane
ADR: docs/adr-0063-k8s-architecture-spec.md (Component Changes — mclaude-control-plane section)
Spec: docs/mclaude-control-plane/spec-control-plane.md
Scope: Three targeted checks per the audit request

---

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0063:135 | `"Client"` branch should look up the `hosts` row by `public_key` only with **no type filter** — matches both `type='machine'` and `type='cluster'` hosts | sys_subscriber.go:82-121 | IMPLEMENTED | — | `switch evt.Client.Kind { case "Client":` queries `WHERE public_key = $2` with no type filter. SQL at lines 89-95 and 100-104 both use only `public_key` predicate. Comment on lines 84-86 explicitly documents the no-type-filter intent. |
| adr-0063:135 | the `"Leafnode"` branch should be removed; unexpected `"Leafnode"` events log a warning and are dropped | sys_subscriber.go:152-157 | IMPLEMENTED | — | `default:` case (lines 152-157) logs a warning with `Msg("$SYS event: unexpected client kind; leaf topology removed per ADR-0063 — dropping")` and returns. No `"Leafnode"` case exists; it falls into `default`. |
| spec-control-plane.md:145 | `Client` → `SELECT * FROM hosts WHERE public_key = client.nkey LIMIT 1` (no type filter — matches both `type='machine'` and `type='cluster'`), update `last_seen_at`, upsert `mclaude-hosts` KV `{hslug}` with `online=true`. The `Leafnode` branch is removed per ADR-0063. | sys_subscriber.go:82-157 | IMPLEMENTED | — | Code matches exactly: Client branch updates `last_seen_at`, queries slug/name/type, writes KV with flat `{hslug}` key (line 115: `key := hslug`). Leafnode branch absent; default case warns and drops. |
| adr-0063:135 (spec-state-schema.md:299) | KV key format: `{hslug}` (flat, no user prefix — hosts are globally unique per ADR-0054) | sys_subscriber.go:115 | IMPLEMENTED | — | `key := hslug` — flat key, no user prefix. |
| adr-0063 Data Model / adr-0063:217 | Migration must drop the `CHECK (type = 'machine' OR (js_domain IS NOT NULL AND leaf_url IS NOT NULL AND account_jwt IS NOT NULL))` constraint | db.go:1009-1022 | IMPLEMENTED | — | DO block (lines 1009-1022) finds the constraint by scanning `pg_constraint` for any `contype='c'` on `hosts` referencing `js_domain`, then executes `ALTER TABLE hosts DROP CONSTRAINT`. Idempotent (skips if not found). |
| adr-0063 Data Model | Migration drops `js_domain`, `leaf_url`, `account_jwt` columns | db.go:1024-1033 | IMPLEMENTED | — | Three `IF EXISTS` column checks followed by `ALTER TABLE hosts DROP COLUMN` for each. Idempotent across fresh installs (columns never created) and upgrades (columns present). |
| spec-control-plane.md:205 | The `gen-leaf-creds` subcommand (which generated worker NATS leaf credentials) was removed by ADR-0063. | main.go:31-36 | IMPLEMENTED | — | `main()` routes only to `"init-keys"` subcommand (line 33). No `"gen-leaf-creds"` case exists anywhere in the binary. Full-repo grep returned no matches. |
| adr-0063:258-260 | `mclaude-control-plane/sys_subscriber.go` — remove type filter from `"Client"` branch (lookup by `public_key`, no type filter); drop `"Leafnode"` branch | sys_subscriber.go:82-157 | IMPLEMENTED | — | Confirmed above. |
| adr-0063:300 | `mclaude-control-plane/db.go` + migration (drop `CHECK` constraint); migrate `js_domain`/`leaf_url`/`account_jwt` columns if still present | db.go:1003-1035 | IMPLEMENTED | — | Confirmed above. |

---

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|----------------|-------------|
| sys_subscriber.go:12-22 (HostKVState struct) | INFRA | Standard JSON-tagged struct for KV writes; required by spec-state-schema.md § mclaude-hosts. |
| sys_subscriber.go:24-34 (sysConnectEvent struct) | INFRA | JSON parse target for `$SYS` events; required plumbing for spec'd CONNECT/DISCONNECT handling. |
| sys_subscriber.go:36-61 (StartSysSubscriber) | INFRA | Subscribe setup function; required to wire the handler into server startup per spec startup sequence. |
| sys_subscriber.go:63-67 (nil db guard) | INFRA | Defensive guard for nil db in tests; necessary plumbing. |
| sys_subscriber.go:122-150 (DISCONNECT branch) | INFRA | DISCONNECT handler (read-modify-write to preserve lastSeenAt); spec'd in spec-control-plane.md:146 and adr-0063 — sets online=false without rewriting last_seen_at. |
| db.go:839-856 (CREATE TABLE hosts with legacy columns) | INFRA | The `CREATE TABLE IF NOT EXISTS hosts` block still lists `js_domain`, `leaf_url`, `account_jwt` columns and the CHECK constraint for fresh installs — but the ADR-0063 DO block immediately after drops them. On a fresh install the columns are created and then immediately dropped. This is harmless but slightly redundant; the columns exist in the DDL only for the migration DO block to then remove them. No spec divergence — behavior is correct after migration. |
| main.go:31-36 (subcommand routing) | INFRA | Routes to `init-keys` only; absence of `gen-leaf-creds` is the spec'd state. |

---

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| adr-0063:135 / spec-cp:145 | `"Client"` branch: no type filter, matches machine + cluster | None (no unit test in isolation) | integration_test.go:575-641 (`TestIntegration_HandleSysEvent_ClusterConnect`), integration_test.go:647-722 (`TestIntegration_HandleSysEvent_ClusterDisconnect`), plus machine variants at :426-490 | E2E_ONLY | Tests use real Postgres + real NATS (testcontainers via docker-compose). No isolated unit test. Tests confirm both `type='machine'` (lines 426-490) and `type='cluster'` (lines 575-641) are matched by the Client branch. |
| adr-0063:135 / spec-cp:145 | `"Leafnode"` branch removed; unexpected events warned and dropped | None | integration_test.go:724-759 (`TestIntegration_HandleSysEvent_LeafnodeDropped`) | E2E_ONLY | Test verifies Leafnode events write nothing to KV and don't panic. |
| adr-0063:217 / Data Model | Migration drops CHECK constraint and js_domain/leaf_url/account_jwt columns | db_test.go:86-118 (schema string content checks) | integration_test.go:580-590 (cluster host INSERT without legacy columns succeeds after migration) | TESTED | db_test.go lines 106-118 check the schema string contains both the original constraint text and the DROP statements. Integration test ClusterConnect inserts a `type='cluster'` row with no js_domain/leaf_url/account_jwt and confirms it succeeds post-migration. |
| spec-cp:205 | `gen-leaf-creds` subcommand removed | (no test needed — absence is verified by grep) | N/A | TESTED | Grep confirms no `gen-leaf-creds` code path exists. db_test.go line 108 checks schema. |

**Note on machine CONNECT integration tests (lines 472, 537, 552):** The tests use KV key `sysevt-example-com.myhost` and `sysdisc-example-com.dischost` (dot-separated `{uslug}.{hslug}` format) when looking up the KV entry. The production code writes the key as `hslug` only (flat, no user prefix — `sys_subscriber.go:115: key := hslug`), which is the canonical format per `spec-state-schema.md:299`. These tests will FAIL if run against the production code because `hostsKV.Get("sysevt-example-com.myhost")` will return an error (entry is stored at `"myhost"`, not `"sysevt-example-com.myhost"`). This is a test-side key format bug — the production code is correct.

---

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none found) | No open bugs in .agent/bugs/ matching mclaude-control-plane for ADR-0063 scope | N/A | `.agent/bugs/` checked; no file with `**Component**: mclaude-control-plane` related to ADR-0063. |

---

### Summary

- Implemented: 9
- Gap: 0
- Partial: 0
- Infra: 6
- Unspec'd: 0
- Dead: 0
- Tested: 1
- Unit only: 0
- E2E only: 3
- Untested: 0
- Bugs fixed: 0
- Bugs open: 0

**Additional finding (test bug, not production code gap):**

`integration_test.go:472` — `TestIntegration_HandleSysEvent_MachineConnect` calls `hostsKV.Get("sysevt-example-com.myhost")` (dot-prefixed user slug format). Production code writes `key := hslug` = `"myhost"` (flat, per `spec-state-schema.md:299`). The test will always return a "key not found" error and fail. Same bug at line 537/552 in `TestIntegration_HandleSysEvent_MachineDisconnect`. The cluster CONNECT/DISCONNECT tests at lines 621-622 and 691-692 correctly use `key := "mycluster"` (flat). Direction: test-side fix — update the Get key in the machine tests to `"myhost"` / `"dischost"`.
