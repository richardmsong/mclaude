## Run: 2026-05-02T00:00:00Z

**Gaps found: 6**

1. **MinIO Deployment startup command and credential env vars not specified** — The design specifies the MinIO Deployment template must be created, but nowhere states the container command (`minio server /data --console-address :9001`) or the required MinIO environment variable names (`MINIO_ROOT_USER` and `MINIO_ROOT_PASSWORD`) used to configure the root credentials. The Secret key names are specified as `access-key` / `secret-key`, but the template author cannot know which env var names to set on the MinIO container without this information. This is a blocking gap: the MinIO server will refuse to start without the correct env vars.
   - **Doc**: "templates/minio-deployment.yaml — MinIO Deployment (single replica, configurable resources)" — no command, volume mount path, or credential env var names specified.
   - **Code**: `mclaude-control-plane/s3.go` reads `S3_ACCESS_KEY_ID` / `S3_SECRET_ACCESS_KEY` (confirmed present), but MinIO itself needs `MINIO_ROOT_USER` / `MINIO_ROOT_PASSWORD` to initialize — neither is named in the ADR.

2. **Bucket Job: mc image tag and wait-for-MinIO strategy not specified** — The `minio-bucket-job.yaml` template is described as "a one-shot `mc mb` Job runs after MinIO is ready," but the ADR does not specify: (a) the `minio/mc` image tag to pin, (b) how the Job waits for MinIO to be ready (e.g., an `mc alias set` retry loop, an init container, or a `kubectl wait` step). Without a readiness strategy the Job will race against MinIO startup and fail on first install.
   - **Doc**: "Bucket creation | Kubernetes Job (post-install hook) using `minio/mc` image" — no tag, no readiness strategy.
   - **Code**: Existing `init-keys-job.yaml` uses `helm.sh/hook: pre-install,pre-upgrade` and a `backoffLimit: 0` pattern — does not solve MinIO boot ordering for a post-install hook.

3. **MinIO Ingress TLS configuration not specified** — The design says "The Ingress rule for `minio.mclaude.richardmcsong.com` is wired automatically when `ingress.enabled: true`" but does not specify (a) what TLS `secretName` to reference in the MinIO Ingress (the existing main Ingress in `values-k3d-ghcr.yaml` uses `secretName: mclaude-richardmcsong-tls`), or (b) whether the MinIO Ingress should reuse `ingress.tls` from values or require a separate TLS block. A developer implementing `minio-ingress.yaml` cannot determine which TLS secret to use.
   - **Doc**: Section "Component Changes > charts/mclaude-cp" — ingress template listed but no TLS configuration stated.
   - **Code**: `charts/mclaude-cp/values-k3d-ghcr.yaml:51` — `secretName: mclaude-richardmcsong-tls`; this is already the wildcard cert the ADR claims covers `minio.*`, but the template needs to be told to use it.

4. **S3_REGION env var not included in control-plane Deployment wiring** — The `loadS3Config()` function in `s3.go` reads a fourth env var `S3_REGION` (defaults to `"us-east-1"` if absent). The ADR's list of new env vars wired into the control-plane Deployment includes only `S3_ENDPOINT`, `S3_BUCKET`, `S3_ACCESS_KEY_ID`, and `S3_SECRET_ACCESS_KEY`. A developer following this spec will not add `S3_REGION`, leaving it unset — which silently defaults to `us-east-1`. For MinIO this is fine but only if the presigning actually works with the default region. If a future deployer configures a non-default region and the env var is absent, requests silently break. The spec should either explicitly omit it (document the default) or include it.
   - **Doc**: "The control-plane Deployment template gains new env vars when `minio.enabled: true`: S3_ENDPOINT, S3_BUCKET, S3_ACCESS_KEY_ID, S3_SECRET_ACCESS_KEY" — S3_REGION absent.
   - **Code**: `mclaude-control-plane/s3.go:40` — `region := os.Getenv("S3_REGION")` with silent default.

5. **Spec path in Impact section is wrong** — The ADR states the spec being updated is `docs/mclaude-charts/spec-helm.md`, but the actual file on disk is `docs/charts-mclaude/spec-helm.md`. This causes the dev-harness to write the spec update to the wrong path.
   - **Doc**: "Specs updated in this commit: `docs/mclaude-charts/spec-helm.md`" and implementation table row `docs/mclaude-charts/spec-helm.md`.
   - **Code**: `ls /Users/rsong/work/mclaude/docs/` confirms the directory is `charts-mclaude/`, not `mclaude-charts/`. `docs/mclaude-charts/` does not exist.

6. **MinIO Ingress annotations not specified (Traefik vs nginx)** — The primary Ingress in `values-k3d-ghcr.yaml` uses Traefik-specific annotations (`traefik.ingress.kubernetes.io/router.entrypoints: websecure`, `traefik.ingress.kubernetes.io/router.tls: "true"`). The `minio-ingress.yaml` template must also carry these annotations when the ingress class is Traefik, but the ADR does not say whether to reuse `ingress.annotations` from values or hardcode them. Without this, the MinIO Ingress will be created without TLS routing in the k3d-ghcr deployment and MinIO will be unreachable via HTTPS.
   - **Doc**: "minio-ingress.yaml — Ingress rule for MinIO API at `minio.<ingress.host>` (gated by `ingress.enabled`)" — no annotation inheritance specified.
   - **Code**: `charts/mclaude-cp/values-k3d-ghcr.yaml:46-47` — Traefik annotations on the existing Ingress; `charts/mclaude-cp/templates/ingress.yaml:11-19` — annotations sourced from `ingress.annotations` via `toYaml`. The MinIO Ingress template needs to follow the same pattern, but this is not stated.

---

## Round 1 fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | MinIO container command/env | Added command, mount path /data, MINIO_ROOT_USER/MINIO_ROOT_PASSWORD env vars | factual |
| 2 | mc image tag + readiness | Pinned mc image tag; init-container retry loop before mc mb | factual |
| 3 | Ingress TLS | Specified MinIO Ingress inherits ingress.tls from values | factual |
| 4 | S3_REGION | Documented as intentionally omitted; defaults to us-east-1 which works for MinIO | factual |
| 5 | Wrong spec path | Fixed mclaude-charts → charts-mclaude | factual |
| 6 | Ingress annotations | Specified MinIO Ingress inherits ingress.annotations | factual |

## Run: 2026-05-02T12:00:00Z

**Gaps found: 2**

1. **`values-dev.yaml` and `values-e2e.yaml` are not addressed — MinIO enabled by default in non-functional environments** — The design states `minio.enabled: true` is the chart default and no new overrides are needed in `values-k3d-ghcr.yaml`. However, `values-dev.yaml` (local k3d, no ingress, no `controlPlane.externalUrl`) and `values-e2e.yaml` (Python stub for control-plane, no ingress) will inherit `minio.enabled: true`. In both environments the bucket-creation Job will deploy (and succeed, since `http://{release}-minio:9000` is reachable in-cluster), but no `S3_ENDPOINT` env var will be set on the control-plane because there is no external URL to point at. The Go code in `loadS3Config()` returns nil when `S3_ENDPOINT` is empty, so S3 features are silently disabled in dev/e2e while MinIO still consumes PVC space and CPU. A developer must decide whether to add `minio.enabled: false` to these values files or wire a cluster-internal endpoint for them. The ADR gives no direction on this, and a developer cannot determine the correct behavior without asking.
   - **Doc**: "values-k3d-ghcr.yaml: No new overrides needed — `minio.enabled: true` is the default in `values.yaml`" — no mention of `values-dev.yaml` or `values-e2e.yaml`.
   - **Code**: `charts/mclaude-cp/values-dev.yaml` — `ingress.enabled: false`, no `controlPlane.externalUrl` set, no minio override. `charts/mclaude-cp/values-e2e.yaml` — Python stub control-plane, no ingress. `mclaude-control-plane/s3.go:33-38` — `loadS3Config()` returns nil when `S3_ENDPOINT` is empty.

2. **MinIO Ingress `proxy-body-size` annotation blocks uploads exceeding 8MB** — The design says `minio-ingress.yaml` inherits `ingress.annotations` from values. The default `values.yaml` sets `nginx.ingress.kubernetes.io/proxy-body-size: "8m"`, which would be inherited by the MinIO Ingress when using nginx. ADR-0053 specifies import archives up to 500MB and attachments up to 50MB. Both far exceed the 8MB cap and would be rejected at the Ingress layer before reaching MinIO. The ADR does not specify that this annotation must be overridden or set to a larger value on the MinIO Ingress specifically, leaving a developer with no guidance on how to handle it.
   - **Doc**: "minio-ingress.yaml — Ingress for MinIO API at `minio.{{ ingress.host }}` (gated by `ingress.enabled`). Inherits `ingress.annotations` and `ingress.tls` from values (same pattern as the existing main Ingress template)."
   - **Code**: `charts/mclaude-cp/values.yaml:170` — `nginx.ingress.kubernetes.io/proxy-body-size: "8m"`. `charts/mclaude-cp/values-aks.yaml:48` — overrides to `100m` for AKS (confirming 8m is known-insufficient for large uploads). ADR-0053 constraints: `maxImportBytes = 500 * 1024 * 1024` (`s3.go:57`), `maxAttachmentBytes = 50 * 1024 * 1024` (`s3.go:54`).

---

## Round 2

**Gaps found: 2**

1. values-dev.yaml and values-e2e.yaml not addressed — MinIO would deploy but be non-functional
2. MinIO Ingress inheriting proxy-body-size: "8m" blocks uploads over 8MB (import max 500MB, attachments 50MB)

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | MinIO in dev/e2e | Added minio.enabled: false to values-dev.yaml and values-e2e.yaml sections | factual |
| 2 | proxy-body-size | MinIO Ingress overrides nginx proxy-body-size to "0" (unlimited) in merged annotation map | factual |

## Run: 2026-05-02T14:00:00Z

**Gaps found: 4**

1. **MinIO hostname is two levels deep — not covered by the wildcard TLS cert** — The ADR states in the Decisions table that the external MinIO URL is `https://minio.mclaude.richardmcsong.com` and the wildcard cert "already covers this subdomain." However, the Component Changes section specifies the template formula as `https://minio.{{ .Values.ingress.host }}`. With `values-k3d-ghcr.yaml` setting `ingress.host: "dev.mclaude.richardmcsong.com"`, this template produces `https://minio.dev.mclaude.richardmcsong.com` — two labels deep under `mclaude.richardmcsong.com`. The wildcard cert `*.mclaude.richardmcsong.com` covers only one label deep (e.g. `minio.mclaude.richardmcsong.com`) and does NOT cover `minio.dev.mclaude.richardmcsong.com`. A developer cannot implement the template formula as written without TLS breaking for presigned URLs. The two references in the ADR give different hostnames and neither is fully workable as stated.
   - **Doc**: Decisions table: "External URL | `https://minio.mclaude.richardmcsong.com`"; Component Changes: "S3_ENDPOINT → `https://minio.{{ .Values.ingress.host }}`"
   - **Code**: `docs/spec-tls-certs.md:5,97-99` — cert SAN is `*.mclaude.richardmcsong.com` (single wildcard label). `charts/mclaude-cp/values-k3d-ghcr.yaml:48` — `ingress.host: "dev.mclaude.richardmcsong.com"`. The derived minio hostname `minio.dev.mclaude.richardmcsong.com` has two labels under `mclaude.richardmcsong.com`, outside the wildcard's scope.

2. **`minio.existingSecret` not wired into the control-plane Deployment template** — The design adds `minio.existingSecret` as a values knob: when set, `minio-secret.yaml` is skipped and the caller provides their own Secret. However, the Component Changes section for the CP Deployment template hardcodes the Secret name as `mclaude-minio` in the env var source (`S3_ACCESS_KEY_ID → from Secret mclaude-minio key access-key`). When `existingSecret` is non-empty the chart-generated `mclaude-minio` Secret does not exist, so the Deployment will fail to start with a missing-secret error. The design does not specify that the CP template should use `{{ .Values.minio.existingSecret | default "mclaude-minio" }}` (or equivalent) for the secret reference. A developer following the spec as written will hardcode `mclaude-minio`.
   - **Doc**: "S3_ACCESS_KEY_ID → from Secret `mclaude-minio` key `access-key`"; values: `existingSecret: ""`
   - **Code**: `charts/mclaude-cp/templates/control-plane-deployment.yaml:103-104` shows how the existing `controlPlane.existingSecret` pattern works (`name: {{ .Values.controlPlane.existingSecret }}`). The MinIO equivalent is unspecified.

3. **MinIO Ingress ExternalDNS hostname annotation points at wrong host when `externalDnsTarget` is set** — The design says `minio-ingress.yaml` follows "the same pattern as the existing main Ingress template" for annotations. The existing main Ingress template emits `external-dns.alpha.kubernetes.io/hostname: {{ .Values.ingress.host }}` when `externalDnsTarget` is set. If the minio-ingress follows this pattern verbatim, ExternalDNS would create or update the A record for `ingress.host` (the main platform hostname) rather than for `minio.<ingress.host>`. The correct behavior for the minio-ingress is to set `external-dns.alpha.kubernetes.io/hostname` to the MinIO-specific hostname. The design does not state this deviation from "same pattern."
   - **Doc**: "minio-ingress.yaml — Inherits `ingress.annotations` and `ingress.tls` from values (same pattern as the existing main Ingress template)"
   - **Code**: `charts/mclaude-cp/templates/ingress.yaml:16` — `external-dns.alpha.kubernetes.io/hostname: {{ .Values.ingress.host | quote }}`; `charts/mclaude-cp/templates/nats-ws-ingress.yaml:19` — uses `{{ .Values.ingress.natsHost | quote }}` for its own hostname. The minio-ingress needs the same per-hostname treatment but the design gives no direction.

4. **Security context for MinIO Deployment and bucket-creation Job not specified** — The design does not state what `securityContext` / `podSecurityContext` to apply to the MinIO Deployment or the `minio/mc` bucket-creation Job. The chart's standard helper `mclaude-cp.securityContext` sets `runAsNonRoot: true, runAsUser: 1000`. The MinIO official image (`minio/minio`) does support running as UID 1000 with a properly chowned PVC, but this is not guaranteed without explicitly setting the `fsGroup` so the PVC at `/data` is writable. The `minio/mc` Job image also runs as root by default. A developer choosing between "use the standard `mclaude-cp.securityContext` helper," "use a custom context matching the MinIO uid," or "omit securityContext entirely" has no guidance from the spec and must guess.
   - **Doc**: "templates/minio-deployment.yaml — MinIO Deployment (single replica). Container command: `minio server /data --console-address :9001`. PVC mounted at `/data`." — no securityContext field specified.
   - **Code**: `charts/mclaude-cp/templates/_helpers.tpl:103-113` — standard `mclaude-cp.securityContext` uses `runAsUser: 1000`; `charts/mclaude-cp/templates/postgres-statefulset.yaml:24-27` uses custom `runAsUser: 999`. Neither UID is specified for MinIO.

---

## Round 3

**Gaps found: 4**

1. MinIO hostname minio.{{ ingress.host }} produces two-level subdomain not covered by wildcard cert
2. minio.existingSecret not wired into CP Deployment Secret name
3. ExternalDNS annotation inherits main platform hostname instead of MinIO hostname
4. Security context for MinIO Deployment and bucket Job not specified

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | Hostname depth | Added ingress.minioHost values key (parallel to natsHost); S3_ENDPOINT sourced from minioHost | factual |
| 2 | existingSecret wiring | CP template uses coalesce existingSecret (release-minio) to resolve Secret name | factual |
| 3 | ExternalDNS annotation | MinIO Ingress overrides external-dns hostname annotation to minioHost (nats-ws-ingress pattern) | factual |
| 4 | Security context | MinIO Deployment: podSecurityContext.fsGroup=1000; mc Job: no special securityContext | factual |

## Run: 2026-05-02T16:00:00Z

**Gaps found: 2**

1. **Bucket-job init-container readiness check does not test connectivity** — The design says the init container "retries `mc alias set local http://{{ release }}-minio:9000 $ACCESS_KEY $SECRET_KEY` in a loop until MinIO responds (max 60s)." However, `mc alias set` only writes an alias entry to `~/.mc/config.json` — it does not open a network connection to MinIO and always exits 0 regardless of whether MinIO is reachable. A developer implementing this exactly as written will produce a loop body that exits on the first iteration, leaving no actual readiness gate before `mc mb` runs in the main container. MinIO may not be up yet, `mc mb` fails, the `backoffLimit: 3` retries may or may not converge depending on MinIO startup time, and the developer must stop to ask what the intended readiness mechanism is (e.g., `mc ready local`, `mc admin info local`, or a `curl` health probe against `:9000/minio/health/live`).
   - **Doc**: "The Job's init container retries `mc alias set local http://{{ release }}-minio:9000 $ACCESS_KEY $SECRET_KEY` in a loop until MinIO responds (max 60s)"
   - **Code**: `minio/mc` CLI: `mc alias set` writes config and exits 0; `mc ready <alias>` or `mc admin info <alias>` is required to probe liveness. No existing chart template uses this pattern to compare against.

2. **`minio/mc` image not configurable — blocks air-gapped deployments** — The bucket-creation Job image `minio/mc:RELEASE.2025-04-16T18-25-19Z` is hardcoded in the template description with no corresponding entry in the values block. The existing chart already has `values-airgap.yaml` that mirrors all component images via `global.imageRegistry`, and the test image is configurable via `tests.curl.*`. For air-gapped environments the `minio/mc` image must be pulled from an internal registry, but there is no values key to override it (e.g., `minio.mcImage.registry / .repository / .tag`). A developer implementing `minio-bucket-job.yaml` must decide whether to hardcode the image reference or add a configurable values key, and there is no guidance in the spec.
   - **Doc**: "Helm post-install/post-upgrade hook Job using image `minio/mc:RELEASE.2025-04-16T18-25-19Z`" — no `minio.mcImage` or equivalent values key in the `minio:` values block (lines 52–78)
   - **Code**: `charts/mclaude-cp/values-airgap.yaml` — overrides all component images via registry-prefixed entries; `charts/mclaude-cp/values.yaml:184-190` — `tests.curl.*` image is configurable; no equivalent pattern defined for `minio/mc`.

---

## Round 4

**Gaps found: 2**

1. mc alias set does not test connectivity — not a valid readiness probe
2. minio/mc image hardcoded, blocks air-gapped deployments

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | mc alias set readiness | Init container: alias set once (writes config), then loop on mc ready local until exit 0 (max 60s) | factual |
| 2 | mc image not configurable | Added minio.mcImage values key (registry/repository/tag/pullPolicy) parallel to minio.image | factual |

## Run: 2026-05-02T18:00:00Z

**Gaps found: 5**

1. **Bucket-job credential env vars not specified in the Job spec** — The init container command uses shell variables `$ACCESS_KEY` and `$SECRET_KEY`, and the main container creates the bucket, but the Job spec's `env:` block (mapping Secret keys to those variable names) is never described. The Secret has keys `access-key` and `secret-key`. A developer must invent the env var names and the secretKeyRef mapping without any guidance from the ADR.
   - **Doc**: "The init container runs: `mc alias set local http://{{ release }}-minio:9000 $ACCESS_KEY $SECRET_KEY`" — no `env:` block specified for either init container or main container.
   - **Code**: `charts/mclaude-cp/templates/control-plane-deployment.yaml:93-94` shows the established `valueFrom.secretKeyRef` pattern; the equivalent mapping for the bucket-job is absent from the design doc.

2. **MinIO Ingress gating condition contradicts the nats-ws-ingress pattern** — The ADR says the MinIO Ingress is "gated by `ingress.minioHost` being non-empty." Every other non-main Ingress in the chart (nats-ws-ingress) is gated by `ingress.enabled AND <specific-host-value>`. If a developer follows the ADR literally (only `minioHost`), the MinIO Ingress is created even when `ingress.enabled: false` (e.g., `values-dev.yaml`). If the developer follows the chart's established pattern (and ingress.enabled), the ADR's wording is wrong. The developer must stop and ask which condition is authoritative.
   - **Doc**: "gated by `ingress.minioHost` being non-empty" — no mention of `ingress.enabled`.
   - **Code**: `charts/mclaude-cp/templates/nats-ws-ingress.yaml:1` — `{{- if and .Values.ingress.enabled .Values.ingress.natsHost }}`. `charts/mclaude-cp/values-dev.yaml:20` — `ingress.enabled: false`. The literal ADR condition would create a MinIO Ingress in dev despite `ingress.enabled: false`.

3. **Annotation merge mechanism for `proxy-body-size` override is unspecified** — The ADR states the MinIO Ingress has "two annotation overrides in the merged map" including `nginx.ingress.kubernetes.io/proxy-body-size: "0"`, but does not describe the Helm template mechanism for merging the overrides with `ingress.annotations`. The existing ingress templates use plain `{{ toYaml .Values.ingress.annotations | nindent 4 }}`. Helm provides `merge`, `mergeOverwrite`, and inline-with-condition patterns; each produces different YAML. A developer cannot implement the override without knowing which mechanism to use.
   - **Doc**: "Two annotation overrides in the merged map: (1) `nginx.ingress.kubernetes.io/proxy-body-size: "0"`; (2) `external-dns.alpha.kubernetes.io/hostname: ...`"
   - **Code**: `charts/mclaude-cp/templates/ingress.yaml:11-19` — existing ingresses use `{{- toYaml . | nindent 4 }}` with no merge. No chart template shows an override-merge pattern to follow.

4. **Bucket-job `helm.sh/hook-delete-policy` not specified** — The ADR says the bucket-creation Job is a `post-install/post-upgrade` hook but does not specify `hook-delete-policy`. Without it, a completed Job from a prior install/upgrade persists in the cluster. On the next `helm upgrade`, Helm will attempt to create a new Job with the same name, fail with a conflict, and abort the upgrade. The chart's established pattern (every existing hook Job in the chart) always specifies `before-hook-creation,hook-succeeded`.
   - **Doc**: "`backoffLimit: 3`. No special `securityContext`." — no `hook-delete-policy` mentioned.
   - **Code**: `charts/mclaude-cp/templates/init-keys-job.yaml:19` — `"helm.sh/hook-delete-policy": before-hook-creation,hook-succeeded`. Every hook resource in the chart carries this annotation; the MinIO bucket job must too, and this is an unambiguous convention the ADR fails to specify.

5. **S3 env var wiring condition is internally inconsistent** — The "Component Changes" section specifies env vars are added "when `minio.enabled: true` and `ingress.minioHost` is non-empty," but the "Scope" section says "Wire `S3_*` env vars into control-plane Deployment when `minio.enabled: true`" (omitting the `minioHost` condition). A developer writing the Helm template conditional for the env vars receives two different specifications from the same document and cannot determine which is authoritative without stopping to ask.
   - **Doc**: Component Changes (line 128): "when `minio.enabled: true` and `ingress.minioHost` is non-empty"; Scope section (line 175): "Wire `S3_*` env vars into control-plane Deployment when `minio.enabled: true`".
   - **Code**: `mclaude-control-plane/s3.go:37-38` — `loadS3Config()` returns nil if `S3_ENDPOINT` is empty, so if minioHost is unset and env vars are wired only on `minio.enabled`, CP would have a nil S3 config anyway. The template condition has a real behavioral effect when `minio.enabled=true` but `minioHost=""`.

---

## Round 5

**Gaps found: 5**

1. Bucket-job credential env vars not specified
2. MinIO Ingress gating missing ingress.enabled check
3. Annotation merge mechanism for proxy-body-size override unspecified
4. Bucket-job hook-delete-policy not specified
5. S3 env var wiring condition inconsistent between Component Changes and Scope sections

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | Job env vars | Specified ACCESS_KEY/SECRET_KEY env vars with secretKeyRef in both init and main containers | factual |
| 2 | Ingress gating | Changed to `and .Values.ingress.enabled .Values.ingress.minioHost` (nats-ws-ingress pattern) | factual |
| 3 | Annotation merge | Specified mergeOverwrite($base, $overrides) pattern with explicit template snippet | factual |
| 4 | hook-delete-policy | Added before-hook-creation,hook-succeeded (matches init-keys-job.yaml convention) | factual |
| 5 | Wiring condition | Updated Scope to match Component Changes: minio.enabled AND ingress.minioHost non-empty | factual |

## Run: 2026-05-02T20:00:00Z

**Gaps found: 4**

1. **Bucket-job: minio.enabled gating not specified for minio-deployment, minio-service, minio-secret, and minio-bucket-job** — The ADR specifies `values-dev.yaml` and `values-e2e.yaml` setting `minio.enabled: false` to disable MinIO, but the template descriptions for `minio-deployment.yaml`, `minio-service.yaml`, `minio-secret.yaml`, and `minio-bucket-job.yaml` state no top-level `{{- if .Values.minio.enabled }}` gate. Only `minio-pvc.yaml` (gated by `minio.persistence.enabled`) and `minio-ingress.yaml` (gated by `and .Values.ingress.enabled .Values.ingress.minioHost`) have explicit gating conditions stated. A developer implementing these four templates cannot determine whether to wrap them in `minio.enabled`. If they do not, setting `minio.enabled: false` in dev/e2e has no effect and MinIO resources are created in every environment.
   - **Doc**: "- `templates/minio-deployment.yaml` — MinIO Deployment..." / "- `templates/minio-service.yaml` — ClusterIP Service..." / "- `templates/minio-secret.yaml` — Secret `mclaude-minio`..." / "- `templates/minio-bucket-job.yaml` — Helm post-install/post-upgrade hook..." — none state `{{- if .Values.minio.enabled }}` gating.
   - **Doc**: "charts/mclaude-cp values-dev.yaml: `minio: enabled: false`" and "charts/mclaude-cp values-e2e.yaml: `minio: enabled: false`" — these overrides only have effect if the templates check this flag.
   - **Code**: `charts/mclaude-cp/templates/nats-statefulset.yaml` (for reference) is wrapped in `{{- if .Values.nats.enabled }}`; the equivalent pattern for MinIO templates is not stated in the ADR.

2. **Bucket-job init container mc alias config not accessible to main container** — The ADR specifies the init container runs `mc alias set local http://...-minio:9000 $ACCESS_KEY $SECRET_KEY` (which writes to `~/.mc/config.json` in the init container's ephemeral filesystem), then the main container runs `mc mb --ignore-existing local/{{ .Values.minio.bucket }}`. In Kubernetes, init containers and main containers each have their own filesystem — the `~/.mc/` written by the init container is not present in the main container. The main container's `mc mb local/bucket` will fail with "local: No such alias." The ADR does not specify a shared `emptyDir` volume at the mc config directory, nor re-running `mc alias set` in the main container, nor using the `MC_HOST_local` environment variable pattern (which requires no config file). A developer has no unambiguous implementation path.
   - **Doc**: "Init container script: `mc alias set local http://{{ release }}-minio:9000 $ACCESS_KEY $SECRET_KEY` (writes alias config, exits 0), then loops `until mc ready local; do sleep 1; done` with a 60-iteration hard limit. Main container: `mc mb --ignore-existing local/{{ .Values.minio.bucket }}`."
   - **Code**: Kubernetes pod containers do not share ephemeral filesystems; `~/.mc/` is per-container. No shared volume is specified in the ADR's bucket-job description.

3. **ExternalDNS /target annotation missing from minio-ingress** — The ADR's `minio-ingress.yaml` annotation snippet sets `external-dns.alpha.kubernetes.io/hostname` when `ingress.externalDnsTarget` is non-empty, but does NOT set `external-dns.alpha.kubernetes.io/target`. The existing `nats-ws-ingress.yaml` sets both `/hostname` and `/target` in the same conditional block. Without the `/target` annotation, ExternalDNS will point the MinIO DNS record at the cluster LoadBalancer IP rather than the configured `externalDnsTarget` override. Deployments using ExternalDNS with a target override (e.g., a Tailscale IP) will have a broken MinIO DNS record.
   - **Doc**: "{{- if .Values.ingress.externalDnsTarget }}\n{{- $_ := set $overrides \"external-dns.alpha.kubernetes.io/hostname\" .Values.ingress.minioHost }}\n{{- end }}" — no `/target` annotation.
   - **Code**: `charts/mclaude-cp/templates/nats-ws-ingress.yaml:18-21` — sets both `external-dns.alpha.kubernetes.io/hostname: {{ .Values.ingress.natsHost | quote }}` and `external-dns.alpha.kubernetes.io/target: {{ .Values.ingress.externalDnsTarget | quote }}` in the same block.

4. **Bucket-job restartPolicy and activeDeadlineSeconds not specified** — The ADR specifies `backoffLimit: 3` for the bucket-job but omits `restartPolicy` for the pod spec and `activeDeadlineSeconds` for the Job. Without `restartPolicy: Never`, a developer may choose `OnFailure` (which causes the same container to restart, losing the init container's effect). Without `activeDeadlineSeconds`, a stuck init container (MinIO never becomes ready) blocks the hook indefinitely and the upgrade never completes. The chart's established convention (see `init-keys-job.yaml`) is `restartPolicy: Never` + `activeDeadlineSeconds: 120`.
   - **Doc**: "`backoffLimit: 3`. No special `securityContext`." — no `restartPolicy` or `activeDeadlineSeconds` stated.
   - **Code**: `charts/mclaude-cp/templates/init-keys-job.yaml:21-22,29` — `activeDeadlineSeconds: 120` and `restartPolicy: Never` are the established hook-job pattern.

---

## Round 6

**Gaps found: 4**

1. minio.enabled gating missing from deployment, service, secret, bucket-job templates
2. Init container mc alias config not accessible to main container (separate filesystems)
3. ExternalDNS /target annotation missing from minio-ingress
4. restartPolicy and activeDeadlineSeconds not specified for bucket-job

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | minio.enabled gating | All four templates explicitly gated by minio.enabled; pvc gated by minio.enabled AND persistence.enabled | factual |
| 2 | Shared alias config | Added emptyDir volume mc-config at /root/.mc shared between init and main containers | factual |
| 3 | ExternalDNS /target | Added external-dns.alpha.kubernetes.io/target: externalDnsTarget alongside /hostname | factual |
| 4 | Job pod spec | Added restartPolicy: Never and activeDeadlineSeconds: 120 (init-keys-job.yaml pattern) | factual |

## Run: 2026-05-02T22:00:00Z

**Gaps found: 1**

1. **MinIO Deployment PVC reference (`claimName`) not specified** — The ADR describes `minio-pvc.yaml` creating a PersistentVolumeClaim and `minio-deployment.yaml` mounting it at `/data`. However, neither the PVC template name nor the `claimName` used in the Deployment's `spec.volumes[]` entry is specified. A developer implementing `minio-deployment.yaml` must know the PVC name to reference in `volumes[].persistentVolumeClaim.claimName`, and that name must match the `metadata.name` in `minio-pvc.yaml`. Neither the ADR nor any existing chart convention (Postgres and NATS both use StatefulSet VolumeClaimTemplates, not standalone PVCs referenced by name) provides this. The developer must stop and decide a naming convention (e.g., `{{ include "mclaude-cp.fullname" . }}-minio` or `{{ .Release.Name }}-minio-data`).
   - **Doc**: "templates/minio-pvc.yaml — PersistentVolumeClaim" and "templates/minio-deployment.yaml — PVC mounted at `/data`" — no `claimName` value or PVC naming convention stated.
   - **Code**: `charts/mclaude-cp/templates/postgres-statefulset.yaml:91-101` — uses `volumeClaimTemplates` (StatefulSet, PVC named `data` managed by K8s); `charts/mclaude-cp/templates/nats-statefulset.yaml` — same pattern. No existing chart template uses a standalone Deployment + external PVC, so there is no naming convention to follow.

---

## Round 7

**Gaps found: 1**

1. MinIO Deployment PVC claimName not specified — no naming convention for standalone PVC in this chart

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | PVC claimName | PVC named `{release}-minio-data`; Deployment mounts by claimName when persistence.enabled, emptyDir otherwise | factual |

## Run: 2026-05-02T23:00:00Z

**Gaps found: 1**

1. **`mergeOverwrite` mutates `.Values.ingress.annotations` in-place, poisoning `nats-ws-ingress.yaml`** — The `minio-ingress.yaml` annotation block uses `$base := .Values.ingress.annotations | default dict` followed by `mergeOverwrite $base $overrides`. In Helm/sprig, `mergeOverwrite` mutates the destination map object in-place. Because `$base` holds a reference to `.Values.ingress.annotations` (not a copy), after `minio-ingress.yaml` renders, `.Values.ingress.annotations` permanently contains the MinIO-specific overrides for all remaining templates in the same `helm template` invocation. `nats-ws-ingress.yaml` renders alphabetically after `minio-ingress.yaml` and emits `{{- toYaml . | nindent 4 }}` of `.Values.ingress.annotations`. This produces duplicate annotation keys in the rendered NATS-WS Ingress YAML when `ingress.externalDnsTarget` is set: `external-dns.alpha.kubernetes.io/hostname` appears twice (once with the MinIO hostname from the mutation, once with `natsHost` from the nats-ws-ingress conditional). The NATS WS Ingress also inherits `nginx.ingress.kubernetes.io/proxy-body-size: "0"` from the mutation. All other chart templates that set annotation overrides use plain `toYaml` with conditional appending — no `mergeOverwrite` — to avoid this side-effect. A developer implementing this spec as written produces a template that technically works today (duplicate-key last-write-wins in Go's JSON unmarshaling means ExternalDNS sees the correct `natsHost`), but it is architecturally incorrect and will break if any new template is added after `minio-ingress.yaml` that depends on `.Values.ingress.annotations` being unmodified.
   - **Doc**: "`{{- $base := .Values.ingress.annotations | default dict }}` … `{{- mergeOverwrite $base $overrides | toYaml | nindent 4 }}`" (Component Changes § minio-ingress.yaml annotation snippet)
   - **Code**: `charts/mclaude-cp/templates/nats-ws-ingress.yaml:13-21` — uses `{{- with .Values.ingress.annotations }}{{- toYaml . | nindent 4 }}{{- end }}` directly on `.Values.ingress.annotations`; rendered alphabetically after the proposed `minio-ingress.yaml`. `charts/mclaude-cp/templates/ingress.yaml:10-18`, `leaf-ingress.yaml:4-14` — same pattern, no `mergeOverwrite`. No existing chart template uses `mergeOverwrite` on any `.Values` map.

---

## Round 8

**Gaps found: 1**

1. mergeOverwrite mutates .Values.ingress.annotations in-place, poisoning nats-ws-ingress.yaml and any template rendering after minio-ingress.yaml

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | mergeOverwrite mutation | Changed $base to deepCopy(.Values.ingress.annotations) so only the local copy is mutated | factual |

---

## Round 9 (agent killed, gap identified from partial output)

**Gaps found: 1**

1. MinIO Ingress inherits ingress.tls unchanged — TLS hosts list contains dev.mclaude.richardmcsong.com but not minio.mclaude.richardmcsong.com; Traefik SNI routing fails

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | TLS hosts list | Specified nats-ws-ingress.yaml pattern: range tls entries, reuse secretName, append minioHost to each hosts list | factual |

## Run: 2026-05-02T23:59:00Z

**Gaps found: 1**

1. **MinIO and minio/mc image references do not specify use of the `mclaude-cp.image` helper** — The ADR specifies `minio.image` and `minio.mcImage` as values keys with `registry`, `repository`, `tag`, and `pullPolicy` sub-fields, but does not specify that the templates must resolve these via the `{{ include "mclaude-cp.image" (dict "imageValues" .Values.minio.image "global" .Values.global) }}` helper. Every existing chart template uses this helper for every image reference so that `global.imageRegistry` overrides the registry for air-gapped deployments. The existing `values-airgap.yaml` does not include overrides for `minio.image` or `minio.mcImage`, implying that `global.imageRegistry` is the intended mechanism. A developer who implements the MinIO Deployment or bucket-job without the helper will produce templates that ignore `global.imageRegistry`, breaking air-gapped installs. A developer who adds the helper invocation is inferring behavior not stated in the ADR.
   - **Doc**: "templates/minio-deployment.yaml — MinIO Deployment" and "templates/minio-bucket-job.yaml — using image from `minio.mcImage` values key" — no helper call or `global.imageRegistry` integration mentioned.
   - **Code**: `charts/mclaude-cp/templates/init-keys-job.yaml:35` — `image: {{ include "mclaude-cp.image" (dict "imageValues" .Values.controlPlane.image "global" .Values.global) }}`; `charts/mclaude-cp/templates/postgres-statefulset.yaml:31`, `charts/mclaude-cp/templates/nats-statefulset.yaml:28` — same pattern for every image in the chart. `charts/mclaude-cp/values-airgap.yaml` — overrides only `nats.image`, `postgres.image`, `controlPlane.image`, `spa.image`, and `tests.curl`; no `minio.image` or `minio.mcImage` entry, relying on `global.imageRegistry` to cover them.

---

## Round 10

**Gaps found: 1**

1. minio.image and mcImage not resolved via mclaude-cp.image helper — breaks global.imageRegistry redirect for air-gapped deployments; values-airgap.yaml also needs entries

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | Image helper + airgap | Both templates use mclaude-cp.image helper; added values-airgap.yaml section with minio.image and minio.mcImage overrides | factual |

## Run: 2026-05-02T00:00:00Z (Round 11 — evaluating post-round-10 ADR)

**Gaps found: 5**

1. **`{{ release }}` in init container command is invalid Helm template syntax** — Line 56 of the ADR contains `mc alias set local http://{{ release }}-minio:9000 $ACCESS_KEY $SECRET_KEY`. In Helm templates, the release name is accessed as `.Release.Name`, not `{{ release }}`. The unqualified `{{ release }}` would either produce a template rendering error or an empty string depending on Helm's behavior with undefined identifiers (Helm treats undefined vars as nil, rendering to the empty string). Either outcome produces a broken Service URL and the init container's `mc alias set` would connect to `http://-minio:9000`, which fails silently. The developer would need to discover this breakage at runtime.
   - **Doc**: Line 56: "Init container script: `mc alias set local http://{{ release }}-minio:9000 $ACCESS_KEY $SECRET_KEY`"
   - **Code**: All templates in `charts/mclaude-cp/templates/` use `.Release.Name`. `{{ release }}` with a lowercase `release` and no dot is not valid Helm/Sprig. The PVC name in the same spec correctly uses `{{ printf "%s-minio-data" .Release.Name }}` (line 47, 49), confirming `.Release.Name` is the intended pattern.

2. **`values-aks.yaml` not addressed** — MinIO will deploy on AKS with wrong storage class defaults, and no guidance is given on whether MinIO should be enabled at all for AKS (which typically uses AWS S3 or Azure Blob as a managed backend instead of in-cluster MinIO). The AKS values file explicitly overrides `storageClass: managed-csi-premium` for NATS and Postgres (confirmed at `charts/mclaude-cp/values-aks.yaml`). The chart default `minio.persistence.storageClass: ""` means MinIO's PVC will use the cluster's default StorageClass on AKS, which may not be `managed-csi-premium`. Without an AKS override, a developer deploying to AKS has no specification for what to do: enable MinIO with a different storage class, disable MinIO and point to Azure Blob or AWS S3, or leave the default. Any of these choices alters the deployment behavior significantly.
   - **Doc**: Component Changes lists values-k3d-ghcr.yaml, values-dev.yaml, values-e2e.yaml, values-airgap.yaml. Implementation Plan table: "overrides in values-k3d-ghcr.yaml, values-dev.yaml, values-e2e.yaml, values-airgap.yaml." No mention of values-aks.yaml.
   - **Code**: `/Users/rsong/work/mclaude/charts/mclaude-cp/values-aks.yaml` — sets `storageClass: managed-csi-premium` for NATS (not shown but follows same pattern) and Postgres. No MinIO section. Chart default `minio.enabled: true` and `storageClass: ""` apply.

3. **`imagePullPolicy` not specified for either container in the bucket-job** — The bucket-job description specifies the image via `{{ include "mclaude-cp.image" (dict "imageValues" .Values.minio.mcImage "global" .Values.global) }}`, which returns only the image reference string (confirmed: `_helpers.tpl` lines 73–85). Every other container spec in the chart that uses `mclaude-cp.image` pairs it with an explicit `imagePullPolicy: {{ .Values.<component>.image.pullPolicy }}` line (confirmed: `init-keys-job.yaml:36`, `control-plane-deployment.yaml:52`). The bucket-job description does not specify the expression for `imagePullPolicy` in either the init container or main container. A developer who omits `imagePullPolicy` gets Kubernetes's tag-inference behavior (`Always` for `:latest`, `IfNotPresent` otherwise), which differs from the chart's explicit `IfNotPresent` default. A developer who guesses `{{ .Values.minio.mcImage.pullPolicy }}` is inferring behavior not stated.
   - **Doc**: "using image resolved via `{{ include "mclaude-cp.image" (dict "imageValues" .Values.minio.mcImage "global" .Values.global) }}`" — no `imagePullPolicy` expression stated.
   - **Code**: `charts/mclaude-cp/templates/init-keys-job.yaml:36` — `imagePullPolicy: {{ .Values.controlPlane.image.pullPolicy }}`; this is the established convention but the ADR does not specify the equivalent for the bucket-job.

4. **MinIO Deployment credential Secret name not specified** — The `minio-deployment.yaml` description states "`MINIO_ROOT_USER` (from Secret key `access-key`) and `MINIO_ROOT_PASSWORD` (from Secret key `secret-key`)" but omits the Secret name. The developer must write `secretKeyRef.name` in the Deployment's env var spec. The Decisions table names the Secret `mclaude-minio` (line 25); the CP Deployment template uses `{{ coalesce .Values.minio.existingSecret (printf "%s-minio" .Release.Name) }}` (lines 182–183); the bucket-job uses `<coalesce existingSecret (release-minio)>`. Three different representations appear across the ADR, none of which is directly in the `minio-deployment.yaml` section. A developer implementing `minio-deployment.yaml` must cross-reference these sections to determine the correct `secretKeyRef.name` expression.
   - **Doc**: Line 47: "Credential env vars: `MINIO_ROOT_USER` (from Secret key `access-key`) and `MINIO_ROOT_PASSWORD` (from Secret key `secret-key`)" — no secretKeyRef name specified.
   - **Code**: No existing chart template puts credential env vars in a Deployment without specifying the Secret name. The Decisions table (`mclaude-minio`) and CP template (`coalesce` expression) are inconsistent representations of the same name and are in separate sections.

5. **`minio-service.yaml` Service name not specified** — The MinIO Deployment and bucket-job both need to reach MinIO at a cluster-internal hostname, but `minio-service.yaml` never states what `metadata.name` to use. The bucket-job init container references `http://{{ release }}-minio:9000` (though `{{ release }}` is invalid as noted in gap 1). Every Service in the chart uses `{{ include "mclaude-cp.fullname" . }}-<component>` for its name (confirmed: nats, postgres, control-plane, spa services all use `mclaude-cp.fullname`). The `mclaude-cp.fullname` value can differ from `.Release.Name` when `nameOverride` or `fullnameOverride` is set (per `_helpers.tpl` lines 11–21). If `minio-service.yaml` uses `mclaude-cp.fullname` (chart convention) but the bucket-job command uses `.Release.Name` as the Service hostname (from the coalesce formula in gap 4), they diverge under non-default naming. The spec gives no guidance on which naming formula the Service should use.
   - **Doc**: "templates/minio-service.yaml — ClusterIP Service (port 9000 API, port 9001 console)" — no `metadata.name` specified.
   - **Code**: `charts/mclaude-cp/templates/nats-services.yaml:6` — `name: {{ include "mclaude-cp.fullname" . }}-nats`; `postgres-service.yaml:5` — same pattern. The bucket-job init container references `.Release.Name`-based hostname while Services use `mclaude-cp.fullname`.

---

## Round 11

**Gaps found: 5**

1. `{{ release }}` invalid Helm syntax — should be `.Release.Name`
2. values-aks.yaml not addressed
3. imagePullPolicy missing from bucket-job containers
4. MinIO Deployment credential Secret name not specified
5. minio-service.yaml metadata.name not specified; bucket-job references service by name

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | {{ release }} | Changed to {{ include "mclaude-cp.fullname" . }}-minio (fullname helper) throughout bucket-job | factual |
| 2 | values-aks.yaml | Added section: minio.persistence.storageClass: managed-csi-premium | factual |
| 3 | imagePullPolicy | Added .Values.minio.image.pullPolicy and .Values.minio.mcImage.pullPolicy to respective containers | factual |
| 4 | Deployment Secret name | Added coalesce .Values.minio.existingSecret (printf "%s-minio" .Release.Name) to Deployment env vars | factual |
| 5 | Service name | Specified {{ include "mclaude-cp.fullname" . }}-minio for Service; bucket-job uses same helper | factual |

## Run: 2026-05-02T00:00:00Z (Round 12 — evaluating post-round-11 ADR)

**Gaps found: 2**

1. **Security context for MinIO Deployment is underspecified** — The `minio-deployment.yaml` description specifies `podSecurityContext.fsGroup: 1000` inline but does not state whether to use the `mclaude-cp.podSecurityContext` helper (which also enforces `runAsNonRoot: true`, `runAsUser: 1000`) or write a minimal custom context with only `fsGroup: 1000`. Similarly, the container-level `mclaude-cp.securityContext` helper (`runAsNonRoot: true`, `runAsUser: 1000`, `readOnlyRootFilesystem: false`, `capabilities.drop: [ALL]`) is not mentioned for the MinIO container, even though every other Deployment and StatefulSet in the chart invokes both helpers. A developer cannot determine whether to call `{{- include "mclaude-cp.podSecurityContext" . | nindent 6 }}` (which adds constraints beyond `fsGroup`) or emit only `securityContext: { fsGroup: 1000 }`. The bucket-job pod spec says "No special `securityContext`" but does not clarify whether the standard helper is still applied.
   - **Doc**: Line 47: "`podSecurityContext.fsGroup: 1000` (MinIO image runs as UID 1000)"; Line 56: "`backoffLimit: 3`. No special `securityContext`."
   - **Code**: `charts/mclaude-cp/templates/_helpers.tpl:119-124` — `mclaude-cp.podSecurityContext` sets `runAsNonRoot: true`, `runAsUser: 1000`, `runAsGroup: 1000`, `fsGroup: 1000`. `charts/mclaude-cp/templates/control-plane-deployment.yaml:22` — `{{- include "mclaude-cp.podSecurityContext" . | nindent 6 }}`; same pattern in all existing Deployments. The MinIO spec does not say to use this helper.

2. **`values-airgap.yaml` registry placeholder contradicts the actual file** — The ADR states the MinIO image overrides use `your-registry.example.com` and notes "(Placeholder values — the file already uses `your-registry.example.com` as the mirror convention for other images.)" However, the actual `charts/mclaude-cp/values-airgap.yaml` uses `registry.internal.example.com` as the registry for every image entry (confirmed: all 5 image overrides in the file use `registry.internal.example.com`). A developer following the ADR will write `your-registry.example.com` for the new MinIO entries, producing an inconsistent file that mixes two different placeholder values.
   - **Doc**: Line 156: "(Placeholder values — the file already uses `your-registry.example.com` as the mirror convention for other images.)"
   - **Code**: `charts/mclaude-cp/values-airgap.yaml:5,11,17,23,29,35` — every registry entry is `registry.internal.example.com`; `your-registry.example.com` does not appear anywhere in the file.

---

## Round 12

**Gaps found: 2**

1. Security context underspecified — unclear whether to use mclaude-cp.podSecurityContext helper or custom fsGroup only
2. values-airgap.yaml registry placeholder wrong — file uses registry.internal.example.com, not your-registry.example.com

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | Security context | Deployment: use mclaude-cp.podSecurityContext + mclaude-cp.securityContext helpers (standard pattern). Bucket-job: explicitly omit both helpers. | factual |
| 2 | Airgap placeholder | Changed to registry.internal.example.com to match actual values-airgap.yaml convention | factual |

## Run: 2026-05-02T00:00:00Z (Round 13 — evaluating post-round-12 ADR)

**Gaps found: 1**

1. **`minio-secret.yaml` metadata.name not specified** — The `minio-secret.yaml` description (line 50) states "Secret with keys `access-key` and `secret-key`; populated from `minio.rootUser` / `minio.rootPassword` values" but does not state the Secret's `metadata.name`. The coalesce formula used throughout the doc — `{{ coalesce .Values.minio.existingSecret (printf "%s-minio" .Release.Name) }}` — implies the chart-generated Secret is named `{release}-minio`. But the minio-secret.yaml template description itself never states this name, and the spec uses `.Release.Name` (not `include "mclaude-cp.fullname"`) as the fallback. This is functionally inconsistent with the rest of the chart, which names all resources via the `mclaude-cp.fullname` helper. A developer implementing `minio-secret.yaml` must determine the correct name expression without guidance and ensure it exactly matches every other template that references it via the coalesce formula.
   - **Doc**: Line 50: "Secret with keys `access-key` and `secret-key`; populated from `minio.rootUser` / `minio.rootPassword` values" — no `metadata.name` expression stated.
   - **Code**: `charts/mclaude-cp/templates/nats-services.yaml:6` — `name: {{ include "mclaude-cp.fullname" . }}-nats`; all chart resources use `mclaude-cp.fullname`. Lines 52–53, 192–193 of the ADR use `printf "%s-minio" .Release.Name` as the fallback in `coalesce` — this would produce a different name than `mclaude-cp.fullname` when `fullnameOverride` or `nameOverride` is set, creating a secret-reference mismatch at deploy time.

---

## Round 13

**Gaps found: 1**

1. minio-secret.yaml metadata.name not specified; coalesce fallback uses .Release.Name which diverges from mclaude-cp.fullname under nameOverride/fullnameOverride

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | Secret name | minio-secret.yaml named {{ include "mclaude-cp.fullname" . }}-minio; all coalesce fallbacks updated to use printf "%s-minio" (include "mclaude-cp.fullname" .) | factual |

## Run: 2026-05-02T00:00:00Z (Round 14 — evaluating post-round-13 ADR)

**Gaps found: 1**

1. **`minio-pvc.yaml` missing `accessModes` and `storageClassName` conditional** — The `minio-pvc.yaml` description (line 49) specifies only the PVC name. It does not state `accessModes` (a required Kubernetes field — the API will reject a PVC without it), the `resources.requests.storage` expression (must reference `{{ .Values.minio.persistence.size }}`), or the `storageClassName` conditional (present in both existing PVC templates: `{{- if .Values.<component>.persistence.storageClass }} storageClassName: ... {{- end }}`). Without `accessModes`, the PVC YAML is invalid and Kubernetes will reject it at apply time. The existing pattern (`accessModes: ["ReadWriteOnce"]`, conditional `storageClassName`, `storage: {{ .Values.<component>.persistence.size }}`) is consistent across both NATS and Postgres StatefulSet VolumeClaimTemplates, but neither of those is a standalone PVC — there is no existing standalone PVC template in the chart for a developer to follow directly. A developer cannot write a valid `minio-pvc.yaml` without these fields.
   - **Doc**: Line 49: "`templates/minio-pvc.yaml` — (gated by `minio.enabled AND minio.persistence.enabled`) PersistentVolumeClaim named `{{ printf "%s-minio-data" (include "mclaude-cp.fullname" .) }}`" — no `accessModes`, `storageClassName` conditional, or `resources.requests.storage` expression specified.
   - **Code**: `charts/mclaude-cp/templates/postgres-statefulset.yaml:95-101` — `accessModes: ["ReadWriteOnce"]`, conditional `storageClassName`, `storage: {{ .Values.postgres.persistence.size }}`; same pattern in `nats-statefulset.yaml:93-99`. These are VolumeClaimTemplates inside StatefulSets — no standalone PVC template exists in the chart to derive the pattern from directly.

---

## Round 14

**Gaps found: 1**

1. minio-pvc.yaml missing accessModes (required field), storageClassName conditional, and resources.requests.storage

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | PVC spec | Specified accessModes: [ReadWriteOnce], storage: .Values.minio.persistence.size, storageClassName conditional — following NATS/Postgres VolumeClaimTemplate pattern | factual |

## Run: 2026-05-02T00:00:00Z (Round 15 — evaluating post-round-14 ADR)

**Gaps found: 4**

1. **`minio-ingress.yaml` spec.rules section is entirely absent** — The ADR describes the annotations block (with deepCopy + mergeOverwrite) and the TLS block for `minio-ingress.yaml` in precise detail, but never specifies the `spec.rules` section. A developer must know the host value, path, pathType, backend service name, and backend service port to produce a valid Ingress. The ADR establishes `ingress.minioHost` as the hostname, `{release}-minio` as the service name, and port 9000 as the API port — but none of this is assembled into a rules block in the spec. The ADR says the Ingress follows "the `nats-ws-ingress.yaml` pattern" for TLS only; it does not extend that characterization to the rules section.
   - **Doc**: The `minio-ingress.yaml` entry describes annotations and TLS only. No `spec.rules` stanza (host, paths, pathType, backend service name/port) is mentioned.
   - **Code**: `charts/mclaude-cp/templates/nats-ws-ingress.yaml:38-48` — full rules block with `host`, `path: /`, `pathType: Prefix`, `backend.service.name`, `backend.service.port.number`; this is the stated pattern but the ADR does not instruct the developer to reproduce it for MinIO.

2. **`minio-bucket-job.yaml` `metadata.name` is unspecified** — The ADR fully describes the Job's hook annotations, containers, volumes, backoffLimit, activeDeadlineSeconds, and restartPolicy, but never states the Job's `metadata.name`. The only hook Job in the chart, `init-keys-job.yaml`, uses `{{ include "mclaude-cp.fullname" . }}-init-keys-{{ .Release.Revision }}` with a release-revision suffix. The `before-hook-creation,hook-succeeded` delete policy means a revision suffix may not be strictly required, but the developer has no authoritative name to use and must choose (e.g., `{release}-minio-bucket-init` vs `{release}-minio-init-bucket` vs `{release}-minio-create-bucket`) without any guidance.
   - **Doc**: The `minio-bucket-job.yaml` entry describes all spec fields, labels, and annotations — except `metadata.name`.
   - **Code**: `charts/mclaude-cp/templates/init-keys-job.yaml:11` — `name: {{ include "mclaude-cp.fullname" . }}-init-keys-{{ .Release.Revision }}`; the only naming convention available in the chart, not mentioned in the ADR for the bucket job.

3. **`minio-bucket-job.yaml` container `command` fields are unspecified** — The ADR describes what each container does ("init container script: `mc alias set local...` then loops `until mc ready local; do sleep 1; done` (60-iteration hard limit)", "main container: `mc mb --ignore-existing local/{{ .Values.minio.bucket }}`") but does not specify the `command:` field for either container. The `minio/mc` image entrypoint is the `mc` binary itself — it cannot execute multi-statement shell scripts directly. A developer must wrap the init container logic in `command: ["sh", "-c", "..."]` or `command: ["/bin/sh", "-c", "..."]` and must independently implement the 60-iteration loop (the ADR names the limit but gives no shell construct — a `for i in $(seq 60)` loop, a `timeout 60` wrapper, or a counter-variable `while` loop are all valid and produce different behaviors on timeout).
   - **Doc**: Line 56: "Init container script: `mc alias set local http://{{ include "mclaude-cp.fullname" . }}-minio:9000 $ACCESS_KEY $SECRET_KEY` then loops `until mc ready local; do sleep 1; done` (60-iteration hard limit)."
   - **Code**: No existing chart template wraps a shell script in an init container — there is no pattern in the chart to follow. The `minio/mc` image has no multi-command entrypoint.

4. **`minio-deployment.yaml` container `resources` wiring is unspecified** — The values block defines `minio.resources` with full `requests` and `limits`, but the `minio-deployment.yaml` spec description never states how to wire those values into the container spec. Every other Deployment in the chart includes `resources: {{- toYaml .Values.<component>.resources | nindent 12 }}`, but this line is absent from the MinIO Deployment description. A developer must decide independently whether to wire `minio.resources`, omit resources entirely, or hard-code them.
   - **Doc**: The `minio-deployment.yaml` entry specifies image, command, volumes, security context, and env vars — but not `resources:`.
   - **Code**: `charts/mclaude-cp/templates/control-plane-deployment.yaml:144` — `resources: {{- toYaml .Values.controlPlane.resources | nindent 12 }}`; same pattern in NATS and Postgres templates. This convention is not mentioned in the ADR's MinIO Deployment spec.

---

## Round 15

**Gaps found: 4**

1. minio-ingress.yaml spec.rules section entirely absent
2. minio-bucket-job.yaml metadata.name not specified
3. Container command fields not specified (mc image needs sh -c wrapper for multi-statement scripts)
4. minio-deployment.yaml resources wiring not specified

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | Ingress rules | Added full spec.rules block (host=minioHost, path=/, pathType=Prefix, backend={fullname}-minio:9000) | factual |
| 2 | Job name | Named {{ include "mclaude-cp.fullname" . }}-minio-bucket | factual |
| 3 | Container commands | Both containers: command: ["sh", "-c", "..."]; 60-iteration limit as counter while loop | factual |
| 4 | Resources | Added {{- toYaml .Values.minio.resources | nindent 12 }} to deployment spec | factual |

## Run: 2026-05-02T00:00:00Z (Round 16 — evaluating post-round-15 ADR)

CLEAN — no blocking gaps found.

---

## Round 16

CLEAN — no blocking gaps found.

### Result

**CLEAN** after 16 rounds, 36 total gaps resolved (36 factual fixes, 0 design decisions).
