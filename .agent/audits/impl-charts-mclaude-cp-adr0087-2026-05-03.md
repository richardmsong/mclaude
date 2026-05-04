## Run: 2026-05-03T00:00:00Z

Component: charts/mclaude-cp (ADR-0087 scope only)
Spec: docs/adr-0087-fix-nats-max-control-line.md

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0087:38 | `max_control_line: {{ .Values.nats.config.maxControlLine }}` in nats-configmap.yaml | charts/mclaude-cp/templates/nats-configmap.yaml:54 | IMPLEMENTED | — | Exact directive present on line 54, preceded by comment on line 53 |
| adr-0087:49 | `maxControlLine: "16384"` under `nats.config` in values.yaml | charts/mclaude-cp/values.yaml:49 | IMPLEMENTED | — | Field present with correct value "16384" and explanatory comment on lines 46-48 |

### Summary

- Implemented: 2
- Gap: 0
- Partial: 0
- Infra: 0
- Unspec'd: 0
- Dead: 0
- Bugs fixed: 0
- Bugs open: 0
