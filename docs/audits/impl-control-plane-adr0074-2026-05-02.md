## Run: 2026-05-02T00:00:00Z

**Component:** mclaude-control-plane (ADR-0074 scope)
**Component root:** mclaude-control-plane/
**Spec files:** docs/mclaude-control-plane/spec-control-plane.md, docs/charts-mclaude/spec-helm.md
**ADRs evaluated:** ADR-0073 (implemented), ADR-0074 (accepted)
**Focus:** gen_host_nkey.go creates Secret with `helm.sh/resource-policy: keep` annotation; gen_host_nkey_test.go tests for it.

---

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0074:33 | `helm.sh/resource-policy: keep` annotation — add to Job-created Secret | gen_host_nkey.go:106-108 | IMPLEMENTED | — | `secret.Annotations = map[string]string{"helm.sh/resource-policy": "keep"}` |
| adr-0074:33 | When `host-creds-secret.yaml` is removed from templates, Helm would try to delete the Secret — annotation prevents deletion | charts/mclaude-worker/templates/ (no host-creds-secret.yaml present) | IMPLEMENTED | — | File does not exist in templates directory; template list confirmed: gen-host-nkey-job.yaml, clusterrole.yaml, etc. — no host-creds-secret.yaml |
| adr-0074:40 | Delete `charts/mclaude-worker/templates/host-creds-secret.yaml` entirely | charts/mclaude-worker/templates/ | IMPLEMENTED | — | Directory listing confirms file is absent |
| adr-0074:42-49 | Add annotations to Secret: `"helm.sh/resource-policy": "keep"` | gen_host_nkey.go:106-108 | IMPLEMENTED | — | Exact annotation key/value present in ObjectMeta.Annotations |
| spec-helm.md:197 | Job creates Secret with `helm.sh/resource-policy: keep` so seed survives `helm uninstall` | gen_host_nkey.go:106-108 | IMPLEMENTED | — | Annotation applied on Secret creation |
| spec-helm.md:198 | Secret `{release}-host-creds` — annotated `helm.sh/resource-policy: keep` — persists across `helm uninstall`. Created exclusively by the gen-host-nkey Job (not a Helm chart template). | gen_host_nkey.go:99-114; charts/ (no host-creds-secret.yaml) | IMPLEMENTED | — | Secret created only by Job; no chart template for same Secret |
| adr-0073:58-66 | `genHostNkeyWithClient`: check existing Secret, skip if exists (idempotent) | gen_host_nkey.go:67-77 | IMPLEMENTED | — | GET then IsNotFound check |
| adr-0073:60 | `nkeys.CreateUser()` for U-prefix key | gen_host_nkey.go:80 | IMPLEMENTED | — | |
| adr-0073:61 | `kp.Seed()` | gen_host_nkey.go:85-88 | IMPLEMENTED | — | |
| adr-0073:62 | `kp.PublicKey()` | gen_host_nkey.go:90-93 | IMPLEMENTED | — | |
| adr-0073:63 | `Data: map[string][]byte{"nkey_seed": seed}` | gen_host_nkey.go:111-113 | IMPLEMENTED | — | |
| adr-0073:64 | Log public key: `logger.Info().Str("nkeyPublic", pub).Msg("generated host NKey")` | gen_host_nkey.go:125-130 | IMPLEMENTED | — | |
| adr-0073:68 | `rest.InClusterConfig()` failure → fatal | gen_host_nkey.go:49-52 | IMPLEMENTED | — | |
| adr-0073:69 | `HOST_CREDS_SECRET` empty → fatal | gen_host_nkey.go:41-43 | IMPLEMENTED | — | |
| adr-0073:70 | Secret GET non-404 error → fatal | gen_host_nkey.go:75-77 | IMPLEMENTED | — | `return err` propagates to `logger.Fatal()` in runGenHostNkey |
| adr-0073:71 | `nkeys.CreateUser()` or `kp.Seed()` error → fatal | gen_host_nkey.go:82-93 | IMPLEMENTED | — | errors returned and caught by runGenHostNkey:59 |
| adr-0073:72 | Secret CREATE fails with AlreadyExists (race) → log and exit 0 | gen_host_nkey.go:117-121 | IMPLEMENTED | — | |
| adr-0073:73 | Other CREATE error → fatal | gen_host_nkey.go:122-123 | IMPLEMENTED | — | `return err` → fatal |
| spec-control-plane.md:202-203 | control-plane binary also doubles as two Helm pre-install Job entrypoints; `gen-host-nkey` is second | main.go:37-40 | IMPLEMENTED | — | Subcommand dispatch present immediately after init-keys block |

---

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| gen_host_nkey.go:1-15 | INFRA | Package declaration and imports — all necessary for K8s client, NKey, logging, and context |
| gen_host_nkey.go:17-61 | INFRA | `runGenHostNkey()` — entrypoint that builds the in-cluster client and logger; calls testable core. Necessary plumbing described by ADR-0073's component changes section |
| gen_host_nkey.go:45 | INFRA | 90-second context timeout — unspec'd exact value but reasonable operational guard; ADR-0073 does not prescribe a timeout value |
| gen_host_nkey.go:103-105 | INFRA | `app.kubernetes.io/managed-by: mclaude-gen-host-nkey` label — not mentioned in ADR-0074 but is a standard K8s ownership label; not dead code, serves observability |

---

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| adr-0074:33 / spec-helm.md:197-198 | Secret carries `helm.sh/resource-policy: keep` | gen_host_nkey_test.go:190-208 `TestGenHostNkey_SecretHasResourcePolicyKeepAnnotation` | None | UNIT_ONLY | Critical path: annotation determines whether Helm deletes the host-identity Secret on uninstall. ADR-0074 integration test cases require a real `helm uninstall` run to verify the Secret survives. |
| adr-0073:58-66 / idempotency | Skips if Secret exists | gen_host_nkey_test.go:58-92 `TestGenHostNkey_Idempotent` | None | UNIT_ONLY | Integration bar applies — pre-upgrade hook idempotency requires a real K8s cluster to verify end-to-end |
| adr-0073:60-63 / NKey creation | Creates valid U-prefix NKey, writes `nkey_seed` | gen_host_nkey_test.go:24-56 `TestGenHostNkey_CreatesSecret` | None | UNIT_ONLY | |
| adr-0073:64 | Logs public key | gen_host_nkey_test.go:118-136 `TestGenHostNkey_LogsPublicKey` | None | UNIT_ONLY | |
| adr-0073:72 | AlreadyExists race → idempotent exit | Not explicitly tested (race path) | None | UNTESTED | No test exercises the Create-returns-AlreadyExists branch (gen_host_nkey.go:117-121). Would require injecting a fake that returns AlreadyExists on Create. |
| adr-0073 integration cases | Worker Helm upgrade succeeds end-to-end | — | None | UNTESTED | ADR-0073 lists "Worker Helm upgrade succeeds" and "Fresh install (no prior Secret)" as integration test cases; no integration test implementation found |
| adr-0074 integration cases | `helm uninstall` keeps Secret (resource-policy annotation) | — | None | UNTESTED | ADR-0074 integration test case; no test implementation found |

---

### Phase 4 — Bug Triage

No open bugs in `.agent/bugs/` have `**Component**: mclaude-control-plane`. Bugs present (002-droid-shield-false-positives.md, 002-ptt-race-condition.md, 003-initial-message-injection-race.md, 005-getting-started-canned-text.md) target web/SPA components, not control-plane.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | — | — | No control-plane bugs in .agent/bugs/ |

---

### Summary

- Implemented: 19
- Gap: 0
- Partial: 0
- Infra: 4
- Unspec'd: 0
- Dead: 0
- Tested (unit+integration): 0
- Unit only: 5
- E2E only: 0
- Untested: 2
- Bugs fixed: 0
- Bugs open: 0
