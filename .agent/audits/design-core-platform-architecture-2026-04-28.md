## Run: 2026-04-28T00:00:00Z (Round 4)

CLEAN — no blocking gaps found.

### Cross-spec consistency observations (advisory, non-blocking)

The following are inconsistencies between ADR-0042 and other specs where the **other spec** needs updating to match the ADR's decisions. None block implementation from the ADR alone.

1. **`extraFlags` missing from state schema SessionState** — ADR-0042's Session State KV value explicitly includes `extraFlags` (with description: "persisted in KV and re-applied on every pod restart"). `spec-state-schema.md`'s `SessionState` definition does not include this field. The ADR is authoritative; the state schema should add `extraFlags: "string"`.

2. **`git_identity_id` missing from state schema `projects` table** — ADR-0042's Postgres `projects` CREATE TABLE includes `git_identity_id TEXT`. The state schema's `projects` table definition omits this column. Both `db.go` and `spec-control-plane.md` include it. The state schema should add the column.

3. **`joinWorktree` in state schema but not in ADR session state example** — `spec-state-schema.md` includes `joinWorktree: false` in the SessionState KV value. ADR-0042's session state JSON example omits it. Minor; the state schema is authoritative for the schema and the ADR's example is illustrative rather than exhaustive.

4. **MCLAUDE_LIFECYCLE retention: ADR says 30 days, state schema says TBD** — ADR-0042's JetStream Stream Filters table sets MCLAUDE_LIFECYCLE retention to 30 days. `spec-state-schema.md` line 294 still reads `MaxAge: TBD`. The state schema should be updated to 30 days per the ADR's decision.

5. **Project KV `status` example value** — ADR-0042's Project State KV example shows `"status": "running"`. The Postgres column constraint allows only `active, pending, archived`. The state schema KV definition is loose (`"status": "string"`). The ADR also mentions `PROJECT_STATUS_FAILED` in the reliability section. These suggest the KV status vocabulary may intentionally differ from Postgres. Not blocking — the KV schema is typed as `string` — but the full KV status enumeration is undocumented.

6. **spec-common.md stale helpers** — `docs/mclaude-common/spec-common.md` still lists `ClusterAPIProjectsProvision`, `ClusterAPIStatus`, `UserHostStatus` as existing subject helpers, and `ClustersKVKey` for the removed `mclaude-clusters` bucket. These were removed by ADR-0035. The actual `subj.go` code no longer has the subject helpers (only `ClustersKVKey` remains). The spec should be updated to match.

### Verification of prior-round fixes

All 22 gaps from rounds 1–3 were verified as addressed. The document is internally consistent, cross-references to ADR-0035 are accurate, the Postgres DDL matches `db.go`'s schema, the NATS subject patterns match `subj.go`'s typed helpers, the KV key formats are consistent across all references, and the component topology/data flow descriptions are unambiguous.
