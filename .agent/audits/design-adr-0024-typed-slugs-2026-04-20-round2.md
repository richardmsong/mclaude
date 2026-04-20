## Run: 2026-04-20T12:00:00Z

VERDICT: CLEAN — no blocking gaps found.

---

### Prior gap review

**Gap 1 — Subject inventory is incomplete vs spec-state-schema.md**

CLOSED. The ADR now includes a KV key format table that covers `mclaude-clusters` with old key `{userId}` → new key `{uslug}`. The NATS subject inventory table lists 12 old→new subject mappings covering all subjects in `spec-state-schema.md` including `mclaude.clusters.{clusterId}.status` → `mclaude.clusters.{cslug}.api.status`. No subject in the schema is left unmapped.

**Gap 2 — ADR-0016 subject-permission grants use old subject shape — no update specified**

CLOSED. The ADR now contains a full "NATS permission grant inventory" section with explicit grant strings for SPA, control-plane, K8s/BYOH controller, and session-agent, including publish-allow, subscribe-allow, and publish-deny lists. A developer can implement the Helm chart templates directly from these strings.

**Gap 3 — Session-agent NATS subject shape contradiction with ADR-0016**

CLOSED. The ADR explicitly addresses the ADR-0016 ceiling (`mclaude.*.sessions.{clusterId}.*.>`) as superseded, provides the new signing-key ceiling (`mclaude.users.*.projects.*.>`), and provides the exact new per-project session-agent JWT claim strings. The note explains the per-project vs per-session scope decision.

**Gap 4 — `mclaude-job-queue` key separator change breaks ADR-0009 callers**

CLOSED. The hard-cutover KV rekeying section now defines a concrete migration procedure: snapshot all keys, join against Postgres to compute new slug-based key, write value under new key, purge old key. For `mclaude-sessions` the purge is total (ephemeral data). For `mclaude-job-queue`, rekeying is required and follows the same join-and-rekey procedure. `{jobId}` stays UUID — explicitly stated.

**Gap 5 — HTTP URL inventory is incomplete**

CLOSED. The ADR now includes a complete method+path HTTP URL inventory table enumerating all routes with old and new paths: 4 project routes, 4 session routes, 4 job routes, and all admin cluster and user routes with concrete method+path strings. Admin cluster routes (`GET/POST /admin/clusters`, `GET /admin/clusters/{cslug}`, `POST /admin/clusters/{cslug}/members`, `DELETE /admin/clusters/{cslug}/members/{uslug}`) and admin user routes are all listed.

**Gap 6 — `users.slug` backfill collision handling is underspecified**

CLOSED. The ADR replaces the "simplified plpgsql function" approach with a Go migration program (`cmd/slug-backfill`). The algorithm is now stated in pseudocode for all three entity types (users, projects, clusters): ordered iteration, collision detection using an in-memory seen set, numeric suffix loop. The fallback rule (empty/reserved/leading-underscore → `{type}-{6 base32 chars from UUID}`) is fully specified.

**Gap 7 — `mclaude-sessions` KV key in ADR-0009 daemon code still uses UUIDs**

CLOSED. The ADR adds `UserSlug`, `ProjectSlug`, `SessionSlug` fields to `JobEntry` and specifies that the dispatcher uses these slug fields (not UUID fields) to construct KV keys. UUID fields (`UserID`, `ProjectID`, `SessionID`) stay for Postgres joins and logging. A developer implementing the `runJobDispatcher` changes has a clear specification for which fields to use for which purpose.

**Gap 8 — JetStream stream subject filters are not updated**

CLOSED. The ADR now contains a "JetStream stream filter inventory" table with concrete old→new filter strings for all three streams: `MCLAUDE_API` (`mclaude.*.*.api.sessions.>` → `mclaude.users.*.projects.*.api.sessions.>`), `MCLAUDE_EVENTS` (`mclaude.*.*.events.*` → `mclaude.users.*.projects.*.events.*`), `MCLAUDE_LIFECYCLE` (`mclaude.*.*.lifecycle.*` → `mclaude.users.*.projects.*.lifecycle.*`). The section also notes that streams are recreated (not renamed) due to JetStream filter constraints.

**Gap 9 — `mclaude-projects` KV key change ambiguous for ADR-0011 multi-cluster**

CLOSED. The ADR now contains an explicit "`mclaude-projects` KV key (scope note)" section that acknowledges the pre-existing drift between `spec-state-schema.md` (user-prefixed) and ADR-0011/ADR-0016 (cluster-prefixed), states which format ADR-0024 preserves (user-prefixed, the current live shape), and explains that cluster-prefix reconciliation is deferred to a future multi-cluster KV-partitioning ADR. The controller grant is stated as `$KV.mclaude-projects.>` (broad, matching today's schema) until that ADR lands.

---

### New ambiguity check

No new blocking ambiguities were found. The additions are internally consistent with `spec-state-schema.md` and the referenced ADRs for the scope this ADR covers.
