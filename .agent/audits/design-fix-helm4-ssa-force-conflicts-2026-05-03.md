## Run: 2026-05-03T00:00:00Z

ADR: docs/adr-0088-fix-helm4-ssa-force-conflicts.md
Specs checked: docs/charts-mclaude/spec-helm.md

ADR claims: "No spec updates — this is a CI operational fix."

### Phase 0 Notes

The ADR's single component change is `.github/workflows/deploy-main.yml`. The spec-helm.md covers chart resources, values knobs, and operator install commands — not CI workflow flags. The spec does not claim to document how CI runs `helm upgrade`, only what the chart deploys and how an operator installs it.

### Forward Pass: ADR Decisions → Spec

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| line 27 | Add `--force-conflicts` to both `helm upgrade --install` commands | spec-helm.md | REFLECTED | — | The spec does not purport to document CI upgrade flags. Its Deployment sections (lines 20, 170–173) show operator install examples, not CI workflow invocations. A developer implementing from the spec has no reason to expect CI flags there. The ADR's claim of "no spec update needed" is accurate for this decision. |
| line 28 | Apply `--force-conflicts` to both `mclaude-cp` and `mclaude-worker` charts | spec-helm.md | REFLECTED | — | Same reasoning — scope is CI workflow, not chart spec. Both charts are addressed in the workflow file; the spec correctly omits CI-specific flag concerns. |
| line 29 | Keep ADR-0084 uninstall-on-failed cleanup steps as complement to `--force-conflicts` | spec-helm.md | REFLECTED | — | The spec does not document CI recovery procedures. The two-tier strategy (uninstall-on-failed + --force-conflicts for normal upgrades) lives in the CI workflow. The spec is not the right place for this, and its absence is not a gap. |
| line 53 | If `--force-conflicts` fails (future Helm version removes the flag), step exits non-zero; no silent data loss. | spec-helm.md | REFLECTED | — | Pure CI workflow error-exit behavior. Not in scope for the chart spec. |

### Summary

- Reflected: 4
- Gap: 0
- Partial: 0

The ADR's claim that "no spec updates" are needed is verified correct. All decisions are scoped to the CI workflow file, which is not documented in spec-helm.md (nor should it be — the spec covers chart resources and operator install patterns, not CI automation flags).
