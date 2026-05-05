## Run: 2026-05-05T00:00:00Z

Component: `charts/mclaude-postgres`
Specs: `docs/charts-mclaude/spec-helm.md`, `docs/adr-0094-split-postgres-chart.md`

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-helm.md:174 | StatefulSet `{fullname}` — single replica, image `postgres:16-alpine`, runs as UID 999, VolumeClaimTemplate `data` (RWO) when `persistence.enabled` | statefulset.yaml:11,30,23-27,89-101 | IMPLEMENTED | — | replicas, image, UID 999 security context, VolumeClaimTemplate all present |
| spec-helm.md:174 | Env: `POSTGRES_DB={{ database.name | default "mclaude" }}`, `POSTGRES_USER={{ database.user | default "mclaude" }}`, `POSTGRES_PASSWORD` from `existingSecret` key `postgres-password` | statefulset.yaml:46-54 | IMPLEMENTED | — | All three env vars present with correct sources |
| spec-helm.md:175 | Service `{fullname}` — ClusterIP, port 5432 | services.yaml:1-19 | IMPLEMENTED | — | ClusterIP service on port 5432 |
| spec-helm.md:176 | Service `{fullname}-headless` — headless, for StatefulSet DNS | services.yaml:21-41 | IMPLEMENTED | — | `clusterIP: None` headless service |
| adr-0094:95 / spec-helm.md:184 | `image.repository` default `postgres` | values.yaml:9 | IMPLEMENTED | — | |
| adr-0094:95 / spec-helm.md:184 | `image.tag` default `16-alpine` | values.yaml:10 | IMPLEMENTED | — | |
| adr-0094:107 / spec-helm.md:184 | `replicas` default `1` | values.yaml:18 | IMPLEMENTED | — | |
| adr-0094:103 / spec-helm.md:185 | `persistence.enabled` default `true` | values.yaml:25 | IMPLEMENTED | — | |
| adr-0094:104 / spec-helm.md:186 | `persistence.size` default `20Gi` | values.yaml:27 | IMPLEMENTED | — | |
| adr-0094:105 / spec-helm.md:187 | `persistence.storageClass` default `""` (cluster default) | values.yaml:29 | IMPLEMENTED | — | |
| adr-0094:106 / spec-helm.md:188 | `existingSecret` default `""` — **Required**; chart fails fast if empty | statefulset.yaml:1 | IMPLEMENTED | — | `required` Helm function used |
| spec-helm.md:189 | `database.user` default `mclaude` | values.yaml:52 | IMPLEMENTED | — | |
| spec-helm.md:190 | `database.name` default `mclaude` | values.yaml:53 | IMPLEMENTED | — | |
| spec-helm.md:186 | `persistence.enabled false` → `emptyDir` (dev/CI only) | statefulset.yaml:84-88 | IMPLEMENTED | — | `emptyDir` block rendered when persistence disabled |
| adr-0094:109 / spec-helm.md:198 | Chart does not generate password — pre-existing Secret is the canonical creator | statefulset.yaml:1, values.yaml:44 | IMPLEMENTED | — | `required` guard + no password generation logic |
| spec-helm.md:215-219 | `values-aks.yaml` — `persistence.storageClass: managed-csi-premium` | values-aks.yaml:4 | IMPLEMENTED | — | |
| spec-helm.md:215-219 | `values-e2e.yaml` — `persistence.enabled: false` | values-e2e.yaml:4 | IMPLEMENTED | — | |
| spec-helm.md:164 | Self-hosted: skip entirely on managed-DB deployments (deploy only mclaude-cp) | Chart.yaml, NOTES.txt | IMPLEMENTED | — | Chart description says "Skip entirely when using a managed database"; this is a deployment convention, not code |
| adr-0094:93 | Headless service name is `{fullname}-headless` and StatefulSet `serviceName` references it | statefulset.yaml:12, services.yaml:25 | IMPLEMENTED | — | StatefulSet `serviceName` matches headless service name |
| spec-helm.md:196 | Pre-requisite Secret table — `mclaude-postgres` (default), `postgres-password` key, created by operator/CI | values.yaml:38-44, NOTES.txt:12 | IMPLEMENTED | — | NOTES.txt documents the Secret contract |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| values.yaml:11 | UNSPEC'd | `image.pullPolicy: IfNotPresent` — not listed in spec-helm.md or ADR-0094 values table |
| values.yaml:56-63 | UNSPEC'd | `resources` block (cpu/memory requests and limits for Postgres pod) — not in spec-helm.md or ADR-0094 values table |
| values.yaml:66-69 | UNSPEC'd | `namespace.name: mclaude-system` knob — not in spec-helm.md or ADR-0094 values table; spec only documents `existingSecret`, `image.*`, `replicas`, `persistence.*`, `database.*` |
| values-aks.yaml:7-14 | UNSPEC'd | `resources` overrides in values-aks.yaml — no resource knob in spec at all |
| _helpers.tpl:1-57 | INFRA | Standard Helm helper template macros (`fullname`, `chart`, `labels`, `selectorLabels`, `namespace`). Necessary chart infrastructure. |
| NOTES.txt:1-13 | INFRA | Post-install usage notes. Required Helm boilerplate. |
| statefulset.yaml:32-40 | UNSPEC'd | Container-level security context (`allowPrivilegeEscalation: false`, `readOnlyRootFilesystem: false`, `capabilities.drop: [ALL]`) — spec only mandates `runAsUser: 999`; the additional hardening fields are not mentioned |
| statefulset.yaml:55 | UNSPEC'd | `PGDATA=/var/lib/postgresql/data/pgdata` env var — not in spec or ADR; it is a Postgres operational best practice to avoid a lost+found conflict but the spec doesn't describe it |
| statefulset.yaml:60-81 | UNSPEC'd | Liveness and readiness probes (`pg_isready`) — not described in spec-helm.md or ADR-0094 |
| Chart.yaml:1-17 | INFRA | Standard Helm chart metadata |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| spec-helm.md:174 | StatefulSet — single replica, postgres:16-alpine, UID 999, VolumeClaimTemplate | None found | None found | UNTESTED | No helm template unit tests (e.g. `helm template` + assertions) and no integration test suite for this chart |
| spec-helm.md:175 | Service `{fullname}` ClusterIP port 5432 | None found | None found | UNTESTED | |
| spec-helm.md:176 | Service `{fullname}-headless` headless | None found | None found | UNTESTED | |
| spec-helm.md:188 | `existingSecret` required — fails fast if empty | None found | None found | UNTESTED | The `required` guard is untested; no `helm template --set existingSecret=` positive/negative test |
| spec-helm.md:185-188 | persistence.enabled=false → emptyDir | None found | None found | UNTESTED | |
| adr-0094 Integration Test Cases | Fresh install (mclaude-postgres then mclaude-cp) | None found | None found | UNTESTED | ADR lists 5 integration test cases; none are implemented |
| adr-0094 Integration Test Cases | Bootstrap idempotence on re-upgrade | None found | None found | UNTESTED | |
| adr-0094 Integration Test Cases | Bootstrap idempotence on password rotation | None found | None found | UNTESTED | |
| adr-0094 Integration Test Cases | Managed-DB shape (no mclaude-postgres release) | None found | None found | UNTESTED | |
| adr-0094 Integration Test Cases | Wrong install order — must fail fast | None found | None found | UNTESTED | |

### Phase 4 — Bug Triage

No open bugs have `**Component**: charts/mclaude-postgres` (or charts-mclaude). All four open bugs target `spa` or `Droid-Shield`. No bug triage action needed.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| 002-droid-shield | Droid-Shield False Positives | N/A | Component: Droid-Shield, not mclaude-postgres |
| 002-ptt | PTT race condition | N/A | Component: spa |
| 003-initial-message | First-run message race | N/A | Component: spa |
| 005-getting-started | Getting Started canned text | N/A | Component: spa |

### Summary

- Implemented: 19
- Gap: 0
- Partial: 0
- Infra: 4
- Unspec'd: 7
- Dead: 0
- Tested: 0
- Unit only: 0
- E2E only: 0
- Untested: 10
- Bugs fixed: 0
- Bugs open: 0
