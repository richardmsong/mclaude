## Run: 2026-05-04T00:00:00Z

**Gaps found: 4**

1. **`BOOTSTRAP_ADMIN_PASSWORD` used in two places in `deploy-cp` — only one is described** — The doc says "Subsequent steps use those values as before," but the existing `deploy-cp` job passes `BOOTSTRAP_ADMIN_PASSWORD` in both the "Ensure prerequisite secrets" step (line 189: `--from-literal=bootstrap-admin-password="${{ secrets.BOOTSTRAP_ADMIN_PASSWORD }}"`) and the "Helm deploy control-plane" step (line 212: `--set "controlPlane.bootstrapAdminPassword=${{ secrets.BOOTSTRAP_ADMIN_PASSWORD }}"`). The design says to remove `secrets.BOOTSTRAP_ADMIN_PASSWORD` references, but does not specify the variable name the fetched value is exported under, nor how it gets from the Bitwarden fetch step into both of these distinct invocations.
   - **Doc**: "Secrets are exported as masked step outputs or env vars." / "Subsequent steps use those values as before."
   - **Code**: `deploy-main.yml` lines 189, 212 — two distinct injection points for the same secret, one as a kubectl literal and one as a helm `--set` argument.

2. **Export mechanism for Bitwarden secrets is underspecified** — The doc says secrets are "exported as masked step outputs or env vars" but does not choose between these two. GitHub Actions step outputs and env vars (`$GITHUB_ENV`) have different syntax, different scoping, and different masking behavior. A developer cannot implement this without deciding which mechanism to use and does not have guidance here.
   - **Doc**: "Secrets are exported as masked step outputs or env vars."
   - **Code**: No existing pattern in `deploy-main.yml` to follow — all current secrets come from `secrets.*` context directly.

3. **`BW_SESSION` expiry is a manual operational burden with no operator guidance on renewal cadence** — The doc acknowledges "BW_SESSION must be regenerated periodically (expires after 15 minutes of inactivity by default) and updated in the GitHub environment secret when it expires," but does not specify: (a) whether the session is kept alive between runs by locking/unlocking, (b) what the operator should do when the session is expired and a deploy is triggered (the deploy just fails with a bw CLI error), or (c) how the operator detects the expiry before a deploy fails. This is a blocking gap because the implementation must decide whether to surface the expiry proactively (e.g., a pre-step that validates the session) or accept silent failures.
   - **Doc**: "bw get fails if BW_SESSION is expired or invalid → step fails with a clear error from the bw CLI; deploy aborts early."
   - **Code**: No existing session-validation step exists. The doc accepts silent deploy failure as the detection mechanism but does not say whether a validation pre-check step should be added.

4. **Bitwarden item name format is not verified to match `bw get password` lookup semantics** — The data model specifies item names like `mclaude postgres password` (with spaces), but the doc does not specify how the `bw get` command identifies items — by name string, by item ID, or by URI. The `bw get password <query>` command performs a fuzzy search by default, meaning a developer cannot write the exact `bw get` invocation without knowing which form of identifier to use (and whether the space-containing names are unambiguous in the vault).
   - **Doc**: "Simpler `bw get password` invocation" and item names `mclaude postgres password`, `mclaude bootstrap admin password`, `mclaude dev oauth token`.
   - **Code**: No existing `bw` usage in the repository to follow as a pattern.

## Run: 2026-05-04T12:00:00Z

**Gaps found: 1**

1. **`bw get password` returning empty string on exit 0 is unhandled** — `bw get password "<item name>"` exits 0 and returns an empty string when the named Bitwarden item exists but has no password field set. This silently writes an empty value into `$GITHUB_ENV`. All downstream steps (`kubectl create secret`, helm `--set`) would then receive empty strings and fail in confusing ways rather than at the secret-fetch step. The error handling section only covers the case where `BW_SESSION` is expired or invalid (non-zero exit from `bw`). A developer implementing the fetch step has no specification for whether to add non-empty guards (e.g., `[[ -n "$POSTGRES_PASSWORD" ]] || exit 1`) for each fetched value.
   - **Doc**: "bw get fails if BW_SESSION is expired or invalid → the 'Fetch secrets from Bitwarden' step fails immediately with a bw CLI error and the deploy aborts. This is the detection mechanism — no separate pre-validation step is added."
   - **Code**: `deploy-main.yml` lines 181–191, 200–216 — both kubectl and helm invocations would silently use empty secret values with no upstream detection.

## Run: 2026-05-04T18:00:00Z

CLEAN — no blocking gaps found.

All previously-identified gaps are resolved in the current document:
- The fetch step YAML snippet is fully specified with exact item names, `$GITHUB_ENV` as the export mechanism, non-empty guards for all three secrets, and `::add-mask::` calls before writing to `$GITHUB_ENV`.
- Both injection points for `BOOTSTRAP_ADMIN_PASSWORD` (kubectl in "Ensure prerequisite secrets" and helm `--set` in "Helm deploy control-plane") are explicitly covered in Component Changes with the exact substitution (`$BOOTSTRAP_ADMIN_PASSWORD`).
- The `DEV_OAUTH_TOKEN` `env:` block removal is covered by the explicit statement "secrets.BOOTSTRAP_ADMIN_PASSWORD and secrets.DEV_OAUTH_TOKEN references removed from workflow."
- The `bw get password` invocation pattern is shown with quoted item names; name-uniqueness is asserted by the operator setup; a non-unique match causes a non-zero bw exit which fails the step.
- Session expiry handling is accepted as fail-fast with documented recovery steps — no validation pre-step is required by design.
