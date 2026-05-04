# ADR: Bitwarden CI Secrets

**Status**: accepted
**Status history**:
- 2026-05-04: draft
- 2026-05-04: accepted — no specs touched (CI-only change)

## Overview

All non-GitHub-managed CI secrets (postgres password, bootstrap admin password, dev OAuth token) are moved from GitHub Actions repository secrets into Bitwarden. A single `BW_SESSION` secret lives in a branch-protected GitHub Actions environment and is used at deploy time to pull the secrets via the Bitwarden CLI.

## Motivation

The deploy-main workflow previously had a mix of hardcoded values (`dev-postgres-password-k3d`) and GitHub Actions repository secrets. Repository secrets are accessible to any workflow that runs in the repo, and their blast radius is difficult to scope. Centralizing credentials in Bitwarden means credentials can be rotated in one place, and the only GitHub-managed secret is the Bitwarden session key.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Bitwarden integration | CLI + `BW_SESSION` | Self-hosted runner already has `bw` installed; no extra product or plan needed |
| Session blast radius mitigation | GitHub Actions environment with `main`-branch protection | `BW_SESSION` unlocks the full personal vault; environment protection ensures only main-branch deploys can access it |
| Secret organization in Bitwarden | One item per secret | Simpler `bw get password` invocation; easy to rotate one secret without touching others |
| Which secrets move | All three (postgres, bootstrap admin, OAuth token) | Single source of truth; BW_SESSION is the only GitHub-managed secret needed for deploy |

## User Flow

**One-time setup (operator):**
1. Create (or update) Bitwarden items for each secret.
2. Unlock Bitwarden locally: `bw unlock` → copy the session key.
3. In GitHub repo → Settings → Environments → create environment named `deploy`.
4. Add branch protection rule: allow only `main`.
5. Add environment secret `BW_SESSION` with the session key value.

**Every deploy (automated):**
1. `deploy-cp` job declares `environment: deploy`.
2. GitHub enforces branch protection — only runs on `main`.
3. A "Fetch secrets from Bitwarden" step runs `bw get password "<item name>"` for each secret with `BW_SESSION` in env scope. If `BW_SESSION` is expired or invalid, the step fails immediately with a `bw` CLI error and the deploy aborts — no pre-validation step needed.
4. Each secret value is masked then written to `$GITHUB_ENV` via `::add-mask::` + `echo "VAR=value >> $GITHUB_ENV"`. The env var is then available to all subsequent steps in the job by name.
5. Steps that previously used `${{ secrets.BOOTSTRAP_ADMIN_PASSWORD }}` or `${{ secrets.DEV_OAUTH_TOKEN }}` use `$BOOTSTRAP_ADMIN_PASSWORD` / `$DEV_OAUTH_TOKEN` instead. Both injection points (kubectl secret and helm `--set`) reference the same env var.

## Component Changes

### .github/workflows/deploy-main.yml

- `deploy-cp` job gains `environment: deploy`.
- New step "Fetch secrets from Bitwarden" added before "Ensure prerequisite secrets":
  ```yaml
  - name: Fetch secrets from Bitwarden
    env:
      BW_SESSION: ${{ secrets.BW_SESSION }}
    run: |
      POSTGRES_PASSWORD=$(bw get password "mclaude postgres password")
      BOOTSTRAP_ADMIN_PASSWORD=$(bw get password "mclaude bootstrap admin password")
      DEV_OAUTH_TOKEN=$(bw get password "mclaude dev oauth token")
      [[ -n "$POSTGRES_PASSWORD" ]] || { echo "ERROR: mclaude postgres password is empty"; exit 1; }
      [[ -n "$BOOTSTRAP_ADMIN_PASSWORD" ]] || { echo "ERROR: mclaude bootstrap admin password is empty"; exit 1; }
      [[ -n "$DEV_OAUTH_TOKEN" ]] || { echo "ERROR: mclaude dev oauth token is empty"; exit 1; }
      echo "::add-mask::$POSTGRES_PASSWORD"
      echo "::add-mask::$BOOTSTRAP_ADMIN_PASSWORD"
      echo "::add-mask::$DEV_OAUTH_TOKEN"
      echo "POSTGRES_PASSWORD=$POSTGRES_PASSWORD" >> "$GITHUB_ENV"
      echo "BOOTSTRAP_ADMIN_PASSWORD=$BOOTSTRAP_ADMIN_PASSWORD" >> "$GITHUB_ENV"
      echo "DEV_OAUTH_TOKEN=$DEV_OAUTH_TOKEN" >> "$GITHUB_ENV"
  ```
- `bw get password` is called with the exact item name (e.g. `"mclaude postgres password"`). Item names are unique in the vault; `bw` matches by name.
- "Ensure prerequisite secrets" step: `${{ secrets.BOOTSTRAP_ADMIN_PASSWORD }}` → `$BOOTSTRAP_ADMIN_PASSWORD`; hardcoded `dev-postgres-password-k3d` → `$POSTGRES_PASSWORD`.
- "Helm deploy control-plane" step: `${{ secrets.BOOTSTRAP_ADMIN_PASSWORD }}` → `$BOOTSTRAP_ADMIN_PASSWORD`; `${{ secrets.DEV_OAUTH_TOKEN }}` → already in env as `DEV_OAUTH_TOKEN`.
- `secrets.BOOTSTRAP_ADMIN_PASSWORD` and `secrets.DEV_OAUTH_TOKEN` references removed from workflow.

## Data Model

Three Bitwarden vault items, each using the built-in password field:

| Item name | Secret |
|-----------|--------|
| `mclaude postgres password` | Postgres DB password |
| `mclaude bootstrap admin password` | `BOOTSTRAP_ADMIN_PASSWORD` for init-keys Job |
| `mclaude dev oauth token` | `DEV_OAUTH_TOKEN` for Anthropic OAuth |

## Error Handling

- `bw get` fails if `BW_SESSION` is expired or invalid → the "Fetch secrets from Bitwarden" step fails immediately with a bw CLI error and the deploy aborts.
- `bw get password` exits 0 with an empty string if the named item exists but has no password field set. Each fetched value is validated non-empty immediately after retrieval (`[[ -n "$VAR" ]] || exit 1`) before being written to `$GITHUB_ENV`, so an empty value fails the step with a clear error message rather than silently propagating downstream.
- Recovery: operator runs `bw unlock` locally, copies the new session key, and updates the `BW_SESSION` environment secret in GitHub repo Settings → Environments → deploy. Then re-triggers the workflow.
- `BW_SESSION` expires after 15 minutes of inactivity. Operator must refresh it before each deploy run if the previous session has expired.

## Security

- `BW_SESSION` is a GitHub environment secret scoped to the `deploy` environment, which is protected to run only on `main`.
- All secrets fetched from Bitwarden are masked in GitHub Actions logs via `::add-mask::` before use.
- Blast radius of `BW_SESSION` leak = full personal Bitwarden vault. Mitigated by branch protection, manual-only workflow trigger, and short session expiry.

## Impact

- `docs/charts-mclaude/spec-helm.md`: no change (chart still reads the same k8s secrets; only how CI populates them changes)
- No spec updates needed — this is a CI-only change. ADR alone is the record.

## Scope

**In v1:**
- `BW_SESSION` GitHub environment secret with `deploy` environment locked to `main`
- Bitwarden items for all three secrets
- `deploy-cp` job updated to fetch from Bitwarden

**Deferred:**
- `deploy-worker` job (currently `if: false`) — will get the same treatment when re-enabled
- Automatic `BW_SESSION` rotation

## Open questions

<!-- none -->

## Integration Test Cases

No integration tests — change is CI/config-only. No runtime behavior affected.

## Implementation Plan

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| .github/workflows/deploy-main.yml | ~15 lines changed | 30k | Add environment, fetch step, remove hardcoded values |

**Total estimated tokens:** 30k
**Estimated wall-clock:** 0.5h
