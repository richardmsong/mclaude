## Run: 2026-05-05T00:00:00Z

Component: charts/mclaude-cp
Spec files: docs/charts-mclaude/spec-helm.md
Cross-cutting ADRs evaluated: adr-0035, adr-0067, adr-0068, adr-0070, adr-0071, adr-0072, adr-0081, adr-0087, adr-0088, adr-0091, adr-0094
Skipped ADRs (draft/superseded): adr-0054 (draft), adr-0083 (superseded)

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-helm.md:3-11 | Chart purpose table: `mclaude-cp` = hub NATS, MinIO, control-plane, SPA, operator/account NKey bootstrap. Connects to Postgres via `database.host` + `database.passwordSecretRef`. | Chart.yaml:1-16, templates/* (all), values.yaml:1-251 | IMPLEMENTED | | Chart description and template set match exactly. |
| spec-helm.md:21 | `helm install mclaude-cp charts/mclaude-cp -n mclaude-system`. Idempotent — safe to re-run for upgrades. | Chart.yaml, templates/init-keys-job.yaml:17 (pre-install,pre-upgrade), templates/minio-bucket-job.yaml:11 (post-install,post-upgrade) | IMPLEMENTED | | Hook delete policies ensure idempotency. |
| spec-helm.md:29 | Job `mclaude-cp-init-keys`, Helm pre-install hook weight `-20`, runs `control-plane init-keys`, idempotent if Secret already exists | init-keys-job.yaml:10 (name with Revision), :17-18 (hook/weight), :38 (command) | PARTIAL | SPEC→FIX | Job name is `{fullname}-init-keys-{.Release.Revision}` (has revision suffix), not `mclaude-cp-init-keys`. Spec says fixed name; code appends revision to avoid name collision on re-run. Code approach is practical. Spec should say `{release}-init-keys-{revision}`. |
| spec-helm.md:29 | Job idempotent: if Secret already exists, exits without regenerating | init-keys-job.yaml:38 (command `control-plane init-keys` — idempotency is in Go binary, not chart) | IMPLEMENTED | | Chart correctly delegates idempotency to the binary. |
| spec-helm.md:29 | Creates bootstrap admin row in Postgres when `controlPlane.bootstrapAdminEmail` is set | init-keys-job.yaml:51-58 (env BOOTSTRAP_ADMIN_EMAIL, BOOTSTRAP_ADMIN_PASSWORD) | IMPLEMENTED | | Both env vars conditionally set. |
| spec-helm.md:30 | Secret `mclaude-system/operator-keys`, type Opaque, keys `operatorJwt`, `operatorSeed`, `accountJwt`, `accountSeed`. Hub NATS + control-plane mount it. | nats-statefulset.yaml:88-90 (operator-keys volume), control-plane-deployment.yaml:84-85 (NATS_ACCOUNT_SEED from accountSeed), :145-148 (operator-keys volume) | IMPLEMENTED | | Secret created by init-keys binary (not chart template), which is correct per spec. |
| spec-helm.md:38-41 | StatefulSet `{release}-nats`, single replica (configurable), VolumeClaimTemplate `data` when persistence enabled | nats-statefulset.yaml:5 (name), :11 (.Values.nats.replicas), :95-107 (VolumeClaimTemplates conditional on persistence) | IMPLEMENTED | | |
| spec-helm.md:38-41 | Service `{release}-nats`, ClusterIP, ports: client (4222), websocket (8080), monitor (8222), leafnodes (7422) | nats-services.yaml:6-32 | IMPLEMENTED | | All four ports present. |
| spec-helm.md:38-41 | Service `{release}-nats-headless`, headless for StatefulSet DNS | nats-services.yaml:34-54 | IMPLEMENTED | | |
| spec-helm.md:41 | ConfigMap `{release}-nats-config`, `nats.conf` with operator, `resolver: nats`, `resolver_preload`, JetStream domain `hub`, `resolver: nats` enables JWT revocation (ADR-0054) | nats-configmap.yaml:47-51 | GAP | SPEC→FIX | Code uses `resolver: MEMORY` (line 48). Spec says `resolver: nats`. However ADR-0054 (which mandates the change to `resolver: nats`) is **draft** status — not authoritative. The spec-helm.md references ADR-0054 here but that ADR is draft. The code's `resolver: MEMORY` is the current correct implementation. The spec-helm.md line should be updated to say `resolver: MEMORY` (current state) and not reference ADR-0054 for this detail until ADR-0054 is accepted. |
| spec-helm.md:43 | "For the full NATS server config see docs/spec-state-schema.md — NATS Server Configuration" | nats-configmap.yaml (cross-ref doc) | IMPLEMENTED | | Documentation cross-reference; code implements the config. |
| spec-helm.md:47 | Postgres NOT part of mclaude-cp; chart accepts `database.host` + `database.passwordSecretRef` | values.yaml:74-90 (database.*), control-plane-deployment.yaml:38-41 (POSTGRES_PASSWORD+DATABASE_URL), init-keys-job.yaml:40-46 | IMPLEMENTED | | No Postgres StatefulSet/Service templates present in chart. |
| spec-helm.md:55 | Control-plane Deployment: configurable replicas, optional dbmate init container when `migrations.enabled` | control-plane-deployment.yaml:11 (replicas), :25-49 (dbmate initContainer conditional on migrations.enabled) | IMPLEMENTED | | |
| spec-helm.md:55 | Mounts `mclaude-system/operator-keys` at `OPERATOR_KEYS_PATH` | control-plane-deployment.yaml:94 (OPERATOR_KEYS_PATH value), :118-121 (volumeMount) | IMPLEMENTED | | |
| spec-helm.md:55 | Mounts `provider-config` ConfigMap when OAuth providers configured | control-plane-deployment.yaml:122-127 (volumeMount conditional), :149-153 (volume conditional) | IMPLEMENTED | | |
| spec-helm.md:55 | POSTGRES_PASSWORD via `valueFrom.secretKeyRef` against `database.passwordSecretRef`, key `postgres-password`; DATABASE_URL composed via env-var interpolation | control-plane-deployment.yaml:87-92 | IMPLEMENTED | | |
| spec-helm.md:55 | dbmate init container uses identical database.* env contract | control-plane-deployment.yaml:36-41 (init container env block) | IMPLEMENTED | | |
| spec-helm.md:55 | S3 env vars when `minio.enabled=true` and `ingress.minioHost` non-empty: `S3_ENDPOINT`, `S3_BUCKET`, `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY` | control-plane-deployment.yaml:102-117 | IMPLEMENTED | | |
| spec-helm.md:55 | `S3_REGION` intentionally omitted | control-plane-deployment.yaml:102-117 (no S3_REGION env var) | IMPLEMENTED | | |
| spec-helm.md:56 | Service `{release}-control-plane`, ClusterIP, port http (8080) | control-plane-service.yaml:1-20 | IMPLEMENTED | | |
| spec-helm.md:57 | ConfigMap `{release}-provider-config`, `providers.json`, created only when `controlPlane.providers` non-empty | provider-config-configmap.yaml:1-12 | IMPLEMENTED | | |
| spec-helm.md:58 | ServiceAccount `{release}-mclaude`, no K8s permissions | serviceaccount.yaml:1-18 (automountServiceAccountToken: false) | IMPLEMENTED | | |
| spec-helm.md:60 | ClusterRole/ClusterRoleBinding removed from mclaude-cp | (absent from templates/) | IMPLEMENTED | | No ClusterRole or ClusterRoleBinding templates exist in mclaude-cp chart. |
| spec-helm.md:64-68 | SPA: Deployment `{release}-spa` (nginx + SPA bundle); Service `{release}-spa` mapping 80→8080 | spa-deployment.yaml:1-52, spa-service.yaml:1-20 | PARTIAL | SPEC→FIX | Spec says "Maps 80 → 8080". Code uses `port: {{ .Values.spa.service.port }}` (values default 80) and `targetPort: http` (containerPort 8080). The mapping is correct per values but the spec assumes fixed ports. The SPA service port is configurable (values-dev overrides to 8080 both ways), so spec claim "Maps 80 → 8080" is an over-specification. Also values-dev.yaml sets `spa.service.port: 8080` — so the actual port in dev is 8080→8080, not 80→8080. `SPEC→FIX`: the spec should say "configured via `spa.service.port`". |
| spec-helm.md:71-79 | MinIO Deployment `{release}-minio`, single replica, command `minio server /data --console-address :9001` | minio-deployment.yaml:5 (name), :11 (replicas: 1), :29 (command) | IMPLEMENTED | | |
| spec-helm.md:71 | MinIO image: `minio/minio:RELEASE.2025-04-22T22-12-26Z` via `mclaude-cp.image` helper (respects `global.imageRegistry`) | minio-deployment.yaml:26 (uses mclaude-cp.image helper with .Values.minio.image), values.yaml:178 (tag: RELEASE.2025-04-22T22-12-26Z) | IMPLEMENTED | | |
| spec-helm.md:75 | PVC `{release}-minio-data` at `/data` when persistence enabled; emptyDir otherwise | minio-deployment.yaml:54-60 (persistence conditional), minio-pvc.yaml:1-19 | IMPLEMENTED | | |
| spec-helm.md:75 | Env vars `MINIO_ROOT_USER`/`MINIO_ROOT_PASSWORD` from Secret `{release}-minio` | minio-deployment.yaml:38-47 | IMPLEMENTED | | |
| spec-helm.md:75 | Pod security context: `runAsNonRoot: true`, `runAsUser: 1000`, `runAsGroup: 1000`, `fsGroup: 1000` via standard helper | minio-deployment.yaml:22 (`include "mclaude-cp.podSecurityContext"`) | IMPLEMENTED | | |
| spec-helm.md:75 | Container security context via standard `mclaude-cp.securityContext` helper | minio-deployment.yaml:28 (`include "mclaude-cp.securityContext"`) | IMPLEMENTED | | |
| spec-helm.md:75 | Resources: `minio.resources` | minio-deployment.yaml:51-52 | IMPLEMENTED | | |
| spec-helm.md:76 | Service `{release}-minio`, ClusterIP, port 9000 (S3 API) and port 9001 (console). Not exposed via Ingress by default. | minio-service.yaml:1-24 | IMPLEMENTED | | |
| spec-helm.md:77 | PVC `{release}-minio-data`, RWO, size `minio.persistence.size`, storageClass `minio.persistence.storageClass`, created only when persistence enabled | minio-pvc.yaml:1-19 | IMPLEMENTED | | |
| spec-helm.md:78 | Secret `{release}-minio`, keys `access-key` and `secret-key`, created only when `minio.existingSecret` unset | minio-secret.yaml:1-14 | IMPLEMENTED | | |
| spec-helm.md:79 | Job `{release}-minio-bucket`, post-install/post-upgrade hook, `restartPolicy: Never`, `activeDeadlineSeconds: {{ minio.bucketJobDeadlineSeconds }}` (default 300), `backoffLimit: 3`, `hook-delete-policy: before-hook-creation,hook-succeeded` | minio-bucket-job.yaml:11-15 | IMPLEMENTED | | |
| spec-helm.md:79 | mc bucket Job: both containers share `emptyDir` named `mc-config` at `/root/.mc` | minio-bucket-job.yaml:43-46 (init), :65-67 (main), :68-70 (volume) | IMPLEMENTED | | |
| spec-helm.md:79 | Init container: `mc alias set local http://{release}-minio:9000 $ACCESS_KEY $SECRET_KEY`, then loops `mc ready local` (max 60s) | minio-bucket-job.yaml:31 (command) | IMPLEMENTED | | |
| spec-helm.md:79 | Main container: `mc mb --ignore-existing local/{minio.bucket}` | minio-bucket-job.yaml:53 | IMPLEMENTED | | |
| spec-helm.md:79 | Security contexts intentionally omitted for bucket Job (mc may run as root) | minio-bucket-job.yaml:1-71 (no podSecurityContext or securityContext helpers in this template) | IMPLEMENTED | | |
| spec-helm.md:87 | Ingress `{release}`: routes `/auth`, `/api`, `/admin`, `/scim` to control-plane; `/` to SPA | ingress.yaml:5 (name), :35-70 (rules) | IMPLEMENTED | | |
| spec-helm.md:88 | Ingress `{release}-nats-ws`: NATS WebSocket on dedicated hostname → hub NATS websocket port. Created when `ingress.natsHost` set. | nats-ws-ingress.yaml:1-49 | IMPLEMENTED | | |
| spec-helm.md:89 | Ingress `{release}-leaf`: exposes leafnodes port 7422 over TLS-terminated proxy when `ingress.leafHost` set | leaf-ingress.yaml:1-48 | IMPLEMENTED | | |
| spec-helm.md:90 | Ingress `{release}-minio`: MinIO S3 at `ingress.minioHost` → `{release}-minio:9000`. Created when `ingress.enabled` AND `ingress.minioHost` non-empty. Inherits `ingress.annotations` (via deepCopy+mergeOverwrite) with `proxy-body-size: "0"` override. ExternalDNS annotations. TLS: appends `minioHost` to each entry's hosts. | minio-ingress.yaml:1-44 | IMPLEMENTED | | deepCopy and mergeOverwrite pattern is exact. |
| spec-helm.md:94-96 | Namespace `mclaude-system` created when `namespace.create` is true | namespace.yaml:1-9 | IMPLEMENTED | | |
| spec-helm.md:107 | `nats.config.maxControlLine` default `"16384"`, supports ~62 concurrent host grants | values.yaml:49 (maxControlLine: "16384"), nats-configmap.yaml:57 (max_control_line: {{ .Values.nats.config.maxControlLine }}) | IMPLEMENTED | | ADR-0087 implemented. |
| spec-helm.md:108 | `database.host` required, chart fails if unset | control-plane-deployment.yaml:90 (`required "database.host is required"`), init-keys-job.yaml:46 | IMPLEMENTED | | |
| spec-helm.md:112 | `database.passwordSecretRef` required | control-plane-deployment.yaml:88 (`required "database.passwordSecretRef is required"`), init-keys-job.yaml:43 | IMPLEMENTED | | |
| spec-helm.md:114 | `controlPlane.externalUrl` required, chart fails if unset | control-plane-deployment.yaml:97 (`required "controlPlane.externalUrl is required"`) | IMPLEMENTED | | |
| spec-helm.md:121 | `controlPlane.service.httpPort` injected as `HTTP_PORT`. Known mismatch: Go code reads `PORT`, not `HTTP_PORT`; both default to 8080 | control-plane-deployment.yaml:67 (HTTP_PORT) | IMPLEMENTED | | Spec explicitly documents the mismatch as known. |
| spec-helm.md:122 | `nats.config.jetstream.encryption.storeKeySecret`: when set, renders `jetstream { ... key: $STORE_KEY }` stanza and mounts Secret. ADR-0091 requirement. | nats-configmap.yaml:29-31, nats-statefulset.yaml:52-58 (STORE_KEY env), :62-64 (operator-keys mount) | IMPLEMENTED | | |
| spec-helm.md:123 | `controlPlane.config.jwtRefreshThresholdSeconds`: injected as `JWT_REFRESH_THRESHOLD_SECONDS`. Not read by Go code — phantom env var. | control-plane-deployment.yaml:72 (JWT_REFRESH_THRESHOLD_SECONDS) | IMPLEMENTED | | Spec documents it as phantom; code still emits it (acceptable per spec). |
| spec-helm.md:124 | `controlPlane.migrations.enabled`: init container inherits `database.*` env contract; no hardcoded Secret name or hostname | control-plane-deployment.yaml:25-49 | IMPLEMENTED | | Exact same secretKeyRef + DSN interpolation pattern. |
| spec-helm.md:346 | `values-dev.yaml` (cp): images built locally (pullPolicy: Never), persistence disabled, no ingress, `minio.enabled: false` | values-dev.yaml:1-42 | IMPLEMENTED | | |
| spec-helm.md:347 | `values-e2e.yaml` (cp): Python stub control-plane and SPA, real NATS, persistence disabled, `minio.enabled: false` | values-e2e.yaml:1-73 | IMPLEMENTED | | |
| spec-helm.md:348 | `values-k3d-ghcr.yaml` (cp): Traefik ingress with TLS, sets `ingress.minioHost`, overrides `minio.resources.requests.memory: 32Mi` (ADR-0068) | values-k3d-ghcr.yaml:49-56 | IMPLEMENTED | | |
| spec-helm.md:349 | `values-airgap.yaml` (cp): all images via `global.imageRegistry`, overrides `minio.image.registry` and `minio.mcImage.registry` | values-airgap.yaml:1-41 | IMPLEMENTED | | |
| spec-helm.md:350 | `values-aks.yaml` (cp): `managed-csi-premium` for NATS and MinIO persistence, larger PVCs, scaled replicas | values-aks.yaml:1-50 | IMPLEMENTED | | |
| adr-0081 | `POSTGRES_PASSWORD` secretKeyRef on init-keys Job must have `optional: true` so pod starts even without the postgres Secret (when bootstrapAdminEmail is not set) | init-keys-job.yaml:40-44 | GAP | CODE→FIX | ADR-0081 (status: implemented) explicitly adds `optional: true`. The current init-keys-job.yaml has no `optional: true` on the secretKeyRef (confirmed: grep returns nothing). The spec-helm.md also does not mention this explicitly, but ADR-0081 is authoritative (status: implemented). |
| adr-0091 chart section | Remove `controlPlane.devOAuthToken` value and env var from mclaude-cp | values.yaml:1-251 (no devOAuthToken key), control-plane-deployment.yaml (no DEV_OAUTH_TOKEN env) | IMPLEMENTED | | devOAuthToken removed. |
| adr-0091 chart section | Add JetStream at-rest encryption: `store_key` stanza in nats-configmap.yaml; key via k8s Secret or Helm value | nats-configmap.yaml:29-31, nats-statefulset.yaml:52-58, values.yaml:55-59 | IMPLEMENTED | | |
| adr-0094 chart section | Remove Postgres StatefulSet/Service templates from mclaude-cp | (confirmed absent in templates/) | IMPLEMENTED | | |
| adr-0094 chart section | Add database.host, database.port, database.user, database.name, database.passwordSecretRef, database.sslmode values | values.yaml:74-90 | IMPLEMENTED | | |
| adr-0094 chart section | init-keys-job uses new database.* env contract | init-keys-job.yaml:40-46 | IMPLEMENTED | | |
| adr-0094 chart section | dbmate init container uses new database.* env contract | control-plane-deployment.yaml:34-41 | IMPLEMENTED | | |
| spec-helm.md:148 | `minio.mcImage.tag` uses `latest` (ADR-0072: pinned tag `RELEASE.2025-04-16T18-25-19Z` did not exist on Docker Hub) | values.yaml:203 (tag: "latest") | IMPLEMENTED | | |
| values-airgap.yaml minio.mcImage.tag | Air-gap overrides mcImage to `RELEASE.2025-04-16T18-25-19Z` | values-airgap.yaml:35 | GAP | SPEC→FIX | ADR-0072 says the pinned tag `RELEASE.2025-04-16T18-25-19Z` was invalid on Docker Hub and the fix is to use `latest`. But `values-airgap.yaml` still overrides to that same invalid tag. In an air-gapped env the mirror may have a different tag, but the tag used here is the one ADR-0072 flagged as non-existent. Either the air-gap mirror must verify this tag exists, or the air-gap values should use a valid pinned tag. Spec does not explicitly address air-gap mc tag override but ADR-0072 is unambiguous about `RELEASE.2025-04-16T18-25-19Z` being invalid. |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| _helpers.tpl:1-125 | INFRA | Standard Helm helper functions (name, fullname, chart, namespace, labels, selectorLabels, serviceAccountName, image resolver, imagePullSecrets, securityContext, podSecurityContext). All referenced by production templates. |
| Chart.yaml:1-17 | INFRA | Standard Helm chart metadata (apiVersion, name, description, version, appVersion, keywords, maintainers, home). |
| values.yaml:1-4 | INFRA | Comment header referencing spec/ADRs. |
| values.yaml:9-11 (global.imagePullSecrets) | INFRA | Standard Helm global pull secrets; used by imagePullSecrets helper. Unspecced explicitly but implied by air-gap ADR-0071/values-airgap.yaml usage. |
| values.yaml:12-13 (nameOverride, fullnameOverride) | INFRA | Standard Helm naming overrides; required by `mclaude-cp.fullname` helper. |
| values.yaml:26-37 (nats.image, nats.resources) | INFRA | Image and resource fields for hub NATS; standard Helm knobs implied by the spec noting resources exist. |
| values.yaml:237-240 (tests.curl) | UNSPEC'd | `tests.curl` image config (registry, repo, tag, pullPolicy) for helm test Pod. Used by `test-smoke.yaml`. The spec does not mention helm test infrastructure or the smoke test pod. This is test infra added beyond spec scope. Acceptable helper; not dead. |
| values.yaml:244-249 (serviceAccount) | INFRA | `serviceAccount.create/annotations/name` knobs. The spec says `ServiceAccount {release}-mclaude` is created. These are standard Helm knobs for controlling creation. |
| values.yaml:134 (controlPlane.existingSecret) | UNSPEC'd | `controlPlane.existingSecret: "mclaude-control-plane"` value is present but the template code does NOT use this field to load a secret (checked: no reference in control-plane-deployment.yaml). The spec-helm.md line 158 mentions `mclaude-control-plane` (or the value of `controlPlane.existingSecret`) — no longer requires an `admin-token` key. This field is a remnant of the previous design; it has no effect in the current templates. It's dead config. |
| control-plane-deployment.yaml:73-74 (HELM_RELEASE_NAME) | UNSPEC'd | `HELM_RELEASE_NAME` env var injected into control-plane. Not mentioned in spec-helm.md knobs or any active ADR. Appears to be needed by Go binary for NATS subject routing. Not dead (binary presumably uses it), but unspecced. |
| control-plane-deployment.yaml:75-77 (NATS_URL) | UNSPEC'd | `NATS_URL=nats://{release}-nats:{clientPort}` env var. Not listed in spec-helm.md configuration table, though implied by the spec's description of control-plane connecting to hub NATS. Necessary infra. |
| control-plane-deployment.yaml:81-85 (NATS_ACCOUNT_SEED) | UNSPEC'd | `NATS_ACCOUNT_SEED` from operator-keys Secret key `accountSeed`. Not explicitly listed in spec-helm.md env contract table, but implied by "the control-plane signs per-host user JWTs using the account seed." Necessary infra. |
| control-plane-deployment.yaml:128-141 (livenessProbe, readinessProbe) | INFRA | Standard k8s health probes. Paths configurable via `controlPlane.probe.*` (documented in values-dev.yaml and values-k3d-ghcr.yaml). Spec doesn't enumerate probes explicitly but they're standard Kubernetes infra. |
| init-keys-job.yaml:58-65 (resources on init-keys container) | INFRA | Hardcoded resource requests/limits (50m/64Mi req, 200m/128Mi limit). Not spec'd; reasonable infra default. |
| init-keys-job.yaml:21 (backoffLimit: 4) | INFRA | backoffLimit for init-keys Job. Spec does not specify this value (only minio-bucket Job's backoffLimit: 3 is spec'd). |
| init-keys-rbac.yaml:1-52 | INFRA | ServiceAccount, Role, RoleBinding for init-keys pre-install hook. Required RBAC so the init-keys Job can create/get the operator-keys Secret. Not spec'd explicitly but logically required by the spec's behavior. |
| NOTES.txt:1-33 | INFRA | Helm post-install notes. Standard Helm output. |
| templates/tests/test-smoke.yaml:1-51 | UNSPEC'd | Helm smoke test pod (GET /version, NATS /healthz). Not mentioned in spec-helm.md. ADR-0089 covers integration tests but that's for Go-level tests, not Helm tests. This is test infrastructure beyond spec scope. Not dead (used by `helm test`); not specced. |
| nats-statefulset.yaml:67-80 (liveness/readiness probes) | INFRA | Standard NATS health probes (`/healthz` and `/healthz?js-enabled=true`). Required for pod lifecycle; unspecced in detail. |
| nats-statefulset.yaml:25 (terminationGracePeriodSeconds: 60) | INFRA | NATS StatefulSet graceful shutdown window. Unspecced; reasonable infra. |
| spa-deployment.yaml:34-49 (liveness/readiness probes) | INFRA | SPA health probes (GET /). Unspecced; standard Kubernetes infra. |
| minio-deployment.yaml (no probes) | INFRA | MinIO has no liveness/readiness probes defined. Consistent with the spec. |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| spec-helm.md:29 / adr-0081 | init-keys Job: POSTGRES_PASSWORD secretKeyRef optional | None (Helm templates have no Go unit tests) | None — ADR-0081 explicitly notes "No integration test — this is a Helm template configuration fix" | UNTESTED | ADR-0081 states no integration test needed; the observable outcome is CI deploy success. However there is a CODE→FIX gap (optional: true missing), so this is doubly a problem. |
| spec-helm.md:29 (init-keys idempotency) | init-keys Job: if Secret exists, exits without regenerating | None | adr-0094 lists integration test case "Bootstrap idempotence on re-upgrade" — check if this test exists | UNTESTED | No Helm-level test for this; Go-level bootstrap tests exist in mclaude-control-plane but not chart-level. |
| spec-helm.md:55 (database.* env contract) | Postgres env contract: POSTGRES_PASSWORD + DATABASE_URL interpolation | None | No chart-level test found | UNTESTED | The env contract correctness depends on the chart's Helm template rendering. No `helm template` test or CI lint for env var correctness. |
| spec-helm.md:71-79 (MinIO resources) | MinIO Deployment, service, PVC, secret, bucket job | None | Helm smoke test (test-smoke.yaml) only checks CP /version and NATS /healthz — MinIO not covered | UNTESTED | MinIO bucket creation (post-install hook) has no test coverage. |
| spec-helm.md:87 (Ingress routing) | Ingress routes `/auth`, `/api`, `/admin`, `/scim` to CP; `/` to SPA | None | None | UNTESTED | Ingress correctness untested; no `helm template` golden-file tests. |
| spec-helm.md:107 / adr-0087 (maxControlLine) | `max_control_line: 16384` in NATS config | None | ADR-0087 states integration test is TestIntegration_Import_HappyPath — exists in mclaude-control-plane tests (verified separately) | E2E_ONLY | No unit-level Helm template test; integration test is in Go test suite. |
| spec-helm.md:122 / adr-0091 (JetStream encryption) | `store_key` stanza when `storeKeySecret` set | None | None | UNTESTED | No test for the conditional JetStream encryption rendering. |
| spec-helm.md:90 (MinIO ingress with proxy-body-size: 0) | MinIO Ingress with unlimited body size via deepCopy+mergeOverwrite | None | None | UNTESTED | The annotation mergeOverwrite logic has no Helm rendering test. |
| adr-0094 integration test cases | Fresh install (postgres + cp), bootstrap idempotence, password rotation, managed-DB shape, wrong install order | None | No chart-level integration tests found in the repository | UNTESTED | ADR-0094 lists 5 integration test cases — none are implemented as automated tests. These are critical auth/bootstrap paths. |

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| .agent/bugs/002-droid-shield-false-positives.md | Droid-Shield False Positives on Non-Secret Patterns | OPEN | Component is "Droid-Shield pre-commit secret scanner" — not charts/mclaude-cp. No relevance to this chart's code. Bug remains open (it's about CI scanner behavior, not chart templates). |

### Summary

- Implemented: 39
- Gap: 3
- Partial: 2
- Infra: 16
- Unspec'd: 6
- Dead: 0
- Tested: 0
- Unit only: 0
- E2E only: 1
- Untested: 8
- Bugs fixed: 0
- Bugs open: 1 (not related to this chart)
