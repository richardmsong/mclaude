## Run: 2026-05-04T00:00:00Z

Component: .github/workflows/deploy-main.yml (deploy-cp job + deploy-worker environment declaration)
Spec: docs/adr-0090-bitwarden-ci-secrets.md
Scope: deploy-cp job (full) + deploy-worker job environment declaration only (internal steps deferred per ADR Scope section)

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0090:35 | `deploy-cp` job declares `environment: deploy` | deploy-main.yml:178 | IMPLEMENTED | | `environment: deploy` present on deploy-cp |
| adr-0090:46 | New step "Fetch secrets from Bitwarden" added before "Ensure prerequisite secrets" | deploy-main.yml:180-195 (before :199) | IMPLEMENTED | | Step present and ordered before "Ensure prerequisite secrets" |
| adr-0090:48-49 | Step env: `BW_SESSION: ${{ secrets.BW_SESSION }}` | deploy-main.yml:181-183 | IMPLEMENTED | | Exact match |
| adr-0090:52 | `POSTGRES_PASSWORD=$(bw get password "mclaude postgres password")` | deploy-main.yml:184 | IMPLEMENTED | | Exact match |
| adr-0090:53 | `BOOTSTRAP_ADMIN_PASSWORD=$(bw get password "mclaude bootstrap admin password")` | deploy-main.yml:185 | IMPLEMENTED | | Exact match |
| adr-0090:54 | `DEV_OAUTH_TOKEN=$(bw get password "mclaude dev oauth token")` | deploy-main.yml:186 | IMPLEMENTED | | Exact match |
| adr-0090:55 | `[[ -n "$POSTGRES_PASSWORD" ]] \|\| { echo "ERROR: mclaude postgres password is empty"; exit 1; }` | deploy-main.yml:187 | IMPLEMENTED | | Exact match |
| adr-0090:56 | `[[ -n "$BOOTSTRAP_ADMIN_PASSWORD" ]] \|\| { echo "ERROR: mclaude bootstrap admin password is empty"; exit 1; }` | deploy-main.yml:188 | IMPLEMENTED | | Exact match |
| adr-0090:57 | `[[ -n "$DEV_OAUTH_TOKEN" ]] \|\| { echo "ERROR: mclaude dev oauth token is empty"; exit 1; }` | deploy-main.yml:189 | IMPLEMENTED | | Exact match |
| adr-0090:58 | `echo "::add-mask::$POSTGRES_PASSWORD"` | deploy-main.yml:190 | IMPLEMENTED | | Exact match |
| adr-0090:59 | `echo "::add-mask::$BOOTSTRAP_ADMIN_PASSWORD"` | deploy-main.yml:191 | IMPLEMENTED | | Exact match |
| adr-0090:60 | `echo "::add-mask::$DEV_OAUTH_TOKEN"` | deploy-main.yml:192 | IMPLEMENTED | | Exact match |
| adr-0090:61 | `echo "POSTGRES_PASSWORD=$POSTGRES_PASSWORD" >> "$GITHUB_ENV"` | deploy-main.yml:193 | IMPLEMENTED | | Exact match |
| adr-0090:62 | `echo "BOOTSTRAP_ADMIN_PASSWORD=$BOOTSTRAP_ADMIN_PASSWORD" >> "$GITHUB_ENV"` | deploy-main.yml:194 | IMPLEMENTED | | Exact match |
| adr-0090:63 | `echo "DEV_OAUTH_TOKEN=$DEV_OAUTH_TOKEN" >> "$GITHUB_ENV"` | deploy-main.yml:195 | IMPLEMENTED | | Exact match |
| adr-0090:66 | "Ensure prerequisite secrets" step: hardcoded `dev-postgres-password-k3d` → `$POSTGRES_PASSWORD` | deploy-main.yml:204 | IMPLEMENTED | | Uses `"$POSTGRES_PASSWORD"` not hardcoded string |
| adr-0090:66 | "Ensure prerequisite secrets" step: `${{ secrets.BOOTSTRAP_ADMIN_PASSWORD }}` → `$BOOTSTRAP_ADMIN_PASSWORD` | deploy-main.yml:208 | IMPLEMENTED | | Uses `"$BOOTSTRAP_ADMIN_PASSWORD"` |
| adr-0090:67 | "Helm deploy control-plane" step: `${{ secrets.BOOTSTRAP_ADMIN_PASSWORD }}` → `$BOOTSTRAP_ADMIN_PASSWORD` | deploy-main.yml:229 | IMPLEMENTED | | `--set "controlPlane.bootstrapAdminPassword=${BOOTSTRAP_ADMIN_PASSWORD}"` |
| adr-0090:67 | "Helm deploy control-plane" step: `${{ secrets.DEV_OAUTH_TOKEN }}` → `DEV_OAUTH_TOKEN` already in env | deploy-main.yml:228 | IMPLEMENTED | | `--set controlPlane.devOAuthToken="${DEV_OAUTH_TOKEN}"` uses env var |
| adr-0090:68 | `secrets.BOOTSTRAP_ADMIN_PASSWORD` and `secrets.DEV_OAUTH_TOKEN` references removed from workflow | deploy-main.yml:173-236 (deploy-cp) | PARTIAL | SPEC→FIX | Line 68 says "removed from workflow" (whole file) but the Scope section (line 106) explicitly defers deploy-worker internal steps. `secrets.DEV_OAUTH_TOKEN` remains at deploy-main.yml:257 inside the `if: false` deploy-worker job. The reference is inert while the job is disabled. Line 68 should be tightened to "removed from deploy-cp steps" to avoid the ambiguity. |
| adr-0090:106 | `deploy-worker` job environment: deploy declared as forward-compatibility | deploy-main.yml:243 | IMPLEMENTED | | `environment: deploy` present on deploy-worker |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| deploy-main.yml:257 | INFRA | `DEV_OAUTH_TOKEN: ${{ secrets.DEV_OAUTH_TOKEN }}` in the `if: false` deploy-worker job. Deferred per ADR Scope section (line 106). Inert while the job is disabled; no code runs. |

### Phase 3 — Test Coverage

No integration tests applicable — ADR-0090 line 113 explicitly states: "No integration tests — change is CI/config-only. No runtime behavior affected."

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| adr-0090:35-68 | All deploy-cp Bitwarden fetch behavior | N/A (CI config) | N/A (CI config) | N/A — CI-only change, no tests required per ADR |

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | No bugs found in .agent/bugs/ matching this component | — | |

### Summary

- Implemented: 20
- Gap: 0
- Partial: 1 (SPEC→FIX)
- Infra: 1
- Unspec'd: 0
- Dead: 0
- Tested: N/A (CI-only, no tests required)
- Bugs fixed: 0
- Bugs open: 0
