## Run: 2026-05-02T00:00:00Z

Component: mclaude-worker (charts/mclaude-worker/)
Spec: docs/charts-mclaude/spec-helm.md
ADRs in scope: ADR-0074 (accepted), ADR-0073 (implemented)

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-helm.md:197 | "Job {release}-gen-host-nkey — Pre-install and pre-upgrade hook (weight -10). Generates NKey pair via nkeys.CreateUser() (U-prefix). Writes decorated seed string to Secret {release}-host-creds field nkey_seed. Idempotent — skips if Secret exists." | gen-host-nkey-job.yaml:59-102; gen_host_nkey.go:65-131 | IMPLEMENTED | — | Job hook weight -10 (line 69). Pre-install,pre-upgrade hooks present. nkeys.CreateUser() called (gen_host_nkey.go:80). Seed written to nkey_seed (line 112). Idempotent GET-then-skip at lines 67-77. |
| spec-helm.md:197 | "Creates Secret with helm.sh/resource-policy: keep so the seed survives helm uninstall." | gen_host_nkey.go:106-108 | IMPLEMENTED | — | Annotation "helm.sh/resource-policy": "keep" set in ObjectMeta.Annotations. |
| spec-helm.md:198 | "Secret {release}-host-creds — Created exclusively by the gen-host-nkey Job (not a Helm chart template)." | charts/mclaude-worker/templates/ — no host-creds-secret.yaml present | IMPLEMENTED | — | host-creds-secret.yaml has been deleted. Job is the sole creator via K8s API in gen_host_nkey.go:114-115. |
| spec-helm.md:198 | "Single field: nkey_seed. Annotated helm.sh/resource-policy: keep — persists across helm uninstall." | gen_host_nkey.go:111-113; line 107 | IMPLEMENTED | — | Data map contains exactly one key "nkey_seed". Annotation present. |
| spec-helm.md:198 | "JWT is not stored here — acquired in-memory via challenge-response on boot." | gen_host_nkey.go:99-113 (Secret data) | IMPLEMENTED | — | Secret Data contains only nkey_seed; no JWT field written. |
| spec-helm.md:204 | "Deployment {release}-controller — Mounts {release}-host-creds at /etc/mclaude/host-creds/. Env: HOST_NKEY_SEED_PATH=/etc/mclaude/host-creds/nkey_seed." | controller-deployment.yaml:39-40, 55-58, 76-79 | IMPLEMENTED | — | volumeMount mountPath=/etc/mclaude/host-creds at line 57, HOST_NKEY_SEED_PATH at line 40, volume references {release}-host-creds Secret at line 78. |
| ADR-0074 (Component Changes) | "Delete charts/mclaude-worker/templates/host-creds-secret.yaml entirely. The gen-host-nkey Job is the sole creator of the {release}-host-creds Secret." | charts/mclaude-worker/templates/ listing | IMPLEMENTED | — | File does not exist in templates directory. Verified via ls. |
| ADR-0074 (Component Changes) | "Add annotations to the Secret when creating it: helm.sh/resource-policy: keep — prevents Helm from deleting the Secret during helm uninstall or when the template is removed." | gen_host_nkey.go:96-108 | IMPLEMENTED | — | Annotation added to ObjectMeta.Annotations with comment referencing ADR-0074. |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| gen_host_nkey.go:1-15 | INFRA | Package declaration and imports — standard plumbing. |
| gen_host_nkey.go:32-61 | INFRA | runGenHostNkey(): reads env vars, builds in-cluster K8s client, calls testable core. All spec-described behavior. |
| gen_host_nkey.go:117-121 | INFRA | Race-condition AlreadyExists guard on Secret Create — necessary error handling for the idempotent path. Not explicitly enumerated in spec but required for correct idempotency guarantee. |
| gen-host-nkey-job.yaml:7-57 | INFRA | ServiceAccount, Role, RoleBinding for the gen-host-nkey Job pod — necessary RBAC allowing Secret get/create. Implied by Job needing Secret write access; not individually itemized in spec. |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| spec-helm.md:197 / ADR-0073 | gen-host-nkey Job generates NKey (U-prefix), writes nkey_seed field | gen_host_nkey_test.go:TestGenHostNkey_CreatesSecret, TestGenHostNkey_UniqueKeysPerInstall, TestGenHostNkey_SeedRoundTrip | CI deploy-main.yml (not a committed test file) | UNIT_ONLY | 8 unit tests in gen_host_nkey_test.go exercise all paths. No in-repo Helm integration test for the Job. The Helm smoke test (test-smoke.yaml) only probes the controller health endpoint. |
| spec-helm.md:197 / ADR-0074 | helm.sh/resource-policy: keep annotation on created Secret | gen_host_nkey_test.go:TestGenHostNkey_SecretHasResourcePolicyKeepAnnotation (line 190-208) | None | UNIT_ONLY | Dedicated unit test for ADR-0074 annotation requirement. No integration test for helm-uninstall-preserves-Secret scenario. |
| spec-helm.md:197 | Idempotent — skips if Secret exists | gen_host_nkey_test.go:TestGenHostNkey_Idempotent, TestGenHostNkey_SkipLogOnExisting | None | UNIT_ONLY | Both idempotency paths tested in unit tests. |
| spec-helm.md:198 | Secret created exclusively by Job, not by chart template | No automated guard — verified manually by file listing | None | UNTESTED | No test prevents re-introduction of host-creds-secret.yaml. A Helm lint or helm template | grep test could assert no chart-template-rendered Secret named {release}-host-creds exists. |

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | No open bugs with Component matching mclaude-worker, gen-host-nkey, or host-creds | — | grep over .agent/bugs/*.md found no matches. |

### Summary

- Implemented: 8
- Gap: 0
- Partial: 0
- Infra: 4
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 3
- E2E only: 0
- Untested: 1
- Bugs fixed: 0
- Bugs open: 0
