## ADR-0011 Multi-Cluster Spec Alignment Audit

**Date**: 2026-04-24
**ADR**: docs/adr-0011-multi-cluster.md
**Status of ADR**: DRAFT (partially superseded by ADR-0004)
**Evaluator note**: This ADR is in `draft` status and partially superseded by ADR-0004 (BYOH). The registry/identity/RBAC model (clusters table user-facing identity, user_clusters join table, cluster RBAC grants, cluster picker via explicit grants) is superseded — NOT evaluated here. The surviving infrastructure topology (leaf nodes, hub NATS, JetStream domains, worker controller) IS evaluated. The user is considering folding 0011 fully into ADR-0004.

Specs checked:
- docs/spec-state-schema.md
- docs/mclaude-control-plane/spec-control-plane.md
- docs/mclaude-session-agent/spec-session-agent.md
- docs/charts-mclaude/spec-helm.md
- docs/ui/spec-auth.md
- docs/ui/mclaude-web/spec-dashboard.md
- docs/ui/mclaude-web/spec-session-detail.md
- docs/adr-0004-multi-laptop.md (ADR-0004, accepted — partial superseder)
- docs/adr-0014-controller-separation.md (draft — informational reference only)
- docs/adr-0016-nats-security.md (accepted)

---

### Phase 1 — Spec → Code (Surviving ADR-0011 Decisions → Spec Coverage)

Superseded sections skipped: cluster registry as user-facing identity model, user_clusters join table, cluster RBAC grants, cluster picker via explicit grants. These are absorbed by ADR-0004's `hosts` model.

| Spec (doc:line) | Spec text | Spec location covering it | Verdict | Direction | Notes |
|-----------------|-----------|---------------------------|---------|-----------|-------|
| ADR-0011:18 | NATS topology: Leaf nodes (hub on CP, workers as leaves) | spec-state-schema.md:339-348 "Cluster infrastructure subjects"; spec-state-schema.md:467-479 mentions no leaf node config | PARTIAL | SPEC→FIX | spec-state-schema.md covers the cluster subjects that flow over leaf nodes, and ADR-0004 references "ADR-0011's infrastructure topology preserved unchanged." But spec-state-schema.md's NATS Server Configuration section (lines 467-479) documents only a single NATS config with no leafnodes block. No spec file documents the hub-side or worker-side NATS leafnodes configuration. The topology decision is referenced but never fully specified in any living spec. |
| ADR-0011:19 | SPA session list: Domain-qualified KV watches across all clusters | No spec coverage found | GAP | SPEC→FIX | No SPA spec (spec-dashboard.md, spec-session-detail.md, spec-auth.md) mentions domain-qualified KV watches, jsDomain, or cross-cluster monitoring. This is consistent with the feature not yet being implemented; ADR-0004 absorbed cluster scoping into the hosts model, so the mechanism (per-cluster domain-qualified KV watch) may need to be respecified in terms of hosts. |
| ADR-0011:20 | Global metadata: Postgres clusters table | spec-state-schema.md:71-88 (`clusters` table) | IMPLEMENTED | — | `clusters` table is fully specified in spec-state-schema.md with same columns as ADR-0011 except ADR-0004 added `slug` column and removed `user_clusters`. |
| ADR-0011:23 | Leaf node auth: Shared credentials from control plane (CP generates leaf NKey pair, stores in Postgres clusters.leaf_creds, returns to worker) | spec-state-schema.md:82 (`leaf_creds` column) | PARTIAL | SPEC→FIX | The `clusters.leaf_creds` column exists in the state schema. But no spec file describes the leaf credential issuance flow: generation at registration time, storage, or the handshake between CP and worker NATS. This behavior exists only in ADR-0011 (draft, not a living spec). |
| ADR-0011:25 | Backwards compatibility: Single-cluster = degenerate multi-cluster (no standalone mode) | No spec coverage found | GAP | SPEC→FIX | No living spec documents the single-cluster degenerate mode: auto-registration logic, LOCAL_CLUSTER_ID concept, or the fact that a single deployment is conceptually a hub+worker on the same cluster. spec-control-plane.md startup sequence (lines 124-136) does not mention auto-registration of cluster rows. |
| ADR-0011:26 | Provisioning: Worker controller via NATS (each worker runs its own controller, CP publishes provisioning requests through leaf nodes) | spec-state-schema.md:340-343 (cluster infrastructure subjects table) | PARTIAL | SPEC→FIX | spec-state-schema.md documents `mclaude.clusters.{cslug}.api.projects.provision` and `mclaude.clusters.{cslug}.api.status` subjects. However, the provisioning flow (CP publishes a NATS request, it routes through leaf nodes to worker controller which creates CRD locally) is not described in any living spec beyond the subject table. spec-control-plane.md project creation flow (lines 180-186) still describes direct MCProject CR creation, not NATS provisioning. |
| ADR-0011:27 | Helm charts: Separate mclaude-cp + mclaude-worker charts | No spec coverage found | GAP | SPEC→FIX | spec-helm.md describes a single `mclaude` chart. No spec documents mclaude-cp or mclaude-worker as separate charts. The helm spec has no mclaude-worker chart, no leaf node config values, no controller deployment values per ADR-0011's worker chart design. |
| ADR-0011:28 | NATS auth: Shared account key (one account NKey across hub and all workers; CP signs user JWTs — valid everywhere) | spec-state-schema.md:467-479 (NATS config) mentions "No auth resolver configured" and "JWT verification uses account public key baked into NATS server config" | PARTIAL | SPEC→FIX | spec-state-schema.md describes the auth mechanism but only for a single NATS instance. It does not document the multi-instance shared account key distribution pattern: how worker NATS instances receive the same operator+account JWT trust chain so user JWTs are valid across all instances. |
| ADR-0011:29 | Client connection: Direct to worker preferred, hub fallback | No spec coverage found | GAP | SPEC→FIX | No SPA spec documents the dual-connection strategy (hub connection always open, direct worker connection opened on demand for session detail, fallback if direct fails). This is a significant architectural decision with no living spec representation. |
| ADR-0011:33-46 | User Flow: Login returns hub NATS URL + worker direct URL per project; SPA opens domain-qualified KV watches per cluster | spec-state-schema.md:339-348; spec-auth.md | GAP | SPEC→FIX | spec-auth.md documents only basic login success/failure, nothing about cluster metadata in login response. No spec describes the extended login response shape (projects[], clusters[], jsDomain, directNatsUrl fields). |
| ADR-0011:49-101 | Control Plane: New Postgres tables (clusters, user_clusters), modified projects table with cluster_id | spec-state-schema.md:71-90 | PARTIAL | SPEC→FIX | `clusters` table is in spec-state-schema.md. `user_clusters` table is explicitly called out as "removed — absorbed into hosts table" (spec-state-schema.md:89). `projects` table in spec-state-schema.md uses `host_id` (FK→hosts) not `cluster_id` (FK→clusters) — this is correct per ADR-0004 supersession. So the ADR-0011 decision was partially superseded before being implemented. No gap on `projects.cluster_id` — that was correctly absorbed into `projects.host_id`. |
| ADR-0011:94-101 | New HTTP endpoints: POST/GET /admin/clusters, POST /admin/clusters/{id}/members, DELETE /admin/clusters/{id}/members/{userId} | spec-control-plane.md:59-69 (admin endpoints table) | GAP | SPEC→FIX | spec-control-plane.md admin endpoint table has no cluster management endpoints. The cluster admin API from ADR-0011 was never reflected in the living spec. (These may be absorbed into ADR-0004's host registration model, but no living spec covers this.) |
| ADR-0011:120-178 | Modified login response: projects[], clusters[], ClusterInfo, ProjectInfo, AuthTokens type extensions | spec-auth.md, spec-state-schema.md | GAP | SPEC→FIX | No living spec documents the extended AuthTokens type with projects/clusters arrays, or the extended login response shape. spec-auth.md documents only the basic login screen behavior. |
| ADR-0011:180-190 | AuthStore: getProjects(), getClusters(), getJwt(), getNkeySeed() accessors | No spec coverage | GAP | SPEC→FIX | No SPA spec documents these AuthStore methods or the cluster-scoped accessors. |
| ADR-0011:192-300 | SessionStore domain-qualified KV watches, EventStore dual-NATS, ConversationVM cluster routing | No spec coverage | GAP | SPEC→FIX | None of these SPA transport-layer extensions (domain parameter on kvWatch/kvGet/jsSubscribe, dual NATS connections, ConversationVM authStore parameter) appear in any living SPA spec. |
| ADR-0011:226-253 | Worker Controller: new component subscribed to mclaude.clusters.{clusterId}.projects.provision | spec-state-schema.md:340-343 | PARTIAL | SPEC→FIX | The subject is in spec-state-schema.md but the worker controller component itself has no spec. No spec documents its deployment, responsibilities, health endpoints, NATS credentials, or K8s access. |
| ADR-0011:257-259 | Session Agent: No changes — leaf node makes events visible to hub-connected clients transparently | spec-session-agent.md (entire spec) | PARTIAL | SPEC→FIX | spec-session-agent.md's subject patterns (NATS Subjects Publish section, lines 76-89) still use the legacy pattern `mclaude.users.{uslug}.projects.{pslug}.*` without `hosts.{hslug}` (updated by ADR-0004). The spec is silent on the transparency of leaf-node routing — it neither affirms nor denies it. The claim "session-agent unchanged" is not documented as a design decision anywhere in the living specs. |
| ADR-0011:444-462 | Hub NATS configuration: leafnodes port 7422, JWT auth with operator+resolver chain | spec-state-schema.md:467-479 | GAP | SPEC→FIX | spec-state-schema.md NATS Server Configuration section has no leafnodes block. It documents a simpler single-NATS config. No living spec describes the hub NATS config with the JWT auth chain (operator, resolver, resolver_preload). |
| ADR-0011:463-485 | Worker NATS configuration: leafnodes.remotes block, JetStream domain, same JWT auth chain | No spec coverage | GAP | SPEC→FIX | No spec documents worker NATS configuration at all. |
| ADR-0011:488-494 | New NATS Subjects: mclaude.clusters.{clusterId}.projects.provision (publisher: CP, subscriber: worker controller) | spec-state-schema.md:340-343 | PARTIAL | SPEC→FIX | spec-state-schema.md uses `{cslug}` not `{clusterId}` — this is correct per ADR-0024 typed slugs. But the subject shape in spec-state-schema.md is `mclaude.clusters.{cslug}.api.projects.provision` (with `.api.` inserted between clusters.{cslug} and projects) while ADR-0011 says `mclaude.clusters.{clusterId}.projects.provision` (no `.api.` segment). This is a schema divergence. |
| ADR-0011:492-494 | Controller liveness via NATS $SYS presence events (connect/disconnect → cluster status update) | spec-state-schema.md (no $SYS section); spec-control-plane.md startup sequence | GAP | SPEC→FIX | No living spec documents the $SYS presence subscription for cluster/controller liveness detection. spec-control-plane.md startup sequence (lines 124-136) does not mention $SYS subscriptions or cluster status updates. |
| ADR-0011:497-498 | Existing subjects flow between hub and worker transparently via leaf node (no explicit allow/deny) | spec-state-schema.md:346-348 | IMPLEMENTED | — | spec-state-schema.md notes "In multi-cluster deployments, all existing subjects flow between hub and worker NATS transparently via leaf node connections." This exactly captures the decision. |
| ADR-0011:500-504 | KV Buckets unchanged — accessible from hub via domain-qualified JetStream | spec-state-schema.md:96-252 (KV bucket definitions) | PARTIAL | SPEC→FIX | spec-state-schema.md documents the KV bucket structure but does not document how hub-connected clients access them via domain-qualified JetStream (`js.KeyValue('mclaude-sessions', { domain: 'worker-a' })`). The domain-qualified access pattern is not described in any living spec. |
| ADR-0011:506 | Cluster RBAC enforced at control-plane HTTP layer; NATS layer does not enforce cluster-level access | spec-control-plane.md; spec-state-schema.md | GAP | SPEC→FIX | No living spec explicitly documents this security boundary: that cluster-level access control is purely at the HTTP/Postgres layer, not a cryptographic NATS boundary. |
| ADR-0011:519-527 | Security: account key distribution, leaf node credentials, user JWT permissions extended for $JS.*.API.> | spec-state-schema.md; spec-control-plane.md (auth section lines 163-167) | GAP | SPEC→FIX | spec-control-plane.md authentication section describes JWT scoping (`mclaude.{userId}.>` with `_INBOX.>`) but does not mention the `$JS.*.API.>` permission extension needed for domain-qualified KV watch from the hub. |

---

### Phase 2 — Code → Spec (Reverse Pass)

This audit is spec-document-only (no production code evaluated) — ADR-0011 is draft status, so no production code implements its decisions yet. The reverse pass checks for spec content that contradicts ADR-0011's surviving decisions.

| Doc:section | Classification | Explanation |
|-------------|----------------|-------------|
| spec-control-plane.md:180-186 (Project Creation Flow) | CONTRADICTS ADR-0011 | spec-control-plane.md describes direct MCProject CR creation (step 4: "Creates an MCProject CR in mclaude-system"). ADR-0011 says provisioning should go via NATS to the worker controller. This is a living spec that reflects the current (pre-0011) implementation, not the desired state. |
| spec-control-plane.md:59-69 (Admin endpoints) | MISSING | Admin endpoints table has no cluster management routes (POST /admin/clusters, member management). ADR-0011 specifies four cluster admin endpoints. |
| spec-control-plane.md:96-100 (NATS KV Buckets) | MISSING | Lists mclaude-projects and mclaude-job-queue but not mclaude-clusters (the per-user cluster list bucket added in ADR-0014/ADR-0004). |
| spec-state-schema.md:467-479 (NATS Server Configuration) | CONTRADICTS ADR-0011 | Documents a single NATS config with no leafnodes block and states "No auth resolver configured." ADR-0011 requires a JWT auth chain (operator, resolver: MEMORY, resolver_preload) on both hub and worker NATS instances. |
| spec-session-agent.md:76-89 (NATS Subjects Publish) | OUTDATED | Subject patterns use `mclaude.users.{uslug}.projects.{pslug}.*` without `.hosts.{hslug}.` — ADR-0004 insert. ADR-0011 says session-agent is unchanged, but ADR-0004 changes its subjects. This is not an ADR-0011 issue but flags a pre-existing spec gap. |
| spec-helm.md (entire) | MISSING | Documents a single `mclaude` chart. ADR-0011 requires separate mclaude-cp and mclaude-worker charts. No worker chart, no leaf node values, no worker controller deployment in any spec. |

---

### Phase 3 — Test Coverage

Not applicable — ADR-0011 is draft and no production code implements its decisions. No test coverage evaluation is possible.

---

### Phase 4 — Bug Triage

Checked `.agent/bugs/` for bugs referencing multi-cluster, NATS topology, JetStream domains, or worker controller.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none found) | — | — | No open bugs in .agent/bugs/ reference ADR-0011 components |

---

### Summary

- **Implemented**: 2 (leaf node transparency note in state schema; clusters table in state schema)
- **Partial**: 8 (leaf node topology reference only, leaf creds column without flow, provisioning subject exists but flow unspecified, worker controller subject exists but component unspecified, session agent leaf transparency not affirmed, KV domain-qualified access undocumented, shared account key multi-instance distribution undocumented, clusters table partial with user_clusters correctly absorbed)
- **Gap**: 11 (single-cluster degenerate mode, separate helm charts, domain-qualified KV watches in SPA, direct+hub dual-connection in SPA, extended login response shape, AuthStore cluster accessors, SessionStore/EventStore/ConversationVM cluster routing, cluster admin HTTP endpoints, hub NATS config w/ JWT chain, worker NATS config, $SYS liveness detection, NATS layer security boundary, JWT $JS.*.API.> permission extension)
- **Contradicts ADR-0011** (living spec disagrees): 2 (spec-control-plane direct CR creation; spec-state-schema single-NATS config without leafnodes/JWT chain)
- **Bugs fixed**: 0
- **Bugs open**: 0

**Note on consolidation question**: The 11 gaps and 8 partials are almost entirely in the living specs (not yet reflecting ADR-0011's surviving decisions). Given that ADR-0011 is draft and its registry/RBAC sections are already superseded by ADR-0004, the remaining surviving decisions (leaf topology, JetStream domains, worker controller, dual-NATS SPA, hub NATS JWT auth) should either: (a) be folded into ADR-0004 as additional infrastructure decisions (extending ADR-0004's "ADR-0011 infrastructure topology preserved unchanged" acknowledgment into concrete spec text), or (b) promoted to accepted+implemented in ADR-0011 once the living specs are updated to match. Option (a) is simpler given the current trajectory.
