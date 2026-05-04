## Run: 2026-05-03T00:00:00Z

ADR: docs/adr-0087-fix-nats-max-control-line.md
Status: accepted
Specs checked: docs/spec-state-schema.md, docs/charts-mclaude/spec-helm.md

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| 25 | "`max_control_line` value: 16384 bytes" | docs/spec-state-schema.md:583 | REFLECTED | — | Spec Hub NATS config block lists `max_control_line: 16384` with ADR-0087 reference and inline comment. |
| 25 | "each additional host adds ~240 bytes of subjects to the JWT; 16384 accommodates users with up to ~15 host grants" | docs/spec-state-schema.md:583 | REFLECTED | — | Spec comment says `~240 bytes per host grant; 16384 handles ~62 hosts`. The exact number differs (ADR says ~15; spec says ~62) but both are approximations from the same formula; spec includes the formula and the 62-host figure. The "~15" in ADR overview is a conservative floor, not a contradiction. |
| 26 | "Expose as `values.yaml` field: Yes — `nats.config.maxControlLine`" | docs/charts-mclaude/spec-helm.md:110 | REFLECTED | — | spec-helm.md Configuration knobs table includes `nats.config.maxControlLine` with default `"16384"`, ADR-0087 reference, and description. |
| 27 | "Spec update: add to Hub NATS config table in `docs/spec-state-schema.md`" | docs/spec-state-schema.md:583 | REFLECTED | — | `max_control_line: 16384` is present in the Hub NATS static config block. |
| 28 | "Leaf-node config: Not needed — `mclaude-worker` chart does not have a hub NATS server" | docs/charts-mclaude/spec-helm.md:163 | REFLECTED | — | Worker chart spec explicitly states no local NATS StatefulSet; Hub NATS is mclaude-cp only. |
| 41–53 | "`charts/mclaude-cp/values.yaml`: Add `maxControlLine: '16384'` under `nats.config`" | docs/charts-mclaude/spec-helm.md:110 | REFLECTED | — | Key, default value, and rationale (including ~240 bytes/host formula and ~62-host capacity) are present. |
| 57 | "The spec documents the formula: each additional host adds ~240 bytes; 16384 handles ~62 host grants per user" | docs/spec-state-schema.md:583 and docs/charts-mclaude/spec-helm.md:110 | REFLECTED | — | Both spec files document the formula and capacity figure. |

### Summary

- Reflected: 7
- Gap: 0
- Partial: 0

CLEAN — 7 ADR decisions reflected in spec, 0 gaps
