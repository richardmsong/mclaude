## Run: 2026-05-02T00:00:00Z

ADR: docs/adr-0071-fix-minio-prepull-registry-prefix.md
Spec: docs/charts-mclaude/spec-helm.md

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| 25 | docker pull reference format: `minio/mc:RELEASE.2025-04-16T18-25-19Z` (no registry prefix) | spec-helm.md:82 — "runs `docker pull minio/mc:<tag>` (short form — no `docker.io/` prefix; the self-hosted runner's Docker daemon cannot resolve the explicit prefix)" | REFLECTED | — | Spec explicitly documents the short-form requirement and reason |
| 26 | k3d import reference format: Same short form `minio/mc:RELEASE.2025-04-16T18-25-19Z` | spec-helm.md:82 — "+ `k3d image import minio/mc:<tag> -c mclaude-dev`" and "k3d normalises the short name to `docker.io/minio/mc:<tag>` in containerd so the pod finds it via `imagePullPolicy: IfNotPresent`" | REFLECTED | — | Spec captures both the short-form import command and the normalisation behaviour |
| 27 | Env var name unchanged (`MINIO_MC_IMAGE`) | spec-helm.md (no mention of MINIO_MC_IMAGE env var name) | REFLECTED | — | Spec documents the pre-pull step by describing what it does rather than the env var name; the env var is a CI workflow internal, not a spec concept. No gap — the spec correctly describes the observable outcome. |
| 33-40 | Component change: `deploy-main.yml` MINIO_MC_IMAGE value changes from `docker.io/minio/mc:...` to `minio/mc:...` | spec-helm.md:82 — pre-pull documented with short form; line 148 — tag sync note references `deploy-main.yml` | REFLECTED | — | Spec cross-references deploy-main.yml and describes the exact short-form reference |

### Cross-spec consistency check

Key shared concepts from ADR-0071: `minio/mc:<tag>` pre-pull short form, `MINIO_MC_IMAGE` env var, `docker.io/` prefix removal.

Grep of all spec files found mentions of `minio/mc`, `MINIO_MC_IMAGE`, and `docker.io/minio` only in `spec-helm.md`. No other spec references these CI workflow implementation details. No cross-spec inconsistencies detected.

### Summary

- Reflected: 4
- Gap: 0
- Partial: 0
