## Run: 2026-05-02T00:00:00Z

# Spec Alignment Audit: ADR-0068 vs docs/charts-mclaude/spec-helm.md

ADR: `docs/adr-0068-minio-k3d-memory-request.md`
Spec: `docs/charts-mclaude/spec-helm.md`

## Phase 1 — ADR → Spec (forward pass)

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| Decisions table | Override placement: `values-k3d-ghcr.yaml` only; production default (128Mi) is unchanged | spec-helm.md:273 — Values files table row for `values-k3d-ghcr.yaml`: "Overrides `minio.resources.requests.memory: 32Mi` (down from default 128Mi) so MinIO can schedule on the shared k3d node whose memory request budget is consumed by session-agent pods (ADR-0068)." | REFLECTED | — | Spec names the file, the value, and the ADR reference |
| Decisions table | New request value: 32Mi | spec-helm.md:138 — `minio.resources` row default is `requests: {cpu: 100m, memory: 128Mi}, limits: {cpu: 500m, memory: 512Mi}`; spec-helm.md:273 — k3d-ghcr override row documents 32Mi override | REFLECTED | — | Default preserved in config table; override documented in values files table |
| Decisions table | CPU request: no change (50m already) | spec-helm.md:138 — `minio.resources` default shows `cpu: 100m` (not 50m). ADR says "50m already" but spec default says 100m. k3d override row at line 273 only documents memory override, consistent with ADR decision to leave CPU unchanged. | PARTIAL | UNCLEAR | Minor inconsistency: ADR says existing CPU request is 50m; spec default table lists 100m. Neither side is clearly authoritative about the k3d-specific baseline. Does not affect the core decision (no CPU change). |
| Decisions table | Memory limit: no change (512Mi) | spec-helm.md:138 — `minio.resources` default: `limits: {cpu: 500m, memory: 512Mi}` | REFLECTED | — | Limit unchanged and matches ADR |
| Component Changes | `values-k3d-ghcr.yaml` gains `minio.resources.requests.memory: 32Mi`; no other file changes; `values.yaml` default (128Mi) unchanged | spec-helm.md:138 — default 128Mi preserved; spec-helm.md:273 — k3d-ghcr row documents the 32Mi override and that it is k3d-specific | REFLECTED | — | Both the preserved default and the override are documented |

### Summary

- Reflected: 4
- Gap: 0
- Partial: 1

## Result

CLEAN (with one PARTIAL that does not constitute a blocking gap)

The one PARTIAL item: ADR says the current CPU request is "50m already" but the spec's configuration knobs table lists the `minio.resources` default CPU request as `100m`. This is a factual discrepancy in the ADR's rationale text (the Decisions table), not a decision itself — it does not affect what the spec should document. A developer implementing from the spec would correctly leave CPU unchanged. Direction: UNCLEAR (the spec's default table may be correct and the ADR's "50m already" parenthetical may be wrong, or the k3d values already override CPU to 50m via a mechanism not captured in the spec).

No blocking gaps. The spec fully reflects all ADR-0068 decisions.

CLEAN — 4 ADR decisions reflected in spec, 0 gaps (1 non-blocking partial on ADR rationale text vs spec default value)
