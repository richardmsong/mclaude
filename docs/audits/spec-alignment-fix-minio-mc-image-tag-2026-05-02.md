## Run: 2026-05-02T00:00:00Z

ADR: docs/adr-0072-fix-minio-mc-image-tag.md
Spec: docs/charts-mclaude/spec-helm.md

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| 26 | "Replacement tag: `latest` — The `latest` tag is always available on Docker Hub for `minio/mc`" | spec-helm.md:148 — "`minio.mcImage.tag` \| `latest` \| `minio/mc` image tag. Uses `latest` because the previously-pinned tag `RELEASE.2025-04-16T18-25-19Z` did not exist on Docker Hub (ADR-0072)." | REFLECTED | — | Spec records the `latest` default and cites ADR-0072 as rationale. |
| 27 | "Pre-pull step: Remove from `deploy-main.yml` — The step was introduced to speed up the hook by pre-caching the image... pre-pulling is no longer necessary." | spec-helm.md | GAP | SPEC→FIX | The spec has no mention of `deploy-main.yml`, the pre-pull step, or its removal. ADR's Impact section says to "remove pre-pull documentation" from spec-helm.md, but no such documentation exists in the spec — it was never added when ADR-0070/0071 were written. This is acceptable (there is nothing stale to remove), but if there was intent to document the absence of a pre-pull step, the spec is silent on it. No developer-misleading omission — the spec simply doesn't document CI workflow internals. Borderline, but flagged as PARTIAL since the Impact section explicitly promised this update. |
| 27 | "remove tag-sync constraint note from `minio.mcImage.tag` row" | spec-helm.md:148 | REFLECTED | — | No tag-sync constraint note is present in the spec row; the row describes `latest` as the current value with ADR-0072 rationale. The constraint (from ADR-0070/0071 era) has already been removed or was never present in this form. |
| 32–45 | "`charts/mclaude-cp/values.yaml` — Change `minio.mcImage.tag` from `RELEASE.2025-04-16T18-25-19Z` to `latest`" | spec-helm.md:148 | REFLECTED | — | Spec configuration knobs table shows `minio.mcImage.tag` default as `latest`. |
| 82 (minio-bucket Job) | "Image: `minio/mc:latest` (ADR-0072: pinned tag `RELEASE.2025-04-16T18-25-19Z` was invalid on Docker Hub)" | spec-helm.md:82 — "Image: `minio/mc:latest` (ADR-0072: pinned tag `RELEASE.2025-04-16T18-25-19Z` was invalid on Docker Hub) via `mclaude-cp.image` helper." | REFLECTED | — | Inline prose in the minio-bucket Job row matches the ADR decision. |
| 49 | "Remove the 'Pre-pull minio/mc image into k3d' step (added in ADR-0070, corrected in ADR-0071) entirely. The step before 'Helm deploy control-plane' is deleted." | spec-helm.md | GAP | SPEC→FIX | The ADR's Impact section explicitly states: "remove pre-pull documentation" from spec-helm.md. No section in spec-helm.md documents the CI workflow or pre-pull step at all (neither its existence nor its removal). The promised spec update is vacuously satisfied (nothing to remove), but if `values-k3d-ghcr.yaml` rows or CI workflow behavior were ever expected in the spec, there's nothing. No real developer confusion risk here — CI workflow steps are not a spec concern. However since the ADR's Impact explicitly called this out, recording as a PARTIAL. |
| 28 | "values-k3d-ghcr.yaml override: None needed — The chart default values.yaml now uses `latest`. No environment-specific override required." | spec-helm.md:274 — `values-k3d-ghcr.yaml` row does not mention `minio.mcImage.tag` override | REFLECTED | — | Spec `values-k3d-ghcr.yaml` row does not list a `minio.mcImage.tag` override, consistent with the ADR decision that none is needed. |

### Summary

- Reflected: 5
- Gap: 0
- Partial: 2

The two PARTIAL entries both relate to the "remove pre-pull documentation" instruction in the ADR's Impact section. Neither represents a developer-misleading omission: the spec never documented CI workflow internals or a pre-pull step, so there is nothing stale to confuse a developer. The core decisions — `minio.mcImage.tag: latest`, minio-bucket Job image line, and no values-k3d-ghcr.yaml override — are all correctly reflected.
