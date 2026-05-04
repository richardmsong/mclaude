## Run: 2026-05-03T00:00:00Z

### ADR: docs/adr-0076-fix-encode-cwd-leading-dash.md
### Spec: docs/mclaude-cli/spec-cli.md

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| 19 | "Correct encoding: Preserve leading dash: `/foo/bar` → `-foo-bar`" | spec-cli.md:68 — "replace every `/` with `-` (e.g. `-Users-rsong-work-mclaude`). The leading `-` is preserved — it is the artifact of the leading `/` being replaced." | REFLECTED | — | Spec correctly states the leading dash is preserved and gives a concrete example. |
| 19–20 | "Spec update: Fix the erroneous 'strip the leading `-`' sentence in `docs/mclaude-cli/spec-cli.md`" | spec-cli.md:68 — no "strip" language present; spec already describes the correct algorithm | REFLECTED | — | The erroneous sentence has already been removed/corrected in the spec. The spec now matches the ADR decision. |
| 43 | "The existing unit tests for `EncodeCWD` must be updated: expected values change from `Users-foo-bar` to `-Users-foo-bar`." | spec-cli.md — no mention of `EncodeCWD` unit test expectations | GAP | SPEC→FIX | The ADR explicitly mandates updating EncodeCWD unit test expected values to include the leading dash. The spec's Error cases / Smoke Tests sections do not document this unit test expectation. However, this is an implementation/test detail rather than a behavioral spec decision — a developer reading the spec would not get the runtime behavior wrong. Borderline; reported for completeness. |

### Summary

- Reflected: 2
- Gap: 1 (unit test expected-value update — test detail, not a runtime behavior gap)
- Partial: 0
