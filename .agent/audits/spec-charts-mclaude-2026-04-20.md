## Run: 2026-04-20T00:00:00Z

Component: `charts/mclaude`
Authoritative docs: `docs/adr-0024-typed-slugs.md` (accepted), `docs/spec-state-schema.md`, `docs/adr-0016-nats-security.md` (accepted)
Scope: `templates/nats-permissions-configmap.yaml`, `templates/slug-backfill-job.yaml`, `values.yaml`, `values-dev.yaml`, `values-e2e.yaml`, `templates/control-plane-deployment.yaml`, `templates/nats-configmap.yaml`

---

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0024:226-239 | SPA pub allow: `mclaude.users.{uslug}.>`, `_INBOX.>` | nats-permissions-configmap.yaml:39-41 | IMPLEMENTED | — | Exact match |
| adr-0024:226-239 | SPA sub allow: `mclaude.users.{uslug}.>`, `$KV.mclaude-sessions.>`, `$KV.mclaude-projects.>`, `$JS.API.DIRECT.GET.>`, `_INBOX.>` | nats-permissions-configmap.yaml:42-48 | IMPLEMENTED | — | Exact match |
| adr-0024:226-239 | SPA pub deny: `$KV.>`, `$JS.>`, `mclaude.system.>` | nats-permissions-configmap.yaml:48-51 | IMPLEMENTED | — | Exact match |
| adr-0024:241-243 | Control-plane: unchanged grants — full `mclaude.>`, `$KV.>`, `$JS.>`, `_INBOX.>`, `$SYS.ACCOUNT.>` pub+sub | nats-permissions-configmap.yaml:60-71 | IMPLEMENTED | — | Both pub.allow and sub.allow include all five entries |
| adr-0024:244-257 | Controller pub allow: `$KV.mclaude-projects.>`, `mclaude.clusters.{cslug}.>`, `_INBOX.>` | nats-permissions-configmap.yaml:89-92 | IMPLEMENTED | — | Exact match; ADR-0024 §KV scope note confirms broad `$KV.mclaude-projects.>` (not cluster-scoped) is correct |
| adr-0024:244-257 | Controller sub allow: `mclaude.clusters.{cslug}.api.>` | nats-permissions-configmap.yaml:93-94 | IMPLEMENTED | — | Exact match |
| adr-0024:244-257 | Controller pub deny: `mclaude.users.*.>`, `$KV.mclaude-sessions.>`, `$JS.>` | nats-permissions-configmap.yaml:95-98 | IMPLEMENTED | — | Exact match |
| adr-0024:260 | Session-agent signing key ceiling: `mclaude.users.*.projects.*.>` (replaces old `mclaude.*.sessions.{clusterId}.*.>`) | nats-permissions-configmap.yaml:105-106 | IMPLEMENTED | — | Exact match; old ceiling superseded per ADR-0024 Note |
| adr-0024:262-270 | Session-agent JWT pub allow: `mclaude.users.{uslug}.projects.{pslug}.events.>`, `mclaude.users.{uslug}.projects.{pslug}.lifecycle.>`, `_INBOX.>` | nats-permissions-configmap.yaml:120-123 | IMPLEMENTED | — | Exact match |
| adr-0024:262-270 | Session-agent JWT sub allow: `mclaude.users.{uslug}.projects.{pslug}.api.sessions.>`, `mclaude.users.{uslug}.projects.{pslug}.api.terminal.>` | nats-permissions-configmap.yaml:124-126 | IMPLEMENTED | — | Exact match |
| adr-0024:278 | Controller KV grant broad `$KV.mclaude-projects.>` (not cluster-scoped per scope note) | nats-permissions-configmap.yaml:90 | IMPLEMENTED | — | ADR-0024 explicitly preserves broad grant until future multi-cluster KV-partitioning ADR |
| adr-0024:125-128 | charts/mclaude: NATS permission templates + backfill migration Job | nats-permissions-configmap.yaml, slug-backfill-job.yaml | IMPLEMENTED | — | Both templates present |
| adr-0024:156 | Backfill binary: `cmd/slug-backfill`, run between ADD COLUMN and SET NOT NULL | slug-backfill-job.yaml:60 | IMPLEMENTED | — | Command `/usr/local/bin/slug-backfill` matches spec's binary name |
| adr-0024:18-19 | Comments/hook: runs BEFORE control-plane rollout (pre-install, pre-upgrade) | slug-backfill-job.yaml:33 | IMPLEMENTED | — | Hook annotations: `pre-install,pre-upgrade` |
| adr-0024:19 | Idempotent: re-runs are no-ops | slug-backfill-job.yaml:14-16 | IMPLEMENTED | — | Comment documents sentinel check `SELECT 1 FROM users WHERE slug IS NOT NULL LIMIT 1`; idempotency is binary behavior, not chart behavior — chart correctly documents it |
| adr-0024:156 | Backfill program run inside a Go program from `cmd/slug-backfill` in control-plane image | slug-backfill-job.yaml:56-60 | IMPLEMENTED | — | Uses `include "mclaude.image" ... .Values.controlPlane.image`; command `/usr/local/bin/slug-backfill` |
| adr-0024:backfill | DATABASE_URL and NATS credentials passed to backfill Job (needs Postgres + NATS for KV rekeying) | slug-backfill-job.yaml:62-79 | IMPLEMENTED | — | `DATABASE_URL`, `NATS_URL`, `NATS_OPERATOR_JWT`, `NATS_OPERATOR_SEED` all present |
| spec-state-schema:85 | KV bucket keys use typed slugs, separator `.` uniformly | nats-permissions-configmap.yaml (grants reference `{uslug}.{pslug}` pattern); backfill job handles rekeying | IMPLEMENTED | — | Permission grants and backfill job are consistent with slug-based key format |
| adr-0016:44-45 | K8s/BYOH controller grants in ADR-0016: `mclaude.clusters.{clusterId}.>` and `$KV.mclaude-projects.{clusterId}.>` (cluster-scoped KV) | nats-permissions-configmap.yaml:89-98 | GAP | SPEC→FIX | ADR-0016 specifies `$KV.mclaude-projects.{clusterId}.>` (cluster-scoped). ADR-0024 explicitly supersedes this: controller gets broad `$KV.mclaude-projects.>`. ADR-0024 §KV scope note says this is intentional pre-existing drift that will be resolved in a future multi-cluster ADR. Since ADR-0024 is the newer accepted ADR and explicitly addresses this, the code follows ADR-0024. ADR-0016's cluster-scoped KV grant text is superseded but not formally marked as such. |
| adr-0016:46-47 | Session-agent ceiling in ADR-0016: `mclaude.*.sessions.{clusterId}.*.>` | nats-permissions-configmap.yaml:105-106 | GAP | SPEC→FIX | ADR-0016 specifies old per-session ceiling. ADR-0024 explicitly replaces it with `mclaude.users.*.projects.*.>`. Code follows ADR-0024. ADR-0016 text is superseded on this point by the newer ADR-0024. |
| values.yaml:255-268 | slugBackfill stanza: enabled=true, backoffLimit=0, activeDeadlineSeconds=300 | values.yaml:255-268 | IMPLEMENTED | — | Present and correct; backoffLimit=0 matches spec rationale (deterministic, human intervention on failure) |
| values-dev.yaml:54-57 | Dev: slugBackfill.enabled=false | values-dev.yaml:54-57 | IMPLEMENTED | — | Correct — dev clusters start fresh, no existing UUID-keyed data |
| values-e2e.yaml:88-90 | E2E: slugBackfill.enabled=false | values-e2e.yaml:88-90 | IMPLEMENTED | — | Correct — python stub has no real slug-backfill binary |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| nats-configmap.yaml:1-57 | INFRA | NATS server config (ports, JetStream, WebSocket, max_payload, server_name). Specified by spec-state-schema.md §NATS Server Configuration. Comments referencing ADR-0024 typed-slug subject scheme are accurate documentation. |
| nats-statefulset.yaml:1-91 | INFRA | NATS StatefulSet with persistence, probes, volume mounts. Necessary infrastructure to run NATS per spec-state-schema §NATS Server Configuration. |
| nats-services.yaml:1-50 | INFRA | ClusterIP service for NATS (client, websocket, monitor ports) and headless service for StatefulSet DNS. Necessary infrastructure. |
| nats-ws-ingress.yaml:1-43 | INFRA | Separate Ingress for NATS WebSocket on dedicated subdomain. Necessary infrastructure per spec-state-schema §NATS Server Configuration (websocket.port: 8080). Uses `*.mclaude.internal`-compatible hosts — no sslip.io. |
| control-plane-deployment.yaml:1-145 | INFRA | Control-plane Deployment with env vars, probes, optional migrations init container, optional providers volume. All env vars correspond to spec-state-schema and adr-0003 control-plane spec. |
| session-agent-pod-template.yaml:1-26 | INFRA | ConfigMap for session-agent pod template parameters. Specified by spec-state-schema §ConfigMap: `{release}-session-agent-template`. |
| ingress.yaml:1-61 | INFRA | Main Ingress routing /auth, /api, /scim → control-plane and / → SPA. Standard K8s infrastructure. HTTP routes align with ADR-0024 HTTP URL inventory. |
| templates/clusterrole.yaml | INFRA | RBAC ClusterRole for control-plane cross-namespace operations. K8s plumbing for reconciler. |
| templates/clusterrolebinding.yaml | INFRA | RBAC ClusterRoleBinding binding ClusterRole to ServiceAccount. K8s plumbing. |
| templates/serviceaccount.yaml | INFRA | ServiceAccount for control-plane. K8s plumbing. |
| templates/namespace.yaml | INFRA | Namespace creation (mclaude-system) when namespace.create=true. K8s plumbing. |
| templates/mcproject-crd.yaml | INFRA | MCProject CRD. Specified by spec-state-schema §CRD: `MCProject`. |
| templates/postgres-statefulset.yaml | INFRA | PostgreSQL StatefulSet. Necessary infrastructure per spec-state-schema §Postgres. |
| templates/postgres-service.yaml | INFRA | PostgreSQL Service. K8s plumbing for Postgres connectivity. |
| templates/spa-deployment.yaml | INFRA | SPA Deployment. Spec-driven by SPA component spec. |
| templates/spa-service.yaml | INFRA | SPA Service. K8s plumbing. |
| templates/provider-config-configmap.yaml | INFRA | OAuth provider config ConfigMap. Spec-driven by adr-0007 (GitHub OAuth). |
| templates/tests/test-smoke.yaml | INFRA | Helm test pod (smoke test). UNSPEC'd in design docs but standard Helm practice for chart verification. |
| values-airgap.yaml | INFRA | Air-gap image registry overrides. Operational configuration, no spec requirement. |
| values-aks.yaml | INFRA | AKS-specific resource/storage class overrides. Operational configuration, no spec requirement. |
| values-k3d-ghcr.yaml | INFRA | k3d local-preview values with mclaude.local DNS (not sslip.io). Operational configuration, no spec requirement. DNS uses `*.mclaude.local` — compliant with no-external-DNS rule. |
| Chart.yaml | INFRA | Helm chart metadata. |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| adr-0024:226-239 | SPA NATS permission grants (pub/sub/deny) | None — Helm chart has no unit tests | test-smoke.yaml (smoke only, not permission-specific) | UNTESTED |
| adr-0024:260 | Session-agent signing key ceiling `mclaude.users.*.projects.*.>` | None | None | UNTESTED |
| adr-0024:244-257 | Controller grants | None | None | UNTESTED |
| adr-0024:125-128 | Backfill Job pre-install/pre-upgrade hook | None | None | UNTESTED |

Note: Helm chart templates have no unit test framework (e.g., helm unittest) configured. The only test is the smoke test pod in `templates/tests/test-smoke.yaml`, which checks HTTP reachability but not NATS permission grant content or backfill job execution.

### Phase 4 — Bug Triage

No open bugs in `.agent/bugs/` have `Component: charts/mclaude` or reference the Helm chart. No bugs to triage.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | — | — | No helm-related bugs open |

### Summary

- Implemented: 19
- Gap: 2 (both SPEC→FIX — ADR-0016 text superseded by ADR-0024)
- Partial: 0
- Infra: 20
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 0
- E2E only: 0
- Untested: 4 (no helm-unittest framework; smoke test only)
- Bugs fixed: 0
- Bugs open: 0
