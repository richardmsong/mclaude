## Run: 2026-05-04T00:00:00Z

Component: charts/mclaude-cp
Spec: docs/charts-mclaude/spec-helm.md
ADR: docs/adr-0089-cp-integration-test-suite.md

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0089:243 | Remove `adminToken`, `devSeed`, `ADMIN_TOKEN`, `ADMIN_PORT`, `DEV_SEED` from `values.yaml`, `values-dev.yaml`, `values-k3d-ghcr.yaml`, and `templates/control-plane-deployment.yaml` | values.yaml, values-dev.yaml, values-k3d-ghcr.yaml, control-plane-deployment.yaml (entire files) | IMPLEMENTED | — | grep confirms zero occurrences of ADMIN_TOKEN, ADMIN_PORT, DEV_SEED, adminToken, adminPort, devSeed in all of charts/mclaude-cp/ |
| adr-0089:244 | Add `bootstrapAdminPassword` value (required); injected as `BOOTSTRAP_ADMIN_PASSWORD` env var on the init-keys Job | values.yaml:115, init-keys-job.yaml:56-59 | IMPLEMENTED | — | Value present in values.yaml with empty default; conditionally injected in init-keys-job.yaml when non-empty |
| spec-helm:59 | Service `{release}-control-plane`: ClusterIP. Port: http (8080). `/metrics` is served on the same port, unauthenticated. | control-plane-service.yaml:15-19 | IMPLEMENTED | — | Service exposes only one port named http at 8080. No port 9091 anywhere in the file. |
| spec-helm:58 | Deployment `{release}-control-plane`: single-port, no admin container port | control-plane-deployment.yaml:57-60 | IMPLEMENTED | — | Only one containerPort declared: `http` at .Values.controlPlane.service.httpPort (default 8080). No second admin or metrics port. |
| spec-helm:114 | `controlPlane.bootstrapAdminEmail`: First admin email. init-keys Job creates users row with is_admin=true and bcrypt password hash from bootstrapAdminPassword. | values.yaml:96, init-keys-job.yaml:52-55 | IMPLEMENTED | — | Value present; conditionally injected as BOOTSTRAP_ADMIN_EMAIL in init-keys job when non-empty |
| spec-helm:115 | `controlPlane.bootstrapAdminPassword`: Password for bootstrap admin. Hashed by init-keys, stored in users.password_hash. If empty, admin can only log in via OAuth. | values.yaml:115, init-keys-job.yaml:56-59 | IMPLEMENTED | — | Value present in values.yaml with empty string default; conditionally injected as BOOTSTRAP_ADMIN_PASSWORD in init-keys job when non-empty |
| spec-helm:157 | Pre-requisite Secrets: `mclaude-control-plane` no longer requires an `admin-token` key | control-plane-deployment.yaml (full) | IMPLEMENTED | — | No reference to admin-token key anywhere in control-plane deployment or service templates |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| control-plane-deployment.yaml:101-104 | INFRA | BOOTSTRAP_ADMIN_EMAIL injected on the Deployment as well as the init-keys Job. spec-helm.md lists the value under controlPlane configuration and spec-control-plane.md:42 lists BOOTSTRAP_ADMIN_EMAIL as a recognized (optional) env var for the CP binary — so this is consistent with the CP spec, not unspec'd. |
| init-keys-job.yaml:56-59 | INFRA | Conditional injection of BOOTSTRAP_ADMIN_PASSWORD in the init-keys Job only when non-empty. Matches spec intent exactly. |

### Phase 3 — Test Coverage

No Helm chart unit tests (helm unittest) exist in charts/mclaude-cp/tests/ for these specific values. The tests/test-smoke.yaml is a runtime integration hook, not a values-rendering unit test.

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| adr-0089:243 | ADMIN_TOKEN/ADMIN_PORT/DEV_SEED removal | — | — | UNTESTED |
| adr-0089:244 | bootstrapAdminPassword injected in init-keys | — | — | UNTESTED |
| spec-helm:59 | Service port 8080 only, no admin port | — | — | UNTESTED |
| spec-helm:114-115 | bootstrapAdminEmail + bootstrapAdminPassword values and injection | — | — | UNTESTED |

### Phase 4 — Bug Triage

No open bugs in .agent/bugs/ reference the charts/mclaude-cp component.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| — | — | — | No matching bugs |

### Summary

- Implemented: 7
- Gap: 0
- Partial: 0
- Infra: 2
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 0
- E2E only: 0
- Untested: 4
- Bugs fixed: 0
- Bugs open: 0
