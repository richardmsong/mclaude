## Run: 2026-05-02T00:00:00Z

ADR: docs/adr-0070-minio-bucket-job-deadline.md
Specs checked: docs/charts-mclaude/spec-helm.md
Round: 2 (prior gaps: k3d cluster name, mcImage.tag sync constraint, pre-pull error handling — claimed fixed)

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| 25 | "New `minio.bucketJobDeadlineSeconds` value (default 300)" | spec-helm.md:150 — `minio.bucketJobDeadlineSeconds \| 300 \| activeDeadlineSeconds for the minio-bucket Job. Increased from 120s…` | REFLECTED | — | Knob name, default value, and rationale all present. |
| 24 | "Deadline default \| 300s" | spec-helm.md:82 — `activeDeadlineSeconds: {{ minio.bucketJobDeadlineSeconds }} (default 300)` | REFLECTED | — | Default value correctly stated in the Job table row. |
| 41–48 | Template: hardcoded `120` replaced with `{{ .Values.minio.bucketJobDeadlineSeconds }}` | spec-helm.md:82 — `activeDeadlineSeconds: {{ minio.bucketJobDeadlineSeconds }}` | REFLECTED | — | Spec shows the template expression rather than hardcoded 120. |
| 27 | "Pre-pull mechanism \| `docker pull` + `k3d image import` in `deploy-main.yml` before helm upgrade" | spec-helm.md:82 — "deploy-main.yml runs `docker pull <mcImage>` + `k3d image import <mcImage> -c mclaude-dev` before `helm upgrade`" | REFLECTED | — | Both commands and ordering constraint are present. |
| 28 | "k3d cluster name \| `mclaude-dev`" | spec-helm.md:82 — "`k3d image import <mcImage> -c mclaude-dev`" | REFLECTED | — | Round 2: cluster name now present in the minio-bucket Job notes. |
| 29 | "Pre-pull placement \| New step before 'Helm deploy control-plane' in `deploy-main.yml`" | spec-helm.md:82 — "before `helm upgrade`" | REFLECTED | — | Ordering constraint captured. |
| 65 | "When upgrading the minio/mc image tag, update this step to match." | spec-helm.md:148 — "When bumping this tag, also update the matching image reference in the `deploy-main.yml` pre-pull step — the two must stay in sync (ADR-0070)." | REFLECTED | — | Round 2: sync constraint now explicit in the `minio.mcImage.tag` knob description. |
| 74–75 | Error handling: `docker pull` fails → step fails, deploy aborts before helm upgrade, hook never executes | spec-helm.md:82 — "If `docker pull` or `k3d image import` fails the deploy step aborts before helm upgrade runs, so the hook never executes." | REFLECTED | — | Round 2: error handling for pre-pull failure now present. |
| 76 | Error handling: `k3d image import` fails → deploy aborts before helm upgrade | spec-helm.md:82 — "If `docker pull` or `k3d image import` fails the deploy step aborts before helm upgrade runs, so the hook never executes." | REFLECTED | — | Round 2: both commands covered together. |
| 77 | Error handling: hook exceeds 300s → `context deadline exceeded` | spec-helm.md:82 — "If the hook still exceeds `bucketJobDeadlineSeconds`, it fails with `context deadline exceeded` — check k3d node resources and network connectivity." | REFLECTED | — | Round 2: deadline breach failure mode now documented. |

### Summary

- Reflected: 10
- Gap: 0
- Partial: 0
